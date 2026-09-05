// Package ai defines KilasFlow's agent contracts: AgentRuntime, requests,
// events, and memory.
//
// These types are the public boundary. Nothing outside an adapter package may
// import a provider or agent-framework module, so the framework stays an
// implementation detail rather than part of the workflow contract. The engine,
// the nodes, and the tests all speak only the types declared here.
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Role names one participant in a conversation.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one conversation turn.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content,omitempty"`
	// ToolCalls are the tools an assistant turn asked to run.
	ToolCalls []ToolCall `json:"toolCalls,omitempty"`
	// ToolCallID ties a tool result back to the call that requested it.
	ToolCallID string `json:"toolCallId,omitempty"`
	// Name is the tool that produced a tool-role message.
	Name string `json:"name,omitempty"`
}

// ToolCall is one tool invocation a model requested.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ToolDefinition describes a tool to a model.
type ToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Parameters is a JSON Schema object describing the tool's arguments.
	Parameters map[string]any `json:"parameters,omitempty"`
}

// Usage is token accounting, populated only where the provider supplies it.
type Usage struct {
	PromptTokens     int `json:"promptTokens,omitempty"`
	CompletionTokens int `json:"completionTokens,omitempty"`
	TotalTokens      int `json:"totalTokens,omitempty"`
}

// Add accumulates usage across the turns of one agent run.
func (usage *Usage) Add(other Usage) {
	usage.PromptTokens += other.PromptTokens
	usage.CompletionTokens += other.CompletionTokens
	usage.TotalTokens += other.TotalTokens
}

// ModelRequest is one completion request.
//
// Every sampling knob is a pointer because absent and zero are different
// instructions. `temperature: 0` is what a user asks for when they want
// deterministic extraction, and a value type would make that indistinguishable
// from "the user never touched this field" — at which point the provider
// silently applies its own default instead.
type ModelRequest struct {
	Model            string
	Messages         []Message
	Tools            []ToolDefinition
	Temperature      *float64
	TopP             *float64
	FrequencyPenalty *float64
	PresencePenalty  *float64
	// MaxTokens stays a value type: it has no meaningful zero to preserve, and
	// a provider that receives `max_tokens: 0` refuses the call outright.
	MaxTokens int
	// MaxRetries bounds how many times a refused or unreachable request is
	// re-sent. Zero means one attempt and no retry.
	MaxRetries int
}

// ModelResponse is one completion.
type ModelResponse struct {
	Message      Message
	Usage        Usage
	FinishReason string
}

// ChatModel is the local model boundary.
//
// An adapter implements it over a provider's HTTP API or over an agent
// framework; nothing above this interface knows which.
type ChatModel interface {
	// Complete returns one assistant turn.
	Complete(ctx context.Context, request ModelRequest) (ModelResponse, error)
	// Stream emits incremental content and returns the completed turn. An
	// adapter with no streaming support may emit nothing and return the whole
	// response, which is why the contract is "emit zero or more chunks".
	Stream(ctx context.Context, request ModelRequest, onChunk func(string)) (ModelResponse, error)
}

// Tool is one callable the agent may invoke.
type Tool interface {
	Definition() ToolDefinition
	Invoke(ctx context.Context, arguments json.RawMessage) (string, error)
}

// Memory persists conversation history between agent runs.
type Memory interface {
	Load(ctx context.Context, session SessionKey) ([]Message, error)
	Append(ctx context.Context, session SessionKey, messages []Message) error
}

// SessionKey scopes memory. Tenant and workflow are always part of it, so one
// tenant's session can never be addressed from another's workflow even if the
// session ID is guessed.
type SessionKey struct {
	TenantID   string
	WorkflowID string
	SessionID  string
}

// Valid reports whether a key can address a conversation.
func (key SessionKey) Valid() bool {
	return key.TenantID != "" && key.WorkflowID != "" && strings.TrimSpace(key.SessionID) != ""
}

