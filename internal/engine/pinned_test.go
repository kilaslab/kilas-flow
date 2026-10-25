package engine_test

import (
	"context"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// pinnedExecutors is the registry the pinned runs use: both node types record
// their invocations, which is the one fact these tests exist to assert — a
// pinned node is not executed.
func pinnedExecutors(t *testing.T, invoked *[]string, received *[]workflow.Item) *engine.Registry {
	t.Helper()
	executors := engine.NewRegistry()
	if err := executors.Register("test.start", engine.ExecutorFunc(
		func(_ context.Context, node workflow.IRNode, _ workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			*invoked = append(*invoked, node.Name)
			return workflow.NodeOutput{{{JSON: map[string]any{"customer": "Ada"}}}}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := executors.Register("test.step", engine.ExecutorFunc(
		func(_ context.Context, node workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			*invoked = append(*invoked, node.Name)
			if node.Name == "After" && received != nil {
				*received = append(*received, input["main"]...)
			}
			return workflow.NodeOutput{input["main"]}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	return executors
}

// TestPinnedNodeReplaysItsItemsInsteadOfExecuting is FEAT-jrrqs2's engine half:
// the document pins a node's output, and a run feeds those items as that
// node's output without invoking it. The trace row is marked, because a node
// that never ran must not read as one that did.
func TestPinnedNodeReplaysItsItemsInsteadOfExecuting(t *testing.T) {
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"))
	pinned := []workflow.Item{
		{JSON: map[string]any{"pinned": float64(1)}},
		{JSON: map[string]any{"pinned": float64(2)}},
	}

	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_pinned", Name: "A pinned node",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "held", Name: "Held", Type: "test.step", TypeVersion: workflow.V(1)},
			{ID: "after", Name: "After", Type: "test.step", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "held"),
			mainEdge("c2", "held", "main", "after"),
		},
		Settings: map[string]any{},
		PinData:  map[string][]workflow.Item{"Held": pinned},
	})

	var invoked []string
	var received []workflow.Item
	runner := engine.NewRunner(pinnedExecutors(t, &invoked, &received))

	result, err := runner.Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	for _, name := range invoked {
		if name == "Held" {
			t.Error("a pinned node was executed")
		}
	}
	if len(received) != 2 || received[0].JSON["pinned"] != float64(1) || received[1].JSON["pinned"] != float64(2) {
		t.Errorf("the node below a pinned one received %#v, want the pinned items", received)
	}

	var row *engine.NodeRun
	for index, run := range result.NodeRuns {
		if run.NodeID == "held" {
			row = &result.NodeRuns[index]
		}
	}
	if row == nil {
		t.Fatal("the pinned node has no trace row")
	}
	if !row.Pinned {
		t.Error("the pinned node's trace row is not marked as pinned")
	}
	if row.Skipped {
		t.Error("a pinned node is not a pruned branch")
	}
	if len(row.Output) != 1 || len(row.Output[0]) != 2 || row.Output[0][0].JSON["pinned"] != float64(1) {
		t.Errorf("the pinned node's trace output = %#v, want the pinned items", row.Output)
	}
}

// TestAPinnedTriggerReplaysItsItems is the other start of a run: a trigger the
// author pinned produces its pinned items rather than firing, which is what
// makes a whole workflow testable against recorded data.
func TestAPinnedTriggerReplaysItsItems(t *testing.T) {
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"))

	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_pinned_trigger", Name: "A pinned trigger",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "after", Name: "After", Type: "test.step", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "after"),
		},
		Settings: map[string]any{},
		PinData:  map[string][]workflow.Item{"Start": {{JSON: map[string]any{"recorded": true}}}},
	})

	var invoked []string
	var received []workflow.Item
	runner := engine.NewRunner(pinnedExecutors(t, &invoked, &received))

	result, err := runner.Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, name := range invoked {
		if name == "Start" {
			t.Error("a pinned trigger was executed")
		}
	}
	if len(received) != 1 || received[0].JSON["recorded"] != true {
		t.Errorf("the node below a pinned trigger received %#v, want the recorded item", received)
	}
	for _, run := range result.NodeRuns {
		if run.NodeID == "start" && !run.Pinned {
			t.Error("the pinned trigger's trace row is not marked as pinned")
		}
	}
}
