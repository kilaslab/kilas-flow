package nodes_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func TestVectorStoreToolDefinitionDeclaresItsSlots(t *testing.T) {
	t.Parallel()

	definition, found := aiRegistry(t).Get(nodes.VectorStoreToolNodeType, workflow.V(1))
	if !found {
		t.Fatal("the Vector Store Tool is not registered")
	}
	wantInputs := map[string]workflow.ConnectionKind{
		"vectorStore": workflow.ConnectionVectorStore,
		"model":       workflow.ConnectionLanguageModel,
	}
	if len(definition.Inputs) != len(wantInputs) {
		t.Fatalf("inputs = %+v, want exactly the vectorStore and model slots", definition.Inputs)
	}
	for _, port := range definition.Inputs {
		if wantInputs[port.Name] != port.Kind {
			t.Errorf("input %q speaks %q, want %q", port.Name, port.Kind, wantInputs[port.Name])
		}
		if !port.Required || port.MaxConnections != 1 {
			t.Errorf("input %q: required=%v max=%d, want one required connection", port.Name, port.Required, port.MaxConnections)
		}
	}
	if len(definition.Outputs) != 1 || definition.Outputs[0].Kind != workflow.ConnectionTool {
		t.Errorf("outputs = %+v, want one ai_tool port", definition.Outputs)
	}
	if definition.Codex == nil || len(definition.Codex.Subcategories["AI"]) == 0 || definition.Codex.Subcategories["AI"][0] != "Tools" {
		t.Errorf("codex = %+v, want the picker's AI tools", definition.Codex)
	}
	if !answerToolParameterRequired(definition, "toolDescription") {
		t.Error("toolDescription is not declared required; the compiler reads the declaration, not the validator")
	}

	valid := map[string]any{"toolDescription": "HR and employee policies"}
	if err := definition.Validate(workflow.Node{Parameters: valid}); err != nil {
		t.Errorf("Validate(description only) = %v, want nil: topK defaults", err)
	}
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{}}); err == nil || !strings.Contains(err.Error(), "toolDescription is required") {
		t.Errorf("Validate(no description) = %v, want toolDescription named", err)
	}
	for _, topK := range []any{float64(0), float64(-1), 2.5, float64(nodes.MaxVectorTopK + 1), "many"} {
		parameters := map[string]any{"toolDescription": "HR", "topK": topK}
		if err := definition.Validate(workflow.Node{Parameters: parameters}); err == nil || !strings.Contains(err.Error(), "topK") {
			t.Errorf("Validate(topK %v) = %v, want a refusal naming topK rather than a silent clamp", topK, err)
		}
	}
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"toolDescription": "HR", "topK": float64(nodes.MaxVectorTopK)}}); err != nil {
		t.Errorf("Validate(topK at the ceiling) = %v, want nil", err)
	}
}

func answerToolParameterRequired(definition node.Definition, key string) bool {
	for _, parameter := range definition.Parameters {
		if parameter.Key == key {
			return parameter.Required
		}
	}
	return false
}

// answerToolStoreItem is what a store in retrieve mode puts on its
// vectorStore port.
func answerToolStoreItem(embeddingsURL string) workflow.Item {
	return workflow.Item{JSON: map[string]any{"$ai": map[string]any{
		"kind": "vectorStoreSource", "nodeName": "Policies Store", "backend": "internal",
		"collection": "hr", "distance": "cosine",
		"embeddings": map[string]any{
			"kind": "embeddings", "model": "text-embedding-3-small",
			"baseUrl": embeddingsURL, "credentialId": "cred-key",
		},
	}}}
}

