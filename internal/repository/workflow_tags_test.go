package repository_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// taggedDocument is a draft with a chosen identity, name and tags.
func taggedDocument(id, name string, tags ...string) workflow.Document {
	document := listDocument(id, name)
	document.Tags = tags
	return document
}

func summaryTags(t *testing.T, store *repository.GORMWorkflowStore, tenant repository.TenantScope, filter repository.WorkflowFilter) map[string][]string {
	t.Helper()
	page, err := store.ListSummaries(context.Background(), tenant, filter)
	if err != nil {
		t.Fatalf("ListSummaries() error = %v", err)
	}
	byID := map[string][]string{}
	for _, summary := range page.Workflows {
		byID[summary.ID] = summary.Tags
	}
	return byID
}

func countTagRows(t *testing.T, db *database.DB, tenantID string) int64 {
	t.Helper()
	var count int64
	if err := db.Raw("SELECT COUNT(*) FROM workflow_tags WHERE tenant_id = ?", tenantID).Scan(&count).Error; err != nil {
		t.Fatalf("count workflow_tags error = %v", err)
	}
	return count
}

// The list shows each workflow's tags in the order they were authored, and an
// untagged workflow shows none.
func TestSavedTagsAreListedWithTheSummary(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-tags-list"}
		store := repository.NewWorkflowStore(db.DB)
		if _, err := store.SaveDraft(ctx, tenant, taggedDocument("drv_tag_list_1", "Tagged", "Prod", "billing", "Zeta")); err != nil {
			t.Fatalf("SaveDraft(tagged) error = %v", err)
		}
		if _, err := store.SaveDraft(ctx, tenant, taggedDocument("drv_tag_list_2", "Plain")); err != nil {
			t.Fatalf("SaveDraft(plain) error = %v", err)
		}

		got := summaryTags(t, store, tenant, repository.WorkflowFilter{})
		if want := "Prod|billing|Zeta"; strings.Join(got["drv_tag_list_1"], "|") != want {
			t.Errorf("tags of the tagged workflow = %q, want %q (authored order and case)", got["drv_tag_list_1"], want)
		}
		if len(got["drv_tag_list_2"]) != 0 {
			t.Errorf("tags of the plain workflow = %q, want none", got["drv_tag_list_2"])
		}

		// The saved document and the summary agree.
		stored, err := store.Get(ctx, tenant, "drv_tag_list_1")
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if strings.Join(stored.LatestVersion.Document.Tags, "|") != "Prod|billing|Zeta" {
			t.Errorf("stored document tags = %q", stored.LatestVersion.Document.Tags)
		}
	})
}

// A tag filter ignores case, keeps the other filters' paging, and returns only
// the workflows that carry the tag.
func TestTheWorkflowListFiltersByTagCaseInsensitively(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-tags-filter"}
		store := repository.NewWorkflowStore(db.DB)
		for _, document := range []workflow.Document{
			taggedDocument("drv_tag_f1", "One", "Prod"),
			taggedDocument("drv_tag_f2", "Two", "staging", "PROD"),
			taggedDocument("drv_tag_f3", "Three", "staging"),
			taggedDocument("drv_tag_f4", "Four"),
		} {
			if _, err := store.SaveDraft(ctx, tenant, document); err != nil {
				t.Fatalf("SaveDraft(%s) error = %v", document.ID, err)
			}
		}

		for _, spelling := range []string{"prod", "PROD", "Prod", "  prod  "} {
			got := summaryTags(t, store, tenant, repository.WorkflowFilter{Tag: spelling})
			if len(got) != 2 || got["drv_tag_f1"] == nil || got["drv_tag_f2"] == nil {
				t.Errorf("filter %q listed %v, want drv_tag_f1 and drv_tag_f2", spelling, got)
			}
		}
		// The filter selects workflows; each row still shows all its tags.
		got := summaryTags(t, store, tenant, repository.WorkflowFilter{Tag: "prod"})
		if strings.Join(got["drv_tag_f2"], "|") != "staging|PROD" {
			t.Errorf("a filtered row's tags = %q, want all of them in order", got["drv_tag_f2"])
		}
		if got := summaryTags(t, store, tenant, repository.WorkflowFilter{Tag: "nothing"}); len(got) != 0 {
			t.Errorf("filter on an unused tag listed %v", got)
		}

		// Paging walks the filtered set, one row at a time, without repeats.
		seen := map[string]bool{}
		cursor := ""
		for pages := 0; pages < 5; pages++ {
			page, err := store.ListSummaries(ctx, tenant, repository.WorkflowFilter{Tag: "staging", Limit: 1, Cursor: cursor})
			if err != nil {
				t.Fatalf("ListSummaries(page %d) error = %v", pages, err)
			}
			for _, summary := range page.Workflows {
				if seen[summary.ID] {
					t.Errorf("%s listed twice while paging", summary.ID)
				}
				seen[summary.ID] = true
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		if len(seen) != 2 || !seen["drv_tag_f2"] || !seen["drv_tag_f3"] {
			t.Errorf("paged tag listing saw %v, want drv_tag_f2 and drv_tag_f3", seen)
		}
	})
}

