package ai_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
)

// spendingTool stands in for a tool that calls a model of its own, such as
// the Vector Store Tool, and reports what that call cost.
type spendingTool struct{ spent ai.Usage }

func (tool *spendingTool) Definition() ai.ToolDefinition {
	return ai.ToolDefinition{Name: "ask_docs", Description: "Answers from documents"}
}

func (tool *spendingTool) Invoke(ctx context.Context, _ json.RawMessage) (string, error) {
	ai.RecordToolUsage(ctx, tool.spent)
	return "an answer", nil
}

func TestAgentUsageCountsWhatAToolSpentOnItsOwnModel(t *testing.T) {
	t.Parallel()

	tool := &spendingTool{spent: ai.Usage{PromptTokens: 30, CompletionTokens: 7, TotalTokens: 37}}
	model := &fakeModel{responses: []ai.ModelResponse{
		toolTurn("ask_docs", `{"query":"leave"}`),
		answer("Twenty days."),
	}}
	sink, events := collect()
	result, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: model, ModelName: "test-model", Input: "How much leave?", Tools: []ai.Tool{tool},
	}, sink)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// 10 + 15 from the agent's own two turns, 37 from the tool's model.
	want := ai.Usage{PromptTokens: 8 + 10 + 30, CompletionTokens: 2 + 5 + 7, TotalTokens: 10 + 15 + 37}
	if result.Usage != want {
		t.Errorf("usage = %+v, want %+v: the run cost what its tools spent too", result.Usage, want)
	}
	var completed *ai.Usage
	for _, event := range *events {
		if event.Kind == ai.EventAgentCompleted {
			completed = event.Usage
		}
	}
	if completed == nil || *completed != want {
		t.Errorf("agent.completed usage = %+v, want %+v", completed, want)
	}
}

func TestRecordingToolUsageOutsideAToolCallIsHarmless(t *testing.T) {
	t.Parallel()

	// No agent run is listening: nothing to add to, and nothing to break.
	ai.RecordToolUsage(context.Background(), ai.Usage{TotalTokens: 5})
}
