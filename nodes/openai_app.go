// The provider app nodes: OpenAI and Google Gemini as steps a workflow calls
// directly, with the model named on the node rather than wired in as a
// sub-node.
//
// n8n ships these as `@n8n/n8n-nodes-langchain.openAi` and
// `@n8n/n8n-nodes-langchain.googleGemini`, and they are the shapes an imported
// workflow most often uses to reach a model: a chat completion, a question
// about a picture, a transcription. Both providers publish OpenAI-compatible
// endpoints, so both nodes run over the same internal/ai adapter the chat
// model sub-node uses — the retries, the per-request timeout and the vision
// parts are that adapter's, not a copy of them here. Google's compatibility
// endpoint is https://generativelanguage.googleapis.com/v1beta/openai, which
// is also what the chat-model mapping for `lmChatGoogleGemini` writes.
//
// The parameter keys mirror n8n's stored names — `modelId`, `messages` with
// its `messageValues` rows, `options` — so an importer maps this node by type
// name alone and the parameters carry across unchanged.
package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Executor IDs for the provider app nodes.
const (
	OpenAIAppExecutorID       = "core.openAiApp"
	GoogleGeminiAppExecutorID = "core.googleGeminiApp"
)

// Node types for the provider app nodes, named after n8n's own so the mapping
// an importer needs is the identity on the last segment. The Gemini
// credential's ID lives with the other credential types in ai.go.
const (
	OpenAIAppNodeType       = "kilasflow.openAi"
	GoogleGeminiAppNodeType = "kilasflow.googleGemini"
)

// The resources and operations the app nodes carry, under n8n's names.
const (
	appResourceText  = "text"
	appResourceImage = "image"
	appResourceAudio = "audio"

	appOperationMessage    = "message"
	appOperationAnalyze    = "analyze"
	appOperationTranscribe = "transcribe"
)

// maxTranscriptionUploadBytes is the size OpenAI itself refuses an audio file
// beyond. Refused here with the number named, rather than by the provider
// after the whole upload has crossed the network.
const maxTranscriptionUploadBytes = 25 << 20

// appModelProvider is one provider whose app node speaks the OpenAI
// chat-completions protocol.
type appModelProvider struct {
	nodeType       string
	executorID     string
	displayName    string
	description    string
	credentialType string
	baseURL        string
	defaultModel   string
	modelHint      string
	// modelPrefix is part of the provider's model names and not part of the
	// chat-completions endpoint's: Gemini lists `models/<id>` and completes
	// with `<id>`.
	modelPrefix    string
	artworkKey     string
	accent         string
	defaultTimeout float64
}

func openAIAppProvider() appModelProvider {
	return appModelProvider{
		nodeType:       OpenAIAppNodeType,
		executorID:     OpenAIAppExecutorID,
		displayName:    "OpenAI",
		description:    "Calls OpenAI directly: message a model, have it read an image, or transcribe audio.",
		credentialType: OpenAICredentialType,
		baseURL:        "https://api.openai.com/v1",
		defaultModel:   "gpt-4o-mini",
		modelHint:      "gpt-4o-mini, or whisper-1 for transcription",
		artworkKey:     "openai",
		accent:         "#10a37f",
		defaultTimeout: 60000,
	}
}

func googleGeminiAppProvider() appModelProvider {
	return appModelProvider{
		nodeType:       GoogleGeminiAppNodeType,
		executorID:     GoogleGeminiAppExecutorID,
		displayName:    "Google Gemini",
		description:    "Messages a Gemini model through Google's OpenAI-compatible endpoint.",
		credentialType: GoogleGeminiCredentialType,
		baseURL:        "https://generativelanguage.googleapis.com/v1beta/openai",
		defaultModel:   "gemini-2.5-flash",
		modelHint:      "gemini-2.5-flash",
		modelPrefix:    "models/",
		artworkKey:     "gemini",
		accent:         "#4285f4",
		defaultTimeout: 60000,
	}
}

// appAPIKeyFields names the field each accepted credential type keeps its key
// in, on the same pattern as the chat models' table: a credential added to the
// registry needs one entry here and no new control flow.
var appAPIKeyFields = map[string]string{
	OpenAICredentialType:       "apiKey",
	GoogleGeminiCredentialType: "apiKey",
}

func openAIAppNode() node.Definition       { return appModelNode(openAIAppProvider(), true) }
func googleGeminiAppNode() node.Definition { return appModelNode(googleGeminiAppProvider(), false) }

