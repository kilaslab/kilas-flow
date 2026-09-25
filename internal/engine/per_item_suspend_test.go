package engine_test

import (
	"context"
	"encoding/json"
	"errors"
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

// perItemSuspendRun is one workflow whose Hold node is resolved one item at a
// time: Start emits one item per label, Hold fails on a label starting
// "fail", suspends on one starting "wait" and passes every other through, and
// OK (Hold's main output) and Bad (its error output, when it has one) record
// every delivery they receive.
type perItemSuspendRun struct {
	ir        workflow.IR
	executors *engine.Registry
	// deliveries is what each probe received, one entry per invocation, in
	// the order they ran.
	deliveries map[string][][]workflow.Item
	// output is the finished pass's Result.Output: what the execution record
	// stores and a sub-workflow call returns.
	output map[string]workflow.NodeOutput
}

func newPerItemSuspendRun(t *testing.T, onError string, labels ...string) *perItemSuspendRun {
	t.Helper()
	catalog := testCatalog(t, startType("test.start", "Start"),
		stepType("test.hold", "Hold"), stepType("test.probe", "Probe"))
	connections := []workflow.Connection{
		mainEdge("c1", "start", "main", "hold"),
		mainEdge("c2", "hold", "main", "ok"),
	}
	documentNodes := []workflow.Node{
		{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
		{ID: "hold", Name: "Hold", Type: "test.hold", TypeVersion: workflow.V(1),
			Settings: map[string]any{"onError": onError}},
		{ID: "ok", Name: "OK", Type: "test.probe", TypeVersion: workflow.V(1), Position: workflow.Position{Y: 0}},
	}
	if onError == "continueErrorOutput" {
		documentNodes = append(documentNodes, workflow.Node{ID: "bad", Name: "Bad", Type: "test.probe",
			TypeVersion: workflow.V(1), Position: workflow.Position{Y: 300}})
		connections = append(connections, mainEdge("c3", "hold", "error", "bad"))
	}
	run := &perItemSuspendRun{
		ir: compileDoc(t, catalog, workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            "wf_per_item_suspend", Name: "Per-item suspend",
			Nodes: documentNodes, Connections: connections, Settings: map[string]any{},
		}),
		executors:  engine.NewRegistry(),
		deliveries: map[string][][]workflow.Item{},
	}
	run.register(t, labels)
	return run
}

// newPerItemLoopRun is the same Hold as the body of a Loop Over Items of
// batchSize, with Side (a probe) hanging off Hold and After (a probe) below
// the loop's done port. Every batch is a new run of Hold, and of Side.
func newPerItemLoopRun(t *testing.T, batchSize int, labels ...string) *perItemSuspendRun {
	t.Helper()
	catalog := testCatalog(t, startType("test.start", "Start"),
		stepType("test.hold", "Hold"), stepType("test.probe", "Probe"))
	run := &perItemSuspendRun{
		ir: compileDoc(t, catalog, workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            "wf_per_item_loop", Name: "Per-item suspend in a loop",
			Nodes: []workflow.Node{
				{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
				{ID: "loop", Name: "Loop", Type: nodes.LoopNodeType, TypeVersion: workflow.V(1),
					Parameters: map[string]any{"batchSize": float64(batchSize), "maxIterations": float64(20)}},
				{ID: "hold", Name: "Hold", Type: "test.hold", TypeVersion: workflow.V(1),
					Settings: map[string]any{"onError": "continueRegularOutput"}},
				// Above the loop, so each batch's Side runs before the loop
				// dispatches the next batch and the last batch's is the last run.
				{ID: "side", Name: "Side", Type: "test.probe", TypeVersion: workflow.V(1), Position: workflow.Position{Y: -300}},
				{ID: "after", Name: "After", Type: "test.probe", TypeVersion: workflow.V(1)},
			},
			Connections: []workflow.Connection{
				mainEdge("c1", "start", "main", "loop"),
				mainEdge("c2", "loop", "loop", "hold"),
				mainEdge("c3", "hold", "main", "loop"),
				mainEdge("c4", "hold", "main", "side"),
				mainEdge("c5", "loop", "done", "after"),
			},
			Settings: map[string]any{},
		}),
		executors:  engine.NewRegistry(),
		deliveries: map[string][][]workflow.Item{},
	}
	run.register(t, labels)
	return run
}

// register installs Start, Hold and the probes.
func (run *perItemSuspendRun) register(t *testing.T, labels []string) {
	t.Helper()
	register := func(typeID string, execute engine.ExecutorFunc) {
		t.Helper()
		if err := run.executors.Register(typeID, execute); err != nil {
			t.Fatalf("Register(%s) error = %v", typeID, err)
		}
	}
	register("test.start", func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
		items := make([]workflow.Item, 0, len(labels))
		for _, label := range labels {
			items = append(items, workflow.Item{JSON: map[string]any{"p": label}})
		}
		return workflow.NodeOutput{items}, nil
	})
	register("test.hold", func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
		label, _ := input["main"][0].JSON["p"].(string)
		switch {
		case strings.HasPrefix(label, "fail"):
			return nil, errors.New(label + " broke")
		case strings.HasPrefix(label, "wait"):
			return nil, &engine.SuspendError{Mode: engine.WaitModeApproval}
		}
		return workflow.NodeOutput{{{JSON: map[string]any{"p": label}}}}, nil
	})
	register("test.probe", func(_ context.Context, node workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
		run.deliveries[node.ID] = append(run.deliveries[node.ID], input["main"])
		return workflow.NodeOutput{input["main"]}, nil
	})
	if err := nodes.RegisterExecutors(run.executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
}

