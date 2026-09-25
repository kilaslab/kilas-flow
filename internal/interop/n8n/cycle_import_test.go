package n8n_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// BUG-gk7mf5: templates 2536 and 3655 imported with nothing blocking, and then
// run and activate both refused them with workflow.invalid_topology — while
// the migration guide promises that a report with no blocking entries means
// the workflow will activate.

// callbackLoopFixture is the shape of template 2536, written out by hand: a
// branch waits for a callback, acknowledges it, and routes back to the If that
// decides whether everything has finished. n8n runs that; KilasFlow refuses
// any cycle that does not close onto a Loop Over Items node.
const callbackLoopFixture = `{
  "name": "Wait for all callbacks",
  "nodes": [
    {"id": "hook", "name": "Webhook", "type": "n8n-nodes-base.webhook", "typeVersion": 2, "webhookId": "w1",
     "parameters": {"path": "start", "httpMethod": "POST", "responseMode": "responseNode"}},
    {"id": "init", "name": "Initialize finishedSet", "type": "n8n-nodes-base.set", "typeVersion": 3.4,
     "parameters": {"assignments": {"assignments": [{"id": "1", "name": "finished", "type": "number", "value": 0}]}}},
    {"id": "if", "name": "If All Finished", "type": "n8n-nodes-base.if", "typeVersion": 2,
     "parameters": {"conditions": {"conditions": [
       {"id": "1", "leftValue": "={{ $json.finished }}", "rightValue": 3,
        "operator": {"type": "number", "operation": "gte"}}
     ]}}},
    {"id": "wait", "name": "Webhook Callback Wait", "type": "n8n-nodes-base.wait", "typeVersion": 1.1,
     "parameters": {"amount": 1, "unit": "seconds"}},
    {"id": "code", "name": "Update finishedSet", "type": "n8n-nodes-base.code", "typeVersion": 2,
     "parameters": {"jsCode": "return $input.all().map(item => ({json: {finished: item.json.finished + 1}}));"}},
    {"id": "ack", "name": "Acknowledge Finished", "type": "n8n-nodes-base.respondToWebhook", "typeVersion": 1.1,
     "parameters": {"respondWith": "noData"}},
    {"id": "done", "name": "Continue Workflow", "type": "n8n-nodes-base.noOp", "typeVersion": 1}
  ],
  "connections": {
    "Webhook": {"main": [[{"node": "Initialize finishedSet", "type": "main", "index": 0}]]},
    "Initialize finishedSet": {"main": [[{"node": "If All Finished", "type": "main", "index": 0}]]},
    "If All Finished": {"main": [
      [{"node": "Continue Workflow", "type": "main", "index": 0}],
      [{"node": "Webhook Callback Wait", "type": "main", "index": 0}]
    ]},
    "Webhook Callback Wait": {"main": [[{"node": "Update finishedSet", "type": "main", "index": 0}]]},
    "Update finishedSet": {"main": [[{"node": "Acknowledge Finished", "type": "main", "index": 0}]]},
    "Acknowledge Finished": {"main": [[{"node": "If All Finished", "type": "main", "index": 0}]]}
  }
}`

func cycleIssues(issues []n8n.ImportIssue) []n8n.ImportIssue {
	var cycles []n8n.ImportIssue
	for _, issue := range issues {
		if issue.Field == "connections" && issue.Severity == n8n.SeverityBlocking {
			cycles = append(cycles, issue)
		}
	}
	return cycles
}

func TestImportReportsACycleThatRunAndActivateRefuse(t *testing.T) {
	t.Parallel()

	result := importFixture(t, callbackLoopFixture)
	cycles := cycleIssues(result.Unsupported)
	if len(cycles) != 1 {
		t.Fatalf("unsupported = %#v, want one blocking issue naming the cycle", result.Unsupported)
	}
	issue := cycles[0]
	// Named from where the flow enters the loop, in the order it runs.
	const path = `"If All Finished" → "Webhook Callback Wait" → "Update finishedSet" → "Acknowledge Finished" → "If All Finished"`
	if !strings.Contains(issue.Reason, path) {
		t.Errorf("reason = %q, want it to name the cycle %s", issue.Reason, path)
	}
	if !strings.Contains(issue.Reason, "Loop Over Items") {
		t.Errorf("reason = %q, want it to say which loops do run", issue.Reason)
	}
	if issue.NodeName != "If All Finished" || issue.NodeID != "if" || issue.Type != "n8n-nodes-base.if" {
		t.Errorf("issue = %#v, want it attached to the node the loop starts at", issue)
	}

	// And the report agrees with the run: the same document is refused by the
	// compiler that run and activate go through, for the cycle.
	document := result.Document
	document.ID = "wf_cycle"
	_, err := workflow.Compile(document, registry(t))
	var validationErrors *workflow.ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("Compile() error = %v, want the cycle refused", err)
	}
	refused := false
	for _, validation := range validationErrors.Issues {
		if validation.Code == workflow.ErrorInvalidTopology && validation.Path == "/connections" {
			refused = true
		}
	}
	if !refused {
		t.Errorf("Compile() issues = %#v, want workflow.invalid_topology for the cycle", validationErrors.Issues)
	}
}

