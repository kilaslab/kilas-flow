package nodes_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// The Google Sheets tool is one row of the tool-form table. These tests pin
// what is specific to it: which operations a model can be offered, which keys
// stay the author's, and that it never writes the agent's input item as a row.

func sheetsBaseNode(t *testing.T) node.Definition {
	t.Helper()
	definitions, _, _ := toolFormCatalogue(t, safehttp.DefaultPolicy())
	base, found := definitions.Get(nodes.GoogleSheetsNodeType, workflow.V(1))
	if !found {
		t.Fatalf("%s is not registered", nodes.GoogleSheetsNodeType)
	}
	return base
}

func sheetsToolNode(t *testing.T) node.Definition {
	t.Helper()
	definitions, _, _ := toolFormCatalogue(t, safehttp.DefaultPolicy())
	tool, found := definitions.Get(nodes.GoogleSheetsToolNodeType, workflow.V(1))
	if !found {
		t.Fatalf("%s is not registered", nodes.GoogleSheetsToolNodeType)
	}
	return tool
}

// sheetsToolParams is a tool's parameters aimed at Sheet1 of the stub.
func sheetsToolParams(operation string, extra map[string]any) map[string]any {
	parameters := sheetsParams(operation, extra)
	parameters["toolDescription"] = "Works with the orders sheet."
	return parameters
}

func modelValue(key, description string) map[string]any {
	return formExpression("{{ $fromAI('" + key + "', '" + description + "') }}")
}

// manualColumns maps each column by hand, the model supplying the values.
func manualColumns(matching string, values map[string]any) map[string]any {
	if matching == "" {
		return columnsParam("defineBelow", values)
	}
	return columnsParam("defineBelow", values, matching)
}

func autoColumns(matching ...string) map[string]any {
	stored := columnsParam("autoMapInputData", map[string]any{}, matching...)
	stored["schema"] = []any{
		map[string]any{"id": "name", "displayName": "name", "type": "string"},
		map[string]any{"id": "status", "displayName": "status", "type": "string"},
	}
	return stored
}

func TestTheGoogleSheetsToolOffersReadAppendUpdateAndDeleteButNotClear(t *testing.T) {
	t.Parallel()

	tool := sheetsToolNode(t)
	values := map[string]any{"name": modelValue("name", "who"), "status": modelValue("status", "the state")}
	accepted := map[string]map[string]any{
		"read":           sheetsToolParams("read", nil),
		"append":         sheetsToolParams("append", map[string]any{"columns": manualColumns("", values)}),
		"appendOrUpdate": sheetsToolParams("appendOrUpdate", map[string]any{"columns": manualColumns("name", values)}),
		"update":         sheetsToolParams("update", map[string]any{"columns": manualColumns("name", values)}),
		"delete": sheetsToolParams("delete", map[string]any{
			"toDelete": "rows", "numberToDelete": float64(1), "startIndex": modelValue("row", "the row number"),
		}),
		// n8n leaves its default out of the file, and the default is read.
		"default operation": formWithout(sheetsToolParams("read", nil), "operation"),
	}
	for name, parameters := range accepted {
		if err := tool.Validate(workflow.Node{Parameters: parameters}); err != nil {
			t.Errorf("Validate(%s) = %v", name, err)
		}
	}

	// Clear empties a whole tab or range in one call, with no row to bound it.
	for _, kind := range []string{"wholeSheet", "specificRows", "specificColumns", "specificRange"} {
		err := tool.Validate(workflow.Node{Parameters: sheetsToolParams("clear", map[string]any{"clear": kind})})
		if err == nil || !strings.Contains(err.Error(), "clear") {
			t.Errorf("Validate(clear %s) = %v, want a refusal naming clear", kind, err)
		}
	}
	// The ordinary node still clears.
	if err := sheetsBaseNode(t).Validate(workflow.Node{Parameters: sheetsParams("clear", map[string]any{"clear": "wholeSheet"})}); err != nil {
		t.Errorf("the ordinary node refused clear: %v", err)
	}
}

