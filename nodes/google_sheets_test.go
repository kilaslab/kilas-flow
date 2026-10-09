package nodes_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

const sheetsDoc = "doc-1"

// sheetsSetup is a fake spreadsheet, the executor aimed at it under a policy
// that grants only that one loopback endpoint, and the request a run carries.
type sheetsSetup struct {
	fake     *fakeSheets
	executor *nodes.GoogleSheetsExecutor
	request  engine.Request
}

func newSheetsSetup(t *testing.T) *sheetsSetup {
	t.Helper()
	fake := newFakeSheets(t, sheetsDoc)
	return &sheetsSetup{
		fake:     fake,
		executor: nodes.NewGoogleSheetsExecutor(sheetsPolicy(t, fake.endpoint())),
		request: engine.Request{Credentials: googleCreds{
			typ: nodes.GoogleSheetsCredentialType, host: httptestHost(fake.server.URL),
		}},
	}
}

// locator builds the stored shape of a resource locator.
func sheetsLocator(mode string, value any) map[string]any {
	return map[string]any{"__rl": true, "mode": mode, "value": value}
}

// expr is the explicit expression marker.
func sheetsExpr(template string) map[string]any {
	return map[string]any{"mode": "expression", "value": template}
}

// sheetsParams is a node's parameters aimed at Sheet1 of the fake document.
func sheetsParams(operation string, extra map[string]any) map[string]any {
	parameters := map[string]any{
		"operation":  operation,
		"documentId": sheetsLocator("id", sheetsDoc),
		"sheetName":  sheetsLocator("name", "Sheet1"),
	}
	for key, value := range extra {
		parameters[key] = value
	}
	return parameters
}

func (setup *sheetsSetup) ir(parameters map[string]any) workflow.IRNode {
	return workflow.IRNode{
		ID: "sheets", Name: "Sheets", Type: nodes.GoogleSheetsNodeType, TypeVersion: workflow.V(1),
		Parameters:  parameters,
		Credentials: map[string]string{nodes.GoogleSheetsCredentialType: "cred"},
	}
}

// items turns plain maps into input items, each paired with a distinct origin
// so lineage can be checked.
func sheetItems(rows ...map[string]any) []workflow.Item {
	items := make([]workflow.Item, 0, len(rows))
	for index, row := range rows {
		items = append(items, workflow.Item{
			JSON:   row,
			Paired: &workflow.PairedItem{SourceNodeID: "source", ItemIndex: index},
		})
	}
	return items
}

func (setup *sheetsSetup) run(t *testing.T, parameters map[string]any, items []workflow.Item) []workflow.Item {
	t.Helper()
	output, err := setup.runErr(parameters, items)
	if err != nil {
		t.Fatalf("Execute() error = %v\ncalls:\n%s", err, setup.fake.callLog())
	}
	return output
}

func (setup *sheetsSetup) runErr(parameters map[string]any, items []workflow.Item) ([]workflow.Item, error) {
	if items == nil {
		items = sheetItems(map[string]any{})
	}
	output, err := setup.executor.Execute(context.Background(), setup.ir(parameters), workflow.NodeInput{"main": items}, setup.request)
	if err != nil {
		return nil, err
	}
	if len(output) != 1 {
		return nil, errors.New("the node must have exactly one output port")
	}
	return output[0], nil
}

func jsonOf(items []workflow.Item) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, item.JSON)
	}
	return out
}

func expectJSON(t *testing.T, got []workflow.Item, want ...map[string]any) {
	t.Helper()
	if want == nil {
		want = []map[string]any{}
	}
	if !reflect.DeepEqual(jsonOf(got), want) {
		t.Fatalf("items\n got %#v\nwant %#v", jsonOf(got), want)
	}
}

func sheetsRow(cells ...any) []any {
	if cells == nil {
		return []any{}
	}
	return cells
}

// peopleTab is a header and four people; Dana's status is empty and the row
// below Cy is a gap.
func (setup *sheetsSetup) peopleTab() *fakeTab {
	return setup.fake.addTab(0, "Sheet1", 20,
		sheetsRow("name", "status", "age"),
		sheetsRow("Ann", "open", 31.0),
		sheetsRow("Bo", "closed", 28.0),
		sheetsRow("Cy", "open"),
		sheetsRow("Dana"),
	)
}

// --- Locators ----------------------------------------------------------------

