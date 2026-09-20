package engine_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
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
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
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
	queued, err := executionStore.QueueManualLatest(ctx, tenant, stored.ID, catalog, "", json.RawMessage(`{"customer":"Ada"}`))
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
	// Asserted by content rather than by exact bytes: items now carry
	// provenance, and pinning the serialization would make every future field
	// on an item a failing test in an unrelated package.
	var setInput map[string][]workflow.Item
	if err := json.Unmarshal(persisted.NodeRuns[1].Input, &setInput); err != nil {
		t.Fatalf("decode set input: %v (%s)", err, persisted.NodeRuns[1].Input)
	}
	if len(setInput["main"]) != 1 || setInput["main"][0].JSON["customer"] != "Ada" {
		t.Errorf("set input = %s, want the trigger's item", persisted.NodeRuns[1].Input)
	}
	var setOutput [][]workflow.Item
	if err := json.Unmarshal(persisted.NodeRuns[1].Output, &setOutput); err != nil {
		t.Fatalf("decode set output: %v (%s)", err, persisted.NodeRuns[1].Output)
	}
	if len(setOutput) != 1 || len(setOutput[0]) != 1 {
		t.Fatalf("set output = %s, want one item on one port", persisted.NodeRuns[1].Output)
	}
	if setOutput[0][0].JSON["status"] != "ready" || setOutput[0][0].JSON["customer"] != "Ada" {
		t.Errorf("set output = %s, want the assignment applied to the trigger's item", persisted.NodeRuns[1].Output)
	}
	// Provenance survives the round trip through durable storage, which is what
	// makes a lookup from a later node possible at all.
	if paired := setOutput[0][0].Paired; paired == nil || paired.SourceNodeID != "manual" {
		t.Errorf("set output provenance = %#v, want it to name the trigger it descends from", setOutput[0][0].Paired)
	}

	// A process may die after it has durably written part of a trace but before
	// the terminal execution update. The next worker must clear that incomplete
	// attempt and complete the pinned workflow rather than becoming stuck on the
	// node-run uniqueness constraints.
	abandoned, err := executionStore.QueueManualLatest(ctx, tenant, stored.ID, catalog, "", nil)
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

// TestAManualRunStartsOnlyFromTheTriggerItChose is the whole point of the
// trigger selection, end to end: the choice the API recorded reaches the runner
// and the run executes one trigger's branch.
//
// Without it a manual run of a two-trigger workflow seeds every root with the
// same item, so the shared tail writes once per trigger and every
// trigger-shaped expression reads the wrong payload — the duplicate side
// effects the live repro caught.
func TestAManualRunStartsOnlyFromTheTriggerItChose(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "kilasflow.db")}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	link := func(id, source, target string) workflow.Connection {
		return workflow.Connection{ID: id, Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: source, Port: "main"},
			Target: workflow.Endpoint{NodeID: target, Port: "main"}}
	}
	tenant := repository.TenantScope{ID: "tenant-a"}
	stored, err := repository.NewWorkflowStore(db.DB).SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_trigger_choice",
		Name:          "Webhook and schedule",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "hook", Name: "Webhook", Type: "kilasflow.webhook", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"path": "orders", "httpMethod": "POST"}},
			{ID: "manual-only", Name: "Manual only", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"via": "manual"}}},
			{ID: "hook-only", Name: "Hook only", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"via": "webhook"}}},
			{ID: "shared", Name: "Shared", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"seen": "yes"}}},
		},
		Connections: []workflow.Connection{
			link("c1", "manual", "manual-only"),
			link("c2", "hook", "hook-only"),
			link("c3", "manual", "shared"),
			link("c4", "hook", "shared"),
		},
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
	queued, err := executionStore.QueueManualLatest(ctx, tenant, stored.ID, catalog, "hook", json.RawMessage(`{"order":"A-1"}`))
	if err != nil {
		t.Fatalf("QueueManualLatest(chosen trigger) error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: executionStore, Catalog: catalog, Runner: engine.NewRunner(executors),
		WorkerID: "test-worker", DefaultTimeout: time.Second,
	})
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
	if got, want := persisted.Status, execution.StatusSucceeded; got != want {
		t.Fatalf("status = %q, want %q (error %s)", got, want, persisted.Error)
	}

	ran := map[string]int{}
	for _, run := range persisted.NodeRuns {
		ran[run.NodeID]++
	}
	for _, want := range []string{"hook", "hook-only", "shared"} {
		if ran[want] != 1 {
			t.Errorf("node %q ran %d times, want once", want, ran[want])
		}
	}
	for _, unwanted := range []string{"manual", "manual-only"} {
		if ran[unwanted] != 0 {
			t.Errorf("node %q ran %d times on a run that chose the webhook trigger, want never", unwanted, ran[unwanted])
		}
	}
	// The shared tail ran once, fed by the chosen trigger's item alone.
	var sharedInput map[string][]workflow.Item
	for _, run := range persisted.NodeRuns {
		if run.NodeID == "shared" {
			if err := json.Unmarshal(run.Input, &sharedInput); err != nil {
				t.Fatalf("decode shared input: %v (%s)", err, run.Input)
			}
		}
	}
	if len(sharedInput["main"]) != 1 || sharedInput["main"][0].JSON["order"] != "A-1" {
		t.Errorf("shared input = %#v, want the single item the webhook run carried", sharedInput)
	}
}

