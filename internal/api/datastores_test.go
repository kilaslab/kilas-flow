package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/datastore"
	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/internal/repository"
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

// A name another data table holds, in any case, is a conflict with that table
// rather than a mistake in the request, and the create and rename dialogs show
// the detail as it is: it has to name the table in the way, which for a clash
// of case alone is not the name that was typed.
func TestADuplicateDatastoreNameIsAConflictNamingTheTableInTheWay(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	createDatastore(t, handler, "Leads")
	const want = "A data table named “Leads” already exists"

	for _, name := range []string{"Leads", "leads"} {
		recorder := doJSON(t, handler, http.MethodPost, "/api/v1/datastores", map[string]any{"name": name})
		if recorder.Code != http.StatusConflict {
			t.Fatalf("creating %q = %d, want 409 (body: %s)", name, recorder.Code, recorder.Body)
		}
		if got := decodeProblem(t, recorder).Detail; got != want {
			t.Errorf("creating %q: detail = %q, want %q", name, got, want)
		}
	}

	other := createDatastore(t, handler, "Other")
	recorder := doJSON(t, handler, http.MethodPut, "/api/v1/datastores/"+other.ID, map[string]any{"name": "LEADS"})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("renaming onto LEADS = %d, want 409 (body: %s)", recorder.Code, recorder.Body)
	}
	if got := decodeProblem(t, recorder).Detail; got != want {
		t.Errorf("renaming onto LEADS: detail = %q, want %q", got, want)
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
	// A workflow session reaches no datastore route even fully scoped: the
	// datastore arm of permits answers only to datastore scopes, and the
	// family rule in Allows keeps workflow scopes from implying them.
	// Asserted here so a later change to permits cannot open them by
	// accident; the datastore session's own paths are proven in
	// embed_datastore_test.go.
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

// A failure underneath the engine is a server fault, and it is answered as one.
//
// The review found the reverse: every error that was not one of the engine's
// own refusals became 422 carrying err.Error(), so a database failure arrived
// as a caller mistake whose message named the physical table the driver could
// not read.
//
// The table is dropped rather than faked because that is what the mapping has
// to recognise — a real driver error, from a real query — and because the
// datastore handler holds the engine itself rather than an interface.
func TestADriverFailureUnderTheEngineIsNotAnsweredAsTheCallersMistake(t *testing.T) {
	db, engine := sharedDatastoreDB(t)
	handler := newTestServer(t, api.Deps{
		DB: db, Datastores: engine, Tenants: fixedTenant{id: "tenant-a"},
	})
	created := createDatastore(t, handler, "Metrics")
	requestJSON[datastoreResource](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/columns",
		map[string]any{"name": "title", "type": "string"}, http.StatusOK)

	// The catalogue still knows the table; the table is gone. That is the state
	// a failed migration or a restore from an older snapshot leaves behind.
	var physical string
	if err := db.Raw("SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE 'ds_%'").
		Row().Scan(&physical); err != nil {
		t.Fatalf("find the physical table: %v", err)
	}
	if err := db.Exec("DROP TABLE " + physical).Error; err != nil {
		t.Fatalf("drop the physical table: %v", err)
	}

	recorder := doJSON(t, handler, http.MethodGet, "/api/v1/datastores/"+created.ID+"/rows", nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for a failure underneath the engine (body: %s)", recorder.Code, recorder.Body)
	}
	// The driver's text names the physical table, which is exactly what a
	// caller must not learn from a server fault.
	if strings.Contains(recorder.Body.String(), physical) || strings.Contains(recorder.Body.String(), "no such table") {
		t.Fatalf("the 500 disclosed the driver's message: %s", recorder.Body)
	}
}

// --- Datastore concurrency surfaces (FEAT-1axhdn) ---------------------------
//
// The row store is atomic per row (see internal/datastore/concurrency.go);
// these tests pin what the HTTP surface does with that: the optional
// ifUpdatedAt precondition and its 409, and the atomic increment operation.

// datastoreIDFilter is the single `id eq` filter a caller addresses one row
// with — the shape the server routes to the one-statement id upsert.
func datastoreIDFilter(rowID int64) map[string]any {
	return map[string]any{
		"type": "and",
		"filters": []any{map[string]any{
			"columnName": "id", "condition": "eq", "value": rowID,
		}},
	}
}

// datastoreNumberTable creates a table with one number column and one row,
// returning both the table and the row's id.
func datastoreNumberTable(t *testing.T, handler http.Handler, name string, value float64) (datastoreResource, int64) {
	t.Helper()
	created := createDatastore(t, handler, name)
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/columns",
		map[string]any{"name": "n", "type": "number"}, http.StatusOK)
	row := requestJSON[rowResource](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/rows",
		map[string]any{"values": map[string]any{"n": value}}, http.StatusCreated)
	id, ok := row["id"].(float64)
	if !ok {
		t.Fatalf("inserted row carries no numeric id: %v", row)
	}
	return created, int64(id)
}

