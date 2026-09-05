package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

// TestUntakenBranchNeverInvokesItsExecutors is the bug this ticket exists for.
//
// Scheduling used to ask only whether upstream nodes had *completed*, and every
// executor reads an empty main port as "run once against an empty $json". So an
// IF whose false arm matched nothing still fired one HTTP request, one SQL
// statement and one webhook response down the arm nobody took — a cost and
// security problem as much as a correctness one.
//
// The proof is that the run succeeds. Every node on the untaken arm is a real
// executor configured so that invoking it *fails*: the HTTP policy allows no
// host, and the SQL node is given a credential that does not resolve. If either
// were reached, Run would return an error rather than a result.
func TestUntakenBranchNeverInvokesItsExecutors(t *testing.T) {
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	ir, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_branch",
		Name:          "Branch with side effects",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "if", Name: "IF", Type: "kilasflow.if", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"conditions": []any{map[string]any{
					"field": "tier", "operator": "equals", "value": "vip",
				}}}},
			{ID: "taken", Name: "Taken", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"arm": "false"}}},
			// The untaken arm, carrying the node types that cost money or reach
			// a third party.
			{ID: "http", Name: "HTTP", Type: "kilasflow.httpRequest", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"url": "https://example.test/never", "method": "GET"}},
			{ID: "respond", Name: "Respond", Type: "kilasflow.respondToWebhook", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"responseCode": float64(200), "responseBody": "never"}},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
				Target: workflow.Endpoint{NodeID: "if", Port: "main"}},
			// tier is not vip, so the false arm is the one taken.
			{ID: "c2", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "if", Port: "false"},
				Target: workflow.Endpoint{NodeID: "taken", Port: "main"}},
			{ID: "c3", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "if", Port: "true"},
				Target: workflow.Endpoint{NodeID: "http", Port: "main"}},
			{ID: "c4", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "http", Port: "main"},
				Target: workflow.Endpoint{NodeID: "respond", Port: "main"}},
		},
		Settings: map[string]any{},
	}, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	// No host is allowed, so any HTTP call at all fails the execution.
	executors := engine.NewRegistry()
	offline := safehttp.Policy{AllowedHosts: []string{"pruned.invalid"}, Timeout: time.Second}
	if err := nodes.RegisterExecutors(executors, offline, sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{"tier": "standard"}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v — a node on the untaken arm was invoked", err)
	}

	byNode := map[string]engine.NodeRun{}
	for _, run := range result.NodeRuns {
		byNode[run.NodeID] = run
	}

	// The taken arm ran.
	if run, present := byNode["taken"]; !present || run.Skipped {
		t.Errorf("the taken arm did not run: %#v", run)
	}
	// The untaken arm is recorded, and recorded as skipped — not absent, which
	// would make the branch look as though it never existed, and not succeeded,
	// which would make it look as though it ran and produced nothing.
	for _, nodeID := range []string{"http", "respond"} {
		run, present := byNode[nodeID]
		if !present {
			t.Errorf("node %q vanished from the trace instead of being recorded as skipped", nodeID)
			continue
		}
		if !run.Skipped {
			t.Errorf("node %q was not marked skipped", nodeID)
		}
		if run.Error != nil {
			t.Errorf("node %q recorded an error: %v", nodeID, run.Error)
		}
	}
}

