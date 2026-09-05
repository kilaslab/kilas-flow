package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/ai"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/expression"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// Executor IDs for the AI family.
const (
	ChatModelExecutorID           = "core.chatModel"
	OpenAIChatModelExecutorID     = "core.openAiChatModel"
	OpenRouterChatModelExecutorID = "core.openRouterChatModel"
	MemoryExecutorID              = "core.memoryBuffer"
	HTTPToolExecutorID            = "core.httpTool"
	AgentExecutorID               = "core.agent"
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
	AgentNodeType               = "kilasflow.agent"
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
				Description: "How long one run may wait for the model, in milliseconds. Above this deployment's ceiling the node is refused rather than quietly given less.",
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
				Key: "sessionId", Label: "Session ID", Kind: node.PropertyString, Required: true,
				Description: "Identifies the conversation. Supports expressions, for example {{ $json.chatId }}.",
			},
			{Key: "maxMessages", Label: "Maximum messages", Kind: node.PropertyNumber, Default: 40},
			{Key: "maxAgeMinutes", Label: "Maximum age (minutes)", Kind: node.PropertyNumber, Default: 1440},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     MemoryExecutorID,
		Validate:       validateMemoryConfiguration,
	}
}

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
			Key: "toolName", Label: "Tool name", Kind: node.PropertyString, Required: true, Default: "http_request",
			Description: "The name the model calls. Letters, digits, and underscores.",
		},
		{
			Key: "toolDescription", Label: "Tool description", Kind: node.PropertyString, Required: true,
			Description: "What the tool does, written for the model.",
		},
	}, definition.Parameters...)
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
		},
		Outputs: mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "prompt", Label: "Prompt", Kind: node.PropertyString, Required: true,
				Description: "The user turn for this run. Supports expressions.",
			},
			{Key: "systemPrompt", Label: "System prompt", Kind: node.PropertyString},
			{Key: "maxIterations", Label: "Maximum tool iterations", Kind: node.PropertyNumber, Default: 8},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     AgentExecutorID,
		Validate:       validateAgentConfiguration,
	}
}

func validateChatModelConfiguration(n workflow.Node) error {
	if statementText(n.Parameters, "model") == "" {
		return fmt.Errorf("model is required")
	}
	// A chat model without a credential would have to fall back to an ambient
	// key, and an ambient key is exactly what must not exist.
	if id, found := n.Credentials[BearerCredentialType]; !found || strings.TrimSpace(id) == "" {
		return fmt.Errorf("an httpBearerAuth credential holding the API key is required")
	}
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
	if statementText(n.Parameters, "sessionId") == "" {
		return fmt.Errorf("sessionId is required")
	}
	return nil
}

func validateHTTPToolConfiguration(n workflow.Node) error {
	name := statementText(n.Parameters, "toolName")
	if name == "" {
		return fmt.Errorf("toolName is required")
	}
	if !validToolName(name) {
		return fmt.Errorf("toolName must contain only letters, digits, and underscores")
	}
	if statementText(n.Parameters, "toolDescription") == "" {
		return fmt.Errorf("toolDescription is required so the model knows when to call it")
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
	credentialID := strings.TrimSpace(ir.Credentials[BearerCredentialType])
	if credentialID == "" {
		return nil, fmt.Errorf("node %q: an httpBearerAuth credential is required", ir.Name)
	}
	if _, err := resolveModelCredential(ctx, ir, request, credentialID, BearerCredentialType); err != nil {
		return nil, err
	}

	descriptor := modelDescriptorFor(ir, credentialID, BearerCredentialType,
		textValue(ir.Parameters["model"], "gpt-4o-mini"),
		textValue(ir.Parameters["baseUrl"], "https://api.openai.com/v1"))
	// Presence, not value. `temperature: 0` is a real instruction — it is what
	// a user asks for when they want deterministic extraction — and the old
	// `!= 0` guard dropped it, leaving the provider to apply its own default
	// on the one setting the user had been most explicit about.
	copyPresentOptions(descriptor, ir.Parameters, samplingOptionKeys)
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
		"stream":         boolValue(ir.Parameters["stream"]),
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
func executeMemory(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// The session ID may reference the triggering item, so it is resolved here
	// rather than treated as a fixed string.
	parameters, err := expression.Resolve(ir.Parameters, expressionContext(request.Input, input, request, 0))
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	sessionID := strings.TrimSpace(textValue(parameters["sessionId"], ""))
	if sessionID == "" {
		return nil, fmt.Errorf("node %q: sessionId resolved to an empty value", ir.Name)
	}
	return workflow.NodeOutput{{{JSON: map[string]any{descriptorKey: map[string]any{
		"kind":          "memory",
		"sessionId":     sessionID,
		"maxMessages":   numberValue(parameters["maxMessages"]),
		"maxAgeMinutes": numberValue(parameters["maxAgeMinutes"]),
	}}}}}, nil
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
		"name":        textValue(ir.Parameters["toolName"], "http_request"),
		"description": textValue(ir.Parameters["toolDescription"], ""),
		"nodeName":    ir.Name,
		"parameters":  parameters,
		"credentials": credentials,
	}}}}}, nil
}

