package n8n_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// legacyNodeFixture is a workflow with a Manual trigger feeding one node of
// the given legacy type, so a test can state exactly which parameters the
// source carried.
func legacyNodeFixture(nodeType string, typeVersion float64, parameters string) string {
	encoded, _ := json.Marshal(parameters)
	_ = encoded
	return `{
	  "name": "Legacy",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Legacy","type":"` + nodeType + `","typeVersion":` + jsonNumber(typeVersion) + `,"position":[220,0],"parameters":` + parameters + `}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Legacy","type":"main","index":0}]]}}
	}`
}

// jsonNumber renders a typeVersion the way n8n writes it, without a decimal
// point on a whole number.
func jsonNumber(value float64) string {
	if value == float64(int(value)) {
		return jsonInt(int(value))
	}
	return jsonFloat(value)
}

func jsonInt(value int) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func jsonFloat(value float64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// intervalAt reads one converted schedule interval as a map, for assertions.
func intervalAt(t *testing.T, document workflow.Document, name string, index int) map[string]any {
	t.Helper()
	node := nodeByName(document, name)
	rule, _ := node.Parameters["rule"].(map[string]any)
	entries, _ := rule["interval"].([]any)
	if index >= len(entries) {
		t.Fatalf("node %q has %d rule intervals, wanted index %d", name, len(entries), index)
	}
	interval, _ := entries[index].(map[string]any)
	return interval
}

// --- The legacy Cron and Interval triggers ----------------------------------

// The Cron node's trigger times become Schedule rule intervals, one mode to
// one interval, and the import names the one thing that genuinely differs:
// n8n's random offset versus this server's calendar.
func TestALegacyCronNodeMapsOntoSchedule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		parameters string
		want       map[string]any
	}{
		{
			name:       "every day at half past nine",
			parameters: `{"triggerTimes":{"item":[{"mode":"everyDay","hour":9,"minute":30}]}}`,
			want:       map[string]any{"field": "days", "daysInterval": float64(1), "triggerAtHour": float64(9), "triggerAtMinute": float64(30)},
		},
		{
			name:       "a day with no hour is n8n's own 14:00 default",
			parameters: `{"triggerTimes":{"item":[{"mode":"everyDay"}]}}`,
			want:       map[string]any{"field": "days", "daysInterval": float64(1), "triggerAtHour": float64(14), "triggerAtMinute": float64(0)},
		},
		{
			name:       "every x minutes",
			parameters: `{"triggerTimes":{"item":[{"mode":"everyX","unit":"minutes","value":5}]}}`,
			want:       map[string]any{"field": "minutes", "minutesInterval": float64(5)},
		},
		{
			name:       "every x without a unit or value is every two hours",
			parameters: `{"triggerTimes":{"item":[{"mode":"everyX"}]}}`,
			want:       map[string]any{"field": "hours", "hoursInterval": float64(2)},
		},
		{
			name:       "every minute",
			parameters: `{"triggerTimes":{"item":[{"mode":"everyMinute"}]}}`,
			want:       map[string]any{"field": "minutes", "minutesInterval": float64(1)},
		},
		{
			name:       "every hour at a quarter past",
			parameters: `{"triggerTimes":{"item":[{"mode":"everyHour","minute":15}]}}`,
			want:       map[string]any{"field": "hours", "hoursInterval": float64(1), "triggerAtMinute": float64(15)},
		},
		{
			name:       "every week on monday, weekday stored as n8n's option string",
			parameters: `{"triggerTimes":{"item":[{"mode":"everyWeek","weekday":"1","hour":8,"minute":0}]}}`,
			want:       map[string]any{"field": "weeks", "weeksInterval": float64(1), "triggerAtDay": []any{float64(1)}, "triggerAtHour": float64(8), "triggerAtMinute": float64(0)},
		},
		{
			name:       "every month on the fifteenth",
			parameters: `{"triggerTimes":{"item":[{"mode":"everyMonth","dayOfMonth":15,"hour":9,"minute":0}]}}`,
			want:       map[string]any{"field": "months", "monthsInterval": float64(1), "triggerAtDayOfMonth": float64(15), "triggerAtHour": float64(9), "triggerAtMinute": float64(0)},
		},
		{
			name:       "a custom expression crosses as itself, seconds field included",
			parameters: `{"triggerTimes":{"item":[{"mode":"custom","cronExpression":"30 9 * * mon-fri"}]}}`,
			want:       map[string]any{"field": "cronExpression", "expression": "30 9 * * mon-fri"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := importFixture(t, legacyNodeFixture("n8n-nodes-base.cron", 1, test.parameters))
			node := nodeByName(result.Document, "Legacy")
			if node.Type != "kilasflow.schedule" {
				t.Fatalf("cron imported as %q, want kilasflow.schedule", node.Type)
			}
			got := intervalAt(t, result.Document, "Legacy", 0)
			for key, want := range test.want {
				if !reflect.DeepEqual(got[key], want) {
					t.Fatalf("interval = %#v, want %q = %#v", got, key, want)
				}
			}
		})
	}
}

