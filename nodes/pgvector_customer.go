package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

const (
	VectorStorePGVectorExecutorID = "core.vectorStorePGVector"
	VectorStorePGVectorNodeType   = "kilasflow.vectorStorePGVector"
)

func vectorStorePGVectorNode() node.Definition {
	return node.Definition{
		Type:        VectorStorePGVectorNodeType,
		Version:     workflow.V(1),
		Credentials: []node.CredentialRequirement{{Type: "postgres", Required: true}},
		DisplayName: "Postgres PGVector Store",
		Description: "Inserts and retrieves vectors in a PostgreSQL database you own. The table must already exist with a vector column; this node never runs CREATE EXTENSION or CREATE TABLE.",
		Category:    "AI",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:database"},
		IconColor:   "#336791",
		Subtitle:    "{{ $parameter.mode }} {{ $parameter.tableName }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		PortsFor:    vectorStorePortsFor,
		Parameters: []node.PropertyDefinition{
			{
				Key: "mode", Label: "Mode", Kind: node.PropertyOptions, Default: VectorModeInsert,
				Options: []node.PropertyOption{
					{Label: "Insert Documents", Value: VectorModeInsert},
					{Label: "Get Many", Value: VectorModeGetMany},
					{Label: "Retrieve as Tool", Value: VectorModeRetrieveAsTool},
				},
			},
			{
				Key: "tableName", Label: "Table name", Kind: node.PropertyString, Required: true, Default: "documents",
				Description: "Existing table. Columns default to n8n/LangChain names: id, text, metadata, embedding.",
			},
			{
				Key: "collection", Label: "Collection", Kind: node.PropertyString,
				Description: "Optional metadata.collection value that isolates rows that share the table.",
			},
			{
				Key: "idColumn", Label: "ID column", Kind: node.PropertyString, Default: "id",
			},
			{
				Key: "contentColumn", Label: "Content column", Kind: node.PropertyString, Default: "text",
			},
			{
				Key: "metadataColumn", Label: "Metadata column", Kind: node.PropertyString, Default: "metadata",
			},
			{
				Key: "embeddingColumn", Label: "Embedding column", Kind: node.PropertyString, Default: "embedding",
			},
			{
				Key: "toolName", Label: "Tool name", Kind: node.PropertyString,
			},
			{
				Key: "toolDescription", Label: "Tool description", Kind: node.PropertyString,
			},
			{
				Key: "topK", Label: "Top K", Kind: node.PropertyNumber, Default: float64(DefaultVectorTopK),
			},
			{
				Key: "query", Label: "Query", Kind: node.PropertyString,
				Description: "Search text used with an embeddings sub-node in Get Many mode.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     VectorStorePGVectorExecutorID,
		Validate:       validateVectorStorePGVector,
		Codex:          &node.NodeCodex{Categories: []string{"AI"}, Subcategories: map[string][]string{"AI": {"Vector Stores"}}},
	}
}

func validateVectorStorePGVector(n workflow.Node) error {
	if strings.TrimSpace(n.Credentials["postgres"]) == "" {
		return fmt.Errorf("a postgres credential pointing at the customer's database is required")
	}
	if err := validateSQLIdentifier(textValue(n.Parameters["tableName"], "documents"), "tableName"); err != nil {
		return err
	}
	for _, key := range []string{"idColumn", "contentColumn", "metadataColumn", "embeddingColumn"} {
		if raw, present := n.Parameters[key]; present && raw != nil && strings.TrimSpace(textValue(raw, "")) != "" {
			if err := validateSQLIdentifier(textValue(raw, ""), key); err != nil {
				return err
			}
		}
	}
	return nil
}

var sqlIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateSQLIdentifier(name, field string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%s is required", field)
	}
	if !sqlIdentifier.MatchString(name) {
		return fmt.Errorf("%s must be a simple SQL identifier", field)
	}
	return nil
}

type VectorStorePGVectorExecutor struct {
	embedder *EmbeddingsExecutor
	guard    sqlnode.Guard
}

func NewVectorStorePGVectorExecutor(embedder *EmbeddingsExecutor, guard sqlnode.Guard) *VectorStorePGVectorExecutor {
	return &VectorStorePGVectorExecutor{embedder: embedder, guard: guard}
}

func (executor *VectorStorePGVectorExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg, err := parsePGVectorTable(ir)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	switch vectorStoreModeOf(ir.Parameters) {
	case VectorModeRetrieveAsTool:
		return executor.emitTool(ir, cfg, input)
	case VectorModeGetMany:
		return executor.search(ctx, ir, cfg, input, request)
	default:
		return executor.insert(ctx, ir, cfg, input, request)
	}
}

type pgvectorTableConfig struct {
	table, id, content, metadata, embedding, collection, credentialID string
}

func parsePGVectorTable(ir workflow.IRNode) (pgvectorTableConfig, error) {
	cfg := pgvectorTableConfig{
		table:        textValue(ir.Parameters["tableName"], "documents"),
		id:           textValue(ir.Parameters["idColumn"], "id"),
		content:      textValue(ir.Parameters["contentColumn"], "text"),
		metadata:     textValue(ir.Parameters["metadataColumn"], "metadata"),
		embedding:    textValue(ir.Parameters["embeddingColumn"], "embedding"),
		collection:   strings.TrimSpace(textValue(ir.Parameters["collection"], "")),
		credentialID: strings.TrimSpace(ir.Credentials["postgres"]),
	}
	if cfg.table == "" {
		cfg.table = "documents"
	}
	if cfg.id == "" {
		cfg.id = "id"
	}
	if cfg.content == "" {
		cfg.content = "text"
	}
	if cfg.metadata == "" {
		cfg.metadata = "metadata"
	}
	if cfg.embedding == "" {
		cfg.embedding = "embedding"
	}
	for _, pair := range [][2]string{
		{"tableName", cfg.table}, {"idColumn", cfg.id}, {"contentColumn", cfg.content},
		{"metadataColumn", cfg.metadata}, {"embeddingColumn", cfg.embedding},
	} {
		if err := validateSQLIdentifier(pair[1], pair[0]); err != nil {
			return pgvectorTableConfig{}, err
		}
	}
	if cfg.credentialID == "" {
		return pgvectorTableConfig{}, fmt.Errorf("a postgres credential is required")
	}
	return cfg, nil
}

func (executor *VectorStorePGVectorExecutor) emitTool(ir workflow.IRNode, cfg pgvectorTableConfig, input workflow.NodeInput) (workflow.NodeOutput, error) {
	name := strings.TrimSpace(textValue(ir.Parameters["toolName"], ""))
	if name == "" {
		name = workflow.NormalizeToolName(ir.Name)
	}
	description := strings.TrimSpace(textValue(ir.Parameters["toolDescription"], ""))
	if description == "" {
		description = "Retrieve relevant documents from the customer PostgreSQL vector table."
	}
	topK := int(numberValue(ir.Parameters["topK"]))
	if topK <= 0 {
		topK = DefaultVectorTopK
	}
	descriptor := map[string]any{
		"kind":            toolKindVectorStore,
		"name":            name,
		"description":     description,
		"nodeName":        ir.Name,
		"backend":         "postgres",
		"collection":      cfg.collection,
		"tableName":       cfg.table,
		"idColumn":        cfg.id,
		"contentColumn":   cfg.content,
		"metadataColumn":  cfg.metadata,
		"embeddingColumn": cfg.embedding,
		"credentialId":    cfg.credentialID,
		"topK":            topK,
	}
	if embeddings := embeddingsDescriptorOf(input["embedding"]); embeddings != nil {
		descriptor["embeddings"] = embeddings
	}
	return workflow.NodeOutput{[]workflow.Item{{JSON: map[string]any{descriptorKey: descriptor}}}}, nil
}

func (executor *VectorStorePGVectorExecutor) insert(ctx context.Context, ir workflow.IRNode, cfg pgvectorTableConfig, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	drafts := documentsFromItems(input["document"])
	if len(drafts) == 0 {
		drafts = documentsFromItems(input["main"])
	}
	if len(drafts) == 0 {
		return nil, fmt.Errorf("node %q: at least one document is required", ir.Name)
	}
	if descriptor := embeddingsDescriptorOf(input["embedding"]); descriptor != nil {
		texts := make([]string, 0, len(drafts))
		for _, draft := range drafts {
			texts = append(texts, draft.Content)
		}
		vectors, err := executor.embedder.EmbedFromDescriptor(ctx, ir.Name, descriptor, request, texts)
		if err != nil {
			return nil, err
		}
		for index := range drafts {
			drafts[index].Embedding = vectors[index]
		}
	}
	connection, err := openCustomerPostgres(ctx, request, cfg.credentialID, executor.guard)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	defer connection.Close()
	output := make([]workflow.Item, 0, len(drafts))
	seen := map[string]struct{}{}
	for index, draft := range drafts {
		if len(draft.Embedding) == 0 {
			return nil, fmt.Errorf("node %q: item %d carries no embedding vector", ir.Name, index+1)
		}
		id := draft.ID
		if id == "" {
			id, err = workflow.NewID("vecdoc")
			if err != nil {
				return nil, fmt.Errorf("node %q: %w", ir.Name, err)
			}
		}
		metadata := draft.Metadata
		if metadata == nil {
			metadata = map[string]any{}
		}
		if cfg.collection != "" {
			metadata["collection"] = cfg.collection
		}
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d metadata is not JSON: %w", ir.Name, index+1, err)
		}
		statement := fmt.Sprintf(
			`INSERT INTO %s (%s, %s, %s, %s) VALUES ($1, $2, $3::jsonb, $4::vector)`,
			quote(cfg.table), quote(cfg.id), quote(cfg.content), quote(cfg.metadata), quote(cfg.embedding),
		)
		_, err = connection.Execute(ctx, statement, []any{id, draft.Content, string(encoded), vectorLiteral(draft.Embedding)}, sqlnode.DefaultLimits())
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, customerPGVectorError(cfg.table, err))
		}
		output = appendUniqueInsertOutput(output, seen, vectorInsertOutput(draft, id, "table", cfg.table))
	}
	return workflow.NodeOutput{output}, nil
}

