package n8n_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// vectorStoreToolFixture is the retrieval half of n8n template 16706, in
// n8n's own shape: a PGVector store with no mode (n8n's default, retrieve)
// feeds a Vector Store Tool over ai_vectorStore, the tool has its own chat
// model, and the agent calls the tool. The tool's version and name are
// placeholders the tests fill in.
const vectorStoreToolFixture = `{
  "name": "Answer from policies",
  "nodes": [
    {"id": "m", "name": "Manual", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1, "position": [0, 0], "parameters": {}},
    {"id": "a", "name": "AI Agent", "type": "@n8n/n8n-nodes-langchain.agent", "typeVersion": 1.7, "position": [300, 0],
     "parameters": {"promptType": "define", "text": "How many leave days do I get?"}},
    {"id": "c", "name": "OpenAI Chat Model", "type": "@n8n/n8n-nodes-langchain.lmChatOpenAi", "typeVersion": 1.1, "position": [200, 200], "parameters": {"options": {}}},
    {"id": "t", "name": "Answer questions with a vector store", "type": "@n8n/n8n-nodes-langchain.toolVectorStore", "typeVersion": TOOL_VERSION, "position": [400, 200],
     "parameters": TOOL_PARAMETERS},
    {"id": "c1", "name": "OpenAI Chat Model1", "type": "@n8n/n8n-nodes-langchain.lmChatOpenAi", "typeVersion": 1.1, "position": [600, 400], "parameters": {"options": {}}},
    {"id": "s", "name": "Postgres PGVector Store", "type": "@n8n/n8n-nodes-langchain.vectorStorePGVector", "typeVersion": 1, "position": [300, 400], "parameters": {"options": {}}},
    {"id": "e", "name": "Embeddings OpenAI1", "type": "@n8n/n8n-nodes-langchain.embeddingsOpenAi", "typeVersion": 1.2, "position": [300, 600], "parameters": {"options": {}}}
  ],
  "connections": {
    "Manual": {"main": [[{"node": "AI Agent", "type": "main", "index": 0}]]},
    "OpenAI Chat Model": {"ai_languageModel": [[{"node": "AI Agent", "type": "ai_languageModel", "index": 0}]]},
    "Answer questions with a vector store": {"ai_tool": [[{"node": "AI Agent", "type": "ai_tool", "index": 0}]]},
    "OpenAI Chat Model1": {"ai_languageModel": [[{"node": "Answer questions with a vector store", "type": "ai_languageModel", "index": 0}]]},
    "Postgres PGVector Store": {"ai_vectorStore": [[{"node": "Answer questions with a vector store", "type": "ai_vectorStore", "index": 0}]]},
    "Embeddings OpenAI1": {"ai_embedding": [[{"node": "Postgres PGVector Store", "type": "ai_embedding", "index": 0}]]}
  }
}`

func vectorStoreToolWorkflow(version, parameters string) string {
	return strings.NewReplacer("TOOL_VERSION", version, "TOOL_PARAMETERS", parameters).Replace(vectorStoreToolFixture)
}

func TestPGVectorStoreWithNoModeImportsAsRetrieve(t *testing.T) {
	t.Parallel()

	result := importFixture(t, vectorStoreToolWorkflow("1", `{"name": "hr_employee_policies", "description": "data for HR"}`))
	store := nodeByName(result.Document, "Postgres PGVector Store")
	if store.Type != nodes.VectorStorePGVectorNodeType {
		t.Fatalf("store imported as %q", store.Type)
	}
	if store.Parameters["mode"] != "retrieve" {
		t.Errorf("mode = %#v, want retrieve: n8n reads a store saved with no mode as its default, retrieve", store.Parameters["mode"])
	}
	if store.Parameters["tableName"] != "n8n_vectors" {
		t.Errorf("tableName = %#v, want n8n's default n8n_vectors: the store reads the table n8n wrote to", store.Parameters["tableName"])
	}

	// A stored mode and table are carried as written.
	named := importFixture(t, strings.Replace(vectorStoreToolWorkflow("1", `{"description": "HR"}`),
		`"parameters": {"options": {}}},
    {"id": "e"`, `"parameters": {"mode": "retrieve-as-tool", "tableName": "documents", "options": {}}},
    {"id": "e"`, 1))
	store = nodeByName(named.Document, "Postgres PGVector Store")
	if store.Parameters["mode"] != "retrieve-as-tool" || store.Parameters["tableName"] != "documents" {
		t.Errorf("stored mode/table = %#v / %#v, want them unchanged", store.Parameters["mode"], store.Parameters["tableName"])
	}
}

