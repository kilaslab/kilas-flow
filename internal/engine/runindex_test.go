package engine_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// `$runIndex` counts the runs of the node being executed. It was always 0,
// because the runner numbered a run only after it completed; a node fed by
// two branches runs once per branch and must see 0, then 1.
func TestRunIndexReachesExpressions(t *testing.T) {
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"))
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_runindex", Name: "Two branches into one tail",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "a", Name: "A", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 200, Y: 0}},
			{ID: "b", Name: "B", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 200, Y: 300}},
			{ID: "tail", Name: "Tail", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 400, Y: 150}},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "a"),
			mainEdge("c2", "start", "main", "b"),
			mainEdge("c3", "a", "main", "tail"),
			mainEdge("c4", "b", "main", "tail"),
		},
		Settings: map[string]any{},
	})

	var seen []string
	executors := engine.NewRegistry()
	if err := executors.Register("test.start", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{{JSON: map[string]any{}}}}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := executors.Register("test.step", engine.ExecutorFunc(
		func(_ context.Context, node workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
			if node.ID == "tail" {
				value, err := expression.Evaluate("{{ $runIndex }}", request.ExpressionContext(input["main"][0], input, 0))
				if err != nil {
					return nil, err
				}
				seen = append(seen, fmt.Sprintf("%d:%v", request.RunIndex, value))
			}
			return workflow.NodeOutput{input["main"]}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	if _, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := fmt.Sprint(seen); got != "[0:0 1:1]" {
		t.Fatalf("the tail saw request run index:$runIndex %s, want [0:0 1:1]", got)
	}
}
