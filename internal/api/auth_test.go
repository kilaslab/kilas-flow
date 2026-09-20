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
	"time"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/api/handlers"
	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/events"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/webhook"
	"github.com/kilaslab/kilas-flow/nodes"
)

const embedOrigin = "https://host.example"

// authenticatedAPI is one server with identity turned on, plus everything a
// test needs to act as a specific tenant against it.
type authenticatedAPI struct {
	handler     http.Handler
	store       *repository.GORMAuthStore
	issuer      *auth.Issuer
	embedIssuer *embed.Issuer
	executions  *repository.GORMExecutionStore
	broker      *events.Broker
}

func signingKey(seed byte) []byte {
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index) + seed
	}
	return key
}

// newAuthenticatedAPI builds a server with authentication on and one API key
// per named tenant, returning the keys by tenant ID.
func newAuthenticatedAPI(t *testing.T, tenantIDs ...string) (*authenticatedAPI, map[string]string) {
	t.Helper()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "auth-api.db"),
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
		cipherKey[index] = byte(index + 1)
	}
	cipher, err := credentials.NewCipher(cipherKey)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}

	issuer, err := auth.NewIssuer(signingKey(1), time.Hour, nil)
	if err != nil {
		t.Fatalf("auth.NewIssuer() error = %v", err)
	}
	// A different key from the session issuer, exactly as production wires it.
	embedIssuer, err := embed.NewIssuer(signingKey(90), []string{embedOrigin}, nil)
	if err != nil {
		t.Fatalf("embed.NewIssuer() error = %v", err)
	}

	authStore := repository.NewAuthStore(db.DB)
	keys := map[string]string{}
	for _, tenantID := range tenantIDs {
		if _, err := authStore.EnsureTenant(context.Background(), tenantID, tenantID); err != nil {
			t.Fatalf("EnsureTenant(%q) error = %v", tenantID, err)
		}
		_, token, err := authStore.CreateAPIKey(context.Background(), repository.TenantScope{ID: tenantID}, tenantID+" key")
		if err != nil {
			t.Fatalf("CreateAPIKey(%q) error = %v", tenantID, err)
		}
		keys[tenantID] = token
	}

	executions := repository.NewExecutionStore(db.DB)
	broker := events.NewBroker(events.BrokerOptions{})
	runtime, err := engine.NewService(engine.ServiceDeps{
		Executions: executions, Catalog: registry, Runner: engine.NewRunner(engine.NewRegistry()),
		Events: broker, WorkerID: "auth-test", DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("engine.NewService() error = %v", err)
	}

	cfg := config.Default()
	cfg.Auth.Enabled = true

	handler := newTestServer(t, api.Deps{
		Config:              cfg,
		DB:                  db,
		NodeRegistry:        registry,
		Workflows:           repository.NewWorkflowStore(db.DB).WithWebhooks(webhook.Extract(registry, nodes.WebhookPath)),
		Executions:          executions,
		Credentials:         repository.NewCredentialStore(db.DB, cipher),
		Schedules:           repository.NewScheduleStore(db.DB),
		ExecutionController: runtime,
		Events:              broker,
		EmbedIssuer:         embedIssuer,
		Tenants:             handlers.NewPrincipalTenants(""),
		AuthStore:           authStore,
		AuthIssuer:          issuer,
	})

	return &authenticatedAPI{
		handler: handler, store: authStore, issuer: issuer,
		embedIssuer: embedIssuer, executions: executions, broker: broker,
	}, keys
}

// call sends one request, optionally carrying an API key.
func (server *authenticatedAPI) call(t *testing.T, method, path, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var contents io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode request body: %v", err)
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
	server.handler.ServeHTTP(recorder, request)
	return recorder
}

func (server *authenticatedAPI) callJSON(t *testing.T, method, path, key string, body any, wantStatus int) map[string]any {
	t.Helper()
	recorder := server.call(t, method, path, key, body)
	if recorder.Code != wantStatus {
		t.Fatalf("%s %s status = %d, want %d (body: %s)", method, path, recorder.Code, wantStatus, recorder.Body)
	}
	if recorder.Body.Len() == 0 {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode %s %s: %v (body: %s)", method, path, err, recorder.Body)
	}
	return decoded
}

