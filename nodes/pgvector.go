// Vector store and embeddings nodes, backed by PostgreSQL's pgvector
// extension.
//
// Retrieval is a PostgreSQL-tier unlock rather than a new external service:
// with the PostgreSQL driver selected and the vector extension available, a
// workflow gets an embeddings node and a vector store node; on SQLite, or on
// a PostgreSQL without the extension, both nodes refuse with a message saying
// what to install instead of appearing to work and returning nothing.
//
// Registration is split the way the AI nodes do it: this file owns the node
// definitions and the executors, and the server wires them in one place —
// definitions through EmbeddingsNode and VectorStoreNode, executors through
// NewEmbeddingsExecutor and NewVectorStoreExecutor. VectorUnavailableReason
// is the one decision the wiring makes: the driver this install runs on, and
// whether a PostgreSQL carries the extension, collapse into the reason string
// both definitions validate against and both executors fail with.
package nodes

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Executor IDs for the vector family.
const (
	EmbeddingsExecutorID  = "core.embeddings"
	VectorStoreExecutorID = "core.vectorStore"
)

// Node types for the vector family.
const (
	EmbeddingsNodeType  = "kilasflow.embeddings"
	VectorStoreNodeType = "kilasflow.vectorStore"
)

// Operations the vector store node runs.
const (
	VectorOperationInsert = "insert"
	VectorOperationSearch = "search"
	VectorOperationDelete = "delete"
)

// Distance functions one collection records. The value is the contract: it
// selects both the query operator and the operator class the migration
// indexed, and an index built for one operator class does not serve another.
const (
	VectorDistanceCosine = "cosine"
	VectorDistanceL2     = "l2"
	VectorDistanceIP     = "ip"
)

// Index types one collection records. Both exist for every distance on every
// document table from the migration, so either choice is indexed from the
// moment a collection can hold rows.
const (
	VectorIndexHNSW    = "hnsw"
	VectorIndexIVFFlat = "ivfflat"
)

// SupportedVectorDimensions names the one document table per dimension the
// migration creates. pgvector fixes a column's dimension and an index cannot
// span dimensions, so these are the embedding widths that actually ship from
// embedding APIs — 384, 768, 1024 and 1536 — rather than an unbounded column
// that could never be indexed.
var SupportedVectorDimensions = []int{384, 768, 1024, 1536}

// MaxIndexableVectorDimension is pgvector's documented limit for the vector
// type: ANN indexes accept vector only up to 2,000 dimensions. A wider
// collection is refused at creation, never accepted and silently left
// unindexed.
const MaxIndexableVectorDimension = 2000

// vectorOperator selects the query operator for a recorded distance.
var vectorOperator = map[string]string{
	VectorDistanceCosine: "<=>",
	VectorDistanceL2:     "<->",
	VectorDistanceIP:     "<#>",
}

// DefaultEmbeddingsModel is what an embeddings node asks for when it names
// none.
const DefaultEmbeddingsModel = "text-embedding-3-small"

// DefaultEmbeddingsBaseURL is what an embeddings node calls when it names no
// address.
const DefaultEmbeddingsBaseURL = "https://api.openai.com/v1"

// DefaultVectorTopK is the match count a search returns when it names none.
const DefaultVectorTopK = 4

// MaxVectorTopK bounds one search so a typo cannot ask for the whole table.
const MaxVectorTopK = 100

// VectorAvailability is the half of the store an embeddings node needs: it
// never reads a document, but it still refuses when there is nowhere to put
// its vectors.
type VectorAvailability interface {
	CheckAvailable(ctx context.Context) error
}

// VectorCollection is one caller-named collection: its dimension, distance
// function and index type, and the storage table it lives in.
type VectorCollection struct {
	ID        string
	Dimension int
	Distance  string
	IndexType string
	TableName string
}

// VectorDocument is one row to insert: content, its vector, and a metadata
// object that search can filter on.
type VectorDocument struct {
	ID        string
	Content   string
	Embedding []float64
	Metadata  map[string]any
}

// VectorMatch is one similarity hit: the document plus its distance. Lower is
// closer under every distance function this store records.
type VectorMatch struct {
	ID       string
	Content  string
	Metadata map[string]any
	Distance float64
}

// VectorStore inserts, deletes and similarity-queries documents scoped by
// tenant and by a caller-named collection.
type VectorStore interface {
	VectorAvailability
	EnsureCollection(ctx context.Context, tenant, name string, dimension int, distance, indexType string) (VectorCollection, error)
	Insert(ctx context.Context, tenant, collection string, documents []VectorDocument) error
	Search(ctx context.Context, tenant, collection string, query []float64, topK int, filter map[string]any) ([]VectorMatch, error)
	Delete(ctx context.Context, tenant, collection string, ids []string) (int64, error)
}

// VectorUnavailableReason collapses the install's vector posture into the
// reason string both definitions validate against: empty when the store can
// run, otherwise the message the nodes fail with.
//
// driver is the configured database driver ("postgres", "sqlite", ...). A
// PostgreSQL whose extension state is unknown at wiring time passes the probe
// separately through ProbeVectorExtension; that probe runs at boot, while
// this one answers for every driver that can never carry the extension.
func VectorUnavailableReason(driver string) string {
	if driver == "postgres" {
		return ""
	}
	if strings.TrimSpace(driver) == "" {
		return "the embeddings and vector store nodes need PostgreSQL with the pgvector extension installed"
	}
	return fmt.Sprintf("the embeddings and vector store nodes need PostgreSQL with the pgvector extension installed; this install runs on %s, where they are unavailable", driver)
}

