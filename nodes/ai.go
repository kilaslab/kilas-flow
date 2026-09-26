package nodes

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Executor IDs for the AI family.
const (
	ChatModelExecutorID           = "core.chatModel"
	OpenAIChatModelExecutorID     = "core.openAiChatModel"
	OpenRouterChatModelExecutorID = "core.openRouterChatModel"
	MemoryExecutorID              = "core.memoryBuffer"
	HTTPToolExecutorID            = "core.httpTool"
	MCPClientToolExecutorID       = "core.mcpClientTool"
	AgentExecutorID               = "core.agent"
	ChainExecutorID               = "core.chainLlm"
	WorkflowToolExecutorID        = "core.workflowTool"
	CalculatorExecutorID          = "core.calculator"
	CalculatorToolExecutorID      = "core.calculatorTool"
	OutputParserExecutorID        = "core.outputParser"
)

// Node types for the AI family.
//
// The two provider models are named after n8n's own node names —
// `lmChatOpenAi`, `lmChatOpenRouter` — so the mapping an importer needs is the
// identity on the last segment. The generic `chatModel` stays beside them: its
// address is a parameter, which is what an OpenAI-compatible endpoint nobody
// has a node type for — a local Ollama, a gateway, a proxy — needs.
const (
	ChatModelNodeType           = "kilasflow.chatModel"
	OpenAIChatModelNodeType     = "kilasflow.lmChatOpenAi"
	OpenRouterChatModelNodeType = "kilasflow.lmChatOpenRouter"
	MemoryNodeType              = "kilasflow.memoryBuffer"
	HTTPToolNodeType            = "kilasflow.httpTool"
	MCPClientToolNodeType       = "kilasflow.mcpClientTool"
	AgentNodeType               = "kilasflow.agent"
	ChainNodeType               = "kilasflow.chainLlm"
	WorkflowToolNodeType        = "kilasflow.workflowTool"
	CalculatorNodeType          = "kilasflow.calculator"
	CalculatorToolNodeType      = "kilasflow.calculatorTool"
	OutputParserNodeType        = "kilasflow.outputParser"
)

// Credential types the chat models accept.
//
// The IDs are n8n's. A workflow names its credential by type, so an imported
// node that says `openRouterApi` has to find a type of exactly that name here
// or arrive with no credential at all.
const (
	OpenAICredentialType     = "openAiApi"
	OpenRouterCredentialType = "openRouterApi"
	// BearerCredentialType is what the generic OpenAI-compatible model takes,
	// because a self-hosted endpoint has no vendor to name.
	BearerCredentialType = "httpBearerAuth"
)

// modelOptionsKey is the collection a provider chat model carries its sampling
// and transport settings in, named as n8n names it.
const modelOptionsKey = "options"

// Option keys inside that collection, using n8n's names verbatim.
//
// These are interchange facts rather than choices: an imported node's stored
// parameters are keyed by these strings, and an export has to write them back.
const (
	ModelOptionTemperature      = "temperature"
	ModelOptionMaxTokens        = "maxTokens"
	ModelOptionTopP             = "topP"
	ModelOptionFrequencyPenalty = "frequencyPenalty"
	ModelOptionPresencePenalty  = "presencePenalty"
	ModelOptionTimeout          = "timeout"
	ModelOptionMaxRetries       = "maxRetries"
)

// samplingOptionKeys are the options that go to the provider as sampling
// parameters, as against the transport ones this server enforces itself.
var samplingOptionKeys = []string{
	ModelOptionTemperature, ModelOptionMaxTokens, ModelOptionTopP,
	ModelOptionFrequencyPenalty, ModelOptionPresencePenalty,
}

// DefaultModelTimeoutCeiling bounds how long a deployment lets one model node
// wait when it has not said otherwise.
//
// Ten minutes rather than the outbound default of thirty seconds: a reasoning
// model working through a long prompt routinely exceeds a minute, and n8n's own
// OpenRouter node defaults to six. It is still a bound — an unbounded model
// call holds a worker for as long as the provider cares to keep the socket
// open.
const DefaultModelTimeoutCeiling = 10 * time.Minute

// defaultModelMaxRetries is what a node that never set the option gets,
// matching n8n's own default for its chat model nodes. Absent-means-two rather
// than absent-means-none is the difference between a rate limit being waited
// out and a run failing on the first 429.
const defaultModelMaxRetries = 2

// ErrModelTimeoutAboveCeiling reports a node asking to wait longer than this
// deployment permits.
//
// Refused rather than clamped on purpose. Silently granting thirty seconds to a
// node that asked for twenty minutes turns a configuration mistake into an
// intermittent one: everything works until a prompt is slow enough to need what
// was asked for, and then it fails as a transport error nobody can trace back
// to the setting that caused it.
var ErrModelTimeoutAboveCeiling = errors.New("the requested model timeout is above this deployment's ceiling")

// modelAPIKeyFields names the field each accepted credential type keeps its key
// in.
//
// A table rather than a switch: a provider credential added to the registry
// needs one entry here and no new control flow, and a type that is absent is
// refused by absence — which is a better error than reading an empty string out
// of a credential that was never meant to authenticate a model.
var modelAPIKeyFields = map[string]string{
	BearerCredentialType:     "token",
	OpenAICredentialType:     "apiKey",
	OpenRouterCredentialType: "apiKey",
}

// descriptorKey marks the single item an AI sub-node emits.
//
// A chat model, a memory, and a tool are configuration for an agent rather
// than item producers. Emitting one descriptor item on a typed port lets them
// use the existing scheduler and item model unchanged: topological order
// already guarantees a sub-node runs before the agent that reads it.
const descriptorKey = "$ai"

// chatModelProvider is everything that differs between one provider's chat
// model node and another's.
//
// The nodes are separate *types* rather than one type with a provider
// parameter, because an imported n8n workflow names a type and an export has to
// name one back: collapsing `lmChatOpenAi` and `lmChatOpenRouter` into a single
// KilasFlow type whose identity is a URL string makes the round trip lossy, and
// nothing downstream could tell the two apart again.
type chatModelProvider struct {
	nodeType       string
	executorID     string
	displayName    string
	description    string
	credentialType string
	baseURL        string
	defaultModel   string
	// modelHint is the placeholder the free-text mode shows.
	modelHint string
	// defaultTimeout is n8n's own default for this provider, in milliseconds,
	// which is what the collection offers when the option is added.
	defaultTimeout float64
}

func openAIChatModelProvider() chatModelProvider {
	return chatModelProvider{
		nodeType:       OpenAIChatModelNodeType,
		executorID:     OpenAIChatModelExecutorID,
		displayName:    "OpenAI Chat Model",
		description:    "Supplies an OpenAI chat model to an AI Agent.",
		credentialType: OpenAICredentialType,
		baseURL:        "https://api.openai.com/v1",
		defaultModel:   "gpt-5-mini",
		modelHint:      "gpt-5-mini",
		defaultTimeout: 60000,
	}
}

func openRouterChatModelProvider() chatModelProvider {
	return chatModelProvider{
		nodeType:       OpenRouterChatModelNodeType,
		executorID:     OpenRouterChatModelExecutorID,
		displayName:    "OpenRouter Chat Model",
		description:    "Supplies a model from OpenRouter's catalogue to an AI Agent.",
		credentialType: OpenRouterCredentialType,
		baseURL:        "https://openrouter.ai/api/v1",
		defaultModel:   "openai/gpt-4.1-mini",
		modelHint:      "openai/gpt-4.1-mini",
		// Six minutes, not one: OpenRouter queues behind whichever upstream
		// provider serves the model, so its own node allows far longer.
		defaultTimeout: 360000,
	}
}

func openAIChatModelNode() node.Definition { return providerChatModelNode(openAIChatModelProvider()) }
func openRouterChatModelNode() node.Definition {
	return providerChatModelNode(openRouterChatModelProvider())
}