func TestSheetsLocatorsReachTheSameTab(t *testing.T) {
	for name, test := range map[string]struct {
		document map[string]any
		sheet    map[string]any
		wantRows string // the first data cell of the tab read
	}{
		"document by id, sheet by name":       {sheetsLocator("id", sheetsDoc), sheetsLocator("name", "Sheet1"), "Ann"},
		"document by url":                     {sheetsLocator("url", "https://docs.google.com/spreadsheets/d/doc-1/edit#gid=7"), sheetsLocator("name", "Sheet1"), "Ann"},
		"document from list":                  {sheetsLocator("list", sheetsDoc), sheetsLocator("name", "Sheet1"), "Ann"},
		"sheet from list is its gid":          {sheetsLocator("id", sheetsDoc), sheetsLocator("list", float64(7)), "Zed"},
		"sheet gid as a number string":        {sheetsLocator("id", sheetsDoc), sheetsLocator("id", "7"), "Zed"},
		"sheet gid with the gid= prefix":      {sheetsLocator("id", sheetsDoc), sheetsLocator("id", "gid=7"), "Zed"},
		"sheet by url fragment":               {sheetsLocator("id", sheetsDoc), sheetsLocator("url", "https://docs.google.com/spreadsheets/d/doc-1/edit#gid=7"), "Zed"},
		"sheet gid zero from list":            {sheetsLocator("id", sheetsDoc), sheetsLocator("list", float64(0)), "Ann"},
		"sheet name with a quote in it":       {sheetsLocator("id", sheetsDoc), sheetsLocator("name", "Zed's data"), "Zed"},
		"a numeric title is a name":           {sheetsLocator("id", sheetsDoc), sheetsLocator("name", "2024"), "Yan"},
		"a locator whose value is expression": {sheetsLocator("id", sheetsExpr("{{ $json.doc }}")), sheetsLocator("name", "Sheet1"), "Ann"},
	} {
		t.Run(name, func(t *testing.T) {
			setup := newSheetsSetup(t)
			setup.peopleTab()
			setup.fake.addTab(7, "Zed's data", 5, sheetsRow("who"), sheetsRow("Zed"))
			setup.fake.addTab(9, "2024", 5, sheetsRow("who"), sheetsRow("Yan"))

			output := setup.run(t, map[string]any{"operation": "read", "documentId": test.document, "sheetName": test.sheet},
				sheetItems(map[string]any{"doc": sheetsDoc}))
			if len(output) == 0 {
				t.Fatalf("no rows read\ncalls:\n%s", setup.fake.callLog())
			}
			var first any
			for _, value := range output[0].JSON {
				if value == test.wantRows {
					first = value
				}
			}
			if first != test.wantRows {
				t.Fatalf("first row = %#v, want it to hold %q", output[0].JSON, test.wantRows)
			}
		})
	}
}

func TestSheetsRefusesAnUnknownTabAndABadDocumentID(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.peopleTab()
	if _, err := setup.runErr(map[string]any{"operation": "read", "documentId": sheetsLocator("id", sheetsDoc), "sheetName": sheetsLocator("name", "Nope")}, nil); err == nil ||
		!strings.Contains(err.Error(), "Nope") {
		t.Errorf("unknown tab error = %v, want it to name the tab", err)
	}
	if _, err := setup.runErr(map[string]any{"operation": "read", "documentId": sheetsLocator("id", "../../etc"), "sheetName": sheetsLocator("name", "Sheet1")}, nil); err == nil {
		t.Error("a document id that is a path was accepted")
	}
	if _, err := setup.runErr(map[string]any{"operation": "read", "documentId": sheetsLocator("url", "https://example.test/not-a-sheet"), "sheetName": sheetsLocator("name", "Sheet1")}, nil); err == nil {
		t.Error("a url with no /spreadsheets/d/ id was accepted")
	}
}

// --- Read ----------------------------------------------------------------------

