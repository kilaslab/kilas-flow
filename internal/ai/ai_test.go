package ai_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/ai"
)

// fakeModel is the deterministic stand-in every agent test uses. No live
// provider credential is required anywhere in this package.
type fakeModel struct {
	mu        sync.Mutex
	responses []ai.ModelResponse
	err       error
	// requests records what the loop actually sent, which is how the tests
	// assert on conversation assembly rather than on internal state.
	requests []ai.ModelRequest
	chunks   []string
}

func (model *fakeModel) Complete(_ context.Context, request ai.ModelRequest) (ai.ModelResponse, error) {
	model.mu.Lock()
	defer model.mu.Unlock()
	model.requests = append(model.requests, request)
	if model.err != nil {
		return ai.ModelResponse{}, model.err
	}
	if len(model.responses) == 0 {
		return ai.ModelResponse{}, errors.New("fake model ran out of responses")
	}
	response := model.responses[0]
	model.responses = model.responses[1:]
	return response, nil
}

func (model *fakeModel) Stream(ctx context.Context, request ai.ModelRequest, onChunk func(string)) (ai.ModelResponse, error) {
	for _, chunk := range model.chunks {
		if onChunk != nil {
			onChunk(chunk)
		}
	}
	return model.Complete(ctx, request)
}

type fakeTool struct {
	name        string
	description string
	result      string
	err         error
	calls       []string
}

func (tool *fakeTool) Definition() ai.ToolDefinition {
	return ai.ToolDefinition{Name: tool.name, Description: tool.description}
}

func (tool *fakeTool) Invoke(_ context.Context, arguments json.RawMessage) (string, error) {
	tool.calls = append(tool.calls, string(arguments))
	if tool.err != nil {
		return "", tool.err
	}
	return tool.result, nil
}

func answer(content string) ai.ModelResponse {
	return ai.ModelResponse{
		Message: ai.Message{Role: ai.RoleAssistant, Content: content},
		Usage:   ai.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
}

func toolTurn(name, arguments string) ai.ModelResponse {
	return ai.ModelResponse{
		Message: ai.Message{Role: ai.RoleAssistant, ToolCalls: []ai.ToolCall{
			{ID: "call-1", Name: name, Arguments: json.RawMessage(arguments)},
		}},
		Usage: ai.Usage{PromptTokens: 8, CompletionTokens: 2, TotalTokens: 10},
	}
}

func collect() (ai.EventSink, *[]ai.Event) {
	events := &[]ai.Event{}
	return func(event ai.Event) { *events = append(*events, event) }, events
}

func TestAgentAnswersWithoutTools(t *testing.T) {
	t.Parallel()

	model := &fakeModel{responses: []ai.ModelResponse{answer("Hello, Ada.")}}
	sink, events := collect()

	result, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: model, ModelName: "test-model", SystemPrompt: "Be brief.", Input: "Say hello",
	}, sink)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "Hello, Ada." || result.Iterations != 1 || result.ToolCalls != 0 {
		t.Fatalf("result = %#v, want a single-turn answer", result)
	}
	if result.Usage.TotalTokens != 15 {
		t.Errorf("usage = %#v, want the provider's tokens carried through", result.Usage)
	}

	// The system prompt must lead, so stored history can never displace it.
	sent := model.requests[0].Messages
	if sent[0].Role != ai.RoleSystem || sent[0].Content != "Be brief." {
		t.Errorf("first message = %#v, want the system prompt", sent[0])
	}
	if sent[len(sent)-1].Role != ai.RoleUser || sent[len(sent)-1].Content != "Say hello" {
		t.Errorf("last message = %#v, want the user turn", sent[len(sent)-1])
	}

	kinds := eventKinds(*events)
	if !contains(kinds, ai.EventModelStarted) || !contains(kinds, ai.EventModelCompleted) || !contains(kinds, ai.EventAgentCompleted) {
		t.Errorf("events = %v, want model and agent lifecycle events", kinds)
	}
}