// providerChatModelNode builds one provider's chat model definition.
func providerChatModelNode(provider chatModelProvider) node.Definition {
	return node.Definition{
		Type:        provider.nodeType,
		Version:     workflow.V(1),
		Credentials: []node.CredentialRequirement{{Type: provider.credentialType, Required: true}},
		DisplayName: provider.displayName,
		Description: provider.description,
		Category:    "AI",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:brain"},
		IconColor:   "#a855f7",
		// No subtitle template. `model` is a resource locator, which is an
		// object, and the canvas renders an object as nothing — so a template
		// here would resolve to an empty line on every configured node, which
		// is worse than declaring none.
		Outputs: []workflow.Port{{Name: "model", Kind: workflow.ConnectionLanguageModel}},
		Parameters: []node.PropertyDefinition{
			{
				Key: "notice", Label: "Connect this to an AI Agent", Kind: node.PropertyNotice,
				Description: "A chat model is configuration for an agent rather than a step of its own. " +
					"Wire its Model output into an agent's Chat Model input.",
			},
			{
				// A locator rather than a plain select, so a catalogue this
				// server could not fetch — an expired key, an outage, a
				// model released this morning — does not leave the user with
				// an empty dropdown and no way to name a model at all.
				Key: "model", Label: "Model", Kind: node.PropertyResourceLocator, Required: true,
				Description: "Choose from the provider's catalogue, or type a model name.",
				Default: map[string]any{
					property.LocatorSentinel: true, "mode": "list", "value": provider.defaultModel,
				},
				Modes: []node.PropertyMode{
					{
						Name: "list", Label: "From list", Kind: node.PropertyOptions, Placeholder: "Choose…",
						LoadOptions: &node.OptionsLoader{
							Source: property.LoaderHTTP, Method: http.MethodGet,
							BaseURLParameter: "baseUrl",
							Endpoint:         "/models",
							CredentialType:   provider.credentialType,
							ItemsPath:        "data",
							ValueField:       "id",
							LabelTemplate:    "{{ id }}",
							// Changing the address means a different catalogue,
							// so the list is discarded rather than kept from
							// the previous one.
							DependsOn: []string{"baseUrl"},
						},
					},
					{Name: "id", Label: "By name", Kind: node.PropertyString, Placeholder: provider.modelHint},
				},
			},
			{
				Key: "baseUrl", Label: "Base URL", Kind: node.PropertyString, Default: provider.baseURL,
				Description: "Change this only to reach the provider through a gateway or a proxy.",
			},
			{Key: "stream", Label: "Stream output", Kind: node.PropertyBoolean, Default: true},
			chatModelOptions(provider),
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     provider.executorID,
		Validate:       validateProviderChatModel(provider),
	}
}

// chatModelOptions is the Options collection both provider nodes carry.
//
// The member names and their defaults are n8n's, transcribed from its own
// LmChatOpenAi.node.ts and LmChatOpenRouter.node.ts at 2.34.0. Transcribed
// rather than read: internal/guardrails forbids any build input — a test
// included — from reading the reference checkout, so the anti-drift check
// compares this declaration against testdata/n8n_chat_model_options.json, a
// committed record of what the reference said and when it was read.
//
// A collection's declared defaults never reach a stored document — the
// registry's default-filling walks the top level only — so a member the user
// never added arrives absent, and absent is what lets this node send nothing at
// all rather than a default of its own invention.
func chatModelOptions(provider chatModelProvider) node.PropertyDefinition {
	return node.PropertyDefinition{
		Key: modelOptionsKey, Label: "Options", Kind: node.PropertyCollection,
		Description: "An option you do not add is not sent, so the provider applies its own default.",
		Fields: []node.PropertyDefinition{
			{
				Key: ModelOptionFrequencyPenalty, Label: "Frequency Penalty", Kind: node.PropertyNumber, Default: float64(0),
				TypeOptions: &node.TypeOptions{MinValue: numberBound(-2), MaxValue: numberBound(2)},
				Description: "Positive values discourage the model from repeating a line it has already produced.",
			},
			{
				Key: ModelOptionMaxTokens, Label: "Maximum Number of Tokens", Kind: node.PropertyNumber, Default: float64(-1),
				Description: "How many tokens the completion may run to. -1 leaves it to the model.",
			},
			{
				Key: ModelOptionMaxRetries, Label: "Max Retries", Kind: node.PropertyNumber, Default: float64(2),
				TypeOptions: &node.TypeOptions{MinValue: numberBound(0)},
				Description: "How many times a rate-limited or failed request is sent again.",
			},
			{
				Key: ModelOptionPresencePenalty, Label: "Presence Penalty", Kind: node.PropertyNumber, Default: float64(0),
				TypeOptions: &node.TypeOptions{MinValue: numberBound(-2), MaxValue: numberBound(2)},
				Description: "Positive values push the model towards topics it has not raised yet.",
			},
			{
				Key: ModelOptionTemperature, Label: "Sampling Temperature", Kind: node.PropertyNumber, Default: float64(0.7),
				TypeOptions: &node.TypeOptions{MinValue: numberBound(0), MaxValue: numberBound(2)},
				Description: "Lower is less random. Zero is as close to deterministic as the model gets, and is sent as zero rather than treated as unset.",
			},
			{
				Key: ModelOptionTimeout, Label: "Timeout", Kind: node.PropertyNumber, Default: provider.defaultTimeout,
				TypeOptions: &node.TypeOptions{MinValue: numberBound(0)},
				Description: "How long one model request may wait, in milliseconds. Above this deployment's ceiling the node is refused rather than quietly given less.",
			},
			{
				Key: ModelOptionTopP, Label: "Top P", Kind: node.PropertyNumber, Default: float64(1),
				TypeOptions: &node.TypeOptions{MinValue: numberBound(0), MaxValue: numberBound(1)},
				Description: "Nucleus sampling. Alter this or the temperature, not both.",
			},
		},
	}
}

// numberBound is a pointer to a numeric limit, which TypeOptions takes so that
// "no bound" stays distinguishable from "a bound of zero".
func numberBound(value float64) *float64 { return &value }

func chatModelNode() node.Definition {
	return node.Definition{
		Type: ChatModelNodeType,
		// Not required: an endpoint that speaks OpenAI's protocol and is not
		// OpenAI — a local Ollama, a vLLM server, an air-gapped gateway —
		// usually authenticates nobody. Requiring a credential made users
		// store a fake token, which is a meaningless secret that gets sent
		// with every request.
		Credentials: []node.CredentialRequirement{
			{Type: BearerCredentialType},
		},
		Version: workflow.V(1),
		// Named for what it is now that the two provider nodes exist beside
		// it: the node for an endpoint that speaks OpenAI's protocol but is
		// not OpenAI — a local Ollama, a gateway, a self-hosted server. The
		// type string is untouched, so no saved workflow notices.
		DisplayName: "OpenAI-Compatible Chat Model",
		Description: "Supplies an OpenAI-compatible chat model to an AI Agent.",
		Category:    "AI",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:brain"},
		IconColor:   "#a855f7",
		Subtitle:    "{{ $parameter.model }}",
		Outputs:     []workflow.Port{{Name: "model", Kind: workflow.ConnectionLanguageModel}},
		Parameters: []node.PropertyDefinition{
			{
				// Loaded from whatever endpoint the credential can reach,
				// rather than typed. A free-text model name makes a typo
				// indistinguishable from a valid model until the run fails.
				Key: "model", Label: "Model", Kind: node.PropertyOptions, Required: true, Default: "gpt-4o-mini",
				LoadOptions: &property.OptionsLoader{
					Source: property.LoaderHTTP, Method: http.MethodGet,
					BaseURLParameter: "baseUrl",
					Endpoint:         "/models",
					CredentialType:   "httpBearerAuth",
					ItemsPath:        "data",
					ValueField:       "id",
					LabelTemplate:    "{{ id }}",
					// Changing the base URL means a different catalogue, so the
					// list is discarded rather than kept from the last one.
					DependsOn: []string{"baseUrl"},
				},
			},
			{
				Key: "baseUrl", Label: "Base URL", Kind: node.PropertyString, Default: "https://api.openai.com/v1",
				Description: "Any OpenAI-compatible endpoint.",
			},
			{Key: "temperature", Label: "Temperature", Kind: node.PropertyNumber},
			{Key: "maxTokens", Label: "Maximum tokens", Kind: node.PropertyNumber},
			{Key: "stream", Label: "Stream output", Kind: node.PropertyBoolean, Default: true},
			// The transport settings the provider nodes already offer. Without
			// them the local-model path had no way to outlive the deployment's
			// outbound timeout, so a long answer died at thirty seconds with
			// nothing in the node to raise.
			{
				Key: modelOptionsKey, Label: "Options", Kind: node.PropertyCollection,
				Description: "An option you do not add is not sent, so the provider applies its own default.",
				Fields: []node.PropertyDefinition{
					{
						Key: ModelOptionTimeout, Label: "Timeout", Kind: node.PropertyNumber,
						TypeOptions: &node.TypeOptions{MinValue: numberBound(0)},
						Description: "How long one model request may wait, in milliseconds. A slower local model needs a larger value; above this deployment's ceiling the node is refused rather than quietly given less.",
					},
					{
						Key: ModelOptionMaxRetries, Label: "Max Retries", Kind: node.PropertyNumber, Default: float64(defaultModelMaxRetries),
						TypeOptions: &node.TypeOptions{MinValue: numberBound(0)},
						Description: "How many times a rate-limited or failed request is sent again, with exponential backoff.",
					},
				},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     ChatModelExecutorID,
		Validate:       validateChatModelConfiguration,
	}
}

func memoryNode() node.Definition {
	return node.Definition{
		Type:        MemoryNodeType,
		Version:     workflow.V(1),
		DisplayName: "Simple Memory",
		Description: "Keeps a bounded window of conversation history for an AI Agent.",
		Category:    "AI",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:memory-stick"},
		IconColor:   "#a855f7",
		Outputs:     []workflow.Port{{Name: "memory", Kind: workflow.ConnectionMemory}},
		Parameters: []node.PropertyDefinition{
			{
				Key: "sessionIdType", Label: "Session ID", Kind: node.PropertyOptions, Default: sessionIDTypeFromInput,
				Options: []node.PropertyOption{
					{Label: "Connected Chat Trigger Node", Value: sessionIDTypeFromInput},
					{Label: "Define below", Value: sessionIDTypeCustomKey},
				},
				Description: "Take the session key from the incoming item, or define it with an expression.",
			},
			{
				Key: "sessionKey", Label: "Session Key", Kind: node.PropertyString,
				Description: "Identifies the conversation. Supports expressions, for example {{ $json.sessionId }}.",
				// Copied onto a freshly placed Memory node. A plain string with
				// `{{ … }}` is not an expression — see BUG-rjd6fm — so every
				// editor-built conversation would share one bucket. Imported
				// and saved keys are unchanged.
				Default: map[string]any{"mode": "expression", "value": "{{ $json.sessionId }}"},
			},
			{
				Key: "sessionId", Label: "Session ID (legacy)", Kind: node.PropertyString,
				Description: "Kept for nodes saved before the session key modes existed. It is used only when no session key mode is stored; prefer Session Key instead.",
			},
			{
				Key: "maxMessages", Label: "Maximum messages", Kind: node.PropertyNumber, Default: 40,
				Description: "How many messages of the conversation are kept. A question and its answer are two messages; the window is trimmed to whole exchanges, so it never begins in the middle of one.",
			},
			{Key: "maxAgeMinutes", Label: "Maximum age (minutes)", Kind: node.PropertyNumber, Default: 1440},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     MemoryExecutorID,
		Validate:       validateMemoryConfiguration,
	}
}

// Session key modes, named as n8n names them so an imported memory node keeps
// its meaning: fromInput reads the key off the incoming item, customKey takes
// an expression the author wrote.
const (
	sessionIDTypeFromInput = "fromInput"
	sessionIDTypeCustomKey = "customKey"
)

func httpToolNode() node.Definition {
	definition := httpRequestNode()
	definition.Type = HTTPToolNodeType
	definition.DisplayName = "HTTP Request Tool"
	definition.Description = "Exposes an HTTP Request to an AI Agent as a callable tool."
	definition.Category = "AI"
	definition.Inputs = nil
	definition.Outputs = []workflow.Port{{Name: "tool", Kind: workflow.ConnectionTool}}
	// Inherited from the HTTP node it is built from: a tool makes the same
	// outbound call and authenticates the same way.
	definition.ExecutorID = HTTPToolExecutorID
	// The tool's own naming and description sit in front of the HTTP Request
	// parameters, which are reused verbatim rather than re-declared.
	definition.Parameters = append([]node.PropertyDefinition{
		{
			Key: "toolName", Label: "Tool name", Kind: node.PropertyString,
			Description: "Optional override for the name the model calls. Defaults to the node name, normalised to letters, digits, and underscores.",
		},
		{
			Key: "toolDescription", Label: "Tool description", Kind: node.PropertyString, Required: true,
			Description: "What the tool does, written for the model.",
		},
	}, definition.Parameters...)
	definition.Codex = toolCodex()
	definition.Validate = validateHTTPToolConfiguration
	return definition
}

func agentNode() node.Definition {
	return node.Definition{
		Type:        AgentNodeType,
		Version:     workflow.V(1),
		DisplayName: "AI Agent",
		Description: "Runs a model with optional memory and tools until it answers.",
		Category:    "AI",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:bot"},
		IconColor:   "#a855f7",
		Inputs: []workflow.Port{
			{Name: "main", Kind: workflow.ConnectionMain},
			// One model, one memory, many tools. These bounds were
			// unenforceable while a port was only a name and a kind: the
			// compiler would accept three language models on one agent, and an
			// agent with none would compile and fail on the first item.
			{
				Name: "model", DisplayName: "Chat Model", Kind: workflow.ConnectionLanguageModel,
				Required: true, MaxConnections: 1,
			},
			{
				Name: "memory", DisplayName: "Memory", Kind: workflow.ConnectionMemory,
				MaxConnections: 1,
			},
			// Unbounded on purpose: an agent may hold as many tools as it likes.
			{Name: "tools", DisplayName: "Tools", Kind: workflow.ConnectionTool},
			{
				Name: "outputParser", DisplayName: "Output Parser", Kind: workflow.ConnectionOutputParser,
				MaxConnections: 1,
			},
		},
		Outputs: mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "prompt", Label: "Prompt", Kind: node.PropertyString, Required: true,
				Description: "The user turn for this run. Supports expressions.",
				// Copied onto a freshly placed Agent. A plain string with
				// `{{ … }}` is not an expression in this runtime — see
				// BUG-rjd6fm — so the default is the marker createWorkflowNode
				// already copies verbatim. Saved and imported Agents that
				// already set prompt are unchanged.
				Default: map[string]any{"mode": "expression", "value": "{{ $json.chatInput }}"},
			},
			{
				Key: "systemMessage", Label: "System message", Kind: node.PropertyString,
				Description: "The message sent to the agent before the conversation starts. Supports expressions.",
			},
			{
				Key: "systemPrompt", Label: "System prompt (legacy)", Kind: node.PropertyString,
				Description: "Kept for graphs saved before the system message existed. It is used only when no system message is set.",
			},
			{Key: "maxIterations", Label: "Maximum tool iterations", Kind: node.PropertyNumber, Default: 10},
			{
				Key: "returnIntermediateSteps", Label: "Return intermediate steps", Kind: node.PropertyBoolean, Default: false,
				Description: "Include the full ordered message list, tool turns included, in the output item.",
			},
			{
				Key: "passthroughBinaryImages", Label: "Passthrough binary images", Kind: node.PropertyBoolean, Default: true,
				Description: "Carry the incoming item's binary attachments through to the output item.",
			},
			{
				Key: "enableStreaming", Label: "Enable streaming", Kind: node.PropertyBoolean, Default: false,
				Description: "Stream the model's response in real time as it generates text.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     AgentExecutorID,
		Validate:       validateAgentConfiguration,
	}
}

// Chain prompt sources, named as n8n names them: auto takes the text from a
// named field of the incoming item, define takes the text below plus the
// ordered message list.
const (
	chainPromptAuto   = "auto"
	chainPromptDefine = "define"
)

// Chain message roles, using the model's own vocabulary.
const (
	chainRoleSystem = "system"
	chainRoleHuman  = "human"
	chainRoleAI     = "ai"
)

func chainLlmNode() node.Definition {
	return node.Definition{
		Type:        ChainNodeType,
		Version:     workflow.V(1),
		DisplayName: "Basic LLM Chain",
		Description: "Prompts a language model once per item, with an optional output parser.",
		Category:    "AI",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:link"},
		IconColor:   "#0ea5e9",
		Inputs: []workflow.Port{
			{Name: "main", Kind: workflow.ConnectionMain},
			{
				Name: "model", DisplayName: "Chat Model", Kind: workflow.ConnectionLanguageModel,
				Required: true, MaxConnections: 1,
			},
			// Deliberately no memory slot: n8n's chain has none, and a user
			// who wires memory expects the agent. Adding one here would make
			// an exported workflow unrepresentable.
			{
				Name: "outputParser", DisplayName: "Output Parser", Kind: workflow.ConnectionOutputParser,
				MaxConnections: 1,
			},
		},
		Outputs: mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "promptType", Label: "Source for Prompt", Kind: node.PropertyOptions, Default: chainPromptAuto,
				Options: []node.PropertyOption{
					{Label: "Take from Field", Value: chainPromptAuto},
					{Label: "Define below", Value: chainPromptDefine},
				},
				Description: "Take the user message from a field of the incoming item, or define it with text and an ordered message list.",
			},
			{
				Key: "inputField", Label: "Input Field", Kind: node.PropertyString, Default: "chatInput",
				Description: "The field of the incoming item holding the user message.",
			},
			{
				Key: "text", Label: "Prompt (User Message)", Kind: node.PropertyString,
				Description: "The user message. Supports expressions.",
			},
			{
				Key: "messages", Label: "Chat Messages", Kind: node.PropertyFixedCollection,
				TypeOptions: &node.TypeOptions{MultipleValues: true, MultipleValueButtonText: "Add prompt"},
				Description: "Ordered System, Human, and AI messages sent before the user message. Each row's text supports expressions.",
				Groups: []node.PropertyGroup{{
					Key: "messageValues", Label: "Prompt",
					Fields: []node.PropertyDefinition{
						{
							Key: "type", Label: "Type", Kind: node.PropertyOptions, Default: chainRoleSystem,
							Options: []node.PropertyOption{
								{Label: "System", Value: chainRoleSystem},
								{Label: "Human", Value: chainRoleHuman},
								{Label: "AI", Value: chainRoleAI},
							},
						},
						{
							Key: "message", Label: "Message", Kind: node.PropertyString, Required: true,
							Description: "The message text. Supports expressions.",
						},
					},
				}},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     ChainExecutorID,
		Validate:       validateChainConfiguration,
	}
}

func validateChatModelConfiguration(n workflow.Node) error {
	if statementText(n.Parameters, "model") == "" {
		return fmt.Errorf("model is required")
	}
	// No credential is accepted, and none is looked up ambiently: the key
	// comes from the credential the node names, or the request carries no
	// Authorization header at all. An endpoint that needs no key is the normal
	// case for this node, and demanding one made users store a fake token.
	return nil
}

// validateProviderChatModel refuses a provider model that cannot run.
func validateProviderChatModel(provider chatModelProvider) workflow.ConfigValidator {
	return func(n workflow.Node) error {
		if !property.LocatorIsSet(n.Parameters["model"]) {
			return fmt.Errorf("model is required")
		}
		// The provider's own credential type, not a generic bearer token: the
		// node exists to be the thing an imported `lmChatOpenRouter` maps onto,
		// and accepting any bearer credential would put that identity back in
		// the hands of whatever the user happened to attach.
		if id, found := n.Credentials[provider.credentialType]; !found || strings.TrimSpace(id) == "" {
			return fmt.Errorf("a %s credential holding the API key is required", provider.credentialType)
		}
		return nil
	}
}

func validateMemoryConfiguration(n workflow.Node) error {
	mode := statementText(n.Parameters, "sessionIdType")
	switch mode {
	case "", sessionIDTypeFromInput, sessionIDTypeCustomKey, "expression":
	default:
		return fmt.Errorf("sessionIdType must be %q or %q", sessionIDTypeFromInput, sessionIDTypeCustomKey)
	}
	if mode == "" {
		// No mode stored: a node saved before the session key modes existed.
		// It keeps addressing its conversation by sessionId alone, with no
		// auto-scoping suffix — or by a newly-entered sessionKey, which is
		// honoured as a defined key.
		if statementText(n.Parameters, "sessionId") == "" && statementText(n.Parameters, "sessionKey") == "" {
			return fmt.Errorf("sessionId is required")
		}
		return nil
	}
	if mode != "expression" && statementText(n.Parameters, "sessionKey") == "" {
		return fmt.Errorf("sessionKey is required")
	}
	return nil
}

func validateHTTPToolConfiguration(n workflow.Node) error {
	// An empty toolName is not missing: the name derives from the canvas
	// name. Only an explicit override has to satisfy the charset here; the
	// derived name is normalised at run time and cannot fail it.
	if name := statementText(n.Parameters, "toolName"); name != "" && !validToolName(name) {
		return fmt.Errorf("toolName must contain only letters, digits, and underscores")
	}
	if statementText(n.Parameters, "toolDescription") == "" {
		return fmt.Errorf("toolDescription is required so the model knows when to call it")
	}
	if err := validateToolFromAI(n); err != nil {
		return err
	}
	return validateHTTPConfiguration(n)
}

func validToolName(name string) bool {
	if name == "expression" {
		// The marker statementText returns for an expression-valued parameter.
		return true
	}
	for _, letter := range name {
		switch {
		case letter >= 'a' && letter <= 'z', letter >= 'A' && letter <= 'Z',
			letter >= '0' && letter <= '9', letter == '_':
		default:
			return false
		}
	}
	return name != ""
}

// NormalizeToolName derives the model-facing tool name from a canvas name.
// The rule is workflow.NormalizeToolName, stated once in the graph package
// so the compiler can refuse duplicates before activation; this alias keeps
// runtime callers and the n8n exporter on the same spelling.
func NormalizeToolName(name string) string {
	return workflow.NormalizeToolName(name)
}

// sanitizeSessionKey prepares a node name for use inside a session key. Some
// memory backends reject characters outside this set, and node names allow
// spaces and punctuation, so sanitise before joining rather than after.
func sanitizeSessionKey(name string) string {
	var builder strings.Builder
	for _, letter := range name {
		switch {
		case letter >= 'a' && letter <= 'z', letter >= 'A' && letter <= 'Z',
			letter >= '0' && letter <= '9', letter == '_', letter == '-':
			builder.WriteRune(letter)
		default:
			builder.WriteRune('_')
		}
	}
	if builder.Len() == 0 {
		return "node"
	}
	return builder.String()
}

func validateAgentConfiguration(n workflow.Node) error {
	if statementText(n.Parameters, "prompt") == "" {
		return fmt.Errorf("prompt is required")
	}
	return nil
}

// executeChatModel emits a model descriptor for the agent that reads it.
func executeChatModel(ctx context.Context, ir workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A credential is optional here: an OpenAI-compatible endpoint that
	// authenticates nobody is the normal case for a local model, and demanding
	// a credential made every local user store a token the server ignores.
	credentialID := strings.TrimSpace(ir.Credentials[BearerCredentialType])
	if credentialID != "" {
		if _, err := resolveModelCredential(ctx, ir, request, credentialID, BearerCredentialType); err != nil {
			return nil, err
		}
	}

	descriptor := modelDescriptorFor(ir, credentialID, BearerCredentialType,
		textValue(ir.Parameters["model"], "gpt-4o-mini"),
		textValue(ir.Parameters["baseUrl"], "https://api.openai.com/v1"))
	// Presence, not value. `temperature: 0` is a real instruction — it is what
	// a user asks for when they want deterministic extraction — and the old
	// `!= 0` guard dropped it, leaving the provider to apply its own default
	// on the one setting the user had been most explicit about.
	//
	// The collection is read first and the top-level fields second, so a
	// top-level value — the one this node's own form shows — wins. Reading the
	// collection at all is what an imported model needs: n8n stores the
	// sampling settings under `options`, the importer writes them back there,
	// and a node that only looked at the top level ran imported models with
	// the provider's defaults however the document was configured.
	options := mapValue(ir.Parameters[modelOptionsKey])
	copyPresentOptions(descriptor, options, samplingOptionKeys)
	copyPresentOptions(descriptor, ir.Parameters, samplingOptionKeys)
	// The transport options stay out of the request body: this server checks
	// the timeout against the deployment's ceiling and performs the retries.
	copyPresentOptions(descriptor, options, []string{ModelOptionTimeout, ModelOptionMaxRetries})
	return workflow.NodeOutput{{{JSON: map[string]any{descriptorKey: descriptor}}}}, nil
}

// executeProviderChatModel emits one provider's model descriptor.
func executeProviderChatModel(provider chatModelProvider) engine.ExecutorFunc {
	return func(ctx context.Context, ir workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		credentialID := strings.TrimSpace(ir.Credentials[provider.credentialType])
		if credentialID == "" {
			return nil, fmt.Errorf("node %q: a %s credential is required", ir.Name, provider.credentialType)
		}
		if _, err := resolveModelCredential(ctx, ir, request, credentialID, provider.credentialType); err != nil {
			return nil, err
		}

		modelName := locatorText(ir.Parameters["model"])
		if modelName == "" {
			modelName = provider.defaultModel
		}
		descriptor := modelDescriptorFor(ir, credentialID, provider.credentialType, modelName,
			textValue(ir.Parameters["baseUrl"], provider.baseURL))
		options := mapValue(ir.Parameters[modelOptionsKey])
		copyPresentOptions(descriptor, options, samplingOptionKeys)
		// The transport options are kept apart from the sampling ones because
		// this server acts on them itself rather than forwarding them: the
		// timeout is checked against the deployment's ceiling and the retries
		// are performed here, so neither may reach the provider's request body.
		copyPresentOptions(descriptor, options, []string{ModelOptionTimeout, ModelOptionMaxRetries})
		return workflow.NodeOutput{{{JSON: map[string]any{descriptorKey: descriptor}}}}, nil
	}
}

// resolveModelCredential resolves a chat model's credential and checks its type.
func resolveModelCredential(ctx context.Context, ir workflow.IRNode, request engine.Request, credentialID, want string) (engine.Credential, error) {
	if request.Credentials == nil {
		return engine.Credential{}, fmt.Errorf("node %q: credentials are not available in this runtime", ir.Name)
	}
	resolved, err := request.Credentials.ResolveCredential(ctx, credentialID)
	if err != nil {
		return engine.Credential{}, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if resolved.Type != want {
		return engine.Credential{}, fmt.Errorf("node %q: credential %q is a %s credential, not %s",
			ir.Name, resolved.Name, resolved.Type, want)
	}
	return resolved, nil
}

// modelDescriptorFor builds the part of a model descriptor every provider
// shares.
//
// It carries the credential ID, never the key: the descriptor travels in an
// item that is persisted in the execution record and streamed to the live feed.
// The agent re-resolves the secret when it actually calls the model.
func modelDescriptorFor(ir workflow.IRNode, credentialID, credentialType, modelName, baseURL string) map[string]any {
	return map[string]any{
		"kind":    "model",
		"model":   modelName,
		"baseUrl": baseURL,
		// The ID the node named, so the agent re-resolves exactly what this
		// node was configured with rather than whatever the store echoed back.
		"credentialId": credentialID,
		// The type, so the agent knows which field of the resolved credential
		// holds the key without guessing at field names.
		"credentialType": credentialType,
		// Absent means the definition's default, which is on: the editor shows
		// "Stream output" enabled for a node that never set the key, and an
		// untouched or imported node must run the way it is shown.
		"stream": boolOr(ir.Parameters, "stream", true),
	}
}

// copyPresentOptions carries across only the options the user actually set.
//
// Presence is the whole point: a numeric option that is absent must reach the
// provider as nothing at all, and one that is present must reach it even when
// it is zero. Collapsing the two — which a `!= 0` guard does — makes
// `temperature: 0` unexpressible.
func copyPresentOptions(descriptor map[string]any, source map[string]any, keys []string) {
	for _, key := range keys {
		value, present := source[key]
		if !present || value == nil {
			continue
		}
		descriptor[key] = numberValue(value)
	}
}

// executeMemory emits a memory descriptor.
//
// The descriptor carries the node's raw parameters rather than a resolved
// session key, because this node runs before the trigger and before the nodes
// upstream of the agent: the item that `$('Trigger').item.json.chatId` or
// `{{ $json.sessionId }}` names does not exist yet, and the execution input is
// not what the agent is answering. The agent resolves these parameters against
// its own current item, which is also what gives every item of a batch its own
// conversation.
func executeMemory(ctx context.Context, ir workflow.IRNode, _ workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parameters := make(map[string]any, len(ir.Parameters))
	for key, value := range ir.Parameters {
		parameters[key] = value
	}
	return workflow.NodeOutput{{{JSON: map[string]any{descriptorKey: map[string]any{
		"kind": "memory",
		// The node's own name, so the fromInput suffix still scopes two
		// memory nodes apart once the key is resolved downstream.
		"nodeName":   ir.Name,
		"parameters": parameters,
	}}}}}, nil
}

// resolveMemorySession evaluates a memory node's parameters against the agent's
// current item and returns the conversation key and the retention bounds.
func resolveMemorySession(agentNode string, descriptor map[string]any, item workflow.Item, input workflow.NodeInput, request engine.Request, index int) (string, ai.Retention, error) {
	raw, _ := descriptor["parameters"].(map[string]any)
	if len(raw) == 0 {
		return "", ai.Retention{}, fmt.Errorf("the memory node attached to node %q has no parameters", agentNode)
	}
	resolved, err := expression.Resolve(raw, expressionContext(item, input, request, index))
	if err != nil {
		return "", ai.Retention{}, err
	}
	memoryNode := textValue(descriptor["nodeName"], agentNode)
	sessionID, err := resolveSessionKey(memoryNode, resolved)
	if err != nil {
		return "", ai.Retention{}, fmt.Errorf("memory node %q: %w", memoryNode, err)
	}
	return sessionID, ai.Retention{
		MaxMessages: int(numberValue(resolved["maxMessages"])),
		MaxAge:      time.Duration(numberValue(resolved["maxAgeMinutes"])) * time.Minute,
	}, nil
}

// resolveSessionKey computes the conversation key a memory node addresses.
//
// A node saved before the session key modes existed keeps the exact key it
// always used — no auto-scoping suffix is added, so its history stays where
// it is. A fromInput key is auto-scoped to this memory node, so two memory
// nodes in one workflow never silently share a conversation; to share one on
// purpose, use "Define below" with the same key in each node, which is also
// the documented way out of the suffix.
func resolveSessionKey(nodeName string, parameters map[string]any) (string, error) {
	mode := textValue(parameters["sessionIdType"], "")
	key := strings.TrimSpace(textValue(parameters["sessionKey"], ""))
	switch mode {
	case sessionIDTypeCustomKey:
		if key == "" {
			return "", fmt.Errorf("sessionKey resolved to an empty value")
		}
		return key, nil
	case sessionIDTypeFromInput:
		if key == "" {
			return "", fmt.Errorf("sessionKey resolved to an empty value")
		}
		return key + "__" + sanitizeSessionKey(nodeName), nil
	default:
		if key != "" {
			return key, nil
		}
		if legacy := strings.TrimSpace(textValue(parameters["sessionId"], "")); legacy != "" {
			return legacy, nil
		}
		return "", fmt.Errorf("sessionId resolved to an empty value")
	}
}

// executeHTTPTool emits a tool descriptor built from HTTP Request parameters.
func executeHTTPTool(ctx context.Context, ir workflow.IRNode, _ workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parameters := make(map[string]any, len(ir.Parameters))
	for key, value := range ir.Parameters {
		parameters[key] = value
	}
	credentials := make(map[string]any, len(ir.Credentials))
	for typeID, id := range ir.Credentials {
		credentials[typeID] = id
	}
	return workflow.NodeOutput{{{JSON: map[string]any{descriptorKey: map[string]any{
		"kind":        "tool",
		"name":        toolNameFor(ir),
		"description": textValue(ir.Parameters["toolDescription"], ""),
		"nodeName":    ir.Name,
		"parameters":  parameters,
		"credentials": credentials,
	}}}}}, nil
}

// toolNameFor is the model-facing name one tool descriptor claims. An
// explicit override wins; otherwise the name derives from the connected
// node's canvas name, so two tools left at their defaults never claim one
// name. A toolName that is still an expression is refused: the descriptor is
// built once upstream with no item to evaluate it against, and a silently
// substituted default would call the tool by a name the author never chose.
func toolNameFor(ir workflow.IRNode) string {
	if override := strings.TrimSpace(textValue(ir.Parameters["toolName"], "")); override != "" {
		return override
	}
	return NormalizeToolName(ir.Name)
}

// CheckDuplicateToolNames refuses two tools claiming one model-facing name,
// naming both canvas nodes. It runs in the agent before any model call so a
// graph that reached the runner still fails fast instead of mid-run; the
// compiler should call it too, where it can refuse the graph before
// activation.
func CheckDuplicateToolNames(descriptors []map[string]any) error {
	seen := make(map[string]string, len(descriptors))
	for _, descriptor := range descriptors {
		name := textValue(descriptor["name"], "")
		if name == "" {
			continue
		}
		nodeName := textValue(descriptor["nodeName"], name)
		if first, duplicate := seen[name]; duplicate {
			return fmt.Errorf("tool name %q is used by both node %q and node %q; rename one of them", name, first, nodeName)
		}
		seen[name] = nodeName
	}
	return nil
}

// AgentExecutor runs the AI Agent node.
//
// It depends on ai.AgentRuntime and ai.ChatModel, never on a provider type, so
// the runtime behind it is replaceable without touching this node.
type AgentExecutor struct {
	runtime ai.AgentRuntime
	modelBackend
	memory ai.Memory
	// httpTool is the same executor the HTTP Request node uses, so exposing an
	// HTTP call as a tool reuses that implementation rather than copying it.
	httpTool *HTTPExecutor
	// mcp is the shared transport MCP tools call through, so every tools/call
	// round trip inherits the deployment's SSRF policy rather than building
	// a client of its own that would quietly lose it.
	mcp *MCPClientToolExecutor
	// datastore is the row store the datastore tool reads through. The tenant
	// always arrives from the execution, never from the document or the
	// model's arguments, so one tenant's tool call can never address another
	// tenant's table. Nil refuses the tool with the install message rather
	// than dereferencing, the way the data-table node does.
	datastore DatastoreStore
	vectors   VectorStore
	embedder  *EmbeddingsExecutor
	sqlGuard  sqlnode.Guard
}

// modelBackend binds a deployment's outbound policy for model calls. Both the
// agent and the chain resolve their model through it, so the descriptor's
// credential handling — the id travels in the item, the key never does — has
// one home instead of one copy per consumer.
type modelBackend struct {
	client *http.Client
	// policy is kept so a model's base URL gets the same pre-flight gate an
	// HTTP Request node's URL does, before a socket is opened.
	policy safehttp.Policy
	// fallbackTimeout is what a model node that names none waits, so a node
	// saved before the option existed behaves exactly as it did.
	fallbackTimeout time.Duration
	// timeoutCeiling is the longest this deployment lets one model node wait.
	timeoutCeiling time.Duration
}

// newModelBackend builds the backend from the deployment's outbound policy.
//
// The model client carries the deployment's own policy with its clock
// removed, not a second policy. http.Client.Timeout is a ceiling a context
// deadline can only lower, so leaving the outbound default of thirty seconds
// on it would make every longer per-node timeout unreachable — a slow
// completion would die as a transport error rather than as a model error.
// Everything that defends the call is unchanged: CheckAddress runs in the
// same dialer on every hop, and CheckRedirect applies the same allowlist,
// because both close over this same Policy value.
func newModelBackend(policy safehttp.Policy) modelBackend {
	modelPolicy := policy
	modelPolicy.Timeout = 0
	return modelBackend{
		// Model calls go through the deployment's outbound policy exactly as
		// an HTTP Request node's do, so a model consumer cannot reach an
		// internal address the rest of the product refuses.
		client:          safehttp.NewClient(modelPolicy),
		policy:          policy,
		fallbackTimeout: policy.Timeout,
		timeoutCeiling:  DefaultModelTimeoutCeiling,
	}
}

// resolvedModel is one model descriptor bound to a deployment: the client to
// call it with, the name to bill it under, and the bounds to run it in.
type resolvedModel struct {
	model            ai.ChatModel
	name             string
	timeout          time.Duration
	stream           bool
	maxTokens        int
	maxRetries       int
	temperature      *float64
	topP             *float64
	frequencyPenalty *float64
	presencePenalty  *float64
}

// resolveModel performs the steps every model consumer repeats: read the
// descriptor from the typed port, re-resolve the secret through the runtime,
// and bind the deployment's outbound policy to the credential's own domain
// scope.
func (backend *modelBackend) resolveModel(ctx context.Context, nodeName string, descriptor map[string]any, request engine.Request) (resolvedModel, error) {
	credentialID, _ := descriptor["credentialId"].(string)
	// No credential is the local-endpoint case: the request goes out with no
	// Authorization header rather than with a fake token.
	apiKey := ""
	secret := engine.Credential{}
	if credentialID != "" {
		if request.Credentials == nil {
			return resolvedModel{}, fmt.Errorf("node %q: credentials are not available in this runtime", nodeName)
		}
		var err error
		secret, err = request.Credentials.ResolveCredential(ctx, credentialID)
		if err != nil {
			return resolvedModel{}, fmt.Errorf("node %q: %w", nodeName, err)
		}
		apiKeyField, usable := modelAPIKeyFields[secret.Type]
		if !usable {
			return resolvedModel{}, fmt.Errorf("node %q: credential %q is a %s credential, which no chat model can authenticate with",
				nodeName, secret.Name, secret.Type)
		}
		apiKey = secret.Fields[apiKeyField]
	}

	baseURL := textValue(descriptor["baseUrl"], "https://api.openai.com/v1")
	target, err := url.Parse(baseURL)
	if err != nil {
		return resolvedModel{}, fmt.Errorf("node %q: the model's base URL is not a valid URL", nodeName)
	}
	// The same pre-flight gate the HTTP node applies. The dialer refuses a
	// private address on its own, but only once DNS has answered — so a
	// disallowed host that does not resolve would fail as a lookup error rather
	// than as the policy refusal it is.
	if err := backend.policy.CheckURL(target); err != nil {
		return resolvedModel{}, fmt.Errorf("node %q: %w", nodeName, err)
	}
	// A model call is an outbound request carrying a secret, so the
	// credential's own domain scope binds it exactly as it binds an HTTP
	// Request node's. It did not before: a credential scoped to
	// api.openai.com could be pointed at any host by editing one parameter
	// on the model node.
	if credentialID != "" && !secret.AllowsHost(target.Host) {
		return resolvedModel{}, fmt.Errorf("node %q: credential %q is not allowed for host %q",
			nodeName, secret.Name, target.Hostname())
	}

	timeout, err := backend.modelTimeout(descriptor)
	if err != nil {
		return resolvedModel{}, fmt.Errorf("node %q: %w", nodeName, err)
	}
	// A node that never set the option keeps n8n's own default of two
	// retries rather than none: imported workflows rarely set it, and the
	// first rate limit would otherwise end the run.
	maxRetries := defaultModelMaxRetries
	if value, present := descriptor[ModelOptionMaxRetries]; present && value != nil {
		maxRetries = positiveInt(numberValue(value))
	}
	model := ai.NewOpenAICompatible(backend.client, baseURL, apiKey)
	if credentialID != "" {
		// The host check above covers the base URL; the scope carries the
		// same bound through every redirect the call meets, holds a
		// credential that names no domains to this host, and refuses a step
		// down to plain http, where the key would travel in the clear.
		model.WithCredentialScope(secret.RedirectScope())
	}
	return resolvedModel{
		model:   model,
		name:    textValue(descriptor["model"], "gpt-4o-mini"),
		timeout: timeout,
		stream:  boolValue(descriptor["stream"]),
		// A model's own -1 means "as many as the model allows", which is
		// said by sending nothing rather than by sending a negative bound
		// the provider would refuse.
		maxTokens:  positiveInt(numberValue(descriptor[ModelOptionMaxTokens])),
		maxRetries: maxRetries,
		// Read back by presence for the reason they were written by
		// presence: zero is a value a user chooses, not the absence of one.
		temperature:      presentNumber(descriptor, ModelOptionTemperature),
		topP:             presentNumber(descriptor, ModelOptionTopP),
		frequencyPenalty: presentNumber(descriptor, ModelOptionFrequencyPenalty),
		presencePenalty:  presentNumber(descriptor, ModelOptionPresencePenalty),
	}, nil
}

// NewAgentExecutor builds the agent node's executor.
func NewAgentExecutor(runtime ai.AgentRuntime, policy safehttp.Policy, memory ai.Memory, options ...AgentOption) *AgentExecutor {
	if runtime == nil {
		runtime = ai.NewLoopRuntime()
	}
	executor := &AgentExecutor{
		runtime:      runtime,
		modelBackend: newModelBackend(policy),
		memory:       memory,
		httpTool:     NewHTTPExecutor(policy),
		mcp:          NewMCPClientToolExecutor(policy),
	}
	for _, option := range options {
		option(executor)
	}
	return executor
}

// AgentOption carries a deployment decision the agent executor needs.
type AgentOption func(*AgentExecutor)

// WithModelTimeoutCeiling bounds how long any one model node may wait.
//
// A deployment decision like the database ceiling beside it: a document may ask
// for less, never for more, and asking for more is refused rather than clamped.
func WithModelTimeoutCeiling(ceiling time.Duration) AgentOption {
	return func(executor *AgentExecutor) {
		if ceiling > 0 {
			executor.timeoutCeiling = ceiling
		}
	}
}

// WithDatastoreStore hands the agent's datastore tool its row store. Without
// it the tool refuses every call with the install message rather than
// dereferencing, the way the data-table node does without its engine.
func WithDatastoreStore(store DatastoreStore) AgentOption {
	return func(executor *AgentExecutor) {
		executor.datastore = store
	}
}

// WithAgentVectorStore hands retrieve-as-tool the install's internal store
// and the embeddings client used to embed the agent's query.
func WithAgentVectorStore(store VectorStore, embedder *EmbeddingsExecutor) AgentOption {
	return func(executor *AgentExecutor) {
		executor.vectors = store
		executor.embedder = embedder
	}
}

// WithAgentSQLGuard is the same database guard SQL nodes use, so a customer
// PGVector tool cannot open KilasFlow's own database.
func WithAgentSQLGuard(guard sqlnode.Guard) AgentOption {
	return func(executor *AgentExecutor) {
		executor.sqlGuard = guard
	}
}

// modelTimeout resolves how long one model request may wait.
//
// It is a request bound, not a run bound: the adapter applies it to each
// attempt, so an agent that takes three tool turns makes three requests and
// each of them is allowed this long — which is what a provider's timeout
// option means there too. The whole run stays bounded by the deployment's
// ceiling (see AgentExecutor.runTimeout).
func (backend *modelBackend) modelTimeout(descriptor map[string]any) (time.Duration, error) {
	ceiling := backend.timeoutCeiling
	if ceiling <= 0 {
		ceiling = DefaultModelTimeoutCeiling
	}
	// Milliseconds, because that is the unit the option is expressed in and
	// interoperating with the format means keeping its unit too.
	milliseconds := numberValue(descriptor[ModelOptionTimeout])
	if milliseconds <= 0 {
		if backend.fallbackTimeout > 0 {
			return backend.fallbackTimeout, nil
		}
		return ceiling, nil
	}
	requested := time.Duration(milliseconds) * time.Millisecond
	if requested > ceiling {
		return 0, fmt.Errorf("%w: the node asks for %s and this deployment allows %s",
			ErrModelTimeoutAboveCeiling, requested, ceiling)
	}
	return requested, nil
}

// Execute runs the agent once per incoming item.
func (executor *AgentExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	modelDescriptor, found, err := soleDescriptor(input["model"], "model")
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if !found {
		return nil, fmt.Errorf("node %q: connect an OpenAI Chat Model to the model port", ir.Name)
	}
	memoryDescriptor, hasMemory, err := soleDescriptor(input["memory"], "memory")
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	// Uncapped on purpose, and order-stable: the runner sorts a node's incoming
	// edges before it assembles input, so N tools arrive as N descriptors in the
	// same order on every run.
	toolDescriptors := descriptorsFrom(input["tools"])

	// Two tools under one name would fail mid-run in the loop; refuse them
	// here instead, naming both nodes while no model call has happened yet.
	if err := CheckDuplicateToolNames(toolDescriptors); err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}

	model, err := executor.resolveModel(ctx, ir.Name, modelDescriptor, request)
	if err != nil {
		return nil, err
	}
	tools := make([]ai.Tool, 0, len(toolDescriptors))

	for _, descriptor := range toolDescriptors {
		tool, err := executor.toolFrom(ir, descriptor, request)
		if err != nil {
			return nil, err
		}
		tools = append(tools, tool)
	}

	// The parser slot is capped at one connection like the model and memory
	// slots; two descriptors here means a graph bypassed the compiler.
	parserSchema, parserRetries, hasParser, err := parserSlot(input["outputParser"], ir.Name)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}

	items := input["main"]
	if len(items) == 0 {
		items = []workflow.Item{{JSON: map[string]any{}}}
	}
	out := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		parameters, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}

		// systemPrompt stays as an alias for graphs saved before the system
		// message existed; where both are set, the message wins.
		systemPrompt := textValue(parameters["systemMessage"], "")
		if systemPrompt == "" {
			systemPrompt = textValue(parameters["systemPrompt"], "")
		}
		agentRequest := ai.AgentRequest{
			Model:            model.model,
			ModelName:        model.name,
			SystemPrompt:     systemPrompt,
			Input:            textValue(parameters["prompt"], ""),
			Tools:            tools,
			MaxIterations:    int(numberValue(parameters["maxIterations"])),
			MaxTokens:        model.maxTokens,
			MaxRetries:       model.maxRetries,
			Stream:           model.stream || boolValue(parameters["enableStreaming"]),
			OutputSchema:     parserSchema,
			OutputMaxRetries: parserRetries,
			// Read back by presence for the reason they were written by
			// presence: zero is a value a user chooses, not the absence of one.
			Temperature:      model.temperature,
			TopP:             model.topP,
			FrequencyPenalty: model.frequencyPenalty,
			PresencePenalty:  model.presencePenalty,
		}
		if hasMemory && executor.memory != nil {
			sessionID, policy, err := resolveMemorySession(ir.Name, memoryDescriptor, item, input, request, index)
			if err != nil {
				return nil, fmt.Errorf("node %q: %w", ir.Name, err)
			}
			agentRequest.Memory = executor.memory
			agentRequest.Session = ai.SessionKey{
				TenantID:   request.Execution.TenantID,
				WorkflowID: request.Execution.WorkflowID,
				SessionID:  sessionID,
			}
			// The bounds the memory node declared travel beside the key, so
			// two memory nodes with different bounds retain different
			// amounts. Zero means the memory's own defaults.
			agentRequest.SessionPolicy = policy
		}

		// The parameter's declared default is on, so a document that never
		// wrote it behaves the way the node describes. The editor writes
		// defaults into the document and an import does not, and the node must
		// mean the same thing either way.
		if boolOr(parameters, "passthroughBinaryImages", true) {
			images, err := inputImages(request, item)
			if err != nil {
				return nil, fmt.Errorf("node %q: %w", ir.Name, err)
			}
			agentRequest.Images = images
		}
		// One model request, not the whole run: an agent that takes three tool
		// turns makes three requests and each of them gets this long. The run
		// as a whole stays bounded by the deployment's ceiling.
		agentRequest.RequestTimeout = model.timeout

		events := make([]any, 0, 8)
		// Cancelled explicitly rather than deferred: this is a loop, and a
		// deferred cancel would hold every item's context alive until the whole
		// node finished.
		runCtx, cancel := context.WithTimeout(ctx, executor.runTimeout())
		stream := newDeltaCoalescer(ir.ID, request, func(event ai.Event) {
			events = append(events, event)
			request.Events.Emit(engine.NodeEvent{
				NodeID: ir.ID, Name: string(event.Kind), Detail: eventDetail(event),
			})
		})
		result, err := executor.runtime.Run(runCtx, agentRequest, stream.emit)
		stream.flush()
		cancel()
		if err != nil {
			// A run that hit the ceiling names the bound that ended it. The
			// chat model node's Timeout option was the wrong name: that option
			// bounds one request, and a node asking for more than the ceiling
			// is refused before it runs — so pointing at it sent the user to a
			// setting that cannot raise the bound they had reached.
			//
			// runCtx inherits the execution's own deadline, which is usually
			// much shorter than the ceiling, so an expired runCtx alone does not
			// say which bound fired. Only when the caller's context is still
			// alive was it the ceiling.
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("node %q: the workflow's execution time limit ended the agent before it finished; raise the workflow's executionTimeout setting (or the deployment's execution.default_timeout) to allow longer runs: %w", ir.Name, err)
			}
			if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("node %q: the agent did not finish within the deployment's %s model timeout ceiling; that ceiling is a deployment-level bound and no node option raises it: %w", ir.Name, executor.runTimeout(), err)
			}
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}

		outputValue := any(result.Output)
		// A run that stopped at the iteration bound answered with the stated
		// fallback, not with the parser's schema, so there is nothing to parse:
		// unmarshalling that sentence failed the node with "parser output is not
		// valid JSON" on a run the loop had already decided was a success. The
		// fallback is returned as it stands, the way a graph without a parser
		// would return it.
		if hasParser && !result.MaxIterationsReached {
			// The loop validated this against the schema; a parse failure
			// here means a graph bypassed the runner, and failing names it.
			var parsed any
			if err := json.Unmarshal([]byte(result.Output), &parsed); err != nil {
				return nil, fmt.Errorf("node %q: parser output is not valid JSON: %w", ir.Name, err)
			}
			outputValue = parsed
		}
		payload := map[string]any{
			"output":     outputValue,
			"usage":      map[string]any{"promptTokens": float64(result.Usage.PromptTokens), "completionTokens": float64(result.Usage.CompletionTokens), "totalTokens": float64(result.Usage.TotalTokens)},
			"iterations": float64(result.Iterations),
			"toolCalls":  float64(result.ToolCalls),
		}
		// Off means byte-identical to the output this node always produced:
		// the key is added, never removed, and nothing else moves. On means
		// n8n's step list — {action, observation} per tool call — rather than
		// the raw message list, which echoed the system prompt into the output
		// of every workflow that asked for it.
		if boolValue(parameters["returnIntermediateSteps"]) {
			payload["intermediateSteps"] = agentSteps(result.Messages)
		}
		output := workflow.Item{JSON: payload}
		if boolOr(parameters, "passthroughBinaryImages", true) && len(item.Binary) > 0 {
			binary := make(map[string]workflow.BinaryRef, len(item.Binary))
			for name, ref := range item.Binary {
				binary[name] = ref
			}
			output.Binary = binary
		}
		out = append(out, output)
	}
	return workflow.NodeOutput{out}, nil
}

