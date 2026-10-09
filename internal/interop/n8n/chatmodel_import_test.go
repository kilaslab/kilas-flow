package n8n_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// providerModelFixture wraps one provider chat-model node in the smallest
// graph that compiles: Manual → AI Agent ← model.
func providerModelFixture(nodeType string, version float64, parameters string) string {
	return fmt.Sprintf(`{
  "name": "Provider model",
  "nodes": [
    {"id": "m", "name": "Manual", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1, "position": [0, 0]},
    {"id": "a", "name": "AI Agent", "type": "@n8n/n8n-nodes-langchain.agent", "typeVersion": 1.7, "position": [200, 0],
     "parameters": {"promptType": "define", "text": "hello", "options": {}}},
    {"id": "g", "name": "Model", "type": %q, "typeVersion": %v, "position": [200, 200], "parameters": %s}
  ],
  "connections": {
    "Manual": {"main": [[{"node": "AI Agent", "type": "main", "index": 0}]]},
    "Model": {"ai_languageModel": [[{"node": "AI Agent", "type": "ai_languageModel", "index": 0}]]}
  }
}`, nodeType, version, parameters)
}

func modelName(t *testing.T, imported workflow.Node) string {
	t.Helper()
	// The OpenAI-compatible node's model is a plain options value.
	name, ok := imported.Parameters["model"].(string)
	if !ok {
		t.Fatalf("model = %#v, want a model name", imported.Parameters["model"])
	}
	return name
}

func issueOn(result n8n.ImportResult, field string) (n8n.ImportIssue, bool) {
	for _, issue := range result.Unsupported {
		if issue.Field == field {
			return issue, true
		}
	}
	return n8n.ImportIssue{}, false
}

func TestGeminiModelNameImportsWithItsOptions(t *testing.T) {
	t.Parallel()

	// Template 2421's shape: modelName with the API's models/ prefix, and a
	// safety setting the OpenAI-compatible endpoint has no field for.
	result := importFixture(t, providerModelFixture("@n8n/n8n-nodes-langchain.lmChatGoogleGemini", 1,
		`{"modelName": "models/gemini-1.5-pro-latest", "options": {"temperature": 0.4, "maxOutputTokens": 2048,
		  "safetySettings": {"values": [{"category": "HARM_CATEGORY_HARASSMENT", "threshold": "BLOCK_NONE"}]}}}`))
	model := nodeByName(result.Document, "Model")
	if got := modelName(t, model); got != "gemini-1.5-pro-latest" {
		t.Errorf("model = %q, want gemini-1.5-pro-latest (the OpenAI-compatible endpoint names it without models/)", got)
	}
	if got := model.Parameters["temperature"]; got != 0.4 {
		t.Errorf("temperature = %#v, want 0.4 at top level, where the form shows it", got)
	}
	if got := model.Parameters["maxTokens"]; got != float64(2048) {
		t.Errorf("maxTokens = %#v, want maxOutputTokens 2048", got)
	}
	issue, found := issueOn(result, "options.safetySettings")
	if !found || issue.Severity != n8n.SeverityDropped {
		t.Errorf("safetySettings issue = %+v (found %v), want a dropped issue", issue, found)
	}

	document := result.Document
	document.ID = "wf_gemini"
	if _, err := workflow.Compile(document, registry(t)); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
}

