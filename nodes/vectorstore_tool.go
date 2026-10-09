// The Vector Store Tool: an agent tool that answers a question from a vector
// store rather than returning raw matches.
//
// It is the n8n toolVectorStore shape. A vector store in retrieve mode hangs
// off its vectorStore port and a chat model off its model port. When the
// agent calls it, the tool embeds the question, takes the closest documents
// from the store, and asks its own chat model to answer from them; the agent
// receives that answer as the tool result. Retrieve-as-tool on the store
// itself stays the other way to give an agent the same documents, as raw
// matches.
package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Vector Store Tool identity.
const (
	VectorStoreToolExecutorID = "core.vectorStoreTool"
	VectorStoreToolNodeType   = "kilasflow.vectorStoreTool"
)

// toolKindVectorStoreAnswer is the descriptor kind the agent resolves into
// the answering tool. Kept here rather than in ai.go's kind block so the
// tool's whole contract lives in one file.
const toolKindVectorStoreAnswer = "vectorStoreAnswer"

// vectorAnswerSystemPrompt instructs the tool's own model. It is ours, not a
// transcription of any other implementation's prompt.
const vectorAnswerSystemPrompt = "You answer one question using only the reference passages that come with it. " +
	"If the passages do not contain the answer, say that you do not know instead of guessing."

func vectorStoreToolNode() node.Definition {
	return node.Definition{
		Type:        VectorStoreToolNodeType,
		Version:     workflow.V(1),
		DisplayName: "Vector Store Tool",
		Description: "Answers an AI Agent's question from a vector store: retrieves the closest documents and has its own chat model answer from them.",
		Category:    "AI",
		Group:       []node.NodeGroup{node.GroupTransform},
		// The store seen from the agent's side, so it borrows the store's glyph.
		Icon:      &node.NodeIcon{Light: "builtin:database-search"},
		IconColor: "#336791",
		Inputs: []workflow.Port{
			{
				Name: "vectorStore", DisplayName: "Vector Store", Kind: workflow.ConnectionVectorStore,
				Required: true, MaxConnections: 1,
			},
			{
				Name: "model", DisplayName: "Chat Model", Kind: workflow.ConnectionLanguageModel,
				Required: true, MaxConnections: 1,
			},
		},
		Outputs: []workflow.Port{{Name: "tool", Kind: workflow.ConnectionTool}},
		Parameters: []node.PropertyDefinition{
			toolNameProperty(),
			toolDescriptionProperty(),
			{
				Key: "topK", Label: "Top K", Kind: node.PropertyNumber, Default: float64(DefaultVectorTopK),
				Description: fmt.Sprintf("How many documents to retrieve for each question, from 1 to %d.", MaxVectorTopK),
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     VectorStoreToolExecutorID,
		Validate:       validateVectorStoreTool,
		Codex:          toolCodex(),
	}
}

func validateVectorStoreTool(n workflow.Node) error {
	if err := validateToolNameAndDescription(n); err != nil {
		return err
	}
	if raw, present := n.Parameters["topK"]; present && raw != nil {
		if _, err := vectorToolTopK(raw); err != nil {
			return err
		}
	}
	return nil
}

// vectorToolTopK reads topK as a whole number within the store's search
// bound. Anything else is refused rather than clamped or rounded: a typo
// should be reported, not quietly answered from a different number of
// documents.
func vectorToolTopK(raw any) (int, error) {
	switch raw.(type) {
	case float64, int, json.Number:
	default:
		return 0, fmt.Errorf("topK must be a whole number from 1 to %d", MaxVectorTopK)
	}
	value := numberValue(raw)
	if value != math.Trunc(value) || value < 1 || value > MaxVectorTopK {
		return 0, fmt.Errorf("topK must be a whole number from 1 to %d, got %v", MaxVectorTopK, raw)
	}
	return int(value), nil
}

// executeVectorStoreTool emits the answering tool's descriptor: its name and
// description, plus the store and model descriptors it was given, carried
// whole. Both name credentials by id only; the agent re-resolves them when it
// builds the tool.
func executeVectorStoreTool(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store, found, err := soleDescriptor(input["vectorStore"], "vectorStore")
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if !found {
		return nil, fmt.Errorf("node %q: connect a vector store in Retrieve mode to the vectorStore port", ir.Name)
	}
	if textValue(store["kind"], "") != vectorStoreSourceKind {
		return nil, fmt.Errorf("node %q: the vectorStore port accepts only a vector store in Retrieve mode (for Vector Store Tool); %q is not one",
			ir.Name, textValue(store["nodeName"], ""))
	}
	model, found, err := soleDescriptor(input["model"], "model")
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if !found || textValue(model["kind"], "") != "model" {
		return nil, fmt.Errorf("node %q: connect a chat model to the model port; the tool answers with it", ir.Name)
	}
	topK := DefaultVectorTopK
	if raw, present := ir.Parameters["topK"]; present && raw != nil {
		if topK, err = vectorToolTopK(raw); err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
	}
	return workflow.NodeOutput{{{JSON: map[string]any{descriptorKey: map[string]any{
		"kind":        toolKindVectorStoreAnswer,
		"name":        toolNameFor(ir),
		"description": textValue(ir.Parameters["toolDescription"], ""),
		"nodeName":    ir.Name,
		"topK":        topK,
		"store":       store,
		"model":       model,
	}}}}}, nil
}

// vectorStoreAnswerToolFrom builds the answering tool. Retrieval reuses the
// retrieve-as-tool path whole — the same refusal when the install has no
// vector store, the same tenant from the execution, the same customer
// PostgreSQL guard — and the model is resolved the way the agent resolves its
// own: the credential re-read through the runtime, the outbound policy's
// pre-flight, the credential's host scope and the deployment's timeout
// ceiling. Both happen here, before the agent's first model call, so a
// misconfigured tool fails the run before anything is spent.
func (executor *AgentExecutor) vectorStoreAnswerToolFrom(ctx context.Context, ir workflow.IRNode, descriptor map[string]any, request engine.Request) (ai.Tool, error) {
	name := textValue(descriptor["name"], "")
	if name == "" {
		return nil, fmt.Errorf("node %q: a connected tool has no name", ir.Name)
	}
	nodeName := textValue(descriptor["nodeName"], name)
	store, _ := descriptor["store"].(map[string]any)
	if textValue(store["kind"], "") != vectorStoreSourceKind {
		return nil, fmt.Errorf("node %q: tool %q carries no vector store", ir.Name, nodeName)
	}
	modelDescriptor, _ := descriptor["model"].(map[string]any)
	if modelDescriptor == nil {
		return nil, fmt.Errorf("node %q: tool %q carries no chat model", ir.Name, nodeName)
	}

	retrieval := make(map[string]any, len(store)+4)
	for key, value := range store {
		retrieval[key] = value
	}
	retrieval["kind"] = toolKindVectorStore
	retrieval["name"] = name
	retrieval["description"] = textValue(descriptor["description"], "")
	retrieval["topK"] = descriptor["topK"]
	retriever, err := executor.vectorStoreToolFrom(ir, retrieval, request)
	if err != nil {
		return nil, err
	}
	model, err := executor.resolveModel(ctx, nodeName, modelDescriptor, request)
	if err != nil {
		return nil, err
	}
	return &vectorStoreAnswerTool{
		name:        name,
		description: textValue(descriptor["description"], ""),
		agentNode:   ir.Name,
		retriever:   retriever,
		model:       model,
	}, nil
}

// vectorStoreAnswerTool retrieves through the retrieve-as-tool tool and
// answers with its own model.
type vectorStoreAnswerTool struct {
	name, description, agentNode string
	retriever                    ai.Tool
	model                        resolvedModel
}

func (tool *vectorStoreAnswerTool) Definition() ai.ToolDefinition {
	description := strings.TrimSpace(tool.description)
	if description != "" {
		description += "\n\n"
	}
	description += "Pass one complete, self-contained question. The tool looks up the most relevant stored documents and returns an answer drawn from them."
	return ai.ToolDefinition{
		Name:        tool.name,
		Description: description,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The question to answer from the stored documents.",
				},
			},
			"required": []string{"query"},
		},
	}
}

