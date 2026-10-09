package nodes_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// A column's name is its heading as written, trailing space included: the
// picker lists it that way, a mapped value is keyed by it, a read keys the row
// by it and a filter finds it by it. One rule everywhere, so a heading like
// "Email " cannot be offered under one name and written under another.
func TestSheetsColumnNamesAreTheHeadingAsWritten(t *testing.T) {
	setup := newSheetsSetup(t)
	setup.fake.addTab(0, "Sheet1", 20,
		sheetsRow("Email ", "name"),
		sheetsRow("a@b", "Ann"),
	)

	read := setup.run(t, sheetsParams("read", nil), nil)
	expectJSON(t, read, map[string]any{"row_number": 2.0, "Email ": "a@b", "name": "Ann"})

	filtered := setup.run(t, sheetsParams("read", map[string]any{"filtersUI": map[string]any{"values": []any{
		map[string]any{"lookupColumn": "Email ", "lookupValue": "a@b"},
	}}}), nil)
	if len(filtered) != 1 {
		t.Fatalf("a filter on %q found %d rows, want 1", "Email ", len(filtered))
	}

	setup.run(t, sheetsParams("append", map[string]any{"columns": columnsParam("defineBelow", map[string]any{
		"Email ": "c@d", "name": "Bo",
	})}), nil)
	expectGrid(t, setup, sheetsRow("Email ", "name"), sheetsRow("a@b", "Ann"), sheetsRow("c@d", "Bo"))

	// The name without the space is a different, missing column.
	_, err := setup.runErr(sheetsParams("append", map[string]any{"columns": columnsParam("defineBelow", map[string]any{"Email": "x"})}), nil)
	if err == nil || !strings.Contains(err.Error(), `"Email"`) {
		t.Fatalf("error = %v, want the trimmed name refused as a column the sheet lacks", err)
	}
}

// A sheet name before the "!" of a range is dropped, because the tab is chosen
// by the node's own Sheet setting. The "!" that ends the name is the last one
// outside quotes: a tab may be called "a!b".
func TestSheetsRangeOptionDropsTheSheetPartAtTheLastBangOutsideQuotes(t *testing.T) {
	for _, region := range []string{"'a!b'!B1:B3", "Other!B1:B3", "B1:B3"} {
		t.Run(region, func(t *testing.T) {
			setup := newSheetsSetup(t)
			setup.fake.addTab(0, "Sheet1", 10, sheetsRow("a", "b"), sheetsRow("1", "2"), sheetsRow("3", "4"))
			output := setup.run(t, sheetsParams("read", map[string]any{"options": map[string]any{"dataLocationOnSheet": map[string]any{
				"values": map[string]any{"rangeDefinition": "specifyRangeA1", "range": region},
			}}}), nil)
			expectJSON(t, output, map[string]any{"row_number": 2.0, "b": "2"}, map[string]any{"row_number": 3.0, "b": "4"})
			reads := setup.fake.callsMatching(`GET .*/values/`)
			if len(reads) != 1 || !strings.HasSuffix(reads[0].Path, "'Sheet1'!B1:B3") {
				t.Fatalf("reads = %v, want one of 'Sheet1'!B1:B3", reads)
			}
		})
	}
	t.Run("clear", func(t *testing.T) {
		setup := newSheetsSetup(t)
		setup.fake.addTab(0, "Sheet1", 10, sheetsRow("a", "b"), sheetsRow("1", "2"))
		setup.run(t, sheetsParams("clear", map[string]any{"clear": "specificRange", "range": "'a!b'!B2:B2"}), nil)
		expectGrid(t, setup, sheetsRow("a", "b"), sheetsRow("1"))
	})
}

