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
	"github.com/kilaslabs/kilas-flow/internal/datastore"
	"github.com/kilaslabs/kilas-flow/internal/embed"
	"github.com/kilaslabs/kilas-flow/internal/repository"
)

// fixedTenant resolves every request to one tenant, so two servers sharing
// one engine prove isolation the way a second tenant would.
type fixedTenant struct{ id string }

func (resolver fixedTenant) Resolve(context.Context) repository.TenantScope {
	return repository.TenantScope{ID: resolver.id}
}

type datastoreResource struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Columns []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"columns"`
}

type rowResource map[string]any

func sharedDatastoreDB(t *testing.T) (*database.DB, *datastore.Engine) {
	t.Helper()
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "datastores.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("database.Migrate() error = %v", err)
	}
	engine, err := datastore.NewEngine(db, "")
	if err != nil {
		t.Fatalf("datastore.NewEngine() error = %v", err)
	}
	return db, engine
}

func newDatastoreAPI(t *testing.T, tenant string) (http.Handler, *datastore.Engine) {
	t.Helper()
	db, engine := sharedDatastoreDB(t)
	return newTestServer(t, api.Deps{
		DB:         db,
		Datastores: engine,
		Tenants:    fixedTenant{id: tenant},
	}), engine
}
func createDatastore(t *testing.T, handler http.Handler, name string) datastoreResource {
	t.Helper()
	return requestJSON[datastoreResource](t, handler, http.MethodPost, "/api/v1/datastores",
		map[string]any{"name": name}, http.StatusCreated)
}
func TestCreateDatastoreReturns201WithLocation(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	encoded, err := json.Marshal(map[string]any{"name": "Metrics"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/datastores", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", recorder.Code, recorder.Body)
	}
	location := recorder.Header().Get("Location")
	if !strings.HasPrefix(location, "/api/v1/datastores/") {
		t.Fatalf("Location = %q, want it under /api/v1/datastores/", location)
	}
}

func TestDatastoreRenameChangesTheNameAndNothingElse(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	created := createDatastore(t, handler, "Metrics")
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/columns",
		map[string]any{"name": "title", "type": "string"}, http.StatusOK)

	renamed := requestJSON[datastoreResource](t, handler, http.MethodPut, "/api/v1/datastores/"+created.ID,
		map[string]any{"name": "Renamed"}, http.StatusOK)
	if renamed.Name != "Renamed" {
		t.Fatalf("name = %q, want Renamed", renamed.Name)
	}
	if len(renamed.Columns) != 1 || renamed.Columns[0].Name != "title" {
		t.Fatalf("columns = %+v, want the untouched title column", renamed.Columns)
	}
	got := requestJSON[datastoreResource](t, handler, http.MethodGet, "/api/v1/datastores/"+created.ID, nil, http.StatusOK)
	if got.Name != "Renamed" || len(got.Columns) != 1 {
		t.Fatalf("get after rename = %+v, want Renamed with one column", got)
	}
}

func TestAnotherTenantsDatastoreReadsAsUnknown(t *testing.T) {
	db := func(t *testing.T) (*database.DB, *datastore.Engine) {
		t.Helper()
		opened, err := database.Open(context.Background(), config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "isolation.db"),
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatalf("database.Open() error = %v", err)
		}
		t.Cleanup(func() { _ = opened.Close() })
		if err := database.Migrate(opened, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
			t.Fatalf("database.Migrate() error = %v", err)
		}
		engine, err := datastore.NewEngine(opened, "")
		if err != nil {
			t.Fatalf("datastore.NewEngine() error = %v", err)
		}
		return opened, engine
	}
	opened, engine := db(t)
	a := newTestServer(t, api.Deps{DB: opened, Datastores: engine, Tenants: fixedTenant{id: "tenant-a"}})
	b := newTestServer(t, api.Deps{DB: opened, Datastores: engine, Tenants: fixedTenant{id: "tenant-b"}})

	created := createDatastore(t, a, "Private")
	for _, path := range []string{
		"/api/v1/datastores/" + created.ID,
		"/api/v1/datastores/" + created.ID + "/rows",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		b.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s as tenant-b = %d, want 404 (body: %s)", path, recorder.Code, recorder.Body)
		}
	}
	listed := requestJSON[struct {
		Items []datastoreResource `json:"items"`
	}](t, b, http.MethodGet, "/api/v1/datastores", nil, http.StatusOK)
	if len(listed.Items) != 0 {
		t.Fatalf("tenant-b lists %+v, want nothing", listed.Items)
	}
}

func TestDeleteRowsWithoutAFilterRemovesNothing(t *testing.T) {
	handler, engine := newDatastoreAPI(t, "tenant-a")
	created := createDatastore(t, handler, "Metrics")
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/columns",
		map[string]any{"name": "title", "type": "string"}, http.StatusOK)
	for _, title := range []string{"a", "b", "c"} {
		requestJSON[rowResource](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/rows",
			map[string]any{"values": map[string]any{"title": title}}, http.StatusCreated)
	}
	count := func() int64 {
		page, err := engine.List(context.Background(), "tenant-a", created.ID, datastore.RowQuery{ReturnAll: true})
		if err != nil {
			t.Fatalf("count rows: %v", err)
		}
		return int64(len(page.Rows))
	}
	if got := count(); got != 3 {
		t.Fatalf("seeded %d rows, want 3", got)
	}
	for name, body := range map[string]any{
		"absent filter": map[string]any{},
		"empty filter":  map[string]any{"filter": map[string]any{"type": "and", "filters": []any{}}},
	} {
		recorder := doJSON(t, handler, http.MethodDelete, "/api/v1/datastores/"+created.ID+"/rows", body)
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status = %d, want 422 (body: %s)", name, recorder.Code, recorder.Body)
		}
		if got := count(); got != 3 {
			t.Fatalf("%s: %d rows remain, want 3 — the refused delete removed rows", name, got)
		}
	}
}

func TestDeleteRowsWithAnUnknownOperatorRemovesNothing(t *testing.T) {
	handler, engine := newDatastoreAPI(t, "tenant-a")
	created := createDatastore(t, handler, "Metrics")
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/columns",
		map[string]any{"name": "title", "type": "string"}, http.StatusOK)
	requestJSON[rowResource](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/rows",
		map[string]any{"values": map[string]any{"title": "keep"}}, http.StatusCreated)

	recorder := doJSON(t, handler, http.MethodDelete, "/api/v1/datastores/"+created.ID+"/rows",
		map[string]any{"filter": map[string]any{"type": "and", "filters": []any{
			map[string]any{"columnName": "title", "condition": "contains", "value": "keep"},
		}}})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body: %s)", recorder.Code, recorder.Body)
	}
	if !strings.Contains(recorder.Body.String(), "eq") {
		t.Fatalf("refusal %q does not name the supported operators", recorder.Body)
	}
	page, err := engine.List(context.Background(), "tenant-a", created.ID, datastore.RowQuery{ReturnAll: true})
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if len(page.Rows) != 1 {
		t.Fatalf("%d rows remain, want 1", len(page.Rows))
	}
}

func TestRowListingCursorRoundTripAndRefusal(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	created := createDatastore(t, handler, "Metrics")
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/columns",
		map[string]any{"name": "title", "type": "string"}, http.StatusOK)
	for _, title := range []string{"a", "b", "c"} {
		requestJSON[rowResource](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/rows",
			map[string]any{"values": map[string]any{"title": title}}, http.StatusCreated)
	}
	first := requestJSON[struct {
		Items      []rowResource `json:"items"`
		NextCursor string        `json:"nextCursor"`
	}](t, handler, http.MethodGet, "/api/v1/datastores/"+created.ID+"/rows?limit=2", nil, http.StatusOK)
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %+v, want 2 items and a cursor", first)
	}
	second := requestJSON[struct {
		Items      []rowResource `json:"items"`
		NextCursor string        `json:"nextCursor"`
	}](t, handler, http.MethodGet, "/api/v1/datastores/"+created.ID+"/rows?limit=2&cursor="+first.NextCursor, nil, http.StatusOK)
	if len(second.Items) != 1 || second.NextCursor != "" {
		t.Fatalf("second page = %+v, want 1 item and no cursor", second)
	}
	recorder := doJSON(t, handler, http.MethodGet, "/api/v1/datastores/"+created.ID+"/rows?cursor=not-a-cursor", nil)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("bogus cursor status = %d, want 400 (body: %s)", recorder.Code, recorder.Body)
	}
}

func TestRowFilterShapeRoundTrip(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	created := createDatastore(t, handler, "Metrics")
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/columns",
		map[string]any{"name": "title", "type": "string"}, http.StatusOK)
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/columns",
		map[string]any{"name": "score", "type": "number"}, http.StatusOK)
	requestJSON[rowResource](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/rows",
		map[string]any{"values": map[string]any{"title": "hello", "score": 3}}, http.StatusCreated)
	requestJSON[rowResource](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/rows",
		map[string]any{"values": map[string]any{"title": "other", "score": 1}}, http.StatusCreated)

	updated := requestJSON[struct {
		Matched int64         `json:"matched"`
		Rows    []rowResource `json:"rows"`
	}](t, handler, http.MethodPut, "/api/v1/datastores/"+created.ID+"/rows",
		map[string]any{
			"filter": map[string]any{"type": "and", "filters": []any{
				map[string]any{"columnName": "title", "condition": "eq", "value": "hello"},
			}},
			"values": map[string]any{"score": 9},
		}, http.StatusOK)
	if updated.Matched != 1 {
		t.Fatalf("matched = %d, want 1", updated.Matched)
	}
	listed := requestJSON[struct {
		Items []rowResource `json:"items"`
	}](t, handler, http.MethodGet,
		"/api/v1/datastores/"+created.ID+"/rows?match=all&columnName=score&condition=eq&value=9",
		nil, http.StatusOK)
	if len(listed.Items) != 1 || listed.Items[0]["title"] != "hello" {
		t.Fatalf("filtered list = %+v, want the updated hello row", listed.Items)
	}
}

func TestRowValidationNamesTheColumn(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	created := createDatastore(t, handler, "Metrics")
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/columns",
		map[string]any{"name": "title", "type": "string"}, http.StatusOK)

	recorder := doJSON(t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/rows",
		map[string]any{"values": map[string]any{"nope": "x"}})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body: %s)", recorder.Code, recorder.Body)
	}
	if !strings.Contains(recorder.Body.String(), "nope") {
		t.Fatalf("refusal %q does not name the column", recorder.Body)
	}
}

func TestDatastoreEndpointsAreUnavailableWithoutAStore(t *testing.T) {
	handler := newTestServer(t, api.Deps{Tenants: fixedTenant{id: "tenant-a"}})
	for _, route := range [][2]string{
		{http.MethodGet, "/api/v1/datastores"},
		{http.MethodPost, "/api/v1/datastores"},
		{http.MethodGet, "/api/v1/datastores/datastore_x"},
	} {
		recorder := doJSON(t, handler, route[0], route[1], map[string]any{"name": "x"})
		if recorder.Code != http.StatusServiceUnavailable && !(route[0] == http.MethodGet && recorder.Code == http.StatusServiceUnavailable) {
			t.Fatalf("%s %s = %d, want 503 (body: %s)", route[0], route[1], recorder.Code, recorder.Body)
		}
	}
}

func TestEmbedSessionsAreDeniedOnDatastorePaths(t *testing.T) {
	issuer := embedIssuer(t)
	opened, engine := sharedDatastoreDB(t)
	handler := newTestServer(t, api.Deps{DB: opened, Datastores: engine, Tenants: fixedTenant{id: "tenant-a"}, EmbedIssuer: issuer})
	created := createDatastore(t, handler, "Metrics")
	_, token, err := issuer.Issue(embed.Request{
		TenantID: "tenant-a", WorkflowID: "wf_embedded",
		Scopes: []embed.Scope{embed.ScopeRead, embed.ScopeWrite, embed.ScopeRun}, Origin: hostOrigin,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	// Every datastore path falls into permits' default arm until V2-p9-15
	// grants a scope, so a fully-scoped session is still refused — asserted
	// here so a later change to permits cannot open them by accident.
	for _, route := range [][2]string{
		{http.MethodGet, "/api/v1/datastores"},
		{http.MethodPost, "/api/v1/datastores"},
		{http.MethodGet, "/api/v1/datastores/" + created.ID},
		{http.MethodGet, "/api/v1/datastores/" + created.ID + "/rows"},
	} {
		if got := embedRequest(t, handler, token, route[0], route[1], map[string]any{"name": "x"}); got.Code != http.StatusForbidden {
			t.Errorf("%s %s status = %d, want 403 (body: %s)", route[0], route[1], got.Code, got.Body)
		}
	}
}
func doJSON(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var request *http.Request
	if body != nil {
		request = httptest.NewRequest(method, path, bytes.NewReader(encoded))
		request.Header.Set("Content-Type", "application/json")
	} else {
		request = httptest.NewRequest(method, path, nil)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}