func TestSheetsReadKeysRowsByHeaderWithTheRealRowNumber(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.peopleTab()

	output := setup.run(t, sheetsParams("read", nil), nil)
	expectJSON(t, output,
		map[string]any{"row_number": float64(2), "name": "Ann", "status": "open", "age": 31.0},
		map[string]any{"row_number": float64(3), "name": "Bo", "status": "closed", "age": 28.0},
		// Short rows are padded with empty text, never missing keys.
		map[string]any{"row_number": float64(4), "name": "Cy", "status": "open", "age": ""},
		map[string]any{"row_number": float64(5), "name": "Dana", "status": "", "age": ""},
	)
	for _, item := range output {
		if item.Paired == nil || item.Paired.SourceNodeID != "source" {
			t.Errorf("item %#v lost the lineage of the item that asked for it", item)
		}
	}
	if got := setup.fake.callsMatching(`GET /v4/spreadsheets/doc-1/values/`); len(got) != 1 {
		t.Fatalf("values reads = %d, want 1\n%s", len(got), setup.fake.callLog())
	}
	call := setup.fake.callsMatching(`GET /v4/spreadsheets/doc-1/values/`)[0]
	if call.Query.Get("valueRenderOption") != "UNFORMATTED_VALUE" || call.Query.Get("dateTimeRenderOption") != "FORMATTED_STRING" {
		t.Errorf("render options = %v, want unformatted values and formatted dates", call.Query)
	}
}

func TestSheetsReadDropsAnEmptyColumnAndNamesAnUnlabelledOne(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.fake.addTab(0, "Sheet1", 5,
		sheetsRow("a", "", "", "d"),
		sheetsRow("1", "", "x", "4"),
	)
	output := setup.run(t, sheetsParams("read", nil), nil)
	// Column B has no header and no data, so it is dropped. Column C has no
	// header but holds data, so it is kept under a generated name.
	expectJSON(t, output, map[string]any{"row_number": float64(2), "a": "1", "col_2": "x", "d": "4"})
}

func TestSheetsReadFiltersCompareText(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.peopleTab()
	filters := func(values ...map[string]any) map[string]any {
		list := make([]any, 0, len(values))
		for _, value := range values {
			list = append(list, value)
		}
		return map[string]any{"values": list}
	}
	statusOpen := map[string]any{"lookupColumn": "status", "lookupValue": "open"}
	age28 := map[string]any{"lookupColumn": "age", "lookupValue": "28"} // the cell holds the number 28

	t.Run("one filter returns every match", func(t *testing.T) {
		output := setup.run(t, sheetsParams("read", map[string]any{"filtersUI": filters(statusOpen)}), nil)
		expectJSON(t, output,
			map[string]any{"row_number": float64(2), "name": "Ann", "status": "open", "age": 31.0},
			map[string]any{"row_number": float64(4), "name": "Cy", "status": "open", "age": ""},
		)
	})
	t.Run("a number cell equals its text", func(t *testing.T) {
		output := setup.run(t, sheetsParams("read", map[string]any{"filtersUI": filters(age28)}), nil)
		expectJSON(t, output, map[string]any{"row_number": float64(3), "name": "Bo", "status": "closed", "age": 28.0})
	})
	t.Run("AND needs every filter", func(t *testing.T) {
		output := setup.run(t, sheetsParams("read", map[string]any{
			"filtersUI": filters(statusOpen, map[string]any{"lookupColumn": "name", "lookupValue": "Cy"}), "combineFilters": "AND",
		}), nil)
		expectJSON(t, output, map[string]any{"row_number": float64(4), "name": "Cy", "status": "open", "age": ""})
	})
	t.Run("OR needs one, and lists rows in the order the filters found them", func(t *testing.T) {
		output := setup.run(t, sheetsParams("read", map[string]any{
			"filtersUI": filters(age28, statusOpen), "combineFilters": "OR",
		}), nil)
		names := []any{}
		for _, item := range output {
			names = append(names, item.JSON["name"])
		}
		if !reflect.DeepEqual(names, []any{"Bo", "Ann", "Cy"}) {
			t.Fatalf("names = %v, want Bo (first filter) then Ann and Cy", names)
		}
	})
	t.Run("return only the first match", func(t *testing.T) {
		output := setup.run(t, sheetsParams("read", map[string]any{
			"filtersUI": filters(statusOpen), "options": map[string]any{"returnFirstMatch": true},
		}), nil)
		expectJSON(t, output, map[string]any{"row_number": float64(2), "name": "Ann", "status": "open", "age": 31.0})
	})
	t.Run("no match is zero items, so Always Output Data has something to act on", func(t *testing.T) {
		output := setup.run(t, sheetsParams("read", map[string]any{
			"filtersUI": filters(map[string]any{"lookupColumn": "status", "lookupValue": "never"}),
		}), nil)
		if len(output) != 0 {
			t.Fatalf("a read with no match produced %#v, want no items", output)
		}
	})
	t.Run("an unknown column is an error", func(t *testing.T) {
		_, err := setup.runErr(sheetsParams("read", map[string]any{
			"filtersUI": filters(map[string]any{"lookupColumn": "nope", "lookupValue": "x"}),
		}), nil)
		if err == nil || !strings.Contains(err.Error(), `"nope"`) {
			t.Fatalf("error = %v, want it to name the column", err)
		}
	})
	t.Run("row_number can be filtered on", func(t *testing.T) {
		output := setup.run(t, sheetsParams("read", map[string]any{
			"filtersUI": filters(map[string]any{"lookupColumn": "row_number", "lookupValue": "3"}),
		}), nil)
		expectJSON(t, output, map[string]any{"row_number": float64(3), "name": "Bo", "status": "closed", "age": 28.0})
	})
}

