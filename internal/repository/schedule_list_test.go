package repository_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/repository"
)

// scheduleWorkflow stores one workflow and returns its ID: schedule rows carry
// a foreign key to it, which is what stops a schedule outliving its workflow.
func scheduleWorkflow(t *testing.T, db *database.DB, tenant repository.TenantScope) string {
	t.Helper()

	stored, err := repository.NewWorkflowStore(db.DB).SaveDraft(context.Background(), tenant,
		listDocument("wf_schedules", "Scheduled"))
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	return stored.ID
}

// saveSchedules stores n schedules for one workflow, which is what activation
// does for a node with several intervals.
func saveSchedules(t *testing.T, store *repository.GORMScheduleStore, tenant repository.TenantScope, workflowID string, n int) []repository.Schedule {
	t.Helper()

	saved := make([]repository.Schedule, 0, n)
	for i := 0; i < n; i++ {
		schedule, err := store.Create(context.Background(), tenant, repository.Schedule{
			WorkflowID: workflowID, IntervalIndex: i, Cron: "0 * * * *", Active: true,
		})
		if err != nil {
			t.Fatalf("Create(%d) error = %v", i, err)
		}
		saved = append(saved, schedule)
	}
	return saved
}

func TestAScheduleListingIsBoundedAndPagesWithoutRepeatingARow(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewScheduleStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	saved := saveSchedules(t, store, tenant, scheduleWorkflow(t, db, tenant), 5)

	seen := map[string]bool{}
	cursor := ""
	for pages := 0; ; pages++ {
		page, err := store.ListPage(context.Background(), tenant, repository.ScheduleFilter{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("ListPage() error = %v", err)
		}
		if len(page.Schedules) > 2 {
			t.Fatalf("page carried %d schedules, want at most the limit of 2", len(page.Schedules))
		}
		for _, schedule := range page.Schedules {
			if seen[schedule.ID] {
				t.Fatalf("schedule %s appeared on two pages", schedule.ID)
			}
			seen[schedule.ID] = true
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
		if pages > 5 {
			t.Fatal("paging did not terminate")
		}
	}

	if len(seen) != len(saved) {
		t.Errorf("schedules seen across pages = %d, want %d", len(seen), len(saved))
	}
}

// Several intervals of one trigger node are created inside the same
// millisecond, so the id tiebreaker is what makes the pages add up to the
// table rather than repeating the tied rows.
func TestAScheduleListingPagesRowsCreatedInTheSameMillisecond(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewScheduleStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	saveSchedules(t, store, tenant, scheduleWorkflow(t, db, tenant), 6)

	seen := map[string]bool{}
	cursor := ""
	for pages := 0; ; pages++ {
		page, err := store.ListPage(context.Background(), tenant, repository.ScheduleFilter{Limit: 1, Cursor: cursor})
		if err != nil {
			t.Fatalf("ListPage() error = %v", err)
		}
		for _, schedule := range page.Schedules {
			if seen[schedule.ID] {
				t.Fatalf("schedule %s appeared on two pages", schedule.ID)
			}
			seen[schedule.ID] = true
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
		if pages > 6 {
			t.Fatal("paging did not terminate")
		}
	}
	if len(seen) != 6 {
		t.Errorf("schedules seen across pages = %d, want 6", len(seen))
	}
}

func TestAScheduleListingHandsBackACursorOnlyWhileMoreRowsRemain(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewScheduleStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	saveSchedules(t, store, tenant, scheduleWorkflow(t, db, tenant), 2)

	page, err := store.ListPage(context.Background(), tenant, repository.ScheduleFilter{Limit: 5})
	if err != nil {
		t.Fatalf("ListPage() error = %v", err)
	}
	if len(page.Schedules) != 2 {
		t.Fatalf("schedules listed = %d, want 2", len(page.Schedules))
	}
	if page.NextCursor != "" {
		t.Errorf("cursor = %q on the last page, want none", page.NextCursor)
	}

	first, err := store.ListPage(context.Background(), tenant, repository.ScheduleFilter{Limit: 1})
	if err != nil {
		t.Fatalf("ListPage() error = %v", err)
	}
	if first.NextCursor == "" {
		t.Fatal("a page that stopped short of the table carried no cursor")
	}
	second, err := store.ListPage(context.Background(), tenant, repository.ScheduleFilter{Cursor: first.NextCursor})
	if err != nil {
		t.Fatalf("ListPage(cursor) error = %v", err)
	}
	if len(second.Schedules) != 1 {
		t.Errorf("second page carried %d schedules, want the remaining 1", len(second.Schedules))
	}
}

func TestAnUnrecognisedScheduleCursorIsRejectedRatherThanIgnored(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewScheduleStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	saveSchedules(t, store, tenant, scheduleWorkflow(t, db, tenant), 1)

	for _, cursor := range []string{"not-a-cursor", "ISE=", "MjAyNi0xMy0wMSAwMDowMDowMFo"} {
		_, err := store.ListPage(context.Background(), tenant, repository.ScheduleFilter{Cursor: cursor})
		if !errors.Is(err, repository.ErrInvalidCursor) {
			t.Errorf("ListPage(cursor %q) error = %v, want ErrInvalidCursor", cursor, err)
		}
	}
}

// The unbounded listing stays for in-process callers, and the page must agree
// with it: the same rows, in the same order.
func TestTheSchedulePageAgreesWithTheUnboundedListing(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewScheduleStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	saveSchedules(t, store, tenant, scheduleWorkflow(t, db, tenant), 3)

	all, err := store.List(context.Background(), tenant)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	page, err := store.ListPage(context.Background(), tenant, repository.ScheduleFilter{Limit: 100})
	if err != nil {
		t.Fatalf("ListPage() error = %v", err)
	}
	if len(all) != len(page.Schedules) {
		t.Fatalf("List returned %d schedules, ListPage returned %d", len(all), len(page.Schedules))
	}
	for index := range all {
		if all[index].ID != page.Schedules[index].ID {
			t.Errorf("row %d: List has %s, ListPage has %s", index, all[index].ID, page.Schedules[index].ID)
		}
	}
	if page.NextCursor != "" {
		t.Errorf("cursor = %q after every row, want none", page.NextCursor)
	}
}
