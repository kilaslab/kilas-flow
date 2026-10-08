package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// These tests are the read-only evaluator's contract, asserted where its two
// bounds actually live: the document's own budget, and the runtime's
// environment allowlist rather than the process environment.

// evaluationRecord is one finished run whose trace holds two node outputs: a
// first node that fetched a customer, and a second that summed them. The
// second node's input is stored too, which is what `$input` reads.
func evaluationRecord() execution.Record {
	return execution.Record{
		ID:      "exec_1",
		Trigger: execution.TriggerManual,
		NodeRuns: []execution.NodeRun{
			{
				NodeID: "fetch", Status: execution.StatusSucceeded,
				Output: json.RawMessage(`[[{"json":{"customer":"Ada","total":41}}]]`),
			},
			{
				NodeID: "sum", Status: execution.StatusSucceeded,
				Input:  json.RawMessage(`{"main":[{"json":{"customer":"Ada","total":41}}]}`),
				Output: json.RawMessage(`[[{"json":{"sum":42}}]]`),
			},
			// A node the branch never reached: it ran, and produced nothing.
			{
				NodeID: "unreached", Status: execution.StatusSkipped,
				Output: json.RawMessage(`[[]]`),
			},
		},
	}
}

// evaluationDocument names the trace's nodes the way a workflow's own
// expressions address them.
func evaluationDocument(settings map[string]any) workflow.Document {
	if settings == nil {
		settings = map[string]any{}
	}
	return workflow.Document{
		ID: "wf_1", Name: "Orders",
		Nodes: []workflow.Node{
			{ID: "fetch", Name: "Fetch Customer", Settings: map[string]any{}},
			{ID: "sum", Name: "Sum", Settings: map[string]any{}},
			{ID: "unreached", Name: "Not Reached", Settings: map[string]any{}},
		},
		Settings: settings,
	}
}

func evaluate(t *testing.T, expression, nodeID string) engine.ExpressionEvaluation {
	t.Helper()
	service := engine.NewEvaluationServiceForTest(time.Minute, "Asia/Jakarta", map[string]string{"REGION": "eu-west-1"})
	result, err := service.EvaluateExpression(t.Context(), evaluationRecord(), evaluationDocument(nil), expression, evalTarget(nodeID))
	if err != nil {
		t.Fatalf("EvaluateExpression(%q, %q) error = %v", expression, nodeID, err)
	}
	return result
}

// The value and its declared type are one answer: the type is read off the
// encoded bytes, so they cannot disagree about what the caller received.
func TestEvaluateExpressionAnswersTheValueAndItsShape(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name       string
		expression string
		nodeID     string
		wantValue  string
		wantType   string
	}{
		// $json is the item the node received, as it was for the node's own
		// parameters, never what the node produced from it.
		{"the named node's input item is $json", "{{ $json.total }}", "sum", "41", "number"},
		{"the named node's output is not $json", "{{ $json.sum }}", "sum", "null", "null"},
		{"a bare expression body is the expression", "$json.customer", "sum", `"Ada"`, "string"},
		{"an earlier node is read by name", `{{ $('Fetch Customer').json.customer }}`, "", `"Ada"`, "string"},
		{"an earlier node is read by name at a node", `{{ $('Fetch Customer').json.customer }}`, "sum", `"Ada"`, "string"},
		{"the named node's stored input is $input", "{{ $input.main[0].json.total }}", "sum", "41", "number"},
		{"arithmetic follows the document grammar", "{{ $json.total + 1 }}", "sum", "42", "number"},
		{"a whole item is an object", "{{ $json }}", "sum", `{"customer":"Ada","total":41}`, "object"},
		{"a template mixing text answers a string", "customer {{ $json.customer }}", "sum", `"customer Ada"`, "string"},
		// A trigger received nothing, and its parameters read an empty item.
		{"a node with no input reads as an empty item", "{{ $json }}", "fetch", `{}`, "object"},
		// A node that was reached with nothing is an answer, not a refusal: the
		// untaken arm of a branch reads as an empty item, which is what the
		// runtime's `$json` is for an item with no fields.
		{"a node that received nothing reads as an empty item", "{{ $json }}", "unreached", `{}`, "object"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			result := evaluate(t, testCase.expression, testCase.nodeID)
			if string(result.Value) != testCase.wantValue || result.Type != testCase.wantType {
				t.Errorf("result = %s (%s), want %s (%s)", result.Value, result.Type, testCase.wantValue, testCase.wantType)
			}
		})
	}
}

