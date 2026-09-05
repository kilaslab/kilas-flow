package nodes

import (
	"context"
	"fmt"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/expression"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// The workflow-composition family.
const (
	ExecuteWorkflowNodeType    = "kilasflow.executeWorkflow"
	ExecuteWorkflowTriggerType = "kilasflow.executeWorkflowTrigger"

	ExecuteWorkflowExecutorID        = "core.executeWorkflow"
	ExecuteWorkflowTriggerExecutorID = "core.executeWorkflowTrigger"
)

// Execute Workflow modes.
const (
	subworkflowWaitForCompletion = "each"
	subworkflowFireAndForget     = "fireAndForget"
)

// executeWorkflowNode calls another workflow of the same tenant.
func executeWorkflowNode() node.Definition {
	return node.Definition{
		Type:        ExecuteWorkflowNodeType,
		Version:     workflow.V(1),
		DisplayName: "Execute Sub-workflow",
		Description: "Runs another workflow of this tenant and, optionally, returns its items.",
		Category:    "Flow",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:workflow"},
		IconColor:   "#8b5cf6",
		Subtitle:    "{{ $parameter.workflowId }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "workflowId", Label: "Workflow", Kind: node.PropertyString, Required: true,
				Description: "The ID of the workflow to run. It must belong to this tenant and be active: " +
					"a run is pinned to an immutable revision, and the active one is the only revision " +
					"this server treats as the one that runs. Supports expressions.",
			},
			{
				Key: "mode", Label: "Mode", Kind: node.PropertyOptions, Default: subworkflowWaitForCompletion,
				Options: []node.PropertyOption{
					{Label: "Wait for the sub-workflow to finish", Value: subworkflowWaitForCompletion},
					{Label: "Run it and continue without its output", Value: subworkflowFireAndForget},
				},
				Description: "Both run the sub-workflow to completion. The difference is whether its items " +
					"replace this node's output or the incoming items pass through unchanged.",
			},
			{
				Key: "itemsPerCall", Label: "Send", Kind: node.PropertyOptions, Default: "allItems",
				Options: []node.PropertyOption{
					{Label: "All incoming items in one run", Value: "allItems"},
					{Label: "One run per incoming item", Value: "eachItem"},
				},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     ExecuteWorkflowExecutorID,
		Validate:       validateExecuteWorkflowConfiguration,
	}
}

// executeWorkflowTrigger is where a called workflow begins.
func executeWorkflowTrigger() node.Definition {
	return node.Definition{
		Type:        ExecuteWorkflowTriggerType,
		Version:     workflow.V(1),
		DisplayName: "When Executed by Another Workflow",
		Description: "Starts this workflow when another workflow calls it, with the items it was given.",
		Category:    "Triggers",
		Group:       []node.NodeGroup{node.GroupTrigger},
		Icon:        &node.NodeIcon{Light: "builtin:workflow"},
		IconColor:   "#8b5cf6",
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "inputSource", Label: "Input", Kind: node.PropertyOptions, Default: "passthrough",
				Options: []node.PropertyOption{
					{Label: "Accept all data the caller sends", Value: "passthrough"},
					{Label: "Define the fields this workflow expects", Value: "fields"},
				},
			},
			{
				Key: "workflowInputs", Label: "Expected fields", Kind: node.PropertyFixedCollection,
				TypeOptions: &node.TypeOptions{MultipleValues: true, MultipleValueButtonText: "Add field"},
				Description: "The fields a caller is expected to send. They are documentation and a check, " +
					"never a filter: an item carrying more than this still arrives whole, because " +
					"silently dropping a caller's field is worse than receiving one nobody declared.",
				Groups: []node.PropertyGroup{{
					Key: "values", Label: "Field",
					Fields: []node.PropertyDefinition{
						{Key: "name", Label: "Name", Kind: node.PropertyString, Required: true},
						{
							Key: "type", Label: "Type", Kind: node.PropertyOptions, Default: "string",
							Options: []node.PropertyOption{
								{Label: "String", Value: "string"},
								{Label: "Number", Value: "number"},
								{Label: "Boolean", Value: "boolean"},
								{Label: "Array", Value: "array"},
								{Label: "Object", Value: "object"},
								{Label: "Any", Value: "any"},
							},
						},
					},
				}},
				VisibleWhen: []node.VisibilityCondition{{Key: "inputSource", Equals: "fields"}},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     ExecuteWorkflowTriggerExecutorID,
		Validate:       validateExecuteWorkflowTriggerConfiguration,
	}
}

func validateExecuteWorkflowConfiguration(n workflow.Node) error {
	if statementText(n.Parameters, "workflowId") == "" {
		return fmt.Errorf("a sub-workflow call needs the workflow to run")
	}
	switch mode := textParameter(n.Parameters, "mode"); mode {
	case "", subworkflowWaitForCompletion, subworkflowFireAndForget:
	default:
		return fmt.Errorf("mode %q is not supported", mode)
	}
	switch each := textParameter(n.Parameters, "itemsPerCall"); each {
	case "", "allItems", "eachItem":
	default:
		return fmt.Errorf("send mode %q is not supported", each)
	}
	return nil
}

func validateExecuteWorkflowTriggerConfiguration(n workflow.Node) error {
	switch source := textParameter(n.Parameters, "inputSource"); source {
	case "", "passthrough", "fields":
		return nil
	default:
		return fmt.Errorf("input source %q is not supported", source)
	}
}

// executeExecuteWorkflow runs the named workflow and returns what it produced.
func executeExecuteWorkflow(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request.Workflows == nil {
		return nil, fmt.Errorf("node %q: this runtime cannot run sub-workflows", ir.Name)
	}
	items := input["main"]
	if len(items) == 0 {
		items = []workflow.Item{{JSON: map[string]any{}}}
	}

	// Resolved against the first item, because the workflow being called is a
	// property of the node rather than of an item. One run per item resolves it
	// again below, which is the case where it can legitimately differ.
	parameters, err := expression.Resolve(ir.Parameters, expressionContext(items[0], input, request, 0))
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	wait := textValue(parameters["mode"], subworkflowWaitForCompletion) != subworkflowFireAndForget

	if textValue(parameters["itemsPerCall"], "allItems") != "eachItem" {
		produced, err := callSubworkflow(ctx, ir, request, parameters, items, wait)
		if err != nil {
			return nil, err
		}
		if !wait {
			// Fire and forget passes the incoming items through, so the branch
			// continues with what it had rather than with nothing.
			return workflow.NodeOutput{items}, nil
		}
		return workflow.NodeOutput{produced}, nil
	}

	out := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		perItem, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		produced, err := callSubworkflow(ctx, ir, request, perItem, []workflow.Item{item}, wait)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
		}
		if !wait {
			out = append(out, item)
			continue
		}
		out = append(out, produced...)
	}
	return workflow.NodeOutput{out}, nil
}

