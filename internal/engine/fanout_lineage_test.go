package engine_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// BUG-14gp8r: after a fan-out, a node that reorders or filters keeps each item
// paired with the fan-out item it came from. Every item of a fan-out used to
// carry the one origin the fan-out's input had, so `$('X').item` could only
// fall back to the item at the same position, which a reorder makes wrong.

// fanOutOrders is the ticket's three orders, in the order they arrive.
func fanOutOrders() map[string]any {
	return map[string]any{"orders": []any{
		map[string]any{"id": "o1", "amount": float64(40)},
		map[string]any{"id": "o2", "amount": float64(250)},
		map[string]any{"id": "o4", "amount": float64(120)},
	}}
}

// traceNode is a Set that records, per item, its own field and what each
// `$('X').item` read resolves to.
func traceNode(reads map[string]string) workflow.Node {
	assignments := map[string]any{}
	for field, expression := range reads {
		assignments[field] = map[string]any{"mode": "expression", "value": "{{ " + expression + " }}"}
	}
	return workflow.Node{ID: "trace", Name: "Trace", Type: "kilasflow.set", TypeVersion: workflow.V(1),
		Parameters: map[string]any{"assignments": assignments}}
}

// runTrace runs a document whose last node is "trace" and answers the listed
// fields of each of its items, joined by "/".
func runTrace(t *testing.T, ir workflow.IR, executors *engine.Registry, request engine.Request, fields ...string) []string {
	t.Helper()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	result, err := engine.NewRunner(executors).Run(context.Background(), ir, request)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	output := result.Output["trace"]
	if len(output) == 0 {
		t.Fatalf("Trace produced no output; runs: %#v", result.NodeRuns)
	}
	var rows []string
	for _, item := range output[0] {
		values := make([]string, len(fields))
		for index, field := range fields {
			values[index] = fmt.Sprint(item.JSON[field])
		}
		rows = append(rows, strings.Join(values, "/"))
	}
	return rows
}

// TestDollarItemPairsByLineageAfterASplitOutAndASort is the ticket's
// reproduction: Split Out → Sort amount:desc → Set reading
// `$('Split Out').item`. Each item reads its own order, not the order that
// sat at its position before the sort.
func TestDollarItemPairsByLineageAfterASplitOutAndASort(t *testing.T) {
	catalog := testCatalog(t)
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_fanout_sort", Name: "Split Out then Sort",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "split", Name: "Split Out", Type: nodes.SplitOutNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"fieldToSplitOut": "orders"}},
			{ID: "sort", Name: "Sort", Type: nodes.SortNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"type": "simple", "sortFieldsUI": "amount:desc"}},
			traceNode(map[string]string{"own": "$json.id", "origin": "$('Split Out').item.json.id"}),
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "manual", "main", "split"),
			mainEdge("c2", "split", "main", "sort"),
			mainEdge("c3", "sort", "main", "trace"),
		},
		Settings: map[string]any{},
	})

	rows := runTrace(t, ir, engine.NewRegistry(), engine.Request{Input: workflow.Item{JSON: fanOutOrders()}}, "own", "origin")
	if got, want := strings.Join(rows, ", "), "o2/o2, o4/o4, o1/o1"; got != want {
		t.Errorf("own/origin = %s, want %s", got, want)
	}
}

// TestDollarItemPairsByLineageAfterASplitOutAFilterAndASort drops an item as
// well as reordering them, so neither the position nor the count lines up.
func TestDollarItemPairsByLineageAfterASplitOutAFilterAndASort(t *testing.T) {
	catalog := testCatalog(t)
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_fanout_filter_sort", Name: "Split Out, Filter, Sort",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "split", Name: "Split Out", Type: nodes.SplitOutNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"fieldToSplitOut": "orders"}},
			{ID: "filter", Name: "Filter", Type: nodes.FilterNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"conditions": map[string]any{
					"combinator": "and",
					"conditions": []any{map[string]any{
						"leftValue":  map[string]any{"mode": "expression", "value": "{{ $json.amount }}"},
						"operator":   map[string]any{"type": "number", "operation": "gt"},
						"rightValue": float64(50),
					}},
				}}},
			{ID: "sort", Name: "Sort", Type: nodes.SortNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"type": "simple", "sortFieldsUI": "amount"}},
			traceNode(map[string]string{"own": "$json.id", "origin": "$('Split Out').item.json.id", "filtered": "$('Filter').item.json.id"}),
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "manual", "main", "split"),
			mainEdge("c2", "split", "main", "filter"),
			mainEdge("c3", "filter", "main", "sort"),
			mainEdge("c4", "sort", "main", "trace"),
		},
		Settings: map[string]any{},
	})

	rows := runTrace(t, ir, engine.NewRegistry(), engine.Request{Input: workflow.Item{JSON: fanOutOrders()}}, "own", "origin", "filtered")
	if got, want := strings.Join(rows, ", "), "o4/o4/o4, o2/o2/o2"; got != want {
		t.Errorf("own/origin/filtered = %s, want %s", got, want)
	}
}

