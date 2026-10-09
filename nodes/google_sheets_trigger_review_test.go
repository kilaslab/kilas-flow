package nodes_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// What a row's fingerprint was made from is part of the cursor's meaning. Edit
// the trigger so the fingerprints would be made differently and every row
// would read as changed: the tick after the edit starts over instead.
func TestSheetsTriggerStartsAgainWhenItsConfigurationChanges(t *testing.T) {
	watching := func(event string, options map[string]any) workflow.Node { return triggerNode(event, options) }
	for name, change := range map[string]struct {
		before, after workflow.Node
	}{
		"the columns to watch": {
			watching("rowUpdate", map[string]any{"columnsToWatch": []any{"status"}}),
			watching("rowUpdate", map[string]any{"columnsToWatch": []any{"age"}}),
		},
		"the columns to watch and the event together": {
			watching("rowUpdate", map[string]any{"columnsToWatch": []any{"status"}}),
			watching("anyUpdate", map[string]any{"columnsToWatch": []any{"age"}}),
		},
		"the event alone (it changes how values are read)": {
			watching("rowAdded", nil),
			watching("rowUpdate", nil),
		},
		"the value rendering": {
			watching("rowAdded", map[string]any{"valueRender": "UNFORMATTED_VALUE"}),
			watching("rowAdded", map[string]any{"valueRender": "FORMATTED_VALUE"}),
		},
		"the date rendering": {
			watching("rowAdded", map[string]any{"dateTimeRenderOption": "FORMATTED_STRING"}),
			watching("rowAdded", map[string]any{"dateTimeRenderOption": "SERIAL_NUMBER"}),
		},
		"the range": {
			watching("anyUpdate", map[string]any{"dataLocationOnSheet": map[string]any{"values": map[string]any{
				"rangeDefinition": "specifyRangeA1", "range": "A:C"}}}),
			watching("anyUpdate", map[string]any{"dataLocationOnSheet": map[string]any{"values": map[string]any{
				"rangeDefinition": "specifyRangeA1", "range": "A1:C"}}}),
		},
		"the header row": {
			watching("anyUpdate", map[string]any{"dataLocationOnSheet": map[string]any{"values": map[string]any{
				"rangeDefinition": "specifyRange", "headerRow": 1.0, "firstDataRow": 2.0}}}),
			watching("anyUpdate", map[string]any{"dataLocationOnSheet": map[string]any{"values": map[string]any{
				"rangeDefinition": "specifyRange", "headerRow": 1.0, "firstDataRow": 3.0}}}),
		},
	} {
		t.Run(name, func(t *testing.T) {
			setup := newTriggerSetup(t)
			tab := setup.people()
			setup.poll(t, change.before)

			if got := setup.poll(t, change.after); len(got) != 0 {
				t.Fatalf("the tick after the edit emitted %v, want nothing: a changed setting starts a new baseline", got)
			}
			// And the new baseline is a working one.
			tab.rows[1][1] = "reopened"
			tab.rows[1][2] = 99.0
			tab.rows[2][1] = "changed"
			tab.rows[2][2] = 98.0
			if got := setup.poll(t, change.after); len(got) == 0 && change.after.Parameters["event"] != "rowAdded" {
				t.Fatalf("a change after the new baseline emitted nothing")
			}
		})
	}
}

