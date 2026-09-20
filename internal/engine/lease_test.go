package engine_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/ai"
	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// This file covers BUG-1tj5wy: the worker lease was the run timeout, nothing
// renewed it, and the trace was written after the lease had run out — so a
// second worker reclaimed the execution and ran the whole graph again, side
// effects included. The tests here assert the three parts of that: a claim is
// not the run timeout, a live worker keeps its claim past it, and a trace that
// cannot be written settles the execution instead of leaving it to be
// reclaimed for ever.

// engineSandbox is one migrated database with the full node catalog and
// executor registry a workflow needs to run for real.
type engineSandbox struct {
	ctx       context.Context
	db        *database.DB
	store     *repository.GORMExecutionStore
	catalog   *node.Registry
	executors *engine.Registry
}

func newEngineSandbox(t *testing.T, name string) *engineSandbox {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), name),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	return &engineSandbox{ctx: ctx, db: db, store: repository.NewExecutionStore(db.DB), catalog: catalog, executors: executors}
}

// leaseRemaining reads how long the claim ClaimNext wrote still has to run,
// which is the durable form of "how long this worker holds the execution".
func (sandbox *engineSandbox) leaseRemaining(t *testing.T, scope repository.TenantScope, executionID string) time.Duration {
	t.Helper()
	var row struct {
		LeaseExpiresAt *time.Time
	}
	if err := sandbox.db.DB.Table("executions").
		Select("lease_expires_at").
		Where("tenant_id = ? AND id = ?", scope.ID, executionID).
		Scan(&row).Error; err != nil {
		t.Fatalf("read lease: %v", err)
	}
	if row.LeaseExpiresAt == nil {
		t.Fatal("the claimed execution carries no lease expiry: nothing fences a second worker")
	}
	return time.Until(*row.LeaseExpiresAt)
}

// queuedLeaseWorkflow saves a manual-trigger workflow followed by one test node
// that blocks until the test releases it, and queues one execution of it.
func (sandbox *engineSandbox) queuedLeaseWorkflow(t *testing.T, scope repository.TenantScope, workflowID string) execution.Record {
	t.Helper()
	if err := sandbox.catalog.Register(node.Definition{
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  "kilasflow.test.lease", Version: workflow.V(1), DisplayName: "Lease", Category: "Test",
		Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.lease",
	}); err != nil {
		t.Fatalf("Register(lease node) error = %v", err)
	}
	_, err := repository.NewWorkflowStore(sandbox.db.DB).SaveDraft(sandbox.ctx, scope, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            workflowID,
		Name:          "Lease fixture",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "slow", Name: "Lease", Type: "kilasflow.test.lease", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{{
			ID: "manual-slow", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "slow", Port: "main"},
		}},
		Settings: map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft(%q) error = %v", workflowID, err)
	}
	queued, err := sandbox.store.QueueManualLatest(sandbox.ctx, scope, workflowID, sandbox.catalog, "", nil)
	if err != nil {
		t.Fatalf("QueueManualLatest(%q) error = %v", workflowID, err)
	}
	return queued
}

// blockingNode is a node that reports when it starts and then waits to be
// released, so a test can hold a run open while it inspects the claim.
type blockingNode struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	calls   int
	mu      sync.Mutex
}

func newBlockingNode() *blockingNode {
	return &blockingNode{started: make(chan struct{}), release: make(chan struct{})}
}

