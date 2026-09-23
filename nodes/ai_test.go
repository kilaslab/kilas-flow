package nodes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
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
	agent, found := registry.Get(nodes.AgentNodeType, workflow.V(1))
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
		definition, found := registry.Get(nodeType, workflow.V(1))
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
			{ID: "trigger", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{
				ID: "model", Name: "Chat Model", Type: nodes.ChatModelNodeType, TypeVersion: workflow.V(1),
				Parameters:  map[string]any{"model": "gpt-test"},
				Credentials: map[string]string{"httpBearerAuth": "cred-key"},
			},
			{
				ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: workflow.V(1),
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
		ID: "tool", Name: "Weather", Type: nodes.HTTPToolNodeType, TypeVersion: workflow.V(1),
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

// TestChatModelNeedsNoCredentialButInventsNone: the OpenAI-compatible node is
// the local-endpoint node, and a local endpoint authenticates nobody. What must
// not change is the other half: no credential means no key is looked up
// ambiently, and the provider nodes still require their own credential type.
func TestChatModelNeedsNoCredentialButInventsNone(t *testing.T) {
	t.Parallel()

	compatible, _ := aiRegistry(t).Lookup(nodes.ChatModelNodeType, workflow.V(1))
	if err := compatible.Validate(workflow.Node{Parameters: map[string]any{"model": "llama3"}}); err != nil {
		t.Fatalf("Validate() = %v, want a local endpoint accepted with no credential", err)
	}
	for _, nodeType := range []string{nodes.OpenAIChatModelNodeType, nodes.OpenRouterChatModelNodeType} {
		definition, _ := aiRegistry(t).Lookup(nodeType, workflow.V(1))
		if err := definition.Validate(workflow.Node{Parameters: map[string]any{"model": modelLocator("gpt-test")}}); err == nil {
			t.Errorf("%s was accepted with no credential", nodeType)
		}
	}
}

func TestChatModelDescriptorCarriesNoAPIKey(t *testing.T) {
	t.Parallel()

	resolver := &stubCredentials{credential: engine.Credential{
		ID: "cred-key", Name: "OpenAI", Type: "httpBearerAuth",
		Fields: map[string]string{"token": "sk-live-secret"},
	}}
	registry := aiRegistry(t)
	definition, _ := registry.Lookup(nodes.ChatModelNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "model", Name: "Chat Model", Type: nodes.ChatModelNodeType, TypeVersion: workflow.V(1),
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
	definition, _ := registry.Lookup(nodes.AgentNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: workflow.V(1),
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
	definition, _ := registry.Lookup(nodes.AgentNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"prompt": "hi"}, Definition: definition,
	}

	_, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, engine.Request{})
	if err == nil || !strings.Contains(err.Error(), "model port") {
		t.Fatalf("Execute() = %v, want a missing-model rejection", err)
	}
}

func TestHTTPToolNameIsValidated(t *testing.T) {
	t.Parallel()

	definition, _ := aiRegistry(t).Lookup(nodes.HTTPToolNodeType, workflow.V(1))
	base := map[string]any{"method": "GET", "url": "https://api.test/x", "toolDescription": "does a thing"}

	for name, toolName := range map[string]string{
		"spaces": "get weather",
		"dashes": "get-weather",
	} {
		parameters := map[string]any{"toolName": toolName}
		for key, value := range base {
			parameters[key] = value
		}
		if err := definition.Validate(workflow.Node{Parameters: parameters}); err == nil {
			t.Errorf("tool name %s (%q) was accepted", name, toolName)
		}
	}

	// An empty toolName is not missing: the name derives from the canvas
	// name, so it must validate cleanly here and resolve at run time.
	derived := map[string]any{}
	for key, value := range base {
		derived[key] = value
	}
	if err := definition.Validate(workflow.Node{Parameters: derived}); err != nil {
		t.Errorf("Validate() without toolName = %v, want the derived name accepted", err)
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

// modelLocator writes the resource locator shape the provider model nodes store
// `model` in, so a test configures the node the way the editor would.
func modelLocator(name string) map[string]any {
	return map[string]any{property.LocatorSentinel: true, "mode": "id", "value": name}
}

// runProviderModel runs one provider chat model node and returns the descriptor
// it hands the agent, so a test exercises the whole round trip — the node's
// parameters, the descriptor, and the agent's read-back — rather than a
// hand-written descriptor that could not catch a mismatch between the two.
func runProviderModel(t *testing.T, nodeType, executorID, credentialType string, parameters map[string]any, resolver *stubCredentials) workflow.Item {
	t.Helper()
	registry := aiRegistry(t)
	definition, found := registry.Lookup(nodeType, workflow.V(1))
	if !found {
		t.Fatalf("node type %q is not registered", nodeType)
	}
	ir := workflow.IRNode{
		ID: "model", Name: "Chat Model", Type: nodeType, TypeVersion: workflow.V(1),
		Parameters: parameters, Credentials: map[string]string{credentialType: "cred-key"},
		Definition: definition,
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	output, err := runExecutor(t, executors, executorID, ir, workflow.NodeInput{}, engine.Request{Credentials: resolver})
	if err != nil {
		t.Fatalf("%s error = %v", nodeType, err)
	}
	if len(output) == 0 || len(output[0]) != 1 {
		t.Fatalf("%s emitted %#v, want one descriptor item", nodeType, output)
	}
	return output[0][0]
}

// runAgentWith drives the agent node over one model descriptor.
func runAgentWith(t *testing.T, executor *nodes.AgentExecutor, model workflow.Item, resolver *stubCredentials) (workflow.NodeOutput, error) {
	t.Helper()
	definition, _ := aiRegistry(t).Lookup(nodes.AgentNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"prompt": "Extract the total."},
		Definition: definition,
	}
	return executor.Execute(context.Background(), ir, workflow.NodeInput{
		"main":  {{JSON: map[string]any{}}},
		"model": {model},
	}, engine.Request{Credentials: resolver})
}

// openAICredential is a stored credential of the provider's own type.
func openAICredential(domains ...string) *stubCredentials {
	return &stubCredentials{credential: engine.Credential{
		ID: "cred-key", Name: "OpenAI", Type: nodes.OpenAICredentialType,
		Fields: map[string]string{"apiKey": "sk-live-secret"}, AllowedDomains: domains,
	}}
}

// answerOnce is a provider that returns one completed turn and records the
// request body it was sent. Every model call in these tests stops here: a test
// that reached api.openai.com would be a test of somebody's billing account.
func answerOnce(received *map[string]any, delay time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		decoded := map[string]any{}
		_ = json.Unmarshal(body, &decoded)
		*received = decoded
		if delay > 0 {
			time.Sleep(delay)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"42"},"finish_reason":"stop"}],"usage":{"total_tokens":6}}`))
	}))
}

// TestEachProviderChatModelIsItsOwnNodeTypeWithItsOwnCredentialAndAddress is
// the point of the split. One node type whose provider is a URL string cannot
// be mapped back to the n8n type it came from, so two different imported types
// would collapse into one and an export could not tell them apart again.
func TestEachProviderChatModelIsItsOwnNodeTypeWithItsOwnCredentialAndAddress(t *testing.T) {
	t.Parallel()

	registry := aiRegistry(t)
	for _, want := range []struct {
		nodeType       string
		displayName    string
		credentialType string
		baseURL        string
		defaultModel   string
	}{
		{nodes.OpenAIChatModelNodeType, "OpenAI Chat Model", nodes.OpenAICredentialType,
			"https://api.openai.com/v1", "gpt-5-mini"},
		{nodes.OpenRouterChatModelNodeType, "OpenRouter Chat Model", nodes.OpenRouterCredentialType,
			"https://openrouter.ai/api/v1", "openai/gpt-4.1-mini"},
	} {
		definition, found := registry.Get(want.nodeType, workflow.V(1))
		if !found {
			t.Errorf("node type %q is not registered", want.nodeType)
			continue
		}
		if definition.DisplayName != want.displayName {
			t.Errorf("%s display name = %q, want %q", want.nodeType, definition.DisplayName, want.displayName)
		}
		if len(definition.Credentials) != 1 || definition.Credentials[0].Type != want.credentialType ||
			!definition.Credentials[0].Required {
			t.Errorf("%s credentials = %#v, want a required %s", want.nodeType, definition.Credentials, want.credentialType)
		}
		// The credential type has to exist in the registry, or an imported node
		// naming it arrives with no credential at all.
		if _, known := credentials.Lookup(want.credentialType); !known {
			t.Errorf("credential type %q is not registered", want.credentialType)
		}
		byKey := map[string]node.PropertyDefinition{}
		for _, parameter := range definition.Parameters {
			byKey[parameter.Key] = parameter
		}
		if got := byKey["baseUrl"].Default; got != want.baseURL {
			t.Errorf("%s base URL default = %#v, want %q", want.nodeType, got, want.baseURL)
		}
		locator, _ := byKey["model"].Default.(map[string]any)
		if locator["value"] != want.defaultModel {
			t.Errorf("%s model default = %#v, want %q", want.nodeType, byKey["model"].Default, want.defaultModel)
		}
		// A catalogue this server cannot fetch must not leave the user with an
		// empty dropdown and no way to name a model, so the field keeps a
		// free-text mode beside the list.
		var hasList, hasFreeText bool
		for _, mode := range byKey["model"].Modes {
			if mode.Kind == node.PropertyOptions && mode.LoadOptions != nil {
				hasList = true
			}
			if mode.Kind == node.PropertyString {
				hasFreeText = true
			}
		}
		if !hasList || !hasFreeText {
			t.Errorf("%s model modes = %#v, want a loaded list and a free-text fallback", want.nodeType, byKey["model"].Modes)
		}
	}
}

// TestTheChatModelOptionsMatchWhatTheReferenceWasRecordedAsSaying is the
// anti-drift check. The names and defaults are interchange facts, and this
// compares the declaration against the committed transcription rather than
// against the reference checkout, which no build input may read.
func TestTheChatModelOptionsMatchWhatTheReferenceWasRecordedAsSaying(t *testing.T) {
	t.Parallel()

	var recorded struct {
		Nodes []struct {
			N8nName        string  `json:"n8nName"`
			DisplayName    string  `json:"displayName"`
			CredentialType string  `json:"credentialType"`
			ModelDefault   string  `json:"modelDefault"`
			BaseURL        string  `json:"baseUrl"`
			TimeoutDefault float64 `json:"timeoutDefault"`
		} `json:"nodes"`
		Options []struct {
			Name    string  `json:"name"`
			Default float64 `json:"default"`
		} `json:"options"`
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "n8n_chat_model_options.json"))
	if err != nil {
		t.Fatalf("read the recorded reference: %v", err)
	}
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatalf("decode the recorded reference: %v", err)
	}

	registry := aiRegistry(t)
	for _, want := range recorded.Nodes {
		// The KilasFlow type is the n8n name under this server's namespace, so
		// the mapping an importer needs is the identity on the last segment.
		nodeType := "kilasflow." + want.N8nName
		definition, found := registry.Get(nodeType, workflow.V(1))
		if !found {
			t.Errorf("node type %q is not registered, so %q cannot map onto anything", nodeType, want.N8nName)
			continue
		}
		if definition.DisplayName != want.DisplayName || definition.Credentials[0].Type != want.CredentialType {
			t.Errorf("%s = %q / %q, want %q / %q", nodeType, definition.DisplayName,
				definition.Credentials[0].Type, want.DisplayName, want.CredentialType)
		}
		var options node.PropertyDefinition
		for _, parameter := range definition.Parameters {
			if parameter.Key == "options" {
				options = parameter
			}
		}
		declared := map[string]any{}
		for _, field := range options.Fields {
			declared[field.Key] = field.Default
		}
		if got := declared["timeout"]; got != want.TimeoutDefault {
			t.Errorf("%s timeout default = %#v, want %v", nodeType, got, want.TimeoutDefault)
		}
		for _, option := range recorded.Options {
			got, present := declared[option.Name]
			if !present {
				t.Errorf("%s does not declare option %q", nodeType, option.Name)
				continue
			}
			if got != option.Default {
				t.Errorf("%s option %q default = %#v, want %v", nodeType, option.Name, got, option.Default)
			}
		}
	}
}

// TestAProviderChatModelRefusesACredentialItCannotUse pins the identity the
// split exists for. A generic bearer token would authenticate the call
// perfectly well, and accepting it would put the node's provider identity back
// in the hands of whatever the user happened to attach.
func TestAProviderChatModelRefusesACredentialItCannotUse(t *testing.T) {
	t.Parallel()

	definition, _ := aiRegistry(t).Get(nodes.OpenAIChatModelNodeType, workflow.V(1))
	configured := workflow.Node{
		Parameters:  map[string]any{"model": modelLocator("gpt-test")},
		Credentials: map[string]string{nodes.BearerCredentialType: "cred-key"},
	}
	err := definition.Validate(configured)
	if err == nil || !strings.Contains(err.Error(), nodes.OpenAICredentialType) {
		t.Fatalf("Validate() = %v, want the provider's own credential type required", err)
	}

	// And at run time, where the stored credential is what actually decides.
	bearer := &stubCredentials{credential: engine.Credential{
		ID: "cred-key", Name: "Some token", Type: nodes.BearerCredentialType,
		Fields: map[string]string{"token": "sk-live-secret"},
	}}
	registry := aiRegistry(t)
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	openAI, _ := registry.Lookup(nodes.OpenAIChatModelNodeType, workflow.V(1))
	_, err = runExecutor(t, executors, nodes.OpenAIChatModelExecutorID, workflow.IRNode{
		ID: "model", Name: "Chat Model", Type: nodes.OpenAIChatModelNodeType, TypeVersion: workflow.V(1),
		Parameters:  map[string]any{"model": modelLocator("gpt-test")},
		Credentials: map[string]string{nodes.OpenAICredentialType: "cred-key"},
		Definition:  openAI,
	}, workflow.NodeInput{}, engine.Request{Credentials: bearer})
	if err == nil || !strings.Contains(err.Error(), "not "+nodes.OpenAICredentialType) {
		t.Fatalf("execute = %v, want the mismatched credential type refused", err)
	}
}

// TestATemperatureOfZeroReachesTheProvider is the case every earlier test
// missed. Zero is the one value a user reaches for deliberately — it is what
// deterministic extraction asks for — and the old descriptor guard dropped it,
// leaving the provider to apply its own default on the setting the user had
// been most explicit about.
func TestATemperatureOfZeroReachesTheProvider(t *testing.T) {
	t.Parallel()

	var received map[string]any
	provider := answerOnce(&received, 0)
	defer provider.Close()

	resolver := openAICredential()
	descriptor := runProviderModel(t, nodes.OpenAIChatModelNodeType, nodes.OpenAIChatModelExecutorID,
		nodes.OpenAICredentialType, map[string]any{
			"model": modelLocator("gpt-test"), "baseUrl": provider.URL, "stream": false,
			"options": map[string]any{nodes.ModelOptionTemperature: float64(0)},
		}, resolver)

	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	if _, err := runAgentWith(t, executor, descriptor, resolver); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	temperature, present := received["temperature"].(float64)
	if !present || temperature != 0 {
		t.Fatalf("request temperature = %#v, want an explicit 0 on the wire", received["temperature"])
	}
}

// TestAnOptionTheUserNeverAddedIsNotSentAtAll is the other half of the same
// rule: absent has to stay absent, or this server would be choosing sampling
// values on the user's behalf and calling them the provider's.
func TestAnOptionTheUserNeverAddedIsNotSentAtAll(t *testing.T) {
	t.Parallel()

	var received map[string]any
	provider := answerOnce(&received, 0)
	defer provider.Close()

	resolver := &stubCredentials{credential: engine.Credential{
		ID: "cred-key", Name: "OpenRouter", Type: nodes.OpenRouterCredentialType,
		Fields: map[string]string{"apiKey": "sk-or-secret"},
	}}
	descriptor := runProviderModel(t, nodes.OpenRouterChatModelNodeType, nodes.OpenRouterChatModelExecutorID,
		nodes.OpenRouterCredentialType, map[string]any{
			"model": modelLocator("openai/gpt-test"), "baseUrl": provider.URL, "stream": false,
			// An Options collection the user opened and set exactly one member of.
			"options": map[string]any{nodes.ModelOptionTopP: float64(0.5)},
		}, resolver)

	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	if _, err := runAgentWith(t, executor, descriptor, resolver); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if top, present := received["top_p"].(float64); !present || top != 0.5 {
		t.Errorf("request top_p = %#v, want the one option that was set", received["top_p"])
	}
	for _, unwanted := range []string{"temperature", "frequency_penalty", "presence_penalty", "max_tokens"} {
		if _, present := received[unwanted]; present {
			t.Errorf("request carries %q = %#v, want nothing sent for an option nobody set", unwanted, received[unwanted])
		}
	}
}

// TestAModelCallMayOutlastTheDeploymentsOutboundTimeout is the third live
// defect. http.Client.Timeout is a ceiling a context deadline can only lower,
// so while the model client carried the outbound default a long completion died
// as a transport error — the user was told the network failed when the model
// was simply still thinking.
func TestAModelCallMayOutlastTheDeploymentsOutboundTimeout(t *testing.T) {
	t.Parallel()

	var received map[string]any
	provider := answerOnce(&received, 300*time.Millisecond)
	defer provider.Close()

	policy := localPolicy()
	policy.Timeout = 100 * time.Millisecond

	resolver := openAICredential()
	descriptor := runProviderModel(t, nodes.OpenAIChatModelNodeType, nodes.OpenAIChatModelExecutorID,
		nodes.OpenAICredentialType, map[string]any{
			"model": modelLocator("gpt-test"), "baseUrl": provider.URL, "stream": false,
			"options": map[string]any{nodes.ModelOptionTimeout: float64(5000)},
		}, resolver)

	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), policy, nil)
	output, err := runAgentWith(t, executor, descriptor, resolver)
	if err != nil {
		t.Fatalf("Execute() error = %v, want the node's own timeout to govern", err)
	}
	if output[0][0].JSON["output"] != "42" {
		t.Errorf("output = %#v, want the model's answer", output[0][0].JSON)
	}
}

// TestAModelTimeoutAboveTheCeilingIsRefusedRatherThanClamped is why the ceiling
// is a refusal. Quietly granting less than was asked for turns a configuration
// mistake into an intermittent one that surfaces only on the slowest prompts,
// as a transport error nobody can trace back to the setting that caused it.
func TestAModelTimeoutAboveTheCeilingIsRefusedRatherThanClamped(t *testing.T) {
	t.Parallel()

	var received map[string]any
	provider := answerOnce(&received, 0)
	defer provider.Close()

	resolver := openAICredential()
	descriptor := runProviderModel(t, nodes.OpenAIChatModelNodeType, nodes.OpenAIChatModelExecutorID,
		nodes.OpenAICredentialType, map[string]any{
			"model": modelLocator("gpt-test"), "baseUrl": provider.URL, "stream": false,
			"options": map[string]any{nodes.ModelOptionTimeout: float64(600000)},
		}, resolver)

	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil,
		nodes.WithModelTimeoutCeiling(time.Minute))
	_, err := runAgentWith(t, executor, descriptor, resolver)
	if !errors.Is(err, nodes.ErrModelTimeoutAboveCeiling) {
		t.Fatalf("Execute() error = %v, want %v", err, nodes.ErrModelTimeoutAboveCeiling)
	}
	if received != nil {
		t.Error("the provider was called for a node the deployment had already refused")
	}
}

// TestTheRunCeilingErrorNamesTheCeilingAndNotTheNodeTimeout is the fourth
// message defect: the bound that ends a run is the deployment's ceiling, and the
// error named the chat model node's Timeout option instead — a knob that cannot
// raise it, because a node asking for more than the ceiling is refused before it
// runs. A user reading that message turned a setting that changed nothing.
func TestTheRunCeilingErrorNamesTheCeilingAndNotTheNodeTimeout(t *testing.T) {
	t.Parallel()

	// A provider that never answers: the ceiling is the only thing that can
	// end the run. It is released at the end of the test, because an idle
	// Go server does not notice the client giving up and httptest.Server
	// waits for its handlers.
	release := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	defer provider.Close()
	defer close(release)

	ceiling := 300 * time.Millisecond
	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil,
		nodes.WithModelTimeoutCeiling(ceiling))
	_, err := runAgentWith(t, executor, agentModelInput(provider.URL)["model"][0], bearerResolver())
	if err == nil {
		t.Fatal("Execute() succeeded against a provider that never answers")
	}
	if !strings.Contains(err.Error(), ceiling.String()) {
		t.Errorf("error = %v, want the ceiling %s named as the bound that ended the run", err, ceiling)
	}
	if strings.Contains(err.Error(), "Timeout option") {
		t.Errorf("error = %v, want no mention of the chat model node's Timeout option: that option bounds one request and cannot raise the run ceiling", err)
	}
}

// TestAModelCallToAPrivateAddressIsRefused proves the SSRF guard still covers
// the model path after the client's clock was removed. The address is checked
// in the dialer, after DNS and immediately before the socket opens, so it holds
// however the base URL was written.
func TestAModelCallToAPrivateAddressIsRefused(t *testing.T) {
	t.Parallel()

	var received map[string]any
	provider := answerOnce(&received, 0)
	defer provider.Close()

	resolver := openAICredential()
	descriptor := runProviderModel(t, nodes.OpenAIChatModelNodeType, nodes.OpenAIChatModelExecutorID,
		nodes.OpenAICredentialType, map[string]any{
			"model": modelLocator("gpt-test"), "baseUrl": provider.URL, "stream": false,
		}, resolver)

	// The deployment's real posture: private networks refused. The test server
	// is on loopback, which is exactly such an address.
	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), safehttp.DefaultPolicy(), nil)
	_, err := runAgentWith(t, executor, descriptor, resolver)
	if err == nil {
		t.Fatal("a model node reached a loopback address")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("error = %v, want the policy refusal", err)
	}
	if received != nil {
		t.Error("the refused request still reached the server")
	}
}

// TestAModelCallReachesTheOneLoopbackEndpointTheDeploymentNamed is the other
// half of the refusal above, and the reason safehttp grew an endpoint list at
// all. A self-hosted install running a model server beside the instance has to
// reach it, and the only lever before this was AllowPrivateNetworks — which
// buys one address by handing every outbound request the whole internal
// network, and leaves the suite unable to catch a regression in the guard it
// had switched off.
func TestAModelCallReachesTheOneLoopbackEndpointTheDeploymentNamed(t *testing.T) {
	t.Parallel()

	var received map[string]any
	provider := answerOnce(&received, 0)
	defer provider.Close()

	address, err := url.Parse(provider.URL)
	if err != nil {
		t.Fatalf("parse %q: %v", provider.URL, err)
	}

	// The deployment's real posture, with one endpoint written down.
	policy := safehttp.DefaultPolicy()
	policy.AllowedPrivateEndpoints = []string{address.Host}

	resolver := openAICredential()
	descriptor := runProviderModel(t, nodes.OpenAIChatModelNodeType, nodes.OpenAIChatModelExecutorID,
		nodes.OpenAICredentialType, map[string]any{
			"model": modelLocator("gpt-test"), "baseUrl": provider.URL, "stream": false,
		}, resolver)

	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), policy, nil)
	output, err := runAgentWith(t, executor, descriptor, resolver)
	if err != nil {
		t.Fatalf("Execute() error = %v, want the named endpoint to be reachable", err)
	}
	if received == nil {
		t.Fatal("the model server was never called")
	}
	if output[0][0].JSON["output"] != "42" {
		t.Errorf("output = %#v, want the model's answer", output[0][0].JSON)
	}

	// The allowance is one endpoint, not a posture. A second loopback server on
	// another port is the neighbouring service this must never have opened.
	var neighbour map[string]any
	other := answerOnce(&neighbour, 0)
	defer other.Close()

	elsewhere := runProviderModel(t, nodes.OpenAIChatModelNodeType, nodes.OpenAIChatModelExecutorID,
		nodes.OpenAICredentialType, map[string]any{
			"model": modelLocator("gpt-test"), "baseUrl": other.URL, "stream": false,
		}, resolver)
	if _, err := runAgentWith(t, executor, elsewhere, resolver); err == nil {
		t.Fatal("a model node reached a loopback address nobody named")
	}
	if neighbour != nil {
		t.Error("the refused request still reached the neighbouring server")
	}
}

// TestACredentialScopedToOneHostCannotBeSentToAnother closes the other half of
// the same question. The SSRF policy says where the deployment may go; the
// credential's own domain scope says where this secret may go, and until now a
// model call honoured only the first — so a key scoped to api.openai.com could
// be pointed anywhere by editing one parameter.
func TestACredentialScopedToOneHostCannotBeSentToAnother(t *testing.T) {
	t.Parallel()

	var received map[string]any
	provider := answerOnce(&received, 0)
	defer provider.Close()

	resolver := openAICredential("api.openai.com")
	descriptor := runProviderModel(t, nodes.OpenAIChatModelNodeType, nodes.OpenAIChatModelExecutorID,
		nodes.OpenAICredentialType, map[string]any{
			"model": modelLocator("gpt-test"), "baseUrl": provider.URL, "stream": false,
		}, resolver)

	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	_, err := runAgentWith(t, executor, descriptor, resolver)
	if err == nil || !strings.Contains(err.Error(), "not allowed for host") {
		t.Fatalf("Execute() error = %v, want the credential's domain scope to refuse", err)
	}
	if received != nil {
		t.Error("the secret was sent to a host the credential does not cover")
	}
}

// TestAStreamedRunReportsTheProvidersOwnTokenUsage is the node-level half of
// the streaming fix: the figures have to survive the descriptor and the agent
// and land in the output item's usage object, which is what every cost report
// downstream is built on.
func TestAStreamedRunReportsTheProvidersOwnTokenUsage(t *testing.T) {
	t.Parallel()

	var asked map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &asked)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: " + `{"choices":[{"delta":{"content":"42"},"finish_reason":"stop"}],"usage":{"prompt_tokens":17,"completion_tokens":2,"total_tokens":19}}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer provider.Close()

	resolver := openAICredential()
	descriptor := runProviderModel(t, nodes.OpenAIChatModelNodeType, nodes.OpenAIChatModelExecutorID,
		nodes.OpenAICredentialType, map[string]any{
			"model": modelLocator("gpt-test"), "baseUrl": provider.URL, "stream": true,
		}, resolver)

	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	output, err := runAgentWith(t, executor, descriptor, resolver)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if options, ok := asked["stream_options"].(map[string]any); !ok || options["include_usage"] != true {
		t.Fatalf("streamed request = %#v, want stream_options asking for usage", asked)
	}
	usage, _ := output[0][0].JSON["usage"].(map[string]any)
	if usage["totalTokens"] != float64(19) || usage["promptTokens"] != float64(17) {
		t.Errorf("usage = %#v, want the provider's own figures rather than zero", usage)
	}
}

// TestTwoChatModelsOnOneAgentDoNotCompile is the coin flip this ticket exists
// to remove. Both models are registered nodes, both edges name a port that
// exists with the matching kind, and topological order runs both before the
// agent — so nothing except the slot's own cap can refuse this graph. Without
// the cap it activates and the model that actually runs is whichever descriptor
// the agent meets first, which is item order rather than anything the author
// chose.
func TestTwoChatModelsOnOneAgentDoNotCompile(t *testing.T) {
	t.Parallel()

	document := aiDocument([]workflow.Node{{
		ID: "model-2", Name: "Second Chat Model", Type: nodes.ChatModelNodeType, TypeVersion: workflow.V(1),
		Parameters:  map[string]any{"model": "gpt-other"},
		Credentials: map[string]string{"httpBearerAuth": "cred-key"},
	}}, []workflow.Connection{{
		ID: "c-model-2", Kind: workflow.ConnectionLanguageModel,
		Source: workflow.Endpoint{NodeID: "model-2", Port: "model"},
		Target: workflow.Endpoint{NodeID: "agent", Port: "model"},
	}})

	_, err := workflow.Compile(document, aiRegistry(t))
	if err == nil {
		t.Fatal("two chat models on one agent compiled; the model that runs would be decided by item order")
	}
	var issues *workflow.ValidationErrors
	if !errors.As(err, &issues) {
		t.Fatalf("Compile() error = %v, want validation errors", err)
	}
	if !hasValidationCode(issues.Issues, workflow.ErrorPortFull) {
		t.Fatalf("issues = %#v, want %q", issues.Issues, workflow.ErrorPortFull)
	}
	// The diagnostic has to name the node, the slot and the count, or the
	// author is told only that something is wrong somewhere.
	for _, want := range []string{"Chat Model", "agent", "2"} {
		if !strings.Contains(portFullMessage(issues.Issues), want) {
			t.Errorf("message = %q, want it to name %q", portFullMessage(issues.Issues), want)
		}
	}
}

// TestASecondMemoryOnOneAgentDoesNotCompile covers the other capped slot. A
// memory is chosen for a run the same way a model is, so two of them is the
// same silent conflict.
func TestASecondMemoryOnOneAgentDoesNotCompile(t *testing.T) {
	t.Parallel()

	memory := func(id string) workflow.Node {
		return workflow.Node{
			ID: id, Name: "Memory " + id, Type: nodes.MemoryNodeType, TypeVersion: workflow.V(1),
			Parameters: map[string]any{"sessionId": "chat-1"},
		}
	}
	edge := func(id, source string) workflow.Connection {
		return workflow.Connection{
			ID: id, Kind: workflow.ConnectionMemory,
			Source: workflow.Endpoint{NodeID: source, Port: "memory"},
			Target: workflow.Endpoint{NodeID: "agent", Port: "memory"},
		}
	}

	// One memory is the shape the agent is designed for and must still compile.
	if _, err := workflow.Compile(
		aiDocument([]workflow.Node{memory("mem-1")}, []workflow.Connection{edge("c-mem-1", "mem-1")}),
		aiRegistry(t),
	); err != nil {
		t.Fatalf("Compile() with one memory error = %v, want it accepted", err)
	}

	_, err := workflow.Compile(
		aiDocument(
			[]workflow.Node{memory("mem-1"), memory("mem-2")},
			[]workflow.Connection{edge("c-mem-1", "mem-1"), edge("c-mem-2", "mem-2")},
		),
		aiRegistry(t),
	)
	if err == nil {
		t.Fatal("two memories on one agent compiled")
	}
	var issues *workflow.ValidationErrors
	if !errors.As(err, &issues) || !hasValidationCode(issues.Issues, workflow.ErrorPortFull) {
		t.Fatalf("Compile() error = %v, want %q", err, workflow.ErrorPortFull)
	}
}

// TestAnAgentWithNoChatModelDoesNotCompile pins the required half of the slot
// rules. An agent with no model cannot do anything at all, so saying so at
// compile time beats activating and failing on the first item.
func TestAnAgentWithNoChatModelDoesNotCompile(t *testing.T) {
	t.Parallel()

	document := aiDocument(nil, nil)
	// Drop the model edge, keeping the model node itself: an author who
	// detaches a model leaves the node on the canvas.
	document.Connections = document.Connections[:1]

	_, err := workflow.Compile(document, aiRegistry(t))
	if err == nil {
		t.Fatal("an agent with no chat model compiled")
	}
	var issues *workflow.ValidationErrors
	if !errors.As(err, &issues) {
		t.Fatalf("Compile() error = %v, want validation errors", err)
	}
	if !hasValidationCode(issues.Issues, workflow.ErrorPortRequired) {
		t.Fatalf("issues = %#v, want %q", issues.Issues, workflow.ErrorPortRequired)
	}
	// The empty slot must be named, not merely counted.
	var named bool
	for _, issue := range issues.Issues {
		if issue.Code == workflow.ErrorPortRequired && strings.Contains(issue.Message, "Chat Model") {
			named = true
		}
	}
	if !named {
		t.Errorf("issues = %#v, want the empty slot named", issues.Issues)
	}
}

// TestAnAgentWithNoMemoryAndNoToolsStillCompiles is the boundary the cap rules
// must not cross. Only the model slot is required; a memory and a tool are
// things an agent may have, so requiring them would refuse the simplest useful
// agent there is.
func TestAnAgentWithNoMemoryAndNoToolsStillCompiles(t *testing.T) {
	t.Parallel()

	if _, err := workflow.Compile(aiDocument(nil, nil), aiRegistry(t)); err != nil {
		t.Fatalf("Compile() error = %v, want an agent with only a model accepted", err)
	}
}

func hasValidationCode(issues []workflow.ValidationError, wanted workflow.ErrorCode) bool {
	for _, issue := range issues {
		if issue.Code == wanted {
			return true
		}
	}
	return false
}

func portFullMessage(issues []workflow.ValidationError) string {
	for _, issue := range issues {
		if issue.Code == workflow.ErrorPortFull {
			return issue.Message
		}
	}
	return ""
}

// TestASecondModelDescriptorIsRefusedRatherThanSilentlyPicked is the guard
// behind the compiler's cap. The cap is what should stop this graph, but a
// document that reached the runner without being compiled — or a cap that
// stopped being enforced — would otherwise land here, where returning the first
// descriptor runs a model the author never chose and reports success.
func TestASecondModelDescriptorIsRefusedRatherThanSilentlyPicked(t *testing.T) {
	t.Parallel()

	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	definition, _ := aiRegistry(t).Lookup(nodes.AgentNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"prompt": "hi"}, Definition: definition,
	}
	descriptor := func(name string) workflow.Item {
		return workflow.Item{JSON: map[string]any{"$ai": map[string]any{
			"kind": "model", "model": name, "baseUrl": "https://api.test/v1", "credentialId": "cred-key",
		}}}
	}

	_, err := executor.Execute(context.Background(), ir, workflow.NodeInput{
		"main":  {{JSON: map[string]any{}}},
		"model": {descriptor("gpt-first"), descriptor("gpt-second")},
	}, engine.Request{})
	if err == nil {
		t.Fatal("two model descriptors were accepted; one of them ran and nothing said which")
	}
	// The message names the slot, because "model" is what the author has to go
	// and disconnect.
	if !strings.Contains(err.Error(), "model port") {
		t.Errorf("Execute() error = %v, want the model slot named", err)
	}
}

