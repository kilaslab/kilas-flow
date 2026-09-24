package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// threeItemStart and step are the executors these cases share: a trigger that
// emits three empty items, and a step whose behaviour the case supplies.
func threeItemStart(t *testing.T, stepID string, step engine.ExecutorFunc) *engine.Registry {
	t.Helper()
	executors := engine.NewRegistry()
	start := engine.ExecutorFunc(func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
		return workflow.NodeOutput{{{JSON: map[string]any{}}, {JSON: map[string]any{}}, {JSON: map[string]any{}}}}, nil
	})
	if err := executors.Register("test.start", start); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := executors.Register(stepID, step); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	return executors
}

// A branch that delivers nothing does not run the node it feeds, so it does
// not count as one of its runs: n8n's $runIndex counts runs, not deliveries.
func TestASkippedDeliveryDoesNotCountAsARun(t *testing.T) {
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"))
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_skip", Name: "One branch empty",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "empty", Name: "Empty", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 200, Y: 0}},
			{ID: "full", Name: "Full", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 200, Y: 300}},
			{ID: "tail", Name: "Tail", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 400, Y: 150}},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "empty"), mainEdge("c2", "start", "main", "full"),
			mainEdge("c3", "empty", "main", "tail"), mainEdge("c4", "full", "main", "tail"),
		},
		Settings: map[string]any{},
	})
	var seen []int
	executors := threeItemStart(t, "test.step", func(_ context.Context, node workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
		switch node.ID {
		case "empty":
			return workflow.NodeOutput{{}}, nil
		case "tail":
			seen = append(seen, request.RunIndex)
		}
		return workflow.NodeOutput{input["main"]}, nil
	})
	if _, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if fmt.Sprint(seen) != "[0]" {
		t.Fatalf("the tail ran with run indexes %v, want its one real run to be 0", seen)
	}
}

// A read that names a run numbers a node's runs as $runIndex does, real runs
// only: the tail was delivered nothing once (an empty run recorded for it)
// and then ran, so its latest run is run 0, and reading it as run 0, or as
// the reader's own $runIndex in step with it, is not an earlier run.
func TestAReadOfANamedRunCountsOnlyRealRuns(t *testing.T) {
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"))
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_skip_read", Name: "Read after a skip",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "empty", Name: "Empty", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 200, Y: 0}},
			{ID: "full", Name: "Full", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 200, Y: 300}},
			{ID: "tail", Name: "Tail", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 400, Y: 150}},
			{ID: "reader", Name: "Reader", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 600, Y: 150}},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "empty"), mainEdge("c2", "start", "main", "full"),
			mainEdge("c3", "empty", "main", "tail"), mainEdge("c4", "full", "main", "tail"), mainEdge("c5", "tail", "main", "reader"),
		},
		Settings: map[string]any{},
	})
	var read []string
	var tail expression.NodeItem
	executors := threeItemStart(t, "test.step", func(_ context.Context, node workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
		switch node.ID {
		case "empty":
			return workflow.NodeOutput{{}}, nil
		case "reader":
			tail = request.NodeItems["Tail"]
			context := request.ExpressionContext(input["main"][0], input, 0)
			for _, template := range []string{"{{ $items('Tail', 0, 0).length }}", "{{ $items('Tail', 0, $runIndex).length }}"} {
				value, err := expression.Evaluate(template, context)
				read = append(read, fmt.Sprint(value, err))
			}
		}
		return workflow.NodeOutput{input["main"]}, nil
	})
	if _, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if tail.RunIndex != 1 || tail.LatestRun() != 0 {
		t.Fatalf("tail recorded as run %d, latest real run %d; want the skipped delivery counted only in the first", tail.RunIndex, tail.LatestRun())
	}
	if got := strings.Join(read, " "); got != "3 <nil> 3 <nil>" {
		t.Fatalf("reads = %s, want the tail's three items both ways", got)
	}
}

