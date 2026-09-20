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
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
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
	// expiresAfter mints the deadline when the node runs instead of when the
	// test was set up, which is what a real wait node does: it turns a
	// duration into a deadline at execution time. A test that runs on the wall
	// clock and mints its deadline earlier would spend the margin on the run
	// prefix before the suspension rather than on the deadline.
	expiresAfter time.Duration
}

func (executor *waitSuspender) Execute(_ context.Context, _ workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	*executor.calls++
	*executor.requests = append(*executor.requests, request)
	expiresAt := executor.expiresAt
	if executor.expiresAfter > 0 {
		expiresAt = time.Now().Add(executor.expiresAfter)
	}
	return nil, &engine.SuspendError{Mode: executor.mode, ExpiresAt: expiresAt}
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

// waitClock is a clock the test owns. Deadlines are validated, swept and
// armed against it through the service's seam, so a test moves time instead
// of racing the wall clock: a loaded machine can no longer push a 50 ms
// deadline into the past between minting it and validating it.
type waitClock struct {
	mu  sync.Mutex
	now time.Time
}

func newWaitClock() *waitClock {
	return &waitClock{now: time.Now().UTC()}
}

func (clock *waitClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *waitClock) Advance(d time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(d)
}

// waitTimerStub stands in for time.AfterFunc: it records the delay a
// suspension armed and holds its callback, so the test decides when a wait's
// exact deadline fires. That is what proves "the deadline itself requeues the
// execution" without a wall-clock latency margin that load can blow through.
type waitTimerStub struct {
	mu    sync.Mutex
	delay time.Duration
	fire  func()
}

func (stub *waitTimerStub) AfterFunc(after time.Duration, fire func()) *time.Timer {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.delay = after
	stub.fire = fire
	return &time.Timer{}
}

func (stub *waitTimerStub) armed() time.Duration {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return stub.delay
}

// release hands back the callback the last arm registered and clears it, so
// one wake-up cannot be fired twice.
func (stub *waitTimerStub) release() func() {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	fire := stub.fire
	stub.fire = nil
	return fire
}

// waitTestServiceWithClock builds the usual test service and points its wait
// clock and timer scheduler at the test-owned fakes.
func waitTestServiceWithClock(t *testing.T, store *repository.GORMExecutionStore, catalog *node.Registry, executors *engine.Registry, worker string, clock *waitClock, timers *waitTimerStub) *engine.Service {
	t.Helper()
	service := waitTestService(t, store, catalog, executors, worker)
	service.SetClockForTest(clock.Now)
	service.SetTimerForTest(timers.AfterFunc)
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
	queued, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, "", json.RawMessage(`{"api_token":"secret-123","customer":"Ada"}`))
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

// waitSuspendOnce suspends the first time it runs and passes its input through
// after that, which is the rate-limited loop shape: the iteration that waited
// resumes, and the iterations after it do not wait again.
type waitSuspendOnce struct {
	calls     *int
	suspended bool
	// mode is the suspension it raises: an approval wait when empty, which is
	// what a human decision resumes, otherwise the timer mode whose deadline
	// resumes by passing the item that waited through.
	mode      string
	expiresAt time.Time
}

func (executor *waitSuspendOnce) Execute(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	*executor.calls++
	if !executor.suspended {
		executor.suspended = true
		mode, expiresAt := executor.mode, executor.expiresAt
		if mode == "" {
			mode = engine.WaitModeApproval
		}
		if expiresAt.IsZero() {
			expiresAt = time.Now().Add(time.Hour)
		}
		return nil, &engine.SuspendError{Mode: mode, ExpiresAt: expiresAt}
	}
	return workflow.NodeOutput{input["main"]}, nil
}

// TestResumeInsideALoopProcessesEveryBatch is the finding that a Wait inside a
// loop silently ended the loop and reported success.
//
// The scheduling state that says which batch is next lived only in memory, so
// the resumed run never reopened the loop entry: the done branch saw an empty
// stream and the remaining items were simply never processed — silent data loss
// in the Loop -> API -> Wait -> Loop shape that most throttled workflows use.
// The state is in the checkpoint now, and this pins the promise end to end.
func TestResumeInsideALoopProcessesEveryBatch(t *testing.T) {
	ctx, db, store, catalog, executors, tenant := waitTestSetup(t)
	_ = db

	var bodyCalls int
	if err := executors.Register("test.once", &waitSuspendOnce{calls: &bodyCalls}); err != nil {
		t.Fatalf("Register(waiting body) error = %v", err)
	}
	var doneCalls int
	var doneInputs []workflow.NodeInput
	if err := executors.Register("test.passthrough", &waitPassthrough{calls: &doneCalls, inputs: &doneInputs}); err != nil {
		t.Fatalf("Register(passthrough) error = %v", err)
	}
	if err := executors.Register("test.three", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{
				{JSON: map[string]any{"n": 1}},
				{JSON: map[string]any{"n": 2}},
				{JSON: map[string]any{"n": 3}},
			}}, nil
		})); err != nil {
		t.Fatalf("Register(source) error = %v", err)
	}
	for _, definition := range []node.Definition{
		waitTestDefinition("test.three", "test.three"),
		waitTestDefinition("test.once", "test.once"),
	} {
		if err := catalog.Register(definition); err != nil {
			t.Fatalf("Register(%q) error = %v", definition.Type, err)
		}
	}

	link := func(id, source, target, port string) workflow.Connection {
		return workflow.Connection{ID: id, Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: source, Port: port},
			Target: workflow.Endpoint{NodeID: target, Port: "main"}}
	}
	workflowStore := repository.NewWorkflowStore(db.DB)
	saved, err := workflowStore.SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_loop_wait", Name: "Throttled loop",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "three", Name: "Three", Type: "test.three", TypeVersion: workflow.V(1)},
			{ID: "loop", Name: "Loop", Type: nodes.LoopNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"batchSize": float64(1), "maxIterations": float64(10)}},
			{ID: "hold", Name: "Hold", Type: "test.once", TypeVersion: workflow.V(1)},
			{ID: "done", Name: "Done", Type: "test.done", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			link("c1", "manual", "three", "main"),
			link("c2", "three", "loop", "main"),
			// The loop output runs the body and the body returns to the loop, so
			// the wait is inside the loop exactly as the throttle pattern puts it.
			link("c3", "loop", "hold", "loop"),
			link("c4", "hold", "loop", "main"),
			link("c5", "loop", "done", "done"),
		},
		Settings: map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}

	queued, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, "", json.RawMessage(`{}`))
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
		t.Fatalf("status = %q, want waiting at the wait inside the loop (error %s)", suspended.Status, suspended.Error)
	}
	if bodyCalls != 1 {
		t.Fatalf("body ran %d times before the wait, want the first batch alone", bodyCalls)
	}

	wait, err := store.FindActiveWait(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("FindActiveWait() error = %v", err)
	}
	if _, _, err := service.ResumeApproval(ctx, tenant.ID, wait.ResumeToken,
		engine.ApprovalDecision{Approved: true, DecidedBy: "tester", RespondedAt: time.Now().UTC()}, false); err != nil {
		t.Fatalf("ResumeApproval() error = %v", err)
	}

	// A fresh worker continues the run, so nothing about the loop's cursor can
	// have survived in process memory.
	restarted := waitTestService(t, store, catalog, executors, "worker-2")
	if worked, err := restarted.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(resume) = (%v, %v), want (true, nil)", worked, err)
	}
	finished, err := store.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get(resumed) error = %v", err)
	}
	if finished.Status != execution.StatusSucceeded {
		t.Fatalf("resumed status = %q, want succeeded (error %s)", finished.Status, finished.Error)
	}
	if bodyCalls != 3 {
		t.Errorf("body ran %d times, want one call per batch: the batches after the wait were dropped", bodyCalls)
	}
	if doneCalls != 1 {
		t.Fatalf("the done branch ran %d times, want once", doneCalls)
	}
	if got := len(doneInputs[0]["main"]); got != 3 {
		t.Errorf("done carried %d items, want all 3 batches: the loop ended after the first one", got)
	}
}

