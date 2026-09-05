package ai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/ai"
)

func TestOpenAICompatibleSendsTheConversationAndReadsTheAnswer(t *testing.T) {
	t.Parallel()

	var received map[string]any
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"role":"assistant","content":"Hello, Ada."},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":11,"completion_tokens":4,"total_tokens":15}
		}`))
	}))
	defer server.Close()

	model := ai.NewOpenAICompatible(server.Client(), server.URL, "sk-test")
	response, err := model.Complete(context.Background(), ai.ModelRequest{
		Model:    "gpt-test",
		Messages: []ai.Message{{Role: ai.RoleUser, Content: "Say hello"}},
		Tools:    []ai.ToolDefinition{{Name: "get_weather", Description: "Weather"}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if response.Message.Content != "Hello, Ada." || response.FinishReason != "stop" {
		t.Fatalf("response = %#v, want the provider's answer", response)
	}
	if response.Usage.TotalTokens != 15 {
		t.Errorf("usage = %#v, want the provider's tokens", response.Usage)
	}
	if authorization != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want the credential applied", authorization)
	}
	if received["model"] != "gpt-test" {
		t.Errorf("request model = %#v, want gpt-test", received["model"])
	}
	tools, _ := received["tools"].([]any)
	if len(tools) != 1 {
		t.Errorf("request tools = %#v, want the tool definition sent", received["tools"])
	}
}

func TestOpenAICompatibleReadsToolCalls(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"role":"assistant","tool_calls":[
				{"id":"call-9","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Utrecht\"}"}}
			]},"finish_reason":"tool_calls"}]
		}`))
	}))
	defer server.Close()

	response, err := ai.NewOpenAICompatible(server.Client(), server.URL, "sk-test").
		Complete(context.Background(), ai.ModelRequest{Model: "m", Messages: []ai.Message{{Role: ai.RoleUser, Content: "x"}}})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %#v, want one", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call-9" || call.Name != "get_weather" || !strings.Contains(string(call.Arguments), "Utrecht") {
		t.Fatalf("tool call = %#v, want the provider's call", call)
	}
}

func TestOpenAICompatibleAssemblesAStream(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range []string{
			`{"choices":[{"delta":{"content":"Hello, "}}]}`,
			`{"choices":[{"delta":{"content":"Ada."}}]}`,
			// A malformed frame mid-stream must not fail a run that is
			// otherwise producing output.
			`not json`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"total_tokens":7}}`,
		} {
			_, _ = w.Write([]byte("data: " + frame + "\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	var chunks []string
	response, err := ai.NewOpenAICompatible(server.Client(), server.URL, "sk-test").
		Stream(context.Background(), ai.ModelRequest{Model: "m", Messages: []ai.Message{{Role: ai.RoleUser, Content: "x"}}},
			func(delta string) { chunks = append(chunks, delta) })
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if strings.Join(chunks, "") != "Hello, Ada." || response.Message.Content != "Hello, Ada." {
		t.Fatalf("stream = %v / %q, want the assembled content", chunks, response.Message.Content)
	}
	if response.FinishReason != "stop" || response.Usage.TotalTokens != 7 {
		t.Errorf("response = %#v, want the trailing finish reason and usage", response)
	}
}

func TestOpenAICompatibleAssemblesStreamedToolCallFragments(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Providers split one tool call across frames, keyed by index.
		for _, frame := range []string{
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"get_weather","arguments":"{\"ci"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ty\":\"Utrecht\"}"}}]}}]}`,
		} {
			_, _ = w.Write([]byte("data: " + frame + "\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	response, err := ai.NewOpenAICompatible(server.Client(), server.URL, "sk-test").
		Stream(context.Background(), ai.ModelRequest{Model: "m", Messages: []ai.Message{{Role: ai.RoleUser, Content: "x"}}}, nil)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %#v, want one assembled call", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.Name != "get_weather" || string(call.Arguments) != `{"city":"Utrecht"}` {
		t.Fatalf("assembled call = %#v, want the fragments joined", call)
	}
}

func TestOpenAICompatibleReportsAProviderErrorWithoutEchoingTheRequest(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer server.Close()

	_, err := ai.NewOpenAICompatible(server.Client(), server.URL, "sk-secret-key").
		Complete(context.Background(), ai.ModelRequest{
			Model: "m", Messages: []ai.Message{{Role: ai.RoleUser, Content: "confidential prompt"}},
		})
	if err == nil {
		t.Fatal("a 401 reported success")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %v, want the status reported", err)
	}
	// Echoing the request would put the key and the conversation into an
	// execution record and the live event feed.
	for _, secret := range []string{"sk-secret-key", "confidential prompt"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("provider error leaked %q: %v", secret, err)
		}
	}
}