// A back edge onto Loop Over Items is the loop the engine runs, so it must not
// be reported: a blocking entry on it would stop people activating a workflow
// that works.
func TestImportDoesNotReportALoopOverItemsBackEdge(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Batched",
	  "nodes": [
	    {"id": "a", "name": "Manual", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1},
	    {"id": "b", "name": "Loop Over Items", "type": "n8n-nodes-base.splitInBatches", "typeVersion": 3, "parameters": {"options": {}}},
	    {"id": "c", "name": "Body", "type": "n8n-nodes-base.set", "typeVersion": 3.4,
	     "parameters": {"assignments": {"assignments": [{"id": "1", "name": "seen", "type": "boolean", "value": true}]}}},
	    {"id": "d", "name": "Done", "type": "n8n-nodes-base.noOp", "typeVersion": 1}
	  ],
	  "connections": {
	    "Manual": {"main": [[{"node": "Loop Over Items", "type": "main", "index": 0}]]},
	    "Loop Over Items": {"main": [
	      [{"node": "Done", "type": "main", "index": 0}],
	      [{"node": "Body", "type": "main", "index": 0}]
	    ]},
	    "Body": {"main": [[{"node": "Loop Over Items", "type": "main", "index": 0}]]}
	  }
	}`)
	if cycles := cycleIssues(result.Unsupported); len(cycles) != 0 {
		t.Fatalf("cycle issues = %#v, want none for a loop that closes onto Loop Over Items", cycles)
	}
	document := result.Document
	document.ID = "wf_batched"
	if _, err := workflow.Compile(document, registry(t)); err != nil {
		t.Fatalf("Compile() error = %v, want the loop to activate", err)
	}
}

// The compiler checks for a cycle only once nothing else is wrong, and nearly
// every real import has something else wrong — a credential to re-bind. The
// report must name the cycle anyway, or it appears only after the credential
// is fixed, as a refusal nobody was warned about.
func TestImportReportsACycleBesideOtherBlockingIssues(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Polling",
	  "nodes": [
	    {"id": "a", "name": "Manual", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1},
	    {"id": "b", "name": "Get Task", "type": "n8n-nodes-base.httpRequest", "typeVersion": 4.2,
	     "credentials": {"httpHeaderAuth": {"id": "7", "name": "Task API"}},
	     "parameters": {"url": "https://example.com/task", "authentication": "genericCredentialType", "genericAuthType": "httpHeaderAuth"}},
	    {"id": "c", "name": "Is Ready", "type": "n8n-nodes-base.if", "typeVersion": 2,
	     "parameters": {"conditions": {"conditions": [
	       {"id": "1", "leftValue": "={{ $json.status }}", "rightValue": "done",
	        "operator": {"type": "string", "operation": "equals"}}
	     ]}}},
	    {"id": "d", "name": "Wait a Minute", "type": "n8n-nodes-base.wait", "typeVersion": 1.1, "parameters": {"amount": 1, "unit": "minutes"}},
	    {"id": "e", "name": "Use Result", "type": "n8n-nodes-base.noOp", "typeVersion": 1}
	  ],
	  "connections": {
	    "Manual": {"main": [[{"node": "Get Task", "type": "main", "index": 0}]]},
	    "Get Task": {"main": [[{"node": "Is Ready", "type": "main", "index": 0}]]},
	    "Is Ready": {"main": [
	      [{"node": "Use Result", "type": "main", "index": 0}],
	      [{"node": "Wait a Minute", "type": "main", "index": 0}]
	    ]},
	    "Wait a Minute": {"main": [[{"node": "Get Task", "type": "main", "index": 0}]]}
	  }
	}`)
	if _, found := issueFor(result.Unsupported, "Get Task", "credentials"); !found {
		t.Fatalf("unsupported = %#v, want the credential reported as well", result.Unsupported)
	}
	cycles := cycleIssues(result.Unsupported)
	if len(cycles) != 1 || !strings.Contains(cycles[0].Reason, `"Get Task" → "Is Ready" → "Wait a Minute" → "Get Task"`) {
		t.Fatalf("cycle issues = %#v, want the polling loop named", cycles)
	}
}