// $env is the runtime's allowlist, never os.Environ: an exposed name resolves
// to the value the instance published, and a name that only the process holds —
// a database DSN, a master key, PATH — is refused with the reason saying so.
func TestEvaluateExpressionReadsTheAllowlistedEnvironmentOnly(t *testing.T) {
	t.Parallel()

	service := engine.NewEvaluationServiceForTest(time.Minute, "", map[string]string{"REGION": "eu-west-1"})
	exposed, err := service.EvaluateExpression(t.Context(), evaluationRecord(), evaluationDocument(nil), "{{ $env.REGION }}", evalTarget(""))
	if err != nil {
		t.Fatalf("EvaluateExpression($env.REGION) error = %v", err)
	}
	if string(exposed.Value) != `"eu-west-1"` {
		t.Errorf("$env.REGION = %s, want the allowlisted value", exposed.Value)
	}

	// Asserted only when the process actually has the variable, so the test
	// says what it proved on a host that exports nothing.
	if _, held := os.LookupEnv("PATH"); !held {
		t.Skip("PATH is not set in this process, so there is nothing to withhold")
	}
	_, err = service.EvaluateExpression(t.Context(), evaluationRecord(), evaluationDocument(nil), "{{ $env.PATH }}", evalTarget(""))
	if err == nil {
		t.Fatal("EvaluateExpression($env.PATH) = nil error, want the process environment to be unreachable")
	}
}

// A node the trace has no row for is a mistake the caller is told about, rather
// than an empty item that reads exactly like a node which produced nothing.
func TestEvaluateExpressionRefusesANodeTheTraceDoesNotHave(t *testing.T) {
	t.Parallel()

	service := engine.NewEvaluationServiceForTest(time.Minute, "", nil)
	_, err := service.EvaluateExpression(t.Context(), evaluationRecord(), evaluationDocument(nil), "{{ $json.x }}", evalTarget("nobody"))
	if !errors.Is(err, engine.ErrExpressionNodeUnknown) {
		t.Fatalf("EvaluateExpression(unknown node) error = %v, want ErrExpressionNodeUnknown", err)
	}
}

// The evaluation runs under the budget a node of this revision would be given:
// a workflow whose budget is already spent answers the deadline rather than
// holding the request open.
func TestEvaluateExpressionAnswersTheNodeDeadline(t *testing.T) {
	t.Parallel()

	// One nanosecond: a context that has expired before the evaluator is even
	// started, which is what makes the answer the deadline rather than a race
	// against how fast the machine is.
	service := engine.NewEvaluationServiceForTest(time.Nanosecond, "", nil)
	_, err := service.EvaluateExpression(t.Context(), evaluationRecord(), evaluationDocument(nil), "{{ $json.sum }}", evalTarget("sum"))
	if !errors.Is(err, engine.ErrExpressionDeadline) {
		t.Fatalf("EvaluateExpression(spent budget) error = %v, want ErrExpressionDeadline", err)
	}
}

// The node's own timeoutSeconds is read from the revision, so a stored setting
// the runtime would refuse is refused here too rather than silently ignored.
func TestEvaluateExpressionReadsTheNodesOwnTimeout(t *testing.T) {
	t.Parallel()

	document := evaluationDocument(nil)
	document.Nodes[1].Settings = map[string]any{"timeoutSeconds": "soon"}
	service := engine.NewEvaluationServiceForTest(time.Minute, "", nil)
	_, err := service.EvaluateExpression(t.Context(), evaluationRecord(), document, "{{ $json.sum }}", evalTarget("sum"))
	if err == nil {
		t.Fatal("EvaluateExpression(malformed node timeout) = nil error, want the revision's own bound to be read")
	}
}