func (executor *VectorStorePGVectorExecutor) search(ctx context.Context, ir workflow.IRNode, cfg pgvectorTableConfig, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	queryText := strings.TrimSpace(textValue(ir.Parameters["query"], ""))
	if queryText == "" && len(input["main"]) > 0 {
		queryText = documentText(input["main"][0].JSON, "text")
	}
	descriptor := embeddingsDescriptorOf(input["embedding"])
	if queryText == "" || descriptor == nil {
		return nil, fmt.Errorf("node %q: query text and an embeddings sub-node are required", ir.Name)
	}
	vectors, err := executor.embedder.EmbedFromDescriptor(ctx, ir.Name, descriptor, request, []string{queryText})
	if err != nil {
		return nil, err
	}
	tool := &vectorStoreTool{
		tableName: cfg.table, idColumn: cfg.id, contentColumn: cfg.content,
		metadataColumn: cfg.metadata, embeddingColumn: cfg.embedding,
		collection: cfg.collection, credentialID: cfg.credentialID,
		topK: int(numberValue(ir.Parameters["topK"])), request: request, sqlGuard: executor.guard,
	}
	if tool.topK <= 0 {
		tool.topK = DefaultVectorTopK
	}
	matches, err := queryCustomerPGVector(ctx, tool, vectors[0])
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	encoded := make([]any, 0, len(matches))
	for _, match := range matches {
		encoded = append(encoded, map[string]any{
			"id": match.ID, "text": match.Content, "metadata": match.Metadata, "distance": match.Distance,
		})
	}
	return workflow.NodeOutput{{{JSON: map[string]any{"table": cfg.table, "matches": encoded}}}}, nil
}