// Update events read the values as numbers and dates as serial numbers, so a
// heading that is a date arrives as 45123. Columns are named by the text the
// sheet shows, as the picker lists them, so the trigger reads the heading row
// as shown.
func TestSheetsTriggerNamesColumnsByTheHeadingAsTheSheetShowsIt(t *testing.T) {
	setup := newTriggerSetup(t)
	tab := setup.fake.addTab(0, "Sheet1", 20,
		sheetsRow(fakeFormatted{Raw: 45123.0, Shown: "2023-07-01"}, "name"),
		sheetsRow(5.0, "Ann"),
	)
	node := triggerNode("anyUpdate", map[string]any{"columnsToWatch": []any{"2023-07-01"}})
	setup.poll(t, node)

	tab.rows[1][0] = 6.0
	got := setup.poll(t, node)
	want := []map[string]any{{"row_number": 2.0, "change_type": "updated", "2023-07-01": 6.0, "name": "Ann"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v: keyed by the heading the sheet shows, not its serial number", got, want)
	}

	// Plain text headings need no second read of the header row.
	plain := newTriggerSetup(t)
	plain.people()
	plain.poll(t, triggerNode("anyUpdate", nil))
	if reads := plain.fake.callsMatching(`GET .*/values/`); len(reads) != 1 {
		t.Errorf("value reads on a tick = %d, want 1 when the headings are text", len(reads))
	}
}

// The tab is identified by its gid; a rename is not a different tab, and must
// not throw the fingerprints away.
func TestSheetsTriggerKeepsItsBaselineWhenTheTabIsRenamed(t *testing.T) {
	setup := newTriggerSetup(t)
	tab := setup.people()
	node := triggerNode("anyUpdate", nil)
	node.Parameters["sheetName"] = sheetsLocator("list", float64(0))
	setup.poll(t, node)

	tab.title = "Orders"
	tab.rows[1][1] = "closed"
	got := setup.poll(t, node)
	if len(got) != 1 || got[0]["row_number"] != 2.0 {
		t.Fatalf("rows = %v, want the one changed row: a renamed tab keeps its baseline", got)
	}
}

// A watched column is named by its heading as written, like every other column.
func TestSheetsTriggerWatchesAColumnByItsHeadingAsWritten(t *testing.T) {
	setup := newTriggerSetup(t)
	tab := setup.fake.addTab(0, "Sheet1", 20,
		sheetsRow("Email ", "name"),
		sheetsRow("a@b", "Ann"),
	)
	node := triggerNode("rowUpdate", map[string]any{"columnsToWatch": []any{"Email "}})
	setup.poll(t, node)

	tab.rows[1][1] = "Anna" // the name is not watched
	if got := setup.poll(t, node); len(got) != 0 {
		t.Fatalf("a change outside the watched column emitted %v", got)
	}
	tab.rows[1][0] = "c@d"
	got := setup.poll(t, node)
	if len(got) != 1 || got[0]["Email "] != "c@d" {
		t.Fatalf("rows = %v, want the row whose Email column changed", got)
	}
}

// A tick that finds nothing new leaves the cursor exactly as it was.
func TestSheetsTriggerLeavesTheCursorAloneWhenNothingChanged(t *testing.T) {
	setup := newTriggerSetup(t)
	tab := setup.people()
	node := triggerNode("anyUpdate", nil)
	setup.poll(t, node)
	before := setup.cursor
	if before == "" {
		t.Fatal("the baseline tick stored no cursor")
	}

	if got := setup.poll(t, node); len(got) != 0 {
		t.Fatalf("an unchanged sheet emitted %v", got)
	}
	if setup.cursor != before {
		t.Errorf("an unchanged sheet rewrote the cursor:\n before %s\n  after %s", before, setup.cursor)
	}

	tab.rows[1][1] = "closed"
	setup.poll(t, node)
	if setup.cursor == before {
		t.Error("a change left the cursor as it was, so the next tick would report it again")
	}
}

// Rows are told apart by their position. Delete a row and the rows below it
// move up one, so each of them now holds what its upper neighbour held: they
// are reported as updated, and the node's description says so.
func TestSheetsTriggerReportsTheRowsBelowADeletedRowAsUpdated(t *testing.T) {
	for _, event := range []string{"rowUpdate", "anyUpdate"} {
		t.Run(event, func(t *testing.T) {
			setup := newTriggerSetup(t)
			tab := setup.fake.addTab(0, "Sheet1", 100,
				sheetsRow("name", "status"),
				sheetsRow("Ann", "open"),
				sheetsRow("Bo", "closed"),
				sheetsRow("Cy", "open"),
			)
			node := triggerNode(event, nil)
			setup.poll(t, node)

			tab.rows = append(tab.rows[:1:1], tab.rows[2:]...) // Ann's row is deleted
			got := setup.poll(t, node)
			if len(got) != 2 || got[0]["row_number"] != 2.0 || got[0]["name"] != "Bo" || got[1]["row_number"] != 3.0 || got[1]["name"] != "Cy" {
				t.Fatalf("rows = %v, want Bo (now row 2) and Cy (now row 3) reported", got)
			}
		})
	}
	definition, _ := sheetsRegistry(t).Get(nodes.GoogleSheetsTriggerNodeType, workflow.V(1))
	if !strings.Contains(definition.Description, "by position") || !strings.Contains(definition.Description, "deleting a row") {
		t.Errorf("description = %q, want it to say rows are compared by position and what deleting one does", definition.Description)
	}
}
