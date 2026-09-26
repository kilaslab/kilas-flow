package nodes_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// stubWorkflows stands in for the runtime's sub-workflow invoker.
type stubWorkflows struct {
	mu    sync.Mutex
	calls []engine.WorkflowCall
	items []workflow.Item
	err   error
}

func (stub *stubWorkflows) InvokeWorkflow(_ context.Context, _ engine.ExecutionContext, call engine.WorkflowCall) (engine.WorkflowCallResult, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.calls = append(stub.calls, call)
	if stub.err != nil {
		return engine.WorkflowCallResult{}, stub.err
	}
	return engine.WorkflowCallResult{ExecutionID: "exec-child", Items: stub.items}, nil
}

// scriptProvider serves one chat-completion body per model turn.
func scriptProvider(t *testing.T, bodies []string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	turn := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if turn >= len(bodies) {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"out of script"},"finish_reason":"stop"}]}`))
			return
		}
		_, _ = w.Write([]byte(bodies[turn]))
		turn++
	}))
	t.Cleanup(server.Close)
	return server
}

func assistantToolCalls(calls ...string) string {
	var rendered []string
	for index, call := range calls {
		rendered = append(rendered, fmt.Sprintf(`{"id":"c%d","type":"function","function":%s}`, index+1, call))
	}
	return `{"choices":[{"message":{"role":"assistant","tool_calls":[` + strings.Join(rendered, ",") + `]},"finish_reason":"tool_calls"}],"usage":{"total_tokens":10}}`
}

func assistantAnswer(content string) string {
	encoded, _ := json.Marshal(content)
	return `{"choices":[{"message":{"role":"assistant","content":` + string(encoded) + `},"finish_reason":"stop"}],"usage":{"total_tokens":5}}`
}

func agentIR(t *testing.T, parameters map[string]any) workflow.IRNode {
	t.Helper()
	definition, _ := aiRegistry(t).Lookup(nodes.AgentNodeType, workflow.V(1))
	return workflow.IRNode{
		ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: workflow.V(1),
		Parameters: parameters, Definition: definition,
	}
}

func modelItem(providerURL string) workflow.Item {
	return workflow.Item{JSON: map[string]any{"$ai": map[string]any{
		"kind": "model", "model": "gpt-test", "baseUrl": providerURL, "credentialId": "cred-key",
	}}}
}

func toolItem(descriptor map[string]any) workflow.Item {
	return workflow.Item{JSON: map[string]any{"$ai": descriptor}}
}

func agentCredentials() *stubCredentials {
	return &stubCredentials{credential: engine.Credential{
		ID: "cred-key", Name: "Provider", Type: "httpBearerAuth",
		Fields: map[string]string{"token": "sk-live-secret"},
	}}
}

func agentExecution() engine.ExecutionContext {
	return engine.ExecutionContext{ID: "exec-1", TenantID: "tenant-1", WorkflowID: "wf_agent"}
}

func TestAgentRunsHTTPWorkflowAndCalculatorToolsInOneRun(t *testing.T) {
	t.Parallel()

	var toolPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		toolPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rate":0.9}`))
	}))
	defer upstream.Close()

	workflows := &stubWorkflows{items: []workflow.Item{{JSON: map[string]any{"total": 3}}}}
	provider := scriptProvider(t, []string{
		assistantToolCalls(
			`{"name":"calc","arguments":"{\"expression\":\"7 * 6\"}"}`,
			`{"name":"get_rate","arguments":"{\"input\":{}}"}`,
			`{"name":"rates_tool","arguments":"{\"input\":{\"currency\":\"CHF\"}}"}`,
		),
		assistantAnswer("42 at 0.9 totals 3"),
	})

	var published []engine.NodeEvent
	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	output, err := executor.Execute(context.Background(), agentIR(t, map[string]any{"prompt": "Do it all."}), workflow.NodeInput{
		"main":  {{JSON: map[string]any{}}},
		"model": {modelItem(provider.URL)},
		"tools": {
			toolItem(map[string]any{
				"kind": "calculator", "name": "calc", "description": "Arithmetic.", "nodeName": "Calc",
				"parameters": map[string]any{"expression": ""},
			}),
			toolItem(map[string]any{
				"kind": "tool", "name": "get_rate", "description": "Rate.", "nodeName": "Rate",
				"parameters":  map[string]any{"method": "GET", "url": upstream.URL + "/rate/CHF"},
				"credentials": map[string]any{},
			}),
			toolItem(map[string]any{
				"kind": "workflow", "name": "rates_tool", "description": "Totals.", "nodeName": "Rates",
				"workflowId": "wf_rates", "parameters": map[string]any{},
			}),
		},
	}, engine.Request{
		Credentials: agentCredentials(), Workflows: workflows,
		Execution: agentExecution(),
		Events:    func(event engine.NodeEvent) { published = append(published, event) },
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output[0]) != 1 || output[0][0].JSON["output"] != "42 at 0.9 totals 3" {
		t.Fatalf("agent output = %#v, want the final answer", output[0])
	}
	if output[0][0].JSON["toolCalls"] != float64(3) {
		t.Errorf("toolCalls = %#v, want 3", output[0][0].JSON["toolCalls"])
	}
	if toolPath != "/rate/CHF" {
		t.Errorf("HTTP tool called %q, want /rate/CHF", toolPath)
	}
	if len(workflows.calls) != 1 || workflows.calls[0].WorkflowID != "wf_rates" {
		t.Fatalf("sub-workflow calls = %#v, want one call to wf_rates", workflows.calls)
	}
	if workflows.calls[0].Items[0].JSON["currency"] != "CHF" {
		t.Errorf("sub-workflow input = %#v, want the model's argument", workflows.calls[0].Items[0].JSON)
	}
	completed := 0
	for _, event := range published {
		if event.Name == string(ai.EventToolCompleted) {
			completed++
		}
	}
	if completed != 3 {
		t.Errorf("tool completions = %d, want 3", completed)
	}
}

