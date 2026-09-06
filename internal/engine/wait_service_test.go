package engine_test

// Durable wait integration proof: suspend releases the worker and the lease,
// the suspended run survives a process restart (a second Service on the same
// database continues it), resume continues from the suspension point with the
// exact upstream data (no completed node executes twice, no value returns as
// "[redacted]"), tokens are single-use, and the sweeper settles what nobody
// answered.
//
// NOTE (shared-server race): the PostgreSQL-gated wake test runs against one
// shared KILASFLOW_TEST_POSTGRES_DSN. A combined run must use `go test -p 1`
// (or give each package its own database), or another package's schema reset
// can vanish the tables mid-test. See .pine/memory/persistence.md.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/ai"
	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/expression"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// waitPassthrough records its calls and passes its input items through
// unchanged. In-memory observation is the point: the durable trace redacts
// by construction, so only an executor's live input proves the checkpoint
// carried the exact upstream data rather than "[redacted]".
type waitPassthrough struct {
	calls  *int
	inputs *[]workflow.NodeInput
}

func (executor *waitPassthrough) Execute(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	*executor.calls++
	*executor.inputs = append(*executor.inputs, input)
	items := input["main"]
	if items == nil {
		items = []workflow.Item{}
	}
	return workflow.NodeOutput{items}, nil
}

// waitSuspender suspends on its first call and passes through after that —
// though in practice the resumed run never calls it again: the runner
// completes the suspending node from the stored resume output.
type waitSuspender struct {
	calls     *int
	requests  *[]engine.Request
	mode      string
	expiresAt time.Time
}

func (executor *waitSuspender) Execute(_ context.Context, _ workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	*executor.calls++
	*executor.requests = append(*executor.requests, request)
	return nil, &engine.SuspendError{Mode: executor.mode, ExpiresAt: executor.expiresAt}
}

func waitTestDefinition(nodeType, executorID string) node.Definition {
	return node.Definition{
		Type: nodeType, Version: workflow.V(1),
		DisplayName: "Test " + nodeType, Category: "Test",
		Group:      []node.NodeGroup{node.GroupTransform},
		Inputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs:    []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		ExecutorID: executorID,
	}
}

// waitTestSetup builds one sqlite install with real nodes plus the fake wait
// family: start and done pass through, mark counts, hold suspends.
func waitTestSetup(t *testing.T) (context.Context, *database.DB, *repository.GORMExecutionStore, *node.Registry, *engine.Registry, repository.TenantScope) {
	t.Helper()
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(ctx, config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "kilasflow.db")}, quiet)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, quiet); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	for _, def := range []node.Definition{
		waitTestDefinition("test.start", "test.passthrough"),
		waitTestDefinition("test.mark", "test.passthrough"),
		waitTestDefinition("test.hold", "test.hold"),
		waitTestDefinition("test.done", "test.passthrough"),
	} {
		if err := catalog.Register(def); err != nil {
			t.Fatalf("Register(%q) error = %v", def.Type, err)
		}
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	return ctx, db, repository.NewExecutionStore(db.DB), catalog, executors, repository.TenantScope{ID: "tenant-wait"}
}

func waitTestService(t *testing.T, store *repository.GORMExecutionStore, catalog *node.Registry, executors *engine.Registry, worker string) *engine.Service {
	t.Helper()
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: store, Catalog: catalog, Runner: engine.NewRunner(executors),
		WorkerID: worker, DefaultTimeout: 30 * time.Second,
		PublicBaseURL: "https://flow.example.com",
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

// waitTestWorkflow wires start → mark → hold → done over main ports.
func waitTestWorkflow() workflow.Document {
	link := func(id, source, target string) workflow.Connection {
		return workflow.Connection{
			ID: id, Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: source, Port: "main"},
			Target: workflow.Endpoint{NodeID: target, Port: "main"},
		}
	}
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_wait", Name: "Durable wait",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "mark", Name: "Mark", Type: "test.mark", TypeVersion: workflow.V(1)},
			{ID: "hold", Name: "Hold", Type: "test.hold", TypeVersion: workflow.V(1)},
			{ID: "done", Name: "Done", Type: "test.done", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			link("start-mark", "start", "mark"),
			link("mark-hold", "mark", "hold"),
			link("hold-done", "hold", "done"),
		},
		Settings: map[string]any{},
	}
}

