package nodes_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// BUG-14gp8r, for the JavaScript Code node: after a fan-out, the node's own
// lineage (an explicit pairedItem, or an input item returned as it was) and
// its `$('X').item` in code both pair by lineage, not by position.

// lineageOrders is four orders on one item, in the order they arrive.
func lineageOrders() workflow.Item {
	order := func(id string, amount float64, status string) map[string]any {
		return map[string]any{"id": id, "amount": amount, "status": status}
	}
	return workflow.Item{JSON: map[string]any{"orders": []any{
		order("o1", 40, "paid"), order("o2", 250, "paid"), order("o3", 90, "cancelled"), order("o4", 120, "paid"),
	}}}
}

// runLineageChain runs Manual → Split Out → the given nodes in a chain and
// answers the listed fields of the last node's items, joined by "/".
func runLineageChain(t *testing.T, chain []workflow.Node, fields ...string) []string {
	t.Helper()
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_js_lineage", Name: "Code node lineage after a fan-out",
		Nodes: append([]workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "split", Name: "Split Out", Type: nodes.SplitOutNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"fieldToSplitOut": "orders"}},
		}, chain...),
		Settings: map[string]any{},
	}
	for index := 1; index < len(document.Nodes); index++ {
		document.Connections = append(document.Connections, workflow.Connection{
			ID: fmt.Sprintf("c%d", index), Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: document.Nodes[index-1].ID, Port: "main"},
			Target: workflow.Endpoint{NodeID: document.Nodes[index].ID, Port: "main"},
		})
	}
	ir, err := workflow.Compile(document, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{Input: lineageOrders()})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	last := chain[len(chain)-1].ID
	if len(result.Output[last]) == 0 {
		t.Fatalf("%s produced no output", last)
	}
	var rows []string
	for _, item := range result.Output[last][0] {
		values := make([]string, len(fields))
		for index, field := range fields {
			values[index] = fmt.Sprint(item.JSON[field])
		}
		rows = append(rows, strings.Join(values, "/"))
	}
	return rows
}

func jsChainNode(id, name, mode, source string) workflow.Node {
	return workflow.Node{ID: id, Name: name, Type: nodes.JSCodeNodeType, TypeVersion: workflow.V(2),
		Parameters: map[string]any{"mode": mode, "jsCode": source}}
}

// The e2e case: a Code node that filters, sorts and rebuilds Split Out's
// items, naming its source with pairedItem for one and returning the input
// items themselves for the rest. A Set after it reads each one's own order.
func TestTheCodeNodesLineageSurvivesAFanOutUpstream(t *testing.T) {
	rank := jsChainNode("rank", "Rank", nodes.CodeModeAllItems, strings.Join([]string{
		"const kept = items.filter((item) => item.json.status !== 'cancelled')",
		"kept.sort((a, b) => b.json.amount - a.json.amount)",
		"return kept.map((item, rank) => rank === 0",
		"  ? { json: { id: item.json.id, rank, top: true }, pairedItem: items.indexOf(item) }",
		"  : Object.assign(item, { json: { ...item.json, rank } }))",
	}, "\n"))
	trace := workflow.Node{ID: "trace", Name: "Trace", Type: "kilasflow.set", TypeVersion: workflow.V(1),
		Parameters: map[string]any{"assignments": map[string]any{
			"origin": map[string]any{"mode": "expression", "value": "{{ $('Split Out').item.json.id }}"},
			"status": map[string]any{"mode": "expression", "value": "{{ $('Split Out').item.json.status }}"},
		}}}

	rows := runLineageChain(t, []workflow.Node{rank, trace}, "id", "rank", "origin", "status")
	if got, want := strings.Join(rows, ", "), "o2/0/o2/paid, o4/1/o4/paid, o1/2/o1/paid"; got != want {
		t.Errorf("id/rank/origin/status = %s, want %s", got, want)
	}
}

// `$('X').item` in code pairs by exactly the rules an expression does, so it
// too reads each item's own order after a native Sort.
func TestDollarItemInCodePairsByLineageAfterAFanOutAndASort(t *testing.T) {
	sorted := workflow.Node{ID: "sort", Name: "Sort", Type: nodes.SortNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"type": "simple", "sortFieldsUI": "amount:desc"}}
	read := jsChainNode("read", "Read", nodes.CodeModeEachItem,
		"return { json: { id: $json.id, origin: $('Split Out').item.json.id } }")

	rows := runLineageChain(t, []workflow.Node{sorted, read}, "id", "origin")
	if got, want := strings.Join(rows, ", "), "o2/o2, o4/o4, o3/o3, o1/o1"; got != want {
		t.Errorf("id/origin = %s, want %s", got, want)
	}
}

