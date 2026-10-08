package n8n_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// BUG-t9e3k5: an import reported cycles as blocking (BUG-gk7mf5), but the
// compiler's other topology refusals imported with nothing blocking, so the
// migration guide's promise — no blocking entries means the workflow
// activates — still failed for them.

func TestImportReportsEveryTopologyRefusalOfTheCompiler(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, fixture, node, says string
	}{
		{
			name: "a node no trigger reaches",
			fixture: `{"name": "Orphan", "nodes": [
				{"id": "t", "name": "Start", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1},
				{"id": "a", "name": "Shape", "type": "n8n-nodes-base.noOp", "typeVersion": 1},
				{"id": "b", "name": "Left Behind", "type": "n8n-nodes-base.noOp", "typeVersion": 1}
			], "connections": {"Start": {"main": [[{"node": "Shape", "type": "main", "index": 0}]]}}}`,
			node: "Left Behind", says: `"Left Behind"`,
		},
		{
			name: "no trigger at all",
			fixture: `{"name": "Headless", "nodes": [
				{"id": "a", "name": "Shape", "type": "n8n-nodes-base.noOp", "typeVersion": 1},
				{"id": "b", "name": "Finish", "type": "n8n-nodes-base.noOp", "typeVersion": 1}
			], "connections": {"Shape": {"main": [[{"node": "Finish", "type": "main", "index": 0}]]}}}`,
			says: "trigger",
		},
		{
			name: "a second chat trigger",
			fixture: `{"name": "Two chats", "nodes": [
				{"id": "c1", "name": "Chat A", "type": "@n8n/n8n-nodes-langchain.chatTrigger", "typeVersion": 1.1, "webhookId": "w1"},
				{"id": "c2", "name": "Chat B", "type": "@n8n/n8n-nodes-langchain.chatTrigger", "typeVersion": 1.1, "webhookId": "w2"},
				{"id": "a", "name": "Shape", "type": "n8n-nodes-base.noOp", "typeVersion": 1}
			], "connections": {
				"Chat A": {"main": [[{"node": "Shape", "type": "main", "index": 0}]]},
				"Chat B": {"main": [[{"node": "Shape", "type": "main", "index": 0}]]}
			}}`,
			says: "only one",
		},
		{
			name: "two tools under one name",
			fixture: `{"name": "Twin tools", "nodes": [
				{"id": "t", "name": "Start", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1},
				{"id": "a", "name": "Assistant", "type": "@n8n/n8n-nodes-langchain.agent", "typeVersion": 1.7,
				 "parameters": {"promptType": "define", "text": "hello"}},
				{"id": "m", "name": "Model", "type": "@n8n/n8n-nodes-langchain.lmChatOpenAi", "typeVersion": 1,
				 "parameters": {"model": "gpt-4o-mini"}, "credentials": {"openAiApi": {"id": "cred1", "name": "OpenAI"}}},
				{"id": "x", "name": "Look up", "type": "@n8n/n8n-nodes-langchain.toolCalculator", "typeVersion": 1},
				{"id": "y", "name": "Look-up", "type": "@n8n/n8n-nodes-langchain.toolCalculator", "typeVersion": 1}
			], "connections": {
				"Start": {"main": [[{"node": "Assistant", "type": "main", "index": 0}]]},
				"Model": {"ai_languageModel": [[{"node": "Assistant", "type": "ai_languageModel", "index": 0}]]},
				"Look up": {"ai_tool": [[{"node": "Assistant", "type": "ai_tool", "index": 0}]]},
				"Look-up": {"ai_tool": [[{"node": "Assistant", "type": "ai_tool", "index": 0}]]}
			}}`,
			says: "tool name",
		},
		{
			name: "an agent with no chat model",
			fixture: `{"name": "Modelless", "nodes": [
				{"id": "t", "name": "Start", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1},
				{"id": "a", "name": "Assistant", "type": "@n8n/n8n-nodes-langchain.agent", "typeVersion": 1.7,
				 "parameters": {"promptType": "define", "text": "hello"}}
			], "connections": {"Start": {"main": [[{"node": "Assistant", "type": "main", "index": 0}]]}}}`,
			node: "Assistant", says: `"Assistant"`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := importFixture(t, test.fixture)

			// The report agrees with the run: the compiler refuses the
			// document, and every topology refusal it names is in the report.
			document := result.Document
			document.ID = "wf_topology"
			_, err := workflow.Compile(document, registry(t))
			var refused *workflow.ValidationErrors
			if !errors.As(err, &refused) {
				t.Fatalf("Compile() error = %v, want the topology refusal the report must carry", err)
			}

			var blocking []n8n.ImportIssue
			for _, issue := range result.Unsupported {
				if issue.Severity == n8n.SeverityBlocking && issue.Field == "topology" {
					blocking = append(blocking, issue)
				}
			}
			if len(blocking) == 0 {
				t.Fatalf("unsupported = %#v, want a blocking topology issue", result.Unsupported)
			}
			found := false
			for _, issue := range blocking {
				if strings.Contains(issue.Reason, test.says) && (test.node == "" || issue.NodeName == test.node) {
					found = true
				}
				if strings.Contains(issue.Reason, `"`+issue.NodeID+`"`) && issue.NodeID != issue.NodeName {
					t.Errorf("reason = %q names the node by its id %q, not its name", issue.Reason, issue.NodeID)
				}
			}
			if !found {
				t.Errorf("blocking = %#v, want one on %q saying %q", blocking, test.node, test.says)
			}
		})
	}
}

func TestACleanImportReportsNoTopologyRefusal(t *testing.T) {
	t.Parallel()
	result := importFixture(t, `{"name": "Fine", "nodes": [
		{"id": "t", "name": "Start", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1},
		{"id": "a", "name": "Shape", "type": "n8n-nodes-base.noOp", "typeVersion": 1}
	], "connections": {"Start": {"main": [[{"node": "Shape", "type": "main", "index": 0}]]}}}`)
	for _, issue := range result.Unsupported {
		if issue.Field == "topology" {
			t.Errorf("a workflow the compiler accepts reported %#v", issue)
		}
	}
}

func TestAPastedFragmentIsNotReportedForItsShape(t *testing.T) {
	t.Parallel()
	// A paste lands in a workflow that has its own trigger; a lone step with
	// nothing feeding it yet is what a fragment is, not a refusal.
	result, err := n8n.ImportFragment([]byte(`{"nodes": [
		{"id": "a", "name": "Shape", "type": "n8n-nodes-base.noOp", "typeVersion": 1}
	], "connections": {}}`), registry(t))
	if err != nil {
		t.Fatalf("ImportFragment() error = %v", err)
	}
	for _, issue := range result.Unsupported {
		if issue.Field == "topology" {
			t.Errorf("a pasted fragment reported %#v", issue)
		}
	}
}
