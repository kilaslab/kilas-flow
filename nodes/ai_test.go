package nodes_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/ai"
	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
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

func TestChatModelRequiresACredentialRatherThanAnAmbientKey(t *testing.T) {
	t.Parallel()

	definition, _ := aiRegistry(t).Lookup(nodes.ChatModelNodeType, workflow.V(1))
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
