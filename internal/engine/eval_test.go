package engine_test

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/execution"
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
	result, err := service.EvaluateExpression(t.Context(), evaluationRecord(), evaluationDocument(nil), expression, nodeID)
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
		{"the named node's item is $json", "{{ $json.sum }}", "sum", "42", "number"},
		{"a bare expression body is the expression", "$json.customer", "fetch", `"Ada"`, "string"},
		{"an earlier node is read by name", `{{ $('Fetch Customer').json.customer }}`, "", `"Ada"`, "string"},
		{"the named node's stored input is $input", "{{ $input.main[0].json.total }}", "sum", "41", "number"},
		{"arithmetic follows the document grammar", "{{ $json.total + 1 }}", "fetch", "42", "number"},
		{"a whole item is an object", "{{ $json }}", "fetch", `{"customer":"Ada","total":41}`, "object"},
		{"a template mixing text answers a string", "customer {{ $json.customer }}", "fetch", `"customer Ada"`, "string"},
		// A node that ran and produced nothing is an answer, not a refusal: the
		// untaken arm of a branch reads as an empty item, which is what the
		// runtime's `$json` is for an item with no fields.
		{"a node that produced nothing reads as an empty item", "{{ $json }}", "unreached", `{}`, "object"},
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
	exposed, err := service.EvaluateExpression(t.Context(), evaluationRecord(), evaluationDocument(nil), "{{ $env.REGION }}", "")
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
	_, err = service.EvaluateExpression(t.Context(), evaluationRecord(), evaluationDocument(nil), "{{ $env.PATH }}", "")
	if err == nil {
		t.Fatal("EvaluateExpression($env.PATH) = nil error, want the process environment to be unreachable")
	}
}

// A node the trace has no row for is a mistake the caller is told about, rather
// than an empty item that reads exactly like a node which produced nothing.
func TestEvaluateExpressionRefusesANodeTheTraceDoesNotHave(t *testing.T) {
	t.Parallel()

	service := engine.NewEvaluationServiceForTest(time.Minute, "", nil)
	_, err := service.EvaluateExpression(t.Context(), evaluationRecord(), evaluationDocument(nil), "{{ $json.x }}", "nobody")
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
	_, err := service.EvaluateExpression(t.Context(), evaluationRecord(), evaluationDocument(nil), "{{ $json.sum }}", "sum")
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
	_, err := service.EvaluateExpression(t.Context(), evaluationRecord(), document, "{{ $json.sum }}", "sum")
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
		result, err := service.EvaluateExpression(t.Context(), record, document, "{{ $('IF').all().map(i => i.json.id).join() }}", nodeID)
		if err != nil || string(result.Value) != want {
			t.Errorf("at %q: $('IF').all() = %s, %v; want %s", nodeID, result.Value, err, want)
		}
	}
}