// Several trigger times become several intervals, an out-of-range one is
// dropped and named, and the offset difference is said once per kept time.
func TestALegacyCronNodeCarriesEveryTimeAndNamesTheDifferences(t *testing.T) {
	t.Parallel()

	result := importFixture(t, legacyNodeFixture("n8n-nodes-base.cron", 1,
		`{"triggerTimes":{"item":[{"mode":"everyDay","hour":9,"minute":0},{"mode":"everyX","unit":"minutes","value":0}]}}`))
	node := nodeByName(result.Document, "Legacy")
	if node.Type != "kilasflow.schedule" {
		t.Fatalf("cron imported as %q, want kilasflow.schedule", node.Type)
	}
	rule, _ := node.Parameters["rule"].(map[string]any)
	entries, _ := rule["interval"].([]any)
	if len(entries) != 1 {
		t.Fatalf("rule carries %d intervals, want the one valid time: %#v", len(entries), rule)
	}

	var dropped, offset bool
	for _, issue := range result.Unsupported {
		if issue.NodeName != "Legacy" {
			continue
		}
		if strings.Contains(issue.Reason, "out of range and was dropped") {
			dropped = true
		}
		if strings.Contains(issue.Reason, "randomly chosen second") {
			offset = true
		}
	}
	if !dropped {
		t.Errorf("no issue named the dropped every-0-minutes time: %#v", result.Unsupported)
	}
	if !offset {
		t.Errorf("no issue named n8n's random-second offset: %#v", result.Unsupported)
	}
}

// An untouched Cron node has no trigger times at all; the parameter's own
// default is one everyDay item at 14:00, which is what the runtime ran.
func TestAnUntouchedCronNodeImportsItsDefaultTime(t *testing.T) {
	t.Parallel()

	result := importFixture(t, legacyNodeFixture("n8n-nodes-base.cron", 1, `{}`))
	if got := intervalAt(t, result.Document, "Legacy", 0); got["triggerAtHour"] != float64(14) {
		t.Fatalf("default interval = %#v, want every day at 14:00", got)
	}
}

// A cron trigger exports as a Schedule Trigger, n8n's own replacement, with
// the rule carrying across.
func TestAnImportedCronExportsAsTheScheduleTrigger(t *testing.T) {
	t.Parallel()

	result := importFixture(t, legacyNodeFixture("n8n-nodes-base.cron", 1,
		`{"triggerTimes":{"item":[{"mode":"everyDay","hour":9,"minute":30}]}}`))
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, node := range exported.Document.Nodes {
		if node.Name != "Legacy" {
			continue
		}
		if node.Type != "n8n-nodes-base.scheduleTrigger" {
			t.Fatalf("exported as %q, want the Schedule Trigger n8n replaced this node with", node.Type)
		}
		rule, _ := node.Parameters["rule"].(map[string]any)
		entries, _ := rule["interval"].([]any)
		if len(entries) != 1 {
			t.Fatalf("exported rule = %#v, want the carried interval", node.Parameters)
		}
	}
}

