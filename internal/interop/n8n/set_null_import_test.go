package n8n_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// BUG-p3j233: n8n's Set has no conversion for its `null` assignment type, so
// the value is written exactly as it arrives — template 5170 ("Learn JSON
// basics") stores `{}` under it and gets an object. The importer carried the
// row as a string assignment, which wrote the text "{}", while the report said
// it wrote null.

// nullTypedSetFixture is the shape of template 5170's "Null" and "Final Exam"
// Set nodes: one null-typed row holding a literal, one holding an expression.
const nullTypedSetFixture = `{
  "name": "Learn JSON basics",
  "nodes": [
    {"id":"t","name":"Start","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
    {"id":"null","name":"Null","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[220,0],
     "parameters":{"options":{},"assignments":{"assignments":[
       {"id":"e87952cb","name":"json_example_null","type":"null","value":{}}]}}},
    {"id":"exam","name":"Final Exam","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[440,0],
     "parameters":{"options":{},"assignments":{"assignments":[
       {"id":"abcde","name":"summary_null","type":"null","value":"={{ $('Null').item.json.json_example_null }}"}]}}}
  ],
  "connections": {
    "Start": {"main": [[{"node":"Null","type":"main","index":0}]]},
    "Null": {"main": [[{"node":"Final Exam","type":"main","index":0}]]}
  }
}`

func TestANullTypedSetRowKeepsItsValue(t *testing.T) {
	t.Parallel()

	result := importFixture(t, nullTypedSetFixture)

	// The literal is carried as the type that holds it unchanged.
	literal := assignmentRows(t, nodeByName(result.Document, "Null"))["json_example_null"]
	if literal["type"] != "object" || !reflect.DeepEqual(literal["value"], map[string]any{}) {
		t.Errorf("literal row = %#v, want an object row holding {}", literal)
	}
	// An expression's result is not known until it runs, so its row declares
	// no type, which writes the value as it arrives — what n8n's null type does.
	expression := assignmentRows(t, nodeByName(result.Document, "Final Exam"))["summary_null"]
	if declared, _ := expression["type"].(string); declared != "" {
		t.Errorf("expression row type = %#v, want no declared type", expression["type"])
	}

	// The report says what happens, and never that the row writes null.
	reported := 0
	for _, issue := range result.Unsupported {
		if !strings.Contains(issue.Reason, "null type") {
			continue
		}
		reported++
		if strings.Contains(issue.Reason, "holding null") || strings.Contains(issue.Reason, "string assignment") {
			t.Errorf("issue = %+v, want the reason to describe the carried row", issue)
		}
		switch issue.NodeName {
		case "Null":
			if !strings.Contains(issue.Reason, "object") {
				t.Errorf("issue = %+v, want the object row named", issue)
			}
		case "Final Exam":
			if !strings.Contains(issue.Reason, "no declared type") {
				t.Errorf("issue = %+v, want the untyped row named", issue)
			}
		}
	}
	if reported != 2 {
		t.Errorf("unsupported = %#v, want one note per null-typed row", result.Unsupported)
	}

	// Run both rows: each writes an object, not the text "{}".
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, _ := executors.Lookup("core.set")
	run := func(name string, input map[string]any) map[string]any {
		t.Helper()
		imported := nodeByName(result.Document, name)
		// `$('Null')` needs a run; the test reads the same value off the item.
		parameters := imported.Parameters
		if name == "Final Exam" {
			parameters = replaceExpression(parameters, "{{ $json.json_example_null }}")
		}
		output, err := executor.Execute(context.Background(), workflow.IRNode{
			ID: imported.ID, Name: imported.Name, Type: imported.Type, TypeVersion: imported.TypeVersion,
			Parameters: parameters,
		}, workflow.NodeInput{"main": {{JSON: input}}}, engine.Request{})
		if err != nil {
			t.Fatalf("Execute(%s) error = %v", name, err)
		}
		return output[0][0].JSON
	}
	first := run("Null", map[string]any{})
	if !reflect.DeepEqual(first["json_example_null"], map[string]any{}) {
		t.Errorf("json_example_null = %#v, want the object {}", first["json_example_null"])
	}
	second := run("Final Exam", first)
	if !reflect.DeepEqual(second["summary_null"], map[string]any{}) {
		t.Errorf("summary_null = %#v, want the object {}", second["summary_null"])
	}

	// And back out: the untyped row is n8n's null type again.
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range exported.Document.Nodes {
		if node.Name != "Final Exam" {
			continue
		}
		wrapper, _ := node.Parameters["assignments"].(map[string]any)
		rows, _ := wrapper["assignments"].([]any)
		row, _ := rows[0].(map[string]any)
		if row["type"] != "null" {
			t.Errorf("exported row = %#v, want n8n's null type", row)
		}
	}
}

// replaceExpression rewrites the one row's expression of an imported Set.
func replaceExpression(parameters map[string]any, template string) map[string]any {
	wrapper, _ := parameters["assignments"].(map[string]any)
	rows, _ := wrapper["assignments"].([]any)
	row, _ := rows[0].(map[string]any)
	rewritten := map[string]any{}
	for key, value := range row {
		rewritten[key] = value
	}
	rewritten["value"] = map[string]any{"mode": "expression", "value": template}
	copied := map[string]any{}
	for key, value := range parameters {
		copied[key] = value
	}
	copied["assignments"] = map[string]any{"assignments": []any{rewritten}}
	return copied
}