// datastoreStamp reads one row's updatedAt exactly as a caller would before a
// conditional write: from the row the API just returned.
func datastoreStamp(t *testing.T, handler http.Handler, datastoreID string, rowID int64) string {
	t.Helper()
	row := requestJSON[rowResource](t, handler, http.MethodGet, rowPath(datastoreID, rowID), nil, http.StatusOK)
	stamp, ok := row["updatedAt"].(string)
	if !ok || stamp == "" {
		t.Fatalf("row %d carries no updatedAt string: %v", rowID, row)
	}
	return stamp
}

func rowPath(datastoreID string, rowID int64) string {
	return fmt.Sprintf("/api/v1/datastores/%s/rows/%d", datastoreID, rowID)
}

// problemBody is the error document huma serves beside a status.
type problemBody struct {
	Detail string `json:"detail"`
	Errors []struct {
		Message  string `json:"message"`
		Location string `json:"location"`
		Value    string `json:"value"`
	} `json:"errors"`
}

func decodeProblem(t *testing.T, recorder *httptest.ResponseRecorder) problemBody {
	t.Helper()
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/problem+json") {
		t.Fatalf("Content-Type = %q, want application/problem+json (body: %s)", got, recorder.Body)
	}
	var problem problemBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem document = %v (body: %s)", err, recorder.Body)
	}
	return problem
}

func TestStaleIfUpdatedAtIsRefusedWithTheCurrentStamp(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	created, rowID := datastoreNumberTable(t, handler, "Metrics", 1)
	stale := datastoreStamp(t, handler, created.ID, rowID)

	// The first conditional write is against the stamp the read returned, so
	// it lands.
	winner := requestJSON[map[string]any](t, handler, http.MethodPut, "/api/v1/datastores/"+created.ID+"/rows",
		map[string]any{"filter": datastoreIDFilter(rowID), "values": map[string]any{"n": 2}, "ifUpdatedAt": stale},
		http.StatusOK)
	rows, _ := winner["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("conditional update returned %+v, want the one row it wrote", winner)
	}

	// The second reuses the same stamp: the row moved, so it is refused
	// rather than overwriting the winner.
	recorder := doJSON(t, handler, http.MethodPut, "/api/v1/datastores/"+created.ID+"/rows",
		map[string]any{"filter": datastoreIDFilter(rowID), "values": map[string]any{"n": 3}, "ifUpdatedAt": stale})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("stale conditional update = %d, want 409 (body: %s)", recorder.Code, recorder.Body)
	}
	problem := decodeProblem(t, recorder)
	if len(problem.Errors) != 1 {
		t.Fatalf("problem errors = %+v, want exactly one", problem.Errors)
	}
	if problem.Errors[0].Location != "body.ifUpdatedAt" {
		t.Errorf("errors[0].location = %q, want body.ifUpdatedAt", problem.Errors[0].Location)
	}
	// The stamp rides in errors[0].value so a caller retries without a
	// second read: it must be the row's current updatedAt.
	reported, err := time.Parse(time.RFC3339Nano, problem.Errors[0].Value)
	if err != nil {
		t.Fatalf("errors[0].value = %q, want an RFC3339 stamp: %v", problem.Errors[0].Value, err)
	}
	current, err := time.Parse(time.RFC3339Nano, datastoreStamp(t, handler, created.ID, rowID))
	if err != nil {
		t.Fatalf("row updatedAt is not RFC3339: %v", err)
	}
	if !reported.Equal(current) {
		t.Errorf("errors[0].value = %s, want the row's current stamp %s", reported, current)
	}

	row := requestJSON[rowResource](t, handler, http.MethodGet, rowPath(created.ID, rowID), nil, http.StatusOK)
	if row["n"] != 2.0 {
		t.Errorf("n = %v, want the winner's 2 — the stale write was not refused", row["n"])
	}
}