func queryCustomerPGVector(ctx context.Context, tool *vectorStoreTool, query []float64) ([]VectorMatch, error) {
	if tool == nil {
		return nil, fmt.Errorf("customer pgvector is not configured")
	}
	if err := validateSQLIdentifier(tool.tableName, "tableName"); err != nil {
		return nil, err
	}
	connection, err := openCustomerPostgres(ctx, tool.request, tool.credentialID, tool.sqlGuard)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	topK := tool.topK
	if topK <= 0 {
		topK = DefaultVectorTopK
	}
	statement := fmt.Sprintf(
		`SELECT %s, %s, %s::text, (%s <=> $1::vector) AS distance FROM %s`,
		quote(tool.idColumn), quote(tool.contentColumn), quote(tool.metadataColumn), quote(tool.embeddingColumn), quote(tool.tableName),
	)
	args := []any{vectorLiteral(query)}
	if strings.TrimSpace(tool.collection) != "" {
		statement += ` WHERE ` + quote(tool.metadataColumn) + `->>'collection' = $2`
		args = append(args, tool.collection)
		statement += ` ORDER BY ` + quote(tool.embeddingColumn) + ` <=> $1::vector LIMIT $3`
		args = append(args, topK)
	} else {
		statement += ` ORDER BY ` + quote(tool.embeddingColumn) + ` <=> $1::vector LIMIT $2`
		args = append(args, topK)
	}
	result, err := connection.Query(ctx, statement, args, sqlnode.DefaultLimits())
	if err != nil {
		return nil, customerPGVectorError(tool.tableName, err)
	}
	matches := make([]VectorMatch, 0, len(result.Rows))
	for _, row := range result.Rows {
		match := VectorMatch{
			ID:       textValue(row[tool.idColumn], ""),
			Content:  textValue(row[tool.contentColumn], ""),
			Distance: numberValue(row["distance"]),
		}
		if raw := textValue(row[tool.metadataColumn], ""); raw != "" {
			_ = json.Unmarshal([]byte(raw), &match.Metadata)
		}
		matches = append(matches, match)
	}
	return matches, nil
}