// A Code node can be the fan-out itself: several items for one input, each
// naming that input with pairedItem. After a sort, a read of the Code node
// finds each item's own, and a read of Split Out, before it, finds the order
// that item was made from.
func TestACodeNodeThatFansOutPairsEachItemItMade(t *testing.T) {
	fan := jsChainNode("fan", "Fan", nodes.CodeModeAllItems, strings.Join([]string{
		"return items.flatMap((item, index) => [1, 2].map((part) => ({",
		"  json: { id: item.json.id, part, weight: item.json.amount * part }, pairedItem: index })))",
	}, "\n"))
	sorted := workflow.Node{ID: "sort", Name: "Sort", Type: nodes.SortNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"type": "simple", "sortFieldsUI": "weight"}}
	read := jsChainNode("read", "Read", nodes.CodeModeEachItem, strings.Join([]string{
		"const made = $('Fan').item.json",
		"return { json: { own: `${$json.id}.${$json.part}`, fan: `${made.id}.${made.part}`, order: $('Split Out').item.json.id } }",
	}, "\n"))

	rows := runLineageChain(t, []workflow.Node{fan, sorted, read}, "own", "fan", "order")
	want := "o1.1/o1.1/o1, o1.2/o1.2/o1, o3.1/o3.1/o3, o4.1/o4.1/o4, o3.2/o3.2/o3, o4.2/o4.2/o4, o2.1/o2.1/o2, o2.2/o2.2/o2"
	if got := strings.Join(rows, ", "); got != want {
		t.Errorf("own/fan/order =\n  %s\nwant\n  %s", got, want)
	}
}

// The runner records how many items each of a node's outputs produced, so a
// Code node after an IF reads one branch with $items or .all(branch): three
// of the four orders were paid (the true branch), one was not. With no
// branch, $('IF').all(), .first() and .last() read the branch the Code node
// is connected to, as n8n's do, even through a node in between: Code hangs
// off true, and Behind sits behind a No Op on false.
func TestACodeNodeReadsOneBranchOfAnIF(t *testing.T) {
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	link := func(id, source, port, target string) workflow.Connection {
		return workflow.Connection{ID: id, Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: source, Port: port}, Target: workflow.Endpoint{NodeID: target, Port: "main"}}
	}
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_js_branches", Name: "Code node reads one branch", Settings: map[string]any{},
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "split", Name: "Split Out", Type: nodes.SplitOutNodeType, TypeVersion: workflow.V(1), Parameters: map[string]any{"fieldToSplitOut": "orders"}},
			{ID: "if", Name: "IF", Type: "kilasflow.if", TypeVersion: workflow.V(1), Parameters: map[string]any{"conditions": []any{
				map[string]any{"field": "status", "operator": "equals", "value": "paid"},
			}}},
			{ID: "code", Name: "Code", Type: nodes.JSCodeNodeType, TypeVersion: workflow.V(1), Parameters: map[string]any{"jsCode": strings.Join([]string{
				"const ids = (list) => list.map((order) => order.json.id).join()",
				"return [{ json: { paid: ids($items('IF')), unpaid: ids($items('IF', 1, -1)), branch: ids($('IF').all(1)), connected: ids($('IF').all()) } }]",
			}, "\n")}},
			{ID: "noop", Name: "No Op", Type: "kilasflow.noOp", TypeVersion: workflow.V(1)},
			{ID: "behind", Name: "Behind", Type: nodes.JSCodeNodeType, TypeVersion: workflow.V(1), Parameters: map[string]any{"jsCode": strings.Join([]string{
				"const ids = (list) => list.map((order) => order.json.id).join()",
				"return [{ json: { connected: ids($('IF').all()), first: $('IF').first().json.id, last: $('IF').last().json.id,",
				"  firstTrue: $('IF').first(0).json.id, lastTrue: $('IF').last(0, -1).json.id, allTrue: ids($('IF').all(0)) } }]",
			}, "\n")}},
		},
		Connections: []workflow.Connection{link("c1", "manual", "main", "split"), link("c2", "split", "main", "if"), link("c3", "if", "true", "code"),
			link("c4", "if", "false", "noop"), link("c5", "noop", "main", "behind")},
	}
	ir, err := workflow.Compile(document, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{Input: lineageOrders()})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	got := result.Output["code"][0][0].JSON
	if got["paid"] != "o1,o2,o4" || got["unpaid"] != "o3" || got["branch"] != "o3" || got["connected"] != "o1,o2,o4" {
		t.Errorf("code read %#v, want each branch on its own and the true branch by default", got)
	}
	behind := result.Output["behind"][0][0].JSON
	if behind["connected"] != "o3" || behind["first"] != "o3" || behind["last"] != "o3" ||
		behind["firstTrue"] != "o1" || behind["lastTrue"] != "o4" || behind["allTrue"] != "o1,o2,o4" {
		t.Errorf("behind the No Op read %#v, want the false branch by default and the true one when named", behind)
	}
}
