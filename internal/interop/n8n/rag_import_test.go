package n8n_test

import (
	"os"
	"path/filepath"
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
				"Manual":                            "kilasflow.manual",
				"HTTP Request":                      "kilasflow.httpRequest",
				"Extract from File":                 nodes.ExtractFromFileNodeType,
				"Default Data Loader":               nodes.DocumentLoaderNodeType,
				"Recursive Character Text Splitter": nodes.TextSplitterNodeType,
				"Embeddings OpenAI":                 nodes.EmbeddingsNodeType,
				"Postgres PGVector Store":           nodes.VectorStorePGVectorNodeType,
				"Telegram Trigger":                  nodes.TelegramTriggerNodeType,
				"Embeddings Query":                  nodes.EmbeddingsNodeType,
				"Postgres PGVector Tool":            nodes.VectorStorePGVectorNodeType,
				"OpenAI Chat Model":                 "kilasflow.lmChatOpenAi",
				"AI Agent":                          "kilasflow.agent",
				"Telegram":                          n8n.TelegramNodeType,
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
