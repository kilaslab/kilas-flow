package nodes_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func TestChatTriggerIsRegisteredAndBound(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Get(nodes.ChatTriggerNodeType, workflow.V(1))
	if !found {
		t.Fatalf("%s is not registered", nodes.ChatTriggerNodeType)
	}
	if definition.ExecutorID != nodes.ChatTriggerExecutorID {
		t.Errorf("executor = %q, want %q", definition.ExecutorID, nodes.ChatTriggerExecutorID)
	}
	if len(definition.Inputs) != 0 || len(definition.Outputs) != 1 {
		t.Errorf("ports = %d in / %d out, want a trigger with one output", len(definition.Inputs), len(definition.Outputs))
	}
	if definition.Webhook != nil {
		t.Errorf("webhook = %#v, want none: hosted chat is out of this slice", definition.Webhook)
	}
	flowExecutor(t, nodes.ChatTriggerExecutorID)
}

func TestChatTriggerEmitsTheRunInput(t *testing.T) {
	t.Parallel()

	executor := flowExecutor(t, nodes.ChatTriggerExecutorID)
	input := workflow.Item{JSON: map[string]any{
		"action": "sendMessage", "sessionId": "s-1", "chatInput": "hello",
	}}

	output, err := executor.Execute(context.Background(),
		flowNode(t, nodes.ChatTriggerNodeType, nil), nil, engine.Request{Input: input})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output) != 1 || len(output[0]) != 1 {
		t.Fatalf("output = %#v, want one stream with one item", output)
	}
	if !reflect.DeepEqual(output[0][0].JSON, input.JSON) {
		t.Errorf("item = %#v, want the run input", output[0][0].JSON)
	}
}

func TestAgentPromptDefaultIsAnExpressionMarker(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Get(nodes.AgentNodeType, workflow.V(1))
	if !found {
		t.Fatal("the AI Agent node is not registered")
	}
	var prompt node.PropertyDefinition
	for _, parameter := range definition.Parameters {
		if parameter.Key == "prompt" {
			prompt = parameter
			break
		}
	}
	if prompt.Key == "" {
		t.Fatal("the AI Agent has no prompt parameter")
	}
	got, _ := prompt.Default.(map[string]any)
	if got["mode"] != "expression" || got["value"] != "{{ $json.chatInput }}" {
		t.Errorf("prompt default = %#v, want the expression marker for $json.chatInput", prompt.Default)
	}
}

func TestMemorySessionKeyDefaultIsAnExpressionMarker(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Get(nodes.MemoryNodeType, workflow.V(1))
	if !found {
		t.Fatal("the Simple Memory node is not registered")
	}
	var sessionKey node.PropertyDefinition
	for _, parameter := range definition.Parameters {
		if parameter.Key == "sessionKey" {
			sessionKey = parameter
			break
		}
	}
	if sessionKey.Key == "" {
		t.Fatal("Simple Memory has no sessionKey parameter")
	}
	got, _ := sessionKey.Default.(map[string]any)
	if got["mode"] != "expression" || got["value"] != "{{ $json.sessionId }}" {
		t.Errorf("sessionKey default = %#v, want the expression marker for $json.sessionId", sessionKey.Default)
	}
}
