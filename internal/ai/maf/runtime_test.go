package maf

import (
	"context"
	"iter"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"

	"github.com/kilaslab/kilas-flow/internal/ai"
)

// stubModel satisfies ai.ChatModel for request validation. The framework agent
// under test carries its own stub provider, so this model is never called.
type stubModel struct{}

func (stubModel) Complete(ctx context.Context, request ai.ModelRequest) (ai.ModelResponse, error) {
	return ai.ModelResponse{}, nil
}

func (stubModel) Stream(ctx context.Context, request ai.ModelRequest, onChunk func(string)) (ai.ModelResponse, error) {
	return ai.ModelResponse{}, nil
}

type weatherIn struct {
	Location string `json:"location"`
}

// TestTwoTurnToolLoop drives the real framework loop — provider Run plus the
// toolautocall middleware — against a stub model: turn one requests a tool,
// the middleware executes it, turn two answers with text and usage. It proves
// the three spike criteria end to end: the loop runs, text deltas stream, and
// token usage reaches the result.
func TestTwoTurnToolLoop(t *testing.T) {
	var calls atomic.Int64
	var gotLocation atomic.Value

	weather := functool.MustNew[weatherIn, string](
		functool.Config{Name: "get_weather", Description: "Reports weather for a location."},
		func(ctx context.Context, in weatherIn) (string, error) {
			calls.Add(1)
			gotLocation.Store(in.Location)
			return "sunny, 21C", nil
		},
	)

	var runs atomic.Int64
	stub := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			switch runs.Add(1) {
			case 1:
				yield(&agent.ResponseUpdate{
					Role:         message.RoleAssistant,
					FinishReason: "tool_calls",
					Contents: message.Contents{
						&message.FunctionCallContent{CallID: "call-1", Name: "get_weather", Arguments: `{"location":"Berlin"}`},
					},
				}, nil)
			default:
				yield(&agent.ResponseUpdate{
					Role:         message.RoleAssistant,
					FinishReason: "stop",
					Contents: message.Contents{
						&message.TextContent{Text: "Sunny in Berlin, 21C."},
						&message.UsageContent{Details: message.UsageDetails{InputTokenCount: 120, OutputTokenCount: 30, TotalTokenCount: 150}},
					},
				}, nil)
			}
		}
	}

	agt := agent.New(
		agent.ProviderConfig{
			ProviderName: "stub",
			Run:          stub,
			Middlewares:  []agent.Middleware{toolautocall.New(toolautocall.Config{AdditionalTools: []tool.Tool{weather}})},
		},
		agent.Config{},
	)

	var events []ai.Event
	sink := ai.EventSink(func(event ai.Event) { events = append(events, event) })

	result, err := New(agt).Run(context.Background(), ai.AgentRequest{
		Model:     stubModel{},
		ModelName: "stub",
		Input:     "What is the weather in Berlin?",
		Stream:    true,
	}, sink)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if calls.Load() != 1 {
		t.Fatalf("tool invocations = %d, want 1", calls.Load())
	}
	if location, _ := gotLocation.Load().(string); location != "Berlin" {
		t.Fatalf("tool location = %q, want %q", location, "Berlin")
	}
	if !strings.Contains(result.Output, "Sunny in Berlin") {
		t.Fatalf("output = %q, want the second-turn answer", result.Output)
	}
	if result.ToolCalls != 1 {
		t.Fatalf("result.ToolCalls = %d, want 1", result.ToolCalls)
	}
	if result.Iterations != 2 {
		t.Fatalf("result.Iterations = %d, want 2", result.Iterations)
	}
	if result.Usage != (ai.Usage{PromptTokens: 120, CompletionTokens: 30, TotalTokens: 150}) {
		t.Fatalf("result.Usage = %+v, want the stub usage mapped", result.Usage)
	}
	if len(result.Messages) == 0 {
		t.Fatal("result.Messages is empty, want the produced turns")
	}

	deltas := 0
	for _, event := range events {
		if event.Kind == ai.EventModelDelta && event.Delta != "" {
			deltas++
		}
	}
	if deltas == 0 {
		t.Fatalf("no EventModelDelta events in %d events, want streamed text", len(events))
	}
}
