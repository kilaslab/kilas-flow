package n8n_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// toolVariantFixture is the agent cluster of n8n's onboarding template 6270,
// whose "Get Weather" tool is the HTTP Request node exported as its
// usableAsTool variant: the base node's own parameters, plus toolDescription,
// under the type name with Tool appended.
const toolVariantFixture = `{
  "name": "First AI agent",
  "nodes": [
    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
    {"id":"b","name":"AI Agent","type":"@n8n/n8n-nodes-langchain.agent","typeVersion":2.2,"position":[220,0],"parameters":{
      "promptType":"define","text":"What is the weather in Berlin?",
      "options":{"systemMessage":"You answer weather questions with the Get Weather tool."}
    }},
    {"id":"c","name":"OpenAI Chat Model","type":"@n8n/n8n-nodes-langchain.lmChatOpenAi","typeVersion":1.2,"position":[160,200],"parameters":{
      "model":{"__rl":true,"mode":"list","value":"gpt-4.1-mini"},"options":{}
    }},
    {"id":"d","name":"Get Weather","type":"n8n-nodes-base.httpRequestTool","typeVersion":4.2,"position":[400,200],"parameters":{
      "toolDescription":"Get the current weather for a latitude and longitude",
      "url":"https://api.open-meteo.com/v1/forecast",
      "sendQuery":true,
      "queryParameters":{"parameters":[
        {"name":"latitude","value":"={{ $fromAI('latitude', 'Latitude of the city', 'number') }}"},
        {"name":"longitude","value":"={{ $fromAI('longitude', 'Longitude of the city', 'number') }}"},
        {"name":"current","value":"temperature_2m,windspeed_10m"}
      ]},
      "options":{}
    }}
  ],
  "connections": {
    "Manual": {"main": [[{"node":"AI Agent","type":"main","index":0}]]},
    "OpenAI Chat Model": {"ai_languageModel": [[{"node":"AI Agent","type":"ai_languageModel","index":0}]]},
    "Get Weather": {"ai_tool": [[{"node":"AI Agent","type":"ai_tool","index":0}]]}
  }
}`

// TestAToolVariantOfAMappedNodeImportsAsItsKilasFlowTool is template 6270's
// blocker. n8n exports every usableAsTool node as <type>Tool, and none of
// those names had an entry, so the weather tool of n8n's own onboarding
// template arrived as a blocking placeholder although the HTTP tool exists.
func TestAToolVariantOfAMappedNodeImportsAsItsKilasFlowTool(t *testing.T) {
	t.Parallel()

	result := importFixture(t, toolVariantFixture)
	tool := nodeByName(result.Document, "Get Weather")
	if tool.Type != "kilasflow.httpTool" {
		t.Fatalf("httpRequestTool imported as %q, want kilasflow.httpTool", tool.Type)
	}
	if got := tool.Parameters["toolDescription"]; got != "Get the current weather for a latitude and longitude" {
		t.Errorf("toolDescription = %#v, want the n8n tool's own description", got)
	}
	if got := tool.Parameters["url"]; got != "https://api.open-meteo.com/v1/forecast" {
		t.Errorf("url = %#v, want the base node's translator to carry it", got)
	}
	for _, issue := range result.Unsupported {
		if issue.Severity == n8n.SeverityBlocking {
			t.Errorf("blocking issue on %q (%s): %s", issue.NodeName, issue.Field, issue.Reason)
		}
	}

	document := result.Document
	document.ID = "wf_imported"
	for index, imported := range document.Nodes {
		if imported.Type == "kilasflow.lmChatOpenAi" {
			document.Nodes[index].Credentials = map[string]string{"openAiApi": "cred-local"}
		}
	}
	if _, err := workflow.Compile(document, registry(t)); err != nil {
		t.Fatalf("Compile() error = %v, want the imported tool to run on the agent", err)
	}
}

// TestAToolVariantWithNoKilasFlowToolSaysSo keeps the placeholder honest for a
// tool variant whose base node is mapped but has no tool of its own here yet:
// "no equivalent" is wrong when the node itself imports, and the author
// needs to know it is the tool form that is missing.
func TestAToolVariantWithNoKilasFlowToolSaysSo(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Mail agent",
	  "nodes": [
	    {"id":"d","name":"Send Email","type":"n8n-nodes-base.gmailTool","typeVersion":2.1,"position":[400,200],"parameters":{
	      "sendTo":"={{ $fromAI('to') }}","subject":"Hello","message":"Hi","options":{}
	    }}
	  ],
	  "connections": {}
	}`)
	if got := nodeByName(result.Document, "Send Email").Type; got != n8n.UnsupportedNodeType {
		t.Fatalf("gmailTool imported as %q, want the placeholder until a Gmail tool exists", got)
	}
	var named bool
	for _, issue := range result.Unsupported {
		if issue.Severity == n8n.SeverityBlocking && issue.NodeName == "Send Email" &&
			strings.Contains(issue.Reason, "n8n-nodes-base.gmail") && strings.Contains(issue.Reason, "tool") &&
			!strings.Contains(issue.Reason, "no equivalent") {
			named = true
		}
	}
	if !named {
		t.Errorf("no blocking diagnostic says the Gmail node imports but its tool form does not: %#v", result.Unsupported)
	}
}
