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
	"github.com/kilaslab/kilas-flow/nodes"
)

func TestSheetsTypesMatchTheNodePack(t *testing.T) {
	t.Parallel()
	if n8n.GoogleSheetsNodeType != nodes.GoogleSheetsNodeType || n8n.GoogleSheetsTriggerNodeType != nodes.GoogleSheetsTriggerNodeType {
		t.Errorf("adapter has %q and %q, node pack has %q and %q",
			n8n.GoogleSheetsNodeType, n8n.GoogleSheetsTriggerNodeType, nodes.GoogleSheetsNodeType, nodes.GoogleSheetsTriggerNodeType)
	}
}

// sheetsNodeJSON is one n8n Google Sheets node, with the document and tab
// locators every real one carries.
func sheetsNodeJSON(name string, version any, parameters string) string {
	return fmt.Sprintf(`{"id":"%s","name":"%s","type":"n8n-nodes-base.googleSheets","typeVersion":%v,"position":[220,0],
	  "parameters":{"documentId":{"__rl":true,"value":"1AbCdEf","mode":"list","cachedResultName":"Budget","cachedResultUrl":"https://docs.google.com/spreadsheets/d/1AbCdEf/edit?usp=drivesdk"},
	  "sheetName":{"__rl":true,"value":0,"mode":"list","cachedResultName":"Sheet1","cachedResultUrl":"https://docs.google.com/spreadsheets/d/1AbCdEf/edit#gid=0"}%s}}`,
		strings.ToLower(strings.ReplaceAll(name, " ", "-")), name, version, parameters)
}

// sheetsWorkflow wires the manual trigger to every node, so a node's own
// diagnostics are not buried under "disconnected" ones.
func sheetsWorkflow(nodes ...string) string {
	targets := make([]string, 0, len(nodes))
	for _, raw := range nodes {
		var parsed struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			panic(err)
		}
		targets = append(targets, fmt.Sprintf(`{"node":%q,"type":"main","index":0}`, parsed.Name))
	}
	return fmt.Sprintf(`{"name":"Sheets","nodes":[
	  {"id":"m","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	  %s],"connections":{"Manual":{"main":[[%s]]}}}`, strings.Join(nodes, ","), strings.Join(targets, ","))
}

func sheetsBlocking(result n8n.ImportResult, nodeName string) []string {
	var reasons []string
	for _, issue := range result.Unsupported {
		if issue.NodeName == nodeName && issue.Severity == n8n.SeverityBlocking {
			reasons = append(reasons, issue.Field+": "+issue.Reason)
		}
	}
	return reasons
}