func TestTheGoogleSheetsToolKeepsItsTargetAndItsColumnsTheAuthors(t *testing.T) {
	t.Parallel()

	tool := sheetsToolNode(t)
	values := map[string]any{"name": modelValue("name", "who")}
	read := sheetsToolParams("read", nil)
	update := sheetsToolParams("update", map[string]any{"columns": manualColumns("name", values)})
	remove := sheetsToolParams("delete", map[string]any{"toDelete": "rows", "numberToDelete": float64(1)})
	withColumns := func(change func(columns map[string]any)) map[string]any {
		columns := manualColumns("name", map[string]any{"name": modelValue("name", "who")})
		change(columns)
		return formWith(update, "columns", columns)
	}
	filter := func(column any) map[string]any {
		return formWith(read, "filtersUI", map[string]any{"values": []any{map[string]any{
			"lookupColumn": column, "lookupValue": modelValue("order", "the order"),
		}}})
	}

	for name, test := range map[string]struct {
		parameters map[string]any
		names      string
	}{
		"model-chosen spreadsheet":      {formWith(read, "documentId", modelValue("doc", "the spreadsheet")), "documentId"},
		"model-chosen spreadsheet id":   {formWith(read, "documentId", sheetsLocator("id", modelValue("doc", "the id"))), "documentId"},
		"computed spreadsheet":          {formWith(read, "documentId", sheetsExpr("{{ $json.sheet }}")), "documentId"},
		"template text in the tab":      {formWith(read, "sheetName", sheetsLocator("name", "{{ $json.tab }}")), "sheetName"},
		"model-chosen tab":              {formWith(read, "sheetName", modelValue("tab", "the tab")), "sheetName"},
		"model-chosen options":          {formWith(read, "options", map[string]any{"dataLocationOnSheet": modelValue("where", "where")}), "options"},
		"computed combine":              {formWith(read, "combineFilters", sheetsExpr("{{ $json.mode }}")), "combineFilters"},
		"model-chosen delete kind":      {formWith(remove, "toDelete", modelValue("kind", "rows or columns")), "toDelete"},
		"model-chosen delete count":     {formWith(remove, "numberToDelete", modelValue("count", "how many")), "numberToDelete"},
		"model-chosen filter column":    {filter(modelValue("column", "the column")), "lookupColumn"},
		"computed filter column":        {filter(sheetsExpr("{{ $json.column }}")), "lookupColumn"},
		"template text filter column":   {filter("{{ $json.column }}"), "lookupColumn"},
		"model-chosen matching column":  {withColumns(func(c map[string]any) { c["matchingColumns"] = []any{modelValue("key", "the key")} }), "matchingColumns"},
		"model-chosen mapping mode":     {withColumns(func(c map[string]any) { c["mappingMode"] = modelValue("mode", "the mode") }), "mappingMode"},
		"model-written columns":         {formWith(update, "columns", modelValue("columns", "the whole mapping")), "columns"},
		"template text in a column key": {withColumns(func(c map[string]any) { c["value"] = map[string]any{"{{ $json.c }}": "x"} }), "column"},
	} {
		err := tool.Validate(workflow.Node{Parameters: test.parameters})
		if err == nil || !strings.Contains(err.Error(), test.names) {
			t.Errorf("Validate(%s) = %v, want a refusal naming %s", name, err, test.names)
		}
	}

	// What the model does choose: values, and where a delete starts.
	for name, parameters := range map[string]map[string]any{
		"a filter value": filter("Order ID"),
		"a column value": withColumns(func(map[string]any) {}),
		"a delete start": formWith(remove, "startIndex", modelValue("row", "the row number")),
	} {
		if err := tool.Validate(workflow.Node{Parameters: parameters}); err != nil {
			t.Errorf("Validate(%s) = %v", name, err)
		}
	}
}

func TestTheGoogleSheetsToolRefusesAutomaticColumnMapping(t *testing.T) {
	t.Parallel()

	tool := sheetsToolNode(t)
	for _, operation := range []string{"append", "appendOrUpdate", "update"} {
		parameters := sheetsToolParams(operation, map[string]any{"columns": autoColumns("name")})
		err := tool.Validate(workflow.Node{Parameters: parameters})
		if err == nil || !strings.Contains(err.Error(), "automatic") {
			t.Errorf("Validate(%s, automatic mapping) = %v, want a refusal naming automatic mapping", operation, err)
		}
	}
	// The ordinary node maps automatically, and the tool's read has no mapping.
	if err := sheetsBaseNode(t).Validate(workflow.Node{Parameters: sheetsParams("append", map[string]any{"columns": autoColumns()})}); err != nil {
		t.Errorf("the ordinary node refused automatic mapping: %v", err)
	}
	if err := tool.Validate(workflow.Node{Parameters: sheetsToolParams("read", map[string]any{"columns": autoColumns()})}); err != nil {
		t.Errorf("Validate(read with a stored mapping) = %v", err)
	}
}

// runAsTool is one tool call: the model supplied fromAI, and nothing else
// differs from a step.
func (setup *sheetsSetup) runAsTool(parameters map[string]any, items []workflow.Item, fromAI map[string]any) ([]workflow.Item, error) {
	if items == nil {
		items = sheetItems(map[string]any{})
	}
	request := setup.request
	request.FromAIArguments = fromAI
	output, err := setup.executor.Execute(context.Background(), setup.ir(parameters), workflow.NodeInput{"main": items}, request)
	if err != nil {
		return nil, err
	}
	return output[0], nil
}

