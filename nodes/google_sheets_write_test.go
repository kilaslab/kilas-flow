package nodes_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// columnsParam is the stored shape of the columns parameter.
func columnsParam(mode string, values map[string]any, matching ...string) map[string]any {
	stored := map[string]any{"mappingMode": mode, "value": values}
	if values == nil {
		stored["value"] = nil
	}
	list := make([]any, 0, len(matching))
	for _, column := range matching {
		list = append(list, column)
	}
	stored["matchingColumns"] = list
	return stored
}

func optionsParam(options map[string]any) map[string]any { return map[string]any{"options": options} }

func expectGrid(t *testing.T, setup *sheetsSetup, want ...[]any) {
	t.Helper()
	got := setup.fake.snapshot("Sheet1")
	if len(want) == 0 {
		want = [][]any{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the sheet holds\n got %v\nwant %v\ncalls:\n%s", got, want, setup.fake.callLog())
	}
}

// --- Append ----------------------------------------------------------------------

func TestSheetsAppendWritesMappedValuesInHeaderOrder(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.peopleTab()
	params := sheetsParams("append", map[string]any{"columns": columnsParam("defineBelow", map[string]any{
		"age": 40.0, "name": sheetsExpr("{{ $json.who }}"), "status": nil, "ghost": nil,
	})})
	delete(params["columns"].(map[string]any)["value"].(map[string]any), "ghost")

	output := setup.run(t, params, sheetItems(map[string]any{"who": "Eve"}, map[string]any{"who": "Fay"}))

	expectGrid(t, setup,
		sheetsRow("name", "status", "age"),
		sheetsRow("Ann", "open", 31.0),
		sheetsRow("Bo", "closed", 28.0),
		sheetsRow("Cy", "open"),
		sheetsRow("Dana"),
		// A mapped value with none is written as empty text, in the column the
		// header says, whatever order the mapping listed them in.
		sheetsRow("Eve", "", 40.0),
		sheetsRow("Fay", "", 40.0),
	)
	// Hand-mapped columns emit what each item sent, null as empty text, with
	// the lineage of the item that sent it.
	expectJSON(t, output,
		map[string]any{"name": "Eve", "status": "", "age": 40.0},
		map[string]any{"name": "Fay", "status": "", "age": 40.0},
	)
	if output[1].Paired == nil || output[1].Paired.ItemIndex != 1 {
		t.Errorf("lineage = %#v, want the second row paired with the second item", output[1].Paired)
	}
	// Both rows go out in one write, after counting the rows once.
	if puts := setup.fake.callsMatching(`^PUT `); len(puts) != 1 {
		t.Fatalf("writes = %d, want 1\n%s", len(puts), setup.fake.callLog())
	}
	put := setup.fake.callsMatching(`^PUT `)[0]
	if !strings.HasSuffix(put.Path, "'Sheet1'!6:7") || put.Query.Get("valueInputOption") != "USER_ENTERED" {
		t.Errorf("write = %s %v, want rows 6 to 7 as USER_ENTERED", put.Path, put.Query)
	}
}

func TestSheetsAppendGrowsTheGridWhenTheSheetIsFull(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.fake.addTab(0, "Sheet1", 0, sheetsRow("name"), sheetsRow("Ann"))
	setup.run(t, sheetsParams("append", map[string]any{"columns": columnsParam("defineBelow", map[string]any{"name": "Bo"})}),
		sheetItems(map[string]any{}, map[string]any{}, map[string]any{}))
	expectGrid(t, setup, sheetsRow("name"), sheetsRow("Ann"), sheetsRow("Bo"), sheetsRow("Bo"), sheetsRow("Bo"))
	grow := setup.fake.callsMatching(`^POST .*:batchUpdate$`)
	if len(grow) != 1 {
		t.Fatalf("grid growth requests = %d, want 1\n%s", len(grow), setup.fake.callLog())
	}
	if tab := setup.fake.tab("Sheet1"); tab.rowCount != 5 {
		t.Errorf("rows in the grid = %d, want exactly the 3 that were needed added", tab.rowCount)
	}
}

func TestSheetsAppendAutoMapHandlesExtraFields(t *testing.T) {
	auto := columnsParam("autoMapInputData", map[string]any{})
	input := func() []workflow.Item {
		return sheetItems(map[string]any{"name": "Eve", "age": 22.0, "note": "hi", "row_number": 9.0})
	}

	t.Run("insertInNewColumn adds the field as a column, sorted, and leaves row_number out", func(t *testing.T) {
		setup := newSheetsSetup(t)
		setup.peopleTab()
		output := setup.run(t, sheetsParams("append", map[string]any{"columns": auto}), input())
		expectGrid(t, setup,
			sheetsRow("name", "status", "age", "note"),
			sheetsRow("Ann", "open", 31.0),
			sheetsRow("Bo", "closed", 28.0),
			sheetsRow("Cy", "open"),
			sheetsRow("Dana"),
			sheetsRow("Eve", "", 22.0, "hi"),
		)
		// Auto-mapped columns emit the input items unchanged.
		expectJSON(t, output, map[string]any{"name": "Eve", "age": 22.0, "note": "hi", "row_number": 9.0})
		header := setup.fake.callsMatching(`^PUT .*!1:1$`)
		if len(header) != 1 || header[0].Query.Get("valueInputOption") != "RAW" {
			t.Errorf("header writes = %v, want one RAW write of row 1", header)
		}
	})
	t.Run("ignoreIt drops the field", func(t *testing.T) {
		setup := newSheetsSetup(t)
		setup.peopleTab()
		setup.run(t, sheetsParams("append", map[string]any{"columns": auto, "options": map[string]any{"handlingExtraData": "ignoreIt"}}), input())
		expectGrid(t, setup,
			sheetsRow("name", "status", "age"),
			sheetsRow("Ann", "open", 31.0),
			sheetsRow("Bo", "closed", 28.0),
			sheetsRow("Cy", "open"),
			sheetsRow("Dana"),
			sheetsRow("Eve", "", 22.0),
		)
	})
	t.Run("error refuses the field and writes nothing", func(t *testing.T) {
		setup := newSheetsSetup(t)
		setup.peopleTab()
		_, err := setup.runErr(sheetsParams("append", map[string]any{"columns": auto, "options": map[string]any{"handlingExtraData": "error"}}), input())
		if err == nil || !strings.Contains(err.Error(), "note") {
			t.Fatalf("error = %v, want it to name the field", err)
		}
		if writes := setup.fake.callsMatching(`^(PUT|POST) `); len(writes) != 0 {
			t.Errorf("writes = %v, want none", writes)
		}
	})
	t.Run("an empty sheet takes its header from the first rows, even when the mapping is by hand", func(t *testing.T) {
		setup := newSheetsSetup(t)
		setup.fake.addTab(0, "Sheet1", 5)
		setup.run(t, sheetsParams("append", map[string]any{"columns": columnsParam("defineBelow", map[string]any{"x": "1"})}),
			sheetItems(map[string]any{"b": "2", "a": "1"}))
		expectGrid(t, setup, sheetsRow("a", "b"), sheetsRow("1", "2"))
	})
}

func TestSheetsAppendStoresStructuredValuesAsJSONText(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.fake.addTab(0, "Sheet1", 5, sheetsRow("name", "tags"))
	setup.run(t, sheetsParams("append", map[string]any{"columns": columnsParam("defineBelow", map[string]any{
		"name": "Eve", "tags": []any{"a", "b"},
	})}), nil)
	expectGrid(t, setup, sheetsRow("name", "tags"), sheetsRow("Eve", `["a","b"]`))
}

func TestSheetsAppendRefusesAMappedColumnTheSheetLacks(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.peopleTab()
	_, err := setup.runErr(sheetsParams("append", map[string]any{"columns": columnsParam("defineBelow", map[string]any{"nme": "typo"})}), nil)
	if err == nil || !strings.Contains(err.Error(), `"nme"`) {
		t.Fatalf("error = %v, want it to name the column the sheet lacks", err)
	}
	if writes := setup.fake.callsMatching(`^(PUT|POST) `); len(writes) != 0 {
		t.Errorf("writes = %v, want none", writes)
	}
}

func TestSheetsAppendOptions(t *testing.T) {
	t.Run("cell format RAW and a header on another row", func(t *testing.T) {
		setup := newSheetsSetup(t)
		setup.fake.addTab(0, "Sheet1", 5, sheetsRow("notes about this sheet"), sheetsRow("name"), sheetsRow("Ann"))
		setup.run(t, sheetsParams("append", map[string]any{
			"columns": columnsParam("defineBelow", map[string]any{"name": "Bo"}),
			"options": map[string]any{"cellFormat": "RAW", "locationDefine": map[string]any{"values": map[string]any{"headerRow": float64(2)}}},
		}), nil)
		expectGrid(t, setup, sheetsRow("notes about this sheet"), sheetsRow("name"), sheetsRow("Ann"), sheetsRow("Bo"))
		if put := setup.fake.callsMatching(`^PUT `)[0]; put.Query.Get("valueInputOption") != "RAW" {
			t.Errorf("valueInputOption = %q, want RAW", put.Query.Get("valueInputOption"))
		}
	})
	t.Run("minimise API calls uses Google's append", func(t *testing.T) {
		setup := newSheetsSetup(t)
		setup.peopleTab()
		setup.run(t, sheetsParams("append", map[string]any{
			"columns": columnsParam("defineBelow", map[string]any{"name": "Eve"}),
			"options": map[string]any{"useAppend": true},
		}), nil)
		posts := setup.fake.callsMatching(`^POST .*:append$`)
		if len(posts) != 1 || posts[0].Query.Get("insertDataOption") != "INSERT_ROWS" || posts[0].Query.Get("valueInputOption") != "USER_ENTERED" {
			t.Fatalf("append calls = %v, want one INSERT_ROWS append", posts)
		}
		if got := setup.fake.snapshot("Sheet1"); !reflect.DeepEqual(got[len(got)-1], sheetsRow("Eve")) {
			t.Errorf("last row = %v, want Eve", got[len(got)-1])
		}
	})
}

// --- Update ----------------------------------------------------------------------

func TestSheetsUpdateByRowNumberWritesOnlyTheMappedCells(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.peopleTab()
	params := sheetsParams("update", map[string]any{"columns": columnsParam("defineBelow", map[string]any{
		"row_number": sheetsExpr("{{ $json.row_number }}"), "status": sheetsExpr("{{ $json.next }}"),
	}, "row_number")})

	output := setup.run(t, params, sheetItems(
		map[string]any{"row_number": 3.0, "next": "reopened"},
		map[string]any{"row_number": 4.0, "next": "done"},
	))
	expectGrid(t, setup,
		sheetsRow("name", "status", "age"),
		sheetsRow("Ann", "open", 31.0),
		sheetsRow("Bo", "reopened", 28.0),
		sheetsRow("Cy", "done"),
		sheetsRow("Dana"),
	)
	// Mapped values come back per item.
	expectJSON(t, output,
		map[string]any{"row_number": 3.0, "status": "reopened"},
		map[string]any{"row_number": 4.0, "status": "done"},
	)
	// One read of the sheet and one write for the whole batch.
	if got := setup.fake.callsMatching(`GET .*/values/`); len(got) != 1 {
		t.Errorf("sheet reads = %d, want 1\n%s", len(got), setup.fake.callLog())
	}
	if got := setup.fake.callsMatching(`POST .*/values:batchUpdate`); len(got) != 1 {
		t.Errorf("batch writes = %d, want 1\n%s", len(got), setup.fake.callLog())
	}
}

func TestSheetsUpdateByColumnFindsTheFirstEqualRowAndSkipsTheRest(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.fake.addTab(0, "Sheet1", 10,
		sheetsRow("id", "status"),
		sheetsRow(1.0, "new"),
		sheetsRow(2.0, "new"),
		sheetsRow(2.0, "dup"),
	)
	params := sheetsParams("update", map[string]any{"columns": columnsParam("defineBelow", map[string]any{
		"id": sheetsExpr("{{ $json.id }}"), "status": "seen",
	}, "id")})
	// The sheet holds the number 2, and the item the text "2": equal as text.
	output := setup.run(t, params, sheetItems(map[string]any{"id": "2"}, map[string]any{"id": "9"}))
	expectGrid(t, setup,
		sheetsRow("id", "status"),
		sheetsRow(1.0, "new"),
		sheetsRow(2.0, "seen"),
		sheetsRow(2.0, "dup"),
	)
	if len(output) != 2 {
		t.Errorf("output = %#v, want the mapped values of both items, found or not", jsonOf(output))
	}
	// Only the status cell was sent: the cell that was matched on is not
	// rewritten, and the item that matched nothing wrote nothing.
	batch := setup.fake.callsMatching(`POST .*/values:batchUpdate`)
	if len(batch) != 1 {
		t.Fatalf("batch writes = %d, want 1", len(batch))
	}
	sent := batch[0].Body["data"].([]any)
	if len(sent) != 1 || sent[0].(map[string]any)["range"] != "'Sheet1'!B3" {
		t.Errorf("cells sent = %v, want only 'Sheet1'!B3", sent)
	}
}

func TestSheetsUpdateAutoMapKeepsTheInputItems(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.peopleTab()
	// What a read produced, edited: the node matches on row_number and writes
	// the fields that are columns.
	input := sheetItems(map[string]any{"row_number": 3.0, "name": "Bo", "status": "reopened", "age": 29.0, "other": "x"})
	output := setup.run(t, sheetsParams("update", map[string]any{
		"columns": columnsParam("autoMapInputData", map[string]any{}, "row_number"),
		"options": map[string]any{"handlingExtraData": "ignoreIt"},
	}), input)
	expectGrid(t, setup,
		sheetsRow("name", "status", "age"),
		sheetsRow("Ann", "open", 31.0),
		sheetsRow("Bo", "reopened", 29.0),
		sheetsRow("Cy", "open"),
		sheetsRow("Dana"),
	)
	expectJSON(t, output, input[0].JSON)
}

func TestSheetsUpdateRefusesWhatItCannotAddress(t *testing.T) {
	cases := map[string]struct {
		columns map[string]any
		items   []workflow.Item
		want    string
	}{
		"no matching column": {
			columnsParam("defineBelow", map[string]any{"status": "x"}), sheetItems(map[string]any{}), "matching column",
		},
		"an empty match value": {
			columnsParam("defineBelow", map[string]any{"name": "", "status": "x"}, "name"), sheetItems(map[string]any{}), `"name"`,
		},
		"a null row number": {
			columnsParam("defineBelow", map[string]any{"row_number": nil, "status": "x"}, "row_number"), sheetItems(map[string]any{}), "row_number",
		},
		"a row number that is not a number": {
			columnsParam("defineBelow", map[string]any{"row_number": "abc", "status": "x"}, "row_number"), sheetItems(map[string]any{}), "row_number",
		},
		"a match column the sheet lacks": {
			columnsParam("defineBelow", map[string]any{"mail": "a@b", "status": "x"}, "mail"), sheetItems(map[string]any{}), `"mail"`,
		},
		"nothing mapped": {
			columnsParam("defineBelow", map[string]any{}, "name"), sheetItems(map[string]any{}), "maps no columns",
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			setup := newSheetsSetup(t)
			setup.peopleTab()
			_, err := setup.runErr(sheetsParams("update", map[string]any{"columns": test.columns}), test.items)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want one mentioning %q", err, test.want)
			}
			if writes := setup.fake.callsMatching(`^(PUT|POST) `); len(writes) != 0 {
				t.Errorf("writes = %v, want none", writes)
			}
		})
	}
}