// TestDollarItemPairsAtEveryLevelOfTwoFanOuts splits customers into orders and
// orders into lines, then sorts the lines across every order. A read of each
// level — above both fan-outs, between them, and at the last — names the
// customer, order and line that line actually came from.
func TestDollarItemPairsAtEveryLevelOfTwoFanOuts(t *testing.T) {
	catalog := testCatalog(t, startType("test.customers", "Customers"))
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_two_fanouts", Name: "Customers, orders, lines",
		Nodes: []workflow.Node{
			{ID: "customers", Name: "Customers", Type: "test.customers", TypeVersion: workflow.V(1)},
			{ID: "orders", Name: "Orders", Type: nodes.SplitOutNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"fieldToSplitOut": "orders"}},
			{ID: "lines", Name: "Lines", Type: nodes.SplitOutNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"fieldToSplitOut": "lines"}},
			{ID: "sort", Name: "Sort", Type: nodes.SortNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"type": "simple", "sortFieldsUI": "qty:desc"}},
			traceNode(map[string]string{
				"sku":      "$json.sku",
				"customer": "$('Customers').item.json.name",
				"order":    "$('Orders').item.json.id",
				"line":     "$('Lines').item.json.sku",
			}),
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "customers", "main", "orders"),
			mainEdge("c2", "orders", "main", "lines"),
			mainEdge("c3", "lines", "main", "sort"),
			mainEdge("c4", "sort", "main", "trace"),
		},
		Settings: map[string]any{},
	})

	line := func(sku string, qty float64) map[string]any { return map[string]any{"sku": sku, "qty": qty} }
	executors := engine.NewRegistry()
	if err := executors.Register("test.customers", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{
				{JSON: map[string]any{"name": "Ada", "orders": []any{
					map[string]any{"id": "o1", "lines": []any{line("a1", 1), line("a2", 5)}},
					map[string]any{"id": "o2", "lines": []any{line("b1", 3)}},
				}}},
				{JSON: map[string]any{"name": "Bo", "orders": []any{
					map[string]any{"id": "o3", "lines": []any{line("c1", 6), line("c2", 2)}},
					map[string]any{"id": "o4", "lines": []any{line("d1", 4)}},
				}}},
			}}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	rows := runTrace(t, ir, executors, engine.Request{}, "sku", "line", "order", "customer")
	want := "c1/c1/o3/Bo, a2/a2/o1/Ada, d1/d1/o4/Bo, b1/b1/o2/Ada, c2/c2/o3/Bo, a1/a1/o1/Ada"
	if got := strings.Join(rows, ", "); got != want {
		t.Errorf("sku/line/order/customer =\n  %s\nwant\n  %s", got, want)
	}
}

// TestDollarItemTellsASplitItemFromOneThatKeptItsSplitStamp covers a Split Out
// whose input carried no lineage of its own: it stamps each split item with
// its own name and the position of the item it split. The first item splits
// in two and the second into one, so the one item of the second keeps that
// stamp (Split Out, position 1) while the two of the first become items of
// their own, one of which sits at position 1. Neither may be mistaken for the
// other after a sort.
func TestDollarItemTellsASplitItemFromOneThatKeptItsSplitStamp(t *testing.T) {
	catalog := testCatalog(t, stepType("test.fanOut", "Fan out"))
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_split_stamp", Name: "Split stamps after a count change",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "fan", Name: "Fan", Type: "test.fanOut", TypeVersion: workflow.V(1)},
			{ID: "split", Name: "Split Out", Type: nodes.SplitOutNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"fieldToSplitOut": "list"}},
			{ID: "sort", Name: "Sort", Type: nodes.SortNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"type": "simple", "sortFieldsUI": "v:desc"}},
			traceNode(map[string]string{"own": "$json.v", "origin": "$('Split Out').item.json.v"}),
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "manual", "main", "fan"),
			mainEdge("c2", "fan", "main", "split"),
			mainEdge("c3", "split", "main", "sort"),
			mainEdge("c4", "sort", "main", "trace"),
		},
		Settings: map[string]any{},
	})

	executors := engine.NewRegistry()
	if err := executors.Register("test.fanOut", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{
				{JSON: map[string]any{"list": []any{map[string]any{"v": "a"}, map[string]any{"v": "b"}}}},
				{JSON: map[string]any{"list": []any{map[string]any{"v": "c"}}}},
			}}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	rows := runTrace(t, ir, executors, engine.Request{Input: workflow.Item{JSON: map[string]any{}}}, "own", "origin")
	if got, want := strings.Join(rows, ", "), "c/c, b/b, a/a"; got != want {
		t.Errorf("own/origin = %s, want each item paired with itself (%s)", got, want)
	}
}
