package n8n_test

// The PR #36 follow-up: the provider app nodes and the root chains import.
// Each lands on the node PR #36 registered under n8n's own parameter keys,
// the operations that node does not run are refused with the reason a
// workflow author can act on, and the two output-parser types map — the
// item-list one onto the item-list schema source, the auto-fixing wrapper
// merged into the parser it wrapped.

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// chainFixture wires one model sub-node to one root chain, the shape every
// template uses.
func chainFixture(chainType string, typeVersion float64, chainParameters, modelParameters string) string {
	return `{
	  "name": "Chain",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"c","name":"Chain","type":"` + chainType + `","typeVersion":` + jsonNumber(typeVersion) + `,"position":[220,0],"parameters":` + chainParameters + `},
	    {"id":"m","name":"Model","type":"@n8n/n8n-nodes-langchain.lmChatOpenAi","typeVersion":1.2,"position":[220,220],
	     "parameters":{"model":{"__rl":true,"mode":"list","value":"gpt-4o-mini"},"options":` + modelParameters + `}}
	  ],
	  "connections": {
	    "Manual": {"main": [[{"node":"Chain","type":"main","index":0}]]},
	    "Model": {"ai_languageModel": [[{"node":"Chain","type":"ai_languageModel","index":0}]]}
	  }
	}`
}