// resumeAll runs the workflow and resumes every suspension until it finishes,
// answering each with answer. Each checkpoint is taken from the bytes the
// suspension carries, decoded exactly as the waits table hands them back, so
// what survives is only what was persisted — never what the suspended run
// still held in memory. It returns every pass's trace rows, in order.
func (run *perItemSuspendRun) resumeAll(t *testing.T, answer func(engine.Checkpoint) workflow.NodeOutput) []engine.NodeRun {
	t.Helper()
	runner := engine.NewRunner(run.executors)
	request := engine.Request{Input: workflow.Item{JSON: map[string]any{}}}
	result, err := runner.Run(context.Background(), run.ir, request)
	rows := append([]engine.NodeRun(nil), result.NodeRuns...)
	for resumes := 0; ; resumes++ {
		var suspended *engine.SuspendError
		if !errors.As(err, &suspended) {
			if err != nil {
				t.Fatalf("run error = %v", err)
			}
			run.output = result.Output
			return rows
		}
		if resumes == 10 {
			t.Fatal("the run was still suspending after 10 resumes")
		}
		var checkpoint engine.Checkpoint
		if decodeErr := json.Unmarshal(suspended.Checkpoint, &checkpoint); decodeErr != nil {
			t.Fatalf("decode checkpoint: %v", decodeErr)
		}
		result, err = runner.Resume(context.Background(), run.ir, request, checkpoint, answer(checkpoint))
		rows = append(rows, result.NodeRuns...)
	}
}

// passItThrough is a timer wait's answer: the item it held, unchanged.
func passItThrough(checkpoint engine.Checkpoint) workflow.NodeOutput {
	return workflow.NodeOutput{{{JSON: checkpoint.Input["main"][0].JSON}}}
}

// approveIt is an approval's answer: a decision that carries no lineage of
// its own, so the runner has to stamp it against the item that waited.
func approveIt(engine.Checkpoint) workflow.NodeOutput {
	return workflow.NodeOutput{{{JSON: map[string]any{"approved": true}}}}
}

// itemLabel names a delivered item by what it is and the Start item it pairs
// with: "p@index" for one that passed, "approved@index" for a decision and
// "error(message)@index" for a tolerated failure.
func itemLabel(item workflow.Item) string {
	name := fmt.Sprint(item.JSON["p"])
	if failure, ok := item.JSON[engine.ErrorItemKey].(map[string]any); ok {
		name = fmt.Sprintf("error(%v)", failure["message"])
	} else if approved, ok := item.JSON["approved"].(bool); ok && approved {
		name = "approved"
	}
	pair := "unpaired"
	if origin := item.Paired; origin != nil && !origin.Lost && origin.SourceNodeID == "start" {
		pair = fmt.Sprint(origin.ItemIndex)
	}
	return name + "@" + pair
}

