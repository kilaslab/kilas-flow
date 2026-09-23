package engine_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
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

// copyItems snapshots the items a test executor was handed, so an assertion
// after the run reads what the executor actually received.
func copyItems(items []workflow.Item) []workflow.Item {
	copied := make([]workflow.Item, len(items))
	for index, item := range items {
		fields := make(map[string]any, len(item.JSON))
		for key, value := range item.JSON {
			fields[key] = value
		}
		copied[index] = workflow.Item{JSON: fields, Paired: item.Paired}
	}
	return copied
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
	for _, want := range []string{"hook", "cron", "hook-only", "cron-only"} {
		if ran[want] != 1 {
			t.Errorf("node %q ran %d times on a manual run, want once", want, ran[want])
		}
	}
	// The node both roots feed runs once per delivering branch, which is what
	// n8n v1 does: a manual run of a two-trigger workflow is two runs into the
	// shared tail, each with its own items, not one run over both streams.
	if ran["shared"] != 2 {
		t.Errorf("the shared node ran %d times on a manual run, want once per root branch (2)", ran["shared"])
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
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  "test.trigger", Version: workflow.V(1), DisplayName: "Trigger", Category: "Test",
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.trigger",
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := catalog.Register(node.Definition{
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  "test.flaky", Version: workflow.V(1), DisplayName: "Flaky", Category: "Test",
		Inputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs:    []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		ExecutorID: "test.flaky",
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := catalog.Register(node.Definition{
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  "test.after", Version: workflow.V(1), DisplayName: "After", Category: "Test",
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
	// A tolerated failure is resolved one item at a time — that is what keeps
	// the items after a failing one from being skipped — so the budget is spent
	// per item: two items from the trigger, three attempts each.
	if calls != 6 {
		t.Errorf("executor was called %d times, want the full budget of 3 spent on each of the 2 items", calls)
	}
	if afterCalls != 1 {
		t.Errorf("the downstream node ran %d times, want 1", afterCalls)
	}
}

// flagIR wires a trigger, a node carrying the settings under test, and a node
// after it, so a test can see what a setting changed for the branch below.
func flagIR(t *testing.T, settings map[string]any) workflow.IR {
	t.Helper()
	catalog := node.NewRegistry()
	for _, definition := range []node.Definition{
		{Group: []node.NodeGroup{node.GroupTransform}, Type: "test.source", Version: workflow.V(1),
			DisplayName: "Source", Category: "Test",
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.source"},
		{Group: []node.NodeGroup{node.GroupTransform}, Type: "test.flags", Version: workflow.V(1),
			DisplayName: "Flags", Category: "Test",
			Inputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs:    []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			ExecutorID: "test.flags"},
		{Group: []node.NodeGroup{node.GroupTransform}, Type: "test.after", Version: workflow.V(1),
			DisplayName: "After", Category: "Test",
			Inputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs:    []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			ExecutorID: "test.after"},
	} {
		if err := catalog.Register(definition); err != nil {
			t.Fatalf("Register(%s) error = %v", definition.Type, err)
		}
	}

	ir, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_flags",
		Name:          "Flagged",
		Nodes: []workflow.Node{
			{ID: "source", Name: "Source", Type: "test.source", TypeVersion: workflow.V(1)},
			{ID: "flags", Name: "Flags", Type: "test.flags", TypeVersion: workflow.V(1), Settings: settings},
			{ID: "after", Name: "After", Type: "test.after", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "source", Port: "main"},
				Target: workflow.Endpoint{NodeID: "flags", Port: "main"}},
			{ID: "c2", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "flags", Port: "main"},
				Target: workflow.Endpoint{NodeID: "after", Port: "main"}},
		},
		Settings: map[string]any{},
	}, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	return ir
}

// TestExecuteOnceRunsTheNodeOnceForTheWholeBatch is the half of the deferred
// pair that duplicates side effects when it is ignored.
//
// n8n runs such a node once, with the first item of its input, so a paid render
// or an LLM call happens once per run rather than once per item. The live repro
// that opened this ticket saw three POSTs where n8n makes one.
func TestExecuteOnceRunsTheNodeOnceForTheWholeBatch(t *testing.T) {
	ir := flagIR(t, map[string]any{"executeOnce": true})

	calls, itemsSeen := 0, 0
	executors := engine.NewRegistry()
	for id, executor := range map[string]engine.Executor{
		"test.source": engine.ExecutorFunc(func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{
				{JSON: map[string]any{"n": 1}},
				{JSON: map[string]any{"n": 2}},
				{JSON: map[string]any{"n": 3}},
			}}, nil
		}),
		"test.flags": engine.ExecutorFunc(func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			calls++
			itemsSeen += len(input["main"])
			return workflow.NodeOutput{input["main"]}, nil
		}),
		"test.after": engine.ExecutorFunc(func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{input["main"]}, nil
		}),
	} {
		if err := executors.Register(id, executor); err != nil {
			t.Fatalf("Register(%s) error = %v", id, err)
		}
	}

	if _, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{}},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls != 1 {
		t.Errorf("the node with executeOnce ran %d times for a 3-item batch, want once", calls)
	}
	if itemsSeen != 1 {
		t.Errorf("the node with executeOnce saw %d items, want the first one alone", itemsSeen)
	}
}

// TestAlwaysOutputDataKeepsTheBranchAliveWithOneEmptyItem is the other half: an
// "insert if missing" step that found nothing must still reach the branch that
// creates it.
//
// The second run is the control — the same graph without the setting — so the
// test proves the setting is what kept the branch alive rather than that the
// branch runs regardless.
func TestAlwaysOutputDataKeepsTheBranchAliveWithOneEmptyItem(t *testing.T) {
	afterCalls, itemsSeen := 0, 0
	executors := engine.NewRegistry()
	for id, executor := range map[string]engine.Executor{
		"test.source": engine.ExecutorFunc(func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{
				{JSON: map[string]any{"n": 1}},
				{JSON: map[string]any{"n": 2}},
			}}, nil
		}),
		// n8n's lookup-then-create node: nothing matched, so it passes nothing.
		"test.flags": engine.ExecutorFunc(func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{}}, nil
		}),
		"test.after": engine.ExecutorFunc(func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			afterCalls++
			itemsSeen += len(input["main"])
			return workflow.NodeOutput{input["main"]}, nil
		}),
	} {
		if err := executors.Register(id, executor); err != nil {
			t.Fatalf("Register(%s) error = %v", id, err)
		}
	}

	if _, err := engine.NewRunner(executors).Run(context.Background(), flagIR(t, map[string]any{"alwaysOutputData": true}), engine.Request{
		Input: workflow.Item{JSON: map[string]any{}},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if afterCalls != 1 || itemsSeen != 1 {
		t.Fatalf("the branch after the empty node ran %d times over %d items, want once with the single empty item n8n emits", afterCalls, itemsSeen)
	}

	// The control: without the setting the branch stops, which is exactly the
	// silent "not found" that never runs.
	if _, err := engine.NewRunner(executors).Run(context.Background(), flagIR(t, map[string]any{}), engine.Request{
		Input: workflow.Item{JSON: map[string]any{}},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if afterCalls != 1 {
		t.Errorf("the branch ran %d more times without alwaysOutputData, want it not to run at all", afterCalls-1)
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
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  "test.source", Version: workflow.V(1), DisplayName: "Source", Category: "Test",
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
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  "test.fanout", Version: workflow.V(1), DisplayName: "Fan out", Category: "Test",
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

// loopIR wires a loop node with a one-node body that feeds back into it, which
// is n8n's Split In Batches shape: the loop output runs the body and the body's
// last node returns to the loop node so the next batch is dispatched.
func loopIR(t *testing.T, parameters map[string]any) (workflow.IR, *node.Registry) {
	t.Helper()
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	ir, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_loop",
		Name:          "Batched",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "loop", Name: "Loop", Type: nodes.LoopNodeType, TypeVersion: workflow.V(1), Parameters: parameters},
			{ID: "body", Name: "Body", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"touched": "yes"}}},
			{ID: "after", Name: "After", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"stage": "after"}}},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
				Target: workflow.Endpoint{NodeID: "loop", Port: "main"}},
			{ID: "c2", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "loop", Port: "loop"},
				Target: workflow.Endpoint{NodeID: "body", Port: "main"}},
			// The back edge, legal only because the loop node declares itself
			// a loop entry.
			{ID: "c3", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "body", Port: "main"},
				Target: workflow.Endpoint{NodeID: "loop", Port: "main"}},
			{ID: "c4", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "loop", Port: "done"},
				Target: workflow.Endpoint{NodeID: "after", Port: "main"}},
		},
		Settings: map[string]any{},
	}, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	return ir, catalog
}

// TestLoopDispatchesOneBatchPerIteration is the pattern that was unrepresentable.
//
// n8n's Split In Batches is a cycle by construction, and refusing every cycle
// made every workflow built on it impossible to import.
func TestLoopDispatchesOneBatchPerIteration(t *testing.T) {
	ir, _ := loopIR(t, map[string]any{"batchSize": float64(2), "maxIterations": float64(10)})

	// Five items at two per batch: three iterations.
	result, err := engine.NewRunner(executorRegistry(t)).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{"n": 1}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	bodyRuns := 0
	loopRuns := 0
	afterRuns := 0
	for _, run := range result.NodeRuns {
		switch run.NodeID {
		case "body":
			if !run.Skipped {
				bodyRuns++
			}
		case "loop":
			loopRuns++
		case "after":
			if !run.Skipped {
				afterRuns++
			}
		}
	}
	// One item in, batch size two: one iteration, then a final call that emits
	// on done.
	if bodyRuns != 1 {
		t.Errorf("body ran %d times, want 1", bodyRuns)
	}
	if loopRuns < 2 {
		t.Errorf("loop ran %d times, want at least a dispatch and a finish", loopRuns)
	}
	// The nodes downstream of `done` run exactly once, however many iterations
	// the loop took.
	if afterRuns != 1 {
		t.Errorf("the node after the loop ran %d times, want exactly 1", afterRuns)
	}

	// Each iteration left its own row, distinguishable by run index.
	seen := map[int]bool{}
	for _, run := range result.NodeRuns {
		if run.NodeID == "loop" {
			if seen[run.RunIndex] {
				t.Errorf("two loop rows share run index %d", run.RunIndex)
			}
			seen[run.RunIndex] = true
		}
	}
}

