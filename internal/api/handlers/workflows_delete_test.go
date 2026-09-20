package handlers

import (
	"context"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// stubWorkflowRepository is a WorkflowRepository that only deletes. Delete
// is the one path under test; every other method fails loudly so a test
// that strays onto one cannot pass by accident.
type stubWorkflowRepository struct {
	deleted []string
}

func (stub *stubWorkflowRepository) Delete(_ context.Context, tenant repository.TenantScope, workflowID string) error {
	stub.deleted = append(stub.deleted, tenant.ID+"/"+workflowID)
	return nil
}

func (stub *stubWorkflowRepository) SaveDraft(context.Context, repository.TenantScope, workflow.Document) (workflow.StoredWorkflow, error) {
	panic("SaveDraft is not part of this test")
}

func (stub *stubWorkflowRepository) ListSummaries(context.Context, repository.TenantScope, repository.WorkflowFilter) (repository.WorkflowSummaryPage, error) {
	panic("ListSummaries is not part of this test")
}

func (stub *stubWorkflowRepository) Get(context.Context, repository.TenantScope, string) (workflow.StoredWorkflow, error) {
	panic("Get is not part of this test")
}

func (stub *stubWorkflowRepository) GetVersion(context.Context, repository.TenantScope, string, int) (workflow.Version, error) {
	panic("GetVersion is not part of this test")
}

func (stub *stubWorkflowRepository) GetVersionByID(context.Context, repository.TenantScope, string, string) (workflow.Version, error) {
	panic("GetVersionByID is not part of this test")
}

func (stub *stubWorkflowRepository) ListVersions(context.Context, repository.TenantScope, string, repository.VersionFilter) (repository.VersionPage, error) {
	panic("ListVersions is not part of this test")
}

func (stub *stubWorkflowRepository) Activate(context.Context, repository.TenantScope, string, workflow.Catalog) (workflow.StoredWorkflow, error) {
	panic("Activate is not part of this test")
}

func (stub *stubWorkflowRepository) PublishVersion(context.Context, repository.TenantScope, string, string, workflow.Catalog, string) (workflow.StoredWorkflow, error) {
	panic("PublishVersion is not part of this test")
}

func (stub *stubWorkflowRepository) RestoreVersion(context.Context, repository.TenantScope, string, string, string) (workflow.StoredWorkflow, error) {
	panic("RestoreVersion is not part of this test")
}

func (stub *stubWorkflowRepository) ListPublishEvents(context.Context, repository.TenantScope, string) ([]workflow.PublishEvent, error) {
	panic("ListPublishEvents is not part of this test")
}

func (stub *stubWorkflowRepository) Deactivate(context.Context, repository.TenantScope, string) (workflow.StoredWorkflow, error) {
	panic("Deactivate is not part of this test")
}

type recordingSessions struct {
	forgotten []string
}

func (sessions *recordingSessions) ForgetWorkflow(tenantID, workflowID string) {
	sessions.forgotten = append(sessions.forgotten, tenantID+"/"+workflowID)
}

// TestDeleteDropsTheWorkflowsAgentSessions proves the delete path, not just
// the store: a deleted workflow's conversations must become unaddressable
// rather than lingering until retention ages them out.
func TestDeleteDropsTheWorkflowsAgentSessions(t *testing.T) {
	t.Parallel()

	store := &stubWorkflowRepository{}
	sessions := &recordingSessions{}
	handler := NewWorkflows(store, nil, nil, nil, nil).WithSessionMemory(sessions)

	if _, err := handler.Delete(context.Background(), &workflowPathInput{ID: "wf-1"}); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(store.deleted) != 1 {
		t.Fatalf("deleted = %#v, want the workflow removed first", store.deleted)
	}
	want := "default" + "/wf-1"
	if len(sessions.forgotten) != 1 || sessions.forgotten[0] != want {
		t.Fatalf("forgotten = %#v, want %q", sessions.forgotten, want)
	}
}

// TestDeleteWithoutASessionStoreStillDeletes keeps the attachment optional:
// composition without a memory store deletes exactly as before.
func TestDeleteWithoutASessionStoreStillDeletes(t *testing.T) {
	t.Parallel()

	store := &stubWorkflowRepository{}
	handler := NewWorkflows(store, nil, nil, nil, nil)

	if _, err := handler.Delete(context.Background(), &workflowPathInput{ID: "wf-1"}); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(store.deleted) != 1 {
		t.Fatalf("deleted = %#v, want the workflow removed", store.deleted)
	}
}
