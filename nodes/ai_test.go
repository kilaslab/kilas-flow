package nodes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/ai"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

func aiRegistry(t *testing.T) *node.Registry {
	t.Helper()
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	return registry
}

func TestAgentPortsAcceptOnlyTheirOwnConnectionKind(t *testing.T) {
	t.Parallel()

	registry := aiRegistry(t)
	agent, found := registry.Get(nodes.AgentNodeType, 1)
	if !found {
		t.Fatal("the AI Agent node is not registered")
	}
	wanted := map[string]workflow.ConnectionKind{
		"main":   workflow.ConnectionMain,
		"model":  workflow.ConnectionLanguageModel,
		"memory": workflow.ConnectionMemory,
		"tools":  workflow.ConnectionTool,
	}
	got := map[string]workflow.ConnectionKind{}
	for _, port := range agent.Inputs {
		got[port.Name] = port.Kind
	}
	for name, kind := range wanted {
		if got[name] != kind {
			t.Errorf("agent input %q kind = %q, want %q", name, got[name], kind)
		}
	}

	for nodeType, kind := range map[string]workflow.ConnectionKind{
		nodes.ChatModelNodeType: workflow.ConnectionLanguageModel,
		nodes.MemoryNodeType:    workflow.ConnectionMemory,
		nodes.HTTPToolNodeType:  workflow.ConnectionTool,
	} {
		definition, found := registry.Get(nodeType, 1)
		if !found {
			t.Fatalf("%s is not registered", nodeType)
		}
		if len(definition.Outputs) != 1 || definition.Outputs[0].Kind != kind {
			t.Errorf("%s outputs = %#v, want a single %q port", nodeType, definition.Outputs, kind)
		}
	}
}

// aiDocument wires a chat model into an agent, optionally with extras.
func aiDocument(extraNodes []workflow.Node, extraConnections []workflow.Connection) workflow.Document {
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_agent",
		Name:          "Agent",
		Nodes: append([]workflow.Node{
			{ID: "trigger", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: 1},
			{
				ID: "model", Name: "Chat Model", Type: nodes.ChatModelNodeType, TypeVersion: 1,
				Parameters:  map[string]any{"model": "gpt-test"},
				Credentials: map[string]string{"httpBearerAuth": "cred-key"},
			},
			{
				ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: 1,
				Parameters: map[string]any{"prompt": "Say hello"},
			},
		}, extraNodes...),
		Connections: append([]workflow.Connection{
			{ID: "c-main", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "trigger", Port: "main"}, Target: workflow.Endpoint{NodeID: "agent", Port: "main"}},
			{ID: "c-model", Kind: workflow.ConnectionLanguageModel, Source: workflow.Endpoint{NodeID: "model", Port: "model"}, Target: workflow.Endpoint{NodeID: "agent", Port: "model"}},
		}, extraConnections...),
		Settings: map[string]any{},
	}
	return document
}

func TestCompilerAcceptsAValidAgentGraph(t *testing.T) {
	t.Parallel()

	if _, err := workflow.Compile(aiDocument(nil, nil), aiRegistry(t)); err != nil {
		t.Fatalf("Compile() error = %v, want a valid agent graph", err)
	}
}

func TestCompilerRejectsAModelWiredIntoTheWrongPort(t *testing.T) {
	t.Parallel()

	document := aiDocument(nil, nil)
	// A language model connected as if it produced items would silently make
	// the agent's model port empty at run time.
	document.Connections[1] = workflow.Connection{
		ID: "c-model", Kind: workflow.ConnectionMain,
		Source: workflow.Endpoint{NodeID: "model", Port: "model"},
		Target: workflow.Endpoint{NodeID: "agent", Port: "main"},
	}

	if _, err := workflow.Compile(document, aiRegistry(t)); err == nil {
		t.Fatal("a language model was accepted on a main port")
	}
}

func TestCompilerRejectsAToolWiredIntoTheModelPort(t *testing.T) {
	t.Parallel()

	document := aiDocument([]workflow.Node{{
		ID: "tool", Name: "Weather", Type: nodes.HTTPToolNodeType, TypeVersion: 1,
		Parameters: map[string]any{
			"toolName": "get_weather", "toolDescription": "Weather by city",
			"method": "GET", "url": "https://api.test/weather",
		},
	}}, []workflow.Connection{{
		ID: "c-tool", Kind: workflow.ConnectionTool,
		Source: workflow.Endpoint{NodeID: "tool", Port: "tool"},
		Target: workflow.Endpoint{NodeID: "agent", Port: "model"},
	}})

	if _, err := workflow.Compile(document, aiRegistry(t)); err == nil {
		t.Fatal("a tool was accepted on the model port")
	}
}

func TestChatModelRequiresACredentialRatherThanAnAmbientKey(t *testing.T) {
	t.Parallel()

	definition, _ := aiRegistry(t).Lookup(nodes.ChatModelNodeType, 1)
	err := definition.Validate(workflow.Node{Parameters: map[string]any{"model": "gpt-test"}})
	if err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("Validate() = %v, want a credential requirement", err)
	}
}

