package nodes_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// availableVectorStub is the VectorAvailability a test wires in when the
// behaviour under test is the HTTP call, not the tier gate.
type availableVectorStub struct{}

func (availableVectorStub) CheckAvailable(_ context.Context) error { return nil }

func vectorRequest(nodeType string, parameters map[string]any, credentials map[string]string) workflow.IRNode {
	return workflow.IRNode{
		ID: "vec-1", Name: "Vector", Type: nodeType, TypeVersion: workflow.V(1),
		Parameters: parameters, Credentials: credentials,
	}
}

func vectorTenantRequest(resolver engine.CredentialResolver) engine.Request {
	return engine.Request{
		Credentials: resolver,
		Execution:   engine.ExecutionContext{TenantID: "tenant-vector"},
	}
}

func TestVectorNodesRefuseWithoutTheTier(t *testing.T) {
	t.Parallel()

	reason := nodes.VectorUnavailableReason("sqlite")
	if !strings.Contains(reason, "pgvector") {
		t.Fatalf("sqlite reason = %q, want the pgvector install message", reason)
	}
	if got := nodes.VectorUnavailableReason("postgres"); got != "" {
		t.Fatalf("postgres reason = %q, want empty", got)
	}

	// The definitions are built the way the server builds them: with the
	// tier's answer for this install. Validate is pure, so it is asked
	// directly rather than through a registration the wiring owns.
	embeddings := nodes.EmbeddingsNode(reason)
	if err := embeddings.Validate(workflow.Node{Parameters: map[string]any{"model": "text-embedding-3-small"}, Credentials: map[string]string{"openAiApi": "c"}}); err != nil {
		t.Fatalf("embeddings validate on sqlite = %v, want nil now that embeddings do not need pgvector", err)
	}
	store := nodes.VectorStoreNode(reason)
	if err := store.Validate(workflow.Node{Parameters: map[string]any{"operation": "search", "collection": "docs"}}); err == nil || !strings.Contains(err.Error(), "pgvector") {
		t.Fatalf("vector store validate on sqlite = %v, want the install message", err)
	}
}

func TestVectorStoreValidateChecksStaticConfiguration(t *testing.T) {
	t.Parallel()

	definition := nodes.VectorStoreNode("")
	cases := map[string]map[string]any{
		"unknown operation":  {"operation": "upsert", "collection": "docs"},
		"missing collection": {"operation": "search"},
		"unknown distance":   {"operation": "insert", "collection": "docs", "distance": "manhattan"},
		"unknown index type": {"operation": "insert", "collection": "docs", "indexType": "flat"},
	}
	for name, parameters := range cases {
		if err := definition.Validate(workflow.Node{Parameters: parameters}); err == nil {
			t.Errorf("%s: validate = nil, want an error", name)
		}
	}
	// Defaults apply: no operation names insert, so an explicit collection
	// with valid options passes.
	valid := []map[string]any{
		{"collection": "docs"},
		{"operation": "insert", "collection": "docs", "distance": "cosine", "indexType": "hnsw"},
		{"operation": "search", "collection": "docs", "distance": "l2", "indexType": "ivfflat"},
		{"operation": "delete", "collection": "docs", "distance": "ip"},
	}
	for index, parameters := range valid {
		if err := definition.Validate(workflow.Node{Parameters: parameters}); err != nil {
			t.Errorf("valid[%d]: validate = %v, want nil", index, err)
		}
	}
}

func TestEmbeddingsValidateRequiresAModel(t *testing.T) {
	t.Parallel()

	definition := nodes.EmbeddingsNode("")
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{}}); err == nil {
		t.Fatal("validate without a model = nil, want an error")
	}
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"model": "text-embedding-3-small"}}); err != nil {
		t.Fatalf("validate = %v, want nil (the credential is checked when the node embeds)", err)
	}
}