func TestSheetsOperationsImportVerbatimWithTheirLocatorsAndMapper(t *testing.T) {
	t.Parallel()

	columns := `,"columns":{"mappingMode":"defineBelow","value":{"id":"={{ $json.id }}","name":"={{ $json.name }}","plain":"x"},
	  "matchingColumns":["id"],"schema":[{"id":"id","displayName":"id","required":false,"defaultMatch":false,"display":true,"type":"string","canBeUsedToMatch":true},
	  {"id":"row_number","displayName":"=row_number","required":false,"defaultMatch":false,"display":true,"type":"number","canBeUsedToMatch":true,"readOnly":true,"removed":true}],
	  "attemptToConvertTypes":false,"convertFieldsToString":true}`
	result := importFixture(t, sheetsWorkflow(
		sheetsNodeJSON("Read", 4.7, `,"operation":"read","filtersUI":{"values":[{"lookupColumn":"status","lookupValue":"={{ $json.status }}"}]}`),
		sheetsNodeJSON("Append", 4.7, `,"operation":"append"`+columns),
		sheetsNodeJSON("Upsert", 4.7, `,"operation":"appendOrUpdate"`+columns),
		sheetsNodeJSON("Update", 4.7, `,"operation":"update"`+columns),
		sheetsNodeJSON("Delete", 4.7, `,"operation":"delete","toDelete":"columns","startIndex":"B","numberToDelete":2`),
		sheetsNodeJSON("Clear", 4.7, `,"operation":"clear","clear":"wholeSheet","keepFirstRow":true`),
	))

	for _, name := range []string{"Read", "Append", "Upsert", "Update", "Delete", "Clear"} {
		sheets := nodeByName(result.Document, name)
		if sheets.Type != n8n.GoogleSheetsNodeType {
			t.Fatalf("%s imported as %q, want the native Google Sheets node", name, sheets.Type)
		}
		if reasons := sheetsBlocking(result, name); len(reasons) != 0 {
			t.Errorf("%s has blocking issues: %v", name, reasons)
		}
		// Both locators arrive as locators, with the cached display data kept
		// and no diagnostic: a Google ID is valid on any instance.
		document, ok := property.ReadLocator(sheets.Parameters["documentId"])
		if !ok || document.Mode != "list" || document.Value != "1AbCdEf" || document.CachedResultName != "Budget" {
			t.Errorf("%s documentId = %#v", name, sheets.Parameters["documentId"])
		}
		sheet, ok := property.ReadLocator(sheets.Parameters["sheetName"])
		if !ok || sheet.Mode != "list" || sheet.Value != float64(0) {
			t.Errorf("%s sheetName = %#v, want the numeric gid kept", name, sheets.Parameters["sheetName"])
		}
		if !property.LocatorIsSet(sheets.Parameters["sheetName"]) {
			t.Errorf("%s: a gid of 0 must count as a chosen sheet", name)
		}
	}
	for _, issue := range result.Unsupported {
		if issue.Field == "documentId" || issue.Field == "sheetName" {
			t.Errorf("unexpected issue on %s: %#v", issue.Field, issue)
		}
	}

	for name, operation := range map[string]string{"Read": "read", "Append": "append", "Upsert": "appendOrUpdate", "Update": "update", "Delete": "delete", "Clear": "clear"} {
		if got := nodeByName(result.Document, name).Parameters["operation"]; got != operation {
			t.Errorf("%s operation = %#v, want %q", name, got, operation)
		}
	}

	read := nodeByName(result.Document, "Read")
	filters := read.Parameters["filtersUI"].(map[string]any)["values"].([]any)[0].(map[string]any)
	if !reflect.DeepEqual(filters["lookupValue"], map[string]any{"mode": "expression", "value": "{{ $json.status }}"}) {
		t.Errorf("lookupValue = %#v, want an expression marker", filters["lookupValue"])
	}

	update := nodeByName(result.Document, "Update").Parameters["columns"].(map[string]any)
	values := update["value"].(map[string]any)
	if !reflect.DeepEqual(values["id"], map[string]any{"mode": "expression", "value": "{{ $json.id }}"}) || values["plain"] != "x" {
		t.Errorf("columns.value = %#v, want the expressions marked and the fixed value kept", values)
	}
	// Everything else in the mapper is carried as written, including a schema
	// row whose label happens to start with "=".
	if update["mappingMode"] != "defineBelow" || update["attemptToConvertTypes"] != false || update["convertFieldsToString"] != true {
		t.Errorf("mapper = %#v", update)
	}
	if !reflect.DeepEqual(update["matchingColumns"], []any{"id"}) {
		t.Errorf("matchingColumns = %#v", update["matchingColumns"])
	}
	schema := update["schema"].([]any)
	last := schema[1].(map[string]any)
	if last["displayName"] != "=row_number" || last["readOnly"] != true || last["removed"] != true {
		t.Errorf("schema row = %#v, want it carried untouched", last)
	}

	del := nodeByName(result.Document, "Delete").Parameters
	if del["toDelete"] != "columns" || del["startIndex"] != "B" || del["numberToDelete"] != float64(2) {
		t.Errorf("delete parameters = %#v", del)
	}
	clear := nodeByName(result.Document, "Clear").Parameters
	if clear["clear"] != "wholeSheet" || clear["keepFirstRow"] != true {
		t.Errorf("clear parameters = %#v", clear)
	}
}

// n8n leaves the default operation out of the file, and a node with no operation
// would be asking the runtime to guess.
func TestSheetsWithNoOperationIsARead(t *testing.T) {
	t.Parallel()
	result := importFixture(t, sheetsWorkflow(sheetsNodeJSON("Lookup", 4.7, "")))
	if got := nodeByName(result.Document, "Lookup").Parameters["operation"]; got != "read" {
		t.Fatalf("operation = %#v, want read", got)
	}
}

