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
	"github.com/kilaslabs/kilas-flow/internal/events"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/scheduler"
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

// Two schedulers against one database do not double-fire: the due claim
// advances next_run_at inside the same transaction that reads it, so of any
// number of processes racing one due time exactly one queues it. This is the
// election — advisory locks are unnecessary — and role gating (workers never
// run the scheduler) keeps a split deployment to a single scheduler anyway.
func TestTwoSchedulersDoNotDoubleFireOnPostgres(t *testing.T) {
	tenant := repository.TenantScope{ID: "drv-multiproc-sched"}
	db, _ := multiprocessPostgres(t, tenant)
	t.Cleanup(func() {
		db.Exec("DELETE FROM schedules WHERE tenant_id = ?", tenant.ID)
	})
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	catalog, _, _, _ := multiprocessCatalog(t)
	workflowStore := repository.NewWorkflowStore(db.DB)
	stored, err := workflowStore.SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "drv_wf_sched", Name: "Scheduled",
		Nodes: []workflow.Node{{
			ID: "n1", Name: "Schedule", Type: nodes.ScheduleType, TypeVersion: workflow.V(1),
			Position: workflow.Position{}, Parameters: map[string]any{"cron": "0 * * * *"},
		}},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	active, err := workflowStore.Activate(ctx, tenant, stored.ID, catalog)
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	schedStore := repository.NewScheduleStore(db.DB)
	past := time.Now().UTC().Add(-time.Minute)
	if _, err := schedStore.Create(ctx, tenant, repository.Schedule{
		WorkflowID: active.ID, NodeID: "n1", Cron: "* * * * *", Active: true, NextRunAt: &past,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var mu sync.Mutex
	queued := 0
	queue := func(_ context.Context, _, _, _, _ string, _ json.RawMessage) error {
		mu.Lock()
		queued++
		mu.Unlock()
		return nil
	}
	serviceA, err := scheduler.New(scheduler.Options{Schedules: schedStore, Queue: queue, Logger: quiet})
	if err != nil {
		t.Fatalf("New(A) error = %v", err)
	}
	serviceB, err := scheduler.New(scheduler.Options{Schedules: schedStore, Queue: queue, Logger: quiet})
	if err != nil {
		t.Fatalf("New(B) error = %v", err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, service := range []*scheduler.Service{serviceA, serviceB} {
		wg.Add(1)
		go func(svc *scheduler.Service) {
			defer wg.Done()
			if _, err := svc.Tick(ctx); err != nil {
				errs <- err
			}
		}(service)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("Tick() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if queued != 1 {
		t.Errorf("concurrent ticks queued %d runs for one due time, want exactly 1", queued)
	}
}

// An execution running in a worker process streams its live node events to a
// browser connected to a different API process: the worker relays
// identifiers-only notices over LISTEN/NOTIFY and the API process
// republishes them into its own broker. The notices stay tens of bytes —
// the node's output never rides the channel — and the durable trace behind
// GET stays complete whether or not a notice lands.
func TestWorkerEventsReachAnAPIProcessBrokerOnPostgres(t *testing.T) {
	tenant := repository.TenantScope{ID: "drv-multiproc-events"}
	db, dsn := multiprocessPostgres(t, tenant)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	catalog, executors, _, _ := multiprocessCatalog(t)
	multiprocessWorkflow(t, ctx, db, tenant, "drv_wf_multiproc_events")

	relay := func(channel, payload string) error {
		return db.Exec("SELECT pg_notify(?, ?)", channel, payload).Error
	}
	workerBroker := events.NewBroker(events.BrokerOptions{})
	worker, err := engine.NewService(engine.ServiceDeps{
		Executions: repository.NewExecutionStore(db.DB), Catalog: catalog, Runner: engine.NewRunner(executors),
		WorkerID: "multiproc-ev-worker", DefaultTimeout: 30 * time.Second,
		Events: workerBroker, RelayPrefix: "", RelaySend: relay,
	})
	if err != nil {
		t.Fatalf("NewService(worker) error = %v", err)
	}
	apiBroker := events.NewBroker(events.BrokerOptions{})
	api, err := engine.NewService(engine.ServiceDeps{
		Executions: repository.NewExecutionStore(db.DB), Catalog: catalog, Runner: engine.NewRunner(executors),
		WorkerID: "multiproc-ev-api", DefaultTimeout: 30 * time.Second, Events: apiBroker,
	})
	if err != nil {
		t.Fatalf("NewService(api) error = %v", err)
	}
	if err := worker.Start(ctx, 1); err != nil {
		t.Fatalf("Start(worker) error = %v", err)
	}
	watchErrs := make(chan error, 8)
	go func() {
		_ = api.WatchRemoteEvents(ctx, dsn, "", func(err error) {
			select {
			case watchErrs <- err:
			default:
			}
		})
	}()
	// One LISTEN round trip before queueing: a notice sent before the
	// listener subscribes is correctly missed, and nothing else would
	// deliver it.
	time.Sleep(time.Second)

	store := repository.NewExecutionStore(db.DB)
	queued, err := store.QueueManualLatest(ctx, tenant, "drv_wf_multiproc_events", catalog, json.RawMessage(`{"n":1}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	subscription := apiBroker.Subscribe(tenant.ID, queued.ID, 0)
	defer subscription.Close()
	deadline := time.After(15 * time.Second)
	var seenNode, seenTerminal bool
	for !seenTerminal {
		select {
		case err := <-watchErrs:
			t.Fatalf("WatchRemoteEvents reported a drop: %v", err)
		case event, open := <-subscription.Events():
			if !open {
				t.Fatal("the API broker closed the feed before a terminal event")
			}
			if event.ExecutionID != queued.ID {
				t.Fatalf("API broker carried execution %q, want %q: tenants must not cross", event.ExecutionID, queued.ID)
			}
			switch event.Type {
			case events.NodeCompleted:
				seenNode = true
			case events.ExecutionCompleted:
				seenTerminal = true
			}
		case <-deadline:
			t.Fatalf("API broker saw node:%v terminal:%v after 15 s: the worker's events never arrived", seenNode, seenTerminal)
		}
	}
	if !seenNode {
		t.Error("the API feed closed on terminal without a node event: live progress never arrived")
	}
}

// A cancellation requested from another process stops the holder in flight
// via the durable row alone: the holder polls for cancelling and the
// runner's between-nodes ctx check turns it into a stop, so the run never
// executes its remaining nodes only to be relabelled afterwards. SQLite has
// no LISTEN/NOTIFY, which is exactly why this floor must work there.
func TestRemoteCancelStopsTheHolderBetweenNodesOnSQLite(t *testing.T) {
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
	tenant := repository.TenantScope{ID: "tenant-remote-cancel"}

	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	if err := catalog.Register(multiprocessDefinition("test.fast", "test.fast")); err != nil {
		t.Fatalf("Register(fast) error = %v", err)
	}
	if err := catalog.Register(multiprocessDefinition("test.gate", "test.gate")); err != nil {
		t.Fatalf("Register(gate) error = %v", err)
	}
	if err := catalog.Register(multiprocessDefinition("test.never", "test.never")); err != nil {
		t.Fatalf("Register(never) error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	var mu sync.Mutex
	fastCalls, neverCalls := 0, 0
	if err := executors.Register("test.fast", &multiprocessEcho{mu: &mu, calls: &fastCalls}); err != nil {
		t.Fatalf("Register(fast executor) error = %v", err)
	}
	if err := executors.Register("test.never", &multiprocessEcho{mu: &mu, calls: &neverCalls}); err != nil {
		t.Fatalf("Register(never executor) error = %v", err)
	}
	gateStarted := make(chan struct{}, 1)
	gateSawCancel := make(chan bool, 1)
	if err := executors.Register("test.gate", engine.ExecutorFunc(func(execCtx context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
		select {
		case gateStarted <- struct{}{}:
		default:
		}
		select {
		case <-execCtx.Done():
			select {
			case gateSawCancel <- true:
			default:
			}
			return nil, execCtx.Err()
		case <-time.After(30 * time.Second):
			select {
			case gateSawCancel <- false:
			default:
			}
			items := input["main"]
			if items == nil {
				items = []workflow.Item{}
			}
			return workflow.NodeOutput{items}, nil
		}
	})); err != nil {
		t.Fatalf("Register(gate executor) error = %v", err)
	}
	_, err = repository.NewWorkflowStore(db.DB).SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_remote_cancel", Name: "Remote cancel",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "fast", Name: "Fast", Type: "test.fast", TypeVersion: workflow.V(1)},
			{ID: "gate", Name: "Gate", Type: "test.gate", TypeVersion: workflow.V(1)},
			{ID: "never", Name: "Never", Type: "test.never", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			{ID: "m-f", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "manual", Port: "main"}, Target: workflow.Endpoint{NodeID: "fast", Port: "main"}},
			{ID: "f-g", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "fast", Port: "main"}, Target: workflow.Endpoint{NodeID: "gate", Port: "main"}},
			{ID: "g-n", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "gate", Port: "main"}, Target: workflow.Endpoint{NodeID: "never", Port: "main"}},
		},
		Settings: map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	store := repository.NewExecutionStore(db.DB)
	queued, err := store.QueueManualLatest(ctx, tenant, "wf_remote_cancel", catalog, nil)
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: store, Catalog: catalog, Runner: engine.NewRunner(executors),
		WorkerID: "cancel-floor-1", DefaultTimeout: 30 * time.Second,
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
	select {
	case <-gateStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("the run never reached the gate: nothing in flight to cancel")
	}
	// From another process: straight at the store, bypassing this service's
	// active map and any notification, so only the status poll can stop it.
	if _, err := store.Cancel(ctx, tenant, queued.ID); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	terminalDeadline := time.Now().Add(10 * time.Second)
	var terminal execution.Record
	for {
		record, err := store.Get(ctx, tenant, queued.ID)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if record.Status == execution.StatusCancelled || record.Status == execution.StatusFailed || record.Status == execution.StatusSucceeded {
			terminal = record
			break
		}
		if time.Now().After(terminalDeadline) {
			t.Fatalf("execution still %q 10 s after a remote cancel: the holder did not stop in flight", record.Status)
		}
		select {
		case <-time.After(20 * time.Millisecond):
		}
	}
	if terminal.Status != execution.StatusCancelled {
		t.Errorf("execution status = %q, want cancelled: a run that finishes first is relabelled, not stopped", terminal.Status)
	}
	select {
	case saw := <-gateSawCancel:
		if !saw {
			t.Error("the gate ran to completion after cancel: the interrupt arrived too late to stop anything")
		}
	case <-time.After(5 * time.Second):
		t.Error("the gate never reported: it neither finished nor observed the cancel")
	}
	mu.Lock()
	defer mu.Unlock()
	if fastCalls != 1 {
		t.Errorf("fast calls = %d, want 1: only completed nodes may have run", fastCalls)
	}
	if neverCalls != 0 {
		t.Errorf("never calls = %d, want 0: nodes after the interrupt must not start", neverCalls)
	}
}

// The low-latency path: with the holder's poll stretched to 10 s, a
// cancellation still lands in well under the tick because the notice
// interrupts the lease holder directly. The poll stays as the fallback;
// this proves the channel, not the absence of the poll.
func TestCancelNoticeInterruptsTheHolderWithoutThePollIntervalOnPostgres(t *testing.T) {
	tenant := repository.TenantScope{ID: "drv-multiproc-cancel"}
	db, dsn := multiprocessPostgres(t, tenant)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	if err := catalog.Register(multiprocessDefinition("test.block", "test.block")); err != nil {
		t.Fatalf("Register(block) error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	blocked := make(chan struct{}, 1)
	if err := executors.Register("test.block", engine.ExecutorFunc(func(execCtx context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
		select {
		case blocked <- struct{}{}:
		default:
		}
		select {
		case <-execCtx.Done():
			return nil, execCtx.Err()
		case <-time.After(60 * time.Second):
			items := input["main"]
			if items == nil {
				items = []workflow.Item{}
			}
			return workflow.NodeOutput{items}, nil
		}
	})); err != nil {
		t.Fatalf("Register(block executor) error = %v", err)
	}
	_, err := repository.NewWorkflowStore(db.DB).SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "drv_wf_cancel", Name: "Cancel",
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
	relay := func(channel, payload string) error {
		return db.Exec("SELECT pg_notify(?, ?)", channel, payload).Error
	}
	holder, err := engine.NewService(engine.ServiceDeps{
		Executions: repository.NewExecutionStore(db.DB), Catalog: catalog, Runner: engine.NewRunner(executors),
		WorkerID: "multiproc-cancel-holder", DefaultTimeout: 60 * time.Second,
		PollInterval: 10 * time.Second, RelayPrefix: "", RelaySend: relay,
	})
	if err != nil {
		t.Fatalf("NewService(holder) error = %v", err)
	}
	canceller, err := engine.NewService(engine.ServiceDeps{
		Executions: repository.NewExecutionStore(db.DB), Catalog: catalog, Runner: engine.NewRunner(executors),
		WorkerID: "multiproc-cancel-other", DefaultTimeout: 60 * time.Second, RelaySend: relay,
	})
	if err != nil {
		t.Fatalf("NewService(canceller) error = %v", err)
	}
	if err := holder.Start(ctx, 1); err != nil {
		t.Fatalf("Start(holder) error = %v", err)
	}
	watchErrs := make(chan error, 8)
	go func() {
		_ = holder.WatchCancellations(ctx, dsn, "", func(err error) {
			select {
			case watchErrs <- err:
			default:
			}
		})
	}()
	time.Sleep(time.Second)

	store := repository.NewExecutionStore(db.DB)
	queued, err := store.QueueManualLatest(ctx, tenant, "drv_wf_cancel", catalog, json.RawMessage(`{"n":1}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	holder.Wake()
	select {
	case <-blocked:
	case <-time.After(10 * time.Second):
		t.Fatal("the holder never reached the blocking node: nothing in flight to cancel")
	}
	cancelledAt := time.Now()
	if _, err := canceller.Cancel(ctx, tenant, queued.ID); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		select {
		case err := <-watchErrs:
			t.Fatalf("WatchCancellations reported a drop: %v", err)
		default:
		}
		record, err := store.Get(ctx, tenant, queued.ID)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if record.Status == execution.StatusCancelled {
			if latency := time.Since(cancelledAt); latency >= 10*time.Second {
				t.Errorf("cancel latency = %v, want well inside the 10 s poll: the notice did not interrupt the holder", latency)
			}
			if record.LeaseOwner != "" {
				t.Errorf("lease owner = %q after cancel, want it released", record.LeaseOwner)
			}
			return
		}
		if record.Status == execution.StatusSucceeded || record.Status == execution.StatusFailed {
			t.Fatalf("execution %q after a mid-run cancel: the work finished instead of stopping", record.Status)
		}
		if time.Now().After(deadline) {
			t.Fatalf("execution still %q 8 s after cancel with a 10 s poll: the notice never reached the holder", record.Status)
		}
		select {
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// A worker killed mid-run (kill -9, power loss: no terminal write at all)
// leaves its lease held until expiry; another worker then reclaims the
// execution and completes it, the abandoned partial trace is cleared rather
// than doubled, and no write under the dead lease is accepted afterwards.
func TestAbandonedLeaseIsReclaimedWithoutDuplicateNodeRunsOnPostgres(t *testing.T) {
	tenant := repository.TenantScope{ID: "drv-multiproc-reclaim"}
	db, _ := multiprocessPostgres(t, tenant)
	ctx := context.Background()

	catalog, executors, mu, calls := multiprocessCatalog(t)
	multiprocessWorkflow(t, ctx, db, tenant, "drv_wf_reclaim")
	store := repository.NewExecutionStore(db.DB)
	queued, err := store.QueueManualLatest(ctx, tenant, "drv_wf_reclaim", catalog, json.RawMessage(`{"n":1}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	dead, _, claimed, err := store.ClaimNext(ctx, "dead-worker", time.Now().UTC().Add(2*time.Second))
	if err != nil || !claimed {
		t.Fatalf("ClaimNext(dead) = (%v, %v), want (true, nil)", claimed, err)
	}
	deadLease := dead.LeaseOwner
	if deadLease == "" {
		t.Fatal("the dead claim stamped no lease owner: nothing fences the successor")
	}
	now := time.Now().UTC()
	if _, err := store.CreateNodeRun(ctx, tenant, execution.NodeRun{
		TenantID: tenant.ID, ExecutionID: dead.ID, NodeID: "echo",
		Attempt: 1, RunIndex: 0, Sequence: 1, Status: execution.StatusSucceeded,
		Input: json.RawMessage(`{"main":[]}`), Output: json.RawMessage(`[[{"json":{}}]]`),
		StartedAt: now, FinishedAt: &now, LeaseOwner: deadLease,
	}); err != nil {
		t.Fatalf("CreateNodeRun(dead partial) error = %v", err)
	}
	// The process dies here: no terminal write, no lease release. What
	// remains is a running row with a lease that expires on its own clock.
	time.Sleep(3 * time.Second)

	reclaimer, err := engine.NewService(engine.ServiceDeps{
		Executions: repository.NewExecutionStore(db.DB), Catalog: catalog, Runner: engine.NewRunner(executors),
		WorkerID: "multiproc-reclaimer", DefaultTimeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewService(reclaimer) error = %v", err)
	}
	if worked, err := reclaimer.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(reclaimer) = (%v, %v), want (true, nil): the abandoned lease must be reclaimable", worked, err)
	}
	record, err := store.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if record.Status != execution.StatusSucceeded {
		t.Fatalf("execution status = %q, want succeeded: the reclaim must complete the work", record.Status)
	}
	mu.Lock()
	got := *calls
	mu.Unlock()
	if got != 1 {
		t.Errorf("echo calls = %d, want exactly 1: the graph must run once, not once per claimant", got)
	}
	seen := map[int]string{}
	echoRows := 0
	for _, run := range record.NodeRuns {
		if prev, dup := seen[run.Sequence]; dup {
			t.Errorf("sequence %d carried by %q and %q: the abandoned trace was doubled, not cleared", run.Sequence, prev, run.NodeID)
		}
		seen[run.Sequence] = run.NodeID
		if run.NodeID == "echo" {
			echoRows++
		}
	}
	if echoRows != 1 {
		t.Errorf("echo node runs = %d, want exactly 1: reclaim clears the partial trace first", echoRows)
	}
	if _, err := store.UpdateRuntime(ctx, tenant, execution.Record{
		ID: queued.ID, Status: execution.StatusSucceeded, LeaseOwner: deadLease,
		Output: json.RawMessage(`{}`), Error: json.RawMessage(`null`),
		FinishedAt: &now,
	}); err == nil {
		t.Error("UpdateRuntime(dead lease) succeeded, want it rejected: a dead worker must not write over its successor")
	}
	if _, err := store.CreateNodeRun(ctx, tenant, execution.NodeRun{
		TenantID: tenant.ID, ExecutionID: queued.ID, NodeID: "echo",
		Attempt: 1, RunIndex: 0, Sequence: 99, Status: execution.StatusSucceeded,
		Input: json.RawMessage(`{}`), Output: json.RawMessage(`{}`),
		StartedAt: now, FinishedAt: &now, LeaseOwner: deadLease,
	}); err == nil {
		t.Error("CreateNodeRun(dead lease) succeeded, want it rejected: the fence must hold for traces too")
	}
}
