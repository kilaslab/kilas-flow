package api_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

// importIssueResponse mirrors n8n.ImportIssue as the API documents it. The test
// decodes what the server really wrote rather than the adapter's own struct, so
// a report that survives storage but loses a field on the way out fails here.
type importIssueResponse struct {
	Severity    string  `json:"severity"`
	NodeName    string  `json:"nodeName,omitempty"`
	NodeID      string  `json:"nodeId,omitempty"`
	Field       string  `json:"field,omitempty"`
	Type        string  `json:"type,omitempty"`
	TypeVersion float64 `json:"typeVersion,omitempty"`
	Reason      string  `json:"reason"`
}

type importedWorkflowResponse struct {
	Workflow struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		LatestVersion struct {
			ID       string `json:"id"`
			Revision int    `json:"revision"`
		} `json:"latestVersion"`
	} `json:"workflow"`
	Unsupported []importIssueResponse `json:"unsupported"`
}

type workflowDiagnosticsResponse struct {
	WorkflowID string                `json:"workflowId"`
	VersionID  string                `json:"versionId"`
	Revision   int                   `json:"revision"`
	Source     string                `json:"source"`
	ImportedAt string                `json:"importedAt"`
	Issues     []importIssueResponse `json:"issues"`
}

// An n8n file with one node the adapter has no equivalent for, plus a trigger it
// does carry: the import has something to report and something to keep.
const n8nImportWithAHole = `{
  "name": "Imported with a hole",
  "nodes": [
    {"id": "a", "name": "On click", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1, "position": [0, 0], "parameters": {}},
    {"id": "b", "name": "Mystery", "type": "n8n-nodes-base.noSuchNode", "typeVersion": 3, "position": [220, 0], "parameters": {}}
  ],
  "connections": {}
}`

func importN8nWorkflow(t *testing.T, handler http.Handler, document string) importedWorkflowResponse {
	t.Helper()
	return requestJSON[importedWorkflowResponse](t, handler, http.MethodPost, "/api/v1/workflows/import",
		map[string]any{"format": "n8n", "workflow": json.RawMessage(document)}, http.StatusCreated)
}

// The report used to exist only in the import response, so it was gone the
// moment the dialog closed and the nodes it named were indistinguishable from
// the rest of the graph. This is the chain the ticket is about: import, save,
// read the report back for the revision the import created.
func TestAnImportedWorkflowReportsItsDiagnosticsAfterTheImportResponseIsGone(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)

	imported := importN8nWorkflow(t, handler, n8nImportWithAHole)
	if len(imported.Unsupported) == 0 {
		t.Fatal("the import answered with no diagnostics, so there is nothing to store")
	}

	diagnostics := requestJSON[workflowDiagnosticsResponse](t, handler, http.MethodGet,
		"/api/v1/workflows/"+imported.Workflow.ID+"/diagnostics", nil, http.StatusOK)

	if diagnostics.WorkflowID != imported.Workflow.ID {
		t.Errorf("diagnostics workflow = %q, want %q", diagnostics.WorkflowID, imported.Workflow.ID)
	}
	if diagnostics.VersionID != imported.Workflow.LatestVersion.ID {
		t.Errorf("diagnostics version = %q, want the imported revision %q", diagnostics.VersionID, imported.Workflow.LatestVersion.ID)
	}
	if diagnostics.Revision != imported.Workflow.LatestVersion.Revision {
		t.Errorf("diagnostics revision = %d, want %d", diagnostics.Revision, imported.Workflow.LatestVersion.Revision)
	}
	if diagnostics.Source != "n8n" || diagnostics.ImportedAt == "" {
		t.Errorf("diagnostics source = %q importedAt = %q, want the n8n import that produced this revision", diagnostics.Source, diagnostics.ImportedAt)
	}
	// The stored report is the response's report, not a summary of it: the
	// editor names the node and the reason it cannot run.
	if !reflect.DeepEqual(diagnostics.Issues, imported.Unsupported) {
		t.Errorf("stored diagnostics = %#v, want the import's own report %#v", diagnostics.Issues, imported.Unsupported)
	}
}

// A report belongs to the revision it was written for. Saving the draft again
// appends a revision nobody imported, and the editor must not present the
// earlier report as that revision's diagnostics — the nodes it names may have
// been edited or deleted since.
func TestADraftSaveLeavesTheImportReportOnTheRevisionItDescribe(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)

	imported := importN8nWorkflow(t, handler, n8nImportWithAHole)
	path := "/api/v1/workflows/" + imported.Workflow.ID

	document := validManualWorkflow("Edited after import")
	document.ID = imported.Workflow.ID
	requestJSON[workflowResource](t, handler, http.MethodPut, path, workflowDraft(document), http.StatusOK)

	latest := requestJSON[workflowDiagnosticsResponse](t, handler, http.MethodGet, path+"/diagnostics", nil, http.StatusOK)
	if latest.Revision != 2 {
		t.Errorf("latest diagnostics revision = %d, want the save's revision 2", latest.Revision)
	}
	if latest.Source != "" || len(latest.Issues) != 0 {
		t.Errorf("latest diagnostics = %#v, want none: revision 2 was not imported", latest)
	}

	original := requestJSON[workflowDiagnosticsResponse](t, handler, http.MethodGet,
		path+"/diagnostics?versionId="+imported.Workflow.LatestVersion.ID, nil, http.StatusOK)
	if !reflect.DeepEqual(original.Issues, imported.Unsupported) {
		t.Errorf("imported revision diagnostics = %#v, want %#v", original.Issues, imported.Unsupported)
	}
}

// The editor reads this route on every workflow it opens, including the ones
// nobody imported. "No import here" has to be an answer, not an error.
func TestAWorkflowBuiltInTheEditorHasNoImportReport(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)
	created := createWorkflow(t, handler, validManualWorkflow("Hand built"))

	diagnostics := requestJSON[workflowDiagnosticsResponse](t, handler, http.MethodGet,
		"/api/v1/workflows/"+created.ID+"/diagnostics", nil, http.StatusOK)
	if diagnostics.Source != "" || len(diagnostics.Issues) != 0 {
		t.Errorf("diagnostics = %#v, want no source and no issues", diagnostics)
	}
	if diagnostics.VersionID != created.LatestVersion.ID {
		t.Errorf("diagnostics version = %q, want the latest revision %q", diagnostics.VersionID, created.LatestVersion.ID)
	}

	requestProblem(t, handler, http.MethodGet, "/api/v1/workflows/wf_missing/diagnostics", nil, http.StatusNotFound)
	requestProblem(t, handler, http.MethodGet,
		"/api/v1/workflows/"+created.ID+"/diagnostics?versionId=wfv_missing", nil, http.StatusNotFound)
}