// Two defaults moved between n8n versions, and a native node has one meaning,
// so the importer writes down the one the source version had.
func TestSheetsVersionDependentDefaultsAreWrittenDown(t *testing.T) {
	t.Parallel()

	filters := `,"operation":"read","filtersUI":{"values":[{"lookupColumn":"a","lookupValue":"1"}]}`
	for _, test := range []struct {
		version     string
		extra       string
		wantCombine any
		wantFirst   any // nil: options.returnFirstMatch absent
	}{
		{"4.1", filters, "OR", true},
		{"4.2", filters, "OR", true},
		{"4.3", filters, "AND", true},
		{"4.4", filters, "AND", true},
		{"4.5", filters, "AND", nil},
		{"4.7", filters, "AND", nil},
		{"4.2", filters + `,"combineFilters":"AND"`, "AND", true},
		{"4.7", filters + `,"combineFilters":"OR"`, "OR", nil},
		{"4.4", filters + `,"options":{"returnAllMatches":"returnAllMatches"}`, "AND", false},
		{"4.4", filters + `,"options":{"returnAllMatches":"returnFirstMatch"}`, "AND", true},
		{"4.5", filters + `,"options":{"returnFirstMatch":true}`, "AND", true},
		{"4.7", filters + `,"options":{"returnFirstMatch":false}`, "AND", false},
	} {
		t.Run(test.version+test.extra, func(t *testing.T) {
			result := importFixture(t, sheetsWorkflow(sheetsNodeJSON("Lookup", test.version, test.extra)))
			parameters := nodeByName(result.Document, "Lookup").Parameters
			if parameters["combineFilters"] != test.wantCombine {
				t.Errorf("combineFilters = %#v, want %v", parameters["combineFilters"], test.wantCombine)
			}
			options, _ := parameters["options"].(map[string]any)
			if got, present := options["returnFirstMatch"]; test.wantFirst == nil && present || test.wantFirst != nil && got != test.wantFirst {
				t.Errorf("options = %#v, want returnFirstMatch %v", options, test.wantFirst)
			}
			if _, present := options["returnAllMatches"]; present {
				t.Errorf("options = %#v, want the old spelling gone", options)
			}
		})
	}
	t.Run("other operations get no filter defaults", func(t *testing.T) {
		result := importFixture(t, sheetsWorkflow(sheetsNodeJSON("Clear", 4.2, `,"operation":"clear"`)))
		parameters := nodeByName(result.Document, "Clear").Parameters
		if _, present := parameters["combineFilters"]; present {
			t.Errorf("parameters = %#v, want no combineFilters on a clear", parameters)
		}
	})
}

func TestSheetsImportBlocksWhatItDoesNotImplement(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		version     any
		parameters  string
		credentials string
		want        string
	}{
		"the version 3 shape":      {3, `,"operation":"append","dataMode":"autoMapInputData"`, "", "version 3"},
		"a service account":        {4.7, `,"authentication":"serviceAccount","operation":"read"`, "", "service account"},
		"a googleApi credential":   {4.7, `,"operation":"read"`, `,"credentials":{"googleApi":{"id":"1","name":"Service account"}}`, "service account"},
		"the spreadsheet resource": {4.7, `,"resource":"spreadsheet","operation":"create"`, "", "resource"},
		"creating a sheet":         {4.7, `,"operation":"create"`, "", "create"},
		"removing a sheet":         {4.7, `,"operation":"remove"`, "", "remove"},
		"an unknown operation":     {4.7, `,"operation":"archive"`, "", "archive"},
	} {
		t.Run(name, func(t *testing.T) {
			raw := sheetsNodeJSON("Sheets", test.version, test.parameters)
			if test.credentials != "" {
				raw = strings.TrimSuffix(raw, "}") + test.credentials + "}"
			}
			result := importFixture(t, sheetsWorkflow(raw))
			reasons := sheetsBlocking(result, "Sheets")
			found := false
			for _, reason := range reasons {
				found = found || strings.Contains(strings.ToLower(reason), strings.ToLower(test.want))
			}
			if !found {
				t.Fatalf("blocking issues = %v, want one mentioning %q", reasons, test.want)
			}
			if sheets := nodeByName(result.Document, "Sheets"); sheets.Type != n8n.GoogleSheetsNodeType {
				t.Errorf("imported as %q, want the native node kept so the author can fix it", sheets.Type)
			}
		})
	}
}

