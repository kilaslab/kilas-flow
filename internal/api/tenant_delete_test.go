package api_test

// End-to-end proof of the operator's tenant deletion: a real server over a real
// SQLite database, with real stores behind a real tenantpurge.Service. What the
// handler tests cannot show is that the purge, the auth boundary and the routes
// agree — that a customer's key is refused, that the deleted tenant's key stops
// working, that a sibling tenant is untouched, and that repeating the request
// converges.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/api/handlers"
	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/binary"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/datastore"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/tenantpurge"
	"github.com/kilaslab/kilas-flow/internal/webhook"
	"github.com/kilaslab/kilas-flow/nodes"
)

// operatorKeyToken is the bootstrapped operator credential. Its prefix half is
// hex because that is the shape the store parses.
const operatorKeyToken = "kfa1_0badc0de_operator-secret"

// tenantDeletionBody is the JSON contract of one deletion, decoded here rather
// than through the handler's own types: what a client reads is the document, not
// the Go struct.
type tenantDeletionBody struct {
	TenantID        string           `json:"tenantId"`
	TenantRemoved   bool             `json:"tenantRemoved"`
	Removed         map[string]int64 `json:"removed"`
	DatastoreTables int              `json:"datastoreTables"`
	Binaries        struct {
		Executions int   `json:"executions"`
		Files      int   `json:"files"`
		Bytes      int64 `json:"bytes"`
	} `json:"binaries"`
}

// tenantDeletionWorld is one instance with identity on and a real purge wired
// behind the operator surface.
type tenantDeletionWorld struct {
	handler    http.Handler
	store      *repository.GORMAuthStore
	datastores *datastore.Engine
	binaries   *binary.FileStore
	keys       map[string]string
}

func newTenantDeletionWorld(t *testing.T, tenantIDs ...string) *tenantDeletionWorld {
	t.Helper()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "tenant-delete.db"),
	}, discard)
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, discard); err != nil {
		t.Fatalf("database.Migrate() error = %v", err)
	}

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	cipherKey := make([]byte, credentials.KeySize)
	for index := range cipherKey {
		cipherKey[index] = byte(index + 7)
	}
	cipher, err := credentials.NewCipher(cipherKey)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}

	authStore := repository.NewAuthStore(db.DB)
	executions := repository.NewExecutionStore(db.DB)
	datastores, err := datastore.NewEngine(db, "")
	if err != nil {
		t.Fatalf("datastore.NewEngine() error = %v", err)
	}
	binaries, err := binary.NewFileStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("binary.NewFileStore() error = %v", err)
	}

	// The real orchestrator, over the real stores. Binaries are wired because
	// the payload step is one of the things this test is about; sessions and
	// triggers are not, and a nil one skips its step by design.
	purger, err := tenantpurge.New(tenantpurge.Deps{
		Logger:     discard,
		Runs:       executions,
		Rows:       repository.NewTenantPurger(db.DB),
		Datastores: datastores,
		Binaries:   binaries,
		Protected:  []string{repository.OperatorTenantID},
	})
	if err != nil {
		t.Fatalf("tenantpurge.New() error = %v", err)
	}

	keys := map[string]string{}
	for _, tenantID := range tenantIDs {
		if _, err := authStore.EnsureTenant(ctx, tenantID, tenantID); err != nil {
			t.Fatalf("EnsureTenant(%q) error = %v", tenantID, err)
		}
		_, token, err := authStore.CreateAPIKey(ctx, repository.TenantScope{ID: tenantID}, tenantID+" key")
		if err != nil {
			t.Fatalf("CreateAPIKey(%q) error = %v", tenantID, err)
		}
		keys[tenantID] = token
	}
	// The operator credential is the one the boot path hands out: a known token
	// registered on the operator tenant, not a minted one.
	if _, err := authStore.EnsureTenant(ctx, repository.OperatorTenantID, "operator"); err != nil {
		t.Fatalf("EnsureTenant(operator) error = %v", err)
	}
	if _, err := authStore.EnsureAPIKey(ctx,
		repository.TenantScope{ID: repository.OperatorTenantID}, "operator", operatorKeyToken); err != nil {
		t.Fatalf("EnsureAPIKey(operator) error = %v", err)
	}
	keys[repository.OperatorTenantID] = operatorKeyToken

	issuer, err := auth.NewIssuer(signingKey(3), time.Hour, nil)
	if err != nil {
		t.Fatalf("auth.NewIssuer() error = %v", err)
	}

	cfg := config.Default()
	cfg.Auth.Enabled = true

	return &tenantDeletionWorld{
		handler: newTestServer(t, api.Deps{
			Config:       cfg,
			DB:           db,
			NodeRegistry: registry,
			Workflows:    repository.NewWorkflowStore(db.DB).WithWebhooks(webhook.Extract(registry, nodes.WebhookPath)),
			Executions:   executions,
			Datastores:   datastores,
			Credentials:  repository.NewCredentialStore(db.DB, cipher),
			Schedules:    repository.NewScheduleStore(db.DB),
			Tenants:      handlers.NewPrincipalTenants(""),
			AuthStore:    authStore,
			AuthIssuer:   issuer,
			TenantPurger: purger,
		}),
		store:      authStore,
		datastores: datastores,
		binaries:   binaries,
		keys:       keys,
	}
}