// ProbeVectorExtension reports whether a PostgreSQL carries the vector
// extension, so the wiring can refuse with the install message rather than
// letting the first workflow discover it.
func ProbeVectorExtension(ctx context.Context, db *sql.DB) error {
	var present int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM pg_extension WHERE extname = 'vector'`).Scan(&present)
	if err == sql.ErrNoRows {
		return fmt.Errorf("the embeddings and vector store nodes need the PostgreSQL pgvector extension: run CREATE EXTENSION vector as a superuser, or install the pgvector package for your server, and restart")
	}
	return err
}

// DisabledVectorStore is the store a driver without vector support wires in:
// every operation fails with the install message. It keeps the executors'
// failure identical on SQLite, on MySQL, and on a PostgreSQL whose extension
// is absent, instead of one wording per driver.
type DisabledVectorStore struct {
	Reason string
}

// NewDisabledVectorStore builds the store that refuses. An empty reason is a
// wiring mistake, so it reads as one rather than as a passing empty error.
func NewDisabledVectorStore(reason string) DisabledVectorStore {
	if strings.TrimSpace(reason) == "" {
		reason = "the vector store is not configured on this install"
	}
	return DisabledVectorStore{Reason: reason}
}

// CheckAvailable implements VectorAvailability.
func (store DisabledVectorStore) CheckAvailable(_ context.Context) error {
	return fmt.Errorf("%s", store.Reason)
}

// EnsureCollection implements VectorStore.
func (store DisabledVectorStore) EnsureCollection(_ context.Context, _, _ string, _ int, _, _ string) (VectorCollection, error) {
	return VectorCollection{}, fmt.Errorf("%s", store.Reason)
}

// Insert implements VectorStore.
func (store DisabledVectorStore) Insert(_ context.Context, _, _ string, _ []VectorDocument) error {
	return fmt.Errorf("%s", store.Reason)
}

// Search implements VectorStore.
func (store DisabledVectorStore) Search(_ context.Context, _, _ string, _ []float64, _ int, _ map[string]any) ([]VectorMatch, error) {
	return nil, fmt.Errorf("%s", store.Reason)
}

// Delete implements VectorStore.
func (store DisabledVectorStore) Delete(_ context.Context, _, _ string, _ []string) (int64, error) {
	return 0, fmt.Errorf("%s", store.Reason)
}

// PostgresVectorStore is the VectorStore over the internal PostgreSQL: the
// tables migration 000006 creates, under the configured table prefix. It
// holds a database/sql handle rather than a GORM one because its SQL is all
// pgvector operators GORM has no spelling for, and the handle is the
// installation's own — never a workflow-authored credential, which must not
// touch the internal database.
type PostgresVectorStore struct {
	db     *sql.DB
	prefix string
}

// NewPostgresVectorStore binds the store to the internal database and the
// table prefix the migration runner applied.
func NewPostgresVectorStore(db *sql.DB, prefix string) *PostgresVectorStore {
	return &PostgresVectorStore{db: db, prefix: prefix}
}

// CheckAvailable implements VectorAvailability.
func (store *PostgresVectorStore) CheckAvailable(ctx context.Context) error {
	return ProbeVectorExtension(ctx, store.db)
}

// collectionsTable names the catalogue table under the configured prefix.
func (store *PostgresVectorStore) collectionsTable() string {
	return store.prefix + "vector_collections"
}

// documentsTable names one dimension's storage table under the configured
// prefix. The dimension reaches here only after EnsureCollection validated
// it, so the name is built from an allowlisted number, never from input.
func (store *PostgresVectorStore) documentsTable(dimension int) string {
	return store.prefix + "vector_documents_" + strconv.Itoa(dimension)
}

// quote quotes one already-validated identifier. Values never pass through
// here — they travel as placeholders — so interpolation cannot carry input.
func quote(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

// EnsureCollection implements VectorStore.
func (store *PostgresVectorStore) EnsureCollection(ctx context.Context, tenant, name string, dimension int, distance, indexType string) (VectorCollection, error) {
	if strings.TrimSpace(tenant) == "" {
		return VectorCollection{}, fmt.Errorf("tenant is required")
	}
	if strings.TrimSpace(name) == "" {
		return VectorCollection{}, fmt.Errorf("collection is required")
	}
	if err := checkVectorDimension(dimension); err != nil {
		return VectorCollection{}, err
	}
	if err := checkVectorDistance(distance); err != nil {
		return VectorCollection{}, err
	}
	if err := checkVectorIndexType(indexType); err != nil {
		return VectorCollection{}, err
	}
	now := time.Now().UTC()
	collections := quote(store.collectionsTable())
	id, err := workflow.NewID("vec")
	if err != nil {
		return VectorCollection{}, err
	}
	row := store.db.QueryRowContext(ctx,
		`SELECT "id", "dimension", "distance", "index_type", "table_name" FROM `+collections+` WHERE "tenant_id" = $1 AND "name" = $2`,
		tenant, name)
	collection := VectorCollection{}
	var dimension64 int64
	if err := row.Scan(&collection.ID, &dimension64, &collection.Distance, &collection.IndexType, &collection.TableName); err == nil {
		collection.Dimension = int(dimension64)
		return store.matchCollection(collection, name, dimension, distance, indexType)
	} else if err != sql.ErrNoRows {
		return VectorCollection{}, err
	}
	tableName := "vector_documents_" + strconv.Itoa(dimension)
	_, err = store.db.ExecContext(ctx,
		`INSERT INTO `+collections+` ("id", "tenant_id", "name", "dimension", "distance", "index_type", "table_name", "created_at", "updated_at") VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, tenant, name, dimension, distance, indexType, tableName, now, now)
	if err != nil {
		// The claim is re-read rather than the error inspected: the row is
		// only there if somebody committed it, and the loser's own insert
		// rolls back with its error. A genuine failure re-reads as absent
		// and returns the original error, so no error text is matched here.
		row := store.db.QueryRowContext(ctx,
			`SELECT "id", "dimension", "distance", "index_type", "table_name" FROM `+collections+` WHERE "tenant_id" = $1 AND "name" = $2`,
			tenant, name)
		collection := VectorCollection{}
		var dimension64 int64
		if readErr := row.Scan(&collection.ID, &dimension64, &collection.Distance, &collection.IndexType, &collection.TableName); readErr != nil {
			return VectorCollection{}, err
		}
		collection.Dimension = int(dimension64)
		return store.matchCollection(collection, name, dimension, distance, indexType)
	}
	return VectorCollection{ID: id, Dimension: dimension, Distance: distance, IndexType: indexType, TableName: tableName}, nil
}

// matchCollection checks a stored collection against the shape the caller
// asked for. A name that already means a 768-dimensional cosine collection
// cannot also mean a 1536-dimensional one; the second writer learns the
// first writer's shape instead of silently sharing it.
func (store *PostgresVectorStore) matchCollection(collection VectorCollection, name string, dimension int, distance, indexType string) (VectorCollection, error) {
	if collection.Dimension != dimension {
		return VectorCollection{}, fmt.Errorf("collection %q already holds %d-dimensional vectors, not %d-dimensional ones", name, collection.Dimension, dimension)
	}
	if collection.Distance != distance {
		return VectorCollection{}, fmt.Errorf("collection %q already searches by %s distance, not %s", name, collection.Distance, distance)
	}
	if collection.IndexType != indexType {
		return VectorCollection{}, fmt.Errorf("collection %q already uses a %s index, not %s", name, collection.IndexType, indexType)
	}
	return collection, nil
}

