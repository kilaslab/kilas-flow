// Package maf implements internal/ai.AgentRuntime on top of Microsoft Agent
// Framework for Go v0.1.0 (github.com/microsoft/agent-framework-go, MIT,
// evaluated 2026-09-06).
//
// This is the only package permitted to import that module. The framework is
// in public preview and its API may move; confining it here keeps that churn
// off the workflow contract.
//
// Transport ownership stays with the caller: the framework never dials
// directly. A provider agent is built over a caller-supplied SDK client, e.g.
// openai.NewClient(option.WithHTTPClient(client)) where client is the
// safehttp-derived *http.Client, and the resulting *agent.Agent is handed to
// New. That preserves the SSRF dialer and per-credential domain scoping,
// which live in that transport.
//
// Spike scope, stated plainly: tools, sampling knobs, and memory are
// configured on the *agent.Agent at construction, not translated from
// AgentRequest per run. Mapping ai.Tool to framework tools, ai.Memory to a
// HistoryProvider, and per-request iteration bounds is adoption work, recorded
// on FEAT-ej0468. LoopRuntime remains the default runtime.
package maf

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"

	"github.com/kilaslabs/kilas-flow/internal/ai"
)

// Runtime runs one agent invocation through a prebuilt framework agent.
type Runtime struct {
	agent *agent.Agent
}

var _ ai.AgentRuntime = Runtime{}

// New wraps a caller-built framework agent. The agent carries its provider
// client (and therefore its *http.Client transport), tools, and loop bounds;
// New panics on nil because an agent-less runtime can never succeed.
func New(agt *agent.Agent) Runtime {
	if agt == nil {
		panic("maf: nil *agent.Agent")
	}
	return Runtime{agent: agt}
}

// Run sends the system prompt and input through the framework agent,
// forwarding streamed text to the sink and folding the collected response
// into an AgentResult.
func (r Runtime) Run(ctx context.Context, request ai.AgentRequest, sink ai.EventSink) (ai.AgentResult, error) {
	if err := request.Validate(); err != nil {
		return ai.AgentResult{}, err
	}

	input := message.New(&message.TextContent{Text: request.Input})
	var options []agent.Option
	if request.SystemPrompt != "" {
		options = append(options, agent.WithInstructions(request.SystemPrompt))
	}
	if request.Stream {
		options = append(options, agent.Stream(true))
	}

	sink.Emit(ai.Event{Kind: ai.EventModelStarted, Iteration: 1, Model: request.ModelName})

	var collected agent.Response
	toolCalls := 0
	rounds := 0
	finished := false
	for update, err := range r.agent.Run(ctx, []*message.Message{input}, options...) {
		if err != nil {
			sink.Emit(ai.Event{Kind: ai.EventAgentFailed, Iteration: rounds + 1, Error: err.Error()})
			return ai.AgentResult{}, err
		}
		if update == nil {
			continue
		}
		collected.Update(update)
		for _, content := range update.Contents {
			switch item := content.(type) {
			case *message.TextContent:
				if request.Stream && item.Text != "" {
					sink.Emit(ai.Event{Kind: ai.EventModelDelta, Iteration: rounds + 1, Delta: item.Text})
				}
			case *message.FunctionCallContent:
				toolCalls++
				sink.Emit(ai.Event{Kind: ai.EventToolStarted, Iteration: rounds + 1, Tool: item.Name})
			case *message.FunctionResultContent:
				sink.Emit(ai.Event{Kind: ai.EventToolCompleted, Iteration: rounds + 1})
			}
		}
		if update.FinishReason != "" {
			rounds++
		}
		finished = true
	}
	if !finished {
		err := fmt.Errorf("maf: agent produced no updates")
		sink.Emit(ai.Event{Kind: ai.EventAgentFailed, Iteration: 1, Error: err.Error()})
		return ai.AgentResult{}, err
	}
	if rounds == 0 {
		rounds = 1
	}

	usage := collected.Usage()
	result := ai.AgentResult{
		Output:     collected.String(),
		Messages:   mapMessages(collected.Messages),
		Usage:      ai.Usage{PromptTokens: int(usage.InputTokenCount), CompletionTokens: int(usage.OutputTokenCount), TotalTokens: int(usage.TotalTokenCount)},
		Iterations: rounds,
		ToolCalls:  toolCalls,
	}
	sink.Emit(ai.Event{Kind: ai.EventAgentCompleted, Iteration: rounds, Usage: &result.Usage})
	return result, nil
}

// mapMessages carries the framework's produced messages across the boundary
// as plain role/content turns. Tool-call payloads stay inside the framework;
// only the rendered text crosses, matching what an inspector shows.
func mapMessages(messages []*message.Message) []ai.Message {
	out := make([]ai.Message, 0, len(messages))
	for _, item := range messages {
		if item == nil {
			continue
		}
		var role ai.Role
		switch item.Role {
		case message.RoleUser:
			role = ai.RoleUser
		case message.RoleSystem:
			role = ai.RoleSystem
		case message.RoleTool:
			role = ai.RoleTool
		default:
			role = ai.RoleAssistant
		}
		out = append(out, ai.Message{Role: role, Content: item.String()})
	}
	return out
}
