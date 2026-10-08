package n8n_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// legacyAgentFixture is an AI Agent from before n8n removed the agent
// selector, at the given typeVersion and with the given selector value (none
// when empty), wired to a chat model and a tool the way template 3066's
// agents are.
func legacyAgentFixture(typeVersion, agent string) string {
	selector := ""
	if agent != "" {
		selector = fmt.Sprintf(`"agent":%q,`, agent)
	}
	return `{
  "name": "Legacy agent",
  "nodes": [
    {"id":"a","name":"When chat message received","type":"@n8n/n8n-nodes-langchain.chatTrigger","typeVersion":1.1,"position":[0,0],"parameters":{"options":{}}},
    {"id":"b","name":"AI Agent","type":"@n8n/n8n-nodes-langchain.agent","typeVersion":` + typeVersion + `,"position":[220,0],"parameters":{` + selector + `
      "promptType":"define","text":"={{ $json.chatInput }}",
      "options":{"systemMessage":"You are a helpful assistant for the sales team.","maxIterations":8}
    }},
    {"id":"c","name":"OpenAI Chat Model","type":"@n8n/n8n-nodes-langchain.lmChatOpenAi","typeVersion":1,"position":[160,200],"parameters":{
      "model":"gpt-4o-mini","options":{}
    }},
    {"id":"d","name":"Calculator","type":"@n8n/n8n-nodes-langchain.toolCalculator","typeVersion":1,"position":[400,200],"parameters":{}}
  ],
  "connections": {
    "When chat message received": {"main": [[{"node":"AI Agent","type":"main","index":0}]]},
    "OpenAI Chat Model": {"ai_languageModel": [[{"node":"AI Agent","type":"ai_languageModel","index":0}]]},
    "Calculator": {"ai_tool": [[{"node":"AI Agent","type":"ai_tool","index":0}]]}
  }
}`
}

// TestImportMapsToolCallingLegacyAgentsWithANote is template 3066's loss. A
// Conversational or OpenAI Functions agent became a blocking placeholder that
// swallowed its prompt, system message and wiring, while an agent at version
// 1.5 or below with no selector — which n8n runs as the Conversational agent
// — imported silently as a Tools Agent. Both are tool-calling loops over one
// chat model, which is what the Tools Agent is, so all three now import as
// the agent with the same lossy note naming what changed.
func TestImportMapsToolCallingLegacyAgentsWithANote(t *testing.T) {
	t.Parallel()

	for name, source := range map[string]struct{ typeVersion, agent string }{
		"conversational":                {"1.7", "conversationalAgent"},
		"openAI functions":              {"1.6", "openAiFunctionsAgent"},
		"no selector at 1.5 or below":   {"1.5", ""},
		"no selector at the first ever": {"1", ""},
	} {
		result := importFixture(t, legacyAgentFixture(source.typeVersion, source.agent))
		agent := nodeByName(result.Document, "AI Agent")
		if agent.Type != "kilasflow.agent" {
			t.Errorf("%s imported as %q, want kilasflow.agent", name, agent.Type)
			continue
		}
		if agent.Parameters["systemMessage"] != "You are a helpful assistant for the sales team." {
			t.Errorf("%s systemMessage = %#v, want it carried", name, agent.Parameters["systemMessage"])
		}
		var noted bool
		for _, issue := range result.Unsupported {
			if issue.NodeName != "AI Agent" {
				continue
			}
			if issue.Severity == n8n.SeverityBlocking {
				t.Errorf("%s: blocking issue (%s): %s", name, issue.Field, issue.Reason)
			}
			if issue.Severity == n8n.SeverityLossy && issue.Field == "agent" &&
				strings.Contains(issue.Reason, "Tools Agent") {
				noted = true
			}
		}
		if !noted {
			t.Errorf("%s: no lossy note on the agent field says it now runs as the Tools Agent: %#v", name, result.Unsupported)
		}

		document := result.Document
		document.ID = "wf_imported"
		for index, imported := range document.Nodes {
			if imported.Type == "kilasflow.lmChatOpenAi" {
				document.Nodes[index].Credentials = map[string]string{"openAiApi": "cred-local"}
			}
		}
		if _, err := workflow.Compile(document, registry(t)); err != nil {
			t.Errorf("%s: Compile() error = %v, want the imported agent to run", name, err)
		}
	}

	// The Conversational agent's user-turn template has no place in the Tools
	// Agent, and is said to be dropped rather than silently ignored.
	templated := strings.Replace(legacyAgentFixture("1.7", "conversationalAgent"), `"maxIterations":8`,
		`"maxIterations":8,"humanMessage":"TOOLS\n------\n{tools}\n\n{format_instructions}\n\nUSER'S INPUT\n{{input}}"`, 1)
	var dropped bool
	for _, issue := range importFixture(t, templated).Unsupported {
		if issue.NodeName == "AI Agent" && issue.Severity == n8n.SeverityDropped && issue.Field == "options.humanMessage" {
			dropped = true
		}
	}
	if !dropped {
		t.Error("a Conversational agent's humanMessage template was not reported dropped")
	}

	// From 1.6 the selector defaults to the Tools Agent, so an agent with no
	// selector there is one, and gets no note.
	for _, typeVersion := range []string{"1.6", "1.9", "2.2"} {
		result := importFixture(t, legacyAgentFixture(typeVersion, ""))
		for _, issue := range result.Unsupported {
			if issue.NodeName == "AI Agent" && issue.Field == "agent" {
				t.Errorf("version %s with no selector got an agent note: %s", typeVersion, issue.Reason)
			}
		}
	}
}
