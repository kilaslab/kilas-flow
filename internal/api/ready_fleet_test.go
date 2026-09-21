package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/datastore"
)

// readyDatastores is the datastores block of the readiness body as a client
// reads it.
type readyDatastores struct {
	SchemaVersion int              `json:"schemaVersion"`
	Spread        map[string]int64 `json:"spread"`
	Behind        int64            `json:"behind"`
	Ahead         int64            `json:"ahead"`
}

type readyBody struct {
	Status     string           `json:"status"`
	Database   string           `json:"database"`
	Datastores *readyDatastores `json:"datastores"`
}

// readyWithFleet serves readiness over a real SQLite catalogue, so the spread
// under test is the one the engine actually reads rather than a stub's answer.
func readyWithFleet(t *testing.T) (http.Handler, *database.DB) {
	t.Helper()

	db, engine := sharedDatastoreDB(t)
	handler := newTestServer(t, api.Deps{DB: db, Datastores: engine, Tenants: fixedTenant{id: "tenant-a"}})

	// The catalogue is updated directly: the only way to produce a datastore
	// at another version through the API is to ship a second schema version,
	// which this ticket does not do.
	t.Cleanup(func() {
		if err := db.Exec("UPDATE datastores SET schema_version = 1").Error; err != nil {
			t.Logf("restoring schema_version: %v", err)
		}
	})

	return handler, db
}

func setSchemaVersion(t *testing.T, db *database.DB, version int) {
	t.Helper()

	if err := db.Exec("UPDATE datastores SET schema_version = ?", version).Error; err != nil {
		t.Fatalf("UPDATE datastores SET schema_version = %d: %v", version, err)
	}
}

func decodeReady(t *testing.T, rec *httptest.ResponseRecorder) (readyBody, map[string]any) {
	t.Helper()

	var body readyBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode readiness body: %v (body: %s)", err, rec.Body)
	}

	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw readiness body: %v", err)
	}

	return body, raw
}

func decodeProblemDetail(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}

	var problem struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem body: %v (body: %s)", err, rec.Body)
	}

	return problem.Detail
}

// The spread is what makes a half-migrated fleet visible instead of green.
func TestReadyReportsTheDatastoreVersionSpread(t *testing.T) {
	handler, _ := readyWithFleet(t)

	rec := get(t, handler, "/api/v1/ready")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body)
	}

	body, raw := decodeReady(t, rec)
	if body.Status != "ok" || body.Database != "ok" {
		t.Errorf("status/database = %q/%q, want ok/ok", body.Status, body.Database)
	}
	if body.Datastores == nil {
		t.Fatalf("datastores block missing from the readiness body: %s", rec.Body)
	}
	if body.Datastores.SchemaVersion != datastore.CurrentSchemaVersion {
		t.Errorf("schemaVersion = %d, want %d", body.Datastores.SchemaVersion, datastore.CurrentSchemaVersion)
	}
	if body.Datastores.Behind != 0 || body.Datastores.Ahead != 0 {
		t.Errorf("behind/ahead = %d/%d, want 0/0", body.Datastores.Behind, body.Datastores.Ahead)
	}
	if len(body.Datastores.Spread) != 0 {
		t.Errorf("spread = %v, want empty on an install with no datastores", body.Datastores.Spread)
	}

	// An empty map must serialise as {} — null would make every generated
	// client model the field as nullable for no reason.
	block, ok := raw["datastores"].(map[string]any)
	if !ok {
		t.Fatalf("datastores = %v, want a JSON object", raw["datastores"])
	}
	if spread, ok := block["spread"].(map[string]any); !ok {
		t.Errorf("spread = %#v, want a JSON object", block["spread"])
	} else if len(spread) != 0 {
		t.Errorf("spread = %v, want an empty object", spread)
	}

	createDatastore(t, handler, "Metrics")
	createDatastore(t, handler, "Events")

	rec = get(t, handler, "/api/v1/ready")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body)
	}

	body, _ = decodeReady(t, rec)
	want := map[string]int64{strconv.Itoa(datastore.CurrentSchemaVersion): 2}
	if !reflect.DeepEqual(body.Datastores.Spread, want) {
		t.Errorf("spread = %v, want %v", body.Datastores.Spread, want)
	}
	if body.Datastores.Behind != 0 {
		t.Errorf("behind = %d, want 0", body.Datastores.Behind)
	}
}

