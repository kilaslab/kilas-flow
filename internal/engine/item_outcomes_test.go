package engine_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

func nodeRuns(result engine.Result, id string) []engine.NodeRun {
	var runs []engine.NodeRun
	for _, run := range result.NodeRuns {
		if run.NodeID == id {
			runs = append(runs, run)
		}
	}
	return runs
}

// A whole-batch node that runs its items one at a time and reports which
// failed is resolved per item when it continues on failure, as n8n resolves
// its item loop, and stops at the first failure when it does not.
func TestAWholeBatchNodeReportingItemOutcomesContinuesPastTheFailedItem(t *testing.T) {
	for _, onError := range []string{"continueErrorOutput", "continueRegularOutput", "stopWorkflow"} {
		definition := stepType("test.step", "Step")
		definition.WholeBatch = true
		catalog := testCatalog(t, startType("test.start", "Start"), definition)
		// Retry on Fail too: a tolerated item failure is the node's answer,
		// and never retried.
		ir := compileDoc(t, catalog, workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_outcomes", Name: "Outcomes",
			Nodes: []workflow.Node{
				{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
				{ID: "each", Name: "Each", Type: "test.step", TypeVersion: workflow.V(1), Settings: map[string]any{
					"onError": onError, "retryOnFail": true, "maxTries": float64(3), "waitBetweenTries": float64(0)}},
			},
			Connections: []workflow.Connection{mainEdge("c1", "start", "main", "each")},
			Settings:    map[string]any{},
		})
		var tolerated []bool
		executors := threeItemStart(t, "test.step", func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
			tolerated = append(tolerated, request.TolerateItemFailures)
			outcomes := make(engine.ItemOutcomes, len(input["main"]))
			for index := range outcomes {
				if index == 1 {
					outcomes[index].Err = errors.New("item 1 broke")
					continue
				}
				outcomes[index].Items = []workflow.Item{{JSON: map[string]any{"done": float64(index)}}}
			}
			return nil, outcomes
		})
		result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
		if onError == "stopWorkflow" {
			// A node that stops on failure retries as any node does.
			if err == nil || !strings.Contains(err.Error(), "item 1 broke") || fmt.Sprint(tolerated) != "[false false false]" {
				t.Errorf("stop: Run() error = %v, tolerated %v; want the first item's failure after three untolerated tries", err, tolerated)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: Run() error = %v", onError, err)
		}
		var ports []string
		var code string
		for _, run := range result.NodeRuns {
			if run.NodeID != "each" {
				continue
			}
			code = run.ErrorCode
			for _, port := range run.Output {
				var items []string
				for _, item := range port {
					if failure, ok := item.JSON[engine.ErrorItemKey].(map[string]any); ok {
						items = append(items, "error:"+fmt.Sprint(failure["message"]))
					} else {
						items = append(items, fmt.Sprint(item.JSON["done"]))
					}
				}
				ports = append(ports, strings.Join(items, ","))
			}
		}
		want := map[string]string{
			"continueErrorOutput":   "0,2 | error:item 1 broke",
			"continueRegularOutput": "0,error:item 1 broke,2",
		}[onError]
		if rows := len(nodeRuns(result, "each")); rows != 1 {
			t.Errorf("%s: %d rows for the node, want one", onError, rows)
		}
		if got := strings.Join(ports, " | "); got != want || code != "node.partial" || fmt.Sprint(tolerated) != "[true]" {
			t.Errorf("%s: ports %q (code %q, tolerated %v), want %q, node.partial, one tolerant call", onError, got, code, tolerated, want)
		}
	}
}