// The Interval node is one interval and one unit, which is what a seconds,
// minutes or hours interval already is.
func TestALegacyIntervalNodeMapsOntoSchedule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		parameters string
		want       map[string]any
	}{
		{
			name:       "every five minutes",
			parameters: `{"interval":5,"unit":"minutes"}`,
			want:       map[string]any{"field": "minutes", "minutesInterval": float64(5)},
		},
		{
			name:       "no unit is seconds, n8n's default",
			parameters: `{"interval":30}`,
			want:       map[string]any{"field": "seconds", "secondsInterval": float64(30)},
		},
		{
			name:       "every two hours",
			parameters: `{"interval":2,"unit":"hours"}`,
			want:       map[string]any{"field": "hours", "hoursInterval": float64(2)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := importFixture(t, legacyNodeFixture("n8n-nodes-base.interval", 1, test.parameters))
			if node := nodeByName(result.Document, "Legacy"); node.Type != "kilasflow.schedule" {
				t.Fatalf("interval imported as %q, want kilasflow.schedule", node.Type)
			}
			got := intervalAt(t, result.Document, "Legacy", 0)
			for key, want := range test.want {
				if !reflect.DeepEqual(got[key], want) {
					t.Fatalf("interval = %#v, want %q = %#v", got, key, want)
				}
			}
		})
	}
}

// The Interval node accepted any positive number; a value the scheduler
// cannot express is named and leaves an empty rule rather than running as a
// schedule its author never wrote.
func TestAnIntervalTheSchedulerCannotExpressIsNamed(t *testing.T) {
	t.Parallel()

	result := importFixture(t, legacyNodeFixture("n8n-nodes-base.interval", 1, `{"interval":90,"unit":"minutes"}`))
	blocking := blockingFor(result, "Legacy")
	if len(blocking) != 1 || !strings.Contains(blocking[0].Reason, "every 90 minutes") {
		t.Fatalf("blocking issues = %#v, want the unrepresentable 90-minute interval named", blocking)
	}
	node := nodeByName(result.Document, "Legacy")
	rule, _ := node.Parameters["rule"].(map[string]any)
	if entries, _ := rule["interval"].([]any); len(entries) != 0 {
		t.Fatalf("rule = %#v, want it empty", rule)
	}
}

// --- The legacy Function nodes -----------------------------------------------

// A Function node's body is preserved on the Code path's placeholder and
// refused in the one sentence, with the mode distinguishing the per-item node.
func TestTheLegacyFunctionNodesLandOnTheCodePathPlaceholder(t *testing.T) {
	t.Parallel()

	const source = "for (item of items) {\n  item.json.seen = true;\n}\nreturn items;"
	encoded, _ := json.Marshal(source)
	tests := []struct {
		nodeType string
		mode     string
	}{
		{nodeType: "n8n-nodes-base.function", mode: nodes.CodeModeAllItems},
		{nodeType: "n8n-nodes-base.functionItem", mode: nodes.CodeModeEachItem},
	}
	for _, test := range tests {
		t.Run(test.nodeType, func(t *testing.T) {
			t.Parallel()
			result := importFixture(t, legacyNodeFixture(test.nodeType, 1, `{"functionCode":`+string(encoded)+`}`))
			node := nodeByName(result.Document, "Legacy")
			if node.Type != n8n.ForeignCodeNodeType {
				t.Fatalf("imported as %q, want the Code path placeholder %q", node.Type, n8n.ForeignCodeNodeType)
			}
			if node.Parameters["functionCode"] != source {
				t.Fatalf("functionCode = %#v, want the source kept exactly", node.Parameters["functionCode"])
			}
			if node.Parameters["mode"] != test.mode {
				t.Fatalf("mode = %#v, want %q", node.Parameters["mode"], test.mode)
			}

			blocking := blockingFor(result, "Legacy")
			if len(blocking) != 1 {
				t.Fatalf("blocking issues %#v, want exactly the refusal", blocking)
			}
			// The refusal is the node's own validator's sentence, so the
			// import report and the editor say the same thing.
			definition, ok := registry(t).Resolve(n8n.ForeignCodeNodeType, workflow.V(1))
			if !ok {
				t.Fatal("the foreignCode placeholder is not registered")
			}
			validation := definition.Validate(workflow.Node{Parameters: node.Parameters})
			if validation == nil || blocking[0].Reason != validation.Error() {
				t.Fatalf("import says %q, validation says %v; want the same sentence", blocking[0].Reason, validation)
			}
			if !strings.HasPrefix(validation.Error(), "this node's code can only run inside the legacy n8n Function node's sandbox") {
				t.Fatalf("validation = %q", validation)
			}

			// The round trip returns the node it came from, source intact.
			exported, err := n8n.Export(result.Document, registry(t))
			if err != nil {
				t.Fatalf("Export() error = %v", err)
			}
			for _, exportedNode := range exported.Document.Nodes {
				if exportedNode.Name != "Legacy" {
					continue
				}
				if exportedNode.Type != test.nodeType {
					t.Fatalf("exported as %q, want %q", exportedNode.Type, test.nodeType)
				}
				if exportedNode.Parameters["functionCode"] != source {
					t.Fatalf("exported functionCode = %#v, want the source unchanged", exportedNode.Parameters["functionCode"])
				}
			}
		})
	}
}