func TestVectorStoreToolEmitsAnAnswerToolDescriptor(t *testing.T) {
	t.Parallel()

	store := answerToolStoreItem("https://api.openai.com/v1")
	model := modelItem("https://api.openai.com/v1")
	output, err := nodes.ExecuteVectorStoreToolForTest(context.Background(), workflow.IRNode{
		ID: "vt", Name: "Answer questions with a vector store", Type: nodes.VectorStoreToolNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"toolDescription": "HR and employee policies", "topK": float64(6)},
	}, workflow.NodeInput{"vectorStore": {store}, "model": {model}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute = %v", err)
	}
	if len(output) != 1 || len(output[0]) != 1 {
		t.Fatalf("output = %#v, want one descriptor on the tool port", output)
	}
	descriptor, _ := output[0][0].JSON["$ai"].(map[string]any)
	if descriptor["kind"] != "vectorStoreAnswer" {
		t.Errorf("kind = %v", descriptor["kind"])
	}
	if descriptor["name"] != "Answer_questions_with_a_vector_store" {
		t.Errorf("name = %v, want the normalised node name when no toolName is set", descriptor["name"])
	}
	if descriptor["description"] != "HR and employee policies" || descriptor["topK"] != 6 {
		t.Errorf("description/topK = %v / %v", descriptor["description"], descriptor["topK"])
	}
	if got, _ := descriptor["store"].(map[string]any); got["collection"] != "hr" || got["kind"] != "vectorStoreSource" {
		t.Errorf("store = %#v, want the retrieve descriptor carried whole", got)
	}
	if got, _ := descriptor["model"].(map[string]any); got["credentialId"] != "cred-key" {
		t.Errorf("model = %#v, want the chat model descriptor carried whole", got)
	}
	encoded, _ := json.Marshal(descriptor)
	if strings.Contains(string(encoded), "sk-live-secret") {
		t.Fatalf("descriptor carries a secret: %s", encoded)
	}

	named, err := nodes.ExecuteVectorStoreToolForTest(context.Background(), workflow.IRNode{
		ID: "vt", Name: "Policies", Type: nodes.VectorStoreToolNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"toolName": "hr_policies", "toolDescription": "HR"},
	}, workflow.NodeInput{"vectorStore": {store}, "model": {model}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute(named) = %v", err)
	}
	descriptor, _ = named[0][0].JSON["$ai"].(map[string]any)
	if descriptor["name"] != "hr_policies" || descriptor["topK"] != nodes.DefaultVectorTopK {
		t.Errorf("named descriptor = %#v, want toolName and the default topK", descriptor)
	}
}

func TestVectorStoreToolRefusesWhatIsNotARetrieveStoreAndAModel(t *testing.T) {
	t.Parallel()

	store := answerToolStoreItem("https://api.openai.com/v1")
	model := modelItem("https://api.openai.com/v1")
	retrieveAsTool := toolItem(map[string]any{"kind": "vectorStore", "name": "search", "nodeName": "Store as tool"})
	cases := map[string]struct {
		input workflow.NodeInput
		want  string
	}{
		"no store":             {workflow.NodeInput{"model": {model}}, "vectorStore"},
		"two stores":           {workflow.NodeInput{"vectorStore": {store, store}, "model": {model}}, "accepts one"},
		"a tool, not a store":  {workflow.NodeInput{"vectorStore": {retrieveAsTool}, "model": {model}}, "Retrieve"},
		"no model":             {workflow.NodeInput{"vectorStore": {store}}, "chat model"},
		"a store as the model": {workflow.NodeInput{"vectorStore": {store}, "model": {store}}, "chat model"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := nodes.ExecuteVectorStoreToolForTest(context.Background(), workflow.IRNode{
				ID: "vt", Name: "Vector Store Tool", Type: nodes.VectorStoreToolNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"toolDescription": "HR"},
			}, tc.input, engine.Request{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Execute = %v, want a refusal mentioning %q", err, tc.want)
			}
		})
	}
}

// answerToolSearchStore records what the answer tool searched for.
type answerToolSearchStore struct {
	mu                 sync.Mutex
	tenant, collection string
	topK               int
	filter             map[string]any
	matches            []nodes.VectorMatch
}

func (store *answerToolSearchStore) CheckAvailable(context.Context) error { return nil }
func (store *answerToolSearchStore) EnsureCollection(context.Context, string, string, int, string, string) (nodes.VectorCollection, error) {
	return nodes.VectorCollection{}, nil
}
func (store *answerToolSearchStore) Insert(context.Context, string, string, []nodes.VectorDocument) error {
	return nil
}
func (store *answerToolSearchStore) Search(_ context.Context, tenant, collection string, _ []float64, topK int, filter map[string]any) ([]nodes.VectorMatch, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.tenant, store.collection, store.topK, store.filter = tenant, collection, topK, filter
	return store.matches, nil
}
func (store *answerToolSearchStore) Delete(context.Context, string, string, []string) (int64, error) {
	return 0, nil
}

