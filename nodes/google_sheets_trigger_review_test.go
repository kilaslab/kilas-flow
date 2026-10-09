package nodes_test

import (
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
