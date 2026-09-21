package nodes_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func TestSplitRecursiveChunksByParagraphThenLine(t *testing.T) {
	t.Parallel()

	chunks := nodes.SplitRecursiveForTest("alpha\n\nbeta gamma delta epsilon", 12, 0)
	if len(chunks) < 2 {
		t.Fatalf("chunks = %v, want at least two", chunks)
	}
	joined := strings.Join(chunks, " ")
	if !strings.Contains(joined, "alpha") || !strings.Contains(joined, "epsilon") {
		t.Fatalf("chunks lost text: %v", chunks)
	}
}

func TestDocumentLoaderEmitsPageContentAndHonoursSplitter(t *testing.T) {
	t.Parallel()

	loader := engine.ExecutorFunc(nodes.ExecuteDocumentLoaderForTest)
	splitterOut, err := nodes.ExecuteTextSplitterForTest(context.Background(), workflow.IRNode{
		ID: "split", Name: "Split", Type: nodes.TextSplitterNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"chunkSize": 8, "chunkOverlap": 0},
	}, nil, engine.Request{})
	if err != nil {
		t.Fatalf("splitter: %v", err)
	}
	output, err := loader.Execute(context.Background(), workflow.IRNode{
		ID: "load", Name: "Loader", Type: nodes.DocumentLoaderNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"textField": "text"},
	}, workflow.NodeInput{
		"main":     {{JSON: map[string]any{"text": "abcdefghijklmnop", "metadata": map[string]any{"source": "n"}}}},
		"splitter": splitterOut[0],
	}, engine.Request{})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	if len(output) != 1 || len(output[0]) < 2 {
		t.Fatalf("documents = %d streams / %d items, want several chunks", len(output), len(output[0]))
	}
	if output[0][0].JSON["pageContent"] == nil {
		t.Fatal("document is missing pageContent")
	}
}

func TestDocumentLoaderCopiesDriveIdentityIntoMetadata(t *testing.T) {
	t.Parallel()

	loader := engine.ExecutorFunc(nodes.ExecuteDocumentLoaderForTest)
	output, err := loader.Execute(context.Background(), workflow.IRNode{
		ID: "load", Name: "Loader", Type: nodes.DocumentLoaderNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"textField": "text"},
	}, workflow.NodeInput{
		"main": {{JSON: map[string]any{
			"text": "handbook body", "id": "file-1", "name": "manual.pdf",
			"parents": []any{"folder-1"},
		}}},
	}, engine.Request{})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	if len(output) != 1 || len(output[0]) != 1 {
		t.Fatalf("documents = %d streams / %d items", len(output), len(output[0]))
	}
	metadata, _ := output[0][0].JSON["metadata"].(map[string]any)
	if metadata["id"] != "file-1" || metadata["name"] != "manual.pdf" {
		t.Fatalf("metadata = %#v, want Drive identity copied in", metadata)
	}
}

func TestEmbeddingsClusterModeEmitsDescriptorWithoutCallingTheProvider(t *testing.T) {
	t.Parallel()

	called := false
	provider := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	t.Cleanup(provider.Close)

	executor := nodes.NewEmbeddingsExecutor(safehttp.DefaultPolicy(), availableVectorStub{})
	ir := vectorRequest(nodes.EmbeddingsNodeType, map[string]any{
		"mode": nodes.EmbeddingsModeCluster, "model": "text-embedding-3-small", "baseUrl": provider.URL,
	}, map[string]string{"openAiApi": "cred-emb"})
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{}, vectorTenantRequest(nil))
	if err != nil {
		t.Fatalf("Execute = %v", err)
	}
	if called {
		t.Fatal("cluster embeddings called the provider with no texts")
	}
	if len(output) != 1 || len(output[0]) != 1 {
		t.Fatalf("output arity = %d / %d", len(output), len(output[0]))
	}
	descriptor, _ := output[0][0].JSON["$ai"].(map[string]any)
	if descriptor["kind"] != "embeddings" || descriptor["credentialId"] != "cred-emb" {
		t.Fatalf("descriptor = %#v", descriptor)
	}
}