// TestLoopCollectsEveryBatchOntoDone proves the accumulated items leave on
// `done` rather than only the last batch.
func TestLoopCollectsEveryBatchOntoDone(t *testing.T) {
	catalog := node.NewRegistry()
	if err := catalog.Register(node.Definition{
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  "test.five", Version: workflow.V(1), DisplayName: "Five", Category: "Test",
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.five",
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	ir, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_collect",
		Name:          "Collecting",
		Nodes: []workflow.Node{
			{ID: "src", Name: "Source", Type: "test.five", TypeVersion: workflow.V(1)},
			{ID: "loop", Name: "Loop", Type: nodes.LoopNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"batchSize": float64(2), "maxIterations": float64(10)}},
			{ID: "body", Name: "Body", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"touched": "yes"}}},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "src", Port: "main"},
				Target: workflow.Endpoint{NodeID: "loop", Port: "main"}},
			{ID: "c2", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "loop", Port: "loop"},
				Target: workflow.Endpoint{NodeID: "body", Port: "main"}},
			{ID: "c3", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "body", Port: "main"},
				Target: workflow.Endpoint{NodeID: "loop", Port: "main"}},
		},
		Settings: map[string]any{},
	}, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	executors := engine.NewRegistry()
	if err := executors.Register("test.five", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			items := make([]workflow.Item, 0, 5)
			for index := range 5 {
				items = append(items, workflow.Item{JSON: map[string]any{"n": float64(index)}})
			}
			return workflow.NodeOutput{items}, nil
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

	// Five items at two per batch is three iterations, and every item the body
	// returned must be on `done`.
	var last engine.NodeRun
	for _, run := range result.NodeRuns {
		if run.NodeID == "loop" {
			last = run
		}
	}
	done := last.Output[0]
	if len(done) != 5 {
		t.Fatalf("done carried %d items, want all 5 accumulated across batches", len(done))
	}
	for index, item := range done {
		if item.JSON["touched"] != "yes" {
			t.Errorf("item %d on done did not pass through the body: %#v", index, item.JSON)
		}
	}
}

// TestLoopFailsRatherThanTruncatingAtItsBound is why the bound fails instead of
// emitting `done`. A truncated loop that reported success would hand downstream
// nodes a partial result they cannot tell from a complete one.
func TestLoopFailsRatherThanTruncatingAtItsBound(t *testing.T) {
	catalog := node.NewRegistry()
	if err := catalog.Register(node.Definition{
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  "test.many", Version: workflow.V(1), DisplayName: "Many", Category: "Test",
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.many",
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	ir, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_bounded",
		Name:          "Bounded",
		Nodes: []workflow.Node{
			{ID: "src", Name: "Source", Type: "test.many", TypeVersion: workflow.V(1)},
			{ID: "loop", Name: "Loop", Type: nodes.LoopNodeType, TypeVersion: workflow.V(1),
				// Twenty items, one at a time, but only three iterations allowed.
				Parameters: map[string]any{"batchSize": float64(1), "maxIterations": float64(3)}},
			{ID: "body", Name: "Body", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"touched": "yes"}}},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "src", Port: "main"},
				Target: workflow.Endpoint{NodeID: "loop", Port: "main"}},
			{ID: "c2", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "loop", Port: "loop"},
				Target: workflow.Endpoint{NodeID: "body", Port: "main"}},
			{ID: "c3", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "body", Port: "main"},
				Target: workflow.Endpoint{NodeID: "loop", Port: "main"}},
		},
		Settings: map[string]any{},
	}, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	executors := engine.NewRegistry()
	if err := executors.Register("test.many", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			items := make([]workflow.Item, 0, 20)
			for index := range 20 {
				items = append(items, workflow.Item{JSON: map[string]any{"n": float64(index)}})
			}
			return workflow.NodeOutput{items}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{}},
	})
	if err == nil {
		t.Fatal("a loop past its bound must fail the execution, not silently emit done")
	}
	if !strings.Contains(err.Error(), "Loop") || !strings.Contains(err.Error(), "3") {
		t.Errorf("error = %v, want it to name the loop node and the bound", err)
	}
	// The iterations that did succeed are recorded rather than discarded.
	bodyRuns := 0
	for _, run := range result.NodeRuns {
		if run.NodeID == "body" && !run.Skipped && run.Error == nil {
			bodyRuns++
		}
	}
	if bodyRuns != 3 {
		t.Errorf("recorded %d successful body runs, want the 3 that completed before the bound", bodyRuns)
	}
}

// TestLoopKeepsItsCursorWhenTheBodyReturnsNewItems is the regression for a loop
// that restarted on its own body's output.
//
// The cursor used to travel on the first dispatched item. A body node that
// builds its output from scratch — HTTP Request, Code, an aggregate, a
// database query — drops it, and the loop then read the body's output as a
// fresh entry: the iteration counter restarted at 1 on every pass, the bound
// never tripped, and the loop called the body's API without limit. The state
// now lives in the runner, keyed by the loop node, where nothing the body does
// to an item can reach it.
func TestLoopKeepsItsCursorWhenTheBodyReturnsNewItems(t *testing.T) {
	catalog := node.NewRegistry()
	for _, definition := range []node.Definition{
		{
			Group: []node.NodeGroup{node.GroupTransform},
			Type:  "test.three", Version: workflow.V(1), DisplayName: "Three", Category: "Test",
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.three",
		},
		{
			Group: []node.NodeGroup{node.GroupTransform},
			Type:  "test.replacing", Version: workflow.V(1), DisplayName: "Replacing", Category: "Test",
			Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.replacing",
		},
	} {
		if err := catalog.Register(definition); err != nil {
			t.Fatalf("Register() error = %v", err)
		}
	}
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	ir, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_loop_state", Name: "Loop with a body that replaces its items",
		Nodes: []workflow.Node{
			{ID: "src", Name: "Source", Type: "test.three", TypeVersion: workflow.V(1)},
			{ID: "loop", Name: "Loop", Type: nodes.LoopNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"batchSize": float64(1), "maxIterations": float64(10)}},
			{ID: "body", Name: "Body", Type: "test.replacing", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "src", Port: "main"},
				Target: workflow.Endpoint{NodeID: "loop", Port: "main"}},
			{ID: "c2", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "loop", Port: "loop"},
				Target: workflow.Endpoint{NodeID: "body", Port: "main"}},
			{ID: "c3", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "body", Port: "main"},
				Target: workflow.Endpoint{NodeID: "loop", Port: "main"}},
		},
		Settings: map[string]any{},
	}, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	executors := engine.NewRegistry()
	if err := executors.Register("test.three", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			items := make([]workflow.Item, 0, 3)
			for index := range 3 {
				items = append(items, workflow.Item{JSON: map[string]any{"n": float64(index)}})
			}
			return workflow.NodeOutput{items}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	// The body answers with an item of its own, exactly as an HTTP node does:
	// nothing of the item it was handed survives.
	var bodyInputs [][]workflow.Item
	if err := executors.Register("test.replacing", engine.ExecutorFunc(
		func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			handed := copyItems(input["main"])
			bodyInputs = append(bodyInputs, handed)
			items := make([]workflow.Item, 0, len(handed))
			for _, item := range handed {
				items = append(items, workflow.Item{JSON: map[string]any{"from": "api", "asked": item.JSON["n"]}})
			}
			return workflow.NodeOutput{items}, nil
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

	// Three items at one per batch is three body calls, whatever the body
	// answers with.
	if len(bodyInputs) != 3 {
		t.Fatalf("body ran %d times, want one call per item", len(bodyInputs))
	}
	for index, handed := range bodyInputs {
		if len(handed) != 1 {
			t.Fatalf("batch %d carried %d items, want 1", index, len(handed))
		}
		// The loop must hand the body the upstream item untouched: bookkeeping
		// riding on it is what a body node destroys.
		if len(handed[0].JSON) != 1 || handed[0].JSON["n"] != float64(index) {
			t.Errorf("batch %d item = %#v, want the source item alone", index, handed[0].JSON)
		}
	}

	var last engine.NodeRun
	for _, run := range result.NodeRuns {
		if run.NodeID == "loop" {
			last = run
		}
	}
	done := last.Output[0]
	if len(done) != 3 {
		t.Fatalf("done carried %d items, want the 3 the body returned", len(done))
	}
	for index, item := range done {
		if item.JSON["from"] != "api" || item.JSON["asked"] != float64(index) {
			t.Errorf("done item %d = %#v, want what the body returned", index, item.JSON)
		}
	}
}

// TestLoopCarriesBinaryAndLineageThroughEveryBatch pins the other half of the
// loop's item fidelity.
//
// The finding was that binary data and pairedItem lineage survived the first
// batch and were lost after it, including on `done` — a loop that fetched a PDF
// and uploaded it in the body handed the second batch an item with no file. The
// loop now passes items through untouched, so every batch and the collected
// output carry both.
func TestLoopCarriesBinaryAndLineageThroughEveryBatch(t *testing.T) {
	catalog := node.NewRegistry()
	for _, definition := range []node.Definition{
		{Group: []node.NodeGroup{node.GroupInput}, Type: "test.files", Version: workflow.V(1),
			DisplayName: "Files", Category: "Test",
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.files"},
		{Group: []node.NodeGroup{node.GroupTransform}, Type: "test.upload", Version: workflow.V(1),
			DisplayName: "Upload", Category: "Test",
			Inputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs:    []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			ExecutorID: "test.upload"},
	} {
		if err := catalog.Register(definition); err != nil {
			t.Fatalf("Register(%s) error = %v", definition.Type, err)
		}
	}
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	ir, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_loop_binary", Name: "Binary through a loop",
		Nodes: []workflow.Node{
			{ID: "files", Name: "Files", Type: "test.files", TypeVersion: workflow.V(1)},
			{ID: "loop", Name: "Loop", Type: nodes.LoopNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"batchSize": float64(1), "maxIterations": float64(10)}},
			{ID: "upload", Name: "Upload", Type: "test.upload", TypeVersion: workflow.V(1)},
			{ID: "after", Name: "After", Type: "test.upload", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "files", Port: "main"},
				Target: workflow.Endpoint{NodeID: "loop", Port: "main"}},
			{ID: "c2", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "loop", Port: "loop"},
				Target: workflow.Endpoint{NodeID: "upload", Port: "main"}},
			{ID: "c3", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "upload", Port: "main"},
				Target: workflow.Endpoint{NodeID: "loop", Port: "main"}},
			{ID: "c4", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "loop", Port: "done"},
				Target: workflow.Endpoint{NodeID: "after", Port: "main"}},
		},
		Settings: map[string]any{},
	}, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	executors := engine.NewRegistry()
	if err := executors.Register("test.files", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			items := make([]workflow.Item, 0, 3)
			for index := range 3 {
				items = append(items, workflow.Item{
					JSON:   map[string]any{"n": float64(index)},
					Binary: map[string]workflow.BinaryRef{"data": {ID: fmt.Sprintf("file-%d", index), MediaType: "application/pdf"}},
				})
			}
			return workflow.NodeOutput{items}, nil
		})); err != nil {
		t.Fatalf("Register(files) error = %v", err)
	}
	// The body answers with what it was handed, the way an HTTP node that
	// uploads the file and returns the response item does.
	var batches []workflow.Item
	if err := executors.Register("test.upload", engine.ExecutorFunc(
		func(_ context.Context, node workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			// A shallow copy keeps the binary references the helper used by the
			// lineage tests deliberately drops.
			items := append([]workflow.Item(nil), input["main"]...)
			if node.ID == "upload" {
				batches = append(batches, items...)
			}
			return workflow.NodeOutput{items}, nil
		})); err != nil {
		t.Fatalf("Register(upload) error = %v", err)
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

	// Every batch, not just the first, is handed the file its item names.
	if len(batches) != 3 {
		t.Fatalf("the body saw %d items across batches, want one per source item", len(batches))
	}
	for index, item := range batches {
		reference, ok := item.Binary["data"]
		if !ok || reference.MediaType != "application/pdf" {
			t.Fatalf("batch %d reached the body without its binary data: %#v", index, item.Binary)
		}
		if want := fmt.Sprintf("file-%d", index); reference.ID != want {
			t.Errorf("batch %d carries %q, want the item's own %q", index, reference.ID, want)
		}
		if item.Paired == nil || item.Paired.SourceNodeID != "files" || item.Paired.ItemIndex != index {
			t.Errorf("batch %d lineage = %#v, want it to name source item %d", index, item.Paired, index)
		}
	}

	// And the collected output keeps both, which is where the second half of the
	// finding showed up.
	var done workflow.NodeOutput
	for _, run := range result.NodeRuns {
		if run.NodeID == "loop" {
			done = run.Output
		}
	}
	if len(done) == 0 || len(done[0]) != 3 {
		t.Fatalf("done carried %#v, want the three items the body returned", done)
	}
	for index, item := range done[0] {
		if _, ok := item.Binary["data"]; !ok {
			t.Errorf("done item %d lost its binary data: %#v", index, item.Binary)
		}
		if item.Paired == nil || item.Paired.ItemIndex != index {
			t.Errorf("done item %d lineage = %#v, want source item %d", index, item.Paired, index)
		}
	}
}

