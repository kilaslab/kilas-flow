package engine_test

import (
	"context"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// ifIntoMerge is Start → IF, with IF's `true` on Merge's first input and its
// `false` on the second, and Merge feeding After. Start's item has no tier, so
// it always goes to `false`: only the second input ever receives items.
func ifIntoMerge(t *testing.T, mergeParameters map[string]any) workflow.IR {
	t.Helper()
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"))
	into := func(id, sourcePort, port string) workflow.Connection {
		return workflow.Connection{ID: id, Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "if", Port: sourcePort},
			Target: workflow.Endpoint{NodeID: "merge", Port: port}}
	}
	return compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_required_inputs", Name: "IF into a Merge",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "if", Name: "IF", Type: "kilasflow.if", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"conditions": []any{map[string]any{
					"field": "tier", "operator": "equals", "value": "vip",
				}}}},
			{ID: "merge", Name: "Merge", Type: "kilasflow.merge", TypeVersion: workflow.V(1),
				Parameters: mergeParameters, Settings: alwaysOutput},
			{ID: "after", Name: "After", Type: "test.step", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "if"),
			into("c2", "true", "input1"),
			into("c3", "false", "input2"),
			mainEdge("c4", "merge", "main", "after"),
		},
		Settings: map[string]any{},
	})
}

// TestMergeChooseBranchDoesNotRunOnOneInput is n8n's rule for its Merge node:
// in `chooseBranch` mode both inputs must have received items before it runs,
// and in every other mode one is enough.
//
// Running it on one side handed that side on as though the author had chosen
// it, and with Always Output Data on, an empty item went on where n8n sends
// nothing at all.
func TestMergeChooseBranchDoesNotRunOnOneInput(t *testing.T) {
	ir := ifIntoMerge(t, map[string]any{"mode": "chooseBranch", "chooseBranch": float64(2)})
	counter := newAlwaysOutputCounter()
	result, err := engine.NewRunner(counter.registry(t, "test.start", "test.step")).Run(context.Background(), ir,
		engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if skippedRows(result, "merge") != 1 {
		t.Errorf("a chooseBranch Merge with one input delivered has %d skipped rows, want 1: it must not run", skippedRows(result, "merge"))
	}
	for _, run := range result.NodeRuns {
		if run.NodeID == "merge" && !run.Skipped {
			t.Errorf("the chooseBranch Merge ran on one input, with output %v", run.Output)
		}
	}
	if counter.calls["After"] != 0 {
		t.Errorf("After ran %d times below a chooseBranch Merge that got one input, want it not to run", counter.calls["After"])
	}
}

// TestMergeInOtherModesRunsOnOneInput keeps the other half of the rule: an
// append Merge still runs on whichever input received items.
func TestMergeInOtherModesRunsOnOneInput(t *testing.T) {
	ir := ifIntoMerge(t, map[string]any{"mode": "append"})
	counter := newAlwaysOutputCounter()
	result, err := engine.NewRunner(counter.registry(t, "test.start", "test.step")).Run(context.Background(), ir,
		engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if skippedRows(result, "merge") != 0 {
		t.Errorf("an append Merge with one input delivered has %d skipped rows, want it to run", skippedRows(result, "merge"))
	}
	if counter.calls["After"] != 1 || counter.items["After"] != 1 {
		t.Errorf("below an append Merge, After ran %d times over %d items, want once over the delivered item",
			counter.calls["After"], counter.items["After"])
	}
}

// TestMergeChooseBranchRunsWhenBothInputsDelivered is the case the mode is
// for: both sides arrived, and the chosen one goes on.
func TestMergeChooseBranchRunsWhenBothInputsDelivered(t *testing.T) {
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"))
	into := func(id, source, port string) workflow.Connection {
		return workflow.Connection{ID: id, Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: source, Port: "main"},
			Target: workflow.Endpoint{NodeID: "merge", Port: port}}
	}
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_required_inputs_both", Name: "Both sides into a chooseBranch Merge",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "left", Name: "Left", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"side": "left"}}},
			{ID: "right", Name: "Right", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"side": "right"}}},
			{ID: "merge", Name: "Merge", Type: "kilasflow.merge", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"mode": "chooseBranch", "chooseBranch": float64(2)}},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "left"),
			mainEdge("c2", "start", "main", "right"),
			into("c3", "left", "input1"),
			into("c4", "right", "input2"),
		},
		Settings: map[string]any{},
	})
	counter := newAlwaysOutputCounter()
	result, err := engine.NewRunner(counter.registry(t, "test.start")).Run(context.Background(), ir,
		engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	output := result.Output["merge"]
	if len(output) != 1 || len(output[0]) != 1 || output[0][0].JSON["side"] != "right" {
		t.Fatalf("chooseBranch Merge output = %#v, want the second input's one item", output)
	}
}

// TestRequiredInputsAreReadFromTheDefinition is the general form: any node
// with several item inputs may name the ones it cannot run without, and the
// scheduler waits for exactly those.
func TestRequiredInputsAreReadFromTheDefinition(t *testing.T) {
	join := node.Definition{
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  "test.join", Version: workflow.V(1), DisplayName: "Join", Category: "Test",
		Inputs: []workflow.Port{
			{Name: "input1", Kind: workflow.ConnectionMain},
			{Name: "input2", Kind: workflow.ConnectionMain},
		},
		Outputs:    []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		ExecutorID: "test.join",
		RequiredInputsFor: func(map[string]any, workflow.TypeVersion) []string {
			return []string{"input2"}
		},
	}
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"), join)
	into := func(id, source, port string) workflow.Connection {
		return workflow.Connection{ID: id, Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: source, Port: "main"},
			Target: workflow.Endpoint{NodeID: "join", Port: port}}
	}
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_required_inputs_fixed", Name: "A join that needs its second input",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "left", Name: "Left", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 200, Y: 0}},
			{ID: "right", Name: "Right", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 200, Y: 300}},
			{ID: "join", Name: "Join", Type: "test.join", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "left"),
			mainEdge("c2", "start", "main", "right"),
			into("c3", "left", "input1"),
			into("c4", "right", "input2"),
		},
		Settings: map[string]any{},
	})

	// Only the first input received items: the second is required, so Join
	// does not run.
	counter := newAlwaysOutputCounter("Right")
	result, err := engine.NewRunner(counter.registry(t, "test.start", "test.step", "test.join")).Run(context.Background(), ir,
		engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if counter.calls["Join"] != 0 || skippedRows(result, "join") != 1 {
		t.Errorf("without its required input, Join ran %d times with %d skipped rows, want no run and one skipped row",
			counter.calls["Join"], skippedRows(result, "join"))
	}

	// Only the second input received items: that is all it requires.
	counter = newAlwaysOutputCounter("Left")
	if _, err := engine.NewRunner(counter.registry(t, "test.start", "test.step", "test.join")).Run(context.Background(), ir,
		engine.Request{Input: workflow.Item{JSON: map[string]any{}}}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if counter.calls["Join"] != 1 || counter.items["Join"] != 1 {
		t.Errorf("with its required input delivered, Join ran %d times over %d items, want once over that item",
			counter.calls["Join"], counter.items["Join"])
	}
}