func waitRegisterFakes(t *testing.T, executors *engine.Registry, hold *waitSuspender, calls *int, inputs *[]workflow.NodeInput) {
	t.Helper()
	passthrough := &waitPassthrough{calls: calls, inputs: inputs}
	for id, executor := range map[string]engine.Executor{"test.passthrough": passthrough, "test.hold": hold} {
		if err := executors.Register(id, executor); err != nil {
			t.Fatalf("Register(%q) error = %v", id, err)
		}
	}
}

func waitNodeOutput(t *testing.T, raw json.RawMessage) []map[string]any {
	t.Helper()
	var output [][]struct {
		JSON map[string]any `json:"json"`
	}
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatalf("decode node output: %v (%s)", err, raw)
	}
	items := make([]map[string]any, 0, len(output))
	for _, port := range output {
		for _, item := range port {
			items = append(items, item.JSON)
		}
	}
	return items
}

func TestSuspendResumeContinuesWithExactUpstreamData(t *testing.T) {
	ctx, db, store, catalog, executors, tenant := waitTestSetup(t)
	_ = db
	hold := &waitSuspender{mode: engine.WaitModeApproval, expiresAt: time.Now().Add(time.Hour)}
	var calls int
	var requests []engine.Request
	hold.calls = &calls
	hold.requests = &requests
	var passCalls int
	var passInputs []workflow.NodeInput
	waitRegisterFakes(t, executors, hold, &passCalls, &passInputs)

	workflowStore := repository.NewWorkflowStore(db.DB)
	saved, err := workflowStore.SaveDraft(ctx, tenant, waitTestWorkflow())
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	// api_token looks sensitive and customer does not: the checkpoint must
	// carry both verbatim, where the redacting write path would not.
	queued, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog,
		json.RawMessage(`{"api_token":"secret-123","customer":"Ada"}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}

	service := waitTestService(t, store, catalog, executors, "worker-1")
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(suspend) = (%v, %v), want (true, nil)", worked, err)
	}
	suspended, err := store.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get(suspended) error = %v", err)
	}
	if suspended.Status != execution.StatusWaiting {
		t.Fatalf("status = %q, want waiting", suspended.Status)
	}
	if suspended.LeaseOwner != "" {
		t.Errorf("lease owner = %q, want the suspend to release the lease", suspended.LeaseOwner)
	}
	if got := len(suspended.NodeRuns); got != 2 {
		t.Fatalf("suspend-segment node runs = %d, want start+mark before the wait", got)
	}
	if calls != 1 {
		t.Fatalf("hold calls = %d, want exactly the suspending one", calls)
	}
	if len(requests) != 1 {
		t.Fatalf("hold requests = %d, want 1", len(requests))
	}
	// The run knew its own resume links before suspending: the workflow can
	// send them while it still runs.
	if !strings.Contains(requests[0].Execution.ResumeURL, "/resume/") {
		t.Errorf("resume URL = %q, want a /resume/ link", requests[0].Execution.ResumeURL)
	}
	if !strings.Contains(requests[0].Execution.ApprovalURL, "/approve/") {
		t.Errorf("approval URL = %q, want an /approve/ link", requests[0].Execution.ApprovalURL)
	}
	// And the expression root agrees: a workflow that cannot compose its own
	// resume URL cannot send anyone a link.
	rendered, err := expression.Evaluate("{{ $execution.resumeUrl }}", expression.Context{
		Execution: expression.ExecutionContext{ID: "exec_1", Mode: "manual", ResumeURL: requests[0].Execution.ResumeURL},
	})
	if err != nil {
		t.Fatalf("Evaluate($execution.resumeUrl) error = %v", err)
	}
	if rendered != requests[0].Execution.ResumeURL {
		t.Errorf("$execution.resumeUrl = %#v, want the composed link", rendered)
	}

	wait, err := store.FindActiveWait(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("FindActiveWait() error = %v", err)
	}
	if wait.ResumeToken == "" {
		t.Fatal("active wait carries no resume token")
	}
	resumeURL, approvalURL, ok := service.WaitingLinks(ctx, tenant, queued.ID)
	if !ok || !strings.HasSuffix(resumeURL, "/resume/"+wait.ResumeToken) {
		t.Errorf("WaitingLinks() = (%q, %q, %v), want links ending in the token", resumeURL, approvalURL, ok)
	}

	_, record, err := service.ResumeApproval(ctx, tenant.ID, wait.ResumeToken,
		engine.ApprovalDecision{Approved: true, DecidedBy: "tester", RespondedAt: time.Now().UTC(), Note: "ok"}, false)
	if err != nil {
		t.Fatalf("ResumeApproval() error = %v", err)
	}
	if record.Status != execution.StatusQueued {
		t.Fatalf("resume status = %q, want queued", record.Status)
	}
	// Single-use: the second call is refused with its own message, and the
	// execution stays exactly where the first call left it.
	if _, _, err := service.ResumeApproval(ctx, tenant.ID, wait.ResumeToken,
		engine.ApprovalDecision{Approved: true}, false); !errors.Is(err, repository.ErrWaitConsumed) {
		t.Fatalf("second ResumeApproval() error = %v, want ErrWaitConsumed", err)
	}

	// Restart: a brand-new Service on the same database continues the run.
	// Nothing about the suspension lived in process memory.
	restarted := waitTestService(t, store, catalog, executors, "worker-2")
	if worked, err := restarted.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(resume) = (%v, %v), want (true, nil)", worked, err)
	}
	finished, err := store.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get(finished) error = %v", err)
	}
	if finished.Status != execution.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded (error %s)", finished.Status, finished.Error)
	}
	if got := len(finished.NodeRuns); got != 4 {
		t.Fatalf("node runs = %d, want start+mark+hold+done", got)
	}
	seen := map[string]int{}
	for index, run := range finished.NodeRuns {
		seen[run.NodeID]++
		if run.Sequence != index+1 {
			t.Errorf("run %d sequence = %d, want contiguous from 1", index, run.Sequence)
		}
	}
	for _, nodeID := range []string{"start", "mark", "hold", "done"} {
		if seen[nodeID] != 1 {
			t.Errorf("node %q appears %d times, want exactly once (no double-execute)", nodeID, seen[nodeID])
		}
	}
	var holdOutput map[string]any
	for _, run := range finished.NodeRuns {
		if run.NodeID != "hold" {
			continue
		}
		items := waitNodeOutput(t, run.Output)
		if len(items) != 1 {
			t.Fatalf("hold output items = %d, want the single decision", len(items))
		}
		holdOutput = items[0]
	}
	if holdOutput["approved"] != true || holdOutput["decidedBy"] != "tester" {
		t.Errorf("hold output = %v, want the recorded decision", holdOutput)
	}
	if _, ok := holdOutput["respondedAt"]; !ok {
		t.Errorf("hold output = %v, want the decision timestamp", holdOutput)
	}
	// The resumed segment ran on the checkpoint's exact data: the live input
	// the final node saw still holds the sensitive value verbatim.
	found := false
	for _, input := range passInputs {
		for _, item := range input["main"] {
			if item.JSON["api_token"] == "secret-123" {
				found = true
			}
			if item.JSON["api_token"] == "[redacted]" {
				t.Error("downstream input carries [redacted] where the checkpoint held data")
			}
		}
	}
	if !found {
		t.Error("no downstream input carried the checkpoint's sensitive value verbatim")
	}
	if passCalls == 0 {
		t.Error("no passthrough node ran after resume")
	}
}

func TestSweeperFailsExpiredApprovalsAndRequeuesExpiredTimers(t *testing.T) {
	ctx, db, store, catalog, executors, tenant := waitTestSetup(t)
	_ = db
	hold := &waitSuspender{mode: engine.WaitModeApproval, expiresAt: time.Now().Add(30 * time.Millisecond)}
	var calls int
	var inputs []workflow.NodeInput
	hold.calls = &calls
	hold.requests = &[]engine.Request{}
	waitRegisterFakes(t, executors, hold, &calls, &inputs)

	workflowStore := repository.NewWorkflowStore(db.DB)
	saved, err := workflowStore.SaveDraft(ctx, tenant, waitTestWorkflow())
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	queued, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, json.RawMessage(`{"customer":"Ada"}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	service := waitTestService(t, store, catalog, executors, "worker-1")
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(suspend) = (%v, %v), want (true, nil)", worked, err)
	}
	// Past the 30 ms deadline, inside the sweeper's reach.
	time.Sleep(100 * time.Millisecond)
	settled, err := service.SweepWaits(ctx)
	if err != nil {
		t.Fatalf("SweepWaits() error = %v", err)
	}
	if settled != 1 {
		t.Fatalf("settled = %d, want the one expired approval", settled)
	}
	failed, err := store.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get(expired) error = %v", err)
	}
	if failed.Status != execution.StatusFailed {
		t.Fatalf("status = %q, want failed", failed.Status)
	}
	if !strings.Contains(string(failed.Error), repository.WaitExpiredCode) {
		t.Errorf("error = %s, want the named %q failure", failed.Error, repository.WaitExpiredCode)
	}
	if failed.FinishedAt == nil {
		t.Error("expired execution has no finish timestamp")
	}

	// Timer waits resolve the other way: the deadline IS the resume.
	hold.mode = engine.WaitModeInterval
	hold.expiresAt = time.Now().Add(30 * time.Millisecond)
	queuedTimer, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, json.RawMessage(`{"customer":"Bo"}`))
	if err != nil {
		t.Fatalf("QueueManualLatest(timer) error = %v", err)
	}
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(timer suspend) = (%v, %v), want (true, nil)", worked, err)
	}
	time.Sleep(100 * time.Millisecond)
	settled, err = service.SweepWaits(ctx)
	if err != nil {
		t.Fatalf("SweepWaits(timer) error = %v", err)
	}
	if settled != 1 {
		t.Fatalf("timer settled = %d, want 1", settled)
	}
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(timer resume) = (%v, %v), want (true, nil)", worked, err)
	}
	resumed, err := store.Get(ctx, tenant, queuedTimer.ID)
	if err != nil {
		t.Fatalf("Get(timer) error = %v", err)
	}
	if resumed.Status != execution.StatusSucceeded {
		t.Fatalf("timer status = %q, want succeeded (error %s)", resumed.Status, resumed.Error)
	}

	// An absolute deadline resolves the same way: until is a timer with a
	// clock time instead of a duration.
	hold.mode = engine.WaitModeUntil
	hold.expiresAt = time.Now().Add(30 * time.Millisecond)
	queuedUntil, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, json.RawMessage(`{"customer":"Cy"}`))
	if err != nil {
		t.Fatalf("QueueManualLatest(until) error = %v", err)
	}
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(until suspend) = (%v, %v), want (true, nil)", worked, err)
	}
	time.Sleep(100 * time.Millisecond)
	settled, err = service.SweepWaits(ctx)
	if err != nil {
		t.Fatalf("SweepWaits(until) error = %v", err)
	}
	if settled != 1 {
		t.Fatalf("until settled = %d, want 1", settled)
	}
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(until resume) = (%v, %v), want (true, nil)", worked, err)
	}
	resumedUntil, err := store.Get(ctx, tenant, queuedUntil.ID)
	if err != nil {
		t.Fatalf("Get(until) error = %v", err)
	}
	if resumedUntil.Status != execution.StatusSucceeded {
		t.Fatalf("until status = %q, want succeeded (error %s)", resumedUntil.Status, resumedUntil.Error)
	}
}

