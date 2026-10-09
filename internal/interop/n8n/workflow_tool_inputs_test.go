package n8n_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// workflowToolV2Fixture is a Workflow Tool v2.2 whose mapper holds one column
// the model defines (n8n's marker comment and backticks included), one fixed
// text, one expression, and a schema listing two more columns the author never
// touched, plus one the author removed from the form.
const workflowToolV2Fixture = `{
  "name": "Conversions",
  "nodes": [
    {"id":"a","name":"When chat message received","type":"@n8n/n8n-nodes-langchain.chatTrigger","typeVersion":1.1,"position":[0,0],"parameters":{"options":{}}},
    {"id":"b","name":"AI Agent","type":"@n8n/n8n-nodes-langchain.agent","typeVersion":1.7,"position":[220,0],"parameters":{"options":{}}},
    {"id":"w","name":"Convert","type":"@n8n/n8n-nodes-langchain.toolWorkflow","typeVersion":2.2,"position":[400,200],"parameters":{
      "name":"celsius_to_fahrenheit","description":"Converts.",
      "workflowId":{"__rl":true,"mode":"list","value":"wf_celsius","cachedResultName":"Celsius"},
      "workflowInputs":{
        "mappingMode":"defineBelow",
        "value":{
          "tempC":"={{ /*n8n-auto-generated-fromAI-override*/ $fromAI('tempC', TICKS, 'number') }}",
          "unit":"F",
          "note":"={{ $json.note }}"
        },
        "matchingColumns":[],
        "schema":[
          {"id":"tempC","displayName":"tempC","required":false,"defaultMatch":false,"display":true,"canBeUsedToMatch":true,"type":"number","removed":false},
          {"id":"roundTo","displayName":"roundTo","required":false,"defaultMatch":false,"display":true,"canBeUsedToMatch":true,"type":"number","removed":false},
          {"id":"unit","displayName":"unit","required":false,"defaultMatch":false,"display":true,"canBeUsedToMatch":true,"type":"string","removed":false},
          {"id":"note","displayName":"note","required":false,"defaultMatch":false,"display":true,"canBeUsedToMatch":true,"type":"string","removed":false},
          {"id":"payload","displayName":"payload","required":false,"defaultMatch":false,"display":true,"canBeUsedToMatch":true,"type":"object","removed":false},
          {"id":"gone","displayName":"gone","required":false,"defaultMatch":false,"display":true,"canBeUsedToMatch":true,"type":"string","removed":true}
        ],
        "attemptToConvertTypes":false,"convertFieldsToString":false}
    }}
  ],
  "connections": {
    "When chat message received": {"main": [[{"node":"AI Agent","type":"main","index":0}]]},
    "Convert": {"ai_tool": [[{"node":"AI Agent","type":"ai_tool","index":0}]]}
  }
}`

func importWorkflowToolV2(t *testing.T) (n8n.ImportResult, workflow.Node) {
	t.Helper()
	result := importFixture(t, strings.ReplaceAll(workflowToolV2Fixture, "TICKS", "``"))
	return result, nodeByName(result.Document, "Convert")
}

func TestWorkflowToolV2FromAIInputsImportAsModelDefinedColumns(t *testing.T) {
	t.Parallel()

	result, tool := importWorkflowToolV2(t)
	if tool.Type != "kilasflow.workflowTool" {
		t.Fatalf("toolWorkflow imported as %q, want kilasflow.workflowTool", tool.Type)
	}
	mapping, ok := property.ReadMapping(tool.Parameters["workflowInputs"])
	if !ok {
		t.Fatalf("workflowInputs = %#v, want a resource mapper value", tool.Parameters["workflowInputs"])
	}
	if mapping.Mode != property.MappingManual {
		t.Errorf("mappingMode = %q, want defineBelow", mapping.Mode)
	}

	// The model's column keeps its call; only n8n's marker comment is gone,
	// because this server's evaluator refuses a comment.
	tempC, _ := mapping.Values["tempC"].(map[string]any)
	template, _ := tempC["value"].(string)
	if tempC["mode"] != "expression" || strings.Contains(template, "n8n-auto-generated") || !strings.Contains(template, "$fromAI('tempC'") {
		t.Errorf("tempC = %#v, want a $fromAI expression without n8n's marker comment", mapping.Values["tempC"])
	}
	if mapping.Values["unit"] != "F" {
		t.Errorf("unit = %#v, want the fixed text kept", mapping.Values["unit"])
	}
	note, _ := mapping.Values["note"].(map[string]any)
	if note["mode"] != "expression" || note["value"] != "{{ $json.note }}" {
		t.Errorf("note = %#v, want the expression translated", mapping.Values["note"])
	}

	// Columns the author never touched are not the model's in n8n: its tool
	// schema comes from the $fromAI calls alone. They are kept as fixed nulls,
	// which here means "not offered", rather than flipping to the model.
	for _, untouched := range []string{"roundTo", "payload"} {
		value, present := mapping.Values[untouched]
		if !present || value != nil {
			t.Errorf("%s = %#v (present %v), want an explicit null so the model is not offered it", untouched, value, present)
		}
	}
	// A column the author removed from the form is not mapped in n8n, so the
	// model is not offered it there either. Here a column with no entry is the
	// model's, so it is kept as a fixed null too (it is not in the schema copy).
	if value, present := mapping.Values["gone"]; !present || value != nil {
		t.Errorf("gone = %#v (present %v), want an explicit null so the model is not offered a removed column", value, present)
	}

	// The schema copy is display data: ids, names and types, minus the removed
	// column, and nothing marking a column as a match.
	ids := make([]string, 0, len(mapping.Schema))
	for _, field := range mapping.Schema {
		ids = append(ids, field.ID+":"+field.Type)
		if field.CanBeUsedToMatch {
			t.Errorf("schema column %q can be used to match, want a workflow input to match nothing", field.ID)
		}
	}
	if got := strings.Join(ids, " "); got != "tempC:number roundTo:number unit:string note:string payload:object" {
		t.Errorf("schema copy = %s, want the live columns with their types in n8n's order", got)
	}
	for _, issue := range result.Unsupported {
		if issue.NodeName == "Convert" {
			t.Errorf("issue on the tool (%s, %s): %s", issue.Severity, issue.Field, issue.Reason)
		}
	}
}