// checkVectorDimension refuses a dimension the store cannot index, naming the
// limit. pgvector ANN indexes accept vector only up to 2,000 dimensions, and
// this store keeps one table per supported width rather than an unbounded
// column — so anything outside the shipped set is refused at creation, never
// accepted and silently left unindexed.
func checkVectorDimension(dimension int) error {
	for _, supported := range SupportedVectorDimensions {
		if dimension == supported {
			return nil
		}
	}
	return fmt.Errorf("dimension %d cannot be indexed: pgvector vector columns are indexable only up to %d dimensions and this store keeps one table per supported width (%s)",
		dimension, MaxIndexableVectorDimension, supportedVectorDimensions())
}

// supportedVectorDimensions names the shipped widths for diagnostics.
func supportedVectorDimensions() string {
	widths := make([]string, 0, len(SupportedVectorDimensions))
	for _, dimension := range SupportedVectorDimensions {
		widths = append(widths, strconv.Itoa(dimension))
	}
	return strings.Join(widths, ", ")
}

// checkVectorDistance refuses an unknown distance function.
func checkVectorDistance(distance string) error {
	if _, found := vectorOperator[distance]; found {
		return nil
	}
	return fmt.Errorf("distance must be %q, %q or %q", VectorDistanceCosine, VectorDistanceL2, VectorDistanceIP)
}

// checkVectorIndexType refuses an unknown index type.
func checkVectorIndexType(indexType string) error {
	switch indexType {
	case VectorIndexHNSW, VectorIndexIVFFlat:
		return nil
	}
	return fmt.Errorf("index type must be %q or %q", VectorIndexHNSW, VectorIndexIVFFlat)
}

// vectorLiteral formats one vector as the text literal pgvector parses, bound
// with an explicit ::vector cast. Text rather than a new dependency: one type
// does not justify a driver in the module graph, and the cast keeps the
// placeholder's type exact instead of leaving it to inference.
func vectorLiteral(embedding []float64) string {
	var literal strings.Builder
	literal.WriteByte('[')
	for index, value := range embedding {
		if index > 0 {
			literal.WriteByte(',')
		}
		literal.WriteString(strconv.FormatFloat(value, 'f', -1, 64))
	}
	literal.WriteByte(']')
	return literal.String()
}

// Insert implements VectorStore.
func (store *PostgresVectorStore) Insert(ctx context.Context, tenant, collection string, documents []VectorDocument) error {
	if len(documents) == 0 {
		return fmt.Errorf("at least one document is required")
	}
	dimension := len(documents[0].Embedding)
	if dimension == 0 {
		return fmt.Errorf("item 1 carries no vector")
	}
	for index, document := range documents[1:] {
		if len(document.Embedding) != dimension {
			return fmt.Errorf("item %d carries a %d-dimensional vector but item 1 carries %d", index+2, len(document.Embedding), dimension)
		}
	}
	// The collection is read, not written blind: its recorded dimension is
	// what every vector is checked against, and an insert that names a new
	// collection refuses rather than inventing one with defaults the caller
	// never chose.
	record, err := store.findCollection(ctx, tenant, collection)
	if err != nil {
		return err
	}
	if record.Dimension != dimension {
		return fmt.Errorf("item 1 carries a %d-dimensional vector but collection %q holds %d-dimensional vectors", dimension, collection, record.Dimension)
	}
	for index, document := range documents {
		if len(document.Embedding) != record.Dimension {
			return fmt.Errorf("item %d carries a %d-dimensional vector but collection %q holds %d-dimensional vectors", index+1, len(document.Embedding), collection, record.Dimension)
		}
	}
	table := quote(store.documentsTable(record.Dimension))
	for index, document := range documents {
		id := strings.TrimSpace(document.ID)
		if id == "" {
			id, err = workflow.NewID("vecdoc")
			if err != nil {
				return err
			}
		}
		metadata, err := json.Marshal(vectorMetadata(document.Metadata))
		if err != nil {
			return fmt.Errorf("item %d has metadata that does not encode as JSON: %w", index+1, err)
		}
		if _, err := store.db.ExecContext(ctx,
			`INSERT INTO `+table+` ("id", "tenant_id", "collection_id", "content", "metadata", "embedding") VALUES ($1, $2, $3, $4, $5::jsonb, $6::vector)`,
			id, tenant, record.ID, document.Content, string(metadata), vectorLiteral(document.Embedding)); err != nil {
			return fmt.Errorf("item %d could not be inserted: %w", index+1, err)
		}
	}
	return nil
}

// vectorMetadata normalises absent metadata to the empty object the column
// default carries, so a document without metadata still matches filters.
func vectorMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return map[string]any{}
	}
	return metadata
}

// findCollection reads one collection row or reports the name as unknown.
func (store *PostgresVectorStore) findCollection(ctx context.Context, tenant, name string) (VectorCollection, error) {
	collections := quote(store.collectionsTable())
	collection := VectorCollection{}
	var dimension64 int64
	err := store.db.QueryRowContext(ctx,
		`SELECT "id", "dimension", "distance", "index_type", "table_name" FROM `+collections+` WHERE "tenant_id" = $1 AND "name" = $2`,
		tenant, name).Scan(&collection.ID, &dimension64, &collection.Distance, &collection.IndexType, &collection.TableName)
	if err == sql.ErrNoRows {
		return VectorCollection{}, fmt.Errorf("collection %q does not exist", name)
	}
	if err != nil {
		return VectorCollection{}, err
	}
	collection.Dimension = int(dimension64)
	return collection, nil
}

