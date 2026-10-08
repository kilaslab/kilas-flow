package engine_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// countingWorkflow is a called workflow whose answer depends on its batch: one
// item counting what it was sent.
type countingWorkflow struct{}

func (countingWorkflow) InvokeWorkflow(_ context.Context, _ engine.ExecutionContext, call engine.WorkflowCall) (engine.WorkflowCallResult, error) {
	return engine.WorkflowCallResult{Items: []workflow.Item{{JSON: map[string]any{"received": float64(len(call.Items))}}}}, nil
}

// A batch-shaped node gives the same answer under continue-on-fail as without
// it, when nothing fails.
//
// Continue-on-fail made the runner split a node into one-item calls unless it
// was WholeBatch, so an Aggregate folded each item on its own, a Limit kept
// every item, a Remove Duplicates found no duplicates and a Summarize counted
// one per group per item: a plausible wrong answer, with no failure anywhere
// to say so. The real nodes run here, because the flag is theirs.
func TestBatchShapedNodesGiveTheSameAnswerUnderContinueOnFail(t *testing.T) {
	sandbox := newEngineSandbox(t, "kilasflow.db")
	if err := sandbox.catalog.Register(startType("test.items", "Items")); err != nil {
		t.Fatalf("Register(test.items) error = %v", err)
	}
	if err := sandbox.executors.Register("test.items", engine.ExecutorFunc(func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
		return workflow.NodeOutput{{
			{JSON: map[string]any{"name": "a", "score": float64(1)}},
			{JSON: map[string]any{"name": "b", "score": float64(2)}},
			{JSON: map[string]any{"name": "a", "score": float64(3)}},
			{JSON: map[string]any{"name": "c", "score": float64(4)}},
		}}, nil
	})); err != nil {
		t.Fatalf("Register(test.items executor) error = %v", err)
	}

	for _, test := range []struct {
		nodeType   string
		parameters map[string]any
	}{
		{nodes.AggregateNodeType, map[string]any{"fieldsToAggregate": "name"}},
		{nodes.AggregateNodeType, map[string]any{"aggregate": "aggregateAllItemData"}},
		{nodes.SummarizeNodeType, map[string]any{
			"fieldsToSummarize": []any{map[string]any{"aggregation": "sum", "field": "score"}}, "fieldsToSplitBy": "name"}},
		{nodes.LimitNodeType, map[string]any{"maxItems": float64(2)}},
		{nodes.LimitNodeType, map[string]any{"maxItems": float64(1), "keep": "lastItems"}},
		{nodes.RemoveDuplicatesNodeType, map[string]any{"compare": "selectedFields", "fieldsToCompare": "name"}},
		{nodes.RespondNodeType, map[string]any{"respondWith": "allIncomingItems"}},
		{nodes.ExecuteWorkflowNodeType, map[string]any{"workflowId": "wf_count"}},
	} {
		label := test.nodeType
		if encoded, err := json.Marshal(test.parameters); err == nil {
			label += " " + string(encoded)
		}
		run := func(settings map[string]any) (string, json.RawMessage) {
			t.Helper()
			ir := compileDoc(t, sandbox.catalog, workflow.Document{
				SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_batch_shape", Name: "Batch shape",
				Nodes: []workflow.Node{
					{ID: "items", Name: "Items", Type: "test.items", TypeVersion: workflow.V(1)},
					{ID: "batch", Name: "Batch", Type: test.nodeType, TypeVersion: workflow.V(1),
						Parameters: test.parameters, Settings: settings},
				},
				Connections: []workflow.Connection{mainEdge("c1", "items", "main", "batch")},
				Settings:    map[string]any{},
			})
			result, err := engine.NewRunner(sandbox.executors).Run(sandbox.ctx, ir, engine.Request{
				Input: workflow.Item{JSON: map[string]any{}}, Workflows: countingWorkflow{}})
			if err != nil {
				t.Fatalf("%s: Run(%v) error = %v", label, settings, err)
			}
			runs := nodeRuns(result, "batch")
			if len(runs) != 1 {
				t.Fatalf("%s: Batch ran %d times under %v, want once", label, len(runs), settings)
			}
			if runs[0].Error != nil {
				t.Fatalf("%s: Batch failed under %v: %v", label, settings, runs[0].Error)
			}
			var answer []map[string]any
			for _, item := range runs[0].Output[0] {
				answer = append(answer, item.JSON)
			}
			encoded, err := json.Marshal(answer)
			if err != nil {
				t.Fatalf("%s: encode output: %v", label, err)
			}
			return string(encoded), runs[0].Response
		}
		plain, plainResponse := run(map[string]any{})
		for _, onError := range []string{"continueRegularOutput", "continueErrorOutput"} {
			tolerant, tolerantResponse := run(map[string]any{"onError": onError})
			if tolerant != plain {
				t.Errorf("%s under %s = %s, want the answer without it %s", label, onError, tolerant, plain)
			}
			if string(tolerantResponse) != string(plainResponse) {
				t.Errorf("%s under %s responded %s, want %s", label, onError, tolerantResponse, plainResponse)
			}
		}
	}
}
