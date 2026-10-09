package n8n_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func TestHandAuthoredRAGFixturesImportWithoutPlaceholders(t *testing.T) {
	t.Parallel()

	cases := []struct {
		file string
		want map[string]string
	}{
		{
			file: "n8n_rag_16706.json",
			want: map[string]string{
				"Manual":                               "kilasflow.manual",
				"HTTP Request":                         "kilasflow.httpRequest",
				"Extract from File":                    nodes.ExtractFromFileNodeType,
				"Default Data Loader":                  nodes.DocumentLoaderNodeType,
				"Recursive Character Text Splitter":    nodes.TextSplitterNodeType,
				"Embeddings OpenAI":                    nodes.EmbeddingsNodeType,
				"Postgres PGVector Store":              nodes.VectorStorePGVectorNodeType,
				"Telegram Trigger":                     nodes.TelegramTriggerNodeType,
				"Embeddings Query":                     nodes.EmbeddingsNodeType,
				"Policies Store":                       nodes.VectorStorePGVectorNodeType,
				"Answer questions with a vector store": nodes.VectorStoreToolNodeType,
				"OpenAI Chat Model1":                   "kilasflow.lmChatOpenAi",
				"OpenAI Chat Model":                    "kilasflow.lmChatOpenAi",
				"AI Agent":                             "kilasflow.agent",
				"Telegram":                             n8n.TelegramNodeType,
			},
		},
		{
			file: "n8n_rag_3647.json",
			want: map[string]string{
				"Google Drive":            nodes.GoogleDriveNodeType,
				"Download":                nodes.GoogleDriveNodeType,
				"Move":                    nodes.GoogleDriveNodeType,
				"Extract from File":       nodes.ExtractFromFileNodeType,
				"Postgres PGVector Store": nodes.VectorStorePGVectorNodeType,
			},
		},
		{
			file: "n8n_rag_4799.json",
			want: map[string]string{
				"Google Drive":           nodes.GoogleDriveNodeType,
				"Telegram Trigger":       nodes.TelegramTriggerNodeType,
				"AI Agent":               "kilasflow.agent",
				"Postgres PGVector Tool": nodes.VectorStorePGVectorNodeType,
			},
		},
		{
			file: "n8n_rag_5908.json",
			want: map[string]string{
				"Gmail Trigger":              nodes.GmailTriggerNodeType,
				"Gmail":                      nodes.GmailNodeType,
				"When chat message received": nodes.ChatTriggerNodeType,
				"Postgres PGVector Store":    nodes.VectorStorePGVectorNodeType,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(filepath.Join("testdata", tc.file))
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			result := importFixture(t, string(raw))
			for name, want := range tc.want {
				got := nodeByName(result.Document, name).Type
				if got != want {
					t.Errorf("%s imported as %q, want %q", name, got, want)
				}
				if got == n8n.UnsupportedNodeType {
					t.Errorf("%s arrived as the unsupported placeholder", name)
				}
			}
			// The fixtures wire the ingest path as n8n does, items on the
			// store's main input. They once fed the loader directly, which no
			// n8n template does, so they passed while every real one lost the
			// edge (BUG-9pmv8y).
			for _, issue := range result.Unsupported {
				if strings.Contains(issue.Reason, "held back") {
					t.Errorf("an edge was held back: %s", issue.Reason)
				}
			}
			document := result.Document
			document.ID = "wf_rag"
			bindRAGCredentials(&document)
			if _, err := workflow.Compile(document, registry(t)); err != nil {
				t.Fatalf("Compile(%s) error = %v — the fixture is the support claim, not an n8n.io scrape", tc.file, err)
			}
		})
	}
}

func bindRAGCredentials(document *workflow.Document) {
	for index, imported := range document.Nodes {
		credentials := imported.Credentials
		if credentials == nil {
			credentials = map[string]string{}
		}
		switch imported.Type {
		case nodes.VectorStorePGVectorNodeType:
			credentials["postgres"] = "cred-pg"
		case nodes.GoogleDriveNodeType, nodes.GoogleDriveTriggerNodeType:
			credentials["googleDriveOAuth2Api"] = "cred-drive"
		case nodes.GmailNodeType, nodes.GmailTriggerNodeType:
			credentials["gmailOAuth2"] = "cred-gmail"
		case nodes.TelegramTriggerNodeType, n8n.TelegramNodeType:
			credentials["telegramApi"] = "cred-tg"
		case "kilasflow.lmChatOpenAi", nodes.EmbeddingsNodeType:
			credentials["openAiApi"] = "cred-openai"
		}
		document.Nodes[index].Credentials = credentials
	}
}

