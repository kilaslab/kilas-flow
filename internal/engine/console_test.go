package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/events"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// What a Code node printed is how its author finds out what it did, so it is
// kept on the node's own run: every console event it emits, merged in order,
// on success and on failure alike, and nothing at all for a node that printed
// nothing.
func TestConsoleLinesAreKeptWithTheNodeRun(t *testing.T) {
	t.Run("every console event the node emits is merged into its run", func(t *testing.T) {
		executors := consoleExecutors(t, func(request engine.Request, nodeID string) error {
			emitConsole(t, request, nodeID, engine.ConsoleDetail{Lines: []engine.ConsoleLine{
				{Level: "log", Text: "first", At: time.Now().UTC()},
				{Level: "warn", Text: "second", At: time.Now().UTC()},
			}})
			emitConsole(t, request, nodeID, engine.ConsoleDetail{Lines: []engine.ConsoleLine{
				{Level: "error", Text: "  third\n  indented", At: time.Now().UTC()},
			}, Truncated: true})
			return nil
		})
		var forwarded []engine.NodeEvent
		var mu sync.Mutex
		result, err := engine.NewRunner(executors).Run(context.Background(), consoleIR(t, nil), engine.Request{
			Events: func(event engine.NodeEvent) {
				mu.Lock()
				defer mu.Unlock()
				forwarded = append(forwarded, event)
			},
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		console := consoleOf(t, nodeRunNamed(t, result.NodeRuns, "code").Console)
		assertConsoleLines(t, console, []engine.ConsoleLine{
			{Level: "log", Text: "first"}, {Level: "warn", Text: "second"}, {Level: "error", Text: "  third\n  indented"},
		})
		if !console.Truncated {
			t.Error("console.truncated = false, want true: one of the node's events dropped output past its limit")
		}
		// Capturing the output must not cost the live feed its events.
		mu.Lock()
		defer mu.Unlock()
		if len(forwarded) != 2 {
			t.Fatalf("forwarded events = %d, want both console events passed on", len(forwarded))
		}
		for _, event := range forwarded {
			if event.Name != engine.ConsoleEventName || event.NodeID != "code" {
				t.Errorf("forwarded event = %s from %q, want %s from code", event.Name, event.NodeID, engine.ConsoleEventName)
			}
		}
	})

	t.Run("a node that fails keeps what it printed on its failure row", func(t *testing.T) {
		executors := consoleExecutors(t, func(request engine.Request, nodeID string) error {
			emitConsole(t, request, nodeID, engine.ConsoleDetail{Lines: []engine.ConsoleLine{
				{Level: "log", Text: "about to fail", At: time.Now().UTC()},
			}})
			return errors.New("the code threw")
		})
		result, err := engine.NewRunner(executors).Run(context.Background(),
			consoleIR(t, map[string]any{"onError": "stopWorkflow"}), engine.Request{})
		if err == nil {
			t.Fatal("Run() error = nil, want the node's failure to stop the workflow")
		}
		run := nodeRunNamed(t, result.NodeRuns, "code")
		if run.Error == nil {
			t.Fatal("code run error = nil, want the failure row")
		}
		assertConsoleLines(t, consoleOf(t, run.Console), []engine.ConsoleLine{{Level: "log", Text: "about to fail"}})
	})

	t.Run("a node that prints nothing has no console", func(t *testing.T) {
		executors := consoleExecutors(t, func(engine.Request, string) error { return nil })
		result, err := engine.NewRunner(executors).Run(context.Background(), consoleIR(t, nil), engine.Request{})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		for _, run := range result.NodeRuns {
			if run.Console != nil {
				t.Errorf("%s console = %s, want nil: the node printed nothing", run.NodeID, run.Console)
			}
		}
	})
}

// The live console event reaches an execution's subscribers under its own
// name, so the editor can listen for it, and the same lines land on the node's
// durable trace row, so an execution opened later still shows them.
func TestTheConsoleEventIsPublishedUnderItsOwnName(t *testing.T) {
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
	stored, err := repository.NewWorkflowStore(db.DB).SaveDraft(ctx, tenant, consoleDocument(nil))
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	catalog := consoleCatalog(t)
	executionStore := repository.NewExecutionStore(db.DB)
	queued, err := executionStore.QueueManualLatest(ctx, tenant, stored.ID, catalog, "", nil)
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	executors := consoleExecutors(t, func(request engine.Request, nodeID string) error {
		emitConsole(t, request, nodeID, engine.ConsoleDetail{Lines: []engine.ConsoleLine{
			{Level: "info", Text: "hello from the code node", At: time.Now().UTC()},
		}})
		return nil
	})
	broker := events.NewBroker(events.BrokerOptions{})
	subscription := broker.Subscribe(tenant.ID, queued.ID, 0)
	t.Cleanup(subscription.Close)
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: executionStore, Catalog: catalog, Runner: engine.NewRunner(executors),
		WorkerID: "test-worker", DefaultTimeout: 5 * time.Second, Events: broker,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", worked, err)
	}

	deadline := time.After(2 * time.Second)
	for published := false; !published; {
		select {
		case event := <-subscription.Events():
			if event.Type != events.Type(engine.ConsoleEventName) {
				continue
			}
			published = true
			if event.NodeID != "code" || event.ExecutionID != queued.ID {
				t.Errorf("console event from node %q of %q, want code of %q", event.NodeID, event.ExecutionID, queued.ID)
			}
			assertConsoleLines(t, consoleOf(t, event.Data), []engine.ConsoleLine{{Level: "info", Text: "hello from the code node"}})
		case <-deadline:
			t.Fatalf("no %s event was published", engine.ConsoleEventName)
		}
	}

	persisted, err := executionStore.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if persisted.Status != execution.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", persisted.Status)
	}
	for _, run := range persisted.NodeRuns {
		switch run.NodeID {
		case "code":
			assertConsoleLines(t, consoleOf(t, run.Console), []engine.ConsoleLine{{Level: "info", Text: "hello from the code node"}})
		default:
			if len(run.Console) != 0 {
				t.Errorf("%s persisted console = %s, want none", run.NodeID, run.Console)
			}
		}
	}
}