// TestDollarItemFollowsPairedLineagePerItem is the other half of the lineage
// contract: `$('X').item` has to resolve per item, not just exist.
//
// The shape is n8n's canonical one — Manual -> Split Out -> a chain of
// one-to-one and filtering nodes -> a node reading back with
// `$('Split Out').item`. Before this, `.item` only resolved when the referenced
// node produced exactly one item, so every reference to a node that produced
// several failed at run time: the single most common expression form in the
// import corpus, broken on any multi-item flow.
func TestDollarItemFollowsPairedLineagePerItem(t *testing.T) {
	catalog := node.NewRegistry()
	if err := catalog.Register(node.Definition{
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  "test.probe", Version: workflow.V(1), DisplayName: "Probe", Category: "Test",
		Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.probe",
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	passing := []any{map[string]any{"field": "seen", "operator": "equals", "value": "yes"}}
	ir, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_item_lineage", Name: "Read Split Out per item",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "split", Name: "Split Out", Type: nodes.SplitOutNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"fieldToSplitOut": "list"}},
			{ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"seen": "yes"}}},
			{ID: "if", Name: "IF", Type: "kilasflow.if", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"conditions": passing}},
			{ID: "filter", Name: "Filter", Type: nodes.FilterNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"conditions": passing}},
			{ID: "limit", Name: "Limit", Type: nodes.LimitNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"maxItems": float64(3)}},
			{ID: "probe", Name: "Probe", Type: "test.probe", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "manual", Port: "main"}, Target: workflow.Endpoint{NodeID: "split", Port: "main"}},
			{ID: "c2", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "split", Port: "main"}, Target: workflow.Endpoint{NodeID: "set", Port: "main"}},
			{ID: "c3", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "set", Port: "main"}, Target: workflow.Endpoint{NodeID: "if", Port: "main"}},
			{ID: "c4", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "if", Port: "true"}, Target: workflow.Endpoint{NodeID: "filter", Port: "main"}},
			{ID: "c5", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "filter", Port: "main"}, Target: workflow.Endpoint{NodeID: "limit", Port: "main"}},
			{ID: "c6", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "limit", Port: "main"}, Target: workflow.Endpoint{NodeID: "probe", Port: "main"}},
		},
		Settings: map[string]any{},
	}, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	executors := engine.NewRegistry()
	var seen []any
	if err := executors.Register("test.probe", engine.ExecutorFunc(
		func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
			items := make([]workflow.Item, 0, len(input["main"]))
			for index, item := range input["main"] {
				// The real path: ExpressionContext pairs every completed node's
				// items with this one, which is what a node's own executor does.
				resolved, err := expression.Resolve(map[string]any{
					"origin": map[string]any{"mode": "expression", "value": "{{ $('Split Out').item.json.v }}"},
				}, request.ExpressionContext(item, input, index))
				if err != nil {
					return nil, err
				}
				seen = append(seen, resolved["origin"])
				items = append(items, workflow.Item{JSON: map[string]any{"saw": resolved["origin"]}})
			}
			return workflow.NodeOutput{items}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	if _, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{"list": []any{
			map[string]any{"v": "a"}, map[string]any{"v": "b"}, map[string]any{"v": "c"},
		}}},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Item N of Split Out for item N of the chain, not the first item three
	// times.
	if len(seen) != 3 {
		t.Fatalf("probe resolved %d items, want 3", len(seen))
	}
	for index, want := range []string{"a", "b", "c"} {
		if seen[index] != want {
			t.Errorf("item %d read %#v from $('Split Out').item, want %#v", index, seen[index], want)
		}
	}
}

// TestDollarItemFailsRatherThanGuessingWhenLineageIsAmbiguous covers the
// refusal: when the correspondence into a fan-out cannot be established and
// the position is not an answer either, `.item` fails with a reason instead of
// returning a confident wrong item.
func TestDollarItemFailsRatherThanGuessingWhenLineageIsAmbiguous(t *testing.T) {
	items := map[string]expression.NodeItem{
		"Fan": {
			Items:       []map[string]any{{"part": 1}, {"part": 2}},
			ItemOrigins: []string{"", ""},
		},
	}
	current := workflow.Item{JSON: map[string]any{}, Paired: &workflow.PairedItem{SourceNodeID: "src", RunIndex: 0, ItemIndex: 0}}
	paired := engine.PairNodeItems(items, current, 7)
	if paired["Fan"].Paired != nil {
		t.Errorf("Fan paired to %#v on no evidence", paired["Fan"].Paired)
	}
	if paired["Fan"].LineageReason == "" {
		t.Error("a refused pairing must carry the reason it was refused")
	}
	if strings.Contains(paired["Fan"].LineageReason, "\x00") {
		t.Errorf("lineage reason leaks an internal root name: %q", paired["Fan"].LineageReason)
	}
}