// TestSkippingCascadesThroughTheWholeArm proves a skipped node yields empty
// streams on every output port, so the same rule prunes everything below it
// without a second traversal.
func TestSkippingCascadesThroughTheWholeArm(t *testing.T) {
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_cascade",
		Name:          "Long untaken arm",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "if", Name: "IF", Type: "kilasflow.if", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"conditions": []any{map[string]any{
					"field": "tier", "operator": "equals", "value": "vip",
				}}}},
			{ID: "taken", Name: "Taken", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"arm": "false"}}},
			{ID: "a", Name: "A", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"step": "a"}}},
			{ID: "b", Name: "B", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"step": "b"}}},
			{ID: "c", Name: "C", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"step": "c"}}},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
				Target: workflow.Endpoint{NodeID: "if", Port: "main"}},
			{ID: "c2", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "if", Port: "false"},
				Target: workflow.Endpoint{NodeID: "taken", Port: "main"}},
			{ID: "c3", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "if", Port: "true"},
				Target: workflow.Endpoint{NodeID: "a", Port: "main"}},
			{ID: "c4", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "a", Port: "main"},
				Target: workflow.Endpoint{NodeID: "b", Port: "main"}},
			{ID: "c5", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "b", Port: "main"},
				Target: workflow.Endpoint{NodeID: "c", Port: "main"}},
		},
		Settings: map[string]any{},
	}
	ir, err := workflow.Compile(document, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	result, err := engine.NewRunner(executorRegistry(t)).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{"tier": "standard"}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	byNode := map[string]engine.NodeRun{}
	for _, run := range result.NodeRuns {
		byNode[run.NodeID] = run
	}
	for _, nodeID := range []string{"a", "b", "c"} {
		run, present := byNode[nodeID]
		if !present || !run.Skipped {
			t.Errorf("node %q was not skipped; the cascade stopped short: %#v", nodeID, run)
		}
		for index, port := range run.Output {
			if len(port) != 0 {
				t.Errorf("skipped node %q emitted %d items on port %d, want none", nodeID, len(port), index)
			}
		}
	}
	if run := byNode["taken"]; run.Skipped {
		t.Error("the taken arm was pruned")
	}
}

// TestATriggerAndAnAgentAreNeverPruned keeps the rule from going too far. A node
// with no declared main input runs unconditionally, and typed attachment edges
// must never gate scheduling — an agent with no memory is a valid agent, and
// gating on those would skip every agent in the product.
func TestATriggerAndAnAgentAreNeverPruned(t *testing.T) {
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	ir, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_agent",
		Name:          "Agent with a model",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "model", Name: "Model", Type: "kilasflow.chatModel", TypeVersion: workflow.V(1),
				Parameters:  map[string]any{"provider": "openai", "model": "gpt-4o-mini"},
				Credentials: map[string]string{"httpBearerAuth": "cred_x"}},
			{ID: "agent", Name: "Agent", Type: "kilasflow.agent", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"prompt": "hello"}},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
				Target: workflow.Endpoint{NodeID: "agent", Port: "main"}},
			{ID: "c2", Kind: workflow.ConnectionLanguageModel,
				Source: workflow.Endpoint{NodeID: "model", Port: "model"},
				Target: workflow.Endpoint{NodeID: "agent", Port: "model"}},
		},
		Settings: map[string]any{},
	}, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	result, _ := engine.NewRunner(executorRegistry(t)).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{"ask": "hello"}},
	})
	for _, run := range result.NodeRuns {
		// The model has no main input at all, and the agent's only item edge
		// carries the trigger's item — neither may be pruned.
		if run.Skipped {
			t.Errorf("node %q was pruned; a sub-node and an agent must always run", run.NodeID)
		}
	}
}

// flakyExecutor fails a set number of times, then succeeds. It records every
// call so a test can prove the attempt count directly.
type flakyExecutor struct {
	failures *int
	calls    *int
}

func (executor flakyExecutor) Execute(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
	*executor.calls++
	if *executor.failures > 0 {
		*executor.failures--
		return nil, errors.New("upstream returned 500")
	}
	return workflow.NodeOutput{{{JSON: map[string]any{"ok": true}}}}, nil
}