// runTimeout is the deployment's ceiling on one node's model work. The chat
// model node's Timeout option bounds one request (AgentRequest.RequestTimeout);
// this is the bound that guarantees the node ends, so a model whose tools keep
// failing cannot run until the execution timeout.
func (backend *modelBackend) runTimeout() time.Duration {
	if backend.timeoutCeiling > 0 {
		return backend.timeoutCeiling
	}
	return DefaultModelTimeoutCeiling
}

// deltaInterval is how often accumulated streamed tokens are published.
const deltaInterval = 250 * time.Millisecond

// deltaCoalescer bounds how often streamed tokens reach the execution feed.
//
// The model stream is one delta per token. Published one event each, a
// few-hundred-word answer filled the execution's bounded event history with
// tokens and pushed out the tool and model events that describe the run, so a
// client that opened the execution mid-stream saw tokens and never the
// terminal event. Tokens are still delivered — just not as fast as the model
// produces them.
type deltaCoalescer struct {
	nodeID    string
	request   engine.Request
	emitEvent func(ai.Event)
	interval  time.Duration
	last      time.Time
	pending   strings.Builder
	model     string
	iteration int
}

func newDeltaCoalescer(nodeID string, request engine.Request, emit func(ai.Event)) *deltaCoalescer {
	return &deltaCoalescer{nodeID: nodeID, request: request, emitEvent: emit, interval: deltaInterval}
}

