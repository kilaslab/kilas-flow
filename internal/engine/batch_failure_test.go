package engine_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// batchFailureRun runs Start (three items, each with its n) into Batch, a
// whole-batch node whose one call fails with the given error, under the
// given onError, and answers Batch's trace rows. Batch retries on failure,
// so a failure it retried shows as a row per attempt.
func batchFailureRun(t *testing.T, onError string, asMessage bool, failure error) []engine.NodeRun {
	t.Helper()
	definition := stepType("test.batch", "Batch")
	definition.WholeBatch = true
	definition.ErrorAsMessage = asMessage
	catalog := testCatalog(t, startType("test.start", "Start"), definition)
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_batch_failure", Name: "Batch failure",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "batch", Name: "Batch", Type: "test.batch", TypeVersion: workflow.V(1), Settings: map[string]any{
				"onError": onError, "retryOnFail": true, "maxTries": float64(3), "waitBetweenTries": float64(0)}},
		},
		Connections: []workflow.Connection{mainEdge("c1", "start", "main", "batch")},
		Settings:    map[string]any{},
	})
	executors := withExecutors(t, map[string]engine.ExecutorFunc{
		"test.start": func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			items := make([]workflow.Item, 0, 3)
			for index := range 3 {
				items = append(items, workflow.Item{JSON: map[string]any{"n": float64(index)}})
			}
			return workflow.NodeOutput{items}, nil
		},
		"test.batch": func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return nil, failure
		},
	})
	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("%s: Run() error = %v", onError, err)
	}
	return nodeRuns(result, "batch")
}

// A node that ran its whole batch as one call and failed, and says so with a
// BatchFailure, tolerates the failure with one error item, as n8n's Code node
// does in "Run Once for All Items" mode: downstream fires once, not once per
// input item. Under continueErrorOutput the item goes to the error output as
// it is, without any input item's fields, because no one input item failed.
// The tolerated failure is the node's answer and is not retried: n8n's Code
// node catches it itself.
func TestABatchFailureIsToleratedWithOneErrorItem(t *testing.T) {
	failure := &engine.BatchFailure{Err: errors.New("the batch broke")}
	for _, onError := range []string{"continueRegularOutput", "continueErrorOutput"} {
		runs := batchFailureRun(t, onError, false, failure)
		if len(runs) != 1 {
			t.Fatalf("%s: %d rows for Batch, want one untried-again row", onError, len(runs))
		}
		run := runs[0]
		port := map[string]int{"continueRegularOutput": 0, "continueErrorOutput": 1}[onError]
		if len(run.Output) <= port {
			t.Fatalf("%s: output %#v has no port %d", onError, run.Output, port)
		}
		items := run.Output[port]
		if len(items) != 1 {
			t.Fatalf("%s: port %d holds %d items, want one error item for the whole batch", onError, port, len(items))
		}
		if len(items[0].JSON) != 1 {
			t.Errorf("%s: the error item = %#v, want the error alone", onError, items[0].JSON)
		}
		descriptor, ok := items[0].JSON[engine.ErrorItemKey].(map[string]any)
		if !ok || descriptor["message"] != "the batch broke" || descriptor["node"] != "Batch" {
			t.Errorf("%s: the error item = %#v, want the node's error descriptor", onError, items[0].JSON)
		}
		for other := range run.Output {
			if other != port && len(run.Output[other]) != 0 {
				t.Errorf("%s: port %d = %#v, want it empty", onError, other, run.Output[other])
			}
		}
		if run.ErrorCode != "node.failed" || !errors.Is(run.Error, failure.Err) {
			t.Errorf("%s: row error %v (%s), want the batch's failure as node.failed", onError, run.Error, run.ErrorCode)
		}
	}
}

// A batch that ran out of the node's own time is tolerated like any batch
// failure, and its row still says it was a timeout.
func TestAToleratedBatchTimeoutStillReadsAsATimeout(t *testing.T) {
	definition := stepType("test.batch", "Batch")
	definition.WholeBatch = true
	catalog := testCatalog(t, startType("test.start", "Start"), definition)
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_batch_timeout", Name: "Batch timeout",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "batch", Name: "Batch", Type: "test.batch", TypeVersion: workflow.V(1), Settings: map[string]any{
				"onError": "continueRegularOutput", "timeoutSeconds": 0.01}},
		},
		Connections: []workflow.Connection{mainEdge("c1", "start", "main", "batch")},
		Settings:    map[string]any{},
	})
	executors := threeItemStart(t, "test.batch", func(ctx context.Context, _ workflow.IRNode, _ workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
		<-ctx.Done()
		return nil, &engine.BatchFailure{Err: ctx.Err()}
	})
	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	runs := nodeRuns(result, "batch")
	if len(runs) != 1 || runs[0].ErrorCode != "node.timeout" || len(runs[0].Output[0]) != 1 {
		t.Fatalf("rows = %#v, want one node.timeout row with one error item", runs)
	}
}

// A node whose definition says its error items carry the message alone —
// n8n's Code node writes `error` as a string — gets that shape both from a
// batch failure and from the one-per-item rule, and keeps the input item's
// fields beside it on the error output as before.
func TestANodeThatWritesItsErrorAsAMessageGetsAString(t *testing.T) {
	runs := batchFailureRun(t, "continueRegularOutput", true, &engine.BatchFailure{Err: errors.New("the batch broke")})
	if run := runs[len(runs)-1]; len(run.Output[0]) != 1 || run.Output[0][0].JSON[engine.ErrorItemKey] != "the batch broke" {
		t.Errorf("batch failure: main = %s, want one item whose error is the message", fmt.Sprint(run.Output[0]))
	}
	// A plain failure is still retried before it is tolerated; the last
	// attempt's row holds the error items.
	runs = batchFailureRun(t, "continueErrorOutput", true, errors.New("every item broke"))
	run := runs[len(runs)-1]
	if len(runs) != 3 || len(run.Output) < 2 || len(run.Output[1]) != 3 {
		t.Fatalf("per item: output %#v, want three error items on the error output", run.Output)
	}
	for index, item := range run.Output[1] {
		if item.JSON[engine.ErrorItemKey] != "every item broke" || item.JSON["n"] != float64(index) {
			t.Errorf("per item: error item %d = %#v, want the message beside the input item's fields", index, item.JSON)
		}
	}
}