// `$('IF').all()` answers what it answered in the run: the branch the named
// node is connected to, here IF's false branch through a No Op, and IF's
// first output for a node IF does not feed.
func TestEvaluateExpressionReadsTheBranchTheNodeIsConnectedTo(t *testing.T) {
	t.Parallel()

	link := func(id, source, port, target string) workflow.Connection {
		return workflow.Connection{ID: id, Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: source, Port: port}, Target: workflow.Endpoint{NodeID: target, Port: "main"}}
	}
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_branch", Name: "Branches", Settings: map[string]any{},
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "if", Name: "IF", Type: "kilasflow.if", TypeVersion: workflow.V(1), Parameters: map[string]any{"conditions": []any{
				map[string]any{"field": "paid", "operator": "equals", "value": "yes"},
			}}},
			{ID: "noop", Name: "No Op", Type: "kilasflow.noOp", TypeVersion: workflow.V(1)},
			{ID: "unpaid", Name: "Unpaid", Type: "kilasflow.noOp", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{link("c1", "manual", "main", "if"), link("c2", "if", "false", "noop"), link("c3", "noop", "main", "unpaid")},
	}
	record := execution.Record{
		ID: "exec_branch", Trigger: execution.TriggerManual,
		NodeRuns: []execution.NodeRun{
			{NodeID: "if", Status: execution.StatusSucceeded, Output: json.RawMessage(`[[{"json":{"id":"o1"}},{"json":{"id":"o2"}}],[{"json":{"id":"o3"}}]]`)},
			{NodeID: "noop", Status: execution.StatusSucceeded, Output: json.RawMessage(`[[{"json":{"id":"o3"}}]]`)},
			{NodeID: "unpaid", Status: execution.StatusSucceeded, Output: json.RawMessage(`[[{"json":{"id":"o3"}}]]`)},
		},
	}
	service := engine.NewEvaluationServiceForTest(time.Minute, "", nil)
	service.SetCatalogForTest(testCatalog(t))
	for nodeID, want := range map[string]string{"unpaid": `"o3"`, "": `"o1,o2"`} {
		result, err := service.EvaluateExpression(t.Context(), record, document, "{{ $('IF').all().map(i => i.json.id).join() }}", evalTarget(nodeID))
		if err != nil || string(result.Value) != want {
			t.Errorf("at %q: $('IF').all() = %s, %v; want %s", nodeID, result.Value, err, want)
		}
	}
}

// evalTarget is a node, or the whole trace, at its default run and item.
func evalTarget(nodeID string) engine.ExpressionTarget {
	return engine.ExpressionTarget{NodeID: nodeID}
}

// storedTrace is a runner's result as the service persists it: each row's
// input and output encoded, a pruned delivery marked skipped, in sequence.
func storedTrace(t *testing.T, id string, runs []engine.NodeRun) execution.Record {
	t.Helper()
	record := execution.Record{ID: id, Trigger: execution.TriggerManual}
	for index, run := range runs {
		input, err := json.Marshal(run.Input)
		if err != nil {
			t.Fatalf("encode %s input: %v", run.NodeID, err)
		}
		output, err := json.Marshal(run.Output)
		if err != nil {
			t.Fatalf("encode %s output: %v", run.NodeID, err)
		}
		status := execution.StatusSucceeded
		if run.Skipped {
			status = execution.StatusSkipped
		}
		record.NodeRuns = append(record.NodeRuns, execution.NodeRun{
			NodeID: run.NodeID, Attempt: max(run.Attempt, 1), RunIndex: run.RunIndex, Sequence: index + 1,
			Status: status, Input: input, Output: output,
		})
	}
	return record
}

