package n8n_test

// FEAT-kfmq1z: the RSS pair imports. The read node and its trigger carry
// n8n's own parameter keys, the trigger lands on the poll framework, and the
// read node's agent-tool form — the one n8n's onboarding template 6270 asks
// for — imports through the tool-variant table.

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
)

func TestRssFeedReadImportsWithItsOptions(t *testing.T) {
	t.Parallel()

	result := importFixture(t, legacyNodeFixture("n8n-nodes-base.rssFeedRead", 1.2,
		`{"url":"https://blog.example.test/rss","options":{"customFields":"author, dc:date"}}`))
	node := nodeByName(result.Document, "Legacy")
	if node.Type != n8n.RssFeedReadNodeType {
		t.Fatalf("imported as %q, want %q", node.Type, n8n.RssFeedReadNodeType)
	}
	if node.Parameters["url"] != "https://blog.example.test/rss" {
		t.Fatalf("url = %#v, want it carried", node.Parameters["url"])
	}
	options, _ := node.Parameters["options"].(map[string]any)
	if options["customFields"] != "author, dc:date" {
		t.Fatalf("options = %#v, want customFields carried", node.Parameters["options"])
	}
	if blocking := blockingFor(result, "Legacy"); len(blocking) != 0 {
		t.Fatalf("blocking issues = %#v, want none", blocking)
	}
}

func TestRssFeedReadNamesTheIgnoredCertificateOption(t *testing.T) {
	t.Parallel()

	result := importFixture(t, legacyNodeFixture("n8n-nodes-base.rssFeedRead", 1,
		`{"url":"https://blog.example.test/rss","options":{"ignoreSSL":true}}`))
	var named bool
	for _, issue := range result.Unsupported {
		if issue.Field == "options.ignoreSSL" {
			named = true
		}
	}
	if !named {
		t.Fatalf("no issue named the dropped ignoreSSL: %#v", result.Unsupported)
	}
}

func TestTheRssFeedTriggerImportsOntoThePollFramework(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Rss",
	  "nodes": [
	    {"id":"a","name":"RSS Feed Trigger","type":"n8n-nodes-base.rssFeedReadTrigger","typeVersion":1,"position":[0,0],
	     "parameters":{"feedUrl":"https://blog.example.test/rss","pollTimes":{"item":[{"mode":"everyX","unit":"hours","value":2}]}}},
	    {"id":"b","name":"No Op","type":"n8n-nodes-base.noOp","typeVersion":1,"position":[220,0],"parameters":{}}
	  ],
	  "connections": {"RSS Feed Trigger": {"main": [[{"node":"No Op","type":"main","index":0}]]}}
	}`)
	node := nodeByName(result.Document, "RSS Feed Trigger")
	if node.Type != n8n.RssFeedReadTriggerNodeType {
		t.Fatalf("imported as %q, want %q", node.Type, n8n.RssFeedReadTriggerNodeType)
	}
	if node.Parameters["feedUrl"] != "https://blog.example.test/rss" {
		t.Fatalf("feedUrl = %#v, want it carried", node.Parameters["feedUrl"])
	}
	if blocking := blockingFor(result, "RSS Feed Trigger"); len(blocking) != 0 {
		t.Fatalf("blocking issues = %#v, want none", blocking)
	}
}

func TestTheRssReadToolImportsThroughTheVariantTable(t *testing.T) {
	t.Parallel()

	// The shape template 6270 uses: a tool form wired to an agent, with the
	// base node's parameters plus the tool's own description.
	result := importFixture(t, `{
	  "name": "Agent with RSS",
	  "nodes": [
	    {"id":"a","name":"AI Agent","type":"@n8n/n8n-nodes-langchain.agent","typeVersion":2.2,"position":[0,0],"parameters":{"promptType":"define","text":"={{ $json.chatInput }}"}},
	    {"id":"b","name":"RSS Tool","type":"n8n-nodes-base.rssFeedReadTool","typeVersion":1.2,"position":[220,0],
	     "parameters":{"url":"https://blog.example.test/rss","toolDescription":"the team blog's feed"}},
	    {"id":"c","name":"Model","type":"@n8n/n8n-nodes-langchain.lmChatOpenAi","typeVersion":1.2,"position":[0,220],
	     "parameters":{"model":{"__rl":true,"mode":"list","value":"gpt-4o-mini"},"options":{}}}
	  ],
	  "connections": {
	    "RSS Tool": {"ai_tool": [[{"node":"AI Agent","type":"ai_tool","index":0}]]},
	    "Model": {"ai_languageModel": [[{"node":"AI Agent","type":"ai_languageModel","index":0}]]}
	  }
	}`)
	tool := nodeByName(result.Document, "RSS Tool")
	if tool.Type != n8n.RssFeedReadToolNodeType {
		t.Fatalf("imported as %q, want the tool form %q", tool.Type, n8n.RssFeedReadToolNodeType)
	}
	if tool.Parameters["url"] != "https://blog.example.test/rss" {
		t.Fatalf("url = %#v, want the base node's parameters carried", tool.Parameters["url"])
	}
	if tool.Parameters["toolDescription"] != "the team blog's feed" {
		t.Fatalf("toolDescription = %#v, want the tool's own naming carried", tool.Parameters["toolDescription"])
	}
	if blocking := blockingFor(result, "RSS Tool"); len(blocking) != 0 {
		t.Fatalf("blocking issues = %#v, want none", blocking)
	}
	for _, connection := range result.Document.Connections {
		if connection.Kind == "ai_tool" && connection.Target.NodeID == "a" && connection.Source.NodeID != "b" {
			t.Fatalf("the tool's ai_tool edge did not land on the agent: %#v", result.Document.Connections)
		}
	}
}

func TestTheRssPairRoundTrips(t *testing.T) {
	t.Parallel()

	read := importFixture(t, legacyNodeFixture("n8n-nodes-base.rssFeedRead", 1.1,
		`{"url":"https://blog.example.test/rss"}`))
	exported, err := n8n.Export(read.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, node := range exported.Document.Nodes {
		if node.Name != "Legacy" {
			continue
		}
		if node.Type != "n8n-nodes-base.rssFeedRead" || node.TypeVersion != 1.1 {
			t.Fatalf("exported as %q v%v, want the authored identity", node.Type, node.TypeVersion)
		}
	}

	trigger := importFixture(t, legacyNodeFixture("n8n-nodes-base.rssFeedReadTrigger", 1,
		`{"feedUrl":"https://blog.example.test/rss"}`))
	exported, err = n8n.Export(trigger.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, node := range exported.Document.Nodes {
		if node.Name != "Legacy" {
			continue
		}
		if node.Type != "n8n-nodes-base.rssFeedReadTrigger" || !strings.Contains(node.Parameters["feedUrl"].(string), "blog.example.test") {
			t.Fatalf("exported as %q with %v, want the trigger identity and feedUrl back", node.Type, node.Parameters["feedUrl"])
		}
	}
}
