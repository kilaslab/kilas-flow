package engine_test

// Two worker processes against one PostgreSQL database: distinct worker IDs,
// one winner per claim, cross-process wake, and graceful shutdown that hands
// nothing half-done.
//
// NOTE (shared-server race): the PostgreSQL-gated tests in this file run
// against one shared KILASFLOW_TEST_POSTGRES_DSN. A combined run must use
// `go test -p 1` (or give each package its own database), or another
// package's schema reset can vanish the tables mid-test. See
// .pine/memory/persistence.md.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
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

// multiprocessEcho counts its calls under a mutex and passes its input items
// through unchanged. The count is the whole assertion: with N queued
// executions and two services racing, exactly N calls means no execution ran
// twice, and N successes means none was left stranded.
type multiprocessEcho struct {
	mu    *sync.Mutex
	calls *int
}

func (executor *multiprocessEcho) Execute(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	executor.mu.Lock()
	*executor.calls++
	executor.mu.Unlock()
	items := input["main"]
	if items == nil {
		items = []workflow.Item{}
	}
	return workflow.NodeOutput{items}, nil
}

func multiprocessDefinition(nodeType, executorID string) node.Definition {
	return node.Definition{
		Type: nodeType, Version: workflow.V(1),
		DisplayName: "Test " + nodeType, Category: "Test",
		Group:      []node.NodeGroup{node.GroupTransform},
		Inputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs:    []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		ExecutorID: executorID,
	}
}

// multiprocessPostgres opens the shared server or skips, migrates, and
// arranges tenant-scoped cleanup. Rows are removed by tenant afterwards
// rather than tables dropped — another package may be using them.
func multiprocessPostgres(t *testing.T, tenant repository.TenantScope) (*database.DB, string) {
	t.Helper()
	dsn := os.Getenv("KILASFLOW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set KILASFLOW_TEST_POSTGRES_DSN to run the PostgreSQL half")
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(context.Background(), config.Database{Driver: "postgres", DSN: dsn}, quiet)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, quiet); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM execution_node_runs WHERE tenant_id = ?", tenant.ID)
		db.Exec("DELETE FROM execution_waits WHERE tenant_id = ?", tenant.ID)
		db.Exec("DELETE FROM executions WHERE tenant_id = ?", tenant.ID)
		db.Exec("DELETE FROM workflow_versions WHERE tenant_id = ?", tenant.ID)
		db.Exec("DELETE FROM workflows WHERE tenant_id = ?", tenant.ID)
	})
	return db, dsn
}

