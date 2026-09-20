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
	// A parser changes only the stop condition: the synthetic tool is defined
	// to the model but never enters the invocable tool map, so answering
	// through it ends the run instead of starting another tool turn.
	outputSchema := request.OutputSchema
	outputRetries := request.OutputMaxRetries
	if outputSchema != nil {
		if outputRetries <= 0 {
			outputRetries = DefaultOutputMaxRetries
		}
		if _, clash := tools[FormatFinalJSONResponse]; clash {
			return AgentResult{}, fmt.Errorf("tool %q is reserved for the output parser", FormatFinalJSONResponse)
		}
		definitions = append(definitions, ToolDefinition{
			Name:        FormatFinalJSONResponse,
			Description: "Respond with the final answer in the requested format.",
			Parameters:  outputSchema,
		})
	}

	messages := make([]Message, 0, 8)
	if prompt := strings.TrimSpace(request.SystemPrompt); prompt != "" {
		messages = append(messages, Message{Role: RoleSystem, Content: prompt})
	}
	// History is loaded after the system prompt so a stored conversation cannot
	// displace the instructions this run was configured with.
	if request.Memory != nil {
		history, err := loadSessionMemory(ctx, request)
		if err != nil {
			return AgentResult{}, fmt.Errorf("load memory: %w", err)
		}
		messages = append(messages, history...)
	}
	userTurn := Message{Role: RoleUser, Content: request.Input, Images: request.Images}
	messages = append(messages, userTurn)

	result := AgentResult{}
	// remembered is what this run contributes to the conversation: the human
	// turn and the final answer, with the tool steps and repair prompts left
	// out. Saving those is what produced histories a provider refuses — an
	// assistant turn asking for the format tool with no result answering it,
	// and a window whose first message was a tool result.
	remembered := []Message{userTurn}
	// outputAttempts counts parser answers that failed schema validation,
	// across both format-tool calls and plain-text fallbacks.
	outputAttempts := 0

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
			Timeout: request.RequestTimeout,
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

		if len(assistant.ToolCalls) == 0 {
			if outputSchema == nil {
				result.Output = assistant.Content
				result.Messages = messages
				remembered = append(remembered, assistant)
				if err := appendSessionMemory(ctx, request, remembered); err != nil {
					return result, err
				}
				sink.Emit(Event{Kind: EventAgentCompleted, Iteration: iteration, Model: request.ModelName, Usage: &result.Usage})
				return result, nil
			}
			// A smaller model answered in plain text without calling the
			// format tool. Validating the text is the first retry step: it
			// costs nothing when the model merely forgot the wrapper and
			// saves a whole extra model call.
			value, validationErr := ParseAndValidateOutput(outputSchema, assistant.Content)
			if validationErr == nil {
				return finishStructuredRun(ctx, request, sink, &result, messages, remembered, iteration, value)
			}
			outputAttempts++
			if outputAttempts > outputRetries {
				return failStructuredRun(ctx, request, sink, &result, messages, iteration, validationErr)
			}
			repairTurn := Message{Role: RoleUser, Content: "That response did not match the required format: " + validationErr.Error() + ". Reply by calling " + FormatFinalJSONResponse + " with the corrected arguments."}
			messages = append(messages, repairTurn)
			continue
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

			// The format tool is defined to the model but never invocable
			// through the tool map: calling it is the final answer, not
			// another turn.
			if outputSchema != nil && call.Name == FormatFinalJSONResponse {
				value, validationErr := parseFormatArguments(outputSchema, call.Arguments)
				if validationErr == nil {
					sink.Emit(Event{
						Kind: EventToolCompleted, Iteration: iteration, Tool: call.Name,
						Detail: call.Arguments,
					})
					return finishStructuredRun(ctx, request, sink, &result, messages, remembered, iteration, value)
				}
				sink.Emit(Event{Kind: EventToolFailed, Iteration: iteration, Tool: call.Name, Error: validationErr.Error()})
				outputAttempts++
				if outputAttempts > outputRetries {
					return failStructuredRun(ctx, request, sink, &result, messages, iteration, validationErr)
				}
				toolTurn := Message{Role: RoleTool, ToolCallID: call.ID, Name: call.Name, Content: "Those arguments did not match the required format: " + validationErr.Error() + ". Call " + FormatFinalJSONResponse + " again with corrected arguments."}
				messages = append(messages, toolTurn)
				continue
			}
			tool, found := tools[call.Name]
			if !found {
				// Telling the model beats failing the run: a model that invents
				// a tool name can usually recover when told the name is wrong.
				message := fmt.Sprintf("tool %q is not available", call.Name)
				sink.Emit(Event{Kind: EventToolFailed, Iteration: iteration, Tool: call.Name, Error: message})
				toolTurn := Message{Role: RoleTool, ToolCallID: call.ID, Name: call.Name, Content: message}
				messages = append(messages, toolTurn)
				continue
			}

			output, err := tool.Invoke(ctx, call.Arguments)
			if err != nil {
				message := "tool failed: " + err.Error()
				sink.Emit(Event{Kind: EventToolFailed, Iteration: iteration, Tool: call.Name, Error: err.Error()})
				toolTurn := Message{Role: RoleTool, ToolCallID: call.ID, Name: call.Name, Content: message}
				messages = append(messages, toolTurn)
				continue
			}
			sink.Emit(Event{
				Kind: EventToolCompleted, Iteration: iteration, Tool: call.Name,
				Detail: jsonString(output),
			})
			toolTurn := Message{Role: RoleTool, ToolCallID: call.ID, Name: call.Name, Content: output}
			messages = append(messages, toolTurn)
		}
	}

	// Reaching the bound is a real outcome, not a crash: a model that keeps
	// asking for tools is answered the way n8n answers it, so the workflow
	// continues with a stated fallback instead of failing and taking the chat
	// reply with it. The partial conversation is still returned for an
	// inspector.
	result.Output = MaxIterationsMessage
	// The flag travels with the fallback: only the runtime knows the bound was
	// reached, and the caller must not have to recognise the sentence.
	result.MaxIterationsReached = true
	result.Messages = messages
	remembered = append(remembered, Message{Role: RoleAssistant, Content: result.Output})
	if err := appendSessionMemory(ctx, request, remembered); err != nil {
		return result, err
	}
	sink.Emit(Event{Kind: EventAgentCompleted, Iteration: maxIterations, Model: request.ModelName, Usage: &result.Usage})
	return result, nil
}