// emit is the sink the runtime writes to.
func (coalescer *deltaCoalescer) emit(event ai.Event) {
	if event.Kind != ai.EventModelDelta {
		coalescer.flush()
		coalescer.emitEvent(event)
		return
	}
	coalescer.pending.WriteString(event.Delta)
	coalescer.model = event.Model
	coalescer.iteration = event.Iteration
	if coalescer.last.IsZero() || time.Since(coalescer.last) >= coalescer.interval {
		coalescer.flush()
	}
}

// flush publishes what has accumulated. It runs before every non-delta event
// and once when the run ends, so the deltas a consumer sees are never missing
// their tail.
func (coalescer *deltaCoalescer) flush() {
	if coalescer.pending.Len() == 0 {
		return
	}
	coalescer.emitEvent(ai.Event{
		Kind: ai.EventModelDelta, Iteration: coalescer.iteration, Model: coalescer.model,
		Delta: coalescer.pending.String(),
	})
	coalescer.pending.Reset()
	coalescer.last = time.Now()
}

// Vision bounds. A picture travels base64-encoded inside the request, so an
// unbounded attachment is a memory and bandwidth problem rather than a feature.
const (
	maxVisionImages = 4
	maxVisionBytes  = 8 << 20
)

// inputImages turns an item's image attachments into the data URIs the model
// reads. Anything that is not an image is skipped: an image part built from a
// PDF fails the whole request.
func inputImages(request engine.Request, item workflow.Item) ([]string, error) {
	if len(item.Binary) == 0 || request.Binaries == nil {
		return nil, nil
	}
	names := make([]string, 0, len(item.Binary))
	for name := range item.Binary {
		names = append(names, name)
	}
	// Sorted, so an item with several attachments sends them in the same
	// order on every run rather than in map order.
	sort.Strings(names)
	images := make([]string, 0, min(len(names), maxVisionImages))
	for _, name := range names {
		if len(images) >= maxVisionImages {
			break
		}
		reference := item.Binary[name]
		if !strings.HasPrefix(reference.MediaType, "image/") {
			continue
		}
		body, resolved, err := request.Binaries.Get(reference.ID)
		if err != nil {
			return nil, fmt.Errorf("read image %q: %w", name, err)
		}
		contents, readErr := io.ReadAll(io.LimitReader(body, maxVisionBytes+1))
		body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read image %q: %w", name, readErr)
		}
		if len(contents) > maxVisionBytes {
			continue
		}
		mediaType := resolved.MediaType
		if mediaType == "" {
			mediaType = reference.MediaType
		}
		images = append(images, "data:"+mediaType+";base64,"+base64.StdEncoding.EncodeToString(contents))
	}
	return images, nil
}

// agentStep is one intermediate step in n8n's shape: the tool the model asked
// for, what it asked with, and what came back.
type agentStep struct {
	Action      map[string]any `json:"action"`
	Observation string         `json:"observation,omitempty"`
}

// agentSteps pairs every tool call in a run with its result, in call order.
// The parser's synthetic format tool is left out: it is how a run ends, not a
// step of it.
func agentSteps(messages []ai.Message) []agentStep {
	observations := map[string]string{}
	for _, message := range messages {
		if message.Role == ai.RoleTool {
			observations[message.ToolCallID] = message.Content
		}
	}
	steps := make([]agentStep, 0, len(observations))
	for _, message := range messages {
		if message.Role != ai.RoleAssistant {
			continue
		}
		for _, call := range message.ToolCalls {
			if call.Name == ai.FormatFinalJSONResponse {
				continue
			}
			var input any = string(call.Arguments)
			if len(call.Arguments) > 0 && json.Valid(call.Arguments) {
				var decoded any
				if err := json.Unmarshal(call.Arguments, &decoded); err == nil {
					input = decoded
				}
			}
			steps = append(steps, agentStep{
				Action: map[string]any{
					"tool": call.Name, "toolInput": input, "toolCallId": call.ID,
					"log": call.Name, "messageLog": message.Content,
				},
				Observation: observations[call.ID],
			})
		}
	}
	return steps
}

// ChainExecutor runs the Basic LLM Chain node: exactly one model call per
// item, with no tool loop and no memory slot. It drives ai.ChatModel
// directly rather than through the agent runtime, which would hand it a loop
// it must not have.
type ChainExecutor struct {
	models modelBackend
}

// NewChainExecutor builds the chain node's executor.
func NewChainExecutor(policy safehttp.Policy, ceiling time.Duration) *ChainExecutor {
	backend := newModelBackend(policy)
	if ceiling > 0 {
		backend.timeoutCeiling = ceiling
	}
	return &ChainExecutor{models: backend}
}

// Execute runs the chain once per incoming item.
func (executor *ChainExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	modelDescriptor, found, err := soleDescriptor(input["model"], "model")
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if !found {
		return nil, fmt.Errorf("node %q: connect a chat model to the model port", ir.Name)
	}
	parserSchema, parserRetries, hasParser, err := parserSlot(input["outputParser"], ir.Name)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	// The model descriptor is read from the model port by name, never from
	// main: on a well-formed graph main carries items, not descriptors, and
	// on a malformed one it carries something surprising.
	model, err := executor.models.resolveModel(ctx, ir.Name, modelDescriptor, request)
	if err != nil {
		return nil, err
	}

	items := input["main"]
	if len(items) == 0 {
		items = []workflow.Item{{JSON: map[string]any{}}}
	}
	out := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		parameters, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		messages, err := chainMessagesForItem(ir.Name, parameters, item)
		if err != nil {
			return nil, err
		}
		if hasParser {
			// The schema goes to the model up front, the way n8n's parser
			// appends its format instructions to the prompt. Validating a
			// free-text answer and then repairing it burns model calls, and on
			// a small local model it usually fails outright.
			messages = withFormatInstructions(messages, ai.FormatInstructions(parserSchema))
		}
		modelRequest := ai.ModelRequest{
			Model:            model.name,
			Messages:         messages,
			Temperature:      model.temperature,
			TopP:             model.topP,
			FrequencyPenalty: model.frequencyPenalty,
			PresencePenalty:  model.presencePenalty,
			MaxTokens:        model.maxTokens,
			MaxRetries:       model.maxRetries,
			Timeout:          model.timeout,
		}
		// Cancelled explicitly rather than deferred: this is a loop, and a
		// deferred cancel would hold every item's context alive until the
		// whole node finished.
		output, err := executor.completeChainItem(ctx, ir, request, model, modelRequest, parserSchema, parserRetries, hasParser)
		if err != nil {
			return nil, err
		}
		out = append(out, output)
	}
	return workflow.NodeOutput{out}, nil
}

