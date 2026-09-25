package nodes_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// runToleratedCode runs Manual → Emit (three items, each with its n) → Code
// (the given mode and body, under the given onError) and answers Code's
// output. Under continueErrorOutput a No Op hangs off each output, so both
// are real.
func runToleratedCode(t *testing.T, mode, source, onError string) workflow.NodeOutput {
	t.Helper()
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	link := func(id, source, port, target string) workflow.Connection {
		return workflow.Connection{ID: id, Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: source, Port: port}, Target: workflow.Endpoint{NodeID: target, Port: "main"}}
	}
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_js_tolerated", Name: "Code node tolerates a failure", Settings: map[string]any{},
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "emit", Name: "Emit", Type: nodes.JSCodeNodeType, TypeVersion: workflow.V(1), Parameters: map[string]any{
				"jsCode": "return [1, 2, 3].map((n) => ({ json: { n } }))"}},
			{ID: "code", Name: "Code", Type: nodes.JSCodeNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"mode": mode, "jsCode": source}, Settings: map[string]any{"onError": onError}},
			{ID: "ok", Name: "OK", Type: nodes.NoOpNodeType, TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{link("c1", "manual", "main", "emit"), link("c2", "emit", "main", "code"), link("c3", "code", "main", "ok")},
	}
	if onError == "continueErrorOutput" {
		document.Nodes = append(document.Nodes, workflow.Node{ID: "failed", Name: "Failed", Type: nodes.NoOpNodeType, TypeVersion: workflow.V(1)})
		document.Connections = append(document.Connections, link("c4", "code", "error", "failed"))
	}
	ir, err := workflow.Compile(document, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, run := range result.NodeRuns {
		if run.NodeID == "code" {
			return run.Output
		}
	}
	t.Fatal("Code has no trace row")
	return nil
}

// In "Run Once for All Items" mode the code ran once over the whole batch, so
// a throw it tolerates is one error item, as in n8n: a node after it runs
// once, not once per input item. The item holds the error's message alone,
// under `error`, on the error output under continueErrorOutput and in the
// items' place otherwise.
func TestAnAllItemsCodeNodeToleratesAThrowWithOneErrorItem(t *testing.T) {
	for _, onError := range []string{"continueRegularOutput", "continueErrorOutput"} {
		output := runToleratedCode(t, nodes.CodeModeAllItems, "throw new Error('the batch broke')", onError)
		port := map[string]int{"continueRegularOutput": 0, "continueErrorOutput": 1}[onError]
		if len(output) <= port || len(output[port]) != 1 {
			t.Fatalf("%s: output %#v, want one error item on port %d", onError, output, port)
		}
		failed := output[port][0].JSON
		// The text is n8n's: the error's message and its line, with neither
		// the node's name the run's error starts with nor the error's type.
		if failed[engine.ErrorItemKey] != "the batch broke [line 1]" || len(failed) != 1 {
			t.Errorf("%s: the error item = %#v, want the message alone under %q", onError, failed, engine.ErrorItemKey)
		}
		if port == 1 && len(output[0]) != 0 {
			t.Errorf("%s: main = %#v, want it empty", onError, output[0])
		}
	}
}

// An error item's text is what n8n's Code node writes: the message and the
// line the code failed on, whatever the error's type, and "Unknown error"
// for an error with no message.
func TestACodeNodesErrorItemReadsAsN8nsDoes(t *testing.T) {
	for source, want := range map[string]string{
		"const order = undefined\nreturn [{ json: { id: order.id } }]": "Cannot read properties of undefined (reading 'id') [line 2]",
		"JSON.parse('{broken')": "Expected property name or '}' in JSON at position 1 (line 1 column 2) [line 1]",
		"throw new Error()":     "Unknown error [line 1]",
	} {
		output := runToleratedCode(t, nodes.CodeModeAllItems, source, "continueRegularOutput")
		if len(output[0]) != 1 || output[0][0].JSON[engine.ErrorItemKey] != want {
			t.Errorf("%q: error items %#v, want one reading %q", source, output[0], want)
		}
	}
}

// In "Run Once for Each Item" mode each item that threw is an error item of
// its own, and `error` is the message there too, with the line but not the
// item, as n8n writes it; on the error output the input item's fields sit
// beside it, as n8n merges them.
func TestAnEachItemCodeNodesErrorItemsCarryTheMessage(t *testing.T) {
	output := runToleratedCode(t, nodes.CodeModeEachItem, "if ($json.n === 2) throw new Error('two broke')\nreturn { json: { n: $json.n } }", "continueErrorOutput")
	if len(output) < 2 || len(output[0]) != 2 || len(output[1]) != 1 {
		t.Fatalf("output %#v, want two items on main and one on the error output", output)
	}
	failed := output[1][0].JSON
	if failed[engine.ErrorItemKey] != "two broke [line 1]" || failed["n"] != float64(2) {
		t.Errorf("the error item = %#v, want the message beside n = 2", failed)
	}
}

// failingEngine is a runtime whose every run fails with err.
type failingEngine struct{ err error }

func (runtime failingEngine) Run(context.Context, jsrun.Task) (jsrun.Result, error) {
	return jsrun.Result{}, runtime.err
}

// Only a failure of the code itself — a throw, code that does not parse, one
// of the code's limits — is the batch's, and so one error item under
// continue-on-fail and no retry. A failure of the server to run the code at
// all (a worker that crashed or could not start, a closed pool, the engine's
// own fault) is an ordinary node failure, which the runner retries and
// tolerates as it would any node's, as n8n's engine does.
func TestOnlyTheCodesOwnFailureIsTheBatchs(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		err   error
		batch bool
	}{
		{"a throw", &jsrun.ScriptError{Name: "Error", Message: "boom", Line: 1, ItemIndex: -1}, true},
		{"code that does not parse", &jsrun.SyntaxError{Message: "Unexpected token", Line: 1, Column: 3}, true},
		{"the time limit", &jsrun.LimitError{Detail: "code exceeded its 10s time limit", Cause: jsrun.ErrTimeLimit}, true},
		{"an invalid return", &jsrun.LimitError{Detail: "code returned a number", Cause: jsrun.ErrInvalidReturn}, true},
		{"the engine's fault", &jsrun.LimitError{Detail: "the JavaScript engine failed", Cause: jsrun.ErrEngineFault}, false},
		{"a closed pool", errors.New("the JavaScript worker pool is closed"), false},
		{"a worker that could not start", fmt.Errorf("start a JavaScript worker: %w", errors.New("exec format error")), false},
	} {
		executor := jsExecutor(t, nodes.JSCodeExecutorID, nodes.WithJSRunner(failingEngine{err: testCase.err}))
		_, err := executor.Execute(context.Background(), jsNode(nodes.JSCodeNodeType, map[string]any{"jsCode": "return items"}), threeItems(),
			engine.Request{TolerateItemFailures: true})
		var batch *engine.BatchFailure
		if errors.As(err, &batch) != testCase.batch || !errors.Is(err, testCase.err) {
			t.Errorf("%s: Execute() error = %#v, want a batch failure: %v, wrapping the runtime's error", testCase.name, err, testCase.batch)
		}
	}
}