// MaxIterationsMessage is what an agent that cannot converge answers with, in
// n8n's own words, so a workflow reading the output sees the same string it
// would see there.
const MaxIterationsMessage = "Agent stopped due to max iterations."

// finishStructuredRun ends a parser run with the validated answer. Output is
// the canonical JSON of the arguments, so the node reading the result
// carries the parsed object rather than a JSON string.
//
// What is remembered is the human turn and this answer, never the assistant
// turn that called the format tool: that call has no result answering it, and
// a provider that enforces message order rejects the history it would produce
// the next time the session is used.
func finishStructuredRun(ctx context.Context, request AgentRequest, sink EventSink, result *AgentResult, messages []Message, remembered []Message, iteration int, value any) (AgentResult, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return *result, err
	}
	result.Output = string(encoded)
	result.Messages = messages
	remembered = append(remembered, Message{Role: RoleAssistant, Content: result.Output})
	if err := appendSessionMemory(ctx, request, remembered); err != nil {
		return *result, err
	}
	sink.Emit(Event{Kind: EventAgentCompleted, Iteration: iteration, Model: request.ModelName, Usage: &result.Usage})
	return *result, nil
}

// failStructuredRun fails a parser run that exhausted its retries, carrying
// both the validation error and the raw text so the failure is diagnosable.
func failStructuredRun(_ context.Context, _ AgentRequest, sink EventSink, result *AgentResult, messages []Message, iteration int, validationErr error) (AgentResult, error) {
	err := fmt.Errorf("output did not match the required format: %w", validationErr)
	sink.Emit(Event{Kind: EventAgentFailed, Iteration: iteration, Error: err.Error()})
	result.Messages = messages
	return *result, err
}

// parseFormatArguments decodes one format-tool call's arguments and
// validates them against the schema.
func parseFormatArguments(schema map[string]any, arguments json.RawMessage) (any, error) {
	raw := strings.TrimSpace(string(arguments))
	if raw == "" {
		return nil, fmt.Errorf("output is empty; raw output: %s", string(arguments))
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, fmt.Errorf("output is not valid JSON: %w; raw output: %s", err, string(arguments))
	}
	if err := ValidateValue(schema, value, "output"); err != nil {
		return nil, fmt.Errorf("%w; raw output: %s", err, string(arguments))
	}
	return value, nil
}

// PolicyMemory is a Memory that enforces per-session retention bounds handed
// to it with each call. A store implementing only Memory keeps its own
// constructor bounds; the loop prefers the policy form when it is there so
// two memory nodes with different bounds retain different amounts.
type PolicyMemory interface {
	Memory
	LoadWithPolicy(ctx context.Context, session SessionKey, retention Retention) ([]Message, error)
	AppendWithPolicy(ctx context.Context, session SessionKey, messages []Message, retention Retention) error
}

func loadSessionMemory(ctx context.Context, request AgentRequest) ([]Message, error) {
	var (
		history []Message
		err     error
	)
	if policy, ok := request.Memory.(PolicyMemory); ok {
		history, err = policy.LoadWithPolicy(ctx, request.Session, request.SessionPolicy)
	} else {
		history, err = request.Memory.Load(ctx, request.Session)
	}
	if err != nil {
		return nil, err
	}
	// Whatever the store kept, a window that opens with a tool result or with
	// an assistant turn asking for tools is not a conversation. Dropping those
	// leading turns is the difference between a history the provider accepts
	// and a 400 the user cannot explain.
	for index, message := range history {
		if message.OpensATurn() {
			return history[index:], nil
		}
	}
	return nil, nil
}

func appendSessionMemory(ctx context.Context, request AgentRequest, turns []Message) error {
	if request.Memory == nil {
		return nil
	}
	// Only turns that can be replayed are stored. A tool result or an
	// assistant turn asking for tools is meaningful only beside the message
	// that introduced it, and a stored picture would be re-sent with every
	// later turn of the conversation.
	remembered := make([]Message, 0, len(turns))
	for _, turn := range turns {
		if turn.Role != RoleUser && turn.Role != RoleAssistant {
			continue
		}
		if turn.Role == RoleAssistant && len(turn.ToolCalls) > 0 {
			continue
		}
		turn.Images = nil
		remembered = append(remembered, turn)
	}
	if len(remembered) == 0 {
		return nil
	}
	if policy, ok := request.Memory.(PolicyMemory); ok {
		if err := policy.AppendWithPolicy(ctx, request.Session, remembered, request.SessionPolicy); err != nil {
			return fmt.Errorf("append memory: %w", err)
		}
		return nil
	}
	if err := request.Memory.Append(ctx, request.Session, remembered); err != nil {
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