// TestDollarItemOnAWholeBatchNodesOutcomesPairsWithTheItemThatFailed covers
// the error items a whole-batch node's ItemOutcomes become, and the items
// around them.
//
// Fan changed the item count, so each of its items carries the lost stamp that
// names it. Each gives the first item two results, fails the second, gives the
// third one result and the fourth none: under continueRegularOutput that is
// four items on main for four input items, so pairing by position reads x1
// for x0's second result and x2 for the error item, a confident wrong answer.
// Stamped as runPerItem stamps, the error item pairs with x1, the item that
// failed; the third item's one result pairs with x2; and the first item's two
// results, which no stamp tells apart, are refused. None reads another item.
func TestDollarItemOnAWholeBatchNodesOutcomesPairsWithTheItemThatFailed(t *testing.T) {
	for _, onError := range []string{"continueErrorOutput", "continueRegularOutput"} {
		definition := stepType("test.each", "Each")
		definition.WholeBatch = true
		catalog := testCatalog(t, stepType("test.fanOut", "Fan out"), definition, stepType("test.probe", "Probe"))
		document := workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            "wf_outcomes_lineage", Name: "Read Fan behind a whole-batch node",
			Nodes: []workflow.Node{
				{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
				{ID: "fan", Name: "Fan", Type: "test.fanOut", TypeVersion: workflow.V(1)},
				{ID: "each", Name: "Each", Type: "test.each", TypeVersion: workflow.V(1), Settings: map[string]any{"onError": onError}},
				{ID: "main-probe", Name: "Main probe", Type: "test.probe", TypeVersion: workflow.V(1)},
			},
			Connections: []workflow.Connection{
				mainEdge("c1", "manual", "main", "fan"),
				mainEdge("c2", "fan", "main", "each"),
				mainEdge("c3", "each", "main", "main-probe"),
			},
			Settings: map[string]any{},
		}
		if onError == "continueErrorOutput" {
			document.Nodes = append(document.Nodes, workflow.Node{ID: "error-probe", Name: "Error probe", Type: "test.probe", TypeVersion: workflow.V(1)})
			document.Connections = append(document.Connections, mainEdge("c4", "each", "error", "error-probe"))
		}
		ir := compileDoc(t, catalog, document)

		// An error item names the item that failed in its message; a result
		// names the item it was made from in its fields.
		own := func(item workflow.Item) string {
			if failure, ok := item.JSON[engine.ErrorItemKey].(map[string]any); ok {
				return fmt.Sprint(failure["message"])
			}
			return fmt.Sprint(item.JSON["from"])
		}
		var onMain, onErrorPort pairingTally
		executors := withExecutors(t, map[string]engine.ExecutorFunc{
			"test.fanOut": func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
				items := make([]workflow.Item, 0, 4)
				for index := range 4 {
					items = append(items, workflow.Item{JSON: map[string]any{"u": fmt.Sprintf("x%d", index)}})
				}
				return workflow.NodeOutput{items}, nil
			},
			"test.each": func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
				produced := []int{2, -1, 1, 0}
				outcomes := make(engine.ItemOutcomes, len(input["main"]))
				for index, item := range input["main"] {
					from := fmt.Sprint(item.JSON["u"])
					if produced[index] < 0 {
						outcomes[index].Err = errors.New(from)
						continue
					}
					outcomes[index].Items = []workflow.Item{}
					for range produced[index] {
						outcomes[index].Items = append(outcomes[index].Items, workflow.Item{JSON: map[string]any{"from": from}})
					}
				}
				return nil, outcomes
			},
			"test.probe": func(ctx context.Context, node workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
				tally := &onMain
				if node.ID == "error-probe" {
					tally = &onErrorPort
				}
				return tallyProbe("{{ $('Fan').item.json.u }}", own, tally)(ctx, node, input, request)
			},
		})

		if _, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{
			Input: workflow.Item{JSON: map[string]any{}},
		}); err != nil {
			t.Fatalf("%s: Run() error = %v", onError, err)
		}
		if len(onMain.wrong) > 0 || len(onErrorPort.wrong) > 0 {
			t.Errorf("%s: $('Fan').item read another item: main %v, error output %v", onError, onMain.wrong, onErrorPort.wrong)
		}
		// The error item sits on the error output or in its place on main.
		wantMain, wantErrorPort := "[x2]", "[x1]"
		if onError == "continueRegularOutput" {
			wantMain, wantErrorPort = "[x1 x2]", "[]"
		}
		if got := fmt.Sprint(onErrorPort.paired); got != wantErrorPort || onErrorPort.refused != 0 {
			t.Errorf("%s: the error output paired %s and refused %d, want %s and none refused", onError, got, onErrorPort.refused, wantErrorPort)
		}
		if got := fmt.Sprint(onMain.paired); got != wantMain || onMain.refused != 2 {
			t.Errorf("%s: main paired %s and refused %d, want %s and x0's two results refused", onError, got, onMain.refused, wantMain)
		}
	}
}