// A node is evaluated as its parameters were, and every answer comes from one
// run. Tag is fed by three branches: Left delivers Start's three items (run
// 0), Right delivers the last of them (run 1), and Empty delivers nothing,
// which leaves a pruned row after both. The runtime's own answer for each run
// and item is recorded while the workflow runs, and the evaluator has to give
// the same one from the stored trace: `$json` is the input item, not Tag's
// output, and `$runIndex`, `$itemIndex`, `$input` and `$('Start').item` all
// describe the run and the item that were picked.
func TestEvaluateExpressionAtANodeAnswersWhatItsParametersSaw(t *testing.T) {
	t.Parallel()

	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"))
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_eval_runs", Name: "Three deliveries",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "left", Name: "Left", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 200, Y: 0}},
			{ID: "right", Name: "Right", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 200, Y: 300}},
			{ID: "empty", Name: "Empty", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 200, Y: 600}},
			{ID: "tag", Name: "Tag", Type: "test.step", TypeVersion: workflow.V(1), Position: workflow.Position{X: 400, Y: 300}},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "left"), mainEdge("c2", "start", "main", "right"), mainEdge("c3", "start", "main", "empty"),
			mainEdge("c4", "left", "main", "tag"), mainEdge("c5", "right", "main", "tag"), mainEdge("c6", "empty", "main", "tag"),
		},
		Settings: map[string]any{},
	}
	ir := compileDoc(t, catalog, document)

	templates := []string{
		"{{ $json }}",
		"{{ $runIndex }}",
		"{{ $itemIndex }}",
		"{{ $input.all().map(i => i.json.u).join() }}",
		"{{ $('Start').item.json.u }}",
	}
	type position struct{ run, item int }
	saw := map[position][]string{}
	executors := engine.NewRegistry()
	register := func(id string, executor engine.ExecutorFunc) {
		if err := executors.Register(id, executor); err != nil {
			t.Fatalf("Register(%s) error = %v", id, err)
		}
	}
	register("test.start", func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
		return workflow.NodeOutput{{{JSON: map[string]any{"u": "a"}}, {JSON: map[string]any{"u": "b"}}, {JSON: map[string]any{"u": "c"}}}}, nil
	})
	register("test.step", func(_ context.Context, node workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
		items := input["main"]
		switch node.ID {
		case "right":
			return workflow.NodeOutput{items[len(items)-1:]}, nil
		case "empty":
			return workflow.NodeOutput{{}}, nil
		case "tag":
			tagged := make([]workflow.Item, len(items))
			for index, item := range items {
				scope := request.ExpressionContext(item, input, index)
				for _, template := range templates {
					value, err := expression.Evaluate(template, scope)
					if err != nil {
						t.Errorf("the run itself could not evaluate %s at run %d item %d: %v", template, request.RunIndex, index, err)
					}
					encoded, _ := json.Marshal(value)
					saw[position{request.RunIndex, index}] = append(saw[position{request.RunIndex, index}], string(encoded))
				}
				tagged[index] = workflow.Item{JSON: map[string]any{"tagged": item.JSON["u"]}, Paired: item.Paired}
			}
			return workflow.NodeOutput{tagged}, nil
		}
		return workflow.NodeOutput{items}, nil
	})
	result, err := engine.NewRunner(executors).Run(t.Context(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(saw) != 4 {
		t.Fatalf("the run evaluated Tag at %d run/item positions, want 4 (three items in run 0, one in run 1)", len(saw))
	}
	record := storedTrace(t, "exec_runs", result.NodeRuns)
	if last := record.NodeRuns[len(record.NodeRuns)-1]; last.NodeID != "tag" || last.Status != execution.StatusSkipped {
		t.Fatalf("the trace ends with %s (%s), want Tag's pruned delivery after its two runs", last.NodeID, last.Status)
	}

	service := engine.NewEvaluationServiceForTest(time.Minute, "", nil)
	service.SetCatalogForTest(catalog)
	answer := func(target engine.ExpressionTarget) []string {
		t.Helper()
		var answers []string
		for _, template := range templates {
			evaluated, err := service.EvaluateExpression(t.Context(), record, document, template, target)
			if err != nil {
				t.Fatalf("EvaluateExpression(%s, %+v) error = %v", template, target, err)
			}
			answers = append(answers, string(evaluated.Value))
		}
		return answers
	}
	for at, want := range saw {
		run, item := at.run, at.item
		if got := answer(engine.ExpressionTarget{NodeID: "tag", Run: &run, Item: &item}); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("run %d item %d answered %v, want what the run saw: %v", run, item, got, want)
		}
	}
	// No run and no item: the last run that received items, at its first
	// item. The pruned delivery after it received nothing and decides nothing.
	if got, want := answer(evalTarget("tag")), saw[position{1, 0}]; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("the default answered %v, want run 1 item 0: %v", got, want)
	}
}