func openCustomerPostgres(ctx context.Context, request engine.Request, credentialID string, guard sqlnode.Guard) (*sqlnode.Connection, error) {
	if request.Credentials == nil {
		return nil, fmt.Errorf("credentials are not available in this runtime")
	}
	secret, err := request.Credentials.ResolveCredential(ctx, credentialID)
	if err != nil {
		return nil, err
	}
	if secret.Type != "postgres" {
		return nil, fmt.Errorf("credential %q is a %s credential, not postgres", secret.Name, secret.Type)
	}
	scoped := guard
	scoped.AllowedDomains = secret.AllowedDomains
	return sqlnode.Open(ctx, sqlnode.DriverPostgres, secret.Fields, scoped)
}

func customerPGVectorError(table string, err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if strings.Contains(message, "does not exist") || strings.Contains(strings.ToLower(message), "undefinedtable") || strings.Contains(message, "42P01") {
		return fmt.Errorf("the vector table %q is missing. An operator must create it, for example:\nCREATE EXTENSION IF NOT EXISTS vector;\nCREATE TABLE %s (id TEXT PRIMARY KEY, text TEXT, metadata JSONB, embedding VECTOR(1536));\nThis workflow will not run CREATE EXTENSION or CREATE TABLE", table, table)
	}
	return err
}

func searchCustomerPGVector(ctx context.Context, tool *vectorStoreTool, query []float64) ([]VectorMatch, error) {
	return queryCustomerPGVector(ctx, tool, query)
}