func callSubworkflow(ctx context.Context, ir workflow.IRNode, request engine.Request, parameters map[string]any, items []workflow.Item, wait bool) ([]workflow.Item, error) {
	target := strings.TrimSpace(textValue(parameters["workflowId"], ""))
	if target == "" {
		return nil, fmt.Errorf("node %q: a sub-workflow call needs the workflow to run", ir.Name)
	}
	result, err := request.Workflows.InvokeWorkflow(ctx, request.Execution, engine.WorkflowCall{
		WorkflowID: target, Items: items, Wait: wait,
	})
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	return result.Items, nil
}

// executeExecuteWorkflowTrigger emits the items the caller sent.
//
// The declared field list is not applied here. It describes what a caller ought
// to send, and enforcing it would mean dropping fields a caller did send —
// which turns a mismatched contract into silently missing data rather than a
// visible one.
func executeExecuteWorkflowTrigger(ctx context.Context, _ workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A call sends a list; the trigger's input carries it under `items`. A run
	// started any other way — a manual test of the sub-workflow itself — has no
	// such key and emits whatever it was given, so the workflow stays testable
	// on its own.
	if list, ok := request.Input.JSON["items"].([]any); ok {
		items := make([]workflow.Item, 0, len(list))
		for _, entry := range list {
			fields, ok := entry.(map[string]any)
			if !ok {
				fields = map[string]any{"value": entry}
			}
			items = append(items, workflow.Item{JSON: fields})
		}
		if len(items) == 0 {
			items = []workflow.Item{{JSON: map[string]any{}}}
		}
		return workflow.NodeOutput{items}, nil
	}
	return workflow.NodeOutput{{request.Input}}, nil
}