func TestReadyIsNotReadyWhileADatastoreMigrationIsOutstanding(t *testing.T) {
	handler, db := readyWithFleet(t)

	created := createDatastore(t, handler, "Metrics")
	setSchemaVersion(t, db, 0)

	rec := get(t, handler, "/api/v1/ready")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 while a datastore is behind (body: %s)", rec.Code, rec.Body)
	}

	detail := decodeProblemDetail(t, rec)
	if !strings.Contains(detail, "migration outstanding") || !strings.Contains(detail, "behind") {
		t.Errorf("detail = %q, want it to say a migration is outstanding and a datastore is behind", detail)
	}
	// Readiness is public: the spread is disclosed, the fleet's identities are not.
	if strings.Contains(detail, created.ID) {
		t.Errorf("detail = %q, want no datastore id", detail)
	}
	if strings.Contains(detail, "tenant-a") {
		t.Errorf("detail = %q, want no tenant id", detail)
	}

	// Readiness follows the live catalogue: nothing is cached from the boot pass.
	if err := db.Exec("UPDATE datastores SET schema_version = ?", datastore.CurrentSchemaVersion).Error; err != nil {
		t.Fatalf("restore schema_version: %v", err)
	}

	rec = get(t, handler, "/api/v1/ready")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 once the datastore is migrated (body: %s)", rec.Code, rec.Body)
	}

	body, _ := decodeReady(t, rec)
	if body.Datastores.Behind != 0 {
		t.Errorf("behind = %d, want 0 after the migration", body.Datastores.Behind)
	}
}

