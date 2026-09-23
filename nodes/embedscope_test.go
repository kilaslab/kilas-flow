package nodes

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The document walker is the one place that decides what a graph reaches
// outside itself, so its rules are pinned here rather than only through the API:
// a datastore node addressed four different ways, an agent tool that binds a
// table, a sub-workflow call, and the expression forms that cannot be bounded at
// all.

func embedTestNode(nodeType, id string, parameters map[string]any) workflow.Node {
	return workflow.Node{
		ID: id, Name: id, Type: nodeType, TypeVersion: workflow.V(1), Parameters: parameters,
	}
}

func embedTestLocator(mode string, value any) map[string]any {
	return map[string]any{"__rl": true, "mode": mode, "value": value}
}

func TestEmbedScopeIssuesRefusesEveryTableOperation(t *testing.T) {
	confinement := embed.Confinement{Datastores: []embed.DatastoreRef{{ID: "ds_1", Name: "Metrics"}}}
	for _, operation := range []string{
		DatastoreOperationCreateTable, DatastoreOperationListTables,
		DatastoreOperationRenameTable, DatastoreOperationDeleteTable, DatastoreOperationClearTable,
	} {
		document := workflow.Document{Nodes: []workflow.Node{
			embedTestNode(DatastoreNodeType, "table", map[string]any{
				"resource": "table", "operation": operation, "dataTableId": embedTestLocator("id", "ds_1"),
			}),
		}}
		if issues := EmbedScopeIssues(document, confinement); len(issues) == 0 {
			t.Errorf("operation %q was allowed for an embed session", operation)
		}
	}
}

func TestEmbedScopeIssuesRefusesAnOperationThatContradictsTheDeclaredResource(t *testing.T) {
	// A document may declare resource "row" beside a table operation; the
	// operation is what the executor acts on, so the operation decides.
	document := workflow.Document{Nodes: []workflow.Node{
		embedTestNode(DatastoreNodeType, "sneaky", map[string]any{
			"resource": "row", "operation": DatastoreOperationListTables,
		}),
	}}
	if issues := EmbedScopeIssues(document, embed.Confinement{}); len(issues) == 0 {
		t.Fatal("a row resource beside a table operation was allowed")
	}
}

func TestEmbedScopeIssuesMatchesADataTableByIdAndByName(t *testing.T) {
	confinement := embed.Confinement{Datastores: []embed.DatastoreRef{{ID: "ds_1", Name: "Metrics"}}}

	for _, allowed := range []map[string]any{
		embedTestLocator("id", "ds_1"),
		embedTestLocator("list", "ds_1"),
		embedTestLocator("name", "Metrics"),
		// A By-Name locator resolves against the tenant's live list without
		// regard to case, so the check has to compare the same way.
		embedTestLocator("name", "metrics"),
	} {
		document := workflow.Document{Nodes: []workflow.Node{
			embedTestNode(DatastoreNodeType, "rows", map[string]any{
				"resource": "row", "operation": DatastoreOperationGet, "dataTableId": allowed,
			}),
		}}
		if issues := EmbedScopeIssues(document, confinement); len(issues) != 0 {
			t.Errorf("locator %#v was refused: %v", allowed, issues)
		}
	}

	for _, refused := range []map[string]any{
		embedTestLocator("id", "ds_2"),
		embedTestLocator("list", "ds_2"),
		embedTestLocator("name", "Sibling"),
	} {
		document := workflow.Document{Nodes: []workflow.Node{
			embedTestNode(DatastoreNodeType, "rows", map[string]any{
				"resource": "row", "operation": DatastoreOperationGet, "dataTableId": refused,
			}),
		}}
		if issues := EmbedScopeIssues(document, confinement); len(issues) == 0 {
			t.Errorf("locator %#v was allowed", refused)
		}
	}
}