func TestGeminiWithoutModelNameGetsNsDefaultAndValidates(t *testing.T) {
	t.Parallel()

	// Template 6270's shape: the model was left at n8n's default, so the key
	// is absent and only the options remain.
	result := importFixture(t, providerModelFixture("@n8n/n8n-nodes-langchain.lmChatGoogleGemini", 1, `{"options": {"temperature": 0}}`))
	model := nodeByName(result.Document, "Model")
	if got := modelName(t, model); got != "gemini-2.5-flash" {
		t.Errorf("model = %q, want n8n's v1 default gemini-2.5-flash", got)
	}
	if got, present := model.Parameters["temperature"]; !present || got != float64(0) {
		t.Errorf("temperature = %#v (present %v), want 0 kept", got, present)
	}
	issue, found := issueOn(result, "modelName")
	if !found || issue.Severity != n8n.SeverityLossy || !strings.Contains(issue.Reason, "gemini-2.5-flash") {
		t.Errorf("default note = %+v (found %v), want a lossy note naming the default", issue, found)
	}

	document := result.Document
	document.ID = "wf_6270"
	if _, err := workflow.Compile(document, registry(t)); err != nil {
		t.Fatalf("Compile() error = %v — the template must get past validation", err)
	}
	newer := importFixture(t, providerModelFixture("@n8n/n8n-nodes-langchain.lmChatGoogleGemini", 1.1, `{"options": {}}`))
	if got := modelName(t, nodeByName(newer.Document, "Model")); got != "gemini-3-flash-preview" {
		t.Errorf("v1.1 model = %q, want n8n's v1.1 default gemini-3-flash-preview", got)
	}
}

func TestProviderModelDefaultsAndOptionsAreCarriedOrReported(t *testing.T) {
	t.Parallel()

	cases := []struct {
		nodeType   string
		parameters string
		wantModel  string
		wantTokens any
		dropped    []string
	}{
		{"@n8n/n8n-nodes-langchain.lmChatOllama", `{"options": {}}`, "llama3.2", nil, nil},
		{"@n8n/n8n-nodes-langchain.lmChatOllama",
			`{"model": "qwen3:8b", "options": {"temperature": 0.2, "numPredict": 512, "keepAlive": "10m", "numCtx": 8192, "numBatch": 64, "think": true}}`,
			"qwen3:8b", float64(512), []string{"options.keepAlive", "options.numCtx", "options.numBatch", "options.think"}},
		{"@n8n/n8n-nodes-langchain.lmChatGroq", `{"model": "llama-3.3-70b-versatile", "options": {"maxTokensToSample": 4096}}`,
			"llama-3.3-70b-versatile", float64(4096), nil},
		{"@n8n/n8n-nodes-langchain.lmChatGroq", `{}`, "llama3-8b-8192", nil, nil},
		{"@n8n/n8n-nodes-langchain.lmChatDeepSeek", `{}`, "deepseek-chat", nil, nil},
		{"@n8n/n8n-nodes-langchain.lmChatMistralCloud", `{"options": {"safeMode": true, "randomSeed": 7}}`, "mistral-small", nil,
			[]string{"options.safeMode", "options.randomSeed"}},
		{"@n8n/n8n-nodes-langchain.lmChatXAiGrok", `{}`, "grok-2-vision-1212", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.nodeType+tc.parameters, func(t *testing.T) {
			t.Parallel()
			result := importFixture(t, providerModelFixture(tc.nodeType, 1, tc.parameters))
			model := nodeByName(result.Document, "Model")
			if got := modelName(t, model); got != tc.wantModel {
				t.Errorf("model = %q, want %q", got, tc.wantModel)
			}
			if tc.wantTokens != nil && model.Parameters["maxTokens"] != tc.wantTokens {
				t.Errorf("maxTokens = %#v, want %#v", model.Parameters["maxTokens"], tc.wantTokens)
			}
			for _, field := range tc.dropped {
				if issue, found := issueOn(result, field); !found || issue.Severity != n8n.SeverityDropped {
					t.Errorf("%s issue = %+v (found %v), want dropped", field, issue, found)
				}
			}
			document := result.Document
			document.ID = "wf_provider"
			if _, err := workflow.Compile(document, registry(t)); err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
		})
	}
}