// Automatic mapping: a match value that is empty text finds nothing, rather than
// the first row whose match cell is empty too.
func TestSheetsAutoMapTreatsAnEmptyMatchValueAsNoMatch(t *testing.T) {
	grid := func(setup *sheetsSetup) {
		setup.fake.addTab(0, "Sheet1", 10,
			sheetsRow("name", "status"),
			sheetsRow("", "blank-key row"),
			sheetsRow("Ann", "open"),
		)
	}
	auto := columnsParam("autoMapInputData", map[string]any{}, "name")
	options := map[string]any{"handlingExtraData": "ignoreIt"}

	t.Run("update leaves the sheet alone", func(t *testing.T) {
		setup := newSheetsSetup(t)
		grid(setup)
		setup.run(t, sheetsParams("update", map[string]any{"columns": auto, "options": options}),
			sheetItems(map[string]any{"name": "", "status": "overwritten"}))
		expectGrid(t, setup, sheetsRow("name", "status"), sheetsRow("", "blank-key row"), sheetsRow("Ann", "open"))
	})
	t.Run("appendOrUpdate appends it", func(t *testing.T) {
		setup := newSheetsSetup(t)
		grid(setup)
		setup.run(t, sheetsParams("appendOrUpdate", map[string]any{"columns": auto, "options": options}),
			sheetItems(map[string]any{"name": "", "status": "new row"}))
		expectGrid(t, setup, sheetsRow("name", "status"), sheetsRow("", "blank-key row"), sheetsRow("Ann", "open"), sheetsRow("", "new row"))
	})
}

// The reasons the node gives are its own words, and each names what to fix.
func TestSheetsErrorsSayWhatToFix(t *testing.T) {
	cases := map[string]struct {
		parameters func() map[string]any
		build      func(*sheetsSetup)
		want       []string
	}{
		"a header row past the rows read": {
			parameters: func() map[string]any {
				return sheetsParams("read", map[string]any{"options": map[string]any{"dataLocationOnSheet": map[string]any{
					"values": map[string]any{"rangeDefinition": "specifyRange", "headerRow": 9.0, "firstDataRow": 10.0},
				}}})
			},
			build: func(setup *sheetsSetup) { setup.peopleTab() },
			want:  []string{"header row 9", "rows read"},
		},
		"a first data row above the header": {
			parameters: func() map[string]any {
				return sheetsParams("read", map[string]any{"options": map[string]any{"dataLocationOnSheet": map[string]any{
					"values": map[string]any{"rangeDefinition": "specifyRange", "headerRow": 3.0, "firstDataRow": 2.0},
				}}})
			},
			build: func(setup *sheetsSetup) { setup.peopleTab() },
			want:  []string{"first data row 2", "header row 3"},
		},
		"a filter on a column the sheet lacks": {
			parameters: func() map[string]any {
				return sheetsParams("read", map[string]any{"filtersUI": map[string]any{"values": []any{
					map[string]any{"lookupColumn": "nope", "lookupValue": "x"},
				}}})
			},
			build: func(setup *sheetsSetup) { setup.peopleTab() },
			want:  []string{`no column "nope"`, "filter"},
		},
		"no column names to map by": {
			parameters: func() map[string]any {
				return sheetsParams("append", map[string]any{
					"columns": columnsParam("defineBelow", map[string]any{"name": "x"}),
					"options": map[string]any{"locationDefine": map[string]any{"values": map[string]any{"headerRow": 9.0}}},
				})
			},
			build: func(setup *sheetsSetup) { setup.peopleTab() },
			want:  []string{"row 9", "no column names"},
		},
		"nothing mapped": {
			parameters: func() map[string]any {
				return sheetsParams("append", map[string]any{"columns": columnsParam("defineBelow", map[string]any{})})
			},
			build: func(setup *sheetsSetup) { setup.peopleTab() },
			want:  []string{"item 0", "maps no columns"},
		},
		"a mapped column the sheet has lost": {
			parameters: func() map[string]any {
				return sheetsParams("append", map[string]any{"columns": columnsParam("defineBelow", map[string]any{"gone": "x"})})
			},
			build: func(setup *sheetsSetup) { setup.peopleTab() },
			want:  []string{`"gone"`, "no longer"},
		},
		"a match column the sheet lacks": {
			parameters: func() map[string]any {
				return sheetsParams("update", map[string]any{
					"columns": columnsParam("autoMapInputData", map[string]any{}, "mail"),
					"options": map[string]any{"handlingExtraData": "ignoreIt"},
				})
			},
			build: func(setup *sheetsSetup) { setup.peopleTab() },
			want:  []string{`"mail"`, "matched on"},
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			setup := newSheetsSetup(t)
			test.build(setup)
			_, err := setup.runErr(test.parameters(), nil)
			if err == nil {
				t.Fatal("no error")
			}
			for _, fragment := range test.want {
				if !strings.Contains(err.Error(), fragment) {
					t.Errorf("error = %q, want it to hold %q", err, fragment)
				}
			}
		})
	}
}

