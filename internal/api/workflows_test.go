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
	"time"

	"github.com/kilaslabs/kilas-flow/internal/api"
	"github.com/kilaslabs/kilas-flow/internal/api/handlers"
	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/embed"
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

type recordingExecutionController struct {
	record      execution.Record
	cancelCalls []string
	getCalls    []string
	wakeCalls   int
}

func (controller *recordingExecutionController) Wake() {
	controller.wakeCalls++
}

func (controller *recordingExecutionController) Cancel(_ context.Context, _ repository.TenantScope, executionID string) (execution.Record, error) {
	controller.cancelCalls = append(controller.cancelCalls, executionID)
	return controller.record, nil
}

func (controller *recordingExecutionController) Get(_ context.Context, _ repository.TenantScope, executionID string) (execution.Record, error) {
	controller.getCalls = append(controller.getCalls, executionID)
	return controller.record, nil
}

// saveBeforeExecutionStore simulates a draft save at the exact point a manual
// run is about to persist. The legacy handler read revision 1, then called
// Create, so this interleaving made it queue stale work. QueueManualLatest
// must instead select revision 2 inside its own transaction.
type saveBeforeExecutionStore struct {
	*repository.GORMExecutionStore
	before func() error
	ran    bool
}

func (store *saveBeforeExecutionStore) beforePersist() error {
	if store.ran {
		return nil
	}
	store.ran = true
	return store.before()
}

func (store *saveBeforeExecutionStore) Create(ctx context.Context, tenant repository.TenantScope, record execution.Record) (execution.Record, error) {
	if err := store.beforePersist(); err != nil {
		return execution.Record{}, err
	}
	return store.GORMExecutionStore.Create(ctx, tenant, record)
}

func (store *saveBeforeExecutionStore) QueueManualLatest(ctx context.Context, tenant repository.TenantScope, workflowID string, catalog workflow.Catalog, input json.RawMessage) (execution.Record, error) {
	if err := store.beforePersist(); err != nil {
		return execution.Record{}, err
	}
	return store.GORMExecutionStore.QueueManualLatest(ctx, tenant, workflowID, catalog, input)
}

// workflowDraftRequest is the public write shape. Workflow identifiers belong
// to the URL and server responses, not draft request bodies.
type workflowDraftRequest struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Name          string                `json:"name"`
	Nodes         []workflow.Node       `json:"nodes"`
	Connections   []workflow.Connection `json:"connections"`
	Settings      map[string]any        `json:"settings"`
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

	updated := requestJSON[workflowResource](t, handler, http.MethodPut, "/api/v1/workflows/"+created.ID, workflowDraft(validManualWorkflow("Second draft")), http.StatusOK)
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

func TestExecutionAPICancelsExecutionThroughRuntimeService(t *testing.T) {
	controller := &recordingExecutionController{record: execution.Record{
		ID: "exec-cancel", WorkflowID: "wf-1", WorkflowVersionID: "wfv-1", Status: execution.StatusCancelling, Trigger: execution.TriggerManual,
	}}
	handler := newTestServer(t, api.Deps{DB: stubPinger{}, ExecutionController: controller})

	got := requestJSON[executionRequestResource](t, handler, http.MethodPost, "/api/v1/executions/exec-cancel/cancel", nil, http.StatusAccepted)
	if got.ID != "exec-cancel" || got.Status != string(execution.StatusCancelling) {
		t.Errorf("cancel response = %#v, want cancelling execution", got)
	}
	if len(controller.cancelCalls) != 1 || controller.cancelCalls[0] != "exec-cancel" {
		t.Errorf("cancelled execution IDs = %v, want [exec-cancel]", controller.cancelCalls)
	}

	recorded := requestJSON[struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}](t, handler, http.MethodGet, "/api/v1/executions/exec-cancel", nil, http.StatusOK)
	if recorded.ID != "exec-cancel" || recorded.Status != string(execution.StatusCancelling) {
		t.Errorf("execution response = %#v, want cancelling execution", recorded)
	}
	if len(controller.getCalls) != 1 || controller.getCalls[0] != "exec-cancel" {
		t.Errorf("loaded execution IDs = %v, want [exec-cancel]", controller.getCalls)
	}
}

