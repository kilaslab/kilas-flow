package nodes_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// The Stop and Error message is evaluated once (BUG-vqghpm). A message an
// expression produced is data: evaluating it again put `{{ $env.SECRET }}`
// from a webhook body into the run's error as the secret's value, and from
// there into the error workflow.
func TestStopAndErrorNeverEvaluatesAResolvedMessage(t *testing.T) {
	t.Parallel()

	executor := flowExecutor(t, nodes.StopAndErrorExecutorID)
	request := engine.Request{Env: map[string]string{"SECRET": "s3cr3t-value"}}
	input := workflow.NodeInput{"main": {{JSON: map[string]any{
		"reason": "{{ $env.SECRET }}",
		"error":  map[string]any{"message": "{{ $env.SECRET }}", "errorDescription": "see {{ $env.SECRET }}"},
	}}}}

	for name, parameters := range map[string]map[string]any{
		"an errorMessage expression": {"errorMessage": map[string]any{"mode": "expression", "value": "{{ $json.reason }}"}},
		"an errorObject expression":  {"errorObject": map[string]any{"mode": "expression", "value": "{{ $json.error }}"}},
		// An object the author wrote whose fields are expressions: each field
		// resolves to item data, which is not evaluated again.
		"an object's errorMessage expression": {"errorObject": map[string]any{
			"errorMessage": map[string]any{"mode": "expression", "value": "{{ $json.reason }}"},
		}},
		"an object's message expression": {"errorObject": map[string]any{
			"message": map[string]any{"mode": "expression", "value": "{{ $json.reason }}"},
		}},
		"an object's errorDescription expression": {"errorObject": map[string]any{
			"errorMessage":     "stopped",
			"errorDescription": map[string]any{"mode": "expression", "value": "{{ $json.reason }}"},
		}},
		"an expression returning JSON text": {"errorObject": map[string]any{
			"mode": "expression", "value": "{{ JSON.stringify({errorMessage: $json.reason}) }}",
		}},
		"an =-prefixed template with spaces": {"errorMessage": "  =  {{ $json.reason }}"},
		"JSON text reading the item":         {"errorObject": `{"errorMessage":"{{ $json.reason }}"}`},
	} {
		_, err := executor.Execute(context.Background(), flowNode(t, nodes.StopAndErrorNodeType, parameters), input, request)
		if err == nil {
			t.Fatalf("%s: Stop and Error succeeded", name)
		}
		if strings.Contains(err.Error(), "s3cr3t-value") {
			t.Errorf("%s: the run's error carries the secret, so a resolved message was evaluated again: %v", name, err)
		}
		if !strings.Contains(err.Error(), "{{ $env.SECRET }}") {
			t.Errorf("%s: error = %v, want the item's text verbatim", name, err)
		}
	}

	// The author's own templates still render, once: a plain string with a
	// template in it, and error-object JSON text with one inside.
	plain := workflow.NodeInput{"main": {{JSON: map[string]any{"order": "A-1"}}}}
	for name, parameters := range map[string]map[string]any{
		"a typed template":        {"errorMessage": "order {{ $json.order }} was blocked"},
		"a template in JSON text": {"errorObject": `{"message":"order {{ $json.order }} was blocked"}`},
		"an n8n-style = prefix":   {"errorMessage": "=order {{ $json.order }} was blocked"},
	} {
		_, err := executor.Execute(context.Background(), flowNode(t, nodes.StopAndErrorNodeType, parameters), plain, request)
		if err == nil || !strings.Contains(err.Error(), "order A-1 was blocked") {
			t.Errorf("%s: error = %v, want the author's template rendered", name, err)
		}
	}
	// And a rendered author template whose data holds braces stays data.
	braces := workflow.NodeInput{"main": {{JSON: map[string]any{"order": "{{ $env.SECRET }}"}}}}
	_, err := executor.Execute(context.Background(), flowNode(t, nodes.StopAndErrorNodeType,
		map[string]any{"errorMessage": "order {{ $json.order }} was blocked"}), braces, request)
	if err == nil || strings.Contains(err.Error(), "s3cr3t-value") || !strings.Contains(err.Error(), "order {{ $env.SECRET }} was blocked") {
		t.Errorf("error = %v, want the item's braces verbatim inside the author's message", err)
	}
}

// The error object is read only when the message is empty, so a template in
// an object the node never shows cannot fail it; and a message that comes
// out a number or a boolean is that text, not nothing.
func TestStopAndErrorReadsTheObjectOnlyWithoutAMessageAndKeepsScalarMessages(t *testing.T) {
	t.Parallel()

	executor := flowExecutor(t, nodes.StopAndErrorExecutorID)
	input := workflow.NodeInput{"main": {{JSON: map[string]any{"id": float64(7), "paid": false}}}}
	for name, testCase := range map[string]struct {
		parameters map[string]any
		want       string
	}{
		"an unused object with a broken template": {map[string]any{
			"errorMessage": "fixed", "errorObject": `{"errorMessage":"{{ $('Nope').item.json.x }}"}`,
		}, "fixed"},
		"an unused object with a syntax error": {map[string]any{
			"errorMessage": "fixed", "errorObject": map[string]any{"errorMessage": "{{ ) }}"},
		}, "fixed"},
		"a number":  {map[string]any{"errorMessage": map[string]any{"mode": "expression", "value": "{{ $json.id }}"}}, "7"},
		"a boolean": {map[string]any{"errorMessage": map[string]any{"mode": "expression", "value": "{{ $json.paid }}"}}, "false"},
		// The shape the importer writes for n8n's object mode.
		"an imported object": {map[string]any{
			"errorObject": `{"errorMessage":{"mode":"expression","value":"{{ $json.id }}"},"errorDescription":"declined"}`,
		}, "7: declined"},
		"a number in the object": {map[string]any{
			"errorObject": map[string]any{"errorMessage": map[string]any{"mode": "expression", "value": "{{ $json.id }}"}, "errorDescription": "declined"},
		}, "7: declined"},
	} {
		_, err := executor.Execute(context.Background(), flowNode(t, nodes.StopAndErrorNodeType, testCase.parameters), input, engine.Request{})
		if err == nil || err.Error() != testCase.want {
			t.Errorf("%s: error = %v, want %q", name, err, testCase.want)
		}
	}
}