func (tool *vectorStoreAnswerTool) Invoke(ctx context.Context, arguments json.RawMessage) (string, error) {
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
	question := strings.TrimSpace(args.Query)
	if question == "" {
		return "", fmt.Errorf("node %q: tool %q: query is required", tool.agentNode, tool.name)
	}
	query, err := json.Marshal(map[string]string{"query": question})
	if err != nil {
		return "", fmt.Errorf("node %q: tool %q: %w", tool.agentNode, tool.name, err)
	}
	raw, err := tool.retriever.Invoke(ctx, query)
	if err != nil {
		return "", err
	}
	var found struct {
		Matches []struct {
			Text string `json:"text"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(raw), &found); err != nil {
		return "", fmt.Errorf("node %q: tool %q: read the retrieved documents: %w", tool.agentNode, tool.name, err)
	}
	passages := make([]string, 0, len(found.Matches))
	for _, match := range found.Matches {
		if text := strings.TrimSpace(match.Text); text != "" {
			passages = append(passages, text)
		}
	}
	// Nothing to answer from: say so rather than spend a model call asking
	// the model to say it.
	if len(passages) == 0 {
		return "No stored document matches this question.", nil
	}

	var prompt strings.Builder
	prompt.WriteString("Reference passages:\n")
	for index, passage := range passages {
		fmt.Fprintf(&prompt, "\n[%d] %s\n", index+1, passage)
	}
	prompt.WriteString("\nQuestion: ")
	prompt.WriteString(question)

	response, err := tool.model.model.Complete(ctx, ai.ModelRequest{
		Model: tool.model.name,
		Messages: []ai.Message{
			{Role: ai.RoleSystem, Content: vectorAnswerSystemPrompt},
			{Role: ai.RoleUser, Content: prompt.String()},
		},
		Temperature:      tool.model.temperature,
		TopP:             tool.model.topP,
		FrequencyPenalty: tool.model.frequencyPenalty,
		PresencePenalty:  tool.model.presencePenalty,
		MaxTokens:        tool.model.maxTokens,
		MaxRetries:       tool.model.maxRetries,
		Timeout:          tool.model.timeout,
		ReasoningEffort:  tool.model.reasoningEffort,
	})
	if err != nil {
		return "", fmt.Errorf("node %q: tool %q: %w", tool.agentNode, tool.name, err)
	}
	answer := strings.TrimSpace(response.Message.Content)
	if answer == "" {
		return "", fmt.Errorf("node %q: tool %q: the chat model returned no answer", tool.agentNode, tool.name)
	}
	return answer, nil
}