// --- Append or update --------------------------------------------------------------

func TestSheetsAppendOrUpdateUpdatesMatchesAndAppendsTheRest(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.peopleTab()
	params := sheetsParams("appendOrUpdate", map[string]any{"columns": columnsParam("defineBelow", map[string]any{
		"name": sheetsExpr("{{ $json.name }}"), "status": sheetsExpr("{{ $json.status }}"), "age": sheetsExpr("{{ $json.age }}"),
	}, "name")})

	setup.run(t, params, sheetItems(
		map[string]any{"name": "Bo", "status": "reopened", "age": 29.0},
		map[string]any{"name": "Eve", "status": "new", "age": 22.0},
		map[string]any{"name": "Fay", "status": "new", "age": 23.0},
	))
	expectGrid(t, setup,
		sheetsRow("name", "status", "age"),
		sheetsRow("Ann", "open", 31.0),
		// The match column is not rewritten; the other mapped cells are.
		sheetsRow("Bo", "reopened", 29.0),
		sheetsRow("Cy", "open"),
		sheetsRow("Dana"),
		sheetsRow("Eve", "new", 22.0),
		sheetsRow("Fay", "new", 23.0),
	)
	// One read, one batch of cell updates, one write for both new rows.
	if got := setup.fake.callsMatching(`GET .*/values/`); len(got) != 1 {
		t.Errorf("sheet reads = %d, want 1\n%s", len(got), setup.fake.callLog())
	}
	if got := setup.fake.callsMatching(`POST .*/values:batchUpdate`); len(got) != 1 {
		t.Errorf("batch updates = %d, want 1\n%s", len(got), setup.fake.callLog())
	}
	if got := setup.fake.callsMatching(`^PUT `); len(got) != 1 {
		t.Errorf("row writes = %d, want 1 for both new rows\n%s", len(got), setup.fake.callLog())
	}
}