// The trigger keeps old and both out of its output, so a trigger that asks for
// them is refused, not run as something else.
func TestGoogleSheetsTriggerValidateRefusesIncludeInOutputOldAndBoth(t *testing.T) {
	t.Parallel()

	definition, _ := sheetsRegistry(t).Get(nodes.GoogleSheetsTriggerNodeType, workflow.V(1))
	build := func(include string) workflow.Node {
		return workflow.Node{
			ID: "trigger", Name: "Trigger", Type: nodes.GoogleSheetsTriggerNodeType, TypeVersion: workflow.V(1),
			Parameters: map[string]any{
				"event":           "rowUpdate",
				"includeInOutput": include,
				"documentId":      sheetsLocator("id", sheetsDoc),
				"sheetName":       sheetsLocator("name", "Sheet1"),
			},
			Credentials: map[string]string{nodes.GoogleSheetsTriggerCredentialType: "cred"},
		}
	}
	if err := definition.Validate(build("new")); err != nil {
		t.Errorf("Validate(new) = %v", err)
	}
	for _, include := range []string{"old", "both"} {
		if err := definition.Validate(build(include)); err == nil || !strings.Contains(err.Error(), "includeInOutput") {
			t.Errorf("Validate(%s) = %v, want a refusal naming includeInOutput", include, err)
		}
	}
}

// Columns to Watch is picked from the tab's own headings, so a native user can
// set it; the headings come from the row the trigger's settings name, which is
// why the loader reads the Options collection too.
func TestGoogleSheetsTriggerColumnsToWatchIsPickedFromTheHeadings(t *testing.T) {
	t.Parallel()

	definition, _ := sheetsRegistry(t).Get(nodes.GoogleSheetsTriggerNodeType, workflow.V(1))
	for _, parameter := range definition.Parameters {
		if parameter.Key != "options" {
			continue
		}
		for _, field := range parameter.Fields {
			if field.Key != "columnsToWatch" {
				continue
			}
			loader := field.LoadOptions
			if loader == nil || loader.Name != "googleSheets.headings" || loader.CredentialType != nodes.GoogleSheetsTriggerCredentialType {
				t.Fatalf("columnsToWatch loader = %#v, want the headings loader under the trigger's credential", loader)
			}
			for _, key := range []string{"documentId", "sheetName", "options"} {
				if !slices.Contains(loader.DependsOn, key) {
					t.Errorf("columnsToWatch loader depends on %v, want %q among them", loader.DependsOn, key)
				}
			}
			return
		}
	}
	t.Fatal("the trigger has no options.columnsToWatch")
}

func TestGoogleSheetsColumnsMapperReadsTheOptionsToo(t *testing.T) {
	t.Parallel()

	definition, _ := sheetsRegistry(t).Get(nodes.GoogleSheetsNodeType, workflow.V(1))
	for _, parameter := range definition.Parameters {
		if parameter.Key != "columns" {
			continue
		}
		if !slices.Contains(parameter.Mapper.Schema.DependsOn, "options") {
			t.Fatalf("columns schema depends on %v, want options among them so Data Location on Sheet moves the header row", parameter.Mapper.Schema.DependsOn)
		}
		return
	}
	t.Fatal("the node has no columns parameter")
}
