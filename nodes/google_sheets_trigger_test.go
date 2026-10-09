package nodes_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/poller"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

type triggerSetup struct {
	fake    *fakeSheets
	poller  *nodes.GoogleSheetsPoller
	request engine.Request
	cursor  string
}

func newTriggerSetup(t *testing.T) *triggerSetup {
	t.Helper()
	fake := newFakeSheets(t, sheetsDoc)
	return &triggerSetup{
		fake:   fake,
		poller: nodes.NewGoogleSheetsPoller(sheetsPolicy(t, fake.endpoint())),
		request: engine.Request{Credentials: googleCreds{
			typ: nodes.GoogleSheetsTriggerCredentialType, host: httptestHost(fake.server.URL),
		}},
	}
}

func triggerNode(event string, options map[string]any) workflow.Node {
	parameters := map[string]any{
		"event":      event,
		"documentId": sheetsLocator("id", sheetsDoc),
		"sheetName":  sheetsLocator("name", "Sheet1"),
	}
	if options != nil {
		parameters["options"] = options
	}
	return workflow.Node{
		ID: "trigger", Name: "Sheets Trigger", Type: nodes.GoogleSheetsTriggerNodeType, TypeVersion: workflow.V(1),
		Parameters: parameters, Credentials: map[string]string{nodes.GoogleSheetsTriggerCredentialType: "cred"},
	}
}

// poll runs one tick, keeping the cursor between ticks as the poll service does.
func (setup *triggerSetup) poll(t *testing.T, node workflow.Node) []map[string]any {
	t.Helper()
	items, err := setup.pollErr(node)
	if err != nil {
		t.Fatalf("Poll() error = %v\ncalls:\n%s", err, setup.fake.callLog())
	}
	return jsonOf(items)
}

func (setup *triggerSetup) pollErr(node workflow.Node) ([]workflow.Item, error) {
	items, next, err := setup.poller.Poll(context.Background(), poller.Claim{
		Cursor:  repository.PollCursor{Cursor: setup.cursor},
		Node:    node,
		Request: setup.request,
		Now:     time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC),
	})
	if err == nil {
		setup.cursor = next
	}
	return items, err
}

func (setup *triggerSetup) people() *fakeTab {
	return setup.fake.addTab(0, "Sheet1", 100,
		sheetsRow("name", "status", "age"),
		sheetsRow("Ann", "open", 31.0),
		sheetsRow("Bo", "closed", 28.0),
	)
}

func TestSheetsTriggerFirstTickOnlyRecordsABaseline(t *testing.T) {
	for _, event := range []string{"rowAdded", "rowUpdate", "anyUpdate"} {
		t.Run(event, func(t *testing.T) {
			setup := newTriggerSetup(t)
			setup.people()
			if got := setup.poll(t, triggerNode(event, nil)); len(got) != 0 {
				t.Fatalf("the first tick emitted %v, want the rows already there left alone", got)
			}
			if setup.cursor == "" {
				t.Fatal("the first tick stored no cursor")
			}
			var stored struct {
				V       int               `json:"v"`
				LastRow int               `json:"lastRow"`
				Rows    map[string]string `json:"rows"`
			}
			if err := json.Unmarshal([]byte(setup.cursor), &stored); err != nil {
				t.Fatalf("cursor %q is not JSON: %v", setup.cursor, err)
			}
			if stored.V != 1 || stored.LastRow != 3 || len(stored.Rows) != 2 {
				t.Errorf("cursor = %+v, want version 1, last row 3 and a hash for each of the two rows", stored)
			}
			// A second tick with nothing changed emits nothing either.
			if got := setup.poll(t, triggerNode(event, nil)); len(got) != 0 {
				t.Fatalf("an unchanged sheet emitted %v", got)
			}
		})
	}
}

