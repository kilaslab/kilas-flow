package nodes_test

import (
	"fmt"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// The editor's column list and the model's tool schema must read a trigger the
// same way, so both go through one parser.
func TestDeclaredWorkflowInputsReadsTheTriggersFields(t *testing.T) {
	t.Parallel()

	trigger := workflow.Node{ID: "start", Type: nodes.ExecuteWorkflowTriggerType, Parameters: map[string]any{
		"inputSource": "fields",
		"workflowInputs": map[string]any{"values": []any{
			map[string]any{"name": " tempC ", "type": "number"},
			map[string]any{"name": "", "type": "string"},
			map[string]any{"name": "note"},
			map[string]any{"name": "payload", "type": "object"},
		}},
	}}
	inputs, declared := nodes.DeclaredWorkflowInputs(trigger)
	if !declared {
		t.Fatal("declared = false, want a trigger that defines its fields to declare them")
	}
	if got := fmt.Sprint(inputs); got != "[{tempC number} {note string} {payload object}]" {
		t.Errorf("inputs = %s, want the named fields in declared order, an untyped one as string", got)
	}

	passthrough := workflow.Node{ID: "start", Type: nodes.ExecuteWorkflowTriggerType, Parameters: map[string]any{"inputSource": "passthrough"}}
	if inputs, declared := nodes.DeclaredWorkflowInputs(passthrough); declared || len(inputs) != 0 {
		t.Errorf("a passthrough trigger = (%v, %v), want nothing declared", inputs, declared)
	}
}

func TestSubworkflowTriggerOfPicksTheFirstSubworkflowTrigger(t *testing.T) {
	t.Parallel()

	document := workflow.Document{Nodes: []workflow.Node{
		{ID: "hook", Type: "kilasflow.webhook"},
		{ID: "first", Type: nodes.ExecuteWorkflowTriggerType},
		{ID: "second", Type: nodes.ExecuteWorkflowTriggerType},
	}}
	trigger, found := nodes.SubworkflowTriggerOf(document)
	if !found || trigger.ID != "first" {
		t.Errorf("SubworkflowTriggerOf() = (%q, %v), want the first sub-workflow trigger", trigger.ID, found)
	}
	if _, found := nodes.SubworkflowTriggerOf(workflow.Document{Nodes: []workflow.Node{{ID: "hook", Type: "kilasflow.webhook"}}}); found {
		t.Error("SubworkflowTriggerOf() found a trigger in a workflow with none")
	}
}
