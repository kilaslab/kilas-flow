package nodes_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/jsrun"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func jsExecutor(t *testing.T, id string, options ...nodes.ExecutorOption) engine.Executor {
	t.Helper()
	registry := engine.NewRegistry()
	if err := nodes.RegisterExecutors(registry, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil, options...); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, ok := registry.Lookup(id)
	if !ok {
		t.Fatalf("no executor registered for %s", id)
	}
	return executor
}

func jsNode(nodeType string, parameters map[string]any) workflow.IRNode {
	return workflow.IRNode{ID: "code", Name: "Code", Type: nodeType, TypeVersion: workflow.V(1), Parameters: parameters}
}

func threeItems() workflow.NodeInput {
	return workflow.NodeInput{"main": {
		{JSON: map[string]any{"n": float64(1)}}, {JSON: map[string]any{"n": float64(2)}}, {JSON: map[string]any{"n": float64(3)}},
	}}
}

func TestTheJavaScriptCodeNodeRunsBothModes(t *testing.T) {
	executor := jsExecutor(t, nodes.JSCodeExecutorID)
	all, err := executor.Execute(context.Background(), jsNode(nodes.JSCodeNodeType, map[string]any{
		"mode": nodes.CodeModeAllItems, "jsCode": "return [{ json: { total: items.reduce((sum, item) => sum + item.json.n, 0) } }]",
	}), threeItems(), engine.Request{})
	if err != nil || len(all) != 1 || len(all[0]) != 1 || all[0][0].JSON["total"] != float64(6) {
		t.Fatalf("all items: %#v, %v; want one item totalling 6", all, err)
	}
	each, err := executor.Execute(context.Background(), jsNode(nodes.JSCodeNodeType, map[string]any{
		"mode": nodes.CodeModeEachItem, "jsCode": "return { json: { doubled: $json.n * 2, index: $itemIndex } }",
	}), threeItems(), engine.Request{})
	if err != nil || len(each[0]) != 3 || each[0][2].JSON["doubled"] != float64(6) || each[0][2].JSON["index"] != float64(2) {
		t.Fatalf("each item: %#v, %v", each, err)
	}
}

// Workflows imported before the runtime kept their JavaScript Code nodes as
// the placeholder; they run now without being imported again.
func TestAForeignCodeJavaScriptNodeRunsWithoutReimport(t *testing.T) {
	output, err := jsExecutor(t, nodes.ForeignCodeExecutorID).Execute(context.Background(), jsNode(nodes.ForeignCodeNodeType, map[string]any{
		"language": "javaScript", "mode": "runOnceForAllItems", "jsCode": "return items.filter(item => item.json.n > 1)",
	}), threeItems(), engine.Request{})
	if err != nil || len(output[0]) != 2 {
		t.Fatalf("Execute() = %#v, %v; want the two items the filter keeps", output, err)
	}
}

func TestPythonForeignCodeStillRefusesInTheSameWords(t *testing.T) {
	parameters := map[string]any{"language": "python", "pythonCode": "return items"}
	_, err := jsExecutor(t, nodes.ForeignCodeExecutorID).Execute(context.Background(), jsNode(nodes.ForeignCodeNodeType, parameters), threeItems(), engine.Request{})
	if err == nil || !strings.Contains(err.Error(), "this node's code is written in Python, which this server does not run.") {
		t.Fatalf("Execute() error = %v, want Python refused in the one sentence", err)
	}
}