func TestToolVectorStoreImportsOverARetrieveStore(t *testing.T) {
	t.Parallel()

	result := importFixture(t, vectorStoreToolWorkflow("1", `{"name": "hr_employee_policies", "description": "data for HR and employee policies"}`))
	tool := nodeByName(result.Document, "Answer questions with a vector store")
	if tool.Type != n8n.VectorStoreToolNodeType {
		t.Fatalf("tool imported as %q, want %s", tool.Type, n8n.VectorStoreToolNodeType)
	}
	want := map[string]any{"toolName": "hr_employee_policies", "toolDescription": "data for HR and employee policies", "topK": float64(4)}
	for key, value := range want {
		if tool.Parameters[key] != value {
			t.Errorf("%s = %#v, want %#v", key, tool.Parameters[key], value)
		}
	}
	for _, issue := range result.Unsupported {
		if strings.Contains(issue.Reason, "held back") {
			t.Errorf("an edge was held back: %s", issue.Reason)
		}
		if issue.Severity == n8n.SeverityBlocking {
			t.Errorf("blocking issue on %s: %s", issue.NodeName, issue.Reason)
		}
	}
	edges := "," + edgesOf(result.Document) + ","
	for _, edge := range []string{
		"Postgres PGVector Store.vectorStore->Answer questions with a vector store.vectorStore",
		"OpenAI Chat Model1.model->Answer questions with a vector store.model",
		"Embeddings OpenAI1.embedding->Postgres PGVector Store.embedding",
		"Answer questions with a vector store.tool->AI Agent.tools",
	} {
		if !strings.Contains(edges, ","+edge+",") {
			t.Errorf("edges = %s, want %s among them", edges, edge)
		}
	}

	document := result.Document
	document.ID = "wf_vector_tool"
	bindRAGCredentials(&document)
	if _, err := workflow.Compile(document, registry(t)); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
}

func TestToolVectorStoreNamesItsToolAfterTheNodeFromVersionOnePointOne(t *testing.T) {
	t.Parallel()

	// 1.1 dropped the name parameter; a stray one left in the JSON is not read.
	result := importFixture(t, vectorStoreToolWorkflow("1.1", `{"name": "ignored", "description": "HR", "topK": 6}`))
	tool := nodeByName(result.Document, "Answer questions with a vector store")
	if tool.Parameters["toolName"] != "Answer_questions_with_a_vector_store" || tool.Parameters["topK"] != float64(6) {
		t.Errorf("parameters = %#v, want the normalised node name and the stored topK", tool.Parameters)
	}

	// Version 1 with no name falls back to the node name too, and a missing
	// description gets one rather than leaving the tool unactivatable.
	bare := importFixture(t, vectorStoreToolWorkflow("1", `{}`))
	tool = nodeByName(bare.Document, "Answer questions with a vector store")
	if tool.Parameters["toolName"] != "Answer_questions_with_a_vector_store" {
		t.Errorf("toolName = %#v, want the normalised node name", tool.Parameters["toolName"])
	}
	if description, _ := tool.Parameters["toolDescription"].(string); strings.TrimSpace(description) == "" {
		t.Errorf("toolDescription = %#v, want a stand-in the tool can activate with", tool.Parameters["toolDescription"])
	}
}