func TestDisabledVectorStoreFailsEveryOperationWithTheReason(t *testing.T) {
	t.Parallel()

	store := nodes.NewDisabledVectorStore("install pgvector first")
	ctx := context.Background()
	if err := store.CheckAvailable(ctx); err == nil || !strings.Contains(err.Error(), "pgvector") {
		t.Fatalf("CheckAvailable = %v, want the reason", err)
	}
	if _, err := store.EnsureCollection(ctx, "tenant", "docs", 384, "cosine", "hnsw"); err == nil {
		t.Fatal("EnsureCollection = nil, want the reason")
	}
	if err := store.Insert(ctx, "tenant", "docs", []nodes.VectorDocument{{Content: "hi"}}); err == nil {
		t.Fatal("Insert = nil, want the reason")
	}
	if _, err := store.Search(ctx, "tenant", "docs", []float64{0.1}, 4, nil); err == nil {
		t.Fatal("Search = nil, want the reason")
	}
	if _, err := store.Delete(ctx, "tenant", "docs", []string{"id"}); err == nil {
		t.Fatal("Delete = nil, want the reason")
	}

	executor := nodes.NewVectorStoreExecutor(store)
	ir := vectorRequest(nodes.VectorStoreNodeType, map[string]any{"operation": "search", "collection": "docs"}, nil)
	if _, err := executor.Execute(ctx, ir, workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, vectorTenantRequest(nil)); err == nil || !strings.Contains(err.Error(), "pgvector") {
		t.Fatalf("executor on sqlite = %v, want the install message", err)
	}
}

func TestEmbeddingsExecutorCallsTheProviderThroughThePolicy(t *testing.T) {
	t.Parallel()

	var gotAuth, gotModel string
	var gotInputs []any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		gotModel = body.Model
		for _, text := range body.Input {
			gotInputs = append(gotInputs, text)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"embedding":[0.1,0.2,0.3],"index":0},{"embedding":[0.4,0.5,0.6],"index":1}]}`)
	}))
	t.Cleanup(provider.Close)

	target, _ := url.Parse(provider.URL)
	policy := safehttp.DefaultPolicy()
	policy.AllowedPrivateEndpoints = []string{target.Host}
	executor := nodes.NewEmbeddingsExecutor(policy, availableVectorStub{})

	resolver := &stubCredentials{credential: engine.Credential{
		ID: "cred-emb", Name: "Embeddings key", Type: "openAiApi",
		Fields: map[string]string{"apiKey": "secret-key"}, AllowedDomains: []string{"127.0.0.1"},
	}}
	ir := vectorRequest(nodes.EmbeddingsNodeType, map[string]any{
		"model": "text-embedding-3-small", "baseUrl": provider.URL,
	}, map[string]string{"openAiApi": "cred-emb"})
	input := workflow.NodeInput{"main": {
		{JSON: map[string]any{"text": "first"}},
		{JSON: map[string]any{"text": "second", "keep": true}},
	}}
	output, err := executor.Execute(context.Background(), ir, input, vectorTenantRequest(resolver))
	if err != nil {
		t.Fatalf("Execute = %v", err)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("Authorization = %q, want the credential's key", gotAuth)
	}
	if gotModel != "text-embedding-3-small" {
		t.Errorf("model = %q, want text-embedding-3-small", gotModel)
	}
	if len(output) != 2 || len(output[0]) != 2 {
		t.Fatalf("Execute produced %d streams of %d items, want two streams and two main items", len(output), len(output[0]))
	}
	first := output[0][0].JSON["embedding"]
	embedding, ok := first.([]float64)
	if !ok || len(embedding) != 3 || embedding[0] != 0.1 {
		t.Errorf("first embedding = %#v, want the provider's vector", first)
	}
	if output[0][1].JSON["keep"] != true {
		t.Error("the second item lost the fields it arrived with")
	}
}

func TestEmbeddingsExecutorRefusesAPrivateProviderUnderTheDefaultPolicy(t *testing.T) {
	t.Parallel()

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":[]}`)
	}))
	t.Cleanup(provider.Close)

	// The default policy with no private-endpoint grant: the call must fail
	// as a policy refusal before a socket opens, exactly as a chat model
	// pointed at the same address would.
	executor := nodes.NewEmbeddingsExecutor(safehttp.DefaultPolicy(), availableVectorStub{})
	resolver := &stubCredentials{credential: engine.Credential{
		ID: "cred-emb", Name: "Embeddings key", Type: "openAiApi",
		Fields: map[string]string{"apiKey": "secret-key"}, AllowedDomains: []string{"127.0.0.1"},
	}}
	ir := vectorRequest(nodes.EmbeddingsNodeType, map[string]any{
		"model": "text-embedding-3-small", "baseUrl": provider.URL,
	}, map[string]string{"openAiApi": "cred-emb"})
	input := workflow.NodeInput{"main": {{JSON: map[string]any{"text": "hello"}}}}
	if _, err := executor.Execute(context.Background(), ir, input, vectorTenantRequest(resolver)); err == nil {
		t.Fatal("Execute against a private address under the default policy = nil, want a refusal")
	}
}