// callArray reads an operation that answers with a bare JSON array.
func (server *authenticatedAPI) callArray(t *testing.T, path, key string) []any {
	t.Helper()
	recorder := server.call(t, http.MethodGet, path, key, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200 (body: %s)", path, recorder.Code, recorder.Body)
	}
	var decoded []any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode GET %s: %v (body: %s)", path, err, recorder.Body)
	}
	return decoded
}

// createWorkflowAs saves a draft under one tenant's key and returns its ID.
func (server *authenticatedAPI) createWorkflowAs(t *testing.T, key, name string) string {
	t.Helper()
	created := server.callJSON(t, http.MethodPost, "/api/v1/workflows", key, workflowDraft(validManualWorkflow(name)), http.StatusCreated)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("created workflow has no id: %#v", created)
	}
	return id
}

// Every operation but the documented public ones has to refuse a caller that
// presents nothing at all. Before authentication existed, all of these served
// the whole installation to anyone who could reach the port.
func TestAnUnauthenticatedAPIRequestIsRefused(t *testing.T) {
	server, _ := newAuthenticatedAPI(t, "acme")

	for _, operation := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/workflows"},
		{http.MethodPost, "/api/v1/workflows"},
		{http.MethodGet, "/api/v1/credentials"},
		{http.MethodGet, "/api/v1/executions"},
		{http.MethodGet, "/api/v1/schedules"},
		{http.MethodPost, "/api/v1/embed-sessions"},
		{http.MethodGet, "/api/v1/api-keys"},
		{http.MethodGet, "/api/v1/auth/me"},
		{http.MethodGet, "/api/v1/executions/exec-1/events"},
	} {
		recorder := server.call(t, operation.method, operation.path, "", nil)
		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a credential = %d, want 401", operation.method, operation.path, recorder.Code)
		}
	}
}

// A refusal that named the tenant, the workflow or the account would turn the
// unauthenticated surface into a way to enumerate them.
func TestARefusalNamesNothingAboutTheInstallation(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme")
	workflowID := server.createWorkflowAs(t, keys["acme"], "Secret Plan")

	recorder := server.call(t, http.MethodGet, "/api/v1/workflows/"+workflowID, "", nil)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	body := recorder.Body.String()
	for _, leaked := range []string{"acme", workflowID, "Secret Plan", "default"} {
		if strings.Contains(body, leaked) {
			t.Errorf("the refusal names %q: %s", leaked, body)
		}
	}
}

// An orchestrator probes these before any account exists, and a login has to be
// reachable by definition.
func TestTheDocumentedPublicOperationsNeedNoCredential(t *testing.T) {
	server, _ := newAuthenticatedAPI(t, "acme")

	for _, path := range []string{"/api/v1/health", "/api/v1/ready"} {
		if recorder := server.call(t, http.MethodGet, path, "", nil); recorder.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, recorder.Code)
		}
	}
	// Wrong credentials, but reached: a 401 from the handler rather than the gate.
	recorder := server.call(t, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"email": "nobody@example.com", "password": "wrong",
	})
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("POST /api/v1/auth/login = %d, want the handler's own 401", recorder.Code)
	}
}

// The router is flat: the webhook prefix and the SPA hang off the same mux the
// API does. A gate mounted over the whole router would demand a credential from
// an inbound delivery, which by definition has none.
func TestThePublicSurfacesOutsideTheAPIStayOpen(t *testing.T) {
	server, _ := newAuthenticatedAPI(t, "acme")

	for _, path := range []string{"/webhook/anything", "/", "/openapi-does-not-matter"} {
		recorder := server.call(t, http.MethodGet, path, "", nil)
		if recorder.Code == http.StatusUnauthorized {
			t.Errorf("GET %s = 401; the authentication gate reached outside the API prefix", path)
		}
	}
}