// aiClusterDocument is an agent with one chat model and one tool per name, with
// the tool edges declared in the order given. Declaration order is a parameter
// precisely because it is the thing that must not decide what the agent sees.
func aiClusterDocument(providerURL string, toolNames, edgeOrder []string) workflow.Document {
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_cluster",
		Name:          "Cluster",
		Nodes: []workflow.Node{
			{ID: "trigger", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{
				ID: "model", Name: "Chat Model", Type: nodes.ChatModelNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{
					"model": "gpt-test", "baseUrl": providerURL,
					// Explicitly off: the streaming path answers in chunks and
					// this test reads one recorded request body.
					"stream": false,
				},
				Credentials: map[string]string{"httpBearerAuth": "cred-key"},
			},
			{
				ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"prompt": "Say hello"},
			},
		},
		Connections: []workflow.Connection{
			{ID: "c-main", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "trigger", Port: "main"}, Target: workflow.Endpoint{NodeID: "agent", Port: "main"}},
			{ID: "c-model", Kind: workflow.ConnectionLanguageModel, Source: workflow.Endpoint{NodeID: "model", Port: "model"}, Target: workflow.Endpoint{NodeID: "agent", Port: "model"}},
		},
		Settings: map[string]any{},
	}
	for _, name := range toolNames {
		document.Nodes = append(document.Nodes, workflow.Node{
			ID: "tool-" + name, Name: "Tool " + name, Type: nodes.HTTPToolNodeType, TypeVersion: workflow.V(1),
			Parameters: map[string]any{
				"toolName": name, "toolDescription": "does " + name,
				"method": "GET", "url": "https://api.test/" + name,
			},
		})
	}
	for _, name := range edgeOrder {
		document.Connections = append(document.Connections, workflow.Connection{
			ID: "c-tool-" + name, Kind: workflow.ConnectionTool,
			Source: workflow.Endpoint{NodeID: "tool-" + name, Port: "tool"},
			Target: workflow.Endpoint{NodeID: "agent", Port: "tools"},
		})
	}
	return document
}

