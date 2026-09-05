package engine_test

import (
	"context"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/ai"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

func TestRunnerExecutesManualSetAndRecordsItemFlow(t *testing.T) {
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	ir, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Manual customer status",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: 1},
			{ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: 1, Parameters: map[string]any{"assignments": map[string]any{"status": "ready"}}},
		},
		Connections: []workflow.Connection{{
			ID: "manual-set", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "set", Port: "main"},
		}},
		Settings: map[string]any{},
	}, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{"customer": "Ada"}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := len(result.NodeRuns), 2; got != want {
		t.Fatalf("node runs = %d, want %d", got, want)
	}
	if got, want := result.NodeRuns[0].NodeID, "manual"; got != want {
		t.Errorf("first node = %q, want %q", got, want)
	}
	if got, want := result.NodeRuns[1].Input["main"][0].JSON["customer"], "Ada"; got != want {
		t.Errorf("set input customer = %#v, want %#v", got, want)
	}
	if got, want := result.NodeRuns[1].Output[0][0].JSON["status"], "ready"; got != want {
		t.Errorf("set output status = %#v, want %#v", got, want)
	}
	if got, want := result.NodeRuns[1].Output[0][0].JSON["customer"], "Ada"; got != want {
		t.Errorf("set output customer = %#v, want %#v", got, want)
	}
}

func TestRunnerRoutesEachItemToTheMatchingIFOutput(t *testing.T) {
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	ir, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_020",
		Name:          "Route VIP customers",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: 1},
			{ID: "if", Name: "IF", Type: "kilasflow.if", TypeVersion: 1, Parameters: map[string]any{"conditions": []any{map[string]any{"field": "customer.tier", "operator": "equals", "value": "vip"}}}},
			{ID: "true-set", Name: "VIP Set", Type: "kilasflow.set", TypeVersion: 1, Parameters: map[string]any{"assignments": map[string]any{"route": "vip"}}},
			{ID: "false-set", Name: "Default Set", Type: "kilasflow.set", TypeVersion: 1, Parameters: map[string]any{"assignments": map[string]any{"route": "default"}}},
		},
		Connections: []workflow.Connection{
			{ID: "manual-if", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "manual", Port: "main"}, Target: workflow.Endpoint{NodeID: "if", Port: "main"}},
			{ID: "if-true", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "if", Port: "true"}, Target: workflow.Endpoint{NodeID: "true-set", Port: "main"}},
			{ID: "if-false", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "if", Port: "false"}, Target: workflow.Endpoint{NodeID: "false-set", Port: "main"}},
		},
		Settings: map[string]any{},
	}, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{"customer": map[string]any{"tier": "vip"}}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	ifRun := nodeRun(t, result, "if")
	if got, want := len(ifRun.Output[0]), 1; got != want {
		t.Errorf("true IF output length = %d, want %d", got, want)
	}
	if got, want := len(ifRun.Output[1]), 0; got != want {
		t.Errorf("false IF output length = %d, want %d", got, want)
	}
	if got, want := nodeRun(t, result, "true-set").Output[0][0].JSON["route"], "vip"; got != want {
		t.Errorf("true branch route = %#v, want %#v", got, want)
	}
	if got := len(nodeRun(t, result, "false-set").Output[0]); got != 0 {
		t.Errorf("false branch item count = %d, want 0", got)
	}
}

func TestRunnerMergesFanInByDeclaredInputPortOrder(t *testing.T) {
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	ir, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_021",
		Name:          "Merge sources",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: 1},
			{ID: "left", Name: "Left", Type: "kilasflow.set", TypeVersion: 1, Parameters: map[string]any{"assignments": map[string]any{"source": "left"}}},
			{ID: "right", Name: "Right", Type: "kilasflow.set", TypeVersion: 1, Parameters: map[string]any{"assignments": map[string]any{"source": "right"}}},
			{ID: "merge", Name: "Merge", Type: "kilasflow.merge", TypeVersion: 1, Parameters: map[string]any{"mode": "append"}},
		},
		Connections: []workflow.Connection{
			{ID: "manual-left", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "manual", Port: "main"}, Target: workflow.Endpoint{NodeID: "left", Port: "main"}},
			{ID: "manual-right", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "manual", Port: "main"}, Target: workflow.Endpoint{NodeID: "right", Port: "main"}},
			{ID: "left-merge", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "left", Port: "main"}, Target: workflow.Endpoint{NodeID: "merge", Port: "input1"}},
			{ID: "right-merge", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "right", Port: "main"}, Target: workflow.Endpoint{NodeID: "merge", Port: "input2"}},
		},
		Settings: map[string]any{},
	}, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{"id": "input"}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	merge := nodeRun(t, result, "merge")
	if got, want := len(merge.Output[0]), 2; got != want {
		t.Fatalf("merged item count = %d, want %d", got, want)
	}
	if got, want := merge.Output[0][0].JSON["source"], "left"; got != want {
		t.Errorf("first merged source = %#v, want %#v", got, want)
	}
	if got, want := merge.Output[0][1].JSON["source"], "right"; got != want {
		t.Errorf("second merged source = %#v, want %#v", got, want)
	}
}

func nodeRun(t *testing.T, result engine.Result, nodeID string) engine.NodeRun {
	t.Helper()
	for _, run := range result.NodeRuns {
		if run.NodeID == nodeID {
			return run
		}
	}
	t.Fatalf("node run %q was not recorded", nodeID)
	return engine.NodeRun{}
}