func TestTheNodeMayOnlyTightenTheTimeLimit(t *testing.T) {
	busy := "const until = Date.now() + 600\nwhile (Date.now() < until) {}\nreturn items"
	// The deployment allows 200ms; the node asks for five seconds and does not get them.
	runner := jsrun.NewRunner(jsrun.Options{Limits: jsrun.Limits{Timeout: 200 * time.Millisecond}})
	_, err := jsExecutor(t, nodes.JSCodeExecutorID, nodes.WithJSRunner(runner)).Execute(context.Background(),
		jsNode(nodes.JSCodeNodeType, map[string]any{"jsCode": busy, "scriptTimeoutSeconds": float64(5)}), threeItems(), engine.Request{})
	if !errors.Is(err, jsrun.ErrTimeLimit) || !strings.Contains(err.Error(), "200ms") {
		t.Fatalf("Execute() error = %v, want the deployment's 200ms limit", err)
	}
	// The node can ask for less than the default.
	_, err = jsExecutor(t, nodes.JSCodeExecutorID).Execute(context.Background(),
		jsNode(nodes.JSCodeNodeType, map[string]any{"jsCode": busy, "scriptTimeoutSeconds": 0.1}), threeItems(), engine.Request{})
	if !errors.Is(err, jsrun.ErrTimeLimit) || !strings.Contains(err.Error(), "100ms") {
		t.Fatalf("Execute() error = %v, want the node's own 100ms limit", err)
	}
}

func TestADisabledRuntimeRefusesNamingTheConfigKey(t *testing.T) {
	_, err := jsExecutor(t, nodes.JSCodeExecutorID, nodes.WithoutJavaScript(nodes.JavaScriptDisabled)).Execute(context.Background(),
		jsNode(nodes.JSCodeNodeType, map[string]any{"jsCode": "return items"}), threeItems(), engine.Request{})
	if err == nil || !strings.Contains(err.Error(), "code.javascript_enabled") || !strings.Contains(err.Error(), "which this server does not run") {
		t.Fatalf("Execute() error = %v, want the refusal naming the key", err)
	}
}

// Validation reads the code; it never runs it.
func TestValidateRefusesUnsupportedConstructsWithoutRunning(t *testing.T) {
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Get(nodes.JSCodeNodeType, workflow.V(1))
	validate := func(source string) error {
		return definition.Validate(workflow.Node{Parameters: map[string]any{"jsCode": source}})
	}
	if err := validate("const fs = require('fs')\nreturn items"); err == nil || !strings.Contains(err.Error(), `requires the module "fs"`) {
		t.Fatalf("Validate() = %v, want fs refused", err)
	}
	start := time.Now()
	if err := validate("while (true) {}"); err != nil {
		t.Fatalf("Validate() = %v, want a body that would loop forever accepted, since it is not run", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("validating took %v; it must not run the code", elapsed)
	}
	if err := validate("   "); err == nil {
		t.Fatal("an empty body was accepted")
	}
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"jsCode": "return items", "mode": "sometimes"}}); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
}

// In per-item mode a node that continues on failure goes on past the item
// that failed, as n8n's item loop does, and reports each item's outcome; one
// that does not stops at that item, and the items after it never run.
func TestPerItemModeGoesOnPastAFailedItemOnlyWhenTheNodeContinuesOnFailure(t *testing.T) {
	code := jsNode(nodes.JSCodeNodeType, map[string]any{
		"mode":   nodes.CodeModeEachItem,
		"jsCode": "console.log('item', $itemIndex, 'of', $input.all().length)\nif ($json.n === 2) JSON.parse('{')\nreturn { json: { doubled: $json.n * 2 } }",
	})
	var lines []string
	request := engine.Request{TolerateItemFailures: true, Events: func(event engine.NodeEvent) {
		var detail engine.ConsoleDetail
		_ = json.Unmarshal(event.Detail, &detail)
		for _, line := range detail.Lines {
			lines = append(lines, line.Text)
		}
	}}
	output, err := jsExecutor(t, nodes.JSCodeExecutorID).Execute(context.Background(), code, threeItems(), request)
	var outcomes engine.ItemOutcomes
	if output != nil || !errors.As(err, &outcomes) || len(outcomes) != 3 {
		t.Fatalf("Execute() = %#v, %v; want three item outcomes", output, err)
	}
	if outcomes[0].Err != nil || outcomes[0].Items[0].JSON["doubled"] != float64(2) || outcomes[2].Items[0].JSON["doubled"] != float64(6) {
		t.Errorf("outcomes 0 and 2 = %#v, %#v; want them doubled", outcomes[0], outcomes[2])
	}
	var script *jsrun.ScriptError
	if !errors.As(outcomes[1].Err, &script) || script.Name != "SyntaxError" || !strings.Contains(outcomes[1].Err.Error(), `node "Code": `) ||
		!strings.Contains(outcomes[1].Err.Error(), "[line 2, for item 1]") {
		t.Errorf("outcome 1 = %v, want the parse error on line 2 for item 1", outcomes[1].Err)
	}
	if strings.Join(lines, "|") != "item 0 of 3|item 1 of 3|item 2 of 3" {
		t.Errorf("console = %q, want every item to have run seeing the whole batch", lines)
	}

	lines = nil
	request.TolerateItemFailures = false
	_, err = jsExecutor(t, nodes.JSCodeExecutorID).Execute(context.Background(), code, threeItems(), request)
	if !errors.As(err, &script) || errors.As(err, &outcomes) || strings.Join(lines, "|") != "item 0 of 3|item 1 of 3" {
		t.Fatalf("Execute() error = %v, console %q; want the run to stop at item 1", err, lines)
	}
}

