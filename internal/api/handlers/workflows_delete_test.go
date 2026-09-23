package handlers

import (
	"context"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/webhook"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// stubWorkflowRepository is a WorkflowRepository that only deletes and reads
// whether the workflow is active. Those are the two paths under test; every
// other method fails loudly so a test that strays onto one cannot pass by
// accident.
type stubWorkflowRepository struct {
	deleted []string
	// active is what Get reports back, so a test can drive Delete's decision
	// to run the lifecycle hook without a real store.
	active bool
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

func (stub *stubWorkflowRepository) Get(_ context.Context, tenant repository.TenantScope, workflowID string) (workflow.StoredWorkflow, error) {
	return workflow.StoredWorkflow{ID: workflowID, TenantID: tenant.ID, Active: stub.active}, nil
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

// deactivatedCall is one recorded invocation of TriggerCoordinator.Deactivated.
type deactivatedCall struct {
	tenantID   string
	workflowID string
}

// recordingTriggerCoordinator is a TriggerCoordinator that only deactivates.
// Activated is not part of this test, so it panics if Delete ever reaches for
// it — Delete has no reason to register anything.
type recordingTriggerCoordinator struct {
	deactivated []deactivatedCall
}

func (coordinator *recordingTriggerCoordinator) Activated(context.Context, string, string, map[string]string) ([]webhook.Notice, error) {
	panic("Activated is not part of this test")
}

func (coordinator *recordingTriggerCoordinator) Deactivated(_ context.Context, tenantID, workflowID string, _ map[string]string) {
	coordinator.deactivated = append(coordinator.deactivated, deactivatedCall{tenantID: tenantID, workflowID: workflowID})
}

// TestDeletingAnActiveWorkflowRunsItsTriggersRemoveHook is BUG-d2t3kp: an
// active workflow's trigger must be told to unregister before the workflow
// disappears, exactly as Deactivate already does, or the remote service is
// left delivering to a route that no longer resolves.
func TestDeletingAnActiveWorkflowRunsItsTriggersRemoveHook(t *testing.T) {
	t.Parallel()

	store := &stubWorkflowRepository{active: true}
	triggers := &recordingTriggerCoordinator{}
	handler := NewWorkflows(store, nil, nil, nil, nil).WithTriggers(triggers)

	if _, err := handler.Delete(context.Background(), &workflowPathInput{ID: "wf-1"}); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(triggers.deactivated) != 1 {
		t.Fatalf("Deactivated ran %d times, want exactly once for an active workflow", len(triggers.deactivated))
	}
	if got := triggers.deactivated[0]; got.tenantID != "default" || got.workflowID != "wf-1" {
		t.Fatalf("Deactivated call = %+v, want tenant %q workflow %q", got, "default", "wf-1")
	}
	if len(store.deleted) != 1 {
		t.Fatalf("deleted = %#v, want the workflow removed", store.deleted)
	}
}

// TestDeletingAnInactiveWorkflowRunsNoLifecycleHook keeps the common case
// cheap: a workflow that was never listening has nothing to unregister.
func TestDeletingAnInactiveWorkflowRunsNoLifecycleHook(t *testing.T) {
	t.Parallel()

	store := &stubWorkflowRepository{active: false}
	triggers := &recordingTriggerCoordinator{}
	handler := NewWorkflows(store, nil, nil, nil, nil).WithTriggers(triggers)

	if _, err := handler.Delete(context.Background(), &workflowPathInput{ID: "wf-1"}); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(triggers.deactivated) != 0 {
		t.Fatalf("Deactivated ran %d times, want none for an inactive workflow", len(triggers.deactivated))
	}
	if len(store.deleted) != 1 {
		t.Fatalf("deleted = %#v, want the workflow removed", store.deleted)
	}
}
