package n8n_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// sheetsToolNode is shaped like the Sheets tool of n8n template 19348: an
// upsert on one matching column, every value a $fromAI written with n8n's
// override comment, and no credential reference. The schema label starts with
// "=" on purpose: only values are expressions.
const sheetsToolNode = `{"id":"d","name":"Tool","type":"n8n-nodes-base.googleSheetsTool","typeVersion":4.7,"position":[400,200],
  "parameters":{"operation":"appendOrUpdate",
    "documentId":{"__rl":true,"mode":"list","value":"1abc"},
    "sheetName":{"__rl":true,"mode":"list","value":"gid=0"},
    "columns":{"mappingMode":"defineBelow","matchingColumns":["Order ID"],
      "value":{"Order ID":"={{ /*n8n-auto-generated-fromAI-override*/ $fromAI('Order_ID', ` + "``" + `, 'string') }}",
               "Status":"={{ $fromAI('Status', 'the state', 'string') }}"},
      "schema":[{"id":"Order ID","displayName":"=total","type":"string"}]},
    "options":{},"descriptionType":"manual","toolDescription":"Saves an order"}}`

func TestAGoogleSheetsToolImportsAsTheSheetsToolForm(t *testing.T) {
	t.Parallel()

	catalogue := registry(t)
	result := importFixture(t, agentWithTool(sheetsToolNode))
	tool := nodeByName(result.Document, "Tool")
	if tool.Type != n8n.GoogleSheetsToolNodeType || tool.TypeVersion != workflow.MustTypeVersion("4.7") {
		t.Fatalf("googleSheetsTool imported as %s v%s, want %s v4.7", tool.Type, tool.TypeVersion, n8n.GoogleSheetsToolNodeType)
	}
	if tool.Parameters["toolDescription"] != "Saves an order" || tool.Parameters["operation"] != "appendOrUpdate" {
		t.Errorf("parameters = %#v", tool.Parameters)
	}
	columns, _ := tool.Parameters["columns"].(map[string]any)
	values, _ := columns["value"].(map[string]any)
	if got := expressionTemplate(t, values["Order ID"]); strings.Contains(got, "n8n-auto-generated") || !strings.Contains(got, "$fromAI('Order_ID'") {
		t.Errorf("Order ID = %q, want the expression without n8n's override comment", got)
	}
	schema, _ := columns["schema"].([]any)
	row, _ := schema[0].(map[string]any)
	if row["displayName"] != "=total" {
		t.Errorf("schema label = %#v, want the plain text kept", row["displayName"])
	}
	if blocking := blockingApartFromCredentials(result); len(blocking) != 0 {
		t.Fatalf("blocking issues apart from credentials: %#v", blocking)
	}
	compileImported(t, result.Document, catalogue)
}

func TestAGoogleSheetsToolExportsAsN8NsToolOfTheSheetsNode(t *testing.T) {
	t.Parallel()

	imported := importFixture(t, agentWithTool(sheetsToolNode))
	exported, err := n8n.Export(imported.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, candidate := range exported.Document.Nodes {
		if candidate.Name != "Tool" {
			continue
		}
		if candidate.Type != "n8n-nodes-base.googleSheetsTool" || candidate.TypeVersion != 4.7 {
			t.Fatalf("exported as %s v%v", candidate.Type, candidate.TypeVersion)
		}
		columns, _ := candidate.Parameters["columns"].(map[string]any)
		values, _ := columns["value"].(map[string]any)
		if got, _ := values["Status"].(string); !strings.HasPrefix(got, "={{ $fromAI('Status'") {
			t.Errorf("Status = %#v, want n8n's expression", values["Status"])
		}
		if candidate.Parameters["descriptionType"] != "manual" {
			t.Errorf("descriptionType = %#v", candidate.Parameters["descriptionType"])
		}
		return
	}
	t.Fatal("the tool is not in the export")
}

// The tool never clears and never maps columns from the agent's item, so an
// n8n tool that does is blocked at import with the reason.
func TestAGoogleSheetsToolThatClearsOrMapsAutomaticallyIsBlocked(t *testing.T) {
	t.Parallel()

	for field, tool := range map[string]string{
		"operation": `{"id":"d","name":"Tool","type":"n8n-nodes-base.googleSheetsTool","typeVersion":4.7,"position":[400,200],
		  "parameters":{"operation":"clear","documentId":"1abc","sheetName":"gid=0","descriptionType":"manual","toolDescription":"Clears"}}`,
		"columns": `{"id":"d","name":"Tool","type":"n8n-nodes-base.googleSheetsTool","typeVersion":4.7,"position":[400,200],
		  "parameters":{"operation":"append","documentId":"1abc","sheetName":"gid=0","descriptionType":"manual","toolDescription":"Adds",
		    "columns":{"mappingMode":"autoMapInputData","value":{},"matchingColumns":[],"schema":[]}}}`,
	} {
		result := importFixture(t, agentWithTool(tool))
		issue, found := issueOn(result, field)
		if !found || issue.Severity != n8n.SeverityBlocking {
			t.Errorf("%s issue = %+v (found %v), want a blocking issue", field, issue, found)
		}
	}
}