func TestApprovalResumeRefusedToEmbedSessionsStaysResumable(t *testing.T) {
	ctx, db, store, catalog, executors, tenant := waitTestSetup(t)
	_ = db
	hold := &waitSuspender{mode: engine.WaitModeApproval, expiresAt: time.Now().Add(time.Hour)}
	var calls int
	var inputs []workflow.NodeInput
	hold.calls = &calls
	hold.requests = &[]engine.Request{}
	waitRegisterFakes(t, executors, hold, &calls, &inputs)

	workflowStore := repository.NewWorkflowStore(db.DB)
	saved, err := workflowStore.SaveDraft(ctx, tenant, waitTestWorkflow())
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	queued, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	service := waitTestService(t, store, catalog, executors, "worker-1")
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(suspend) = (%v, %v), want (true, nil)", worked, err)
	}
	wait, err := store.FindActiveWait(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("FindActiveWait() error = %v", err)
	}
	// Denied before consuming: the error is the embed one, not the consumed
	// one, and the token still resumes afterwards.
	if _, _, err := service.ResumeApproval(ctx, tenant.ID, wait.ResumeToken,
		engine.ApprovalDecision{Approved: true}, true); !errors.Is(err, engine.ErrWaitEmbedDenied) {
		t.Fatalf("embed ResumeApproval() error = %v, want ErrWaitEmbedDenied", err)
	}
	if _, _, err := service.ResumeApproval(ctx, tenant.ID, wait.ResumeToken,
		engine.ApprovalDecision{Approved: true, DecidedBy: "owner"}, false); err != nil {
		t.Fatalf("legit ResumeApproval() after denial error = %v", err)
	}
}

