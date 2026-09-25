package nodes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// The HTTP Request, Workflow and Calculator tools hand the model's $fromAI
// arguments to the author's expressions as data. These tests stand a probe in
// for the model and send the four shapes that used to become expression
// syntax once spliced into the author's template: a verbatim expression
// marker, template text around a call, a call spliced into code, and a `}}`
// literal that confused segmenting. Each one has to be refused or arrive as
// the literal text the model sent — never as `exec-1`, which is what
// `$execution.id` evaluates to in these runs.

// probeOutcome is what one tool call came to.
type probeOutcome struct {
	failed  bool
	failure string
	result  string
}

// probeTool runs one agent turn in which the model calls the tool named
// "probe" with the given arguments, then answers.
func probeTool(t *testing.T, descriptor map[string]any, arguments map[string]any, workflows engine.WorkflowInvoker) probeOutcome {
	t.Helper()
	encoded, err := json.Marshal(arguments)
	if err != nil {
		t.Fatalf("encode arguments: %v", err)
	}
	quoted, _ := json.Marshal(string(encoded))
	provider := scriptProvider(t, []string{
		assistantToolCalls(`{"name":"probe","arguments":` + string(quoted) + `}`),
		assistantAnswer("done"),
	})
	var mu sync.Mutex
	var outcome probeOutcome
	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	_, err = executor.Execute(context.Background(), agentIR(t, map[string]any{"prompt": "Probe."}), workflow.NodeInput{
		"main":  {{JSON: map[string]any{}}},
		"model": {modelItem(provider.URL)},
		"tools": {toolItem(descriptor)},
	}, engine.Request{
		Credentials: agentCredentials(), Workflows: workflows, Execution: agentExecution(),
		Events: func(event engine.NodeEvent) {
			var detail ai.Event
			_ = json.Unmarshal(event.Detail, &detail)
			mu.Lock()
			defer mu.Unlock()
			switch event.Name {
			case string(ai.EventToolFailed):
				outcome.failed = true
				outcome.failure = detail.Error
			case string(ai.EventToolCompleted):
				_ = json.Unmarshal(detail.Detail, &outcome.result)
			}
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return outcome
}

// expr writes an expression marker.
func expr(template string) map[string]any {
	return map[string]any{"mode": "expression", "value": template}
}

// bodyRecorder is an upstream that keeps every JSON body it was sent.
type bodyRecorder struct {
	mu     sync.Mutex
	bodies []map[string]any
	server *httptest.Server
}

func newBodyRecorder(t *testing.T) *bodyRecorder {
	t.Helper()
	recorder := &bodyRecorder{}
	recorder.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		recorder.mu.Lock()
		recorder.bodies = append(recorder.bodies, body)
		recorder.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(recorder.server.Close)
	return recorder
}

func (recorder *bodyRecorder) sent() []map[string]any {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]map[string]any(nil), recorder.bodies...)
}

// httpProbe is an HTTP Request tool that posts the given body fields.
func httpProbe(url string, fields map[string]any) map[string]any {
	return map[string]any{
		"kind": "tool", "name": "probe", "description": "Probe.", "nodeName": "Probe",
		"parameters": map[string]any{
			"method": "POST", "url": url,
			"sendBody": true, "bodyType": "json", "bodyFields": fields,
		},
		"credentials": map[string]any{},
	}
}

// workflowProbe is a Workflow tool whose static inputs are the given fields.
func workflowProbe(fields map[string]any) map[string]any {
	return map[string]any{
		"kind": "workflow", "name": "probe", "description": "Probe.", "nodeName": "Probe",
		"workflowId": "wf_child",
		"parameters": map[string]any{"workflowId": "wf_child", "workflowInputs": fields},
	}
}

// injectionProbe is one shape a model-supplied value can take. out is the
// template the author wrote for the field the tool sends; want is the literal
// the field must carry, or "" when the call must be refused.
type injectionProbe struct {
	name      string
	out       any
	arguments map[string]any
	want      string
}

func injectionProbes() []injectionProbe {
	verbatim := map[string]any{"mode": "expression", "value": "{{ $execution.id }}"}
	return []injectionProbe{
		{
			name:      "verbatim marker for a string-typed call in an expression",
			out:       expr("{{ $fromAI('note', 'a note') }}"),
			arguments: map[string]any{"note": verbatim},
		},
		{
			name:      "verbatim marker for a string-typed call in a plain string",
			out:       "$fromAI('note', 'a note')",
			arguments: map[string]any{"note": verbatim},
		},
		{
			name:      "template text around a call",
			out:       expr("Note: {{ $fromAI('note', 'a note') }}"),
			arguments: map[string]any{"note": "{{ $execution.id }}"},
			want:      "Note: {{ $execution.id }}",
		},
		{
			name:      "a call spliced into code",
			out:       expr("{{ $fromAI('note', 'a note').toUpperCase() }}"),
			arguments: map[string]any{"note": "$execution.id + 'x'"},
			want:      "$EXECUTION.ID + 'X'",
		},
		{
			name:      "a literal whose }} confuses segmenting",
			out:       expr("{{ '}}' + $fromAI('note', 'a note') }}"),
			arguments: map[string]any{"note": "$execution.id"},
			want:      "}}$execution.id",
		},
	}
}

func TestHTTPToolTreatsFromAIArgumentsAsData(t *testing.T) {
	t.Parallel()

	for _, probe := range injectionProbes() {
		t.Run(probe.name, func(t *testing.T) {
			t.Parallel()
			upstream := newBodyRecorder(t)
			outcome := probeTool(t, httpProbe(upstream.server.URL, map[string]any{"out": probe.out}), probe.arguments, nil)
			sent := upstream.sent()
			for _, body := range sent {
				if encoded, _ := json.Marshal(body); strings.Contains(string(encoded), "exec-1") {
					t.Fatalf("the model's value was evaluated: the request carried %s", encoded)
				}
			}
			if probe.want == "" {
				if !outcome.failed || len(sent) != 0 {
					t.Fatalf("outcome = %+v with %d requests sent, want the call refused", outcome, len(sent))
				}
				return
			}
			if outcome.failed || len(sent) != 1 {
				t.Fatalf("outcome = %+v with %d requests sent, want one request", outcome, len(sent))
			}
			if got := sent[0]["out"]; got != probe.want {
				t.Errorf("out = %#v, want the literal %q", got, probe.want)
			}
		})
	}
}

func TestWorkflowToolTreatsFromAIArgumentsAsData(t *testing.T) {
	t.Parallel()

	for _, probe := range injectionProbes() {
		t.Run(probe.name, func(t *testing.T) {
			t.Parallel()
			workflows := &stubWorkflows{items: []workflow.Item{{JSON: map[string]any{"ok": true}}}}
			outcome := probeTool(t, workflowProbe(map[string]any{"out": probe.out}), probe.arguments, workflows)
			workflows.mu.Lock()
			calls := append([]engine.WorkflowCall(nil), workflows.calls...)
			workflows.mu.Unlock()
			for _, call := range calls {
				if encoded, _ := json.Marshal(call.Items); strings.Contains(string(encoded), "exec-1") {
					t.Fatalf("the model's value was evaluated: the sub-workflow received %s", encoded)
				}
			}
			if probe.want == "" {
				if !outcome.failed || len(calls) != 0 {
					t.Fatalf("outcome = %+v with %d sub-workflow calls, want the call refused", outcome, len(calls))
				}
				return
			}
			if outcome.failed || len(calls) != 1 {
				t.Fatalf("outcome = %+v with %d sub-workflow calls, want one", outcome, len(calls))
			}
			if got := calls[0].Items[0].JSON["out"]; got != probe.want {
				t.Errorf("out = %#v, want the literal %q", got, probe.want)
			}
		})
	}
}

// legitimateFields is a parameter set that exercises every legitimate use:
// typed values in expressions and plain strings, defaults in both, text
// around a call, and the lowercase spelling an imported workflow carries.
func legitimateFields() map[string]any {
	return map[string]any{
		"count":   expr("{{ $fromAI('count', 'how many', 'number') }}"),
		"flag":    "$fromAI('flag', 'whether', 'boolean')",
		"meta":    expr("{{ $fromAI('meta', 'extra', 'json') }}"),
		"label":   expr("Order {{ $fromAI('label', 'the label') }} is ready"),
		"doubled": expr("{{ $fromAI('count', 'how many', 'number') * 2 }}"),
		"limit":   expr("{{ $fromAI('limit', 'max rows', 'number', 10) }}"),
		"plan":    "$fromAI('plan', 'the plan', 'string', 'free')",
		"city":    expr("{{ $fromai('city', 'the city') }}"),
	}
}

func legitimateArguments() map[string]any {
	return map[string]any{
		"count": 3.0, "flag": true, "meta": map[string]any{"tags": []any{"a", "b"}},
		"label": "A-1", "city": "Utrecht",
	}
}

func checkLegitimate(t *testing.T, got map[string]any) {
	t.Helper()
	want := map[string]any{
		"count": 3.0, "flag": true, "meta": map[string]any{"tags": []any{"a", "b"}},
		"label": "Order A-1 is ready", "doubled": 6.0, "limit": 10.0, "plan": "free", "city": "Utrecht",
	}
	for key, value := range want {
		if !reflect.DeepEqual(got[key], value) {
			t.Errorf("%s = %#v, want %#v", key, got[key], value)
		}
	}
}

func TestHTTPToolKeepsLegitimateFromAIValues(t *testing.T) {
	t.Parallel()

	upstream := newBodyRecorder(t)
	outcome := probeTool(t, httpProbe(upstream.server.URL, legitimateFields()), legitimateArguments(), nil)
	sent := upstream.sent()
	if outcome.failed || len(sent) != 1 {
		t.Fatalf("outcome = %+v with %d requests sent, want one request", outcome, len(sent))
	}
	checkLegitimate(t, sent[0])
}

func TestWorkflowToolKeepsLegitimateFromAIValues(t *testing.T) {
	t.Parallel()

	workflows := &stubWorkflows{items: []workflow.Item{{JSON: map[string]any{"ok": true}}}}
	outcome := probeTool(t, workflowProbe(legitimateFields()), legitimateArguments(), workflows)
	if outcome.failed || len(workflows.calls) != 1 {
		t.Fatalf("outcome = %+v with %d sub-workflow calls, want one", outcome, len(workflows.calls))
	}
	checkLegitimate(t, workflows.calls[0].Items[0].JSON)
}

// A required argument the model left out is refused by name before anything
// is sent, as it was when the values were spliced.
func TestFromAIToolsRefuseAMissingRequiredArgument(t *testing.T) {
	t.Parallel()

	upstream := newBodyRecorder(t)
	outcome := probeTool(t, httpProbe(upstream.server.URL, map[string]any{"q": expr("{{ $fromAI('q', 'the query') }}")}), map[string]any{}, nil)
	if !outcome.failed || !strings.Contains(outcome.failure, `"q"`) || len(upstream.sent()) != 0 {
		t.Errorf("HTTP tool outcome = %+v with %d requests, want a refusal naming q", outcome, len(upstream.sent()))
	}

	workflows := &stubWorkflows{}
	outcome = probeTool(t, workflowProbe(map[string]any{"q": expr("{{ $fromAI('q', 'the query') }}")}), map[string]any{}, workflows)
	if !outcome.failed || !strings.Contains(outcome.failure, `"q"`) || len(workflows.calls) != 0 {
		t.Errorf("Workflow tool outcome = %+v with %d calls, want a refusal naming q", outcome, len(workflows.calls))
	}
}

// The Calculator tool resolves its expression parameter the same way before
// the arithmetic runs, so a model value never reaches the expression evaluator
// as source either.
func TestCalculatorToolTreatsFromAIArgumentsAsData(t *testing.T) {
	t.Parallel()

	calculator := func(template any) map[string]any {
		return map[string]any{
			"kind": "calculator", "name": "probe", "description": "Arithmetic.", "nodeName": "Calc",
			"parameters": map[string]any{"expression": template},
		}
	}
	// `$execution.id.length` is 6: evaluated, the arithmetic would be 6 + 1.
	outcome := probeTool(t, calculator(expr("{{ $fromAI('a', 'the operand') }} + 1")), map[string]any{"a": "{{ $execution.id.length }}"}, nil)
	if !outcome.failed {
		t.Errorf("outcome = %+v, want the literal text refused as arithmetic rather than evaluated", outcome)
	}
	outcome = probeTool(t, calculator(expr("{{ $fromAI('a', 'the operand') }} + 1")), map[string]any{"a": "2"}, nil)
	if outcome.failed || outcome.result != `{"result":3}` {
		t.Errorf("outcome = %+v, want 2 + 1 to evaluate to 3", outcome)
	}
}