// tenantCall issues one request carrying an API key.
func (world *tenantDeletionWorld) call(t *testing.T, method, path, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var contents io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		contents = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, contents)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	recorder := httptest.NewRecorder()
	world.handler.ServeHTTP(recorder, request)
	return recorder
}

// deleteTenant calls the operation and decodes its answer.
func (world *tenantDeletionWorld) deleteTenant(t *testing.T, tenantID, key string, wantStatus int) tenantDeletionBody {
	t.Helper()
	recorder := world.call(t, http.MethodDelete, "/api/v1/tenants/"+tenantID, key, nil)
	if recorder.Code != wantStatus {
		t.Fatalf("DELETE /api/v1/tenants/%s = %d, want %d (body: %s)",
			tenantID, recorder.Code, wantStatus, recorder.Body)
	}
	if wantStatus != http.StatusOK {
		return tenantDeletionBody{}
	}
	var body tenantDeletionBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the deletion: %v (body: %s)", err, recorder.Body)
	}
	return body
}

// createWorkflowFor saves a draft under one tenant's key and returns its ID.
func (world *tenantDeletionWorld) createWorkflowFor(t *testing.T, key, name string) string {
	t.Helper()
	recorder := world.call(t, http.MethodPost, "/api/v1/workflows", key, workflowDraft(validManualWorkflow(name)))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/workflows = %d, want 201 (body: %s)", recorder.Code, recorder.Body)
	}
	var created workflowResource
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode the created workflow: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("the created workflow has no id: %s", recorder.Body)
	}
	return created.ID
}