// A sub-workflow's trace is written by its own path once the call returns, and
// what its code printed has to survive that write as well: the child is an
// execution of its own, opened on its own detail page.
func TestASubWorkflowKeepsWhatItsCodePrinted(t *testing.T) {
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
	if err := catalog.Register(node.Definition{
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  "test.code", Version: workflow.V(1), DisplayName: "Code", Category: "Test",
		Inputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs:    []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		ExecutorID: "test.code",
	}); err != nil {
		t.Fatalf("Register(test.code): %v", err)
	}
	executors := consoleExecutors(t, func(request engine.Request, nodeID string) error {
		emitConsole(t, request, nodeID, engine.ConsoleDetail{Lines: []engine.ConsoleLine{
			{Level: "debug", Text: "inside the child", At: time.Now().UTC()},
		}})
		return nil
	})
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executionStore := repository.NewExecutionStore(db.DB)
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: executionStore, Catalog: catalog, Runner: engine.NewRunner(executors),
		WorkerID: "test-worker", DefaultTimeout: 30 * time.Second,
		SubworkflowTriggerType: nodes.ExecuteWorkflowTriggerType,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	setup := composition{
		executions: executionStore, workflows: repository.NewWorkflowStore(db.DB),
		catalog: catalog, service: service, tenant: repository.TenantScope{ID: "tenant-a"},
	}
	child := setup.activate(t, setup.tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, Name: "Printing child",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Called", Type: nodes.ExecuteWorkflowTriggerType, TypeVersion: workflow.V(1)},
			{ID: "code", Name: "Code", Type: "test.code", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "start", Port: "main"},
			Target: workflow.Endpoint{NodeID: "code", Port: "main"},
		}},
		Settings: map[string]any{},
	})
	parent := setup.activate(t, setup.tenant, caller("Parent", child, nil))

	record := setup.runManual(t, parent, `{"customer":"Ada"}`)
	if record.Status != execution.StatusSucceeded {
		t.Fatalf("parent status = %s, error = %s", record.Status, record.Error)
	}
	page, err := executionStore.List(ctx, setup.tenant, repository.ExecutionFilter{WorkflowID: child})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Records) != 1 {
		t.Fatalf("child executions = %d, want one", len(page.Records))
	}
	stored, err := executionStore.Get(ctx, setup.tenant, page.Records[0].ID)
	if err != nil {
		t.Fatalf("Get(child) error = %v", err)
	}
	for _, run := range stored.NodeRuns {
		if run.NodeID == "code" {
			assertConsoleLines(t, consoleOf(t, run.Console), []engine.ConsoleLine{{Level: "debug", Text: "inside the child"}})
			return
		}
	}
	t.Fatalf("the child recorded no run of its code node: %s", describeRuns(stored))
}