// labels names every item of one node's finished output, port by port.
func labels(output workflow.NodeOutput) string {
	var names []string
	for _, port := range output {
		for _, item := range port {
			names = append(names, itemLabel(item))
		}
	}
	return strings.Join(names, " ")
}

// received is every item a probe was delivered, in the order it received
// them, across all of its invocations.
func (run *perItemSuspendRun) received(nodeID string) string {
	var labels []string
	for _, delivery := range run.deliveries[nodeID] {
		for _, item := range delivery {
			labels = append(labels, itemLabel(item))
		}
	}
	return strings.Join(labels, " ")
}

// TestAPerItemSuspendDeliversTheToleratedItemBeforeIt is BUG-jx2g0k as
// reported: the item before the one that suspended failed and was tolerated,
// and its error item was assembled before the suspension, checkpointed
// nowhere, and never delivered by the resume — downstream saw the resumed
// item alone.
func TestAPerItemSuspendDeliversTheToleratedItemBeforeIt(t *testing.T) {
	run := newPerItemSuspendRun(t, "continueRegularOutput", "fail-a", "wait-b")
	rows := run.resumeAll(t, approveIt)

	if got, want := run.received("ok"), "error(fail-a broke)@0 approved@1"; got != want {
		t.Fatalf("downstream received %q, want %q", got, want)
	}
	// Hold's row is written when it completes, which for a node that
	// suspended is on the resume: that row is the whole node's, so it says
	// one of its items failed, as the row of a run that never waited does.
	var hold []engine.NodeRun
	for _, row := range rows {
		if row.NodeID == "hold" {
			hold = append(hold, row)
		}
	}
	if len(hold) != 1 {
		t.Fatalf("Hold has %d trace rows, want the one written when it completed", len(hold))
	}
	if hold[0].ErrorCode != "node.partial" || hold[0].Error == nil || !strings.Contains(hold[0].Error.Error(), "fail-a broke") {
		t.Errorf("Hold's row = (%q, %v), want node.partial naming the tolerated failure", hold[0].ErrorCode, hold[0].Error)
	}
	if got := len(hold[0].Output[0]); got != 2 {
		t.Errorf("Hold's row carries %d items, want both items' outcomes", got)
	}
}

// The items before the suspension that succeeded are the same loss: nothing
// about the fix may depend on the earlier item having failed.
func TestAPerItemSuspendDeliversTheSucceededItemsBeforeIt(t *testing.T) {
	run := newPerItemSuspendRun(t, "continueRegularOutput", "a", "b", "wait-c", "d")
	run.resumeAll(t, passItThrough)

	if got, want := run.received("ok"), "a@0 b@1 wait-c@2 d@3"; got != want {
		t.Fatalf("downstream received %q, want every item in order: %q", got, want)
	}
}

// Several suspensions in one batch: every resume delivers what was assembled
// since the last one, so every item arrives exactly once and in the batch's
// order, including the failure that happened between two waits and the item
// that failed after the last of them.
func TestAPerItemNodeThatSuspendsRepeatedlyDeliversEveryItemInOrder(t *testing.T) {
	run := newPerItemSuspendRun(t, "continueRegularOutput", "a", "wait-b", "fail-c", "wait-d", "e", "fail-f")
	run.resumeAll(t, passItThrough)

	want := "a@0 wait-b@1 error(fail-c broke)@2 wait-d@3 e@4 error(fail-f broke)@5"
	if got := run.received("ok"); got != want {
		t.Fatalf("downstream received %q, want %q", got, want)
	}
}