// answerToolProvider is one OpenAI-compatible endpoint serving both
// embeddings and scripted chat turns, recording every request body.
type answerToolProvider struct {
	server     *httptest.Server
	mu         sync.Mutex
	chats      []string
	embeddings []string
}

func newAnswerToolProvider(t *testing.T, turns []string) *answerToolProvider {
	t.Helper()
	provider := &answerToolProvider{}
	turn := 0
	provider.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, _ := io.ReadAll(r.Body)
		provider.mu.Lock()
		defer provider.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/embeddings") {
			provider.embeddings = append(provider.embeddings, string(body))
			fmt.Fprint(w, `{"data":[{"embedding":[1,0,0,0],"index":0}]}`)
			return
		}
		provider.chats = append(provider.chats, string(body))
		if turn >= len(turns) {
			fmt.Fprint(w, assistantAnswer("out of script"))
			return
		}
		fmt.Fprint(w, turns[turn])
		turn++
	}))
	t.Cleanup(provider.server.Close)
	return provider
}

func (provider *answerToolProvider) recordedChats() []string {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return append([]string(nil), provider.chats...)
}

func (provider *answerToolProvider) recordedEmbeddings() []string {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return append([]string(nil), provider.embeddings...)
}

// answerToolOverProvider builds the answering tool against the provider for
// both retrieval and the tool's model, over the given store.
func answerToolOverProvider(t *testing.T, provider *answerToolProvider, store nodes.VectorStore) ai.Tool {
	t.Helper()
	policy := answerToolPolicy(t, provider.server.URL)
	agent := nodes.NewAgentExecutor(nil, policy, nil, nodes.WithAgentVectorStore(store, nodes.NewEmbeddingsExecutor(policy, availableVectorStub{})))
	storeDescriptor, _ := answerToolStoreItem(provider.server.URL).JSON["$ai"].(map[string]any)
	modelDescriptor, _ := modelItem(provider.server.URL).JSON["$ai"].(map[string]any)
	tool, err := nodes.VectorStoreAnswerToolFromForTest(context.Background(), agent, workflow.IRNode{Name: "AI Agent"}, map[string]any{
		"kind": "vectorStoreAnswer", "name": "hr_policies", "description": "HR and employee policies",
		"store": storeDescriptor, "model": modelDescriptor,
	}, engine.Request{Credentials: answerToolCredentials(), Execution: agentExecution()})
	if err != nil {
		t.Fatalf("toolFrom = %v", err)
	}
	return tool
}

// answerToolChatMessages decodes one recorded chat request's messages.
func answerToolChatMessages(t *testing.T, body string) (messages []map[string]any, payload map[string]any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("chat body is not JSON: %v", err)
	}
	raw, _ := payload["messages"].([]any)
	for _, entry := range raw {
		message, _ := entry.(map[string]any)
		messages = append(messages, message)
	}
	return messages, payload
}

// answerToolPolicy is the default outbound policy with one exact loopback
// endpoint let through, never the private-network switch.
func answerToolPolicy(t *testing.T, serverURL string) safehttp.Policy {
	t.Helper()
	target, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse %q: %v", serverURL, err)
	}
	policy := safehttp.DefaultPolicy()
	policy.AllowedPrivateEndpoints = []string{target.Host}
	return policy
}

func answerToolCredentials() *stubCredentials {
	return &stubCredentials{credential: engine.Credential{
		ID: "cred-key", Name: "Provider", Type: "httpBearerAuth",
		Fields: map[string]string{"token": "sk-live-secret"}, AllowedDomains: []string{"127.0.0.1"},
	}}
}