// Search implements VectorStore.
func (store *PostgresVectorStore) Search(ctx context.Context, tenant, collection string, query []float64, topK int, filter map[string]any) ([]VectorMatch, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("the query vector is empty")
	}
	if topK <= 0 {
		topK = DefaultVectorTopK
	}
	if topK > MaxVectorTopK {
		topK = MaxVectorTopK
	}
	record, err := store.findCollection(ctx, tenant, collection)
	if err != nil {
		return nil, err
	}
	if len(query) != record.Dimension {
		return nil, fmt.Errorf("the query carries a %d-dimensional vector but collection %q holds %d-dimensional vectors", len(query), collection, record.Dimension)
	}
	statement, arguments, err := searchStatement(store.documentsTable(record.Dimension), record, collection, tenant, query, topK, filter)
	if err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var matches []VectorMatch
	for rows.Next() {
		match := VectorMatch{}
		var rawMetadata string
		if err := rows.Scan(&match.ID, &match.Content, &rawMetadata, &match.Distance); err != nil {
			return nil, err
		}
		metadata := map[string]any{}
		if err := json.Unmarshal([]byte(rawMetadata), &metadata); err != nil {
			return nil, fmt.Errorf("document %q carries metadata that does not decode as JSON: %w", match.ID, err)
		}
		match.Metadata = metadata
		matches = append(matches, match)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return matches, nil
}

// searchStatement builds the similarity query one collection's recorded
// distance drives: the operator in the SELECT and the ORDER BY that ranks by
// it. Shared by Search and ExplainSearch so the captured plan is the plan of
// the statement the server actually receives.
func searchStatement(table string, record VectorCollection, name, tenant string, query []float64, topK int, filter map[string]any) (string, []any, error) {
	operator, found := vectorOperator[record.Distance]
	if !found {
		return "", nil, fmt.Errorf("collection %q records unknown distance %q", name, record.Distance)
	}
	quoted := quote(table)
	arguments := []any{vectorLiteral(query), tenant, record.ID}
	statement := `SELECT "id", "content", "metadata", ("embedding" ` + operator + ` $1::vector) AS "distance" FROM ` + quoted + ` WHERE "tenant_id" = $2 AND "collection_id" = $3`
	if len(filter) > 0 {
		encoded, err := json.Marshal(filter)
		if err != nil {
			return "", nil, fmt.Errorf("the metadata filter does not encode as JSON: %w", err)
		}
		arguments = append(arguments, string(encoded))
		statement += ` AND "metadata" @> $` + strconv.Itoa(len(arguments)) + `::jsonb`
	}
	statement += ` ORDER BY "distance" ASC LIMIT ` + strconv.Itoa(topK)
	return statement, arguments, nil
}

// ExplainSearch returns the query plan for the similarity statement Search
// would run, so coverage can capture as evidence that the ANN index serves
// the query. Sequential scans are disabled inside the plan's transaction —
// the planner would otherwise scan a test-sized table and prove nothing
// about the index — and SET LOCAL keeps the toggle inside it.
func (store *PostgresVectorStore) ExplainSearch(ctx context.Context, tenant, collection string, query []float64, topK int) ([]string, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("the query vector is empty")
	}
	if topK <= 0 {
		topK = DefaultVectorTopK
	}
	if topK > MaxVectorTopK {
		topK = MaxVectorTopK
	}
	record, err := store.findCollection(ctx, tenant, collection)
	if err != nil {
		return nil, err
	}
	if len(query) != record.Dimension {
		return nil, fmt.Errorf("the query carries a %d-dimensional vector but collection %q holds %d-dimensional vectors", len(query), collection, record.Dimension)
	}
	statement, arguments, err := searchStatement(store.documentsTable(record.Dimension), record, collection, tenant, query, topK, nil)
	if err != nil {
		return nil, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `EXPLAIN `+statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, err
		}
		plan = append(plan, line)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return plan, nil
}

// Delete implements VectorStore.
func (store *PostgresVectorStore) Delete(ctx context.Context, tenant, collection string, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, fmt.Errorf("at least one document id is required")
	}
	record, err := store.findCollection(ctx, tenant, collection)
	if err != nil {
		return 0, err
	}
	table := quote(store.documentsTable(record.Dimension))
	result, err := store.db.ExecContext(ctx,
		`DELETE FROM `+table+` WHERE "tenant_id" = $1 AND "collection_id" = $2 AND "id" = ANY($3)`,
		tenant, record.ID, ids)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// EmbeddingsNode builds the embeddings definition.
