package nodes_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// The Workflow Tool's inputs are a resource mapper whose columns are the fields
// the picked workflow's trigger declares, and each column may be left to the
// model. The flag is opt-in: another mapper (a table's columns) has no business
// offering a column to a model.
func TestWorkflowToolDeclaresItsInputsAsAResourceMapper(t *testing.T) {
	t.Parallel()

	definition, found := aiRegistry(t).Get(nodes.WorkflowToolNodeType, workflow.V(1))
	if !found {
		t.Fatal("the Workflow Tool is not registered")
	}
	var inputs *node.PropertyDefinition
	for index := range definition.Parameters {
		if definition.Parameters[index].Key == "workflowInputs" {
			inputs = &definition.Parameters[index]
		}
	}
	if inputs == nil {
		t.Fatal("the Workflow Tool has no workflowInputs property")
	}
	if inputs.Kind != node.PropertyResourceMapper || inputs.Mapper == nil || inputs.Mapper.Schema == nil {
		t.Fatalf("workflowInputs = kind %q mapper %+v, want a resource mapper with a schema source", inputs.Kind, inputs.Mapper)
	}
	schema := inputs.Mapper.Schema
	if schema.Source != property.LoaderInternal || schema.Name != nodes.WorkflowInputsLoader ||
		fmt.Sprint(schema.DependsOn) != "[workflowId]" {
		t.Errorf("schema source = %+v, want the internal workflow inputs loader depending on workflowId", schema)
	}
	if !inputs.Mapper.ModelDefinable {
		t.Error("ModelDefinable = false, want the tool's columns to be offered to the model")
	}
	if inputs.Mapper.SupportsAutoMap || inputs.Mapper.MatchingColumnsRequired {
		t.Errorf("mapper = %+v, want a manual mapper: a workflow call matches no rows and has no item to map automatically", inputs.Mapper)
	}
}

// workflowToolWith builds the descriptor the agent receives from a Workflow
// Tool node whose workflowInputs holds the value given.
func workflowToolWith(inputs any) workflow.Item {
	parameters := map[string]any{"workflowId": "wf_celsius"}
	if inputs != nil {
		parameters["workflowInputs"] = inputs
	}
	return toolItem(map[string]any{
		"kind": "workflow", "name": "celsius_to_fahrenheit", "description": "Converts.", "nodeName": "Convert",
		"workflowId": "wf_celsius", "parameters": parameters,
	})
}

func celsiusWorkflows() *describingWorkflows {
	return &describingWorkflows{
		stubWorkflows: stubWorkflows{items: []workflow.Item{{JSON: map[string]any{"fahrenheit": 98.6}}}},
		triggers: map[string]workflow.Node{
			"wf_celsius": {ID: "start", Type: nodes.ExecuteWorkflowTriggerType, Parameters: map[string]any{
				"inputSource": "fields",
				"workflowInputs": map[string]any{"values": []any{
					map[string]any{"name": "tempC", "type": "number"},
					map[string]any{"name": "roundTo", "type": "number"},
					map[string]any{"name": "unit", "type": "string"},
					map[string]any{"name": "payload", "type": "object"},
				}},
			}},
		},
	}
}

func fromAIMarker(key, kind string) map[string]any {
	return map[string]any{"mode": "expression", "value": fmt.Sprintf("{{ $fromAI('%s', '', '%s') }}", key, kind)}
}