// retryIR is a manual trigger feeding one node bound to a test executor, with
// whatever settings the case needs.
func retryIR(t *testing.T, settings map[string]any) workflow.IR {
	t.Helper()
	catalog := node.NewRegistry()
	if err := catalog.Register(node.Definition{
		Type: "test.trigger", Version: workflow.V(1), DisplayName: "Trigger", Category: "Test",
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.trigger",
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := catalog.Register(node.Definition{
		Type: "test.flaky", Version: workflow.V(1), DisplayName: "Flaky", Category: "Test",
		Inputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs:    []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		ExecutorID: "test.flaky",
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := catalog.Register(node.Definition{
		Type: "test.after", Version: workflow.V(1), DisplayName: "After", Category: "Test",
		Inputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs:    []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		ExecutorID: "test.after",
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	ir, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_retry",
		Name:          "Retrying",
		Nodes: []workflow.Node{
			{ID: "trigger", Name: "Trigger", Type: "test.trigger", TypeVersion: workflow.V(1)},
			{ID: "flaky", Name: "Flaky", Type: "test.flaky", TypeVersion: workflow.V(1), Settings: settings},
			{ID: "after", Name: "After", Type: "test.after", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "trigger", Port: "main"},
				Target: workflow.Endpoint{NodeID: "flaky", Port: "main"}},
			{ID: "c2", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "flaky", Port: "main"},
				Target: workflow.Endpoint{NodeID: "after", Port: "main"}},
		},
		Settings: map[string]any{},
	}, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	return ir
}

func retryExecutors(t *testing.T, failures, calls, afterCalls *int) *engine.Registry {
	t.Helper()
	registry := engine.NewRegistry()
	for id, executor := range map[string]engine.Executor{
		"test.trigger": engine.ExecutorFunc(func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{{JSON: map[string]any{"seed": 1}}, {JSON: map[string]any{"seed": 2}}}}, nil
		}),
		"test.flaky": flakyExecutor{failures: failures, calls: calls},
		"test.after": engine.ExecutorFunc(func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			*afterCalls++
			return workflow.NodeOutput{input["main"]}, nil
		}),
	} {
		if err := registry.Register(id, executor); err != nil {
			t.Fatalf("Register(%s) error = %v", id, err)
		}
	}
	return registry
}

// TestRetryOnFailAttemptsUpToMaxTriesAndStopsOnSuccess is the setting that
// visibly existed and did nothing: retryOnFail with maxTries 3 produced exactly
// one attempt.
func TestRetryOnFailAttemptsUpToMaxTriesAndStopsOnSuccess(t *testing.T) {
	failures, calls, afterCalls := 2, 0, 0
	ir := retryIR(t, map[string]any{
		"retryOnFail": true, "maxTries": float64(3), "waitBetweenTries": float64(0),
	})

	result, err := engine.NewRunner(retryExecutors(t, &failures, &calls, &afterCalls)).
		Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls != 3 {
		t.Errorf("executor was called %d times, want 3 — two failures then a success", calls)
	}

	// Every attempt is its own row, in attempt order.
	var attempts []int
	for _, run := range result.NodeRuns {
		if run.NodeID == "flaky" {
			attempts = append(attempts, run.Attempt)
		}
	}
	if len(attempts) != 3 {
		t.Fatalf("recorded %d attempts, want 3: %#v", len(attempts), attempts)
	}
	for index, attempt := range attempts {
		if attempt != index+1 {
			t.Errorf("attempt %d recorded as %d, want %d", index, attempt, index+1)
		}
	}
	// It stopped on the first success rather than using its whole budget.
	if last := result.NodeRuns[len(result.NodeRuns)-1]; last.Error != nil {
		t.Errorf("the run ended with an error: %v", last.Error)
	}
}

// TestRetryStopsEarlyOnSuccess proves the budget is a ceiling, not a schedule.
func TestRetryStopsEarlyOnSuccess(t *testing.T) {
	failures, calls, afterCalls := 0, 0, 0
	ir := retryIR(t, map[string]any{
		"retryOnFail": true, "maxTries": float64(8), "waitBetweenTries": float64(0),
	})

	if _, err := engine.NewRunner(retryExecutors(t, &failures, &calls, &afterCalls)).
		Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls != 1 {
		t.Errorf("executor was called %d times, want 1 — it succeeded first time", calls)
	}
}

// TestExhaustedRetriesWithoutContinueOnFailStillFailTheExecution keeps the
// existing behaviour where nothing asked for it to change.
func TestExhaustedRetriesWithoutContinueOnFailStillFailTheExecution(t *testing.T) {
	failures, calls, afterCalls := 99, 0, 0
	ir := retryIR(t, map[string]any{
		"retryOnFail": true, "maxTries": float64(3), "waitBetweenTries": float64(0),
	})

	_, err := engine.NewRunner(retryExecutors(t, &failures, &calls, &afterCalls)).
		Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err == nil {
		t.Fatal("Run() succeeded after every attempt failed")
	}
	if !strings.Contains(err.Error(), "upstream returned 500") {
		t.Errorf("error = %v, want the error from the last attempt", err)
	}
	if calls != 3 {
		t.Errorf("executor was called %d times, want the full budget of 3", calls)
	}
	if afterCalls != 0 {
		t.Errorf("the downstream node ran %d times after an unrecovered failure", afterCalls)
	}
}

