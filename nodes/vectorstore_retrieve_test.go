package nodes_test

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// retrieveStoreDefinitions are the two vector-store definitions that share
// one port rule, built the way the server builds them.
func retrieveStoreDefinitions(t *testing.T) map[string]node.Definition {
	t.Helper()
	registry := aiRegistry(t)
	pgvector, found := registry.Get(nodes.VectorStorePGVectorNodeType, workflow.V(1))
	if !found {
		t.Fatal("the PGVector store is not registered")
	}
	return map[string]node.Definition{
		nodes.VectorStoreNodeType:         nodes.VectorStoreNode(""),
		nodes.VectorStorePGVectorNodeType: pgvector,
	}
}

func TestRetrieveModeDeclaresAnEmbeddingInputAndAVectorStoreOutput(t *testing.T) {
	t.Parallel()

	for nodeType, definition := range retrieveStoreDefinitions(t) {
		t.Run(nodeType, func(t *testing.T) {
			t.Parallel()

			inputs, outputs := definition.PortsFor(map[string]any{"mode": "retrieve"}, workflow.V(1))
			wantInputs := []workflow.Port{{Name: "embedding", Kind: workflow.ConnectionEmbedding, Required: true, MaxConnections: 1}}
			wantOutputs := []workflow.Port{{Name: "vectorStore", Kind: workflow.ConnectionVectorStore}}
			if !reflect.DeepEqual(inputs, wantInputs) {
				t.Errorf("retrieve inputs = %+v, want %+v: n8n's retrieve store takes only its embeddings, never items", inputs, wantInputs)
			}
			if !reflect.DeepEqual(outputs, wantOutputs) {
				t.Errorf("retrieve outputs = %+v, want %+v", outputs, wantOutputs)
			}

			// load is n8n's Get Many and keeps the Get Many ports.
			loadInputs, loadOutputs := definition.PortsFor(map[string]any{"mode": "load"}, workflow.V(1))
			getManyInputs, getManyOutputs := definition.PortsFor(map[string]any{"mode": "getMany"}, workflow.V(1))
			if !reflect.DeepEqual(loadInputs, getManyInputs) || !reflect.DeepEqual(loadOutputs, getManyOutputs) {
				t.Errorf("load ports = %+v -> %+v, want the Get Many ports %+v -> %+v", loadInputs, loadOutputs, getManyInputs, getManyOutputs)
			}

			// Stored native graphs: `operation` is not `mode`. An operation
			// spelled retrieve kept meaning a search before the mode existed,
			// and it still declares the search's main ports, never the
			// retrieve ones.
			main := []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}
			for _, operation := range []string{"retrieve", "search"} {
				inputs, outputs := definition.PortsFor(map[string]any{"operation": operation}, workflow.V(1))
				if !reflect.DeepEqual(inputs, main) || !reflect.DeepEqual(outputs, main) {
					t.Errorf("operation %q ports = %+v -> %+v, want main -> main", operation, inputs, outputs)
				}
			}

			toolInputs, toolOutputs := definition.PortsFor(map[string]any{"mode": "retrieve-as-tool"}, workflow.V(1))
			if len(toolInputs) != 1 || toolInputs[0].Kind != workflow.ConnectionEmbedding {
				t.Errorf("retrieve-as-tool inputs = %+v, want the embedding port alone", toolInputs)
			}
			if len(toolOutputs) != 1 || toolOutputs[0].Kind != workflow.ConnectionTool {
				t.Errorf("retrieve-as-tool outputs = %+v, want the tool port alone", toolOutputs)
			}
		})
	}
}

func TestRetrieveModeIsOfferedInTheModeOptions(t *testing.T) {
	t.Parallel()

	for nodeType, definition := range retrieveStoreDefinitions(t) {
		offered := false
		for _, parameter := range definition.Parameters {
			if parameter.Key != "mode" {
				continue
			}
			for _, option := range parameter.Options {
				if option.Value == nodes.VectorModeRetrieve {
					offered = true
				}
			}
		}
		if !offered {
			t.Errorf("%s does not offer mode %q, so the editor cannot select it", nodeType, nodes.VectorModeRetrieve)
		}
	}
}

