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

// A whole-batch node that runs its items one at a time and reports which
// failed is resolved per item when it continues on failure, as n8n resolves
// its item loop, and stops at the first failure when it does not.
func TestAWholeBatchNodeReportingItemOutcomesContinuesPastTheFailedItem(t *testing.T) {
	for _, onError := range []string{"continueErrorOutput", "continueRegularOutput", "stopWorkflow"} {
		definition := stepType("test.step", "Step")
		definition.WholeBatch = true
		catalog := testCatalog(t, startType("test.start", "Start"), definition)
		ir := compileDoc(t, catalog, workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_outcomes", Name: "Outcomes",
			Nodes: []workflow.Node{
				{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
				{ID: "each", Name: "Each", Type: "test.step", TypeVersion: workflow.V(1), Settings: map[string]any{"onError": onError}},
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
			if err == nil || !strings.Contains(err.Error(), "item 1 broke") || fmt.Sprint(tolerated) != "[false]" {
				t.Errorf("stop: Run() error = %v, tolerated %v; want the first item's failure, untolerated", err, tolerated)
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
		if got := strings.Join(ports, " | "); got != want || code != "node.partial" || fmt.Sprint(tolerated) != "[true]" {
			t.Errorf("%s: ports %q (code %q, tolerated %v), want %q, node.partial, one tolerant call", onError, got, code, tolerated, want)
		}
	}
}
