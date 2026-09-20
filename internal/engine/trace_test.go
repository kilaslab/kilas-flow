package engine_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/datastore"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/events"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// The trace contract pins the node type the projector recognises to the
// type the datastore node registers. A rename on either side without the
// other silently restores cells — and redaction markers — into the trace.
func TestDatastoreTraceContractMatchesNodeType(t *testing.T) {
	if datastore.NodeType != nodes.DatastoreNodeType {
		t.Errorf("datastore.NodeType = %q, nodes.DatastoreNodeType = %q: the projector would miss the node's runs",
			datastore.NodeType, nodes.DatastoreNodeType)
	}
}

// The runner hands full rows to the next node in memory: redaction and
// projection both live downstream of here, at the durable write and the
// live publish, so live values are never redacted in transit.
func TestRunnerPassesFullDatastoreRowsDownstream(t *testing.T) {
	catalog := traceCatalog(t)
	executors := engine.NewRegistry()
	cells := map[string]any{"id": 1, "api_key": "live-secret", "name": "cookie", "value": "chocolate chip"}
	if err := executors.Register("test.trigger", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{{JSON: map[string]any{"seed": 1}}}}, nil
		})); err != nil {
		t.Fatalf("Register(trigger): %v", err)
	}
	if err := executors.Register("test.ds", engine.ExecutorFunc(
		func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{{JSON: cells}}}, nil
		})); err != nil {
		t.Fatalf("Register(ds): %v", err)
	}
	if err := executors.Register("test.after", engine.ExecutorFunc(
		func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{input["main"]}, nil
		})); err != nil {
		t.Fatalf("Register(after): %v", err)
	}

	ir, err := workflow.Compile(traceEchoDocument(), catalog)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.NodeRuns) != 3 {
		t.Fatalf("node runs = %d, want trigger, datastore and echo", len(result.NodeRuns))
	}
	// The datastore run and the echo of it both carry the live cells: the
	// runner neither redacts nor projects.
	for _, run := range result.NodeRuns {
		if run.NodeID == "trigger" {
			continue
		}
		raw, err := json.Marshal(run.Output)
		if err != nil {
			t.Fatalf("marshal %s output: %v", run.NodeID, err)
		}
		for _, cell := range []string{"live-secret", "chocolate chip"} {
			if !strings.Contains(string(raw), cell) {
				t.Errorf("in-memory %s output = %s, want the live cell %q", run.NodeID, raw, cell)
			}
		}
	}
}

// The durable write and the live event carry the summary instead: counts
// and row identifiers, with neither a cell nor a redaction marker. Any
// other node's output passes through byte-identical.
func TestServiceProjectsDatastoreOutputFromTrace(t *testing.T) {
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
	stored, err := workflowStore.SaveDraft(ctx, tenant, traceDocument())
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	catalog := traceCatalog(t)
	executionStore := repository.NewExecutionStore(db.DB)
	queued, err := executionStore.QueueManualLatest(ctx, tenant, stored.ID, catalog, "", nil)
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := executors.Register("test.trigger", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{{JSON: map[string]any{"customer": "Ada"}}}}, nil
		})); err != nil {
		t.Fatalf("Register(trigger): %v", err)
	}
	if err := executors.Register("test.ds", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{{JSON: map[string]any{
				"id": 1, "api_key": "live-secret", "name": "cookie", "value": "chocolate chip",
			}}}}, nil
		})); err != nil {
		t.Fatalf("Register(ds): %v", err)
	}
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

	persisted, err := executionStore.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if persisted.Status != execution.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", persisted.Status)
	}
	outputs := map[string]json.RawMessage{}
	for _, run := range persisted.NodeRuns {
		outputs[run.NodeID] = run.Output
	}
	trigger, ok := outputs["trigger"]
	if !ok || !strings.Contains(string(trigger), "Ada") {
		t.Errorf("trigger output = %s, want the untouched items", trigger)
	}
	ds, ok := outputs["ds"]
	if !ok {
		t.Fatalf("no datastore node run persisted")
	}
	var summary struct {
		Datastore struct {
			Rows int64   `json:"rows"`
			IDs  []int64 `json:"ids"`
		} `json:"datastore"`
	}
	if err := json.Unmarshal(ds, &summary); err != nil {
		t.Fatalf("datastore output %s is not the summary envelope: %v", ds, err)
	}
	if summary.Datastore.Rows != 1 || len(summary.Datastore.IDs) != 1 || summary.Datastore.IDs[0] != 1 {
		t.Errorf("datastore summary = %s, want rows 1 ids [1]", ds)
	}
	for _, leaked := range []string{"live-secret", "chocolate chip", execution.RedactedValue} {
		if strings.Contains(string(ds), leaked) {
			t.Errorf("datastore trace %s contains %q", ds, leaked)
		}
	}

	// The live stream carries the same summary: subscribers never see a
	// cell, and redaction never sees one to rewrite.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-subscription.Events():
			if event.NodeID != "ds" {
				continue
			}
			// node.started is published before the node runs, so it carries no
			// data by design: the event this asserts on is the one that reports
			// what the node produced.
			if event.Type == events.NodeStarted {
				continue
			}
			if string(event.Data) != string(ds) {
				t.Errorf("live event data = %s, want the persisted summary %s", event.Data, ds)
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for the datastore node event")
		}
	}
}

func traceCatalog(t *testing.T) *node.Registry {
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
			Type:  datastore.NodeType, Version: workflow.V(1), DisplayName: "Datastore", Category: "Test",
			Inputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs:    []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			ExecutorID: "test.ds",
		},
		{
			Group: []node.NodeGroup{node.GroupTransform},
			Type:  "test.after", Version: workflow.V(1), DisplayName: "After", Category: "Test",
			Inputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs:    []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			ExecutorID: "test.after",
		},
	} {
		if err := catalog.Register(definition); err != nil {
			t.Fatalf("Register(%s): %v", definition.Type, err)
		}
	}
	return catalog
}

func traceDocument() workflow.Document {
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_trace",
		Name:          "Datastore trace",
		Nodes: []workflow.Node{
			{ID: "trigger", Name: "Trigger", Type: "test.trigger", TypeVersion: workflow.V(1)},
			{ID: "ds", Name: "Datastore", Type: datastore.NodeType, TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{{
			ID: "trigger-ds", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "trigger", Port: "main"},
			Target: workflow.Endpoint{NodeID: "ds", Port: "main"},
		}},
		Settings: map[string]any{},
	}
}

// traceEchoDocument extends the trace document with a downstream echo: it
// proves the live cells travel past the datastore node in memory.
func traceEchoDocument() workflow.Document {
	document := traceDocument()
	document.ID = "wf_trace_echo"
	document.Nodes = append(document.Nodes,
		workflow.Node{ID: "after", Name: "After", Type: "test.after", TypeVersion: workflow.V(1)})
	document.Connections = append(document.Connections, workflow.Connection{
		ID: "ds-after", Kind: workflow.ConnectionMain,
		Source: workflow.Endpoint{NodeID: "ds", Port: "main"},
		Target: workflow.Endpoint{NodeID: "after", Port: "main"},
	})
	return document
}