// The executor refuses again at run time, before it calls Google, because a
// descriptor is data and the agent hands the executor whatever it holds.
func TestTheGoogleSheetsExecutorRefusesAutomaticMappingAndClearWhenRunAsATool(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.peopleTab()
	conversation := sheetItems(map[string]any{"chatInput": "hello", "sessionId": "s1", "name": "Mallory"})

	for _, operation := range []string{"append", "appendOrUpdate", "update"} {
		_, err := setup.runAsTool(sheetsParams(operation, map[string]any{"columns": autoColumns("name")}), conversation, map[string]any{})
		if err == nil || !strings.Contains(err.Error(), "automatic") {
			t.Errorf("%s as a tool, automatic mapping: error = %v, want it refused", operation, err)
		}
	}
	// A mode that an expression chooses is refused as written, before it can resolve.
	chosen := sheetsParams("append", map[string]any{"columns": map[string]any{
		"mappingMode": sheetsExpr("{{ 'autoMapInputData' }}"), "value": map[string]any{}, "matchingColumns": []any{},
	}})
	if _, err := setup.runAsTool(chosen, conversation, map[string]any{}); err == nil || !strings.Contains(err.Error(), "mappingMode") {
		t.Errorf("a computed automatic mapping: error = %v, want it refused", err)
	}
	if _, err := setup.runAsTool(sheetsParams("clear", map[string]any{"clear": "wholeSheet"}), conversation, map[string]any{}); err == nil ||
		!strings.Contains(err.Error(), "clear") {
		t.Errorf("clear as a tool: error = %v, want it refused", err)
	}
	if log := setup.fake.callLog(); log != "" {
		t.Fatalf("Google was called before the refusal:\n%s", log)
	}

	// The same item as an ordinary step is written.
	if _, err := setup.runErr(sheetsParams("append", map[string]any{"columns": autoColumns()}), conversation); err != nil {
		t.Fatalf("the ordinary step failed: %v", err)
	}
	if rows := setup.fake.snapshot("Sheet1"); len(rows) != 6 || rows[5][0] != "Mallory" {
		t.Errorf("the ordinary step did not append the item: %v", rows)
	}
}

// An empty sheet has no column names for a tool to map by. The ordinary node
// writes the item's fields as the header there; a tool run must not, because
// its item is the conversation.
func TestAToolRunNeverWritesTheAgentsItemAsAHeaderRow(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.fake.addTab(0, "Sheet1", 20)
	conversation := sheetItems(map[string]any{"chatInput": "hello", "sessionId": "s1"})
	manual := sheetsParams("append", map[string]any{"columns": manualColumns("", map[string]any{"name": "Eve"})})
	appendOrUpdate := sheetsParams("appendOrUpdate", map[string]any{"columns": manualColumns("name", map[string]any{"name": "Eve"})})

	for name, parameters := range map[string]map[string]any{"append": manual, "appendOrUpdate": appendOrUpdate} {
		_, err := setup.runAsTool(parameters, conversation, map[string]any{})
		if err == nil || !strings.Contains(err.Error(), "no column names") {
			t.Errorf("%s as a tool on an empty sheet: error = %v, want the missing header named", name, err)
		}
	}
	expectGrid(t, setup)

	// As a step, the first rows of an empty sheet define its columns.
	if _, err := setup.runErr(manual, sheetItems(map[string]any{"name": "Eve"})); err != nil {
		t.Fatalf("the ordinary step failed: %v", err)
	}
	expectGrid(t, setup, sheetsRow("name"), sheetsRow("Eve"))
}

// A model-chosen row number must not reach the column names. n8n lets a sheet
// node address any row; an assistant that can overwrite or delete row 1 can
// break every later lookup, so a tool call stays below the header.
func TestAToolRunLeavesTheHeaderRowAlone(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.peopleTab()
	byRow := func(row any) map[string]any {
		return sheetsParams("update", map[string]any{"columns": manualColumns("row_number", map[string]any{
			"row_number": row, "status": "closed",
		})})
	}

	if _, err := setup.runAsTool(byRow(float64(1)), nil, map[string]any{}); err == nil || !strings.Contains(err.Error(), "column names") {
		t.Errorf("update of row 1 as a tool: error = %v, want the header named", err)
	}
	if _, err := setup.runAsTool(sheetsParams("delete", map[string]any{
		"toDelete": "rows", "numberToDelete": float64(1), "startIndex": "1",
	}), nil, map[string]any{}); err == nil || !strings.Contains(err.Error(), "column names") {
		t.Errorf("delete of row 1 as a tool: error = %v, want the header named", err)
	}
	expectGrid(t, setup,
		sheetsRow("name", "status", "age"), sheetsRow("Ann", "open", 31.0), sheetsRow("Bo", "closed", 28.0),
		sheetsRow("Cy", "open"), sheetsRow("Dana"))

	// Below the header both work, as a tool and as a step.
	if _, err := setup.runAsTool(byRow(float64(2)), nil, map[string]any{}); err != nil {
		t.Fatalf("update of row 2 as a tool: %v", err)
	}
	if _, err := setup.runAsTool(sheetsParams("delete", map[string]any{
		"toDelete": "rows", "numberToDelete": float64(1), "startIndex": "5",
	}), nil, map[string]any{}); err != nil {
		t.Fatalf("delete of row 5 as a tool: %v", err)
	}
	expectGrid(t, setup,
		sheetsRow("name", "status", "age"), sheetsRow("Ann", "closed", 31.0), sheetsRow("Bo", "closed", 28.0), sheetsRow("Cy", "open"))
	if _, err := setup.runErr(byRow(float64(1)), nil); err != nil {
		t.Fatalf("the ordinary step may address row 1: %v", err)
	}
}