// runCluster compiles and runs one agent cluster, returning the tool names the
// model was offered, in the order it was offered them.
func runCluster(t *testing.T, document workflow.Document, providerBody *map[string]any) []string {
	t.Helper()

	ir, err := workflow.Compile(document, aiRegistry(t))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	resolver := &stubCredentials{credential: engine.Credential{
		ID: "cred-key", Name: "Provider", Type: "httpBearerAuth",
		Fields: map[string]string{"token": "sk-live-secret"},
	}}
	if _, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{
		Input:       workflow.Item{JSON: map[string]any{}},
		Credentials: resolver,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	offered, _ := (*providerBody)["tools"].([]any)
	names := make([]string, 0, len(offered))
	for _, entry := range offered {
		tool, _ := entry.(map[string]any)
		function, _ := tool["function"].(map[string]any)
		name, _ := function["name"].(string)
		names = append(names, name)
	}
	return names
}

// TestEveryAttachedToolReachesTheAgentInAStableOrder is the uncapped half of the
// slot rules. A tool slot takes as many connections as an author likes, so the
// guarantee it owes is not a bound but determinism: every attached tool arrives,
// none twice, and in an order fixed by the graph rather than by the order the
// edges happen to sit in the document. The edges here are declared scrambled for
// that reason — a run whose tool order tracked declaration order would let a
// cosmetic reordering of the saved JSON change which tool a model reaches for
// first.
func TestEveryAttachedToolReachesTheAgentInAStableOrder(t *testing.T) {
	t.Parallel()

	var body map[string]any
	provider := answerOnce(&body, 0)
	defer provider.Close()

	tools := []string{"alpha", "bravo", "charlie"}
	scrambled := runCluster(t, aiClusterDocument(provider.URL, tools, []string{"charlie", "alpha", "bravo"}), &body)

	if len(scrambled) != len(tools) {
		t.Fatalf("the model was offered %d tools (%v), want %d", len(scrambled), scrambled, len(tools))
	}
	for index, want := range tools {
		if scrambled[index] != want {
			t.Fatalf("tool order = %v, want %v", scrambled, tools)
		}
	}

	// The same cluster with the edges written in a different order must offer
	// the same list. This is the assertion that would fail if delivery order
	// ever became document order.
	reversed := runCluster(t, aiClusterDocument(provider.URL, tools, []string{"bravo", "charlie", "alpha"}), &body)
	for index, want := range scrambled {
		if reversed[index] != want {
			t.Fatalf("reordering the edges changed the tool order: %v then %v", scrambled, reversed)
		}
	}
}

func runAgentNode(t *testing.T, parameters map[string]any, input workflow.NodeInput, resolver *stubCredentials, published *[]string) (workflow.NodeOutput, error) {
	t.Helper()
	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	definition, _ := aiRegistry(t).Lookup(nodes.AgentNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: workflow.V(1),
		Parameters: parameters, Definition: definition,
	}
	request := engine.Request{Credentials: resolver}
	if published != nil {
		request.Events = func(event engine.NodeEvent) { *published = append(*published, event.Name) }
	}
	return executor.Execute(context.Background(), ir, input, request)
}

func agentModelInput(providerURL string) workflow.NodeInput {
	return workflow.NodeInput{
		"main": {{JSON: map[string]any{}}},
		"model": {{JSON: map[string]any{"$ai": map[string]any{
			"kind": "model", "model": "gpt-test", "baseUrl": providerURL, "credentialId": "cred-key",
		}}}},
	}
}

func bearerResolver() *stubCredentials {
	return &stubCredentials{credential: engine.Credential{
		ID: "cred-key", Name: "Provider", Type: "httpBearerAuth",
		Fields: map[string]string{"token": "sk-live-secret"},
	}}
}

func firstMessageContent(t *testing.T, received map[string]any) string {
	t.Helper()
	messages, ok := received["messages"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatalf("provider received %#v, want a message list", received["messages"])
	}
	first, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("first message = %#v, want an object", messages[0])
	}
	content, _ := first["content"].(string)
	return content
}