func TestAgentRunsTheToolLoopAndFeedsResultsBack(t *testing.T) {
	t.Parallel()

	weather := &fakeTool{name: "get_weather", description: "Weather by city", result: `{"tempC":19}`}
	model := &fakeModel{responses: []ai.ModelResponse{
		toolTurn("get_weather", `{"city":"Utrecht"}`),
		answer("It is 19°C in Utrecht."),
	}}
	sink, events := collect()

	result, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: model, ModelName: "test-model", Input: "What is the weather?", Tools: []ai.Tool{weather},
	}, sink)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "It is 19°C in Utrecht." || result.Iterations != 2 || result.ToolCalls != 1 {
		t.Fatalf("result = %#v, want two turns and one tool call", result)
	}
	if len(weather.calls) != 1 || !strings.Contains(weather.calls[0], "Utrecht") {
		t.Fatalf("tool calls = %#v, want the model's arguments passed through", weather.calls)
	}
	// Usage accumulates across every turn, not just the last one.
	if result.Usage.TotalTokens != 25 {
		t.Errorf("usage = %#v, want both turns accumulated", result.Usage)
	}

	// The second model turn must see the tool result.
	second := model.requests[1].Messages
	last := second[len(second)-1]
	if last.Role != ai.RoleTool || last.Content != `{"tempC":19}` || last.ToolCallID != "call-1" {
		t.Fatalf("tool turn = %#v, want the result tied to its call", last)
	}
	// The tool definition must reach the model, or it could never call it.
	if len(model.requests[0].Tools) != 1 || model.requests[0].Tools[0].Name != "get_weather" {
		t.Errorf("tools sent = %#v, want the attached tool", model.requests[0].Tools)
	}

	kinds := eventKinds(*events)
	if !contains(kinds, ai.EventToolStarted) || !contains(kinds, ai.EventToolCompleted) {
		t.Errorf("events = %v, want nested tool events", kinds)
	}
}

func TestAgentTellsTheModelWhenAToolFailsInsteadOfFailingTheRun(t *testing.T) {
	t.Parallel()

	broken := &fakeTool{name: "lookup", err: errors.New("upstream is down")}
	model := &fakeModel{responses: []ai.ModelResponse{
		toolTurn("lookup", `{}`),
		answer("I could not reach the service."),
	}}
	sink, events := collect()

	result, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: model, ModelName: "test-model", Input: "Look it up", Tools: []ai.Tool{broken},
	}, sink)
	if err != nil {
		t.Fatalf("Run() error = %v, want the run to recover", err)
	}
	if result.Output != "I could not reach the service." {
		t.Errorf("output = %q, want the model's recovery", result.Output)
	}

	second := model.requests[1].Messages
	if last := second[len(second)-1]; !strings.Contains(last.Content, "upstream is down") {
		t.Errorf("tool turn = %#v, want the failure reported to the model", last)
	}
	if !contains(eventKinds(*events), ai.EventToolFailed) {
		t.Error("a failing tool emitted no failure event")
	}
}

func TestAgentReportsAnUnknownToolToTheModel(t *testing.T) {
	t.Parallel()

	model := &fakeModel{responses: []ai.ModelResponse{
		toolTurn("no_such_tool", `{}`),
		answer("Understood."),
	}}
	sink, _ := collect()

	if _, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: model, ModelName: "test-model", Input: "Go",
	}, sink); err != nil {
		t.Fatalf("Run() error = %v, want the run to recover", err)
	}
	second := model.requests[1].Messages
	if last := second[len(second)-1]; !strings.Contains(last.Content, "not available") {
		t.Errorf("tool turn = %#v, want the unknown tool reported", last)
	}
}

func TestAgentStopsAtTheIterationBound(t *testing.T) {
	t.Parallel()

	looping := &fakeTool{name: "spin", result: "again"}
	responses := make([]ai.ModelResponse, 0, 10)
	for range 10 {
		responses = append(responses, toolTurn("spin", `{}`))
	}
	model := &fakeModel{responses: responses}
	sink, events := collect()

	// A model that keeps asking for tools would otherwise run until the whole
	// execution times out. n8n answers with a fallback and lets the workflow
	// continue, so a flaky tool does not take the chat reply with it.
	result, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: model, ModelName: "test-model", Input: "Spin", Tools: []ai.Tool{looping}, MaxIterations: 3,
	}, sink)
	if err != nil {
		t.Fatalf("hitting the bound failed the run: %v", err)
	}
	if result.Output != ai.MaxIterationsMessage {
		t.Errorf("output = %q, want n8n's max-iterations answer", result.Output)
	}
	if result.Iterations != 3 || len(result.Messages) == 0 {
		t.Errorf("result = %#v, want the partial run reported", result)
	}
	if !contains(eventKinds(*events), ai.EventAgentCompleted) {
		t.Error("hitting the bound did not report the run completed")
	}
}