// TestContinueOnFailKeepsTheRunGoingWithErrorItems is the setting a user ticks
// and expects to work. An HTTP node's first 500 used to abort the whole
// execution regardless.
func TestContinueOnFailKeepsTheRunGoingWithErrorItems(t *testing.T) {
	failures, calls, afterCalls := 99, 0, 0
	ir := retryIR(t, map[string]any{"continueOnFail": true})

	result, err := engine.NewRunner(retryExecutors(t, &failures, &calls, &afterCalls)).
		Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v, want the failure tolerated", err)
	}
	if afterCalls != 1 {
		t.Errorf("the downstream node ran %d times, want 1 — the run must continue", afterCalls)
	}

	var flaky engine.NodeRun
	for _, run := range result.NodeRuns {
		if run.NodeID == "flaky" {
			flaky = run
		}
	}
	if flaky.Error == nil {
		t.Error("the tolerated failure was not recorded as an error on its own run")
	}

	// One error item per input item, so downstream item counts survive: the
	// trigger emitted two items, so the tolerated failure emits two.
	items := flaky.Output[0]
	if len(items) != 2 {
		t.Fatalf("emitted %d error items, want one per input item (2)", len(items))
	}
	for index, item := range items {
		descriptor, ok := item.JSON[engine.ErrorItemKey].(map[string]any)
		if !ok {
			t.Fatalf("item %d = %#v, want an %s descriptor", index, item.JSON, engine.ErrorItemKey)
		}
		if descriptor["message"] != "upstream returned 500" {
			t.Errorf("item %d message = %#v, want the cause", index, descriptor["message"])
		}
		// Not the input passed through: a downstream node has to be able to
		// tell a tolerated failure from a success.
		if _, leaked := item.JSON["seed"]; leaked {
			t.Errorf("item %d carried the input through unchanged: %#v", index, item.JSON)
		}
	}
}

// TestRetryAndContinueOnFailComposeSoTheBudgetIsSpentFirst pins the interaction
// between the two settings, which is the case a user actually configures.
func TestRetryAndContinueOnFailComposeSoTheBudgetIsSpentFirst(t *testing.T) {
	failures, calls, afterCalls := 99, 0, 0
	ir := retryIR(t, map[string]any{
		"retryOnFail": true, "maxTries": float64(3), "waitBetweenTries": float64(0), "continueOnFail": true,
	})

	if _, err := engine.NewRunner(retryExecutors(t, &failures, &calls, &afterCalls)).
		Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}}); err != nil {
		t.Fatalf("Run() error = %v, want the exhausted failure tolerated", err)
	}
	if calls != 3 {
		t.Errorf("executor was called %d times, want the full budget before tolerating", calls)
	}
	if afterCalls != 1 {
		t.Errorf("the downstream node ran %d times, want 1", afterCalls)
	}
}