// Every item is matched against the sheet as it was when the batch began, so
// two items with a new key both append, as n8n's node does.
func TestSheetsAppendOrUpdateMatchesAgainstTheSheetAsItWas(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.peopleTab()
	params := sheetsParams("appendOrUpdate", map[string]any{"columns": columnsParam("defineBelow", map[string]any{
		"name": sheetsExpr("{{ $json.name }}"), "status": "x",
	}, "name")})
	setup.run(t, params, sheetItems(map[string]any{"name": "Gus"}, map[string]any{"name": "Gus"}))
	got := setup.fake.snapshot("Sheet1")
	if len(got) != 7 {
		t.Fatalf("the sheet holds %d rows, want the header, the four people and two Guses: %v", len(got), got)
	}
}

func TestSheetsAppendOrUpdateAutoMapAppendsAnItemWithoutTheKey(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.peopleTab()
	setup.run(t, sheetsParams("appendOrUpdate", map[string]any{
		"columns": columnsParam("autoMapInputData", map[string]any{}, "name"),
		"options": map[string]any{"handlingExtraData": "ignoreIt"},
	}), sheetItems(map[string]any{"status": "keyless"}, map[string]any{"name": "Ann", "status": "again"}))
	expectGrid(t, setup,
		sheetsRow("name", "status", "age"),
		sheetsRow("Ann", "again", 31.0),
		sheetsRow("Bo", "closed", 28.0),
		sheetsRow("Cy", "open"),
		sheetsRow("Dana"),
		sheetsRow("", "keyless"),
	)
}