// TestResumeOfAPerItemSuspendProcessesEveryItem is the finding that a node
// resolving its failures item by item dropped the items behind the one that
// suspended.
//
// A tolerated failure is resolved one item at a time, so a node that waits on
// its first item has two more to reach when the suspension ends the pass. The
// scheduling of those items used to happen after the checkpoint had already
// been marshalled, and the checkpoint is the only thing a resumed run reads its
// work from: the run resumed, the waiting node completed from its stored output,
// and every item behind it was lost — silently, with the execution reporting
// success.
func TestResumeOfAPerItemSuspendProcessesEveryItem(t *testing.T) {
	ctx, db, store, catalog, executors, tenant := waitTestSetup(t)
	_ = db

	// The wait clock is the test's: the 50 ms deadline is minted and validated
	// against the same clock, so a loaded machine cannot push it into the past
	// before the suspension is written.
	clock := newWaitClock()
	timers := &waitTimerStub{}

	// A wait with a deadline resumes on its own by passing the item it held
	// through, so every item downstream can be traced back to the batch.
	var holdCalls int
	hold := &waitSuspendOnce{
		calls:     &holdCalls,
		mode:      engine.WaitModeInterval,
		expiresAt: clock.Now().Add(50 * time.Millisecond),
	}
	if err := executors.Register("test.hold", hold); err != nil {
		t.Fatalf("Register(hold) error = %v", err)
	}
	var doneCalls int
	var doneInputs []workflow.NodeInput
	if err := executors.Register("test.passthrough", &waitPassthrough{calls: &doneCalls, inputs: &doneInputs}); err != nil {
		t.Fatalf("Register(passthrough) error = %v", err)
	}
	if err := executors.Register("test.three", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{
				{JSON: map[string]any{"n": float64(1)}},
				{JSON: map[string]any{"n": float64(2)}},
				{JSON: map[string]any{"n": float64(3)}},
			}}, nil
		})); err != nil {
		t.Fatalf("Register(source) error = %v", err)
	}
	if err := catalog.Register(waitTestDefinition("test.three", "test.three")); err != nil {
		t.Fatalf("Register(source) error = %v", err)
	}

	link := func(id, source, target string) workflow.Connection {
		return workflow.Connection{ID: id, Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: source, Port: "main"},
			Target: workflow.Endpoint{NodeID: target, Port: "main"}}
	}
	workflowStore := repository.NewWorkflowStore(db.DB)
	saved, err := workflowStore.SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_per_item_wait", Name: "Per-item wait",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "three", Name: "Three", Type: "test.three", TypeVersion: workflow.V(1)},
			// Tolerating the failure is what resolves this node one item at a
			// time; without it the batch is a single invocation and there is
			// nothing left to schedule.
			{ID: "hold", Name: "Hold", Type: "test.hold", TypeVersion: workflow.V(1),
				Settings: map[string]any{"onError": "continueRegularOutput"}},
			{ID: "done", Name: "Done", Type: "test.done", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			link("c1", "manual", "three"),
			link("c2", "three", "hold"),
			link("c3", "hold", "done"),
		},
		Settings: map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}

	queued, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	service := waitTestServiceWithClock(t, store, catalog, executors, "worker-1", clock, timers)
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(suspend) = (%v, %v), want (true, nil)", worked, err)
	}
	suspended, err := store.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get(suspended) error = %v", err)
	}
	if suspended.Status != execution.StatusWaiting {
		t.Fatalf("status = %q, want waiting at the per-item wait (error %s)", suspended.Status, suspended.Error)
	}
	if holdCalls != 1 {
		t.Fatalf("the waiting node ran %d times before the wait, want the first item alone", holdCalls)
	}

	// The suspension armed the wake-up for its own deadline, and firing that
	// wake-up alone — with the clock at the deadline and no sweep tick —
	// requeues the execution. That is the property the periodic sweep could
	// never deliver, proven without a wall-clock latency margin.
	if got := timers.armed(); got != 50*time.Millisecond {
		t.Fatalf("the suspension armed a %s wake-up, want its 50 ms deadline", got)
	}
	clock.Advance(50 * time.Millisecond)
	timers.release()()

	// The deadline requeues the execution; a fresh worker continues it.
	awaitExecutionStatus(t, store, tenant, queued.ID, execution.StatusQueued)
	restarted := waitTestService(t, store, catalog, executors, "worker-2")
	if worked, err := restarted.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(resume) = (%v, %v), want (true, nil)", worked, err)
	}
	finished, err := store.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get(resumed) error = %v", err)
	}
	if finished.Status != execution.StatusSucceeded {
		t.Fatalf("resumed status = %q, want succeeded (error %s)", finished.Status, finished.Error)
	}
	// Every item of the batch reaches the node downstream: the one that waited
	// and the two the wait never got to.
	seen := map[float64]int{}
	for _, input := range doneInputs {
		for _, item := range input["main"] {
			number, ok := item.JSON["n"].(float64)
			if !ok {
				t.Fatalf("the node after the wait saw %v, want an item of the batch", item.JSON)
			}
			seen[number]++
		}
	}
	for _, want := range []float64{1, 2, 3} {
		if seen[want] != 1 {
			t.Errorf("item %v reached the node after the wait %d times, want exactly once; it saw %v",
				want, seen[want], seen)
		}
	}
	if holdCalls != 3 {
		t.Errorf("the waiting node ran %d times, want one per item: %d", holdCalls, doneCalls)
	}
}