func TestChatModelDescriptorCarriesNoAPIKey(t *testing.T) {
	t.Parallel()

	resolver := &stubCredentials{credential: engine.Credential{
		ID: "cred-key", Name: "OpenAI", Type: "httpBearerAuth",
		Fields: map[string]string{"token": "sk-live-secret"},
	}}
	registry := aiRegistry(t)
	definition, _ := registry.Lookup(nodes.ChatModelNodeType, 1)
	ir := workflow.IRNode{
		ID: "model", Name: "Chat Model", Type: nodes.ChatModelNodeType, TypeVersion: 1,
		Parameters:  map[string]any{"model": "gpt-test", "baseUrl": "https://api.test/v1"},
		Credentials: map[string]string{"httpBearerAuth": "cred-key"},
		Definition:  definition,
	}

	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	output, err := runExecutor(t, executors, nodes.ChatModelExecutorID, ir, workflow.NodeInput{}, engine.Request{Credentials: resolver})
	if err != nil {
		t.Fatalf("chat model error = %v", err)
	}

	// The descriptor travels in an item that is persisted in the execution
	// record and streamed to the live feed, so it must carry the reference and
	// never the key.
	encoded, _ := json.Marshal(output)
	if strings.Contains(string(encoded), "sk-live-secret") {
		t.Fatalf("the model descriptor leaked the API key: %s", encoded)
	}
	if !strings.Contains(string(encoded), "cred-key") {
		t.Errorf("descriptor = %s, want the credential reference", encoded)
	}
}

func TestAgentRunsWithAToolThatReusesTheHTTPRequestImplementation(t *testing.T) {
	t.Parallel()

	var toolPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		toolPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tempC":19}`))
	}))
	defer upstream.Close()

	// A fake provider: the agent's model call never leaves the test.
	turn := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		turn++
		w.Header().Set("Content-Type", "application/json")
		if turn == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[
				{"id":"c1","type":"function","function":{"name":"get_weather","arguments":"{\"input\":{\"city\":\"Utrecht\"}}"}}
			]},"finish_reason":"tool_calls"}],"usage":{"total_tokens":10}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"It is 19C."},"finish_reason":"stop"}],"usage":{"total_tokens":5}}`))
	}))
	defer provider.Close()

	resolver := &stubCredentials{credential: engine.Credential{
		ID: "cred-key", Name: "Provider", Type: "httpBearerAuth",
		Fields: map[string]string{"token": "sk-live-secret"},
	}}

	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	registry := aiRegistry(t)
	definition, _ := registry.Lookup(nodes.AgentNodeType, 1)
	ir := workflow.IRNode{
		ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: 1,
		Parameters: map[string]any{"prompt": "What is the weather?"},
		Definition: definition,
	}

	var published []string
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{
		"main": {{JSON: map[string]any{}}},
		"model": {{JSON: map[string]any{"$ai": map[string]any{
			"kind": "model", "model": "gpt-test", "baseUrl": provider.URL, "credentialId": "cred-key",
		}}}},
		"tools": {{JSON: map[string]any{"$ai": map[string]any{
			"kind": "tool", "name": "get_weather", "description": "Weather by city", "nodeName": "Weather",
			"parameters": map[string]any{
				"method": "GET",
				"url":    map[string]any{"mode": "expression", "value": upstream.URL + "/weather/{{ $json.city }}"},
			},
			"credentials": map[string]any{},
		}}}},
	}, engine.Request{
		Credentials: resolver,
		Events:      func(event engine.NodeEvent) { published = append(published, event.Name) },
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(output[0]) != 1 || output[0][0].JSON["output"] != "It is 19C." {
		t.Fatalf("agent output = %#v, want the model's final answer", output[0])
	}
	// The tool ran the real HTTP Request executor, so its expression resolved
	// from the model's arguments.
	if toolPath != "/weather/Utrecht" {
		t.Fatalf("tool called %q, want the model's argument resolved through $json", toolPath)
	}
	if output[0][0].JSON["toolCalls"] != float64(1) {
		t.Errorf("toolCalls = %#v, want 1", output[0][0].JSON["toolCalls"])
	}

	// Nested model and tool events must reach the standard execution channel.
	for _, want := range []string{string(ai.EventModelStarted), string(ai.EventToolStarted), string(ai.EventToolCompleted), string(ai.EventAgentCompleted)} {
		if !containsString(published, want) {
			t.Errorf("published events = %v, want %q", published, want)
		}
	}
}

func TestAgentRequiresAConnectedModel(t *testing.T) {
	t.Parallel()

	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	registry := aiRegistry(t)
	definition, _ := registry.Lookup(nodes.AgentNodeType, 1)
	ir := workflow.IRNode{
		ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: 1,
		Parameters: map[string]any{"prompt": "hi"}, Definition: definition,
	}

	_, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, engine.Request{})
	if err == nil || !strings.Contains(err.Error(), "model port") {
		t.Fatalf("Execute() = %v, want a missing-model rejection", err)
	}
}

func TestHTTPToolNameIsValidated(t *testing.T) {
	t.Parallel()

	definition, _ := aiRegistry(t).Lookup(nodes.HTTPToolNodeType, 1)
	base := map[string]any{"method": "GET", "url": "https://api.test/x", "toolDescription": "does a thing"}

	for name, toolName := range map[string]string{
		"spaces": "get weather",
		"dashes": "get-weather",
		"empty":  "",
	} {
		parameters := map[string]any{"toolName": toolName}
		for key, value := range base {
			parameters[key] = value
		}
		if err := definition.Validate(workflow.Node{Parameters: parameters}); err == nil {
			t.Errorf("tool name %s (%q) was accepted", name, toolName)
		}
	}

	valid := map[string]any{"toolName": "get_weather"}
	for key, value := range base {
		valid[key] = value
	}
	if err := definition.Validate(workflow.Node{Parameters: valid}); err != nil {
		t.Errorf("Validate() = %v, want a valid tool accepted", err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
