package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// newSubworkflowAPI builds the server with the sub-workflow extractor the
// composition root wires. The shared newWorkflowAPI helper leaves it off, which
// is why the refusal this file pins went unnoticed as a 500: nothing in the API
// suite ever activated a caller whose target was not live.
func newSubworkflowAPI(t *testing.T) http.Handler {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "subworkflows.db"),
	}, log)
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, log); err != nil {
		t.Fatalf("database.Migrate() error = %v", err)
	}
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	return newTestServer(t, api.Deps{
		DB:           db,
		NodeRegistry: registry,
		Workflows:    repository.NewWorkflowStore(db.DB).WithSubworkflows(nodes.SubworkflowCalls),
		Executions:   repository.NewExecutionStore(db.DB),
	})
}

// subworkflowCaller is a manual trigger feeding one Execute Sub-workflow node
// that calls target.
func subworkflowCaller(name, target string) workflow.Document {
	document := validManualWorkflow(name)
	document.Nodes = append(document.Nodes, workflow.Node{
		ID: "call", Name: "Run billing", Type: nodes.ExecuteWorkflowNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"workflowId": map[string]any{"__rl": true, "mode": "id", "value": target}},
	})
	document.Connections = []workflow.Connection{{
		ID: "c1", Kind: workflow.ConnectionMain,
		Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
		Target: workflow.Endpoint{NodeID: "call", Port: "main"},
	}}
	return document
}

type subworkflowProblem struct {
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
	Errors []struct {
		Message  string `json:"message"`
		Location string `json:"location"`
		Value    struct {
			Code   string `json:"code"`
			NodeID string `json:"nodeId"`
		} `json:"value"`
	} `json:"errors"`
}

func postForProblem(t *testing.T, handler http.Handler, path string, wantStatus int) subworkflowProblem {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
	if recorder.Code != wantStatus {
		t.Fatalf("POST %s = %d, want %d (body: %s)", path, recorder.Code, wantStatus, recorder.Body)
	}
	if got, want := recorder.Header().Get("Content-Type"), "application/problem+json"; got != want {
		t.Errorf("POST %s content type = %q, want %q", path, got, want)
	}
	var problem subworkflowProblem
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode POST %s problem: %v (body: %s)", path, err, recorder.Body)
	}
	return problem
}

// Activating a parent whose sub-workflow is not active is a refusal about the
// document the caller wrote, not a server fault. It answers 422 with the node
// and the child named, so the caller can fix it from the response instead of
// from the server log.
func TestActivatingAParentWhoseSubworkflowIsInactiveAnswers422NamingTheNodeAndTheChild(t *testing.T) {
	handler := newSubworkflowAPI(t)

	child := createWorkflow(t, handler, validManualWorkflow("Billing child"))
	parent := createWorkflow(t, handler, subworkflowCaller("Parent", child.ID))

	problem := postForProblem(t, handler, "/api/v1/workflows/"+parent.ID+"/activate", http.StatusUnprocessableEntity)
	if problem.Status != http.StatusUnprocessableEntity {
		t.Errorf("problem status = %d, want 422", problem.Status)
	}
	for _, want := range []string{"Run billing", "call", "Billing child", child.ID, "not active", "activate that workflow first"} {
		if !strings.Contains(problem.Detail, want) {
			t.Errorf("problem detail = %q, want it to contain %q", problem.Detail, want)
		}
	}
	if strings.Contains(problem.Detail, "workflow operation failed") {
		t.Errorf("problem detail = %q, want the reason and not the generic 500 sentence", problem.Detail)
	}
	if len(problem.Errors) != 1 {
		t.Fatalf("problem errors = %#v, want exactly one issue", problem.Errors)
	}
	issue := problem.Errors[0]
	if issue.Value.Code != string(workflow.ErrorInvalidConfig) || issue.Value.NodeID != "call" {
		t.Errorf("issue value = %+v, want config.invalid on node call", issue.Value)
	}
	if want := "body/nodes/1/parameters/workflowId"; issue.Location != want {
		t.Errorf("issue location = %q, want %q", issue.Location, want)
	}
	if issue.Message != problem.Detail {
		t.Errorf("issue message = %q, want the same sentence as the detail %q", issue.Message, problem.Detail)
	}

	// The refusal changed nothing, and the fix it names works.
	stored := requestJSON[workflowResource](t, handler, http.MethodGet, "/api/v1/workflows/"+parent.ID, nil, http.StatusOK)
	if stored.Active {
		t.Error("the parent is active after a refused activation")
	}
	requestJSON[workflowResource](t, handler, http.MethodPost, "/api/v1/workflows/"+child.ID+"/activate", nil, http.StatusOK)
	activated := requestJSON[workflowResource](t, handler, http.MethodPost, "/api/v1/workflows/"+parent.ID+"/activate", nil, http.StatusOK)
	if !activated.Active {
		t.Error("the parent is not active after its child was activated")
	}
}