func TestStaleIfUpdatedAtIsRefusedOnDelete(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	created, rowID := datastoreNumberTable(t, handler, "Metrics", 1)
	stale := datastoreStamp(t, handler, created.ID, rowID)
	requestJSON[map[string]any](t, handler, http.MethodPut, "/api/v1/datastores/"+created.ID+"/rows",
		map[string]any{"filter": datastoreIDFilter(rowID), "values": map[string]any{"n": 2}, "ifUpdatedAt": stale},
		http.StatusOK)

	recorder := doJSON(t, handler, http.MethodDelete, "/api/v1/datastores/"+created.ID+"/rows",
		map[string]any{"filter": datastoreIDFilter(rowID), "ifUpdatedAt": stale})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("stale conditional delete = %d, want 409 (body: %s)", recorder.Code, recorder.Body)
	}
	problem := decodeProblem(t, recorder)
	if len(problem.Errors) != 1 || problem.Errors[0].Location != "body.ifUpdatedAt" {
		t.Fatalf("problem = %+v, want one error located at body.ifUpdatedAt", problem.Errors)
	}
	if _, err := time.Parse(time.RFC3339Nano, problem.Errors[0].Value); err != nil {
		t.Errorf("errors[0].value = %q, want an RFC3339 stamp: %v", problem.Errors[0].Value, err)
	}
	// The row is still there, with the winner's value.
	row := requestJSON[rowResource](t, handler, http.MethodGet, rowPath(created.ID, rowID), nil, http.StatusOK)
	if row["n"] != 2.0 {
		t.Errorf("n = %v, want 2 — the refused delete removed the row", row["n"])
	}

	// With the current stamp the same delete lands.
	current := datastoreStamp(t, handler, created.ID, rowID)
	requestJSON[map[string]any](t, handler, http.MethodDelete, "/api/v1/datastores/"+created.ID+"/rows",
		map[string]any{"filter": datastoreIDFilter(rowID), "ifUpdatedAt": current}, http.StatusOK)
	if recorder := doJSON(t, handler, http.MethodGet, rowPath(created.ID, rowID), nil); recorder.Code != http.StatusNotFound {
		t.Errorf("row after the conditional delete = %d, want 404 (body: %s)", recorder.Code, recorder.Body)
	}
}

func TestIfUpdatedAtNeedsExactlyOneRow(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	created, rowID := datastoreNumberTable(t, handler, "Metrics", 1)
	requestJSON[rowResource](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/rows",
		map[string]any{"values": map[string]any{"n": 1}}, http.StatusCreated)
	stamp := datastoreStamp(t, handler, created.ID, rowID)

	// A bulk conditional write has no sane partial-application story, so the
	// handler refuses a filter that matches more than one row.
	filter := map[string]any{"type": "and", "filters": []any{
		map[string]any{"columnName": "n", "condition": "eq", "value": 1},
	}}
	for name, recorder := range map[string]*httptest.ResponseRecorder{
		"update": doJSON(t, handler, http.MethodPut, "/api/v1/datastores/"+created.ID+"/rows",
			map[string]any{"filter": filter, "values": map[string]any{"n": 2}, "ifUpdatedAt": stamp}),
		"delete": doJSON(t, handler, http.MethodDelete, "/api/v1/datastores/"+created.ID+"/rows",
			map[string]any{"filter": filter, "ifUpdatedAt": stamp}),
	} {
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s over two rows = %d, want 422 (body: %s)", name, recorder.Code, recorder.Body)
		}
		if !strings.Contains(recorder.Body.String(), "exactly one") {
			t.Errorf("%s refusal = %s, want it to name the single-row requirement", name, recorder.Body)
		}
	}
	// Nothing was written: both rows still hold 1.
	for _, id := range []int64{rowID, rowID + 1} {
		row := requestJSON[rowResource](t, handler, http.MethodGet, rowPath(created.ID, id), nil, http.StatusOK)
		if row["n"] != 1.0 {
			t.Errorf("row %d n = %v, want the refused write to leave 1", id, row["n"])
		}
	}
}

func TestUpdateWithoutIfUpdatedAtStaysLastWriterWins(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	created, rowID := datastoreNumberTable(t, handler, "Metrics", 1)

	// The ordinary write stays unconditional: no read, no stamp, no refusal.
	for _, value := range []float64{2, 3} {
		requestJSON[map[string]any](t, handler, http.MethodPut, "/api/v1/datastores/"+created.ID+"/rows",
			map[string]any{"filter": datastoreIDFilter(rowID), "values": map[string]any{"n": value}}, http.StatusOK)
	}
	row := requestJSON[rowResource](t, handler, http.MethodGet, rowPath(created.ID, rowID), nil, http.StatusOK)
	if row["n"] != 3.0 {
		t.Errorf("n = %v, want the last writer's 3", row["n"])
	}
}