func multiprocessCatalog(t *testing.T) (*node.Registry, *engine.Registry, *sync.Mutex, *int) {
	t.Helper()
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	if err := catalog.Register(multiprocessDefinition("test.echo", "test.echo")); err != nil {
		t.Fatalf("Register(test.echo) error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	var mu sync.Mutex
	var calls int
	if err := executors.Register("test.echo", &multiprocessEcho{mu: &mu, calls: &calls}); err != nil {
		t.Fatalf("Register(echo executor) error = %v", err)
	}
	return catalog, executors, &mu, &calls
}

func multiprocessWorkflow(t *testing.T, ctx context.Context, db *database.DB, tenant repository.TenantScope, workflowID string) {
	t.Helper()
	_, err := repository.NewWorkflowStore(db.DB).SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            workflowID, Name: "Multiprocess",
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
}

func multiprocessService(t *testing.T, store *repository.GORMExecutionStore, catalog *node.Registry, executors *engine.Registry, worker string, poll time.Duration) *engine.Service {
	t.Helper()
	deps := engine.ServiceDeps{
		Executions: store, Catalog: catalog, Runner: engine.NewRunner(executors),
		WorkerID: worker, DefaultTimeout: 30 * time.Second,
	}
	if poll > 0 {
		deps.PollInterval = poll
	}
	service, err := engine.NewService(deps)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

// Two services with distinct worker IDs against one database claim disjoint
// executions: every queued run succeeds exactly once, so the shared counter
// reads N and no execution is left queued or running.
func TestTwoServicesClaimDisjointExecutionsOnPostgres(t *testing.T) {
	tenant := repository.TenantScope{ID: "drv-multiproc"}
	db, _ := multiprocessPostgres(t, tenant)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	catalog, executors, mu, calls := multiprocessCatalog(t)
	multiprocessWorkflow(t, ctx, db, tenant, "drv_wf_multiproc")

	const queued = 8
	store := repository.NewExecutionStore(db.DB)
	ids := make([]string, 0, queued)
	for range queued {
		record, err := store.QueueManualLatest(ctx, tenant, "drv_wf_multiproc", catalog, json.RawMessage(`{"n":1}`))
		if err != nil {
			t.Fatalf("QueueManualLatest() error = %v", err)
		}
		ids = append(ids, record.ID)
	}

	serviceA := multiprocessService(t, repository.NewExecutionStore(db.DB), catalog, executors, "multiproc-a", 0)
	serviceB := multiprocessService(t, repository.NewExecutionStore(db.DB), catalog, executors, "multiproc-b", 0)
	if err := serviceA.Start(ctx, 2); err != nil {
		t.Fatalf("Start(A) error = %v", err)
	}
	if err := serviceB.Start(ctx, 2); err != nil {
		t.Fatalf("Start(B) error = %v", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for _, id := range ids {
		for {
			record, err := store.Get(ctx, tenant, id)
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			if record.Status == execution.StatusSucceeded {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("execution %q still %q after 30 s with two services running: work was stranded", id, record.Status)
			}
			select {
			case <-ctx.Done():
				t.Fatal("test context cancelled while waiting for executions")
			case <-time.After(50 * time.Millisecond):
			}
		}
	}

	mu.Lock()
	got := *calls
	mu.Unlock()
	if got != queued {
		t.Errorf("echo calls = %d, want exactly %d: an execution ran twice or not at all", got, queued)
	}
}

// A queued execution wakes the other process's worker over LISTEN/NOTIFY
// without waiting out the poll interval. Both services tick every 10 s and
// only B listens; success well inside 10 s proves the channel delivered it.
func TestCrossProcessWakeReachesTheOtherWorkerOnPostgres(t *testing.T) {
	tenant := repository.TenantScope{ID: "drv-multiproc-wake"}
	db, dsn := multiprocessPostgres(t, tenant)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	catalog, executors, mu, calls := multiprocessCatalog(t)
	multiprocessWorkflow(t, ctx, db, tenant, "drv_wf_multiproc_wake")

	store := repository.NewExecutionStore(db.DB)
	serviceA := multiprocessService(t, repository.NewExecutionStore(db.DB), catalog, executors, "multiproc-wake-a", 10*time.Second)
	serviceB := multiprocessService(t, repository.NewExecutionStore(db.DB), catalog, executors, "multiproc-wake-b", 10*time.Second)
	if err := serviceA.Start(ctx, 1); err != nil {
		t.Fatalf("Start(A) error = %v", err)
	}
	if err := serviceB.Start(ctx, 1); err != nil {
		t.Fatalf("Start(B) error = %v", err)
	}
	watchErrs := make(chan error, 8)
	go func() {
		_ = serviceB.WatchQueue(ctx, dsn, "", func(err error) {
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

	queuedAt := time.Now()
	queued, err := store.QueueManualLatest(ctx, tenant, "drv_wf_multiproc_wake", catalog, json.RawMessage(`{"ping":"wake"}`))
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
			t.Fatalf("execution still %q after 8 s with a 10 s tick: the channel did not reach the other worker", record.Status)
		}
		select {
		case err := <-watchErrs:
			t.Fatalf("WatchQueue reported a drop: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if latency := time.Since(queuedAt); latency >= 10*time.Second {
		t.Errorf("queue-to-success latency = %v, want well inside the 10 s tick", latency)
	}
	mu.Lock()
	got := *calls
	mu.Unlock()
	if got != 1 {
		t.Errorf("echo calls = %d, want exactly one: the other worker must win the claim alone", got)
	}
}

// Cancelling a service's context mid-run hands nothing half-done: the
// in-flight execution reaches a terminal cancelled state, its lease is
// released, and a second service finds nothing left to claim.
func TestGracefulShutdownHandsNothingHalfDone(t *testing.T) {
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(ctx, config.Database{Driver: "sqlite", DSN: t.TempDir() + "/kilasflow.db"}, quiet)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, quiet); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	tenant := repository.TenantScope{ID: "tenant-shutdown"}

	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	if err := catalog.Register(multiprocessDefinition("test.block", "test.block")); err != nil {
		t.Fatalf("Register(test.block) error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	// A context-aware executor: SIGTERM (the service context ending) stops
	// the run the way a cooperative worker does, rather than hanging until
	// the execution timeout.
	if err := executors.Register("test.block", engine.ExecutorFunc(func(execCtx context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
		<-execCtx.Done()
		return nil, execCtx.Err()
	})); err != nil {
		t.Fatalf("Register(block executor) error = %v", err)
	}
	_, err = repository.NewWorkflowStore(db.DB).SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_shutdown", Name: "Shutdown",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "block", Name: "Block", Type: "test.block", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{{
			ID: "manual-block", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "block", Port: "main"},
		}},
		Settings: map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	store := repository.NewExecutionStore(db.DB)
	queued, err := store.QueueManualLatest(ctx, tenant, "wf_shutdown", catalog, nil)
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}

	service, err := engine.NewService(engine.ServiceDeps{
		Executions: store, Catalog: catalog, Runner: engine.NewRunner(executors),
		WorkerID: "shutdown-1", DefaultTimeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	defer stop()
	if err := service.Start(runCtx, 1); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	service.Wake()

	claimed := false
	claimDeadline := time.Now().Add(10 * time.Second)
	for !claimed {
		record, err := store.Get(ctx, tenant, queued.ID)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if record.Status == execution.StatusRunning {
			claimed = true
			break
		}
		if time.Now().After(claimDeadline) {
			t.Fatalf("execution still %q after 10 s: the worker never claimed it", record.Status)
		}
		select {
		case <-time.After(20 * time.Millisecond):
		}
	}

	// SIGTERM: the service context ends while the run is in flight.
	stop()
	terminalDeadline := time.Now().Add(10 * time.Second)
	var terminal execution.Record
	for {
		record, err := store.Get(ctx, tenant, queued.ID)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		switch record.Status {
		case execution.StatusCancelled, execution.StatusFailed, execution.StatusSucceeded:
			terminal = record
		}
		if terminal.ID != "" {
			break
		}
		if time.Now().After(terminalDeadline) {
			t.Fatalf("execution still %q after shutdown: the lease was left held", record.Status)
		}
		select {
		case <-time.After(20 * time.Millisecond):
		}
	}
	if terminal.Status != execution.StatusCancelled {
		t.Errorf("execution status after shutdown = %q, want %q", terminal.Status, execution.StatusCancelled)
	}
	if terminal.LeaseOwner != "" {
		t.Errorf("lease owner after shutdown = %q, want it released so nothing is left half-done", terminal.LeaseOwner)
	}

	restarted, err := engine.NewService(engine.ServiceDeps{
		Executions: store, Catalog: catalog, Runner: engine.NewRunner(executors),
		WorkerID: "shutdown-2", DefaultTimeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if worked, err := restarted.RunOnce(ctx); err != nil || worked {
		t.Errorf("RunOnce(after shutdown) = (%v, %v), want (false, nil): nothing half-done may remain", worked, err)
	}
}
