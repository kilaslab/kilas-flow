package nodes_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

func flowExecutor(t *testing.T, id string) engine.Executor {
	t.Helper()
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlGuard(), nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, found := executors.Lookup(id)
	if !found {
		t.Fatalf("executor %q is not registered", id)
	}
	return executor
}

func flowNode(t *testing.T, nodeType string, parameters map[string]any) workflow.IRNode {
	t.Helper()
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Lookup(nodeType, workflow.V(1))
	if !found {
		t.Fatalf("%s is not registered", nodeType)
	}
	// A compiled node carries the ports its own configuration produces.
	if catalogued, ok := registry.Get(nodeType, workflow.V(1)); ok && catalogued.PortsFor != nil {
		definition.Inputs, definition.Outputs = catalogued.PortsFor(parameters, workflow.V(1))
	}
	return workflow.IRNode{
		ID: "flow-1", Name: nodeType, Type: nodeType, TypeVersion: workflow.V(1),
		Parameters: parameters, Definition: definition,
	}
}

// stringRule is one Switch rule over a field of the item.
func stringRule(field, operation, value, outputKey string) map[string]any {
	rule := map[string]any{"conditions": map[string]any{
		"combinator": "and",
		"options":    map[string]any{"caseSensitive": true},
		"conditions": []any{map[string]any{
			"leftValue":  map[string]any{"mode": "expression", "value": "{{ $json." + field + " }}"},
			"operator":   map[string]any{"type": "string", "operation": operation},
			"rightValue": value,
		}},
	}}
	if outputKey != "" {
		rule["outputKey"] = outputKey
	}
	return rule
}

// A Switch's outputs come from its own rules. A fixed maximum would show dead
// ports on the canvas and refuse a workflow with one rule more than the
// maximum.
func TestSwitchDeclaresOneOutputPerRuleAndNamesThem(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Get(nodes.SwitchNodeType, workflow.V(1))
	if definition.PortsFor == nil {
		t.Fatal("Switch declares no PortsFor, so its outputs cannot follow its rules")
	}

	parameters := map[string]any{"rules": []any{
		stringRule("tier", "equals", "gold", "VIP"),
		stringRule("tier", "equals", "silver", ""),
	}}
	_, outputs := definition.PortsFor(parameters, workflow.V(1))
	if len(outputs) != 2 {
		t.Fatalf("outputs = %#v, want one per rule", outputs)
	}
	// The port *name* is the index: n8n identifies an output positionally, and
	// a renamed output has to keep landing on the same wire.
	if outputs[0].Name != "0" || outputs[0].DisplayName != "VIP" {
		t.Errorf("first output = %#v, want the index as its name and the rule's label", outputs[0])
	}
	if outputs[1].Name != "1" || outputs[1].DisplayName != "Rule 2" {
		t.Errorf("second output = %#v, want a default label", outputs[1])
	}

	parameters["fallbackOutput"] = "extra"
	_, withFallback := definition.PortsFor(parameters, workflow.V(1))
	if len(withFallback) != 3 || withFallback[2].DisplayName != "Fallback" {
		t.Fatalf("outputs = %#v, want a fallback port last", withFallback)
	}
}

