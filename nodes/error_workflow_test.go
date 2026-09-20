package nodes_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// The error-workflow pair is registered under the names a document refers to
// and each binds a real executor.
//
// The Error Trigger has to be a root the compiler can start a run from — no
// inputs — and Stop and Error has to be terminal: nothing runs after a workflow
// stops itself on purpose.
func TestErrorWorkflowNodesAreRegisteredAndBound(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	trigger, found := registry.Lookup(nodes.ErrorTriggerNodeType, workflow.V(1))
	if !found {
		t.Fatalf("%s is not registered: no error workflow can be started", nodes.ErrorTriggerNodeType)
	}
	if len(trigger.Inputs) != 0 || len(trigger.Outputs) != 1 {
		t.Errorf("Error Trigger ports = %d in / %d out, want a root with one output", len(trigger.Inputs), len(trigger.Outputs))
	}
	stop, found := registry.Lookup(nodes.StopAndErrorNodeType, workflow.V(1))
	if !found {
		t.Fatalf("%s is not registered: no workflow can stop itself with an error", nodes.StopAndErrorNodeType)
	}
	if len(stop.Inputs) != 1 || len(stop.Outputs) != 0 {
		t.Errorf("Stop and Error ports = %d in / %d out, want one input and no output", len(stop.Inputs), len(stop.Outputs))
	}
	for _, executorID := range []string{nodes.ErrorTriggerExecutorID, nodes.StopAndErrorExecutorID} {
		// Fatals when the binding is missing, which is the assertion.
		flowExecutor(t, executorID)
	}
}

// Stop and Error is the author declaring a failure: the node fails with their
// message, which is what makes the run fail and its error workflow start.
func TestStopAndErrorFailsWithTheMessageTheAuthorGave(t *testing.T) {
	t.Parallel()

	executor := flowExecutor(t, nodes.StopAndErrorExecutorID)
	input := workflow.NodeInput{"main": {{JSON: map[string]any{"order": "A-1"}}}}

	_, err := executor.Execute(context.Background(),
		flowNode(t, nodes.StopAndErrorNodeType, map[string]any{"errorMessage": "payment declined"}), input, engine.Request{})
	if err == nil || !strings.Contains(err.Error(), "payment declined") {
		t.Errorf("Stop and Error error = %v, want the author's message", err)
	}

	// The message is expression-capable and reads the item it was handed.
	_, err = executor.Execute(context.Background(),
		flowNode(t, nodes.StopAndErrorNodeType, map[string]any{"errorMessage": "order {{ $json.order }} was blocked"}), input, engine.Request{})
	if err == nil || !strings.Contains(err.Error(), "order A-1 was blocked") {
		t.Errorf("Stop and Error error = %v, want the message resolved against the item", err)
	}

	// The error object an imported n8n node carries, as the importer writes it:
	// JSON text holding the message and the description.
	_, err = executor.Execute(context.Background(), flowNode(t, nodes.StopAndErrorNodeType, map[string]any{
		"errorObject": `{"errorMessage":"declined","errorDescription":"insufficient funds"}`,
	}), input, engine.Request{})
	if err == nil || !strings.Contains(err.Error(), "declined: insufficient funds") {
		t.Errorf("Stop and Error error = %v, want the imported object's message and description", err)
	}

	// Neither set: a failure with a message, never a silent success. A node
	// that stops a workflow and reports nothing is worse than one that fails.
	_, err = executor.Execute(context.Background(), flowNode(t, nodes.StopAndErrorNodeType, map[string]any{}), input, engine.Request{})
	if err == nil {
		t.Error("Stop and Error with no message succeeded, want a failure")
	}
}

// The Error Trigger hands the error object to the graph.
//
// Two shapes arrive: the one the engine sends when it starts an error workflow
// for a failure — the error object under `items`, the same envelope a
// sub-workflow call uses — and the one an author's own manual run produces,
// where the item they typed is what the trigger emits.
func TestErrorTriggerEmitsTheErrorObjectItWasStartedWith(t *testing.T) {
	t.Parallel()

	executor := flowExecutor(t, nodes.ErrorTriggerExecutorID)
	output, err := executor.Execute(context.Background(), flowNode(t, nodes.ErrorTriggerNodeType, nil), workflow.NodeInput{},
		engine.Request{Input: workflow.Item{JSON: map[string]any{"items": []any{
			map[string]any{"execution": map[string]any{"id": "exec-1"}},
		}}}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output) != 1 || len(output[0]) != 1 {
		t.Fatalf("Error Trigger output = %#v, want one item", output)
	}
	executionDetail, _ := output[0][0].JSON["execution"].(map[string]any)
	if executionDetail == nil || executionDetail["id"] != "exec-1" {
		t.Errorf("Error Trigger item = %#v, want the error object the run was started with", output[0][0].JSON)
	}

	output, err = executor.Execute(context.Background(), flowNode(t, nodes.ErrorTriggerNodeType, nil), workflow.NodeInput{},
		engine.Request{Input: workflow.Item{JSON: map[string]any{"manual": true}}})
	if err != nil {
		t.Fatalf("Execute(manual run) error = %v", err)
	}
	if len(output) != 1 || len(output[0]) != 1 || output[0][0].JSON["manual"] != true {
		t.Errorf("Error Trigger manual output = %#v, want the item the run was started with", output)
	}
}