func TestMaxIterationsAnswerIsRememberedAsTheReply(t *testing.T) {
	t.Parallel()

	looping := &fakeTool{name: "spin", result: "again"}
	responses := make([]ai.ModelResponse, 0, 4)
	for range 4 {
		responses = append(responses, toolTurn("spin", `{}`))
	}
	memory, err := ai.NewBufferMemory(ai.Retention{}, nil)
	if err != nil {
		t.Fatalf("NewBufferMemory() error = %v", err)
	}
	session := ai.SessionKey{TenantID: "t", WorkflowID: "w", SessionID: "s"}
	if _, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: &fakeModel{responses: responses}, ModelName: "m", Input: "Spin",
		Tools: []ai.Tool{looping}, MaxIterations: 2, Memory: memory, Session: session,
	}, nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// The stored conversation is the question and the fallback answer, so the
	// next turn is a history the provider accepts.
	loaded, err := memory.Load(context.Background(), session)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded) != 2 || loaded[0].Role != ai.RoleUser || loaded[1].Content != ai.MaxIterationsMessage {
		t.Fatalf("loaded = %#v, want the question and the fallback answer", loaded)
	}
}

func TestMemoryWindowNeverOpensWithAToolStep(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	memory, err := ai.NewBufferMemory(ai.Retention{MaxMessages: 3, MaxAge: time.Hour}, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewBufferMemory() error = %v", err)
	}
	session := ai.SessionKey{TenantID: "t", WorkflowID: "w", SessionID: "s"}
	ctx := context.Background()

	// A tool turn is four messages: the question, the assistant turn asking
	// for the tool, its result, and the answer. A count-based trim of the
	// newest three lands on the tool result.
	if err := memory.Append(ctx, session, []ai.Message{
		{Role: ai.RoleUser, Content: "what is 2+2"},
		{Role: ai.RoleAssistant, ToolCalls: []ai.ToolCall{{ID: "call_1", Name: "calc", Arguments: json.RawMessage(`{"expression":"2+2"}`)}}},
		{Role: ai.RoleTool, ToolCallID: "call_1", Name: "calc", Content: "4"},
		{Role: ai.RoleAssistant, Content: "4"},
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	loaded, err := memory.Load(ctx, session)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded) == 0 {
		t.Fatal("the window was emptied rather than moved to a turn boundary")
	}
	// A window that opens with a tool result, or with an assistant turn whose
	// calls were cut off, is rejected by providers that enforce message order:
	// the conversation fails with a 400 at a turn nobody can explain.
	for _, message := range loaded {
		if message.OpensATurn() {
			return
		}
	}
	t.Fatalf("window = %#v, want it to open with a message that can start a turn", loaded)
}

func TestStructuredRunLeavesNoUnansweredToolCallInMemory(t *testing.T) {
	t.Parallel()

	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"answer": map[string]any{"type": "string"}},
		"required":   []any{"answer"},
	}
	memory, err := ai.NewBufferMemory(ai.Retention{}, nil)
	if err != nil {
		t.Fatalf("NewBufferMemory() error = %v", err)
	}
	session := ai.SessionKey{TenantID: "t", WorkflowID: "w", SessionID: "s"}

	first := &fakeModel{responses: []ai.ModelResponse{toolTurn(ai.FormatFinalJSONResponse, `{"answer":"ok"}`)}}
	if _, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: first, ModelName: "m", Input: "extract", Memory: memory, Session: session, OutputSchema: schema,
	}, nil); err != nil {
		t.Fatalf("first run error = %v", err)
	}

	// The second turn in the same session must send a history whose tool calls
	// are all answered: the format tool's call is how the first run ended, and
	// storing it replayed an assistant turn with no result.
	second := &fakeModel{responses: []ai.ModelResponse{answer(`{"answer":"ok"}`)}}
	if _, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: second, ModelName: "m", Input: "extract again", Memory: memory, Session: session, OutputSchema: schema,
	}, nil); err != nil {
		t.Fatalf("second run error = %v", err)
	}
	for index, message := range second.requests[0].Messages {
		if len(message.ToolCalls) > 0 {
			t.Fatalf("history message %d asks for tools (%#v) that nothing answers", index, message.ToolCalls)
		}
		if message.Role == ai.RoleTool {
			t.Fatalf("history message %d is a tool result whose call was cut off", index)
		}
	}
	if len(second.requests[0].Messages) != 3 {
		t.Fatalf("history = %#v, want the question, the answer, and the new turn", second.requests[0].Messages)
	}
}