// A save is the whole truth about a workflow's tags: saving without them
// removes them from the list and from the filter.
func TestASaveWithoutATagUnlistsIt(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-tags-unlist"}
		store := repository.NewWorkflowStore(db.DB)
		if _, err := store.SaveDraft(ctx, tenant, taggedDocument("drv_tag_u1", "Unlist", "a", "b")); err != nil {
			t.Fatalf("SaveDraft() error = %v", err)
		}
		if _, err := store.SaveDraft(ctx, tenant, taggedDocument("drv_tag_u1", "Unlist", "b", "c")); err != nil {
			t.Fatalf("SaveDraft(retag) error = %v", err)
		}
		if got := strings.Join(summaryTags(t, store, tenant, repository.WorkflowFilter{})["drv_tag_u1"], "|"); got != "b|c" {
			t.Fatalf("tags after a re-tag = %q, want b|c", got)
		}
		if got := summaryTags(t, store, tenant, repository.WorkflowFilter{Tag: "a"}); len(got) != 0 {
			t.Errorf("the dropped tag still filters in %v", got)
		}

		if _, err := store.SaveDraft(ctx, tenant, taggedDocument("drv_tag_u1", "Unlist")); err != nil {
			t.Fatalf("SaveDraft(untag) error = %v", err)
		}
		if got := summaryTags(t, store, tenant, repository.WorkflowFilter{})["drv_tag_u1"]; len(got) != 0 {
			t.Errorf("tags after saving none = %q, want none", got)
		}
		if n := countTagRows(t, db, tenant.ID); n != 0 {
			t.Errorf("workflow_tags holds %d rows for an untagged workflow, want 0", n)
		}
	})
}

// A restore brings back the tags its revision carried, the way it brings back
// the name, and a revision from before tagging clears them.
func TestRestoringAVersionRestoresItsTags(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-tags-restore"}
		store := repository.NewWorkflowStore(db.DB)
		first, err := store.SaveDraft(ctx, tenant, taggedDocument("drv_tag_r1", "Restore", "alpha", "beta"))
		if err != nil {
			t.Fatalf("SaveDraft(1) error = %v", err)
		}
		if _, err := store.SaveDraft(ctx, tenant, taggedDocument("drv_tag_r1", "Restore", "gamma")); err != nil {
			t.Fatalf("SaveDraft(2) error = %v", err)
		}
		if got := strings.Join(summaryTags(t, store, tenant, repository.WorkflowFilter{})["drv_tag_r1"], "|"); got != "gamma" {
			t.Fatalf("tags before the restore = %q, want gamma", got)
		}

		restored, err := store.RestoreVersion(ctx, tenant, "drv_tag_r1", first.LatestVersion.ID, "")
		if err != nil {
			t.Fatalf("RestoreVersion() error = %v", err)
		}
		if got := strings.Join(restored.LatestVersion.Document.Tags, "|"); got != "alpha|beta" {
			t.Errorf("restored document tags = %q, want alpha|beta", got)
		}
		if got := strings.Join(summaryTags(t, store, tenant, repository.WorkflowFilter{})["drv_tag_r1"], "|"); got != "alpha|beta" {
			t.Errorf("listed tags after the restore = %q, want alpha|beta", got)
		}
		if got := summaryTags(t, store, tenant, repository.WorkflowFilter{Tag: "gamma"}); len(got) != 0 {
			t.Errorf("a tag the restore removed still filters in %v", got)
		}

		// Restoring a revision that had no tags empties the list.
		bare, err := store.SaveDraft(ctx, tenant, taggedDocument("drv_tag_r2", "Bare"))
		if err != nil {
			t.Fatalf("SaveDraft(bare) error = %v", err)
		}
		if _, err := store.SaveDraft(ctx, tenant, taggedDocument("drv_tag_r2", "Bare", "later")); err != nil {
			t.Fatalf("SaveDraft(tag) error = %v", err)
		}
		if _, err := store.RestoreVersion(ctx, tenant, "drv_tag_r2", bare.LatestVersion.ID, ""); err != nil {
			t.Fatalf("RestoreVersion(bare) error = %v", err)
		}
		if got := summaryTags(t, store, tenant, repository.WorkflowFilter{})["drv_tag_r2"]; len(got) != 0 {
			t.Errorf("tags after restoring an untagged revision = %q, want none", got)
		}
	})
}