// insertStoreFixture is wired the way n8n wires an ingest path, the shape
// templates 3647, 16706, 5908 and 18957 share: the items to embed arrive on
// the insert-mode store's own main input, and the Default Data Loader hangs off
// the store as an ai_document sub-node with no main input of its own. Two
// sources feed the store, as three extractors do in 3647.
const insertStoreFixture = `{
  "name": "Ingest",
  "nodes": [
    {"id": "m", "name": "Manual", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1, "position": [0, 0]},
    {"id": "p", "name": "Extract from PDF", "type": "n8n-nodes-base.extractFromFile", "typeVersion": 1, "position": [200, 0], "parameters": {"operation": "pdf"}},
    {"id": "t", "name": "Extract from Text", "type": "n8n-nodes-base.extractFromFile", "typeVersion": 1, "position": [200, 200], "parameters": {"operation": "text"}},
    {"id": "s", "name": "Postgres PGVector Store", "type": "@n8n/n8n-nodes-langchain.vectorStorePGVector", "typeVersion": 1.1, "position": [400, 0],
     "parameters": {"mode": "insert", "tableName": "documents", "options": {}}},
    {"id": "l", "name": "Default Data Loader", "type": "@n8n/n8n-nodes-langchain.documentDefaultDataLoader", "typeVersion": 1, "position": [500, 200], "parameters": {"options": {}}},
    {"id": "r", "name": "Recursive Character Text Splitter", "type": "@n8n/n8n-nodes-langchain.textSplitterRecursiveCharacterTextSplitter", "typeVersion": 1, "position": [500, 400], "parameters": {"chunkSize": 1000, "chunkOverlap": 200}},
    {"id": "e", "name": "Embeddings OpenAI", "type": "@n8n/n8n-nodes-langchain.embeddingsOpenAi", "typeVersion": 1.2, "position": [300, 200], "parameters": {"model": "text-embedding-3-small", "options": {}}},
    {"id": "d", "name": "Done", "type": "n8n-nodes-base.noOp", "typeVersion": 1, "position": [700, 0]}
  ],
  "connections": {
    "Manual": {"main": [[{"node": "Extract from PDF", "type": "main", "index": 0}, {"node": "Extract from Text", "type": "main", "index": 0}]]},
    "Extract from PDF": {"main": [[{"node": "Postgres PGVector Store", "type": "main", "index": 0}]]},
    "Extract from Text": {"main": [[{"node": "Postgres PGVector Store", "type": "main", "index": 0}]]},
    "Default Data Loader": {"ai_document": [[{"node": "Postgres PGVector Store", "type": "ai_document", "index": 0}]]},
    "Recursive Character Text Splitter": {"ai_textSplitter": [[{"node": "Default Data Loader", "type": "ai_textSplitter", "index": 0}]]},
    "Embeddings OpenAI": {"ai_embedding": [[{"node": "Postgres PGVector Store", "type": "ai_embedding", "index": 0}]]},
    "Postgres PGVector Store": {"main": [[{"node": "Done", "type": "main", "index": 0}]]}
  }
}`

func TestMainEdgeIntoAnInsertStoreLandsOnItsDataLoader(t *testing.T) {
	t.Parallel()

	result := importFixture(t, insertStoreFixture)
	for _, issue := range result.Unsupported {
		if strings.Contains(issue.Reason, "held back") {
			t.Errorf("an ingest edge was held back: %s", issue.Reason)
		}
	}

	edges := "," + edgesOf(result.Document) + ","
	for _, want := range []string{
		"Extract from PDF.main->Default Data Loader.main",
		"Extract from Text.main->Default Data Loader.main",
		"Default Data Loader.document->Postgres PGVector Store.document",
		"Postgres PGVector Store.main->Done.main",
	} {
		if !strings.Contains(edges, ","+want+",") {
			t.Errorf("edges = %s, want %s among them", edges, want)
		}
	}

	rerouted := 0
	for _, issue := range result.Unsupported {
		if issue.NodeName == "Postgres PGVector Store" && strings.Contains(issue.Reason, "Default Data Loader") {
			rerouted++
			if issue.Severity != n8n.SeverityLossy {
				t.Errorf("re-route note severity = %q, want lossy: the edge was carried, onto another node", issue.Severity)
			}
		}
	}
	if rerouted != 2 {
		t.Errorf("re-route notes = %d, want one per re-routed edge (2); issues = %+v", rerouted, result.Unsupported)
	}

	document := result.Document
	document.ID = "wf_ingest"
	bindRAGCredentials(&document)
	if _, err := workflow.Compile(document, registry(t)); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
}

