package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kilaslab/kilas-flow/internal/safehttp"
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
		Temperature: request.Temperature, TopP: request.TopP,
		FrequencyPenalty: request.FrequencyPenalty, PresencePenalty: request.PresencePenalty,
		MaxTokens: request.MaxTokens,
	}
	if stream {
		// A provider reports token usage on a streamed run only when asked to.
		// Without this the final chunk carries no `usage` object, every
		// streamed completion accounts for zero tokens, and every cost figure
		// built on that number is wrong — while streaming is on by default.
		//
		// Sent only when streaming: several OpenAI-compatible endpoints reject
		// `stream_options` outright on a non-streamed request.
		payload.StreamOptions = &streamOptions{IncludeUsage: true}
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

	// Retrying happens here rather than around Complete or Stream because this
	// is the last point at which nothing has been consumed yet: a stream that
	// has already emitted chunks to the caller cannot be replayed, and a
	// retry loop one level up would duplicate half an answer.
	attempts := request.MaxRetries + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := range attempts {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, err
		}
		// Each attempt gets its own deadline, the way a provider's timeout
		// option is meant: a slow answer is this request's problem, not the
		// conversation's. The deadline is released when the body is closed,
		// so a streamed answer is not cut off by the call returning.
		//
		// A streamed answer is bounded by silence rather than by length. A
		// reasoning model streams its thinking for a long time before the
		// first word of the answer — gemma4 on Ollama sends a frame within a
		// second and content only after eighty — and a total deadline cut
		// such a stream off while it was plainly alive. The same duration
		// still bounds the wait for the first byte and every pause after it.
		attemptCtx := ctx
		cancel := context.CancelFunc(func() {})
		var idle *idleDeadline
		if request.Timeout > 0 {
			if stream {
				idle = newIdleDeadline(ctx, request.Timeout)
				attemptCtx, cancel = idle.ctx, idle.stop
			} else {
				attemptCtx, cancel = context.WithTimeout(ctx, request.Timeout)
			}
		}
		httpRequest, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, model.baseURL+"/chat/completions", bytes.NewReader(encoded))
		if err != nil {
			cancel()
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
			cancel()
			// The transport error prints the URL, which a provider's base URL
			// may have given a key in its query: only the scheme and host go on.
			err = safehttp.RedactError(err)
			// The attempt's own deadline is named only when the attempt is
			// what expired. A caller whose context ended first — the
			// deployment's run ceiling on an agent node — did not ask for too
			// little time: naming the request timeout there reported a
			// duration that had not elapsed and pointed the user at an option
			// that cannot raise a bound the node does not own.
			if request.Timeout > 0 && (errors.Is(attemptCtx.Err(), context.DeadlineExceeded) || idle.expired()) && ctx.Err() == nil {
				lastErr = fmt.Errorf("model request did not answer within %s (raise the model node's Timeout option to allow longer): %w", request.Timeout, err)
			} else {
				lastErr = fmt.Errorf("call model: %w", err)
			}
			if attempt+1 < attempts {
				if err := waitBeforeRetry(ctx, retryDelay(attempt, "")); err != nil {
					return nil, lastErr
				}
			}
			continue
		}
		if attempt+1 < attempts && retryableStatus(response.StatusCode) {
			// The body is drained and closed rather than abandoned so the
			// connection returns to the pool instead of being torn down on
			// every retry.
			lastErr = model.statusError(response)
			retryAfter := response.Header.Get("Retry-After")
			response.Body.Close()
			cancel()
			// A rate limit answered immediately produces the same rate limit:
			// back off, and honour the delay the provider asked for.
			if err := waitBeforeRetry(ctx, retryDelay(attempt, retryAfter)); err != nil {
				return nil, lastErr
			}
			continue
		}
		if request.Timeout > 0 {
			body := response.Body
			if idle != nil {
				body = idle.watch(body)
			}
			response.Body = cancelOnClose{ReadCloser: body, cancel: cancel}
		} else {
			cancel()
		}
		return response, nil
	}
	return nil, lastErr
}

// cancelOnClose releases an attempt's deadline once its body has been read,
// rather than when the call returned: a streamed answer is still arriving
// after post() hands the response back.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (body cancelOnClose) Close() error {
	err := body.ReadCloser.Close()
	body.cancel()
	return err
}

// Retry pacing: a refusal worth retrying is a refusal worth waiting out, and
// three requests a millisecond apart spend the user's quota to collect the
// same answer again.
const (
	retryBackoffBase = 250 * time.Millisecond
	retryBackoffCap  = 10 * time.Second
)

