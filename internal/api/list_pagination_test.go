package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/datastore"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// pagedListDB opens one migrated database for a listing test. The lists under
// test all read the same schema, so one database serves whichever store the
// case needs.
func pagedListDB(t *testing.T) *database.DB {
	t.Helper()

	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "lists.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("database.Migrate() error = %v", err)
	}
	return db
}

// pagedDocument is the smallest document the store accepts.
func pagedDocument(id, name string) workflow.Document {
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

func seedPagedWorkflows(t *testing.T, store *repository.GORMWorkflowStore, n int) []string {
	t.Helper()

	tenant := repository.TenantScope{ID: repository.DefaultTenantID}
	ids := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		stored, err := store.SaveDraft(context.Background(), tenant, pagedDocument(fmt.Sprintf("wf_paged_%d", i), fmt.Sprintf("Paged %d", i)))
		if err != nil {
			t.Fatalf("SaveDraft(%d) error = %v", i, err)
		}
		ids = append(ids, stored.ID)
	}
	return ids
}

// listBody is the bare-array shape every list keeps: pagination moved the
// cursor into a header precisely so this did not have to change.
func decodeList[T any](t *testing.T, recorder *httptest.ResponseRecorder) []T {
	t.Helper()

	var decoded []T
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode list body = %v", err)
	}
	return decoded
}

// A tenant with more workflows than fit on a page must be able to read them all
// without the first response carrying the whole table, and a cursor nobody
// issued must be a 400 — paging from an invented position silently repeats or
// skips rows, which is worse than a refusal.
func TestTheWorkflowListIsPagedAndRefusesAnInventedCursor(t *testing.T) {
	db := pagedListDB(t)
	store := repository.NewWorkflowStore(db.DB)
	seedPagedWorkflows(t, store, 3)
	handler := newTestServer(t, api.Deps{DB: db, Workflows: store})

	first := get(t, handler, "/api/v1/workflows?limit=2")
	if first.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", first.Code, first.Body)
	}
	if got := len(decodeList[map[string]any](t, first)); got != 2 {
		t.Errorf("first page carried %d workflows, want exactly the limit of 2", got)
	}
	cursor := first.Header().Get("X-Next-Cursor")
	if cursor == "" {
		t.Fatal("a page that stopped short of the table carried no X-Next-Cursor")
	}

	second := get(t, handler, "/api/v1/workflows?limit=2&cursor="+url.QueryEscape(cursor))
	if second.Code != http.StatusOK {
		t.Fatalf("second page status = %d, want 200 (body: %s)", second.Code, second.Body)
	}
	if got := len(decodeList[map[string]any](t, second)); got != 1 {
		t.Errorf("second page carried %d workflows, want the remaining 1", got)
	}
	if got := second.Header().Get("X-Next-Cursor"); got != "" {
		t.Errorf("X-Next-Cursor = %q on the last page, want empty", got)
	}

	bad := get(t, handler, "/api/v1/workflows?cursor=not-a-cursor")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invented cursor status = %d, want 400 (body: %s)", bad.Code, bad.Body)
	}
}

func TestTheScheduleListIsPagedAndRefusesAnInventedCursor(t *testing.T) {
	db := pagedListDB(t)
	workflows := repository.NewWorkflowStore(db.DB)
	workflowIDs := seedPagedWorkflows(t, workflows, 1)
	schedules := repository.NewScheduleStore(db.DB)
	tenant := repository.TenantScope{ID: repository.DefaultTenantID}
	for i := 0; i < 3; i++ {
		if _, err := schedules.Create(context.Background(), tenant, repository.Schedule{
			WorkflowID: workflowIDs[0], IntervalIndex: i, Cron: "0 * * * *", Active: true,
		}); err != nil {
			t.Fatalf("Create schedule %d error = %v", i, err)
		}
	}
	handler := newTestServer(t, api.Deps{DB: db, Schedules: schedules})

	first := get(t, handler, "/api/v1/schedules?limit=2")
	if first.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", first.Code, first.Body)
	}
	if got := len(decodeList[map[string]any](t, first)); got != 2 {
		t.Errorf("first page carried %d schedules, want exactly the limit of 2", got)
	}
	cursor := first.Header().Get("X-Next-Cursor")
	if cursor == "" {
		t.Fatal("a page that stopped short of the table carried no X-Next-Cursor")
	}

	second := get(t, handler, "/api/v1/schedules?cursor="+url.QueryEscape(cursor))
	if second.Code != http.StatusOK {
		t.Fatalf("second page status = %d, want 200 (body: %s)", second.Code, second.Body)
	}
	if got := len(decodeList[map[string]any](t, second)); got != 1 {
		t.Errorf("second page carried %d schedules, want the remaining 1", got)
	}
	if got := second.Header().Get("X-Next-Cursor"); got != "" {
		t.Errorf("X-Next-Cursor = %q on the last page, want empty", got)
	}

	bad := get(t, handler, "/api/v1/schedules?cursor=not-a-cursor")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invented cursor status = %d, want 400 (body: %s)", bad.Code, bad.Body)
	}
}

func TestTheDatastoreListIsPagedAndRefusesAnInventedCursor(t *testing.T) {
	db := pagedListDB(t)
	engine, err := datastore.NewEngine(db, "")
	if err != nil {
		t.Fatalf("datastore.NewEngine() error = %v", err)
	}
	tenant := repository.TenantScope{ID: repository.DefaultTenantID}
	for _, name := range []string{"Alpha", "Beta", "Gamma"} {
		if _, err := engine.Create(context.Background(), tenant.ID, name, []datastore.ColumnInput{{Name: "title", Type: "string"}}); err != nil {
			t.Fatalf("Create %s error = %v", name, err)
		}
	}
	handler := newTestServer(t, api.Deps{DB: db, Datastores: engine})

	first := get(t, handler, "/api/v1/datastores?limit=2")
	if first.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", first.Code, first.Body)
	}
	var page struct {
		Items []datastoreResource `json:"items"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode datastore list = %v", err)
	}
	if len(page.Items) != 2 {
		t.Errorf("first page carried %d datastores, want exactly the limit of 2", len(page.Items))
	}
	cursor := first.Header().Get("X-Next-Cursor")
	if cursor == "" {
		t.Fatal("a page that stopped short of the table carried no X-Next-Cursor")
	}

	second := get(t, handler, "/api/v1/datastores?cursor="+url.QueryEscape(cursor))
	if second.Code != http.StatusOK {
		t.Fatalf("second page status = %d, want 200 (body: %s)", second.Code, second.Body)
	}
	var rest struct {
		Items []datastoreResource `json:"items"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &rest); err != nil {
		t.Fatalf("decode second page = %v", err)
	}
	if len(rest.Items) != 1 {
		t.Errorf("second page carried %d datastores, want the remaining 1", len(rest.Items))
	}
	if got := second.Header().Get("X-Next-Cursor"); got != "" {
		t.Errorf("X-Next-Cursor = %q on the last page, want empty", got)
	}

	bad := get(t, handler, "/api/v1/datastores?cursor=not-a-cursor")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invented cursor status = %d, want 400 (body: %s)", bad.Code, bad.Body)
	}
}