func TestSwitchRoutesToTheFirstMatchingRule(t *testing.T) {
	t.Parallel()

	parameters := map[string]any{"rules": []any{
		stringRule("tier", "equals", "gold", ""),
		stringRule("tier", "equals", "silver", ""),
	}}
	output, err := flowExecutor(t, nodes.SwitchExecutorID).Execute(context.Background(),
		flowNode(t, nodes.SwitchNodeType, parameters),
		workflow.NodeInput{"main": {
			{JSON: map[string]any{"tier": "gold", "name": "a"}},
			{JSON: map[string]any{"tier": "silver", "name": "b"}},
			{JSON: map[string]any{"tier": "bronze", "name": "c"}},
		}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(output) != 2 {
		t.Fatalf("output = %d ports, want one per rule", len(output))
	}
	if len(output[0]) != 1 || output[0][0].JSON["name"] != "a" {
		t.Fatalf("first branch = %#v, want the gold item", output[0])
	}
	if len(output[1]) != 1 || output[1][0].JSON["name"] != "b" {
		t.Fatalf("second branch = %#v, want the silver item", output[1])
	}
	// An item matching nothing is dropped, which is what n8n does. Every port
	// still gets a stream — returning fewer aborts the whole execution on the
	// runner's arity check.
	for index, items := range output {
		if len(items) > 1 {
			t.Fatalf("port %d carried %d items", index, len(items))
		}
	}
}

func TestSwitchSendsToEveryMatchingOutputWhenAsked(t *testing.T) {
	t.Parallel()

	parameters := map[string]any{
		"allMatchingOutputs": true,
		"rules": []any{
			stringRule("tier", "equals", "gold", ""),
			stringRule("name", "equals", "a", ""),
		},
	}
	output, err := flowExecutor(t, nodes.SwitchExecutorID).Execute(context.Background(),
		flowNode(t, nodes.SwitchNodeType, parameters),
		workflow.NodeInput{"main": {{JSON: map[string]any{"tier": "gold", "name": "a"}}}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output[0]) != 1 || len(output[1]) != 1 {
		t.Fatalf("output = %#v, want the item on both matching branches", output)
	}
}

func TestSwitchSendsUnmatchedItemsToTheFallbackWhenThereIsOne(t *testing.T) {
	t.Parallel()

	parameters := map[string]any{
		"fallbackOutput": "extra",
		"rules":          []any{stringRule("tier", "equals", "gold", "")},
	}
	output, err := flowExecutor(t, nodes.SwitchExecutorID).Execute(context.Background(),
		flowNode(t, nodes.SwitchNodeType, parameters),
		workflow.NodeInput{"main": {
			{JSON: map[string]any{"tier": "gold"}},
			{JSON: map[string]any{"tier": "bronze"}},
		}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output) != 2 {
		t.Fatalf("output = %d ports, want the rule and the fallback", len(output))
	}
	if len(output[1]) != 1 || output[1][0].JSON["tier"] != "bronze" {
		t.Fatalf("fallback = %#v, want the unmatched item", output[1])
	}
}

func TestFilterKeepsOnlyMatchingItems(t *testing.T) {
	t.Parallel()

	parameters := map[string]any{"conditions": map[string]any{
		"combinator": "and",
		"options":    map[string]any{"caseSensitive": true},
		"conditions": []any{map[string]any{
			"leftValue":  map[string]any{"mode": "expression", "value": "{{ $json.n }}"},
			"operator":   map[string]any{"type": "number", "operation": "gt"},
			"rightValue": float64(2),
		}},
	}}
	output, err := flowExecutor(t, nodes.FilterExecutorID).Execute(context.Background(),
		flowNode(t, nodes.FilterNodeType, parameters),
		workflow.NodeInput{"main": {
			{JSON: map[string]any{"n": float64(1)}},
			{JSON: map[string]any{"n": float64(3)}},
			{JSON: map[string]any{"n": float64(5)}},
		}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output[0]) != 2 {
		t.Fatalf("kept %d items, want the two above the threshold", len(output[0]))
	}
	// A Filter that keeps nothing still returns one empty stream, which is what
	// tells the runner not to run the branch below it.
	empty, err := flowExecutor(t, nodes.FilterExecutorID).Execute(context.Background(),
		flowNode(t, nodes.FilterNodeType, parameters),
		workflow.NodeInput{"main": {{JSON: map[string]any{"n": float64(0)}}}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(empty) != 1 || len(empty[0]) != 0 {
		t.Fatalf("output = %#v, want one empty stream", empty)
	}
}

func TestLimitKeepsFirstOrLastItems(t *testing.T) {
	t.Parallel()

	items := workflow.NodeInput{"main": {
		{JSON: map[string]any{"n": float64(1)}},
		{JSON: map[string]any{"n": float64(2)}},
		{JSON: map[string]any{"n": float64(3)}},
	}}
	for name, testCase := range map[string]struct {
		parameters map[string]any
		want       []float64
	}{
		"first two":           {map[string]any{"maxItems": float64(2)}, []float64{1, 2}},
		"last two":            {map[string]any{"maxItems": float64(2), "keep": "lastItems"}, []float64{2, 3}},
		"more than there are": {map[string]any{"maxItems": float64(10)}, []float64{1, 2, 3}},
	} {
		t.Run(name, func(t *testing.T) {
			output, err := flowExecutor(t, nodes.LimitExecutorID).Execute(context.Background(),
				flowNode(t, nodes.LimitNodeType, testCase.parameters), items, engine.Request{})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			got := make([]float64, 0, len(output[0]))
			for _, item := range output[0] {
				got = append(got, item.JSON["n"].(float64))
			}
			if len(got) != len(testCase.want) {
				t.Fatalf("kept %v, want %v", got, testCase.want)
			}
			for index := range got {
				if got[index] != testCase.want[index] {
					t.Fatalf("kept %v, want %v", got, testCase.want)
				}
			}
		})
	}
}

// No Op looks pointless and is not: n8n workflows use it as a join point, and
// without it a single one made an entire imported workflow unactivatable.
func TestNoOpPassesItemsThrough(t *testing.T) {
	t.Parallel()

	output, err := flowExecutor(t, nodes.NoOpExecutorID).Execute(context.Background(),
		flowNode(t, nodes.NoOpNodeType, map[string]any{}),
		workflow.NodeInput{"main": {{JSON: map[string]any{"a": float64(1)}}}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output[0]) != 1 || output[0][0].JSON["a"] != float64(1) {
		t.Fatalf("output = %#v, want the item unchanged", output[0])
	}
}

func mergeIR(t *testing.T, parameters map[string]any) workflow.IRNode {
	t.Helper()
	return flowNode(t, "kilasflow.merge", parameters)
}

func TestMergeCombinesStreamsEveryWayItAdvertises(t *testing.T) {
	t.Parallel()

	left := []workflow.Item{
		{JSON: map[string]any{"id": float64(1), "name": "Ada"}},
		{JSON: map[string]any{"id": float64(2), "name": "Grace"}},
	}
	right := []workflow.Item{
		{JSON: map[string]any{"id": float64(1), "role": "engineer"}},
		{JSON: map[string]any{"id": float64(3), "role": "admiral"}},
	}
	input := workflow.NodeInput{"input1": left, "input2": right}
	executor := flowExecutor(t, "core.merge")

	t.Run("append concatenates", func(t *testing.T) {
		output, err := executor.Execute(context.Background(), mergeIR(t, map[string]any{"mode": "append"}), input, engine.Request{})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if len(output[0]) != 4 {
			t.Fatalf("appended %d items, want all four", len(output[0]))
		}
	})

	t.Run("by position pairs the nth of each", func(t *testing.T) {
		output, err := executor.Execute(context.Background(), mergeIR(t, map[string]any{"mode": "combineByPosition"}), input, engine.Request{})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if len(output[0]) != 2 {
			t.Fatalf("combined %d items, want the shorter stream's length", len(output[0]))
		}
		if output[0][0].JSON["name"] != "Ada" || output[0][0].JSON["role"] != "engineer" {
			t.Fatalf("first pair = %#v, want both sides", output[0][0].JSON)
		}
	})

	t.Run("by fields joins on equal values", func(t *testing.T) {
		output, err := executor.Execute(context.Background(),
			mergeIR(t, map[string]any{"mode": "combineByFields", "fieldsToMatch": "id"}), input, engine.Request{})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		// keepMatches is the default: only the row both sides have.
		if len(output[0]) != 1 || output[0][0].JSON["role"] != "engineer" {
			t.Fatalf("joined = %#v, want only the matching row", output[0])
		}
	})

	t.Run("by fields keeping everything", func(t *testing.T) {
		output, err := executor.Execute(context.Background(),
			mergeIR(t, map[string]any{"mode": "combineByFields", "fieldsToMatch": "id", "joinMode": "keepEverything"}),
			input, engine.Request{})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if len(output[0]) != 3 {
			t.Fatalf("joined %d items, want the match plus both unmatched", len(output[0]))
		}
	})

	t.Run("combine all is a cross join", func(t *testing.T) {
		output, err := executor.Execute(context.Background(), mergeIR(t, map[string]any{"mode": "combineAll"}), input, engine.Request{})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if len(output[0]) != 4 {
			t.Fatalf("crossed %d items, want two by two", len(output[0]))
		}
	})

	t.Run("choose branch keeps one side", func(t *testing.T) {
		output, err := executor.Execute(context.Background(),
			mergeIR(t, map[string]any{"mode": "chooseBranch", "chooseBranch": float64(2)}), input, engine.Request{})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if len(output[0]) != 2 || output[0][0].JSON["role"] != "engineer" {
			t.Fatalf("chosen = %#v, want the second input", output[0])
		}
	})

	t.Run("a branch outside the inputs is named", func(t *testing.T) {
		_, err := executor.Execute(context.Background(),
			mergeIR(t, map[string]any{"mode": "chooseBranch", "chooseBranch": float64(9)}), input, engine.Request{})
		if err == nil || !strings.Contains(err.Error(), "outside") {
			t.Fatalf("Execute() error = %v, want the branch named", err)
		}
	})
}

// Merge's inputs come from its own configuration: a fixed pair cannot import a
// workflow that merged three streams.
func TestMergeDeclaresAsManyInputsAsItWasToldToTake(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Get("kilasflow.merge", workflow.V(1))
	if definition.PortsFor == nil {
		t.Fatal("Merge declares no PortsFor")
	}
	inputs, _ := definition.PortsFor(map[string]any{"numberInputs": float64(3)}, workflow.V(1))
	if len(inputs) != 3 || inputs[2].Name != "input3" {
		t.Fatalf("inputs = %#v, want three named ports", inputs)
	}
	// An unconfigured node still shows the pair the picker advertises.
	defaults, _ := definition.PortsFor(map[string]any{}, workflow.V(1))
	if len(defaults) != 2 {
		t.Fatalf("inputs = %#v, want the default pair", defaults)
	}
}

// The condition shape IF stored before this family shared an evaluator still
// runs, unchanged.
func TestIFStillRunsTheOldFlatConditionShape(t *testing.T) {
	t.Parallel()

	output, err := flowExecutor(t, "core.if").Execute(context.Background(),
		flowNode(t, "kilasflow.if", map[string]any{"conditions": []any{
			map[string]any{"field": "customer.tier", "operator": "equals", "value": "vip"},
		}}),
		workflow.NodeInput{"main": {
			{JSON: map[string]any{"customer": map[string]any{"tier": "vip"}}},
			{JSON: map[string]any{"customer": map[string]any{"tier": "standard"}}},
		}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output[0]) != 1 || len(output[1]) != 1 {
		t.Fatalf("output = %#v, want one item on each branch", output)
	}
}

// And the rich shape, with several conditions — which the old IF could not
// express at all.
func TestIFEvaluatesSeveralConditionsWithACombinator(t *testing.T) {
	t.Parallel()

	conditionsFor := func(combinator string) map[string]any {
		return map[string]any{"conditions": map[string]any{
			"combinator": combinator,
			"options":    map[string]any{"caseSensitive": true},
			"conditions": []any{
				map[string]any{
					"leftValue":  map[string]any{"mode": "expression", "value": "{{ $json.tier }}"},
					"operator":   map[string]any{"type": "string", "operation": "equals"},
					"rightValue": "vip",
				},
				map[string]any{
					"leftValue":  map[string]any{"mode": "expression", "value": "{{ $json.spend }}"},
					"operator":   map[string]any{"type": "number", "operation": "gt"},
					"rightValue": float64(100),
				},
			},
		}}
	}
	items := workflow.NodeInput{"main": {
		{JSON: map[string]any{"tier": "vip", "spend": float64(500)}},
		{JSON: map[string]any{"tier": "vip", "spend": float64(5)}},
	}}

	both, err := flowExecutor(t, "core.if").Execute(context.Background(),
		flowNode(t, "kilasflow.if", conditionsFor("and")), items, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(both[0]) != 1 || len(both[1]) != 1 {
		t.Fatalf("and = %#v, want the two items parted", both)
	}

	either, err := flowExecutor(t, "core.if").Execute(context.Background(),
		flowNode(t, "kilasflow.if", conditionsFor("or")), items, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(either[0]) != 2 {
		t.Fatalf("or = %#v, want both items on the true branch", either)
	}
}
