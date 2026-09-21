package nodes_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sidecarnode"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
	"github.com/kilaslab/kilas-flow/sidecar"
	"github.com/kilaslab/kilas-flow/sidecar/sidecartest"
)

// The proofs here drive the real engine over the real sidecar process with the
// hostile fixture package. They answer the ticket's intactness criterion at the
// level it is written: a community node that crashes, hangs or is killed takes
// its own node run down and nothing else — not the items around it, not the
// nodes after it, and not the host.
//
// They need Node on PATH and skip with a named reason without it
// (KILASFLOW_TEST_REQUIRE_NODE=1 turns that skip into a failure).

const (
	hostileType = "sidecar.kf-fixture-hostile.fixtureHostile"
	hostileName = "Hostile"
)

// sidecarEngine is the composition these tests run: the built-in catalogue and
// executors, plus the community package loaded through the sidecar.
type sidecarEngine struct {
	catalogue *node.Registry
	executors *engine.Registry
	index     *sidecarnode.Index
	spawns    *spawnCounts
}

func newSidecarEngine(t *testing.T, packages ...string) *sidecarEngine {
	t.Helper()
	nodePath := sidecartest.Node(t)
	runnerPath, err := sidecar.ExtractRunner(t.TempDir())
	if err != nil {
		t.Fatalf("ExtractRunner() error = %v", err)
	}
	limits := sidecar.DefaultLimits()
	limits.Timeout = 30 * time.Second
	limits.SpawnTimeout = 30 * time.Second

	spawns := &spawnCounts{}
	spawn := spawns.wrap(sidecar.RunnerSpawn(sidecar.RunnerConfig{
		NodePath: nodePath, RunnerPath: runnerPath,
		PackagesDir: sidecartest.PackagesDir(t), Packages: packages, RuntimeDir: t.TempDir(),
	}))
	pool := sidecar.NewPool(spawn, limits)
	t.Cleanup(pool.Close)

	catalogue := node.NewRegistry()
	if err := nodes.RegisterAll(catalogue); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	index, err := sidecarnode.Load(context.Background(), sidecarnode.LoadDeps{
		Spawn: spawn, Limits: limits, Definitions: catalogue, Credentials: credentials.NewRegistry(),
		SharedSettings: nodes.SidecarSharedSettings(),
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	if err := nodes.RegisterSidecarExecutor(executors, pool, index, catalogue, safehttp.DefaultPolicy(),
		slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("RegisterSidecarExecutor() error = %v", err)
	}
	return &sidecarEngine{catalogue: catalogue, executors: executors, index: index, spawns: spawns}
}

// spawnCounts counts the processes the pool started, per tenant, without
// changing what the spawn does.
type spawnCounts struct {
	mu       sync.Mutex
	byTenant map[string]int
	inner    sidecar.SpawnFunc
}

func (counts *spawnCounts) wrap(inner sidecar.SpawnFunc) sidecar.SpawnFunc {
	counts.inner = inner
	return func(ctx context.Context, tenant, socketPath string) (*sidecar.Child, error) {
		counts.mu.Lock()
		if counts.byTenant == nil {
			counts.byTenant = map[string]int{}
		}
		counts.byTenant[tenant]++
		counts.mu.Unlock()
		return counts.inner(ctx, tenant, socketPath)
	}
}

func (counts *spawnCounts) get(tenant string) int {
	counts.mu.Lock()
	defer counts.mu.Unlock()
	return counts.byTenant[tenant]
}

// run compiles the document against the composed catalogue and runs it.
func (fixture *sidecarEngine) run(t *testing.T, document workflow.Document, tenant string, input map[string]any) (engine.Result, error) {
	t.Helper()
	ir, err := workflow.Compile(document, fixture.catalogue)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	return engine.NewRunner(fixture.executors).Run(context.Background(), ir, engine.Request{
		Input:     workflow.Item{JSON: input},
		Execution: engine.ExecutionContext{ID: "exec-1", Mode: "manual", TenantID: tenant, WorkflowID: document.ID},
	})
}

// hostileDocument is a manual trigger that splits a list into items, the
// community node, and a node after it that records what it received.
func hostileDocument(onError string, downstreamPort string) workflow.Document {
	nodes := []workflow.Node{
		{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
		{ID: "split", Name: "Split", Type: "kilasflow.splitOut", TypeVersion: workflow.V(1),
			Parameters: map[string]any{"fieldToSplitOut": "items"}},
		{ID: "hostile", Name: hostileName, Type: hostileType, TypeVersion: workflow.V(1),
			Parameters: map[string]any{"mode": map[string]any{"mode": "expression", "value": "{{ $json.mode }}"}}},
		{ID: "after", Name: "After", Type: "kilasflow.set", TypeVersion: workflow.V(1),
			Parameters: map[string]any{"assignments": map[string]any{"seen": true}}},
	}
	if onError != "" {
		nodes[2].Settings = map[string]any{"onError": onError}
	}
	connections := []workflow.Connection{
		{ID: "manual-split", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "manual", Port: "main"}, Target: workflow.Endpoint{NodeID: "split", Port: "main"}},
		{ID: "split-hostile", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "split", Port: "main"}, Target: workflow.Endpoint{NodeID: "hostile", Port: "main"}},
		{ID: "hostile-after", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "hostile", Port: downstreamPort}, Target: workflow.Endpoint{NodeID: "after", Port: "main"}},
	}
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_sidecar", Name: "Community node failure",
		Nodes: nodes, Connections: connections, Settings: map[string]any{},
	}
}