//
// unavailable is ignored: embeddings call an OpenAI-compatible HTTP API and
// do not need the install's pgvector. The argument is kept so existing call
// sites that pass VectorUnavailableReason keep compiling.
func EmbeddingsNode(unavailable string) node.Definition {
	_ = unavailable
	return node.Definition{
		Type:        EmbeddingsNodeType,
		Version:     workflow.V(1),
		Credentials: []node.CredentialRequirement{{Type: OpenAICredentialType}, {Type: OpenRouterCredentialType}, {Type: BearerCredentialType}},
		DisplayName: "Embeddings",
		Description: "Turns text into embedding vectors through an OpenAI-compatible embeddings API. Connected as a cluster sub-node it emits a descriptor the Vector Store calls; connected on main it still embeds each item's text.",
		Category:    "AI",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:brain"},
		IconColor:   "#a855f7",
		Subtitle:    "{{ $parameter.model }}",
		Inputs:      mainInput(),
		Outputs: []workflow.Port{
			{Name: "main", Kind: workflow.ConnectionMain},
			{Name: "embedding", Kind: workflow.ConnectionEmbedding},
		},
		PortsFor: embeddingsPortsFor,
		Parameters: []node.PropertyDefinition{
			{
				Key: "model", Label: "Model", Kind: node.PropertyString, Required: true,
				Default:     DefaultEmbeddingsModel,
				Description: "The embeddings model to call, for example text-embedding-3-small.",
			},
			{
				Key: "baseUrl", Label: "Base URL", Kind: node.PropertyString, Default: DefaultEmbeddingsBaseURL,
				Description: "Change this only to reach the provider through a gateway or a proxy. Any OpenAI-compatible /embeddings endpoint works.",
			},
			{
				Key: "timeout", Label: "Timeout (ms)", Kind: node.PropertyNumber, Default: float64(60000),
				Description: "How long one embeddings call may take before it fails.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     EmbeddingsExecutorID,
		Validate:       validateEmbeddings,
		Codex:          &node.NodeCodex{Categories: []string{"AI"}, Subcategories: map[string][]string{"AI": {"Embeddings"}}},
	}
}

func validateEmbeddings(n workflow.Node) error {
	if strings.TrimSpace(textValue(n.Parameters["model"], "")) == "" {
		return fmt.Errorf("model is required")
	}
	return nil
}

// embeddingsCredentialID names the first embeddings credential the node
// carries, in provider order.
func embeddingsCredentialID(credentials map[string]string) string {
	for _, credentialType := range []string{OpenAICredentialType, OpenRouterCredentialType, BearerCredentialType} {
		if id := strings.TrimSpace(credentials[credentialType]); id != "" {
			return id
		}
	}
	return ""
}

// VectorStoreNode builds the vector store definition. unavailable reads as
// EmbeddingsNode's does.
func VectorStoreNode(unavailable string) node.Definition {
	return node.Definition{
		Type:        VectorStoreNodeType,
		Version:     workflow.V(1),
		DisplayName: "Vector Store",
		Description: "Inserts, deletes and similarity-searches documents in pgvector collections.",
		Category:    "AI",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:layers"},
		IconColor:   "#0ea5e9",
		Subtitle:    "{{ $parameter.operation }} {{ $parameter.collection }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		PortsFor:    vectorStorePortsFor,
		Parameters: []node.PropertyDefinition{
			{
				Key: "mode", Label: "Mode", Kind: node.PropertyOptions,
				Description: "n8n cluster mode. Leave empty on native sequential graphs that use Operation.",
				Options: []node.PropertyOption{
					{Label: "Insert Documents", Value: VectorModeInsert},
					{Label: "Get Many", Value: VectorModeGetMany},
					{Label: "Retrieve as Tool", Value: VectorModeRetrieveAsTool},
				},
			},
			{
				Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Required: true,
				Default: VectorOperationInsert,
				Options: []node.PropertyOption{
					{Label: "Insert Documents", Value: VectorOperationInsert},
					{Label: "Similarity Search", Value: VectorOperationSearch},
					{Label: "Delete Documents", Value: VectorOperationDelete},
				},
			},
			{
				Key: "toolName", Label: "Tool name", Kind: node.PropertyString,
				Description: "Name the agent calls when mode is retrieve-as-tool.",
			},
			{
				Key: "toolDescription", Label: "Tool description", Kind: node.PropertyString,
				Description: "What the vector store tool retrieves, written for the model.",
			},
			{
				Key: "topK", Label: "Top K", Kind: node.PropertyNumber, Default: float64(DefaultVectorTopK),
				Description: "How many matches a similarity search or retrieve-as-tool call returns.",
			},
			{
				Key: "collection", Label: "Collection", Kind: node.PropertyString, Required: true,
				Description: "The caller-named collection, scoped to this tenant. Insert creates it with the dimension, distance and index type below when it does not exist yet.",
			},
			{
				Key: "distance", Label: "Distance", Kind: node.PropertyOptions, Default: VectorDistanceCosine,
				Options: []node.PropertyOption{
					{Label: "Cosine", Value: VectorDistanceCosine},
					{Label: "Euclidean (L2)", Value: VectorDistanceL2},
					{Label: "Inner Product", Value: VectorDistanceIP},
				},
				Description: "Recorded when the collection is created; an index built for one distance does not serve another.",
			},
			{
				Key: "indexType", Label: "Index Type", Kind: node.PropertyOptions, Default: VectorIndexHNSW,
				Options: []node.PropertyOption{
					{Label: "HNSW", Value: VectorIndexHNSW},
					{Label: "IVFFlat", Value: VectorIndexIVFFlat},
				},
				Description: "Recorded when the collection is created. Both exist for every distance from the migration, so either choice is indexed.",
			},
			{
				Key: "queryVector", Label: "Query Vector", Kind: node.PropertyJSON,
				Description: "The query embedding as a JSON array, for example {{ $json.embedding }} from an Embeddings node.",
			},
			{
				Key: "metadataFilter", Label: "Metadata Filter", Kind: node.PropertyJSON,
				Description: "A JSON object every match's metadata must contain, for example {\"source\": \"handbook\"}.",
			},
			{
				Key: "ids", Label: "Document IDs", Kind: node.PropertyJSON,
				Description: "A JSON array of document ids the delete operation removes.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     VectorStoreExecutorID,
		Validate:       validateVectorStore(unavailable),
	}
}

// validateVectorStore checks the static configuration plus availability.
func validateVectorStore(unavailable string) workflow.ConfigValidator {
	return func(n workflow.Node) error {
		if strings.TrimSpace(unavailable) != "" {
			return fmt.Errorf("%s", unavailable)
		}
		mode := vectorStoreModeOf(n.Parameters)
		switch mode {
		case VectorModeInsert, VectorModeGetMany, VectorModeRetrieveAsTool, VectorOperationDelete:
		default:
			return fmt.Errorf("operation must be %q, %q or %q, or mode %q, %q or %q",
				VectorOperationInsert, VectorOperationSearch, VectorOperationDelete,
				VectorModeInsert, VectorModeGetMany, VectorModeRetrieveAsTool)
		}
		if strings.TrimSpace(textValue(n.Parameters["collection"], "")) == "" {
			return fmt.Errorf("collection is required")
		}
		if raw, present := n.Parameters["distance"]; present && raw != nil && strings.TrimSpace(textValue(raw, "")) != "" {
			if err := checkVectorDistance(textValue(raw, "")); err != nil {
				return err
			}
		}
		if raw, present := n.Parameters["indexType"]; present && raw != nil && strings.TrimSpace(textValue(raw, "")) != "" {
			if err := checkVectorIndexType(textValue(raw, "")); err != nil {
				return err
			}
		}
		return nil
	}
}

// EmbeddingsExecutor calls an OpenAI-compatible embeddings API through the
// deployment's outbound policy. The HTTP client carries that policy with its
// clock removed — the same shape newModelBackend builds — so per-node
// timeouts stay reachable while the SSRF guard and redirect policy apply
// exactly as they do to a chat completion; no second policy and no new HTTP
// client are constructed anywhere in the path.
type EmbeddingsExecutor struct {
	client *http.Client
	policy safehttp.Policy
	store  VectorAvailability
}

// NewEmbeddingsExecutor binds the executor to the deployment's outbound
// policy and the install's vector availability.
func NewEmbeddingsExecutor(policy safehttp.Policy, store VectorAvailability) *EmbeddingsExecutor {
	modelPolicy := policy
	modelPolicy.Timeout = 0
	return &EmbeddingsExecutor{client: safehttp.NewClient(modelPolicy), policy: policy, store: store}
}

// Execute implements engine.Executor.
func (executor *EmbeddingsExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	descriptor := embeddingsDescriptorItem(ir)
	if strings.EqualFold(strings.TrimSpace(textValue(ir.Parameters["mode"], "")), EmbeddingsModeCluster) {
		return workflow.NodeOutput{[]workflow.Item{descriptor}}, nil
	}
	items := input["main"]
	if len(items) == 0 {
		return workflow.NodeOutput{[]workflow.Item{}, []workflow.Item{descriptor}}, nil
	}
	texts := make([]string, 0, len(items))
	for index, item := range items {
		text, _ := item.JSON["text"].(string)
		if strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("node %q: item %d carries no text to embed", ir.Name, index+1)
		}
		texts = append(texts, text)
	}
	credentialID := embeddingsCredentialID(ir.Credentials)
	if credentialID == "" {
		return nil, fmt.Errorf("node %q: an openAiApi, openRouterApi or httpBearerAuth credential holding the API key is required", ir.Name)
	}
	if request.Credentials == nil {
		return nil, fmt.Errorf("node %q: credentials are not available in this runtime", ir.Name)
	}
	secret, err := request.Credentials.ResolveCredential(ctx, credentialID)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	apiKeyField, usable := modelAPIKeyFields[secret.Type]
	if !usable {
		return nil, fmt.Errorf("node %q: credential %q is a %s credential, which no embeddings model can authenticate with",
			ir.Name, secret.Name, secret.Type)
	}
	baseURL := textValue(ir.Parameters["baseUrl"], DefaultEmbeddingsBaseURL)
	target, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(baseURL), "/"))
	if err != nil || strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("node %q: the embeddings base URL is not a valid URL", ir.Name)
	}
	// The same pre-flight gate the HTTP node applies, and the credential's
	// own domain scope binding the call exactly as it binds a chat model: a
	// credential scoped to one host cannot be pointed at another by editing
	// one parameter on this node.
	if err := executor.policy.CheckURL(target); err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if !secret.AllowsHost(target.Host) {
		return nil, fmt.Errorf("node %q: credential %q is not allowed for host %q",
			ir.Name, secret.Name, target.Hostname())
	}
	model := textValue(ir.Parameters["model"], DefaultEmbeddingsModel)
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("node %q: model is required", ir.Name)
	}
	timeout, err := embeddingsTimeout(ir)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	embeddings, err := executor.embed(ctx, target.String(), secret.Fields[apiKeyField], model, texts, timeout)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	output := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		json, err := cloneItemJSON(item.JSON)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d is not JSON-serialisable: %w", ir.Name, index+1, err)
		}
		json["embedding"] = embeddings[index]
		json["model"] = model
		output = append(output, workflow.Item{JSON: json})
	}
	return workflow.NodeOutput{output, []workflow.Item{descriptor}}, nil
}