func TestAgentRunsOneToolCallPerItem(t *testing.T) {
	t.Parallel()

	provider := scriptProvider(t, []string{
		assistantToolCalls(`{"name":"calc","arguments":"{\"expression\":\"2 + 3\"}"}`),
		assistantAnswer("five"),
		assistantToolCalls(`{"name":"calc","arguments":"{\"expression\":\"4 * 5\"}"}`),
		assistantAnswer("twenty"),
	})

	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	ir := agentIR(t, map[string]any{"prompt": "Add."})
	input := func() workflow.NodeInput {
		return workflow.NodeInput{
			"main":  {{JSON: map[string]any{}}},
			"model": {modelItem(provider.URL)},
			"tools": {toolItem(map[string]any{
				"kind": "calculator", "name": "calc", "description": "Arithmetic.", "nodeName": "Calc",
				"parameters": map[string]any{"expression": ""},
			})},
		}
	}
	request := func() engine.Request {
		return engine.Request{
			Credentials: agentCredentials(), Execution: agentExecution(),
			Events: func(event engine.NodeEvent) {},
		}
	}
	first, err := executor.Execute(context.Background(), ir, workflow.NodeInput{
		"main":  {{JSON: map[string]any{"n": 1}}, {JSON: map[string]any{"n": 2}}},
		"model": input()["model"], "tools": input()["tools"],
	}, request())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(first[0]) != 2 {
		t.Fatalf("outputs = %d items, want one per incoming item", len(first[0]))
	}
	if first[0][0].JSON["output"] != "five" || first[0][1].JSON["output"] != "twenty" {
		t.Errorf("outputs = %#v, want per-item answers", first[0])
	}
}

func TestWorkflowToolSurfacesTheEnginesRecursionRefusal(t *testing.T) {
	t.Parallel()

	workflows := &stubWorkflows{err: fmt.Errorf("sub-workflow call would repeat workflow %q: wf_child → wf_child", "wf_child")}
	provider := scriptProvider(t, []string{
		assistantToolCalls(`{"name":"child","arguments":"{}"}`),
		assistantAnswer("giving up"),
	})

	var published []engine.NodeEvent
	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	_, err := executor.Execute(context.Background(), agentIR(t, map[string]any{"prompt": "Recurse."}), workflow.NodeInput{
		"main":  {{JSON: map[string]any{}}},
		"model": {modelItem(provider.URL)},
		"tools": {toolItem(map[string]any{
			"kind": "workflow", "name": "child", "description": "Child.", "nodeName": "Child",
			"workflowId": "wf_child", "parameters": map[string]any{},
		})},
	}, engine.Request{
		Credentials: agentCredentials(), Workflows: workflows, Execution: agentExecution(),
		Events: func(event engine.NodeEvent) { published = append(published, event) },
	})
	if err != nil {
		t.Fatalf("Execute() error = %v, want the agent to recover after the refused call", err)
	}
	named := false
	for _, event := range published {
		if event.Name == string(ai.EventToolFailed) && strings.Contains(string(event.Detail), "repeat workflow") {
			named = true
		}
	}
	if !named {
		t.Errorf("no tool failure named the cycle; events = %v", published)
	}
}