// --- Delete ----------------------------------------------------------------------

func TestSheetsDeleteSpans(t *testing.T) {
	grid := func(setup *sheetsSetup) {
		setup.fake.addTab(5, "Sheet1", 5,
			sheetsRow("a", "b", "c", "d"),
			sheetsRow(1.0, 2.0, 3.0, 4.0),
			sheetsRow(5.0, 6.0, 7.0, 8.0),
			sheetsRow(9.0, 10.0, 11.0, 12.0),
		)
	}
	t.Run("rows are counted from one, and the span starts at the start row", func(t *testing.T) {
		setup := newSheetsSetup(t)
		grid(setup)
		output := setup.run(t, sheetsParams("delete", map[string]any{"toDelete": "rows", "startIndex": float64(3), "numberToDelete": float64(2)}), nil)
		expectGrid(t, setup, sheetsRow("a", "b", "c", "d"), sheetsRow(1.0, 2.0, 3.0, 4.0))
		expectJSON(t, output, map[string]any{"success": true})
		call := setup.fake.callsMatching(`^POST .*:batchUpdate$`)[0]
		span := call.Body["requests"].([]any)[0].(map[string]any)["deleteDimension"].(map[string]any)["range"].(map[string]any)
		if span["sheetId"] != 5.0 || span["dimension"] != "ROWS" || span["startIndex"] != 2.0 || span["endIndex"] != 4.0 {
			t.Errorf("span = %v, want sheet 5 rows [2,4)", span)
		}
	})
	t.Run("rows default to row 2 and one row", func(t *testing.T) {
		setup := newSheetsSetup(t)
		grid(setup)
		setup.run(t, sheetsParams("delete", nil), nil)
		expectGrid(t, setup, sheetsRow("a", "b", "c", "d"), sheetsRow(5.0, 6.0, 7.0, 8.0), sheetsRow(9.0, 10.0, 11.0, 12.0))
	})
	t.Run("columns take a letter", func(t *testing.T) {
		setup := newSheetsSetup(t)
		grid(setup)
		setup.run(t, sheetsParams("delete", map[string]any{"toDelete": "columns", "startIndex": "B", "numberToDelete": float64(2)}), nil)
		expectGrid(t, setup, sheetsRow("a", "d"), sheetsRow(1.0, 4.0), sheetsRow(5.0, 8.0), sheetsRow(9.0, 12.0))
	})
	t.Run("several items delete in order in one request", func(t *testing.T) {
		setup := newSheetsSetup(t)
		grid(setup)
		setup.run(t, sheetsParams("delete", map[string]any{"startIndex": float64(2)}), sheetItems(map[string]any{}, map[string]any{}))
		expectGrid(t, setup, sheetsRow("a", "b", "c", "d"), sheetsRow(9.0, 10.0, 11.0, 12.0))
		if got := setup.fake.callsMatching(`^POST .*:batchUpdate$`); len(got) != 1 {
			t.Errorf("requests = %d, want 1", len(got))
		}
	})
	t.Run("a bad span is refused before anything is sent", func(t *testing.T) {
		setup := newSheetsSetup(t)
		grid(setup)
		for name, parameters := range map[string]map[string]any{
			"row 0":       {"startIndex": float64(0)},
			"count 0":     {"numberToDelete": float64(0)},
			"bad column":  {"toDelete": "columns", "startIndex": "7"},
			"unknown dim": {"toDelete": "tabs"},
		} {
			if _, err := setup.runErr(sheetsParams("delete", parameters), nil); err == nil {
				t.Errorf("%s: no error", name)
			}
		}
		if got := setup.fake.callsMatching(`^POST .*:batchUpdate$`); len(got) != 0 {
			t.Errorf("requests = %v, want none", got)
		}
	})
}

