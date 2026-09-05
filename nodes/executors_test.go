package nodes_test

import (
	"context"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/ai"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// TestMergePreservesEachSidesProvenance is the case the runner cannot infer.
//
// Merge concatenates two unrelated streams, so an output item's position says
// nothing about where it came from. Flattening or renumbering would make a
// later reach-back confidently wrong rather than honestly unable.
func TestMergePreservesEachSidesProvenance(t *testing.T) {
	registry := engine.NewRegistry()
	if err := nodes.RegisterExecutors(registry, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, found := registry.Lookup("core.merge")
	if !found {
		t.Fatal("the merge executor is not registered")
	}

	input := workflow.NodeInput{
		"input1": {
			{JSON: map[string]any{"side": "left", "n": 1},
				Paired: &workflow.PairedItem{SourceNodeID: "left-source", ItemIndex: 0}},
			{JSON: map[string]any{"side": "left", "n": 2},
				Paired: &workflow.PairedItem{SourceNodeID: "left-source", ItemIndex: 1}},
		},
		"input2": {
			{JSON: map[string]any{"side": "right", "n": 1},
				Paired: &workflow.PairedItem{SourceNodeID: "right-source", ItemIndex: 0}},
		},
	}

	output, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "merge", Name: "Merge", Parameters: map[string]any{"mode": "append"},
	}, input, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output) != 1 || len(output[0]) != 3 {
		t.Fatalf("merge produced %#v, want three items on one port", output)
	}

	for index, want := range []struct {
		source string
		item   int
	}{
		{"left-source", 0},
		{"left-source", 1},
		{"right-source", 0},
	} {
		paired := output[0][index].Paired
		if paired == nil {
			t.Errorf("item %d lost its provenance in the merge", index)
			continue
		}
		if paired.SourceNodeID != want.source || paired.ItemIndex != want.item {
			t.Errorf("item %d descends from %s[%d], want %s[%d]",
				index, paired.SourceNodeID, paired.ItemIndex, want.source, want.item)
		}
	}
}

// TestIFRoutesItemsWithoutRenumberingTheirProvenance covers the filtering node.
// The runner only infers by position when the counts match, and IF's never do —
// so it keeps each item's own origin, which is what makes a reach-back from
// either branch land on the right item.
func TestIFRoutesItemsWithoutRenumberingTheirProvenance(t *testing.T) {
	registry := engine.NewRegistry()
	if err := nodes.RegisterExecutors(registry, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, _ := registry.Lookup("core.if")

	input := workflow.NodeInput{"main": {
		{JSON: map[string]any{"tier": "vip"}, Paired: &workflow.PairedItem{SourceNodeID: "src", ItemIndex: 0}},
		{JSON: map[string]any{"tier": "std"}, Paired: &workflow.PairedItem{SourceNodeID: "src", ItemIndex: 1}},
		{JSON: map[string]any{"tier": "vip"}, Paired: &workflow.PairedItem{SourceNodeID: "src", ItemIndex: 2}},
	}}
	output, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "if", Name: "IF",
		Parameters: map[string]any{"conditions": []any{map[string]any{
			"field": "tier", "operator": "equals", "value": "vip",
		}}},
	}, input, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// The true branch holds items 0 and 2 — at positions 0 and 1 — and each
	// must still say which input item it was.
	if len(output[0]) != 2 {
		t.Fatalf("true branch = %#v, want two items", output[0])
	}
	for position, wantIndex := range []int{0, 2} {
		if got := output[0][position].Paired.ItemIndex; got != wantIndex {
			t.Errorf("true branch position %d descends from item %d, want %d — renumbering by position is exactly the wrong answer here",
				position, got, wantIndex)
		}
	}
	if len(output[1]) != 1 || output[1][0].Paired.ItemIndex != 1 {
		t.Errorf("false branch = %#v, want the item that was at index 1", output[1])
	}
}