// With an error output the tolerated failure is delivered on the error branch
// and the successes on the main one — the port each item was assembled on
// before the suspension is the port it is delivered on after it.
func TestAPerItemSuspendKeepsEachItemOnItsPort(t *testing.T) {
	run := newPerItemSuspendRun(t, "continueErrorOutput", "fail-a", "b", "wait-c", "fail-d", "e")
	run.resumeAll(t, approveIt)

	if got, want := run.received("ok"), "b@1 approved@2 e@4"; got != want {
		t.Errorf("the main branch received %q, want %q", got, want)
	}
	if got, want := run.received("bad"), "error(fail-a broke)@0 error(fail-d broke)@3"; got != want {
		t.Errorf("the error branch received %q, want %q", got, want)
	}
	for _, delivery := range run.deliveries["bad"] {
		for _, item := range delivery {
			if _, ok := item.JSON["p"]; !ok {
				t.Errorf("an error-branch item lost the input item it carries: %#v", item.JSON)
			}
		}
	}
}

// A checkpoint written before the partial output existed carries none: an
// older binary scheduled the items after the wait as a pending invocation of
// Hold and kept nothing of the items before it. It resumes exactly as it
// always did — the resumed item, then the rest as a run of their own — rather
// than failing, because refusing every wait already stored would strand them.
func TestACheckpointWithoutAPartialOutputResumesAsBefore(t *testing.T) {
	run := newPerItemSuspendRun(t, "continueRegularOutput", "fail-a", "wait-b", "c")
	runner := engine.NewRunner(run.executors)
	request := engine.Request{Input: workflow.Item{JSON: map[string]any{}}}
	_, err := runner.Run(context.Background(), run.ir, request)
	var suspended *engine.SuspendError
	if !errors.As(err, &suspended) {
		t.Fatalf("Run() error = %v, want a suspension", err)
	}
	var checkpoint engine.Checkpoint
	if err := json.Unmarshal(suspended.Checkpoint, &checkpoint); err != nil {
		t.Fatalf("decode checkpoint: %v", err)
	}
	if checkpoint.Partial == nil || checkpoint.Version != 2 {
		t.Fatalf("the checkpoint is version %d with partial %v, want version 2 carrying the run", checkpoint.Version, checkpoint.Partial)
	}
	// Rewritten into the shape an older binary stored: version 1, no partial,
	// and the items after the wait on the stack.
	rest := checkpoint.Partial.Input["main"][checkpoint.Partial.Position+1:]
	checkpoint.Version, checkpoint.Partial = 1, nil
	checkpoint.Pending = append(checkpoint.Pending, engine.PendingNode{NodeID: "hold", Input: workflow.NodeInput{"main": rest}})
	old, _ := json.Marshal(checkpoint)
	var stored map[string]json.RawMessage
	if err := json.Unmarshal(old, &stored); err != nil {
		t.Fatalf("decode old checkpoint: %v", err)
	}
	if _, found := stored["partial"]; found {
		t.Fatalf("the old-shape checkpoint still carries a partial output: %s", old)
	}
	checkpoint = engine.Checkpoint{}
	if err := json.Unmarshal(old, &checkpoint); err != nil {
		t.Fatalf("decode old checkpoint: %v", err)
	}
	if _, err := runner.Resume(context.Background(), run.ir, request, checkpoint, approveIt(checkpoint)); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if got, want := run.received("ok"), "approved@1 c@2"; got != want {
		t.Fatalf("downstream received %q, want the resumed item and the rest: %q", got, want)
	}
	if got := len(run.deliveries["ok"]); got != 2 {
		t.Errorf("downstream ran %d times, want twice, as an old checkpoint always resumed", got)
	}
}