// embeddingsTimeout reads the node's own timeout in milliseconds. Refused
// above the deployment's ceiling rather than clamped, for the same reason a
// model timeout is: silently granting less than asked turns a configuration
// mistake into an intermittent transport error.
func embeddingsTimeout(ir workflow.IRNode) (time.Duration, error) {
	raw, present := ir.Parameters["timeout"]
	if !present || raw == nil {
		return 60 * time.Second, nil
	}
	milliseconds := numberValue(raw)
	if milliseconds <= 0 {
		return 0, fmt.Errorf("timeout must be a positive number of milliseconds")
	}
	timeout := time.Duration(milliseconds * float64(time.Millisecond))
	if timeout > DefaultModelTimeoutCeiling {
		return 0, ErrModelTimeoutAboveCeiling
	}
	return timeout, nil
}

// embed calls the provider's embeddings endpoint once for the whole batch.
// One request rather than one per item: the API takes an array, and a call
// per item would spend the handshake once per row for no reason.
func (executor *EmbeddingsExecutor) embed(ctx context.Context, baseURL, apiKey, model string, texts []string, timeout time.Duration) ([][]float64, error) {
	body, err := json.Marshal(map[string]any{"model": model, "input": texts})
	if err != nil {
		return nil, fmt.Errorf("the embeddings request does not encode as JSON: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	call, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("the embeddings request could not be built: %w", err)
	}
	call.Header.Set("Content-Type", "application/json")
	call.Header.Set("Authorization", "Bearer "+apiKey)
	response, err := executor.client.Do(call)
	if err != nil {
		return nil, safehttp.RedactError(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("the embeddings response could not be read: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the embeddings request failed with status %d: %.200s", response.StatusCode, strings.TrimSpace(string(payload)))
	}
	var decoded struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("the embeddings response did not decode: %w", err)
	}
	if len(decoded.Data) != len(texts) {
		return nil, fmt.Errorf("the embeddings response carried %d vectors for %d texts", len(decoded.Data), len(texts))
	}
	embeddings := make([][]float64, len(texts))
	for _, datum := range decoded.Data {
		if datum.Index < 0 || datum.Index >= len(texts) {
			return nil, fmt.Errorf("the embeddings response carried a vector for out-of-range index %d", datum.Index)
		}
		if len(datum.Embedding) == 0 {
			return nil, fmt.Errorf("the embeddings response carried an empty vector for index %d", datum.Index)
		}
		embeddings[datum.Index] = datum.Embedding
	}
	return embeddings, nil
}

// EmbedFromDescriptor calls the embeddings API using a cluster sub-node's
// $ai descriptor, so a Vector Store or retrieve-as-tool can embed without
// the embeddings node itself processing main items.
func (executor *EmbeddingsExecutor) EmbedFromDescriptor(ctx context.Context, nodeName string, descriptor map[string]any, request engine.Request, texts []string) ([][]float64, error) {
	if executor == nil {
		return nil, fmt.Errorf("node %q: embeddings are not configured on this install", nodeName)
	}
	if len(texts) == 0 {
		return nil, fmt.Errorf("node %q: at least one text is required to embed", nodeName)
	}
	credentialID := textValue(descriptor["credentialId"], "")
	if credentialID == "" {
		return nil, fmt.Errorf("node %q: an embeddings credential is required", nodeName)
	}
	if request.Credentials == nil {
		return nil, fmt.Errorf("node %q: credentials are not available in this runtime", nodeName)
	}
	secret, err := request.Credentials.ResolveCredential(ctx, credentialID)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", nodeName, err)
	}
	apiKeyField, usable := modelAPIKeyFields[secret.Type]
	if !usable {
		return nil, fmt.Errorf("node %q: credential %q is a %s credential, which no embeddings model can authenticate with",
			nodeName, secret.Name, secret.Type)
	}
	baseURL := textValue(descriptor["baseUrl"], DefaultEmbeddingsBaseURL)
	target, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(baseURL), "/"))
	if err != nil || strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("node %q: the embeddings base URL is not a valid URL", nodeName)
	}
	if err := executor.policy.CheckURL(target); err != nil {
		return nil, fmt.Errorf("node %q: %w", nodeName, err)
	}
	if !secret.AllowsHost(target.Host) {
		return nil, fmt.Errorf("node %q: credential %q is not allowed for host %q",
			nodeName, secret.Name, target.Hostname())
	}
	model := textValue(descriptor["model"], DefaultEmbeddingsModel)
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("node %q: model is required", nodeName)
	}
	timeout := 60 * time.Second
	if milliseconds := numberValue(descriptor["timeout"]); milliseconds > 0 {
		timeout = time.Duration(milliseconds) * time.Millisecond
	}
	embeddings, err := executor.embed(ctx, target.String(), secret.Fields[apiKeyField], model, texts, timeout)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", nodeName, err)
	}
	return embeddings, nil
}

