package nodes_test

import (
	"reflect"
	"testing"
)

// A sheet cell can hold anything, including text that looks like a template.
// Once an expression has produced a value, that value is data: it is written
// to a cell, and compared with a cell, as the characters it is. Evaluating it
// a second time would run text a stranger put in a spreadsheet, with `$env` in
// reach (BUG-prhxdy). The plain node and the tool both hold to this.

const cellTemplate = "{{ $env.X }}"

func (setup *sheetsSetup) withEnv() {
	setup.request.Env = map[string]string{"X": "leaked"}
}

// Both spellings of an author's expression are resolved once: the explicit
// marker, and the plain string holding {{ }} that an import leaves nested.
func templateSpellings() map[string]func(string) any {
	return map[string]func(string) any{
		"expression marker": func(template string) any { return sheetsExpr(template) },
		"plain template":    func(template string) any { return template },
	}
}

func TestACellHoldingTemplateTextIsWrittenLiterallyThroughColumnsValue(t *testing.T) {
	for name, spell := range templateSpellings() {
		t.Run(name, func(t *testing.T) {
			setup := newSheetsSetup(t)
			setup.withEnv()
			setup.fake.addTab(0, "Sheet1", 20, sheetsRow("name", "note"))

			setup.run(t, sheetsParams("append", map[string]any{"columns": columnsParam("defineBelow", map[string]any{
				"name": "Ann", "note": spell("{{ $json.x }}"),
			})}), sheetItems(map[string]any{"x": cellTemplate}))
			expectGrid(t, setup, sheetsRow("name", "note"), sheetsRow("Ann", cellTemplate))

			// The same through an update that finds the row by name.
			setup.run(t, sheetsParams("update", map[string]any{"columns": columnsParam("defineBelow", map[string]any{
				"name": "Ann", "note": spell("{{ $json.y }}"),
			}, "name")}), sheetItems(map[string]any{"y": cellTemplate + " again"}))
			expectGrid(t, setup, sheetsRow("name", "note"), sheetsRow("Ann", cellTemplate+" again"))
		})
	}
}

func TestACellHoldingTemplateTextIsComparedLiterallyThroughALookupValue(t *testing.T) {
	for name, spell := range templateSpellings() {
		t.Run(name, func(t *testing.T) {
			setup := newSheetsSetup(t)
			setup.withEnv()
			setup.fake.addTab(0, "Sheet1", 20,
				sheetsRow("name", "note"),
				sheetsRow("Ann", "open"),
				sheetsRow("Bo", cellTemplate),
				sheetsRow("Cy", "leaked"),
			)

			output := setup.run(t, sheetsParams("read", map[string]any{"filtersUI": map[string]any{"values": []any{map[string]any{
				"lookupColumn": "note", "lookupValue": spell("{{ $json.x }}"),
			}}}}), sheetItems(map[string]any{"x": cellTemplate}))
			got := jsonOf(output)
			want := []map[string]any{{"row_number": float64(3), "name": "Bo", "note": cellTemplate}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("rows = %v, want only the row holding the template text %v", got, want)
			}
		})
	}
}