func TestWorkflowAPIWakesRuntimeAfterQueuingManualRun(t *testing.T) {
	controller := &recordingExecutionController{}
	handler, _, _ := newWorkflowAPIWithController(t, controller)
	created := createWorkflow(t, handler, validManualWorkflow("Wake worker"))

	requestJSON[executionRequestResource](t, handler, http.MethodPost, "/api/v1/workflows/"+created.ID+"/run", nil, http.StatusAccepted)
	if got, want := controller.wakeCalls, 1; got != want {
		t.Errorf("runtime wake calls = %d, want %d", got, want)
	}
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
	updated := requestJSON[workflowResource](t, handler, http.MethodPut, "/api/v1/workflows/"+created.ID, workflowDraft(invalid), http.StatusOK)
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

func TestWorkflowAPIManualRunPinsRevisionCurrentAtPersistence(t *testing.T) {
	_, workflows, executions := newWorkflowAPI(t)
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	initialHandler := newTestServer(t, api.Deps{
		DB:           stubPinger{},
		NodeRegistry: registry,
		Workflows:    workflows,
		Executions:   executions,
	})
	created := createWorkflow(t, initialHandler, validManualWorkflow("Revision one"))

	latestDraft := validManualWorkflow("Revision two")
	latestDraft.ID = created.ID
	interleavingExecutions := &saveBeforeExecutionStore{
		GORMExecutionStore: executions,
		before: func() error {
			_, err := workflows.SaveDraft(context.Background(), repository.TenantScope{ID: repository.DefaultTenantID}, latestDraft)
			return err
		},
	}
	handler := newTestServer(t, api.Deps{
		DB:           stubPinger{},
		NodeRegistry: registry,
		Workflows:    workflows,
		Executions:   interleavingExecutions,
	})

	run := requestJSON[executionRequestResource](t, handler, http.MethodPost, "/api/v1/workflows/"+created.ID+"/run", nil, http.StatusAccepted)
	stored, err := workflows.Get(context.Background(), repository.TenantScope{ID: repository.DefaultTenantID}, created.ID)
	if err != nil {
		t.Fatalf("Get() after run = %v", err)
	}
	if got, want := stored.LatestVersion.Revision, 2; got != want {
		t.Fatalf("latest revision = %d, want %d", got, want)
	}
	if got, want := run.WorkflowVersionID, stored.LatestVersion.ID; got != want {
		t.Errorf("queued execution version = %q, want current revision %q", got, want)
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
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
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
		{"/api/v1/executions/{id}/cancel", http.MethodPost, "202"},
		{"/api/v1/executions/{id}", http.MethodGet, "200"},
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
	input, found := document.Components.Schemas["WorkflowDocumentInput"]
	if !found {
		t.Fatalf("OpenAPI schemas do not contain WorkflowDocumentInput: %#v", document.Components.Schemas)
	}
	if _, exposesID := input.Properties["id"]; exposesID {
		t.Error("workflow write schema exposes server-owned id")
	}
}

func newWorkflowAPI(t *testing.T) (http.Handler, *repository.GORMWorkflowStore, *repository.GORMExecutionStore) {
	return newWorkflowAPIWithController(t, nil)
}

// newWorkflowAPIWithEmbed builds one server that has the real repositories and
// an embed issuer, so an embed test exercises the same handlers the dashboard
// uses rather than a parallel stack.
func newWorkflowAPIWithEmbed(t *testing.T, issuer *embed.Issuer) (http.Handler, string) {
	t.Helper()
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "embed.db"),
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
	executions := repository.NewExecutionStore(db.DB)
	handler := newTestServer(t, api.Deps{
		DB:           db,
		NodeRegistry: registry,
		Workflows:    repository.NewWorkflowStore(db.DB),
		Executions:   executions,
		Credentials:  repository.NewCredentialStore(db.DB, nil),
		EmbedIssuer:  issuer,
	})
	created := createWorkflow(t, handler, validManualWorkflow("Embeddable"))
	return handler, created.ID
}

func newWorkflowAPIWithController(t *testing.T, controller handlers.ExecutionController) (http.Handler, *repository.GORMWorkflowStore, *repository.GORMExecutionStore) {
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
		DB:                  db,
		NodeRegistry:        registry,
		Workflows:           workflows,
		Executions:          executions,
		ExecutionController: controller,
	}), workflows, executions
}

func createWorkflow(t *testing.T, handler http.Handler, document workflow.Document) workflowResource {
	t.Helper()
	return requestJSON[workflowResource](t, handler, http.MethodPost, "/api/v1/workflows", workflowDraft(document), http.StatusCreated)
}