// What the code printed before it failed is usually how its author finds out
// why, so it is handed over on a failure too.
func TestConsoleIsEmittedEvenWhenTheCodeFails(t *testing.T) {
	var events []engine.NodeEvent
	request := engine.Request{Events: func(event engine.NodeEvent) { events = append(events, event) }}
	_, err := jsExecutor(t, nodes.JSCodeExecutorID).Execute(context.Background(),
		jsNode(nodes.JSCodeNodeType, map[string]any{"jsCode": "console.log('about to fail', items.length)\nthrow new Error('broken')"}), threeItems(), request)
	if err == nil {
		t.Fatal("Execute() succeeded, want the thrown error")
	}
	if len(events) != 1 || events[0].Name != engine.ConsoleEventName || events[0].NodeID != "code" {
		t.Fatalf("events = %#v, want one console event from the node", events)
	}
	var detail engine.ConsoleDetail
	if err := json.Unmarshal(events[0].Detail, &detail); err != nil || len(detail.Lines) != 1 || detail.Lines[0].Text != "about to fail 3" {
		t.Fatalf("console detail = %s", events[0].Detail)
	}
}

// tenantEngine is a runtime that records the tenant each task runs for.
type tenantEngine struct{ tenants []string }

func (engine *tenantEngine) Run(_ context.Context, task jsrun.Task) (jsrun.Result, error) {
	engine.tenants = append(engine.tenants, task.Tenant)
	return jsrun.Result{Order: []int{0, 1, 2}}, nil
}

// The runtime is told whose execution a task belongs to, so that a worker
// pool can keep each tenant's code on workers of its own: the Code node's
// and the Sort node's comparator alike.
func TestTheRuntimeIsToldTheExecutionsTenant(t *testing.T) {
	runtime := &tenantEngine{}
	request := engine.Request{Execution: engine.ExecutionContext{ID: "exe_1", TenantID: "tenant-a"}}
	if _, err := jsExecutor(t, nodes.JSCodeExecutorID, nodes.WithJSRunner(runtime)).Execute(context.Background(),
		jsNode(nodes.JSCodeNodeType, map[string]any{"jsCode": "return items"}), threeItems(), request); err != nil {
		t.Fatalf("Code: Execute() error = %v", err)
	}
	if _, err := jsExecutor(t, nodes.SortExecutorID, nodes.WithJSRunner(runtime)).Execute(context.Background(),
		jsNode(nodes.SortNodeType, map[string]any{"type": "code", "code": "return a.json.n - b.json.n"}), threeItems(), request); err != nil {
		t.Fatalf("Sort: Execute() error = %v", err)
	}
	if got := strings.Join(runtime.tenants, ","); got != "tenant-a,tenant-a" {
		t.Fatalf("the runtime was told tenants %q, want tenant-a for both", got)
	}
}
