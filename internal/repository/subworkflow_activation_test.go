package repository_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
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
	// A typed error, so the API can answer a 4xx that names the node and the
	// child instead of the generic 500 an untyped string became.
	var refusal *repository.SubworkflowRefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("Activate(caller) error = %T %v, want a *SubworkflowRefusalError", err, err)
	}
	if len(refusal.Refusals) != 1 {
		t.Fatalf("refusals = %+v, want exactly the one bad call", refusal.Refusals)
	}
	only := refusal.Refusals[0]
	if only.NodeID != "call" || only.NodeName != "Run target" ||
		only.WorkflowID != "wf_target" || only.WorkflowName != "Target" || only.Missing {
		t.Errorf("refusal = %+v, want node call/Run target, child wf_target/Target, not missing", only)
	}
	if want := "/nodes/1/parameters/workflowId"; only.Path != want {
		t.Errorf("refusal path = %q, want %q", only.Path, want)
	}
	if !strings.Contains(err.Error(), `"Target"`) || !strings.Contains(err.Error(), "wf_target") {
		t.Errorf("Activate(caller) error = %v, want it to name the child by name and id", err)
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
	} else {
		var refusal *repository.SubworkflowRefusalError
		if !errors.As(err, &refusal) || len(refusal.Refusals) != 1 ||
			!refusal.Refusals[0].Missing || refusal.Refusals[0].WorkflowID != "wf_gone" || refusal.Refusals[0].WorkflowName != "" {
			t.Errorf("Activate(orphan) refusal = %+v, want a typed missing-target refusal for wf_gone", refusal)
		}
	}

	// Without the extractor the check is off, which is what an installation
	// that never wires one keeps: activation behaves as it did before.
	plain := repository.NewWorkflowStore(db.DB)
	if _, err := plain.Activate(ctx, tenant, orphan.ID, catalog); err != nil {
		t.Errorf("Activate(orphan) without an extractor = %v, want the check off", err)
	}
}

// Every call that cannot run is in the refusal, in the document's node order, and
// a call that can run is not. Returning on the first bad one made a workflow with
// three bad calls take three activations to fix.
func TestActivationRefusesEveryBadSubWorkflowCallAtOnce(t *testing.T) {
	db := openClaimDB(t, "kilasflow.db")
	ctx := context.Background()
	tenant := repository.TenantScope{ID: "tenant-activation-every"}
	main := workflow.Port{Name: "main", Kind: workflow.ConnectionMain}
	catalog := activationCatalog{
		"kilasflow.manual": {Type: "kilasflow.manual", Version: workflow.V(1), Outputs: []workflow.Port{main}},
		"kilasflow.calls": {Type: "kilasflow.calls", Version: workflow.V(1),
			Inputs: []workflow.Port{main}, Outputs: []workflow.Port{main}},
	}
	workflows := repository.NewWorkflowStore(db.DB).WithSubworkflows(subworkflowCalls)

	for id, name := range map[string]string{"wf_live": "Live child", "wf_draft": "Draft child"} {
		if _, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion, ID: id, Name: name,
			Nodes:       []workflow.Node{{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)}},
			Connections: []workflow.Connection{}, Settings: map[string]any{},
		}); err != nil {
			t.Fatalf("SaveDraft(%q) error = %v", id, err)
		}
	}
	if _, err := workflows.Activate(ctx, tenant, "wf_live", catalog); err != nil {
		t.Fatalf("Activate(live child) error = %v", err)
	}

	call := func(id, name, target string) workflow.Node {
		return workflow.Node{ID: id, Name: name, Type: "kilasflow.calls", TypeVersion: workflow.V(1),
			Parameters: map[string]any{"workflowId": map[string]any{"value": target}}}
	}
	chain := []string{"manual", "first", "fine", "second", "third"}
	connections := make([]workflow.Connection, 0, len(chain)-1)
	for index := 1; index < len(chain); index++ {
		connections = append(connections, workflow.Connection{
			ID: "c" + chain[index], Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: chain[index-1], Port: "main"},
			Target: workflow.Endpoint{NodeID: chain[index], Port: "main"},
		})
	}
	caller, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_every", Name: "Caller",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			call("first", "Run draft", "wf_draft"),
			call("fine", "Run live", "wf_live"),
			call("second", "Run gone", "wf_gone"),
			call("third", "Run draft again", "wf_draft"),
		},
		Connections: connections, Settings: map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft(caller) error = %v", err)
	}

	_, err = workflows.Activate(ctx, tenant, caller.ID, catalog)
	var refusal *repository.SubworkflowRefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("Activate(caller) error = %T %v, want a *SubworkflowRefusalError", err, err)
	}
	type want struct {
		node, child, name string
		missing           bool
	}
	wants := []want{
		{"first", "wf_draft", "Draft child", false},
		{"second", "wf_gone", "", true},
		{"third", "wf_draft", "Draft child", false},
	}
	if len(refusal.Refusals) != len(wants) {
		t.Fatalf("refusals = %+v, want the %d bad calls and not the live one", refusal.Refusals, len(wants))
	}
	for index, expected := range wants {
		got := refusal.Refusals[index]
		if got.NodeID != expected.node || got.WorkflowID != expected.child || got.WorkflowName != expected.name || got.Missing != expected.missing {
			t.Errorf("refusal %d = %+v, want node %s calling %s (%q, missing=%v)", index, got, expected.node, expected.child, expected.name, expected.missing)
		}
	}
	for _, sentence := range []string{"Run draft", "Run gone", "Run draft again"} {
		if !strings.Contains(err.Error(), sentence) {
			t.Errorf("error = %q, want it to name %q", err, sentence)
		}
	}
	if strings.Contains(err.Error(), "Run live") {
		t.Errorf("error = %q, want the call to the live child left out", err)
	}
}

