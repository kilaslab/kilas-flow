package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OpenAICompatible is a ChatModel over the OpenAI chat-completions API shape.
//
// It is an adapter: nothing above ChatModel knows this type exists, so a
// framework-backed runtime can replace it without touching the engine, the
// registry, or workflow JSON.
type OpenAICompatible struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

var _ ChatModel = (*OpenAICompatible)(nil)

// NewOpenAICompatible builds the adapter.
//
// The HTTP client is supplied rather than constructed so the deployment's
// outbound policy — including the SSRF guard — applies to model calls exactly
// as it does to an HTTP Request node.
func NewOpenAICompatible(client *http.Client, baseURL, apiKey string) *OpenAICompatible {
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAICompatible{client: client, baseURL: baseURL, apiKey: apiKey}
}

// Complete returns one assistant turn.
func (model *OpenAICompatible) Complete(ctx context.Context, request ModelRequest) (ModelResponse, error) {
	body, err := model.send(ctx, request, false)
	if err != nil {
		return ModelResponse{}, err
	}
	var payload chatCompletion
	if err := json.Unmarshal(body, &payload); err != nil {
		return ModelResponse{}, fmt.Errorf("decode model response: %w", err)
	}
	if len(payload.Choices) == 0 {
		return ModelResponse{}, fmt.Errorf("model returned no choices")
	}
	return ModelResponse{
		Message:      payload.Choices[0].Message.toMessage(),
		Usage:        payload.Usage.toUsage(),
		FinishReason: payload.Choices[0].FinishReason,
	}, nil
}

// Stream emits incremental content and returns the completed turn.
func (model *OpenAICompatible) Stream(ctx context.Context, request ModelRequest, onChunk func(string)) (ModelResponse, error) {
	response, err := model.post(ctx, request, true)
	if err != nil {
		return ModelResponse{}, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return ModelResponse{}, model.statusError(response)
	}

	var content strings.Builder
	assembled := ModelResponse{Message: Message{Role: RoleAssistant}}
	toolCalls := map[int]*ToolCall{}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk chatCompletionChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			// A malformed frame mid-stream is not worth failing a run that is
			// otherwise producing output.
			continue
		}
		if chunk.Usage != nil {
			assembled.Usage = chunk.Usage.toUsage()
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != "" {
			assembled.FinishReason = choice.FinishReason
		}
		if text := choice.Delta.Content; text != "" {
			content.WriteString(text)
			if onChunk != nil {
				onChunk(text)
			}
		}
		// Tool calls arrive in fragments keyed by index, so they are assembled
		// rather than replaced.
		for _, delta := range choice.Delta.ToolCalls {
			call, found := toolCalls[delta.Index]
			if !found {
				call = &ToolCall{}
				toolCalls[delta.Index] = call
			}
			if delta.ID != "" {
				call.ID = delta.ID
			}
			if delta.Function.Name != "" {
				call.Name = delta.Function.Name
			}
			if delta.Function.Arguments != "" {
				call.Arguments = append(call.Arguments, delta.Function.Arguments...)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return ModelResponse{}, fmt.Errorf("read model stream: %w", err)
	}

	assembled.Message.Content = content.String()
	for index := 0; index < len(toolCalls); index++ {
		call, found := toolCalls[index]
		if !found || call.Name == "" {
			continue
		}
		if !json.Valid(call.Arguments) {
			call.Arguments = json.RawMessage(`{}`)
		}
		assembled.Message.ToolCalls = append(assembled.Message.ToolCalls, *call)
	}
	return assembled, nil
}

func (model *OpenAICompatible) send(ctx context.Context, request ModelRequest, stream bool) ([]byte, error) {
	response, err := model.post(ctx, request, stream)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return nil, model.statusError(response)
	}
	var buffer bytes.Buffer
	if _, err := buffer.ReadFrom(response.Body); err != nil {
		return nil, fmt.Errorf("read model response: %w", err)
	}
	return buffer.Bytes(), nil
}

func (model *OpenAICompatible) post(ctx context.Context, request ModelRequest, stream bool) (*http.Response, error) {
	payload := chatRequest{
		Model: request.Model, Stream: stream,
		Temperature: request.Temperature, MaxTokens: request.MaxTokens,
	}
	for _, message := range request.Messages {
		payload.Messages = append(payload.Messages, wireMessageFrom(message))
	}
	for _, tool := range request.Tools {
		parameters := tool.Parameters
		if parameters == nil {
			parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		payload.Tools = append(payload.Tools, wireTool{
			Type: "function",
			Function: wireToolFunction{
				Name: tool.Name, Description: tool.Description, Parameters: parameters,
			},
		})
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode model request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, model.baseURL+"/chat/completions", bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("build model request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if model.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+model.apiKey)
	}
	if stream {
		httpRequest.Header.Set("Accept", "text/event-stream")
	}

	response, err := model.client.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("call model: %w", err)
	}
	return response, nil
}

// statusError reports a provider failure without echoing the request, which
// would carry the conversation and the API key header.
func (model *OpenAICompatible) statusError(response *http.Response) error {
	var buffer bytes.Buffer
	_, _ = buffer.ReadFrom(response.Body)
	detail := strings.TrimSpace(buffer.String())
	if len(detail) > 500 {
		detail = detail[:500]
	}
	if detail == "" {
		return fmt.Errorf("model request failed with status %d", response.StatusCode)
	}
	return fmt.Errorf("model request failed with status %d: %s", response.StatusCode, detail)
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []wireMessage `json:"messages"`
	Tools       []wireTool    `json:"tools,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
}

func wireMessageFrom(message Message) wireMessage {
	wire := wireMessage{
		Role: string(message.Role), Content: message.Content,
		Name: message.Name, ToolCallID: message.ToolCallID,
	}
	for _, call := range message.ToolCalls {
		arguments := string(call.Arguments)
		if arguments == "" {
			arguments = "{}"
		}
		wire.ToolCalls = append(wire.ToolCalls, wireToolCall{
			ID: call.ID, Type: "function",
			Function: wireToolCallFunction{Name: call.Name, Arguments: arguments},
		})
	}
	return wire
}

func (wire wireMessage) toMessage() Message {
	message := Message{
		Role: Role(wire.Role), Content: wire.Content,
		Name: wire.Name, ToolCallID: wire.ToolCallID,
	}
	for _, call := range wire.ToolCalls {
		arguments := json.RawMessage(call.Function.Arguments)
		if !json.Valid(arguments) {
			arguments = json.RawMessage(`{}`)
		}
		message.ToolCalls = append(message.ToolCalls, ToolCall{
			ID: call.ID, Name: call.Function.Name, Arguments: arguments,
		})
	}
	return message
}

type wireToolCall struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function wireToolCallFunction `json:"function"`
}

type wireToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireTool struct {
	Type     string           `json:"type"`
	Function wireToolFunction `json:"function"`
}

type wireToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type chatCompletion struct {
	Choices []struct {
		Message      wireMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage wireUsage `json:"usage"`
}

type chatCompletionChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
}

type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (usage wireUsage) toUsage() Usage {
	return Usage{
		PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens,
		TotalTokens: usage.TotalTokens,
	}
}