func TestAgentStreamsIncrementalContent(t *testing.T) {
	t.Parallel()

	model := &fakeModel{
		responses: []ai.ModelResponse{answer("Hello, Ada.")},
		chunks:    []string{"Hello, ", "Ada."},
	}
	sink, events := collect()

	if _, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: model, ModelName: "test-model", Input: "Say hello", Stream: true,
	}, sink); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var streamed strings.Builder
	for _, event := range *events {
		if event.Kind == ai.EventModelDelta {
			streamed.WriteString(event.Delta)
		}
	}
	if streamed.String() != "Hello, Ada." {
		t.Errorf("streamed = %q, want the assembled deltas", streamed.String())
	}
}

func TestAgentUsesAndUpdatesMemory(t *testing.T) {
	t.Parallel()

	memory, err := ai.NewBufferMemory(ai.Retention{}, nil)
	if err != nil {
		t.Fatalf("NewBufferMemory() error = %v", err)
	}
	session := ai.SessionKey{TenantID: "t", WorkflowID: "w", SessionID: "chat-1"}

	first := &fakeModel{responses: []ai.ModelResponse{answer("Hello, Ada.")}}
	if _, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: first, ModelName: "m", Input: "I am Ada", Memory: memory, Session: session,
	}, nil); err != nil {
		t.Fatalf("first run error = %v", err)
	}

	second := &fakeModel{responses: []ai.ModelResponse{answer("You are Ada.")}}
	if _, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: second, ModelName: "m", Input: "Who am I?", Memory: memory, Session: session,
	}, nil); err != nil {
		t.Fatalf("second run error = %v", err)
	}

	// The second run must see the first conversation, and must not duplicate it.
	sent := second.requests[0].Messages
	if len(sent) != 3 {
		t.Fatalf("second run sent %d messages, want history plus the new turn: %#v", len(sent), sent)
	}
	if sent[0].Content != "I am Ada" || sent[1].Content != "Hello, Ada." || sent[2].Content != "Who am I?" {
		t.Fatalf("conversation = %#v, want history then the new turn", sent)
	}
}

func TestMemoryIsScopedByTenantAndWorkflow(t *testing.T) {
	t.Parallel()

	memory, _ := ai.NewBufferMemory(ai.Retention{}, nil)
	mine := ai.SessionKey{TenantID: "tenant-a", WorkflowID: "wf-1", SessionID: "chat"}
	theirs := ai.SessionKey{TenantID: "tenant-b", WorkflowID: "wf-1", SessionID: "chat"}

	if err := memory.Append(context.Background(), mine, []ai.Message{{Role: ai.RoleUser, Content: "private"}}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	// Guessing the session ID must not reach another tenant's conversation.
	loaded, err := memory.Load(context.Background(), theirs)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("another tenant loaded %#v", loaded)
	}
}