func TestAgentVectorStoreAnswerToolRetrievesThenAnswers(t *testing.T) {
	t.Parallel()

	provider := newAnswerToolProvider(t, []string{assistantAnswer("Employees get 20 days of paid leave.")})
	store := &answerToolSearchStore{matches: []nodes.VectorMatch{
		{ID: "doc-1", Content: "Paid leave is 20 days per calendar year.", Distance: 0.1},
		{ID: "doc-2", Content: "Leave requests go to your manager.", Distance: 0.2},
	}}
	policy := answerToolPolicy(t, provider.server.URL)
	agent := nodes.NewAgentExecutor(nil, policy, nil, nodes.WithAgentVectorStore(store, nodes.NewEmbeddingsExecutor(policy, availableVectorStub{})))
	request := engine.Request{Credentials: answerToolCredentials(), Execution: agentExecution()}

	storeDescriptor, _ := answerToolStoreItem(provider.server.URL).JSON["$ai"].(map[string]any)
	modelDescriptor, _ := modelItem(provider.server.URL).JSON["$ai"].(map[string]any)
	tool, err := nodes.VectorStoreAnswerToolFromForTest(context.Background(), agent, workflow.IRNode{Name: "AI Agent"}, map[string]any{
		"kind": "vectorStoreAnswer", "name": "hr_policies", "description": "HR and employee policies",
		"nodeName": "Answer questions", "topK": 3, "store": storeDescriptor, "model": modelDescriptor,
	}, request)
	if err != nil {
		t.Fatalf("toolFrom = %v", err)
	}
	definition := tool.Definition()
	if definition.Name != "hr_policies" || !strings.Contains(definition.Description, "HR and employee policies") {
		t.Errorf("definition = %+v, want the configured name and the description the model reads", definition)
	}
	if properties, _ := definition.Parameters["properties"].(map[string]any); properties["query"] == nil {
		t.Errorf("parameters = %#v, want one query argument", definition.Parameters)
	}

	answer, err := tool.Invoke(context.Background(), json.RawMessage(`{"query":"How many days of leave do I get?"}`))
	if err != nil {
		t.Fatalf("Invoke = %v", err)
	}
	if answer != "Employees get 20 days of paid leave." {
		t.Errorf("answer = %q, want the tool model's answer, not the raw matches", answer)
	}
	if store.tenant != "tenant-1" || store.collection != "hr" || store.topK != 3 {
		t.Errorf("search = tenant %q collection %q topK %d, want the execution's tenant, the store's collection and the tool's topK",
			store.tenant, store.collection, store.topK)
	}
	chats := provider.recordedChats()
	if len(chats) != 1 {
		t.Fatalf("chat calls = %d, want exactly one: the tool's own model", len(chats))
	}
	for _, want := range []string{"Paid leave is 20 days per calendar year.", "Leave requests go to your manager.", "How many days of leave do I get?", `"model":"gpt-test"`} {
		if !strings.Contains(chats[0], want) {
			t.Errorf("tool model request lacks %q: %s", want, chats[0])
		}
	}
}

func TestAgentVectorStoreAnswerToolSaysSoWhenNothingMatches(t *testing.T) {
	t.Parallel()

	provider := newAnswerToolProvider(t, nil)
	policy := answerToolPolicy(t, provider.server.URL)
	agent := nodes.NewAgentExecutor(nil, policy, nil, nodes.WithAgentVectorStore(&answerToolSearchStore{}, nodes.NewEmbeddingsExecutor(policy, availableVectorStub{})))
	storeDescriptor, _ := answerToolStoreItem(provider.server.URL).JSON["$ai"].(map[string]any)
	modelDescriptor, _ := modelItem(provider.server.URL).JSON["$ai"].(map[string]any)
	tool, err := nodes.VectorStoreAnswerToolFromForTest(context.Background(), agent, workflow.IRNode{Name: "AI Agent"}, map[string]any{
		"kind": "vectorStoreAnswer", "name": "hr_policies", "description": "HR",
		"store": storeDescriptor, "model": modelDescriptor,
	}, engine.Request{Credentials: answerToolCredentials(), Execution: agentExecution()})
	if err != nil {
		t.Fatalf("toolFrom = %v", err)
	}
	answer, err := tool.Invoke(context.Background(), json.RawMessage(`{"query":"Anything?"}`))
	if err != nil {
		t.Fatalf("Invoke = %v", err)
	}
	if !strings.Contains(strings.ToLower(answer), "no stored document") {
		t.Errorf("answer = %q, want a plain statement that nothing matched", answer)
	}
	if chats := provider.recordedChats(); len(chats) != 0 {
		t.Errorf("chat calls = %d, want none: there is no context to answer from", len(chats))
	}
}

