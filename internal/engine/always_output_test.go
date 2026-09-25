package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// alwaysOutputCounter counts the invocations of each test node and hands
// every one of them its input back, or, for the nodes named in empty, nothing.
type alwaysOutputCounter struct {
	calls map[string]int
	items map[string]int
	empty map[string]bool
}

func newAlwaysOutputCounter(empty ...string) *alwaysOutputCounter {
	counter := &alwaysOutputCounter{calls: map[string]int{}, items: map[string]int{}, empty: map[string]bool{}}
	for _, name := range empty {
		counter.empty[name] = true
	}
	return counter
}

func (counter *alwaysOutputCounter) registry(t *testing.T, typeIDs ...string) *engine.Registry {
	t.Helper()
	executors := engine.NewRegistry()
	for _, typeID := range typeIDs {
		if err := executors.Register(typeID, engine.ExecutorFunc(
			func(_ context.Context, node workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
				counter.calls[node.Name]++
				delivered := []workflow.Item{}
				for _, items := range input {
					delivered = append(delivered, items...)
				}
				counter.items[node.Name] += len(delivered)
				output := make(workflow.NodeOutput, len(node.Definition.Outputs))
				for index := range output {
					output[index] = []workflow.Item{}
				}
				switch {
				case len(node.Definition.Inputs) == 0:
					output[0] = []workflow.Item{{JSON: map[string]any{}}}
				case !counter.empty[node.Name]:
					output[0] = delivered
				}
				return output, nil
			})); err != nil {
			t.Fatalf("Register(%s) error = %v", typeID, err)
		}
	}
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	return executors
}

// skippedRows counts the trace rows of a node that record it as not run.
func skippedRows(result engine.Result, nodeID string) int {
	count := 0
	for _, run := range result.NodeRuns {
		if run.NodeID == nodeID && run.Skipped {
			count++
		}
	}
	return count
}

var alwaysOutput = map[string]any{"alwaysOutputData": true}

// TestAlwaysOutputDataDoesNotRunANodeThatGotNoItems is the linear case the
// Code-node self-test found.
//
// Always Output Data is about what a node that ran hands on, not about whether
// it runs: a node whose parent emitted nothing is not executed at all, the
// setting notwithstanding. Running it anyway made it emit an item out of
// nothing, and every node after it ran on that invented item.
func TestAlwaysOutputDataDoesNotRunANodeThatGotNoItems(t *testing.T) {
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"))
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_always_linear", Name: "Always Output Data after an empty node",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "nothing", Name: "Nothing", Type: "test.step", TypeVersion: workflow.V(1)},
			{ID: "flagged", Name: "Flagged", Type: "test.step", TypeVersion: workflow.V(1), Settings: alwaysOutput},
			{ID: "after", Name: "After", Type: "test.step", TypeVersion: workflow.V(1), Settings: alwaysOutput},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "nothing"),
			mainEdge("c2", "nothing", "main", "flagged"),
			mainEdge("c3", "flagged", "main", "after"),
		},
		Settings: map[string]any{},
	})

	counter := newAlwaysOutputCounter("Nothing")
	result, err := engine.NewRunner(counter.registry(t, "test.start", "test.step")).Run(context.Background(), ir,
		engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if counter.calls["Nothing"] != 1 {
		t.Fatalf("the empty node ran %d times, want once", counter.calls["Nothing"])
	}
	if counter.calls["Flagged"] != 0 || counter.calls["After"] != 0 {
		t.Errorf("after an empty node, Flagged ran %d times and After %d, want neither to run whatever their alwaysOutputData",
			counter.calls["Flagged"], counter.calls["After"])
	}
	if skippedRows(result, "flagged") != 1 {
		t.Errorf("the flagged node has %d skipped rows, want 1: it was reached and not run", skippedRows(result, "flagged"))
	}
	for _, run := range result.NodeRuns {
		if run.NodeID == "flagged" && !run.Skipped {
			t.Errorf("the flagged node has a row that ran, with output %v", run.Output)
		}
	}
}

