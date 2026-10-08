package ai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
)

// reasoningProvider answers the first request with a tool call carrying the
// model's thinking under field, and every later one with a plain answer. It
// records each request body so a test can read what was sent back.
func reasoningProvider(t *testing.T, field string, stream bool) (*httptest.Server, func() []map[string]any) {
	t.Helper()
	var (
		mu       sync.Mutex
		requests []map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var decoded map[string]any
		_ = json.Unmarshal(body, &decoded)
		mu.Lock()
		requests = append(requests, decoded)
		first := len(requests) == 1
		mu.Unlock()

		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			frames := []string{`{"choices":[{"delta":{"content":"The result is 7006561."},"finish_reason":"stop"}]}`}
			if first {
				// The thinking arrives in fragments, before the tool call.
				frames = []string{
					`{"choices":[{"delta":{"role":"assistant","` + field + `":"Multiply first, "}}]}`,
					`{"choices":[{"delta":{"` + field + `":"then subtract."}}]}`,
					`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"calculator","arguments":"{\"input\":\"1234*5678-91\"}"}}]},"finish_reason":"tool_calls"}]}`,
				}
			}
			for _, frame := range frames {
				_, _ = w.Write([]byte("data: " + frame + "\n\n"))
			}
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if first {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","` + field + `":"Multiply first, then subtract.","tool_calls":[
				{"id":"call-1","type":"function","function":{"name":"calculator","arguments":"{\"input\":\"1234*5678-91\"}"}}
			]},"finish_reason":"tool_calls"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"The result is 7006561."},"finish_reason":"stop"}]}`))
	}))
	return server, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any(nil), requests...)
	}
}

// TestReasoningFromAToolTurnIsSentBackOnTheNextTurn: a thinking model replayed
// its tool-call turn without the thinking that produced it answered wrongly in
// most runs (gemma4 on Ollama: 4 of 12 correct as sent, 11 of 12 with the
// reasoning and an explicit empty content restored). The thinking goes back
// under the field it arrived in, exactly as received.
func TestReasoningFromAToolTurnIsSentBackOnTheNextTurn(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"reasoning", "reasoning_content"} {
		for _, stream := range []bool{false, true} {
			name := field
			if stream {
				name += "/stream"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				server, requests := reasoningProvider(t, field, stream)
				defer server.Close()

				calculator := &fakeTool{name: "calculator", result: `{"result":7006561}`}
				result, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
					Model:     ai.NewOpenAICompatible(server.Client(), server.URL, ""),
					ModelName: "gemma4", Input: "what is 1234*5678 minus 91?",
					Tools: []ai.Tool{calculator}, Stream: stream,
				}, nil)
				if err != nil {
					t.Fatalf("Run() error = %v", err)
				}
				if result.Output != "The result is 7006561." {
					t.Fatalf("output = %q, want the answer", result.Output)
				}

				sent := requests()
				if len(sent) != 2 {
					t.Fatalf("requests = %d, want a tool turn and an answer turn", len(sent))
				}
				messages, _ := sent[1]["messages"].([]any)
				var assistant map[string]any
				for _, raw := range messages {
					message, _ := raw.(map[string]any)
					if message["role"] == "assistant" {
						assistant = message
					}
				}
				if assistant == nil {
					t.Fatalf("second request messages = %#v, want the assistant tool-call turn replayed", messages)
				}
				if got := assistant[field]; got != "Multiply first, then subtract." {
					t.Errorf("replayed %s = %#v, want the thinking exactly as received", field, got)
				}
				other := "reasoning_content"
				if field == other {
					other = "reasoning"
				}
				if _, found := assistant[other]; found {
					t.Errorf("replayed turn = %#v, want the thinking only under %q, the field it arrived in", assistant, field)
				}
				content, found := assistant["content"]
				if !found || content != "" {
					t.Errorf("replayed turn = %#v, want an explicit empty content rather than none", assistant)
				}
			})
		}
	}
}

// TestLeakedReasoningTokensNeverReachTheOutput: a thinking model's control
// tokens and think blocks sometimes arrive in the content itself — gemma4
// streamed "…Let me re-run the calculation.<channel|>The result is …". Only
// the answer after them is the turn's content; the thinking is kept aside.
func TestLeakedReasoningTokensNeverReachTheOutput(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		content, reasoning string
	}{
		"closing channel token":  {"Let me re-run the calculation.<channel|>The result is 7006561.", "Let me re-run the calculation."},
		"whole channel block":    {"<|channel>thought\nMultiply first.<channel|>The result is 7006561.", "Multiply first."},
		"think block":            {"<think>Multiply first.</think>\n\nThe result is 7006561.", "Multiply first."},
		"unopened think block":   {"Multiply first.</think>\nThe result is 7006561.", "Multiply first."},
		"nothing to strip":       {"The result is 7006561.", ""},
		"think block after text": {"The result is <think>check</think>7006561.", "check"},
	}
	for name, test := range cases {
		for _, stream := range []bool{false, true} {
			label := name
			if stream {
				label += "/stream"
			}
			t.Run(label, func(t *testing.T) {
				t.Parallel()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						// Split mid-token: stripping must work on the whole
						// answer, not frame by frame.
						half := len(test.content) / 2
						for _, part := range []string{test.content[:half], test.content[half:]} {
							encoded, _ := json.Marshal(part)
							_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":` + string(encoded) + `}}]}` + "\n\n"))
						}
						_, _ = w.Write([]byte("data: [DONE]\n\n"))
						return
					}
					encoded, _ := json.Marshal(test.content)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":` + string(encoded) + `},"finish_reason":"stop"}]}`))
				}))
				defer server.Close()

				result, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
					Model:     ai.NewOpenAICompatible(server.Client(), server.URL, ""),
					ModelName: "gemma4", Input: "what is 1234*5678 minus 91?", Stream: stream,
				}, nil)
				if err != nil {
					t.Fatalf("Run() error = %v", err)
				}
				if result.Output != "The result is 7006561." {
					t.Errorf("output = %q, want only the answer", result.Output)
				}
				final := result.Messages[len(result.Messages)-1]
				if final.Reasoning != test.reasoning {
					t.Errorf("reasoning = %q, want %q kept aside for debugging", final.Reasoning, test.reasoning)
				}
				if strings.Contains(final.Content, "channel") || strings.Contains(final.Content, "think>") {
					t.Errorf("content = %q, want no control tokens", final.Content)
				}
			})
		}
	}
}

// TestAReasoningEffortIsSentOnlyWhenSet: the agent's effort reaches every
// model request as `reasoning_effort`, and an agent that never set one sends
// no such key, so a provider that rejects it is unaffected.
func TestAReasoningEffortIsSentOnlyWhenSet(t *testing.T) {
	t.Parallel()

	for _, effort := range []string{"none", ""} {
		t.Run("effort="+effort, func(t *testing.T) {
			t.Parallel()
			var received map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(body, &received)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
			}))
			defer server.Close()

			if _, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
				Model:     ai.NewOpenAICompatible(server.Client(), server.URL, ""),
				ModelName: "gemma4", Input: "hi", ReasoningEffort: effort,
			}, nil); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			value, found := received["reasoning_effort"]
			if effort == "" && found {
				t.Fatalf("reasoning_effort = %#v, want no key when none was set", value)
			}
			if effort != "" && value != effort {
				t.Fatalf("reasoning_effort = %#v, want %q", value, effort)
			}
		})
	}
}