// String is the storage key. Its parts are joined with a separator that cannot
// appear in an ID, so two different keys can never collide into one history.
func (key SessionKey) String() string {
	return key.TenantID + "\x00" + key.WorkflowID + "\x00" + key.SessionID
}

// EventKind names one nested agent event.
type EventKind string

const (
	EventModelStarted   EventKind = "ai.model.started"
	EventModelDelta     EventKind = "ai.model.delta"
	EventModelCompleted EventKind = "ai.model.completed"
	EventToolStarted    EventKind = "ai.tool.started"
	EventToolCompleted  EventKind = "ai.tool.completed"
	EventToolFailed     EventKind = "ai.tool.failed"
	EventAgentCompleted EventKind = "ai.agent.completed"
	EventAgentFailed    EventKind = "ai.agent.failed"
)

// Event is one nested occurrence inside an agent run. It feeds the existing
// execution event model rather than a parallel channel.
type Event struct {
	Kind EventKind `json:"kind"`
	// Iteration is the tool-loop turn this event belongs to, so a consumer can
	// reconstruct the nesting without a tree.
	Iteration int    `json:"iteration"`
	Model     string `json:"model,omitempty"`
	Tool      string `json:"tool,omitempty"`
	// Delta carries streamed content for EventModelDelta.
	Delta string `json:"delta,omitempty"`
	// Detail carries tool arguments or results, already redacted by the caller
	// that publishes it.
	Detail json.RawMessage `json:"detail,omitempty"`
	Usage  *Usage          `json:"usage,omitempty"`
	Error  string          `json:"error,omitempty"`
	At     time.Time       `json:"at"`
}

// EventSink receives nested agent events. It must never block the run: an
// implementation that cannot keep up drops rather than stalls.
type EventSink func(Event)

// Emit is a nil-safe helper so callers need not guard every publish.
func (sink EventSink) Emit(event Event) {
	if sink == nil {
		return
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	sink(event)
}

// AgentRequest is one agent invocation.
type AgentRequest struct {
	Model        ChatModel
	ModelName    string
	SystemPrompt string
	// Input is the user turn for this run.
	Input string
	Tools []Tool
	// Memory and Session are optional; without both, the run is stateless.
	Memory  Memory
	Session SessionKey
	// MaxIterations bounds the tool loop. A model that keeps asking for tools
	// would otherwise run until the execution timeout.
	MaxIterations int
	// The sampling knobs are pointers for the reason ModelRequest's are: a
	// value the user never set must not be sent at all.
	Temperature      *float64
	TopP             *float64
	FrequencyPenalty *float64
	PresencePenalty  *float64
	MaxTokens        int
	MaxRetries       int
	// Stream requests incremental content where the model supports it.
	Stream bool
}

// AgentResult is one completed agent run.
type AgentResult struct {
	Output     string    `json:"output"`
	Messages   []Message `json:"messages"`
	Usage      Usage     `json:"usage"`
	Iterations int       `json:"iterations"`
	ToolCalls  int       `json:"toolCalls"`
}

// AgentRuntime runs the tool loop.
//
// The engine depends on this interface and never on a provider or framework
// type, so the runtime behind it is replaceable without touching workflow
// JSON, the registry, or the engine.
type AgentRuntime interface {
	Run(ctx context.Context, request AgentRequest, sink EventSink) (AgentResult, error)
}

// DefaultMaxIterations bounds a tool loop that never converges.
const DefaultMaxIterations = 8

// Validate reports whether a request can be run at all.
func (request AgentRequest) Validate() error {
	if request.Model == nil {
		return fmt.Errorf("an agent needs a language model")
	}
	if strings.TrimSpace(request.Input) == "" {
		return fmt.Errorf("an agent needs input")
	}
	if request.Memory != nil && !request.Session.Valid() {
		return fmt.Errorf("memory needs a tenant, workflow, and session")
	}
	return nil
}