func TestVectorStoreRetrieveAsToolEmitsAToolDescriptor(t *testing.T) {
	t.Parallel()

	store := memoryVectorStore{}
	executor := nodes.NewVectorStoreExecutor(store)
	ir := vectorRequest(nodes.VectorStoreNodeType, map[string]any{
		"mode": "retrieve-as-tool", "collection": "docs", "toolName": "search_docs",
		"toolDescription": "Find handbook passages.",
	}, nil)
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{
		"embedding": {{JSON: map[string]any{"$ai": map[string]any{"kind": "embeddings", "model": "text-embedding-3-small", "credentialId": "cred-emb"}}}},
	}, vectorTenantRequest(nil))
	if err != nil {
		t.Fatalf("Execute = %v", err)
	}
	if len(output) != 1 || len(output[0]) != 1 {
		t.Fatalf("output = %#v", output)
	}
	descriptor, _ := output[0][0].JSON["$ai"].(map[string]any)
	if descriptor["kind"] != "vectorStore" || descriptor["name"] != "search_docs" {
		t.Fatalf("descriptor = %#v", descriptor)
	}
}

func TestVectorStoreClusterInsertEmbedsDocuments(t *testing.T) {
	t.Parallel()

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"embedding":[0.1,0.2,0.3,0.4],"index":0}]}`)
	}))
	t.Cleanup(provider.Close)
	target, _ := url.Parse(provider.URL)
	policy := safehttp.DefaultPolicy()
	policy.AllowedPrivateEndpoints = []string{target.Host}

	store := &recordingVectorStore{}
	embedder := nodes.NewEmbeddingsExecutor(policy, availableVectorStub{})
	executor := nodes.NewVectorStoreExecutor(store).WithEmbedder(embedder)
	ir := vectorRequest(nodes.VectorStoreNodeType, map[string]any{
		"mode": "insert", "collection": "docs",
	}, nil)
	resolver := &stubCredentials{credential: engine.Credential{
		ID: "cred-emb", Name: "Embeddings key", Type: "openAiApi",
		Fields: map[string]string{"apiKey": "secret-key"},
	}}
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{
		"document": {{JSON: map[string]any{
			"pageContent": "hello world",
			"metadata":    map[string]any{"id": "file-1", "name": "manual.pdf", "parents": []any{"folder-1"}, "source": "n"},
		}}},
		"embedding": {{JSON: map[string]any{"$ai": map[string]any{
			"kind": "embeddings", "model": "text-embedding-3-small",
			"baseUrl": provider.URL, "credentialId": "cred-emb",
		}}}},
	}, vectorTenantRequest(resolver))
	if err != nil {
		t.Fatalf("Execute = %v", err)
	}
	if len(store.inserted) != 1 || store.inserted[0].Content != "hello world" {
		t.Fatalf("inserted = %#v", store.inserted)
	}
	if store.inserted[0].ID == "file-1" {
		t.Fatal("vector primary key reused the Drive file id")
	}
	item := output[0][0].JSON
	if item["id"] != "file-1" || item["collection"] != "docs" {
		t.Fatalf("output = %#v, want Drive id on the item", item)
	}
	if item["vectorId"] != store.inserted[0].ID {
		t.Fatalf("vectorId = %#v, want %q", item["vectorId"], store.inserted[0].ID)
	}
	if item["name"] != "manual.pdf" {
		t.Fatalf("name = %#v", item["name"])
	}
}

func TestVectorStoreInsertEmitsOneItemPerDriveFile(t *testing.T) {
	t.Parallel()

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"embedding":[0.1,0.2,0.3,0.4],"index":0},{"embedding":[0.2,0.3,0.4,0.5],"index":1}]}`)
	}))
	t.Cleanup(provider.Close)
	target, _ := url.Parse(provider.URL)
	policy := safehttp.DefaultPolicy()
	policy.AllowedPrivateEndpoints = []string{target.Host}

	store := &recordingVectorStore{}
	embedder := nodes.NewEmbeddingsExecutor(policy, availableVectorStub{})
	executor := nodes.NewVectorStoreExecutor(store).WithEmbedder(embedder)
	ir := vectorRequest(nodes.VectorStoreNodeType, map[string]any{
		"mode": "insert", "collection": "docs",
	}, nil)
	resolver := &stubCredentials{credential: engine.Credential{
		ID: "cred-emb", Name: "Embeddings key", Type: "openAiApi",
		Fields: map[string]string{"apiKey": "secret-key"},
	}}
	driveMeta := map[string]any{"id": "file-1", "name": "manual.pdf", "parents": []any{"folder-1"}}
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{
		"document": {
			{JSON: map[string]any{"pageContent": "chunk one", "metadata": driveMeta}},
			{JSON: map[string]any{"pageContent": "chunk two", "metadata": driveMeta}},
		},
		"embedding": {{JSON: map[string]any{"$ai": map[string]any{
			"kind": "embeddings", "model": "text-embedding-3-small",
			"baseUrl": provider.URL, "credentialId": "cred-emb",
		}}}},
	}, vectorTenantRequest(resolver))
	if err != nil {
		t.Fatalf("Execute = %v", err)
	}
	if len(store.inserted) != 2 {
		t.Fatalf("inserted %d rows, want both chunks stored", len(store.inserted))
	}
	if len(output) != 1 || len(output[0]) != 1 {
		t.Fatalf("output arity = %d / %d, want one item so Drive Move runs once", len(output), len(output[0]))
	}
	if output[0][0].JSON["id"] != "file-1" {
		t.Fatalf("output = %#v", output[0][0].JSON)
	}
}

