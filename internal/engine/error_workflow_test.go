package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// A workflow that names an error workflow in its settings gets one run when it
// fails, and the error workflow sees the failure.
//
// n8n's settings.errorWorkflow: the error workflow starts from its Error
// Trigger with the error object, which is where an alerting workflow reads the
// message, the node that failed and the execution it belongs to. The node types
// here are test ones — the real Error Trigger and Stop and Error nodes live in
// the nodes package and are registered there — because what this pins is the
// engine's half: the failure path starts the named workflow, from its trigger,
// with the payload, and does not do it when there is nothing to report.
func TestAFailedRunStartsTheErrorWorkflowNamedInItsSettings(t *testing.T) {
	sandbox := newEngineSandbox(t, "kilasflow.db")
	tenant := repository.TenantScope{ID: "tenant-error-workflow"}

	const (
		errorTriggerType = "kilasflow.test.errorTrigger"
		failingType      = "kilasflow.test.failing"
	)
	for _, definition := range []node.Definition{
		{
			Group: []node.NodeGroup{node.GroupTrigger}, Type: errorTriggerType, Version: workflow.V(1),
			DisplayName: "Error Trigger", Category: "Test", Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			ExecutorID: "test.errorTrigger",
		},
		{
			Group: []node.NodeGroup{node.GroupTransform}, Type: failingType, Version: workflow.V(1),
			DisplayName: "Failing", Category: "Test",
			Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.failing",
		},
	} {
		if err := sandbox.catalog.Register(definition); err != nil {
			t.Fatalf("Register(%q) error = %v", definition.Type, err)
		}
	}
	seen := make(chan map[string]any, 1)
	if err := sandbox.executors.Register("test.errorTrigger", engine.ExecutorFunc(func(ctx context.Context, _ workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
		// A trigger node turns the run's own input into the items the graph
		// starts with: the engine sends the error object under `items`, which
		// is the shape the real Error Trigger node unpacks.
		items := []workflow.Item{{JSON: request.Input.JSON}}
		if list, ok := request.Input.JSON["items"].([]any); ok {
			items = items[:0]
			for _, entry := range list {
				if fields, ok := entry.(map[string]any); ok {
					items = append(items, workflow.Item{JSON: fields})
				}
			}
		}
		if len(items) > 0 {
			select {
			case seen <- items[0].JSON:
			default:
			}
		}
		return workflow.NodeOutput{items}, nil
	})); err != nil {
		t.Fatalf("Register(error trigger executor) error = %v", err)
	}
	if err := sandbox.executors.Register("test.failing", engine.ExecutorFunc(func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
		return nil, errors.New("the API key was rejected")
	})); err != nil {
		t.Fatalf("Register(failing executor) error = %v", err)
	}

	// The error workflow: its trigger, then a node after it.
	if _, err := repository.NewWorkflowStore(sandbox.db.DB).SaveDraft(sandbox.ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_alert",
		Name:          "Alert on failure",
		Nodes: []workflow.Node{
			{ID: "onError", Name: "Error Trigger", Type: errorTriggerType, TypeVersion: workflow.V(1)},
			{ID: "alert", Name: "Alert", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"alerted": "yes"}}},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "onError", Port: "main"},
			Target: workflow.Endpoint{NodeID: "alert", Port: "main"},
		}},
		Settings: map[string]any{},
	}); err != nil {
		t.Fatalf("SaveDraft(error workflow) error = %v", err)
	}
	// An error workflow has to be active to run, exactly as a called
	// sub-workflow does: a workflow nobody published is a draft, and starting
	// one from a failure would run a graph its author never put into service.
	workflows := repository.NewWorkflowStore(sandbox.db.DB)
	if _, err := workflows.Activate(sandbox.ctx, tenant, "wf_alert", sandbox.catalog); err != nil {
		t.Fatalf("Activate(error workflow) error = %v", err)
	}
	// The workflow that fails and names it.
	stored, err := repository.NewWorkflowStore(sandbox.db.DB).SaveDraft(sandbox.ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_fragile",
		Name:          "Fragile",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "failing", Name: "Failing", Type: failingType, TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "failing", Port: "main"},
		}},
		Settings: map[string]any{"errorWorkflow": "wf_alert"},
	})
	if err != nil {
		t.Fatalf("SaveDraft(fragile) error = %v", err)
	}
	queued, err := sandbox.store.QueueManualLatest(sandbox.ctx, tenant, stored.ID, sandbox.catalog, "", nil)
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}

	service, err := engine.NewService(engine.ServiceDeps{
		Executions: sandbox.store, Catalog: sandbox.catalog, Runner: engine.NewRunner(sandbox.executors),
		WorkerID: "error-worker", DefaultTimeout: 5 * time.Second, ErrorTriggerType: errorTriggerType,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if worked, err := service.RunOnce(sandbox.ctx); err != nil || !worked {
		t.Fatalf("RunOnce() = (%v, %v), want the fragile execution run", worked, err)
	}

	failed, err := sandbox.store.Get(sandbox.ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get(failed) error = %v", err)
	}
	if got, want := failed.Status, execution.StatusFailed; got != want {
		t.Fatalf("fragile execution status = %q, want %q", got, want)
	}

	// The error workflow ran as its own execution, parented to the failure.
	page, err := sandbox.store.List(sandbox.ctx, tenant, repository.ExecutionFilter{WorkflowID: "wf_alert"})
	if err != nil {
		t.Fatalf("List(error workflow executions) error = %v", err)
	}
	if len(page.Records) != 1 {
		t.Fatalf("error workflow executions = %d, want 1: a failed run must start the workflow its settings name", len(page.Records))
	}
	alert := page.Records[0]
	if got, want := alert.Status, execution.StatusSucceeded; got != want {
		t.Errorf("error workflow execution status = %q, want %q", got, want)
	}
	if got, want := alert.ParentExecutionID, queued.ID; got != want {
		t.Errorf("error workflow parent = %q, want %q: history must show which failure it belongs to", got, want)
	}

	var payload map[string]any
	select {
	case payload = <-seen:
	case <-time.After(time.Second):
		t.Fatal("the error workflow's trigger never ran: the workflow was started from something other than its Error Trigger")
	}
	detail, _ := payload["execution"].(map[string]any)
	if detail == nil {
		t.Fatalf("error trigger item = %s, want n8n's execution/workflow/trigger shape", payload)
	}
	if got, want := detail["id"], queued.ID; got != want {
		t.Errorf("error payload execution id = %v, want %q", got, want)
	}
	failure, _ := detail["error"].(map[string]any)
	message, _ := failure["message"].(string)
	if !strings.Contains(message, "the API key was rejected") {
		t.Errorf("error payload message = %q, want the failure the workflow hit", message)
	}
	nodeDetail, _ := failure["node"].(map[string]any)
	if got, want := nodeDetail["id"], "failing"; got != want {
		t.Errorf("error payload node = %v, want %q: an alert has to name what failed", got, want)
	}
	if got, want := detail["lastNodeExecuted"], "failing"; got != want {
		t.Errorf("error payload lastNodeExecuted = %v, want %q", got, want)
	}
	workflowDetail, _ := payload["workflow"].(map[string]any)
	if got, want := workflowDetail["name"], "Fragile"; got != want {
		t.Errorf("error payload workflow name = %v, want %q", got, want)
	}

	// A workflow that names itself is refused rather than recursed into: the
	// error run would fail, which would start the error run again.
	self, err := repository.NewWorkflowStore(sandbox.db.DB).SaveDraft(sandbox.ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_self_referential",
		Name:          "Self referential",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "failing", Name: "Failing", Type: failingType, TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "failing", Port: "main"},
		}},
		Settings: map[string]any{"errorWorkflow": "wf_self_referential"},
	})
	if err != nil {
		t.Fatalf("SaveDraft(self referential) error = %v", err)
	}
	if _, err := sandbox.store.QueueManualLatest(sandbox.ctx, tenant, self.ID, sandbox.catalog, "", nil); err != nil {
		t.Fatalf("QueueManualLatest(self referential) error = %v", err)
	}
	if worked, err := service.RunOnce(sandbox.ctx); err != nil || !worked {
		t.Fatalf("RunOnce(self referential) = (%v, %v), want the execution run", worked, err)
	}
	page, err = sandbox.store.List(sandbox.ctx, tenant, repository.ExecutionFilter{WorkflowID: "wf_self_referential"})
	if err != nil {
		t.Fatalf("List(self referential) error = %v", err)
	}
	if len(page.Records) != 1 {
		t.Errorf("self-referential error workflow produced %d executions, want 1: it must be refused, not recursed into", len(page.Records))
	}
}
