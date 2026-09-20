package nodes

import (
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/embed"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
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
	// A tool is read-only, so the table binding is the whole of its authority.
	if issues := EmbedScopeIssues(document, allowed); len(issues) != 0 {
		t.Fatalf("issues = %v, want none inside the confinement", issues)
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
