package nodes_test

import (
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// Both node types that call another workflow are read, and nothing else is.
//
// Activation refuses a document whose call target is not active, and this is
// the reading it refuses on: an Execute Sub-workflow node and the Workflow Tool
// an agent uses name their target with the same locator, and a document that
// calls nothing has to read as calling nothing — a false positive here would
// block an activation for a workflow that runs fine.
func TestSubworkflowCallsReadsBothCallingNodeTypes(t *testing.T) {
	t.Parallel()

	document := workflow.Document{Nodes: []workflow.Node{
		{ID: "call", Name: "Run target", Type: nodes.ExecuteWorkflowNodeType,
			Parameters: map[string]any{"workflowId": map[string]any{"__rl": true, "value": "wf_a", "mode": "list"}}},
		{ID: "tool", Name: "Workflow tool", Type: nodes.WorkflowToolNodeType,
			Parameters: map[string]any{"workflowId": map[string]any{"__rl": true, "value": "wf_b", "mode": "id"}}},
		{ID: "plain", Name: "Plain target", Type: nodes.ExecuteWorkflowNodeType,
			Parameters: map[string]any{"workflowId": "wf_c"}},
		{ID: "set", Name: "Set", Type: "kilasflow.set", Parameters: map[string]any{}},
		// A locator with nothing in it is a half-built draft, not a call: the
		// compiler refuses the unset required parameter at save time, and
		// reporting it here would block an activation for a different reason.
		{ID: "unset", Name: "Unset call", Type: nodes.ExecuteWorkflowNodeType, Parameters: map[string]any{}},
	}}

	calls := nodes.SubworkflowCalls(document)
	if len(calls) != 3 {
		t.Fatalf("calls = %#v, want the three calling nodes", calls)
	}
	byNode := map[string]string{}
	for _, call := range calls {
		byNode[call.NodeID] = call.WorkflowID
		if call.NodeName == "" {
			t.Errorf("call from node %q carries no name: the refusal could not say which node to fix", call.NodeID)
		}
	}
	if byNode["call"] != "wf_a" {
		t.Errorf("Execute Sub-workflow target = %q, want wf_a", byNode["call"])
	}
	if byNode["tool"] != "wf_b" {
		t.Errorf("Workflow Tool target = %q, want wf_b", byNode["tool"])
	}
	// A plain string is the other shape a locator takes, and the executor reads
	// it, so the activation check has to see it too.
	if byNode["plain"] != "wf_c" {
		t.Errorf("plain-string target = %q, want wf_c", byNode["plain"])
	}
}
