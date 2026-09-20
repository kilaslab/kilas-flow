package repository_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// A workflow cannot be activated while the workflow it calls is not.
//
// The failure this prevents is silent: a sub-workflow call resolves its target
// when it runs, so a published caller whose target is a draft, deleted or
// renamed looks healthy on the canvas and fails halfway through a run whose
// earlier nodes have already done their work. n8n refuses the activation; so
// does this, naming the node to fix.
func TestActivationRefusesASubWorkflowCallToAnInactiveWorkflow(t *testing.T) {
	db := openClaimDB(t, "kilasflow.db")
	ctx := context.Background()
	tenant := repository.TenantScope{ID: "tenant-activation"}
	main := workflow.Port{Name: "main", Kind: workflow.ConnectionMain}
	catalog := activationCatalog{
		"kilasflow.manual": {Type: "kilasflow.manual", Version: workflow.V(1), Outputs: []workflow.Port{main}},
		"kilasflow.calls": {Type: "kilasflow.calls", Version: workflow.V(1),
			Inputs: []workflow.Port{main}, Outputs: []workflow.Port{main}},
	}
	workflows := repository.NewWorkflowStore(db.DB).WithSubworkflows(subworkflowCalls)

	seed := func(id, name string) workflow.StoredWorkflow {
		t.Helper()
		stored, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            id,
			Name:          name,
			Nodes: []workflow.Node{
				{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			},
			Connections: []workflow.Connection{},
			Settings:    map[string]any{},
		})
		if err != nil {
			t.Fatalf("SaveDraft(%q) error = %v", id, err)
		}
		return stored
	}
	seed("wf_target", "Target")

	// The caller names the target through the same locator an Execute
	// Sub-workflow node uses.
	caller, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_caller",
		Name:          "Caller",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "call", Name: "Run target", Type: "kilasflow.calls", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"workflowId": map[string]any{"value": "wf_target", "mode": "list"}}},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "call", Port: "main"},
		}},
		Settings: map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft(caller) error = %v", err)
	}

	_, err = workflows.Activate(ctx, tenant, caller.ID, catalog)
	if err == nil {
		t.Fatal("Activate(caller) succeeded with an inactive target, want a refusal")
	}
	if !strings.Contains(err.Error(), "not active") || !strings.Contains(err.Error(), "Run target") {
		t.Errorf("Activate(caller) error = %v, want it to name the node and the reason", err)
	}
	// A refused activation changes nothing: the workflow is not live, so the
	// refusal cannot leave a half-published caller behind.
	refused, err := workflows.Get(ctx, tenant, caller.ID)
	if err != nil {
		t.Fatalf("Get(caller) error = %v", err)
	}
	if refused.Active {
		t.Error("the caller is active after a refused activation")
	}

	// Activate the target and the same activation succeeds.
	if _, err := workflows.Activate(ctx, tenant, "wf_target", catalog); err != nil {
		t.Fatalf("Activate(target) error = %v", err)
	}
	activated, err := workflows.Activate(ctx, tenant, caller.ID, catalog)
	if err != nil {
		t.Fatalf("Activate(caller) after activating the target = %v", err)
	}
	if !activated.Active {
		t.Error("the caller is not active after its target was activated")
	}

	// A deleted target is a different refusal: it needs a different fix, so it
	// is not reported as "not active".
	orphan, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_orphan",
		Name:          "Orphan",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "call", Name: "Run missing", Type: "kilasflow.calls", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"workflowId": map[string]any{"value": "wf_gone", "mode": "list"}}},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "call", Port: "main"},
		}},
		Settings: map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft(orphan) error = %v", err)
	}
	if _, err := workflows.Activate(ctx, tenant, orphan.ID, catalog); err == nil {
		t.Error("Activate(orphan) succeeded with a target that does not exist, want a refusal")
	} else if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("Activate(orphan) error = %v, want it to say the target does not exist", err)
	}

	// Without the extractor the check is off, which is what an installation
	// that never wires one keeps: activation behaves as it did before.
	plain := repository.NewWorkflowStore(db.DB)
	if _, err := plain.Activate(ctx, tenant, orphan.ID, catalog); err != nil {
		t.Errorf("Activate(orphan) without an extractor = %v, want the check off", err)
	}
}

// subworkflowCalls is the extractor the nodes package supplies in production,
// reduced to what this test needs: every node whose parameters name a target
// workflow.
func subworkflowCalls(document workflow.Document) []repository.SubworkflowCall {
	calls := make([]repository.SubworkflowCall, 0, 2)
	for _, node := range document.Nodes {
		if node.Type != "kilasflow.calls" {
			continue
		}
		locator, _ := node.Parameters["workflowId"].(map[string]any)
		target, _ := locator["value"].(string)
		if strings.TrimSpace(target) == "" {
			continue
		}
		calls = append(calls, repository.SubworkflowCall{NodeID: node.ID, NodeName: node.Name, WorkflowID: target})
	}
	return calls
}
