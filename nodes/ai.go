package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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
	ChatModelExecutorID = "core.chatModel"
	MemoryExecutorID    = "core.memoryBuffer"
	HTTPToolExecutorID  = "core.httpTool"
	AgentExecutorID     = "core.agent"
)

// Node types for the AI family.
const (
	ChatModelNodeType = "kilasflow.chatModel"
	MemoryNodeType    = "kilasflow.memoryBuffer"
	HTTPToolNodeType  = "kilasflow.httpTool"
	AgentNodeType     = "kilasflow.agent"
)

// descriptorKey marks the single item an AI sub-node emits.
//
// A chat model, a memory, and a tool are configuration for an agent rather
// than item producers. Emitting one descriptor item on a typed port lets them
// use the existing scheduler and item model unchanged: topological order
// already guarantees a sub-node runs before the agent that reads it.
const descriptorKey = "$ai"

func chatModelNode() node.Definition {
	return node.Definition{
		Type:        ChatModelNodeType,
		Version:     workflow.V(1),
		DisplayName: "OpenAI Chat Model",
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
	if id, found := n.Credentials["httpBearerAuth"]; !found || strings.TrimSpace(id) == "" {
		return fmt.Errorf("an httpBearerAuth credential holding the API key is required")
	}
	return nil
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
	credentialID := strings.TrimSpace(ir.Credentials["httpBearerAuth"])
	if credentialID == "" {
		return nil, fmt.Errorf("node %q: an httpBearerAuth credential is required", ir.Name)
	}
	if request.Credentials == nil {
		return nil, fmt.Errorf("node %q: credentials are not available in this runtime", ir.Name)
	}
	resolved, err := request.Credentials.ResolveCredential(ctx, credentialID)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if resolved.Type != "httpBearerAuth" {
		return nil, fmt.Errorf("node %q: credential %q is a %s credential, not httpBearerAuth", ir.Name, resolved.Name, resolved.Type)
	}

	// The descriptor carries the credential ID, never the key: it travels in an
	// item that is persisted in the execution record and streamed to the live
	// feed. The agent re-resolves the secret when it actually calls the model.
	descriptor := map[string]any{
		"kind":         "model",
		"model":        textValue(ir.Parameters["model"], "gpt-4o-mini"),
		"baseUrl":      textValue(ir.Parameters["baseUrl"], "https://api.openai.com/v1"),
		"credentialId": credentialID,
		"stream":       boolValue(ir.Parameters["stream"]),
	}
	if temperature := numberValue(ir.Parameters["temperature"]); temperature != 0 {
		descriptor["temperature"] = temperature
	}
	if maxTokens := numberValue(ir.Parameters["maxTokens"]); maxTokens != 0 {
		descriptor["maxTokens"] = maxTokens
	}
	return workflow.NodeOutput{{{JSON: map[string]any{descriptorKey: descriptor}}}}, nil
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
	// httpTool is the same executor the HTTP Request node uses, so exposing an
	// HTTP call as a tool reuses that implementation rather than copying it.
	httpTool *HTTPExecutor
}

// NewAgentExecutor builds the agent node's executor.
func NewAgentExecutor(runtime ai.AgentRuntime, policy safehttp.Policy, memory ai.Memory) *AgentExecutor {
	if runtime == nil {
		runtime = ai.NewLoopRuntime()
	}
	return &AgentExecutor{
		runtime: runtime,
		// Model calls go through the deployment's outbound policy exactly as an
		// HTTP Request node's do, so an agent cannot reach an internal address
		// the rest of the product refuses.
		client:   safehttp.NewClient(policy),
		memory:   memory,
		httpTool: NewHTTPExecutor(policy),
	}
}

// Execute runs the agent once per incoming item.
func (executor *AgentExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	modelDescriptor, found := descriptorFrom(input["model"])
	if !found {
		return nil, fmt.Errorf("node %q: connect an OpenAI Chat Model to the model port", ir.Name)
	}
	memoryDescriptor, hasMemory := descriptorFrom(input["memory"])
	toolDescriptors := descriptorsFrom(input["tools"])

	credentialID, _ := modelDescriptor["credentialId"].(string)
	if request.Credentials == nil {
		return nil, fmt.Errorf("node %q: credentials are not available in this runtime", ir.Name)
	}
	resolved, err := request.Credentials.ResolveCredential(ctx, credentialID)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	model := ai.NewOpenAICompatible(executor.client,
		textValue(modelDescriptor["baseUrl"], "https://api.openai.com/v1"), resolved.Fields["token"])

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
			MaxTokens:     int(numberValue(modelDescriptor["maxTokens"])),
			Stream:        boolValue(modelDescriptor["stream"]),
		}
		if temperature := numberValue(modelDescriptor["temperature"]); temperature != 0 {
			agentRequest.Temperature = &temperature
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
		result, err := executor.runtime.Run(ctx, agentRequest, func(event ai.Event) {
			events = append(events, event)
			request.Events.Emit(engine.NodeEvent{
				NodeID: ir.ID, Name: string(event.Kind), Detail: eventDetail(event),
			})
		})
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

func descriptorFrom(items []workflow.Item) (map[string]any, bool) {
	for _, item := range items {
		if descriptor, ok := item.JSON[descriptorKey].(map[string]any); ok {
			return descriptor, true
		}
	}
	return nil, false
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
