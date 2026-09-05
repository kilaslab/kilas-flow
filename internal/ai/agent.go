package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// LoopRuntime is the built-in AgentRuntime: a deterministic tool loop.
//
// It lives behind AgentRuntime like any other implementation, so replacing it
// with a framework-backed runtime is a composition change rather than an
// engine change.
type LoopRuntime struct{}

var _ AgentRuntime = LoopRuntime{}

// NewLoopRuntime constructs the built-in runtime.
func NewLoopRuntime() LoopRuntime { return LoopRuntime{} }

// Run drives model turns and tool calls until the model answers without asking
// for another tool, or the iteration bound is reached.
func (LoopRuntime) Run(ctx context.Context, request AgentRequest, sink EventSink) (AgentResult, error) {
	if err := request.Validate(); err != nil {
		return AgentResult{}, err
	}
	maxIterations := request.MaxIterations
	if maxIterations <= 0 {
		maxIterations = DefaultMaxIterations
	}

	tools := make(map[string]Tool, len(request.Tools))
	definitions := make([]ToolDefinition, 0, len(request.Tools))
	for _, tool := range request.Tools {
		definition := tool.Definition()
		if definition.Name == "" {
			return AgentResult{}, fmt.Errorf("every tool needs a name")
		}
		if _, duplicate := tools[definition.Name]; duplicate {
			// Two tools with one name make the model's choice ambiguous, and
			// silently picking one would be worse than refusing.
			return AgentResult{}, fmt.Errorf("tool %q is attached more than once", definition.Name)
		}
		tools[definition.Name] = tool
		definitions = append(definitions, definition)
	}

	messages := make([]Message, 0, 8)
	if prompt := strings.TrimSpace(request.SystemPrompt); prompt != "" {
		messages = append(messages, Message{Role: RoleSystem, Content: prompt})
	}
	// History is loaded after the system prompt so a stored conversation cannot
	// displace the instructions this run was configured with.
	if request.Memory != nil {
		history, err := request.Memory.Load(ctx, request.Session)
		if err != nil {
			return AgentResult{}, fmt.Errorf("load memory: %w", err)
		}
		messages = append(messages, history...)
	}
	userTurn := Message{Role: RoleUser, Content: request.Input}
	messages = append(messages, userTurn)

	result := AgentResult{}
	// newTurns records only what this run produced, so memory does not
	// re-append the history it just loaded.
	newTurns := []Message{userTurn}

	for iteration := 1; iteration <= maxIterations; iteration++ {
		if err := ctx.Err(); err != nil {
			sink.Emit(Event{Kind: EventAgentFailed, Iteration: iteration, Error: err.Error()})
			return result, err
		}
		result.Iterations = iteration

		modelRequest := ModelRequest{
			Model: request.ModelName, Messages: messages, Tools: definitions,
			Temperature: request.Temperature, TopP: request.TopP,
			FrequencyPenalty: request.FrequencyPenalty, PresencePenalty: request.PresencePenalty,
			MaxTokens: request.MaxTokens, MaxRetries: request.MaxRetries,
		}
		sink.Emit(Event{Kind: EventModelStarted, Iteration: iteration, Model: request.ModelName})

		var response ModelResponse
		var err error
		if request.Stream {
			response, err = request.Model.Stream(ctx, modelRequest, func(delta string) {
				if delta == "" {
					return
				}
				sink.Emit(Event{Kind: EventModelDelta, Iteration: iteration, Model: request.ModelName, Delta: delta})
			})
		} else {
			response, err = request.Model.Complete(ctx, modelRequest)
		}
		if err != nil {
			sink.Emit(Event{Kind: EventAgentFailed, Iteration: iteration, Model: request.ModelName, Error: err.Error()})
			return result, fmt.Errorf("model turn %d: %w", iteration, err)
		}

		result.Usage.Add(response.Usage)
		usage := response.Usage
		sink.Emit(Event{
			Kind: EventModelCompleted, Iteration: iteration, Model: request.ModelName,
			Usage: &usage,
		})

		assistant := response.Message
		assistant.Role = RoleAssistant
		messages = append(messages, assistant)
		newTurns = append(newTurns, assistant)

		if len(assistant.ToolCalls) == 0 {
			result.Output = assistant.Content
			if err := appendMemory(ctx, request, newTurns); err != nil {
				return result, err
			}
			sink.Emit(Event{Kind: EventAgentCompleted, Iteration: iteration, Model: request.ModelName, Usage: &result.Usage})
			return result, nil
		}

		for _, call := range assistant.ToolCalls {
			if err := ctx.Err(); err != nil {
				sink.Emit(Event{Kind: EventAgentFailed, Iteration: iteration, Error: err.Error()})
				return result, err
			}
			result.ToolCalls++
			sink.Emit(Event{
				Kind: EventToolStarted, Iteration: iteration, Tool: call.Name, Detail: call.Arguments,
			})

			tool, found := tools[call.Name]
			if !found {
				// Telling the model beats failing the run: a model that invents
				// a tool name can usually recover when told the name is wrong.
				message := fmt.Sprintf("tool %q is not available", call.Name)
				sink.Emit(Event{Kind: EventToolFailed, Iteration: iteration, Tool: call.Name, Error: message})
				toolTurn := Message{Role: RoleTool, ToolCallID: call.ID, Name: call.Name, Content: message}
				messages = append(messages, toolTurn)
				newTurns = append(newTurns, toolTurn)
				continue
			}

			output, err := tool.Invoke(ctx, call.Arguments)
			if err != nil {
				message := "tool failed: " + err.Error()
				sink.Emit(Event{Kind: EventToolFailed, Iteration: iteration, Tool: call.Name, Error: err.Error()})
				toolTurn := Message{Role: RoleTool, ToolCallID: call.ID, Name: call.Name, Content: message}
				messages = append(messages, toolTurn)
				newTurns = append(newTurns, toolTurn)
				continue
			}
			sink.Emit(Event{
				Kind: EventToolCompleted, Iteration: iteration, Tool: call.Name,
				Detail: jsonString(output),
			})
			toolTurn := Message{Role: RoleTool, ToolCallID: call.ID, Name: call.Name, Content: output}
			messages = append(messages, toolTurn)
			newTurns = append(newTurns, toolTurn)
		}
	}

	// Reaching the bound is a real outcome, not a crash: the partial
	// conversation is still returned so an inspector can show what happened.
	err := fmt.Errorf("agent stopped after %d iterations without a final answer", maxIterations)
	sink.Emit(Event{Kind: EventAgentFailed, Iteration: maxIterations, Error: err.Error()})
	result.Messages = messages
	return result, err
}

func appendMemory(ctx context.Context, request AgentRequest, turns []Message) error {
	if request.Memory == nil {
		return nil
	}
	if err := request.Memory.Append(ctx, request.Session, turns); err != nil {
		return fmt.Errorf("append memory: %w", err)
	}
	return nil
}

func jsonString(value string) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return encoded
}