// completeChainItem runs one item's model call, validating against the
// parser schema when one is attached. A response that fails validation is
// retried a bounded number of times with the failure fed back, and then
// fails carrying both the validation error and the raw text.
func (executor *ChainExecutor) completeChainItem(ctx context.Context, ir workflow.IRNode, request engine.Request, model resolvedModel, modelRequest ai.ModelRequest, parserSchema map[string]any, parserRetries int, hasParser bool) (workflow.Item, error) {
	messages := modelRequest.Messages
	var lastErr error
	attempts := 1
	if hasParser {
		attempts += parserRetries
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		modelRequest.Messages = messages
		runCtx, cancel := context.WithTimeout(ctx, executor.models.runTimeout())
		request.Events.Emit(engine.NodeEvent{
			NodeID: ir.ID, Name: string(ai.EventModelStarted),
			Detail: eventDetail(ai.Event{Kind: ai.EventModelStarted, Iteration: attempt, Model: model.name}),
		})
		var response ai.ModelResponse
		var err error
		if model.stream {
			response, err = model.model.Stream(runCtx, modelRequest, func(delta string) {
				if delta == "" {
					return
				}
				request.Events.Emit(engine.NodeEvent{
					NodeID: ir.ID, Name: string(ai.EventModelDelta),
					Detail: eventDetail(ai.Event{Kind: ai.EventModelDelta, Iteration: attempt, Model: model.name, Delta: delta}),
				})
			})
		} else {
			response, err = model.model.Complete(runCtx, modelRequest)
		}
		cancel()
		if err != nil {
			request.Events.Emit(engine.NodeEvent{
				NodeID: ir.ID, Name: string(ai.EventAgentFailed),
				Detail: eventDetail(ai.Event{Kind: ai.EventAgentFailed, Iteration: attempt, Model: model.name, Error: err.Error()}),
			})
			return workflow.Item{}, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		usage := response.Usage
		request.Events.Emit(engine.NodeEvent{
			NodeID: ir.ID, Name: string(ai.EventModelCompleted),
			Detail: eventDetail(ai.Event{Kind: ai.EventModelCompleted, Iteration: attempt, Model: model.name, Usage: &usage}),
		})
		if !hasParser {
			// n8n's Basic LLM Chain answers with `text` when no parser is
			// attached, and every imported `{{ $json.text }}` downstream
			// reads that field. Answering with `output` alone made those
			// references resolve to nothing while the run reported success.
			return workflow.Item{JSON: map[string]any{"text": response.Message.Content}}, nil
		}
		value, validationErr := ai.ParseAndValidateOutput(parserSchema, response.Message.Content)
		if validationErr == nil {
			return workflow.Item{JSON: map[string]any{"output": value}}, nil
		}
		lastErr = validationErr
		if attempt >= attempts {
			break
		}
		// The repair turn carries the whole schema rather than one validation
		// error: a model guessing field names one error at a time spends every
		// retry it has.
		messages = append(messages,
			ai.Message{Role: ai.RoleAssistant, Content: response.Message.Content},
			ai.Message{Role: ai.RoleUser, Content: "That response did not match the required format: " + validationErr.Error() + "\n" + ai.FormatInstructions(parserSchema)},
		)
	}
	request.Events.Emit(engine.NodeEvent{
		NodeID: ir.ID, Name: string(ai.EventAgentFailed),
		Detail: eventDetail(ai.Event{Kind: ai.EventAgentFailed, Iteration: attempts, Model: model.name, Error: lastErr.Error()}),
	})
	return workflow.Item{}, fmt.Errorf("node %q: output did not match the required format: %w", ir.Name, lastErr)
}

// chainMessagesForItem builds one item's prompt: the ordered message rows
// first, then the user message from the field or the defined text.
func chainMessagesForItem(nodeName string, parameters map[string]any, item workflow.Item) ([]ai.Message, error) {
	rows, err := chainMessageRows(parameters["messages"])
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", nodeName, err)
	}
	messages := make([]ai.Message, 0, len(rows)+1)
	for index, row := range rows {
		var role ai.Role
		switch row.Type {
		case chainRoleSystem, "":
			role = ai.RoleSystem
		case chainRoleHuman:
			role = ai.RoleUser
		case chainRoleAI:
			role = ai.RoleAssistant
		default:
			return nil, fmt.Errorf("node %q: message %d has type %q, want system, human, or ai", nodeName, index+1, row.Type)
		}
		if strings.TrimSpace(row.Text) == "" {
			return nil, fmt.Errorf("node %q: message %d has empty text", nodeName, index+1)
		}
		messages = append(messages, ai.Message{Role: role, Content: row.Text})
	}
	prompt := ""
	if textValue(parameters["promptType"], chainPromptAuto) == chainPromptDefine {
		prompt = textValue(parameters["text"], "")
	} else {
		field := textValue(parameters["inputField"], "chatInput")
		if field == "" {
			field = "chatInput"
		}
		prompt, _ = item.JSON[field].(string)
	}
	if strings.TrimSpace(prompt) != "" {
		messages = append(messages, ai.Message{Role: ai.RoleUser, Content: prompt})
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("node %q: the prompt is empty; provide text or at least one message", nodeName)
	}
	return messages, nil
}

// withFormatInstructions states the required output shape in the last human
// turn, which is what n8n does and what every provider accepts. A trailing
// system message would be refused by the providers that only allow one at the
// start.
func withFormatInstructions(messages []ai.Message, instructions string) []ai.Message {
	if instructions == "" {
		return messages
	}
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role != ai.RoleUser {
			continue
		}
		last := messages[index]
		last.Content = last.Content + "\n\n" + instructions
		messages[index] = last
		return messages
	}
	return append(messages, ai.Message{Role: ai.RoleUser, Content: instructions})
}

// chainMessageRow is one typed row of the chain's message list.
type chainMessageRow struct {
	Type string
	Text string
}

// chainMessageRows reads the stored messages fixedCollection. An
// expression-valued collection is left for run time; anything else shaped
// wrong is refused here rather than misread there.
func chainMessageRows(value any) ([]chainMessageRow, error) {
	if value == nil || expression.IsExpression(value) {
		return nil, nil
	}
	collection, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("messages must be a fixed collection of message rows")
	}
	raw, present := collection["messageValues"]
	if !present {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("messages.messageValues must be a list of message rows")
	}
	rows := make([]chainMessageRow, 0, len(list))
	for _, entry := range list {
		fields, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("messages.messageValues must be a list of message rows")
		}
		rows = append(rows, chainMessageRow{Type: chainLiteral(fields["type"]), Text: chainLiteral(fields["message"])})
	}
	return rows, nil
}

// chainLiteral reads a fixed-collection cell, treating an expression as
// present-but-unknown: static validation cannot judge it, run time resolves
// it and refuses the empty result there.
func chainLiteral(value any) string {
	if expression.IsExpression(value) {
		return "expression"
	}
	text, _ := value.(string)
	return text
}

func validateChainConfiguration(n workflow.Node) error {
	promptType := statementText(n.Parameters, "promptType")
	switch promptType {
	case "", chainPromptAuto, chainPromptDefine, "expression":
	default:
		return fmt.Errorf("promptType must be %q or %q", chainPromptAuto, chainPromptDefine)
	}
	rows, err := chainMessageRows(n.Parameters["messages"])
	if err != nil {
		return err
	}
	for index, row := range rows {
		switch row.Type {
		case chainRoleSystem, chainRoleHuman, chainRoleAI, "expression", "":
		default:
			return fmt.Errorf("message %d has type %q, want system, human, or ai", index+1, row.Type)
		}
		if strings.TrimSpace(row.Text) == "" {
			return fmt.Errorf("message %d has empty text", index+1)
		}
	}
	if promptType == chainPromptDefine && len(rows) == 0 && statementText(n.Parameters, "text") == "" {
		return fmt.Errorf("define a prompt with text or at least one message")
	}
	return nil
}

func eventDetail(event ai.Event) json.RawMessage {
	encoded, err := json.Marshal(event)
	if err != nil {
		return nil
	}
	return encoded
}

// httpToolFrom wraps an HTTP Request tool descriptor as a callable tool.
//
// The tool runs the *same* HTTPExecutor the HTTP Request node uses, so it
// inherits the SSRF policy, credential scoping, timeout, and response limits
// rather than reimplementing any of them.
func (executor *AgentExecutor) httpToolFrom(ir workflow.IRNode, descriptor map[string]any, request engine.Request) (ai.Tool, error) {
	name := textValue(descriptor["name"], "")
	if name == "" {
		return nil, fmt.Errorf("node %q: a connected tool has no name", ir.Name)
	}
	parameters, _ := descriptor["parameters"].(map[string]any)
	credentials := map[string]string{}
	if raw, ok := descriptor["credentials"].(map[string]any); ok {
		for typeID, id := range raw {
			if text, ok := id.(string); ok {
				credentials[typeID] = text
			}
		}
	}
	return &httpRequestTool{
		executor:    executor.httpTool,
		name:        name,
		description: textValue(descriptor["description"], ""),
		node: workflow.IRNode{
			ID: ir.ID + ":" + name, Name: textValue(descriptor["nodeName"], name),
			Type: HTTPToolNodeType, TypeVersion: workflow.V(1),
			Parameters: parameters, Credentials: credentials,
			Definition: workflow.NodeDefinition{
				Type: HTTPToolNodeType, Version: workflow.V(1),
				Outputs: mainOutput(), ExecutorID: HTTPExecutorID,
			},
		},
		request: request,
	}, nil
}

type httpRequestTool struct {
	executor    *HTTPExecutor
	name        string
	description string
	node        workflow.IRNode
	request     engine.Request
}

func (tool *httpRequestTool) Definition() ai.ToolDefinition {
	// The schema is derived rather than hand-written: every $fromAI call in
	// the parameters contributes a typed property carrying its description.
	if calls, err := ai.ExtractFromAI(tool.node.Parameters); err == nil && len(calls) > 0 {
		return ai.ToolDefinition{
			Name:        tool.name,
			Description: tool.description,
			Parameters:  ai.FromAISchema(calls),
		}
	}
	return ai.ToolDefinition{
		Name:        tool.name,
		Description: tool.description,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"input": map[string]any{
					"type":        "object",
					"description": "Values the request's expressions read through $json.",
				},
			},
		},
	}
}

// Invoke runs the configured HTTP request with the model's arguments exposed
// as the item, so `{{ $json.city }}` in the URL resolves from what the model
// supplied. It returns the whole response, not the first item of it.
func (tool *httpRequestTool) Invoke(ctx context.Context, arguments json.RawMessage) (string, error) {
	argsMap := map[string]any{}
	item := workflow.Item{JSON: map[string]any{}}
	if len(arguments) > 0 && json.Valid(arguments) {
		var decoded map[string]any
		if err := json.Unmarshal(arguments, &decoded); err == nil {
			argsMap = decoded
			if nested, ok := decoded["input"].(map[string]any); ok {
				item.JSON = nested
			} else {
				item.JSON = decoded
			}
		}
	}
	// The model's arguments reach the $fromAI calls as data: plain strings
	// are filled, and the expressions read them from their context when the
	// request resolves (fillToolFromAI). Anything without such a call keeps
	// reading them as $json.
	parameters, fromAI, err := fillToolFromAI(tool.node.Parameters, argsMap)
	if err != nil {
		return "", fmt.Errorf("tool %q: %w", tool.name, err)
	}
	// The filled parameters go into a copy of the node, never into the tool's
	// own template. A tool outlives one call: writing the first call's
	// arguments back over the $fromAI placeholders made every later call — and
	// every later item — repeat the first request, while the agent answered
	// with the wrong data and the execution still reported success. The copy
	// is also what keeps Definition() offering the model the real argument
	// schema after the first call.
	node := tool.node
	node.Parameters = parameters

	output, err := tool.executor.execute(ctx, node, workflow.NodeInput{"main": {item}}, tool.request, fromAI)
	if err != nil {
		return "", err
	}
	if len(output) == 0 || len(output[0]) == 0 {
		return "", fmt.Errorf("tool returned nothing")
	}
	return httpToolObservation(output[0])
}

// httpToolMaxBytes is the most of one response the model is handed. It is the
// budget a data table tool's result gets (datastoreToolMaxBytes) for the same
// reason: an endpoint's whole payload must not fill the model's context. Past
// it the observation is cut and says so; the note is added on top of the cap.
const httpToolMaxBytes = 256 * 1024

// httpToolObservation is the text the model reads for one HTTP tool call: the
// whole response, as n8n's tool gives it. The request node turns a top-level
// array into one item per element, so keeping only the first item answered "how
// many customers?" with one while the run stayed green. One item is its own
// object, as before; several are a JSON array of every item's object.
//
// Only the first httpToolMaxBytes are kept. The model is told how much is
// missing, because a silently shortened list is the same wrong answer again.
func httpToolObservation(items []workflow.Item) (string, error) {
	var result any = items[0].JSON
	if len(items) > 1 {
		bodies := make([]map[string]any, len(items))
		for index, item := range items {
			bodies[index] = item.JSON
		}
		result = bodies
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode tool result: %w", err)
	}
	if len(encoded) <= httpToolMaxBytes {
		return string(encoded), nil
	}
	// Cut on a character boundary, so the model is not handed a broken rune.
	cut := httpToolMaxBytes
	for cut > 0 && !utf8.RuneStart(encoded[cut]) {
		cut--
	}
	extent := fmt.Sprintf("%d bytes", len(encoded))
	if len(items) > 1 {
		extent = fmt.Sprintf("%d items, %d bytes", len(items), len(encoded))
	}
	return fmt.Sprintf("%s\n[truncated: the response is %s and only the first %d bytes are shown; %d bytes are omitted. This is a partial result, so do not count or summarise it as if it were the whole response.]",
		encoded[:cut], extent, cut, len(encoded)-cut), nil
}

// boolOr reads a boolean parameter, falling back to the default the node
// declares when the document never wrote one. The editor writes defaults into
// the document, an imported workflow does not, and a node must mean the same
// thing either way.
func boolOr(parameters map[string]any, key string, fallback bool) bool {
	if value, present := parameters[key]; present {
		return boolValue(value)
	}
	return fallback
}

// presentNumber reads a descriptor key that has to survive being zero.
func presentNumber(descriptor map[string]any, key string) *float64 {
	value, present := descriptor[key]
	if !present || value == nil {
		return nil
	}
	number := numberValue(value)
	return &number
}

// positiveInt drops a bound that is not one. A model option of -1 or 0 means
// "no bound", which is said by omitting the field rather than by sending it.
func positiveInt(value float64) int {
	if value <= 0 {
		return 0
	}
	return int(value)
}

// soleDescriptor returns the single descriptor a capped slot may carry.
//
// The compiler caps `model` and `memory` at one connection each, so two
// descriptors on one of them means a graph reached the runner without passing
// validatePortCardinality — an import that skipped compilation, or a cap that
// stopped being enforced. Returning the first match, as this once did, makes
// the model that actually runs a function of item order: the wrong model runs,
// the run succeeds, and nothing anywhere says so. Refusing names the slot
// instead.
func soleDescriptor(items []workflow.Item, slot string) (map[string]any, bool, error) {
	descriptors := descriptorsFrom(items)
	switch len(descriptors) {
	case 0:
		return nil, false, nil
	case 1:
		return descriptors[0], true, nil
	default:
		return nil, false, fmt.Errorf(
			"the %s port carries %d connections but accepts one; disconnect all but one", slot, len(descriptors))
	}
}

func descriptorsFrom(items []workflow.Item) []map[string]any {
	descriptors := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if descriptor, ok := item.JSON[descriptorKey].(map[string]any); ok {
			descriptors = append(descriptors, descriptor)
		}
	}
	return descriptors
}

// Tool descriptor kinds. The HTTP tool predates the family and its
// descriptors carry "tool"; the newer tools name themselves.
const (
	toolKindHTTP        = "tool"
	toolKindWorkflow    = "workflow"
	toolKindCalculator  = "calculator"
	toolKindMCP         = "mcp"
	toolKindDatastore   = "datastore"
	toolKindVectorStore = "vectorStore"
)

// toolNameProperty is the optional override for the name the model calls,
// shared by every tool in the family so the spelling stays identical.
func toolNameProperty() node.PropertyDefinition {
	return node.PropertyDefinition{
		Key: "toolName", Label: "Tool name", Kind: node.PropertyString,
		Description: "Optional override for the name the model calls. Defaults to the node name, normalised to letters, digits, and underscores.",
	}
}

// toolDescriptionProperty is the model-facing description every tool
// requires: without it the model cannot know when to call the tool.
func toolDescriptionProperty() node.PropertyDefinition {
	return node.PropertyDefinition{
		Key: "toolDescription", Label: "Tool description", Kind: node.PropertyString, Required: true,
		Description: "What the tool does, written for the model.",
	}
}

// toolCodex files a tool variant under the picker's AI tools, the way a
// tool-capable node is re-listed when used as a tool rather than as a step.
func toolCodex() *node.NodeCodex {
	return &node.NodeCodex{
		Categories:    []string{"AI"},
		Subcategories: map[string][]string{"AI": {"Tools"}},
	}
}

// toolVariantOf derives a tool node from an ordinary node definition: same
// parameters, same credentials, same executor, differing only in ports,
// tool naming, and picker filing. One definition stays one definition —
// there are never two parameter lists to drift apart.
func toolVariantOf(base node.Definition, toolType, displayName, description, executorID string) node.Definition {
	base.Type = toolType
	base.DisplayName = displayName
	base.Description = description
	base.Category = "AI"
	base.Inputs = nil
	base.Outputs = []workflow.Port{{Name: "tool", Kind: workflow.ConnectionTool}}
	base.Parameters = append([]node.PropertyDefinition{toolNameProperty(), toolDescriptionProperty()}, base.Parameters...)
	base.ExecutorID = executorID
	base.Codex = toolCodex()
	return base
}

// validateToolNameAndDescription checks the two parameters every tool in
// the family carries, so each validator states only what its own node adds.
func validateToolNameAndDescription(n workflow.Node) error {
	if name := statementText(n.Parameters, "toolName"); name != "" && !validToolName(name) {
		return fmt.Errorf("toolName must contain only letters, digits, and underscores")
	}
	if statementText(n.Parameters, "toolDescription") == "" {
		return fmt.Errorf("toolDescription is required so the model knows when to call it")
	}
	return nil
}

// validateToolFromAI refuses a tool whose $fromAI calls cannot build a
// schema, naming the offending call at configuration time rather than
// mid-run.
func validateToolFromAI(n workflow.Node) error {
	if _, err := ai.ExtractFromAI(n.Parameters); err != nil {
		return err
	}
	return nil
}

// calculatorNode is the ordinary node a calculator tool is derived from: it
// evaluates one arithmetic expression per item, with no network, no
// credentials, and no side effects.
func calculatorNode() node.Definition {
	return node.Definition{
		Type:        CalculatorNodeType,
		Version:     workflow.V(1),
		DisplayName: "Calculator",
		Description: "Evaluates an arithmetic expression per item.",
		Category:    "Core",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:calculator"},
		IconColor:   "#16a34a",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "expression", Label: "Expression", Kind: node.PropertyString, Required: true,
				Description: "The arithmetic to evaluate, for example (19 - 32) * 5 / 9. Supports expressions; the incoming item is available as $json.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     CalculatorExecutorID,
		Validate:       validateCalculatorConfiguration,
	}
}