// TestAgentDefaultIterationsMatchTheRuntimeBound pins the two defaults
// together. The node's declared default is what the user sees; the package
// constant is what a node saved before the parameter existed runs with. If
// they disagree, older nodes silently run a different bound than the editor
// shows.
func TestAgentDefaultIterationsMatchTheRuntimeBound(t *testing.T) {
	t.Parallel()

	definition, _ := aiRegistry(t).Get(nodes.AgentNodeType, workflow.V(1))
	for _, property := range definition.Parameters {
		if property.Key != "maxIterations" {
			continue
		}
		var declared float64
		switch value := property.Default.(type) {
		case int:
			declared = float64(value)
		case float64:
			declared = value
		default:
			t.Fatalf("maxIterations default = %#v, want a number", property.Default)
		}
		if declared != float64(ai.DefaultMaxIterations) {
			t.Fatalf("node default = %v, runtime default = %d", declared, ai.DefaultMaxIterations)
		}
		return
	}
	t.Fatal("the agent has no maxIterations parameter")
}

// TestAParserRunThatHitsTheIterationBoundAnswersWithTheFallback is the first
// defect: the loop answers a run that cannot converge with a stated fallback
// instead of failing, but with a parser attached the node then tried to
// json.Unmarshal that sentence — so a run the runtime had already called a
// success failed the node with "parser output is not valid JSON".
func TestAParserRunThatHitsTheIterationBoundAnswersWithTheFallback(t *testing.T) {
	t.Parallel()

	// A model that asks for the tool again on every turn: nothing but the
	// iteration bound stops it, so the run ends on the fallback.
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"calculator","arguments":"{\"expression\":\"1+1\"}"}}]},"finish_reason":"tool_calls"}]}`))
	}))
	defer provider.Close()

	input := agentModelInput(provider.URL)
	input["tools"] = []workflow.Item{{JSON: map[string]any{"$ai": map[string]any{
		"kind": "calculator", "name": "calculator", "description": "does arithmetic",
		"nodeName": "Calculator Tool",
	}}}}
	input["outputParser"] = []workflow.Item{{JSON: map[string]any{"$ai": map[string]any{
		"kind": "parser", "name": "Structured Output Parser", "nodeName": "Structured Output Parser",
		"schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"total": map[string]any{"type": "number"},
			},
			"required": []any{"total"},
		},
	}}}}

	output, err := runAgentNode(t, map[string]any{"prompt": "what is 1+1", "maxIterations": 2},
		input, bearerResolver(), nil)
	if err != nil {
		t.Fatalf("Execute() error = %v, want the stated fallback rather than a parse failure", err)
	}
	if output[0][0].JSON["output"] != ai.MaxIterationsMessage {
		t.Fatalf("output = %#v, want %q", output[0][0].JSON["output"], ai.MaxIterationsMessage)
	}
}

func TestSystemMessageWinsOverLegacySystemPrompt(t *testing.T) {
	t.Parallel()

	var received map[string]any
	provider := answerOnce(&received, 0)
	defer provider.Close()

	if _, err := runAgentNode(t, map[string]any{
		"prompt": "hi", "systemMessage": "You are concise.", "systemPrompt": "You are verbose.",
	}, agentModelInput(provider.URL), bearerResolver(), nil); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if content := firstMessageContent(t, received); content != "You are concise." {
		t.Fatalf("system message = %q, want the systemMessage value", content)
	}

	// Without a systemMessage the legacy key still works, so graphs saved
	// before the rename run unchanged.
	if _, err := runAgentNode(t, map[string]any{
		"prompt": "hi", "systemPrompt": "You are verbose.",
	}, agentModelInput(provider.URL), bearerResolver(), nil); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if content := firstMessageContent(t, received); content != "You are verbose." {
		t.Fatalf("system message = %q, want the legacy systemPrompt honoured", content)
	}
}

func TestReturnIntermediateStepsControlsTheOutputShape(t *testing.T) {
	t.Parallel()

	// A run that calls a tool, so the step list has something to pair: n8n
	// reports one entry per tool call — {action, observation} — rather than the
	// raw message list, which echoed the system prompt into the output.
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_7","type":"function","function":{"name":"plus","arguments":"{\"a\":1,\"b\":2}"}}]},"finish_reason":"tool_calls"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"3"},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()

	input := agentModelInput(provider.URL)
	input["tools"] = []workflow.Item{{JSON: map[string]any{"$ai": map[string]any{
		"kind": "calculator", "name": "plus", "description": "adds",
		"parameters": map[string]any{"expression": map[string]any{"mode": "expression", "value": "{{ $json.a }} + {{ $json.b }}"}},
	}}}}
	with, err := runAgentNode(t, map[string]any{
		"prompt": "hi", "systemMessage": "You are concise.", "returnIntermediateSteps": true,
	}, input, bearerResolver(), nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// The step list crosses into the execution record as JSON, so that is the
	// shape asserted here rather than the executor's internal type.
	encoded, err := json.Marshal(with[0][0].JSON["intermediateSteps"])
	if err != nil {
		t.Fatalf("encode intermediateSteps: %v", err)
	}
	var steps []map[string]any
	if err := json.Unmarshal(encoded, &steps); err != nil {
		t.Fatalf("decode intermediateSteps: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("steps = %s, want one step for the one tool call", encoded)
	}
	action, _ := steps[0]["action"].(map[string]any)
	input0, _ := action["toolInput"].(map[string]any)
	if action["tool"] != "plus" || steps[0]["observation"] != `{"result":3}` || input0["a"] != float64(1) {
		t.Fatalf("step = %s, want n8n's action with the call's input and its observation", encoded)
	}
	if strings.Contains(string(encoded), "You are concise.") {
		t.Fatalf("intermediateSteps leaked the system prompt: %s", encoded)
	}

	without, err := runAgentNode(t, map[string]any{
		"prompt": "hi",
	}, agentModelInput(provider.URL), bearerResolver(), nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, present := without[0][0].JSON["intermediateSteps"]; present {
		t.Fatalf("output = %#v, want no intermediateSteps key when the toggle is off", without[0][0].JSON)
	}
	for _, key := range []string{"output", "usage", "iterations", "toolCalls"} {
		if _, present := without[0][0].JSON[key]; !present {
			t.Errorf("output lost key %q when the toggle is off", key)
		}
	}
}

func TestToolNameDerivesFromTheCanvasName(t *testing.T) {
	t.Parallel()

	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	definition, _ := aiRegistry(t).Lookup(nodes.HTTPToolNodeType, workflow.V(1))
	runTool := func(name string, parameters map[string]any) map[string]any {
		t.Helper()
		ir := workflow.IRNode{
			ID: "tool", Name: name, Type: nodes.HTTPToolNodeType, TypeVersion: workflow.V(1),
			Parameters: parameters, Definition: definition,
		}
		output, err := runExecutor(t, executors, nodes.HTTPToolExecutorID, ir, workflow.NodeInput{}, engine.Request{})
		if err != nil {
			t.Fatalf("tool %q error = %v", name, err)
		}
		descriptor, ok := output[0][0].JSON["$ai"].(map[string]any)
		if !ok {
			t.Fatalf("tool %q emitted %#v, want a descriptor", name, output[0][0].JSON)
		}
		return descriptor
	}
	base := map[string]any{"method": "GET", "url": "https://api.test/x", "toolDescription": "does a thing"}

	derived := runTool("Get Weather", base)
	if derived["name"] != "Get_Weather" {
		t.Errorf("derived name = %q, want the canvas name normalised", derived["name"])
	}

	overridden := runTool("Get Weather", map[string]any{
		"method": "GET", "url": "https://api.test/x",
		"toolDescription": "does a thing", "toolName": "custom_tool",
	})
	if overridden["name"] != "custom_tool" {
		t.Errorf("overridden name = %q, want the explicit override kept", overridden["name"])
	}
}

func TestDuplicateToolNamesFailFastNamingBothNodes(t *testing.T) {
	t.Parallel()

	var received map[string]any
	provider := answerOnce(&received, 0)
	defer provider.Close()

	input := agentModelInput(provider.URL)
	tool := func(nodeName string) workflow.Item {
		return workflow.Item{JSON: map[string]any{"$ai": map[string]any{
			"kind": "tool", "name": "get_weather", "description": "Weather", "nodeName": nodeName,
			"parameters": map[string]any{}, "credentials": map[string]any{},
		}}}
	}
	input["tools"] = []workflow.Item{tool("Weather A"), tool("Weather B")}

	_, err := runAgentNode(t, map[string]any{"prompt": "hi"}, input, bearerResolver(), nil)
	if err == nil {
		t.Fatal("Execute() accepted two tools under one name")
	}
	for _, want := range []string{"get_weather", "Weather A", "Weather B"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name %q", err.Error(), want)
		}
	}
	if received["messages"] != nil {
		t.Error("the model was called before the duplicate was refused")
	}
}

func TestAgentEnforcesPerNodeRetention(t *testing.T) {
	t.Parallel()

	memory, err := ai.NewBufferMemory(ai.Retention{}, nil)
	if err != nil {
		t.Fatalf("NewBufferMemory() error = %v", err)
	}
	var received map[string]any
	provider := answerOnce(&received, 0)
	defer provider.Close()

	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), memory)
	definition, _ := aiRegistry(t).Lookup(nodes.AgentNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"prompt": "remember this"}, Definition: definition,
	}
	resolver := bearerResolver()
	execution := engine.ExecutionContext{TenantID: "tenant-a", WorkflowID: "wf-1"}
	for range 3 {
		input := agentModelInput(provider.URL)
		input["memory"] = []workflow.Item{{JSON: map[string]any{"$ai": map[string]any{
			"kind": "memory", "nodeName": "Memory",
			"parameters": map[string]any{
				"sessionIdType": "customKey", "sessionKey": "chat-1",
				"maxMessages": float64(2), "maxAgeMinutes": float64(60),
			},
		}}}}
		if _, err := executor.Execute(context.Background(), ir, input,
			engine.Request{Credentials: resolver, Execution: execution}); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
	}
	// Three runs stored six turns; the node's bound of two keeps two.
	loaded, err := memory.Load(context.Background(), ai.SessionKey{
		TenantID: "tenant-a", WorkflowID: "wf-1", SessionID: "chat-1",
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded %d messages, want the node's bound of two", len(loaded))
	}
}

func runMemoryNode(t *testing.T, name string, parameters map[string]any, live workflow.Item) map[string]any {
	t.Helper()
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	definition, _ := aiRegistry(t).Lookup(nodes.MemoryNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "memory", Name: name, Type: nodes.MemoryNodeType, TypeVersion: workflow.V(1),
		Parameters: parameters, Definition: definition,
	}
	output, err := runExecutor(t, executors, nodes.MemoryExecutorID, ir,
		workflow.NodeInput{}, engine.Request{Input: live})
	if err != nil {
		t.Fatalf("memory %q error = %v", name, err)
	}
	descriptor, ok := output[0][0].JSON["$ai"].(map[string]any)
	if !ok {
		t.Fatalf("memory %q emitted %#v, want a descriptor", name, output[0][0].JSON)
	}
	return descriptor
}

// memoryDescriptorFor runs the memory node and returns the descriptor it
// emits, which is what the agent resolves per item.
func memoryDescriptorFor(t *testing.T, name string, parameters map[string]any) map[string]any {
	t.Helper()
	return runMemoryNode(t, name, parameters, workflow.Item{JSON: map[string]any{}})
}

// sessionRecorder is the memory store the agent writes through, remembering
// which conversation each run addressed. Probing a fixed list of session IDs
// would only find the keys a test already expected.
type sessionRecorder struct {
	inner    *ai.BufferMemory
	sessions []string
}

func (recorder *sessionRecorder) Load(ctx context.Context, session ai.SessionKey) ([]ai.Message, error) {
	return recorder.inner.Load(ctx, session)
}

func (recorder *sessionRecorder) Append(ctx context.Context, session ai.SessionKey, messages []ai.Message) error {
	recorder.sessions = append(recorder.sessions, session.SessionID)
	return recorder.inner.Append(ctx, session, messages)
}

func (recorder *sessionRecorder) LoadWithPolicy(ctx context.Context, session ai.SessionKey, retention ai.Retention) ([]ai.Message, error) {
	return recorder.inner.LoadWithPolicy(ctx, session, retention)
}

func (recorder *sessionRecorder) AppendWithPolicy(ctx context.Context, session ai.SessionKey, messages []ai.Message, retention ai.Retention) error {
	recorder.sessions = append(recorder.sessions, session.SessionID)
	return recorder.inner.AppendWithPolicy(ctx, session, messages, retention)
}

// agentSessionsFor runs the agent once per item with a memory node's
// descriptor and reports the conversations the store ended up holding, keyed
// by session ID.
func agentSessionsFor(t *testing.T, descriptor map[string]any, nodeItems map[string]expression.NodeItem, items ...workflow.Item) map[string][]ai.Message {
	t.Helper()

	inner, err := ai.NewBufferMemory(ai.Retention{MaxMessages: 40, MaxAge: time.Hour}, nil)
	if err != nil {
		t.Fatalf("NewBufferMemory() error = %v", err)
	}
	recorder := &sessionRecorder{inner: inner}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()

	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), recorder)
	definition, _ := aiRegistry(t).Lookup(nodes.AgentNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"prompt": "hi"}, Definition: definition,
	}
	input := agentModelInput(provider.URL)
	input["main"] = items
	input["memory"] = []workflow.Item{{JSON: map[string]any{"$ai": descriptor}}}
	request := engine.Request{
		Credentials: bearerResolver(),
		Execution:   engine.ExecutionContext{TenantID: "tenant-a", WorkflowID: "wf-1"},
		NodeItems:   nodeItems,
	}
	if _, err := executor.Execute(context.Background(), ir, input, request); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	sessions := map[string][]ai.Message{}
	for _, sessionID := range recorder.sessions {
		loaded, err := inner.Load(context.Background(), ai.SessionKey{
			TenantID: "tenant-a", WorkflowID: "wf-1", SessionID: sessionID,
		})
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		sessions[sessionID] = loaded
	}
	return sessions
}

// TestMemorySessionKeyModesStayUnresolvedUntilTheAgentRuns: the memory node
// runs before the trigger, so resolving its key there read the execution input
// rather than the item the agent is answering, and `$json.sessionId` or
// `$('Trigger')` had nothing to resolve against.
func TestMemorySessionKeyModesStayUnresolvedUntilTheAgentRuns(t *testing.T) {
	t.Parallel()

	fromInput := memoryDescriptorFor(t, "Memory", map[string]any{
		"sessionIdType": "fromInput", "sessionKey": "chat-9",
		"maxMessages": float64(10), "maxAgeMinutes": float64(30),
	})
	parameters, _ := fromInput["parameters"].(map[string]any)
	if parameters["sessionIdType"] != "fromInput" || parameters["sessionKey"] != "chat-9" {
		t.Fatalf("descriptor = %#v, want the node's own parameters carried", fromInput)
	}
	if fromInput["nodeName"] != "Memory" {
		t.Fatalf("descriptor = %#v, want the memory node's name for the key suffix", fromInput)
	}

	// An expression-valued key is carried as written, not resolved against
	// whatever input the memory node happened to see.
	expressionKey := memoryDescriptorFor(t, "Memory", map[string]any{
		"sessionIdType": "customKey",
		"sessionKey":    map[string]any{"mode": "expression", "value": "{{ $json.sessionId }}"},
	})
	expressionParameters, _ := expressionKey["parameters"].(map[string]any)
	if _, isMap := expressionParameters["sessionKey"].(map[string]any); !isMap {
		t.Fatalf("sessionKey = %#v, want the expression carried to the agent", expressionParameters["sessionKey"])
	}
}

// TestMemorySessionKeyIsResolvedPerItem: a batch gives every item its own
// conversation, and the key is read from that item rather than from the
// execution input.
func TestMemorySessionKeyIsResolvedPerItem(t *testing.T) {
	t.Parallel()

	descriptor := memoryDescriptorFor(t, "Memory", map[string]any{
		"sessionIdType": "fromInput",
		"sessionKey":    map[string]any{"mode": "expression", "value": "{{ $json.sessionId }}"},
	})
	sessions := agentSessionsFor(t, descriptor, nil,
		workflow.Item{JSON: map[string]any{"sessionId": "alice"}},
		workflow.Item{JSON: map[string]any{"sessionId": "bob"}},
	)
	if len(sessions) != 2 {
		t.Fatalf("sessions = %#v, want one conversation per item", sessions)
	}
	for _, sessionID := range []string{"alice__Memory", "bob__Memory"} {
		if len(sessions[sessionID]) != 2 {
			t.Errorf("session %q = %#v, want the item's own exchange", sessionID, sessions[sessionID])
		}
	}
}

// TestMemorySessionKeyCanReferenceAnUpstreamNode is the reported repro: a chat
// trigger's id, read through $('Trigger'), reached the memory node before the
// trigger had produced anything.
func TestMemorySessionKeyCanReferenceAnUpstreamNode(t *testing.T) {
	t.Parallel()

	descriptor := memoryDescriptorFor(t, "Memory", map[string]any{
		"sessionIdType": "customKey",
		"sessionKey":    map[string]any{"mode": "expression", "value": "{{ $('Trigger').item.json.sessionId }}"},
	})
	nodeItems := map[string]expression.NodeItem{
		"Trigger": {
			Items:  []map[string]any{{"sessionId": "from-trigger"}},
			Paired: map[string]any{"sessionId": "from-trigger"},
		},
	}
	sessions := agentSessionsFor(t, descriptor, nodeItems, workflow.Item{JSON: map[string]any{}})
	if len(sessions["from-trigger"]) != 2 {
		t.Fatalf("sessions = %#v, want the trigger's id as the conversation", sessions)
	}
}

// TestMemoryCustomAndLegacyKeysStillAddressOneConversation pins the two modes
// that must not change: a defined key is used verbatim, and a node saved
// before the modes existed keeps the key it always had.
func TestMemoryCustomAndLegacyKeysStillAddressOneConversation(t *testing.T) {
	t.Parallel()

	shared := memoryDescriptorFor(t, "Memory", map[string]any{
		"sessionIdType": "customKey", "sessionKey": "shared",
	})
	if sessions := agentSessionsFor(t, shared, nil, workflow.Item{JSON: map[string]any{}}); len(sessions["shared"]) != 2 {
		t.Fatalf("sessions = %#v, want the defined key used verbatim", sessions)
	}

	legacy := memoryDescriptorFor(t, "Memory", map[string]any{"sessionId": "old-1"})
	if sessions := agentSessionsFor(t, legacy, nil, workflow.Item{JSON: map[string]any{}}); len(sessions["old-1"]) != 2 {
		t.Fatalf("sessions = %#v, want the legacy key kept as it was", sessions)
	}
}

func TestMemorySessionKeyValidation(t *testing.T) {
	t.Parallel()

	definition, _ := aiRegistry(t).Lookup(nodes.MemoryNodeType, workflow.V(1))
	for name, parameters := range map[string]map[string]any{
		"fromInput without a key": {"sessionIdType": "fromInput"},
		"nothing at all":          {},
		"unknown mode":            {"sessionIdType": "carrierPigeon", "sessionKey": "k"},
	} {
		if err := definition.Validate(workflow.Node{Parameters: parameters}); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	for name, parameters := range map[string]map[string]any{
		"custom key":   {"sessionIdType": "customKey", "sessionKey": "shared"},
		"legacy key":   {"sessionId": "old-1"},
		"legacy alias": {"sessionKey": "typed-into-old-node"},
	} {
		if err := definition.Validate(workflow.Node{Parameters: parameters}); err != nil {
			t.Errorf("%s rejected: %v", name, err)
		}
	}
}

func runChainNode(t *testing.T, parameters map[string]any, input workflow.NodeInput, resolver *stubCredentials, published *[]string) (workflow.NodeOutput, error) {
	t.Helper()
	executor := nodes.NewChainExecutor(localPolicy(), 0)
	definition, _ := aiRegistry(t).Lookup(nodes.ChainNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "chain", Name: "Basic LLM Chain", Type: nodes.ChainNodeType, TypeVersion: workflow.V(1),
		Parameters: parameters, Definition: definition,
	}
	request := engine.Request{Credentials: resolver}
	if published != nil {
		request.Events = func(event engine.NodeEvent) { *published = append(*published, event.Name) }
	}
	return executor.Execute(context.Background(), ir, input, request)
}

// countingProvider answers every model call with the same text while
// recording each request body, so a test asserts call count and per-item
// prompts rather than taking either on faith.
func countingProvider(t *testing.T, bodies *[]map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		decoded := map[string]any{}
		_ = json.Unmarshal(body, &decoded)
		*bodies = append(*bodies, decoded)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"42"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	}))
}

func chainModelInput(providerURL string, items ...workflow.Item) workflow.NodeInput {
	return workflow.NodeInput{
		"main": items,
		"model": {{JSON: map[string]any{"$ai": map[string]any{
			"kind": "model", "model": "gpt-test", "baseUrl": providerURL, "credentialId": "cred-key",
		}}}},
	}
}

func promptContents(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	messages, ok := body["messages"].([]any)
	if !ok {
		t.Fatalf("provider received %#v, want a message list", body["messages"])
	}
	contents := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		entry, ok := message.(map[string]any)
		if !ok {
			t.Fatalf("message = %#v, want an object", message)
		}
		contents = append(contents, entry)
	}
	return contents
}

// TestChainRunsOneModelCallPerItem is the node's core contract: no loop, no
// memory, one call per item, proven by the bodies the provider received and
// the ai.model.* events on the execution feed.
func TestChainRunsOneModelCallPerItem(t *testing.T) {
	t.Parallel()

	var bodies []map[string]any
	provider := countingProvider(t, &bodies)
	defer provider.Close()

	var published []string
	output, err := runChainNode(t,
		map[string]any{"promptType": "auto", "inputField": "question"},
		chainModelInput(provider.URL,
			workflow.Item{JSON: map[string]any{"question": "First?"}},
			workflow.Item{JSON: map[string]any{"question": "Second?"}},
		),
		bearerResolver(), &published)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output[0]) != 2 {
		t.Fatalf("output has %d items, want one per incoming item", len(output[0]))
	}
	for index, item := range output[0] {
		// n8n's chain answers with `text` when no parser is attached, and
		// imported references read that field.
		if item.JSON["text"] != "42" {
			t.Errorf("item %d text = %#v, want the model's answer under n8n's key", index, item.JSON["text"])
		}
	}
	if len(bodies) != 2 {
		t.Fatalf("provider received %d calls, want exactly one per item", len(bodies))
	}
	for index, want := range []string{"First?", "Second?"} {
		messages := promptContents(t, bodies[index])
		if len(messages) != 1 || messages[0]["content"] != want {
			t.Errorf("call %d messages = %#v, want the item's field as one user turn", index, messages)
		}
	}
	started, completed := 0, 0
	for _, name := range published {
		switch name {
		case string(ai.EventModelStarted):
			started++
		case string(ai.EventModelCompleted):
			completed++
		}
	}
	if started != 2 || completed != 2 {
		t.Errorf("events = %v, want one model started/completed pair per item", published)
	}
}

func TestChainMessageListRendersTypedRowsWithExpressions(t *testing.T) {
	t.Parallel()

	var bodies []map[string]any
	provider := countingProvider(t, &bodies)
	defer provider.Close()

	output, err := runChainNode(t,
		map[string]any{
			"promptType": "define",
			"text":       "Summarise:",
			"messages": map[string]any{"messageValues": []any{
				map[string]any{"type": "system", "message": "You are a summariser."},
				map[string]any{"type": "human", "message": map[string]any{"mode": "expression", "value": "{{ $json.prefix }}"}},
			}},
		},
		chainModelInput(provider.URL, workflow.Item{JSON: map[string]any{"prefix": "Be brief."}}),
		bearerResolver(), nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output[0][0].JSON["text"] != "42" {
		t.Fatalf("output = %#v, want the model's text", output[0][0].JSON)
	}
	messages := promptContents(t, bodies[0])
	if len(messages) != 3 {
		t.Fatalf("messages = %#v, want two rows then the user message", messages)
	}
	if messages[0]["role"] != "system" || messages[0]["content"] != "You are a summariser." {
		t.Errorf("row 1 = %#v, want the system row verbatim", messages[0])
	}
	if messages[1]["role"] != "user" || messages[1]["content"] != "Be brief." {
		t.Errorf("row 2 = %#v, want the row expression resolved against the item", messages[1])
	}
	if messages[2]["role"] != "user" || messages[2]["content"] != "Summarise:" {
		t.Errorf("row 3 = %#v, want the defined text as the final user turn", messages[2])
	}
}

func TestChainValidationNamesTheEmptyRow(t *testing.T) {
	t.Parallel()

	definition, _ := aiRegistry(t).Lookup(nodes.ChainNodeType, workflow.V(1))
	emptyRow := map[string]any{
		"promptType": "define", "text": "Summarise:",
		"messages": map[string]any{"messageValues": []any{
			map[string]any{"type": "system", "message": "You are a summariser."},
			map[string]any{"type": "human", "message": ""},
		}},
	}
	if err := definition.Validate(workflow.Node{Parameters: emptyRow}); err == nil ||
		!strings.Contains(err.Error(), "message 2") {
		t.Errorf("Validate() = %v, want an error naming row 2", err)
	}

	badType := map[string]any{
		"messages": map[string]any{"messageValues": []any{
			map[string]any{"type": "carrierPigeon", "message": "hi"},
		}},
	}
	if err := definition.Validate(workflow.Node{Parameters: badType}); err == nil ||
		!strings.Contains(err.Error(), "message 1") {
		t.Errorf("Validate() = %v, want an error naming row 1", err)
	}

	emptyDefine := map[string]any{"promptType": "define"}
	if err := definition.Validate(workflow.Node{Parameters: emptyDefine}); err == nil {
		t.Errorf("Validate() accepted a defined prompt with no text and no messages")
	}

	valid := map[string]any{
		"promptType": "define", "text": "Summarise:",
		"messages": map[string]any{"messageValues": []any{
			map[string]any{"type": "system", "message": "You are a summariser."},
		}},
	}
	if err := definition.Validate(workflow.Node{Parameters: valid}); err != nil {
		t.Errorf("Validate() = %v, want a complete chain accepted", err)
	}
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"promptType": "auto"}}); err != nil {
		t.Errorf("Validate() = %v, want field mode accepted without rows", err)
	}
}

func TestChainRequiresAModelOnTheModelPort(t *testing.T) {
	t.Parallel()

	// The descriptor lives on the model port by name. Main carries items on
	// a well-formed graph and something surprising on a malformed one, so a
	// graph with no model port must fail here rather than read main.
	_, err := runChainNode(t, map[string]any{"promptType": "auto"},
		workflow.NodeInput{"main": {{JSON: map[string]any{"question": "hi"}}}},
		bearerResolver(), nil)
	if err == nil || !strings.Contains(err.Error(), "model port") {
		t.Fatalf("Execute() = %v, want a missing-model rejection", err)
	}
}

func TestChainRejectsAnEmptyPrompt(t *testing.T) {
	t.Parallel()

	var bodies []map[string]any
	provider := countingProvider(t, &bodies)
	defer provider.Close()

	_, err := runChainNode(t, map[string]any{"promptType": "auto", "inputField": "question"},
		chainModelInput(provider.URL, workflow.Item{JSON: map[string]any{"other": "field"}}),
		bearerResolver(), nil)
	if err == nil || !strings.Contains(err.Error(), "prompt is empty") {
		t.Fatalf("Execute() = %v, want an empty-prompt rejection", err)
	}
	if len(bodies) != 0 {
		t.Error("the model was called with an empty prompt")
	}
}

func TestChainRefusesAnOutputParserItCannotRun(t *testing.T) {
	t.Parallel()

	var bodies []map[string]any
	provider := countingProvider(t, &bodies)
	defer provider.Close()

	input := chainModelInput(provider.URL, workflow.Item{JSON: map[string]any{"question": "hi"}})
	input["outputParser"] = []workflow.Item{{JSON: map[string]any{"$ai": map[string]any{"kind": "parser"}}}}
	_, err := runChainNode(t, map[string]any{"promptType": "auto"},
		input, bearerResolver(), nil)
	if err == nil || !strings.Contains(err.Error(), "output parser") {
		t.Fatalf("Execute() = %v, want the parser refused by name", err)
	}
}

// TestHTTPToolReevaluatesItsFromAIArgumentsOnEveryCall is the reported repro:
// the tool wrote the first call's substituted parameters back over its own
// template, so every later call repeated the first request while the agent
// answered from the wrong data.
func TestHTTPToolReevaluatesItsFromAIArgumentsOnEveryCall(t *testing.T) {
	t.Parallel()

	requested := make([]string, 0, 2)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.Query().Get("city"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"temp":"18C"}`))
	}))
	defer stub.Close()

	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}]}`))
			return
		}
		if calls == 2 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_2","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Tokyo\"}"}}]},"finish_reason":"tool_calls"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"both looked up"},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()

	input := agentModelInput(provider.URL)
	input["tools"] = []workflow.Item{{JSON: map[string]any{"$ai": map[string]any{
		"kind": "tool", "name": "get_weather", "description": "Weather", "nodeName": "Get Weather",
		"parameters": map[string]any{
			"method": "GET",
			"url": map[string]any{
				"mode":  "expression",
				"value": stub.URL + "/weather?city={{ $fromAI('city','the city name','string') }}",
			},
		},
		"credentials": map[string]any{},
	}}}}
	if _, err := runAgentNode(t, map[string]any{"prompt": "Paris and Tokyo?", "maxIterations": 3},
		input, bearerResolver(), nil); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(requested) != 2 || requested[0] != "Paris" || requested[1] != "Tokyo" {
		t.Fatalf("requests = %#v, want each call to use its own arguments", requested)
	}
}

// TestCalculatorToolEvaluatesWhatTheModelAsks pins both halves of the fix: the
// tool needs no configured expression, and a literal one no longer answers
// every call with the same number.
func TestCalculatorToolEvaluatesWhatTheModelAsks(t *testing.T) {
	t.Parallel()

	definition, found := aiRegistry(t).Get(nodes.CalculatorToolNodeType, workflow.V(1))
	if !found {
		t.Fatal("the Calculator Tool node is not registered")
	}
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"toolDescription": "does arithmetic"}}); err != nil {
		t.Fatalf("a calculator tool with no expression was refused: %v", err)
	}

	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"calculator","arguments":"{\"expression\":\"17 * 23\"}"}}]},"finish_reason":"tool_calls"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"391"},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()

	var published []ai.Event
	input := agentModelInput(provider.URL)
	input["tools"] = []workflow.Item{{JSON: map[string]any{"$ai": map[string]any{
		"kind": "calculator", "name": "calculator", "description": "does arithmetic", "nodeName": "Calculator Tool",
		// A literal the author typed, which must not override the model.
		"parameters": map[string]any{"expression": "1+1"},
	}}}}
	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	agentDefinition, _ := aiRegistry(t).Lookup(nodes.AgentNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"prompt": "what is 17 * 23"}, Definition: agentDefinition,
	}
	if _, err := executor.Execute(context.Background(), ir, input, engine.Request{
		Credentials: bearerResolver(),
		Events: func(event engine.NodeEvent) {
			var decoded ai.Event
			if json.Unmarshal(event.Detail, &decoded) == nil {
				published = append(published, decoded)
			}
		},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, event := range published {
		if event.Kind == ai.EventToolCompleted && event.Tool == "calculator" {
			if string(event.Detail) != `"{\"result\":391}"` {
				t.Fatalf("tool result = %s, want the model's own arithmetic", event.Detail)
			}
			return
		}
	}
	t.Fatalf("no tool result was published: %#v", published)
}

// TestCalculatorToolCompilesWithNoExpression is the half the validator test
// above cannot see: the compiler reads the *declaration* through
// registry.RequiredFor, not the validator, so a tool that declared `expression`
// required and stored nothing was refused with config.required even though
// Validate accepted it. That is exactly the node an import produces — the n8n
// adapter writes toolName and toolDescription and nothing else — so the import
// could not be activated at all.
func TestCalculatorToolCompilesWithNoExpression(t *testing.T) {
	t.Parallel()

	document := aiDocument([]workflow.Node{{
		ID: "tool", Name: "Calculator Tool", Type: nodes.CalculatorToolNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"toolName": "calculator", "toolDescription": "does arithmetic"},
	}}, []workflow.Connection{{
		ID: "c-tool", Kind: workflow.ConnectionTool,
		Source: workflow.Endpoint{NodeID: "tool", Port: "tool"},
		Target: workflow.Endpoint{NodeID: "agent", Port: "tools"},
	}})

	if _, err := workflow.Compile(document, aiRegistry(t)); err != nil {
		t.Fatalf("Compile() error = %v, want an imported calculator tool accepted", err)
	}
}

// TestCalculatorUnderstandsNamedConstants: a model reaches for pi, and
// refusing it burns a call it cannot recover from.
func TestCalculatorUnderstandsNamedConstants(t *testing.T) {
	t.Parallel()

	registry := aiRegistry(t)
	definition, _ := registry.Lookup(nodes.CalculatorNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "calc", Name: "Calculator", Type: nodes.CalculatorNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"expression": "pi * 2"}, Definition: definition,
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	output, err := runExecutor(t, executors, nodes.CalculatorExecutorID, ir,
		workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result, _ := output[0][0].JSON["result"].(float64); result < 6.28 || result > 6.29 {
		t.Fatalf("result = %#v, want pi evaluated", output[0][0].JSON["result"])
	}
}

// TestAgentSendsTheItemsImagesToTheModel is the vision half of
// passthroughBinaryImages: the picture on the input item reaches the model as
// a content part, rather than the agent answering as though the item were
// text-only.
func TestAgentSendsTheItemsImagesToTheModel(t *testing.T) {
	t.Parallel()

	payload := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x01, 0x02}
	store := binaryStore(t)
	stored, err := store.Put("plate.png", "image/png", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	var received map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"742"},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()

	input := agentModelInput(provider.URL)
	input["main"] = []workflow.Item{{JSON: map[string]any{}, Binary: map[string]workflow.BinaryRef{
		"data": {ID: stored.ID, FileName: "plate.png", MediaType: "image/png", Size: int64(len(payload))},
	}}}
	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	definition, _ := aiRegistry(t).Lookup(nodes.AgentNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"prompt": "what number is on the plate"}, Definition: definition,
	}
	output, err := executor.Execute(context.Background(), ir, input,
		engine.Request{Credentials: bearerResolver(), Binaries: store})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	messages, _ := received["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("provider received %#v, want one user turn", received["messages"])
	}
	turn, _ := messages[0].(map[string]any)
	parts, ok := turn["content"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("content = %#v, want the prompt and the image", turn["content"])
	}
	image, _ := parts[1].(map[string]any)
	imageURL, _ := image["image_url"].(map[string]any)
	if url, _ := imageURL["url"].(string); !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("image part = %#v, want the stored picture as a data URI", image)
	}
	// The picture is carried through to the output as well, which is what the
	// flag meant before it also meant vision.
	if len(output[0][0].Binary) != 1 {
		t.Fatalf("output = %#v, want the attachment carried through", output[0][0].Binary)
	}
}

// TestAgentSendsImagesOnlyWhenTheFlagIsOn keeps the opt-out honest.
func TestAgentSendsImagesOnlyWhenTheFlagIsOn(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	stored, err := store.Put("plate.png", "image/png", bytes.NewReader([]byte{0x89, 'P', 'N', 'G'}))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	var received map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()

	input := agentModelInput(provider.URL)
	input["main"] = []workflow.Item{{JSON: map[string]any{}, Binary: map[string]workflow.BinaryRef{
		"data": {ID: stored.ID, FileName: "plate.png", MediaType: "image/png"},
	}}}
	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	definition, _ := aiRegistry(t).Lookup(nodes.AgentNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"prompt": "hi", "passthroughBinaryImages": false}, Definition: definition,
	}
	if _, err := executor.Execute(context.Background(), ir, input,
		engine.Request{Credentials: bearerResolver(), Binaries: store}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	messages, _ := received["messages"].([]any)
	turn, _ := messages[0].(map[string]any)
	if _, isParts := turn["content"].([]any); isParts {
		t.Fatalf("content = %#v, want no image parts when the flag is off", turn["content"])
	}
}

// TestChatModelNodeNeedsNoCredentialForALocalEndpoint: an endpoint that
// authenticates nobody must not force a fake token, and the request must go
// out with no Authorization header.
func TestChatModelNodeNeedsNoCredentialForALocalEndpoint(t *testing.T) {
	t.Parallel()

	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	definition, _ := aiRegistry(t).Lookup(nodes.ChatModelNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "model", Name: "Local Model", Type: nodes.ChatModelNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"model": "llama3", "baseUrl": "http://127.0.0.1:11434/v1"},
		Definition: definition,
	}
	output, err := runExecutor(t, executors, nodes.ChatModelExecutorID, ir, workflow.NodeInput{}, engine.Request{})
	if err != nil {
		t.Fatalf("a model node with no credential was refused: %v", err)
	}
	descriptor, _ := output[0][0].JSON["$ai"].(map[string]any)
	if descriptor["credentialId"] != "" {
		t.Fatalf("descriptor = %#v, want no credential carried", descriptor)
	}

	var authorization string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()

	agentDefinition, _ := aiRegistry(t).Lookup(nodes.AgentNodeType, workflow.V(1))
	agent := workflow.IRNode{
		ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"prompt": "hi"}, Definition: agentDefinition,
	}
	input := workflow.NodeInput{
		"main": {{JSON: map[string]any{}}},
		"model": {{JSON: map[string]any{"$ai": map[string]any{
			"kind": "model", "model": "llama3", "baseUrl": provider.URL, "credentialId": "",
		}}}},
	}
	// A resolver that fails any lookup: the run must not ask for a credential
	// it was never given.
	if _, err := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil).Execute(context.Background(), agent, input,
		engine.Request{Credentials: &stubCredentials{err: errors.New("no credential was configured")}}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if authorization != "" {
		t.Fatalf("Authorization = %q, want no header for an endpoint that needs none", authorization)
	}
}

// TestChatModelNodeCarriesTheTransportOptions: the local-model path had no
// timeout or retry setting at all, so a long answer died at the deployment's
// outbound timeout with nothing to raise.
func TestChatModelNodeCarriesTheTransportOptions(t *testing.T) {
	t.Parallel()

	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	definition, _ := aiRegistry(t).Lookup(nodes.ChatModelNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "model", Name: "Local Model", Type: nodes.ChatModelNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{
			"model": "llama3", "baseUrl": "http://127.0.0.1:11434/v1",
			"options": map[string]any{"timeout": float64(240000), "maxRetries": float64(4)},
		},
		Definition: definition,
	}
	output, err := runExecutor(t, executors, nodes.ChatModelExecutorID, ir, workflow.NodeInput{}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	descriptor, _ := output[0][0].JSON["$ai"].(map[string]any)
	if descriptor["timeout"] != float64(240000) || descriptor["maxRetries"] != float64(4) {
		t.Fatalf("descriptor = %#v, want the transport options carried", descriptor)
	}
}

// TestAnImportedModelsSamplingOptionsReachTheDescriptor is the second defect:
// n8n stores temperature, topP, maxTokens and the penalties in the `options`
// collection, the importer writes them back there and marks them consumed — and
// this node read them from the top level only, so every imported model ran with
// the provider's defaults while the document said otherwise.
func TestAnImportedModelsSamplingOptionsReachTheDescriptor(t *testing.T) {
	t.Parallel()

	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	definition, _ := aiRegistry(t).Lookup(nodes.ChatModelNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "model", Name: "Local Model", Type: nodes.ChatModelNodeType, TypeVersion: workflow.V(1),
		// The shape an import produces: the sampling settings live in the
		// collection and nothing is written to the top level.
		Parameters: map[string]any{
			"model": "llama3", "baseUrl": "http://127.0.0.1:11434/v1",
			"options": map[string]any{
				"temperature": float64(0.2), "topP": float64(0.9), "maxTokens": float64(256),
				"frequencyPenalty": float64(0.5), "presencePenalty": float64(-0.25),
			},
		},
		Definition: definition,
	}
	output, err := runExecutor(t, executors, nodes.ChatModelExecutorID, ir, workflow.NodeInput{}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	descriptor, _ := output[0][0].JSON["$ai"].(map[string]any)
	for key, want := range map[string]float64{
		"temperature": 0.2, "topP": 0.9, "maxTokens": 256,
		"frequencyPenalty": 0.5, "presencePenalty": -0.25,
	} {
		if descriptor[key] != want {
			t.Errorf("descriptor[%q] = %#v, want %v from the options collection", key, descriptor[key], want)
		}
	}
}

// TestAnUnsetRetryOptionKeepsTwoRetries is n8n's default: imported models
// rarely set maxRetries, and absent-means-none failed the run on the first 429.
func TestAnUnsetRetryOptionKeepsTwoRetries(t *testing.T) {
	t.Parallel()

	attempts := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"slow down"}`))
	}))
	defer provider.Close()

	_, err := runAgentNode(t, map[string]any{"prompt": "hi"}, agentModelInput(provider.URL), bearerResolver(), nil)
	if err == nil {
		t.Fatal("Execute() succeeded against a provider that always rate-limits")
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want the default two retries", attempts)
	}
}