func TestAnAPIKeyScopesEveryReadToItsOwnTenant(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme", "globex")

	acmeWorkflow := server.createWorkflowAs(t, keys["acme"], "Acme Onboarding")
	globexWorkflow := server.createWorkflowAs(t, keys["globex"], "Globex Billing")
	if acmeWorkflow == globexWorkflow {
		t.Fatal("the two tenants were given the same workflow")
	}

	items := server.callArray(t, "/api/v1/workflows", keys["acme"])
	if len(items) != 1 {
		t.Fatalf("acme listed %d workflows, want only its own: %#v", len(items), items)
	}
	first, _ := items[0].(map[string]any)
	if id, _ := first["id"].(string); id != acmeWorkflow {
		t.Errorf("acme's listing names workflow %q, want %q", id, acmeWorkflow)
	}
}

// The headline promise: two tenants sharing one deployment cannot reach each
// other's anything, proven end to end through HTTP.
func TestTwoTenantsCannotReachEachOthersData(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme", "globex")
	acme, globex := keys["acme"], keys["globex"]

	acmeWorkflow := server.createWorkflowAs(t, acme, "Acme Onboarding")

	acmeCredential := server.callJSON(t, http.MethodPost, "/api/v1/credentials", acme, map[string]any{
		"name": "Acme database", "type": "sqlite",
		"fields": map[string]string{"path": filepath.Join(t.TempDir(), "acme.db")},
	}, http.StatusCreated)
	credentialID, _ := acmeCredential["id"].(string)

	acmeSchedule := server.callJSON(t, http.MethodPost, "/api/v1/schedules", acme, map[string]any{
		"workflowId": acmeWorkflow, "nodeId": "manual", "cron": "0 9 * * *", "active": true,
	}, http.StatusCreated)
	scheduleID, _ := acmeSchedule["id"].(string)

	run := server.seedExecution(t, "acme", acme, acmeWorkflow)

	t.Run("a workflow", func(t *testing.T) {
		if recorder := server.call(t, http.MethodGet, "/api/v1/workflows/"+acmeWorkflow, globex, nil); recorder.Code != http.StatusNotFound {
			t.Errorf("globex reading acme's workflow = %d, want 404", recorder.Code)
		}
		if recorder := server.call(t, http.MethodDelete, "/api/v1/workflows/"+acmeWorkflow, globex, nil); recorder.Code != http.StatusNotFound {
			t.Errorf("globex deleting acme's workflow = %d, want 404", recorder.Code)
		}
	})

	t.Run("a credential", func(t *testing.T) {
		if recorder := server.call(t, http.MethodGet, "/api/v1/credentials/"+credentialID, globex, nil); recorder.Code != http.StatusNotFound {
			t.Errorf("globex reading acme's credential = %d, want 404", recorder.Code)
		}
		listed := server.callArray(t, "/api/v1/credentials", globex)
		if body, _ := json.Marshal(listed); strings.Contains(string(body), "Acme database") {
			t.Errorf("globex's credential listing names acme's credential: %s", body)
		}
	})

	t.Run("an execution", func(t *testing.T) {
		if recorder := server.call(t, http.MethodGet, "/api/v1/executions/"+run.ID, globex, nil); recorder.Code != http.StatusNotFound {
			t.Errorf("globex reading acme's execution = %d, want 404", recorder.Code)
		}
	})

	t.Run("a schedule", func(t *testing.T) {
		if recorder := server.call(t, http.MethodDelete, "/api/v1/schedules/"+scheduleID, globex, nil); recorder.Code != http.StatusNotFound {
			t.Errorf("globex deleting acme's schedule = %d, want 404", recorder.Code)
		}
		listed := server.call(t, http.MethodGet, "/api/v1/schedules", globex, nil)
		if strings.Contains(listed.Body.String(), scheduleID) {
			t.Errorf("globex's schedule listing names acme's schedule: %s", listed.Body)
		}
	})

	t.Run("an event stream", func(t *testing.T) {
		// The stream is opened with a ticket, and a ticket is only minted for
		// an execution the caller can already read, so the refusal lands here
		// rather than on a connection that silently returns no events.
		if recorder := server.call(t, http.MethodPost, "/api/v1/stream-tickets", globex, map[string]any{
			"executionId": run.ID,
		}); recorder.Code != http.StatusNotFound {
			t.Errorf("globex minting a ticket for acme's execution = %d, want 404", recorder.Code)
		}
	})

	t.Run("an API key", func(t *testing.T) {
		listed := server.callJSON(t, http.MethodGet, "/api/v1/api-keys", globex, nil, http.StatusOK)
		items, _ := listed["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("globex sees %d keys, want only its own", len(items))
		}
		entry, _ := items[0].(map[string]any)
		if label, _ := entry["label"].(string); label != "globex key" {
			t.Errorf("globex's key listing shows %q", label)
		}
	})
}