func TestSheetsTriggerRowAddedEmitsRowsPastTheLastOne(t *testing.T) {
	setup := newTriggerSetup(t)
	tab := setup.people()
	node := triggerNode("rowAdded", nil)
	setup.poll(t, node)

	tab.rows = append(tab.rows, sheetsRow("Cy", "open", 40.0), sheetsRow("Dana", "closed"))
	got := setup.poll(t, node)
	// Rows are keyed by the header, with no row_number, as n8n's trigger emits them.
	want := []map[string]any{
		{"name": "Cy", "status": "open", "age": 40.0},
		{"name": "Dana", "status": "closed", "age": ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	// Edits to old rows are not additions.
	tab.rows[1][1] = "reopened"
	if got := setup.poll(t, node); len(got) != 0 {
		t.Fatalf("an edit emitted %v for rowAdded", got)
	}
}

func TestSheetsTriggerRowUpdateEmitsChangedRowsOnly(t *testing.T) {
	setup := newTriggerSetup(t)
	tab := setup.people()
	node := triggerNode("rowUpdate", nil)
	setup.poll(t, node)

	tab.rows[2][2] = 29.0                             // Bo's age changes
	tab.rows = append(tab.rows, sheetsRow("Cy", "x")) // a new row is not an update
	got := setup.poll(t, node)
	want := []map[string]any{{"row_number": 3.0, "name": "Bo", "status": "closed", "age": 29.0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	// Reported once: the next tick sees no change.
	if got := setup.poll(t, node); len(got) != 0 {
		t.Fatalf("the same change was reported twice: %v", got)
	}
}

func TestSheetsTriggerAnyUpdateTellsAddedFromUpdated(t *testing.T) {
	setup := newTriggerSetup(t)
	tab := setup.people()
	node := triggerNode("anyUpdate", nil)
	setup.poll(t, node)

	tab.rows[1][1] = "closed"
	tab.rows = append(tab.rows, sheetsRow("Cy", "open", 40.0))
	got := setup.poll(t, node)
	want := []map[string]any{
		{"row_number": 2.0, "change_type": "updated", "name": "Ann", "status": "closed", "age": 31.0},
		{"row_number": 4.0, "change_type": "added", "name": "Cy", "status": "open", "age": 40.0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}

func TestSheetsTriggerColumnsToWatch(t *testing.T) {
	setup := newTriggerSetup(t)
	tab := setup.people()
	node := triggerNode("rowUpdate", map[string]any{"columnsToWatch": []any{"status"}})
	setup.poll(t, node)

	tab.rows[1][2] = 99.0 // age is not watched
	if got := setup.poll(t, node); len(got) != 0 {
		t.Fatalf("a change in an unwatched column emitted %v", got)
	}
	tab.rows[1][1] = "closed" // status is
	got := setup.poll(t, node)
	if len(got) != 1 || got[0]["row_number"] != 2.0 || got[0]["age"] != 99.0 {
		t.Fatalf("rows = %v, want row 2, carrying its current values", got)
	}
	bad := triggerNode("rowUpdate", map[string]any{"columnsToWatch": []any{"nope"}})
	if _, err := setup.pollErr(bad); err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Fatalf("error = %v, want a refusal naming the column the sheet lacks", err)
	}
}

func TestSheetsTriggerDeletedRowsLowerTheMark(t *testing.T) {
	setup := newTriggerSetup(t)
	tab := setup.people()
	node := triggerNode("rowAdded", nil)
	setup.poll(t, node)

	// Bo is deleted and Eve takes row 3: the sheet is as long as it was, and Eve
	// is still new.
	tab.rows = [][]any{tab.rows[0], tab.rows[1]}
	if got := setup.poll(t, node); len(got) != 0 {
		t.Fatalf("a deletion emitted %v", got)
	}
	tab.rows = append(tab.rows, sheetsRow("Eve", "new", 22.0))
	got := setup.poll(t, node)
	if len(got) != 1 || got[0]["name"] != "Eve" {
		t.Fatalf("rows = %v, want Eve reported as added after the deletion", got)
	}
}

func TestSheetsTriggerDataLocation(t *testing.T) {
	t.Run("an A1 range sets the header row and the offset of row numbers", func(t *testing.T) {
		setup := newTriggerSetup(t)
		tab := setup.fake.addTab(0, "Sheet1", 100,
			sheetsRow("ignore me"),
			sheetsRow("name", "qty"),
			sheetsRow("a", 1.0),
		)
		node := triggerNode("anyUpdate", map[string]any{"dataLocationOnSheet": map[string]any{"values": map[string]any{
			"rangeDefinition": "specifyRangeA1", "range": "A2:B",
		}}})
		setup.poll(t, node)
		tab.rows[2][1] = 2.0
		got := setup.poll(t, node)
		want := []map[string]any{{"row_number": 3.0, "change_type": "updated", "name": "a", "qty": 2.0}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("rows = %v, want %v", got, want)
		}
	})
	t.Run("rows by number", func(t *testing.T) {
		setup := newTriggerSetup(t)
		tab := setup.fake.addTab(0, "Sheet1", 100,
			sheetsRow("note"),
			sheetsRow("name", "qty"),
			sheetsRow("skip", 0.0),
			sheetsRow("a", 1.0),
		)
		node := triggerNode("anyUpdate", map[string]any{"dataLocationOnSheet": map[string]any{"values": map[string]any{
			"rangeDefinition": "specifyRange", "headerRow": 2.0, "firstDataRow": 4.0,
		}}})
		setup.poll(t, node)
		tab.rows[2][1] = 5.0 // above the first data row: ignored
		tab.rows[3][1] = 2.0
		got := setup.poll(t, node)
		if len(got) != 1 || got[0]["row_number"] != 4.0 || got[0]["name"] != "a" {
			t.Fatalf("rows = %v, want only row 4", got)
		}
	})
}

func TestSheetsTriggerRefusesASheetPastTheRowCap(t *testing.T) {
	setup := newTriggerSetup(t)
	rows := make([][]any, 0, nodes.GoogleSheetsTriggerRowCap+2)
	rows = append(rows, sheetsRow("n"))
	for index := 0; index <= nodes.GoogleSheetsTriggerRowCap; index++ {
		rows = append(rows, sheetsRow(fmt.Sprint(index)))
	}
	tab := setup.fake.addTab(0, "Sheet1", 10, rows...)
	_, err := setup.pollErr(triggerNode("anyUpdate", nil))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprint(nodes.GoogleSheetsTriggerRowCap)) {
		t.Fatalf("error = %v, want a refusal that names the cap", err)
	}
	if setup.cursor != "" {
		t.Errorf("cursor = %d bytes, want none written for a sheet it refuses", len(setup.cursor))
	}
	// Narrowing the range brings it back under the cap.
	tab.rows = tab.rows[:10]
	if _, err := setup.pollErr(triggerNode("anyUpdate", nil)); err != nil {
		t.Fatalf("Poll() after the sheet shrank = %v", err)
	}
}

func TestSheetsTriggerStartsAgainWhenItIsPointedAtAnotherTab(t *testing.T) {
	setup := newTriggerSetup(t)
	setup.people()
	other := setup.fake.addTab(4, "Other", 10, sheetsRow("x"), sheetsRow("1"), sheetsRow("2"))
	_ = other
	setup.poll(t, triggerNode("rowAdded", nil))
	moved := triggerNode("rowAdded", nil)
	moved.Parameters["sheetName"] = sheetsLocator("name", "Other")
	if got := setup.poll(t, moved); len(got) != 0 {
		t.Fatalf("a different tab emitted %v on its first tick, want a fresh baseline", got)
	}
}

func TestSheetsTriggerRefusesALoopbackEndpointThePolicyDidNotGrant(t *testing.T) {
	setup := newTriggerSetup(t)
	setup.people()
	strict := nodes.NewGoogleSheetsPoller(sheetsPolicy(t, "127.0.0.1:1"))
	_, _, err := strict.Poll(context.Background(), poller.Claim{
		Node: triggerNode("rowAdded", nil), Request: setup.request, Now: time.Now(),
	})
	if !errors.Is(err, safehttp.ErrBlocked) {
		t.Fatalf("error = %v, want safehttp.ErrBlocked for a loopback port the policy did not grant", err)
	}
}

func TestSheetsTriggerKeepsItsCursorWhenGoogleFails(t *testing.T) {
	setup := newTriggerSetup(t)
	setup.people()
	node := triggerNode("rowAdded", nil)
	setup.poll(t, node)
	before := setup.cursor
	setup.fake.failWith = 503
	if _, err := setup.pollErr(node); err == nil {
		t.Fatal("Poll() = nil, want the failure")
	}
	if setup.cursor != before {
		t.Errorf("cursor changed to %q after a failed tick", setup.cursor)
	}
}

func TestSheetsTriggerExecutorEmitsThePolledRow(t *testing.T) {
	output, err := nodes.GoogleSheetsTriggerExecutor{}.Execute(context.Background(), workflow.IRNode{}, nil,
		engine.Request{Input: workflow.Item{JSON: map[string]any{"name": "Cy"}}})
	if err != nil || len(output) != 1 || len(output[0]) != 1 || output[0][0].JSON["name"] != "Cy" {
		t.Fatalf("Execute() = %v, %v, want the polled row", output, err)
	}
}

// The poll service looks handlers up by node type, and activation stores a
// cursor row only for the types ExtractPolls recognises: the two lists must be
// the same list.
func TestPollHandlersCoverEveryTriggerExtractPollsRecognises(t *testing.T) {
	policy := safehttp.DefaultPolicy()
	handlers := nodes.PollHandlers(policy)

	recognised := []string{}
	for _, nodeType := range []string{
		nodes.GmailTriggerNodeType, nodes.GoogleDriveTriggerNodeType, nodes.GoogleSheetsTriggerNodeType,
		"kilasflow.manual", "kilasflow.googleSheets",
	} {
		polls := nodes.ExtractPolls(workflow.Document{Nodes: []workflow.Node{{ID: "n", Type: nodeType, Parameters: map[string]any{}}}})
		if len(polls) == 1 && polls[0].NodeType == nodeType {
			recognised = append(recognised, nodeType)
		}
	}
	keys := make([]string, 0, len(handlers))
	for key := range handlers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	sort.Strings(recognised)
	if !reflect.DeepEqual(keys, recognised) {
		t.Fatalf("handlers = %v, ExtractPolls recognises %v", keys, recognised)
	}
	if !contains(keys, nodes.GoogleSheetsTriggerNodeType) {
		t.Errorf("handlers = %v, want the Sheets trigger among them", keys)
	}
}

func contains(list []string, want string) bool {
	for _, entry := range list {
		if entry == want {
			return true
		}
	}
	return false
}

func TestSheetsTriggerPollIntervalComesFromPollTimes(t *testing.T) {
	triggers := nodes.ExtractPolls(workflow.Document{Nodes: []workflow.Node{{
		ID: "s", Type: nodes.GoogleSheetsTriggerNodeType,
		Parameters: map[string]any{"pollTimes": map[string]any{"item": []any{map[string]any{"mode": "everyX", "value": 5.0, "unit": "minutes"}}}},
	}}})
	if len(triggers) != 1 || triggers[0].Interval != 5*time.Minute {
		t.Fatalf("triggers = %#v, want one every five minutes", triggers)
	}
}