func TestMemoryEnforcesItsRetentionContract(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	memory, err := ai.NewBufferMemory(ai.Retention{MaxMessages: 3, MaxAge: time.Hour}, clock)
	if err != nil {
		t.Fatalf("NewBufferMemory() error = %v", err)
	}
	session := ai.SessionKey{TenantID: "t", WorkflowID: "w", SessionID: "s"}

	for index := range 5 {
		if err := memory.Append(context.Background(), session,
			[]ai.Message{{Role: ai.RoleUser, Content: fmt.Sprintf("m%d", index)}}); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	loaded, _ := memory.Load(context.Background(), session)
	if len(loaded) != 3 || loaded[0].Content != "m2" || loaded[2].Content != "m4" {
		t.Fatalf("loaded = %#v, want the newest three", loaded)
	}

	// A session idle past MaxAge is emptied, not merely trimmed.
	now = now.Add(2 * time.Hour)
	expired, _ := memory.Load(context.Background(), session)
	if len(expired) != 0 {
		t.Fatalf("loaded %#v after the retention window, want nothing", expired)
	}
}

func TestMemoryRejectsNegativeRetentionRatherThanTreatingItAsUnlimited(t *testing.T) {
	t.Parallel()

	if _, err := ai.NewBufferMemory(ai.Retention{MaxMessages: -1}, nil); err == nil {
		t.Error("a negative message bound was accepted")
	}
	if _, err := ai.NewBufferMemory(ai.Retention{MaxAge: -time.Hour}, nil); err == nil {
		t.Error("a negative age bound was accepted")
	}
}

func TestMemoryRequiresAFullyScopedSession(t *testing.T) {
	t.Parallel()

	memory, _ := ai.NewBufferMemory(ai.Retention{}, nil)
	for name, session := range map[string]ai.SessionKey{
		"no tenant":   {WorkflowID: "w", SessionID: "s"},
		"no workflow": {TenantID: "t", SessionID: "s"},
		"no session":  {TenantID: "t", WorkflowID: "w"},
		"blank":       {TenantID: "t", WorkflowID: "w", SessionID: "  "},
	} {
		if _, err := memory.Load(context.Background(), session); err == nil {
			t.Errorf("Load(%s) was accepted", name)
		}
		if err := memory.Append(context.Background(), session, []ai.Message{{Content: "x"}}); err == nil {
			t.Errorf("Append(%s) was accepted", name)
		}
	}
}

func TestAgentRefusesDuplicateToolNames(t *testing.T) {
	t.Parallel()

	model := &fakeModel{responses: []ai.ModelResponse{answer("ok")}}
	_, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: model, ModelName: "m", Input: "go",
		Tools: []ai.Tool{&fakeTool{name: "same"}, &fakeTool{name: "same"}},
	}, nil)
	// Silently picking one would make the model's choice unpredictable.
	if err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("Run() = %v, want a duplicate-tool rejection", err)
	}
}

func TestAgentValidatesItsRequest(t *testing.T) {
	t.Parallel()

	runtime := ai.NewLoopRuntime()
	if _, err := runtime.Run(context.Background(), ai.AgentRequest{Input: "hi"}, nil); err == nil {
		t.Error("a request with no model was accepted")
	}
	if _, err := runtime.Run(context.Background(), ai.AgentRequest{Model: &fakeModel{}}, nil); err == nil {
		t.Error("a request with no input was accepted")
	}
	memory, _ := ai.NewBufferMemory(ai.Retention{}, nil)
	if _, err := runtime.Run(context.Background(), ai.AgentRequest{
		Model: &fakeModel{}, Input: "hi", Memory: memory,
	}, nil); err == nil {
		t.Error("memory without a session key was accepted")
	}
}

func TestAgentStopsWhenCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sink, events := collect()
	_, err := ai.NewLoopRuntime().Run(ctx, ai.AgentRequest{
		Model: &fakeModel{responses: []ai.ModelResponse{answer("never")}}, ModelName: "m", Input: "go",
	}, sink)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if !contains(eventKinds(*events), ai.EventAgentFailed) {
		t.Error("cancellation emitted no failure event")
	}
}

func TestAgentReportsAModelFailure(t *testing.T) {
	t.Parallel()

	sink, events := collect()
	_, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: &fakeModel{err: errors.New("provider unavailable")}, ModelName: "m", Input: "go",
	}, sink)
	if err == nil || !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("Run() error = %v, want the model failure surfaced", err)
	}
	if !contains(eventKinds(*events), ai.EventAgentFailed) {
		t.Error("a model failure emitted no failure event")
	}
}