// TestAlwaysOutputDataStillEmitsOneEmptyItemWhenTheNodeRan is the half the
// fix must keep: a node that did run and returned nothing hands on exactly one
// empty item, and the trace shows it as run rather than skipped.
func TestAlwaysOutputDataStillEmitsOneEmptyItemWhenTheNodeRan(t *testing.T) {
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"))
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_always_ran", Name: "Always Output Data on a node that ran",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "lookup", Name: "Lookup", Type: "test.step", TypeVersion: workflow.V(1), Settings: alwaysOutput},
			{ID: "after", Name: "After", Type: "test.step", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "lookup"),
			mainEdge("c2", "lookup", "main", "after"),
		},
		Settings: map[string]any{},
	})

	counter := newAlwaysOutputCounter("Lookup")
	result, err := engine.NewRunner(counter.registry(t, "test.start", "test.step")).Run(context.Background(), ir,
		engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if counter.calls["Lookup"] != 1 {
		t.Fatalf("the flagged node ran %d times, want once", counter.calls["Lookup"])
	}
	lookup := nodeRun(t, result, "lookup")
	if lookup.Skipped {
		t.Fatal("the flagged node that ran is recorded as skipped")
	}
	if len(lookup.Output) != 1 || len(lookup.Output[0]) != 1 || len(lookup.Output[0][0].JSON) != 0 {
		t.Fatalf("the flagged node's output = %v, want exactly one empty item", lookup.Output)
	}
	if counter.calls["After"] != 1 || counter.items["After"] != 1 {
		t.Errorf("the node after it ran %d times over %d items, want once over the one empty item",
			counter.calls["After"], counter.items["After"])
	}
}

// TestAlwaysOutputDataInALoopBodyLetsTheLoopFinish is the template-2896 shape:
// a Loop Over Items whose body node carries Always Output Data.
//
// When the loop finishes it sends its items out on `done` and nothing on
// `loop`. The body was run on that nothing, emitted an empty item because of
// its setting, and fed it back into the loop as new work — so the loop never
// finished. The body must simply not run once the loop port is empty.
func TestAlwaysOutputDataInALoopBodyLetsTheLoopFinish(t *testing.T) {
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"))
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_always_loop", Name: "Loop whose body always outputs data",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "loop", Name: "Loop", Type: nodes.LoopNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"batchSize": float64(1), "maxIterations": float64(20)}},
			{ID: "body", Name: "Body", Type: "test.step", TypeVersion: workflow.V(1), Settings: alwaysOutput,
				Position: workflow.Position{X: 400, Y: 200}},
			{ID: "done", Name: "Done", Type: "test.step", TypeVersion: workflow.V(1),
				Position: workflow.Position{X: 400, Y: -200}},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "loop"),
			mainEdge("c2", "loop", "done", "done"),
			mainEdge("c3", "loop", "loop", "body"),
			mainEdge("c4", "body", "main", "loop"),
		},
		Settings: map[string]any{},
	})

	// A bound on the test, not on the product: the loop's own iteration cap
	// is what ends the broken run, and this only keeps a regression from
	// hanging the suite if that cap ever stops applying.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	counter := newAlwaysOutputCounter()
	result, err := engine.NewRunner(counter.registry(t, "test.start", "test.step")).Run(ctx, ir,
		engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v; the loop must finish after its one item", err)
	}
	if counter.calls["Body"] != 1 {
		t.Errorf("the loop body ran %d times for one item, want once", counter.calls["Body"])
	}
	if counter.calls["Done"] != 1 || counter.items["Done"] != 1 {
		t.Errorf("the node on done ran %d times over %d items, want once over the one item",
			counter.calls["Done"], counter.items["Done"])
	}
	if skippedRows(result, "body") != 1 {
		t.Errorf("the loop body has %d skipped rows, want the one for the empty loop port on the final call",
			skippedRows(result, "body"))
	}
}

