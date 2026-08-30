package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/api"
	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

type workflowResource struct {
	ID            string                   `json:"id"`
	Name          string                   `json:"name"`
	Active        bool                     `json:"active"`
	LatestVersion workflowVersionResource  `json:"latestVersion"`
	ActiveVersion *workflowVersionResource `json:"activeVersion"`
}

type workflowVersionResource struct {
	ID       string            `json:"id"`
	Revision int               `json:"revision"`
	Document workflow.Document `json:"document"`
}

type workflowSummary struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Active         bool   `json:"active"`
	LatestRevision int    `json:"latestRevision"`
}

type executionRequestResource struct {
	ID                string `json:"id"`
	WorkflowID        string `json:"workflowId"`
	WorkflowVersionID string `json:"workflowVersionId"`
	Status            string `json:"status"`
	Trigger           string `json:"trigger"`
}

func TestWorkflowAPICreatesListsUpdatesAndDeletesDrafts(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)
	created := createWorkflow(t, handler, validManualWorkflow("First draft"))
	if created.ID == "" || created.LatestVersion.Revision != 1 {
		t.Fatalf("created workflow = %#v, want server ID and revision 1", created)
	}
	if got, want := created.LatestVersion.Document.ID, created.ID; got != want {
		t.Errorf("persisted document ID = %q, want %q", got, want)
	}

	got := requestJSON[workflowResource](t, handler, http.MethodGet, "/api/v1/workflows/"+created.ID, nil, http.StatusOK)
	if got.LatestVersion.Revision != 1 || got.Name != "First draft" {
		t.Errorf("GET workflow = %#v, want first revision", got)
	}

	updated := requestJSON[workflowResource](t, handler, http.MethodPut, "/api/v1/workflows/"+created.ID, validManualWorkflow("Second draft"), http.StatusOK)
	if got, want := updated.LatestVersion.Revision, 2; got != want {
		t.Errorf("updated revision = %d, want %d", got, want)
	}

	summaries := requestJSON[[]workflowSummary](t, handler, http.MethodGet, "/api/v1/workflows", nil, http.StatusOK)
	if got, want := len(summaries), 1; got != want {
		t.Fatalf("workflow list length = %d, want %d", got, want)
	}
	if summaries[0].ID != created.ID || summaries[0].LatestRevision != 2 {
		t.Errorf("workflow list = %#v, want updated workflow summary", summaries)
	}

	requestJSON[struct{}](t, handler, http.MethodDelete, "/api/v1/workflows/"+created.ID, nil, http.StatusNoContent)
	requestProblem(t, handler, http.MethodGet, "/api/v1/workflows/"+created.ID, nil, http.StatusNotFound)
}

func TestWorkflowAPIActivatesAndQueuesOnlyLatestValidDraft(t *testing.T) {
	handler, workflows, executions := newWorkflowAPI(t)
	created := createWorkflow(t, handler, validManualWorkflow("Runnable"))

	activated := requestJSON[workflowResource](t, handler, http.MethodPost, "/api/v1/workflows/"+created.ID+"/activate", nil, http.StatusOK)
	if !activated.Active || activated.ActiveVersion == nil || activated.ActiveVersion.Revision != 1 {
		t.Fatalf("activation result = %#v, want active revision 1", activated)
	}

	run := requestJSON[executionRequestResource](t, handler, http.MethodPost, "/api/v1/workflows/"+created.ID+"/run", map[string]any{"input": map[string]any{"customer": "Ada"}}, http.StatusAccepted)
	if run.Status != string(execution.StatusQueued) || run.Trigger != string(execution.TriggerManual) || run.WorkflowVersionID != activated.LatestVersion.ID {
		t.Errorf("run request = %#v, want queued manual request pinned to latest revision", run)
	}
	persisted, err := executions.Get(context.Background(), repository.TenantScope{ID: repository.DefaultTenantID}, run.ID)
	if err != nil {
		t.Fatalf("load queued execution = %v", err)
	}
	if persisted.WorkflowVersionID != activated.LatestVersion.ID {
		t.Errorf("persisted execution version = %q, want %q", persisted.WorkflowVersionID, activated.LatestVersion.ID)
	}

	invalid := validManualWorkflow("Invalid latest draft")
	invalid.Nodes[0].Type = "kilasflow.unknown"
	updated := requestJSON[workflowResource](t, handler, http.MethodPut, "/api/v1/workflows/"+created.ID, invalid, http.StatusOK)
	if got, want := updated.LatestVersion.Revision, 2; got != want {
		t.Fatalf("invalid draft revision = %d, want %d", got, want)
	}
	requestProblem(t, handler, http.MethodPost, "/api/v1/workflows/"+created.ID+"/activate", nil, http.StatusUnprocessableEntity)
	problem := requestValidationProblem(t, handler, http.MethodPost, "/api/v1/workflows/"+created.ID+"/run")
	if got, want := problem.Errors[0].Value.Code, string(workflow.ErrorUnknownNode); got != want {
		t.Errorf("run validation issue code = %q, want %q", got, want)
	}
	if got, want := problem.Errors[0].Value.NodeID, "manual"; got != want {
		t.Errorf("run validation issue node = %q, want %q", got, want)
	}

	stored, err := workflows.Get(context.Background(), repository.TenantScope{ID: repository.DefaultTenantID}, created.ID)
	if err != nil {
		t.Fatalf("Get() active workflow = %v", err)
	}
	if !stored.Active || stored.ActiveVersion == nil || stored.ActiveVersion.Revision != 1 {
		t.Errorf("invalid draft changed active snapshot = %#v, want revision 1 still active", stored)
	}

	first := requestJSON[workflowResource](t, handler, http.MethodPost, "/api/v1/workflows/"+created.ID+"/deactivate", nil, http.StatusOK)
	second := requestJSON[workflowResource](t, handler, http.MethodPost, "/api/v1/workflows/"+created.ID+"/deactivate", nil, http.StatusOK)
	if first.Active || second.Active {
		t.Errorf("deactivation must be idempotent, got %#v then %#v", first, second)
	}
}