func TestVectorStoreValidateAcceptsRetrieveMode(t *testing.T) {
	t.Parallel()

	if err := nodes.VectorStoreNode("").Validate(workflow.Node{Parameters: map[string]any{"mode": "retrieve", "collection": "docs"}}); err != nil {
		t.Fatalf("Validate(retrieve) = %v, want nil", err)
	}
	// The tier gate still applies: retrieving from the internal store needs
	// pgvector exactly as inserting into it does.
	reason := nodes.VectorUnavailableReason("sqlite")
	if err := nodes.VectorStoreNode(reason).Validate(workflow.Node{Parameters: map[string]any{"mode": "retrieve", "collection": "docs"}}); err == nil || !strings.Contains(err.Error(), "pgvector") {
		t.Fatalf("Validate(retrieve) on sqlite = %v, want the install message", err)
	}
}

func TestInternalVectorStoreRetrieveEmitsAStoreDescriptor(t *testing.T) {
	t.Parallel()

	store := &recordingVectorStore{}
	executor := nodes.NewVectorStoreExecutor(store)
	ir := vectorRequest(nodes.VectorStoreNodeType, map[string]any{
		"mode": "retrieve", "collection": "handbook", "distance": "l2",
		"metadataFilter": map[string]any{"source": "hr"},
	}, nil)
	embeddings := map[string]any{"kind": "embeddings", "model": "text-embedding-3-small", "credentialId": "cred-emb"}
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{
		"embedding": {{JSON: map[string]any{"$ai": embeddings}}},
	}, vectorTenantRequest(nil))
	if err != nil {
		t.Fatalf("Execute = %v", err)
	}
	if len(output) != 1 || len(output[0]) != 1 {
		t.Fatalf("output = %#v, want one descriptor on the vectorStore port", output)
	}
	descriptor, _ := output[0][0].JSON["$ai"].(map[string]any)
	want := map[string]any{
		"kind": "vectorStoreSource", "nodeName": "Vector", "backend": "internal",
		"collection": "handbook", "distance": "l2",
		"metadataFilter": map[string]any{"source": "hr"},
		"embeddings":     embeddings,
	}
	if !reflect.DeepEqual(descriptor, want) {
		t.Fatalf("descriptor = %#v, want %#v", descriptor, want)
	}
	if len(store.inserted) != 0 {
		t.Fatalf("retrieve inserted %d rows; it only describes the store", len(store.inserted))
	}
}

func TestPGVectorRetrieveEmitsAStoreDescriptorWithoutOpeningTheDatabase(t *testing.T) {
	t.Parallel()

	// No credential resolver at all: any attempt to open the customer's
	// database would fail, so a descriptor coming back proves none was made.
	executor := nodes.NewVectorStorePGVectorExecutor(nil, sqlnode.Guard{})
	embeddings := map[string]any{"kind": "embeddings", "model": "text-embedding-3-small", "credentialId": "cred-emb"}
	output, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "vs", Name: "Postgres PGVector Store", Type: nodes.VectorStorePGVectorNodeType, TypeVersion: workflow.V(1),
		Parameters:  map[string]any{"mode": "retrieve", "tableName": "n8n_vectors", "contentColumn": "body"},
		Credentials: map[string]string{"postgres": "cred-pg"},
	}, workflow.NodeInput{
		"embedding": {{JSON: map[string]any{"$ai": embeddings}}},
	}, engine.Request{Execution: engine.ExecutionContext{TenantID: "tenant-vector"}})
	if err != nil {
		t.Fatalf("Execute = %v", err)
	}
	if len(output) != 1 || len(output[0]) != 1 {
		t.Fatalf("output = %#v, want one descriptor", output)
	}
	descriptor, _ := output[0][0].JSON["$ai"].(map[string]any)
	want := map[string]any{
		"kind": "vectorStoreSource", "nodeName": "Postgres PGVector Store", "backend": "postgres",
		"collection": "", "tableName": "n8n_vectors",
		"idColumn": "id", "contentColumn": "body", "metadataColumn": "metadata", "embeddingColumn": "embedding",
		"credentialId": "cred-pg",
		"embeddings":   embeddings,
	}
	if !reflect.DeepEqual(descriptor, want) {
		t.Fatalf("descriptor = %#v, want %#v", descriptor, want)
	}
}