func TestVectorStoreToolExportsAsToolVectorStoreOverARetrieveStore(t *testing.T) {
	t.Parallel()

	imported := importFixture(t, vectorStoreToolWorkflow("1.1", `{"description": "HR", "topK": 6}`))
	exported, err := n8n.Export(imported.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	byName := map[string]n8n.Node{}
	for _, exportedNode := range exported.Document.Nodes {
		byName[exportedNode.Name] = exportedNode
	}
	tool := byName["Answer questions with a vector store"]
	if tool.Type != "@n8n/n8n-nodes-langchain.toolVectorStore" || tool.TypeVersion != 1 {
		t.Fatalf("tool exported as %s@%v, want toolVectorStore at version 1", tool.Type, tool.TypeVersion)
	}
	if tool.Parameters["name"] != "Answer_questions_with_a_vector_store" || tool.Parameters["description"] != "HR" || tool.Parameters["topK"] != float64(6) {
		t.Errorf("tool parameters = %#v, want name, description and topK", tool.Parameters)
	}
	if store := byName["Postgres PGVector Store"]; store.Parameters["mode"] != "retrieve" {
		t.Errorf("store mode = %#v, want retrieve written out", store.Parameters["mode"])
	}
	for source, kind := range map[string]string{"Postgres PGVector Store": "ai_vectorStore", "OpenAI Chat Model1": "ai_languageModel"} {
		slots := exported.Document.Connections[source][kind]
		if len(slots) != 1 || len(slots[0]) != 1 || slots[0][0].Node != "Answer questions with a vector store" {
			t.Errorf("%s %s = %+v, want the tool", source, kind, slots)
		}
	}
}

// TestNativePGVectorStoresExportInAModeN8NReadsTheSameWay is the export half of
// the retrieve default. n8n reads a store with no mode as retrieve, so a store
// authored here with no mode — a native insert — must say insert on the way
// out, Get Many must use n8n's own value for it (load), and the table must be
// written, because n8n's default table is not this node's.
func TestNativePGVectorStoresExportInAModeN8NReadsTheSameWay(t *testing.T) {
	t.Parallel()

	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_native_stores",
		Name:          "Native stores",
		Nodes: []workflow.Node{
			{ID: "m", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "i", Name: "Insert Store", Type: nodes.VectorStorePGVectorNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"collection": "hr"}},
			{ID: "g", Name: "Search Store", Type: nodes.VectorStorePGVectorNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"mode": "getMany", "tableName": "policies", "query": "leave"}},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "m", Port: "main"}, Target: workflow.Endpoint{NodeID: "i", Port: "main"}},
		},
		Settings: map[string]any{},
	}
	exported, err := n8n.Export(document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	insert := exportedNode(t, exported.Document, "Insert Store")
	if insert.Parameters["mode"] != "insert" || insert.Parameters["tableName"] != "documents" {
		t.Errorf("native insert exported as %#v, want mode insert and the table it writes, documents", insert.Parameters)
	}
	search := exportedNode(t, exported.Document, "Search Store")
	if search.Parameters["mode"] != "load" || search.Parameters["tableName"] != "policies" {
		t.Errorf("Get Many exported as %#v, want n8n's load and its own table", search.Parameters)
	}
	noted := false
	for _, issue := range exported.Lossy {
		if issue.NodeName == "Insert Store" && issue.Severity == n8n.SeverityLossy {
			noted = true
		}
	}
	if !noted {
		t.Errorf("issues = %+v, want a lossy note: n8n's insert mode takes its documents from a loader, not vectors on main", exported.Lossy)
	}

	// Back in, each store keeps the mode it went out with: nothing comes back
	// as n8n's default, retrieve.
	raw, err := json.Marshal(exported.Document)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	reimported := importFixture(t, string(raw))
	definition, _ := registry(t).Get(nodes.VectorStorePGVectorNodeType, workflow.V(1))
	for name, want := range map[string]string{"Insert Store": "insert", "Search Store": "load"} {
		store := nodeByName(reimported.Document, name)
		if store.Parameters["mode"] != want {
			t.Errorf("%s re-imported with mode %#v, want %q", name, store.Parameters["mode"], want)
		}
		_, outputs := definition.PortsFor(store.Parameters, workflow.V(1))
		if len(outputs) != 1 || outputs[0].Kind != workflow.ConnectionMain {
			t.Errorf("%s re-imported with outputs %+v, want its main output, not the retrieve store's", name, outputs)
		}
	}
}

func TestToolVectorStoreReportsALimitItCannotUse(t *testing.T) {
	t.Parallel()

	for _, topK := range []string{"2.5", "0", "-3", "101", `"={{ $json.k }}"`} {
		result := importFixture(t, vectorStoreToolWorkflow("1", `{"name": "hr", "description": "HR", "topK": `+topK+`}`))
		tool := nodeByName(result.Document, "Answer questions with a vector store")
		if tool.Parameters["topK"] != float64(nodes.DefaultVectorTopK) {
			t.Errorf("topK %s imported as %#v, want the default %d rather than a clamp", topK, tool.Parameters["topK"], nodes.DefaultVectorTopK)
		}
		reported := false
		for _, issue := range result.Unsupported {
			if issue.NodeName == "Answer questions with a vector store" && issue.Field == "topK" && issue.Severity == n8n.SeverityLossy {
				reported = true
			}
		}
		if !reported {
			t.Errorf("topK %s: issues = %+v, want a lossy note naming topK", topK, result.Unsupported)
		}
	}

	kept := importFixture(t, vectorStoreToolWorkflow("1", `{"name": "hr", "description": "HR", "topK": 100}`))
	if tool := nodeByName(kept.Document, "Answer questions with a vector store"); tool.Parameters["topK"] != float64(100) {
		t.Errorf("topK 100 imported as %#v, want it kept", tool.Parameters["topK"])
	}
	for _, issue := range kept.Unsupported {
		if issue.Field == "topK" {
			t.Errorf("a usable topK was reported: %+v", issue)
		}
	}
}

func TestToolVectorStoreSaysWhenItWroteTheDescription(t *testing.T) {
	t.Parallel()

	result := importFixture(t, vectorStoreToolWorkflow("1", `{"name": "hr"}`))
	reported := false
	for _, issue := range result.Unsupported {
		if issue.NodeName == "Answer questions with a vector store" && issue.Field == "description" {
			reported = issue.Severity == n8n.SeverityLossy
		}
	}
	if !reported {
		t.Errorf("issues = %+v, want a lossy note that the description was written on import", result.Unsupported)
	}
	described := importFixture(t, vectorStoreToolWorkflow("1", `{"name": "hr", "description": "HR"}`))
	for _, issue := range described.Unsupported {
		if issue.Field == "description" {
			t.Errorf("a described tool was reported: %+v", issue)
		}
	}
}