func TestOperatorDeletesOneTenantAndItsSiblingSurvives(t *testing.T) {
	world := newTenantDeletionWorld(t, "acme", "globex")
	ctx := context.Background()

	acmeWorkflow := world.createWorkflowFor(t, world.keys["acme"], "Acme intake")
	globexWorkflow := world.createWorkflowFor(t, world.keys["globex"], "Globex intake")

	// A datastore with a column and a row, so the purge has a physical table to
	// drop and not just catalogue rows.
	notes, err := world.datastores.Create(ctx, "acme", "Notes",
		[]datastore.ColumnInput{{Name: "title", Type: "string"}})
	if err != nil {
		t.Fatalf("datastore Create() error = %v", err)
	}
	if _, err := world.datastores.Insert(ctx, "acme", notes.ID, map[string]any{"title": "first"}); err != nil {
		t.Fatalf("datastore Insert() error = %v", err)
	}

	// A customer's key cannot delete anybody, not even itself: this is the
	// operator's authority alone, and a 403 says nothing about what exists.
	if recorder := world.call(t, http.MethodDelete, "/api/v1/tenants/acme", world.keys["globex"], nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("DELETE /api/v1/tenants/acme as globex = %d, want 403 (body: %s)", recorder.Code, recorder.Body)
	}

	body := world.deleteTenant(t, "acme", world.keys[repository.OperatorTenantID], http.StatusOK)
	if body.TenantID != "acme" {
		t.Errorf("tenantId = %q, want acme", body.TenantID)
	}
	if !body.TenantRemoved {
		t.Error("tenantRemoved = false, want true: the tenant row went with the rest")
	}
	// The counts are the evidence the operator keeps: the workflow, its version
	// and the draft's publish trail are rows, and the datastore's catalogue rows
	// are counted beside the table that was dropped.
	for table, want := range map[string]int64{
		"workflows": 1, "tenants": 1, "datastores": 1, "datastore_columns": 1,
		"executions": 0, "webhook_deliveries": 0,
	} {
		if body.Removed[table] != want {
			t.Errorf("removed[%q] = %d, want %d", table, body.Removed[table], want)
		}
	}
	if body.Removed["workflow_versions"] < 1 {
		t.Errorf("removed[workflow_versions] = %d, want at least the version the draft created",
			body.Removed["workflow_versions"])
	}
	if body.DatastoreTables != 1 {
		t.Errorf("datastoreTables = %d, want the one physical table the engine dropped", body.DatastoreTables)
	}
	exists, err := world.datastores.TableExists(ctx, "acme", notes.ID)
	if err != nil {
		t.Fatalf("TableExists() error = %v", err)
	}
	if exists {
		t.Error("the deleted tenant's physical datastore table is still there")
	}

	// The listing is the operator's view of who is here, and acme is not.
	listed := world.call(t, http.MethodGet, "/api/v1/tenants", world.keys[repository.OperatorTenantID], nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/tenants = %d, want 200 (body: %s)", listed.Code, listed.Body)
	}
	var tenants struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &tenants); err != nil {
		t.Fatalf("decode the tenant listing: %v (body: %s)", err, listed.Body)
	}
	for _, tenant := range tenants.Items {
		if tenant.ID == "acme" {
			t.Errorf("the tenant listing still holds acme: %s", listed.Body)
		}
	}

	// The sibling is untouched: its own workflow still reads back with its own
	// key, which is the whole point of a tenant-scoped deletion.
	sibling := world.call(t, http.MethodGet, "/api/v1/workflows/"+globexWorkflow, world.keys["globex"], nil)
	if sibling.Code != http.StatusOK {
		t.Fatalf("GET the sibling's workflow = %d, want 200 (body: %s)", sibling.Code, sibling.Body)
	}
	var read workflowResource
	if err := json.Unmarshal(sibling.Body.Bytes(), &read); err != nil {
		t.Fatalf("decode the sibling's workflow: %v", err)
	}
	if read.ID != globexWorkflow || read.Name != "Globex intake" {
		t.Errorf("the sibling's workflow = %#v, want it unchanged", read)
	}

	// The deleted tenant's key is locked out from the first step onwards, so
	// nothing it holds can write while its rows are going.
	revoked := world.call(t, http.MethodGet, "/api/v1/workflows/"+acmeWorkflow, world.keys["acme"], nil)
	if revoked.Code != http.StatusUnauthorized {
		t.Errorf("the deleted tenant's key = %d, want 401 (body: %s)", revoked.Code, revoked.Body)
	}

	// Repeating the request converges: every covered table is reported at zero
	// and no tenant row is claimed.
	again := world.deleteTenant(t, "acme", world.keys[repository.OperatorTenantID], http.StatusOK)
	if again.TenantRemoved {
		t.Error("the repeat reported removing the tenant row again")
	}
	for table, count := range again.Removed {
		if count != 0 {
			t.Errorf("the repeat removed %d row(s) from %s, want a converged purge to remove nothing", count, table)
		}
	}
	if again.DatastoreTables != 0 {
		t.Errorf("the repeat dropped %d datastore table(s), want 0", again.DatastoreTables)
	}
	if len(again.Removed) != len(body.Removed) {
		t.Errorf("the repeat reported %d tables, want the same %d it reported the first time",
			len(again.Removed), len(body.Removed))
	}
}

// An unknown tenant is not a 404: a deletion means "remove everything keyed to
// this id", and rows an earlier partial deletion or a stale embed session left
// behind are exactly what has to go.
func TestOperatorDeletingAnUnknownTenantAnswers200WithZeros(t *testing.T) {
	world := newTenantDeletionWorld(t)

	body := world.deleteTenant(t, "never-existed", world.keys[repository.OperatorTenantID], http.StatusOK)
	if body.TenantID != "never-existed" {
		t.Errorf("tenantId = %q, want never-existed", body.TenantID)
	}
	if body.TenantRemoved {
		t.Error("tenantRemoved = true for a tenant that was never there")
	}
	if len(body.Removed) == 0 {
		t.Error("removed is empty, want every covered table reported at zero")
	}
	for table, count := range body.Removed {
		if count != 0 {
			t.Errorf("removed[%q] = %d, want 0", table, count)
		}
	}
}

// The operator's own tenant is refused: deleting it deletes the credential the
// caller is using, and there is nobody left to recreate it.
func TestOperatorDeletingItsOwnTenantIsRefused(t *testing.T) {
	world := newTenantDeletionWorld(t)

	recorder := world.call(t, http.MethodDelete,
		"/api/v1/tenants/"+repository.OperatorTenantID, world.keys[repository.OperatorTenantID], nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("DELETE the operator tenant = %d, want 409 (body: %s)", recorder.Code, recorder.Body)
	}
	if _, err := world.store.GetTenant(context.Background(), repository.OperatorTenantID); err != nil {
		t.Errorf("the operator tenant is gone after a refused deletion: %v", err)
	}
}