// Minting an embed session used to need no credential at all, which let anyone
// who could reach the port hand themselves a token for any workflow.
func TestMintingAnEmbedSessionNeedsAPrincipalAndUsesItsTenant(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme", "globex")
	acmeWorkflow := server.createWorkflowAs(t, keys["acme"], "Acme Onboarding")

	if recorder := server.call(t, http.MethodPost, "/api/v1/embed-sessions", "", map[string]any{
		"workflowId": acmeWorkflow, "scopes": []string{"workflow:read"}, "origin": embedOrigin,
	}); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("minting without a credential = %d, want 401", recorder.Code)
	}

	// Another tenant's key cannot mint against acme's workflow, because the
	// handler looks the workflow up inside the caller's own tenant first.
	if recorder := server.call(t, http.MethodPost, "/api/v1/embed-sessions", keys["globex"], map[string]any{
		"workflowId": acmeWorkflow, "scopes": []string{"workflow:read"}, "origin": embedOrigin,
	}); recorder.Code != http.StatusNotFound {
		t.Fatalf("globex minting for acme's workflow = %d, want 404", recorder.Code)
	}

	minted := server.callJSON(t, http.MethodPost, "/api/v1/embed-sessions", keys["acme"], map[string]any{
		"workflowId": acmeWorkflow, "scopes": []string{"workflow:read"}, "origin": embedOrigin,
	}, http.StatusCreated)
	token, _ := minted["token"].(string)
	if token == "" {
		t.Fatalf("no token in %#v", minted)
	}

	session, err := server.embedIssuer.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	// The session carries the minting principal's tenant. It used to carry
	// "default" for every host and every customer alike.
	if session.TenantID != "acme" {
		t.Errorf("minted session tenant = %q, want acme", session.TenantID)
	}
}