// A document that calls a sub-workflow dynamically activates.
//
// The gate reads the workflows a document calls and refuses the ones whose
// target cannot run, but a call node whose locator holds an expression holds no
// workflow ID: its target is only knowable when the node runs. The gate looked
// the template up as an ID — for a marker locator that was the marker's own
// JSON — and refused the activation, so a workflow calling a sub-workflow
// dynamically could not be published at all.
func TestActivationAllowsADynamicSubWorkflowTarget(t *testing.T) {
	db := openClaimDB(t, "kilasflow.db")
	ctx := context.Background()
	tenant := repository.TenantScope{ID: "tenant-activation-dynamic"}
	main := workflow.Port{Name: "main", Kind: workflow.ConnectionMain}
	catalog := activationCatalog{
		"kilasflow.manual": {Type: "kilasflow.manual", Version: workflow.V(1), Outputs: []workflow.Port{main}},
		nodes.ExecuteWorkflowNodeType: {Type: nodes.ExecuteWorkflowNodeType, Version: workflow.V(1),
			Inputs: []workflow.Port{main}, Outputs: []workflow.Port{main}},
	}
	// The production reading, not a stand-in: this is the walk the gate refuses
	// on and the one the confinement is minted from.
	workflows := repository.NewWorkflowStore(db.DB).WithSubworkflows(nodes.SubworkflowCalls)

	caller := func(id, name string, locator map[string]any) workflow.StoredWorkflow {
		t.Helper()
		stored, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            id,
			Name:          name,
			Nodes: []workflow.Node{
				{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
				{ID: "call", Name: "Run dynamic", Type: nodes.ExecuteWorkflowNodeType, TypeVersion: workflow.V(1),
					Parameters: map[string]any{"workflowId": locator}},
			},
			Connections: []workflow.Connection{{
				ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
				Target: workflow.Endpoint{NodeID: "call", Port: "main"},
			}},
			Settings: map[string]any{},
		})
		if err != nil {
			t.Fatalf("SaveDraft(%q) error = %v", id, err)
		}
		return stored
	}

	// The marker spelling: the expression sits in the locator's value slot.
	marked := caller("wf_dynamic_marker", "Dynamic by marker", map[string]any{
		"__rl": true, "mode": "id",
		"value": map[string]any{"mode": "expression", "value": "{{ $json.wf }}"},
	})
	activated, err := workflows.Activate(ctx, tenant, marked.ID, catalog)
	if err != nil {
		t.Fatalf("Activate(dynamic by marker) error = %v, want the activation to succeed", err)
	}
	if !activated.Active {
		t.Error("the caller is not active after activating a dynamic target")
	}

	// n8n spells the same thing as a leading `=` on a plain string, and a
	// document can still carry that spelling.
	prefixed := caller("wf_dynamic_string", "Dynamic by string", map[string]any{
		"__rl": true, "mode": "id", "value": "={{ $json.wf }}",
	})
	if _, err := workflows.Activate(ctx, tenant, prefixed.ID, catalog); err != nil {
		t.Fatalf("Activate(dynamic by string) error = %v, want the activation to succeed", err)
	}

	// A literal target is still a target: one that does not exist is refused,
	// with the message it has always used.
	missing := caller("wf_literal_missing", "Literal missing", map[string]any{
		"__rl": true, "mode": "list", "value": "wf_gone",
	})
	if _, err := workflows.Activate(ctx, tenant, missing.ID, catalog); err == nil {
		t.Error("Activate(literal missing) succeeded, want a refusal")
	} else if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("Activate(literal missing) error = %v, want it to say the target does not exist", err)
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