// calculatorToolNode exposes the calculator as an agent tool, derived from
// the ordinary node rather than re-declared.
func calculatorToolNode() node.Definition {
	definition := toolVariantOf(calculatorNode(), CalculatorToolNodeType, "Calculator Tool", "Evaluates an arithmetic expression for an AI Agent.", CalculatorToolExecutorID)
	// The model supplies the arithmetic, so `expression` is not required here.
	// Clearing the declaration — not only the validator — is what matters: the
	// compiler reads the declaration through registry.RequiredFor, so a tool
	// that declared it required and stored nothing was refused with
	// `config.required` before its executor ever ran. That is how every
	// imported calculator tool (the importer writes only toolName and
	// toolDescription) stayed unactivatable while the validator said the
	// parameter was optional.
	definition.Parameters = optionalProperty(definition.Parameters, "expression")
	definition.Validate = validateCalculatorToolConfiguration
	return definition
}

// optionalProperty returns the properties with one key's requirement cleared.
//
// It exists for a variant where a value is supplied at run time rather than
// configured — a tool's argument comes from the model — and the declaration
// inherited from the node it is derived from still says otherwise.
func optionalProperty(properties []node.PropertyDefinition, key string) []node.PropertyDefinition {
	cleared := append([]node.PropertyDefinition(nil), properties...)
	for index := range cleared {
		if cleared[index].Key == key {
			cleared[index].Required = false
		}
	}
	return cleared
}

func validateCalculatorConfiguration(n workflow.Node) error {
	if statementText(n.Parameters, "expression") == "" {
		return fmt.Errorf("expression is required")
	}
	return nil
}

func validateCalculatorToolConfiguration(n workflow.Node) error {
	if err := validateToolNameAndDescription(n); err != nil {
		return err
	}
	// No expression is required: the model supplies it, the way n8n's
	// Calculator tool takes the arithmetic from the call. The parameter is
	// still honoured when it holds an expression or a $fromAI call.
	return validateToolFromAI(n)
}

// executeCalculator evaluates the expression for each incoming item.
func executeCalculator(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := input["main"]
	if len(items) == 0 {
		items = []workflow.Item{{JSON: map[string]any{}}}
	}
	out := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		parameters, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		result, err := evaluateCalculatorExpression(textValue(parameters["expression"], ""))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		out = append(out, workflow.Item{JSON: map[string]any{"result": result}})
	}
	return workflow.NodeOutput{out}, nil
}

// executeCalculatorTool emits a calculator tool descriptor.
func executeCalculatorTool(ctx context.Context, ir workflow.IRNode, _ workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parameters := make(map[string]any, len(ir.Parameters))
	for key, value := range ir.Parameters {
		parameters[key] = value
	}
	return workflow.NodeOutput{{{JSON: map[string]any{descriptorKey: map[string]any{
		"kind":        toolKindCalculator,
		"name":        toolNameFor(ir),
		"description": textValue(ir.Parameters["toolDescription"], ""),
		"nodeName":    ir.Name,
		"parameters":  parameters,
	}}}}}, nil
}

// evaluateCalculatorExpression evaluates one arithmetic expression:
// addition, subtraction, multiplication, division, remainder, and
// exponentiation over parenthesised decimals. Anything else is refused
// rather than guessed at.
func evaluateCalculatorExpression(text string) (float64, error) {
	parser := &calcParser{input: strings.TrimSpace(text)}
	if parser.input == "" {
		return 0, fmt.Errorf("expression is required")
	}
	result, err := parser.parseSum()
	if err != nil {
		return 0, err
	}
	parser.skipSpaces()
	if parser.pos != len(parser.input) {
		return 0, fmt.Errorf("unexpected %q at offset %d", parser.input[parser.pos:], parser.pos)
	}
	if math.IsNaN(result) || math.IsInf(result, 0) {
		return 0, fmt.Errorf("the result is out of range")
	}
	return result, nil
}

type calcParser struct {
	input string
	pos   int
}

// calcConstants are the named values the calculator evaluates, matching what
// n8n's own Calculator tool resolves.
var calcConstants = map[string]float64{"pi": math.Pi, "e": math.E}

func (parser *calcParser) skipSpaces() {
	for parser.pos < len(parser.input) && (parser.input[parser.pos] == ' ' || parser.input[parser.pos] == '\t') {
		parser.pos++
	}
}

func (parser *calcParser) parseSum() (float64, error) {
	left, err := parser.parseProduct()
	if err != nil {
		return 0, err
	}
	for {
		parser.skipSpaces()
		if parser.pos >= len(parser.input) {
			return left, nil
		}
		operator := parser.input[parser.pos]
		if operator != '+' && operator != '-' {
			return left, nil
		}
		parser.pos++
		right, err := parser.parseProduct()
		if err != nil {
			return 0, err
		}
		if operator == '+' {
			left += right
		} else {
			left -= right
		}
	}
}

func (parser *calcParser) parseProduct() (float64, error) {
	left, err := parser.parseUnary()
	if err != nil {
		return 0, err
	}
	for {
		parser.skipSpaces()
		if parser.pos >= len(parser.input) {
			return left, nil
		}
		operator := parser.input[parser.pos]
		if operator != '*' && operator != '/' && operator != '%' {
			return left, nil
		}
		parser.pos++
		right, err := parser.parseUnary()
		if err != nil {
			return 0, err
		}
		switch operator {
		case '*':
			left *= right
		case '/':
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left /= right
		case '%':
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left = math.Mod(left, right)
		}
	}
}

func (parser *calcParser) parseUnary() (float64, error) {
	parser.skipSpaces()
	if parser.pos < len(parser.input) && (parser.input[parser.pos] == '+' || parser.input[parser.pos] == '-') {
		negative := parser.input[parser.pos] == '-'
		parser.pos++
		value, err := parser.parseUnary()
		if err != nil {
			return 0, err
		}
		if negative {
			return -value, nil
		}
		return value, nil
	}
	return parser.parsePower()
}

func (parser *calcParser) parsePower() (float64, error) {
	base, err := parser.parsePrimary()
	if err != nil {
		return 0, err
	}
	parser.skipSpaces()
	if parser.pos < len(parser.input) && parser.input[parser.pos] == '^' {
		parser.pos++
		exponent, err := parser.parseUnary()
		if err != nil {
			return 0, err
		}
		return math.Pow(base, exponent), nil
	}
	return base, nil
}

func (parser *calcParser) parsePrimary() (float64, error) {
	parser.skipSpaces()
	if parser.pos >= len(parser.input) {
		return 0, fmt.Errorf("unexpected end of expression")
	}
	if parser.input[parser.pos] == '(' {
		parser.pos++
		value, err := parser.parseSum()
		if err != nil {
			return 0, err
		}
		parser.skipSpaces()
		if parser.pos >= len(parser.input) || parser.input[parser.pos] != ')' {
			return 0, fmt.Errorf("missing closing parenthesis")
		}
		parser.pos++
		return value, nil
	}
	start := parser.pos
	seenDigit := false
	seenDot := false
	for parser.pos < len(parser.input) {
		char := parser.input[parser.pos]
		if char >= '0' && char <= '9' {
			seenDigit = true
			parser.pos++
		} else if char == '.' && !seenDot {
			seenDot = true
			parser.pos++
		} else {
			break
		}
	}
	if !seenDigit {
		// Named constants: a model asked to work out a circumference reaches
		// for pi, and refusing it burns a tool call it cannot recover from.
		nameStart := parser.pos
		for parser.pos < len(parser.input) {
			char := parser.input[parser.pos]
			if char < 'a' || char > 'z' {
				if char < 'A' || char > 'Z' {
					break
				}
			}
			parser.pos++
		}
		if name := parser.input[nameStart:parser.pos]; name != "" {
			if constant, ok := calcConstants[strings.ToLower(name)]; ok {
				return constant, nil
			}
		}
		return 0, fmt.Errorf("unexpected %q at offset %d", parser.input[nameStart:], nameStart)
	}
	value, err := strconv.ParseFloat(parser.input[start:parser.pos], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number %q", parser.input[start:parser.pos])
	}
	return value, nil
}

// workflowToolNode exposes another workflow of the same tenant as an agent
// tool, so an agent can delegate a packaged task to a whole workflow.
func workflowToolNode() node.Definition {
	return node.Definition{
		Type:        WorkflowToolNodeType,
		Version:     workflow.V(1),
		DisplayName: "Workflow Tool",
		Description: "Calls another workflow of this tenant as an AI Agent tool.",
		Category:    "AI",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:workflow"},
		IconColor:   "#8b5cf6",
		Inputs:      nil,
		Outputs:     []workflow.Port{{Name: "tool", Kind: workflow.ConnectionTool}},
		Parameters: []node.PropertyDefinition{
			toolNameProperty(),
			toolDescriptionProperty(),
			{
				Key: "workflowId", Label: "Workflow", Kind: node.PropertyResourceLocator, Required: true,
				Description: "The workflow to run. It must belong to this tenant and be active. The model's arguments become the sub-workflow's input item.",
				Modes: []node.PropertyMode{
					{
						Name: "list", Label: "From list", Kind: node.PropertyOptions,
						Placeholder: "Choose…",
						LoadOptions: &node.OptionsLoader{Source: property.LoaderInternal, Name: WorkflowListLoader},
					},
					{
						Name: "id", Label: "By ID", Kind: node.PropertyString,
						Placeholder: "wf_…",
						Hint:        "The KilasFlow workflow ID. Supports expressions.",
					},
				},
			},
			{
				Key: "workflowInputs", Label: "Workflow inputs", Kind: node.PropertyJSON,
				Description: "Extra static inputs merged over the model's arguments into the sub-workflow's input item. Supports expressions.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     WorkflowToolExecutorID,
		Validate:       validateWorkflowToolConfiguration,
		Codex:          toolCodex(),
	}
}

func validateWorkflowToolConfiguration(n workflow.Node) error {
	if err := validateToolNameAndDescription(n); err != nil {
		return err
	}
	if !property.LocatorIsSet(n.Parameters["workflowId"]) {
		return fmt.Errorf("workflowId is required")
	}
	return validateToolFromAI(n)
}

// executeWorkflowTool emits a workflow tool descriptor.
func executeWorkflowTool(ctx context.Context, ir workflow.IRNode, _ workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parameters := make(map[string]any, len(ir.Parameters))
	for key, value := range ir.Parameters {
		parameters[key] = value
	}
	return workflow.NodeOutput{{{JSON: map[string]any{descriptorKey: map[string]any{
		"kind":        toolKindWorkflow,
		"name":        toolNameFor(ir),
		"description": textValue(ir.Parameters["toolDescription"], ""),
		"nodeName":    ir.Name,
		"workflowId":  ir.Parameters["workflowId"],
		"parameters":  parameters,
	}}}}}, nil
}

// workflowTool runs a sub-workflow through the runtime's own invoker, so a
// tool call inherits the deployment's recursion refusal and call-depth
// bound instead of reimplementing either.
type workflowTool struct {
	name        string
	description string
	rawWorkflow any
	parameters  map[string]any
	nodeName    string
	agentNode   string
	request     engine.Request
}

func (tool *workflowTool) Definition() ai.ToolDefinition {
	if calls, err := ai.ExtractFromAI(tool.parameters); err == nil && len(calls) > 0 {
		return ai.ToolDefinition{Name: tool.name, Description: tool.description, Parameters: ai.FromAISchema(calls)}
	}
	return ai.ToolDefinition{
		Name:        tool.name,
		Description: tool.description,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"input": map[string]any{
					"type":        "object",
					"description": "The sub-workflow's input item.",
				},
			},
		},
	}
}

// Invoke runs the sub-workflow with the model's arguments as its input item
// and returns what it produced.
func (tool *workflowTool) Invoke(ctx context.Context, arguments json.RawMessage) (string, error) {
	if tool.request.Workflows == nil {
		return "", fmt.Errorf("node %q: this runtime cannot run sub-workflows", tool.agentNode)
	}
	argsMap, item, err := toolArgumentsItem(arguments)
	if err != nil {
		return "", err
	}
	// The model's arguments reach the $fromAI calls as data, the same way the
	// HTTP Request tool's do (fillToolFromAI).
	parameters, fromAI, err := fillToolFromAI(tool.parameters, argsMap)
	if err != nil {
		return "", fmt.Errorf("node %q: tool %q: %w", tool.agentNode, tool.name, err)
	}
	scope := expressionContext(item, nil, tool.request, 0)
	scope.FromAIArguments = fromAI
	target, err := workflowToolTarget(tool.agentNode, tool.rawWorkflow, parameters, scope)
	if err != nil {
		return "", err
	}
	inputItem, err := workflowToolInput(tool.agentNode, parameters, item, scope)
	if err != nil {
		return "", err
	}
	result, err := tool.request.Workflows.InvokeWorkflow(ctx, tool.request.Execution, engine.WorkflowCall{
		WorkflowID: target, Items: []workflow.Item{inputItem}, Wait: true,
	})
	if err != nil {
		// The model reads this text and repeats it to the user, so an internal
		// lookup failure is translated rather than echoed: "repository record
		// not found: active workflow" is how an agent ends up telling someone
		// their order does not exist while the execution reports success.
		return "", fmt.Errorf("node %q: the sub-workflow %q this tool calls could not be run — it is not active, or it no longer exists. This is a configuration problem in this workflow, not a missing record: tell the user the tool is unavailable instead of answering with the data it would have returned (%w)",
			tool.agentNode, target, err)
	}
	if len(result.Items) == 1 {
		encoded, err := json.Marshal(result.Items[0].JSON)
		if err != nil {
			return "", fmt.Errorf("encode tool result: %w", err)
		}
		return string(encoded), nil
	}
	encoded, err := json.Marshal(result.Items)
	if err != nil {
		return "", fmt.Errorf("encode tool result: %w", err)
	}
	return string(encoded), nil
}

// workflowToolTarget resolves which workflow to run. A locator chosen from
// the list is fixed; one written by ID may still be an expression, which
// resolves in the tool call's scope: its input item and the model's $fromAI
// arguments.
func workflowToolTarget(agentNode string, raw any, parameters map[string]any, scope expression.Context) (string, error) {
	value := raw
	if expression.IsExpression(raw) {
		resolved, err := expression.Resolve(map[string]any{"workflowId": raw}, scope)
		if err != nil {
			return "", fmt.Errorf("node %q: %w", agentNode, err)
		}
		value = resolved["workflowId"]
	} else if expression.IsExpression(parameters["workflowId"]) {
		resolved, err := expression.Resolve(map[string]any{"workflowId": parameters["workflowId"]}, scope)
		if err != nil {
			return "", fmt.Errorf("node %q: %w", agentNode, err)
		}
		value = resolved["workflowId"]
	}
	locator, _ := property.ReadLocator(value)
	if target := strings.TrimSpace(textValue(locator.Value, "")); target != "" {
		return target, nil
	}
	return "", fmt.Errorf("node %q: a sub-workflow call needs the workflow to run", agentNode)
}

// workflowToolInput builds the sub-workflow's input item: the model's
// arguments overlaid with the configured static inputs, which win on
// conflict because the author wrote them explicitly. The inputs resolve in
// the tool call's scope.
func workflowToolInput(agentNode string, parameters map[string]any, item workflow.Item, scope expression.Context) (workflow.Item, error) {
	merged := make(map[string]any, len(item.JSON)+1)
	for key, value := range item.JSON {
		merged[key] = value
	}
	raw, present := parameters["workflowInputs"]
	if !present || raw == nil {
		return workflow.Item{JSON: merged}, nil
	}
	resolved, err := expression.Resolve(map[string]any{"workflowInputs": raw}, scope)
	if err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: %w", agentNode, err)
	}
	extra, ok := resolved["workflowInputs"].(map[string]any)
	if !ok {
		return workflow.Item{}, fmt.Errorf("node %q: workflowInputs must be an object", agentNode)
	}
	for key, value := range extra {
		merged[key] = value
	}
	return workflow.Item{JSON: merged}, nil
}

// toolArgumentsItem decodes a tool call's arguments into the item the
// wrapped node executes against. A call shaped {"input": {...}} under the
// legacy schema unwraps one level, so $json reads what the model sent.
func toolArgumentsItem(arguments json.RawMessage) (map[string]any, workflow.Item, error) {
	argsMap := map[string]any{}
	if len(arguments) > 0 {
		if !json.Valid(arguments) {
			return nil, workflow.Item{}, fmt.Errorf("tool arguments are not valid JSON")
		}
		if err := json.Unmarshal(arguments, &argsMap); err != nil {
			return nil, workflow.Item{}, fmt.Errorf("decode tool arguments: %w", err)
		}
	}
	itemJSON := argsMap
	if nested, ok := argsMap["input"].(map[string]any); ok && len(argsMap) == 1 {
		itemJSON = nested
	}
	return argsMap, workflow.Item{JSON: itemJSON}, nil
}

// calculatorTool runs one arithmetic evaluation per tool call, with no
// outbound path at all.
type calculatorTool struct {
	name        string
	description string
	parameters  map[string]any
	nodeName    string
	agentNode   string
	request     engine.Request
}

func (tool *calculatorTool) Definition() ai.ToolDefinition {
	if calls, err := ai.ExtractFromAI(tool.parameters); err == nil && len(calls) > 0 {
		return ai.ToolDefinition{Name: tool.name, Description: tool.description, Parameters: ai.FromAISchema(calls)}
	}
	return ai.ToolDefinition{
		Name:        tool.name,
		Description: tool.description,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"expression": map[string]any{
					"type":        "string",
					"description": "The arithmetic to evaluate, for example (19 - 32) * 5 / 9.",
				},
			},
			"required": []string{"expression"},
		},
	}
}