// A request holding both credentials must end up with the embed session's
// authority, which is the narrower of the two: it names one workflow where the
// key names a whole tenant.
func TestAnEmbedTokenBeatsAnAPIKeyOnTheSameRequest(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme", "globex")
	acmeWorkflow := server.createWorkflowAs(t, keys["acme"], "Acme Onboarding")
	server.createWorkflowAs(t, keys["globex"], "Globex Billing")

	minted := server.callJSON(t, http.MethodPost, "/api/v1/embed-sessions", keys["acme"], map[string]any{
		"workflowId": acmeWorkflow, "scopes": []string{"workflow:read"}, "origin": embedOrigin,
	}, http.StatusCreated)
	embedToken, _ := minted["token"].(string)

	both := func(method, path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, nil)
		request.Header.Set("Authorization", "Bearer "+embedToken)
		request.Header.Set("X-KilasFlow-Embed", embedToken)
		// Globex's key rides along on a header of its own; if it were read,
		// the request would be scoped to globex instead.
		request.Header.Set("X-Api-Key", keys["globex"])
		recorder := httptest.NewRecorder()
		server.handler.ServeHTTP(recorder, request)
		return recorder
	}

	// The embed session's own workflow is readable, under acme's tenant.
	if recorder := both(http.MethodGet, "/api/v1/workflows/"+acmeWorkflow); recorder.Code != http.StatusOK {
		t.Errorf("reading the embedded workflow = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}
	// Listing every workflow is outside an embed session's authority, and the
	// key riding along must not buy it back.
	if recorder := both(http.MethodGet, "/api/v1/workflows"); recorder.Code != http.StatusForbidden {
		t.Errorf("listing workflows with an embed token = %d, want 403", recorder.Code)
	}
}

// EventSource cannot set headers, so a caller exchanges its credential for a
// ticket and spends it in the query string.
func TestTheEventStreamAuthenticatesWithATicketRatherThanAHeader(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme")
	acmeWorkflow := server.createWorkflowAs(t, keys["acme"], "Acme Onboarding")
	run := server.seedExecution(t, "acme", keys["acme"], acmeWorkflow)

	minted := server.callJSON(t, http.MethodPost, "/api/v1/stream-tickets", keys["acme"], map[string]any{
		"executionId": run.ID,
	}, http.StatusCreated)
	ticket, _ := minted["ticket"].(string)
	if ticket == "" {
		t.Fatalf("no ticket in %#v", minted)
	}

	// A terminal event is published first so the stream replays it and closes,
	// rather than holding the test open waiting for a run that already ended.
	server.broker.Publish(events.Event{
		TenantID: "acme", ExecutionID: run.ID, Type: events.ExecutionCompleted,
		Status: execution.StatusSucceeded,
	})

	live := httptest.NewServer(server.handler)
	t.Cleanup(live.Close)
	streamURL := live.URL + "/api/v1/executions/" + run.ID + "/events?ticket=" + ticket

	// No Authorization header anywhere: this is what an EventSource can send.
	opened, err := http.Get(streamURL)
	if err != nil {
		t.Fatalf("open the stream: %v", err)
	}
	body, _ := io.ReadAll(opened.Body)
	_ = opened.Body.Close()
	if opened.StatusCode != http.StatusOK {
		t.Fatalf("opening the stream with a ticket = %d, want 200 (body: %s)", opened.StatusCode, body)
	}
	if !strings.Contains(string(body), string(events.ExecutionCompleted)) {
		t.Errorf("the stream carried no events: %s", body)
	}

	// A ticket recovered from a proxy log has to be worthless, so the second
	// use of one is refused even inside its lifetime.
	replayed, err := http.Get(streamURL)
	if err != nil {
		t.Fatalf("replay the ticket: %v", err)
	}
	_ = replayed.Body.Close()
	if replayed.StatusCode != http.StatusUnauthorized {
		t.Errorf("replaying the ticket = %d, want 401", replayed.StatusCode)
	}
}

func TestARevokedKeyStopsWorkingAgainstTheAPI(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme")

	listed := server.callJSON(t, http.MethodGet, "/api/v1/api-keys", keys["acme"], nil, http.StatusOK)
	items, _ := listed["items"].([]any)
	entry, _ := items[0].(map[string]any)
	keyID, _ := entry["id"].(string)

	server.callJSON(t, http.MethodDelete, "/api/v1/api-keys/"+keyID, keys["acme"], nil, http.StatusOK)

	if recorder := server.call(t, http.MethodGet, "/api/v1/workflows", keys["acme"], nil); recorder.Code != http.StatusUnauthorized {
		t.Errorf("a revoked key = %d, want 401", recorder.Code)
	}
}

// A key is returned whole once, at creation, and never again.
func TestACreatedKeyIsShownOnceAndNeverListed(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme")

	created := server.callJSON(t, http.MethodPost, "/api/v1/api-keys", keys["acme"], map[string]any{
		"label": "deploy pipeline",
	}, http.StatusCreated)
	token, _ := created["token"].(string)
	if token == "" {
		t.Fatalf("creation returned no token: %#v", created)
	}
	// The new key works, which is what makes losing it costly enough to matter.
	if recorder := server.call(t, http.MethodGet, "/api/v1/workflows", token, nil); recorder.Code != http.StatusOK {
		t.Fatalf("the newly minted key = %d, want 200", recorder.Code)
	}

	listed := server.call(t, http.MethodGet, "/api/v1/api-keys", keys["acme"], nil)
	if strings.Contains(listed.Body.String(), token) {
		t.Errorf("a key listing returned the whole token: %s", listed.Body)
	}
	_, secret, _ := auth.SplitKey(token)
	if strings.Contains(listed.Body.String(), secret) {
		t.Errorf("a key listing returned the key's secret: %s", listed.Body)
	}
}

func TestSigningInReturnsACookieThatAuthenticates(t *testing.T) {
	server, _ := newAuthenticatedAPI(t, "acme")
	hash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if _, err := server.store.CreateUser(context.Background(),
		repository.TenantScope{ID: "acme"}, "owner@acme.example", "Owner", hash); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	recorder := server.call(t, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"email": "owner@acme.example", "password": "hunter2",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("login = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login set %d cookies, want 1", len(cookies))
	}
	session := cookies[0]
	if !session.HttpOnly {
		t.Error("the session cookie is readable by scripts in the page")
	}

	// The cookie alone authenticates, which is what an EventSource and a plain
	// fetch from the dashboard both rely on.
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	request.AddCookie(session)
	authenticated := httptest.NewRecorder()
	server.handler.ServeHTTP(authenticated, request)
	if authenticated.Code != http.StatusOK {
		t.Fatalf("GET /auth/me with the session cookie = %d, want 200 (body: %s)", authenticated.Code, authenticated.Body)
	}
	var me struct {
		TenantID string `json:"tenantId"`
		Email    string `json:"email"`
		Kind     string `json:"kind"`
	}
	if err := json.Unmarshal(authenticated.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if me.TenantID != "acme" || me.Email != "owner@acme.example" || me.Kind != string(auth.KindSession) {
		t.Errorf("me = %#v, want the signed-in owner of acme", me)
	}
}

// Unknown address and wrong password have to be indistinguishable, or the login
// form becomes a way to find out who has an account here.
func TestAFailedLoginSaysNothingAboutWhoHasAnAccount(t *testing.T) {
	server, _ := newAuthenticatedAPI(t, "acme")
	hash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if _, err := server.store.CreateUser(context.Background(),
		repository.TenantScope{ID: "acme"}, "owner@acme.example", "Owner", hash); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	wrongPassword := server.call(t, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"email": "owner@acme.example", "password": "not it",
	})
	unknownAddress := server.call(t, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"email": "nobody@acme.example", "password": "not it",
	})

	if wrongPassword.Code != http.StatusUnauthorized || unknownAddress.Code != http.StatusUnauthorized {
		t.Fatalf("statuses = %d and %d, want both 401", wrongPassword.Code, unknownAddress.Code)
	}
	if wrongPassword.Body.String() != unknownAddress.Body.String() {
		t.Errorf("the two refusals differ:\n  wrong password: %s\n  unknown address: %s",
			wrongPassword.Body, unknownAddress.Body)
	}
}

