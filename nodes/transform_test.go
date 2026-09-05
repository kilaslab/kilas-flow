package nodes_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// Aggregate and Split Out are inverses, so the round trip is one fixture.
func TestAggregateAndSplitOutAreInverses(t *testing.T) {
	t.Parallel()

	rows := workflow.NodeInput{"main": {
		{JSON: map[string]any{"name": "Ada", "team": "core"}},
		{JSON: map[string]any{"name": "Grace", "team": "core"}},
	}}

	gathered, err := flowExecutor(t, nodes.AggregateExecutorID).Execute(context.Background(),
		flowNode(t, nodes.AggregateNodeType, map[string]any{
			"aggregate": "aggregateAllItemData", "destinationFieldName": "people",
		}), rows, engine.Request{})
	if err != nil {
		t.Fatalf("Aggregate error = %v", err)
	}
	if len(gathered[0]) != 1 {
		t.Fatalf("aggregated into %d items, want one", len(gathered[0]))
	}
	people, _ := gathered[0][0].JSON["people"].([]any)
	if len(people) != 2 {
		t.Fatalf("gathered = %#v, want both rows", gathered[0][0].JSON)
	}

	split, err := flowExecutor(t, nodes.SplitOutExecutorID).Execute(context.Background(),
		flowNode(t, nodes.SplitOutNodeType, map[string]any{"fieldToSplitOut": "people"}),
		workflow.NodeInput{"main": gathered[0]}, engine.Request{})
	if err != nil {
		t.Fatalf("Split Out error = %v", err)
	}
	if len(split[0]) != 2 {
		t.Fatalf("split into %d items, want the two we started with", len(split[0]))
	}
	if split[0][0].JSON["name"] != "Ada" || split[0][1].JSON["name"] != "Grace" {
		t.Fatalf("split = %#v, want the original rows back", split[0])
	}
}