func TestSheetsCredentialsAreReportedUnboundAsEveryOtherNodesAre(t *testing.T) {
	t.Parallel()
	raw := strings.TrimSuffix(sheetsNodeJSON("Sheets", 4.7, `,"operation":"read"`), "}") +
		`,"credentials":{"googleSheetsOAuth2Api":{"id":"7","name":"Team Google"}}}`
	result := importFixture(t, sheetsWorkflow(raw))
	reasons := sheetsBlocking(result, "Sheets")
	if len(reasons) != 1 || !strings.Contains(reasons[0], "googleSheetsOAuth2Api") {
		t.Fatalf("blocking issues = %v, want the one unbound-credential report", reasons)
	}
	if sheets := nodeByName(result.Document, "Sheets"); len(sheets.Credentials) != 0 {
		t.Errorf("credentials = %#v, want the foreign reference dropped", sheets.Credentials)
	}
}

// --- The trigger ---------------------------------------------------------------

func sheetsTriggerJSON(parameters string) string {
	return fmt.Sprintf(`{"id":"t","name":"Sheet Trigger","type":"n8n-nodes-base.googleSheetsTrigger","typeVersion":1,"position":[0,0],
	  "parameters":{"documentId":{"__rl":true,"value":"1AbCdEf","mode":"list","cachedResultName":"Budget"},
	  "sheetName":{"__rl":true,"value":"gid=5","mode":"list","cachedResultName":"Tab"}%s}}`, parameters)
}

func TestSheetsTriggerEventsImportAsN8NSpellsThem(t *testing.T) {
	t.Parallel()
	for _, event := range []string{"rowAdded", "rowUpdate", "anyUpdate"} {
		t.Run(event, func(t *testing.T) {
			result := importFixture(t, fmt.Sprintf(`{"name":"T","nodes":[%s],"connections":{}}`,
				sheetsTriggerJSON(`,"event":"`+event+`","options":{"columnsToWatch":["status"]},"pollTimes":{"item":[{"mode":"everyX","value":5,"unit":"minutes"}]}`)))
			trigger := nodeByName(result.Document, "Sheet Trigger")
			if trigger.Type != n8n.GoogleSheetsTriggerNodeType {
				t.Fatalf("imported as %q", trigger.Type)
			}
			if trigger.Parameters["event"] != event {
				t.Errorf("event = %#v", trigger.Parameters["event"])
			}
			options := trigger.Parameters["options"].(map[string]any)
			if !reflect.DeepEqual(options["columnsToWatch"], []any{"status"}) {
				t.Errorf("columnsToWatch = %#v", options["columnsToWatch"])
			}
			if _, ok := trigger.Parameters["pollTimes"].(map[string]any); !ok {
				t.Errorf("pollTimes = %#v, want it carried", trigger.Parameters["pollTimes"])
			}
			for _, issue := range result.Unsupported {
				if issue.NodeName == "Sheet Trigger" && issue.Severity != n8n.SeverityDropped {
					t.Errorf("unexpected issue %#v", issue)
				}
			}
		})
	}
}

func TestSheetsTriggerSaysWhatItDoesNotKeep(t *testing.T) {
	t.Parallel()
	for _, include := range []string{"old", "both"} {
		result := importFixture(t, fmt.Sprintf(`{"name":"T","nodes":[%s],"connections":{}}`,
			sheetsTriggerJSON(`,"event":"rowUpdate","includeInOutput":"`+include+`"`)))
		if got := nodeByName(result.Document, "Sheet Trigger").Parameters["includeInOutput"]; got != "new" {
			t.Errorf("includeInOutput %q imported as %#v, want new", include, got)
		}
		lossy := false
		for _, issue := range result.Unsupported {
			lossy = lossy || issue.Severity == n8n.SeverityLossy && issue.Field == "includeInOutput"
		}
		if !lossy {
			t.Errorf("includeInOutput %q: no lossy issue in %#v", include, result.Unsupported)
		}
	}
	result := importFixture(t, fmt.Sprintf(`{"name":"T","nodes":[%s],"connections":{}}`,
		sheetsTriggerJSON(`,"authentication":"serviceAccount"`)))
	if reasons := sheetsBlocking(result, "Sheet Trigger"); len(reasons) == 0 || !strings.Contains(reasons[0], "service account") {
		t.Errorf("service account trigger: blocking issues = %v", reasons)
	}
}

// --- Export --------------------------------------------------------------------

