package nodes_test

import (
	"context"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// A workflow tool whose By ID locator holds an expression resolves it in the
// tool call's scope. `{{ $workflow.id }}` is how n8n's recursive agents name
// the workflow they sit in, and the importer carries it in exactly this
// shape: a locator the validator accepts, with the expression inside it.
func TestWorkflowToolResolvesAnExpressionInsideItsLocator(t *testing.T) {
	t.Parallel()

	workflows := &stubWorkflows{items: []workflow.Item{{JSON: map[string]any{"answer": "done"}}}}
	provider := scriptProvider(t, []string{
		assistantToolCalls(`{"name":"recurse","arguments":"{}"}`),
		assistantAnswer("done"),
	})

	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	_, err := executor.Execute(context.Background(), agentIR(t, map[string]any{"prompt": "Recurse."}), workflow.NodeInput{
		"main":  {{JSON: map[string]any{}}},
		"model": {modelItem(provider.URL)},
		"tools": {toolItem(map[string]any{
			"kind": "workflow", "name": "recurse", "description": "This workflow.", "nodeName": "Recurse",
			"workflowId": map[string]any{
				"__rl": true, "mode": "id",
				"value": map[string]any{"mode": "expression", "value": "{{ $workflow.id }}"},
			},
			"parameters": map[string]any{},
		})},
	}, engine.Request{
		Credentials: agentCredentials(), Workflows: workflows, Execution: agentExecution(),
		Workflow: expression.WorkflowContext{ID: "wf_agent", Name: "Recursive agent"},
		Events:   func(event engine.NodeEvent) {},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(workflows.calls) != 1 || workflows.calls[0].WorkflowID != "wf_agent" {
		t.Fatalf("sub-workflow calls = %#v, want one call to the running workflow wf_agent", workflows.calls)
	}
}