// A retried attempt is part of the run its last attempt decided, not a run of
// its own: a node that failed once and then succeeded ran once, as `$runIndex`
// counts.
func TestEvaluateExpressionCountsARetryAsOneRun(t *testing.T) {
	t.Parallel()

	record := execution.Record{ID: "exec_retry", Trigger: execution.TriggerManual, NodeRuns: []execution.NodeRun{
		{NodeID: "flaky", Attempt: 1, Status: execution.StatusFailed, Input: json.RawMessage(`{"main":[{"json":{"try":1}}]}`)},
		{NodeID: "flaky", Attempt: 2, Status: execution.StatusSucceeded, Input: json.RawMessage(`{"main":[{"json":{"try":2}}]}`),
			Output: json.RawMessage(`[[{"json":{"ok":true}}]]`)},
	}}
	document := workflow.Document{ID: "wf_retry", Nodes: []workflow.Node{{ID: "flaky", Name: "Flaky"}}, Settings: map[string]any{}}
	service := engine.NewEvaluationServiceForTest(time.Minute, "", nil)

	result, err := service.EvaluateExpression(t.Context(), record, document, "{{ $runIndex + ':' + $json.try }}", evalTarget("flaky"))
	if err != nil || string(result.Value) != `"0:2"` {
		t.Fatalf("at the retried node = %s, %v; want run 0 read at its last attempt", result.Value, err)
	}
	one := 1
	if _, err := service.EvaluateExpression(t.Context(), record, document, "{{ $json }}", engine.ExpressionTarget{NodeID: "flaky", Run: &one}); err == nil || !strings.Contains(err.Error(), "ran 1 time") {
		t.Fatalf("run 1 of a node retried once = %v, want a refusal saying it ran once", err)
	}
}

// A run or an item the node did not have is refused with what it did have,
// and a run or an item without a node has nothing to pick from.
func TestEvaluateExpressionRefusesARunOrItemTheNodeDidNotHave(t *testing.T) {
	t.Parallel()

	service := engine.NewEvaluationServiceForTest(time.Minute, "", nil)
	one, two := 1, 2
	for _, testCase := range []struct {
		name   string
		target engine.ExpressionTarget
		want   string
	}{
		{"a run past the last", engine.ExpressionTarget{NodeID: "sum", Run: &one}, "ran 1 time"},
		{"an item past the last", engine.ExpressionTarget{NodeID: "sum", Item: &two}, "received 1 item"},
		{"a run without a node", engine.ExpressionTarget{Run: &one}, "a node"},
		{"an item without a node", engine.ExpressionTarget{Item: &one}, "a node"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := service.EvaluateExpression(t.Context(), evaluationRecord(), evaluationDocument(nil), "{{ $json }}", testCase.target)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("EvaluateExpression(%+v) error = %v, want one containing %q", testCase.target, err, testCase.want)
			}
		})
	}
}