// TestDollarNodeByNameReachesTheExecutor is the regression for a defect that
// made `$('Name')` — the form every imported n8n workflow uses to read an
// earlier node — resolve to "that node has not produced output in this run" no
// matter what had run.
//
// The runner filled `NodeItems` correctly and then handed each executor a clone
// that did not carry it. The evaluator was right and the data never reached it,
// which is why the expression package's own tests passed throughout.
func TestDollarNodeByNameReachesTheExecutor(t *testing.T) {
	t.Parallel()

	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	ir, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_dollar_node", Name: "Read an earlier node",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "first", Name: "First", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"colour": "green"}}},
			{ID: "second", Name: "Second", Type: "kilasflow.httpRequest", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"method": "GET", "url": "https://reader.invalid/x"}},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "manual", Port: "main"}, Target: workflow.Endpoint{NodeID: "first", Port: "main"}},
			{ID: "c2", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "first", Port: "main"}, Target: workflow.Endpoint{NodeID: "second", Port: "main"}},
		},
		Settings: map[string]any{},
	}, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	// A purpose-built executor in place of the HTTP node, so the assertion is
	// about what reaches an executor rather than about any node's own
	// behaviour.
	var seen map[string]any
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	reader := engine.NewRegistry()
	for _, id := range executors.Registered() {
		installed, _ := executors.Lookup(id)
		if id == "core.httpRequest" {
			installed = engine.ExecutorFunc(func(_ context.Context, node workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
				resolved, err := expression.Resolve(map[string]any{
					"fromEarlier":  map[string]any{"mode": "expression", "value": "{{ $('First').item.json.colour }}"},
					"fromTrigger":  map[string]any{"mode": "expression", "value": "{{ $('Manual Trigger').item.json.customer }}"},
					"workflowName": map[string]any{"mode": "expression", "value": "{{ $workflow.name }}"},
					"trigger":      map[string]any{"mode": "expression", "value": "{{ $execution.mode }}"},
				}, request.ExpressionContext(input["main"][0], input, 0))
				if err != nil {
					return nil, err
				}
				seen = resolved
				return workflow.NodeOutput{{{JSON: resolved}}}, nil
			})
		}
		if err := reader.Register(id, installed); err != nil {
			t.Fatalf("Register(%s) error = %v", id, err)
		}
	}

	if _, err := engine.NewRunner(reader).Run(context.Background(), ir, engine.Request{
		Input:     workflow.Item{JSON: map[string]any{"customer": "Ada"}},
		Workflow:  expression.WorkflowContext{ID: "wf_dollar_node", Name: "Read an earlier node"},
		Execution: engine.ExecutionContext{ID: "exec-1", Mode: "manual"},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	for key, want := range map[string]any{
		"fromEarlier":  "green",
		"fromTrigger":  "Ada",
		"workflowName": "Read an earlier node",
		"trigger":      "manual",
	} {
		if seen[key] != want {
			t.Errorf("%s = %#v, want %#v", key, seen[key], want)
		}
	}
}

// testCatalog is a node registry with the product's own nodes plus whatever
// test node types a case needs.
func testCatalog(t *testing.T, definitions ...node.Definition) *node.Registry {
	t.Helper()
	catalog := node.NewRegistry()
	for _, definition := range definitions {
		if err := catalog.Register(definition); err != nil {
			t.Fatalf("Register(%s) error = %v", definition.Type, err)
		}
	}
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	return catalog
}

// stepType is a test node with one item input and one item output.
func stepType(typeID, displayName string) node.Definition {
	return node.Definition{
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  typeID, Version: workflow.V(1), DisplayName: displayName, Category: "Test",
		Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: typeID,
	}
}

// startType is a test trigger: no input, one item output.
func startType(typeID, displayName string) node.Definition {
	return node.Definition{
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  typeID, Version: workflow.V(1), DisplayName: displayName, Category: "Test",
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: typeID,
	}
}

// compileDoc compiles a document, failing the test with the validation issues.
func compileDoc(t *testing.T, catalog *node.Registry, document workflow.Document) workflow.IR {
	t.Helper()
	ir, err := workflow.Compile(document, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	return ir
}

func mainEdge(id, source, sourcePort, target string) workflow.Connection {
	return workflow.Connection{
		ID: id, Kind: workflow.ConnectionMain,
		Source: workflow.Endpoint{NodeID: source, Port: sourcePort},
		Target: workflow.Endpoint{NodeID: target, Port: "main"},
	}
}

// TestBranchesRunDepthFirstInCanvasOrder is the parity gap that made
// cross-branch reads fail.
//
// Branches used to be chosen by node ID, so an imported workflow — whose IDs
// are n8n's random UUIDs — visited them in an order nobody drew, and a whole
// branch did not finish before the next one started. n8n v1 runs the topmost
// branch to its end first, ordered by output index and then by canvas
// position. The IDs here are deliberately reversed against the canvas so an
// ID-ordered scheduler cannot pass.
func TestBranchesRunDepthFirstInCanvasOrder(t *testing.T) {
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"))

	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_order", Name: "Two branches",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "zz-a1", Name: "A1", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 200, Y: 0}},
			{ID: "zz-a2", Name: "A2", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 400, Y: 0}},
			{ID: "aa-b1", Name: "B1", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 200, Y: 300}},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "zz-a1"),
			mainEdge("c2", "zz-a1", "main", "zz-a2"),
			mainEdge("c3", "start", "main", "aa-b1"),
		},
		Settings: map[string]any{},
	})

	var order []string
	executors := engine.NewRegistry()
	if err := executors.Register("test.start", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{{JSON: map[string]any{}}}}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := executors.Register("test.step", engine.ExecutorFunc(
		func(_ context.Context, node workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			order = append(order, node.Name)
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
	if got, want := strings.Join(order, ","), "A1,A2,B1"; got != want {
		t.Errorf("execution order = %s, want %s — the topmost branch runs to its end first", got, want)
	}
}

// TestAFanInNodeRunsOncePerDeliveringBranch is n8n v1's other half.
//
// Every incoming edge on one port used to be concatenated into a single
// invocation, so a branch that re-joined a shared tail ran the tail once with
// both streams and $runIndex and per-run side effects differed from n8n. n8n
// runs the node once for each branch that delivered data.
func TestAFanInNodeRunsOncePerDeliveringBranch(t *testing.T) {
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"))

	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_fanin", Name: "Two branches into one tail",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "set-a", Name: "Set A", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 200, Y: 0}},
			{ID: "set-b", Name: "Set B", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 200, Y: 300}},
			{ID: "limit", Name: "Limit", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 400, Y: 150}},
			{ID: "after", Name: "After", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 600, Y: 150}},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "set-a"),
			mainEdge("c2", "start", "main", "set-b"),
			mainEdge("c3", "set-a", "main", "limit"),
			mainEdge("c4", "set-b", "main", "limit"),
			mainEdge("c5", "limit", "main", "after"),
		},
		Settings: map[string]any{},
	})

	runs := map[string][][]string{}
	executors := engine.NewRegistry()
	if err := executors.Register("test.start", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{{JSON: map[string]any{}}}}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := executors.Register("test.step", engine.ExecutorFunc(
		func(_ context.Context, node workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			produced := make([]workflow.Item, 0, len(input["main"]))
			for _, item := range input["main"] {
				source, _ := item.JSON["src"].(string)
				runs[node.Name] = append(runs[node.Name], []string{source})
				produced = append(produced, workflow.Item{JSON: map[string]any{"src": source}})
			}
			if node.Name != "Start" && len(produced) == 0 {
				produced = append(produced, workflow.Item{JSON: map[string]any{}})
			}
			return workflow.NodeOutput{produced}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	// The two branches carry their own labels.
	executors2 := engine.NewRegistry()
	_ = executors2
	if _, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got, want := len(runs["Limit"]), 2; got != want {
		t.Fatalf("the fan-in node ran %d times, want once per delivering branch (%d)", got, want)
	}
	if got, want := len(runs["After"]), 2; got != want {
		t.Errorf("the node below the fan-in ran %d times, want %d", got, want)
	}
}

// TestDisabledNodePassesItsInputThrough is the safety property: an imported
// workflow carries nodes its author switched off, and importing one must not
// fire them.
func TestDisabledNodePassesItsInputThrough(t *testing.T) {
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"))

	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_disabled", Name: "Switched off in the middle",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "off", Name: "Off", Type: "test.step", TypeVersion: workflow.V(1), Disabled: true},
			{ID: "after", Name: "After", Type: "test.step", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "off"),
			mainEdge("c2", "off", "main", "after"),
		},
		Settings: map[string]any{},
	})

	var invoked []string
	var afterItems []workflow.Item
	executors := engine.NewRegistry()
	if err := executors.Register("test.start", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{{JSON: map[string]any{"customer": "Ada"}}}}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := executors.Register("test.step", engine.ExecutorFunc(
		func(_ context.Context, node workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			invoked = append(invoked, node.Name)
			if node.Name == "After" {
				afterItems = input["main"]
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
	for _, name := range invoked {
		if name == "Off" {
			t.Error("a disabled node was invoked")
		}
	}
	if len(afterItems) != 1 || afterItems[0].JSON["customer"] != "Ada" {
		t.Errorf("the node after a disabled one received %#v, want the input passed through", afterItems)
	}
}

// TestDisabledTriggerNeverFires covers the other half: a trigger the author
// switched off must not start a run, and naming it is an error rather than a
// silent run of everything.
func TestDisabledTriggerNeverFires(t *testing.T) {
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"))

	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_off_trigger", Name: "A switched-off trigger",
		Nodes: []workflow.Node{
			{ID: "on", Name: "On", Type: "test.start", TypeVersion: workflow.V(1), Position: workflow.Position{X: 0, Y: 0}},
			{ID: "off", Name: "Off", Type: "test.start", TypeVersion: workflow.V(1), Disabled: true, Position: workflow.Position{X: 0, Y: 200}},
			{ID: "after-on", Name: "After On", Type: "test.step", TypeVersion: workflow.V(1)},
			{ID: "after-off", Name: "After Off", Type: "test.step", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "on", "main", "after-on"),
			mainEdge("c2", "off", "main", "after-off"),
		},
		Settings: map[string]any{},
	})

	var invoked []string
	executors := engine.NewRegistry()
	if err := executors.Register("test.start", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{{JSON: map[string]any{}}}}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := executors.Register("test.step", engine.ExecutorFunc(
		func(_ context.Context, node workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			invoked = append(invoked, node.Name)
			return workflow.NodeOutput{input["main"]}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	runner := engine.NewRunner(executors)

	result, err := runner.Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	byNode := map[string]engine.NodeRun{}
	for _, run := range result.NodeRuns {
		byNode[run.NodeID] = run
	}
	if !byNode["off"].Skipped {
		t.Error("a disabled trigger was started")
	}
	if !byNode["after-off"].Skipped {
		t.Error("a node fed only by a disabled trigger ran")
	}
	if byNode["after-on"].Skipped {
		t.Error("the enabled trigger's branch did not run")
	}
	for _, name := range invoked {
		if name == "After Off" {
			t.Error("the disabled trigger's branch was invoked")
		}
	}

	if _, err := runner.Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{}}, TriggerNodeID: "off",
	}); err == nil {
		t.Error("running a disabled trigger by name was accepted")
	}
}

// TestContinueOnFailKeepsTheItemsThatSucceeded is the per-item property.
//
// Error tolerance used to work per node: any executor error replaced the whole
// output with error items and stopped at the first failing item, so the items
// that had already succeeded were discarded and the ones after it never ran —
// a bulk sender silently skipping every item after its first failure.
func TestContinueOnFailKeepsTheItemsThatSucceeded(t *testing.T) {
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.perItem", "Per item"))

	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_per_item", Name: "One failing item",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "send", Name: "Send", Type: "test.perItem", TypeVersion: workflow.V(1),
				Settings: map[string]any{"onError": "continueRegularOutput"}},
		},
		Connections: []workflow.Connection{mainEdge("c1", "start", "main", "send")},
		Settings:    map[string]any{},
	})

	var attempted []string
	executors := engine.NewRegistry()
	if err := executors.Register("test.start", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			items := make([]workflow.Item, 0, 3)
			for _, p := range []string{"ok1", "fail", "ok3"} {
				items = append(items, workflow.Item{JSON: map[string]any{"p": p}})
			}
			return workflow.NodeOutput{items}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := executors.Register("test.perItem", engine.ExecutorFunc(
		func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			p, _ := input["main"][0].JSON["p"].(string)
			attempted = append(attempted, p)
			if p == "fail" {
				return nil, errors.New("upstream returned 500")
			}
			return workflow.NodeOutput{{{JSON: map[string]any{"sent": p}}}}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Every item was attempted, including the one after the failure.
	if got, want := strings.Join(attempted, ","), "ok1,fail,ok3"; got != want {
		t.Errorf("attempted items = %s, want %s", got, want)
	}

	send := nodeRun(t, result, "send")
	items := send.Output[0]
	if len(items) != 3 {
		t.Fatalf("emitted %d items, want one per input item (3)", len(items))
	}
	if items[0].JSON["sent"] != "ok1" || items[2].JSON["sent"] != "ok3" {
		t.Errorf("the successful items were not kept: %#v", items)
	}
	descriptor, ok := items[1].JSON[engine.ErrorItemKey].(map[string]any)
	if !ok {
		t.Fatalf("the failed item = %#v, want an %s descriptor", items[1].JSON, engine.ErrorItemKey)
	}
	if descriptor["message"] != "upstream returned 500" {
		t.Errorf("error message = %#v, want the cause", descriptor["message"])
	}
	if _, old := items[1].JSON["$error"]; old {
		t.Errorf("the failed item still uses $error: %#v", items[1].JSON)
	}
	if items[1].Paired == nil {
		t.Error("the failed item lost its paired-item lineage")
	}
}

// TestContinueErrorOutputRoutesFailuresToTheErrorBranch is n8n's
// continueErrorOutput, and the reason a wired error branch used to make a
// workflow unrunnable: the port the branch was wired to did not exist.
func TestContinueErrorOutputRoutesFailuresToTheErrorBranch(t *testing.T) {
	catalog := testCatalog(t, startType("test.start", "Start"),
		stepType("test.perItem", "Per item"), stepType("test.step", "Step"))

	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_error_branch", Name: "Error branch",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "send", Name: "Send", Type: "test.perItem", TypeVersion: workflow.V(1),
				Settings: map[string]any{"onError": "continueErrorOutput"}},
			{ID: "ok", Name: "OK", Type: "test.step", TypeVersion: workflow.V(1)},
			{ID: "bad", Name: "Bad", Type: "test.step", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "send"),
			mainEdge("c2", "send", "main", "ok"),
			mainEdge("c3", "send", "error", "bad"),
		},
		Settings: map[string]any{},
	})

	executors := engine.NewRegistry()
	if err := executors.Register("test.start", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			items := make([]workflow.Item, 0, 3)
			for _, p := range []string{"ok1", "fail", "ok3"} {
				items = append(items, workflow.Item{JSON: map[string]any{"p": p}})
			}
			return workflow.NodeOutput{items}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := executors.Register("test.perItem", engine.ExecutorFunc(
		func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			p, _ := input["main"][0].JSON["p"].(string)
			if p == "fail" {
				return nil, errors.New("upstream returned 500")
			}
			return workflow.NodeOutput{{{JSON: map[string]any{"sent": p}}}}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := executors.Register("test.step", engine.ExecutorFunc(
		func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{input["main"]}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	send := nodeRun(t, result, "send")
	if len(send.Output) != 2 {
		t.Fatalf("Send returned %d streams, want the main output and the error output", len(send.Output))
	}
	if got, want := len(send.Output[0]), 2; got != want {
		t.Errorf("main output carried %d items, want the %d that succeeded", got, want)
	}
	if got, want := len(send.Output[1]), 1; got != want {
		t.Fatalf("error output carried %d items, want the 1 that failed", got)
	}
	failed := send.Output[1][0]
	if failed.JSON["p"] != "fail" {
		t.Errorf("the error branch item = %#v, want the input item plus the error", failed.JSON)
	}
	if _, ok := failed.JSON[engine.ErrorItemKey].(map[string]any); !ok {
		t.Errorf("the error branch item carries no %s: %#v", engine.ErrorItemKey, failed.JSON)
	}
	// Both branches ran: the successes continued and the failure was handled.
	if run := nodeRun(t, result, "ok"); len(run.Output[0]) != 2 {
		t.Errorf("the success branch received %d items, want 2", len(run.Output[0]))
	}
	if run := nodeRun(t, result, "bad"); len(run.Output[0]) != 1 {
		t.Errorf("the error branch received %d items, want 1", len(run.Output[0]))
	}
}

// TestNestedLoopsIterateIndependently is the property that made a loop body's
// analysis fragile: an inner loop inside an outer loop's body.
//
// The scheduler no longer classifies a loop's body at all — a loop's ports and
// its runner-owned state are enough — so nesting is a shape the graph either
// is or is not, rather than a case the analysis has to get right.
func TestNestedLoopsIterateIndependently(t *testing.T) {
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"))

	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_nested_loops", Name: "A loop inside a loop",
		Nodes: []workflow.Node{
			{ID: "src", Name: "Source", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "outer", Name: "Outer", Type: nodes.LoopNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"batchSize": float64(1), "maxIterations": float64(10)}},
			{ID: "expand", Name: "Expand", Type: "test.step", TypeVersion: workflow.V(1)},
			{ID: "inner", Name: "Inner", Type: nodes.LoopNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"batchSize": float64(1), "maxIterations": float64(10)}},
			{ID: "body", Name: "Body", Type: "test.step", TypeVersion: workflow.V(1)},
			{ID: "after", Name: "After", Type: "test.step", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			// Source is an ordinary node with a fixed output; the loop reads it.
			mainEdge("c1", "src", "main", "outer"),
			mainEdge("c2", "outer", "loop", "expand"),
			mainEdge("c3", "expand", "main", "inner"),
			mainEdge("c4", "inner", "loop", "body"),
			mainEdge("c5", "body", "main", "inner"),
			mainEdge("c6", "inner", "done", "outer"),
			mainEdge("c7", "outer", "done", "after"),
		},
		Settings: map[string]any{},
	})

	executors := engine.NewRegistry()
	if err := executors.Register("test.start", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{{JSON: map[string]any{"n": 0}}, {JSON: map[string]any{"n": 1}}, {JSON: map[string]any{"n": 2}}}}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := executors.Register("test.step", engine.ExecutorFunc(
		func(_ context.Context, node workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			switch node.Name {
			case "Expand":
				// Each outer batch becomes two inner items.
				items := make([]workflow.Item, 0, 2*len(input["main"]))
				for _, item := range input["main"] {
					items = append(items, workflow.Item{JSON: map[string]any{"n": item.JSON["n"], "half": 0}})
					items = append(items, workflow.Item{JSON: map[string]any{"n": item.JSON["n"], "half": 1}})
				}
				return workflow.NodeOutput{items}, nil
			default:
				return workflow.NodeOutput{input["main"]}, nil
			}
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	bodyRuns := 0
	for _, run := range result.NodeRuns {
		if run.NodeID == "body" && !run.Skipped {
			bodyRuns++
		}
	}
	// Three outer batches, two inner items each.
	if bodyRuns != 6 {
		t.Errorf("the inner body ran %d times, want 3 outer iterations x 2 inner items", bodyRuns)
	}

	var last engine.NodeRun
	for _, run := range result.NodeRuns {
		if run.NodeID == "outer" {
			last = run
		}
	}
	if got, want := len(last.Output[0]), 6; got != want {
		t.Errorf("the outer loop's done carried %d items, want %d", got, want)
	}
	if got, want := len(nodeRun(t, result, "after").Output[0]), 6; got != want {
		t.Errorf("the node after the outer loop received %d items, want %d", got, want)
	}
}

// TestContinueOnFailSendsEveryItemToARealServer is the reported repro run
// against a real server and the product's own HTTP node rather than a test
// executor: three items, the middle one answered with a 500.
//
// Before the fix the executor stopped at the failing item, so the third request
// was never sent, and the runner replaced the whole output with error items —
// discarding the response the first item had already received.
func TestContinueOnFailSendsEveryItemToARealServer(t *testing.T) {
	var (
		mu   sync.Mutex
		seen []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path := strings.TrimPrefix(request.URL.Path, "/")
		mu.Lock()
		seen = append(seen, path)
		mu.Unlock()
		if path == "fail" {
			http.Error(writer, "boom", http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(writer, `{"sent":%q}`, path)
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")

	// The stub is a real local endpoint, which the policy has to name exactly:
	// the private-address guard stays on for everything else.
	policy := safehttp.Policy{
		// The allowlist matches a hostname; the private-endpoint grant names
		// the exact host:port the stub listens on.
		AllowedHosts:            []string{"127.0.0.1"},
		AllowedPrivateEndpoints: []string{host},
		MaxResponseBytes:        1 << 20,
		Timeout:                 5 * time.Second,
	}
	catalog := testCatalog(t, startType("test.three", "Three"))
	document := func(settings map[string]any) workflow.Document {
		return workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            "wf_real_http", Name: "Bulk send with one failure",
			Nodes: []workflow.Node{
				{ID: "src", Name: "Three", Type: "test.three", TypeVersion: workflow.V(1)},
				{ID: "send", Name: "Send", Type: "kilasflow.httpRequest", TypeVersion: workflow.V(1),
					Settings: settings,
					Parameters: map[string]any{
						"method": "GET",
						"url":    map[string]any{"mode": "expression", "value": "http://" + host + "/{{ $json.p }}"},
					}},
			},
			Connections: []workflow.Connection{mainEdge("c1", "src", "main", "send")},
			Settings:    map[string]any{},
		}
	}

	executors := engine.NewRegistry()
	if err := executors.Register("test.three", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			items := make([]workflow.Item, 0, 3)
			for _, p := range []string{"ok1", "fail", "ok3"} {
				items = append(items, workflow.Item{JSON: map[string]any{"p": p}})
			}
			return workflow.NodeOutput{items}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterExecutors(executors, policy, sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	ir := compileDoc(t, catalog, document(map[string]any{"onError": "continueRegularOutput"}))
	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v, want the failure tolerated", err)
	}

	mu.Lock()
	requested := append([]string(nil), seen...)
	mu.Unlock()
	if got, want := strings.Join(requested, ","), "ok1,fail,ok3"; got != want {
		t.Errorf("server saw %s, want %s — every item is sent, including the ones after a failure", got, want)
	}

	send := nodeRun(t, result, "send")
	items := send.Output[0]
	if len(items) != 3 {
		t.Fatalf("Send emitted %d items, want one per input item", len(items))
	}
	if items[0].JSON["sent"] != "ok1" || items[2].JSON["sent"] != "ok3" {
		t.Errorf("the successful responses were not kept: %#v", items)
	}
	descriptor, ok := items[1].JSON[engine.ErrorItemKey].(map[string]any)
	if !ok {
		t.Fatalf("the failed item = %#v, want an %s descriptor", items[1].JSON, engine.ErrorItemKey)
	}
	if message, _ := descriptor["message"].(string); !strings.Contains(message, "500") {
		t.Errorf("error message = %#v, want the upstream status", descriptor["message"])
	}

	// The default is still to stop: without onError the same workflow fails on
	// the first 500 and the third item is never sent, which is what the ticket
	// reported as the difference from n8n.
	mu.Lock()
	seen = nil
	mu.Unlock()
	stopping := compileDoc(t, catalog, document(nil))
	if _, err := engine.NewRunner(executors).Run(context.Background(), stopping, engine.Request{Input: workflow.Item{JSON: map[string]any{}}}); err == nil {
		t.Fatal("a node without onError tolerated a failure")
	}
	mu.Lock()
	requested = append([]string(nil), seen...)
	mu.Unlock()
	if got, want := strings.Join(requested, ","), "ok1,fail"; got != want {
		t.Errorf("the stopping node sent %s, want %s", got, want)
	}
}

// TestDollarItemResolvesThroughHttpAndALoop extends the lineage contract to the
// two topologies the ticket names that a one-to-one chain does not cover: a
// real HTTP Request per item, and a Loop Over Items whose body returns new
// items.
func TestDollarItemResolvesThroughHttpAndALoop(t *testing.T) {
	var (
		mu   sync.Mutex
		seen []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		v := strings.TrimPrefix(request.URL.Path, "/item/")
		mu.Lock()
		seen = append(seen, v)
		mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(writer, `{"echo":%q}`, v)
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")

	catalog := testCatalog(t, stepType("test.probe", "Probe"))
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_lineage_http_loop", Name: "Lineage through HTTP and a loop",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "split", Name: "Split Out", Type: nodes.SplitOutNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"fieldToSplitOut": "list"}},
			{ID: "http", Name: "HTTP", Type: "kilasflow.httpRequest", TypeVersion: workflow.V(1),
				Parameters: map[string]any{
					"method": "GET",
					"url":    map[string]any{"mode": "expression", "value": "http://" + host + "/item/{{ $json.v }}"},
				}},
			{ID: "loop", Name: "Loop", Type: nodes.LoopNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"batchSize": float64(1), "maxIterations": float64(10)}},
			{ID: "body", Name: "Body", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"touched": "yes"}}},
			{ID: "probe", Name: "Probe", Type: "test.probe", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "manual", "main", "split"),
			mainEdge("c2", "split", "main", "http"),
			mainEdge("c3", "http", "main", "loop"),
			mainEdge("c4", "loop", "loop", "body"),
			mainEdge("c5", "body", "main", "loop"),
			mainEdge("c6", "loop", "done", "probe"),
		},
		Settings: map[string]any{},
	})

	var seenByProbe []any
	executors := engine.NewRegistry()
	if err := executors.Register("test.probe", engine.ExecutorFunc(
		func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
			items := make([]workflow.Item, 0, len(input["main"]))
			for index, item := range input["main"] {
				resolved, err := expression.Resolve(map[string]any{
					"origin": map[string]any{"mode": "expression", "value": "{{ $('Split Out').item.json.v }}"},
				}, request.ExpressionContext(item, input, index))
				if err != nil {
					return nil, err
				}
				seenByProbe = append(seenByProbe, resolved["origin"])
				items = append(items, workflow.Item{JSON: map[string]any{"saw": resolved["origin"]}})
			}
			return workflow.NodeOutput{items}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterExecutors(executors, safehttp.Policy{
		AllowedHosts:            []string{"127.0.0.1"},
		AllowedPrivateEndpoints: []string{host},
		MaxResponseBytes:        1 << 20,
		Timeout:                 5 * time.Second,
	}, sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	if _, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{"list": []any{
			map[string]any{"v": "a"}, map[string]any{"v": "b"}, map[string]any{"v": "c"},
		}}},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	mu.Lock()
	requested := append([]string(nil), seen...)
	mu.Unlock()
	if got, want := strings.Join(requested, ","), "a,b,c"; got != want {
		t.Errorf("the HTTP node requested %s, want one request per split item", got)
	}
	if len(seenByProbe) != 3 {
		t.Fatalf("the probe resolved %d items, want 3", len(seenByProbe))
	}
	for index, want := range []string{"a", "b", "c"} {
		if seenByProbe[index] != want {
			t.Errorf("after HTTP and a loop, item %d read %#v from $('Split Out').item, want %#v", index, seenByProbe[index], want)
		}
	}
}

// lineageAuditRun runs the lineage workflow the n8n-parity audit reported
// (BUG-zf4pnj): a Code-style node that turns the one trigger item into one item
// per path, an HTTP Request per item against a local stub that fails the path
// "fail", and a Set node on each output of the HTTP node reading
// `$('Two urls').item`. The error branch is wired only when the HTTP node has
// one.
//
// It returns what each Set node read back, by node ID, in output order.
func lineageAuditRun(t *testing.T, httpSettings map[string]any, paths ...string) map[string][]any {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/fail" {
			http.Error(writer, "boom", http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(writer, `{"path":%q}`, request.URL.Path)
	}))
	t.Cleanup(server.Close)
	host := strings.TrimPrefix(server.URL, "http://")

	readBack := func(id, name string) workflow.Node {
		return workflow.Node{ID: id, Name: name, Type: "kilasflow.set", TypeVersion: workflow.V(1),
			Parameters: map[string]any{"assignments": map[string]any{
				"fromTwoUrls": map[string]any{"mode": "expression", "value": "{{ $('Two urls').item.json.u }}"},
			}}}
	}
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_lineage_audit", Name: "Code then HTTP then Set",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "two-urls", Name: "Two urls", Type: "test.twoUrls", TypeVersion: workflow.V(1)},
			{ID: "http", Name: "HTTP Request", Type: "kilasflow.httpRequest", TypeVersion: workflow.V(1),
				Settings: httpSettings,
				Parameters: map[string]any{
					"method": "GET",
					"url":    map[string]any{"mode": "expression", "value": "http://" + host + "/{{ $json.u }}"},
				}},
			readBack("after-regular", "After regular"),
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "manual", "main", "two-urls"),
			mainEdge("c2", "two-urls", "main", "http"),
			mainEdge("c3", "http", "main", "after-regular"),
		},
		Settings: map[string]any{},
	}
	if httpSettings["onError"] == "continueErrorOutput" {
		document.Nodes = append(document.Nodes, readBack("after-error", "After error"))
		document.Connections = append(document.Connections, mainEdge("c4", "http", "error", "after-error"))
	}
	ir := compileDoc(t, testCatalog(t, stepType("test.twoUrls", "Two urls")), document)

	executors := engine.NewRegistry()
	// One item in, one per path out: the item count changes, so the runner
	// rightly records these items' own lineage as lost.
	if err := executors.Register("test.twoUrls", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			items := make([]workflow.Item, 0, len(paths))
			for _, path := range paths {
				items = append(items, workflow.Item{JSON: map[string]any{"u": path}})
			}
			return workflow.NodeOutput{items}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterExecutors(executors, safehttp.Policy{
		AllowedHosts:            []string{"127.0.0.1"},
		AllowedPrivateEndpoints: []string{host},
		MaxResponseBytes:        1 << 20,
		Timeout:                 5 * time.Second,
	}, sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	written := map[string][]any{}
	for _, run := range result.NodeRuns {
		if run.NodeID != "after-regular" && run.NodeID != "after-error" {
			continue
		}
		for _, port := range run.Output {
			for _, item := range port {
				written[run.NodeID] = append(written[run.NodeID], item.JSON["fromTwoUrls"])
			}
		}
	}
	return written
}

// TestDollarItemReachesACodeNodesItemThroughHttp is the audit's workflow k.
//
// The Code node changed the item count, so its own items have no lineage — but
// every HTTP item names the Code item it was made from, which is all
// `$('Two urls').item` needs. Comparing origins alone could not see that, and
// failed with "changed the item correspondence".
func TestDollarItemReachesACodeNodesItemThroughHttp(t *testing.T) {
	written := lineageAuditRun(t, nil, "a", "b", "c")
	if got, want := fmt.Sprint(written["after-regular"]), "[a b c]"; got != want {
		t.Errorf("After regular read %s from $('Two urls').item, want %s", got, want)
	}
}

// TestDollarItemResolvesOnBothBranchesOfAnErrorOutput is the audit's workflow
// i: an HTTP node that routes its failures to an error output, and both
// branches reading `$('Two urls').item`.
//
// Every item on both outputs used to be stamped lost, because the node's
// output was stamped once as a whole, and neither output has as many items as
// the node was given.
func TestDollarItemResolvesOnBothBranchesOfAnErrorOutput(t *testing.T) {
	written := lineageAuditRun(t, map[string]any{"onError": "continueErrorOutput"}, "a", "fail", "c")
	if got, want := fmt.Sprint(written["after-regular"]), "[a c]"; got != want {
		t.Errorf("the success branch read %s from $('Two urls').item, want %s", got, want)
	}
	if got, want := fmt.Sprint(written["after-error"]), "[fail]"; got != want {
		t.Errorf("the error branch read %s from $('Two urls').item, want %s", got, want)
	}
}

// TestDollarItemResolvesAfterAContinueOnFailFailure is the audit's workflow j:
// the failed item continues on the regular output, and it pairs with the item
// that failed exactly as the items that succeeded pair with theirs.
func TestDollarItemResolvesAfterAContinueOnFailFailure(t *testing.T) {
	written := lineageAuditRun(t, map[string]any{"onError": "continueRegularOutput"}, "a", "fail", "c")
	if got, want := fmt.Sprint(written["after-regular"]), "[a fail c]"; got != want {
		t.Errorf("After regular read %s from $('Two urls').item, want %s", got, want)
	}
}

// TestDollarItemResolvesAfterANodeLevelFailure covers the tolerated failure
// that is not resolved item by item. Execute Once runs the node against the
// first item as a whole, so its error item is built for the whole node — and it
// has to pair with that item as much as a per-item one does.
func TestDollarItemResolvesAfterANodeLevelFailure(t *testing.T) {
	written := lineageAuditRun(t, map[string]any{"onError": "continueErrorOutput", "executeOnce": true}, "fail", "b")
	if got, want := fmt.Sprint(written["after-error"]), "[fail]"; got != want {
		t.Errorf("the error branch read %s from $('Two urls').item, want %s", got, want)
	}
}

// TestDollarItemPairsWithTheRunThatDeliveredTheItem covers the run half of the
// pointer an item takes when its own lineage is lost.
//
// A loop runs once per batch and once more to hand on `done`, while the node
// after it runs once. Recording that node's own run index in the pointer named
// the loop's first run rather than the one that delivered the items, so
// `$('Loop').item` found nothing to pair with.
func TestDollarItemPairsWithTheRunThatDeliveredTheItem(t *testing.T) {
	catalog := testCatalog(t, stepType("test.fanOut", "Fan out"), stepType("test.pass", "Pass"),
		stepType("test.fresh", "Fresh"), stepType("test.probe", "Probe"))
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_lineage_runs", Name: "Read the loop after it is done",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "fan", Name: "Fan", Type: "test.fanOut", TypeVersion: workflow.V(1)},
			{ID: "loop", Name: "Loop", Type: nodes.LoopNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"batchSize": float64(1), "maxIterations": float64(10)}},
			{ID: "body", Name: "Body", Type: "test.pass", TypeVersion: workflow.V(1)},
			{ID: "after", Name: "After", Type: "test.fresh", TypeVersion: workflow.V(1)},
			{ID: "probe", Name: "Probe", Type: "test.probe", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "manual", "main", "fan"),
			mainEdge("c2", "fan", "main", "loop"),
			mainEdge("c3", "loop", "loop", "body"),
			mainEdge("c4", "body", "main", "loop"),
			mainEdge("c5", "loop", "done", "after"),
			mainEdge("c6", "after", "main", "probe"),
		},
		Settings: map[string]any{},
	})

	executors := engine.NewRegistry()
	register := func(typeID string, execute engine.ExecutorFunc) {
		t.Helper()
		if err := executors.Register(typeID, execute); err != nil {
			t.Fatalf("Register(%s) error = %v", typeID, err)
		}
	}
	// One item in, three out: these items' own lineage is lost.
	register("test.fanOut", func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
		return workflow.NodeOutput{{{JSON: map[string]any{"u": "x0"}}, {JSON: map[string]any{"u": "x1"}}, {JSON: map[string]any{"u": "x2"}}}}, nil
	})
	// The body hands its batch back untouched, lineage included, so what the
	// loop collects on done still has none.
	register("test.pass", func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
		return workflow.NodeOutput{input["main"]}, nil
	})
	// After builds new items and leaves their lineage to the runner.
	register("test.fresh", func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
		items := make([]workflow.Item, 0, len(input["main"]))
		for index := range input["main"] {
			items = append(items, workflow.Item{JSON: map[string]any{"n": index}})
		}
		return workflow.NodeOutput{items}, nil
	})
	var read []string
	register("test.probe", func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
		for index, item := range input["main"] {
			resolved, err := expression.Resolve(map[string]any{
				"u": map[string]any{"mode": "expression", "value": "{{ $('Loop').item.json.u }}"},
			}, request.ExpressionContext(item, input, index))
			if err != nil {
				return nil, err
			}
			read = append(read, fmt.Sprint(resolved["u"]))
		}
		return workflow.NodeOutput{input["main"]}, nil
	})
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	if _, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{}},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := strings.Join(read, ","), "x0,x1,x2"; got != want {
		t.Errorf("$('Loop').item read %s after the loop, want %s", got, want)
	}
}