// The not-ready problem document carries the same datastores block the 200
// body carries. The one state in which a monitor needs the spread is the one
// in which readiness refuses, and the contract forbids reading it out of
// `detail` (`reference/api-contract.md`: "never match on `detail` text"), so a
// 503 without the block would leave the spread machine-readable only while it
// says nothing.
func TestReadyNotReadyCarriesTheDatastoreSpreadInTheProblem(t *testing.T) {
	handler, db := readyWithFleet(t)

	behind := createDatastore(t, handler, "Metrics")
	createDatastore(t, handler, "Events")
	if err := db.Exec("UPDATE datastores SET schema_version = 0 WHERE id = ?", behind.ID).Error; err != nil {
		t.Fatalf("push %s behind: %v", behind.ID, err)
	}

	rec := get(t, handler, "/api/v1/ready")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body: %s)", rec.Code, rec.Body)
	}

	// The problem shape is unchanged: same media type, title, status, and the
	// human-readable detail a person reads.
	if detail := decodeProblemDetail(t, rec); !strings.Contains(detail, "migration outstanding") {
		t.Errorf("detail = %q, want it to say a migration is outstanding", detail)
	}

	var problem struct {
		Title      string           `json:"title"`
		Status     int              `json:"status"`
		Datastores *readyDatastores `json:"datastores"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem body: %v (body: %s)", err, rec.Body)
	}
	block := problem.Datastores
	if block == nil {
		t.Fatalf("datastores block missing from the 503 problem document: %s", rec.Body)
	}
	if block.SchemaVersion != datastore.CurrentSchemaVersion {
		t.Errorf("schemaVersion = %d, want %d", block.SchemaVersion, datastore.CurrentSchemaVersion)
	}
	if block.Behind != 1 || block.Ahead != 0 {
		t.Errorf("behind/ahead = %d/%d, want 1/0", block.Behind, block.Ahead)
	}
	want := map[string]int64{"0": 1, strconv.Itoa(datastore.CurrentSchemaVersion): 1}
	if !reflect.DeepEqual(block.Spread, want) {
		t.Errorf("spread = %v, want %v", block.Spread, want)
	}
	if problem.Status != http.StatusServiceUnavailable {
		t.Errorf("status in the body = %d, want %d", problem.Status, http.StatusServiceUnavailable)
	}
	if problem.Title != http.StatusText(http.StatusServiceUnavailable) {
		t.Errorf("title = %q, want the status text", problem.Title)
	}

	// Readiness is public: the spread is disclosed, the fleet's identities are not.
	for _, secret := range []string{behind.ID, "tenant-a", "datastore_"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("the 503 body leaks %q: %s", secret, rec.Body)
		}
	}
}

func TestReadyReportsButToleratesADatastoreAheadOfTheBuild(t *testing.T) {
	handler, db := readyWithFleet(t)

	createDatastore(t, handler, "Metrics")
	setSchemaVersion(t, db, 99)

	rec := get(t, handler, "/api/v1/ready")
	// An old replica is not broken because a newer peer migrated a datastore;
	// the row gate refuses exactly those datastores, and draining every old
	// pod mid-rollout would be the outage this feature exists to prevent.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a datastore ahead of this build (body: %s)", rec.Code, rec.Body)
	}

	body, _ := decodeReady(t, rec)
	if body.Datastores == nil {
		t.Fatalf("datastores block missing from the readiness body: %s", rec.Body)
	}
	if body.Datastores.Ahead != 1 {
		t.Errorf("ahead = %d, want 1", body.Datastores.Ahead)
	}
	if _, ok := body.Datastores.Spread["99"]; !ok {
		t.Errorf("spread = %v, want a 99 entry", body.Datastores.Spread)
	}

	if err := db.Exec("UPDATE datastores SET schema_version = ?", datastore.CurrentSchemaVersion).Error; err != nil {
		t.Fatalf("restore schema_version: %v", err)
	}

	body, _ = decodeReady(t, get(t, handler, "/api/v1/ready"))
	if body.Datastores.Ahead != 0 {
		t.Errorf("ahead = %d, want 0 after the newer build's datastore is gone", body.Datastores.Ahead)
	}
}

// A Deps without a datastore store must keep answering exactly as before: an
// unconditional reporter would call through a nil engine and 503 every
// instance that has no row store.
func TestReadyWithoutADatastoreEngineOmitsTheBlock(t *testing.T) {
	h := newTestServer(t, api.Deps{DB: stubPinger{}})

	rec := get(t, h, "/api/v1/ready")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body)
	}

	_, raw := decodeReady(t, rec)
	if _, ok := raw["datastores"]; ok {
		t.Errorf("datastores present in %s, want the block omitted", rec.Body)
	}
}

// The database-unreachable 503 is a plain problem document. The driver text
// stays in the log beside the request id — a driver names tables and hosts, and
// /ready is public — and there is no `datastores` block, because a catalogue
// that cannot be read has no spread to report. A monitor that reads
// `datastores.behind` on every 503 must therefore not do so here.
func TestReadyDatabaseUnreachableServesNoDriverTextAndNoSpread(t *testing.T) {
	driver := errors.New("modernc.org/sqlite: dial tcp: lookup db.internal:5432: no such host")
	h := newTestServer(t, api.Deps{DB: stubPinger{err: driver}})

	rec := get(t, h, "/api/v1/ready")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when the database is unreachable (body: %s)", rec.Code, rec.Body)
	}

	// The human sentence a person reads is unchanged.
	if detail := decodeProblemDetail(t, rec); detail != "database unreachable" {
		t.Errorf("detail = %q, want the generic database unreachable", detail)
	}

	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw problem body: %v (body: %s)", err, rec.Body)
	}
	if block, ok := raw["datastores"]; ok {
		t.Errorf("datastores = %v, want the block absent from the database-unreachable 503", block)
	}

	for _, leak := range []string{"modernc.org/sqlite", "db.internal", "no such host"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("the 503 body leaks the driver error (%q): %s", leak, rec.Body)
		}
	}
}

// A driver error names tables and hosts, so it is logged, never served.
func TestReadyIsNotReadyWhenTheCatalogueCannotBeRead(t *testing.T) {
	handler, db := readyWithFleet(t)

	if err := db.Exec("DROP TABLE datastores").Error; err != nil {
		t.Fatalf("DROP TABLE datastores: %v", err)
	}

	rec := get(t, handler, "/api/v1/ready")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when the fleet cannot be read (body: %s)", rec.Code, rec.Body)
	}

	detail := decodeProblemDetail(t, rec)
	if detail != "datastore fleet status unavailable" {
		t.Errorf("detail = %q, want the generic datastore fleet status unavailable", detail)
	}
	if strings.Contains(detail, "no such table") {
		t.Errorf("detail = %q, want no driver text", detail)
	}
}
