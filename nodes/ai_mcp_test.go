package nodes_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// mcpStubServer speaks just enough Streamable HTTP to exercise the MCP
// client tool: initialize, notifications/initialized, tools/list and
// tools/call, all as plain JSON-RPC. Anything the client sends outside that
// handshake fails the test, so drift in the protocol shows up here first.
type mcpStubServer struct {
	t *testing.T

	mu          sync.Mutex
	methods     []string
	authHeaders []string
	callNames   []string
	callArgs    []map[string]any

	// slowTools names tools whose call sleeps before answering, to push a
	// call over its time budget.
	slowTools map[string]time.Duration
	// sseList serves tools/list as text/event-stream instead of plain JSON.
	sseList bool
}

func startMCPStub(t *testing.T, stub *mcpStubServer) *httptest.Server {
	t.Helper()
	stub.t = t
	server := httptest.NewServer(http.HandlerFunc(stub.serve))
	t.Cleanup(server.Close)
	return server
}

func (stub *mcpStubServer) methodBody(r *http.Request) (string, float64, map[string]any) {
	var decoded struct {
		Method string         `json:"method"`
		ID     float64        `json:"id"`
		Params map[string]any `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
		return "", 0, nil
	}
	if decoded.Params == nil {
		decoded.Params = map[string]any{}
	}
	return decoded.Method, decoded.ID, decoded.Params
}

func (stub *mcpStubServer) writeResult(w http.ResponseWriter, id float64, result any) {
	w.Header().Set("Content-Type", "application/json")
	encoded, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	if err != nil {
		stub.t.Errorf("encode stub result: %v", err)
		return
	}
	_, _ = w.Write(encoded)
}

func (stub *mcpStubServer) serve(w http.ResponseWriter, r *http.Request) {
	if contentType := r.Header.Get("Content-Type"); contentType != "application/json" {
		http.Error(w, "want application/json", http.StatusBadRequest)
		return
	}
	method, id, params := stub.methodBody(r)

	stub.mu.Lock()
	stub.methods = append(stub.methods, method)
	stub.authHeaders = append(stub.authHeaders, r.Header.Get("Authorization"))
	stub.mu.Unlock()

	switch method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "sess-1")
		stub.writeResult(w, id, map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"serverInfo":      map[string]any{"name": "stub", "version": "0"},
		})
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		tools := []map[string]any{
			{
				"name":        "get_weather",
				"description": "Reads the forecast.",
				"inputSchema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"city": map[string]any{"type": "string"}},
					"required":   []string{"city"},
				},
			},
			{
				"name":        "echo",
				"description": "Echoes its arguments.",
				"inputSchema": map[string]any{"type": "object"},
			},
			{
				"name":        "failing_tool",
				"description": "Always fails.",
				"inputSchema": map[string]any{"type": "object"},
			},
			{
				"name":        "slow_tool",
				"description": "Answers too late.",
				"inputSchema": map[string]any{"type": "object"},
			},
		}
		if stub.sseList {
			w.Header().Set("Content-Type", "text/event-stream")
			encoded, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"tools": tools}})
			_, _ = fmt.Fprintf(w, ": serving the list as events\n\ndata: %s\n\n", encoded)
			return
		}
		stub.writeResult(w, id, map[string]any{"tools": tools})
	case "tools/call":
		name, _ := params["name"].(string)
		args, _ := params["arguments"].(map[string]any)
		if args == nil {
			args = map[string]any{}
		}
		stub.mu.Lock()
		stub.callNames = append(stub.callNames, name)
		stub.callArgs = append(stub.callArgs, args)
		stub.mu.Unlock()

		if delay := stub.slowTools[name]; delay > 0 {
			time.Sleep(delay)
		}
		if name == "failing_tool" {
			stub.writeResult(w, id, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "boom"}},
				"isError": true,
			})
			return
		}
		text := "ok"
		if name == "get_weather" {
			city, _ := args["city"].(string)
			text = "sunny in " + city
		} else if raw, err := json.Marshal(args); err == nil {
			text = string(raw)
		}
		stub.writeResult(w, id, map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": false,
		})
	default:
		w.Header().Set("Content-Type", "application/json")
		encoded, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": id,
			"error": map[string]any{"code": -32601, "message": "unknown method " + method},
		})
		_, _ = w.Write(encoded)
	}
}

func (stub *mcpStubServer) callsTo(name string) (map[string]any, bool) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	for index, called := range stub.callNames {
		if called == name {
			return stub.callArgs[index], true
		}
	}
	return nil, false
}

func mcpToolIR(serverURL string, parameters map[string]any) workflow.IRNode {
	merged := map[string]any{"serverUrl": serverURL}
	for key, value := range parameters {
		merged[key] = value
	}
	return workflow.IRNode{
		ID: "mcp", Name: "MCP", Type: nodes.MCPClientToolNodeType, TypeVersion: workflow.V(1),
		Parameters:  merged,
		Credentials: map[string]string{"httpBearerAuth": "cred-mcp"},
	}
}

func mcpToolRequest() engine.Request {
	return engine.Request{
		Credentials: agentCredentials(), Execution: agentExecution(),
		Events: func(event engine.NodeEvent) {},
	}
}

func mcpDescriptors(t *testing.T, output workflow.NodeOutput) []map[string]any {
	t.Helper()
	if len(output) != 1 {
		t.Fatalf("outputs = %d, want one port", len(output))
	}
	descriptors := make([]map[string]any, 0, len(output[0]))
	for _, item := range output[0] {
		descriptor, ok := item.JSON["$ai"].(map[string]any)
		if !ok {
			t.Fatalf("item = %#v, want an $ai descriptor", item.JSON)
		}
		descriptors = append(descriptors, descriptor)
	}
	return descriptors
}

func TestMCPClientToolListsOneDescriptorPerServerTool(t *testing.T) {
	t.Parallel()

	stub := &mcpStubServer{}
	server := startMCPStub(t, stub)

	executor := nodes.NewMCPClientToolExecutor(localPolicy())
	output, err := executor.Execute(context.Background(), mcpToolIR(server.URL, nil), workflow.NodeInput{}, mcpToolRequest())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	descriptors := mcpDescriptors(t, output)
	if len(descriptors) != 4 {
		t.Fatalf("descriptors = %d, want one per server tool", len(descriptors))
	}
	names := map[string]map[string]any{}
	for _, descriptor := range descriptors {
		if descriptor["kind"] != "mcp" {
			t.Errorf("descriptor kind = %#v, want mcp", descriptor["kind"])
		}
		name, _ := descriptor["name"].(string)
		names[name] = descriptor
		if descriptor["nodeName"] != "MCP" {
			t.Errorf("descriptor nodeName = %#v, want the canvas name", descriptor["nodeName"])
		}
		if descriptor["serverUrl"] != server.URL {
			t.Errorf("descriptor serverUrl = %#v, want the endpoint", descriptor["serverUrl"])
		}
	}
	weather, found := names["get_weather"]
	if !found {
		t.Fatal("no descriptor for get_weather; the agent must expose each server tool under its own name")
	}
	schema, _ := weather["inputSchema"].(map[string]any)
	properties, _ := schema["properties"].(map[string]any)
	if _, found := properties["city"]; !found {
		t.Errorf("get_weather schema = %#v, want the server's own input schema", schema)
	}

	// The descriptor travels in an item that is persisted in the execution
	// record: it may carry the credential id, never the secret.
	marshalled, _ := json.Marshal(output)
	if strings.Contains(string(marshalled), "sk-live-secret") {
		t.Error("execution output contains the credential secret; only the credential id may travel in the descriptor")
	}
	if !strings.Contains(string(marshalled), "cred-mcp") {
		t.Error("execution output has no credential id; the agent could not re-resolve the secret at call time")
	}

	// The listing itself already authenticated from the store.
	stub.mu.Lock()
	defer stub.mu.Unlock()
	authenticated := false
	for _, header := range stub.authHeaders {
		if header == "Bearer sk-live-secret" {
			authenticated = true
		}
	}
	if !authenticated {
		t.Errorf("auth headers = %q, want the stored bearer applied to the MCP requests", stub.authHeaders)
	}
}

func TestMCPClientToolAllowlistFiltersAndNamesMisses(t *testing.T) {
	t.Parallel()

	stub := &mcpStubServer{}
	server := startMCPStub(t, stub)
	executor := nodes.NewMCPClientToolExecutor(localPolicy())

	output, err := executor.Execute(context.Background(),
		mcpToolIR(server.URL, map[string]any{"tools": "echo, get_weather"}), workflow.NodeInput{}, mcpToolRequest())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if descriptors := mcpDescriptors(t, output); len(descriptors) != 2 {
		t.Fatalf("descriptors = %d, want only the selected tools", len(descriptors))
	}

	_, err = executor.Execute(context.Background(),
		mcpToolIR(server.URL, map[string]any{"tools": "echo, missing_tool"}), workflow.NodeInput{}, mcpToolRequest())
	if err == nil || !strings.Contains(err.Error(), "missing_tool") {
		t.Fatalf("Execute() error = %v, want it to name the tool the server has not got", err)
	}
}

func TestMCPClientToolReadsAnSSEList(t *testing.T) {
	t.Parallel()

	stub := &mcpStubServer{sseList: true}
	server := startMCPStub(t, stub)

	executor := nodes.NewMCPClientToolExecutor(localPolicy())
	output, err := executor.Execute(context.Background(), mcpToolIR(server.URL, map[string]any{"tools": "echo"}), workflow.NodeInput{}, mcpToolRequest())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if descriptors := mcpDescriptors(t, output); len(descriptors) != 1 {
		t.Fatalf("descriptors = %d, want the selected tool from the event stream", len(descriptors))
	}
}

func TestMCPClientToolCallReachesTheServerThroughTheAgent(t *testing.T) {
	t.Parallel()

	stub := &mcpStubServer{}
	server := startMCPStub(t, stub)

	listing := nodes.NewMCPClientToolExecutor(localPolicy())
	output, err := listing.Execute(context.Background(),
		mcpToolIR(server.URL, map[string]any{"tools": "get_weather"}), workflow.NodeInput{}, mcpToolRequest())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	tools := make([]workflow.Item, 0)
	for _, item := range output[0] {
		tools = append(tools, workflow.Item{JSON: item.JSON})
	}

	provider := scriptProvider(t, []string{
		assistantToolCalls(`{"name":"get_weather","arguments":"{\"city\":\"Oslo\"}"}`),
		assistantAnswer("sunny indeed"),
	})
	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	result, err := executor.Execute(context.Background(),
		agentIR(t, map[string]any{"prompt": "Weather?"}),
		workflow.NodeInput{
			"main":  {{JSON: map[string]any{}}},
			"model": {modelItem(provider.URL)},
			"tools": tools,
		}, engine.Request{
			Credentials: agentCredentials(), Execution: agentExecution(),
			Events: func(event engine.NodeEvent) {},
		})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result[0]) != 1 || result[0][0].JSON["output"] != "sunny indeed" {
		t.Fatalf("agent output = %#v, want the final answer after the tool turn", result)
	}
	args, found := stub.callsTo("get_weather")
	if !found {
		t.Fatal("the stub saw no tools/call; the model turn never became a round trip")
	}
	if args["city"] != "Oslo" {
		t.Errorf("tools/call arguments = %#v, want the model's city", args)
	}
}

func TestMCPClientToolDuplicateNameFailsTheRun(t *testing.T) {
	t.Parallel()

	provider := scriptProvider(t, []string{assistantAnswer("unused")})
	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	_, err := executor.Execute(context.Background(),
		agentIR(t, map[string]any{"prompt": "Do it."}),
		workflow.NodeInput{
			"main":  {{JSON: map[string]any{}}},
			"model": {modelItem(provider.URL)},
			"tools": {
				toolItem(map[string]any{
					"kind": "mcp", "name": "get_weather", "description": "A.", "nodeName": "MCP A",
					"serverUrl": "https://mcp.test/", "inputSchema": map[string]any{"type": "object"},
				}),
				toolItem(map[string]any{
					"kind": "calculator", "name": "get_weather", "description": "B.", "nodeName": "Calc B",
					"parameters": map[string]any{"expression": ""},
				}),
			},
		}, engine.Request{
			Credentials: agentCredentials(), Execution: agentExecution(),
			Events: func(event engine.NodeEvent) {},
		})
	if err == nil {
		t.Fatal("Execute() succeeded with two tools under one name; one would shadow the other")
	}
	for _, want := range []string{"get_weather", "MCP A", "Calc B"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name %q", err.Error(), want)
		}
	}
}

func TestMCPClientToolRefusesLoopbackUnderDefaultPolicy(t *testing.T) {
	t.Parallel()

	stub := &mcpStubServer{}
	server := startMCPStub(t, stub)

	executor := nodes.NewMCPClientToolExecutor(safehttp.DefaultPolicy())
	_, err := executor.Execute(context.Background(), mcpToolIR(server.URL, nil), workflow.NodeInput{}, mcpToolRequest())
	if err == nil {
		t.Fatal("Execute() reached loopback under the default policy; the SSRF guard did not apply")
	}
	if !errors.Is(err, safehttp.ErrBlocked) {
		t.Fatalf("Execute() error = %v, want the policy refusal", err)
	}
}

func TestMCPClientToolRefusesAStdioServer(t *testing.T) {
	t.Parallel()

	executor := nodes.NewMCPClientToolExecutor(localPolicy())
	_, err := executor.Execute(context.Background(),
		mcpToolIR("stdio://weather-server", nil), workflow.NodeInput{}, mcpToolRequest())
	if err == nil || !strings.Contains(err.Error(), "stdio") {
		t.Fatalf("Execute() error = %v, want the stdio refusal with its diagnostic", err)
	}
}

func TestMCPClientToolCallErrorIsReportedToTheModel(t *testing.T) {
	t.Parallel()

	stub := &mcpStubServer{}
	server := startMCPStub(t, stub)

	provider := scriptProvider(t, []string{
		assistantToolCalls(`{"name":"failing_tool","arguments":"{}"}`),
		assistantAnswer("it failed gracefully"),
	})
	var failed []string
	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	result, err := executor.Execute(context.Background(),
		agentIR(t, map[string]any{"prompt": "Try it."}),
		workflow.NodeInput{
			"main":  {{JSON: map[string]any{}}},
			"model": {modelItem(provider.URL)},
			"tools": {toolItem(map[string]any{
				"kind": "mcp", "name": "failing_tool", "description": "Fails.", "nodeName": "MCP",
				"serverUrl": server.URL, "inputSchema": map[string]any{"type": "object"},
				"credentials": map[string]any{"httpBearerAuth": "cred-mcp"},
			})},
		}, engine.Request{
			Credentials: agentCredentials(), Execution: agentExecution(),
			Events: func(event engine.NodeEvent) {
				if event.Name == string(ai.EventToolFailed) {
					failed = append(failed, event.Name)
				}
			},
		})
	if err != nil {
		t.Fatalf("Execute() error = %v; a failing tool call must not abort the run", err)
	}
	if len(result[0]) != 1 || result[0][0].JSON["output"] != "it failed gracefully" {
		t.Fatalf("agent output = %#v, want the model to answer after the tool diagnostic", result)
	}
	if len(failed) != 1 {
		t.Errorf("tool failures = %d, want the diagnostic event for the failed call", len(failed))
	}
}

func TestMCPClientToolCallOverBudgetFailsTheCallNotTheRun(t *testing.T) {
	t.Parallel()

	stub := &mcpStubServer{slowTools: map[string]time.Duration{"slow_tool": 400 * time.Millisecond}}
	server := startMCPStub(t, stub)

	provider := scriptProvider(t, []string{
		assistantToolCalls(`{"name":"slow_tool","arguments":"{}"}`),
		assistantAnswer("too slow, skipping"),
	})
	executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
	result, err := executor.Execute(context.Background(),
		agentIR(t, map[string]any{"prompt": "Try it."}),
		workflow.NodeInput{
			"main":  {{JSON: map[string]any{}}},
			"model": {modelItem(provider.URL)},
			"tools": {toolItem(map[string]any{
				"kind": "mcp", "name": "slow_tool", "description": "Slow.", "nodeName": "MCP",
				"serverUrl": server.URL, "inputSchema": map[string]any{"type": "object"},
				"credentials":    map[string]any{"httpBearerAuth": "cred-mcp"},
				"timeoutSeconds": 0.05,
			})},
		}, engine.Request{
			Credentials: agentCredentials(), Execution: agentExecution(),
			Events: func(event engine.NodeEvent) {},
		})
	if err != nil {
		t.Fatalf("Execute() error = %v; an over-budget call must fail the call, not the run", err)
	}
	if len(result[0]) != 1 || result[0][0].JSON["output"] != "too slow, skipping" {
		t.Fatalf("agent output = %#v, want the model to answer after the timeout diagnostic", result)
	}
}