func TestAggregateGathersNamedFields(t *testing.T) {
	t.Parallel()

	output, err := flowExecutor(t, nodes.AggregateExecutorID).Execute(context.Background(),
		flowNode(t, nodes.AggregateNodeType, map[string]any{"fieldsToAggregate": "name,missing"}),
		workflow.NodeInput{"main": {
			{JSON: map[string]any{"name": "Ada"}},
			{JSON: map[string]any{"name": "Grace"}},
		}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	names, _ := output[0][0].JSON["name"].([]any)
	if len(names) != 2 || names[0] != "Ada" {
		t.Fatalf("names = %#v, want both", output[0][0].JSON["name"])
	}
	// A field nothing has is an empty list, not a list of nulls: a list with
	// holes in it is worse than a shorter one.
	missing, present := output[0][0].JSON["missing"].([]any)
	if !present || len(missing) != 0 {
		t.Fatalf("missing = %#v, want an empty list", output[0][0].JSON["missing"])
	}
}

func TestSplitOutCarriesTheFieldsItWasToldTo(t *testing.T) {
	t.Parallel()

	item := workflow.NodeInput{"main": {{JSON: map[string]any{
		"order": "A-1", "customer": "Ada",
		"lines": []any{map[string]any{"sku": "x"}, map[string]any{"sku": "y"}},
	}}}}

	none, err := flowExecutor(t, nodes.SplitOutExecutorID).Execute(context.Background(),
		flowNode(t, nodes.SplitOutNodeType, map[string]any{"fieldToSplitOut": "lines"}), item, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, carried := none[0][0].JSON["order"]; carried {
		t.Fatalf("item = %#v, want only the split element by default", none[0][0].JSON)
	}

	all, err := flowExecutor(t, nodes.SplitOutExecutorID).Execute(context.Background(),
		flowNode(t, nodes.SplitOutNodeType, map[string]any{
			"fieldToSplitOut": "lines", "include": "allOtherFields",
		}), item, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if all[0][0].JSON["order"] != "A-1" || all[0][0].JSON["sku"] != "x" {
		t.Fatalf("item = %#v, want the context and the element", all[0][0].JSON)
	}
	// The split field itself is not carried alongside its own elements.
	if _, carried := all[0][0].JSON["lines"]; carried {
		t.Fatalf("item = %#v, want the source list dropped", all[0][0].JSON)
	}

	selected, err := flowExecutor(t, nodes.SplitOutExecutorID).Execute(context.Background(),
		flowNode(t, nodes.SplitOutNodeType, map[string]any{
			"fieldToSplitOut": "lines", "include": "selectedOtherFields", "fieldsToInclude": "customer",
		}), item, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if selected[0][0].JSON["customer"] != "Ada" {
		t.Fatalf("item = %#v, want the selected field", selected[0][0].JSON)
	}
	if _, carried := selected[0][0].JSON["order"]; carried {
		t.Fatalf("item = %#v, want only the selected field", selected[0][0].JSON)
	}
}

// A field that is not a list is one element, not an error: an API returning a
// single object where it usually returns an array is ordinary.
func TestSplitOutTreatsANonListAsOneElement(t *testing.T) {
	t.Parallel()

	output, err := flowExecutor(t, nodes.SplitOutExecutorID).Execute(context.Background(),
		flowNode(t, nodes.SplitOutNodeType, map[string]any{"fieldToSplitOut": "result"}),
		workflow.NodeInput{"main": {
			{JSON: map[string]any{"result": map[string]any{"id": "only"}}},
			{JSON: map[string]any{}},
		}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output[0]) != 1 || output[0][0].JSON["id"] != "only" {
		t.Fatalf("output = %#v, want the single object as one item and nothing for the absent field", output[0])
	}
}

func TestSortOrdersByFieldsAndDirections(t *testing.T) {
	t.Parallel()

	items := workflow.NodeInput{"main": {
		{JSON: map[string]any{"team": "b", "score": float64(9)}},
		{JSON: map[string]any{"team": "a", "score": float64(10)}},
		{JSON: map[string]any{"team": "a", "score": float64(2)}},
	}}

	ascending, err := flowExecutor(t, nodes.SortExecutorID).Execute(context.Background(),
		flowNode(t, nodes.SortNodeType, map[string]any{"sortFieldsUI": "team,score"}), items, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// Numbers compare as numbers: comparing their text would put 10 before 9.
	if ascending[0][0].JSON["score"] != float64(2) || ascending[0][1].JSON["score"] != float64(10) {
		t.Fatalf("sorted = %#v, want a numeric comparison within each team", ascending[0])
	}

	descending, err := flowExecutor(t, nodes.SortExecutorID).Execute(context.Background(),
		flowNode(t, nodes.SortNodeType, map[string]any{"sortFieldsUI": "score:desc"}), items, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if descending[0][0].JSON["score"] != float64(10) {
		t.Fatalf("sorted = %#v, want the largest first", descending[0])
	}
}

func TestSortIsStableForItemsItCannotTellApart(t *testing.T) {
	t.Parallel()

	// An unstable sort makes a workflow's output vary run to run for no reason
	// a reader can see.
	items := workflow.NodeInput{"main": {
		{JSON: map[string]any{"team": "a", "id": float64(1)}},
		{JSON: map[string]any{"team": "a", "id": float64(2)}},
		{JSON: map[string]any{"team": "a", "id": float64(3)}},
	}}
	output, err := flowExecutor(t, nodes.SortExecutorID).Execute(context.Background(),
		flowNode(t, nodes.SortNodeType, map[string]any{"sortFieldsUI": "team"}), items, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for index, want := range []float64{1, 2, 3} {
		if output[0][index].JSON["id"] != want {
			t.Fatalf("sorted = %#v, want the input order kept", output[0])
		}
	}
}

func TestSummarizeAggregatesEveryWayItAdvertises(t *testing.T) {
	t.Parallel()

	items := workflow.NodeInput{"main": {
		{JSON: map[string]any{"team": "core", "score": float64(10), "name": "Ada"}},
		{JSON: map[string]any{"team": "core", "score": float64(20), "name": "Grace"}},
		{JSON: map[string]any{"team": "ops", "score": float64(5), "name": "Ada"}},
	}}

	output, err := flowExecutor(t, nodes.SummarizeExecutorID).Execute(context.Background(),
		flowNode(t, nodes.SummarizeNodeType, map[string]any{
			"fieldsToSplitBy": "team",
			"fieldsToSummarize": []any{
				map[string]any{"aggregation": "sum", "field": "score"},
				map[string]any{"aggregation": "average", "field": "score"},
				map[string]any{"aggregation": "min", "field": "score"},
				map[string]any{"aggregation": "max", "field": "score"},
				map[string]any{"aggregation": "count", "field": "name"},
				map[string]any{"aggregation": "countUnique", "field": "name"},
				map[string]any{"aggregation": "concatenate", "field": "name"},
				map[string]any{"aggregation": "append", "field": "name"},
			},
		}), items, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output[0]) != 2 {
		t.Fatalf("summarised into %d rows, want one per team", len(output[0]))
	}
	// Groups keep their first-seen order rather than being sorted, so the
	// output is deterministic and reads the way the input did.
	core := output[0][0].JSON
	if core["team"] != "core" {
		t.Fatalf("first row = %#v, want the first team seen", core)
	}
	for key, want := range map[string]any{
		"sum_score": float64(30), "average_score": float64(15),
		"min_score": float64(10), "max_score": float64(20),
		"count_name": float64(2), "countUnique_name": float64(2),
		"concatenate_name": "Ada, Grace",
	} {
		if core[key] != want {
			t.Errorf("%s = %#v, want %#v", key, core[key], want)
		}
	}
	appended, _ := core["append_name"].([]any)
	if len(appended) != 2 {
		t.Errorf("append_name = %#v, want both names", core["append_name"])
	}
}

// Averaging nothing is not zero: zero is an answer, and this is the absence of
// one.
func TestSummarizeAveragesNothingToNothing(t *testing.T) {
	t.Parallel()

	output, err := flowExecutor(t, nodes.SummarizeExecutorID).Execute(context.Background(),
		flowNode(t, nodes.SummarizeNodeType, map[string]any{
			"fieldsToSummarize": []any{map[string]any{"aggregation": "average", "field": "score"}},
		}), workflow.NodeInput{"main": {{JSON: map[string]any{"other": "x"}}}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output[0][0].JSON["average_score"] != nil {
		t.Fatalf("average = %#v, want nothing rather than zero", output[0][0].JSON["average_score"])
	}
}

func TestRemoveDuplicatesComparesWhatItWasToldTo(t *testing.T) {
	t.Parallel()

	items := workflow.NodeInput{"main": {
		{JSON: map[string]any{"id": float64(1), "seen": "first"}},
		{JSON: map[string]any{"id": float64(1), "seen": "second"}},
		{JSON: map[string]any{"id": float64(2), "seen": "third"}},
	}}

	all, err := flowExecutor(t, nodes.RemoveDuplicatesExecutorID).Execute(context.Background(),
		flowNode(t, nodes.RemoveDuplicatesNodeType, map[string]any{}), items, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// Comparing every field, the first two differ.
	if len(all[0]) != 3 {
		t.Fatalf("kept %d items, want all three", len(all[0]))
	}

	selected, err := flowExecutor(t, nodes.RemoveDuplicatesExecutorID).Execute(context.Background(),
		flowNode(t, nodes.RemoveDuplicatesNodeType, map[string]any{
			"compare": "selectedFields", "fieldsToCompare": "id",
		}), items, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(selected[0]) != 2 || selected[0][0].JSON["seen"] != "first" {
		t.Fatalf("kept = %#v, want the first of each id", selected[0])
	}

	except, err := flowExecutor(t, nodes.RemoveDuplicatesExecutorID).Execute(context.Background(),
		flowNode(t, nodes.RemoveDuplicatesNodeType, map[string]any{
			"compare": "allFieldsExcept", "fieldsToExclude": "seen",
		}), items, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(except[0]) != 2 {
		t.Fatalf("kept %d items, want the two distinct ids", len(except[0]))
	}
}

// Removing items seen in *previous* executions needs durable per-workflow state
// this deployment does not have. Refusing is better than quietly doing the
// local thing and calling it the same.
func TestRemoveDuplicatesRefusesTheDurableOperation(t *testing.T) {
	t.Parallel()

	_, err := flowExecutor(t, nodes.RemoveDuplicatesExecutorID).Execute(context.Background(),
		flowNode(t, nodes.RemoveDuplicatesNodeType, map[string]any{
			"operation": "removeItemsSeenInPreviousExecutions",
		}), workflow.NodeInput{"main": {}}, engine.Request{})
	if err == nil || !strings.Contains(err.Error(), "durable") {
		t.Fatalf("Execute() error = %v, want the missing capability named", err)
	}
}