func TestHTTPToolDerivesSchemaFromFromAIAndSubstitutes(t *testing.T) {
	t.Parallel()

	var toolPath string
	var sawSchema map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		toolPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tempC":19}`))
	}))
	defer upstream.Close()

	var mu sync.Mutex
	turn := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var request struct {
			Tools []struct {
				Function struct {
					Name       string         `json:"name"`
					Parameters map[string]any `json:"parameters"`
				} `json:"function"`
			} `json:"tools"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if len(request.Tools) == 1 {
			sawSchema = request.Tools[0].Function.Parameters
		}
		w.Header().Set("Content-Type", "application/json")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(assistantToolCalls(`{"name":"get_weather","arguments":"{\"city\":\"Utrecht\"}"}`)))
			return
		}
		_, _ = w.Write([]byte(assistantAnswer("19C")))
	}))
	defer provider.Close()

	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	output, err := executor.Execute(context.Background(), agentIR(t, map[string]any{"prompt": "Weather?"}), workflow.NodeInput{
		"main":  {{JSON: map[string]any{}}},
		"model": {modelItem(provider.URL)},
		"tools": {toolItem(map[string]any{
			"kind": "tool", "name": "get_weather", "description": "Weather by city.", "nodeName": "Weather",
			"parameters": map[string]any{
				"method": "GET",
				"url":    map[string]any{"mode": "expression", "value": upstream.URL + "/weather/{{ $fromAI('city', 'the city to look up') }}"},
			},
			"credentials": map[string]any{},
		})},
	}, engine.Request{
		Credentials: agentCredentials(), Execution: agentExecution(),
		Events: func(event engine.NodeEvent) {},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output[0]) != 1 || output[0][0].JSON["output"] != "19C" {
		t.Fatalf("agent output = %#v, want the final answer", output[0])
	}
	// The schema is derived: the one $fromAI call advertises one property
	// carrying its description.
	properties, _ := sawSchema["properties"].(map[string]any)
	city, _ := properties["city"].(map[string]any)
	if city["description"] != "the city to look up" {
		t.Errorf("tool schema = %#v, want the derived city property", sawSchema)
	}
	// And the model's argument replaced the call at invoke time.
	if toolPath != "/weather/Utrecht" {
		t.Errorf("tool called %q, want the model's argument substituted", toolPath)
	}
}

// observedByModel runs an agent that calls one HTTP tool once against upstream,
// and returns what the model was handed back for that call: the tool message in
// the request the provider received for its second turn. Asserting there, not
// on the tool's own return value, is what pins what the model can see.
func observedByModel(t *testing.T, upstream http.HandlerFunc) string {
	t.Helper()

	server := httptest.NewServer(upstream)
	defer server.Close()

	var mu sync.Mutex
	turn := 0
	var observation string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var request struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		for _, message := range request.Messages {
			if message.Role == "tool" {
				observation = message.Content
			}
		}
		w.Header().Set("Content-Type", "application/json")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(assistantToolCalls(`{"name":"list_users","arguments":"{}"}`)))
			return
		}
		_, _ = w.Write([]byte(assistantAnswer("done")))
	}))
	defer provider.Close()

	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	if _, err := executor.Execute(context.Background(), agentIR(t, map[string]any{"prompt": "How many users?"}), workflow.NodeInput{
		"main":  {{JSON: map[string]any{}}},
		"model": {modelItem(provider.URL)},
		"tools": {toolItem(map[string]any{
			"kind": "tool", "name": "list_users", "description": "List every user.", "nodeName": "List users",
			"parameters":  map[string]any{"method": "GET", "url": server.URL + "/users"},
			"credentials": map[string]any{},
		})},
	}, engine.Request{
		Credentials: agentCredentials(), Execution: agentExecution(),
		Events: func(event engine.NodeEvent) {},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if observation == "" {
		t.Fatal("the model was never handed a tool result")
	}
	return observation
}

func TestHTTPToolHandsTheModelEveryElementOfAnArrayResponse(t *testing.T) {
	t.Parallel()

	// The request node makes one item per array element; the tool used to keep
	// the first, so an agent asked how many users there were answered one.
	observation := observedByModel(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":1,"name":"Rina Wijaya","plan":"pro"},
			{"id":2,"name":"Budi Santoso","plan":"free"},
			{"id":3,"name":"Sari Lestari","plan":"pro"}
		]`))
	})

	var users []map[string]any
	if err := json.Unmarshal([]byte(observation), &users); err != nil {
		t.Fatalf("observation = %q, want a JSON array of the users: %v", observation, err)
	}
	if len(users) != 3 {
		t.Fatalf("observation = %s, want all 3 users", observation)
	}
	for index, name := range []string{"Rina Wijaya", "Budi Santoso", "Sari Lestari"} {
		if users[index]["name"] != name {
			t.Errorf("user %d = %#v, want %q, in the order the endpoint sent them", index, users[index], name)
		}
	}
}

