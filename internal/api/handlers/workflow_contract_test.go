package handlers

import (
	"reflect"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func contractDocument(trigger *workflow.Node, settings map[string]any) workflow.Document {
	document := workflow.Document{Name: "Lookup", Settings: settings}
	if trigger != nil {
		document.Nodes = append(document.Nodes, *trigger)
	}
	return document
}

func declaredTrigger() *workflow.Node {
	return &workflow.Node{
		ID: "entry", Name: "When Executed by Another Workflow", Type: nodes.ExecuteWorkflowTriggerType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{
			"inputSource": "fields",
			"workflowInputs": map[string]any{"values": []any{
				map[string]any{"name": "email", "type": "string"},
				map[string]any{"name": "limit", "type": "number"},
				map[string]any{"name": "tags", "type": "array"},
				map[string]any{"name": "extra", "type": "any"},
			}},
		},
	}
}

// The contract describes the active revision when there is one, with the
// declared fields as a typed, open JSON Schema object and the declared output.
func TestContractOfDescribesTheActiveRevision(t *testing.T) {
	output := map[string]any{"type": "object", "properties": map[string]any{"found": map[string]any{"type": "boolean"}}}
	stored := workflow.StoredWorkflow{
		ID: "wf_1", Name: "Lookup", Active: true,
		LatestVersion: workflow.Version{ID: "ver_2", Document: contractDocument(nil, nil)},
		ActiveVersion: &workflow.Version{ID: "ver_1", Document: contractDocument(declaredTrigger(), map[string]any{"outputSchema": output})},
	}
	contract := contractOf(stored)
	if contract.VersionID != "ver_1" || contract.Source != "active" {
		t.Fatalf("described %s (%s), want the active ver_1", contract.VersionID, contract.Source)
	}
	if contract.Entry == nil || contract.Entry.NodeID != "entry" {
		t.Fatalf("entry = %+v, want the sub-workflow trigger", contract.Entry)
	}
	wantInput := map[string]any{
		"type": "object", "additionalProperties": true,
		"properties": map[string]any{
			"email": map[string]any{"type": "string"},
			"limit": map[string]any{"type": "number"},
			"tags":  map[string]any{"type": "array", "items": map[string]any{}},
			"extra": map[string]any{},
		},
	}
	if !contract.Input.Declared || !reflect.DeepEqual(contract.Input.Schema, wantInput) {
		t.Fatalf("input = %+v, want %v", contract.Input, wantInput)
	}
	if !contract.Output.Declared || !reflect.DeepEqual(contract.Output.Schema, output) {
		t.Fatalf("output = %+v, want the declared outputSchema", contract.Output)
	}
}

// An inactive workflow is described from its latest draft; a passthrough
// trigger and a missing outputSchema are undeclared, permissive objects; a
// workflow with no sub-workflow trigger has no entry.
func TestContractOfUndeclaredSides(t *testing.T) {
	passthrough := declaredTrigger()
	passthrough.Parameters = map[string]any{"inputSource": "passthrough"}
	for name, stored := range map[string]workflow.StoredWorkflow{
		"passthrough draft": {ID: "wf_2", LatestVersion: workflow.Version{ID: "ver_9", Document: contractDocument(passthrough, nil)}},
		"no trigger":        {ID: "wf_3", LatestVersion: workflow.Version{ID: "ver_1", Document: contractDocument(nil, map[string]any{"outputSchema": "not an object"})}},
	} {
		t.Run(name, func(t *testing.T) {
			contract := contractOf(stored)
			if contract.Source != "draft" || contract.VersionID != stored.LatestVersion.ID {
				t.Fatalf("described %s (%s), want the latest draft", contract.VersionID, contract.Source)
			}
			permissive := map[string]any{"type": "object"}
			if contract.Input.Declared || !reflect.DeepEqual(contract.Input.Schema, permissive) {
				t.Errorf("input = %+v, want undeclared and permissive", contract.Input)
			}
			if contract.Output.Declared || !reflect.DeepEqual(contract.Output.Schema, permissive) {
				t.Errorf("output = %+v, want undeclared and permissive", contract.Output)
			}
		})
	}
	if contract := contractOf(workflow.StoredWorkflow{LatestVersion: workflow.Version{Document: contractDocument(nil, nil)}}); contract.Entry != nil {
		t.Errorf("entry = %+v, want none for a workflow without a sub-workflow trigger", contract.Entry)
	}
}