func TestTheOpenAiAppNodeImportsItsV1Family(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "OpenAI",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"OpenAI","type":"@n8n/n8n-nodes-langchain.openAi","typeVersion":1.5,"position":[220,0],
	     "parameters":{"resource":"text","operation":"message",
	       "modelId":{"__rl":true,"mode":"list","value":"gpt-4o-mini"},
	       "messages":{"values":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"}]},
	       "jsonOutput":false,
	       "options":{"temperature":0.4,"frequency_penalty":0.2,"maxTokens":512,"n":1}}}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"OpenAI","type":"main","index":0}]]}}
	}`)
	node := nodeByName(result.Document, "OpenAI")
	if node.Type != n8n.OpenAIAppNodeType {
		t.Fatalf("imported as %q, want %q", node.Type, n8n.OpenAIAppNodeType)
	}
	if locator, isMap := node.Parameters["modelId"].(map[string]any); !isMap || locator["value"] != "gpt-4o-mini" {
		t.Fatalf("modelId = %#v, want the locator carried", node.Parameters["modelId"])
	}
	messages, isMap := node.Parameters["messages"].(map[string]any)
	if !isMap {
		t.Fatalf("messages = %#v, want the collection carried", node.Parameters["messages"])
	}
	rows, _ := messages["messageValues"].([]any)
	if len(rows) != 2 {
		t.Fatalf("messageValues = %#v, want the rows renamed from n8n's values", messages)
	}
	options, _ := node.Parameters["options"].(map[string]any)
	if options["frequencyPenalty"] != 0.2 || options["temperature"] != 0.4 || options["maxTokens"] != 512.0 {
		t.Fatalf("options = %#v, want the snake-case penalties renamed and the rest carried", options)
	}
	if _, has := options["n"]; has {
		t.Errorf("options carried n, which was reported dropped instead")
	}
	var dropped bool
	for _, issue := range result.Unsupported {
		if issue.Field == "options.n" {
			dropped = true
		}
	}
	if !dropped {
		t.Errorf("no issue named the dropped completions count: %#v", result.Unsupported)
	}
	if blocking := blockingFor(result, "OpenAI"); len(blocking) != 0 {
		t.Fatalf("blocking issues = %#v, want none", blocking)
	}
}

func TestTheOpenAiAppNodeRefusesTheOperationsItCannotRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		typeVersion float64
		parameters  string
		wantNeedle  string
	}{
		{
			name: "v2 message a model is the responses protocol", typeVersion: 2.3,
			parameters: `{"resource":"text","operation":"response"}`, wantNeedle: "text/response",
		},
		{
			name: "v2 with no operation defaults to the responses protocol", typeVersion: 2,
			parameters: `{}`, wantNeedle: "text/response",
		},
		{
			name: "image generation", typeVersion: 1.5,
			parameters: `{"resource":"image","operation":"generate"}`, wantNeedle: "image/generate",
		},
		{
			name: "audio translation", typeVersion: 1.5,
			parameters: `{"resource":"audio","operation":"translate"}`, wantNeedle: "audio/translate",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := importFixture(t, legacyNodeFixture("@n8n/n8n-nodes-langchain.openAi", test.typeVersion, test.parameters))
			node := nodeByName(result.Document, "Legacy")
			if node.Type != n8n.UnsupportedNodeType {
				t.Fatalf("imported as %q, want the placeholder", node.Type)
			}
			blocking := blockingFor(result, "Legacy")
			if len(blocking) != 1 || !strings.Contains(blocking[0].Reason, test.wantNeedle) {
				t.Fatalf("blocking issues = %#v, want the reason naming %q", blocking, test.wantNeedle)
			}
		})
	}
}

func TestTheGeminiAppNodeImportsAndMapsTheModelRole(t *testing.T) {
	t.Parallel()

	result := importFixture(t, legacyNodeFixture("@n8n/n8n-nodes-langchain.googleGemini", 1.2,
		`{"resource":"text","operation":"message",
		  "modelId":{"__rl":true,"mode":"list","value":"models/gemini-2.5-flash"},
		  "messages":{"values":[{"role":"user","content":"hello"},{"role":"model","content":"hi there"}]},
		  "jsonOutput":true}`))
	node := nodeByName(result.Document, "Legacy")
	if node.Type != n8n.GoogleGeminiAppNodeType {
		t.Fatalf("imported as %q, want %q", node.Type, n8n.GoogleGeminiAppNodeType)
	}
	messages, _ := node.Parameters["messages"].(map[string]any)
	rows, _ := messages["messageValues"].([]any)
	second, _ := rows[1].(map[string]any)
	if second["role"] != "assistant" {
		t.Fatalf("role = %#v, want Gemini's model role renamed to assistant", second["role"])
	}
	if blocking := blockingFor(result, "Legacy"); len(blocking) != 0 {
		t.Fatalf("blocking issues = %#v, want none", blocking)
	}

	// Built-in tools change what the model answers; they are refused.
	refused := importFixture(t, legacyNodeFixture("@n8n/n8n-nodes-langchain.googleGemini", 1.2,
		`{"builtInTools":{"googleSearch":true}}`))
	if node := nodeByName(refused.Document, "Legacy"); node.Type != n8n.UnsupportedNodeType {
		t.Fatalf("imported as %q with tools on, want the placeholder", node.Type)
	}
	if blocking := blockingFor(refused, "Legacy"); len(blocking) != 1 || !strings.Contains(blocking[0].Reason, "built-in tools") {
		t.Fatalf("blocking issues = %#v, want the tools named", blocking)
	}
}

func TestTheRootChainsImportOntoTheirNodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		n8nType     string
		typeVersion float64
		parameters  string
		wantType    string
		check       func(*testing.T, workflow.Node)
	}{
		{
			name: "information extractor renames its attribute rows", n8nType: "@n8n/n8n-nodes-langchain.informationExtractor",
			typeVersion: 1.2, wantType: n8n.InformationExtractorType,
			parameters: `{"text":"={{ $json.chatInput }}","attributes":{"attributes":[
				{"name":"city","type":"string","description":"where","required":false},
				{"name":"founded","type":"date"}]}}`,
			check: func(t *testing.T, node workflow.Node) {
				attributes, _ := node.Parameters["attributes"].(map[string]any)
				rows, _ := attributes["values"].([]any)
				if len(rows) != 2 {
					t.Fatalf("attributes = %#v, want the rows renamed from n8n's attributes list", attributes)
				}
				first, _ := rows[0].(map[string]any)
				if _, has := first["required"]; has {
					t.Errorf("the required flag was carried; it was reported instead")
				}
				second, _ := rows[1].(map[string]any)
				if second["type"] != "string" {
					t.Errorf("date attribute = %#v, want it read as a string", second)
				}
			},
		},
		{
			name: "text classifier renames its category rows", n8nType: "@n8n/n8n-nodes-langchain.textClassifier",
			typeVersion: 1.1, wantType: n8n.TextClassifierType,
			parameters: `{"inputText":"={{ $json.chatInput }}","categories":{"categories":[
				{"category":"question","description":"asks something"},
				{"category":"complaint"}]}}`,
			check: func(t *testing.T, node workflow.Node) {
				categories, _ := node.Parameters["categories"].(map[string]any)
				rows, _ := categories["values"].([]any)
				if len(rows) != 2 {
					t.Fatalf("categories = %#v, want the rows renamed from n8n's categories list", categories)
				}
			},
		},
		{
			name: "summarization maps the json input mode", n8nType: "@n8n/n8n-nodes-langchain.chainSummarization",
			typeVersion: 2.1, wantType: n8n.ChainSummarizationType,
			parameters: `{"operationMode":"nodeInputJson","chunkingMode":"simple"}`,
			check: func(t *testing.T, node workflow.Node) {
				if node.Parameters["operationMode"] != "node" {
					t.Fatalf("operationMode = %#v, want nodeInputJson mapped onto node", node.Parameters["operationMode"])
				}
			},
		},
		{
			name: "question and answer takes the query", n8nType: "@n8n/n8n-nodes-langchain.chainRetrievalQa",
			typeVersion: 1.5, wantType: n8n.ChainRetrievalQaType,
			parameters: `{"query":"={{ $json.chatInput }}","options":{"systemPromptTemplate":"answer briefly"}}`,
			check: func(t *testing.T, node workflow.Node) {
				if marker, isMap := node.Parameters["prompt"].(map[string]any); !isMap || marker["value"] != "{{ $json.chatInput }}" {
					t.Fatalf("prompt = %#v, want the query carried as an expression marker", node.Parameters["prompt"])
				}
				options, _ := node.Parameters["options"].(map[string]any)
				if options["systemPromptTemplate"] != "answer briefly" {
					t.Fatalf("options = %#v, want the system prompt carried", options)
				}
			},
		},
		{
			name: "sentiment analysis keeps its default scale", n8nType: "@n8n/n8n-nodes-langchain.sentimentAnalysis",
			typeVersion: 1.1, wantType: n8n.SentimentAnalysisType,
			parameters: `{"inputText":"={{ $json.chatInput }}"}`,
			check: func(t *testing.T, node workflow.Node) {
				if marker, isMap := node.Parameters["inputText"].(map[string]any); !isMap || marker["value"] != "{{ $json.chatInput }}" {
					t.Fatalf("inputText = %#v, want it carried as an expression marker", node.Parameters["inputText"])
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := importFixture(t, chainFixture(test.n8nType, test.typeVersion, test.parameters, `{}`))
			node := nodeByName(result.Document, "Chain")
			if node.Type != test.wantType {
				t.Fatalf("imported as %q, want %q", node.Type, test.wantType)
			}
			test.check(t, node)
			if blocking := blockingFor(result, "Chain"); len(blocking) != 0 {
				t.Fatalf("blocking issues = %#v, want none", blocking)
			}
			// The model edge lands on the chain's model port.
			wired := false
			for _, connection := range result.Document.Connections {
				if connection.Kind == "ai_languageModel" && connection.Target.NodeID == "c" && connection.Source.NodeID == "m" {
					wired = true
				}
			}
			if !wired {
				t.Fatalf("the model edge did not land on the chain: %#v", result.Document.Connections)
			}
		})
	}
}

func TestTheChainsRefuseTheShapesTheyCannotRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		n8nType    string
		parameters string
		wantNeedle string
	}{
		{
			name: "a multi-class classifier", n8nType: "@n8n/n8n-nodes-langchain.textClassifier",
			parameters: `{"inputText":"x","categories":{"categories":[{"category":"a"}]},"options":{"multiClass":true}}`,
			wantNeedle: "several categories",
		},
		{
			name: "a summarization over binary input", n8nType: "@n8n/n8n-nodes-langchain.chainSummarization",
			parameters: `{"operationMode":"nodeInputBinary"}`, wantNeedle: "binary input",
		},
		{
			name: "a summarization over a document loader", n8nType: "@n8n/n8n-nodes-langchain.chainSummarization",
			parameters: `{"operationMode":"documentLoader"}`, wantNeedle: "document-loader",
		},
		{
			name: "a custom sentiment scale", n8nType: "@n8n/n8n-nodes-langchain.sentimentAnalysis",
			parameters: `{"inputText":"x","options":{"categories":{"categories":[
				{"category":"delighted"},{"category":"furious"}]}}}`,
			wantNeedle: "delighted, furious",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := importFixture(t, legacyNodeFixture(test.n8nType, 1.1, test.parameters))
			node := nodeByName(result.Document, "Legacy")
			if node.Type != n8n.UnsupportedNodeType {
				t.Fatalf("imported as %q, want the placeholder", node.Type)
			}
			blocking := blockingFor(result, "Legacy")
			if len(blocking) != 1 || !strings.Contains(blocking[0].Reason, test.wantNeedle) {
				t.Fatalf("blocking issues = %#v, want the reason naming %q", blocking, test.wantNeedle)
			}
		})
	}
}

func TestTheChainsRoundTripAtTheirAuthoredVersions(t *testing.T) {
	t.Parallel()

	result := importFixture(t, chainFixture("@n8n/n8n-nodes-langchain.informationExtractor", 1.1,
		`{"text":"hello","attributes":{"attributes":[{"name":"city","type":"string","description":"where"}]}}`, `{}`))
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, node := range exported.Document.Nodes {
		if node.Name != "Chain" {
			continue
		}
		if node.Type != "@n8n/n8n-nodes-langchain.informationExtractor" || node.TypeVersion != 1.1 {
			t.Fatalf("exported as %q v%v, want the authored identity", node.Type, node.TypeVersion)
		}
		attributes, _ := node.Parameters["attributes"].(map[string]any)
		if _, has := attributes["attributes"]; !has {
			t.Fatalf("attributes = %#v, want the rows returned to n8n's list name", node.Parameters["attributes"])
		}
	}
}

func TestTheItemListParserImportsOntoTheItemListSchemaSource(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Item list",
	  "nodes": [
	    {"id":"a","name":"Chain","type":"@n8n/n8n-nodes-langchain.chainLlm","typeVersion":1.5,"position":[0,0],
	     "parameters":{"promptType":"define","text":"list three"}},
	    {"id":"b","name":"Legacy","type":"@n8n/n8n-nodes-langchain.outputParserItemList","typeVersion":1,"position":[220,0],
	     "parameters":{"options":{"numberOfItems":3,"separator":"|"}}}
	  ],
	  "connections": {"Legacy": {"ai_outputParser": [[{"node":"Chain","type":"ai_outputParser","index":0}]]}}
	}`)
	node := nodeByName(result.Document, "Legacy")
	if node.Type != "kilasflow.outputParser" {
		t.Fatalf("imported as %q, want kilasflow.outputParser", node.Type)
	}
	if node.Parameters["schemaType"] != "itemList" {
		t.Fatalf("schemaType = %#v, want the item-list source", node.Parameters["schemaType"])
	}
	if node.Parameters["jsonSchema"] != `{"type":"string"}` {
		t.Fatalf("jsonSchema = %#v, want a string item", node.Parameters["jsonSchema"])
	}
	report := ""
	for _, issue := range result.Unsupported {
		report += issue.Reason + "\n"
	}
	if !strings.Contains(report, "3 items") || !strings.Contains(report, "separator") {
		t.Fatalf("the report does not name the count and separator differences:\n%s", report)
	}
	if blocking := blockingFor(result, "Legacy"); len(blocking) != 0 {
		t.Fatalf("blocking issues = %#v, want none", blocking)
	}
}