// The pieces a per-item node's run is resolved in across its suspensions are
// one run. The execution's output — what the record stores and a sub-workflow
// call returns — used to keep only the last piece, because every piece
// completed the node, and its leaf, afresh: here the leaf's output ended as the
// items after the last wait, and a, b, c and d were gone from the record.
func TestAPerItemNodeThatSuspendsRepeatedlyIsOneRunInTheExecutionOutput(t *testing.T) {
	run := newPerItemSuspendRun(t, "continueRegularOutput", "a", "wait-b", "fail-c", "wait-d", "e", "fail-f")
	rows := run.resumeAll(t, passItThrough)

	want := "a@0 wait-b@1 error(fail-c broke)@2 wait-d@3 e@4 error(fail-f broke)@5"
	if got := labels(run.output["ok"]); got != want {
		t.Errorf("the execution output holds %q for the leaf, want %q", got, want)
	}
	if got := len(run.deliveries["ok"]); got != 1 {
		t.Errorf("the leaf ran %d times, want once: Hold ran once, however often it waited", got)
	}
	var hold []engine.NodeRun
	for _, row := range rows {
		if row.NodeID == "hold" {
			hold = append(hold, row)
		}
	}
	if len(hold) != 1 {
		t.Fatalf("Hold has %d trace rows, want one for its one run", len(hold))
	}
	if got := labels(hold[0].Output); got != want {
		t.Errorf("Hold's row holds %q, want %q", got, want)
	}
	if got := len(hold[0].Input["main"]); got != 6 {
		t.Errorf("Hold's row records %d input items, want the 6 its run was given", got)
	}
	if hold[0].RunIndex != 0 || hold[0].ErrorCode != "node.partial" {
		t.Errorf("Hold's row = run %d, %q; want run 0, node.partial", hold[0].RunIndex, hold[0].ErrorCode)
	}
}

// The smallest case: two items that both wait.
func TestTwoPerItemWaitsAreOneRunInTheExecutionOutput(t *testing.T) {
	run := newPerItemSuspendRun(t, "continueRegularOutput", "wait-a", "wait-b")
	run.resumeAll(t, passItThrough)

	if got, want := labels(run.output["ok"]), "wait-a@0 wait-b@1"; got != want {
		t.Errorf("the execution output holds %q for the leaf, want %q", got, want)
	}
	if got, want := run.received("ok"), "wait-a@0 wait-b@1"; got != want {
		t.Errorf("the leaf received %q, want %q", got, want)
	}
}

// A loop's batches are genuinely separate runs, and the execution output
// keeps a node's last run, as it always has: Side ends with the last batch.
// Nothing waits here, so this pins the semantics the per-item fix must not
// touch.
func TestALoopLeafKeepsItsLastRunInTheExecutionOutput(t *testing.T) {
	run := newPerItemLoopRun(t, 2, "a", "b", "c", "d")
	run.resumeAll(t, passItThrough)

	if got, want := labels(run.output["side"]), "c@2 d@3"; got != want {
		t.Errorf("the execution output holds %q for the loop's leaf, want its last run: %q", got, want)
	}
	if got, want := len(run.deliveries["side"]), 2; got != want {
		t.Errorf("the loop's leaf ran %d times, want once per batch: %d", got, want)
	}
	if got, want := labels(run.output["after"]), "a@0 b@1 c@2 d@3"; got != want {
		t.Errorf("the done branch holds %q, want every batch: %q", got, want)
	}
}

// A per-item wait inside a loop: each batch is still one run of Hold, whatever
// it waited on, so the loop gets one return per batch and the leaf's last run
// is the whole last batch, not the piece after its wait.
func TestAPerItemWaitInsideALoopIsOneRunPerBatch(t *testing.T) {
	run := newPerItemLoopRun(t, 2, "a", "wait-b", "wait-c", "d")
	run.resumeAll(t, passItThrough)

	if got, want := labels(run.output["side"]), "wait-c@2 d@3"; got != want {
		t.Errorf("the execution output holds %q for the loop's leaf, want the whole last batch: %q", got, want)
	}
	if got, want := run.received("side"), "a@0 wait-b@1 wait-c@2 d@3"; got != want {
		t.Errorf("the loop's leaf received %q, want %q", got, want)
	}
	if got, want := len(run.deliveries["side"]), 2; got != want {
		t.Errorf("the loop's leaf ran %d times, want once per batch: %d", got, want)
	}
	if got, want := labels(run.output["after"]), "a@0 wait-b@1 wait-c@2 d@3"; got != want {
		t.Errorf("the done branch holds %q, want every batch once: %q", got, want)
	}
}