func TestWorkflowToolMapperExportsItsEntries(t *testing.T) {
	t.Parallel()

	result, _ := importWorkflowToolV2(t)
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	var tool n8n.Node
	for _, candidate := range exported.Document.Nodes {
		if candidate.Name == "Convert" {
			tool = candidate
		}
	}
	mapper, _ := tool.Parameters["workflowInputs"].(map[string]any)
	if mapper["mappingMode"] != "defineBelow" {
		t.Fatalf("exported workflowInputs = %#v, want a defineBelow mapper", tool.Parameters["workflowInputs"])
	}
	value, _ := mapper["value"].(map[string]any)
	if _, nested := value["value"]; nested {
		t.Errorf("exported value = %#v, want the columns directly under value, never value.value", value)
	}
	const marker = "/*n8n-auto-generated-fromAI-override*/"
	// The model's column carries n8n's marker, so n8n shows "defined by the model".
	tempC, _ := value["tempC"].(string)
	if !strings.HasPrefix(tempC, "={{ "+marker+" $fromAI('tempC'") {
		t.Errorf("exported tempC = %q, want n8n's override form with the marker comment", tempC)
	}
	if value["unit"] != "F" || value["note"] != "={{ $json.note }}" {
		t.Errorf("exported value = %#v, want the fixed text and the `=`-prefixed expression", value)
	}
	if schema, _ := mapper["schema"].([]any); len(schema) != 5 {
		t.Errorf("exported schema = %#v, want the five live columns", mapper["schema"])
	}
	// Each column left as a fixed null stays fixed on the way out.
	for _, untouched := range []string{"roundTo", "payload"} {
		if got, present := value[untouched]; !present || got != nil {
			t.Errorf("exported %s = %#v (present %v), want the fixed null kept", untouched, got, present)
		}
	}
}

