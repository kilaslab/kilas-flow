package nodes

import (
	"context"
	"fmt"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/expression"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// The workflow-composition family.
const (
	ExecuteWorkflowNodeType    = "kilasflow.executeWorkflow"
	ExecuteWorkflowTriggerType = "kilasflow.executeWorkflowTrigger"

	ExecuteWorkflowExecutorID        = "core.executeWorkflow"
	ExecuteWorkflowTriggerExecutorID = "core.executeWorkflowTrigger"
)

// WorkflowListLoader names the internal loader that lists a tenant's workflows.
//
// Named here rather than in the composition root so the node and the
// registration cannot drift: a loader nobody registered fails at edit time with
// "this field's option source is not available", which reads as a server
// problem rather than a missing wire.
const WorkflowListLoader = "workflows.list"

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
				Key: "workflowId", Label: "Workflow", Kind: node.PropertyResourceLocator, Required: true,
				Description: "The workflow to run. It must belong to this tenant and be active: a run is " +
					"pinned to an immutable revision, and the active one is the only revision this " +
					"server treats as the one that runs.",
				Modes: []node.PropertyMode{
					{
						Name: "list", Label: "From list", Kind: node.PropertyOptions,
						Placeholder: "Choose…",
						// Internal, not HTTP: the list comes from this process's
						// own storage, so there is no request to govern and no
						// credential to sign with.
						LoadOptions: &node.OptionsLoader{Source: property.LoaderInternal, Name: WorkflowListLoader},
					},
					{
						Name: "id", Label: "By ID", Kind: node.PropertyString,
						Placeholder: "wf_…",
						Hint:        "The KilasFlow workflow ID. Supports expressions.",
					},
				},
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
			{
				Key: "inputFields", Label: "Input fields", Kind: node.PropertyKeyValue,
				Description: "What to send the sub-workflow, evaluated in this workflow once per item — " +
					"field name to value, expressions allowed. Empty sends the incoming items unchanged, " +
					"which is what an n8n call that defines no fields sends too.",
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
	if !property.LocatorIsSet(n.Parameters["workflowId"]) {
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
		// The declared fields are evaluated once per incoming item, because
		// that is what n8n's mapper means: one resolve against the first item
		// would send that item's values for all of them.
		sent, err := mappedSubworkflowItems(ir, items, input, request, parameters)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		produced, err := callSubworkflow(ctx, ir, request, parameters, sent, wait)
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
		produced, err := callSubworkflow(ctx, ir, request, perItem, projectSubworkflowInput(perItem, []workflow.Item{item}), wait)
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

// mappedSubworkflowItems evaluates the declared input fields per incoming item.
//
// The fields are resolved from the node's raw parameters rather than from the
// already-resolved tree, so each item is mapped with its own `$json` — which is
// the whole point of the mapping.
func mappedSubworkflowItems(ir workflow.IRNode, items []workflow.Item, input workflow.NodeInput, request engine.Request, resolved map[string]any) ([]workflow.Item, error) {
	if fields, ok := resolved["inputFields"].(map[string]any); !ok || len(fields) == 0 {
		return items, nil
	}
	out := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		fields, err := expression.Resolve(
			map[string]any{"inputFields": ir.Parameters["inputFields"]},
			expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("input fields: %w", err)
		}
		out = append(out, projectSubworkflowInput(fields, []workflow.Item{item})...)
	}
	return out, nil
}

// projectSubworkflowInput replaces each item with the fields the caller
// declared.
//
// Declared keys only, and a declared key whose expression resolved to nothing
// arrives as null: n8n's mapper builds the item from the mapping, so a field
// the caller's expression could not produce is present and empty rather than
// missing, and a downstream `$json.x` reads null instead of failing to resolve.
// An undeclared key the caller's own items carried is not sent — the mapping is
// the contract, and passing the raw item through alongside it would send the
// very fields the mapping exists to replace.
func projectSubworkflowInput(parameters map[string]any, items []workflow.Item) []workflow.Item {
	fields, _ := parameters["inputFields"].(map[string]any)
	if len(fields) == 0 {
		return items
	}
	out := make([]workflow.Item, 0, len(items))
	for range items {
		projected := make(map[string]any, len(fields))
		for name, value := range fields {
			projected[name] = value
		}
		out = append(out, workflow.Item{JSON: projected})
	}
	return out
}

// WorkflowCall is one node in a document that names another workflow to run.
type WorkflowCall struct {
	// Node is the calling node itself, so a refusal can name it.
	Node workflow.Node
	// Target is the workflow the node names, read exactly as the executor will
	// read it: the plain text of the locator. Empty when the node names nothing
	// yet.
	Target string
	// Expression reports a locator whose value is only knowable at run time.
	Expression bool
}

// WorkflowCalls lists every node in a document that names another workflow to
// run.
//
// This is the one enumeration of "a node that calls a workflow", and it exists
// because there is more than one gate that has to know: activation refuses a
// call to a workflow that is not active, and an embed session's confinement is
// both minted from (DocumentReferences) and checked against (EmbedScopeIssues)
// the same reading. Two node types call another workflow — Execute Sub-workflow
// and the Workflow Tool an agent uses — and both name their target with the same
// locator key.
//
// When these walks were separate the Workflow Tool was missing from the
// confinement, so a guest editor could save a tool node naming any workflow in
// the tenant and run it with the tenant's authority. A node type added here is
// now a node type every gate knows about, which is the point.
func WorkflowCalls(document workflow.Document) []WorkflowCall {
	calls := make([]WorkflowCall, 0, 2)
	for _, node := range document.Nodes {
		if node.Type != ExecuteWorkflowNodeType && node.Type != WorkflowToolNodeType {
			continue
		}
		locator, _ := property.ReadLocator(node.Parameters["workflowId"])
		calls = append(calls, WorkflowCall{
			Node:       node,
			Target:     strings.TrimSpace(textValue(locator.Value, "")),
			Expression: property.ExpressionMarker(locator.Value),
		})
	}
	return calls
}

// SubworkflowCalls reads the workflows a document calls.
//
// For activation: a workflow whose document calls a workflow that is not active
// would activate cleanly and fail mid-run, so the store refuses it and this is
// the reading it refuses on.
//
// A node whose locator holds nothing is skipped rather than reported: the
// compiler already refuses an unset required locator at save time, and a
// half-built draft must not be the thing that blocks an unrelated activation.
func SubworkflowCalls(document workflow.Document) []repository.SubworkflowCall {
	var calls []repository.SubworkflowCall
	for _, call := range WorkflowCalls(document) {
		if call.Target == "" {
			continue
		}
		calls = append(calls, repository.SubworkflowCall{
			NodeID: call.Node.ID, NodeName: call.Node.Name, WorkflowID: call.Target,
		})
	}
	return calls
}

func callSubworkflow(ctx context.Context, ir workflow.IRNode, request engine.Request, parameters map[string]any, items []workflow.Item, wait bool) ([]workflow.Item, error) {
	// The locator's own value, never the object: the executor wants the
	// workflow ID, and a mode is how the user found it rather than part of it.
	locator, _ := property.ReadLocator(parameters["workflowId"])
	target := strings.TrimSpace(textValue(locator.Value, ""))
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
// visible one. n8n behaves the same way: its trigger hands the caller's items
// through, and the caller's own field mapping (imported onto the Execute
// Sub-workflow node) is what shapes them.
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
