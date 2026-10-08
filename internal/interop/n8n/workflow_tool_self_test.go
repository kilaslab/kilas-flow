package n8n_test

import (
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// recursiveAgentFixture is the recursive-agent pattern of template 2006 and
// nine others in the top 198: a Workflow Tool whose workflowId locator is
// `={{ $workflow.id }}`, so the agent calls the workflow it sits in, which
// answers through its Execute Workflow Trigger.
const recursiveAgentFixture = `{
  "name": "Recursive agent",
  "nodes": [
    {"id":"a","name":"When chat message received","type":"@n8n/n8n-nodes-langchain.chatTrigger","typeVersion":1.1,"position":[0,0],"parameters":{"options":{}}},
    {"id":"t","name":"When Executed by Another Workflow","type":"n8n-nodes-base.executeWorkflowTrigger","typeVersion":1.1,"position":[0,300],"parameters":{"inputSource":"passthrough"}},
    {"id":"b","name":"AI Agent","type":"@n8n/n8n-nodes-langchain.agent","typeVersion":1.7,"position":[220,0],"parameters":{"options":{}}},
    {"id":"c","name":"OpenAI Chat Model","type":"@n8n/n8n-nodes-langchain.lmChatOpenAi","typeVersion":1.2,"position":[160,200],"parameters":{
      "model":{"__rl":true,"mode":"list","value":"gpt-4o-mini"},"options":{}
    }},
    {"id":"w","name":"Call this workflow","type":"@n8n/n8n-nodes-langchain.toolWorkflow","typeVersion":2,"position":[400,200],"parameters":{
      "name":"research","description":"Researches a sub-question with this same agent.",
      "workflowId":{"__rl":true,"mode":"id","value":"={{ $workflow.id }}"},
      "workflowInputs":{"mappingMode":"defineBelow","value":{},"matchingColumns":[],"schema":[],"attemptToConvertTypes":false,"convertFieldsToString":false}
    }},
    {"id":"s","name":"Answer","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[220,300],"parameters":{
      "assignments":{"assignments":[{"id":"x","name":"response","value":"={{ $json.query }}","type":"string"}]},"options":{}
    }}
  ],
  "connections": {
    "When chat message received": {"main": [[{"node":"AI Agent","type":"main","index":0}]]},
    "When Executed by Another Workflow": {"main": [[{"node":"Answer","type":"main","index":0}]]},
    "OpenAI Chat Model": {"ai_languageModel": [[{"node":"AI Agent","type":"ai_languageModel","index":0}]]},
    "Call this workflow": {"ai_tool": [[{"node":"AI Agent","type":"ai_tool","index":0}]]}
  }
}`

// TestASelfReferencingWorkflowToolImportsAsAValidLocator is the recursive
// agent. The self-reference was carried as a bare expression, which is not a
// resource locator, so the import reported nothing and validation then
// refused the tool with "workflowId is required".
func TestASelfReferencingWorkflowToolImportsAsAValidLocator(t *testing.T) {
	t.Parallel()

	result := importFixture(t, recursiveAgentFixture)
	tool := nodeByName(result.Document, "Call this workflow")
	if tool.Type != "kilasflow.workflowTool" {
		t.Fatalf("toolWorkflow imported as %q, want kilasflow.workflowTool", tool.Type)
	}
	locator, ok := property.ReadLocator(tool.Parameters["workflowId"])
	if !ok || locator.Mode != "id" {
		t.Fatalf("workflowId = %#v, want a By ID resource locator", tool.Parameters["workflowId"])
	}
	inner, _ := locator.Value.(map[string]any)
	if inner["mode"] != "expression" || inner["value"] != "{{ $workflow.id }}" {
		t.Errorf("locator value = %#v, want the $workflow.id expression", locator.Value)
	}
	for _, issue := range result.Unsupported {
		if issue.Severity == n8n.SeverityBlocking && issue.NodeName == "Call this workflow" {
			t.Errorf("blocking issue on the tool (%s): %s", issue.Field, issue.Reason)
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
		t.Fatalf("Compile() error = %v, want the recursive agent to validate", err)
	}

	// And it goes back out as the self-reference n8n wrote, not as an empty
	// locator: the expression now sits one level deeper.
	exported, err := n8n.Export(document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, node := range exported.Document.Nodes {
		if node.Name != "Call this workflow" {
			continue
		}
		locator, _ := node.Parameters["workflowId"].(map[string]any)
		if locator["value"] != "={{ $workflow.id }}" {
			t.Errorf("exported workflowId = %#v, want the $workflow.id self-reference", node.Parameters["workflowId"])
		}
	}
}