// appModelNode builds one provider's app node definition.
//
// The OpenAI node carries the full resource surface (text, image, audio); the
// Gemini node carries the one resource its templates use — the chat message,
// which is also the one operation this server runs for it.
func appModelNode(provider appModelProvider, fullSurface bool) node.Definition {
	resource := node.PropertyDefinition{
		Key: "resource", Label: "Resource", Kind: node.PropertyOptions, Required: true, Default: appResourceText,
		Options: []node.PropertyOption{
			{Label: "Text", Value: appResourceText},
			{Label: "Image", Value: appResourceImage},
			{Label: "Audio", Value: appResourceAudio},
		},
		Description: "Which kind of call to make.",
	}
	operation := node.PropertyDefinition{
		Key: "operation", Label: "Operation", Kind: node.PropertyOptions,
		Options: []node.PropertyOption{
			{Label: "Message a Model", Value: appOperationMessage},
			{Label: "Analyze Image", Value: appOperationAnalyze},
			{Label: "Transcribe Audio", Value: appOperationTranscribe},
		},
		Description: "What to do with the model. Left unset, the operation follows the resource.",
	}
	if !fullSurface {
		resource = node.PropertyDefinition{
			Key: "resource", Label: "Resource", Kind: node.PropertyOptions, Required: true, Default: appResourceText,
			Options:     []node.PropertyOption{{Label: "Text", Value: appResourceText}},
			Description: "The chat message, sent through Gemini's OpenAI-compatible endpoint.",
		}
		operation = node.PropertyDefinition{
			Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Default: appOperationMessage,
			Options:     []node.PropertyOption{{Label: "Message a Model", Value: appOperationMessage}},
			Description: "Messages the model with the message list below.",
		}
	}
	parameters := []node.PropertyDefinition{
		resource,
		operation,
		{
			// A locator rather than a select, for the same reason as the chat
			// model nodes: a catalogue this server could not fetch — an
			// expired key, an outage, a model released this morning — must not
			// leave the user with an empty dropdown and no way to name a model
			// at all. The key is n8n's own `modelId`, so an imported node's
			// stored locator carries across unchanged.
			Key: "modelId", Label: "Model", Kind: node.PropertyResourceLocator, Required: true,
			Description: "Choose from the provider's catalogue, or type a model name.",
			Default: map[string]any{
				property.LocatorSentinel: true, "mode": "list", "value": provider.defaultModel,
			},
			Modes: []node.PropertyMode{
				{
					Name: "list", Label: "From list", Kind: node.PropertyOptions, Placeholder: "Choose…",
					LoadOptions: &property.OptionsLoader{
						Source: property.LoaderHTTP, Method: http.MethodGet,
						BaseURLParameter: "baseUrl",
						Endpoint:         "/models",
						CredentialType:   provider.credentialType,
						ItemsPath:        "data",
						ValueField:       "id",
						LabelTemplate:    "{{ id }}",
						// Changing the address means a different catalogue, so
						// the list is discarded rather than kept from the
						// previous one.
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
		appMessagesParameter(),
		{
			Key: "jsonOutput", Label: "Output Content as JSON", Kind: node.PropertyBoolean, Default: false,
			Description: "Ask the model for a JSON object. The parsed object is also written to the item's output field.",
			VisibleWhen: []node.VisibilityCondition{{Key: "resource", Equals: appResourceText}},
		},
	}
	if fullSurface {
		parameters = append(parameters,
			node.PropertyDefinition{
				Key: "text", Label: "Prompt", Kind: node.PropertyString, Required: true,
				Description: "What to ask about the image. Supports expressions.",
				VisibleWhen: []node.VisibilityCondition{{Key: "resource", Equals: appResourceImage}},
			},
			node.PropertyDefinition{
				Key: "binaryInput", Label: "Input Binary Field", Kind: node.PropertyString, Default: "data",
				Description: "The binary field of the incoming item holding the image, or the audio file.",
				// Two entries on one key mean "one of these values".
				VisibleWhen: []node.VisibilityCondition{
					{Key: "resource", Equals: appResourceImage},
					{Key: "resource", Equals: appResourceAudio},
				},
			},
			node.PropertyDefinition{
				Key: "language", Label: "Language", Kind: node.PropertyString,
				Description: "The spoken language of the audio, as an ISO-639-1 code such as en or nl. Leave it empty to auto-detect.",
				VisibleWhen: []node.VisibilityCondition{{Key: "resource", Equals: appResourceAudio}},
			},
		)
	}
	parameters = append(parameters, appModelOptions(provider))
	return node.Definition{
		Type:        provider.nodeType,
		Version:     workflow.V(1),
		Credentials: []node.CredentialRequirement{{Type: provider.credentialType, Required: true}},
		DisplayName: provider.displayName,
		Description: provider.description,
		Category:    "AI",
		Group:       []node.NodeGroup{node.GroupTransform},
		// A provider's own mark is the node's own artwork rather than a shared
		// glyph: it is served by the icon route and needs no editor change.
		Icon:           &node.NodeIcon{Light: provider.artworkKey},
		IconLight:      appProviderArtwork(provider),
		IconColor:      provider.accent,
		Subtitle:       "{{ $parameter.resource }}: {{ $parameter.operation }}",
		Inputs:         mainInput(),
		Outputs:        mainOutput(),
		Parameters:     parameters,
		SharedSettings: sharedSettings(),
		ExecutorID:     provider.executorID,
		Validate:       validateAppModelNode(provider, fullSurface),
	}
}

// appMessagesParameter is the ordered message list both app nodes carry, in
// n8n's shape: a fixed collection of rows under `messageValues`, each a role
// and its content.
func appMessagesParameter() node.PropertyDefinition {
	return node.PropertyDefinition{
		Key: "messages", Label: "Messages", Kind: node.PropertyFixedCollection, Required: true,
		TypeOptions: &node.TypeOptions{MultipleValues: true, MultipleValueButtonText: "Add message"},
		Description: "The conversation sent to the model, in order. Each row's content supports expressions.",
		VisibleWhen: []node.VisibilityCondition{{Key: "resource", Equals: appResourceText}},
		Groups: []node.PropertyGroup{{
			Key: "messageValues", Label: "Message",
			Fields: []node.PropertyDefinition{
				{
					Key: "role", Label: "Role", Kind: node.PropertyOptions, Default: chainRoleHuman,
					Options: []node.PropertyOption{
						{Label: "User", Value: chainRoleHuman},
						{Label: "Assistant", Value: chainRoleAI},
						{Label: "System", Value: chainRoleSystem},
					},
					Description: "Who says this row.",
				},
				{
					Key: "content", Label: "Content", Kind: node.PropertyString,
					Description: "The row's text. Supports expressions.",
				},
			},
		}},
	}
}

// appModelOptions is the Options collection both app nodes carry: the same
// sampling and transport members the chat model sub-nodes offer, so a user who
// knows one surface knows the other. An option the user does not add is not
// sent, so the provider applies its own default.
func appModelOptions(provider appModelProvider) node.PropertyDefinition {
	return node.PropertyDefinition{
		Key: modelOptionsKey, Label: "Options", Kind: node.PropertyCollection,
		Description: "An option you do not add is not sent, so the provider applies its own default.",
		Fields: []node.PropertyDefinition{
			{
				Key: ModelOptionFrequencyPenalty, Label: "Frequency Penalty", Kind: node.PropertyNumber, Default: float64(0),
				TypeOptions: &node.TypeOptions{MinValue: numberBound(-2), MaxValue: numberBound(2)},
			},
			{
				Key: ModelOptionMaxTokens, Label: "Maximum Number of Tokens", Kind: node.PropertyNumber, Default: float64(-1),
				Description: "How many tokens the completion may run to. -1 leaves it to the model.",
			},
			{
				Key: ModelOptionMaxRetries, Label: "Max Retries", Kind: node.PropertyNumber, Default: float64(2),
				TypeOptions: &node.TypeOptions{MinValue: numberBound(0)},
				Description: "How many times a rate-limited or failed model request is sent again. A transcription is sent once.",
			},
			{
				Key: ModelOptionPresencePenalty, Label: "Presence Penalty", Kind: node.PropertyNumber, Default: float64(0),
				TypeOptions: &node.TypeOptions{MinValue: numberBound(-2), MaxValue: numberBound(2)},
			},
			{
				Key: ModelOptionTemperature, Label: "Sampling Temperature", Kind: node.PropertyNumber, Default: float64(0.7),
				TypeOptions: &node.TypeOptions{MinValue: numberBound(0), MaxValue: numberBound(2)},
			},
			{
				Key: ModelOptionTimeout, Label: "Timeout", Kind: node.PropertyNumber, Default: provider.defaultTimeout,
				TypeOptions: &node.TypeOptions{MinValue: numberBound(0)},
				Description: "How long one request may wait, in milliseconds. Above this deployment's ceiling the node is refused rather than quietly given less.",
			},
			{
				Key: ModelOptionTopP, Label: "Top P", Kind: node.PropertyNumber, Default: float64(1),
				TypeOptions: &node.TypeOptions{MinValue: numberBound(0), MaxValue: numberBound(1)},
			},
		},
	}
}

// appMessageRow is one row of the app node's message list.
type appMessageRow struct {
	Role    string
	Content string
}

// appMessageRole maps a stored role onto the wire role the adapter sends.
// n8n's own OpenAI node stores `user`/`assistant`/`system`; the chain's
// `human`/`ai` spellings are accepted so the two message surfaces agree.
func appMessageRole(role string) (ai.Role, bool) {
	switch role {
	case "", chainRoleHuman, "user":
		return ai.RoleUser, true
	case chainRoleAI, "assistant":
		return ai.RoleAssistant, true
	case chainRoleSystem:
		return ai.RoleSystem, true
	default:
		return "", false
	}
}

// appMessageRows reads the stored messages fixed collection. An
// expression-valued collection is left for run time; a shape this reader
// cannot parse is refused here rather than misread there.
func appMessageRows(value any) ([]appMessageRow, error) {
	if value == nil || expression.IsExpression(value) {
		return nil, nil
	}
	collection, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("messages must be a fixed collection of message rows")
	}
	// n8n's OpenAI/Gemini app nodes name the row list "messageValues"; accept
	// the plain "values" spelling too so documents written either way load.
	raw, present := collection["messageValues"]
	if !present {
		raw, present = collection["values"]
	}
	if !present {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("messages.messageValues must be a list of message rows")
	}
	rows := make([]appMessageRow, 0, len(list))
	for _, entry := range list {
		fields, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("messages.messageValues must be a list of message rows")
		}
		rows = append(rows, appMessageRow{
			Role:    chainLiteral(fields["role"]),
			Content: chainLiteral(fields["content"]),
		})
	}
	return rows, nil
}

func validateAppModelNode(provider appModelProvider, fullSurface bool) workflow.ConfigValidator {
	return func(n workflow.Node) error {
		if id, found := n.Credentials[provider.credentialType]; !found || strings.TrimSpace(id) == "" {
			return fmt.Errorf("a %s credential holding the API key is required", provider.credentialType)
		}
		if !property.LocatorIsSet(n.Parameters["modelId"]) {
			return fmt.Errorf("modelId is required")
		}
		if !fullSurface {
			if resource := statementText(n.Parameters, "resource"); resource != "" && resource != appResourceText {
				return fmt.Errorf("resource %q is not supported by this node", resource)
			}
			rows, err := appMessageRows(n.Parameters["messages"])
			if err != nil {
				return err
			}
			return validateAppMessageRows(rows)
		}
		resource := statementText(n.Parameters, "resource")
		if resource == "" {
			resource = appResourceText
		}
		operation := statementText(n.Parameters, "operation")
		switch resource {
		case appResourceText:
			if operation != "" && operation != appOperationMessage {
				return fmt.Errorf("text/%s does not exist; this resource runs %q", operation, appOperationMessage)
			}
			rows, err := appMessageRows(n.Parameters["messages"])
			if err != nil {
				return err
			}
			return validateAppMessageRows(rows)
		case appResourceImage:
			if operation != "" && operation != appOperationAnalyze {
				return fmt.Errorf("image/%s does not exist; this resource runs %q", operation, appOperationAnalyze)
			}
			return nil
		case appResourceAudio:
			if operation != "" && operation != appOperationTranscribe {
				return fmt.Errorf("audio/%s does not exist; this resource runs %q", operation, appOperationTranscribe)
			}
			return nil
		default:
			return fmt.Errorf("resource %q is not supported", resource)
		}
	}
}

// validateAppMessageRows refuses a role this node cannot send and a row whose
// content is empty text. An expression-valued content is present-but-unknown
// and is judged at run time.
func validateAppMessageRows(rows []appMessageRow) error {
	if len(rows) == 0 {
		return fmt.Errorf("messages needs at least one message row")
	}
	for index, row := range rows {
		if _, known := appMessageRole(row.Role); !known && row.Role != "expression" {
			return fmt.Errorf("message %d has role %q, want user, assistant, or system", index+1, row.Role)
		}
		if strings.TrimSpace(row.Content) == "" {
			return fmt.Errorf("message %d has empty content", index+1)
		}
	}
	return nil
}

// AppModelExecutor runs one provider's app node.
type AppModelExecutor struct {
	provider appModelProvider
	models   modelBackend
}

// NewAppModelExecutor builds the executor from the deployment's outbound
// policy and model-timeout ceiling, exactly as the agent and chain executors
// are: a model call is an outbound request and cannot reach an address the
// rest of the product refuses, and a node may not wait past the ceiling the
// deployment set.
func NewAppModelExecutor(provider appModelProvider, policy safehttp.Policy, ceiling time.Duration) *AppModelExecutor {
	backend := newModelBackend(policy)
	if ceiling > 0 {
		backend.timeoutCeiling = ceiling
	}
	return &AppModelExecutor{provider: provider, models: backend}
}

// resolvedAppModel is one provider app node bound to a deployment: the adapter
// to call through, the address it calls, and the settings the node's options
// declared.
type resolvedAppModel struct {
	model       ai.ChatModel
	name        string
	baseURL     string
	timeout     time.Duration
	maxRetries  int
	maxTokens   int
	temperature *float64
	topP        *float64
	frequency   *float64
	presence    *float64
}

// Execute runs the node once per incoming item.
func (executor *AppModelExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if executor == nil || executor.models.client == nil {
		return nil, fmt.Errorf("node %q: the app model client is not configured", ir.Name)
	}
	secret, err := executor.resolveCredential(ctx, ir, request)
	if err != nil {
		return nil, err
	}
	bound, err := executor.boundModel(ir, secret)
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
		resource, operation := appResourceOperation(parameters)
		switch resource + "/" + operation {
		case appResourceText + "/" + appOperationMessage:
			produced, err := executor.message(ctx, ir, request, bound, parameters)
			if err != nil {
				return nil, err
			}
			out = append(out, produced)
		case appResourceImage + "/" + appOperationAnalyze:
			produced, err := executor.analyze(ctx, ir, request, bound, parameters, item)
			if err != nil {
				return nil, err
			}
			out = append(out, produced)
		case appResourceAudio + "/" + appOperationTranscribe:
			produced, err := executor.transcribe(ctx, ir, request, bound, parameters, item)
			if err != nil {
				return nil, err
			}
			out = append(out, produced)
		default:
			return nil, fmt.Errorf("node %q: resource %q has no operation %q this node runs", ir.Name, resource, operation)
		}
	}
	return workflow.NodeOutput{out}, nil
}

// resolveCredential reads and checks the node's own credential, on the same
// pattern the chat models resolve theirs: type checked, key read from the
// field the type declares, and never carried anywhere but into the adapter.
func (executor *AppModelExecutor) resolveCredential(ctx context.Context, ir workflow.IRNode, request engine.Request) (engine.Credential, error) {
	if request.Credentials == nil {
		return engine.Credential{}, fmt.Errorf("node %q: credentials are not available in this runtime", ir.Name)
	}
	resolved, found, err := request.ResolveAttachedCredential(ctx, ir, executor.provider.credentialType)
	if err != nil {
		return engine.Credential{}, err
	}
	if !found {
		return engine.Credential{}, fmt.Errorf("node %q: a %s credential holding the API key is required",
			ir.Name, executor.provider.credentialType)
	}
	if _, known := appAPIKeyFields[resolved.Type]; !known {
		return engine.Credential{}, fmt.Errorf("node %q: credential %q is a %s credential, which this node cannot authenticate with",
			ir.Name, resolved.Name, resolved.Type)
	}
	return resolved, nil
}

// appBaseURL is where the node sends its calls: the node's own base URL when
// it names one other than the provider's, else the one an openAiApi credential
// carries, else the provider's — the same order the chat models resolve in, so
// a credential pointed at an OpenAI-compatible endpoint reaches every OpenAI
// node, as it does in n8n.
func appBaseURL(provider appModelProvider, configured any, credentialFields map[string]string) string {
	if chosen := strings.TrimSpace(textValue(configured, "")); chosen != "" && chosen != provider.baseURL {
		return chosen
	}
	if field, carries := credentialBaseURLFields[provider.credentialType]; carries {
		if fromCredential := strings.TrimSpace(credentialFields[field]); fromCredential != "" {
			return fromCredential
		}
	}
	return provider.baseURL
}

// boundModel resolves the node's provider, base URL and options once, before
// any item is served: a misconfigured node fails the run before anything is
// spent.
func (executor *AppModelExecutor) boundModel(ir workflow.IRNode, secret engine.Credential) (resolvedAppModel, error) {
	baseURL := appBaseURL(executor.provider, ir.Parameters["baseUrl"], secret.Fields)
	target, err := url.Parse(baseURL)
	if err != nil {
		return resolvedAppModel{}, fmt.Errorf("node %q: the base URL is not a valid URL", ir.Name)
	}
	if err := executor.models.policy.CheckURL(target); err != nil {
		return resolvedAppModel{}, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	// The credential's own domain scope binds the call exactly as it binds an
	// HTTP Request node's: a key scoped to a provider cannot be aimed at
	// another host by editing one parameter here.
	if !secret.AllowsHost(target.Host) {
		return resolvedAppModel{}, fmt.Errorf("node %q: credential %q is not allowed for host %q",
			ir.Name, secret.Name, target.Hostname())
	}
	options := mapValue(ir.Parameters[modelOptionsKey])
	descriptor := map[string]any{}
	copyPresentOptions(descriptor, options, []string{ModelOptionTimeout})
	timeout, err := executor.models.modelTimeout(descriptor)
	if err != nil {
		return resolvedAppModel{}, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	maxRetries := defaultModelMaxRetries
	if value, present := options[ModelOptionMaxRetries]; present && value != nil {
		maxRetries = positiveInt(numberValue(value))
	}
	apiKey := secret.Fields[appAPIKeyFields[secret.Type]]
	model := ai.NewOpenAICompatible(executor.models.client, baseURL, apiKey)
	if secret.ID != "" {
		// The host check above covers the base URL; the scope carries the
		// same bound through every redirect the call meets.
		model.WithCredentialScope(secret.RedirectScope())
	}
	name := locatorText(ir.Parameters["modelId"])
	if name == "" {
		name = executor.provider.defaultModel
	}
	// The locator may carry the provider's listing prefix, which the
	// completions endpoint does not want.
	name = strings.TrimPrefix(name, executor.provider.modelPrefix)
	return resolvedAppModel{
		model:       model,
		name:        name,
		baseURL:     baseURL,
		timeout:     timeout,
		maxRetries:  maxRetries,
		maxTokens:   positiveInt(numberValue(options[ModelOptionMaxTokens])),
		temperature: presentNumber(options, ModelOptionTemperature),
		topP:        presentNumber(options, ModelOptionTopP),
		frequency:   presentNumber(options, ModelOptionFrequencyPenalty),
		presence:    presentNumber(options, ModelOptionPresencePenalty),
	}, nil
}

// appResourceOperation reads the resource and the operation, defaulting the
// operation to the one its resource runs when the document left it unset.
func appResourceOperation(parameters map[string]any) (string, string) {
	resource := textValue(parameters["resource"], appResourceText)
	operation := textValue(parameters["operation"], "")
	switch {
	case operation != "":
	case resource == appResourceImage:
		operation = appOperationAnalyze
	case resource == appResourceAudio:
		operation = appOperationTranscribe
	default:
		operation = appOperationMessage
	}
	return resource, operation
}

// message runs the text resource's message operation: the stored rows as one
// conversation, one completion, with the JSON ask added when the node wants
// JSON output.
func (executor *AppModelExecutor) message(ctx context.Context, ir workflow.IRNode, request engine.Request, bound resolvedAppModel, parameters map[string]any) (workflow.Item, error) {
	rows, err := appMessageRows(parameters["messages"])
	if err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if len(rows) == 0 {
		return workflow.Item{}, fmt.Errorf("node %q: messages needs at least one message row", ir.Name)
	}
	messages := make([]ai.Message, 0, len(rows)+1)
	jsonOutput := parameters["jsonOutput"] == true
	if jsonOutput {
		// A provider accepts the JSON response format only when the messages
		// mention JSON, so the ask is stated rather than left to the format
		// flag alone.
		messages = append(messages, ai.Message{Role: ai.RoleSystem, Content: "Respond with a single JSON object and nothing else."})
	}
	for rowIndex, row := range rows {
		role, known := appMessageRole(row.Role)
		if !known {
			return workflow.Item{}, fmt.Errorf("node %q: message %d has role %q, want user, assistant, or system",
				ir.Name, rowIndex+1, row.Role)
		}
		if strings.TrimSpace(row.Content) == "" {
			return workflow.Item{}, fmt.Errorf("node %q: message %d has empty content", ir.Name, rowIndex+1)
		}
		messages = append(messages, ai.Message{Role: role, Content: row.Content})
	}
	modelRequest := ai.ModelRequest{
		Model: bound.name, Messages: messages,
		Temperature: bound.temperature, TopP: bound.topP,
		FrequencyPenalty: bound.frequency, PresencePenalty: bound.presence,
		MaxTokens: bound.maxTokens, MaxRetries: bound.maxRetries, Timeout: bound.timeout,
	}
	if jsonOutput {
		modelRequest.ResponseFormat = "json_object"
	}
	response, err := executor.complete(ctx, ir, request, bound, modelRequest)
	if err != nil {
		return workflow.Item{}, err
	}
	return appCompletionItem(bound.name, response, jsonOutput), nil
}

// analyze runs the image resource's analyze operation: the item's picture and
// one prompt, through the same vision parts the agent and the chain send.
func (executor *AppModelExecutor) analyze(ctx context.Context, ir workflow.IRNode, request engine.Request, bound resolvedAppModel, parameters map[string]any, item workflow.Item) (workflow.Item, error) {
	prompt := strings.TrimSpace(textValue(parameters["text"], ""))
	if prompt == "" {
		return workflow.Item{}, fmt.Errorf("node %q: the image prompt is empty", ir.Name)
	}
	key := strings.TrimSpace(textValue(parameters["binaryInput"], "data"))
	if key == "" {
		key = "data"
	}
	reference, ok := item.Binary[key]
	if !ok {
		return workflow.Item{}, fmt.Errorf("node %q: reads binary %q, which the item does not carry", ir.Name, key)
	}
	if !strings.HasPrefix(reference.MediaType, "image/") {
		return workflow.Item{}, fmt.Errorf("node %q: reads binary %q, which is %q rather than an image",
			ir.Name, key, reference.MediaType)
	}
	if request.Binaries == nil {
		return workflow.Item{}, fmt.Errorf("node %q: reads binary %q, but this run has no binary store", ir.Name, key)
	}
	image, fits, err := imageDataURI(request, key, reference)
	if err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if !fits {
		return workflow.Item{}, fmt.Errorf("node %q: reads binary %q, which is larger than the %d MiB a vision request carries",
			ir.Name, key, maxVisionBytes>>20)
	}
	response, err := executor.complete(ctx, ir, request, bound, ai.ModelRequest{
		Model: bound.name,
		Messages: []ai.Message{{
			Role: ai.RoleUser, Content: prompt, Images: []string{image},
		}},
		Temperature: bound.temperature, TopP: bound.topP,
		FrequencyPenalty: bound.frequency, PresencePenalty: bound.presence,
		MaxTokens: bound.maxTokens, MaxRetries: bound.maxRetries, Timeout: bound.timeout,
	})
	if err != nil {
		return workflow.Item{}, err
	}
	return appCompletionItem(bound.name, response, false), nil
}

// transcribe runs the audio resource's transcribe operation. The adapter
// speaks chat completions only, so the multipart upload is this node's own
// request — through the same policy-bound client, and authenticated through
// the engine's credential seam so the type's own placement and domain scope
// apply exactly as an HTTP Request node's do.
func (executor *AppModelExecutor) transcribe(ctx context.Context, ir workflow.IRNode, request engine.Request, bound resolvedAppModel, parameters map[string]any, item workflow.Item) (workflow.Item, error) {
	key := strings.TrimSpace(textValue(parameters["binaryInput"], "data"))
	if key == "" {
		key = "data"
	}
	reference, ok := item.Binary[key]
	if !ok {
		return workflow.Item{}, fmt.Errorf("node %q: reads binary %q, which the item does not carry", ir.Name, key)
	}
	if !strings.HasPrefix(reference.MediaType, "audio/") {
		return workflow.Item{}, fmt.Errorf("node %q: reads binary %q, which is %q rather than audio",
			ir.Name, key, reference.MediaType)
	}
	if request.Binaries == nil {
		return workflow.Item{}, fmt.Errorf("node %q: reads binary %q, but this run has no binary store", ir.Name, key)
	}
	body, resolved, err := request.Binaries.Get(reference.ID)
	if err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: read audio %q: %w", ir.Name, key, err)
	}
	contents, readErr := io.ReadAll(io.LimitReader(body, maxTranscriptionUploadBytes+1))
	body.Close()
	if readErr != nil {
		return workflow.Item{}, fmt.Errorf("node %q: read audio %q: %w", ir.Name, key, readErr)
	}
	if len(contents) > maxTranscriptionUploadBytes {
		return workflow.Item{}, fmt.Errorf("node %q: reads binary %q, which is larger than the %d MiB the transcription endpoint accepts",
			ir.Name, key, maxTranscriptionUploadBytes>>20)
	}
	mediaType := resolved.MediaType
	if mediaType == "" {
		mediaType = reference.MediaType
	}

	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	file, err := writer.CreateFormFile("file", appAudioFilename(reference, mediaType))
	if err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: build the transcription request: %w", ir.Name, err)
	}
	if _, err := file.Write(contents); err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: build the transcription request: %w", ir.Name, err)
	}
	for field, value := range map[string]string{"model": bound.name, "response_format": "json"} {
		if err := writer.WriteField(field, value); err != nil {
			return workflow.Item{}, fmt.Errorf("node %q: build the transcription request: %w", ir.Name, err)
		}
	}
	if language := strings.TrimSpace(textValue(parameters["language"], "")); language != "" {
		if err := writer.WriteField("language", language); err != nil {
			return workflow.Item{}, fmt.Errorf("node %q: build the transcription request: %w", ir.Name, err)
		}
	}
	if err := writer.Close(); err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: build the transcription request: %w", ir.Name, err)
	}

	endpoint := strings.TrimRight(bound.baseURL, "/") + "/audio/transcriptions"
	target, err := url.Parse(endpoint)
	if err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: the transcription URL is not a valid URL", ir.Name)
	}
	if err := executor.models.policy.CheckURL(target); err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	transcribeCtx, cancel := context.WithTimeout(ctx, bound.timeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(transcribeCtx, http.MethodPost, target.String(), bytes.NewReader(payload.Bytes()))
	if err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: build the transcription request: %w", ir.Name, err)
	}
	httpRequest.Header.Set("Content-Type", writer.FormDataContentType())
	if err := request.AuthenticateAs(transcribeCtx, ir, executor.provider.credentialType, httpRequest); err != nil {
		return workflow.Item{}, err
	}
	response, err := executor.models.client.Do(httpRequest)
	if err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: %w", ir.Name, safehttp.RedactError(err))
	}
	answer, truncated, err := executor.models.policy.ReadBody(response.Body)
	_ = response.Body.Close()
	if err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: read the transcription response: %w", ir.Name, err)
	}
	if truncated {
		return workflow.Item{}, fmt.Errorf("node %q: the transcription response exceeds the configured size limit", ir.Name)
	}
	if response.StatusCode >= 400 {
		return workflow.Item{}, fmt.Errorf("node %q: transcription failed with status %d: %s",
			ir.Name, response.StatusCode, appErrorSnippet(answer))
	}
	var transcribed struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(answer, &transcribed); err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: decode the transcription response: %w", ir.Name, err)
	}
	return workflow.Item{JSON: map[string]any{"text": transcribed.Text}}, nil
}