// runCelsiusTool lets a model call the tool once with the arguments given and
// returns the tool schema the model was offered and what the sub-workflow got.
func runCelsiusTool(t *testing.T, inputs any, arguments string) (map[string]any, map[string]any) {
	t.Helper()
	workflows := celsiusWorkflows()
	provider, offered := schemaRecordingProvider(t, []string{
		assistantToolCalls(`{"name":"celsius_to_fahrenheit","arguments":` + fmt.Sprintf("%q", arguments) + `}`),
		assistantAnswer("98.6F"),
	})
	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	output, err := executor.Execute(context.Background(), agentIR(t, map[string]any{"prompt": "Convert 37C."}), workflow.NodeInput{
		"main":  {{JSON: map[string]any{}}},
		"model": {modelItem(provider.URL)},
		"tools": {workflowToolWith(inputs)},
	}, engine.Request{
		Credentials: agentCredentials(), Workflows: workflows, Execution: agentExecution(),
		Events: func(engine.NodeEvent) {},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output[0][0].JSON["output"] != "98.6F" {
		t.Fatalf("agent output = %#v, want the final answer", output[0])
	}
	if len(workflows.calls) != 1 {
		t.Fatalf("sub-workflow calls = %#v, want one", workflows.calls)
	}
	return offered()["celsius_to_fahrenheit"], workflows.calls[0].Items[0].JSON
}

func sortedRequired(schema map[string]any) []string {
	listed, _ := schema["required"].([]any)
	names := make([]string, 0, len(listed))
	for _, name := range listed {
		names = append(names, fmt.Sprint(name))
	}
	sort.Strings(names)
	return names
}

// The stored mapper is {mappingMode, value, matchingColumns, schema}. Reading
// it as the legacy plain object would send those four keys to the sub-workflow
// as if they were inputs, and fix every declared field it did not name.
func TestWorkflowToolReadsAMapperShapedWorkflowInputs(t *testing.T) {
	t.Parallel()

	schema, sent := runCelsiusTool(t, map[string]any{
		"mappingMode": "defineBelow",
		"value": map[string]any{
			"tempC":   fromAIMarker("tempC", "number"),
			"unit":    "F",
			"payload": fromAIMarker("payload", "json"),
		},
		"matchingColumns": []any{},
		"schema": []any{
			map[string]any{"id": "tempC", "displayName": "tempC", "type": "number"},
			map[string]any{"id": "roundTo", "displayName": "roundTo", "type": "number"},
			map[string]any{"id": "unit", "displayName": "unit", "type": "string"},
			map[string]any{"id": "payload", "displayName": "payload", "type": "object"},
		},
	}, `{"tempC":37,"roundTo":1,"payload":{"a":1}}`)

	properties, _ := schema["properties"].(map[string]any)
	for name, want := range map[string]string{"tempC": "number", "roundTo": "number", "payload": "object"} {
		property, _ := properties[name].(map[string]any)
		if property["type"] != want {
			t.Errorf("tool schema %q = %#v, want type %q", name, properties[name], want)
		}
	}
	if _, offeredFixed := properties["unit"]; offeredFixed {
		t.Errorf("tool schema = %#v, want unit left out: the author fixed it", schema)
	}
	if got := fmt.Sprint(sortedRequired(schema)); got != "[payload roundTo tempC]" {
		t.Errorf("required = %s, want exactly the fields the model defines", got)
	}
	for _, key := range []string{"mappingMode", "value", "matchingColumns", "schema"} {
		if _, leaked := sent[key]; leaked {
			t.Errorf("sub-workflow input = %#v: the mapper's own key %q must never reach the sub-workflow", sent, key)
		}
	}
	if len(sent) != 4 || sent["tempC"] != float64(37) || sent["roundTo"] != float64(1) || sent["unit"] != "F" {
		t.Errorf("sub-workflow input = %#v, want {tempC, roundTo, unit:F, payload}", sent)
	}
	payload, _ := sent["payload"].(map[string]any)
	if payload["a"] != float64(1) {
		t.Errorf("payload = %#v, want the model's object", sent["payload"])
	}
}

// A fixed column is sent as written, including null: the author chose it. An
// expression in a fixed column evaluates against the call's item, not as the
// model's value.
func TestWorkflowToolMapperFixesAColumnWhateverItHolds(t *testing.T) {
	t.Parallel()

	schema, sent := runCelsiusTool(t, map[string]any{
		"mappingMode": "defineBelow",
		"value": map[string]any{
			"roundTo": nil,
			"unit":    map[string]any{"mode": "expression", "value": "{{ 'K' }}"},
			"payload": "",
		},
	}, `{"tempC":37}`)
	properties, _ := schema["properties"].(map[string]any)
	if len(properties) != 1 || properties["tempC"] == nil {
		t.Errorf("tool schema = %#v, want only tempC offered", schema)
	}
	if sent["tempC"] != float64(37) || sent["unit"] != "K" || sent["payload"] != "" {
		t.Errorf("sub-workflow input = %#v, want the model's tempC and the fixed unit and payload", sent)
	}
	if value, present := sent["roundTo"]; !present || value != nil {
		t.Errorf("sub-workflow input = %#v, want roundTo present as null", sent)
	}
}

// A mapper whose own value is one expression has no columns to read until it
// runs, which is the whole-expression case: it fixes every field, and what it
// produces is the input, not the mapper's own `mode` and `value` keys.
func TestWorkflowToolMapperWhoseValueIsAnExpressionIsTheWholeInput(t *testing.T) {
	t.Parallel()

	expression := map[string]any{"mode": "expression", "value": `{{ { "unit": "F" } }}`}
	mapper := map[string]any{"mappingMode": "defineBelow", "value": expression, "matchingColumns": []any{}}

	entries, whole, present := nodes.WorkflowToolInputs(mapper)
	if !whole || !present || len(entries) != 0 {
		t.Fatalf("WorkflowToolInputs() = (%v, whole %v, present %v), want a whole expression with no entries", entries, whole, present)
	}

	_, sent := runCelsiusTool(t, mapper, `{"input":{"tempC":37}}`)
	for _, key := range []string{"mode", "value", "mappingMode", "matchingColumns"} {
		if _, leaked := sent[key]; leaked {
			t.Errorf("sub-workflow input = %#v: the key %q must not reach the sub-workflow", sent, key)
		}
	}
	if len(sent) != 2 || sent["tempC"] != float64(37) || sent["unit"] != "F" {
		t.Errorf("sub-workflow input = %#v, want the model's tempC and the expression's unit", sent)
	}
}

// A mapper that has not been touched yet leaves every declared column to the
// model, the same as no workflowInputs at all.
func TestWorkflowToolMapperWithNoValuesOffersEveryColumn(t *testing.T) {
	t.Parallel()

	schema, _ := runCelsiusTool(t, map[string]any{"mappingMode": "defineBelow", "value": map[string]any{}, "matchingColumns": []any{}}, `{"tempC":37,"unit":"F"}`)
	properties, _ := schema["properties"].(map[string]any)
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	if got := strings.Join(names, ","); got != "payload,roundTo,tempC,unit" {
		t.Errorf("offered = %s, want all four declared columns", got)
	}
}

// The model sees the same schema on every run: map iteration order must not
// reach the tool definition.
func TestWorkflowToolSchemaRequiresFieldsInDeclaredOrder(t *testing.T) {
	t.Parallel()

	for run := 0; run < 5; run++ {
		schema, _ := runCelsiusTool(t, map[string]any{
			"mappingMode": "defineBelow",
			"value": map[string]any{
				"payload": fromAIMarker("payload", "json"),
				"tempC":   fromAIMarker("tempC", "number"),
			},
		}, `{"tempC":37,"roundTo":1,"unit":"F","payload":{}}`)
		listed, _ := schema["required"].([]any)
		if got := fmt.Sprint(listed); got != "[tempC roundTo unit payload]" {
			t.Fatalf("run %d: required = %s, want declared order", run, got)
		}
	}
}