func TestAgentVectorStoreToolReturnsMatches(t *testing.T) {
	t.Parallel()

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"embedding":[1,0,0,0],"index":0}]}`)
	}))
	t.Cleanup(provider.Close)
	target, _ := url.Parse(provider.URL)
	policy := safehttp.DefaultPolicy()
	policy.AllowedPrivateEndpoints = []string{target.Host}

	store := &recordingVectorStore{matches: []nodes.VectorMatch{{
		ID: "doc-1", Content: "the answer", Metadata: map[string]any{"source": "n"}, Distance: 0.1,
	}}}
	embedder := nodes.NewEmbeddingsExecutor(policy, availableVectorStub{})
	agent := nodes.NewAgentExecutor(nil, policy, nil, nodes.WithAgentVectorStore(store, embedder))
	tool, err := nodes.VectorStoreToolFromForTest(agent, workflow.IRNode{Name: "Agent"}, map[string]any{
		"kind": "vectorStore", "name": "search_docs", "description": "Find docs.",
		"collection": "docs", "backend": "internal",
		"embeddings": map[string]any{"kind": "embeddings", "model": "text-embedding-3-small", "baseUrl": provider.URL, "credentialId": "cred-emb"},
	}, vectorTenantRequest(&stubCredentials{credential: engine.Credential{
		ID: "cred-emb", Name: "Embeddings key", Type: "openAiApi",
		Fields: map[string]string{"apiKey": "secret-key"},
	}}))
	if err != nil {
		t.Fatalf("toolFrom = %v", err)
	}
	result, err := tool.Invoke(context.Background(), json.RawMessage(`{"query":"hello"}`))
	if err != nil {
		t.Fatalf("Invoke = %v", err)
	}
	if !strings.Contains(result, "the answer") {
		t.Fatalf("result = %s", result)
	}
}

type memoryVectorStore struct{}

func (memoryVectorStore) CheckAvailable(context.Context) error { return nil }
func (memoryVectorStore) EnsureCollection(context.Context, string, string, int, string, string) (nodes.VectorCollection, error) {
	return nodes.VectorCollection{}, nil
}
func (memoryVectorStore) Insert(context.Context, string, string, []nodes.VectorDocument) error {
	return nil
}
func (memoryVectorStore) Search(context.Context, string, string, []float64, int, map[string]any) ([]nodes.VectorMatch, error) {
	return nil, nil
}
func (memoryVectorStore) Delete(context.Context, string, string, []string) (int64, error) {
	return 0, nil
}

type recordingVectorStore struct {
	inserted []nodes.VectorDocument
	matches  []nodes.VectorMatch
}

func (store *recordingVectorStore) CheckAvailable(context.Context) error { return nil }
func (store *recordingVectorStore) EnsureCollection(context.Context, string, string, int, string, string) (nodes.VectorCollection, error) {
	return nodes.VectorCollection{Dimension: 4}, nil
}
func (store *recordingVectorStore) Insert(_ context.Context, _, _ string, documents []nodes.VectorDocument) error {
	store.inserted = append(store.inserted, documents...)
	return nil
}
func (store *recordingVectorStore) Search(context.Context, string, string, []float64, int, map[string]any) ([]nodes.VectorMatch, error) {
	return store.matches, nil
}
func (store *recordingVectorStore) Delete(context.Context, string, string, []string) (int64, error) {
	return 0, nil
}