// --- The Item Lists node ------------------------------------------------------

// Every Item Lists operation lands on the native node that does its work,
// with the v1 spellings and fixed-collection field lists read as well as the
// v3 ones.
func TestItemListsOperationsMapOntoTheirNativeNodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		parameters string
		wantType   string
		wantKey    string
		wantValue  any
	}{
		{
			name:       "v3 concatenate items maps onto aggregate",
			parameters: `{"resource":"itemList","operation":"concatenateItems","aggregate":"aggregateIndividualFields","fieldsToAggregate":{"fieldToAggregate":[{"fieldToAggregate":"email"}]}}`,
			wantType:   n8n.AggregateNodeType,
			wantKey:    "fieldsToAggregate", wantValue: "email",
		},
		{
			name:       "v1 aggregate items is the same operation under its older spelling",
			parameters: `{"resource":"itemList","operation":"aggregateItems","aggregate":"aggregateAllItemData","destinationFieldName":"data"}`,
			wantType:   n8n.AggregateNodeType,
			wantKey:    "destinationFieldName", wantValue: "data",
		},
		{
			name:       "v3 split out carries its comma-string include list",
			parameters: `{"resource":"itemList","operation":"splitOutItems","fieldToSplitOut":"rows","include":"selectedOtherFields","fieldsToInclude":"email, name"}`,
			wantType:   n8n.SplitOutNodeType,
			wantKey:    "fieldsToInclude", wantValue: "email, name",
		},
		{
			name:       "v1 split out reads the fixed-collection include list",
			parameters: `{"resource":"itemList","operation":"splitOutItems","fieldToSplitOut":"rows","include":"selectedOtherFields","fieldsToInclude":{"fields":[{"fieldName":"email"},{"fieldName":"name"}]}}`,
			wantType:   n8n.SplitOutNodeType,
			wantKey:    "fieldsToInclude", wantValue: "email,name",
		},
		{
			name:       "an operation selector left at its default is split out",
			parameters: `{"resource":"itemList","fieldToSplitOut":"rows"}`,
			wantType:   n8n.SplitOutNodeType,
			wantKey:    "fieldToSplitOut", wantValue: "rows",
		},
		{
			name:       "limit carries its budget and end",
			parameters: `{"resource":"itemList","operation":"limit","maxItems":10,"keep":"lastItems"}`,
			wantType:   n8n.LimitNodeType,
			wantKey:    "maxItems", wantValue: float64(10),
		},
		{
			name:       "remove duplicates reads the v1 fixed-collection compare list",
			parameters: `{"resource":"itemList","operation":"removeDuplicates","compare":"selectedFields","fieldsToCompare":{"fields":[{"fieldName":"email"}]}}`,
			wantType:   n8n.RemoveDuplicatesNodeType,
			wantKey:    "fieldsToCompare", wantValue: "email",
		},
		{
			name:       "sort carries its field and order",
			parameters: `{"resource":"itemList","operation":"sort","type":"simple","sortFieldsUi":{"sortField":[{"fieldName":"count","order":"descending"}]}}`,
			wantType:   n8n.SortNodeType,
			wantKey:    "sortFieldsUI", wantValue: "count:desc",
		},
		{
			name:       "summarize carries its columns",
			parameters: `{"resource":"itemList","operation":"summarize","fieldsToSummarize":{"values":[{"aggregation":"count","field":"id"}]},"fieldsToSplitBy":"country"}`,
			wantType:   n8n.SummarizeNodeType,
			wantKey:    "fieldsToSplitBy", wantValue: "country",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := importFixture(t, legacyNodeFixture("n8n-nodes-base.itemLists", 3, test.parameters))
			node := nodeByName(result.Document, "Legacy")
			if node.Type != test.wantType {
				t.Fatalf("imported as %q, want %q", node.Type, test.wantType)
			}
			if got := node.Parameters[test.wantKey]; got != test.wantValue {
				t.Fatalf("%s = %#v, want %#v", test.wantKey, got, test.wantValue)
			}
			if blocking := blockingFor(result, "Legacy"); len(blocking) != 0 {
				t.Fatalf("blocking issues %#v, want none", blocking)
			}
		})
	}
}