// openVectorPostgres migrates a throwaway database on the gated server and
// hands back the store bound to its prefix. A fresh database rather than the
// shared one: the migration runner refuses to create a second prefixed schema
// beside bare tables, so reusing the shared database would either fail or
// disturb the packages that own it.
func openVectorPostgres(t *testing.T) (*nodes.PostgresVectorStore, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("KILASFLOW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set KILASFLOW_TEST_POSTGRES_DSN to run the pgvector integration coverage")
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("%s is not a URL: %v", os.Getenv("KILASFLOW_TEST_POSTGRES_DSN"), err)
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	defer admin.Close()
	var available bool
	if err := admin.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'vector')`).Scan(&available); err != nil {
		t.Fatalf("probe pgvector availability: %v", err)
	}
	if !available {
		t.Skip("this PostgreSQL was built without the pgvector extension; install pgvector (for example the pgvector/pgvector:pg17 image) to run this coverage")
	}
	ctx := context.Background()
	if _, err := admin.ExecContext(ctx, `DROP DATABASE IF EXISTS kf_vector_test`); err != nil {
		t.Fatalf("drop throwaway database: %v", err)
	}
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE kf_vector_test`); err != nil {
		t.Fatalf("create throwaway database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), `DROP DATABASE IF EXISTS kf_vector_test`)
	})
	parsed.Path = "/kf_vector_test"
	handle, err := database.Open(ctx, config.Database{Driver: "postgres", DSN: parsed.String(), TablePrefix: "kvtest_"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("open throwaway database: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	raw, err := handle.DB.DB()
	if err != nil {
		t.Fatalf("unwrap sql.DB: %v", err)
	}
	// The extension is per-database, not per-server. The probe above asks
	// pg_available_extensions, which only says the server carries pgvector's
	// binaries; the migration runner gates 000006 on pg_extension, which it
	// asks of the database being migrated — and this one is a fresh clone of
	// template1, so it has none. Without this the migration is recorded as
	// skipped and every table it owns is missing.
	//
	// Installed here rather than in the migration on purpose: the migration
	// keeps failing loudly for a shared database whose owner has not run
	// CREATE EXTENSION vector, because CREATE EXTENSION needs a privilege a
	// shared-database role very often does not have. A test harness owns its
	// throwaway database and can grant itself the extension; an operator's
	// workflow cannot.
	if _, err := raw.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		t.Fatalf("install pgvector in the throwaway database: %v", err)
	}
	if err := database.Migrate(handle, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("migrate throwaway database: %v", err)
	}
	// The migration files are re-runnable, so migrating the same database a
	// second time is a no-op rather than 42P07 on the tables the first pass
	// created.
	if err := database.Migrate(handle, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("migrate throwaway database again: %v", err)
	}
	return nodes.NewPostgresVectorStore(raw, "kvtest_"), raw
}

func TestVectorStoreRoundTripOnPostgres(t *testing.T) {
	store, _ := openVectorPostgres(t)
	ctx := context.Background()
	tenant := "tenant-vector"

	collection, err := store.EnsureCollection(ctx, tenant, "handbook", 384, "cosine", "hnsw")
	if err != nil {
		t.Fatalf("EnsureCollection = %v", err)
	}
	if collection.Dimension != 384 || collection.Distance != "cosine" || collection.IndexType != "hnsw" {
		t.Fatalf("EnsureCollection = %+v, want the recorded shape", collection)
	}
	// Creating it again with the same shape is the same collection, not a
	// second one — and the concurrent-create race re-reads rather than
	// failing on the unique index both writers hit.
	again, err := store.EnsureCollection(ctx, tenant, "handbook", 384, "cosine", "hnsw")
	if err != nil {
		t.Fatalf("EnsureCollection again = %v", err)
	}
	if again.ID != collection.ID {
		t.Fatalf("EnsureCollection again returned %q, want %q", again.ID, collection.ID)
	}
	if _, err := store.EnsureCollection(ctx, tenant, "handbook", 768, "cosine", "hnsw"); err == nil ||
		!strings.Contains(err.Error(), "768-dimensional") || !strings.Contains(err.Error(), "384-dimensional") {
		t.Fatalf("EnsureCollection with a new dimension = %v, want both numbers named", err)
	}

	handbook := unitVector(384, 0)
	other := unitVector(384, 1)
	documents := []nodes.VectorDocument{
		{ID: "doc-handbook", Content: "the handbook", Embedding: handbook, Metadata: map[string]any{"source": "handbook"}},
		{ID: "doc-other", Content: "something else", Embedding: other, Metadata: map[string]any{"source": "other"}},
	}
	if err := store.Insert(ctx, tenant, "handbook", documents); err != nil {
		t.Fatalf("Insert = %v", err)
	}
	if err := store.Insert(ctx, tenant, "handbook", []nodes.VectorDocument{
		{ID: "doc-bad", Content: "wrong width", Embedding: unitVector(768, 0)},
	}); err == nil || !strings.Contains(err.Error(), "768") || !strings.Contains(err.Error(), "384") {
		t.Fatalf("Insert of the wrong width = %v, want both dimensions named", err)
	}

	matches, err := store.Search(ctx, tenant, "handbook", handbook, 4, nil)
	if err != nil {
		t.Fatalf("Search = %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("Search returned %d matches, want 2", len(matches))
	}
	if matches[0].ID != "doc-handbook" {
		t.Fatalf("nearest match = %q, want doc-handbook", matches[0].ID)
	}
	filtered, err := store.Search(ctx, tenant, "handbook", handbook, 4, map[string]any{"source": "other"})
	if err != nil {
		t.Fatalf("filtered Search = %v", err)
	}
	if len(filtered) != 1 || filtered[0].ID != "doc-other" {
		t.Fatalf("filtered Search = %+v, want only doc-other", filtered)
	}
	if _, err := store.Search(ctx, tenant, "handbook", unitVector(768, 0), 4, nil); err == nil ||
		!strings.Contains(err.Error(), "768") || !strings.Contains(err.Error(), "384") {
		t.Fatalf("Search with the wrong width = %v, want both dimensions named", err)
	}

	deleted, err := store.Delete(ctx, tenant, "handbook", []string{"doc-other"})
	if err != nil {
		t.Fatalf("Delete = %v", err)
	}
	if deleted != 1 {
		t.Fatalf("Delete removed %d rows, want 1", deleted)
	}
	remaining, err := store.Search(ctx, tenant, "handbook", handbook, 4, nil)
	if err != nil {
		t.Fatalf("Search after delete = %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != "doc-handbook" {
		t.Fatalf("Search after delete = %+v, want only doc-handbook", remaining)
	}
}

func TestVectorSearchUsesTheANNIndex(t *testing.T) {
	store, raw := openVectorPostgres(t)
	ctx := context.Background()
	tenant := "tenant-vector"

	if _, err := store.EnsureCollection(ctx, tenant, "indexed", 384, "cosine", "hnsw"); err != nil {
		t.Fatalf("EnsureCollection = %v", err)
	}
	// A populated collection, not a toy one: with twenty rows the planner
	// serves the query from the tenant btree plus a sort, which proves
	// nothing about the ANN index. Thousands of rows make the sort cost more
	// than the HNSW scan, so the captured plan shows the index the ticket
	// promises.
	documents := make([]nodes.VectorDocument, 0, 5000)
	for index := range 5000 {
		documents = append(documents, nodes.VectorDocument{
			ID: fmt.Sprintf("doc-%04d", index), Content: fmt.Sprintf("document %d", index),
			Embedding: unitVector(384, index),
		})
	}
	if err := store.Insert(ctx, tenant, "indexed", documents); err != nil {
		t.Fatalf("Insert = %v", err)
	}
	// The planner needs statistics to price the ANN scan against the tenant
	// btree plus a sort: with no ANALYZE it estimates one matching row from
	// the lookup index and serves the query from there, which is what the
	// plan asserted below would then see. Autovacuum would get there
	// eventually; a test cannot wait for it.
	if _, err := raw.ExecContext(ctx, `ANALYZE `+`kvtest_vector_documents_384`); err != nil {
		t.Fatalf("analyze the vector table: %v", err)
	}
	plan, err := store.ExplainSearch(ctx, tenant, "indexed", unitVector(384, 0), 4)
	if err != nil {
		t.Fatalf("ExplainSearch = %v", err)
	}
	joined := strings.Join(plan, "\n")
	// Either ANN index built for the collection's distance counts: the
	// planner owns the choice between the HNSW and IVFFlat indexes the
	// migration created, and both serve the cosine query. The tenant btree
	// (idx_vdocs384_lookup) does not count, which is why the names are
	// matched rather than any index scan.
	if !strings.Contains(joined, "hnsw_cos") && !strings.Contains(joined, "ivf_cos") {
		t.Fatalf("query plan uses no cosine ANN index:\n%s", joined)
	}
	t.Logf("similarity query plan:\n%s", joined)
}

func TestVectorMigrationRerunsOnSQLite(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handle, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite", DSN: t.TempDir() + "/kilasflow.db",
	}, logger)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	// Re-runnable like the PostgreSQL half: the second Migrate is a no-op
	// rather than an already-exists failure on the catalogue table.
	for pass := range 2 {
		if err := database.Migrate(handle, logger); err != nil {
			t.Fatalf("migrate sqlite (pass %d): %v", pass+1, err)
		}
	}
	if !handle.Migrator().HasTable("vector_collections") {
		t.Fatal("the sqlite migration did not create vector_collections")
	}
}

func TestVectorStoreExecutorOnPostgres(t *testing.T) {
	store, _ := openVectorPostgres(t)
	ctx := context.Background()
	executor := nodes.NewVectorStoreExecutor(store)

	embedding := make([]any, 0, 384)
	for _, value := range unitVector(384, 0) {
		embedding = append(embedding, value)
	}
	insert := vectorRequest(nodes.VectorStoreNodeType, map[string]any{
		"operation": "insert", "collection": "executor",
		"distance": "cosine", "indexType": "hnsw",
	}, nil)
	inserted, err := executor.Execute(ctx, insert, workflow.NodeInput{"main": {
		{JSON: map[string]any{"text": "executor handbook", "embedding": embedding, "metadata": map[string]any{"source": "handbook"}}},
	}}, vectorTenantRequest(nil))
	if err != nil {
		t.Fatalf("insert Execute = %v", err)
	}
	if len(inserted) != 1 || len(inserted[0]) != 1 {
		t.Fatalf("insert Execute produced %+v, want one item", inserted)
	}
	search := vectorRequest(nodes.VectorStoreNodeType, map[string]any{
		"operation": "search", "collection": "executor",
		"queryVector": embedding, "topK": float64(4),
		"metadataFilter": map[string]any{"source": "handbook"},
	}, nil)
	found, err := executor.Execute(ctx, search, workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, vectorTenantRequest(nil))
	if err != nil {
		t.Fatalf("search Execute = %v", err)
	}
	matches, _ := found[0][0].JSON["matches"].([]any)
	if len(matches) != 1 {
		t.Fatalf("search Execute matches = %+v, want one", found[0][0].JSON["matches"])
	}
	remove := vectorRequest(nodes.VectorStoreNodeType, map[string]any{
		"operation": "delete", "collection": "executor",
		"ids": []any{inserted[0][0].JSON["id"]},
	}, nil)
	deleted, err := executor.Execute(ctx, remove, workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, vectorTenantRequest(nil))
	if err != nil {
		t.Fatalf("delete Execute = %v", err)
	}
	if deleted[0][0].JSON["deleted"] != int64(1) {
		t.Fatalf("delete Execute = %+v, want one row removed", deleted[0][0].JSON)
	}
}

// unitVector builds a deterministic pseudo-embedding: a unit pulse at one
// position plus a shared floor, so cosine ordering between seeds is stable
// without a provider in the loop.
func unitVector(dimension, seed int) []float64 {
	vector := make([]float64, dimension)
	for index := range vector {
		vector[index] = 0.01
	}
	vector[seed%dimension] = 1
	return vector
}