// A By-Name grant is compared by datastore.ResolveByName, the rule a run
// resolves the name by, so the check and the run cannot disagree about which
// names are the same. A name two grants share is still granted — each grant
// would allow it alone, so allowing it widens nothing — and whether the name
// picks out one table is the run's question, asked against the tenant's live
// list, where a name two tables share is refused.
func TestEmbedScopeIssuesComparesGrantedNamesTheWayARunResolvesThem(t *testing.T) {
	byName := func(name string) workflow.Document {
		return workflow.Document{Nodes: []workflow.Node{
			embedTestNode(DatastoreNodeType, "rows", map[string]any{
				"resource": "row", "operation": DatastoreOperationGet, "dataTableId": embedTestLocator("name", name),
			}),
		}}
	}

	shared := embed.Confinement{Datastores: []embed.DatastoreRef{{Name: "Leads"}, {Name: "leads"}}}
	for _, name := range []string{"LEADS", "  leads "} {
		if issues := EmbedScopeIssues(byName(name), shared); len(issues) != 0 {
			t.Errorf("the name %q, which two grants carry, was refused: %v", name, issues)
		}
	}
	if issues := EmbedScopeIssues(byName("Leads"), embed.Confinement{}); len(issues) == 0 {
		t.Error("an empty confinement granted a name")
	}
	// A grant by id is a different reference, not a name to compare against.
	idOnly := embed.Confinement{Datastores: []embed.DatastoreRef{{ID: "Leads"}}}
	if issues := EmbedScopeIssues(byName("Leads"), idOnly); len(issues) == 0 {
		t.Error("a grant by id granted a name that happens to spell the id")
	}
}

func TestEmbedScopeIssuesRefusesAnExpressionWhereATargetIsRequired(t *testing.T) {
	// The value is only knowable at run time, and a check that cannot see the
	// target cannot bound it.
	document := workflow.Document{Nodes: []workflow.Node{
		embedTestNode(DatastoreNodeType, "rows", map[string]any{
			"resource": "row", "operation": DatastoreOperationGet,
			"dataTableId": embedTestLocator("id", map[string]any{"mode": "expression", "value": "={{ $json.table }}"}),
		}),
		embedTestNode(ExecuteWorkflowNodeType, "call", map[string]any{
			"workflowId": embedTestLocator("id", map[string]any{"mode": "expression", "value": "={{ $json.wf }}"}),
		}),
	}}
	if issues := EmbedScopeIssues(document, embed.Confinement{}); len(issues) != 2 {
		t.Fatalf("issues = %v, want one per expression target", issues)
	}
}

func TestEmbedScopeIssuesBoundsTheDataTableToolNode(t *testing.T) {
	document := workflow.Document{Nodes: []workflow.Node{
		embedTestNode(DatastoreToolNodeType, "tool", map[string]any{
			"dataTableId": embedTestLocator("id", "ds_sibling"),
		}),
	}}
	if issues := EmbedScopeIssues(document, embed.Confinement{}); len(issues) == 0 {
		t.Fatal("an agent tool bound to a sibling table was allowed")
	}
	allowed := embed.Confinement{Datastores: []embed.DatastoreRef{{ID: "ds_sibling"}}}
	if issues := EmbedScopeIssues(document, allowed); len(issues) != 0 {
		t.Fatalf("issues = %v, want none inside the confinement", issues)
	}

	// The tool writes as the step node does, so it answers to the same
	// operation check: a row write inside the confinement is allowed, and a
	// table operation is refused whatever the confinement names.
	for _, operation := range []string{DatastoreOperationInsert, DatastoreOperationUpdate, DatastoreOperationDelete} {
		writer := workflow.Document{Nodes: []workflow.Node{
			embedTestNode(DatastoreToolNodeType, "tool", map[string]any{
				"operation": operation, "dataTableId": embedTestLocator("id", "ds_sibling"),
			}),
		}}
		if issues := EmbedScopeIssues(writer, allowed); len(issues) != 0 {
			t.Errorf("issues = %v, want a %s tool inside the confinement allowed", issues, operation)
		}
	}
	for _, operation := range []string{DatastoreOperationClearTable, DatastoreOperationDeleteTable, DatastoreOperationListTables} {
		manager := workflow.Document{Nodes: []workflow.Node{
			embedTestNode(DatastoreToolNodeType, "tool", map[string]any{
				"operation": operation, "dataTableId": embedTestLocator("id", "ds_sibling"),
			}),
		}}
		if issues := EmbedScopeIssues(manager, allowed); len(issues) == 0 {
			t.Errorf("a tool performing the table operation %q was allowed for an embed session", operation)
		}
	}
}