// cloneItemJSON copies one item's document so the embedding annotates the
// item without aliasing the input the runner still holds.
func cloneItemJSON(document map[string]any) (map[string]any, error) {
	if document == nil {
		return map[string]any{}, nil
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

// VectorStoreExecutor runs the vector store node's operations against the
// installation's own database handle. Tenant scoping comes from the
// execution, never from a parameter: one tenant's collection name can never
// address another tenant's rows even when the names match.
type VectorStoreExecutor struct {
	store    VectorStore
	embedder *EmbeddingsExecutor
}

// NewVectorStoreExecutor binds the executor to the install's vector store.
func NewVectorStoreExecutor(store VectorStore) *VectorStoreExecutor {
	return &VectorStoreExecutor{store: store}
}

// WithEmbedder lets cluster insert and retrieve-as-tool call the embeddings API.
func (executor *VectorStoreExecutor) WithEmbedder(embedder *EmbeddingsExecutor) *VectorStoreExecutor {
	if executor != nil {
		executor.embedder = embedder
	}
	return executor
}

// Execute implements engine.Executor.
func (executor *VectorStoreExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if executor.store == nil {
		return nil, fmt.Errorf("node %q: the vector store is not configured on this install", ir.Name)
	}
	if err := executor.store.CheckAvailable(ctx); err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	tenant := strings.TrimSpace(request.Execution.TenantID)
	if tenant == "" {
		return nil, fmt.Errorf("node %q: tenant is required", ir.Name)
	}
	collection := strings.TrimSpace(textValue(ir.Parameters["collection"], ""))
	if collection == "" {
		return nil, fmt.Errorf("node %q: collection is required", ir.Name)
	}
	switch vectorStoreModeOf(ir.Parameters) {
	case VectorModeInsert:
		return executor.executeInsert(ctx, ir, tenant, collection, input, request)
	case VectorModeGetMany:
		return executor.executeSearch(ctx, ir, tenant, collection, input, request)
	case VectorModeRetrieveAsTool:
		return executor.executeRetrieveAsTool(ir, collection, input)
	case VectorOperationDelete:
		return executor.executeDelete(ctx, ir, tenant, collection)
	default:
		return nil, fmt.Errorf("node %q: operation must be %q, %q or %q", ir.Name, VectorOperationInsert, VectorOperationSearch, VectorOperationDelete)
	}
}

// executeInsert embeds every input item's text alongside its vector. The
// vector travels in the item — written there by the Embeddings node — so the
// two nodes chain without an intermediate store of their own.
func (executor *VectorStoreExecutor) executeInsert(ctx context.Context, ir workflow.IRNode, tenant, collection string, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	drafts := documentsFromItems(input["document"])
	if len(drafts) == 0 {
		drafts = documentsFromItems(input["main"])
	}
	if len(drafts) == 0 {
		return nil, fmt.Errorf("node %q: at least one input item carrying text and an embedding is required", ir.Name)
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
	documents := make([]VectorDocument, 0, len(drafts))
	for index, draft := range drafts {
		if strings.TrimSpace(draft.Content) == "" {
			return nil, fmt.Errorf("node %q: item %d carries no text to store", ir.Name, index+1)
		}
		if len(draft.Embedding) == 0 {
			return nil, fmt.Errorf("node %q: item %d carries no embedding vector", ir.Name, index+1)
		}
		id := draft.ID
		if id == "" {
			var err error
			id, err = workflow.NewID("vecdoc")
			if err != nil {
				return nil, fmt.Errorf("node %q: %w", ir.Name, err)
			}
		}
		documents = append(documents, VectorDocument{ID: id, Content: draft.Content, Embedding: draft.Embedding, Metadata: draft.Metadata})
	}
	dimension := len(documents[0].Embedding)
	distance := textValue(ir.Parameters["distance"], VectorDistanceCosine)
	indexType := textValue(ir.Parameters["indexType"], VectorIndexHNSW)
	if _, err := executor.store.EnsureCollection(ctx, tenant, collection, dimension, distance, indexType); err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if err := executor.store.Insert(ctx, tenant, collection, documents); err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	output := make([]workflow.Item, 0, len(documents))
	seen := map[string]struct{}{}
	for index, document := range documents {
		output = appendUniqueInsertOutput(output, seen, vectorInsertOutput(drafts[index], document.ID, "collection", collection))
	}
	return workflow.NodeOutput{output}, nil
}

func (executor *VectorStoreExecutor) executeSearch(ctx context.Context, ir workflow.IRNode, tenant, collection string, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	query, err := vectorNumbers(ir.Parameters["queryVector"])
	if err != nil || len(query) == 0 {
		if drafts := documentsFromItems(input["main"]); len(drafts) > 0 && len(drafts[0].Embedding) > 0 {
			query = drafts[0].Embedding
		}
	}
	if len(query) == 0 {
		queryText := strings.TrimSpace(textValue(ir.Parameters["query"], ""))
		if queryText == "" {
			if items := input["main"]; len(items) > 0 {
				queryText = documentText(items[0].JSON, "text")
			}
		}
		if queryText != "" {
			if descriptor := embeddingsDescriptorOf(input["embedding"]); descriptor != nil {
				vectors, embedErr := executor.embedder.EmbedFromDescriptor(ctx, ir.Name, descriptor, request, []string{queryText})
				if embedErr != nil {
					return nil, embedErr
				}
				query = vectors[0]
			}
		}
	}
	if len(query) == 0 {
		return nil, fmt.Errorf("node %q: queryVector must be a JSON array of numbers, for example {{ $json.embedding }} from an Embeddings node", ir.Name)
	}
	topK := int(numberValue(ir.Parameters["topK"]))
	if _, present := ir.Parameters["topK"]; !present || ir.Parameters["topK"] == nil {
		topK = DefaultVectorTopK
	}
	filter, err := vectorObject(ir.Parameters["metadataFilter"])
	if err != nil {
		return nil, fmt.Errorf("node %q: metadataFilter must be a JSON object", ir.Name)
	}
	matches, err := executor.store.Search(ctx, tenant, collection, query, topK, filter)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	encoded := make([]any, 0, len(matches))
	for _, match := range matches {
		encoded = append(encoded, map[string]any{
			"id": match.ID, "text": match.Content, "metadata": match.Metadata, "distance": match.Distance,
		})
	}
	return workflow.NodeOutput{{{JSON: map[string]any{"collection": collection, "matches": encoded}}}}, nil
}

func (executor *VectorStoreExecutor) executeRetrieveAsTool(ir workflow.IRNode, collection string, input workflow.NodeInput) (workflow.NodeOutput, error) {
	name := strings.TrimSpace(textValue(ir.Parameters["toolName"], ""))
	if name == "" {
		name = workflow.NormalizeToolName(ir.Name)
	}
	description := strings.TrimSpace(textValue(ir.Parameters["toolDescription"], ""))
	if description == "" {
		description = "Retrieve relevant documents from the " + collection + " vector store."
	}
	topK := int(numberValue(ir.Parameters["topK"]))
	if topK <= 0 {
		topK = DefaultVectorTopK
	}
	descriptor := map[string]any{
		"kind":        toolKindVectorStore,
		"name":        name,
		"description": description,
		"nodeName":    ir.Name,
		"backend":     "internal",
		"collection":  collection,
		"topK":        topK,
		"distance":    textValue(ir.Parameters["distance"], VectorDistanceCosine),
	}
	if embeddings := embeddingsDescriptorOf(input["embedding"]); embeddings != nil {
		descriptor["embeddings"] = embeddings
	}
	if filter, err := vectorObject(ir.Parameters["metadataFilter"]); err == nil && filter != nil {
		descriptor["metadataFilter"] = filter
	}
	return workflow.NodeOutput{[]workflow.Item{{JSON: map[string]any{descriptorKey: descriptor}}}}, nil
}

// executeDelete removes the named documents and reports how many went.
func (executor *VectorStoreExecutor) executeDelete(ctx context.Context, ir workflow.IRNode, tenant, collection string) (workflow.NodeOutput, error) {
	raw, err := vectorStrings(ir.Parameters["ids"])
	if err != nil || len(raw) == 0 {
		return nil, fmt.Errorf("node %q: ids must be a JSON array of document id strings", ir.Name)
	}
	deleted, err := executor.store.Delete(ctx, tenant, collection, raw)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	return workflow.NodeOutput{{{JSON: map[string]any{"collection": collection, "deleted": deleted}}}}, nil
}

// vectorNumbers reads a JSON array of numbers as floats. The value may arrive
// decoded already or still encoded as a string; both spellings are accepted
// so the parameter works whether the editor sent an array or an expression
// produced text.
func vectorNumbers(raw any) ([]float64, error) {
	switch value := raw.(type) {
	case nil:
		return nil, fmt.Errorf("not a vector")
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil, fmt.Errorf("not a vector")
		}
		var decoded []any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
			return nil, err
		}
		return vectorNumbers(decoded)
	case []byte:
		var decoded []any
		if err := json.Unmarshal(value, &decoded); err != nil {
			return nil, err
		}
		return vectorNumbers(decoded)
	case []any:
		numbers := make([]float64, 0, len(value))
		for _, element := range value {
			number, ok := element.(float64)
			if !ok {
				return nil, fmt.Errorf("not a vector of numbers")
			}
			numbers = append(numbers, number)
		}
		return numbers, nil
	case []float64:
		return value, nil
	default:
		return nil, fmt.Errorf("not a vector of numbers")
	}
}

// vectorStrings reads a JSON array of strings, encoded or decoded like
// vectorNumbers.
func vectorStrings(raw any) ([]string, error) {
	switch value := raw.(type) {
	case nil:
		return nil, fmt.Errorf("not a string array")
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil, fmt.Errorf("not a string array")
		}
		var decoded []any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
			return nil, err
		}
		return vectorStrings(decoded)
	case []byte:
		var decoded []any
		if err := json.Unmarshal(value, &decoded); err != nil {
			return nil, err
		}
		return vectorStrings(decoded)
	case []any:
		values := make([]string, 0, len(value))
		for _, element := range value {
			text, ok := element.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("not a string array")
			}
			values = append(values, text)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("not a string array")
	}
}

// vectorObject reads a JSON object, encoded or decoded. Absent reads as no
// filter rather than as an error.
func vectorObject(raw any) (map[string]any, error) {
	switch value := raw.(type) {
	case nil:
		return nil, nil
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil, nil
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
			return nil, err
		}
		return decoded, nil
	case []byte:
		trimmed := bytes.TrimSpace(value)
		if len(trimmed) == 0 {
			return nil, nil
		}
		var decoded map[string]any
		if err := json.Unmarshal(trimmed, &decoded); err != nil {
			return nil, err
		}
		return decoded, nil
	case map[string]any:
		return value, nil
	default:
		return nil, fmt.Errorf("not a JSON object")
	}
}

// RegisterVectorNodes installs the embeddings and vector store definitions
// with the install's availability verdict. Kept beside RegisterAll rather
// than inside it because the verdict is a deployment fact main computes
// from its database handle, not a constant the catalogue can state.
func RegisterVectorNodes(registry *node.Registry, unavailable string) error {
	return registry.Register(VectorStoreNode(unavailable))
}