func engineNodeRun(t *testing.T, result engine.Result, nodeID string) engine.NodeRun {
	t.Helper()
	for _, run := range result.NodeRuns {
		if run.NodeID == nodeID {
			return run
		}
	}
	t.Fatalf("the execution has no run for %q: %+v", nodeID, result.NodeRuns)
	return engine.NodeRun{}
}

// TestSidecarCrashFailsOnlyThatNodeAndTheRunContinuesWithOnError is the
// criterion: the crashing item fails, the items around it succeed, a new
// process serves the item after the crash, the downstream node still runs, and
// the trace row carries the diagnostic.
func TestSidecarCrashFailsOnlyThatNodeAndTheRunContinuesWithOnError(t *testing.T) {
	fixture := newSidecarEngine(t, "kf-fixture-hostile")
	document := hostileDocument(workflow.OnErrorContinueRegular, "main")
	input := map[string]any{"items": []any{
		map[string]any{"mode": "env"}, map[string]any{"mode": "crash"}, map[string]any{"mode": "env"},
	}}

	result, err := fixture.run(t, document, "tenant-a", input)
	if err != nil {
		t.Fatalf("Run() error = %v, want the failing item tolerated", err)
	}

	hostile := engineNodeRun(t, result, "hostile")
	if hostile.Error == nil {
		t.Fatal("the hostile node's trace row carries no diagnostic")
	}
	if !strings.Contains(hostile.Error.Error(), "exited with status 7") {
		t.Errorf("diagnostic = %v, want the child's exit status", hostile.Error)
	}
	if got, want := hostile.ErrorCode, "node.partial"; got != want {
		t.Errorf("error code = %q, want %q", got, want)
	}
	if got, want := len(hostile.Output), 1; got != want {
		t.Fatalf("output streams = %d, want %d", got, want)
	}
	if got, want := len(hostile.Output[0]), 3; got != want {
		t.Fatalf("items after the failure = %d, want %d: the items around the crash survive", got, want)
	}
	if _, failed := hostile.Output[0][1].JSON[engine.ErrorItemKey]; !failed {
		t.Errorf("item 2 = %#v, want the failure reported on it", hostile.Output[0][1].JSON)
	}
	for _, position := range []int{0, 2} {
		if _, failed := hostile.Output[0][position].JSON[engine.ErrorItemKey]; failed {
			t.Errorf("item %d = %#v, want it succeeded", position+1, hostile.Output[0][position].JSON)
		}
	}
	if got, want := len(engineNodeRun(t, result, "after").Input["main"]), 3; got != want {
		t.Errorf("the downstream node received %d items, want %d", got, want)
	}
	// The crashing item took its process with it, so the item after it cold
	// started a replacement rather than reusing a dead one.
	if got, want := fixture.spawns.get("tenant-a"), 2; got != want {
		t.Errorf("processes started = %d, want %d: the item after the crash needs a new one", got, want)
	}
}