func TestEmbedScopeIssuesBoundsTheWorkflowToolNode(t *testing.T) {
	// The Workflow Tool is the second node type that calls a workflow, and it
	// is the one the confinement did not know: the mint and the check switched
	// on the Execute Sub-workflow type alone while the activation gate already
	// knew both, so a tool node naming any workflow in the tenant saved and ran
	// unchecked with the tenant's authority. Both walks now come from one
	// enumeration, and this is the case that pins it.
	document := workflow.Document{Nodes: []workflow.Node{
		embedTestNode(WorkflowToolNodeType, "tool", map[string]any{
			"toolName":   "delegate",
			"workflowId": embedTestLocator("id", "wf_sibling"),
		}),
	}}

	issues := EmbedScopeIssues(document, embed.Confinement{})
	if len(issues) != 1 {
		t.Fatalf("issues = %v, want the tool node's target refused", issues)
	}
	if !strings.Contains(issues[0], "tool") || !strings.Contains(issues[0], "wf_sibling") {
		t.Errorf("issue %q does not name the node and the workflow it calls", issues[0])
	}

	// And the mint reads the same walk: a document that calls a workflow is
	// what a confinement derived from it allows, which is what keeps the two
	// functions inverses for this node type too.
	collected := DocumentReferences(document)
	if len(collected.Workflows) != 1 || collected.Workflows[0] != "wf_sibling" {
		t.Fatalf("workflows = %v, want wf_sibling: the tool node calls a workflow", collected.Workflows)
	}
	if inside := EmbedScopeIssues(document, collected); len(inside) != 0 {
		t.Fatalf("issues = %v, want none inside the confinement it was minted from", inside)
	}

	// A tool node whose target is an expression is unbounded for the same
	// reason an Execute Sub-workflow node's is: the value is only knowable at
	// run time.
	expression := workflow.Document{Nodes: []workflow.Node{
		embedTestNode(WorkflowToolNodeType, "tool", map[string]any{
			"workflowId": embedTestLocator("id", map[string]any{"mode": "expression", "value": "={{ $json.wf }}"}),
		}),
	}}
	if issues := EmbedScopeIssues(expression, collected); len(issues) != 1 {
		t.Fatalf("issues = %v, want the expression target refused", issues)
	}
}

func TestEmbedScopeIssuesBoundsCredentialsAndSubWorkflows(t *testing.T) {
	document := workflow.Document{Nodes: []workflow.Node{
		{
			ID: "http", Name: "http", Type: "kilasflow.httpRequest", TypeVersion: workflow.V(1),
			Credentials: map[string]string{"httpHeaderAuth": "cred_other"},
		},
		embedTestNode(ExecuteWorkflowNodeType, "call", map[string]any{
			"workflowId": embedTestLocator("id", "wf_other"),
		}),
	}}
	issues := EmbedScopeIssues(document, embed.Confinement{})
	if len(issues) != 2 {
		t.Fatalf("issues = %v, want the credential and the sub-workflow", issues)
	}
	// The order is stable even though node.Credentials is a map: a message
	// that reorders itself between runs is a message nobody can diff.
	again := EmbedScopeIssues(document, embed.Confinement{})
	for index := range issues {
		if issues[index] != again[index] {
			t.Fatalf("issue order is unstable: %v vs %v", issues, again)
		}
	}

	inside := embed.Confinement{Credentials: []string{"cred_other"}, Workflows: []string{"wf_other"}}
	if remaining := EmbedScopeIssues(document, inside); len(remaining) != 0 {
		t.Fatalf("issues = %v, want none inside the confinement", remaining)
	}
}