func TestReRoutedIngestEdgeExportsBackOntoTheStore(t *testing.T) {
	t.Parallel()

	imported := importFixture(t, insertStoreFixture)
	exported, err := n8n.Export(imported.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, source := range []string{"Extract from PDF", "Extract from Text"} {
		slots := exported.Document.Connections[source]["main"]
		if len(slots) != 1 || len(slots[0]) != 1 || slots[0][0].Node != "Postgres PGVector Store" {
			t.Errorf("%s main = %+v, want the store, where n8n takes the items: its loader has no main input", source, slots)
		}
	}
}

func TestMainEdgeIntoAnInsertStoreWithoutALoaderIsStillHeldBack(t *testing.T) {
	t.Parallel()

	fixture := strings.Replace(insertStoreFixture,
		`"Default Data Loader": {"ai_document": [[{"node": "Postgres PGVector Store", "type": "ai_document", "index": 0}]]},`, "", 1)
	result := importFixture(t, fixture)
	held := 0
	for _, issue := range result.Unsupported {
		if strings.Contains(issue.Reason, "held back") && strings.Contains(issue.Reason, "Postgres PGVector Store") {
			held++
		}
	}
	if held != 2 {
		t.Errorf("held-back notes = %d, want 2: with no loader there is nowhere honest to put the items; issues = %+v", held, result.Unsupported)
	}
}

// TestRealRAGTemplatesKeepTheirIngestPath imports the published templates the
// RAG epic targets, from their own JSON. Templates are user content and are
// never committed (.pine/memory/licensing.md), so the test reads them from a
// directory named by KILASFLOW_N8N_TEMPLATES_DIR, one <id>.json per template
// exactly as api.n8n.io/api/templates/workflows/<id> returns it, and skips
// without one. The fixtures above are the always-on regression; this is the
// check that they still describe the real thing.
func TestRealRAGTemplatesKeepTheirIngestPath(t *testing.T) {
	t.Parallel()

	dir := strings.TrimSpace(os.Getenv("KILASFLOW_N8N_TEMPLATES_DIR"))
	if dir == "" {
		t.Skip("KILASFLOW_N8N_TEMPLATES_DIR is not set; fetch api.n8n.io/api/templates/workflows/{3647,16706,5908,18957} into it as <id>.json")
	}
	for _, id := range []string{"3647", "16706", "5908", "18957"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(filepath.Join(dir, id+".json"))
			if err != nil {
				t.Skipf("template %s not fetched: %v", id, err)
			}
			result := importFixture(t, string(unwrapTemplate(t, raw)))
			for _, issue := range result.Unsupported {
				switch {
				case strings.Contains(issue.Reason, `"main" connection`) && strings.Contains(issue.Reason, "held back"):
					t.Errorf("an ingest edge was held back: %s", issue.Reason)
				case strings.Contains(issue.Reason, "held back"):
					// 16706's retrieval side (a retrieve-mode store under a
					// Vector Store Tool) once stopped here; nothing may now.
					t.Errorf("an edge was held back: %s", issue.Reason)
				}
			}
		})
	}
}

// unwrapTemplate returns the workflow document inside a templates API
// response, which nests it as workflow.workflow. The listing level between
// the two has a nodes array of its own (node-type summaries), so the descent
// follows "workflow" keys rather than stopping at the first "nodes".
func unwrapTemplate(t *testing.T, raw []byte) []byte {
	t.Helper()
	for {
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("template JSON: %v", err)
		}
		inner, nested := envelope["workflow"]
		if !nested {
			return raw
		}
		raw = inner
	}
}