// TestSidecarCrashStopsTheExecutionWithDefaultSettings is the other half: the
// engine's rule for a node with no onError setting is to fail the execution.
func TestSidecarCrashStopsTheExecutionWithDefaultSettings(t *testing.T) {
	fixture := newSidecarEngine(t, "kf-fixture-hostile")
	result, err := fixture.run(t, hostileDocument("", "main"), "tenant-a", map[string]any{"items": []any{
		map[string]any{"mode": "crash"},
	}})
	if err == nil {
		t.Fatal("Run() error = nil, want the crashing node to fail the execution")
	}
	if !strings.Contains(err.Error(), "exited with status 7") {
		t.Errorf("error = %v, want the sidecar's diagnostic", err)
	}
	for _, run := range result.NodeRuns {
		if run.NodeID == "after" {
			t.Errorf("the downstream node ran after a fatal failure: %+v", run)
		}
	}
}

// TestSidecarErrorBranchGetsTheFailedItem is the error-port arithmetic: the
// executor returns one stream fewer than the definition declares, the engine
// owns the error port, and the failed item lands on it with the input attached.
func TestSidecarErrorBranchGetsTheFailedItem(t *testing.T) {
	fixture := newSidecarEngine(t, "kf-fixture-hostile")
	document := hostileDocument(workflow.OnErrorContinueBranch, "error")
	result, err := fixture.run(t, document, "tenant-a", map[string]any{"items": []any{
		map[string]any{"mode": "env"}, map[string]any{"mode": "crash"}, map[string]any{"mode": "env"},
	}})
	if err != nil {
		t.Fatalf("Run() error = %v, want the failure routed to the error branch", err)
	}

	hostile := engineNodeRun(t, result, "hostile")
	if got, want := len(hostile.Output), 2; got != want {
		t.Fatalf("output streams = %d, want %d (main and the error branch)", got, want)
	}
	if got, want := len(hostile.Output[0]), 2; got != want {
		t.Errorf("main output items = %d, want the two that succeeded", got)
	}
	if got, want := len(hostile.Output[1]), 1; got != want {
		t.Fatalf("error output items = %d, want the one that failed", got)
	}
	failed := hostile.Output[1][0].JSON
	if _, present := failed[engine.ErrorItemKey]; !present {
		t.Errorf("error item = %#v, want the failure on it", failed)
	}
	if failed["mode"] != "crash" {
		t.Errorf("error item = %#v, want the input item carried alongside the error", failed)
	}
	after := engineNodeRun(t, result, "after")
	if got, want := len(after.Input["main"]), 1; got != want {
		t.Errorf("the error branch node received %d items, want %d", got, want)
	}
}

// TestOneTenantsCrashDoesNotTouchAnotherTenantsProcess is the tenancy rule
// across a crash: tenant B keeps the process it had, because tenant A's child
// died and not B's.
func TestOneTenantsCrashDoesNotTouchAnotherTenantsProcess(t *testing.T) {
	fixture := newSidecarEngine(t, "kf-fixture-hostile")
	document := hostileDocument(workflow.OnErrorContinueRegular, "main")
	healthy := map[string]any{"items": []any{map[string]any{"mode": "env"}}}

	if _, err := fixture.run(t, document, "tenant-b", healthy); err != nil {
		t.Fatalf("Run(tenant-b) error = %v", err)
	}
	if got, want := fixture.spawns.get("tenant-b"), 1; got != want {
		t.Fatalf("tenant-b processes = %d, want %d", got, want)
	}

	if _, err := fixture.run(t, document, "tenant-a", map[string]any{"items": []any{map[string]any{"mode": "crash"}}}); err != nil {
		t.Fatalf("Run(tenant-a) error = %v, want the crash tolerated", err)
	}
	if got, want := fixture.spawns.get("tenant-a"), 1; got != want {
		t.Errorf("tenant-a processes = %d, want %d", got, want)
	}

	result, err := fixture.run(t, document, "tenant-b", healthy)
	if err != nil {
		t.Fatalf("Run(tenant-b) after tenant-a's crash error = %v", err)
	}
	if got, want := fixture.spawns.get("tenant-b"), 1; got != want {
		t.Errorf("tenant-b processes = %d, want %d: another tenant's crash must not evict its process", got, want)
	}
	if run := engineNodeRun(t, result, "hostile"); run.Error != nil {
		t.Errorf("tenant-b's node failed after tenant-a's crash: %v", run.Error)
	}
}