// Each input item filters on its own value, and the sheet is read once for the
// whole batch: Google allows about sixty reads a minute per user.
func TestSheetsReadResolvesFiltersPerItemAndReadsTheSheetOnce(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.peopleTab()
	params := sheetsParams("read", map[string]any{"filtersUI": map[string]any{"values": []any{
		map[string]any{"lookupColumn": "name", "lookupValue": sheetsExpr("{{ $json.who }}")},
	}}})
	output := setup.run(t, params, sheetItems(map[string]any{"who": "Bo"}, map[string]any{"who": "Ann"}, map[string]any{"who": "Nobody"}))
	if len(output) != 2 || output[0].JSON["name"] != "Bo" || output[1].JSON["name"] != "Ann" {
		t.Fatalf("output = %#v, want Bo then Ann", jsonOf(output))
	}
	if output[0].Paired.ItemIndex != 0 || output[1].Paired.ItemIndex != 1 {
		t.Errorf("lineage = %#v / %#v, want each row paired with the item that found it", output[0].Paired, output[1].Paired)
	}
	if got := setup.fake.callsMatching(`GET /v4/spreadsheets/doc-1/values/`); len(got) != 1 {
		t.Fatalf("values reads = %d, want one read shared by the three items\n%s", len(got), setup.fake.callLog())
	}
	if got := setup.fake.callsMatching(`^GET /v4/spreadsheets/doc-1$`); len(got) != 1 {
		t.Fatalf("metadata reads = %d, want 1\n%s", len(got), setup.fake.callLog())
	}
}

func TestSheetsReadDataLocation(t *testing.T) {
	// A blank first row, the header on row 2, a gap on row 4.
	grid := func(setup *sheetsSetup) {
		setup.fake.addTab(0, "Sheet1", 5,
			sheetsRow(),
			sheetsRow("name", "qty"),
			sheetsRow("a", 1.0),
			sheetsRow(),
			sheetsRow("b", 2.0),
		)
	}
	location := func(values map[string]any) map[string]any {
		return map[string]any{"options": map[string]any{"dataLocationOnSheet": map[string]any{"values": values}}}
	}

	t.Run("detect automatically skips leading and blank rows and keeps the real row numbers", func(t *testing.T) {
		setup := newSheetsSetup(t)
		grid(setup)
		output := setup.run(t, sheetsParams("read", location(map[string]any{"rangeDefinition": "detectAutomatically"})), nil)
		expectJSON(t, output,
			map[string]any{"row_number": float64(3), "name": "a", "qty": 1.0},
			map[string]any{"row_number": float64(5), "name": "b", "qty": 2.0},
		)
	})
	t.Run("a read with no location setting detects automatically", func(t *testing.T) {
		setup := newSheetsSetup(t)
		grid(setup)
		output := setup.run(t, sheetsParams("read", nil), nil)
		if len(output) != 2 || output[1].JSON["row_number"] != float64(5) {
			t.Fatalf("output = %#v, want the two data rows", jsonOf(output))
		}
	})
	t.Run("first empty row stops at the gap", func(t *testing.T) {
		setup := newSheetsSetup(t)
		grid(setup)
		output := setup.run(t, sheetsParams("read", location(map[string]any{"rangeDefinition": "detectAutomatically", "readRowsUntil": "firstEmptyRow"})), nil)
		expectJSON(t, output, map[string]any{"row_number": float64(3), "name": "a", "qty": 1.0})
	})
	t.Run("specify range by rows names the header and the first data row", func(t *testing.T) {
		setup := newSheetsSetup(t)
		grid(setup)
		output := setup.run(t, sheetsParams("read", location(map[string]any{"rangeDefinition": "specifyRange", "headerRow": float64(2), "firstDataRow": float64(3)})), nil)
		expectJSON(t, output,
			map[string]any{"row_number": float64(3), "name": "a", "qty": 1.0},
			map[string]any{"row_number": float64(4), "name": "", "qty": ""},
			map[string]any{"row_number": float64(5), "name": "b", "qty": 2.0},
		)
	})
	t.Run("specify range in A1 notation reads only that range, from its first row", func(t *testing.T) {
		setup := newSheetsSetup(t)
		grid(setup)
		output := setup.run(t, sheetsParams("read", location(map[string]any{"rangeDefinition": "specifyRangeA1", "range": "B2:B5"})), nil)
		// The range starts at row 2, so its first row is the header and the first
		// data row is sheet row 3: row_number is the sheet's row, not the range's.
		expectJSON(t, output,
			map[string]any{"row_number": float64(3), "qty": 1.0},
			map[string]any{"row_number": float64(4), "qty": ""},
			map[string]any{"row_number": float64(5), "qty": 2.0},
		)
		reads := setup.fake.callsMatching(`GET /v4/spreadsheets/doc-1/values/`)
		if len(reads) != 1 || !strings.HasSuffix(reads[0].Path, "!B2:B5") {
			t.Fatalf("reads = %v, want one read of 'Sheet1'!B2:B5", reads)
		}
	})
	t.Run("output formatting is passed to Google", func(t *testing.T) {
		setup := newSheetsSetup(t)
		grid(setup)
		setup.run(t, sheetsParams("read", map[string]any{"options": map[string]any{"outputFormatting": map[string]any{"values": map[string]any{
			"general": "FORMATTED_VALUE", "date": "SERIAL_NUMBER",
		}}}}), nil)
		call := setup.fake.callsMatching(`GET /v4/spreadsheets/doc-1/values/`)[0]
		if call.Query.Get("valueRenderOption") != "FORMATTED_VALUE" || call.Query.Get("dateTimeRenderOption") != "SERIAL_NUMBER" {
			t.Errorf("render options = %v", call.Query)
		}
	})
}