func TestTheAutofixingParserMergesIntoTheParserItWrapped(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Autofix",
	  "nodes": [
	    {"id":"a","name":"Chain","type":"@n8n/n8n-nodes-langchain.chainLlm","typeVersion":1.5,"position":[0,0],
	     "parameters":{"promptType":"define","text":"extract"}},
	    {"id":"w","name":"Auto-fixing Output Parser","type":"@n8n/n8n-nodes-langchain.outputParserAutofixing","typeVersion":1,"position":[220,0],
	     "parameters":{"options":{"prompt":"repair it"}}},
	    {"id":"p","name":"Structured Output Parser","type":"@n8n/n8n-nodes-langchain.outputParserStructured","typeVersion":1.3,"position":[440,0],
	     "parameters":{"schemaType":"fromJson","jsonSchemaExample":"{\"city\":\"Lyon\"}"}},
	    {"id":"m","name":"Repair Model","type":"@n8n/n8n-nodes-langchain.lmChatOpenAi","typeVersion":1.2,"position":[220,220],
	     "parameters":{"model":{"__rl":true,"mode":"list","value":"gpt-4o-mini"},"options":{}}},
	    {"id":"c","name":"Chain Model","type":"@n8n/n8n-nodes-langchain.lmChatOpenAi","typeVersion":1.2,"position":[0,220],
	     "parameters":{"model":{"__rl":true,"mode":"list","value":"gpt-4o-mini"},"options":{}}}
	  ],
	  "connections": {
	    "Chain Model": {"ai_languageModel": [[{"node":"Chain","type":"ai_languageModel","index":0}]]},
	    "Repair Model": {"ai_languageModel": [[{"node":"Auto-fixing Output Parser","type":"ai_languageModel","index":0}]]},
	    "Structured Output Parser": {"ai_outputParser": [[{"node":"Auto-fixing Output Parser","type":"ai_outputParser","index":0}]]},
	    "Auto-fixing Output Parser": {"ai_outputParser": [[{"node":"Chain","type":"ai_outputParser","index":0}]]}
	  }
	}`)

	// The wrapper is gone: its whole contribution is the repair loop, which
	// the merged parser now carries.
	for _, node := range result.Document.Nodes {
		if node.ID == "w" {
			t.Fatalf("the wrapper survived the import: %#v", node)
		}
	}
	// The structured parser reaches the chain directly, with its repair loop
	// turned on and its schema intact.
	parser := nodeByName(result.Document, "Structured Output Parser")
	if parser.Parameters["maxRetries"] != float64(2) {
		t.Fatalf("maxRetries = %#v, want the wrapper's repair loop switched on", parser.Parameters["maxRetries"])
	}
	if parser.Parameters["exampleJson"] != `{"city":"Lyon"}` {
		t.Fatalf("exampleJson = %#v, want the schema the wrapper wrapped", parser.Parameters["exampleJson"])
	}
	wired := false
	for _, connection := range result.Document.Connections {
		if connection.Kind == "ai_outputParser" && connection.Source.NodeID == "p" && connection.Target.NodeID == "a" {
			wired = true
		}
		if connection.Kind == "ai_outputParser" && connection.Source.NodeID == "w" {
			t.Fatalf("an output-parser edge still leaves the removed wrapper: %#v", connection)
		}
	}
	if !wired {
		t.Fatalf("the wrapped parser does not reach the chain: %#v", result.Document.Connections)
	}
	// And the import says what it did.
	report := ""
	for _, issue := range result.Unsupported {
		report += issue.Reason + "\n"
	}
	for _, want := range []string{"was merged into the parser it wrapped", "repair model is not connected"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not say %q:\n%s", want, report)
		}
	}
	if blocking := blockingFor(result, "Auto-fixing Output Parser"); len(blocking) != 0 {
		t.Fatalf("blocking issues = %#v, want none", blocking)
	}
}

func TestAnAutofixingParserStandingAloneStaysAndNamesItself(t *testing.T) {
	t.Parallel()

	result := importFixture(t, legacyNodeFixture("@n8n/n8n-nodes-langchain.outputParserAutofixing", 1, `{}`))
	node := nodeByName(result.Document, "Legacy")
	if node.Type != "kilasflow.outputParser" {
		t.Fatalf("imported as %q, want kilasflow.outputParser", node.Type)
	}
	// Nothing wraps: the node stays, and the parser's own validator names the
	// schema it never had — the honest refusal rather than a silent drop.
	definition, ok := registry(t).Resolve("kilasflow.outputParser", workflow.V(1))
	if !ok {
		t.Fatal("the output parser is not registered")
	}
	if err := definition.Validate(workflow.Node{Parameters: node.Parameters}); err == nil ||
		!strings.Contains(err.Error(), "required") {
		t.Fatalf("Validate() = %v, want the missing schema named", err)
	}
}