func TestRetrieveModeRefusesAStoreWithNoEmbeddings(t *testing.T) {
	t.Parallel()

	_, err := nodes.NewVectorStoreExecutor(&recordingVectorStore{}).Execute(context.Background(),
		vectorRequest(nodes.VectorStoreNodeType, map[string]any{"mode": "retrieve", "collection": "docs"}, nil),
		workflow.NodeInput{}, vectorTenantRequest(nil))
	if err == nil || !strings.Contains(err.Error(), "Embeddings") {
		t.Fatalf("Execute(internal, no embeddings) = %v, want the missing sub-node named", err)
	}
	_, err = nodes.NewVectorStorePGVectorExecutor(nil, sqlnode.Guard{}).Execute(context.Background(), workflow.IRNode{
		ID: "vs", Name: "PGVector", Type: nodes.VectorStorePGVectorNodeType, TypeVersion: workflow.V(1),
		Parameters:  map[string]any{"mode": "retrieve", "tableName": "documents"},
		Credentials: map[string]string{"postgres": "cred-pg"},
	}, workflow.NodeInput{}, engine.Request{})
	if err == nil || !strings.Contains(err.Error(), "Embeddings") {
		t.Fatalf("Execute(postgres, no embeddings) = %v, want the missing sub-node named", err)
	}
}

// TestRetrieveAsToolDescriptorsKeepTheirFields pins the retrieve-as-tool
// descriptors field by field, so sharing their store fields with the retrieve
// descriptor changes nothing an agent reads.
func TestRetrieveAsToolDescriptorsKeepTheirFields(t *testing.T) {
	t.Parallel()

	embeddings := map[string]any{"kind": "embeddings", "credentialId": "cred-emb"}
	input := workflow.NodeInput{"embedding": {{JSON: map[string]any{"$ai": embeddings}}}}

	internal, err := nodes.NewVectorStoreExecutor(&recordingVectorStore{}).Execute(context.Background(),
		vectorRequest(nodes.VectorStoreNodeType, map[string]any{
			"mode": "retrieve-as-tool", "collection": "docs", "toolName": "search_docs",
			"toolDescription": "Find handbook passages.", "topK": 7,
			"metadataFilter": map[string]any{"source": "hr"},
		}, nil), input, vectorTenantRequest(nil))
	if err != nil {
		t.Fatalf("internal Execute = %v", err)
	}
	if got := vectorDescriptorKeys(internal); got != "backend,collection,description,distance,embeddings,kind,metadataFilter,name,nodeName,topK" {
		t.Errorf("internal retrieve-as-tool keys = %s", got)
	}

	customer, err := nodes.NewVectorStorePGVectorExecutor(nil, sqlnode.Guard{}).Execute(context.Background(), workflow.IRNode{
		ID: "vs", Name: "PGVector", Type: nodes.VectorStorePGVectorNodeType, TypeVersion: workflow.V(1),
		Parameters:  map[string]any{"mode": "retrieve-as-tool", "tableName": "documents", "toolDescription": "Find rows."},
		Credentials: map[string]string{"postgres": "cred-pg"},
	}, input, engine.Request{})
	if err != nil {
		t.Fatalf("customer Execute = %v", err)
	}
	if got := vectorDescriptorKeys(customer); got != "backend,collection,contentColumn,credentialId,description,embeddingColumn,embeddings,idColumn,kind,metadataColumn,name,nodeName,tableName,topK" {
		t.Errorf("customer retrieve-as-tool keys = %s", got)
	}
	descriptor, _ := customer[0][0].JSON["$ai"].(map[string]any)
	if descriptor["kind"] != "vectorStore" || descriptor["name"] != "PGVector" || descriptor["topK"] != 4 {
		t.Errorf("customer retrieve-as-tool descriptor = %#v", descriptor)
	}
}

func vectorDescriptorKeys(output workflow.NodeOutput) string {
	if len(output) != 1 || len(output[0]) != 1 {
		return "<no single descriptor>"
	}
	descriptor, _ := output[0][0].JSON["$ai"].(map[string]any)
	keys := make([]string, 0, len(descriptor))
	for key := range descriptor {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}
