package n8n_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
)

// sheetsTool19348Fixture is a hand-authored workflow shaped like the Sheets tool
// of n8n template 19348: an agent with a chat model and a memory, a Sheets tool
// that upserts an order with every column a fromAI value written with n8n's
// override comment (no description written, so n8n generates one), a second
// Sheets tool that reads with a fromAI filter, and a Gmail tool. It is not an
// n8n.io export and carries none of its bytes.
func sheetsTool19348Fixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "n8n_sheets_tool_19348.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(raw), "hand-authored KilasFlow fixture") {
		t.Fatal("the fixture must say it is hand-authored")
	}
	return raw
}

func TestTheSheetsTool19348ShapeImportsWithNoBlockingIssueAndRunsOnTheAgent(t *testing.T) {
	t.Parallel()

	catalogue := registry(t)
	result, err := n8n.Import(sheetsTool19348Fixture(t), catalogue)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if blocking := blockingApartFromCredentials(result); len(blocking) != 0 {
		t.Fatalf("blocking issues apart from credentials: %#v", blocking)
	}
	for _, imported := range result.Document.Nodes {
		if imported.Type == n8n.UnsupportedNodeType {
			t.Errorf("node %q imported as the unsupported placeholder", imported.Name)
		}
	}

	upsert := nodeByName(result.Document, "Order Database Sync")
	if upsert.Type != n8n.GoogleSheetsToolNodeType || upsert.Parameters["operation"] != "appendOrUpdate" {
		t.Fatalf("upsert tool = %s %#v", upsert.Type, upsert.Parameters["operation"])
	}
	columns, _ := upsert.Parameters["columns"].(map[string]any)
	values, _ := columns["value"].(map[string]any)
	if len(values) != 5 {
		t.Fatalf("column values = %#v, want the five columns", values)
	}
	for column, value := range values {
		if got := expressionTemplate(t, value); strings.Contains(got, "n8n-auto-generated") || !strings.Contains(got, "$fromAI(") {
			t.Errorf("%s = %q, want a fromAI expression without n8n's override comment", column, got)
		}
	}
	if matching, _ := columns["matchingColumns"].([]any); len(matching) != 1 || matching[0] != "Order ID" {
		t.Errorf("matchingColumns = %#v", columns["matchingColumns"])
	}
	if columns["mappingMode"] != "defineBelow" {
		t.Errorf("mappingMode = %#v", columns["mappingMode"])
	}

	lookup := nodeByName(result.Document, "Order Lookup")
	filters, _ := lookup.Parameters["filtersUI"].(map[string]any)
	list, _ := filters["values"].([]any)
	row, _ := list[0].(map[string]any)
	if row["lookupColumn"] != "Order ID" || !strings.Contains(expressionTemplate(t, row["lookupValue"]), "$fromAI('Order_ID'") {
		t.Errorf("filter = %#v, want the column fixed and the value a fromAI expression", row)
	}
	if lookup.Parameters["operation"] != "read" || lookup.Parameters["toolDescription"] != "Finds the rows of one order by its order ID." {
		t.Errorf("lookup tool = %#v", lookup.Parameters)
	}

	// n8n generates the upsert tool's description; this server writes one and
	// says so. The lookup tool wrote its own, so it has no such note.
	var noted []string
	for _, issue := range result.Unsupported {
		if issue.Field == "toolDescription" {
			if issue.Severity != n8n.SeverityLossy {
				t.Errorf("toolDescription issue on %q is %s, want lossy", issue.NodeName, issue.Severity)
			}
			noted = append(noted, issue.NodeName)
		}
	}
	if !namesInclude(noted, "Order Database Sync") || namesInclude(noted, "Order Lookup") {
		t.Errorf("toolDescription notes on %v, want the upsert tool's and not the lookup's", noted)
	}
	if description, _ := upsert.Parameters["toolDescription"].(string); description == "" {
		t.Error("the upsert tool has no written description")
	}

	compileImported(t, result.Document, catalogue)
}

func namesInclude(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// Export then import lands on the same tools: the parameters of both Sheets
// tools come back as they were, and the export names n8n's own tool type.
func TestTheSheetsTool19348ShapeSurvivesAnExportAndAnImport(t *testing.T) {
	t.Parallel()

	catalogue := registry(t)
	first, err := n8n.Import(sheetsTool19348Fixture(t), catalogue)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	exported, err := n8n.Export(first.Document, catalogue)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, candidate := range exported.Document.Nodes {
		if candidate.Name == "Order Database Sync" || candidate.Name == "Order Lookup" {
			if candidate.Type != "n8n-nodes-base.googleSheetsTool" || candidate.TypeVersion != 4.7 {
				t.Errorf("%s exported as %s v%v", candidate.Name, candidate.Type, candidate.TypeVersion)
			}
			if candidate.Parameters["descriptionType"] != "manual" {
				t.Errorf("%s descriptionType = %#v", candidate.Name, candidate.Parameters["descriptionType"])
			}
		}
	}
	for _, lossy := range exported.Lossy {
		if strings.HasPrefix(lossy.NodeName, "Order") {
			t.Errorf("export reported %s on %s: %s", lossy.Field, lossy.NodeName, lossy.Reason)
		}
	}

	encoded, err := json.Marshal(exported.Document)
	if err != nil {
		t.Fatalf("marshal the export: %v", err)
	}
	second, err := n8n.Import(encoded, catalogue)
	if err != nil {
		t.Fatalf("Import(export) error = %v", err)
	}
	if blocking := blockingApartFromCredentials(second); len(blocking) != 0 {
		t.Fatalf("importing the export is blocked: %#v", blocking)
	}
	for _, name := range []string{"Order Database Sync", "Order Lookup"} {
		before, after := nodeByName(first.Document, name), nodeByName(second.Document, name)
		if after.Type != before.Type || after.TypeVersion != before.TypeVersion {
			t.Errorf("%s = %s v%s after the round trip, was %s v%s", name, after.Type, after.TypeVersion, before.Type, before.TypeVersion)
		}
		if !reflect.DeepEqual(before.Parameters, after.Parameters) {
			t.Errorf("%s parameters changed over the round trip\n before %#v\n after  %#v", name, before.Parameters, after.Parameters)
		}
	}
	compileImported(t, second.Document, catalogue)
}
