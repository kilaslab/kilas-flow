package engine_test

import (
	"context"
	"encoding/json"
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

	errorTriggerType, failingType, seen := registerErrorWorkflowNodes(t, sandbox)

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
	// Active here; TestAnErrorWorkflowRunsItsLatestRevisionWhetherOrNotItIsActive
	// covers the one nobody activated.
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
		PublicBaseURL: "https://flows.example.com/",
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
	// And the failure names the run it started, on the error the execution
	// page shows: the parent link alone answers "which failure was this", not
	// "did this failure's error workflow run".
	failed, err = sandbox.store.Get(sandbox.ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get(failed) error = %v", err)
	}
	recorded := failureNotes(t, failed)
	if got, want := recorded["errorWorkflowId"], "wf_alert"; got != want {
		t.Errorf("failed execution errorWorkflowId = %v, want %q", got, want)
	}
	if got, want := recorded["errorExecutionId"], alert.ID; got != want {
		t.Errorf("failed execution errorExecutionId = %v, want the error workflow's execution %q", got, want)
	}
	if message, _ := recorded["message"].(string); !strings.Contains(message, "the API key was rejected") {
		t.Errorf("failed execution error message = %q, want the run's own failure kept", message)
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
	// n8n's lastNodeExecuted is the node's name, which is what an alert prints
	// and what `$('Failing')` would name; the ID is an internal handle.
	if got, want := detail["lastNodeExecuted"], "Failing"; got != want {
		t.Errorf("error payload lastNodeExecuted = %v, want the node's name %q", got, want)
	}
	// The link an alert carries back to the failed run, under the public URL.
	if got, want := detail["url"], "https://flows.example.com/executions/"+queued.ID; got != want {
		t.Errorf("error payload url = %v, want %q: an alert cannot link to the failed run", got, want)
	}
	stack, _ := failure["stack"].(string)
	if !strings.Contains(stack, "the API key was rejected") || !strings.Contains(stack, "Failing") {
		t.Errorf("error payload stack = %q, want the failure and the node it happened in", stack)
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
	// The refusal is on the failed execution, not only in the server log.
	if refusal, _ := failureNotes(t, page.Records[0])["errorWorkflowError"].(string); !strings.Contains(refusal, "its own workflow") {
		t.Errorf("self-referential errorWorkflowError = %q, want the refusal named on the execution", refusal)
	}
}

// An error workflow runs whether or not anybody activated it, and it runs its
// latest revision.
//
// n8n: "If a workflow uses the Error Trigger node, you don't have to publish
// the workflow." An error handler was skipped unless it was active, and the
// only trace of that was a line in the server log, so an alerting workflow
// somebody saved and never activated reported nothing while every failure it
// was written for went by.
func TestAnErrorWorkflowRunsItsLatestRevisionWhetherOrNotItIsActive(t *testing.T) {
	sandbox := newEngineSandbox(t, "kilasflow.db")
	tenant := repository.TenantScope{ID: "tenant-error-workflow-draft"}
	errorTriggerType, failingType, seen := registerErrorWorkflowNodes(t, sandbox)
	workflows := repository.NewWorkflowStore(sandbox.db.DB)

	alert := func(marker string) workflow.Document {
		return workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            "wf_alert_draft",
			Name:          "Alert on failure",
			Nodes: []workflow.Node{
				{ID: "onError", Name: "Error Trigger", Type: errorTriggerType, TypeVersion: workflow.V(1)},
				{ID: "alert", Name: "Alert", Type: "kilasflow.set", TypeVersion: workflow.V(1),
					Parameters: map[string]any{"assignments": map[string]any{"revision": marker}}},
			},
			Connections: []workflow.Connection{{
				ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "onError", Port: "main"},
				Target: workflow.Endpoint{NodeID: "alert", Port: "main"},
			}},
			Settings: map[string]any{},
		}
	}
	saved, err := workflows.SaveDraft(sandbox.ctx, tenant, alert("first"))
	if err != nil {
		t.Fatalf("SaveDraft(error workflow) error = %v", err)
	}
	fragile, err := workflows.SaveDraft(sandbox.ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_fragile_draft",
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
		Settings: map[string]any{"errorWorkflow": "wf_alert_draft"},
	})
	if err != nil {
		t.Fatalf("SaveDraft(fragile) error = %v", err)
	}
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: sandbox.store, Catalog: sandbox.catalog, Runner: engine.NewRunner(sandbox.executors),
		WorkerID: "error-worker", DefaultTimeout: 5 * time.Second, ErrorTriggerType: errorTriggerType,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	// failOnce runs the fragile workflow and returns the error-workflow
	// execution it started.
	failOnce := func(step string) execution.Record {
		t.Helper()
		queued, err := sandbox.store.QueueManualLatest(sandbox.ctx, tenant, fragile.ID, sandbox.catalog, "", nil)
		if err != nil {
			t.Fatalf("%s: QueueManualLatest() error = %v", step, err)
		}
		if worked, err := service.RunOnce(sandbox.ctx); err != nil || !worked {
			t.Fatalf("%s: RunOnce() = (%v, %v), want the fragile execution run", step, worked, err)
		}
		select {
		case <-seen:
		case <-time.After(time.Second):
			t.Fatalf("%s: the error workflow's trigger never ran", step)
		}
		page, err := sandbox.store.List(sandbox.ctx, tenant, repository.ExecutionFilter{WorkflowID: "wf_alert_draft"})
		if err != nil {
			t.Fatalf("%s: List(error workflow executions) error = %v", step, err)
		}
		for _, record := range page.Records {
			if record.ParentExecutionID == queued.ID {
				if record.Status != execution.StatusSucceeded {
					t.Errorf("%s: error workflow execution status = %q, want %q", step, record.Status, execution.StatusSucceeded)
				}
				return record
			}
		}
		t.Fatalf("%s: no error workflow execution for the failed run %s", step, queued.ID)
		return execution.Record{}
	}

	// Saved and never activated: it runs anyway.
	if got, want := failOnce("never activated").WorkflowVersionID, saved.LatestVersion.ID; got != want {
		t.Errorf("never activated: error workflow ran version %q, want the saved revision %q", got, want)
	}

	// Activated, then edited: the latest revision runs, not the activated one,
	// so a fix to the alert takes effect the moment it is saved.
	if _, err := workflows.Activate(sandbox.ctx, tenant, saved.ID, sandbox.catalog); err != nil {
		t.Fatalf("Activate(error workflow) error = %v", err)
	}
	edited, err := workflows.SaveDraft(sandbox.ctx, tenant, alert("second"))
	if err != nil {
		t.Fatalf("SaveDraft(edited error workflow) error = %v", err)
	}
	if edited.LatestVersion.ID == saved.LatestVersion.ID {
		t.Fatalf("the edit did not save a new revision; the test cannot tell the two apart")
	}
	if got, want := failOnce("edited after activation").WorkflowVersionID, edited.LatestVersion.ID; got != want {
		t.Errorf("edited after activation: error workflow ran version %q, want the latest revision %q", got, want)
	}

	// One that does not exist: nothing runs, and the failed execution says so
	// rather than leaving it to the server log.
	orphan, err := workflows.SaveDraft(sandbox.ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_fragile_orphan",
		Name:          "Fragile orphan",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "failing", Name: "Failing", Type: failingType, TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "failing", Port: "main"},
		}},
		Settings: map[string]any{"errorWorkflow": "wf_deleted_long_ago"},
	})
	if err != nil {
		t.Fatalf("SaveDraft(orphan) error = %v", err)
	}
	queued, err := sandbox.store.QueueManualLatest(sandbox.ctx, tenant, orphan.ID, sandbox.catalog, "", nil)
	if err != nil {
		t.Fatalf("QueueManualLatest(orphan) error = %v", err)
	}
	if worked, err := service.RunOnce(sandbox.ctx); err != nil || !worked {
		t.Fatalf("RunOnce(orphan) = (%v, %v), want the execution run", worked, err)
	}
	failed, err := sandbox.store.Get(sandbox.ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get(orphan) error = %v", err)
	}
	notes := failureNotes(t, failed)
	if reason, _ := notes["errorWorkflowError"].(string); reason == "" {
		t.Errorf("orphan error = %v, want errorWorkflowError naming why the error workflow did not run", notes)
	}
	if _, ran := notes["errorExecutionId"]; ran {
		t.Errorf("orphan error = %v, want no errorExecutionId: nothing ran", notes)
	}
}

// failureNotes decodes a failed execution's error object.
func failureNotes(t *testing.T, record execution.Record) map[string]any {
	t.Helper()
	var notes map[string]any
	if err := json.Unmarshal(record.Error, &notes); err != nil {
		t.Fatalf("execution %s error = %s, want an object: %v", record.ID, record.Error, err)
	}
	return notes
}

// registerErrorWorkflowNodes registers a test Error Trigger and a node that
// always fails, and returns the channel the trigger hands its first item to.
func registerErrorWorkflowNodes(t *testing.T, sandbox *engineSandbox) (errorTriggerType, failingType string, seen chan map[string]any) {
	t.Helper()
	errorTriggerType, failingType = "kilasflow.test.errorTrigger", "kilasflow.test.failing"
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
	seen = make(chan map[string]any, 1)
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

	return errorTriggerType, failingType, seen
}
