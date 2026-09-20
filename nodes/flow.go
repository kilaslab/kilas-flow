package nodes

import (
	"context"
	"fmt"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/conditions"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The flow-control family's server-owned bindings.
const (
	SwitchExecutorID = "core.switch"
	FilterExecutorID = "core.filter"
	LimitExecutorID  = "core.limit"
	NoOpExecutorID   = "core.noOp"
)

// The flow-control family's node types.
const (
	SwitchNodeType = "kilasflow.switch"
	FilterNodeType = "kilasflow.filter"
	LimitNodeType  = "kilasflow.limit"
	NoOpNodeType   = "kilasflow.noOp"
)

// MaxSwitchOutputs bounds how many branches one Switch may declare.
//
// A safety limit rather than a preference: the rules come from a document, and
// a document claiming ten thousand outputs would build ten thousand ports
// before anything looked at them.
const MaxSwitchOutputs = 64

// switchNode routes each item to the branch its rules choose.
//
// Its outputs come from its own rules, which is why it needs PortsFor: a fixed
// maximum would show dead ports on the canvas and refuse to import a workflow
// with one rule more than the maximum.
func switchNode() node.Definition {
	return node.Definition{
		Type:        SwitchNodeType,
		Version:     workflow.V(1),
		DisplayName: "Switch",
		Description: "Routes each item to the first rule it matches, or to a fallback.",
		Category:    "Flow",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:git-branch"},
		IconColor:   "#8b5cf6",
		Subtitle:    "{{ $parameter.mode }}",
		Inputs:      mainInput(),
		// The static list is what the picker shows for a node nobody has
		// configured yet: one branch, before any rule exists.
		Outputs: []workflow.Port{{Name: "0", DisplayName: "Rule 1", Kind: workflow.ConnectionMain}},
		Parameters: []node.PropertyDefinition{
			{
				Key: "rules", Label: "Routing Rules", Kind: node.PropertyJSON, Required: true,
				Description: "One rule per output, each `{conditions, outputKey}`. The first match wins unless Send to All Matching Outputs is on.",
			},
			{
				Key: "fallbackOutput", Label: "Fallback Output", Kind: node.PropertyOptions, Default: "none",
				Options: []node.PropertyOption{
					{Label: "None — drop unmatched items", Value: "none"},
					{Label: "Extra output", Value: "extra"},
				},
				Description: "Where an item that matched no rule goes. Without one it is dropped, which is what n8n does.",
			},
			{
				Key: "allMatchingOutputs", Label: "Send to All Matching Outputs", Kind: node.PropertyBoolean, Default: false,
				Description: "Send an item to every rule it matches rather than only the first.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     SwitchExecutorID,
		Validate:       validateSwitchConfiguration,
		PortsFor:       switchPorts,
	}
}

// switchRule is one branch.
type switchRule struct {
	filter conditions.Filter
	name   string
}

// switchRules reads the rules parameter.
func switchRules(parameters map[string]any, item workflow.Item) ([]switchRule, error) {
	list, ok := parameters["rules"].([]any)
	if !ok {
		// n8n nests them under `values`; both forms are accepted so an import
		// needs no rewriting.
		if wrapper, wrapped := parameters["rules"].(map[string]any); wrapped {
			list, ok = wrapper["values"].([]any)
		}
		if !ok {
			return nil, fmt.Errorf("switch rules must be a list")
		}
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("a switch needs at least one rule")
	}
	if len(list) > MaxSwitchOutputs {
		return nil, fmt.Errorf("a switch may declare at most %d rules", MaxSwitchOutputs)
	}

	rules := make([]switchRule, 0, len(list))
	for index, entry := range list {
		declared, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("switch rule %d is not an object", index)
		}
		filter, err := readFilter(declared["conditions"], item)
		if err != nil {
			return nil, fmt.Errorf("switch rule %d: %w", index, err)
		}
		name := textOf(declared["outputKey"])
		if name == "" {
			name = textOf(declared["renameOutput"])
		}
		rules = append(rules, switchRule{filter: filter, name: name})
	}
	return rules, nil
}

// switchPorts is the node's declared ports for one configuration.
func switchPorts(parameters map[string]any, _ workflow.TypeVersion) ([]workflow.Port, []workflow.Port) {
	inputs := mainInput()
	rules, err := switchRules(parameters, workflow.Item{})
	if err != nil {
		// An unreadable rule set still has to produce *some* ports, or the
		// compiler reports "no such port" for every connection instead of the
		// one error that explains it.
		return inputs, []workflow.Port{{Name: "0", DisplayName: "Rule 1", Kind: workflow.ConnectionMain}}
	}

	outputs := make([]workflow.Port, 0, len(rules)+1)
	for index, rule := range rules {
		label := rule.name
		if label == "" {
			label = fmt.Sprintf("Rule %d", index+1)
		}
		// The port *name* is the index as a string. n8n identifies an output
		// positionally, and a renamed output has to keep landing on the same
		// wire — so the label is what moves and the identity is what does not.
		outputs = append(outputs, workflow.Port{
			Name: fmt.Sprintf("%d", index), DisplayName: label, Kind: workflow.ConnectionMain,
		})
	}
	if textOf(parameters["fallbackOutput"]) == "extra" {
		outputs = append(outputs, workflow.Port{
			Name: fmt.Sprintf("%d", len(rules)), DisplayName: "Fallback", Kind: workflow.ConnectionMain,
		})
	}
	return inputs, outputs
}

func validateSwitchConfiguration(n workflow.Node) error {
	_, err := switchRules(n.Parameters, workflow.Item{})
	return err
}

// executeSwitch routes each item to the branches its rules choose.
func executeSwitch(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Every declared port gets a stream, empty where nothing matched. Returning
	// only the matched branch aborts the whole execution on the runner's output
	// arity check, and an empty stream is what tells the runner not to run the
	// branch below it.
	output := make(workflow.NodeOutput, len(ir.Definition.Outputs))
	for index := range output {
		output[index] = []workflow.Item{}
	}

	fallback := textOf(ir.Parameters["fallbackOutput"]) == "extra"
	all := ir.Parameters["allMatchingOutputs"] == true
	for index, item := range input["main"] {
		resolved, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		rules, err := switchRules(resolved, item)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}

		matched := false
		for ruleIndex, rule := range rules {
			passed, err := conditions.Evaluate(rule.filter)
			if err != nil {
				return nil, fmt.Errorf("node %q rule %d: %w", ir.Name, ruleIndex, err)
			}
			if !passed {
				continue
			}
			matched = true
			if ruleIndex < len(output) {
				output[ruleIndex] = append(output[ruleIndex], routedItem(ir, item, index))
			}
			if !all {
				break
			}
		}
		// An unmatched item is dropped unless a fallback output exists, which
		// is what n8n does — silently, and this node says so in the parameter's
		// own description rather than inventing a branch.
		if !matched && fallback && len(rules) < len(output) {
			output[len(rules)] = append(output[len(rules)], routedItem(ir, item, index))
		}
	}
	return output, nil
}

// routedItem keeps an item's provenance across a branch.
//
// A routing node changes the item count per port, so the runner cannot infer
// where an item came from — each one keeps the origin it arrived with, which is
// what makes a reach-back from either branch land on the right item.
func routedItem(ir workflow.IRNode, item workflow.Item, index int) workflow.Item {
	routed := cloneItem(item)
	if routed.Paired == nil {
		routed.Paired = &workflow.PairedItem{SourceNodeID: ir.ID, SourcePort: "main", ItemIndex: index}
	}
	return routed
}

// filterNode keeps the items its conditions match.
func filterNode() node.Definition {
	return node.Definition{
		Type:        FilterNodeType,
		Version:     workflow.V(1),
		DisplayName: "Filter",
		Description: "Passes on only the items whose conditions match.",
		Category:    "Flow",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:filter"},
		IconColor:   "#8b5cf6",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{{
			Key: "conditions", Label: "Conditions", Kind: node.PropertyConditions, Required: true,
			Description: "Only items matching these conditions are passed on.",
		}},
		SharedSettings: sharedSettings(),
		ExecutorID:     FilterExecutorID,
		Validate:       validateFilterConfiguration,
	}
}

func validateFilterConfiguration(n workflow.Node) error {
	_, err := readFilter(n.Parameters["conditions"], workflow.Item{})
	return err
}

func executeFilter(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	kept := []workflow.Item{}
	for index, item := range input["main"] {
		resolved, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		filter, err := readFilter(resolved["conditions"], item)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		passed, err := conditions.Evaluate(filter)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		if passed {
			kept = append(kept, routedItem(ir, item, index))
		}
	}
	return workflow.NodeOutput{kept}, nil
}

// limitNode truncates a stream.
func limitNode() node.Definition {
	return node.Definition{
		Type:        LimitNodeType,
		Version:     workflow.V(1),
		DisplayName: "Limit",
		Description: "Passes on at most a fixed number of items.",
		Category:    "Flow",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:scissors"},
		IconColor:   "#8b5cf6",
		Subtitle:    "{{ $parameter.maxItems }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "maxItems", Label: "Max Items", Kind: node.PropertyNumber, Default: 1,
				Description: "How many items to keep.",
			},
			{
				Key: "keep", Label: "Keep", Kind: node.PropertyOptions, Default: "firstItems",
				Options: []node.PropertyOption{
					{Label: "First Items", Value: "firstItems"},
					{Label: "Last Items", Value: "lastItems"},
				},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     LimitExecutorID,
		Validate:       validateLimitConfiguration,
	}
}

func validateLimitConfiguration(n workflow.Node) error {
	if max, ok := numericParameter(n.Parameters, "maxItems"); ok && max < 1 {
		return fmt.Errorf("maxItems must be at least 1")
	}
	switch keep := textOf(n.Parameters["keep"]); keep {
	case "", "firstItems", "lastItems":
		return nil
	default:
		return fmt.Errorf("keep %q is not supported", keep)
	}
}

func executeLimit(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := cloneItems(input["main"])
	maximum := 1
	if declared, ok := numericParameter(ir.Parameters, "maxItems"); ok {
		maximum = int(declared)
	}
	if maximum < 0 {
		maximum = 0
	}
	if len(items) > maximum {
		if textOf(ir.Parameters["keep"]) == "lastItems" {
			items = items[len(items)-maximum:]
		} else {
			items = items[:maximum]
		}
	}
	return workflow.NodeOutput{items}, nil
}

// noOpNode passes its input through unchanged.
//
// It looks pointless and is not: n8n workflows use it as a join point and as a
// labelled marker on the canvas, and without it a single No Op made an entire
// imported workflow unactivatable.
func noOpNode() node.Definition {
	return node.Definition{
		Type:           NoOpNodeType,
		Version:        workflow.V(1),
		DisplayName:    "No Operation",
		Description:    "Passes items through unchanged. Useful as a join point or a label.",
		Category:       "Flow",
		Group:          []node.NodeGroup{node.GroupTransform},
		Icon:           &node.NodeIcon{Light: "builtin:circle-help"},
		IconColor:      "#64748b",
		Inputs:         mainInput(),
		Outputs:        mainOutput(),
		Parameters:     []node.PropertyDefinition{},
		SharedSettings: sharedSettings(),
		ExecutorID:     NoOpExecutorID,
	}
}

func executeNoOp(ctx context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return workflow.NodeOutput{cloneItems(input["main"])}, nil
}

// mergeInputName is the port name for one of Merge's inputs.
func mergeInputName(index int) string { return fmt.Sprintf("input%d", index+1) }

// mergePorts gives Merge as many inputs as it was told to take.
func mergePorts(parameters map[string]any, _ workflow.TypeVersion) ([]workflow.Port, []workflow.Port) {
	count := 2
	if declared, ok := numericParameter(parameters, "numberInputs"); ok && declared >= 2 {
		count = int(declared)
	}
	if count > MaxMergeInputs {
		count = MaxMergeInputs
	}
	inputs := make([]workflow.Port, 0, count)
	for index := 0; index < count; index++ {
		inputs = append(inputs, workflow.Port{
			Name: mergeInputName(index), DisplayName: fmt.Sprintf("Input %d", index+1),
			Kind: workflow.ConnectionMain,
		})
	}
	return inputs, mainOutput()
}

// MaxMergeInputs bounds how many streams one Merge may take, for the same
// reason a Switch's rules are bounded: the number comes from a document.
const MaxMergeInputs = 32

// mergeStreams is every input's items, in port order.
func mergeStreams(ir workflow.IRNode, input workflow.NodeInput) [][]workflow.Item {
	streams := make([][]workflow.Item, 0, len(ir.Definition.Inputs))
	for _, port := range ir.Definition.Inputs {
		if port.Kind != workflow.ConnectionMain {
			continue
		}
		streams = append(streams, cloneItems(input[port.Name]))
	}
	return streams
}

// Merge's modes, as n8n names them.
const (
	MergeAppend       = "append"
	MergeCombine      = "combine"
	MergeChooseBranch = "chooseBranch"
	MergeCombineAll   = "combineAll"
	MergeByFields     = "combineByFields"
	MergeByPosition   = "combineByPosition"
)

func validateMergeMode(n workflow.Node) error {
	mode := textOf(n.Parameters["mode"])
	switch mode {
	case "", MergeAppend, MergeChooseBranch, MergeCombineAll, MergeByFields, MergeByPosition:
	case MergeCombine:
		// n8n's `combine` names a family, not a mode: which one is decided by
		// `combineBy`. Reading it here keeps the imported value working
		// unchanged rather than making the importer rewrite it.
		switch textOf(n.Parameters["combineBy"]) {
		case "", "combineByFields", "combineByPosition", "combineAll":
		default:
			return fmt.Errorf("combineBy %q is not supported", n.Parameters["combineBy"])
		}
	default:
		return fmt.Errorf("mode %q is not supported", mode)
	}
	if count, ok := numericParameter(n.Parameters, "numberInputs"); ok {
		if count < 2 {
			return fmt.Errorf("numberInputs must be at least 2")
		}
		if count > MaxMergeInputs {
			return fmt.Errorf("numberInputs must not exceed %d", MaxMergeInputs)
		}
	}
	if textOf(n.Parameters["mode"]) == MergeByFields && strings.TrimSpace(textOf(n.Parameters["fieldsToMatch"])) == "" {
		return fmt.Errorf("combining by fields needs at least one field to match on")
	}
	return nil
}