func TestHTTPToolHandsTheModelASingleObjectResponseUnchanged(t *testing.T) {
	t.Parallel()

	observation := observedByModel(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"name":"Rina Wijaya","plan":"pro"}`))
	})

	if observation != `{"id":1,"name":"Rina Wijaya","plan":"pro"}` {
		t.Fatalf("observation = %q, want the one object, not wrapped in an array", observation)
	}
}

// TestHTTPToolCutsAnOversizeResponseAndSaysSo: the cap keeps an endpoint's whole
// payload out of the model's context, and the note is what stops the cut from
// being a silent one — a model that reads 200 rows out of 3000 must know it.
func TestHTTPToolCutsAnOversizeResponseAndSaysSo(t *testing.T) {
	t.Parallel()

	// 12 rows of 30 KiB is 360 KiB of JSON, past the 256 KiB cap.
	observation := observedByModel(t, func(w http.ResponseWriter, _ *http.Request) {
		rows := make([]map[string]any, 12)
		for index := range rows {
			rows[index] = map[string]any{"id": index + 1, "note": strings.Repeat("é", 15*1024)}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rows)
	})

	shown, note, found := strings.Cut(observation, "\n[truncated: ")
	if !found {
		t.Fatalf("an oversize response reached the model with no truncation note (%d bytes)", len(observation))
	}
	if len(shown) > 256*1024 {
		t.Errorf("the model was handed %d bytes of response, want at most the 262144-byte cap", len(shown))
	}
	if !utf8.ValidString(shown) {
		t.Error("the cut split a multi-byte character")
	}
	if !strings.HasPrefix(shown, `[{"id":1,`) {
		t.Errorf("the kept part starts %q, want the start of the array", shown[:20])
	}
	for _, want := range []string{"12 items", "only the first", "bytes are omitted", "partial result"} {
		if !strings.Contains(note, want) {
			t.Errorf("truncation note = %q, want it to say %q", note, want)
		}
	}
}

func parserItem(schema map[string]any, maxRetries float64) workflow.Item {
	return toolItem(map[string]any{
		"kind": "parser", "nodeName": "Parser",
		"schema": schema, "maxRetries": maxRetries,
	})
}

func TestAgentParserReturnsTheParsedObject(t *testing.T) {
	t.Parallel()

	provider := scriptProvider(t, []string{
		assistantToolCalls(`{"name":"format_final_json_response","arguments":"{\"city\":\"Utrecht\",\"tempC\":19}"}`),
	})

	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	output, err := executor.Execute(context.Background(), agentIR(t, map[string]any{"prompt": "Weather?"}), workflow.NodeInput{
		"main":  {{JSON: map[string]any{}}},
		"model": {modelItem(provider.URL)},
		"outputParser": {parserItem(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city":  map[string]any{"type": "string"},
				"tempC": map[string]any{"type": "number"},
			},
			"required": []any{"city"},
		}, 2)},
	}, engine.Request{
		Credentials: agentCredentials(), Execution: agentExecution(),
		Events: func(event engine.NodeEvent) {},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// The output item carries the parsed object, not a JSON string, and it
	// validates against the declared schema.
	parsed, ok := output[0][0].JSON["output"].(map[string]any)
	if !ok {
		t.Fatalf("output = %#v, want the parsed object", output[0][0].JSON["output"])
	}
	if parsed["city"] != "Utrecht" || parsed["tempC"] != float64(19) {
		t.Errorf("output = %#v, want the validated answer", parsed)
	}
}

func TestChainParserRetriesOnceThenReturnsTheObject(t *testing.T) {
	t.Parallel()

	var calls int
	var mu sync.Mutex
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write([]byte(assistantAnswer("nope")))
			return
		}
		_, _ = w.Write([]byte(assistantAnswer(`{"city":"Utrecht"}`)))
	}))
	defer provider.Close()

	var published []engine.NodeEvent
	definition, _ := aiRegistry(t).Lookup(nodes.ChainNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "chain", Name: "Chain", Type: nodes.ChainNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"promptType": "auto", "inputField": "q"},
		Definition: definition,
	}
	executor := nodes.NewChainExecutor(localPolicy(), 0)
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{
		"main":  {{JSON: map[string]any{"q": "Where?"}}},
		"model": {modelItem(provider.URL)},
		"outputParser": {parserItem(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{"type": "string"},
			},
		}, 2)},
	}, engine.Request{
		Credentials: agentCredentials(), Execution: agentExecution(),
		Events: func(event engine.NodeEvent) { published = append(published, event) },
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	parsed, ok := output[0][0].JSON["output"].(map[string]any)
	if !ok || parsed["city"] != "Utrecht" {
		t.Fatalf("output = %#v, want the parsed object", output[0][0].JSON["output"])
	}
	if calls != 2 {
		t.Errorf("model calls = %d, want the first attempt plus one retry", calls)
	}
	started := 0
	for _, event := range published {
		if event.Name == string(ai.EventModelStarted) {
			started++
		}
	}
	if started != 2 {
		t.Errorf("model started events = %d, want the retry count visible in events", started)
	}
}

func TestChainHasNoMemorySlot(t *testing.T) {
	t.Parallel()

	definition, found := aiRegistry(t).Get(nodes.ChainNodeType, workflow.V(1))
	if !found {
		t.Fatal("the Basic LLM Chain node is not registered")
	}
	for _, port := range definition.Inputs {
		if port.Kind == workflow.ConnectionMemory {
			t.Errorf("chain has a memory input %q; the chain must stay memoryless so a wired memory cannot silently do nothing", port.Name)
		}
	}
}

// TestToolFamilyDefinitions pins the catalogue shape the importer maps
// onto. It needs the five RegisterAll lines; without them the lookups fail
// and TestEveryBuiltinExecutorIsReferenced names the dead executors.
func TestToolFamilyDefinitions(t *testing.T) {
	t.Parallel()

	registry := aiRegistry(t)
	singleToolPort := func(t *testing.T, nodeType string) {
		t.Helper()
		definition, found := registry.Get(nodeType, workflow.V(1))
		if !found {
			t.Fatalf("%s is not registered", nodeType)
		}
		if len(definition.Inputs) != 0 {
			t.Errorf("%s inputs = %#v, want none", nodeType, definition.Inputs)
		}
		if len(definition.Outputs) != 1 || definition.Outputs[0].Kind != workflow.ConnectionTool {
			t.Errorf("%s outputs = %#v, want a single ai_tool port", nodeType, definition.Outputs)
		}
		if definition.Codex == nil || len(definition.Codex.Categories) == 0 {
			t.Errorf("%s has no picker codex; tool variants must re-file under AI tools", nodeType)
		}
	}
	singleToolPort(t, nodes.WorkflowToolNodeType)
	singleToolPort(t, nodes.CalculatorToolNodeType)
	singleToolPort(t, nodes.MCPClientToolNodeType)
	singleToolPort(t, nodes.DatastoreToolNodeType)

	parser, found := registry.Get(nodes.OutputParserNodeType, workflow.V(1))
	if !found {
		t.Fatal("kilasflow.outputParser is not registered")
	}
	if len(parser.Outputs) != 1 || parser.Outputs[0].Kind != workflow.ConnectionOutputParser {
		t.Errorf("outputParser outputs = %#v, want a single ai_outputParser port", parser.Outputs)
	}

	agent, found := registry.Get(nodes.AgentNodeType, workflow.V(1))
	if !found {
		t.Fatal("the AI Agent node is not registered")
	}
	parsers := 0
	for _, port := range agent.Inputs {
		if port.Kind == workflow.ConnectionOutputParser {
			parsers++
			if port.MaxConnections != 1 {
				t.Errorf("agent outputParser port allows %d connections, want at most one", port.MaxConnections)
			}
		}
	}
	if parsers != 1 {
		t.Errorf("agent has %d output parser ports, want exactly one", parsers)
	}

	calculator, found := registry.Get(nodes.CalculatorNodeType, workflow.V(1))
	if !found {
		t.Fatal("kilasflow.calculator is not registered")
	}
	if len(calculator.Inputs) != 1 || calculator.Inputs[0].Kind != workflow.ConnectionMain {
		t.Errorf("calculator inputs = %#v, want one main port", calculator.Inputs)
	}
	if err := calculator.Validate(workflow.Node{Parameters: map[string]any{}}); err == nil {
		t.Error("calculator without an expression was accepted")
	}
	if err := parser.Validate(workflow.Node{Parameters: map[string]any{
		"schemaType": "jsonSchema", "jsonSchema": `{"type":"object"}`,
	}}); err != nil {
		t.Errorf("Validate(valid parser) = %v", err)
	}
	if err := parser.Validate(workflow.Node{Parameters: map[string]any{
		"schemaType": "jsonSchema", "jsonSchema": `{"type":"paragraph"}`,
	}}); err == nil {
		t.Error("parser with an unknown schema type was accepted")
	}
}
