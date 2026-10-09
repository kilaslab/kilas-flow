package n8n_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// template6270Shape has the shape of n8n's onboarding template 6270 ("Build
// your first AI agent") as the 2026-09-23 import report records it: a chat
// trigger, an agent, a Gemini model given only options, a window memory,
// sticky notes, and a Get Weather HTTP Request tool whose query the model
// fills. The template's second tool, an RSS Feed Read tool, is left out: this
// server has no RSS node yet (FEAT-kfmq1z). Written here rather than copied,
// since the template itself is not in the repository.
const template6270Shape = `{
  "name": "First AI agent",
  "nodes": [
    {"id":"s1","name":"Sticky Note","type":"n8n-nodes-base.stickyNote","typeVersion":1,"position":[-400,-200],
     "parameters":{"content":"## Try me\nAsk for the weather.","height":200,"width":300}},
    {"id":"s2","name":"Sticky Note1","type":"n8n-nodes-base.stickyNote","typeVersion":1,"position":[400,-200],
     "parameters":{"content":"The agent picks a tool on its own.","height":200,"width":300,"color":4}},
    {"id":"a","name":"When chat message received","type":"@n8n/n8n-nodes-langchain.chatTrigger","typeVersion":1.1,"position":[0,0],
     "webhookId":"6270-shape","parameters":{"options":{}}},
    {"id":"b","name":"Your First AI Agent","type":"@n8n/n8n-nodes-langchain.agent","typeVersion":2.2,"position":[220,0],"parameters":{
      "options":{"systemMessage":"You are a helpful assistant. Use the Get Weather tool for weather questions."}
    }},
    {"id":"c","name":"Connect your model","type":"@n8n/n8n-nodes-langchain.lmChatGoogleGemini","typeVersion":1,"position":[100,220],
     "credentials":{"googlePalmApi":{"id":"1","name":"Google Gemini account"}},
     "parameters":{"options":{}}},
    {"id":"d","name":"Conversation Memory","type":"@n8n/n8n-nodes-langchain.memoryBufferWindow","typeVersion":1.3,"position":[260,220],"parameters":{}},
    {"id":"e","name":"Get Weather","type":"n8n-nodes-base.httpRequestTool","typeVersion":4.2,"position":[420,220],"parameters":{
      "toolDescription":"Get the current weather for a latitude and longitude",
      "url":"https://api.open-meteo.com/v1/forecast",
      "sendQuery":true,
      "queryParameters":{"parameters":[
        {"name":"latitude","value":"={{ $fromAI('latitude', 'Latitude of the city', 'number') }}"},
        {"name":"longitude","value":"={{ $fromAI('longitude', 'Longitude of the city', 'number') }}"},
        {"name":"current","value":"temperature_2m,windspeed_10m"}
      ]},
      "options":{}
    }}
  ],
  "connections": {
    "When chat message received": {"main": [[{"node":"Your First AI Agent","type":"main","index":0}]]},
    "Connect your model": {"ai_languageModel": [[{"node":"Your First AI Agent","type":"ai_languageModel","index":0}]]},
    "Conversation Memory": {"ai_memory": [[{"node":"Your First AI Agent","type":"ai_memory","index":0}]]},
    "Get Weather": {"ai_tool": [[{"node":"Your First AI Agent","type":"ai_tool","index":0}]]}
  }
}`

// BUG-b8bwhw criterion 3, on the template's shape: everything but the RSS
// tool imports with no blocking issue an author could not clear by attaching
// their own credentials, and compiles.
func TestTemplate6270ShapeImportsAndCompiles(t *testing.T) {
	t.Parallel()

	catalogue := registry(t)
	result := importFixture(t, template6270Shape)
	if blocking := blockingApartFromCredentials(result); len(blocking) != 0 {
		t.Fatalf("blocking issues apart from credentials: %#v", blocking)
	}
	if got := nodeByName(result.Document, "Get Weather").Type; got != "kilasflow.httpTool" {
		t.Fatalf("Get Weather imported as %q, want kilasflow.httpTool", got)
	}
	compileImported(t, result.Document, catalogue)
}

