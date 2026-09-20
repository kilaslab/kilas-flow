package engine_test

import (
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/expression"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// TestExpressionContextCarriesEveryRootItPromises pins the mapping between the
// per-run Request and the roots an expression can read.
//
// Every field here was dropped at some point, and each drop was invisible: the
// expression resolved to an empty string rather than failing. `$workflow.*` was
// never set at all, `$execution.resumeUrl` was dropped when the links moved
// into the request, and `$now` read UTC because no timezone reached the
// context.
func TestExpressionContextCarriesEveryRootItPromises(t *testing.T) {
	t.Parallel()

	request := engine.Request{
		Execution: engine.ExecutionContext{
			ID:          "exec_1",
			Mode:        "manual",
			ResumeURL:   "https://host.example/resume/tok_1",
			ApprovalURL: "https://host.example/approve/tok_1",
		},
		Workflow: expression.WorkflowContext{
			ID:       "wf_1",
			Name:     "Orders",
			Active:   true,
			Timezone: "Asia/Jakarta",
		},
		Env: map[string]string{"REGION": "eu-west-1"},
	}

	ctx := request.ExpressionContext(workflow.Item{JSON: map[string]any{"id": "item_1"}}, nil, 0)
	resolved, err := expression.Resolve(map[string]any{
		"executionId": map[string]any{"mode": "expression", "value": "{{ $execution.id }}"},
		"mode":        map[string]any{"mode": "expression", "value": "{{ $execution.mode }}"},
		"resume":      map[string]any{"mode": "expression", "value": "{{ $execution.resumeUrl }}"},
		"approve":     map[string]any{"mode": "expression", "value": "{{ $execution.approvalUrl }}"},
		"workflowId":  map[string]any{"mode": "expression", "value": "{{ $workflow.id }}"},
		"workflow":    map[string]any{"mode": "expression", "value": "{{ $workflow.name }}"},
		"active":      map[string]any{"mode": "expression", "value": "{{ $workflow.active }}"},
		"zone":        map[string]any{"mode": "expression", "value": "{{ $now.zoneName }}"},
		"env":         map[string]any{"mode": "expression", "value": "{{ $env.REGION }}"},
		"item":        map[string]any{"mode": "expression", "value": "{{ $json.id }}"},
	}, ctx)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	for key, want := range map[string]any{
		"executionId": "exec_1",
		"mode":        "manual",
		"resume":      "https://host.example/resume/tok_1",
		"approve":     "https://host.example/approve/tok_1",
		"workflowId":  "wf_1",
		"workflow":    "Orders",
		"active":      true,
		"zone":        "Asia/Jakarta",
		"env":         "eu-west-1",
		"item":        "item_1",
	} {
		if resolved[key] != want {
			t.Errorf("resolved[%q] = %#v, want %#v", key, resolved[key], want)
		}
	}
}