func workflowDraft(document workflow.Document) workflowDraftRequest {
	return workflowDraftRequest{
		SchemaVersion: document.SchemaVersion,
		Name:          document.Name,
		Nodes:         document.Nodes,
		Connections:   document.Connections,
		Settings:      document.Settings,
	}
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

type executionSummary struct {
	ID                string `json:"id"`
	WorkflowID        string `json:"workflowId"`
	WorkflowVersionID string `json:"workflowVersionId"`
	Status            string `json:"status"`
	Trigger           string `json:"trigger"`
	StartedAt         string `json:"startedAt"`
	FinishedAt        string `json:"finishedAt"`
	DurationMs        *int64 `json:"durationMs"`
}

type executionListResource struct {
	Items      []executionSummary `json:"items"`
	NextCursor string             `json:"nextCursor"`
}

func TestExecutionAPIListsHistoryNewestFirstWithFiltersAndPaging(t *testing.T) {
	handler, _, executions := newWorkflowAPI(t)
	created := createWorkflow(t, handler, validManualWorkflow("History"))
	other := createWorkflow(t, handler, validManualWorkflow("Other history"))

	tenant := repository.TenantScope{ID: repository.DefaultTenantID}
	base := time.Now().UTC().Add(-time.Hour)
	queued := make([]string, 0, 3)
	for index, status := range []execution.Status{execution.StatusSucceeded, execution.StatusFailed, execution.StatusSucceeded} {
		record, err := executions.Create(context.Background(), tenant, execution.Record{
			WorkflowID:        created.ID,
			WorkflowVersionID: created.LatestVersion.ID,
			Status:            status,
			Trigger:           execution.TriggerManual,
			StartedAt:         base.Add(time.Duration(index) * time.Minute),
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		queued = append(queued, record.ID)
	}
	if _, err := executions.Create(context.Background(), tenant, execution.Record{
		WorkflowID:        other.ID,
		WorkflowVersionID: other.LatestVersion.ID,
		Status:            execution.StatusQueued,
		Trigger:           execution.TriggerSchedule,
		StartedAt:         base.Add(10 * time.Minute),
	}); err != nil {
		t.Fatalf("Create() other workflow error = %v", err)
	}

	page := requestJSON[executionListResource](t, handler, http.MethodGet, "/api/v1/executions?limit=2", nil, http.StatusOK)
	if len(page.Items) != 2 {
		t.Fatalf("page items = %d, want 2", len(page.Items))
	}
	if page.Items[0].Trigger != string(execution.TriggerSchedule) {
		t.Errorf("first item trigger = %q, want the newest execution", page.Items[0].Trigger)
	}
	if page.NextCursor == "" {
		t.Fatal("expected a next cursor while more history remains")
	}

	next := requestJSON[executionListResource](t, handler, http.MethodGet, "/api/v1/executions?limit=2&cursor="+page.NextCursor, nil, http.StatusOK)
	if len(next.Items) != 2 || next.Items[0].ID != queued[1] || next.Items[1].ID != queued[0] {
		t.Fatalf("second page = %#v, want the two oldest executions", next.Items)
	}

	byWorkflow := requestJSON[executionListResource](t, handler, http.MethodGet, "/api/v1/executions?workflowId="+created.ID+"&status=succeeded", nil, http.StatusOK)
	if len(byWorkflow.Items) != 2 {
		t.Fatalf("filtered items = %d, want 2", len(byWorkflow.Items))
	}
	for _, item := range byWorkflow.Items {
		if item.WorkflowID != created.ID || item.Status != string(execution.StatusSucceeded) {
			t.Errorf("filter leaked %#v", item)
		}
	}

	requestProblem(t, handler, http.MethodGet, "/api/v1/executions?cursor=not-a-real-cursor", nil, http.StatusBadRequest)
	requestProblem(t, handler, http.MethodGet, "/api/v1/executions?status=not-a-status", nil, http.StatusUnprocessableEntity)
}

func TestExecutionAPIReportsDurationForFinishedRuns(t *testing.T) {
	handler, _, executions := newWorkflowAPI(t)
	created := createWorkflow(t, handler, validManualWorkflow("Duration"))

	startedAt := time.Now().UTC().Add(-time.Minute)
	finishedAt := startedAt.Add(1500 * time.Millisecond)
	if _, err := executions.Create(context.Background(), repository.TenantScope{ID: repository.DefaultTenantID}, execution.Record{
		WorkflowID:        created.ID,
		WorkflowVersionID: created.LatestVersion.ID,
		Status:            execution.StatusSucceeded,
		Trigger:           execution.TriggerManual,
		StartedAt:         startedAt,
		FinishedAt:        &finishedAt,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	page := requestJSON[executionListResource](t, handler, http.MethodGet, "/api/v1/executions", nil, http.StatusOK)
	if len(page.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(page.Items))
	}
	if page.Items[0].DurationMs == nil || *page.Items[0].DurationMs != 1500 {
		t.Errorf("durationMs = %v, want 1500", page.Items[0].DurationMs)
	}
}

func TestExecutionAPIRedactsCredentialsInResponses(t *testing.T) {
	finished := time.Now().UTC()
	controller := &recordingExecutionController{record: execution.Record{
		ID: "exec-secret", WorkflowID: "wf-1", WorkflowVersionID: "wfv-1",
		Status: execution.StatusSucceeded, Trigger: execution.TriggerWebhook,
		Input:      json.RawMessage(`{"headers":{"Authorization":"Bearer super-secret-token"}}`),
		Output:     json.RawMessage(`{"cookie":"sid=leaky"}`),
		FinishedAt: &finished,
		NodeRuns: []execution.NodeRun{{
			NodeID: "http", Attempt: 1, Sequence: 1, Status: execution.StatusSucceeded,
			Input: json.RawMessage(`{"apiKey":"sk-live-99"}`),
		}},
	}}
	handler := newTestServer(t, api.Deps{DB: stubPinger{}, ExecutionController: controller})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/executions/exec-secret", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}
	body := recorder.Body.String()
	for _, secret := range []string{"super-secret-token", "sid=leaky", "sk-live-99"} {
		if strings.Contains(body, secret) {
			t.Errorf("execution response leaked %q: %s", secret, body)
		}
	}
}

func TestWorkflowAPIReturnsThePinnedRevisionAnExecutionRan(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)
	created := createWorkflow(t, handler, validManualWorkflow("Pinned"))
	pinnedVersionID := created.LatestVersion.ID

	updated := requestJSON[workflowResource](t, handler, http.MethodPut, "/api/v1/workflows/"+created.ID, workflowDraft(validManualWorkflow("Renamed")), http.StatusOK)
	if updated.LatestVersion.ID == pinnedVersionID {
		t.Fatal("expected the update to create a new revision")
	}

	// An execution inspector replays the revision that actually ran, so reading
	// a superseded version by ID must keep working after later saves.
	version := requestJSON[workflowVersionResource](t, handler, http.MethodGet,
		"/api/v1/workflows/"+created.ID+"/versions/"+pinnedVersionID, nil, http.StatusOK)
	if version.ID != pinnedVersionID || version.Revision != 1 || version.Document.Name != "Pinned" {
		t.Fatalf("pinned version = %#v, want revision 1 named Pinned", version)
	}

	requestProblem(t, handler, http.MethodGet, "/api/v1/workflows/"+created.ID+"/versions/wfv_missing", nil, http.StatusNotFound)

	// A version ID belonging to a different workflow must not be readable
	// through this workflow's path.
	other := createWorkflow(t, handler, validManualWorkflow("Other"))
	requestProblem(t, handler, http.MethodGet, "/api/v1/workflows/"+created.ID+"/versions/"+other.LatestVersion.ID, nil, http.StatusNotFound)
}

type credentialResource struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	Fields         map[string]string `json:"fields"`
	AllowedDomains []string          `json:"allowedDomains"`
}

type credentialTypeResource struct {
	ID     string `json:"id"`
	Fields []struct {
		Key    string `json:"key"`
		Secret bool   `json:"secret"`
	} `json:"fields"`
}

func TestCredentialAPINeverDisclosesASecretAfterItIsStored(t *testing.T) {
	handler, credentialStore := newCredentialAPI(t)

	created := requestJSON[credentialResource](t, handler, http.MethodPost, "/api/v1/credentials", map[string]any{
		"name":           "Partner API",
		"type":           "httpBasicAuth",
		"fields":         map[string]string{"user": "ada", "password": "hunter2"},
		"allowedDomains": []string{"api.partner.test"},
	}, http.StatusCreated)

	if created.Fields["user"] != "ada" {
		t.Errorf("non-secret field = %q, want it returned", created.Fields["user"])
	}
	if strings.Contains(created.Fields["password"], "hunter2") {
		t.Fatalf("create response disclosed the secret: %#v", created.Fields)
	}

	// Neither list nor get may disclose it either, on any later request.
	listed := requestJSON[[]credentialResource](t, handler, http.MethodGet, "/api/v1/credentials", nil, http.StatusOK)
	fetched := requestJSON[credentialResource](t, handler, http.MethodGet, "/api/v1/credentials/"+created.ID, nil, http.StatusOK)
	for _, body := range []any{listed, fetched} {
		encoded, _ := json.Marshal(body)
		if strings.Contains(string(encoded), "hunter2") {
			t.Errorf("credential read disclosed the secret: %s", encoded)
		}
	}

	// The stored payload itself must be ciphertext, not JSON with the password.
	record, fields, err := credentialStore.Resolve(context.Background(), repository.TenantScope{ID: repository.DefaultTenantID}, created.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if fields["password"] != "hunter2" || record.Name != "Partner API" {
		t.Fatalf("Resolve() = (%#v, %#v), want the original payload", record, fields)
	}
}

func TestCredentialAPIKeepsAStoredSecretWhenOnlyTheNameChanges(t *testing.T) {
	handler, credentialStore := newCredentialAPI(t)
	created := requestJSON[credentialResource](t, handler, http.MethodPost, "/api/v1/credentials", map[string]any{
		"name": "Original", "type": "httpBearerAuth", "fields": map[string]string{"token": "keep-me"},
	}, http.StatusCreated)

	// The client only ever saw the placeholder, so sending it back must not
	// blank the stored token.
	requestJSON[credentialResource](t, handler, http.MethodPut, "/api/v1/credentials/"+created.ID, map[string]any{
		"name": "Renamed", "fields": map[string]string{"token": created.Fields["token"]},
	}, http.StatusOK)

	_, fields, err := credentialStore.Resolve(context.Background(), repository.TenantScope{ID: repository.DefaultTenantID}, created.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if fields["token"] != "keep-me" {
		t.Errorf("stored token = %q, want it preserved", fields["token"])
	}
}

func TestCredentialAPIRejectsInvalidPayloadsAndExposesTypeMetadata(t *testing.T) {
	handler, _ := newCredentialAPI(t)

	requestProblem(t, handler, http.MethodPost, "/api/v1/credentials", map[string]any{
		"name": "Missing password", "type": "httpBasicAuth", "fields": map[string]string{"user": "ada"},
	}, http.StatusUnprocessableEntity)
	requestProblem(t, handler, http.MethodPost, "/api/v1/credentials", map[string]any{
		"name": "Unknown type", "type": "nope", "fields": map[string]string{},
	}, http.StatusUnprocessableEntity)
	requestProblem(t, handler, http.MethodGet, "/api/v1/credentials/cred_missing", nil, http.StatusNotFound)

	types := requestJSON[[]credentialTypeResource](t, handler, http.MethodGet, "/api/v1/credential-types", nil, http.StatusOK)
	if len(types) == 0 {
		t.Fatal("credential types are not exposed")
	}
	for _, definition := range types {
		if len(definition.Fields) == 0 {
			t.Errorf("credential type %q has no field metadata", definition.ID)
		}
	}
}

func TestNodeCatalogueIncludesHTTPRequest(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)

	definitions := requestJSON[[]struct {
		Type       string `json:"type"`
		Parameters []struct {
			Key string `json:"key"`
		} `json:"parameters"`
	}](t, handler, http.MethodGet, "/api/v1/node-types", nil, http.StatusOK)

	for _, definition := range definitions {
		if definition.Type != "kilasflow.httpRequest" {
			continue
		}
		keys := map[string]bool{}
		for _, parameter := range definition.Parameters {
			keys[parameter.Key] = true
		}
		for _, required := range []string{"method", "url", "headers", "body", "responseFormat"} {
			if !keys[required] {
				t.Errorf("HTTP Request metadata is missing parameter %q", required)
			}
		}
		return
	}
	t.Fatal("HTTP Request is not in the node catalogue")
}

func newCredentialAPI(t *testing.T) (http.Handler, *repository.GORMCredentialStore) {
	t.Helper()
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "credentials.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, repository.Models()...); err != nil {
		t.Fatalf("database.Migrate() error = %v", err)
	}
	key := make([]byte, credentials.KeySize)
	for index := range key {
		key[index] = byte(index + 1)
	}
	cipher, err := credentials.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	store := repository.NewCredentialStore(db.DB, cipher)
	return newTestServer(t, api.Deps{DB: db, Credentials: store}), store
}