func TestServicePersistsFailedNodeRunWhenItsConfiguredTimeoutExpires(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "kilasflow.db")}, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	if err := catalog.Register(node.Definition{
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  "kilasflow.test.slow", Version: workflow.V(1), DisplayName: "Slow", Category: "Test",
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
	queued, err := executionStore.QueueManualLatest(ctx, tenant, stored.ID, catalog, "", nil)
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
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
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
	queued, err := executionStore.QueueManualLatest(ctx, tenant, stored.ID, catalog, "", nil)
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
	abandoned, err := executionStore.QueueManualLatest(ctx, tenant, stored.ID, catalog, "", nil)
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
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	if err := catalog.Register(node.Definition{
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  "kilasflow.test.block", Version: workflow.V(1), DisplayName: "Block", Category: "Test",
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
	queued, err := executionStore.QueueManualLatest(ctx, tenant, stored.ID, catalog, "", nil)
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

// lightPollStore counts the two ways a cancellation can be noticed: the whole
// record, and the one-column status read.
type lightPollStore struct {
	*repository.GORMExecutionStore
	states atomic.Int64
	gets   atomic.Int64
}

func (store *lightPollStore) Get(ctx context.Context, tenant repository.TenantScope, executionID string) (execution.Record, error) {
	store.gets.Add(1)
	return store.GORMExecutionStore.Get(ctx, tenant, executionID)
}

func (store *lightPollStore) ExecutionState(ctx context.Context, tenant repository.TenantScope, executionID string) (execution.Status, bool, error) {
	store.states.Add(1)
	return store.GORMExecutionStore.ExecutionState(ctx, tenant, executionID)
}

// A cancellation requested by another process reaches a running execution
// through the status read alone.
//
// The poll used to load the whole execution record ten times a second — every
// node-run row and every payload this execution had written, for a run that
// could be sitting on megabytes of them — to answer one question about one
// small column. This test fails if the poll goes back to the full read: the
// decorated store fails the assertion when Get is touched while a run is in
// flight, and the cancellation is written straight through the store so
// nothing but the poll can deliver it.
func TestCancellationReachesARunningExecutionThroughTheStatusRead(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "kilasflow.db")}, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	if err := catalog.Register(node.Definition{
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  "kilasflow.test.cancellable", Version: workflow.V(1), DisplayName: "Cancellable", Category: "Test",
		Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.cancellable",
	}); err != nil {
		t.Fatalf("Register(cancellable) error = %v", err)
	}
	tenant := repository.TenantScope{ID: "tenant-poll"}
	stored, err := repository.NewWorkflowStore(db.DB).SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_poll", Name: "Poll",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "wait", Name: "Cancellable", Type: "kilasflow.test.cancellable", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{{ID: "manual-wait", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "manual", Port: "main"}, Target: workflow.Endpoint{NodeID: "wait", Port: "main"}}},
		Settings:    map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	inner := repository.NewExecutionStore(db.DB)
	queued, err := inner.QueueManualLatest(ctx, tenant, stored.ID, catalog, "", nil)
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	started := make(chan struct{})
	if err := executors.Register("test.cancellable", engine.ExecutorFunc(func(ctx context.Context, _ workflow.IRNode, _ workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})); err != nil {
		t.Fatalf("Register(cancellable executor) error = %v", err)
	}
	store := &lightPollStore{GORMExecutionStore: inner}
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: store, Catalog: catalog, Runner: engine.NewRunner(executors),
		WorkerID: "test-worker", DefaultTimeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := service.RunOnce(ctx)
		done <- err
	}()
	<-started
	// Written through the store, not service.Cancel: the in-process shortcut
	// must not be what delivers this.
	if _, err := inner.Cancel(ctx, tenant, queued.ID); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if gets := store.gets.Load(); gets != 0 {
		t.Errorf("the cancellation poll loaded the whole execution record %d time(s); it must read only the status", gets)
	}
	if states := store.states.Load(); states == 0 {
		t.Error("the cancellation poll never made a status read")
	}
	final, err := inner.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if final.Status != execution.StatusCancelled {
		t.Errorf("execution status = %q, want %q", final.Status, execution.StatusCancelled)
	}
}

// A workflow that asks for a longer run gets one, and the instance default is
// only what applies when it asks for nothing.
//
// Every execution used to be killed at execution.default_timeout whatever the
// workflow said, so an imported workflow declaring five minutes died at sixty
// seconds — and the import dropped the setting, so nobody could see why. This
// asserts the setting is what changed: the same node that fails under the
// instance default succeeds under the workflow's own budget.
func TestAWorkflowKeepsTheRunBudgetItAsksFor(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "kilasflow.db")}, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	if err := catalog.Register(node.Definition{
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  "kilasflow.test.slow", Version: workflow.V(1), DisplayName: "Slow", Category: "Test",
		Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.slow",
	}); err != nil {
		t.Fatalf("Register(slow) error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	if err := executors.Register("test.slow", engine.ExecutorFunc(func(ctx context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
		return workflow.NodeOutput{input["main"]}, nil
	})); err != nil {
		t.Fatalf("Register(slow executor) error = %v", err)
	}
	tenant := repository.TenantScope{ID: "tenant-budget"}
	workflowStore := repository.NewWorkflowStore(db.DB)
	save := func(id string, settings map[string]any) workflow.StoredWorkflow {
		t.Helper()
		stored, err := workflowStore.SaveDraft(ctx, tenant, workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            id, Name: id,
			Nodes: []workflow.Node{
				{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
				{ID: "slow", Name: "Slow", Type: "kilasflow.test.slow", TypeVersion: workflow.V(1)},
			},
			Connections: []workflow.Connection{{ID: "manual-slow", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "manual", Port: "main"}, Target: workflow.Endpoint{NodeID: "slow", Port: "main"}}},
			Settings:    settings,
		})
		if err != nil {
			t.Fatalf("SaveDraft(%s) error = %v", id, err)
		}
		return stored
	}
	store := repository.NewExecutionStore(db.DB)
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: store, Catalog: catalog, Runner: engine.NewRunner(executors),
		WorkerID: "test-worker", DefaultTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	// The instance default still binds a workflow that asks for nothing: that
	// is the behaviour the setting is measured against.
	defaulted := save("wf_budget_default", map[string]any{})
	queuedDefault, err := store.QueueManualLatest(ctx, tenant, defaulted.ID, catalog, "", nil)
	if err != nil {
		t.Fatalf("QueueManualLatest(default) error = %v", err)
	}
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(default) = (%v, %v), want (true, nil)", worked, err)
	}
	if record, err := store.Get(ctx, tenant, queuedDefault.ID); err != nil {
		t.Fatalf("Get(default) error = %v", err)
	} else if record.Status != execution.StatusFailed {
		t.Errorf("status without a workflow timeout = %q, want failed at the instance default", record.Status)
	}

	// Five seconds asked for, four hundred milliseconds used.
	budgeted := save("wf_budget_asked", map[string]any{engine.ExecutionTimeoutSetting: float64(5)})
	queuedAsked, err := store.QueueManualLatest(ctx, tenant, budgeted.ID, catalog, "", nil)
	if err != nil {
		t.Fatalf("QueueManualLatest(asked) error = %v", err)
	}
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(asked) = (%v, %v), want (true, nil)", worked, err)
	}
	if record, err := store.Get(ctx, tenant, queuedAsked.ID); err != nil {
		t.Fatalf("Get(asked) error = %v", err)
	} else if record.Status != execution.StatusSucceeded {
		t.Errorf("status with settings.executionTimeout = %q, want succeeded (error %s)", record.Status, record.Error)
	}

	// n8n's -1 means no timeout at all, which is also the only way to run a
	// workflow whose work is genuinely open-ended.
	unbounded := save("wf_budget_none", map[string]any{engine.ExecutionTimeoutSetting: float64(-1)})
	queuedNone, err := store.QueueManualLatest(ctx, tenant, unbounded.ID, catalog, "", nil)
	if err != nil {
		t.Fatalf("QueueManualLatest(unbounded) error = %v", err)
	}
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(unbounded) = (%v, %v), want (true, nil)", worked, err)
	}
	if record, err := store.Get(ctx, tenant, queuedNone.ID); err != nil {
		t.Fatalf("Get(unbounded) error = %v", err)
	} else if record.Status != execution.StatusSucceeded {
		t.Errorf("status with a -1 timeout = %q, want succeeded (error %s)", record.Status, record.Error)
	}

	// The instance ceiling still wins over what the workflow asks for.
	capped, err := engine.NewService(engine.ServiceDeps{
		Executions: store, Catalog: catalog, Runner: engine.NewRunner(executors),
		WorkerID: "test-worker-capped", DefaultTimeout: 100 * time.Millisecond, MaxTimeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewService(capped) error = %v", err)
	}
	greedy := save("wf_budget_greedy", map[string]any{engine.ExecutionTimeoutSetting: float64(3600)})
	queuedGreedy, err := store.QueueManualLatest(ctx, tenant, greedy.ID, catalog, "", nil)
	if err != nil {
		t.Fatalf("QueueManualLatest(greedy) error = %v", err)
	}
	if worked, err := capped.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(greedy) = (%v, %v), want (true, nil)", worked, err)
	}
	if record, err := store.Get(ctx, tenant, queuedGreedy.ID); err != nil {
		t.Fatalf("Get(greedy) error = %v", err)
	} else if record.Status != execution.StatusFailed {
		t.Errorf("status with an hour asked for under a 200 ms ceiling = %q, want failed", record.Status)
	}
}