// TestHungNodeHonoursTheNodeTimeoutAndTheHostSurvives is the wall-clock rule:
// the node's own timeout ends the run with a timeout diagnostic, and the host
// is still serving the next execution.
func TestHungNodeHonoursTheNodeTimeoutAndTheHostSurvives(t *testing.T) {
	fixture := newSidecarEngine(t, "kf-fixture-hostile")
	hung := map[string]any{"items": []any{map[string]any{"mode": "hang"}}}

	// Tolerated first: the item is reported as a failure and the execution
	// carries on, which is what the item around a hang gets.
	tolerated := hostileDocument(workflow.OnErrorContinueRegular, "main")
	withTimeout(t, &tolerated, workflow.OnErrorContinueRegular, 2)
	started := time.Now()
	result, err := fixture.run(t, tolerated, "tenant-hang", hung)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("Run() error = %v, want the hanging item tolerated", err)
	}
	if elapsed > 20*time.Second {
		t.Errorf("the run took %s, want the node's own timeout to end it", elapsed)
	}
	if run := engineNodeRun(t, result, "hostile"); run.Error == nil {
		t.Fatal("the hanging node's trace row carries no diagnostic")
	} else if !strings.Contains(run.Error.Error(), "deadline") {
		t.Errorf("diagnostic = %v, want the deadline the node set", run.Error)
	}

	// Then with the engine's default: the node's timeout fails the execution,
	// and the code says it was the clock rather than the node.
	stopping := hostileDocument("", "main")
	withTimeout(t, &stopping, "", 2)
	_, err = fixture.run(t, stopping, "tenant-hang-stop", hung)
	if err == nil {
		t.Fatal("Run() error = nil, want the hang to fail the execution by default")
	}
	if !strings.Contains(err.Error(), "deadline") {
		t.Errorf("error = %v, want it to name the deadline", err)
	}

	// The host survived both, and its other tenants are unaffected.
	healthy := hostileDocument(workflow.OnErrorContinueRegular, "main")
	next, err := fixture.run(t, healthy, "tenant-after-hang", map[string]any{"items": []any{map[string]any{"mode": "env"}}})
	if err != nil {
		t.Fatalf("Run() after the hang error = %v, want the host intact", err)
	}
	if run := engineNodeRun(t, next, "after"); len(run.Input["main"]) != 1 {
		t.Errorf("the downstream node after the hang received %d items, want 1", len(run.Input["main"]))
	}
}

// withTimeout sets the community node's shared settings for one document.
func withTimeout(t *testing.T, document *workflow.Document, onError string, seconds float64) {
	t.Helper()
	settings := map[string]any{"timeoutSeconds": seconds}
	if onError != "" {
		settings["onError"] = onError
	}
	for index := range document.Nodes {
		if document.Nodes[index].ID == "hostile" {
			document.Nodes[index].Settings = settings
			return
		}
	}
	t.Fatal("the document has no hostile node")
}

// TestEveryLoadedSidecarDefinitionIsBoundAndDispatchable is the binding
// invariant for the catalogue a load produced: every definition is bound to a
// registered executor and addressable in the dispatch index.
func TestEveryLoadedSidecarDefinitionIsBoundAndDispatchable(t *testing.T) {
	fixture := newSidecarEngine(t, "kf-fixture-hostile", "kf-fixture-nodes")
	seen := 0
	for _, definition := range fixture.catalogue.List() {
		if definition.Source != node.SourceSidecar {
			continue
		}
		seen++
		if _, found := fixture.executors.Lookup(definition.ExecutorID); !found {
			t.Errorf("%s names executor %q, which is not registered; it would fail on its first item",
				definition.Type, definition.ExecutorID)
		}
		if _, found := fixture.index.Get(definition.Type, definition.Version); !found {
			t.Errorf("%s is in the catalogue but not in the sidecar dispatch index", definition.Type)
		}
	}
	if seen < 3 {
		t.Fatalf("only %d sidecar definitions were registered, want the fixtures' nodes", seen)
	}
}