// TestStreamedTokensReachTheFeedCoalesced: one event per token filled the
// execution's bounded event history with tokens and pushed the tool and model
// events out of it. The deltas must still arrive complete — just fewer of them.
func TestStreamedTokensReachTheFeedCoalesced(t *testing.T) {
	t.Parallel()

	tokens := make([]string, 0, 20)
	for index := range 20 {
		tokens = append(tokens, fmt.Sprintf("tok%d ", index))
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, token := range tokens {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":`+strconv.Quote(token)+`}}]}`)
		}
		_, _ = w.Write([]byte("data: " + `{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":20,"total_tokens":23}}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer provider.Close()

	input := agentModelInput(provider.URL)
	descriptor := input["model"][0].JSON["$ai"].(map[string]any)
	descriptor["stream"] = true

	var deltas []string
	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	definition, _ := aiRegistry(t).Lookup(nodes.AgentNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"prompt": "hi"}, Definition: definition,
	}
	output, err := executor.Execute(context.Background(), ir, input, engine.Request{
		Credentials: bearerResolver(),
		Events: func(event engine.NodeEvent) {
			if event.Name != string(ai.EventModelDelta) {
				return
			}
			var decoded ai.Event
			if json.Unmarshal(event.Detail, &decoded) == nil {
				deltas = append(deltas, decoded.Delta)
			}
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if assembled := strings.Join(deltas, ""); assembled != strings.Join(tokens, "") {
		t.Fatalf("streamed = %q, want every token delivered", assembled)
	}
	if len(deltas) >= len(tokens) {
		t.Fatalf("published %d delta events for %d tokens, want them coalesced", len(deltas), len(tokens))
	}
	if output[0][0].JSON["output"] != strings.Join(tokens, "") {
		t.Fatalf("output = %#v, want the assembled answer", output[0][0].JSON["output"])
	}
}

// TestChainSendsTheParserSchemaWithThePrompt: the chain used the parser only
// to judge the answer afterwards, so the first reply was free text and the run
// usually failed — especially on a small local model.
func TestChainSendsTheParserSchemaWithThePrompt(t *testing.T) {
	t.Parallel()

	var bodies []map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		decoded := map[string]any{}
		_ = json.Unmarshal(body, &decoded)
		bodies = append(bodies, decoded)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"fullName\":\"Budi\",\"ageYears\":34}"},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()

	schema := `{"type":"object","properties":{"fullName":{"type":"string"},"ageYears":{"type":"integer"}},"required":["fullName","ageYears"]}`
	input := chainModelInput(provider.URL, workflow.Item{JSON: map[string]any{"question": "Budi is 34"}})
	input["outputParser"] = []workflow.Item{parserDescriptor(t, schema)}
	output, err := runChainNode(t, map[string]any{"promptType": "auto", "inputField": "question"},
		input, bearerResolver(), nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	messages := promptContents(t, bodies[0])
	last := messages[len(messages)-1]
	content, _ := last["content"].(string)
	if !strings.Contains(content, "ageYears") || !strings.Contains(content, "JSON Schema") {
		t.Fatalf("prompt = %q, want the schema stated to the model", content)
	}
	// With a parser the answer keeps n8n's `output` key, holding the parsed
	// object rather than the raw text.
	parsed, _ := output[0][0].JSON["output"].(map[string]any)
	if parsed == nil {
		t.Fatalf("output = %#v, want the parsed object", output[0][0].JSON)
	}
}

// parserDescriptor runs the Structured Output Parser node, so a test consumes
// the same descriptor the agent and chain do rather than a hand-built one.
func parserDescriptor(t *testing.T, schema string) workflow.Item {
	t.Helper()
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	definition, _ := aiRegistry(t).Lookup(nodes.OutputParserNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "parser", Name: "Parser", Type: nodes.OutputParserNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"schemaType": "jsonSchema", "jsonSchema": schema}, Definition: definition,
	}
	output, err := runExecutor(t, executors, nodes.OutputParserExecutorID, ir, workflow.NodeInput{}, engine.Request{})
	if err != nil {
		t.Fatalf("parser node error = %v", err)
	}
	return output[0][0]
}

// TestARunEndedByTheExecutionTimeoutDoesNotBlameTheModelCeiling: the run's
// own context carries the execution's deadline — two minutes by default, far
// under the ten-minute model ceiling. When that deadline is what ended the
// agent, naming the ceiling sent the user to a bound nothing had reached.
func TestARunEndedByTheExecutionTimeoutDoesNotBlameTheModelCeiling(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	defer provider.Close()
	defer close(release)

	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	definition, _ := aiRegistry(t).Lookup(nodes.AgentNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"prompt": "Extract the total."}, Definition: definition,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := executor.Execute(ctx, ir, workflow.NodeInput{
		"main":  {{JSON: map[string]any{}}},
		"model": {agentModelInput(provider.URL)["model"][0]},
	}, engine.Request{Credentials: bearerResolver()})
	if err == nil {
		t.Fatal("Execute() succeeded against a provider that never answers")
	}
	if strings.Contains(err.Error(), "model timeout ceiling") {
		t.Errorf("error = %v, want no mention of the model ceiling: the execution's own deadline ended the run", err)
	}
	if !strings.Contains(err.Error(), "executionTimeout") {
		t.Errorf("error = %v, want it to name the workflow setting that raises the execution's time limit", err)
	}
}

// TestAChatModelStreamsByDefaultAsItsDefinitionSays: the node declares "Stream
// output" on by default, and the editor shows it on, but an imported or
// untouched node carries no `stream` key at all — and reading that as false
// made every such node non-streaming. The run must follow the definition.
func TestAChatModelStreamsByDefaultAsItsDefinitionSays(t *testing.T) {
	t.Parallel()

	item := runProviderModel(t, nodes.ChatModelNodeType, nodes.ChatModelExecutorID, nodes.BearerCredentialType,
		map[string]any{"model": "m", "baseUrl": "http://127.0.0.1:1/v1"}, bearerResolver())
	descriptor, _ := item.JSON["$ai"].(map[string]any)
	if descriptor["stream"] != true {
		t.Fatalf("descriptor stream = %#v, want true when the node never set it", descriptor["stream"])
	}

	item = runProviderModel(t, nodes.ChatModelNodeType, nodes.ChatModelExecutorID, nodes.BearerCredentialType,
		map[string]any{"model": "m", "baseUrl": "http://127.0.0.1:1/v1", "stream": false}, bearerResolver())
	descriptor, _ = item.JSON["$ai"].(map[string]any)
	if descriptor["stream"] != false {
		t.Fatalf("descriptor stream = %#v, want false when the node turned it off", descriptor["stream"])
	}
}