// TestLineageSurvivesAOneToOneChain is the property the ticket exists for.
//
// About a third of the expressions in the import corpus reach sideways with
// $('Node').item. The closest KilasFlow could offer was "the first item of a
// node", which is correct only when every node processed exactly one item and
// silently wrong otherwise — the worst failure mode an imported workflow can
// have.
func TestLineageSurvivesAOneToOneChain(t *testing.T) {
	catalog := node.NewRegistry()
	if err := catalog.Register(node.Definition{
		Type: "test.source", Version: workflow.V(1), DisplayName: "Source", Category: "Test",
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.source",
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	ir, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_lineage",
		Name:          "A to Set to B",
		Nodes: []workflow.Node{
			{ID: "a", Name: "A", Type: "test.source", TypeVersion: workflow.V(1)},
			{ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"seen": "yes"}}},
			{ID: "b", Name: "B", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"stage": "b"}}},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "a", Port: "main"},
				Target: workflow.Endpoint{NodeID: "set", Port: "main"}},
			{ID: "c2", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "set", Port: "main"},
				Target: workflow.Endpoint{NodeID: "b", Port: "main"}},
		},
		Settings: map[string]any{},
	}, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	executors := engine.NewRegistry()
	if err := executors.Register("test.source", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{
				{JSON: map[string]any{"n": 1}},
				{JSON: map[string]any{"n": 2}},
				{JSON: map[string]any{"n": 3}},
			}}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var bRun engine.NodeRun
	for _, run := range result.NodeRuns {
		if run.NodeID == "b" {
			bRun = run
		}
	}
	if len(bRun.Output) != 1 || len(bRun.Output[0]) != 3 {
		t.Fatalf("B produced %#v, want three items", bRun.Output)
	}

	// Item 2 of B must reach back to item 2 of A, not item 1.
	second := bRun.Output[0][1]
	if second.Paired == nil {
		t.Fatal("item 2 of B carries no provenance")
	}
	if second.Paired.Lost {
		t.Errorf("item 2 of B lost its lineage across a one-to-one chain: %#v", second.Paired)
	}
	if second.Paired.SourceNodeID != "a" {
		t.Errorf("item 2 of B descends from %q, want %q", second.Paired.SourceNodeID, "a")
	}
	if second.Paired.ItemIndex != 1 {
		t.Errorf("item 2 of B descends from item index %d, want 1 — resolving to the first item is the silent wrongness this exists to stop", second.Paired.ItemIndex)
	}

	// And every item's lineage is its own, not a shared first.
	for index, item := range bRun.Output[0] {
		if item.Paired.ItemIndex != index {
			t.Errorf("item %d of B descends from item %d", index, item.Paired.ItemIndex)
		}
	}
}

// TestLineageIsReportedLostRatherThanGuessed covers the other half. When the
// correspondence genuinely cannot be established, saying so is what lets a
// later lookup fail with a reason instead of returning a confident wrong
// answer.
func TestLineageIsReportedLostRatherThanGuessed(t *testing.T) {
	catalog := node.NewRegistry()
	if err := catalog.Register(node.Definition{
		Type: "test.fanout", Version: workflow.V(1), DisplayName: "Fan out", Category: "Test",
		Inputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs:    []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		ExecutorID: "test.fanout",
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	ir, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_lost",
		Name:          "Changed item count",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "fan", Name: "Fan", Type: "test.fanout", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "fan", Port: "main"},
		}},
		Settings: map[string]any{},
	}, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	executors := engine.NewRegistry()
	// One item in, three out: the correspondence is genuinely unknown.
	if err := executors.Register("test.fanout", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{
				{JSON: map[string]any{"part": 1}},
				{JSON: map[string]any{"part": 2}},
				{JSON: map[string]any{"part": 3}},
			}}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{"one": true}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, run := range result.NodeRuns {
		if run.NodeID != "fan" {
			continue
		}
		for index, item := range run.Output[0] {
			if item.Paired == nil || !item.Paired.Lost {
				t.Errorf("item %d claims a lineage it cannot have: %#v", index, item.Paired)
			}
		}
	}
}

// TestRunIndexIsRecordedPerRun proves the second dimension exists, and that it
// is not Attempt. A node inside a loop or a fan-out produces several distinct
// runs, and conflating the two would make a retry inside a loop
// unrepresentable.
func TestRunIndexIsRecordedPerRun(t *testing.T) {
	failures, calls, afterCalls := 1, 0, 0
	ir := retryIR(t, map[string]any{
		"retryOnFail": true, "maxTries": float64(3), "waitBetweenTries": float64(0),
	})

	result, err := engine.NewRunner(retryExecutors(t, &failures, &calls, &afterCalls)).
		Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// One retry: two attempts, both of the same run.
	var attempts []int
	var runIndexes []int
	for _, run := range result.NodeRuns {
		if run.NodeID != "flaky" {
			continue
		}
		attempts = append(attempts, run.Attempt)
		runIndexes = append(runIndexes, run.RunIndex)
	}
	if len(attempts) != 2 {
		t.Fatalf("recorded %d rows for the retried node, want 2", len(attempts))
	}
	if attempts[0] != 1 || attempts[1] != 2 {
		t.Errorf("attempts = %v, want 1 then 2", attempts)
	}
	for index, runIndex := range runIndexes {
		if runIndex != 0 {
			t.Errorf("row %d has run index %d; a retry is the same run, not a second one", index, runIndex)
		}
	}
}