// The imported Get Weather tool runs: the agent calls it, the model's
// coordinates reach the weather service's query string as values, and the
// model reads the service's answer.
func TestImported6270WeatherToolRuns(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var weatherQuery string
	weather := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		weatherQuery = r.URL.RawQuery
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"current":{"temperature_2m":21.5,"windspeed_10m":9.1}}`)
	}))
	t.Cleanup(weather.Close)

	var modelRequests []string
	turns := []string{
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"Get_Weather","arguments":"{\"latitude\":52.52,\"longitude\":13.41}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"choices":[{"message":{"role":"assistant","content":"It is 21.5 degrees in Berlin."},"finish_reason":"stop"}]}`,
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		turn := len(modelRequests)
		modelRequests = append(modelRequests, string(raw))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if turn >= len(turns) {
			turn = len(turns) - 1
		}
		_, _ = io.WriteString(w, turns[turn])
	}))
	t.Cleanup(model.Close)

	policy := safehttp.DefaultPolicy()
	for _, server := range []*httptest.Server{weather, model} {
		policy.AllowedPrivateEndpoints = append(policy.AllowedPrivateEndpoints, strings.TrimPrefix(server.URL, "http://"))
	}
	catalogue := registry(t)
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, policy, sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil,
		nodes.WithNodeCatalog(catalogue)); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	result := importFixture(t, template6270Shape)
	toolNode := nodeByName(result.Document, "Get Weather")
	// The real service is outside the test; the imported request is aimed at
	// the stand-in, and nothing else about it changes.
	toolNode.Parameters["url"] = weather.URL + "/v1/forecast"
	toolDefinition, _ := catalogue.Lookup(toolNode.Type, toolNode.TypeVersion)
	toolExecutor, _ := executors.Lookup(toolDefinition.ExecutorID)
	descriptor, err := toolExecutor.Execute(context.Background(), workflow.IRNode{
		ID: toolNode.ID, Name: toolNode.Name, Type: toolNode.Type, TypeVersion: toolNode.TypeVersion,
		Parameters: toolNode.Parameters, Definition: toolDefinition,
	}, workflow.NodeInput{}, engine.Request{})
	if err != nil {
		t.Fatalf("tool node Execute() error = %v", err)
	}

	agentNode := nodeByName(result.Document, "Your First AI Agent")
	agentDefinition, _ := catalogue.Lookup(agentNode.Type, agentNode.TypeVersion)
	agent, _ := executors.Lookup(agentDefinition.ExecutorID)
	output, err := agent.Execute(context.Background(), workflow.IRNode{
		ID: agentNode.ID, Name: agentNode.Name, Type: agentNode.Type, TypeVersion: agentNode.TypeVersion,
		Parameters: agentNode.Parameters, Definition: agentDefinition,
	}, workflow.NodeInput{
		"main": {{JSON: map[string]any{"chatInput": "What is the weather in Berlin?", "sessionId": "s1"}}},
		"model": {{JSON: map[string]any{"$ai": map[string]any{
			"kind": "model", "model": "test-model", "baseUrl": model.URL, "credentialId": "cred-key",
		}}}},
		"tools": descriptor[0],
	}, engine.Request{
		Credentials: credentialsByID{"cred-key": {ID: "cred-key", Type: "httpBearerAuth", Fields: map[string]string{"token": "k"}}},
		Execution:   engine.ExecutionContext{ID: "exec-6270", TenantID: "tenant-1", WorkflowID: "wf_6270"},
	})
	if err != nil {
		t.Fatalf("agent Execute() error = %v", err)
	}
	if got := output[0][0].JSON["output"]; got != "It is 21.5 degrees in Berlin." {
		t.Fatalf("agent output = %#v", output[0][0].JSON)
	}
	if !strings.Contains(weatherQuery, "latitude=52.52") || !strings.Contains(weatherQuery, "longitude=13.41") ||
		!strings.Contains(weatherQuery, "current=temperature_2m") {
		t.Errorf("weather query = %q, want the model's coordinates and the fixed field list", weatherQuery)
	}
	if len(modelRequests) != 2 || !strings.Contains(modelRequests[1], "temperature_2m") {
		t.Errorf("the model was not handed the weather service's answer: %d requests", len(modelRequests))
	}
}

// credentialsByID resolves credentials by id, for the agent and its model.
type credentialsByID map[string]engine.Credential

func (credentials credentialsByID) ResolveCredential(_ context.Context, id string) (engine.Credential, error) {
	credential, found := credentials[id]
	if !found {
		return engine.Credential{}, fmt.Errorf("credential %q is not stored", id)
	}
	return credential, nil
}