func TestServiceDefaultsPollSweepAndBaseURL(t *testing.T) {
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: &idleExecutionStore{}, Catalog: idleCatalog{},
		Runner:   engine.NewRunner(engine.NewRegistry()),
		WorkerID: "test-worker", DefaultTimeout: time.Second,
		PublicBaseURL: "https://flow.example.com/",
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	// Trailing slash trimmed: links never render a double slash.
	if got := service.ResumeURL("token-1"); got != "https://flow.example.com/resume/token-1" {
		t.Errorf("ResumeURL() = %q", got)
	}
	if got := service.ApprovalURL("token-1"); got != "https://flow.example.com/approve/token-1" {
		t.Errorf("ApprovalURL() = %q", got)
	}
	bare, err := engine.NewService(engine.ServiceDeps{
		Executions: &idleExecutionStore{}, Catalog: idleCatalog{},
		Runner:   engine.NewRunner(engine.NewRegistry()),
		WorkerID: "test-worker", DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if got := bare.ResumeURL("token-1"); got != "/resume/token-1" {
		t.Errorf("path-only ResumeURL() = %q", got)
	}
}

func TestResumeTokenIsUnguessableAndSingleUseShaped(t *testing.T) {
	first, err := engine.NewResumeToken()
	if err != nil {
		t.Fatalf("NewResumeToken() error = %v", err)
	}
	second, err := engine.NewResumeToken()
	if err != nil {
		t.Fatalf("NewResumeToken() error = %v", err)
	}
	if first == second {
		t.Error("two resume tokens collide")
	}
	raw, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil || len(raw) != 16 {
		t.Errorf("token = %q, want 16 bytes of base64url entropy", first)
	}
}

// Queuing in one process must wake an idle worker in another without waiting
// for its poll interval. PollInterval is 10 s here precisely to isolate the
// channel from the tick: success well under that proves the notification, not
// the fallback, delivered the work.
func TestQueuedExecutionWakesWorkerViaChannel(t *testing.T) {
	dsn := os.Getenv("KILASFLOW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set KILASFLOW_TEST_POSTGRES_DSN to run the PostgreSQL half")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(ctx, config.Database{Driver: "postgres", DSN: dsn}, quiet)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := database.Migrate(db, quiet); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	tenant := repository.TenantScope{ID: "drv-wait-svc"}
	defer func() {
		db.Exec("DELETE FROM execution_node_runs WHERE tenant_id = ?", tenant.ID)
		db.Exec("DELETE FROM execution_waits WHERE tenant_id = ?", tenant.ID)
		db.Exec("DELETE FROM executions WHERE tenant_id = ?", tenant.ID)
		db.Exec("DELETE FROM workflow_versions WHERE tenant_id = ?", tenant.ID)
		db.Exec("DELETE FROM workflows WHERE tenant_id = ?", tenant.ID)
	}()

	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	if err := catalog.Register(waitTestDefinition("test.echo", "test.echo")); err != nil {
		t.Fatalf("Register(test.echo) error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	var echoCalls int
	var echoInputs []workflow.NodeInput
	if err := executors.Register("test.echo", &waitPassthrough{calls: &echoCalls, inputs: &echoInputs}); err != nil {
		t.Fatalf("Register(echo executor) error = %v", err)
	}
	workflowStore := repository.NewWorkflowStore(db.DB)
	saved, err := workflowStore.SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_wake", Name: "Wake me",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "echo", Name: "Echo", Type: "test.echo", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{{
			ID: "manual-echo", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "echo", Port: "main"},
		}},
		Settings: map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	store := repository.NewExecutionStore(db.DB)
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: store, Catalog: catalog, Runner: engine.NewRunner(executors),
		WorkerID: "wake-worker", DefaultTimeout: 30 * time.Second,
		PollInterval: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if err := service.Start(ctx, 2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	watchErrs := make(chan error, 8)
	go func() {
		_ = service.WatchQueue(ctx, dsn, "", func(err error) {
			select {
			case watchErrs <- err:
			default:
			}
		})
	}()
	// One LISTEN round trip before queueing: a notification sent before the
	// listener subscribes is correctly missed, and the 10 s tick would hide
	// that miss past this test's deadline.
	time.Sleep(time.Second)
	queued, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, json.RawMessage(`{"ping":"wake"}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		record, err := store.Get(ctx, tenant, queued.ID)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if record.Status == execution.StatusSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("execution still %q after 8 s with a 10 s tick: the channel did not deliver it", record.Status)
		}
		select {
		case err := <-watchErrs:
			t.Fatalf("WatchQueue reported a drop: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if echoCalls != 1 {
		t.Errorf("echo calls = %d, want exactly one", echoCalls)
	}
}

// Losing the listener is neither silent nor fatal: drops report to onError,
// the watcher stops with its context, and the poll tick keeps delivering
// work on drivers without LISTEN/NOTIFY.
func TestWatchQueueReportsDropsAndTickStillDelivers(t *testing.T) {
	ctx, db, store, catalog, executors, tenant := waitTestSetup(t)
	_ = db
	service := waitTestService(t, store, catalog, executors, "worker-1")
	watchCtx, stop := context.WithCancel(context.Background())
	dropped := make(chan error, 8)
	watched := make(chan error, 1)
	go func() {
		watched <- service.WatchQueue(watchCtx, "postgres://127.0.0.1:1/kilasflow?sslmode=disable", "", func(err error) {
			select {
			case dropped <- err:
			default:
			}
		})
	}()
	select {
	case err := <-dropped:
		if err == nil {
			t.Error("reported drop is nil")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("WatchQueue reported no drop against an unreachable server")
	}
	stop()
	select {
	case err := <-watched:
		if err != nil {
			t.Errorf("WatchQueue() after cancel = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("WatchQueue did not stop with its context")
	}
	// And the tick path never needed the listener: direct claims still work.
	workflowStore := repository.NewWorkflowStore(db.DB)
	saved, err := workflowStore.SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_tick", Name: "Tick delivery",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{}, Settings: map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	if _, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, nil); err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", worked, err)
	}
}
