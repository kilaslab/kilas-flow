package engine_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
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

func TestServiceRunOncePersistsCompletedManualSetExecution(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "kilasflow.db")}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, repository.Models()...); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	tenant := repository.TenantScope{ID: "tenant-a"}
	workflowStore := repository.NewWorkflowStore(db.DB)
	stored, err := workflowStore.SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_023",
		Name:          "Persisted manual set",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: workflow.V(1), Parameters: map[string]any{"assignments": map[string]any{"status": "ready"}}},
		},
		Connections: []workflow.Connection{{
			ID: "manual-set", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "set", Port: "main"},
		}},
		Settings: map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	executionStore := repository.NewExecutionStore(db.DB)
	queued, err := executionStore.QueueManualLatest(ctx, tenant, stored.ID, catalog, json.RawMessage(`{"customer":"Ada"}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	service, err := engine.NewService(engine.ServiceDeps{
		Executions:     executionStore,
		Catalog:        catalog,
		Runner:         engine.NewRunner(executors),
		WorkerID:       "test-worker",
		DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	worked, err := service.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !worked {
		t.Fatal("RunOnce() did not claim the queued execution")
	}
	persisted, err := executionStore.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got, want := persisted.Status, execution.StatusSucceeded; got != want {
		t.Errorf("execution status = %q, want %q", got, want)
	}
	if persisted.FinishedAt == nil {
		t.Error("execution finishedAt is nil")
	}
	if got, want := len(persisted.NodeRuns), 2; got != want {
		t.Fatalf("node run count = %d, want %d", got, want)
	}
	if persisted.NodeRuns[1].FinishedAt == nil || persisted.FinishedAt.Before(*persisted.NodeRuns[1].FinishedAt) {
		t.Errorf("execution finishedAt = %v, want it at or after final node finishedAt %v", persisted.FinishedAt, persisted.NodeRuns[1].FinishedAt)
	}
	if got, want := persisted.NodeRuns[1].Input, json.RawMessage(`{"main":[{"json":{"customer":"Ada"}}]}`); string(got) != string(want) {
		t.Errorf("set input = %s, want %s", got, want)
	}
	if got, want := persisted.NodeRuns[1].Output, json.RawMessage(`[[{"json":{"customer":"Ada","status":"ready"}}]]`); string(got) != string(want) {
		t.Errorf("set output = %s, want %s", got, want)
	}

	// A process may die after it has durably written part of a trace but before
	// the terminal execution update. The next worker must clear that incomplete
	// attempt and complete the pinned workflow rather than becoming stuck on the
	// node-run uniqueness constraints.
	abandoned, err := executionStore.QueueManualLatest(ctx, tenant, stored.ID, catalog, nil)
	if err != nil {
		t.Fatalf("QueueManualLatest(abandoned) error = %v", err)
	}
	lostClaim, _, claimed, err := executionStore.ClaimNext(ctx, "lost-worker", time.Now().UTC().Add(-time.Second))
	if err != nil || !claimed {
		t.Fatalf("ClaimNext(abandoned) = (%v, %v), want (true, nil)", claimed, err)
	}
	now := time.Now().UTC()
	if _, err := executionStore.CreateNodeRun(ctx, tenant, execution.NodeRun{
		TenantID: tenant.ID, ExecutionID: abandoned.ID, NodeID: "manual", Attempt: 1, Sequence: 1,
		Status: execution.StatusSucceeded, Input: json.RawMessage(`{}`), Output: json.RawMessage(`[[{}]]`), StartedAt: now, FinishedAt: &now, LeaseOwner: lostClaim.LeaseOwner,
	}); err != nil {
		t.Fatalf("CreateNodeRun(partial) error = %v", err)
	}
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(recovery) = (%v, %v), want (true, nil)", worked, err)
	}
	recovered, err := executionStore.Get(ctx, tenant, abandoned.ID)
	if err != nil {
		t.Fatalf("Get(recovered) error = %v", err)
	}
	if got, want := recovered.Status, execution.StatusSucceeded; got != want {
		t.Errorf("recovered status = %q, want %q", got, want)
	}
	if got, want := len(recovered.NodeRuns), 2; got != want {
		t.Errorf("recovered node runs = %d, want %d without the partial attempt", got, want)
	}
}

func TestServicePersistsFailedNodeRunWhenItsConfiguredTimeoutExpires(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "kilasflow.db")}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, repository.Models()...); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	if err := catalog.Register(node.Definition{
		Type: "kilasflow.test.slow", Version: workflow.V(1), DisplayName: "Slow", Category: "Test",
		Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.slow",
	}); err != nil {
		t.Fatalf("Register(slow) error = %v", err)
	}
	tenant := repository.TenantScope{ID: "tenant-a"}
	workflowStore := repository.NewWorkflowStore(db.DB)
	stored, err := workflowStore.SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_024",
		Name:          "Slow workflow",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "slow", Name: "Slow", Type: "kilasflow.test.slow", TypeVersion: workflow.V(1), Settings: map[string]any{"timeoutSeconds": 0.01}},
		},
		Connections: []workflow.Connection{{
			ID: "manual-slow", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "slow", Port: "main"},
		}},
		Settings: map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	executionStore := repository.NewExecutionStore(db.DB)
	queued, err := executionStore.QueueManualLatest(ctx, tenant, stored.ID, catalog, nil)
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	if err := executors.Register("test.slow", engine.ExecutorFunc(func(ctx context.Context, _ workflow.IRNode, _ workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})); err != nil {
		t.Fatalf("Register(slow executor) error = %v", err)
	}
	service, err := engine.NewService(engine.ServiceDeps{Executions: executionStore, Catalog: catalog, Runner: engine.NewRunner(executors), WorkerID: "test-worker", DefaultTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", worked, err)
	}
	persisted, err := executionStore.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got, want := persisted.Status, execution.StatusFailed; got != want {
		t.Errorf("execution status = %q, want %q", got, want)
	}
	if got, want := len(persisted.NodeRuns), 2; got != want {
		t.Fatalf("node run count = %d, want %d", got, want)
	}
	if got, want := persisted.NodeRuns[1].Status, execution.StatusFailed; got != want {
		t.Errorf("slow node status = %q, want %q", got, want)
	}
	if got, want := string(persisted.NodeRuns[1].Error), `{"code":"node.timeout"}`; !jsonContains(got, want) {
		t.Errorf("slow node error = %s, want code %s", got, want)
	}
}

func TestServiceCancelsQueuedExecutionBeforeAWorkerClaimsIt(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "kilasflow.db")}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, repository.Models()...); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	tenant := repository.TenantScope{ID: "tenant-a"}
	stored, err := repository.NewWorkflowStore(db.DB).SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_025",
		Name:          "Cancellable workflow",
		Nodes:         []workflow.Node{{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)}},
		Connections:   []workflow.Connection{},
		Settings:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	executionStore := repository.NewExecutionStore(db.DB)
	queued, err := executionStore.QueueManualLatest(ctx, tenant, stored.ID, catalog, nil)
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	service, err := engine.NewService(engine.ServiceDeps{Executions: executionStore, Catalog: catalog, Runner: engine.NewRunner(executors), WorkerID: "test-worker", DefaultTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	cancelled, err := service.Cancel(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if got, want := cancelled.Status, execution.StatusCancelled; got != want {
		t.Errorf("cancel result status = %q, want %q", got, want)
	}
	if worked, err := service.RunOnce(ctx); err != nil || worked {
		t.Fatalf("RunOnce() after cancellation = (%v, %v), want (false, nil)", worked, err)
	}
	persisted, err := executionStore.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got, want := persisted.Status, execution.StatusCancelled; got != want {
		t.Errorf("persisted status = %q, want %q", got, want)
	}
	if persisted.FinishedAt == nil {
		t.Error("cancelled execution finishedAt is nil")
	}

	// A cancellation accepted after the owner has claimed work must survive a
	// process crash. The recovery worker finalizes the expired cancelling lease
	// without re-running the workflow.
	abandoned, err := executionStore.QueueManualLatest(ctx, tenant, stored.ID, catalog, nil)
	if err != nil {
		t.Fatalf("QueueManualLatest(cancelling recovery) error = %v", err)
	}
	if _, _, claimed, err := executionStore.ClaimNext(ctx, "lost-worker", time.Now().UTC().Add(-time.Second)); err != nil || !claimed {
		t.Fatalf("ClaimNext(cancelling recovery) = (%v, %v), want (true, nil)", claimed, err)
	}
	if cancelled, err := service.Cancel(ctx, tenant, abandoned.ID); err != nil || cancelled.Status != execution.StatusCancelling {
		t.Fatalf("Cancel(cancelling recovery) = (%q, %v), want (cancelling, nil)", cancelled.Status, err)
	}
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(cancelling recovery) = (%v, %v), want (true, nil)", worked, err)
	}
	recovered, err := executionStore.Get(ctx, tenant, abandoned.ID)
	if err != nil {
		t.Fatalf("Get(cancelling recovery) error = %v", err)
	}
	if got, want := recovered.Status, execution.StatusCancelled; got != want {
		t.Errorf("recovered cancelling status = %q, want %q", got, want)
	}
}

func TestServiceCancelsAnActiveExecutionAndPersistsCancelledNodeRun(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "kilasflow.db")}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, repository.Models()...); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	if err := catalog.Register(node.Definition{
		Type: "kilasflow.test.block", Version: workflow.V(1), DisplayName: "Block", Category: "Test",
		Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.block",
	}); err != nil {
		t.Fatalf("Register(block) error = %v", err)
	}
	tenant := repository.TenantScope{ID: "tenant-a"}
	stored, err := repository.NewWorkflowStore(db.DB).SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_026",
		Name:          "Active cancellation",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "block", Name: "Block", Type: "kilasflow.test.block", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{{ID: "manual-block", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "manual", Port: "main"}, Target: workflow.Endpoint{NodeID: "block", Port: "main"}}},
		Settings:    map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	executionStore := repository.NewExecutionStore(db.DB)
	queued, err := executionStore.QueueManualLatest(ctx, tenant, stored.ID, catalog, nil)
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	if err := executors.Register("test.block", engine.ExecutorFunc(func(ctx context.Context, _ workflow.IRNode, _ workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})); err != nil {
		t.Fatalf("Register(block executor) error = %v", err)
	}
	service, err := engine.NewService(engine.ServiceDeps{Executions: executionStore, Catalog: catalog, Runner: engine.NewRunner(executors), WorkerID: "test-worker", DefaultTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := service.RunOnce(ctx)
		done <- err
	}()
	awaitExecutionStatus(t, executionStore, tenant, queued.ID, execution.StatusRunning)
	if _, err := service.Cancel(ctx, tenant, queued.ID); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	persisted, err := executionStore.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got, want := persisted.Status, execution.StatusCancelled; got != want {
		t.Errorf("execution status = %q, want %q", got, want)
	}
	if got, want := persisted.NodeRuns[1].Status, execution.StatusCancelled; got != want {
		t.Errorf("block node status = %q, want %q", got, want)
	}
}

func awaitExecutionStatus(t *testing.T, store *repository.GORMExecutionStore, tenant repository.TenantScope, executionID string, want execution.Status) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		record, err := store.Get(context.Background(), tenant, executionID)
		if err == nil && record.Status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	record, err := store.Get(context.Background(), tenant, executionID)
	t.Fatalf("execution status = (%q, %v), want %q before timeout", record.Status, err, want)
}

func jsonContains(payload, fragment string) bool {
	var value map[string]any
	var wanted map[string]any
	return json.Unmarshal([]byte(payload), &value) == nil && json.Unmarshal([]byte(fragment), &wanted) == nil && value["code"] == wanted["code"]
}