// An import saves through the same path, so its tags are listed too.
func TestAnImportedRevisionsTagsAreListed(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-tags-import"}
		store := repository.NewWorkflowStore(db.DB)
		if _, err := store.SaveDraftWithDiagnostics(ctx, tenant, taggedDocument("drv_tag_i1", "Imported", "from-n8n"), json.RawMessage(`{"issues":[]}`)); err != nil {
			t.Fatalf("SaveDraftWithDiagnostics() error = %v", err)
		}
		if got := summaryTags(t, store, tenant, repository.WorkflowFilter{Tag: "from-n8n"}); len(got) != 1 {
			t.Errorf("filter on an imported tag listed %v, want the one workflow", got)
		}
	})
}

// One tenant's tags never list, and never filter, for another.
func TestTagsNeverCrossTenants(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		mine := repository.TenantScope{ID: "drv-tags-mine"}
		theirs := repository.TenantScope{ID: "drv-tags-theirs"}
		store := repository.NewWorkflowStore(db.DB)
		if _, err := store.SaveDraft(ctx, mine, taggedDocument("drv_tag_t1", "Mine", "shared-name", "private-mine")); err != nil {
			t.Fatalf("SaveDraft(mine) error = %v", err)
		}
		if _, err := store.SaveDraft(ctx, theirs, taggedDocument("drv_tag_t2", "Theirs", "shared-name")); err != nil {
			t.Fatalf("SaveDraft(theirs) error = %v", err)
		}

		if got := summaryTags(t, store, mine, repository.WorkflowFilter{Tag: "shared-name"}); len(got) != 1 || got["drv_tag_t1"] == nil {
			t.Errorf("my filter listed %v, want only my workflow", got)
		}
		if got := summaryTags(t, store, theirs, repository.WorkflowFilter{Tag: "private-mine"}); len(got) != 0 {
			t.Errorf("another tenant's filter on my private tag listed %v", got)
		}
		if got := summaryTags(t, store, theirs, repository.WorkflowFilter{})["drv_tag_t2"]; strings.Join(got, "|") != "shared-name" {
			t.Errorf("their workflow's tags = %q, want only shared-name", got)
		}
	})
}

// Deleting a workflow removes its tag rows, and only its own.
func TestDeletingAWorkflowRemovesItsTags(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-tags-delete"}
		store := repository.NewWorkflowStore(db.DB)
		for _, document := range []workflow.Document{
			taggedDocument("drv_tag_d1", "Doomed", "x", "y"),
			taggedDocument("drv_tag_d2", "Kept", "x"),
		} {
			if _, err := store.SaveDraft(ctx, tenant, document); err != nil {
				t.Fatalf("SaveDraft(%s) error = %v", document.ID, err)
			}
		}
		if n := countTagRows(t, db, tenant.ID); n != 3 {
			t.Fatalf("workflow_tags rows before the delete = %d, want 3", n)
		}
		if err := store.Delete(ctx, tenant, "drv_tag_d1"); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if n := countTagRows(t, db, tenant.ID); n != 1 {
			t.Errorf("workflow_tags rows after the delete = %d, want 1 (the kept workflow's)", n)
		}
		if got := summaryTags(t, store, tenant, repository.WorkflowFilter{Tag: "x"}); len(got) != 1 || got["drv_tag_d2"] == nil {
			t.Errorf("filter after the delete listed %v, want only the kept workflow", got)
		}
	})
}
