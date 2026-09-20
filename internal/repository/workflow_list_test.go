package repository_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// listDocument is the smallest document SaveDraft accepts, with an identity the
// test chooses so the listing can be asserted by name rather than by position.
func listDocument(id, name string) workflow.Document {
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            id,
		Name:          name,
		Nodes: []workflow.Node{{
			ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual",
			TypeVersion: workflow.V(1), Position: workflow.Position{},
		}},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	}
}

// saveWorkflows stores n drafts, returning them in save order.
func saveWorkflows(t *testing.T, store *repository.GORMWorkflowStore, tenant repository.TenantScope, n int) []workflow.StoredWorkflow {
	t.Helper()

	saved := make([]workflow.StoredWorkflow, 0, n)
	for i := 1; i <= n; i++ {
		stored, err := store.SaveDraft(context.Background(), tenant, listDocument(fmt.Sprintf("wf_list_%d", i), fmt.Sprintf("Workflow %d", i)))
		if err != nil {
			t.Fatalf("SaveDraft(%d) error = %v", i, err)
		}
		saved = append(saved, stored)
	}
	return saved
}

// The list is what a tenant with many workflows hits on every dashboard load,
// so it has to stop at the page size and hand back the position of the next
// page rather than returning everything it can find.
func TestAWorkflowListingIsBoundedAndPagesWithoutRepeatingARow(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	saved := saveWorkflows(t, store, tenant, 5)

	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		page, err := store.ListSummaries(context.Background(), tenant, repository.WorkflowFilter{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("ListSummaries() error = %v", err)
		}
		if len(page.Workflows) > 2 {
			t.Fatalf("page carried %d workflows, want at most the limit of 2", len(page.Workflows))
		}
		for _, summary := range page.Workflows {
			if seen[summary.ID] {
				t.Fatalf("workflow %s appeared on two pages", summary.ID)
			}
			seen[summary.ID] = true
		}
		pages++
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
		if pages > 5 {
			t.Fatal("paging did not terminate")
		}
	}

	if len(seen) != len(saved) {
		t.Errorf("workflows seen across pages = %d, want %d", len(seen), len(saved))
	}
	for _, stored := range saved {
		if !seen[stored.ID] {
			t.Errorf("workflow %s never appeared in any page", stored.ID)
		}
	}
}

// A full first page carries a cursor and the last page does not, which is the
// whole contract a client pages with.
func TestAWorkflowListingHandsBackACursorOnlyWhileMoreRowsRemain(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	saveWorkflows(t, store, tenant, 3)

	first, err := store.ListSummaries(context.Background(), tenant, repository.WorkflowFilter{Limit: 3})
	if err != nil {
		t.Fatalf("ListSummaries() error = %v", err)
	}
	if len(first.Workflows) != 3 {
		t.Fatalf("first page carried %d workflows, want exactly the 3 saved", len(first.Workflows))
	}
	if first.NextCursor != "" {
		t.Errorf("cursor = %q on a page holding every row, want none", first.NextCursor)
	}

	second, err := store.ListSummaries(context.Background(), tenant, repository.WorkflowFilter{Limit: 2})
	if err != nil {
		t.Fatalf("ListSummaries() error = %v", err)
	}
	if second.NextCursor == "" {
		t.Fatal("a page that stopped short of the table carried no cursor")
	}

	last, err := store.ListSummaries(context.Background(), tenant, repository.WorkflowFilter{Cursor: second.NextCursor})
	if err != nil {
		t.Fatalf("ListSummaries(cursor) error = %v", err)
	}
	if len(last.Workflows) != 1 {
		t.Errorf("second page carried %d workflows, want the remaining 1", len(last.Workflows))
	}
	if last.NextCursor != "" {
		t.Errorf("cursor = %q after the final row, want none", last.NextCursor)
	}
}

// The dashboard renders these four fields, so a summary that lost one would be
// a visible regression even though no document is loaded any more.
func TestAWorkflowListingCarriesTheFieldsTheDashboardRenders(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	saved := saveWorkflows(t, store, tenant, 1)
	if _, err := store.Activate(context.Background(), tenant, saved[0].ID, historyCatalog()); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}

	page, err := store.ListSummaries(context.Background(), tenant, repository.WorkflowFilter{})
	if err != nil {
		t.Fatalf("ListSummaries() error = %v", err)
	}
	if len(page.Workflows) != 1 {
		t.Fatalf("workflows listed = %d, want 1", len(page.Workflows))
	}
	summary := page.Workflows[0]
	if summary.ID != saved[0].ID || summary.Name != "Workflow 1" {
		t.Errorf("summary = %+v, want the saved workflow's identity and name", summary)
	}
	if !summary.Active {
		t.Error("summary reports the workflow inactive after Activate")
	}
	if summary.LatestRevision != 1 {
		t.Errorf("latestRevision = %d, want 1", summary.LatestRevision)
	}
	if summary.UpdatedAt.IsZero() {
		t.Error("summary carries no updatedAt")
	}
}

// A cursor nobody handed out is ErrInvalidCursor, which the API answers with a
// 400: paging from an invented position would silently repeat or skip rows.
func TestAnUnrecognisedWorkflowCursorIsRejectedRatherThanIgnored(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	saveWorkflows(t, store, tenant, 1)

	for _, cursor := range []string{"not-a-cursor", "ISE=", ""} {
		if cursor == "" {
			continue
		}
		_, err := store.ListSummaries(context.Background(), tenant, repository.WorkflowFilter{Cursor: cursor})
		if !errors.Is(err, repository.ErrInvalidCursor) {
			t.Errorf("ListSummaries(cursor %q) error = %v, want ErrInvalidCursor", cursor, err)
		}
	}
}

// A limit past the maximum is clamped rather than honoured: the bound exists so
// one request cannot pull the whole table into memory.
func TestAWorkflowListingClampsAnExcessiveLimit(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	saveWorkflows(t, store, tenant, 2)

	page, err := store.ListSummaries(context.Background(), tenant, repository.WorkflowFilter{Limit: 1_000_000})
	if err != nil {
		t.Fatalf("ListSummaries() error = %v", err)
	}
	if len(page.Workflows) != 2 || page.NextCursor != "" {
		t.Errorf("page = (%d workflows, cursor %q), want both rows and no cursor", len(page.Workflows), page.NextCursor)
	}
}