// import(export(x)) leaves each column in the state it was in: the model's
// stays the model's, a fixed value stays fixed, an untouched column stays out
// of the model's hands.
func TestWorkflowToolMapperSurvivesAnN8NRoundTrip(t *testing.T) {
	t.Parallel()

	first, _ := importWorkflowToolV2(t)
	exported, err := n8n.Export(first.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	encoded, err := json.Marshal(exported.Document)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	again, err := n8n.Import(encoded, registry(t))
	if err != nil {
		t.Fatalf("Import() of the exported document error = %v", err)
	}
	before, _ := property.ReadMapping(nodeByName(first.Document, "Convert").Parameters["workflowInputs"])
	after, ok := property.ReadMapping(nodeByName(again.Document, "Convert").Parameters["workflowInputs"])
	if !ok {
		t.Fatalf("re-imported workflowInputs = %#v, want a mapper", nodeByName(again.Document, "Convert").Parameters["workflowInputs"])
	}
	if !reflect.DeepEqual(before.Values, after.Values) {
		t.Errorf("columns after a round trip = %#v, want %#v", after.Values, before.Values)
	}
	if !reflect.DeepEqual(before.Schema, after.Schema) {
		t.Errorf("schema copy after a round trip = %#v, want %#v", after.Schema, before.Schema)
	}
}

// A column of the schema copy that has no entry in value is the model's here
// (a freshly picked workflow works at once), so it leaves as n8n's override.
func TestWorkflowToolMapperColumnWithNoEntryExportsAsTheModelsOwn(t *testing.T) {
	t.Parallel()

	document := workflow.Document{
		Nodes: []workflow.Node{
			{ID: "a", Name: "AI Agent", Type: "kilasflow.agent", TypeVersion: workflow.V(1), Parameters: map[string]any{"prompt": "x"}},
			{ID: "w", Name: "Convert", Type: "kilasflow.workflowTool", TypeVersion: workflow.V(1), Parameters: map[string]any{
				"toolName": "convert", "toolDescription": "Converts.",
				"workflowId": property.WriteLocator(property.Locator{Mode: "list", Value: "wf_celsius"}),
				"workflowInputs": property.WriteMapping(property.Mapping{
					Mode:   property.MappingManual,
					Values: map[string]any{"unit": "F"},
					Schema: []property.MapperField{
						{ID: "tempC", DisplayName: "tempC", Type: "number"},
						{ID: "unit", DisplayName: "unit", Type: "string"},
						{ID: "payload", DisplayName: "payload", Type: "object"},
						{ID: "ok", DisplayName: "ok", Type: "boolean"},
						{ID: "any", DisplayName: "any"},
					},
				}),
			}},
		},
	}
	exported, err := n8n.Export(document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	var value map[string]any
	for _, candidate := range exported.Document.Nodes {
		if candidate.Name == "Convert" {
			mapper, _ := candidate.Parameters["workflowInputs"].(map[string]any)
			value, _ = mapper["value"].(map[string]any)
		}
	}
	for column, want := range map[string]string{"tempC": "number", "payload": "json", "ok": "boolean", "any": "json"} {
		got, _ := value[column].(string)
		wanted := fmt.Sprintf("={{ /*n8n-auto-generated-fromAI-override*/ $fromAI('%s', ``, '%s') }}", column, want)
		if got != wanted {
			t.Errorf("exported %s = %q, want %q", column, got, wanted)
		}
	}
	if value["unit"] != "F" {
		t.Errorf("exported unit = %#v, want the fixed text", value["unit"])
	}
}

// The plain object an earlier version stored still exports the way it did.
func TestWorkflowToolPlainInputsStillExportAsTheyDid(t *testing.T) {
	t.Parallel()

	document := workflow.Document{
		Nodes: []workflow.Node{
			{ID: "w", Name: "Convert", Type: "kilasflow.workflowTool", TypeVersion: workflow.V(1), Parameters: map[string]any{
				"toolName": "convert", "toolDescription": "Converts.",
				"workflowId": "wf_celsius",
				"workflowInputs": map[string]any{
					"unit": "F",
					"note": map[string]any{"mode": "expression", "value": "{{ $json.note }}"},
				},
			}},
		},
	}
	exported, err := n8n.Export(document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	mapper, _ := exported.Document.Nodes[0].Parameters["workflowInputs"].(map[string]any)
	value, _ := mapper["value"].(map[string]any)
	if mapper["mappingMode"] != "defineBelow" || value["unit"] != "F" || value["note"] != "={{ $json.note }}" {
		t.Errorf("exported workflowInputs = %#v, want {mappingMode: defineBelow, value: {unit, note}}", mapper)
	}
	if _, wrapped := mapper["schema"]; wrapped {
		t.Errorf("exported workflowInputs = %#v, want no schema for the plain shape", mapper)
	}
}

// One expression for all the inputs has no n8n counterpart: a mapper is
// columns, and which columns it produces is only known when it runs.
func TestWorkflowToolWholeExpressionInputsAreReportedNotGarbled(t *testing.T) {
	t.Parallel()

	document := workflow.Document{
		Nodes: []workflow.Node{
			{ID: "w", Name: "Convert", Type: "kilasflow.workflowTool", TypeVersion: workflow.V(1), Parameters: map[string]any{
				"toolName": "convert", "toolDescription": "Converts.",
				"workflowId":     "wf_celsius",
				"workflowInputs": map[string]any{"mode": "expression", "value": "{{ { unit: 'F' } }}"},
			}},
		},
	}
	exported, err := n8n.Export(document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if _, carried := exported.Document.Nodes[0].Parameters["workflowInputs"]; carried {
		t.Errorf("exported workflowInputs = %#v, want it left out rather than written as a column map", exported.Document.Nodes[0].Parameters["workflowInputs"])
	}
	reported := false
	for _, issue := range exported.Lossy {
		if issue.Field == "workflowInputs" {
			reported = true
		}
	}
	if !reported {
		t.Errorf("lossy = %#v, want the dropped inputs named", exported.Lossy)
	}
}