// An operation with no mapping is kept as the placeholder and told which
// native node does the closest work, rather than guessed onto one.
func TestAnUnmappedItemListsOperationNamesTheClosestNativeNode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		operation  string
		wantNeedle string
	}{
		{operation: "append", wantNeedle: "Merge (kilasflow.merge)"},
		{operation: "concatItems", wantNeedle: "Aggregate (kilasflow.aggregate)"},
		{operation: "splitOutAndClone", wantNeedle: "The operations that map are"},
	}
	for _, test := range tests {
		t.Run(test.operation, func(t *testing.T) {
			t.Parallel()
			parameters := `{"resource":"itemList","operation":"` + test.operation + `"}`
			result := importFixture(t, legacyNodeFixture("n8n-nodes-base.itemLists", 1, parameters))
			node := nodeByName(result.Document, "Legacy")
			if node.Type != n8n.UnsupportedNodeType {
				t.Fatalf("imported as %q, want the unsupported placeholder", node.Type)
			}
			blocking := blockingFor(result, "Legacy")
			if len(blocking) != 1 || !strings.Contains(blocking[0].Reason, test.wantNeedle) {
				t.Fatalf("blocking issues = %#v, want the reason naming %q", blocking, test.wantNeedle)
			}
		})
	}
}

// --- The placeholder's replacement hint ---------------------------------------

// A legacy node with no mapping of its own keeps the honest placeholder, and
// the reason now names the closest native node where one exists.
func TestALegacyPlaceholderNamesTheClosestNativeNode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		nodeType   string
		wantNeedle string
	}{
		{nodeType: "n8n-nodes-base.readBinaryFile", wantNeedle: "Extract From File (kilasflow.extractFromFile)"},
		{nodeType: "n8n-nodes-base.spreadsheetFile", wantNeedle: "Extract From File (kilasflow.extractFromFile)"},
	}
	for _, test := range tests {
		t.Run(test.nodeType, func(t *testing.T) {
			t.Parallel()
			result := importFixture(t, legacyNodeFixture(test.nodeType, 1, `{}`))
			node := nodeByName(result.Document, "Legacy")
			if node.Type != n8n.UnsupportedNodeType {
				t.Fatalf("imported as %q, want the unsupported placeholder", node.Type)
			}
			blocking := blockingFor(result, "Legacy")
			if len(blocking) != 1 || !strings.Contains(blocking[0].Reason, test.wantNeedle) {
				t.Fatalf("blocking issues = %#v, want the reason naming the closest node", blocking)
			}
		})
	}
}