// TestAlwaysOutputDataDoesNotRunAMultiInputNodeNothingReached covers a node
// with several item inputs, the Merge shape.
//
// Such a node runs once the branches feeding it have finished, and only if at
// least one of them delivered items. Neither did here, so it does not run,
// and its setting does not change that.
func TestAlwaysOutputDataDoesNotRunAMultiInputNodeNothingReached(t *testing.T) {
	join := node.Definition{
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  "test.join", Version: workflow.V(1), DisplayName: "Join", Category: "Test",
		Inputs: []workflow.Port{
			{Name: "input1", Kind: workflow.ConnectionMain},
			{Name: "input2", Kind: workflow.ConnectionMain},
		},
		Outputs:    []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		ExecutorID: "test.join",
	}
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"), join)
	into := func(id, source, port string) workflow.Connection {
		return workflow.Connection{ID: id, Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: source, Port: "main"},
			Target: workflow.Endpoint{NodeID: "join", Port: port}}
	}
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_always_join", Name: "Always Output Data on a two-input node",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "left", Name: "Left", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 200, Y: 0}},
			{ID: "right", Name: "Right", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 200, Y: 300}},
			{ID: "join", Name: "Join", Type: "test.join", TypeVersion: workflow.V(1), Settings: alwaysOutput},
			{ID: "after", Name: "After", Type: "test.step", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "left"),
			mainEdge("c2", "start", "main", "right"),
			into("c3", "left", "input1"),
			into("c4", "right", "input2"),
			mainEdge("c5", "join", "main", "after"),
		},
		Settings: map[string]any{},
	})

	counter := newAlwaysOutputCounter("Left", "Right")
	result, err := engine.NewRunner(counter.registry(t, "test.start", "test.step", "test.join")).Run(context.Background(), ir,
		engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if counter.calls["Join"] != 0 || counter.calls["After"] != 0 {
		t.Errorf("with neither branch delivering, Join ran %d times and After %d, want neither to run",
			counter.calls["Join"], counter.calls["After"])
	}
	if skippedRows(result, "join") != 1 {
		t.Errorf("the two-input node has %d skipped rows, want 1", skippedRows(result, "join"))
	}

	// One branch delivering is enough to run it, and a run that produced
	// nothing is the case the setting exists for: one empty item goes on.
	counter = newAlwaysOutputCounter("Right", "Join")
	result, err = engine.NewRunner(counter.registry(t, "test.start", "test.step", "test.join")).Run(context.Background(), ir,
		engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if counter.calls["Join"] != 1 || counter.items["Join"] != 1 {
		t.Fatalf("with one branch delivering, Join ran %d times over %d items, want once over that branch's item",
			counter.calls["Join"], counter.items["Join"])
	}
	if counter.calls["After"] != 1 || counter.items["After"] != 1 {
		t.Errorf("after the two-input node returned nothing, After ran %d times over %d items, want once over one empty item",
			counter.calls["After"], counter.items["After"])
	}
	if skippedRows(result, "join") != 0 {
		t.Errorf("the two-input node that ran has %d skipped rows, want none", skippedRows(result, "join"))
	}
}

// TestAResumedRunDoesNotRunABranchThatGotNoItems covers the same rule across a
// suspension.
//
// A branch that received nothing is still owed a skipped row, so it waits on
// the stack behind the branch that suspended. The checkpoint keeps its node
// and its (empty) input, and a resumed run must not mistake that entry for
// work: before the fix it ran the node on no items.
func TestAResumedRunDoesNotRunABranchThatGotNoItems(t *testing.T) {
	split := node.Definition{
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  "test.split", Version: workflow.V(1), DisplayName: "Split", Category: "Test",
		Inputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs: []workflow.Port{
			{Name: "yes", Kind: workflow.ConnectionMain},
			{Name: "no", Kind: workflow.ConnectionMain},
		},
		ExecutorID: "test.split",
	}
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"),
		stepType("test.wait", "Wait"), split)
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_always_resume", Name: "Suspension with an empty sibling branch",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "split", Name: "Split", Type: "test.split", TypeVersion: workflow.V(1)},
			{ID: "wait", Name: "Wait", Type: "test.wait", TypeVersion: workflow.V(1), Position: workflow.Position{X: 400, Y: 0}},
			{ID: "untaken", Name: "Untaken", Type: "test.step", TypeVersion: workflow.V(1),
				Position: workflow.Position{X: 400, Y: 300}},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "split"),
			mainEdge("c2", "split", "yes", "wait"),
			mainEdge("c3", "split", "no", "untaken"),
		},
		Settings: map[string]any{},
	})

	counter := newAlwaysOutputCounter()
	executors := counter.registry(t, "test.start", "test.step")
	if err := executors.Register("test.split", engine.ExecutorFunc(
		func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{input["main"], {}}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := executors.Register("test.wait", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return nil, &engine.SuspendError{Mode: engine.WaitModeApproval}
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	runner := engine.NewRunner(executors)
	request := engine.Request{Input: workflow.Item{JSON: map[string]any{}}}
	_, err := runner.Run(context.Background(), ir, request)
	var suspended *engine.SuspendError
	if !errors.As(err, &suspended) {
		t.Fatalf("Run() error = %v, want the wait to suspend the run", err)
	}
	var checkpoint engine.Checkpoint
	if err := json.Unmarshal(suspended.Checkpoint, &checkpoint); err != nil {
		t.Fatalf("decode checkpoint: %v", err)
	}
	result, err := runner.Resume(context.Background(), ir, request, checkpoint,
		workflow.NodeOutput{{{JSON: map[string]any{"approved": true}}}})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if counter.calls["Untaken"] != 0 {
		t.Errorf("the branch that got no items ran %d times after the resume, want it not to run", counter.calls["Untaken"])
	}
	if skippedRows(result, "untaken") != 1 {
		t.Errorf("the untaken branch has %d skipped rows after the resume, want 1", skippedRows(result, "untaken"))
	}
}