// Invoke resolves the parameters with the model's arguments as data, and
// evaluates the arithmetic they produce.
func (tool *calculatorTool) Invoke(ctx context.Context, arguments json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	argsMap, item, err := toolArgumentsItem(arguments)
	if err != nil {
		return "", err
	}
	// The model's arguments reach the $fromAI calls as data, so what reaches
	// the arithmetic is the value the model sent, never something the
	// expression evaluator ran first (fillToolFromAI).
	parameters, fromAI, err := fillToolFromAI(tool.parameters, argsMap)
	if err != nil {
		return "", fmt.Errorf("node %q: tool %q: %w", tool.agentNode, tool.name, err)
	}
	scope := expressionContext(item, nil, tool.request, 0)
	scope.FromAIArguments = fromAI
	resolved, err := expression.Resolve(parameters, scope)
	if err != nil {
		return "", fmt.Errorf("node %q: %w", tool.agentNode, err)
	}
	expressionText := textValue(resolved["expression"], "")
	// With no $fromAI call claiming the expression, the model's argument is
	// the arithmetic. Letting a literal parameter win instead made the tool
	// answer every call with the same number while the execution reported
	// success, which is the worst possible outcome for a calculator.
	if calls, _ := ai.ExtractFromAI(tool.parameters); len(calls) == 0 {
		if fromModel := textValue(argsMap["expression"], ""); fromModel != "" {
			expressionText = fromModel
		}
	}
	result, err := evaluateCalculatorExpression(expressionText)
	if err != nil {
		return "", fmt.Errorf("node %q: %w", tool.agentNode, err)
	}
	encoded, err := json.Marshal(map[string]any{"result": result})
	if err != nil {
		return "", fmt.Errorf("encode tool result: %w", err)
	}
	return string(encoded), nil
}

// outputParserNode constrains an agent's or chain's final answer to a
// declared shape, given either as a JSON Schema or as an example document.
func outputParserNode() node.Definition {
	return node.Definition{
		Type:        OutputParserNodeType,
		Version:     workflow.V(1),
		DisplayName: "Structured Output Parser",
		Description: "Constrains the final answer to a JSON shape the model fills in.",
		Category:    "AI",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:arrow-down-up"},
		IconColor:   "#a855f7",
		Inputs:      nil,
		Outputs:     []workflow.Port{{Name: "parser", Kind: workflow.ConnectionOutputParser}},
		Parameters: []node.PropertyDefinition{
			{
				Key: "schemaType", Label: "Schema source", Kind: node.PropertyOptions, Default: ai.OutputSchemaJSONSchema,
				Options: []node.PropertyOption{
					{Label: "JSON Schema", Value: ai.OutputSchemaJSONSchema},
					{Label: "Example JSON", Value: ai.OutputSchemaExample},
				},
				Description: "Describe the output shape directly, or show one example it is inferred from.",
			},
			{
				Key: "jsonSchema", Label: "JSON Schema", Kind: node.PropertyString,
				Description: "The output shape as a JSON Schema object.",
			},
			{
				Key: "exampleJson", Label: "Example JSON", Kind: node.PropertyString,
				Description: "One example of the output; the shape is inferred from it.",
			},
			{
				Key: "maxRetries", Label: "Maximum retries", Kind: node.PropertyNumber, Default: float64(ai.DefaultOutputMaxRetries),
				Description: "How many responses that fail validation are retried before the run fails.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     OutputParserExecutorID,
		Validate:       validateOutputParserConfiguration,
	}
}

func validateOutputParserConfiguration(n workflow.Node) error {
	if _, _, err := ai.ParseOutputSchema(n.Parameters); err != nil {
		return err
	}
	return nil
}

// executeOutputParser validates the declared shape now and emits a parser
// descriptor the agent or chain reads from its output parser slot.
func executeOutputParser(ctx context.Context, ir workflow.IRNode, _ workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	schema, maxRetries, err := ai.ParseOutputSchema(ir.Parameters)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	return workflow.NodeOutput{{{JSON: map[string]any{descriptorKey: map[string]any{
		"kind":       "parser",
		"nodeName":   ir.Name,
		"schema":     schema,
		"maxRetries": float64(maxRetries),
	}}}}}, nil
}

// parserSlot reads the optional parser descriptor from a root node's output
// parser slot, which the compiler caps at one connection. Anything on the
// slot that is not a parser descriptor is refused rather than misread.
func parserSlot(items []workflow.Item, nodeName string) (schema map[string]any, maxRetries int, present bool, err error) {
	descriptor, found, err := soleDescriptor(items, "outputParser")
	if err != nil || !found {
		return nil, 0, false, err
	}
	if textValue(descriptor["kind"], "") != "parser" {
		return nil, 0, false, fmt.Errorf("node %q: the output parser port accepts only a structured output parser", nodeName)
	}
	schema, _ = descriptor["schema"].(map[string]any)
	if len(schema) == 0 {
		return nil, 0, false, fmt.Errorf("node %q: the output parser carries no schema", nodeName)
	}
	return schema, int(numberValue(descriptor["maxRetries"])), true, nil
}

// toolFrom wraps any connected tool descriptor as a callable tool. The HTTP
// tool runs the same executor the HTTP Request node uses, so it inherits
// the SSRF policy, credential scoping, timeout, and response limits rather
// than reimplementing any of them; the workflow and calculator tools reuse
// their own node implementations the same way, and the MCP tool calls
// through the shared MCP transport below for the same reason.
func (executor *AgentExecutor) toolFrom(ir workflow.IRNode, descriptor map[string]any, request engine.Request) (ai.Tool, error) {
	switch textValue(descriptor["kind"], toolKindHTTP) {
	case toolKindHTTP:
		return executor.httpToolFrom(ir, descriptor, request)
	case toolKindWorkflow:
		return executor.workflowToolFrom(ir, descriptor, request)
	case toolKindCalculator:
		return executor.calculatorToolFrom(ir, descriptor, request)
	case toolKindDatastore:
		return executor.datastoreToolFrom(ir, descriptor, request)
	case toolKindVectorStore:
		return executor.vectorStoreToolFrom(ir, descriptor, request)
	case toolKindMCP:
		return executor.mcpToolFrom(ir, descriptor, request)
	default:
		return nil, fmt.Errorf("node %q: connected tool %q has unknown kind %q",
			ir.Name, textValue(descriptor["nodeName"], ""), textValue(descriptor["kind"], ""))
	}
}

// workflowToolFrom wraps a workflow tool descriptor as a callable tool.
func (executor *AgentExecutor) workflowToolFrom(ir workflow.IRNode, descriptor map[string]any, request engine.Request) (ai.Tool, error) {
	name := textValue(descriptor["name"], "")
	if name == "" {
		return nil, fmt.Errorf("node %q: a connected tool has no name", ir.Name)
	}
	parameters, _ := descriptor["parameters"].(map[string]any)
	return &workflowTool{
		name:        name,
		description: textValue(descriptor["description"], ""),
		rawWorkflow: descriptor["workflowId"],
		parameters:  parameters,
		nodeName:    textValue(descriptor["nodeName"], name),
		agentNode:   ir.Name,
		request:     request,
	}, nil
}

// calculatorToolFrom wraps a calculator tool descriptor as a callable tool.
func (executor *AgentExecutor) calculatorToolFrom(ir workflow.IRNode, descriptor map[string]any, request engine.Request) (ai.Tool, error) {
	name := textValue(descriptor["name"], "")
	if name == "" {
		return nil, fmt.Errorf("node %q: a connected tool has no name", ir.Name)
	}
	parameters, _ := descriptor["parameters"].(map[string]any)
	return &calculatorTool{
		name:        name,
		description: textValue(descriptor["description"], ""),
		parameters:  parameters,
		nodeName:    textValue(descriptor["nodeName"], name),
		agentNode:   ir.Name,
		request:     request,
	}, nil
}

func (executor *AgentExecutor) vectorStoreToolFrom(ir workflow.IRNode, descriptor map[string]any, request engine.Request) (ai.Tool, error) {
	name := textValue(descriptor["name"], "")
	if name == "" {
		return nil, fmt.Errorf("node %q: a connected tool has no name", ir.Name)
	}
	nodeName := textValue(descriptor["nodeName"], name)
	if executor.vectors == nil && textValue(descriptor["backend"], "internal") == "internal" {
		return nil, fmt.Errorf("node %q: tool %q needs the vector store, which is not available on this install", ir.Name, nodeName)
	}
	topK := int(numberValue(descriptor["topK"]))
	if topK <= 0 {
		topK = DefaultVectorTopK
	}
	embeddings, _ := descriptor["embeddings"].(map[string]any)
	filter, _ := descriptor["metadataFilter"].(map[string]any)
	return &vectorStoreTool{
		name:            name,
		description:     textValue(descriptor["description"], ""),
		nodeName:        nodeName,
		agentNode:       ir.Name,
		tenant:          strings.TrimSpace(request.Execution.TenantID),
		collection:      textValue(descriptor["collection"], ""),
		backend:         textValue(descriptor["backend"], "internal"),
		topK:            topK,
		filter:          filter,
		embeddings:      embeddings,
		request:         request,
		store:           executor.vectors,
		embedder:        executor.embedder,
		sqlGuard:        executor.sqlGuard,
		tableName:       textValue(descriptor["tableName"], ""),
		idColumn:        textValue(descriptor["idColumn"], "id"),
		contentColumn:   textValue(descriptor["contentColumn"], "text"),
		metadataColumn:  textValue(descriptor["metadataColumn"], "metadata"),
		embeddingColumn: textValue(descriptor["embeddingColumn"], "embedding"),
		credentialID:    textValue(descriptor["credentialId"], ""),
	}, nil
}

type vectorStoreTool struct {
	name, description, nodeName, agentNode, tenant, collection, backend               string
	topK                                                                              int
	filter, embeddings                                                                map[string]any
	request                                                                           engine.Request
	store                                                                             VectorStore
	embedder                                                                          *EmbeddingsExecutor
	sqlGuard                                                                          sqlnode.Guard
	tableName, idColumn, contentColumn, metadataColumn, embeddingColumn, credentialID string
}

func (tool *vectorStoreTool) Definition() ai.ToolDefinition {
	return ai.ToolDefinition{
		Name:        tool.name,
		Description: tool.description,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The search query to retrieve relevant documents for.",
				},
			},
			"required": []string{"query"},
		},
	}
}

func (tool *vectorStoreTool) Invoke(ctx context.Context, arguments json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var args struct {
		Query string `json:"query"`
	}
	if len(bytes.TrimSpace(arguments)) > 0 {
		if err := json.Unmarshal(arguments, &args); err != nil {
			return "", fmt.Errorf("node %q: tool %q: invalid arguments: %w", tool.agentNode, tool.name, err)
		}
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return "", fmt.Errorf("node %q: tool %q: query is required", tool.agentNode, tool.name)
	}
	if tool.embedder == nil || tool.embeddings == nil {
		return "", fmt.Errorf("node %q: tool %q: connect an Embeddings sub-node", tool.agentNode, tool.name)
	}
	vectors, err := tool.embedder.EmbedFromDescriptor(ctx, tool.agentNode, tool.embeddings, tool.request, []string{query})
	if err != nil {
		return "", err
	}
	var matches []VectorMatch
	if tool.backend == "postgres" {
		matches, err = searchCustomerPGVector(ctx, tool, vectors[0])
	} else {
		if tool.store == nil {
			return "", fmt.Errorf("node %q: tool %q: the vector store is not configured on this install", tool.agentNode, tool.name)
		}
		matches, err = tool.store.Search(ctx, tool.tenant, tool.collection, vectors[0], tool.topK, tool.filter)
	}
	if err != nil {
		return "", fmt.Errorf("node %q: tool %q: %w", tool.agentNode, tool.name, err)
	}
	encoded := make([]any, 0, len(matches))
	for _, match := range matches {
		encoded = append(encoded, map[string]any{
			"id": match.ID, "text": match.Content, "metadata": match.Metadata, "distance": match.Distance,
		})
	}
	payload, err := json.Marshal(map[string]any{"matches": encoded})
	if err != nil {
		return "", fmt.Errorf("encode tool result: %w", err)
	}
	return string(payload), nil
}

// datastoreToolFrom wraps a datastore tool descriptor as a callable tool. The
// call reads or writes through the deployment's own row store scoped to the
// execution's tenant, so it inherits tenant isolation rather than
// reimplementing it. A descriptor that names no operation reads, as every
// descriptor did before the tool wrote.
func (executor *AgentExecutor) datastoreToolFrom(ir workflow.IRNode, descriptor map[string]any, request engine.Request) (ai.Tool, error) {
	name := textValue(descriptor["name"], "")
	if name == "" {
		return nil, fmt.Errorf("node %q: a connected tool has no name", ir.Name)
	}
	nodeName := textValue(descriptor["nodeName"], name)
	if executor.datastore == nil {
		return nil, fmt.Errorf("node %q: tool %q needs datastore storage, which is not available on this server", ir.Name, nodeName)
	}
	tenant := strings.TrimSpace(request.Execution.TenantID)
	if tenant == "" {
		return nil, fmt.Errorf("node %q: tool %q runs without a tenant", ir.Name, nodeName)
	}
	datastoreID := textValue(descriptor["datastoreId"], "")
	if datastoreID == "" {
		return nil, fmt.Errorf("node %q: tool %q names no data table", ir.Name, nodeName)
	}
	columns := datastoreToolColumns(descriptor["columns"])
	if len(columns) == 0 {
		return nil, fmt.Errorf("node %q: tool %q carries no columns", ir.Name, nodeName)
	}
	operation := textValue(descriptor["operation"], DatastoreOperationGet)
	if !datastoreToolOperations[operation] {
		return nil, fmt.Errorf("node %q: tool %q carries operation %q, which a datastore tool does not perform", ir.Name, nodeName, operation)
	}
	parameters, _ := descriptor["parameters"].(map[string]any)
	if operation != DatastoreOperationGet && parameters == nil {
		// A write without its template has nothing to say what it writes.
		return nil, fmt.Errorf("node %q: tool %q runs %s but carries no parameters", ir.Name, nodeName, operation)
	}
	return &datastoreTool{
		name:          name,
		description:   textValue(descriptor["description"], ""),
		nodeName:      nodeName,
		agentNode:     ir.Name,
		tenant:        tenant,
		datastoreID:   datastoreID,
		datastoreName: textValue(descriptor["datastoreName"], ""),
		columns:       columns,
		store:         executor.datastore,
		operation:     operation,
		parameters:    parameters,
		request:       request,
	}, nil
}

