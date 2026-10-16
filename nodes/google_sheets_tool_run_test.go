package nodes_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// These tests run the Google Sheets tool the way a deployment does: an agent,
// a scripted model that calls the tool, and the httptest Sheets stub. They are
// not parallel because the stub aims a package-level API root at itself.

type sheetsAgentRig struct {
	fake        *fakeSheets
	model       *recordingModel
	harness     formHarness
	credentials credentialMap
}

func newSheetsAgentRig(t *testing.T, bodies ...string) *sheetsAgentRig {
	t.Helper()
	fake := newFakeSheets(t, sheetsDoc)
	model := newRecordingModel(t, bodies...)
	policy := allowing(model.server)
	policy.AllowedPrivateEndpoints = append(policy.AllowedPrivateEndpoints, fake.endpoint())
	return &sheetsAgentRig{
		fake: fake, model: model, harness: newFormHarness(t, policy),
		credentials: credentialMap{
			"cred-key": modelCredential(),
			"cred-sheets": {
				ID: "cred-sheets", Name: "Sheets", Type: nodes.GoogleSheetsCredentialType,
				Fields:         map[string]string{"access_token": "token"},
				AllowedDomains: []string{httptestHost(fake.server.URL)},
			},
		},
	}
}

// tool builds the descriptor the Sheets tool node emits.
func (rig *sheetsAgentRig) tool(t *testing.T, name string, parameters map[string]any) workflow.Item {
	t.Helper()
	return rig.harness.descriptor(t, nodes.GoogleSheetsToolNodeType, workflow.V(1), name, parameters,
		map[string]string{nodes.GoogleSheetsCredentialType: "cred-sheets"})
}

// run answers one input item with the agent and returns what the agent said.
func (rig *sheetsAgentRig) run(t *testing.T, item map[string]any, tools ...workflow.Item) string {
	t.Helper()
	output, err := rig.harness.agent.Execute(context.Background(), agentIR(t, map[string]any{"prompt": "Do it."}), workflow.NodeInput{
		"main":  {{JSON: item}},
		"model": {modelItem(rig.model.server.URL)},
		"tools": tools,
	}, engine.Request{
		Credentials: rig.credentials,
		Execution:   agentExecution(),
		Env:         map[string]string{"SECRET": secretValue},
	})
	if err != nil {
		t.Fatalf("agent Execute() error = %v\nStub calls:\n%s", err, rig.fake.callLog())
	}
	answer, _ := output[0][0].JSON["output"].(string)
	return answer
}

func (rig *sheetsAgentRig) orders(rows ...[]any) {
	rig.fake.addTab(0, "Sheet1", 20, append([][]any{sheetsRow("Order ID", "Status")}, rows...)...)
}

func (rig *sheetsAgentRig) expectNoWrites(t *testing.T) {
	t.Helper()
	if writes := rig.fake.callsMatching(`^(PUT|POST|PATCH|DELETE) `); len(writes) != 0 {
		t.Errorf("a read wrote to the sheet:\n%s", rig.fake.callLog())
	}
}

func orderLookup(lookupValue any) map[string]any {
	return sheetsToolParams("read", map[string]any{"filtersUI": map[string]any{"values": []any{map[string]any{
		"lookupColumn": "Order ID", "lookupValue": lookupValue,
	}}}})
}

// The model's filter value reaches the stub as the value to look for.
func TestTheSheetsToolReadsTheRowTheModelAsksFor(t *testing.T) {
	rig := newSheetsAgentRig(t,
		toolCall("Order_Lookup", map[string]any{"order": "A-2"}),
		assistantAnswer("it shipped"),
	)
	rig.orders(sheetsRow("A-1", "open"), sheetsRow("A-2", "shipped"), sheetsRow("A-3", "open"))
	tool := rig.tool(t, "Order Lookup", orderLookup(modelValue("order", "the order id")))

	if answer := rig.run(t, map[string]any{}, tool); answer != "it shipped" {
		t.Fatalf("agent said %q", answer)
	}
	turns := rig.model.toolTurns(t)
	if len(turns) != 1 || !strings.Contains(turns[0], "A-2") || !strings.Contains(turns[0], "shipped") ||
		strings.Contains(turns[0], "A-1") || strings.Contains(turns[0], "A-3") {
		t.Fatalf("tool turns = %v, want only the A-2 row", turns)
	}
	rig.expectNoWrites(t)
}