func TestWorkflowAPIOpenAPIDocumentsLifecycleResponseStatuses(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)
	recorder := get(t, handler, "/api/openapi.json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET OpenAPI status = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}
	var document struct {
		Paths map[string]map[string]struct {
			Responses map[string]json.RawMessage `json:"responses"`
		} `json:"paths"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&document); err != nil {
		t.Fatalf("decode OpenAPI document = %v", err)
	}
	for _, test := range []struct {
		path, method, status string
	}{
		{"/api/v1/workflows", http.MethodPost, "201"},
		{"/api/v1/workflows/{id}", http.MethodDelete, "204"},
		{"/api/v1/workflows/{id}/run", http.MethodPost, "202"},
	} {
		path, found := document.Paths[test.path]
		if !found {
			t.Errorf("OpenAPI paths do not contain %q: %#v", test.path, document.Paths)
			continue
		}
		operation, found := path[strings.ToLower(test.method)]
		if !found {
			t.Errorf("OpenAPI methods for %q do not contain %q: %#v", test.path, test.method, path)
			continue
		}
		if _, found := operation.Responses[test.status]; !found {
			t.Errorf("OpenAPI %s %s responses = %#v, want %s", test.method, test.path, operation.Responses, test.status)
		}
	}
}

func newWorkflowAPI(t *testing.T) (http.Handler, *repository.GORMWorkflowStore, *repository.GORMExecutionStore) {
	t.Helper()
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "workflows.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, repository.Models()...); err != nil {
		t.Fatalf("database.Migrate() error = %v", err)
	}
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	workflows := repository.NewWorkflowStore(db.DB)
	executions := repository.NewExecutionStore(db.DB)
	return newTestServer(t, api.Deps{
		DB:           db,
		NodeRegistry: registry,
		Workflows:    workflows,
		Executions:   executions,
	}), workflows, executions
}

func createWorkflow(t *testing.T, handler http.Handler, document workflow.Document) workflowResource {
	t.Helper()
	return requestJSON[workflowResource](t, handler, http.MethodPost, "/api/v1/workflows", document, http.StatusCreated)
}

func validManualWorkflow(name string) workflow.Document {
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		Name:          name,
		Nodes: []workflow.Node{{
			ID:          "manual",
			Name:        "Manual Trigger",
			Type:        "kilasflow.manual",
			TypeVersion: 1,
			Position:    workflow.Position{},
		}},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	}
}

func requestJSON[T any](t *testing.T, handler http.Handler, method, path string, body any, wantStatus int) T {
	t.Helper()
	var contents io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body = %v", err)
		}
		contents = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, contents)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != wantStatus {
		t.Fatalf("%s %s status = %d, want %d (body: %s)", method, path, recorder.Code, wantStatus, recorder.Body)
	}
	var decoded T
	if wantStatus != http.StatusNoContent {
		if err := json.NewDecoder(recorder.Body).Decode(&decoded); err != nil {
			t.Fatalf("decode %s %s response = %v", method, path, err)
		}
	}
	return decoded
}

func requestProblem(t *testing.T, handler http.Handler, method, path string, body any, wantStatus int) {
	t.Helper()
	var contents io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal problem request body = %v", err)
		}
		contents = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, contents)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != wantStatus {
		t.Fatalf("%s %s status = %d, want %d (body: %s)", method, path, recorder.Code, wantStatus, recorder.Body)
	}
	if got, want := recorder.Header().Get("Content-Type"), "application/problem+json"; got != want {
		t.Errorf("problem content type = %q, want %q", got, want)
	}
}

func requestValidationProblem(t *testing.T, handler http.Handler, method, path string) struct {
	Errors []struct {
		Value struct {
			Code   string `json:"code"`
			NodeID string `json:"nodeId"`
		} `json:"value"`
	} `json:"errors"`
} {
	t.Helper()
	request := httptest.NewRequest(method, path, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("%s %s status = %d, want 422 (body: %s)", method, path, recorder.Code, recorder.Body)
	}
	var problem struct {
		Errors []struct {
			Value struct {
				Code   string `json:"code"`
				NodeID string `json:"nodeId"`
			} `json:"value"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil {
		t.Fatalf("decode validation problem = %v", err)
	}
	if len(problem.Errors) == 0 {
		t.Fatalf("validation problem errors = %#v, want at least one issue", problem)
	}
	return problem
}