func eventKinds(events []ai.Event) []ai.EventKind {
	kinds := make([]ai.EventKind, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	return kinds
}

func contains(kinds []ai.EventKind, want ai.EventKind) bool {
	for _, kind := range kinds {
		if kind == want {
			return true
		}
	}
	return false
}

// TestMemoryKeepsDistinctBucketsForDistinctSessions is the second half of the
// redaction fix.
//
// `sessionId` was on the sensitive-key list, so every conversation reached the
// memory node as the same literal "[redacted]" and collapsed into one shared
// bucket. In a multi-tenant product that is one user reading another's chat
// history, not merely a lost feature — so the separation is asserted here as
// well as at the boundary that used to destroy it.
func TestMemoryKeepsDistinctBucketsForDistinctSessions(t *testing.T) {
	memory, err := ai.NewBufferMemory(ai.Retention{}, nil)
	if err != nil {
		t.Fatalf("NewBufferMemory() error = %v", err)
	}
	ctx := context.Background()

	alice := ai.SessionKey{TenantID: "t", WorkflowID: "w", SessionID: "6281111111111@c.us"}
	bob := ai.SessionKey{TenantID: "t", WorkflowID: "w", SessionID: "6282222222222@c.us"}

	if err := memory.Append(ctx, alice, []ai.Message{{Role: ai.RoleUser, Content: "alice's question"}}); err != nil {
		t.Fatalf("Append(alice) error = %v", err)
	}
	if err := memory.Append(ctx, bob, []ai.Message{{Role: ai.RoleUser, Content: "bob's question"}}); err != nil {
		t.Fatalf("Append(bob) error = %v", err)
	}

	aliceHistory, err := memory.Load(ctx, alice)
	if err != nil {
		t.Fatalf("Load(alice) error = %v", err)
	}
	if len(aliceHistory) != 1 || aliceHistory[0].Content != "alice's question" {
		t.Fatalf("alice's history = %#v, want only her own message", aliceHistory)
	}

	// The redaction bug made both sessions the same key, so this is the exact
	// assertion that would have failed.
	if ai.SessionKey(alice) == ai.SessionKey(bob) {
		t.Fatal("two chat identities collapsed onto one session key")
	}
	bobHistory, err := memory.Load(ctx, bob)
	if err != nil {
		t.Fatalf("Load(bob) error = %v", err)
	}
	if len(bobHistory) != 1 || bobHistory[0].Content != "bob's question" {
		t.Errorf("bob's history = %#v, want only his own message", bobHistory)
	}
}

func TestAgentReturnsMessagesOnTheSuccessPath(t *testing.T) {
	t.Parallel()

	model := &fakeModel{responses: []ai.ModelResponse{
		toolTurn("get_weather", `{"city":"Utrecht"}`),
		answer("It is 19C."),
	}}
	weather := &fakeTool{name: "get_weather", result: `{"tempC":19}`}

	result, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: model, ModelName: "m", SystemPrompt: "Be brief.", Input: "Weather?",
		Tools: []ai.Tool{weather},
	}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// returnIntermediateSteps has nothing to return unless the success path
	// carries the conversation too.
	if len(result.Messages) != 5 {
		t.Fatalf("Messages = %#v, want system, user, assistant, tool, assistant", result.Messages)
	}
	roles := []ai.Role{
		ai.RoleSystem, ai.RoleUser, ai.RoleAssistant, ai.RoleTool, ai.RoleAssistant,
	}
	for index, want := range roles {
		if result.Messages[index].Role != want {
			t.Errorf("Messages[%d].Role = %q, want %q", index, result.Messages[index].Role, want)
		}
	}
	if result.Messages[3].Name != "get_weather" {
		t.Errorf("tool turn = %#v, want the tool result attributed", result.Messages[3])
	}
}

func TestAgentAppliesTheSessionPolicyItWasGiven(t *testing.T) {
	t.Parallel()

	memory, err := ai.NewBufferMemory(ai.Retention{}, nil)
	if err != nil {
		t.Fatalf("NewBufferMemory() error = %v", err)
	}
	session := ai.SessionKey{TenantID: "t", WorkflowID: "w", SessionID: "s"}

	// Two nodes in one workflow declare different bounds; the store default
	// is wider than either, so whatever survives came from the policy.
	model := &fakeModel{responses: []ai.ModelResponse{answer("ok")}}
	if _, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: model, ModelName: "m", Input: "remember this", Memory: memory, Session: session,
		SessionPolicy: ai.Retention{MaxMessages: 2, MaxAge: time.Hour},
	}, nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	loaded, err := memory.LoadWithPolicy(context.Background(), session, ai.Retention{MaxMessages: 2, MaxAge: time.Hour})
	if err != nil {
		t.Fatalf("LoadWithPolicy() error = %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded %d messages, want the policy's two", len(loaded))
	}
	// The same conversation under a wider policy keeps everything the run
	// stored.
	wide, err := memory.LoadWithPolicy(context.Background(), session, ai.Retention{MaxMessages: 40, MaxAge: 24 * time.Hour})
	if err != nil {
		t.Fatalf("LoadWithPolicy() error = %v", err)
	}
	if len(wide) != 2 {
		t.Fatalf("loaded %d messages under the wide policy, want both turns", len(wide))
	}
}