func TestDocumentReferencesCollectsWhatTheGraphUses(t *testing.T) {
	document := workflow.Document{Nodes: []workflow.Node{
		{
			ID: "http", Name: "http", Type: "kilasflow.httpRequest", TypeVersion: workflow.V(1),
			Credentials: map[string]string{"httpHeaderAuth": "cred_1"},
		},
		embedTestNode(DatastoreNodeType, "rows", map[string]any{
			"resource": "row", "operation": DatastoreOperationGet, "dataTableId": embedTestLocator("id", "ds_1"),
		}),
		embedTestNode(DatastoreNodeType, "byName", map[string]any{
			"resource": "row", "operation": DatastoreOperationInsert, "dataTableId": embedTestLocator("name", "Metrics"),
		}),
		embedTestNode(ExecuteWorkflowNodeType, "call", map[string]any{
			"workflowId": embedTestLocator("id", "wf_2"),
		}),
	}}

	collected := DocumentReferences(document)
	if len(collected.Credentials) != 1 || collected.Credentials[0] != "cred_1" {
		t.Errorf("credentials = %v, want cred_1", collected.Credentials)
	}
	if len(collected.Datastores) != 2 {
		t.Fatalf("datastores = %#v, want the table by id and the one by name", collected.Datastores)
	}
	if !containsExactRef(collected.Datastores, embed.DatastoreRef{ID: "ds_1"}) {
		t.Errorf("datastores = %#v, want ds_1 by id", collected.Datastores)
	}
	if !containsExactRef(collected.Datastores, embed.DatastoreRef{Name: "Metrics"}) {
		t.Errorf("datastores = %#v, want Metrics by name", collected.Datastores)
	}
	if len(collected.Workflows) != 1 || collected.Workflows[0] != "wf_2" {
		t.Errorf("workflows = %v, want wf_2", collected.Workflows)
	}

	// And what it collects is exactly what the same document is allowed to
	// reference: the two functions are inverses, which is what lets a
	// confinement be derived from a trusted revision.
	if issues := EmbedScopeIssues(document, collected); len(issues) != 0 {
		t.Fatalf("a document's own references were refused: %v", issues)
	}
}

func TestDocumentReferencesIgnoresWhatItCannotProve(t *testing.T) {
	document := workflow.Document{Nodes: []workflow.Node{
		embedTestNode(DatastoreNodeType, "rows", map[string]any{
			"resource": "row", "operation": DatastoreOperationGet,
			"dataTableId": embedTestLocator("id", map[string]any{"mode": "expression", "value": "={{ $json.t }}"}),
		}),
		embedTestNode(ExecuteWorkflowNodeType, "call", map[string]any{
			"workflowId": embedTestLocator("id", ""),
		}),
	}}
	collected := DocumentReferences(document)
	if !collected.Empty() {
		t.Fatalf("collected = %#v, want nothing: an unprovable reference must not widen a confinement", collected)
	}
}

func TestEmbedScopeIssuesAcceptsAnEmptyDocument(t *testing.T) {
	if issues := EmbedScopeIssues(workflow.Document{}, embed.Confinement{}); len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

func containsExactRef(refs []embed.DatastoreRef, want embed.DatastoreRef) bool {
	for _, ref := range refs {
		if ref == want {
			return true
		}
	}
	return false
}

// The refusal text has to name the node, because an integrator reads it out of
// a host application's console.
func TestEmbedScopeIssuesNamesTheNodeItRefused(t *testing.T) {
	document := workflow.Document{Nodes: []workflow.Node{
		{
			ID: "n1", Name: "Collect", Type: "kilasflow.httpRequest", TypeVersion: workflow.V(1),
			Credentials: map[string]string{"httpHeaderAuth": "cred_1"},
		},
	}}
	issues := EmbedScopeIssues(document, embed.Confinement{})
	if len(issues) != 1 || !strings.Contains(issues[0], "Collect") || !strings.Contains(issues[0], "cred_1") {
		t.Fatalf("issues = %v, want the node name and the credential", issues)
	}
}
