package handlers

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// WorkflowContract describes a workflow as something to call: the input it
// expects and the output it declares, as JSON Schema a host's agent can hand a
// model as a tool definition.
type WorkflowContract struct {
	WorkflowID string `json:"workflowId"`
	Name       string `json:"name"`
	Active     bool   `json:"active"`
	// VersionID is the revision described: the active one when the workflow
	// is active, which is what a call runs, otherwise the latest draft.
	VersionID string `json:"versionId"`
	// Source is "active" or "draft", naming which revision VersionID is.
	Source string `json:"source" enum:"active,draft"`
	// Entry is the sub-workflow trigger a call starts from, or null when the
	// workflow has none and cannot be called as a sub-workflow or tool.
	Entry  *WorkflowContractEntry `json:"entry"`
	Input  WorkflowContractSchema `json:"input"`
	Output WorkflowContractSchema `json:"output"`
}

// WorkflowContractEntry names the node a call starts from.
type WorkflowContractEntry struct {
	NodeID   string `json:"nodeId"`
	NodeName string `json:"nodeName"`
	NodeType string `json:"nodeType"`
}

// WorkflowContractSchema is one side of the contract.
type WorkflowContractSchema struct {
	// Declared reports whether the workflow states this side. An undeclared
	// side carries the permissive schema `{"type": "object"}`.
	Declared bool           `json:"declared"`
	Schema   map[string]any `json:"schema"`
}

type workflowContractOutput struct {
	Body WorkflowContract
}

// registerContract mounts get-workflow-contract.
func (handler *Workflows) registerContract(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-workflow-contract", Method: http.MethodGet, Path: "/workflows/{id}/contract",
		Summary: "Describe a workflow's input and output contract",
		Description: "Returns the JSON Schema of the input the workflow's \"When Executed by Another Workflow\" trigger declares " +
			"(its expected fields) and of the output the workflow declares in `settings.outputSchema`, for the revision a call " +
			"would run: the active one, or the latest draft when the workflow is not active. A side the workflow does not " +
			"declare is `{\"type\": \"object\"}` with `declared: false`. Declared fields are a check, never a filter, so the " +
			"input schema allows additional properties.",
		Tags: []string{"Workflows"},
	}, handler.Contract)
}

// Contract answers a workflow's input and output contract.
func (handler *Workflows) Contract(ctx context.Context, input *workflowPathInput) (*workflowContractOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	stored, err := handler.workflows.Get(ctx, handler.tenant(ctx), input.ID)
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	return &workflowContractOutput{Body: contractOf(stored)}, nil
}

// contractOf builds the contract of the revision a call would run.
func contractOf(stored workflow.StoredWorkflow) WorkflowContract {
	version, source := stored.LatestVersion, "draft"
	if stored.Active && stored.ActiveVersion != nil {
		version, source = *stored.ActiveVersion, "active"
	}
	contract := WorkflowContract{
		WorkflowID: stored.ID, Name: stored.Name, Active: stored.Active,
		VersionID: version.ID, Source: source,
		Input:  WorkflowContractSchema{Schema: permissiveObject()},
		Output: WorkflowContractSchema{Schema: permissiveObject()},
	}
	if trigger, found := nodes.SubworkflowTriggerOf(version.Document); found {
		contract.Entry = &WorkflowContractEntry{NodeID: trigger.ID, NodeName: trigger.Name, NodeType: trigger.Type}
		if inputs, defines := nodes.DeclaredWorkflowInputs(trigger); defines {
			contract.Input = WorkflowContractSchema{Declared: true, Schema: nodes.WorkflowInputsSchema(inputs)}
		}
	}
	if declared, ok := version.Document.Settings["outputSchema"].(map[string]any); ok && len(declared) > 0 {
		contract.Output = WorkflowContractSchema{Declared: true, Schema: declared}
	}
	return contract
}

func permissiveObject() map[string]any {
	return map[string]any{"type": "object"}
}