func TestEmbeddingsOpenAiGetsTheVersionsDefaultModel(t *testing.T) {
	t.Parallel()

	for version, want := range map[float64]string{1: "text-embedding-ada-002", 1.2: "text-embedding-3-small"} {
		fixture := fmt.Sprintf(`{"name": "E", "nodes": [
		  {"id": "e", "name": "Embeddings OpenAI", "type": "@n8n/n8n-nodes-langchain.embeddingsOpenAi", "typeVersion": %v, "position": [0, 0], "parameters": {"options": {}}}
		], "connections": {}}`, version)
		result := importFixture(t, fixture)
		if got := nodeByName(result.Document, "Embeddings OpenAI").Parameters["model"]; got != want {
			t.Errorf("v%v model = %#v, want n8n's default %q", version, got, want)
		}
		if issue, found := issueOn(result, "model"); !found || issue.Severity != n8n.SeverityLossy {
			t.Errorf("v%v default note = %+v (found %v), want lossy", version, issue, found)
		}
	}
}

func TestProviderModelExportsAsItsOwnN8NNode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		nodeType string
		version  float64
		input    string
		key      string
		want     string
		option   string
	}{
		{"@n8n/n8n-nodes-langchain.lmChatGoogleGemini", 1.1, `{"modelName": "models/gemini-1.5-pro-latest", "options": {"maxOutputTokens": 100}}`,
			"modelName", "models/gemini-1.5-pro-latest", "maxOutputTokens"},
		{"@n8n/n8n-nodes-langchain.lmChatOllama", 1, `{"options": {"numPredict": 100}}`, "model", "llama3.2", "numPredict"},
		{"@n8n/n8n-nodes-langchain.lmChatGroq", 1, `{"model": "llama-3.3-70b-versatile", "options": {"maxTokensToSample": 100}}`,
			"model", "llama-3.3-70b-versatile", "maxTokensToSample"},
		{"@n8n/n8n-nodes-langchain.lmChatDeepSeek", 1, `{"model": "deepseek-reasoner", "options": {"maxTokens": 100}}`, "model", "deepseek-reasoner", "maxTokens"},
		{"@n8n/n8n-nodes-langchain.lmChatMistralCloud", 1, `{"model": "mistral-large-latest", "options": {"maxTokens": 100}}`, "model", "mistral-large-latest", "maxTokens"},
		{"@n8n/n8n-nodes-langchain.lmChatXAiGrok", 1, `{"model": "grok-3", "options": {"maxTokens": 100}}`, "model", "grok-3", "maxTokens"},
	}
	for _, tc := range cases {
		t.Run(tc.nodeType, func(t *testing.T) {
			t.Parallel()
			imported := importFixture(t, providerModelFixture(tc.nodeType, tc.version, tc.input))
			exported, err := n8n.Export(imported.Document, registry(t))
			if err != nil {
				t.Fatalf("Export() error = %v", err)
			}
			var node n8n.Node
			for _, candidate := range exported.Document.Nodes {
				if candidate.Name == "Model" {
					node = candidate
				}
			}
			if node.Type != tc.nodeType || node.TypeVersion != tc.version {
				t.Fatalf("exported as %s v%v, want %s v%v", node.Type, node.TypeVersion, tc.nodeType, tc.version)
			}
			if got := node.Parameters[tc.key]; got != tc.want {
				t.Errorf("%s = %#v, want %q", tc.key, got, tc.want)
			}
			options, _ := node.Parameters["options"].(map[string]any)
			if got := options[tc.option]; got != float64(100) {
				t.Errorf("options.%s = %#v, want 100 under the provider's own name; options = %v", tc.option, got, options)
			}
			for _, issue := range exported.Lossy {
				if issue.NodeName == "Model" && strings.Contains(issue.Reason, "no equivalent") {
					t.Errorf("exported as an unknown node: %s", issue.Reason)
				}
			}
		})
	}
}