// mcpClientToolNode exposes a Model Context Protocol server's tools to an AI
// Agent. One MCP server usually publishes many tools while one KilasFlow
// descriptor maps to one ai.Tool, so the node lists the server at run time
// and emits one descriptor item per selected tool on the tool port. That
// keeps descriptorsFrom unchanged and makes the agent's duplicate-name check
// apply naturally across MCP and HTTP tools alike.
func mcpClientToolNode() node.Definition {
	return node.Definition{
		Type:        MCPClientToolNodeType,
		Version:     workflow.V(1),
		DisplayName: "MCP Client Tool",
		Description: "Exposes tools from a Model Context Protocol server to an AI Agent as callable tools.",
		Category:    "AI",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:wrench"},
		IconColor:   "#a855f7",
		Inputs:      nil,
		Outputs:     []workflow.Port{{Name: "tool", Kind: workflow.ConnectionTool}},
		// The generic HTTP credential types, the same three the HTTP Request
		// node accepts: the MCP endpoint is a tenant-authored URL and its
		// token is resolved from the store at call time, never stored in the
		// workflow document. No new credential type was needed.
		Credentials: []node.CredentialRequirement{
			{Type: "httpBearerAuth"},
			{Type: "httpHeaderAuth"},
			{Type: "httpBasicAuth"},
		},
		Parameters: []node.PropertyDefinition{
			{
				Key: "serverUrl", Label: "Server URL", Kind: node.PropertyString, Required: true,
				Description: "The MCP server's streamable HTTP endpoint. Supports expressions.",
			},
			{
				Key: "tools", Label: "Tools to expose", Kind: node.PropertyString,
				Description: "Comma-separated server tool names to expose. Leave empty to expose every tool the server lists.",
			},
			{
				Key: "requestTimeoutSeconds", Label: "Request timeout (seconds)", Kind: node.PropertyNumber, Default: 30,
				Description: "Time budget for listing the server's tools and for each tool call. It can only tighten the deployment's ceiling, never raise it.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     MCPClientToolExecutorID,
		Validate:       validateMCPClientToolConfiguration,
		Codex:          toolCodex(),
	}
}

func validateMCPClientToolConfiguration(n workflow.Node) error {
	raw := strings.TrimSpace(statementText(n.Parameters, "serverUrl"))
	if raw == "" {
		return fmt.Errorf("serverUrl is required")
	}
	if raw == "expression" {
		// An expression-valued URL resolves at run time; static validation
		// cannot judge it, and run time refuses what it cannot use.
		return nil
	}
	target, err := url.Parse(raw)
	if err != nil || target == nil {
		return fmt.Errorf("serverUrl is not a valid URL")
	}
	switch strings.ToLower(target.Scheme) {
	case "http", "https":
		return nil
	case "stdio":
		return fmt.Errorf("serverUrl uses stdio, which would run a local process: point it at the server's streamable HTTP endpoint instead")
	default:
		return fmt.Errorf("serverUrl scheme %q is not supported; use http or https", target.Scheme)
	}
}

// mcpProtocolVersion is the Streamable HTTP protocol version this client
// announces in initialize. The server's answer is accepted as-is rather than
// negotiated: refusing a server that speaks an older revision would turn a
// working tool into a version error.
const mcpProtocolVersion = "2025-06-18"

// mcpRequestIDs numbers JSON-RPC requests process-wide, so two concurrent
// tool calls never share an id on one server.
var mcpRequestIDs atomic.Int64

// MCPClientToolExecutor lists an MCP server's tools and calls them, every
// request through an http.Client built by internal/safehttp. The client's
// dialer re-checks the resolved IP of every connection and every redirect
// hop, so a server URL pointing at loopback, a private range, or the cloud
// metadata service is refused by the same policy the HTTP node enforces.
type MCPClientToolExecutor struct {
	policy safehttp.Policy
	client *http.Client
}

// NewMCPClientToolExecutor builds the MCP client tool executor for one
// policy. Exported so the composition root registers it beside the other
// executors; it carries no per-workflow state.
func NewMCPClientToolExecutor(policy safehttp.Policy) *MCPClientToolExecutor {
	return &MCPClientToolExecutor{policy: policy, client: safehttp.NewClient(policy)}
}

// Execute lists the server's tools and emits one tool descriptor item per
// selected tool.
func (executor *MCPClientToolExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// The node carries no main input: it configures the agent rather than
	// transforming items. Resolve against an empty item so $env and literals
	// work; there is no $json to read here.
	resolved, err := expression.Resolve(ir.Parameters, expressionContext(workflow.Item{JSON: map[string]any{}}, input, request, 0))
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	endpoint := strings.TrimSpace(textValue(resolved["serverUrl"], ""))
	if endpoint == "" {
		return nil, fmt.Errorf("node %q: serverUrl is required", ir.Name)
	}
	target, err := url.Parse(endpoint)
	if err != nil || target == nil {
		return nil, fmt.Errorf("node %q: serverUrl is not a valid URL", ir.Name)
	}
	if strings.ToLower(target.Scheme) == "stdio" {
		return nil, fmt.Errorf("node %q: serverUrl uses stdio, which would run a local process: point it at the server's streamable HTTP endpoint instead", ir.Name)
	}
	// The same pre-flight gate the HTTP node applies. The dialer refuses a
	// private address on its own, but only once DNS has answered — so a
	// disallowed host that does not resolve would fail as a lookup error
	// rather than as the policy refusal it is.
	if err := executor.policy.CheckURL(target); err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	callCtx := ctx
	if timeout := mcpTimeout(resolved, executor.policy); timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	listed, err := executor.listTools(callCtx, ir, request, endpoint)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	wanted := mcpToolAllowlist(resolved)
	items := make([]workflow.Item, 0, len(listed))
	seen := make(map[string]bool, len(listed))
	for _, tool := range listed {
		if wanted != nil && !wanted[tool.name] {
			continue
		}
		seen[tool.name] = true
		credentials := make(map[string]any, len(ir.Credentials))
		for typeID, id := range ir.Credentials {
			credentials[typeID] = id
		}
		// The descriptor carries the credential id, never the secret: it
		// travels in an item that is persisted in the execution record, and
		// the agent re-resolves the secret when it actually calls the tool.
		items = append(items, workflow.Item{JSON: map[string]any{descriptorKey: map[string]any{
			"kind":           toolKindMCP,
			"name":           tool.name,
			"description":    tool.description,
			"nodeName":       ir.Name,
			"serverUrl":      endpoint,
			"inputSchema":    tool.schema,
			"timeoutSeconds": timeoutParameter(resolved, "requestTimeoutSeconds"),
			"credentials":    credentials,
		}}})
	}
	if wanted != nil {
		missing := make([]string, 0)
		for name := range wanted {
			if !seen[name] {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("node %q: the MCP server has no tools named %s", ir.Name, strings.Join(missing, ", "))
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("node %q: the MCP server listed no tools to expose", ir.Name)
	}
	return workflow.NodeOutput{items}, nil
}

// mcpToolAllowlist parses the node's tool selection. Empty means expose
// every tool the server lists; anything else is an exact-name allowlist.
// A plain string rather than a multi-option select: the option list can only
// be populated once dynamic load-options exist, and a select that cannot be
// populated would lie about what it offers.
func mcpToolAllowlist(parameters map[string]any) map[string]bool {
	raw := strings.TrimSpace(textValue(parameters["tools"], ""))
	if raw == "" {
		return nil
	}
	wanted := make(map[string]bool)
	for _, name := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			wanted[trimmed] = true
		}
	}
	return wanted
}

// mcpTimeout resolves the node's time budget the way the HTTP executor does:
// the node may only tighten the deployment's ceiling, never raise it.
func mcpTimeout(parameters map[string]any, policy safehttp.Policy) time.Duration {
	timeout := policy.Timeout
	if seconds := timeoutParameter(parameters, "requestTimeoutSeconds"); seconds > 0 {
		requested := time.Duration(seconds * float64(time.Second))
		if policy.Timeout <= 0 || requested < policy.Timeout {
			timeout = requested
		}
	}
	return timeout
}

// mcpListedTool is one entry of an MCP tools/list result.
type mcpListedTool struct {
	name        string
	description string
	schema      map[string]any
}

// listTools runs the Streamable HTTP handshake — initialize, the initialized
// notification, then tools/list — and returns what the server publishes.
// Resources and prompts are out of scope: this node exposes tools only.
func (executor *MCPClientToolExecutor) listTools(ctx context.Context, ir workflow.IRNode, request engine.Request, endpoint string) ([]mcpListedTool, error) {
	_, session, err := executor.mcpCall(ctx, ir, request, endpoint, "", "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "kilasflow", "version": "1"},
	})
	if err != nil {
		return nil, err
	}
	if err := executor.mcpNotify(ctx, ir, request, endpoint, session); err != nil {
		return nil, err
	}
	raw, _, err := executor.mcpCall(ctx, ir, request, endpoint, session, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var decoded struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("the MCP server returned no tools/list result")
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("the MCP server's tools/list result is not valid JSON: %w", err)
	}
	listed := make([]mcpListedTool, 0, len(decoded.Tools))
	for _, entry := range decoded.Tools {
		if strings.TrimSpace(entry.Name) == "" {
			return nil, fmt.Errorf("the MCP server listed a tool with no name")
		}
		schema := entry.InputSchema
		if len(schema) == 0 {
			schema = map[string]any{"type": "object"}
		}
		listed = append(listed, mcpListedTool{name: entry.Name, description: entry.Description, schema: schema})
	}
	return listed, nil
}

// callTool runs one tools/call round trip over a fresh handshake. A fresh
// handshake per call costs two extra round trips against holding one session
// open for the whole agent run; it keeps the tool stateless — no shared
// session cache with expiry and invalidation to get wrong — and MCP
// initialize is cheap beside a model turn.
func (executor *MCPClientToolExecutor) callTool(ctx context.Context, ir workflow.IRNode, request engine.Request, endpoint, name string, arguments map[string]any) (string, error) {
	_, session, err := executor.mcpCall(ctx, ir, request, endpoint, "", "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "kilasflow", "version": "1"},
	})
	if err != nil {
		return "", err
	}
	if err := executor.mcpNotify(ctx, ir, request, endpoint, session); err != nil {
		return "", err
	}
	raw, _, err := executor.mcpCall(ctx, ir, request, endpoint, session, "tools/call", map[string]any{
		"name": name, "arguments": arguments,
	})
	if err != nil {
		return "", err
	}
	var decoded struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", fmt.Errorf("the MCP server's tools/call result is not valid JSON: %w", err)
	}
	texts := make([]string, 0, len(decoded.Content))
	for _, block := range decoded.Content {
		if block.Type == "text" {
			texts = append(texts, block.Text)
		}
	}
	joined := strings.Join(texts, "\n")
	if decoded.IsError {
		if joined == "" {
			joined = "the tool reported an error without detail"
		}
		return "", fmt.Errorf("MCP tool %q reported an error: %s", name, joined)
	}
	if joined == "" {
		// No text blocks: hand the model the raw result rather than an
		// empty turn that says nothing.
		if len(raw) == 0 {
			return "", fmt.Errorf("MCP tool %q returned nothing", name)
		}
		return string(raw), nil
	}
	return joined, nil
}

// mcpCall posts one JSON-RPC request and returns its result. Authentication
// goes through the runtime's Authenticate, exactly as an HTTP Request node's
// does: ownership, type and domain scope are checked before the secret
// touches the request, and the token never appears in a log line here.
func (executor *MCPClientToolExecutor) mcpCall(ctx context.Context, ir workflow.IRNode, request engine.Request, endpoint, session, method string, params map[string]any) (json.RawMessage, string, error) {
	payload := map[string]any{"jsonrpc": "2.0", "id": mcpRequestIDs.Add(1), "method": method}
	if params != nil {
		payload["params"] = params
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("MCP %s: encode request: %w", method, err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, "", fmt.Errorf("MCP %s: build request: %w", method, err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json, text/event-stream")
	if session != "" {
		httpRequest.Header.Set("mcp-session-id", session)
	}
	if err := request.Authenticate(ctx, ir, httpRequest); err != nil {
		return nil, "", err
	}
	response, err := executor.client.Do(httpRequest)
	if err != nil {
		// An MCP server's credential may sit in the URL's query; the
		// transport error prints the URL, so only its scheme and host go on.
		return nil, "", fmt.Errorf("MCP %s: %w", method, safehttp.RedactError(err))
	}
	defer response.Body.Close()
	next := response.Header.Get("mcp-session-id")
	body, err := io.ReadAll(io.LimitReader(response.Body, mcpMaxResponseBytes(executor.policy)))
	if err != nil {
		return nil, "", fmt.Errorf("MCP %s: read response: %w", method, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("MCP %s failed with status %d: %s", method, response.StatusCode, mcpErrorSnippet(body))
	}
	envelope, err := mcpEnvelopeFor(method, response, body)
	if err != nil {
		return nil, "", err
	}
	if envelope.Error != nil {
		return nil, "", fmt.Errorf("MCP %s failed: %s", method, envelope.Error.Message)
	}
	return envelope.Result, next, nil
}

// mcpNotify posts a JSON-RPC notification, which carries no id and expects
// no result: a 202 with an empty body is the ordinary answer.
func (executor *MCPClientToolExecutor) mcpNotify(ctx context.Context, ir workflow.IRNode, request engine.Request, endpoint, session string) error {
	encoded, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if err != nil {
		return fmt.Errorf("MCP notifications/initialized: encode request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("MCP notifications/initialized: build request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json, text/event-stream")
	if session != "" {
		httpRequest.Header.Set("mcp-session-id", session)
	}
	if err := request.Authenticate(ctx, ir, httpRequest); err != nil {
		return err
	}
	response, err := executor.client.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("MCP notifications/initialized: %w", safehttp.RedactError(err))
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, mcpMaxResponseBytes(executor.policy)))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("MCP notifications/initialized failed with status %d", response.StatusCode)
	}
	return nil
}

// mcpMaxResponseBytes bounds one MCP response body in memory. The deployment
// policy's bound applies; an unset bound falls back to the policy default
// rather than to unbounded.
func mcpMaxResponseBytes(policy safehttp.Policy) int64 {
	if policy.MaxResponseBytes > 0 {
		return policy.MaxResponseBytes
	}
	return 8 << 20
}

// mcpErrorSnippet renders a failing response body for a diagnostic: trimmed
// and cut short, so an HTML error page does not flood the tool turn.
func mcpErrorSnippet(body []byte) string {
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 512 {
		snippet = snippet[:512] + "…"
	}
	if snippet == "" {
		return "no response body"
	}
	return snippet
}

// mcpEnvelopeFor decodes one JSON-RPC response, whether the server answered
// with plain JSON or with a text/event-stream carrying data events.
func mcpEnvelopeFor(method string, response *http.Response, body []byte) (mcpRPCEnvelope, error) {
	payload := body
	if strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		payload = mcpEventPayload(body)
	}
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return mcpRPCEnvelope{}, fmt.Errorf("MCP %s returned an empty response", method)
	}
	candidates := []json.RawMessage{payload}
	if payload[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(payload, &batch); err != nil {
			return mcpRPCEnvelope{}, fmt.Errorf("MCP %s response is not valid JSON: %w", method, err)
		}
		candidates = batch
	}
	for _, candidate := range candidates {
		var envelope mcpRPCEnvelope
		if err := json.Unmarshal(candidate, &envelope); err != nil {
			continue
		}
		if envelope.Error != nil || len(envelope.Result) > 0 {
			return envelope, nil
		}
	}
	return mcpRPCEnvelope{}, fmt.Errorf("MCP %s response is not valid JSON-RPC", method)
}

// mcpRPCEnvelope is the response half of one JSON-RPC exchange.
type mcpRPCEnvelope struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// mcpEventPayload extracts the last data event from a text/event-stream
// body. Multi-line data fields join with newlines per the SSE framing; event
// names and comments carry no JSON-RPC payload and are skipped.
func mcpEventPayload(body []byte) []byte {
	var last []byte
	var current []string
	flush := func() {
		if len(current) > 0 {
			last = []byte(strings.Join(current, "\n"))
			current = nil
		}
	}
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSuffix(line, "\r")
		switch {
		case strings.HasPrefix(trimmed, "data:"):
			value := strings.TrimPrefix(trimmed, "data:")
			value = strings.TrimPrefix(value, " ")
			current = append(current, value)
		case trimmed == "" || strings.HasPrefix(trimmed, ":") || strings.HasPrefix(trimmed, "event:"):
			flush()
		}
	}
	flush()
	if last == nil {
		return bytes.TrimSpace(body)
	}
	return last
}

// mcpToolFrom wraps an MCP tool descriptor as a callable tool. The call goes
// through the executor's shared MCP transport, so it inherits the SSRF
// policy, credential scoping and response limits rather than reimplementing
// any of them.
func (executor *AgentExecutor) mcpToolFrom(ir workflow.IRNode, descriptor map[string]any, request engine.Request) (ai.Tool, error) {
	name := textValue(descriptor["name"], "")
	if name == "" {
		return nil, fmt.Errorf("node %q: a connected tool has no name", ir.Name)
	}
	serverURL := strings.TrimSpace(textValue(descriptor["serverUrl"], ""))
	if serverURL == "" {
		return nil, fmt.Errorf("node %q: connected tool %q names no MCP server", ir.Name, textValue(descriptor["nodeName"], name))
	}
	schema, _ := descriptor["inputSchema"].(map[string]any)
	credentials := map[string]string{}
	if raw, ok := descriptor["credentials"].(map[string]any); ok {
		for typeID, id := range raw {
			if text, ok := id.(string); ok {
				credentials[typeID] = text
			}
		}
	}
	parameters, _ := descriptor["parameters"].(map[string]any)
	if parameters == nil {
		if timeout, ok := descriptor["timeoutSeconds"]; ok {
			parameters = map[string]any{"requestTimeoutSeconds": timeout}
		}
	}
	return &mcpTool{
		server:      executor.mcp,
		name:        name,
		description: textValue(descriptor["description"], ""),
		schema:      schema,
		serverURL:   serverURL,
		node: workflow.IRNode{
			ID: ir.ID + ":" + name, Name: textValue(descriptor["nodeName"], name),
			Type: MCPClientToolNodeType, TypeVersion: workflow.V(1),
			Parameters: parameters, Credentials: credentials,
			Definition: workflow.NodeDefinition{
				Type: MCPClientToolNodeType, Version: workflow.V(1),
				Outputs: mainOutput(), ExecutorID: MCPClientToolExecutorID,
			},
		},
		timeout: mcpTimeout(parameters, executor.policy),
		request: request,
	}, nil
}

// mcpTool is one MCP server tool exposed to the model under the server's own
// name and input schema, so a model tool call produces a real tools/call
// round trip whose result returns as a tool turn.
type mcpTool struct {
	server      *MCPClientToolExecutor
	name        string
	description string
	schema      map[string]any
	serverURL   string
	node        workflow.IRNode
	timeout     time.Duration
	request     engine.Request
}

func (tool *mcpTool) Definition() ai.ToolDefinition {
	parameters := tool.schema
	if len(parameters) == 0 {
		parameters = map[string]any{"type": "object"}
	}
	return ai.ToolDefinition{Name: tool.name, Description: tool.description, Parameters: parameters}
}

// Invoke runs one tools/call round trip with the model's arguments. A
// failure — unreachable server, server error, over-budget call — returns an
// error the agent reports back to the model as a tool turn, without aborting
// the surrounding execution.
func (tool *mcpTool) Invoke(ctx context.Context, arguments json.RawMessage) (string, error) {
	args := map[string]any{}
	if len(arguments) > 0 && json.Valid(arguments) {
		var decoded map[string]any
		if err := json.Unmarshal(arguments, &decoded); err != nil {
			return "", fmt.Errorf("tool arguments are not valid JSON: %w", err)
		}
		if decoded != nil {
			args = decoded
		}
	}
	callCtx := ctx
	if tool.timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, tool.timeout)
		defer cancel()
	}
	target, err := url.Parse(tool.serverURL)
	if err != nil || target == nil {
		return "", fmt.Errorf("tool %q: the MCP server URL is not valid", tool.name)
	}
	// Pre-flight again at call time: the descriptor outlives the listing,
	// and the deployment's allowlist may have changed since.
	if err := tool.server.policy.CheckURL(target); err != nil {
		return "", fmt.Errorf("tool %q: %w", tool.name, err)
	}
	return tool.server.callTool(callCtx, tool.node, tool.request, tool.serverURL, tool.name, args)
}