// Text a model sends is data. A filter value that looks like a template is
// compared as the characters it is, so it finds the row that holds exactly
// that text and never the secret it names.
func TestTheSheetsToolComparesAModelsFilterValueLiterally(t *testing.T) {
	rig := newSheetsAgentRig(t,
		toolCall("Order_Lookup", map[string]any{"order": modelTemplate}),
		assistantAnswer("done"),
	)
	rig.orders(sheetsRow("A-1", "open"), sheetsRow(modelTemplate, "literal-row"), sheetsRow(secretValue, "secret-row"))
	tool := rig.tool(t, "Order Lookup", orderLookup(modelValue("order", "the order id")))

	rig.run(t, map[string]any{}, tool)
	turns := rig.model.toolTurns(t)
	if len(turns) != 1 || !strings.Contains(turns[0], "literal-row") || strings.Contains(turns[0], "secret-row") ||
		strings.Contains(turns[0], "A-1") {
		t.Fatalf("tool turns = %v, want only the row holding the template text", turns)
	}
}

// The model's column values are written to the cells as data.
func TestTheSheetsToolAppendsTheValuesTheModelSupplies(t *testing.T) {
	rig := newSheetsAgentRig(t,
		toolCall("Order_Database_Sync", map[string]any{"order": "A-9", "status": modelTemplate}),
		assistantAnswer("added"),
	)
	rig.orders()
	tool := rig.tool(t, "Order Database Sync", sheetsToolParams("append", map[string]any{
		"columns": manualColumns("", map[string]any{
			"Order ID": modelValue("order", "the order id"), "Status": modelValue("status", "the state"),
		}),
	}))

	rig.run(t, map[string]any{"chatInput": "add order A-9"}, tool)
	want := [][]any{sheetsRow("Order ID", "Status"), sheetsRow("A-9", modelTemplate)}
	if got := rig.fake.snapshot("Sheet1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("the sheet holds\n got %v\nwant %v\ncalls:\n%s", got, want, rig.fake.callLog())
	}
	for _, row := range rig.fake.snapshot("Sheet1") {
		for _, cell := range row {
			if cell == secretValue {
				t.Fatalf("the model's template text was evaluated into a cell: %v", row)
			}
		}
	}
}

func TestTheSheetsToolUpdatesTheRowTheModelMatches(t *testing.T) {
	rig := newSheetsAgentRig(t,
		toolCall("Order_Database_Sync", map[string]any{"order": "A-2", "status": "delivered"}),
		assistantAnswer("updated"),
	)
	rig.orders(sheetsRow("A-1", "open"), sheetsRow("A-2", "shipped"))
	tool := rig.tool(t, "Order Database Sync", sheetsToolParams("appendOrUpdate", map[string]any{
		"columns": manualColumns("Order ID", map[string]any{
			"Order ID": modelValue("order", "the order id"), "Status": modelValue("status", "the state"),
		}),
	}))

	rig.run(t, map[string]any{}, tool)
	want := [][]any{sheetsRow("Order ID", "Status"), sheetsRow("A-1", "open"), sheetsRow("A-2", "delivered")}
	if got := rig.fake.snapshot("Sheet1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("the sheet holds\n got %v\nwant %v\ncalls:\n%s", got, want, rig.fake.callLog())
	}
}

// --- A cell that holds template text, through the tool ------------------------

// The agent's item carries text that looks like a template. `$json.x` reads it
// as data, and it goes into a cell and into a filter exactly as it is.
func TestTemplateTextFromTheAgentsItemIsWrittenToACellLiterally(t *testing.T) {
	rig := newSheetsAgentRig(t,
		toolCall("Order_Database_Sync", map[string]any{}),
		assistantAnswer("added"),
	)
	rig.orders()
	tool := rig.tool(t, "Order Database Sync", sheetsToolParams("append", map[string]any{
		"columns": manualColumns("", map[string]any{
			"Order ID": "A-7", "Status": formExpression("{{ $json.x }}"),
		}),
	}))

	rig.run(t, map[string]any{"x": modelTemplate}, tool)
	want := [][]any{sheetsRow("Order ID", "Status"), sheetsRow("A-7", modelTemplate)}
	if got := rig.fake.snapshot("Sheet1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("the sheet holds\n got %v\nwant %v", got, want)
	}
}

func TestTemplateTextFromTheAgentsItemIsComparedLiterallyInAFilter(t *testing.T) {
	rig := newSheetsAgentRig(t,
		toolCall("Order_Lookup", map[string]any{}),
		assistantAnswer("found"),
	)
	rig.orders(sheetsRow("A-1", "open"), sheetsRow("A-2", modelTemplate), sheetsRow("A-3", secretValue))
	tool := rig.tool(t, "Order Lookup", sheetsToolParams("read", map[string]any{
		"filtersUI": map[string]any{"values": []any{map[string]any{
			"lookupColumn": "Status", "lookupValue": formExpression("{{ $json.x }}"),
		}}},
	}))

	rig.run(t, map[string]any{"x": modelTemplate}, tool)
	turns := rig.model.toolTurns(t)
	if len(turns) != 1 || !strings.Contains(turns[0], "A-2") || strings.Contains(turns[0], "A-3") || strings.Contains(turns[0], "A-1") {
		t.Fatalf("tool turns = %v, want only the row holding the template text", turns)
	}
}