// --- Clear -----------------------------------------------------------------------

func TestSheetsClear(t *testing.T) {
	grid := func(setup *sheetsSetup) {
		setup.fake.addTab(0, "Sheet1", 5,
			sheetsRow("a", "b", "c"),
			sheetsRow(1.0, 2.0, 3.0),
			sheetsRow(4.0, 5.0, 6.0),
			sheetsRow(7.0, 8.0, 9.0),
		)
	}
	t.Run("the whole sheet", func(t *testing.T) {
		setup := newSheetsSetup(t)
		grid(setup)
		input := sheetItems(map[string]any{"keep": "me"})
		output := setup.run(t, sheetsParams("clear", nil), input)
		expectGrid(t, setup)
		// The input items pass through.
		expectJSON(t, output, map[string]any{"keep": "me"})
		if output[0].Paired == nil || output[0].Paired.SourceNodeID != "source" {
			t.Errorf("lineage = %#v", output[0].Paired)
		}
	})
	t.Run("the whole sheet but its first row", func(t *testing.T) {
		setup := newSheetsSetup(t)
		grid(setup)
		setup.run(t, sheetsParams("clear", map[string]any{"clear": "wholeSheet", "keepFirstRow": true}), nil)
		expectGrid(t, setup, sheetsRow("a", "b", "c"))
	})
	t.Run("specific rows", func(t *testing.T) {
		setup := newSheetsSetup(t)
		grid(setup)
		setup.run(t, sheetsParams("clear", map[string]any{"clear": "specificRows", "startIndex": float64(2), "rowsToDelete": float64(2)}), nil)
		expectGrid(t, setup, sheetsRow("a", "b", "c"), sheetsRow(), sheetsRow(), sheetsRow(7.0, 8.0, 9.0))
	})
	t.Run("specific columns", func(t *testing.T) {
		setup := newSheetsSetup(t)
		grid(setup)
		setup.run(t, sheetsParams("clear", map[string]any{"clear": "specificColumns", "startIndex": "B", "columnsToDelete": float64(2)}), nil)
		expectGrid(t, setup, sheetsRow("a"), sheetsRow(1.0), sheetsRow(4.0), sheetsRow(7.0))
	})
	t.Run("a specific range, whatever sheet name it carries", func(t *testing.T) {
		setup := newSheetsSetup(t)
		grid(setup)
		setup.run(t, sheetsParams("clear", map[string]any{"clear": "specificRange", "range": "Other!A2:B3"}), nil)
		expectGrid(t, setup, sheetsRow("a", "b", "c"), sheetsRow("", "", 3.0), sheetsRow("", "", 6.0), sheetsRow(7.0, 8.0, 9.0))
	})
}

// A write after a read in the same call must not see the read's stale copy.
func TestSheetsWritesInvalidateWhatWasRead(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.peopleTab()
	// append reads the sheet to count its rows; if the cache survived the write,
	// the next batch element would count the old length. Two items, one write.
	setup.run(t, sheetsParams("append", map[string]any{"columns": columnsParam("defineBelow", map[string]any{"name": "N"})}),
		sheetItems(map[string]any{}, map[string]any{}))
	if got := setup.fake.snapshot("Sheet1"); len(got) != 7 {
		t.Fatalf("rows = %d, want 7: %v", len(got), got)
	}
}