func TestAgentVectorStoreAnswerToolKeepsTheOutboundGuardOnItsModel(t *testing.T) {
	t.Parallel()

	provider := newAnswerToolProvider(t, []string{assistantAnswer("should never be read")})
	reached := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, assistantAnswer("leaked"))
	}))
	t.Cleanup(other.Close)

	// Only the provider's exact host:port is let through; the tool's model
	// names another loopback port and must be refused at the dialer.
	policy := answerToolPolicy(t, provider.server.URL)
	store := &answerToolSearchStore{matches: []nodes.VectorMatch{{ID: "doc-1", Content: "context"}}}
	agent := nodes.NewAgentExecutor(nil, policy, nil, nodes.WithAgentVectorStore(store, nodes.NewEmbeddingsExecutor(policy, availableVectorStub{})))
	storeDescriptor, _ := answerToolStoreItem(provider.server.URL).JSON["$ai"].(map[string]any)
	modelDescriptor, _ := modelItem(other.URL).JSON["$ai"].(map[string]any)
	modelDescriptor["maxRetries"] = 0
	tool, err := nodes.VectorStoreAnswerToolFromForTest(context.Background(), agent, workflow.IRNode{Name: "AI Agent"}, map[string]any{
		"kind": "vectorStoreAnswer", "name": "hr_policies", "description": "HR",
		"store": storeDescriptor, "model": modelDescriptor,
	}, engine.Request{Credentials: answerToolCredentials(), Execution: agentExecution()})
	if err == nil {
		_, err = tool.Invoke(context.Background(), json.RawMessage(`{"query":"Anything?"}`))
	}
	if err == nil || !strings.Contains(err.Error(), safehttp.ErrBlocked.Error()) {
		t.Fatalf("err = %v, want the outbound policy refusal", err)
	}
	if reached {
		t.Fatal("the tool's model reached a loopback port the policy does not list")
	}
}

// answerToolDocument is Manual → AI Agent, with the agent's chat model and a
// Vector Store Tool whose own chat model and retrieve-mode store (with its
// embeddings) hang off it: the retrieval half of n8n template 16706.
func answerToolDocument(providerURL string) workflow.Document {
	chatModel := func(id, name string) workflow.Node {
		return workflow.Node{
			ID: id, Name: name, Type: nodes.ChatModelNodeType, TypeVersion: workflow.V(1),
			Parameters:  map[string]any{"model": "gpt-test", "baseUrl": providerURL, "stream": false},
			Credentials: map[string]string{"httpBearerAuth": "cred-key"},
		}
	}
	edge := func(id string, kind workflow.ConnectionKind, source, sourcePort, target, targetPort string) workflow.Connection {
		return workflow.Connection{
			ID: id, Kind: kind,
			Source: workflow.Endpoint{NodeID: source, Port: sourcePort},
			Target: workflow.Endpoint{NodeID: target, Port: targetPort},
		}
	}
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_answer_tool",
		Name:          "Answer tool",
		Nodes: []workflow.Node{
			{ID: "trigger", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: workflow.V(1), Parameters: map[string]any{"prompt": "How many days of leave do I get?"}},
			chatModel("agent-model", "Agent Model"),
			{
				ID: "tool", Name: "Answer questions", Type: nodes.VectorStoreToolNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"toolName": "hr_policies", "toolDescription": "HR and employee policies", "topK": float64(2)},
			},
			chatModel("tool-model", "Tool Model"),
			{
				ID: "store", Name: "Policies Store", Type: nodes.VectorStoreNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"mode": "retrieve", "collection": "hr"},
			},
			{
				ID: "embeddings", Name: "Embeddings", Type: nodes.EmbeddingsNodeType, TypeVersion: workflow.V(1),
				Parameters:  map[string]any{"mode": "cluster", "model": "text-embedding-3-small", "baseUrl": providerURL},
				Credentials: map[string]string{"httpBearerAuth": "cred-key"},
			},
		},
		Connections: []workflow.Connection{
			edge("c-main", workflow.ConnectionMain, "trigger", "main", "agent", "main"),
			edge("c-agent-model", workflow.ConnectionLanguageModel, "agent-model", "model", "agent", "model"),
			edge("c-tool", workflow.ConnectionTool, "tool", "tool", "agent", "tools"),
			edge("c-tool-model", workflow.ConnectionLanguageModel, "tool-model", "model", "tool", "model"),
			edge("c-store", workflow.ConnectionVectorStore, "store", "vectorStore", "tool", "vectorStore"),
			edge("c-embeddings", workflow.ConnectionEmbedding, "embeddings", "embedding", "store", "embedding"),
		},
		Settings: map[string]any{},
	}
}