// A resumed run counts on from the checkpoint's executed runs. A checkpoint
// written before they were counted has only the runs, skipped deliveries
// included, which is what the count was read from then.
func TestAResumedRunCountsOnFromTheCheckpoint(t *testing.T) {
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"))
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_resume", Name: "Resume",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "wait", Name: "Wait", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 200}},
			{ID: "count", Name: "Count", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 400}},
		},
		Connections: []workflow.Connection{mainEdge("c1", "start", "main", "wait"), mainEdge("c2", "wait", "main", "count")},
		Settings:    map[string]any{},
	})
	item := workflow.Item{JSON: map[string]any{}}
	ran := workflow.NodeOutput{{item}}
	for _, executions := range []map[string]int{{"count": 1}, nil} {
		var seen []int
		executors := threeItemStart(t, "test.step", func(_ context.Context, node workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
			if node.ID == "count" {
				seen = append(seen, request.RunIndex)
			}
			return workflow.NodeOutput{input["main"]}, nil
		})
		checkpoint := engine.Checkpoint{
			SuspendNode: "wait", SuspendAttempt: 1,
			Input:     workflow.NodeInput{"main": {item}},
			Completed: map[string]workflow.NodeOutput{"start": ran},
			// Count ran once, and a skipped branch delivered to it once.
			Runs:       map[string][]workflow.NodeOutput{"start": {ran}, "count": {ran, {{}}}},
			Executions: executions,
		}
		if _, err := engine.NewRunner(executors).Resume(context.Background(), ir, engine.Request{}, checkpoint, ran); err != nil {
			t.Fatalf("Resume() error = %v", err)
		}
		want := "[1]"
		if executions == nil {
			want = "[2]"
		}
		if got := fmt.Sprint(seen); got != want {
			t.Errorf("executions %v: the resumed node ran with run index %s, want %s", executions, got, want)
		}
	}
}

// A checkpoint written before a node's items were recorded by output still
// reads one output: the lengths are rebuilt from its named ports, in the
// order its definition gives its outputs.
func TestAnOlderCheckpointStillReadsOneOutput(t *testing.T) {
	gate := stepType("test.gate", "Gate")
	gate.Outputs = []workflow.Port{{Name: "true", Kind: workflow.ConnectionMain}, {Name: "false", Kind: workflow.ConnectionMain}}
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"), gate)
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_old_gate", Name: "Old gate",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "gate", Name: "Gate", Type: "test.gate", TypeVersion: workflow.V(1), Position: workflow.Position{X: 200}},
			{ID: "wait", Name: "Wait", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 400}},
			{ID: "reader", Name: "Reader", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 600}},
		},
		Connections: []workflow.Connection{mainEdge("c1", "start", "main", "gate"), mainEdge("c2", "gate", "true", "wait"), mainEdge("c3", "wait", "main", "reader")},
		Settings:    map[string]any{},
	})
	item := workflow.Item{JSON: map[string]any{}}
	ran := workflow.NodeOutput{{item}}
	var read []string
	executors := threeItemStart(t, "test.step", func(_ context.Context, node workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
		if node.ID != "reader" {
			return workflow.NodeOutput{input["main"]}, nil
		}
		context := request.ExpressionContext(input["main"][0], input, 0)
		for _, template := range []string{"{{ $items('Gate').map(i => i.json.v) }}", "{{ $items('Gate', 1).map(i => i.json.v) }}"} {
			value, err := expression.Evaluate(template, context)
			read = append(read, fmt.Sprint(value, err))
		}
		return workflow.NodeOutput{input["main"]}, nil
	})
	gated := workflow.NodeOutput{{item, item}, {item}}
	if err := executors.Register("test.gate", engine.ExecutorFunc(func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
		return gated, nil
	})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	checkpoint := engine.Checkpoint{
		SuspendNode: "wait", SuspendAttempt: 1,
		Input:     workflow.NodeInput{"main": {item}},
		Completed: map[string]workflow.NodeOutput{"start": ran, "gate": gated},
		Runs:      map[string][]workflow.NodeOutput{"start": {ran}, "gate": {gated}},
		NodeItems: map[string]expression.NodeItem{"Gate": {
			NodeID: "gate", Items: []map[string]any{{"v": "t1"}, {"v": "t2"}, {"v": "f1"}},
			PortOffsets: map[string]int{"true": 0, "false": 2}, PortLengths: map[string]int{"true": 2, "false": 1},
		}},
	}
	if _, err := engine.NewRunner(executors).Resume(context.Background(), ir, engine.Request{}, checkpoint, ran); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if got := strings.Join(read, " "); got != "[t1 t2] <nil> [f1] <nil>" {
		t.Fatalf("reads = %s, want each output on its own", got)
	}
}