// Import, export and look at what n8n would read. The expressions nested in the
// mapper, the filters and the locators have to be =-prefixed strings again.
func TestSheetsNodesGoBackToN8NAsTheyCame(t *testing.T) {
	t.Parallel()

	columns := `,"columns":{"mappingMode":"defineBelow","value":{"id":"={{ $json.id }}","plain":"x"},"matchingColumns":["id"],
	  "schema":[{"id":"id","displayName":"id","type":"string","canBeUsedToMatch":true}],"attemptToConvertTypes":false,"convertFieldsToString":false}`
	fixture := fmt.Sprintf(`{"name":"Round trip","nodes":[
	  {"id":"m","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	  %s,
	  %s,
	  %s,
	  %s],"connections":{}}`,
		sheetsNodeJSON("Update", 4.7, `,"operation":"update"`+columns),
		sheetsNodeJSON("Old Read", 4.4, `,"operation":"read","filtersUI":{"values":[{"lookupColumn":"a","lookupValue":"={{ $json.a }}"}]},"options":{"returnAllMatches":"returnAllMatches"}`),
		sheetsNodeJSON("New Read", 4.7, `,"operation":"read","options":{"returnFirstMatch":true}`),
		sheetsTriggerJSON(`,"event":"anyUpdate"`))
	imported := importFixture(t, fixture)
	exported, err := n8n.Export(imported.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	encoded, err := json.Marshal(exported.Document)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"={{ $json.id }}"`, `"={{ $json.a }}"`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("export = %s\nwant the expression %s restored with its = prefix", encoded, want)
		}
	}
	if strings.Contains(string(encoded), `"mode":"expression"`) {
		t.Errorf("export still holds an expression marker object: %s", encoded)
	}

	update := exportedNode(t, exported.Document, "Update")
	if update.Type != "n8n-nodes-base.googleSheets" || update.TypeVersion != 4.7 {
		t.Errorf("Update exported as %s %v", update.Type, update.TypeVersion)
	}
	mapper := update.Parameters["columns"].(map[string]any)
	if mapper["attemptToConvertTypes"] != false || !reflect.DeepEqual(mapper["matchingColumns"], []any{"id"}) {
		t.Errorf("exported mapper = %#v", mapper)
	}
	locator := update.Parameters["documentId"].(map[string]any)
	if locator["__rl"] != true || locator["mode"] != "list" || locator["cachedResultName"] != "Budget" {
		t.Errorf("exported documentId = %#v", locator)
	}

	// A node that came from 4.4 goes back in the 4.4 spelling of the option.
	oldRead := exportedNode(t, exported.Document, "Old Read")
	if oldRead.TypeVersion != 4.4 {
		t.Errorf("Old Read exported at %v, want its own version 4.4", oldRead.TypeVersion)
	}
	options := oldRead.Parameters["options"].(map[string]any)
	if options["returnAllMatches"] != "returnAllMatches" {
		t.Errorf("Old Read options = %#v, want returnAllMatches restored", options)
	}
	if _, present := options["returnFirstMatch"]; present {
		t.Errorf("Old Read options = %#v, want no 4.5 option on a 4.4 node", options)
	}
	newRead := exportedNode(t, exported.Document, "New Read")
	if got := newRead.Parameters["options"].(map[string]any)["returnFirstMatch"]; got != true {
		t.Errorf("New Read options = %#v", newRead.Parameters["options"])
	}

	trigger := exportedNode(t, exported.Document, "Sheet Trigger")
	if trigger.Type != "n8n-nodes-base.googleSheetsTrigger" || trigger.TypeVersion != 1 || trigger.Parameters["event"] != "anyUpdate" {
		t.Errorf("trigger exported as %s %v %#v", trigger.Type, trigger.TypeVersion, trigger.Parameters)
	}

	// And it imports the same again.
	again := importFixture(t, string(encoded))
	for _, name := range []string{"Update", "Old Read", "New Read", "Sheet Trigger"} {
		before, _ := json.Marshal(nodeByName(imported.Document, name).Parameters)
		after, _ := json.Marshal(nodeByName(again.Document, name).Parameters)
		if string(before) != string(after) {
			t.Errorf("%s changed over an export and an import\n before %s\n  after %s", name, before, after)
		}
	}
}

func TestANativeSheetsNodeExportsAtN8NsCurrentVersion(t *testing.T) {
	t.Parallel()
	document := importFixture(t, sheetsWorkflow(sheetsNodeJSON("Read", 4.7, `,"operation":"read"`))).Document
	for index := range document.Nodes {
		if document.Nodes[index].Name == "Read" {
			document.Nodes[index].TypeVersion = workflow.V(1)
		}
	}
	exported, err := n8n.Export(document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if got := exportedNode(t, exported.Document, "Read").TypeVersion; got != 4.7 {
		t.Errorf("a node authored here exported at %v, want 4.7", got)
	}
}