// The deadline used to be noticed only by the periodic sweep, so a two-second
// pause took up to a minute and a webhook whose workflow waited answered 504
// long before the wait ended. Each suspension now arms a timer for the exact
// deadline it was written with; the sweep remains the floor for waits a
// process left behind when it exited. The test owns the service's clock and
// timer through the seam, so it proves that wake-up — not the periodic sweep,
// and not wall-clock luck — is what settles the wait.
func TestExpiredWaitsResolveOnTheirOwnDeadline(t *testing.T) {
	ctx, db, store, catalog, executors, tenant := waitTestSetup(t)
	_ = db
	clock := newWaitClock()
	timers := &waitTimerStub{}
	hold := &waitSuspender{mode: engine.WaitModeApproval, expiresAt: clock.Now().Add(250 * time.Millisecond)}
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
	queued, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, "", json.RawMessage(`{"customer":"Ada"}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	service := waitTestServiceWithClock(t, store, catalog, executors, "worker-1", clock, timers)
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(suspend) = (%v, %v), want (true, nil)", worked, err)
	}
	// Nothing to settle yet: the same call that would have reaped a minute-old
	// wait finds this one still inside its deadline, and that is not an error.
	if settled, err := service.SweepWaits(ctx); err != nil || settled != 0 {
		t.Fatalf("SweepWaits(before the deadline) = (%d, %v), want (0, nil)", settled, err)
	}
	if got := timers.armed(); got != 250*time.Millisecond {
		t.Fatalf("the suspension armed a %s wake-up, want its 250 ms deadline", got)
	}
	// The execution settles itself. An approval is the mode nothing resumes on
	// its own, so it fails by name at the deadline rather than staying
	// suspended forever.
	clock.Advance(250 * time.Millisecond)
	timers.release()()
	awaitExecutionStatus(t, store, tenant, queued.ID, execution.StatusFailed)
	failed, err := store.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get(expired) error = %v", err)
	}
	if !strings.Contains(string(failed.Error), repository.WaitExpiredCode) {
		t.Errorf("error = %s, want the named %q failure", failed.Error, repository.WaitExpiredCode)
	}
	if failed.FinishedAt == nil {
		t.Error("expired execution has no finish timestamp")
	}
	if settled, err := service.SweepWaits(ctx); err != nil || settled != 0 {
		t.Fatalf("SweepWaits(after settling) = (%d, %v), want (0, nil): settling twice must be a no-op", settled, err)
	}

	// Timer waits resolve the other way: the deadline IS the resume. Firing
	// the exact 50 ms wake-up the suspension armed requeues the execution,
	// with no sweep tick and no wall-clock margin in the way.
	hold.mode = engine.WaitModeInterval
	hold.expiresAt = clock.Now().Add(50 * time.Millisecond)
	queuedTimer, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, "", json.RawMessage(`{"customer":"Bo"}`))
	if err != nil {
		t.Fatalf("QueueManualLatest(timer) error = %v", err)
	}
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(timer suspend) = (%v, %v), want (true, nil)", worked, err)
	}
	awaitExecutionStatus(t, store, tenant, queuedTimer.ID, execution.StatusWaiting)
	if got := timers.armed(); got != 50*time.Millisecond {
		t.Fatalf("the suspension armed a %s wake-up, want its 50 ms deadline", got)
	}
	clock.Advance(50 * time.Millisecond)
	timers.release()()
	awaitExecutionStatus(t, store, tenant, queuedTimer.ID, execution.StatusQueued)
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(timer resume) = (%v, %v), want (true, nil)", worked, err)
	}
	resumed, err := store.Get(ctx, tenant, queuedTimer.ID)
	if err != nil {
		t.Fatalf("Get(timer) error = %v", err)
	}
	if resumed.Status != execution.StatusSucceeded {
		t.Fatalf("timer status = %q, want succeeded (error %s) runs=%s", resumed.Status, resumed.Error, describeRuns(resumed))
	}

	// An absolute deadline resolves the same way: until is a timer with a
	// clock time instead of a duration.
	hold.mode = engine.WaitModeUntil
	hold.expiresAt = clock.Now().Add(50 * time.Millisecond)
	queuedUntil, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, "", json.RawMessage(`{"customer":"Cy"}`))
	if err != nil {
		t.Fatalf("QueueManualLatest(until) error = %v", err)
	}
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(until suspend) = (%v, %v), want (true, nil)", worked, err)
	}
	awaitExecutionStatus(t, store, tenant, queuedUntil.ID, execution.StatusWaiting)
	if got := timers.armed(); got != 50*time.Millisecond {
		t.Fatalf("the suspension armed a %s wake-up, want its 50 ms deadline", got)
	}
	clock.Advance(50 * time.Millisecond)
	timers.release()()
	awaitExecutionStatus(t, store, tenant, queuedUntil.ID, execution.StatusQueued)
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

// TestDefaultSchedulerSettlesAWaitAtItsDeadline is the production half of the
// deadline proof above, and it exists because owning the seam everywhere left
// the nil defaults untested: the tests above always install a clock and a
// scheduler, so nothing would notice if the fallbacks behind them broke.
// Replace the nil scheduler with one that never fires and every other test in
// this package stays green, while a deployed process arms no wake-up at all —
// only the minute-long sweep settles a wait, and a webhook whose workflow
// waits answers 504 long before its wait ends.
//
// So this test wires the service exactly as production does — no
// SetClockForTest, no SetTimerForTest — and gives the wait a real 20 ms
// deadline on the wall clock. Nothing here moves time, calls SweepWaits or
// fires a wake-up by hand: the real time.AfterFunc timer is the only thing
// that can requeue the execution, and the assertion is the exact status it
// leaves behind, not how long that took.
func TestDefaultSchedulerSettlesAWaitAtItsDeadline(t *testing.T) {
	ctx, db, store, catalog, executors, tenant := waitTestSetup(t)
	_ = db
	var calls int
	var inputs []workflow.NodeInput
	hold := &waitSuspender{
		mode:         engine.WaitModeInterval,
		expiresAfter: 20 * time.Millisecond,
		calls:        &calls,
		requests:     &[]engine.Request{},
	}
	waitRegisterFakes(t, executors, hold, &calls, &inputs)

	workflowStore := repository.NewWorkflowStore(db.DB)
	saved, err := workflowStore.SaveDraft(ctx, tenant, waitTestWorkflow())
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	queued, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, "", json.RawMessage(`{"customer":"Ada"}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	service := waitTestService(t, store, catalog, executors, "worker-1")
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(suspend) = (%v, %v), want (true, nil)", worked, err)
	}
	// A suspension that never parked an execution would be indistinguishable
	// from an expired one later, so the wait is proven parked before the
	// deadline is allowed to pass.
	suspended, err := store.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get(suspended) error = %v", err)
	}
	if suspended.Status != execution.StatusWaiting {
		t.Fatalf("status = %q, want waiting on a 20 ms deadline the production clock accepted (error %s)",
			suspended.Status, suspended.Error)
	}

	// Only the armed production timer requeues this execution.
	awaitExecutionStatus(t, store, tenant, queued.ID, execution.StatusQueued)

	// The requeue is a real resume, not just a status change: a second worker
	// continues the workflow from the checkpoint and finishes it.
	resuming := waitTestService(t, store, catalog, executors, "worker-2")
	if worked, err := resuming.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(resume) = (%v, %v), want (true, nil)", worked, err)
	}
	resumed, err := store.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get(resumed) error = %v", err)
	}
	if resumed.Status != execution.StatusSucceeded {
		t.Fatalf("resumed status = %q, want succeeded (error %s) runs=%s", resumed.Status, resumed.Error, describeRuns(resumed))
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
	queued, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, "", json.RawMessage(`{}`))
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
	queued, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, "", json.RawMessage(`{"ping":"wake"}`))
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
	if _, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, "", nil); err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", worked, err)
	}
}

// describeRuns renders one execution's node runs for a failure message: which
// node ran, in what status, and how many times.
func describeRuns(record execution.Record) string {
	rendered := make([]string, 0, len(record.NodeRuns))
	for _, run := range record.NodeRuns {
		rendered = append(rendered, fmt.Sprintf("%s#%d/%s", run.NodeID, run.RunIndex, run.Status))
	}
	return "[" + strings.Join(rendered, " ") + "]"
}