// TestTemplate6270GeminiModelGetsPastValidation imports n8n's own "Build your
// first AI agent" template from its real JSON, read from
// KILASFLOW_N8N_TEMPLATES_DIR as TestRealRAGTemplatesKeepTheirIngestPath reads
// its templates, and skips without it.
func TestTemplate6270GeminiModelGetsPastValidation(t *testing.T) {
	t.Parallel()

	dir := strings.TrimSpace(os.Getenv("KILASFLOW_N8N_TEMPLATES_DIR"))
	if dir == "" {
		t.Skip("KILASFLOW_N8N_TEMPLATES_DIR is not set; fetch api.n8n.io/api/templates/workflows/6270 into it as 6270.json")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "6270.json"))
	if err != nil {
		t.Skipf("template 6270 not fetched: %v", err)
	}
	result := importFixture(t, string(unwrapTemplate(t, raw)))
	models := 0
	for _, imported := range result.Document.Nodes {
		if imported.Type != "kilasflow.chatModel" {
			continue
		}
		models++
		if name, _ := imported.Parameters["model"].(string); name == "" {
			t.Errorf("%s imported with no model", imported.Name)
		}
	}
	if models == 0 {
		t.Fatal("template 6270 imported no OpenAI-compatible chat model")
	}
	document := result.Document
	document.ID = "wf_6270"
	// Its RSS Feed Read tool is a *Tool variant this server does not map yet:
	// there is no RSS node (FEAT-kfmq1z). Its Get Weather tool imports
	// (BUG-b8bwhw). The placeholder is set aside so compile reaches the model
	// whatever order it checks nodes in.
	placeholders := map[string]bool{}
	kept := document.Nodes[:0:0]
	for _, imported := range document.Nodes {
		if imported.Type == n8n.UnsupportedNodeType {
			placeholders[imported.ID] = true
			continue
		}
		kept = append(kept, imported)
	}
	document.Nodes = kept
	edges := document.Connections[:0:0]
	for _, connection := range document.Connections {
		if !placeholders[connection.Source.NodeID] && !placeholders[connection.Target.NodeID] {
			edges = append(edges, connection)
		}
	}
	document.Connections = edges
	if _, err := workflow.Compile(document, registry(t)); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
}

func TestOpenAIChatModelReportsTheOptionsItCannotCarry(t *testing.T) {
	t.Parallel()

	result := importFixture(t, providerModelFixture("@n8n/n8n-nodes-langchain.lmChatOpenAi", 1.2,
		`{"model": {"__rl": true, "mode": "list", "value": "gpt-4o-mini"}, "options": {"temperature": 0.1, "responsesApiEnabled": true}}`))
	if issue, found := issueOn(result, "options.responsesApiEnabled"); !found || issue.Severity != n8n.SeverityDropped {
		t.Errorf("responsesApiEnabled issue = %+v (found %v), want dropped", issue, found)
	}
	if _, found := issueOn(result, "options.temperature"); found {
		t.Error("temperature was reported, but it is carried")
	}
}

// TestOpenAIChatModelCarriesItsReasoningEffort: the provider nodes offer
// n8n's Reasoning Effort under the same name, so an imported effort reaches
// the model rather than being reported as dropped, and goes back out on export.
func TestOpenAIChatModelCarriesItsReasoningEffort(t *testing.T) {
	t.Parallel()

	result := importFixture(t, providerModelFixture("@n8n/n8n-nodes-langchain.lmChatOpenAi", 1.2,
		`{"model": {"__rl": true, "mode": "list", "value": "gpt-5-mini"}, "options": {"reasoningEffort": "low"}}`))
	if issue, found := issueOn(result, "options.reasoningEffort"); found {
		t.Errorf("reasoningEffort reported as %+v, but it is carried", issue)
	}
	carried := false
	for _, imported := range result.Document.Nodes {
		if imported.Type != "kilasflow.lmChatOpenAi" {
			continue
		}
		options, _ := imported.Parameters["options"].(map[string]any)
		carried = options["reasoningEffort"] == "low"
	}
	if !carried {
		t.Fatalf("imported nodes = %+v, want options.reasoningEffort low on the OpenAI model", result.Document.Nodes)
	}

	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, candidate := range exported.Document.Nodes {
		if candidate.Name != "Model" {
			continue
		}
		options, _ := candidate.Parameters["options"].(map[string]any)
		if options["reasoningEffort"] != "low" {
			t.Errorf("exported options = %#v, want reasoningEffort low back on n8n's OpenAI node", options)
		}
	}
}