func TestMemoryEnforcesPerNodeAgeThenCount(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	memory, err := ai.NewBufferMemory(ai.Retention{}, clock)
	if err != nil {
		t.Fatalf("NewBufferMemory() error = %v", err)
	}
	session := ai.SessionKey{TenantID: "t", WorkflowID: "w", SessionID: "s"}
	ctx := context.Background()

	for index := range 5 {
		if err := memory.AppendWithPolicy(ctx, session,
			[]ai.Message{{Role: ai.RoleUser, Content: fmt.Sprintf("m%d", index)}},
			ai.Retention{MaxMessages: 3, MaxAge: time.Hour}); err != nil {
			t.Fatalf("AppendWithPolicy() error = %v", err)
		}
	}
	loaded, _ := memory.LoadWithPolicy(ctx, session, ai.Retention{MaxMessages: 3, MaxAge: time.Hour})
	if len(loaded) != 3 || loaded[0].Content != "m2" {
		t.Fatalf("loaded = %#v, want the newest three", loaded)
	}
	// Age applies before count: a long-idle session is emptied, not trimmed.
	now = now.Add(2 * time.Hour)
	expired, _ := memory.LoadWithPolicy(ctx, session, ai.Retention{MaxMessages: 3, MaxAge: time.Hour})
	if len(expired) != 0 {
		t.Fatalf("loaded %#v after the window, want nothing", expired)
	}
}

func TestPerTenantCeilingEvictsTheOldestSessionFirst(t *testing.T) {
	t.Parallel()

	memory, err := ai.NewBufferMemory(ai.Retention{}, nil, ai.WithPerTenantSessionLimit(2))
	if err != nil {
		t.Fatalf("NewBufferMemory() error = %v", err)
	}
	ctx := context.Background()
	say := func(tenant, session, content string) {
		t.Helper()
		key := ai.SessionKey{TenantID: tenant, WorkflowID: "w", SessionID: session}
		if err := memory.Append(ctx, key, []ai.Message{{Role: ai.RoleUser, Content: content}}); err != nil {
			t.Fatalf("Append(%s/%s) error = %v", tenant, session, err)
		}
	}

	say("tenant-a", "one", "first")
	say("tenant-a", "two", "second")
	say("tenant-b", "one", "unrelated tenant keeps its own")
	say("tenant-a", "three", "third")

	stats := memory.Stats()
	if stats.Sessions != 3 {
		t.Fatalf("Stats().Sessions = %d, want 3", stats.Sessions)
	}
	if stats.PerTenant["tenant-a"] != 2 || stats.PerTenant["tenant-b"] != 1 {
		t.Fatalf("Stats().PerTenant = %#v, want the ceiling applied per tenant", stats.PerTenant)
	}
	evicted, _ := memory.Load(ctx, ai.SessionKey{TenantID: "tenant-a", WorkflowID: "w", SessionID: "one"})
	if len(evicted) != 0 {
		t.Fatalf("oldest session survived the ceiling: %#v", evicted)
	}
	kept, _ := memory.Load(ctx, ai.SessionKey{TenantID: "tenant-a", WorkflowID: "w", SessionID: "three"})
	if len(kept) != 1 {
		t.Fatalf("newest session = %#v, want it retained", kept)
	}
}

func TestForgetWorkflowAndTenantDropTheirSessions(t *testing.T) {
	t.Parallel()

	memory, _ := ai.NewBufferMemory(ai.Retention{}, nil)
	ctx := context.Background()
	appendTo := func(tenant, workflow, session string) {
		t.Helper()
		key := ai.SessionKey{TenantID: tenant, WorkflowID: workflow, SessionID: session}
		if err := memory.Append(ctx, key, []ai.Message{{Role: ai.RoleUser, Content: "hi"}}); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	appendTo("t", "gone", "s1")
	appendTo("t", "gone", "s2")
	appendTo("t", "kept", "s1")

	memory.ForgetWorkflow("t", "gone")
	if stats := memory.Stats(); stats.Sessions != 1 {
		t.Fatalf("Stats().Sessions = %d after ForgetWorkflow, want 1", stats.Sessions)
	}

	memory.ForgetTenant("t")
	if stats := memory.Stats(); stats.Sessions != 0 {
		t.Fatalf("Stats().Sessions = %d after ForgetTenant, want 0", stats.Sessions)
	}
}
