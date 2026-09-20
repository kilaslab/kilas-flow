package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// WorkflowDiagnosticsStore is the optional half of the workflow repository that
// keeps an import report with the revision it describes.
//
// It is optional for the same reason WebhookRouteMinter is: a report belongs to
// the import boundary, and the repository seam is also used by callers — and by
// tests — that never import anything. A caller that needs it asserts this
// interface and answers for the absence, rather than WorkflowRepository
// widening and every implementation carrying a method it cannot answer.
type WorkflowDiagnosticsStore interface {
	// SaveDraftWithDiagnostics appends a revision carrying report, which is the
	// import adapter's own JSON. The report and the revision are written in one
	// transaction: a report without the document it describes, and a document
	// whose import went silently unrecorded, are both worse than a failed save.
	SaveDraftWithDiagnostics(ctx context.Context, tenant TenantScope, document workflow.Document, report json.RawMessage) (workflow.StoredWorkflow, error)
	// WorkflowDiagnostics reads the report stored with one revision. An empty
	// versionID means the newest revision, which is what an editor showing the
	// current draft asks for.
	WorkflowDiagnostics(ctx context.Context, tenant TenantScope, workflowID, versionID string) (RevisionDiagnostics, error)
}

// RevisionDiagnostics is one revision's stored import report.
type RevisionDiagnostics struct {
	// VersionID and Revision name the revision the report belongs to. A caller
	// that asked for "the newest revision" still has to be able to say which
	// snapshot it got — the draft may have moved on since the page loaded, and
	// a report shown against the wrong revision is worse than none.
	VersionID string
	Revision  int
	// Report is the stored JSON exactly as the importer wrote it, and is empty
	// when the revision was not created by an import.
	Report json.RawMessage
}

// SaveDraftWithDiagnostics stores an imported revision together with the report
// that describes the translation.
//
// An empty report is refused rather than treated as "no diagnostics": the
// caller asked for the variant that records a report, and storing nothing while
// answering 201 would reproduce exactly the bug this exists to fix. A
// translation that found nothing to report still writes its envelope, so the
// revision says an import happened.
func (store *GORMWorkflowStore) SaveDraftWithDiagnostics(ctx context.Context, tenant TenantScope, document workflow.Document, report json.RawMessage) (workflow.StoredWorkflow, error) {
	if len(report) == 0 {
		return workflow.StoredWorkflow{}, fmt.Errorf("an import report is required to save a draft with diagnostics")
	}
	return store.saveDraft(ctx, tenant, document, report)
}

// WorkflowDiagnostics reads the import report stored with one revision.
//
// The lookup is scoped by tenant and workflow, so a version ID from another
// tenant's workflow resolves to nothing rather than to somebody else's report.
func (store *GORMWorkflowStore) WorkflowDiagnostics(ctx context.Context, tenant TenantScope, workflowID, versionID string) (RevisionDiagnostics, error) {
	if err := tenant.validate(); err != nil {
		return RevisionDiagnostics{}, err
	}
	if workflowID == "" {
		return RevisionDiagnostics{}, fmt.Errorf("workflow ID is required")
	}

	query := store.db.WithContext(ctx).Where("tenant_id = ? AND workflow_id = ?", tenant.ID, workflowID)
	if versionID != "" {
		query = query.Where("id = ?", versionID)
	} else {
		query = query.Order("revision DESC").Limit(1)
	}

	var model workflowVersionModel
	if err := query.First(&model).Error; err != nil {
		return RevisionDiagnostics{}, mapNotFound(err, "workflow revision")
	}
	return RevisionDiagnostics{VersionID: model.ID, Revision: model.Revision, Report: model.Diagnostics}, nil
}