// TestDollarItemRefusesAnEarlierRunRatherThanReadingTheLatest covers a branch
// the stack holds back while the loop it hangs off runs again.
//
// Per batch changes the item count on every batch, and Side hangs off it after
// the edge back to the loop, so the Side invocations of every batch wait until
// the loop is done. By then Per batch's latest run is the last batch's. An item
// has to keep naming the run it came from: taking the latest run instead would
// pair the first two batches' items with the last batch's, a confident wrong
// answer where the honest one is that an earlier run's items are no longer
// there to read. Head keeps Per batch off the loop's port, so the loop's final,
// empty dispatch skips Head rather than recording one more run of Per batch.
func TestDollarItemRefusesAnEarlierRunRatherThanReadingTheLatest(t *testing.T) {
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.perBatch", "Per batch"),
		stepType("test.pass", "Pass"), stepType("test.copy", "Copy"), stepType("test.probe", "Probe"))
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_lineage_held_branch", Name: "A branch held behind a loop",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "loop", Name: "Loop", Type: nodes.LoopNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"batchSize": float64(1), "maxIterations": float64(10)}},
			{ID: "head", Name: "Head", Type: "test.pass", TypeVersion: workflow.V(1)},
			{ID: "per-batch", Name: "Per batch", Type: "test.perBatch", TypeVersion: workflow.V(1)},
			{ID: "back", Name: "Back", Type: "test.pass", TypeVersion: workflow.V(1)},
			{ID: "side", Name: "Side", Type: "test.copy", TypeVersion: workflow.V(1)},
			{ID: "probe", Name: "Probe", Type: "test.probe", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "loop"),
			mainEdge("c2", "loop", "loop", "head"),
			mainEdge("c3", "head", "main", "per-batch"),
			mainEdge("c4", "per-batch", "main", "back"),
			mainEdge("c5", "per-batch", "main", "side"),
			mainEdge("c6", "back", "main", "loop"),
			mainEdge("c7", "side", "main", "probe"),
		},
		Settings: map[string]any{},
	})

	executors := engine.NewRegistry()
	register := func(typeID string, execute engine.ExecutorFunc) {
		t.Helper()
		if err := executors.Register(typeID, execute); err != nil {
			t.Fatalf("Register(%s) error = %v", typeID, err)
		}
	}
	register("test.start", func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
		return workflow.NodeOutput{{{JSON: map[string]any{"b": "b0"}}, {JSON: map[string]any{"b": "b1"}}, {JSON: map[string]any{"b": "b2"}}}}, nil
	})
	// Two items from the batch's one, so their own lineage is lost.
	register("test.perBatch", func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
		batch, _ := input["main"][0].JSON["b"].(string)
		return workflow.NodeOutput{{{JSON: map[string]any{"u": batch + "-0"}}, {JSON: map[string]any{"u": batch + "-1"}}}}, nil
	})
	register("test.pass", func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
		return workflow.NodeOutput{input["main"]}, nil
	})
	// Side copies the fields and leaves the lineage to the runner, so the probe
	// can tell what each item should pair with.
	register("test.copy", func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
		items := make([]workflow.Item, 0, len(input["main"]))
		for _, item := range input["main"] {
			items = append(items, workflow.Item{JSON: item.JSON})
		}
		return workflow.NodeOutput{items}, nil
	})
	var paired, refused int
	var wrong []string
	register("test.probe", func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
		for index, item := range input["main"] {
			resolved, err := expression.Resolve(map[string]any{
				"u": map[string]any{"mode": "expression", "value": "{{ $('Per batch').item.json.u }}"},
			}, request.ExpressionContext(item, input, index))
			switch {
			case err != nil:
				refused++
			case resolved["u"] == item.JSON["u"]:
				paired++
			default:
				wrong = append(wrong, fmt.Sprintf("%v read %v", item.JSON["u"], resolved["u"]))
			}
		}
		return workflow.NodeOutput{input["main"]}, nil
	})
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	if _, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{}},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(wrong) > 0 {
		t.Errorf("$('Per batch').item paired items with another batch's: %s", strings.Join(wrong, "; "))
	}
	// The last batch's Side still runs against the run it came from, and the
	// two before it are refused rather than guessed.
	if paired != 2 || refused != 4 {
		t.Errorf("paired %d and refused %d items, want the last batch's 2 paired and the other 4 refused", paired, refused)
	}
}