// An installation that has not turned authentication on keeps working exactly
// as it did, under the default tenant its data is already stored against.
func TestAnInstallationWithAuthenticationOffIsUnchanged(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/workflows", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("listing workflows with authentication off = %d, want 200", recorder.Code)
	}
}

// seedExecution stores a finished run for one tenant's workflow.
//
// Written straight to the repository rather than through the API, because the
// point of the test is who may read a run back, not how one is started.
func (server *authenticatedAPI) seedExecution(t *testing.T, tenantID, key, workflowID string) execution.Record {
	t.Helper()
	stored := server.callJSON(t, http.MethodGet, "/api/v1/workflows/"+workflowID, key, nil, http.StatusOK)
	latest, _ := stored["latestVersion"].(map[string]any)
	versionID, _ := latest["id"].(string)
	if versionID == "" {
		t.Fatalf("workflow %q has no latest version: %#v", workflowID, stored)
	}

	startedAt := time.Now().UTC().Add(-time.Minute)
	finishedAt := startedAt.Add(time.Second)
	record, err := server.executions.Create(context.Background(), repository.TenantScope{ID: tenantID}, execution.Record{
		WorkflowID:        workflowID,
		WorkflowVersionID: versionID,
		Status:            execution.StatusSucceeded,
		Trigger:           execution.TriggerManual,
		StartedAt:         startedAt,
		FinishedAt:        &finishedAt,
	})
	if err != nil {
		t.Fatalf("seed an execution: %v", err)
	}
	return record
}