// complete performs one model call and publishes the same nested model events
// the chain does, so an app node's run reads on the execution feed exactly as
// a chain's does.
func (executor *AppModelExecutor) complete(ctx context.Context, ir workflow.IRNode, request engine.Request, bound resolvedAppModel, modelRequest ai.ModelRequest) (ai.ModelResponse, error) {
	runCtx, cancel := context.WithTimeout(ctx, executor.models.runTimeout())
	defer cancel()
	request.Events.Emit(engine.NodeEvent{
		NodeID: ir.ID, Name: string(ai.EventModelStarted),
		Detail: eventDetail(ai.Event{Kind: ai.EventModelStarted, Model: bound.name}),
	})
	response, err := bound.model.Complete(runCtx, modelRequest)
	if err != nil {
		request.Events.Emit(engine.NodeEvent{
			NodeID: ir.ID, Name: string(ai.EventAgentFailed),
			Detail: eventDetail(ai.Event{Kind: ai.EventAgentFailed, Model: bound.name, Error: err.Error()}),
		})
		return ai.ModelResponse{}, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	usage := response.Usage
	request.Events.Emit(engine.NodeEvent{
		NodeID: ir.ID, Name: string(ai.EventModelCompleted),
		Detail: eventDetail(ai.Event{Kind: ai.EventModelCompleted, Model: bound.name, Usage: &usage}),
	})
	return response, nil
}

// appCompletionItem maps one completion to the item the node emits.
//
// `content` is the model's text under n8n's simplified key, and the usage and
// finish reason travel beside it because a workflow that bills or audits a
// model reads them off the same item. When the node asked for JSON and the
// model obliged, the parsed object is also written to `output`, the field
// every structured answer in this product lands in.
func appCompletionItem(model string, response ai.ModelResponse, jsonOutput bool) workflow.Item {
	fields := map[string]any{
		"content":      response.Message.Content,
		"role":         "assistant",
		"model":        model,
		"finishReason": response.FinishReason,
	}
	if response.Usage.TotalTokens > 0 {
		fields["usage"] = map[string]any{
			"promptTokens":     float64(response.Usage.PromptTokens),
			"completionTokens": float64(response.Usage.CompletionTokens),
			"totalTokens":      float64(response.Usage.TotalTokens),
		}
	}
	if jsonOutput {
		if value, err := ai.ParseAndValidateOutput(map[string]any{}, response.Message.Content); err == nil {
			fields["output"] = value
		}
	}
	return workflow.Item{JSON: fields}
}

// appErrorSnippet carries a bounded piece of an error body, the way the
// adapter's own status errors do: enough to read, never enough to echo a
// prompt back.
func appErrorSnippet(body []byte) string {
	detail := strings.TrimSpace(string(body))
	if len(detail) > 500 {
		detail = detail[:500]
	}
	return detail
}

// appAudioFilename names the uploaded file after the binary's own file name,
// or after its media type when the item carried none — the transcription
// endpoint reads the format from the file name's extension.
func appAudioFilename(reference workflow.BinaryRef, mediaType string) string {
	if name := strings.TrimSpace(reference.FileName); name != "" {
		return name
	}
	extensions := map[string]string{
		"audio/mpeg": "mp3", "audio/mp3": "mp3", "audio/mp4": "mp4", "audio/m4a": "m4a",
		"audio/wav": "wav", "audio/x-wav": "wav", "audio/webm": "webm", "audio/ogg": "ogg",
	}
	if extension, known := extensions[strings.ToLower(mediaType)]; known {
		return "audio." + extension
	}
	return "audio"
}

// appProviderArtwork is the small monochrome mark each provider app node ships
// as its own artwork, in the stroke idiom the editor's builtin glyphs draw in.
// Served by the icon route behind the same validation every node's artwork
// passes, so a provider mark reaches the canvas without an editor change.
func appProviderArtwork(provider appModelProvider) *node.IconAsset {
	marks := map[string]string{
		// A chat step: a bubble with a spark in it, for the node that talks to
		// a model directly.
		"openai": `<path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/><path d="M12 6.5l1 2.5 2.5 1-2.5 1-1 2.5-1-2.5-2.5-1 2.5-1z"/>`,
		// Gemini's four-point star.
		"gemini": `<path d="M12 2.5l2.2 7.3 7.3 2.2-7.3 2.2-2.2 7.3-2.2-7.3-7.3-2.2 7.3-2.2z"/><path d="M19 3.5l.6 1.9 1.9.6-1.9.6-.6 1.9-.6-1.9-1.9-.6 1.9-.6z"/>`,
	}
	body, drawn := marks[provider.artworkKey]
	if !drawn {
		return nil
	}
	return &node.IconAsset{
		MediaType: node.IconSVG,
		Bytes:     []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`),
	}
}