// TestDollarItemBehindARoutingNodeNeverReadsAnotherBatch is the held branch of
// the test above, with a real IF between Per batch and its fan-out.
//
// IF hands its items on with the lineage they arrived with, so what reaches
// Side carries Per batch's stamp rather than one naming IF, and IF's own run
// and position have to be worked out. Taking IF's latest run paired every
// earlier batch's items with the last batch's, silently. Refusing an item
// whose run cannot be established is the acceptable answer; reading another
// batch's is not.
func TestDollarItemBehindARoutingNodeNeverReadsAnotherBatch(t *testing.T) {
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.perBatch", "Per batch"),
		stepType("test.pass", "Pass"), stepType("test.copy", "Copy"), stepType("test.probe", "Probe"))
	always := []any{map[string]any{"field": "keep", "operator": "equals", "value": "yes"}}
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_lineage_held_if", Name: "A branch held behind a loop, after an IF",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "loop", Name: "Loop", Type: nodes.LoopNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"batchSize": float64(1), "maxIterations": float64(10)}},
			{ID: "head", Name: "Head", Type: "test.pass", TypeVersion: workflow.V(1)},
			{ID: "per-batch", Name: "Per batch", Type: "test.perBatch", TypeVersion: workflow.V(1)},
			{ID: "if", Name: "IF", Type: "kilasflow.if", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"conditions": always}},
			{ID: "back", Name: "Back", Type: "test.pass", TypeVersion: workflow.V(1)},
			{ID: "side", Name: "Side", Type: "test.copy", TypeVersion: workflow.V(1)},
			{ID: "probe", Name: "Probe", Type: "test.probe", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "loop"),
			mainEdge("c2", "loop", "loop", "head"),
			mainEdge("c3", "head", "main", "per-batch"),
			mainEdge("c4", "per-batch", "main", "if"),
			mainEdge("c5", "if", "true", "back"),
			mainEdge("c6", "if", "true", "side"),
			mainEdge("c7", "back", "main", "loop"),
			mainEdge("c8", "side", "main", "probe"),
		},
		Settings: map[string]any{},
	})

	executors := engine.NewRegistry()
	register := func(typeID string, execute engine.ExecutorFunc) {
		t.Helper()
		if err := executors.Register(typeID, execute); err != nil {
			t.Fatalf("Register(%s) error = %v", typeID, err)
		}
	}
	register("test.start", func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
		return workflow.NodeOutput{{{JSON: map[string]any{"b": "b0"}}, {JSON: map[string]any{"b": "b1"}}, {JSON: map[string]any{"b": "b2"}}}}, nil
	})
	// Two items from the batch's one, so their own lineage is lost.
	register("test.perBatch", func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
		batch, _ := input["main"][0].JSON["b"].(string)
		return workflow.NodeOutput{{
			{JSON: map[string]any{"u": batch + "-0", "keep": "yes"}},
			{JSON: map[string]any{"u": batch + "-1", "keep": "yes"}},
		}}, nil
	})
	register("test.pass", func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
		return workflow.NodeOutput{input["main"]}, nil
	})
	register("test.copy", func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
		items := make([]workflow.Item, 0, len(input["main"]))
		for _, item := range input["main"] {
			items = append(items, workflow.Item{JSON: item.JSON})
		}
		return workflow.NodeOutput{items}, nil
	})
	var paired, refused int
	var wrong []string
	register("test.probe", func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
		for index, item := range input["main"] {
			resolved, err := expression.Resolve(map[string]any{
				"u": map[string]any{"mode": "expression", "value": "{{ $('IF').item.json.u }}"},
			}, request.ExpressionContext(item, input, index))
			switch {
			case err != nil:
				refused++
			case resolved["u"] == item.JSON["u"]:
				paired++
			default:
				wrong = append(wrong, fmt.Sprintf("%v read %v", item.JSON["u"], resolved["u"]))
			}
		}
		return workflow.NodeOutput{input["main"]}, nil
	})
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	if _, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{}},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(wrong) > 0 {
		t.Errorf("$('IF').item paired items with another batch's: %s", strings.Join(wrong, "; "))
	}
	if paired+refused != 6 {
		t.Errorf("the probe saw %d items, want the 6 the three batches produced", paired+refused)
	}
}

