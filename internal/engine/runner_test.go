package engine_test

import (
	"context"
	"strings"
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
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: workflow.V(1), Parameters: map[string]any{"assignments": map[string]any{"status": "ready"}}},
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
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
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
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "if", Name: "IF", Type: "kilasflow.if", TypeVersion: workflow.V(1), Parameters: map[string]any{"conditions": []any{map[string]any{"field": "customer.tier", "operator": "equals", "value": "vip"}}}},
			{ID: "true-set", Name: "VIP Set", Type: "kilasflow.set", TypeVersion: workflow.V(1), Parameters: map[string]any{"assignments": map[string]any{"route": "vip"}}},
			{ID: "false-set", Name: "Default Set", Type: "kilasflow.set", TypeVersion: workflow.V(1), Parameters: map[string]any{"assignments": map[string]any{"route": "default"}}},
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
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
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
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "left", Name: "Left", Type: "kilasflow.set", TypeVersion: workflow.V(1), Parameters: map[string]any{"assignments": map[string]any{"source": "left"}}},
			{ID: "right", Name: "Right", Type: "kilasflow.set", TypeVersion: workflow.V(1), Parameters: map[string]any{"assignments": map[string]any{"source": "right"}}},
			{ID: "merge", Name: "Merge", Type: "kilasflow.merge", TypeVersion: workflow.V(1), Parameters: map[string]any{"mode": "append"}},
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
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
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

// multiTriggerIR is a webhook and a schedule that both feed one shared node,
// each with a step of its own downstream. It is the shape this ticket exists
// for: live traffic on one trigger, a nightly catch-up on the other.
func multiTriggerIR(t *testing.T) workflow.IR {
	t.Helper()
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	ir, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_multi",
		Name:          "Webhook and schedule",
		Nodes: []workflow.Node{
			{ID: "hook", Name: "Webhook", Type: "kilasflow.webhook", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"path": "orders", "httpMethod": "POST"}},
			{ID: "cron", Name: "Schedule", Type: "kilasflow.schedule", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"cron": "0 3 * * *"}},
			{ID: "hook-only", Name: "Hook only", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"via": "webhook"}}},
			{ID: "cron-only", Name: "Cron only", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"via": "schedule"}}},
			{ID: "shared", Name: "Shared", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"seen": "yes"}}},
		},
		Connections: []workflow.Connection{
			{ID: "e1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "hook", Port: "main"},
				Target: workflow.Endpoint{NodeID: "hook-only", Port: "main"}},
			{ID: "e2", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "cron", Port: "main"},
				Target: workflow.Endpoint{NodeID: "cron-only", Port: "main"}},
			{ID: "e3", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "hook", Port: "main"},
				Target: workflow.Endpoint{NodeID: "shared", Port: "main"}},
			{ID: "e4", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "cron", Port: "main"},
				Target: workflow.Endpoint{NodeID: "shared", Port: "main"}},
		},
		Settings: map[string]any{},
	}, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	return ir
}

func executorRegistry(t *testing.T) *engine.Registry {
	t.Helper()
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	return executors
}

// TestRunStartsFromTheNamedTriggerOnly is the property that had to land with
// the compiler change. Allowing several roots without deciding what happens at
// run time would produce documents that save and activate and then execute the
// wrong thing — every root seeded with the same item, so a schedule trigger
// firing on a webhook delivery.
func TestRunStartsFromTheNamedTriggerOnly(t *testing.T) {
	ir := multiTriggerIR(t)

	result, err := engine.NewRunner(executorRegistry(t)).Run(context.Background(), ir, engine.Request{
		Input:         workflow.Item{JSON: map[string]any{"order": "A-1"}},
		TriggerNodeID: "hook",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	ran := map[string]int{}
	for _, run := range result.NodeRuns {
		ran[run.NodeID]++
	}
	for _, want := range []string{"hook", "hook-only", "shared"} {
		if ran[want] != 1 {
			t.Errorf("node %q ran %d times, want once", want, ran[want])
		}
	}
	// The other trigger's executor is not invoked and its exclusive downstream
	// node does not run.
	for _, unwanted := range []string{"cron", "cron-only"} {
		if ran[unwanted] != 0 {
			t.Errorf("node %q ran %d times on a webhook execution, want never", unwanted, ran[unwanted])
		}
	}

	// The shared node ran once, fed only by the trigger that fired.
	for _, run := range result.NodeRuns {
		if run.NodeID != "shared" {
			continue
		}
		items := run.Input["main"]
		if len(items) != 1 {
			t.Fatalf("shared node received %d items, want 1 from the firing trigger alone", len(items))
		}
		if items[0].JSON["order"] != "A-1" {
			t.Errorf("shared node input = %#v, want the webhook's item", items[0].JSON)
		}
	}
}

// TestRunFromTheOtherTriggerIsTheMirrorImage proves the selection is real
// rather than an ordering accident.
func TestRunFromTheOtherTriggerIsTheMirrorImage(t *testing.T) {
	ir := multiTriggerIR(t)

	result, err := engine.NewRunner(executorRegistry(t)).Run(context.Background(), ir, engine.Request{
		Input:         workflow.Item{JSON: map[string]any{"nightly": true}},
		TriggerNodeID: "cron",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	ran := map[string]int{}
	for _, run := range result.NodeRuns {
		ran[run.NodeID]++
	}
	for _, want := range []string{"cron", "cron-only", "shared"} {
		if ran[want] != 1 {
			t.Errorf("node %q ran %d times, want once", want, ran[want])
		}
	}
	for _, unwanted := range []string{"hook", "hook-only"} {
		if ran[unwanted] != 0 {
			t.Errorf("node %q ran %d times on a schedule execution, want never", unwanted, ran[unwanted])
		}
	}
}

// TestRunWithNoNamedTriggerRunsEveryRoot is what a manual run means, and what
// keeps a single-root graph behaving exactly as it did before.
func TestRunWithNoNamedTriggerRunsEveryRoot(t *testing.T) {
	ir := multiTriggerIR(t)

	result, err := engine.NewRunner(executorRegistry(t)).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{"manual": true}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	ran := map[string]int{}
	for _, run := range result.NodeRuns {
		ran[run.NodeID]++
	}
	for _, want := range []string{"hook", "cron", "hook-only", "cron-only", "shared"} {
		if ran[want] != 1 {
			t.Errorf("node %q ran %d times on a manual run, want once", want, ran[want])
		}
	}
}

// TestRunRejectsATriggerThatIsNotInTheWorkflow refuses to silently run
// everything when the named node is wrong, which would look like success.
func TestRunRejectsATriggerThatIsNotInTheWorkflow(t *testing.T) {
	ir := multiTriggerIR(t)

	_, err := engine.NewRunner(executorRegistry(t)).Run(context.Background(), ir, engine.Request{
		Input:         workflow.Item{JSON: map[string]any{}},
		TriggerNodeID: "no-such-node",
	})
	if err == nil {
		t.Fatal("Run() accepted a trigger node that is not in the workflow")
	}
	if !strings.Contains(err.Error(), "no-such-node") {
		t.Errorf("error = %v, want it to name the missing trigger", err)
	}
}