// AgentExecutor runs the AI Agent node.
//
// It depends on ai.AgentRuntime and ai.ChatModel, never on a provider type, so
// the runtime behind it is replaceable without touching this node.
type AgentExecutor struct {
	runtime ai.AgentRuntime
	client  *http.Client
	memory  ai.Memory
	// policy is kept so a model's base URL gets the same pre-flight gate an
	// HTTP Request node's URL does, before a socket is opened.
	policy safehttp.Policy
	// fallbackTimeout is what a model node that names none waits, so a node
	// saved before the option existed behaves exactly as it did.
	fallbackTimeout time.Duration
	// timeoutCeiling is the longest this deployment lets one model node wait.
	timeoutCeiling time.Duration
	// httpTool is the same executor the HTTP Request node uses, so exposing an
	// HTTP call as a tool reuses that implementation rather than copying it.
	httpTool *HTTPExecutor
}

// NewAgentExecutor builds the agent node's executor.
func NewAgentExecutor(runtime ai.AgentRuntime, policy safehttp.Policy, memory ai.Memory, options ...AgentOption) *AgentExecutor {
	if runtime == nil {
		runtime = ai.NewLoopRuntime()
	}
	// The model client is the deployment's own policy with its clock removed,
	// not a second policy. http.Client.Timeout is a ceiling a context deadline
	// can only lower, so leaving the outbound default of thirty seconds on it
	// would make every longer per-node timeout unreachable — a slow completion
	// would die as a transport error rather than as a model error. Everything
	// that defends the call is unchanged: CheckAddress runs in the same dialer
	// on every hop, and CheckRedirect applies the same allowlist, because both
	// close over this same Policy value.
	modelPolicy := policy
	modelPolicy.Timeout = 0

	executor := &AgentExecutor{
		runtime: runtime,
		// Model calls go through the deployment's outbound policy exactly as an
		// HTTP Request node's do, so an agent cannot reach an internal address
		// the rest of the product refuses.
		client:          safehttp.NewClient(modelPolicy),
		memory:          memory,
		policy:          policy,
		fallbackTimeout: policy.Timeout,
		timeoutCeiling:  DefaultModelTimeoutCeiling,
		httpTool:        NewHTTPExecutor(policy),
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

// modelTimeout resolves how long one run of this agent may wait for its model.
//
// The deadline bounds the whole turn rather than each individual model call.
// The runtime owns the tool loop, and threading a per-call deadline through
// AgentRuntime would push a transport concern into the agent contract that
// every future runtime would have to honour. For the single-call case the two
// are the same, and for a tool loop this is the bound a user actually means:
// how long this node may take.
func (executor *AgentExecutor) modelTimeout(descriptor map[string]any) (time.Duration, error) {
	ceiling := executor.timeoutCeiling
	if ceiling <= 0 {
		ceiling = DefaultModelTimeoutCeiling
	}
	// Milliseconds, because that is the unit the option is expressed in and
	// interoperating with the format means keeping its unit too.
	milliseconds := numberValue(descriptor[ModelOptionTimeout])
	if milliseconds <= 0 {
		if executor.fallbackTimeout > 0 {
			return executor.fallbackTimeout, nil
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

	credentialID, _ := modelDescriptor["credentialId"].(string)
	if request.Credentials == nil {
		return nil, fmt.Errorf("node %q: credentials are not available in this runtime", ir.Name)
	}
	resolved, err := request.Credentials.ResolveCredential(ctx, credentialID)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	apiKeyField, usable := modelAPIKeyFields[resolved.Type]
	if !usable {
		return nil, fmt.Errorf("node %q: credential %q is a %s credential, which no chat model can authenticate with",
			ir.Name, resolved.Name, resolved.Type)
	}

	baseURL := textValue(modelDescriptor["baseUrl"], "https://api.openai.com/v1")
	target, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("node %q: the model's base URL is not a valid URL", ir.Name)
	}
	// The same pre-flight gate the HTTP node applies. The dialer refuses a
	// private address on its own, but only once DNS has answered — so a
	// disallowed host that does not resolve would fail as a lookup error rather
	// than as the policy refusal it is.
	if err := executor.policy.CheckURL(target); err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	// A model call is an outbound request carrying a secret, so the credential's
	// own domain scope binds it exactly as it binds an HTTP Request node's. It
	// did not before: a credential scoped to api.openai.com could be pointed at
	// any host by editing one parameter on the model node.
	if !resolved.AllowsHost(target.Host) {
		return nil, fmt.Errorf("node %q: credential %q is not allowed for host %q",
			ir.Name, resolved.Name, target.Hostname())
	}

	timeout, err := executor.modelTimeout(modelDescriptor)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	model := ai.NewOpenAICompatible(executor.client, baseURL, resolved.Fields[apiKeyField])

	tools := make([]ai.Tool, 0, len(toolDescriptors))
	for _, descriptor := range toolDescriptors {
		tool, err := executor.httpToolFrom(ir, descriptor, request)
		if err != nil {
			return nil, err
		}
		tools = append(tools, tool)
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

		agentRequest := ai.AgentRequest{
			Model:         model,
			ModelName:     textValue(modelDescriptor["model"], "gpt-4o-mini"),
			SystemPrompt:  textValue(parameters["systemPrompt"], ""),
			Input:         textValue(parameters["prompt"], ""),
			Tools:         tools,
			MaxIterations: int(numberValue(parameters["maxIterations"])),
			// A model's own -1 means "as many as the model allows", which is
			// said by sending nothing rather than by sending a negative bound
			// the provider would refuse.
			MaxTokens:  positiveInt(numberValue(modelDescriptor[ModelOptionMaxTokens])),
			MaxRetries: positiveInt(numberValue(modelDescriptor[ModelOptionMaxRetries])),
			Stream:     boolValue(modelDescriptor["stream"]),
			// Read back by presence for the reason they were written by
			// presence: zero is a value a user chooses, not the absence of one.
			Temperature:      presentNumber(modelDescriptor, ModelOptionTemperature),
			TopP:             presentNumber(modelDescriptor, ModelOptionTopP),
			FrequencyPenalty: presentNumber(modelDescriptor, ModelOptionFrequencyPenalty),
			PresencePenalty:  presentNumber(modelDescriptor, ModelOptionPresencePenalty),
		}
		if hasMemory && executor.memory != nil {
			agentRequest.Memory = executor.memory
			agentRequest.Session = ai.SessionKey{
				TenantID:   request.Execution.TenantID,
				WorkflowID: request.Execution.WorkflowID,
				SessionID:  textValue(memoryDescriptor["sessionId"], ""),
			}
		}

		events := make([]any, 0, 8)
		// Cancelled explicitly rather than deferred: this is a loop, and a
		// deferred cancel would hold every item's context alive until the whole
		// node finished.
		runCtx, cancel := context.WithTimeout(ctx, timeout)
		result, err := executor.runtime.Run(runCtx, agentRequest, func(event ai.Event) {
			events = append(events, event)
			request.Events.Emit(engine.NodeEvent{
				NodeID: ir.ID, Name: string(event.Kind), Detail: eventDetail(event),
			})
		})
		cancel()
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}

		out = append(out, workflow.Item{JSON: map[string]any{
			"output":     result.Output,
			"usage":      map[string]any{"promptTokens": float64(result.Usage.PromptTokens), "completionTokens": float64(result.Usage.CompletionTokens), "totalTokens": float64(result.Usage.TotalTokens)},
			"iterations": float64(result.Iterations),
			"toolCalls":  float64(result.ToolCalls),
		}})
	}
	return workflow.NodeOutput{out}, nil
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
// supplied.
func (tool *httpRequestTool) Invoke(ctx context.Context, arguments json.RawMessage) (string, error) {
	item := workflow.Item{JSON: map[string]any{}}
	if len(arguments) > 0 && json.Valid(arguments) {
		var decoded map[string]any
		if err := json.Unmarshal(arguments, &decoded); err == nil {
			if nested, ok := decoded["input"].(map[string]any); ok {
				item.JSON = nested
			} else {
				item.JSON = decoded
			}
		}
	}

	output, err := tool.executor.Execute(ctx, tool.node, workflow.NodeInput{"main": {item}}, tool.request)
	if err != nil {
		return "", err
	}
	if len(output) == 0 || len(output[0]) == 0 {
		return "", fmt.Errorf("tool returned nothing")
	}
	encoded, err := json.Marshal(output[0][0].JSON)
	if err != nil {
		return "", fmt.Errorf("encode tool result: %w", err)
	}
	return string(encoded), nil
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