func TestIncrementRowsIsAtomicOverHTTP(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	created, rowID := datastoreNumberTable(t, handler, "Counters", 0)

	const writers = 10
	statuses := make([]int, writers)
	amounts := make([]float64, writers)
	payload, err := json.Marshal(map[string]any{
		"filter": datastoreIDFilter(rowID), "column": "n", "amount": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for writer := range writers {
		writer := writer
		wait.Add(1)
		go func() {
			defer wait.Done()
			request := httptest.NewRequest(http.MethodPost,
				"/api/v1/datastores/"+created.ID+"/rows/increment", bytes.NewReader(payload))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			statuses[writer] = recorder.Code
			var body struct {
				Rows []struct {
					N float64 `json:"n"`
				} `json:"rows"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err == nil && len(body.Rows) == 1 {
				amounts[writer] = body.Rows[0].N
			}
		}()
	}
	wait.Wait()
	for writer, status := range statuses {
		if status != http.StatusOK {
			t.Fatalf("increment %d = %d, want 200", writer, status)
		}
	}
	// Ten concurrent increments of one row land ten times, and each caller
	// reads its own post-image: the ten values are exactly 1..10.
	seen := make(map[float64]bool, writers)
	for _, amount := range amounts {
		seen[amount] = true
	}
	if len(seen) != writers {
		t.Fatalf("increment returned %v, want each of 1..%d exactly once", amounts, writers)
	}
	for want := 1.0; want <= writers; want++ {
		if !seen[want] {
			t.Fatalf("increment returned %v, want every value in 1..%d", amounts, writers)
		}
	}
	row := requestJSON[rowResource](t, handler, http.MethodGet, rowPath(created.ID, rowID), nil, http.StatusOK)
	if row["n"] != float64(writers) {
		t.Errorf("n = %v after %d concurrent increments, want %d", row["n"], writers, writers)
	}

	// A missing amount counts as one, and a negative amount subtracts.
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/rows/increment",
		map[string]any{"filter": datastoreIDFilter(rowID), "column": "n"}, http.StatusOK)
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/rows/increment",
		map[string]any{"filter": datastoreIDFilter(rowID), "column": "n", "amount": -3}, http.StatusOK)
	row = requestJSON[rowResource](t, handler, http.MethodGet, rowPath(created.ID, rowID), nil, http.StatusOK)
	if row["n"] != float64(writers-2) {
		t.Errorf("n = %v after a default and a negative increment, want %d", row["n"], writers-2)
	}
}

func TestIncrementRowsRefusesWhatItCannotDo(t *testing.T) {
	db, engine := sharedDatastoreDB(t)
	tenantA := newTestServer(t, api.Deps{DB: db, Datastores: engine, Tenants: fixedTenant{id: "tenant-a"}})
	tenantB := newTestServer(t, api.Deps{DB: db, Datastores: engine, Tenants: fixedTenant{id: "tenant-b"}})
	created, rowID := datastoreNumberTable(t, tenantA, "Counters", 0)
	// A second table carries a string column, which no increment can add to.
	textual := createDatastore(t, tenantA, "Notes")
	requestJSON[map[string]any](t, tenantA, http.MethodPost, "/api/v1/datastores/"+textual.ID+"/columns",
		map[string]any{"name": "title", "type": "string"}, http.StatusOK)
	textRow := requestJSON[rowResource](t, tenantA, http.MethodPost, "/api/v1/datastores/"+textual.ID+"/rows",
		map[string]any{"values": map[string]any{"title": "a"}}, http.StatusCreated)
	textRowID := int64(textRow["id"].(float64))

	for name, test := range map[string]struct {
		handler  http.Handler
		path     string
		body     map[string]any
		wantCode int
	}{
		"empty filter": {tenantA, "/api/v1/datastores/" + created.ID + "/rows/increment",
			map[string]any{"filter": map[string]any{"type": "and", "filters": []any{}}, "column": "n", "amount": 1},
			http.StatusUnprocessableEntity},
		"string column": {tenantA, "/api/v1/datastores/" + textual.ID + "/rows/increment",
			map[string]any{"filter": datastoreIDFilter(textRowID), "column": "title", "amount": 1},
			http.StatusUnprocessableEntity},
		"unknown datastore": {tenantA, "/api/v1/datastores/datastore_missing/rows/increment",
			map[string]any{"filter": datastoreIDFilter(rowID), "column": "n", "amount": 1},
			http.StatusNotFound},
		"foreign datastore": {tenantB, "/api/v1/datastores/" + created.ID + "/rows/increment",
			map[string]any{"filter": datastoreIDFilter(rowID), "column": "n", "amount": 1},
			http.StatusNotFound},
	} {
		recorder := doJSON(t, test.handler, http.MethodPost, test.path, test.body)
		if recorder.Code != test.wantCode {
			t.Errorf("%s = %d, want %d (body: %s)", name, recorder.Code, test.wantCode, recorder.Body)
		}
	}
	row := requestJSON[rowResource](t, tenantA, http.MethodGet, rowPath(created.ID, rowID), nil, http.StatusOK)
	if row["n"] != 0.0 {
		t.Errorf("n = %v, want the refused increments to leave 0", row["n"])
	}
}

func TestUpsertOnIDCreatesThenUpdatesOverHTTP(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	created := createDatastore(t, handler, "Addresses")
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/columns",
		map[string]any{"name": "n", "type": "number"}, http.StatusOK)

	first := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/rows/upsert",
		map[string]any{"filter": datastoreIDFilter(50), "values": map[string]any{"n": 1}}, http.StatusOK)
	if first["inserted"] != true {
		t.Fatalf("upsert on a missing id = %+v, want inserted true", first)
	}
	rows, _ := first["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("upsert on a missing id returned %+v, want the created row", first)
	}
	if row, _ := rows[0].(map[string]any); row["id"] != 50.0 {
		t.Fatalf("upsert created %v, want id 50 — the id is the address", rows[0])
	}

	second := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/rows/upsert",
		map[string]any{"filter": datastoreIDFilter(50), "values": map[string]any{"n": 2}}, http.StatusOK)
	if second["inserted"] != false {
		t.Fatalf("second upsert on id 50 = %+v, want inserted false", second)
	}
	rows, _ = second["rows"].([]any)
	if row, _ := rows[0].(map[string]any); row["n"] != 2.0 {
		t.Fatalf("second upsert = %v, want n 2", rows[0])
	}

	// An id past the explicit-id bound cannot be honoured, so it is refused
	// rather than silently retargeted — and the table still takes plain
	// inserts afterwards, which is what the bound protects.
	recorder := doJSON(t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/rows/upsert",
		map[string]any{"filter": datastoreIDFilter(9223372036854775807), "values": map[string]any{"n": 3}})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("upsert on an unbounded id = %d, want 422 (body: %s)", recorder.Code, recorder.Body)
	}
	requestJSON[rowResource](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/rows",
		map[string]any{"values": map[string]any{"n": 4}}, http.StatusCreated)
}

func TestDatastoreOperationDescriptionsStateTheirConcurrency(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	recorder := doJSON(t, handler, http.MethodGet, "/api/openapi.json", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/openapi.json = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}
	var document struct {
		Paths map[string]map[string]struct {
			Description string `json:"description"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode the OpenAPI document = %v", err)
	}
	description := func(path, method string) string {
		t.Helper()
		operations, found := document.Paths[path]
		if !found {
			t.Fatalf("the document has no path %q", path)
		}
		operation, found := operations[method]
		if !found {
			t.Fatalf("the document has no %s %s", method, path)
		}
		return operation.Description
	}
	for _, test := range []struct {
		path, method string
		phrases      []string
	}{
		{"/api/v1/datastores/{id}/rows", "put", []string{
			"One statement, atomic per row on both drivers", "no row lock is taken",
			"ifUpdatedAt", "409", "errors[0].value",
		}},
		{"/api/v1/datastores/{id}/rows", "delete", []string{
			"One statement, atomic per row on both drivers", "no row lock is taken",
			"ifUpdatedAt", "409", "errors[0].value",
		}},
		{"/api/v1/datastores/{id}/rows/upsert", "post", []string{
			"ON CONFLICT", "9007199254740991", "read-then-write in no single transaction",
			"uses increment",
		}},
		{"/api/v1/datastores/{id}/rows/increment", "post", []string{
			"one statement", "atomic per row on both drivers", "A NULL cell counts as zero",
			"Concurrent increments never lose a write",
		}},
	} {
		got := description(test.path, test.method)
		for _, phrase := range test.phrases {
			if !strings.Contains(got, phrase) {
				t.Errorf("%s %s description lacks %q: %s", test.method, test.path, phrase, got)
			}
		}
	}
}