func (blocker *blockingNode) execute(ctx context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	blocker.mu.Lock()
	blocker.calls++
	blocker.mu.Unlock()
	select {
	case blocker.started <- struct{}{}:
	default:
	}
	select {
	case <-blocker.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return workflow.NodeOutput{input["main"]}, nil
}

func (blocker *blockingNode) callCount() int {
	blocker.mu.Lock()
	defer blocker.mu.Unlock()
	return blocker.calls
}

func (blocker *blockingNode) unblock() {
	blocker.once.Do(func() { close(blocker.release) })
}

// A claim is not an assertion about how long the run will take.
//
// They were the same number, which is what let a run plus its trace write
// outlive the claim and be handed to a second worker (BUG-1tj5wy). The lease is
// read from the row the claim wrote, so this fails on the old arithmetic
// whatever the timing.
func TestAClaimedExecutionOutlivesItsRunTimeout(t *testing.T) {
	sandbox := newEngineSandbox(t, "kilasflow.db")
	tenant := repository.TenantScope{ID: "tenant-lease-length"}
	blocker := newBlockingNode()
	t.Cleanup(blocker.unblock)
	if err := sandbox.executors.Register("test.lease", engine.ExecutorFunc(blocker.execute)); err != nil {
		t.Fatalf("Register(lease executor) error = %v", err)
	}
	queued := sandbox.queuedLeaseWorkflow(t, tenant, "wf_lease_length")

	const timeout = 200 * time.Millisecond
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: sandbox.store, Catalog: sandbox.catalog, Runner: engine.NewRunner(sandbox.executors),
		WorkerID: "lease-length", DefaultTimeout: timeout,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := service.RunOnce(sandbox.ctx)
		done <- err
	}()
	select {
	case <-blocker.started:
	case <-time.After(2 * time.Second):
		t.Fatal("the run never reached its node")
	}

	// Measured while the run is in flight, so this is the claim the worker is
	// actually holding: on the old arithmetic it was the run timeout counted
	// from the claim, which is what expired underneath a run that used its
	// whole budget and the trace write after it.
	if remaining := sandbox.leaseRemaining(t, tenant, queued.ID); remaining <= timeout {
		t.Fatalf("claim has %v left with a run timeout of %v, want the lease to outlive the run: a claim no longer than the timeout expires while the worker is still writing its trace", remaining, timeout)
	}

	blocker.unblock()
	if err := <-done; err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	record, err := sandbox.store.Get(sandbox.ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got, want := record.Status, execution.StatusSucceeded; got != want {
		t.Errorf("execution status = %q, want %q", got, want)
	}
	if got := blocker.callCount(); got != 1 {
		t.Errorf("node calls = %d, want 1", got)
	}
}

// A worker that outlives its lease keeps the execution, because it is still
// holding it.
//
// The second worker's claim is the defect itself: before the heartbeat, the
// lease simply expired on schedule while the first worker was still inside the
// graph, and the graph started again from the trigger alongside it.
func TestAWorkerThatOutlivesItsLeaseIsNotReclaimed(t *testing.T) {
	sandbox := newEngineSandbox(t, "kilasflow.db")
	tenant := repository.TenantScope{ID: "tenant-lease-heartbeat"}
	blocker := newBlockingNode()
	t.Cleanup(blocker.unblock)
	if err := sandbox.executors.Register("test.lease", engine.ExecutorFunc(blocker.execute)); err != nil {
		t.Fatalf("Register(lease executor) error = %v", err)
	}
	queued := sandbox.queuedLeaseWorkflow(t, tenant, "wf_lease_heartbeat")

	// A lease far shorter than the run: only a renewal can keep it.
	const (
		timeout = 5 * time.Second
		lease   = 300 * time.Millisecond
	)
	holder, err := engine.NewService(engine.ServiceDeps{
		Executions: sandbox.store, Catalog: sandbox.catalog, Runner: engine.NewRunner(sandbox.executors),
		WorkerID: "holder", DefaultTimeout: timeout, LeaseDuration: lease,
	})
	if err != nil {
		t.Fatalf("NewService(holder) error = %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := holder.RunOnce(sandbox.ctx)
		done <- err
	}()
	select {
	case <-blocker.started:
	case <-time.After(2 * time.Second):
		t.Fatal("the run never reached its node")
	}
	claimed, err := sandbox.store.Get(sandbox.ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get(claimed) error = %v", err)
	}

	// Three lease periods: without a renewal the execution is reclaimable long
	// before this claim is made.
	time.Sleep(3 * lease)

	second, err := engine.NewService(engine.ServiceDeps{
		Executions: sandbox.store, Catalog: sandbox.catalog, Runner: engine.NewRunner(sandbox.executors),
		WorkerID: "second", DefaultTimeout: timeout, LeaseDuration: lease,
	})
	if err != nil {
		t.Fatalf("NewService(second) error = %v", err)
	}
	claimedBySecond := make(chan bool, 1)
	go func() {
		worked, _ := second.RunOnce(sandbox.ctx)
		claimedBySecond <- worked
	}()
	select {
	case worked := <-claimedBySecond:
		if worked {
			t.Fatal("a second worker claimed an execution whose worker is still running: the graph would run twice, side effects included")
		}
	case <-time.After(time.Second):
		t.Fatal("a second worker is running the execution this worker already holds")
	}

	running, err := sandbox.store.Get(sandbox.ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get(running) error = %v", err)
	}
	if running.LeaseOwner != claimed.LeaseOwner {
		t.Errorf("lease owner = %q, want %q: the claim changed hands while the holder was alive", running.LeaseOwner, claimed.LeaseOwner)
	}
	if got, want := running.Status, execution.StatusRunning; got != want {
		t.Errorf("execution status = %q, want %q", got, want)
	}

	blocker.unblock()
	if err := <-done; err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	record, err := sandbox.store.Get(sandbox.ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got, want := record.Status, execution.StatusSucceeded; got != want {
		t.Errorf("execution status = %q, want %q", got, want)
	}
	if got := blocker.callCount(); got != 1 {
		t.Errorf("node calls = %d, want 1: the execution ran once", got)
	}
}

// failingTraceStore refuses the trace write, which is the failure mode a run
// index collision used to produce.
//
// Both write paths are refused: the batch the service uses now and the single
// row it used before, so the test states the behaviour it wants — a failed
// trace is terminal — rather than the shape of the call that failed.
type failingTraceStore struct {
	*repository.GORMExecutionStore
}

func (store failingTraceStore) CreateNodeRuns(context.Context, repository.TenantScope, []execution.NodeRun) ([]execution.NodeRun, error) {
	return nil, fmt.Errorf("simulated trace write failure")
}

func (store failingTraceStore) CreateNodeRun(context.Context, repository.TenantScope, execution.NodeRun) (execution.NodeRun, error) {
	return execution.NodeRun{}, fmt.Errorf("simulated trace write failure")
}

// A trace that cannot be written settles the execution instead of leaving it
// running to be reclaimed and run again.
//
// This is the second half of BUG-hfhzq6 and the reason a single collision —
// one skipped row inside a loop — turned into a workflow that ran for ever:
// the persist error returned without a terminal write, the lease expired, the
// execution was reclaimed, and the whole graph ran again to fail at the same
// write. One failed execution carrying the reason is the same failure reported
// once.
func TestAFailedTraceWriteSettlesTheExecutionInsteadOfLeavingItForReclaim(t *testing.T) {
	sandbox := newEngineSandbox(t, "kilasflow.db")
	tenant := repository.TenantScope{ID: "tenant-persist-failure"}
	blocker := newBlockingNode()
	blocker.unblock()
	if err := sandbox.executors.Register("test.lease", engine.ExecutorFunc(blocker.execute)); err != nil {
		t.Fatalf("Register(lease executor) error = %v", err)
	}
	queued := sandbox.queuedLeaseWorkflow(t, tenant, "wf_persist_failure")

	const timeout = 300 * time.Millisecond
	failing, err := engine.NewService(engine.ServiceDeps{
		Executions: failingTraceStore{sandbox.store}, Catalog: sandbox.catalog, Runner: engine.NewRunner(sandbox.executors),
		WorkerID: "broken", DefaultTimeout: timeout,
	})
	if err != nil {
		t.Fatalf("NewService(failing) error = %v", err)
	}
	worked, runErr := failing.RunOnce(sandbox.ctx)
	if !worked {
		t.Fatal("RunOnce() claimed nothing, want the queued execution attempted")
	}
	if runErr == nil {
		t.Fatal("RunOnce() = nil error with a trace that cannot be written")
	}

	record, err := sandbox.store.Get(sandbox.ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got, want := record.Status, execution.StatusFailed; got != want {
		t.Fatalf("execution status = %q, want %q: a trace that cannot be written must be terminal, not left for a reclaim", got, want)
	}
	if !jsonContains(string(record.Error), `{"code":"execution.persist_failed"}`) {
		t.Errorf("execution error = %s, want the persist failure reported", record.Error)
	}
	if record.FinishedAt == nil {
		t.Error("execution finishedAt is nil")
	}

	// Past the lease it would have held, nothing may pick it up again.
	time.Sleep(timeout + 100*time.Millisecond)
	healthy, err := engine.NewService(engine.ServiceDeps{
		Executions: sandbox.store, Catalog: sandbox.catalog, Runner: engine.NewRunner(sandbox.executors),
		WorkerID: "healthy", DefaultTimeout: timeout,
	})
	if err != nil {
		t.Fatalf("NewService(healthy) error = %v", err)
	}
	if worked, err := healthy.RunOnce(sandbox.ctx); err != nil || worked {
		t.Fatalf("RunOnce(after persist failure) = (%v, %v), want nothing to claim: a failed trace must not be re-run for ever", worked, err)
	}
	if got := blocker.callCount(); got != 1 {
		t.Errorf("node calls = %d, want 1: the graph must not run again", got)
	}
}