// retryDelay is how long to wait before the retry that follows attempt
// (zero-based). A provider that states Retry-After is obeyed within this
// deployment's ceiling; otherwise the delay doubles per attempt, with jitter
// so several nodes that hit one rate limit do not return in lockstep.
func retryDelay(attempt int, retryAfter string) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && seconds > 0 {
		delay := time.Duration(seconds) * time.Second
		if delay > retryBackoffCap {
			delay = retryBackoffCap
		}
		return delay
	}
	delay := retryBackoffBase
	for range attempt {
		delay *= 2
		if delay >= retryBackoffCap {
			delay = retryBackoffCap
			break
		}
	}
	if half := delay / 2; half > 0 {
		delay += time.Duration(rand.Int64N(int64(half)))
	}
	return delay
}

// waitBeforeRetry pauses until the delay elapses or the run is cancelled. It
// is a variable so a test can observe the pacing without spending it.
var waitBeforeRetry = func(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// retryableStatus reports a refusal worth sending again.
//
// Rate limiting and a server-side fault are transient by definition; a 400 or a
// 401 is the request itself being wrong, and re-sending it only spends the
// user's quota to receive the same answer.
func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
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
	Model            string         `json:"model"`
	Messages         []wireMessage  `json:"messages"`
	Tools            []wireTool     `json:"tools,omitempty"`
	Temperature      *float64       `json:"temperature,omitempty"`
	TopP             *float64       `json:"top_p,omitempty"`
	FrequencyPenalty *float64       `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64       `json:"presence_penalty,omitempty"`
	MaxTokens        int            `json:"max_tokens,omitempty"`
	Stream           bool           `json:"stream,omitempty"`
	StreamOptions    *streamOptions `json:"stream_options,omitempty"`
}

// streamOptions asks the provider for the accounting it otherwise withholds.
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	// Images never carry their own JSON: MarshalJSON folds them into the
	// content parts, and a decoded response has none.
	Images []string `json:"-"`
}

// MarshalJSON writes the text-only shape unless the turn carries images, in
// which case content becomes the part list the OpenAI API expects —
// `[{type:"text"},{type:"image_url"}]` — while everything else is unchanged.
// A plain request keeps the string form, which every compatible endpoint
// accepts.
func (wire wireMessage) MarshalJSON() ([]byte, error) {
	type plain wireMessage
	if len(wire.Images) == 0 {
		return json.Marshal(plain(wire))
	}
	parts := make([]map[string]any, 0, len(wire.Images)+1)
	if wire.Content != "" {
		parts = append(parts, map[string]any{"type": "text", "text": wire.Content})
	}
	for _, url := range wire.Images {
		parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
	}
	type withParts struct {
		Role       string           `json:"role"`
		Content    []map[string]any `json:"content"`
		Name       string           `json:"name,omitempty"`
		ToolCallID string           `json:"tool_call_id,omitempty"`
		ToolCalls  []wireToolCall   `json:"tool_calls,omitempty"`
	}
	return json.Marshal(withParts{
		Role: wire.Role, Content: parts,
		Name: wire.Name, ToolCallID: wire.ToolCallID, ToolCalls: wire.ToolCalls,
	})
}

func wireMessageFrom(message Message) wireMessage {
	wire := wireMessage{
		Role: string(message.Role), Content: message.Content,
		Name: message.Name, ToolCallID: message.ToolCallID,
		Images: message.Images,
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

// idleDeadline cancels a streamed request once it has been silent for its
// window: before the response starts, and between any two reads after it.
type idleDeadline struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	window time.Duration
	timer  *time.Timer
}

// errStreamSilent is the cause an idle deadline cancels with, so an expiry can
// be told apart from the caller's own context ending.
var errStreamSilent = errors.New("model stream was silent past its request timeout")

func newIdleDeadline(parent context.Context, window time.Duration) *idleDeadline {
	ctx, cancel := context.WithCancelCause(parent)
	deadline := &idleDeadline{ctx: ctx, cancel: cancel, window: window}
	deadline.timer = time.AfterFunc(window, func() { cancel(errStreamSilent) })
	return deadline
}

// expired reports whether silence, rather than anything else, ended the
// request. A nil deadline never expires, so callers need not guard it.
func (deadline *idleDeadline) expired() bool {
	return deadline != nil && errors.Is(context.Cause(deadline.ctx), errStreamSilent)
}

func (deadline *idleDeadline) stop() {
	deadline.timer.Stop()
	deadline.cancel(nil)
}

// watch wraps a response body so every read that returns data restarts the
// silence window. Releasing the deadline is cancelOnClose's job, as it is for a
// plain request.
func (deadline *idleDeadline) watch(body io.ReadCloser) io.ReadCloser {
	return &idleBody{ReadCloser: body, deadline: deadline}
}

type idleBody struct {
	io.ReadCloser
	deadline *idleDeadline
}

func (body *idleBody) Read(buffer []byte) (int, error) {
	n, err := body.ReadCloser.Read(buffer)
	if n > 0 {
		body.deadline.timer.Reset(body.deadline.window)
	}
	if err != nil && body.deadline.expired() {
		err = fmt.Errorf("model stream was silent for %s (raise the model node's Timeout option to allow a longer pause): %w", body.deadline.window, err)
	}
	return n, err
}