// TestDollarItemStaysWithinThePortItsOriginNames covers the bound on a pointer
// into a node with several ports. The ports' items sit end to end, so an item
// index past the end of its own port would land on the next port's items.
func TestDollarItemStaysWithinThePortItsOriginNames(t *testing.T) {
	items := map[string]expression.NodeItem{
		"IF": {
			NodeID: "if", RunIndex: 0,
			PortOffsets: map[string]int{"true": 0, "false": 1},
			PortLengths: map[string]int{"true": 1, "false": 2},
			Items:       []map[string]any{{"side": "true-0"}, {"side": "false-0"}, {"side": "false-1"}},
			ItemOrigins: []string{"", "", ""},
		},
	}
	pointsAt := func(port string, index int) workflow.Item {
		return workflow.Item{JSON: map[string]any{}, Paired: &workflow.PairedItem{SourceNodeID: "if", SourcePort: port, ItemIndex: index}}
	}

	if got := engine.PairNodeItems(items, pointsAt("false", 1), 0)["IF"].Paired; got == nil || got["side"] != "false-1" {
		t.Errorf("a pointer at false[1] paired with %#v, want false-1", got)
	}
	if got := engine.PairNodeItems(items, pointsAt("true", 1), 0)["IF"].Paired; got != nil {
		t.Errorf("a pointer past the end of true paired with %#v, want a refusal", got)
	}
}

