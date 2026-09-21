package nodes

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Executor IDs for the RAG cluster sub-nodes.
const (
	DocumentLoaderExecutorID = "core.documentLoader"
	TextSplitterExecutorID   = "core.textSplitter"
)

// Node types for the RAG cluster sub-nodes.
const (
	DocumentLoaderNodeType = "kilasflow.documentLoader"
	TextSplitterNodeType   = "kilasflow.textSplitter"
)

// Vector store modes that match n8n's Vector Store parameter. Native KilasFlow
// workflows keep using `operation` and stay on the main channel; imported
// graphs write `mode` and attach embeddings / documents on typed ports.
const (
	VectorModeInsert           = "insert"
	VectorModeGetMany          = "getMany"
	VectorModeRetrieveAsTool   = "retrieve-as-tool"
	embeddingsDescriptorKind   = "embeddings"
	splitterDescriptorKind     = "textSplitter"
	DefaultTextSplitterSize    = 1000
	DefaultTextSplitterOverlap = 200
)

func documentLoaderNode() node.Definition {
	return node.Definition{
		Type:        DocumentLoaderNodeType,
		Version:     workflow.V(1),
		DisplayName: "Default Data Loader",
		Description: "Turns incoming JSON or extracted text into documents for a Vector Store. Connect a Text Splitter to chunk them.",
		Category:    "AI",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:layers"},
		IconColor:   "#a855f7",
		Inputs: []workflow.Port{
			{Name: "main", Kind: workflow.ConnectionMain},
			{Name: "splitter", Kind: workflow.ConnectionTextSplitter},
		},
		Outputs: []workflow.Port{{Name: "document", Kind: workflow.ConnectionDocument}},
		Parameters: []node.PropertyDefinition{
			{
				Key: "textField", Label: "Text field", Kind: node.PropertyString, Default: "text",
				Description: "JSON field holding the document text. Also accepts pageContent and content.",
			},
			{
				Key: "metadataField", Label: "Metadata field", Kind: node.PropertyString, Default: "metadata",
				Description: "JSON object copied onto every produced document as metadata.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     DocumentLoaderExecutorID,
		Codex:          &node.NodeCodex{Categories: []string{"AI"}, Subcategories: map[string][]string{"AI": {"Document Loaders"}}},
	}
}

func textSplitterNode() node.Definition {
	return node.Definition{
		Type:        TextSplitterNodeType,
		Version:     workflow.V(1),
		DisplayName: "Recursive Character Text Splitter",
		Description: "Splits documents into overlapping chunks by paragraph, line, then word.",
		Category:    "AI",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:scissors"},
		IconColor:   "#a855f7",
		Inputs:      nil,
		Outputs:     []workflow.Port{{Name: "splitter", Kind: workflow.ConnectionTextSplitter}},
		Parameters: []node.PropertyDefinition{
			{
				Key: "chunkSize", Label: "Chunk size", Kind: node.PropertyNumber, Default: float64(DefaultTextSplitterSize),
				Description: "Maximum characters in one chunk.",
			},
			{
				Key: "chunkOverlap", Label: "Chunk overlap", Kind: node.PropertyNumber, Default: float64(DefaultTextSplitterOverlap),
				Description: "Characters repeated from the previous chunk so a split does not lose a sentence.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     TextSplitterExecutorID,
		Validate:       validateTextSplitter,
		Codex:          &node.NodeCodex{Categories: []string{"AI"}, Subcategories: map[string][]string{"AI": {"Text Splitters"}}},
	}
}

func validateTextSplitter(n workflow.Node) error {
	size := int(numberValue(n.Parameters["chunkSize"]))
	if _, present := n.Parameters["chunkSize"]; present && n.Parameters["chunkSize"] != nil && size <= 0 {
		return fmt.Errorf("chunkSize must be a positive number")
	}
	overlap := int(numberValue(n.Parameters["chunkOverlap"]))
	if overlap < 0 {
		return fmt.Errorf("chunkOverlap cannot be negative")
	}
	if size > 0 && overlap >= size {
		return fmt.Errorf("chunkOverlap must be smaller than chunkSize")
	}
	return nil
}

func executeDocumentLoader(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := input["main"]
	if len(items) == 0 {
		return workflow.NodeOutput{[]workflow.Item{}}, nil
	}
	textField := strings.TrimSpace(textValue(ir.Parameters["textField"], "text"))
	if textField == "" {
		textField = "text"
	}
	metadataField := strings.TrimSpace(textValue(ir.Parameters["metadataField"], "metadata"))
	chunkSize, chunkOverlap := splitterSettings(input["splitter"])
	documents := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		text := documentText(item.JSON, textField)
		if strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("node %q: item %d carries no text in %q, pageContent, content or data", ir.Name, index+1, textField)
		}
		metadata, err := vectorObject(item.JSON[metadataField])
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d carries metadata that is not a JSON object", ir.Name, index+1)
		}
		if metadata == nil {
			metadata = map[string]any{}
		}
		copyDocumentIdentity(metadata, item.JSON)
		for _, chunk := range splitRecursive(text, chunkSize, chunkOverlap, nil) {
			meta, cloneErr := cloneItemJSON(metadata)
			if cloneErr != nil {
				return nil, fmt.Errorf("node %q: item %d metadata is not JSON-serialisable: %w", ir.Name, index+1, cloneErr)
			}
			documents = append(documents, workflow.Item{JSON: map[string]any{
				"pageContent": chunk,
				"text":        chunk,
				"metadata":    meta,
			}, Paired: item.Paired})
		}
	}
	return workflow.NodeOutput{documents}, nil
}

func executeTextSplitter(ctx context.Context, ir workflow.IRNode, _ workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	chunkSize := int(numberValue(ir.Parameters["chunkSize"]))
	if chunkSize <= 0 {
		chunkSize = DefaultTextSplitterSize
	}
	chunkOverlap := int(numberValue(ir.Parameters["chunkOverlap"]))
	if chunkOverlap < 0 {
		chunkOverlap = 0
	}
	return workflow.NodeOutput{[]workflow.Item{{JSON: map[string]any{
		descriptorKey: map[string]any{
			"kind":         splitterDescriptorKind,
			"nodeName":     ir.Name,
			"chunkSize":    chunkSize,
			"chunkOverlap": chunkOverlap,
		},
	}}}}, nil
}

func splitterSettings(items []workflow.Item) (int, int) {
	chunkSize, chunkOverlap := DefaultTextSplitterSize, DefaultTextSplitterOverlap
	descriptor, found, err := soleDescriptor(items, "splitter")
	if err != nil || !found {
		return chunkSize, chunkOverlap
	}
	if size := int(numberValue(descriptor["chunkSize"])); size > 0 {
		chunkSize = size
	}
	if overlap := int(numberValue(descriptor["chunkOverlap"])); overlap >= 0 {
		chunkOverlap = overlap
		if _, present := descriptor["chunkOverlap"]; !present {
			chunkOverlap = DefaultTextSplitterOverlap
		}
	}
	if chunkOverlap >= chunkSize {
		chunkOverlap = 0
	}
	return chunkSize, chunkOverlap
}

func copyDocumentIdentity(dst, src map[string]any) {
	if dst == nil || src == nil {
		return
	}
	for _, key := range []string{"id", "fileId", "name", "mimeType", "parents", "webViewLink", "driveId"} {
		if _, exists := dst[key]; exists {
			continue
		}
		if value, ok := src[key]; ok && value != nil && value != "" {
			dst[key] = value
		}
	}
}

func vectorInsertOutput(draft vectorDocumentDraft, storedID, extraKey, extraValue string) workflow.Item {
	fields := map[string]any{}
	sourceID := ""
	if draft.Metadata != nil {
		sourceID = strings.TrimSpace(textValue(draft.Metadata["id"], textValue(draft.Metadata["fileId"], "")))
	}
	if sourceID != "" {
		fields["id"] = sourceID
		if storedID != "" && storedID != sourceID {
			fields["vectorId"] = storedID
		}
	} else {
		fields["id"] = storedID
	}
	if extraKey != "" {
		fields[extraKey] = extraValue
	}
	if draft.Metadata != nil {
		if parents := draft.Metadata["parents"]; parents != nil {
			fields["parents"] = parents
		}
		if name := strings.TrimSpace(textValue(draft.Metadata["name"], "")); name != "" {
			fields["name"] = name
		}
	}
	return workflow.Item{JSON: fields}
}

func appendUniqueInsertOutput(output []workflow.Item, seen map[string]struct{}, item workflow.Item) []workflow.Item {
	sourceID := strings.TrimSpace(textValue(item.JSON["id"], ""))
	if sourceID != "" {
		if _, exists := seen[sourceID]; exists {
			return output
		}
		seen[sourceID] = struct{}{}
	}
	return append(output, item)
}

func documentText(fields map[string]any, preferred string) string {
	for _, key := range []string{preferred, "pageContent", "text", "content", "data"} {
		if key == "" {
			continue
		}
		if text := strings.TrimSpace(textValue(fields[key], "")); text != "" {
			return text
		}
	}
	return ""
}

func documentsFromItems(items []workflow.Item) []vectorDocumentDraft {
	drafts := make([]vectorDocumentDraft, 0, len(items))
	for _, item := range items {
		content := documentText(item.JSON, "pageContent")
		if strings.TrimSpace(content) == "" {
			continue
		}
		metadata, _ := vectorObject(item.JSON["metadata"])
		id, _ := item.JSON["id"].(string)
		embedding, _ := vectorNumbers(item.JSON["embedding"])
		drafts = append(drafts, vectorDocumentDraft{
			ID: strings.TrimSpace(id), Content: content, Metadata: metadata, Embedding: embedding,
		})
	}
	return drafts
}

type vectorDocumentDraft struct {
	ID        string
	Content   string
	Metadata  map[string]any
	Embedding []float64
}

func embeddingsDescriptorOf(items []workflow.Item) map[string]any {
	descriptor, found, err := soleDescriptor(items, "embedding")
	if err != nil || !found {
		return nil
	}
	if textValue(descriptor["kind"], "") != embeddingsDescriptorKind {
		return nil
	}
	return descriptor
}

const EmbeddingsModeCluster = "cluster"

func embeddingsPortsFor(parameters map[string]any, _ workflow.TypeVersion) ([]workflow.Port, []workflow.Port) {
	if strings.EqualFold(strings.TrimSpace(textValue(parameters["mode"], "")), EmbeddingsModeCluster) {
		return nil, []workflow.Port{{Name: "embedding", Kind: workflow.ConnectionEmbedding}}
	}
	return mainInput(), []workflow.Port{
		{Name: "main", Kind: workflow.ConnectionMain},
		{Name: "embedding", Kind: workflow.ConnectionEmbedding},
	}
}

func embeddingsDescriptorItem(ir workflow.IRNode) workflow.Item {
	return workflow.Item{JSON: map[string]any{
		descriptorKey: map[string]any{
			"kind":         embeddingsDescriptorKind,
			"nodeName":     ir.Name,
			"model":        textValue(ir.Parameters["model"], DefaultEmbeddingsModel),
			"baseUrl":      textValue(ir.Parameters["baseUrl"], DefaultEmbeddingsBaseURL),
			"timeout":      ir.Parameters["timeout"],
			"credentialId": embeddingsCredentialID(ir.Credentials),
		},
	}}
}

func vectorStoreModeOf(parameters map[string]any) string {
	mode := strings.TrimSpace(textValue(parameters["mode"], ""))
	if mode == "" {
		mode = strings.TrimSpace(textValue(parameters["operation"], VectorOperationInsert))
	}
	switch strings.ToLower(mode) {
	case "insert":
		return VectorModeInsert
	case VectorModeGetMany, "get_many", "load", "retrieve", VectorOperationSearch:
		return VectorModeGetMany
	case VectorModeRetrieveAsTool, "retrieve_as_tool", "retrieveastool":
		return VectorModeRetrieveAsTool
	case VectorOperationDelete:
		return VectorOperationDelete
	default:
		return mode
	}
}

func vectorStorePortsFor(parameters map[string]any, _ workflow.TypeVersion) ([]workflow.Port, []workflow.Port) {
	switch vectorStoreModeOf(parameters) {
	case VectorModeInsert:
		if _, hasMode := parameters["mode"]; hasMode {
			return []workflow.Port{
				{Name: "embedding", Kind: workflow.ConnectionEmbedding},
				{Name: "document", Kind: workflow.ConnectionDocument},
			}, mainOutput()
		}
		return mainInput(), mainOutput()
	case VectorModeGetMany:
		if _, hasMode := parameters["mode"]; hasMode {
			return []workflow.Port{
				{Name: "main", Kind: workflow.ConnectionMain},
				{Name: "embedding", Kind: workflow.ConnectionEmbedding},
			}, mainOutput()
		}
		return mainInput(), mainOutput()
	case VectorModeRetrieveAsTool:
		return []workflow.Port{
			{Name: "embedding", Kind: workflow.ConnectionEmbedding},
		}, []workflow.Port{{Name: "tool", Kind: workflow.ConnectionTool}}
	default:
		return mainInput(), mainOutput()
	}
}

func splitRecursive(text string, chunkSize, overlap int, separators []string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if chunkSize <= 0 {
		chunkSize = DefaultTextSplitterSize
	}
	if overlap < 0 || overlap >= chunkSize {
		overlap = 0
	}
	if utf8.RuneCountInString(text) <= chunkSize {
		return []string{text}
	}
	if len(separators) == 0 {
		separators = []string{"\n\n", "\n", " ", ""}
	}
	sep := separators[0]
	rest := separators[1:]
	if sep == "" {
		return splitByRunes(text, chunkSize, overlap)
	}
	pieces := strings.Split(text, sep)
	chunks := make([]string, 0, len(pieces))
	var current strings.Builder
	flush := func() {
		part := strings.TrimSpace(current.String())
		if part == "" {
			current.Reset()
			return
		}
		if utf8.RuneCountInString(part) > chunkSize {
			chunks = append(chunks, splitRecursive(part, chunkSize, overlap, rest)...)
		} else {
			chunks = append(chunks, part)
		}
		current.Reset()
	}
	for _, piece := range pieces {
		candidate := piece
		if current.Len() > 0 {
			candidate = current.String() + sep + piece
		}
		if utf8.RuneCountInString(strings.TrimSpace(candidate)) <= chunkSize {
			current.Reset()
			current.WriteString(candidate)
			continue
		}
		flush()
		current.WriteString(piece)
	}
	flush()
	if overlap == 0 || len(chunks) < 2 {
		return chunks
	}
	return applyOverlap(chunks, overlap)
}

func splitByRunes(text string, chunkSize, overlap int) []string {
	runes := []rune(text)
	step := chunkSize - overlap
	if step <= 0 {
		step = chunkSize
	}
	chunks := make([]string, 0, (len(runes)/step)+1)
	for start := 0; start < len(runes); start += step {
		end := start + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
		if end == len(runes) {
			break
		}
	}
	return chunks
}

func applyOverlap(chunks []string, overlap int) []string {
	if overlap <= 0 || len(chunks) < 2 {
		return chunks
	}
	out := make([]string, 0, len(chunks))
	out = append(out, chunks[0])
	for index := 1; index < len(chunks); index++ {
		previous := []rune(out[len(out)-1])
		prefix := overlap
		if prefix > len(previous) {
			prefix = len(previous)
		}
		merged := string(previous[len(previous)-prefix:]) + chunks[index]
		out = append(out, merged)
	}
	return out
}
