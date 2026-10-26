package nodes_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/runcode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// batchShapedNodes are the native nodes whose answer depends on the whole batch
// they are handed, or that act once per batch. Each must be WholeBatch: under
// continue-on-fail the runner otherwise splits a node into one-item calls, and
// each of these then computes something plausible and wrong for every item.
var batchShapedNodes = map[string]string{
	nodes.AggregateNodeType:          "folds every item into one",
	nodes.SummarizeNodeType:          "groups and counts across items",
	nodes.LimitNodeType:              "keeps the first or last N of the batch",
	nodes.RemoveDuplicatesNodeType:   "compares items with each other",
	nodes.SortNodeType:               "orders the batch",
	nodes.JSCodeNodeType:             "the body sees $input.all()",
	nodes.ForeignCodeNodeType:        "the body sees the whole batch",
	nodes.RespondNodeType:            "answers the caller once, with the first item or all of them",
	nodes.CodeNodeType:               "runs once for all items by default",
	nodes.ExecuteWorkflowNodeType:    "calls the workflow once with all items by default",
	nodes.GoogleSheetsNodeType:       "reads the sheet once and writes the batch in one request, and fails as one operation",
	nodes.ChainSummarizationNodeType: "answers once for the whole batch in its together mode",
}

// perItemNodes are the native nodes an item is the unit of work for: one item
// in, its own result out, so a tolerated failure is resolved item by item.
var perItemNodes = map[string]bool{
	"kilasflow.calculator": true, nodes.DatastoreNodeType: true, nodes.DateTimeNodeType: true,
	nodes.EmbeddingsNodeType: true, nodes.ExtractFromFileNodeType: true,
	nodes.FilterNodeType: true, nodes.GmailNodeType: true, nodes.GoogleDriveNodeType: true, "kilasflow.httpRequest": true,
	"kilasflow.if": true, nodes.MySQLNodeType: true, nodes.NoOpNodeType: true, nodes.PostgresNodeType: true,
	"kilasflow.set": true, nodes.SplitOutNodeType: true, nodes.SQLiteNodeType: true, nodes.StopAndErrorNodeType: true,
	"kilasflow.switch": true, nodes.VectorStorePGVectorNodeType: true, nodes.WaitNodeType: true,
	nodes.AgentNodeType: true, nodes.ChainNodeType: true, nodes.DocumentLoaderNodeType: true,
	nodes.OpenAIAppNodeType: true, nodes.GoogleGeminiAppNodeType: true,
	nodes.InformationExtractorNodeType: true, nodes.TextClassifierNodeType: true,
	nodes.ChainRetrievalQaNodeType: true, nodes.SentimentAnalysisNodeType: true,
}

// Every native node the runner could split into one-item calls is classified,
// and every batch-shaped one is WholeBatch.
//
// The classification is the test: a new node with one item input fails here
// until its author says which kind it is, so a batch node cannot forget the
// flag the way Aggregate, Summarize, Limit and Remove Duplicates did.
func TestEveryBatchShapedNodeIsWholeBatch(t *testing.T) {
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	for _, definition := range registry.List() {
		if !splittable(definition) {
			continue
		}
		if reason, batch := batchShapedNodes[definition.Type]; batch {
			if !definition.WholeBatch {
				t.Errorf("%s v%s is not WholeBatch, but it %s: under continue-on-fail it would run one item at a time",
					definition.Type, definition.Version, reason)
			}
			continue
		}
		if !perItemNodes[definition.Type] {
			t.Errorf("%s v%s has one item input and is not classified: add it to batchShapedNodes (and set WholeBatch) "+
				"or to perItemNodes", definition.Type, definition.Version)
		}
	}
	for nodeType := range batchShapedNodes {
		if !registry.HasType(nodeType) {
			t.Errorf("batchShapedNodes names %s, which is not registered", nodeType)
		}
	}
}

// A whole-batch node with a per-item mode still continues past a failed item
// in that mode: it reports each item's outcome, as the JavaScript Code node
// does, and the runner routes the failed ones. Without tolerance it still
// stops at the first failure.
func TestPerItemModesOfWholeBatchNodesReportEachItem(t *testing.T) {
	items := workflow.NodeInput{"main": {
		{JSON: map[string]any{"n": float64(1)}},
		{JSON: map[string]any{"n": float64(2)}},
		{JSON: map[string]any{"n": float64(3)}},
	}}
	check := func(t *testing.T, label string, run func(engine.Request) (workflow.NodeOutput, error)) {
		t.Helper()
		_, err := run(engine.Request{TolerateItemFailures: true})
		var outcomes engine.ItemOutcomes
		if !errors.As(err, &outcomes) {
			t.Fatalf("%s: tolerated error = %v, want each item's outcome", label, err)
		}
		if len(outcomes) != 3 || outcomes[0].Err != nil || outcomes[1].Err == nil || outcomes[2].Err != nil {
			t.Fatalf("%s: outcomes = %+v, want the second item failed and the others through", label, outcomes)
		}
		if len(outcomes[2].Items) != 1 || outcomes[2].Items[0].JSON["n"] != float64(3) {
			t.Errorf("%s: third outcome = %+v, want the item after the failure run", label, outcomes[2].Items)
		}
		_, err = run(engine.Request{})
		if err == nil || errors.As(err, &outcomes) {
			t.Errorf("%s: untolerated error = %v, want the first failure alone", label, err)
		}
	}

	t.Run("execute workflow", func(t *testing.T) {
		executor := flowExecutor(t, nodes.ExecuteWorkflowExecutorID)
		ir := flowNode(t, nodes.ExecuteWorkflowNodeType, map[string]any{
			"workflowId": "wf_child", "itemsPerCall": "eachItem"})
		check(t, "execute workflow", func(request engine.Request) (workflow.NodeOutput, error) {
			request.Workflows = failOnTwo{}
			return executor.Execute(context.Background(), ir, items, request)
		})
	})
	t.Run("go code", func(t *testing.T) {
		compiler := toolchainOrSkip(t)
		executor := nodes.NewCodeExecutor(compiler, runcode.NewMemoryCache(), runcode.DefaultLimits())
		ir := codeIR(t, map[string]any{"mode": nodes.CodeModeEachItem, "code": `
	if items[0].JSON["n"].(float64) == 2 {
		return nil, errors.New("two is refused")
	}
	return items, nil
`})
		check(t, "go code", func(request engine.Request) (workflow.NodeOutput, error) {
			return executor.Execute(context.Background(), ir, items, request)
		})
	})
}

// failOnTwo is a called workflow that refuses the item whose n is 2 and hands
// every other item back.
type failOnTwo struct{}

func (failOnTwo) InvokeWorkflow(_ context.Context, _ engine.ExecutionContext, call engine.WorkflowCall) (engine.WorkflowCallResult, error) {
	for _, item := range call.Items {
		if item.JSON["n"] == float64(2) {
			return engine.WorkflowCallResult{}, errors.New("two is refused")
		}
	}
	return engine.WorkflowCallResult{Items: call.Items}, nil
}

// splittable mirrors the runner's perItemTolerance: one item input and not a
// loop entry. Execute Once is a setting, not a property of the type.
func splittable(definition node.Definition) bool {
	if definition.LoopEntry || definition.Type == nodes.UnsupportedNodeType {
		return false
	}
	ports := 0
	for _, port := range definition.Inputs {
		if port.Kind == workflow.ConnectionMain {
			ports++
		}
	}
	return ports == 1
}