// TestDollarItemDoesNotReadAnInputPositionAsAnOutputPosition guards the
// shortcut `.item` takes when an item's origin names the referenced node.
//
// Split Out stamps an item whose incoming lineage was lost with its own name
// and the position of the item it split, which is a position in its input, not
// in its output. Reading it as an output position would pair all four items
// with the first two; the origin comparison pairs each with itself.
func TestDollarItemDoesNotReadAnInputPositionAsAnOutputPosition(t *testing.T) {
	catalog := testCatalog(t, stepType("test.fanOut", "Fan out"), stepType("test.probe", "Probe"))
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_split_positions", Name: "Split after a count change",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "fan", Name: "Fan", Type: "test.fanOut", TypeVersion: workflow.V(1)},
			{ID: "split", Name: "Split Out", Type: nodes.SplitOutNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"fieldToSplitOut": "list"}},
			{ID: "probe", Name: "Probe", Type: "test.probe", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "manual", "main", "fan"),
			mainEdge("c2", "fan", "main", "split"),
			mainEdge("c3", "split", "main", "probe"),
		},
		Settings: map[string]any{},
	})

	executors := engine.NewRegistry()
	if err := executors.Register("test.fanOut", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{
				{JSON: map[string]any{"list": []any{map[string]any{"v": "a"}, map[string]any{"v": "b"}}}},
				{JSON: map[string]any{"list": []any{map[string]any{"v": "c"}, map[string]any{"v": "d"}}}},
			}}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	var read []string
	if err := executors.Register("test.probe", engine.ExecutorFunc(
		func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
			for index, item := range input["main"] {
				resolved, err := expression.Resolve(map[string]any{
					"v": map[string]any{"mode": "expression", "value": "{{ $('Split Out').item.json.v }}"},
				}, request.ExpressionContext(item, input, index))
				if err != nil {
					return nil, err
				}
				read = append(read, fmt.Sprint(resolved["v"]))
			}
			return workflow.NodeOutput{input["main"]}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	if _, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{}},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := strings.Join(read, ","), "a,b,c,d"; got != want {
		t.Errorf("$('Split Out').item read %s, want each item paired with itself (%s)", got, want)
	}
}