func answerToolRegistry(t *testing.T) *node.Registry {
	t.Helper()
	registry := aiRegistry(t)
	if err := nodes.RegisterVectorNodes(registry, ""); err != nil {
		t.Fatalf("RegisterVectorNodes() error = %v", err)
	}
	return registry
}

func TestAgentAnswersThroughAVectorStoreToolOverARetrieveStore(t *testing.T) {
	t.Parallel()

	provider := newAnswerToolProvider(t, []string{
		assistantToolCalls(`{"name":"hr_policies","arguments":"{\"query\":\"How many days of leave?\"}"}`),
		assistantAnswer("Twenty days, per the leave policy."),
		assistantAnswer("You get 20 days of paid leave."),
	})
	ir, err := workflow.Compile(answerToolDocument(provider.server.URL), answerToolRegistry(t))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	store := &answerToolSearchStore{matches: []nodes.VectorMatch{{ID: "doc-1", Content: "Paid leave is 20 days per calendar year."}}}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, answerToolPolicy(t, provider.server.URL), sqlGuard(), ai.NewLoopRuntime(), nil, nil, nodes.WithVectorStore(store)); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{
		Input:       workflow.Item{JSON: map[string]any{}},
		Credentials: answerToolCredentials(),
		Execution:   engine.ExecutionContext{ID: "exec-answer", TenantID: "tenant-answer", WorkflowID: "wf_answer_tool"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	agentOutput := result.Output["agent"]
	if len(agentOutput) == 0 || len(agentOutput[0]) == 0 || agentOutput[0][0].JSON["output"] != "You get 20 days of paid leave." {
		t.Fatalf("agent output = %#v, want the final answer", agentOutput)
	}
	if store.tenant != "tenant-answer" || store.collection != "hr" || store.topK != 2 {
		t.Errorf("search = tenant %q collection %q topK %d", store.tenant, store.collection, store.topK)
	}
	chats := provider.recordedChats()
	if len(chats) != 3 {
		t.Fatalf("chat calls = %d, want agent, tool model, agent", len(chats))
	}
	if !strings.Contains(chats[1], "Paid leave is 20 days per calendar year.") || !strings.Contains(chats[1], "How many days of leave?") {
		t.Errorf("tool model request = %s, want the retrieved text and the agent's question", chats[1])
	}
	if !strings.Contains(chats[2], "Twenty days, per the leave policy.") {
		t.Errorf("agent's second turn = %s, want the tool's answer fed back", chats[2])
	}
}

func TestVectorStoreToolCompileRefusesAMisfitStoreAndAMissingModel(t *testing.T) {
	t.Parallel()

	registry := answerToolRegistry(t)

	asTool := answerToolDocument("https://api.openai.com/v1")
	for index := range asTool.Nodes {
		if asTool.Nodes[index].ID == "store" {
			asTool.Nodes[index].Parameters["mode"] = "retrieve-as-tool"
		}
	}
	_, err := workflow.Compile(asTool, registry)
	if !answerToolCompileCode(err, workflow.ErrorIncompatiblePort) && !answerToolCompileCode(err, workflow.ErrorUnknownPort) {
		t.Errorf("Compile(retrieve-as-tool store on the vectorStore port) = %v, want a port refusal", err)
	}

	noModel := answerToolDocument("https://api.openai.com/v1")
	kept := noModel.Connections[:0]
	for _, connection := range noModel.Connections {
		if connection.ID != "c-tool-model" {
			kept = append(kept, connection)
		}
	}
	noModel.Connections = kept
	_, err = workflow.Compile(noModel, registry)
	if !answerToolCompileCode(err, workflow.ErrorPortRequired) {
		t.Errorf("Compile(no tool model) = %v, want %s", err, workflow.ErrorPortRequired)
	}
}

func answerToolCompileCode(err error, code workflow.ErrorCode) bool {
	var issues *workflow.ValidationErrors
	if !errors.As(err, &issues) {
		return false
	}
	for _, issue := range issues.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