// A call to a workflow that no longer exists is the other refusal, and it needs
// a different fix, so it is a 422 with its own sentence rather than "not active".
func TestActivatingAParentWhoseSubworkflowIsGoneAnswers422SayingItDoesNotExist(t *testing.T) {
	handler := newSubworkflowAPI(t)

	parent := createWorkflow(t, handler, subworkflowCaller("Orphaned parent", "wf_gone"))
	problem := postForProblem(t, handler, "/api/v1/workflows/"+parent.ID+"/activate", http.StatusUnprocessableEntity)
	for _, want := range []string{"Run billing", "wf_gone", "does not exist"} {
		if !strings.Contains(problem.Detail, want) {
			t.Errorf("problem detail = %q, want it to contain %q", problem.Detail, want)
		}
	}
	if strings.Contains(problem.Detail, "not active") {
		t.Errorf("problem detail = %q, want a missing target reported as missing, not inactive", problem.Detail)
	}
	if len(problem.Errors) != 1 || problem.Errors[0].Value.Code != string(workflow.ErrorInvalidConfig) || problem.Errors[0].Value.NodeID != "call" {
		t.Errorf("problem errors = %#v, want one config.invalid issue on node call", problem.Errors)
	}
}

// A parent with two bad calls is told about both at once: one errors[] entry per
// node, each placeable on its own node, and both sentences in the detail.
func TestActivatingAParentWithTwoBadSubworkflowCallsNamesBothNodes(t *testing.T) {
	handler := newSubworkflowAPI(t)

	child := createWorkflow(t, handler, validManualWorkflow("Billing child"))
	document := subworkflowCaller("Two bad calls", child.ID)
	document.Nodes = append(document.Nodes, workflow.Node{
		ID: "second", Name: "Run archive", Type: nodes.ExecuteWorkflowNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"workflowId": map[string]any{"__rl": true, "mode": "id", "value": "wf_gone"}},
	})
	document.Connections = append(document.Connections, workflow.Connection{
		ID: "c2", Kind: workflow.ConnectionMain,
		Source: workflow.Endpoint{NodeID: "call", Port: "main"},
		Target: workflow.Endpoint{NodeID: "second", Port: "main"},
	})
	parent := createWorkflow(t, handler, document)

	problem := postForProblem(t, handler, "/api/v1/workflows/"+parent.ID+"/activate", http.StatusUnprocessableEntity)
	for _, want := range []string{"Run billing", "Billing child", "not active", "Run archive", "wf_gone", "does not exist"} {
		if !strings.Contains(problem.Detail, want) {
			t.Errorf("problem detail = %q, want it to contain %q", problem.Detail, want)
		}
	}
	if len(problem.Errors) != 2 {
		t.Fatalf("problem errors = %#v, want one entry per bad node", problem.Errors)
	}
	for index, want := range []struct{ node, location string }{
		{"call", "body/nodes/1/parameters/workflowId"},
		{"second", "body/nodes/2/parameters/workflowId"},
	} {
		got := problem.Errors[index]
		if got.Value.NodeID != want.node || got.Value.Code != string(workflow.ErrorInvalidConfig) || got.Location != want.location {
			t.Errorf("errors[%d] = %+v, want config.invalid on node %s at %s", index, got, want.node, want.location)
		}
	}
}

// Publishing a revision goes through the same gate as activation, so it answers
// the same refusal rather than a 500 of its own.
func TestPublishingAParentWhoseSubworkflowIsInactiveAnswersTheSame422(t *testing.T) {
	handler := newSubworkflowAPI(t)

	child := createWorkflow(t, handler, validManualWorkflow("Billing child"))
	parent := createWorkflow(t, handler, subworkflowCaller("Parent", child.ID))

	path := "/api/v1/workflows/" + parent.ID + "/versions/" + parent.LatestVersion.ID + "/publish"
	problem := postForProblem(t, handler, path, http.StatusUnprocessableEntity)
	if !strings.Contains(problem.Detail, "Run billing") || !strings.Contains(problem.Detail, "Billing child") {
		t.Errorf("publish problem detail = %q, want the node and the child named", problem.Detail)
	}
}