// consoleExecutors registers a trigger and a code node whose body is print.
func consoleExecutors(t *testing.T, print func(request engine.Request, nodeID string) error) *engine.Registry {
	t.Helper()
	executors := engine.NewRegistry()
	if err := executors.Register("test.trigger", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{{JSON: map[string]any{"seed": 1}}}}, nil
		})); err != nil {
		t.Fatalf("Register(trigger): %v", err)
	}
	if err := executors.Register("test.code", engine.ExecutorFunc(
		func(_ context.Context, node workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
			if err := print(request, node.ID); err != nil {
				return nil, err
			}
			return workflow.NodeOutput{input["main"]}, nil
		})); err != nil {
		t.Fatalf("Register(code): %v", err)
	}
	return executors
}

func emitConsole(t *testing.T, request engine.Request, nodeID string, detail engine.ConsoleDetail) {
	t.Helper()
	encoded, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal console detail: %v", err)
	}
	request.Events.Emit(engine.NodeEvent{NodeID: nodeID, Name: engine.ConsoleEventName, Detail: encoded})
}

func consoleCatalog(t *testing.T) *node.Registry {
	t.Helper()
	catalog := node.NewRegistry()
	for _, definition := range []node.Definition{
		{
			Group: []node.NodeGroup{node.GroupTransform},
			Type:  "test.trigger", Version: workflow.V(1), DisplayName: "Trigger", Category: "Test",
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.trigger",
		},
		{
			Group: []node.NodeGroup{node.GroupTransform},
			Type:  "test.code", Version: workflow.V(1), DisplayName: "Code", Category: "Test",
			Inputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs:    []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			ExecutorID: "test.code",
		},
	} {
		if err := catalog.Register(definition); err != nil {
			t.Fatalf("Register(%s): %v", definition.Type, err)
		}
	}
	return catalog
}

// consoleDocument is a trigger feeding one code node with the given settings.
func consoleDocument(settings map[string]any) workflow.Document {
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_console",
		Name:          "Console",
		Nodes: []workflow.Node{
			{ID: "trigger", Name: "Trigger", Type: "test.trigger", TypeVersion: workflow.V(1)},
			{ID: "code", Name: "Code", Type: "test.code", TypeVersion: workflow.V(1), Settings: settings},
		},
		Connections: []workflow.Connection{{
			ID: "trigger-code", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "trigger", Port: "main"},
			Target: workflow.Endpoint{NodeID: "code", Port: "main"},
		}},
		Settings: map[string]any{},
	}
}

func consoleIR(t *testing.T, settings map[string]any) workflow.IR {
	t.Helper()
	ir, err := workflow.Compile(consoleDocument(settings), consoleCatalog(t))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return ir
}

func nodeRunNamed(t *testing.T, runs []engine.NodeRun, nodeID string) engine.NodeRun {
	t.Helper()
	for _, run := range runs {
		if run.NodeID == nodeID {
			return run
		}
	}
	t.Fatalf("no run of node %q among %d runs", nodeID, len(runs))
	return engine.NodeRun{}
}

func consoleOf(t *testing.T, raw json.RawMessage) engine.ConsoleDetail {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("console = nil, want the lines the node printed")
	}
	var detail engine.ConsoleDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("console %s is not a console detail: %v", raw, err)
	}
	return detail
}

func assertConsoleLines(t *testing.T, got engine.ConsoleDetail, want []engine.ConsoleLine) {
	t.Helper()
	if len(got.Lines) != len(want) {
		t.Fatalf("console lines = %+v, want %d lines", got.Lines, len(want))
	}
	for index, line := range want {
		if got.Lines[index].Level != line.Level || got.Lines[index].Text != line.Text {
			t.Errorf("line %d = %s %q, want %s %q", index, got.Lines[index].Level, got.Lines[index].Text, line.Level, line.Text)
		}
		if got.Lines[index].At.IsZero() {
			t.Errorf("line %d lost its time", index)
		}
	}
}
