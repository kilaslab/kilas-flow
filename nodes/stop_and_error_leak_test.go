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