// Each attempt of a retried node has its own row, and its own console lines.
func TestEachRetryKeepsWhatItPrinted(t *testing.T) {
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"))
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_retry", Name: "Flaky",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "flaky", Name: "Flaky", Type: "test.step", TypeVersion: workflow.V(1),
				Settings: map[string]any{"retryOnFail": true, "maxTries": float64(3), "waitBetweenTries": float64(0)}},
		},
		Connections: []workflow.Connection{mainEdge("c1", "start", "main", "flaky")},
		Settings:    map[string]any{},
	})
	attempt := 0
	executors := threeItemStart(t, "test.step", func(_ context.Context, node workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
		attempt++
		detail, _ := json.Marshal(engine.ConsoleDetail{Lines: []engine.ConsoleLine{{Level: "log", Text: fmt.Sprintf("attempt %d", attempt)}}})
		request.Events.Emit(engine.NodeEvent{NodeID: node.ID, Name: engine.ConsoleEventName, Detail: detail})
		if attempt < 3 {
			return nil, fmt.Errorf("flaky %d", attempt)
		}
		return workflow.NodeOutput{input["main"]}, nil
	})
	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var rows []string
	for _, run := range result.NodeRuns {
		if run.NodeID != "flaky" {
			continue
		}
		var detail engine.ConsoleDetail
		if err := json.Unmarshal(run.Console, &detail); err != nil {
			t.Fatalf("attempt %d console %q: %v", run.Attempt, run.Console, err)
		}
		texts := make([]string, 0, len(detail.Lines))
		for _, line := range detail.Lines {
			texts = append(texts, line.Text)
		}
		rows = append(rows, fmt.Sprintf("%d:%s", run.Attempt, strings.Join(texts, "|")))
	}
	if got, want := strings.Join(rows, " "), "1:attempt 1 2:attempt 2 3:attempt 3"; got != want {
		t.Fatalf("rows = %q, want %q", got, want)
	}
}

// A whole-batch node sees its items together even when it tolerates
// failures. Split into one-item calls, a node that sums its items would sum
// them one at a time; a node without the flag is still split, as before.
func TestAWholeBatchNodeIsNotSplitByContinueOnFail(t *testing.T) {
	for _, whole := range []bool{true, false} {
		definition := stepType("test.step", "Step")
		definition.WholeBatch = whole
		catalog := testCatalog(t, startType("test.start", "Start"), definition)
		ir := compileDoc(t, catalog, workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_batch", Name: "Batch",
			Nodes: []workflow.Node{
				{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
				{ID: "sum", Name: "Sum", Type: "test.step", TypeVersion: workflow.V(1),
					Settings: map[string]any{"onError": "continueRegularOutput"}},
			},
			Connections: []workflow.Connection{mainEdge("c1", "start", "main", "sum")},
			Settings:    map[string]any{},
		})
		var batches []int
		executors := threeItemStart(t, "test.step", func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			batches = append(batches, len(input["main"]))
			return workflow.NodeOutput{input["main"]}, nil
		})
		if _, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}}); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		want := "[1 1 1]"
		if whole {
			want = "[3]"
		}
		if got := fmt.Sprint(batches); got != want {
			t.Errorf("whole batch %v: the node saw batches %s, want %s", whole, got, want)
		}
	}
}
