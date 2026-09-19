package handlers

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// conflictRepository serves one stored revision and records whether a save
// was attempted. A 409 must refuse before anything is written.
type conflictRepository struct {
	stored workflow.StoredWorkflow
	saved  int
}

func (stub *conflictRepository) SaveDraft(context.Context, repository.TenantScope, workflow.Document) (workflow.StoredWorkflow, error) {
	stub.saved++
	return stub.stored, nil
}

func (stub *conflictRepository) List(context.Context, repository.TenantScope) ([]workflow.StoredWorkflow, error) {
	return nil, errors.New("List is not part of this test")
}

func (stub *conflictRepository) Get(context.Context, repository.TenantScope, string) (workflow.StoredWorkflow, error) {
	return stub.stored, nil
}

func (stub *conflictRepository) GetVersion(context.Context, repository.TenantScope, string, int) (workflow.Version, error) {
	return workflow.Version{}, errors.New("GetVersion is not part of this test")
}

func (stub *conflictRepository) GetVersionByID(context.Context, repository.TenantScope, string, string) (workflow.Version, error) {
	return workflow.Version{}, errors.New("GetVersionByID is not part of this test")
}

func (stub *conflictRepository) ListVersions(context.Context, repository.TenantScope, string, repository.VersionFilter) (repository.VersionPage, error) {
	return repository.VersionPage{}, errors.New("ListVersions is not part of this test")
}

func (stub *conflictRepository) Activate(context.Context, repository.TenantScope, string, workflow.Catalog) (workflow.StoredWorkflow, error) {
	return workflow.StoredWorkflow{}, errors.New("Activate is not part of this test")
}

func (stub *conflictRepository) PublishVersion(context.Context, repository.TenantScope, string, string, workflow.Catalog, string) (workflow.StoredWorkflow, error) {
	return workflow.StoredWorkflow{}, errors.New("PublishVersion is not part of this test")
}

func (stub *conflictRepository) RestoreVersion(context.Context, repository.TenantScope, string, string, string) (workflow.StoredWorkflow, error) {
	return workflow.StoredWorkflow{}, errors.New("RestoreVersion is not part of this test")
}

func (stub *conflictRepository) ListPublishEvents(context.Context, repository.TenantScope, string) ([]workflow.PublishEvent, error) {
	return nil, errors.New("ListPublishEvents is not part of this test")
}

func (stub *conflictRepository) Deactivate(context.Context, repository.TenantScope, string) (workflow.StoredWorkflow, error) {
	return workflow.StoredWorkflow{}, errors.New("Deactivate is not part of this test")
}

func (stub *conflictRepository) Delete(context.Context, repository.TenantScope, string) error {
	return errors.New("Delete is not part of this test")
}

func conflictStored() workflow.StoredWorkflow {
	return workflow.StoredWorkflow{
		ID:   "wf-conflict",
		Name: "Conflict fixture",
		LatestVersion: workflow.Version{
			ID: "v-new", WorkflowID: "wf-conflict", Revision: 2,
			Document: workflow.Document{ID: "wf-conflict", SchemaVersion: workflow.CurrentSchemaVersion, Name: "Conflict fixture"},
		},
	}
}

func conflictBody() workflowDocumentInput {
	return workflowDocumentInput{
		SchemaVersion: workflow.CurrentSchemaVersion,
		Name:          "Conflict fixture",
		Nodes:         []workflow.Node{},
		Connections:   []workflow.Connection{},
		Settings:      map[string]any{},
	}
}

func TestUpdateRefusesStaleBaseVersionWithConflict(t *testing.T) {
	store := &conflictRepository{stored: conflictStored()}
	handler := NewWorkflows(store, nil, nil, nil, nil)

	body := conflictBody()
	body.BaseVersionID = "v-old"
	_, err := handler.Update(context.Background(), &updateWorkflowInput{ID: "wf-conflict", Body: body})
	if err == nil {
		t.Fatal("Update() with a stale base version succeeded, want 409")
	}
	model, ok := err.(*huma.ErrorModel)
	if !ok {
		t.Fatalf("Update() error = %T, want *huma.ErrorModel", err)
	}
	if model.Status != http.StatusConflict {
		t.Fatalf("Update() status = %d, want 409", model.Status)
	}
	if store.saved != 0 {
		t.Fatalf("Update() saved %d revision(s) before refusing, want 0", store.saved)
	}
}

func TestUpdateAcceptsCurrentBaseVersion(t *testing.T) {
	store := &conflictRepository{stored: conflictStored()}
	handler := NewWorkflows(store, nil, nil, nil, nil)

	body := conflictBody()
	body.BaseVersionID = "v-new"
	if _, err := handler.Update(context.Background(), &updateWorkflowInput{ID: "wf-conflict", IfMatch: "v-new", Body: body}); err != nil {
		t.Fatalf("Update() with the current base version error = %v", err)
	}
	if store.saved != 1 {
		t.Fatalf("Update() saved %d revision(s), want 1", store.saved)
	}
}