func TestSheetsReadOfAnEmptySheetIsNoItems(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.fake.addTab(0, "Sheet1", 5)
	if output := setup.run(t, sheetsParams("read", nil), nil); len(output) != 0 {
		t.Fatalf("output = %#v, want none", jsonOf(output))
	}
}

// --- Egress and errors --------------------------------------------------------

// The policy grants one exact host:port. A stub on another loopback port is a
// different endpoint and stays refused, which is the guarantee that lets these
// tests run without allow_private_networks.
func TestSheetsRefusesALoopbackEndpointThePolicyDidNotGrant(t *testing.T) {
	granted := newSheetsSetup(t)
	granted.peopleTab()

	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the refused endpoint was contacted: %s", r.URL)
	}))
	t.Cleanup(other.Close)
	t.Cleanup(nodes.SetGoogleSheetsAPIRootForTest(other.URL + "/v4"))

	_, err := granted.runErr(sheetsParams("read", nil), nil)
	if err == nil {
		t.Fatal("Execute() = nil, want the policy to refuse an endpoint it did not grant")
	}
	if !errors.Is(err, safehttp.ErrBlocked) {
		t.Fatalf("error = %v, want safehttp.ErrBlocked", err)
	}
}

func TestSheetsSurfacesAGoogleErrorWithItsStatus(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.peopleTab()
	setup.fake.failWith = http.StatusTooManyRequests
	_, err := setup.runErr(sheetsParams("read", nil), nil)
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("error = %v, want the 429 to reach the caller", err)
	}
}

func TestSheetsNeedsItsCredential(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.peopleTab()
	ir := setup.ir(sheetsParams("read", nil))
	ir.Credentials = nil
	_, err := setup.executor.Execute(context.Background(), ir, workflow.NodeInput{"main": sheetItems(map[string]any{})}, setup.request)
	if err == nil || !strings.Contains(err.Error(), nodes.GoogleSheetsCredentialType) {
		t.Fatalf("error = %v, want it to name the missing credential type", err)
	}
	if got := setup.fake.callsMatching(`.`); len(got) != 0 {
		t.Errorf("a request went out anonymously: %v", got)
	}
}

func TestSheetsRefusesAnOperationItDoesNotImplement(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.peopleTab()
	_, err := setup.runErr(sheetsParams("create", nil), nil)
	if err == nil || !strings.Contains(err.Error(), "create") {
		t.Fatalf("error = %v, want a refusal naming the operation", err)
	}
}
