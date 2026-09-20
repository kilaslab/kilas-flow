package webhook_test

import (
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

	"github.com/golang-jwt/jwt/v5"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/events"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/webhook"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

type harness struct {
	handler     http.Handler
	workflows   *repository.GORMWorkflowStore
	credentials *repository.GORMCredentialStore
	runtime     *engine.Service
	runner      *recordingRunner
	registry    *node.Registry
	tenant      repository.TenantScope
}

// recordingRunner remembers what a delivery queued.
//
// The acknowledgement body is n8n's own — `{"message":"Workflow was started"}` —
// and carries no execution id, so a test that wants to inspect the run reads the
// id from here instead of from the response.
type recordingRunner struct {
	*engine.Service
	queued []string
}

func (runner *recordingRunner) QueueWebhook(ctx context.Context, binding repository.WebhookBinding, payload json.RawMessage) (execution.Record, error) {
	record, err := runner.Service.QueueWebhook(ctx, binding, payload)
	if err == nil {
		runner.queued = append(runner.queued, record.ID)
	}
	return record, err
}

// lastExecution is the execution the most recent delivery queued.
func (h harness) lastExecution(t *testing.T) string {
	t.Helper()
	if len(h.runner.queued) == 0 {
		t.Fatal("no execution was queued")
	}
	return h.runner.queued[len(h.runner.queued)-1]
}

// queuedCount is how many executions deliveries have queued.
func (h harness) queuedCount() int { return len(h.runner.queued) }

// drain runs queued work until the queue is empty, so a test never sleeps
// waiting for a background worker.
func (h harness) drain(t *testing.T) {
	t.Helper()
	for range 20 {
		worked, err := h.runtime.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("RunOnce() error = %v", err)
		}
		if !worked {
			return
		}
	}
	t.Fatal("the execution queue did not drain")
}

func newHarness(t *testing.T) harness {
	t.Helper()
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "webhook.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	executors := engine.NewRegistry()
	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	if err := nodes.RegisterExecutors(executors, policy, sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	key := make([]byte, credentials.KeySize)
	for index := range key {
		key[index] = byte(index + 3)
	}
	cipher, err := credentials.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}

	workflows := repository.NewWorkflowStore(db.DB).
		WithWebhooks(webhook.Extract(registry, nodes.WebhookPath))
	credentialStore := repository.NewCredentialStore(db.DB, cipher)
	executions := repository.NewExecutionStore(db.DB)
	// The broker is what carries a Respond to Webhook node's answer to the
	// waiting HTTP boundary, so the harness wires the same one into the engine
	// and the handler — which is what composition does.
	broker := events.NewBroker(events.BrokerOptions{})
	runtime, err := engine.NewService(engine.ServiceDeps{
		Executions: executions, Catalog: registry, Runner: engine.NewRunner(executors),
		Credentials: credentialStore, WorkerID: "webhook-test", DefaultTimeout: 5 * time.Second,
		Events: broker,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	runner := &recordingRunner{Service: runtime}

	// The trigger registry is what tells the boundary how each trigger type
	// shapes a delivery, so the harness wires it exactly as composition does.
	// Without it every delivery falls back to KilasFlow's own envelope and the
	// tests would assert a shape no deployment produces.
	triggers := webhook.NewRegistry()
	if err := nodes.RegisterTriggerKinds(triggers); err != nil {
		t.Fatalf("RegisterTriggerKinds() error = %v", err)
	}

	return harness{
		handler: webhook.NewHandler(workflows, runner, credentialStore, broker, webhook.Limits{MaxBodyBytes: 512, ResponseTimeout: 2 * time.Second}).
			WithTriggers(triggers),
		workflows:   workflows,
		credentials: credentialStore,
		runtime:     runtime,
		runner:      runner,
		registry:    registry,
		tenant:      repository.TenantScope{ID: repository.DefaultTenantID},
	}
}

func webhookDocument(name string, triggerParameters map[string]any, extra ...workflow.Node) workflow.Document {
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		Name:          name,
		Nodes: append([]workflow.Node{{
			ID: "trigger", Name: "Webhook", Type: nodes.WebhookNodeType, TypeVersion: workflow.V(1),
			Parameters: triggerParameters,
		}}, extra...),
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	}
	for index := range extra {
		source := "trigger"
		if index > 0 {
			source = extra[index-1].ID
		}
		document.Connections = append(document.Connections, workflow.Connection{
			ID:     "c" + extra[index].ID,
			Kind:   workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: source, Port: "main"},
			Target: workflow.Endpoint{NodeID: extra[index].ID, Port: "main"},
		})
	}
	return document
}

func (h harness) activate(t *testing.T, document workflow.Document) workflow.StoredWorkflow {
	t.Helper()
	stored, err := h.workflows.SaveDraft(context.Background(), h.tenant, document)
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	active, err := h.workflows.Activate(context.Background(), h.tenant, stored.ID, h.registry)
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	return active
}

// url is the public address of a workflow's first webhook trigger.
//
// The route is opaque and minted at activation, so a test can no longer build
// the URL from the path it configured — which is the whole point: two tenants
// importing the same template get different addresses for the same path.
func (h harness) url(t *testing.T, stored workflow.StoredWorkflow) string {
	t.Helper()
	routes, err := h.workflows.WebhookRoutes(context.Background(), h.tenant, stored.ID)
	if err != nil {
		t.Fatalf("WebhookRoutes() error = %v", err)
	}
	if len(routes) == 0 {
		t.Fatalf("workflow %s has no webhook route", stored.ID)
	}
	return "/webhook/" + routes[0].Route
}

func TestWebhookAnswersImmediatelyAndQueuesANormalExecution(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Immediate", map[string]any{
		"path": "orders", "httpMethod": http.MethodPost, "responseMode": "immediate", "responseCode": float64(202),
	}))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{"id":7}`))
	h.handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want the configured 202 (body: %s)", recorder.Code, recorder.Body)
	}
	// n8n's own acknowledgement, not KilasFlow's {executionId, status}.
	var body struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response = %v", err)
	}
	if body.Message != "Workflow was started" {
		t.Fatalf("response = %#v, want n8n's acknowledgement", body)
	}

	// The webhook must go through the same durable queue as a manual run.
	h.drain(t)
	record, err := h.runtime.Get(context.Background(), h.tenant, h.lastExecution(t))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if record.Status != "succeeded" || record.Trigger != "webhook" {
		t.Fatalf("execution = %s/%s, want a succeeded webhook run", record.Status, record.Trigger)
	}
}

func TestWebhookRespondsFromARespondToWebhookNode(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Responder", map[string]any{
		"path": "reply", "httpMethod": http.MethodPost, "responseMode": "responseNode",
	}, workflow.Node{
		ID: "respond", Name: "Respond to Webhook", Type: nodes.RespondNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{
			"respondWith":     "json",
			"responseCode":    float64(201),
			"responseBody":    `{"ok":true}`,
			"responseHeaders": map[string]any{"X-Kilas": "yes"},
		},
	}))

	// The handler waits for the run, so a worker has to be draining alongside.
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			worked, _ := h.runtime.RunOnce(context.Background())
			if !worked {
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()

	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`)))
	<-done

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want the node's 201 (body: %s)", recorder.Code, recorder.Body)
	}
	if got := recorder.Body.String(); got != `{"ok":true}` {
		t.Errorf("body = %q, want the node's body", got)
	}
	if got := recorder.Header().Get("X-Kilas"); got != "yes" {
		t.Errorf("X-Kilas = %q, want the node's header", got)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json for a JSON body", got)
	}
}

func TestWebhookAnswersFromTheDurableResponseWhenTheBoundarySeesNoEvent(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Responder elsewhere", map[string]any{
		"path": "reply-durable", "httpMethod": http.MethodPost, "responseMode": "responseNode",
	}, workflow.Node{
		ID: "respond", Name: "Respond to Webhook", Type: nodes.RespondNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{
			"respondWith":     "json",
			"responseCode":    float64(201),
			"responseBody":    `{"ok":true}`,
			"responseHeaders": map[string]any{"X-Kilas": "yes"},
		},
	}))

	// A split api+worker deployment: the graph runs in a worker process and the
	// caller is held by an API process. The relay between them carries event
	// identifiers rather than data, so this boundary's broker never sees the
	// node's response event — a nil broker is exactly that.
	triggers := webhook.NewRegistry()
	if err := nodes.RegisterTriggerKinds(triggers); err != nil {
		t.Fatalf("RegisterTriggerKinds() error = %v", err)
	}
	boundary := webhook.NewHandler(h.workflows, h.runner, h.credentials, nil, webhook.Limits{
		MaxBodyBytes: 512, ResponseTimeout: 5 * time.Second,
	}).WithTriggers(triggers)

	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			worked, _ := h.runtime.RunOnce(context.Background())
			if !worked {
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()

	recorder := httptest.NewRecorder()
	boundary.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`)))
	<-done

	// The answer was persisted with the Respond node's own run, so the boundary
	// that never saw the event still answers with the node's status, body and
	// headers — not the empty 200 a responseNode run used to get.
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want the node's 201 (body: %s)", recorder.Code, recorder.Body)
	}
	if got := recorder.Body.String(); got != `{"ok":true}` {
		t.Errorf("body = %q, want the node's body", got)
	}
	if got := recorder.Header().Get("X-Kilas"); got != "yes" {
		t.Errorf("X-Kilas = %q, want the node's header", got)
	}
}

func TestWebhookAnswersEmptyWhenNoResponseNodeWasReached(t *testing.T) {
	h := newHarness(t)
	// Configured to answer from a node, but the graph has none.
	active := h.activate(t, webhookDocument("No responder", map[string]any{
		"path": "silent", "httpMethod": http.MethodPost, "responseMode": "responseNode",
	}))

	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if worked, _ := h.runtime.RunOnce(context.Background()); !worked {
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()

	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`)))

	// n8n answers 200 with an empty body: the run finished cleanly, it simply
	// had nothing to say. A 500 here told the caller the workflow had broken.
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}
	if got := strings.TrimSpace(recorder.Body.String()); got != "" {
		t.Errorf("body = %q, want nothing", got)
	}
}

// TestWebhookBindsEveryMethodTheNodeSelected covers the precedence between the
// two parameters that name a method.
//
// A node edited into multi-method mode keeps `httpMethod` holding the single
// value it had before, and the node's own definition reads `multipleMethods`
// first. Binding extraction read `httpMethod` first, so the GET half of a
// two-method endpoint answered 404 to every caller while the POST half worked.
func TestWebhookBindsEveryMethodTheNodeSelected(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Two methods", map[string]any{
		"path": "items", "responseMode": "immediate",
		"multipleMethods": true, "httpMethods": []any{"GET", "POST"}, "httpMethod": "POST",
	}))

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		recorder := httptest.NewRecorder()
		h.handler.ServeHTTP(recorder, httptest.NewRequest(method, h.url(t, active), strings.NewReader(`{}`)))
		if recorder.Code != http.StatusOK {
			t.Errorf("%s status = %d, want the route to answer (body: %s)", method, recorder.Code, recorder.Body)
		}
	}
	h.drain(t)
}

// TestFormPageIsRefusedToAnAddressOutsideTheAllowList covers the order the page
// was served in.
//
// The hosted page was written before the allow-list and the credential were
// checked, so a form restricted by IP served its title, labels and options to
// any caller who found the URL — while the submission it exists for was
// refused.
func TestFormPageIsRefusedToAnAddressOutsideTheAllowList(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, formDocument(map[string]any{
		"path": "secret-form", "formTitle": "Staff only",
		"formFields": map[string]any{"values": []any{
			map[string]any{"fieldLabel": "Name", "fieldType": "text"},
		}},
		"responseMode": "immediate",
		"options":      map[string]any{"ipWhitelist": "10.0.0.1"},
	}))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, h.url(t, active), nil)
	request.RemoteAddr = "203.0.113.9:1234"
	h.handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want the page refused (body: %s)", recorder.Code, recorder.Body)
	}
	if strings.Contains(recorder.Body.String(), "Staff only") {
		t.Errorf("the refused page still carried the form:\n%s", recorder.Body)
	}
}

func TestWebhookTimesOutRatherThanHangingForever(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Slow", map[string]any{
		"path": "slow", "httpMethod": http.MethodPost, "responseMode": "lastNode",
	}))

	// No worker drains the queue, so the run never finishes.
	recorder := httptest.NewRecorder()
	start := time.Now()
	h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`)))

	if recorder.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504 (body: %s)", recorder.Code, recorder.Body)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the request took %s, want it bounded by the response timeout", elapsed)
	}
}

func TestWebhookRefusesUnknownInactiveAndWrongMethodRequests(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Bound", map[string]any{
		"path": "bound", "httpMethod": http.MethodPost, "responseMode": "immediate",
	}))

	// Captured before deactivating, because the route is only listed while the
	// workflow is active — and this URL has to keep being addressable after it
	// stops answering.
	url := h.url(t, active)

	for name, request := range map[string]*http.Request{
		// A well-formed route that was never minted, so it cannot be confused
		// with a malformed one.
		"unknown route": httptest.NewRequest(http.MethodPost, "/webhook/0123456789abcdef0123456789abcdef", nil),
		"wrong method":  httptest.NewRequest(http.MethodGet, url, nil),
		"empty path":    httptest.NewRequest(http.MethodPost, "/webhook/", nil),
	} {
		recorder := httptest.NewRecorder()
		h.handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", name, recorder.Code)
		}
	}

	// Deactivating must stop the endpoint answering immediately.
	if _, err := h.workflows.Deactivate(context.Background(), h.tenant, active.ID); err != nil {
		t.Fatalf("Deactivate() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, url, nil))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("deactivated status = %d, want 404", recorder.Code)
	}
}

func TestWebhookEnforcesBasicAuthentication(t *testing.T) {
	h := newHarness(t)
	credential, err := h.credentials.Create(context.Background(), h.tenant, credentials.Record{
		Name: "Inbound", Type: "httpBasicAuth", Fields: map[string]string{"user": "ada", "password": "hunter2"},
	})
	if err != nil {
		t.Fatalf("Create() credential error = %v", err)
	}

	document := webhookDocument("Secured", map[string]any{
		"path": "secure", "httpMethod": http.MethodPost, "responseMode": "immediate", "authentication": "basicAuth",
	})
	document.Nodes[0].Credentials = map[string]string{"httpBasicAuth": credential.ID}
	active := h.activate(t, document)

	anonymous := httptest.NewRecorder()
	h.handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`)))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401 (body: %s)", anonymous.Code, anonymous.Body)
	}
	if anonymous.Header().Get("WWW-Authenticate") == "" {
		t.Error("a 401 was returned without a WWW-Authenticate challenge")
	}

	wrong := httptest.NewRecorder()
	badRequest := httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`))
	badRequest.SetBasicAuth("ada", "wrong")
	h.handler.ServeHTTP(wrong, badRequest)
	if wrong.Code != http.StatusUnauthorized {
		t.Errorf("wrong password status = %d, want 401", wrong.Code)
	}

	authorized := httptest.NewRecorder()
	goodRequest := httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`))
	goodRequest.SetBasicAuth("ada", "hunter2")
	h.handler.ServeHTTP(authorized, goodRequest)
	if authorized.Code != http.StatusOK {
		t.Errorf("authorized status = %d, want 200 (body: %s)", authorized.Code, authorized.Body)
	}
}

// An endpoint that asks for authentication but has nothing to check against
// used to activate and then answer 500 to every caller. Refusing activation
// names the problem while it can still be fixed, and keeps a secured n8n
// endpoint from going live unprotected.
func TestWebhookConfiguredToAuthenticateWithoutACredentialIsRefusedAtActivation(t *testing.T) {
	h := newHarness(t)
	stored, err := h.workflows.SaveDraft(context.Background(), h.tenant, webhookDocument("Misconfigured", map[string]any{
		"path": "half-secured", "httpMethod": http.MethodPost, "responseMode": "immediate", "authentication": "headerAuth",
	}))
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	_, err = h.workflows.Activate(context.Background(), h.tenant, stored.ID, h.registry)
	if err == nil {
		t.Fatal("a webhook configured to authenticate was activated without a credential")
	}
	if !strings.Contains(err.Error(), "httpHeaderAuth") {
		t.Errorf("error = %v, want it to name the credential type the node needs", err)
	}
}

// A jwtAuth webhook needs a jwtAuth credential, exactly like the other two
// modes: the mode is offered now, and an endpoint that asks for a verification
// nothing can perform is still refused at activation rather than published
// open.
func TestJWTWebhookNeedsACredentialBeforeActivation(t *testing.T) {
	h := newHarness(t)
	document := webhookDocument("JWT", map[string]any{
		"path": "jwt", "httpMethod": http.MethodPost, "responseMode": "immediate", "authentication": "jwtAuth",
	})
	stored, err := h.workflows.SaveDraft(context.Background(), h.tenant, document)
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	if _, err := h.workflows.Activate(context.Background(), h.tenant, stored.ID, h.registry); err == nil {
		t.Fatal("a jwtAuth webhook activated without a credential")
	} else if !strings.Contains(err.Error(), "jwtAuth") {
		t.Errorf("error = %v, want it to name the credential type the node needs", err)
	}

	// A credential of another type, attached under its own key, is not a
	// jwtAuth credential: the reference the validator looks for is absent, so
	// the endpoint remains unverifiable and stays refused.
	header, err := h.credentials.Create(context.Background(), h.tenant, credentials.Record{
		Name: "Wrong type", Type: "httpHeaderAuth", Fields: map[string]string{"name": "X-Api-Key", "value": "k-1"},
	})
	if err != nil {
		t.Fatalf("Create() credential error = %v", err)
	}
	document.ID = stored.ID
	document.Nodes[0].Credentials = map[string]string{"httpHeaderAuth": header.ID}
	if _, err := h.workflows.SaveDraft(context.Background(), h.tenant, document); err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	if _, err := h.workflows.Activate(context.Background(), h.tenant, stored.ID, h.registry); err == nil {
		t.Fatal("a jwtAuth webhook activated with a credential of another type attached")
	}
}

func TestJWTWebhookRunsForAValidTokenAndRefusesOthers(t *testing.T) {
	h := newHarness(t)
	credential, err := h.credentials.Create(context.Background(), h.tenant, credentials.Record{
		Name: "Inbound JWT", Type: "jwtAuth",
		Fields: map[string]string{"keyType": "passphrase", "secret": "hunter2-secret", "algorithm": "HS256"},
	})
	if err != nil {
		t.Fatalf("Create() credential error = %v", err)
	}

	document := webhookDocument("Armed", map[string]any{
		"path": "jwt-armed", "httpMethod": http.MethodPost, "responseMode": "immediate", "authentication": "jwtAuth",
	})
	document.Nodes[0].Credentials = map[string]string{"jwtAuth": credential.ID}
	active := h.activate(t, document)

	deliver := func(target workflow.StoredWorkflow, token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, h.url(t, target), strings.NewReader(`{"note":"hi"}`))
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		recorder := httptest.NewRecorder()
		h.handler.ServeHTTP(recorder, request)
		return recorder
	}
	tokenFor := func(secret string, claims jwt.MapClaims) string {
		t.Helper()
		signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
		if err != nil {
			t.Fatalf("SignedString() error = %v", err)
		}
		return signed
	}

	valid := tokenFor("hunter2-secret", jwt.MapClaims{"sub": "ada"})
	if recorder := deliver(active, valid); recorder.Code != http.StatusOK {
		t.Fatalf("a valid token status = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}
	h.drain(t)
	record, err := h.runtime.Get(context.Background(), h.tenant, h.lastExecution(t))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	var item map[string]any
	if err := json.Unmarshal(record.Input, &item); err != nil {
		t.Fatalf("the queued payload is not an item: %v (%s)", err, record.Input)
	}
	// n8n's own contract: the verified payload reaches the workflow as
	// `jwtPayload`, so an imported workflow reading `$json.jwtPayload.sub`
	// resolves.
	claims, ok := item["jwtPayload"].(map[string]any)
	if !ok || claims["sub"] != "ada" {
		t.Errorf("jwtPayload = %#v, want the token's subject", item["jwtPayload"])
	}

	refused := deliver(active, "")
	if refused.Code != http.StatusUnauthorized {
		t.Errorf("no token status = %d, want 401", refused.Code)
	}
	if challenge := refused.Header().Get("WWW-Authenticate"); challenge != "" {
		t.Errorf("a refused JWT caller was sent the basic-auth challenge %q", challenge)
	}
	// The token is signed, well-formed and unexpired — with somebody else's
	// secret. Only the credential's own secret may open the endpoint.
	if wrong := deliver(active, tokenFor("other-secret", jwt.MapClaims{"sub": "ada"})); wrong.Code != http.StatusUnauthorized {
		t.Errorf("a token signed with another secret = %d, want 401", wrong.Code)
	}

	// A reference under the jwtAuth key that resolves to a credential of
	// another type is a misconfiguration: using it would mean verifying tokens
	// with an API key.
	header, err := h.credentials.Create(context.Background(), h.tenant, credentials.Record{
		Name: "Wrong type", Type: "httpHeaderAuth", Fields: map[string]string{"name": "X-Api-Key", "value": "k-1"},
	})
	if err != nil {
		t.Fatalf("Create() credential error = %v", err)
	}
	mistyped := webhookDocument("Mistyped", map[string]any{
		"path": "jwt-mistyped", "httpMethod": http.MethodPost, "responseMode": "immediate", "authentication": "jwtAuth",
	})
	mistyped.Nodes[0].Credentials = map[string]string{"jwtAuth": header.ID}
	if recorder := deliver(h.activate(t, mistyped), valid); recorder.Code != http.StatusInternalServerError {
		t.Errorf("a jwtAuth reference to an httpHeaderAuth credential = %d, want 500", recorder.Code)
	}
}

// A node carrying more than one credential authenticates with the one its mode
// names.
//
// Ranging the binding's `$credentials` map and taking the first entry made the
// answer depend on Go's randomised map iteration: with the Webhook node now
// offering three credential types and a document free to carry more than one,
// a legitimate caller was refused intermittently — a 500 that no configuration
// explained. Each attempt re-reads the binding, so the loop is what a
// first-entry lookup fails on.
func TestWebhookAuthenticationUsesTheCredentialItsModeNames(t *testing.T) {
	h := newHarness(t)
	jwtCredential, err := h.credentials.Create(context.Background(), h.tenant, credentials.Record{
		Name: "Inbound JWT", Type: "jwtAuth",
		Fields: map[string]string{"keyType": "passphrase", "secret": "hunter2-secret", "algorithm": "HS256"},
	})
	if err != nil {
		t.Fatalf("Create() credential error = %v", err)
	}
	header, err := h.credentials.Create(context.Background(), h.tenant, credentials.Record{
		Name: "Also attached", Type: "httpHeaderAuth", Fields: map[string]string{"name": "X-Api-Key", "value": "k-1"},
	})
	if err != nil {
		t.Fatalf("Create() credential error = %v", err)
	}

	document := webhookDocument("Two credentials", map[string]any{
		"path": "jwt-two", "httpMethod": http.MethodPost, "responseMode": "immediate", "authentication": "jwtAuth",
	})
	document.Nodes[0].Credentials = map[string]string{"jwtAuth": jwtCredential.ID, "httpHeaderAuth": header.ID}
	active := h.activate(t, document)

	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "ada"}).SignedString([]byte("hunter2-secret"))
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}
	for attempt := range 5 {
		request := httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		h.handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("attempt %d: status = %d, want 200 (body: %s)", attempt, recorder.Code, recorder.Body)
		}
	}

	// The other direction: header auth with the same two credentials attached.
	// A first-entry lookup resolved whichever credential came first, so one of
	// the two modes had to be answered 500 at random.
	headerDocument := webhookDocument("Two credentials, header mode", map[string]any{
		"path": "header-two", "httpMethod": http.MethodPost, "responseMode": "immediate", "authentication": "headerAuth",
	})
	headerDocument.Nodes[0].Credentials = map[string]string{"jwtAuth": jwtCredential.ID, "httpHeaderAuth": header.ID}
	headerActive := h.activate(t, headerDocument)
	for attempt := range 5 {
		request := httptest.NewRequest(http.MethodPost, h.url(t, headerActive), strings.NewReader(`{}`))
		request.Header.Set("X-Api-Key", "k-1")
		recorder := httptest.NewRecorder()
		h.handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("header mode attempt %d: status = %d, want 200 (body: %s)", attempt, recorder.Code, recorder.Body)
		}
	}
	// A document that attaches a credential of another type and none for its
	// mode never reaches here: activation refuses it by name
	// (TestJWTWebhookNeedsACredentialBeforeActivation), and the mode-keyed
	// lookup keeps that same refusal at the boundary.
}

// TestWebhookKeepsInboundHeadersForTheRunAndRedactsThemOnRead pins the split
// Main ruled on (2026-09-20).
//
// The stored record is what the runner rehydrates as the trigger item, so
// redacting it on write would change what the tenant's own workflow sees — an
// imported n8n workflow's `$json.headers['x-api-key']` check has to read the
// caller's value. Inbound caller headers are the tenant's own data, and the read
// surfaces are where they are hidden: every record served by the executions API
// or the live event feed goes through execution.Redact.
func TestWebhookKeepsInboundHeadersForTheRunAndRedactsThemOnRead(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Redacting", map[string]any{
		"path": "redact", "httpMethod": http.MethodPost, "responseMode": "immediate",
	}))

	request := httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{"note":"body-value"}`))
	request.Header.Set("Authorization", "Bearer inbound-secret")
	request.Header.Set("Cookie", "sid=inbound-cookie")
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", recorder.Code, recorder.Body)
	}

	h.drain(t)
	record, err := h.runtime.Get(context.Background(), h.tenant, h.lastExecution(t))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	// What the run executes on: the caller's own headers, verbatim.
	for _, value := range []string{"inbound-secret", "inbound-cookie", "body-value"} {
		if !strings.Contains(string(record.Input), value) {
			t.Errorf("the stored trigger input lost %q, which the workflow runs on: %s", value, record.Input)
		}
	}

	// What a reader is served: the same record through the redaction the API
	// handlers and the live feed apply.
	served, err := json.Marshal(execution.Redact(record.Input))
	if err != nil {
		t.Fatalf("marshal the served input = %v", err)
	}
	for _, secret := range []string{"inbound-secret", "inbound-cookie"} {
		if strings.Contains(string(served), secret) {
			t.Errorf("the served input retained the header credential %q: %s", secret, served)
		}
	}
	if !strings.Contains(string(served), "body-value") {
		t.Errorf("the served input lost the request body: %s", served)
	}

	// And the node-run trace never carries a whole header value, whatever the
	// trigger copy holds.
	trace, _ := json.Marshal(record.NodeRuns)
	for _, secret := range []string{"inbound-secret", "inbound-cookie"} {
		if strings.Contains(string(trace), secret) {
			t.Errorf("the node-run trace retained the header credential %q: %s", secret, trace)
		}
	}
}

// TestWebhookDeliversSessionAndSchemePrefixedTextVerbatim is the correctness
// this ticket exists for.
//
// `session` and `sessionId` were on the sensitive-key list, so a WAHA envelope
// arrived with its session replaced and every action node sent the literal
// "[redacted]" to the API, while n8n-style AI memory collapsed every
// conversation into one bucket. A value-prefix match on "basic " destroyed any
// message that happened to start with the word.
func TestWebhookDeliversSessionAndSchemePrefixedTextVerbatim(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("WAHA-shaped", map[string]any{
		"path": "waha", "httpMethod": http.MethodPost, "responseMode": "immediate",
	}))

	const envelope = `{"session":"default","payload":{"sessionId":"6281234567890@c.us","body":"basic plan pricing?"}}`
	request := httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(envelope))
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", recorder.Code, recorder.Body)
	}

	h.drain(t)
	record, err := h.runtime.Get(context.Background(), h.tenant, h.lastExecution(t))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	// What the workflow actually ran on, not merely what was stored.
	var stored struct {
		Body struct {
			Session string `json:"session"`
			Payload struct {
				SessionID string `json:"sessionId"`
				Body      string `json:"body"`
			} `json:"payload"`
		} `json:"body"`
	}
	if err := json.Unmarshal(record.Input, &stored); err != nil {
		t.Fatalf("decoding stored input: %v (%s)", err, record.Input)
	}
	if stored.Body.Session != "default" {
		t.Errorf("session = %q, want %q — a redacted session is sent to the WAHA API verbatim", stored.Body.Session, "default")
	}
	if stored.Body.Payload.SessionID != "6281234567890@c.us" {
		t.Errorf("sessionId = %q, want the chat identity — redacting it collapses every conversation into one memory bucket", stored.Body.Payload.SessionID)
	}
	if stored.Body.Payload.Body != "basic plan pricing?" {
		t.Errorf("message = %q, want it verbatim — an ordinary sentence must not be destroyed for its first word", stored.Body.Payload.Body)
	}
}

func TestWebhookEnforcesTheBodyLimit(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Bounded", map[string]any{
		"path": "bounded", "httpMethod": http.MethodPost, "responseMode": "immediate",
	}))

	recorder := httptest.NewRecorder()
	oversized := strings.NewReader(`{"data":"` + strings.Repeat("a", 1000) + `"}`)
	h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, h.url(t, active), oversized))

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body: %s)", recorder.Code, recorder.Body)
	}
}

// Two workflows may carry the same path label.
//
// The label is a display name; the routable identity is the opaque route minted
// per node, so importing the same n8n template twice — or activating two
// workflows that both call their endpoint "shared" — is not a conflict. It used
// to be refused, with a 500 naming nothing.
func TestTwoWorkflowsMayShareOnePathLabel(t *testing.T) {
	h := newHarness(t)
	first := h.activate(t, webhookDocument("First", map[string]any{
		"path": "shared", "httpMethod": http.MethodPost, "responseMode": "immediate",
	}))
	second := h.activate(t, webhookDocument("Second", map[string]any{
		"path": "shared", "httpMethod": http.MethodPost, "responseMode": "immediate",
	}))

	for name, testCase := range map[string]struct {
		url  string
		want string
	}{
		"first":  {h.url(t, first), first.ID},
		"second": {h.url(t, second), second.ID},
	} {
		recorder := httptest.NewRecorder()
		h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, testCase.url, strings.NewReader(`{}`)))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: status = %d (body: %s)", name, recorder.Code, recorder.Body)
		}
		h.drain(t)
		record, err := h.runtime.Get(context.Background(), h.tenant, h.lastExecution(t))
		if err != nil {
			t.Fatalf("%s: Get() error = %v", name, err)
		}
		if record.WorkflowID != testCase.want {
			t.Errorf("%s: delivery ran workflow %s, want %s", name, record.WorkflowID, testCase.want)
		}
	}
}

// One path may answer several methods: n8n serves GET and POST on the same
// endpoint, and binding only one of them answered 404 to the other.
func TestOnePathMayAnswerSeveralMethods(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Paired", map[string]any{
		"path": "items", "multipleMethods": true, "httpMethods": []any{http.MethodGet, http.MethodPost},
		"responseMode": "immediate",
	}))

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		recorder := httptest.NewRecorder()
		h.handler.ServeHTTP(recorder, httptest.NewRequest(method, h.url(t, active), nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("%s status = %d (body: %s), want both bound methods answered", method, recorder.Code, recorder.Body)
		}
	}
}

func TestWebhookPayloadCarriesTheRequestShape(t *testing.T) {
	h := newHarness(t)
	// The Set node reads a header inside the workflow, which is the half that
	// has to see the caller's real value under n8n's lower-case name: asserting
	// on the stored record alone would prove nothing, because the repository
	// redacts credential-shaped header names on the way to disk.
	active := h.activate(t, webhookDocument("Shape", map[string]any{
		"path": "shape", "httpMethod": http.MethodPost, "responseMode": "immediate",
	}, workflow.Node{
		ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: workflow.V(1),
		Parameters: map[string]any{"assignments": map[string]any{
			"seen": map[string]any{"mode": "expression", "value": "{{ $json.headers['x-api-key'] }}"},
		}},
	}))

	request := httptest.NewRequest(http.MethodPost, h.url(t, active)+"?tier=gold&tag=a&tag=b", strings.NewReader(`{"id":9}`))
	request.Header.Set("X-Api-Key", "abc")
	request.Header.Set("X-Tenant", "acme")
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)

	h.drain(t)
	record, err := h.runtime.Get(context.Background(), h.tenant, h.lastExecution(t))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	var input struct {
		Body          map[string]any `json:"body"`
		Headers       map[string]any `json:"headers"`
		Params        map[string]any `json:"params"`
		Query         map[string]any `json:"query"`
		WebhookURL    string         `json:"webhookUrl"`
		ExecutionMode string         `json:"executionMode"`
	}
	if err := json.Unmarshal(record.Input, &input); err != nil {
		t.Fatalf("decode trigger input = %v", err)
	}
	// n8n's item shape, key for key. The item carries the parsed body, the
	// request headers under their lower-case names, the path parameters, the
	// query, the address it arrived at and the mode it ran in.
	if input.Body["id"] != float64(9) {
		t.Errorf("body = %#v, want the parsed request body", input.Body)
	}
	if _, present := input.Headers["x-api-key"]; !present {
		t.Errorf("headers = %#v, want the caller's header under its lower-case name", input.Headers)
	}
	if input.Headers["host"] == nil {
		t.Errorf("headers = %#v, want host", input.Headers)
	}
	if input.Query["tier"] != "gold" {
		t.Errorf("query = %#v, want the query string", input.Query)
	}
	tags, ok := input.Query["tag"].([]any)
	if !ok || len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Errorf("query tag = %#v, want a list for a repeated key", input.Query["tag"])
	}
	if _, present := input.Params["id"]; present {
		t.Errorf("params = %#v, want no parameters on a route with none", input.Params)
	}
	if !strings.HasSuffix(input.WebhookURL, h.url(t, active)) {
		t.Errorf("webhookUrl = %q, want the address the request arrived at", input.WebhookURL)
	}
	if input.ExecutionMode != "production" {
		t.Errorf("executionMode = %q, want production", input.ExecutionMode)
	}

	// What the workflow saw. A header check written the way an n8n workflow
	// writes it — `$json.headers['x-api-key']` — resolves to the caller's real
	// value, which it could not before: the name arrived as `X-Api-Key` so the
	// lookup was undefined, and the value was "[redacted]" before the item was
	// ever built. Main's ruling of 2026-09-20 is that the stored record is the
	// runtime input, so inbound headers are stored verbatim and every read
	// surface (the executions API, the event feed) is where they are hidden.
	encoded, _ := json.Marshal(record)
	if !strings.Contains(string(encoded), `"seen":"abc"`) {
		t.Errorf("the workflow did not read the caller's header value: %s", encoded)
	}
}

// A trigger the author switched off must not open an endpoint. The runner
// refuses to start a disabled trigger, so a binding for one would answer and
// then never run anything.
func TestADisabledTriggerIsNotRegistered(t *testing.T) {
	h := newHarness(t)
	document := webhookDocument("Disabled", map[string]any{
		"path": "switched-off", "httpMethod": http.MethodPost, "responseMode": "immediate",
	})
	document.Nodes[0].Disabled = true
	stored, err := h.workflows.SaveDraft(context.Background(), h.tenant, document)
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	active, err := h.workflows.Activate(context.Background(), h.tenant, stored.ID, h.registry)
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	routes, err := h.workflows.WebhookRoutes(context.Background(), h.tenant, active.ID)
	if err != nil {
		t.Fatalf("WebhookRoutes() error = %v", err)
	}
	if len(routes) != 0 {
		t.Fatalf("routes = %#v, want none for a disabled trigger", routes)
	}
}

// A route pattern's parameters are filled from the segments after the route,
// which is how n8n's `/user/:id` endpoints work and what used to be impossible:
// the request answered 404 and `$json.params` was always empty.
func TestWebhookFillsPathParametersFromTheRoutePattern(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("REST", map[string]any{
		"path": "user/:id", "httpMethod": http.MethodGet, "responseMode": "immediate",
	}))

	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, h.url(t, active)+"/42", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s), want the parameterised route to match", recorder.Code, recorder.Body)
	}
	h.drain(t)
	record, err := h.runtime.Get(context.Background(), h.tenant, h.lastExecution(t))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	var input struct {
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(record.Input, &input); err != nil {
		t.Fatalf("decode trigger input = %v", err)
	}
	if input.Params["id"] != "42" {
		t.Fatalf("params = %#v, want the path parameter", input.Params)
	}
}

// A cross-origin caller is answered with CORS headers, and its preflight is
// answered at all — a browser page could not post to a KilasFlow webhook
// before this, because OPTIONS matched no binding and 404ed.
func TestWebhookAnswersCORSPreflightAndEchoesTheOrigin(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Browser", map[string]any{
		"path": "browser", "httpMethod": http.MethodPost, "responseMode": "immediate",
	}))

	preflight := httptest.NewRequest(http.MethodOptions, h.url(t, active), nil)
	preflight.Header.Set("Origin", "https://app.example.test")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Access-Control-Request-Headers", "content-type")
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, preflight)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204 (body: %s)", recorder.Code, recorder.Body)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.test" {
		t.Errorf("Allow-Origin = %q, want the caller's origin", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodPost) {
		t.Errorf("Allow-Methods = %q, want the bound method", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Headers"); got != "content-type" {
		t.Errorf("Allow-Headers = %q, want the requested headers", got)
	}
	if recorder.Header().Get("Access-Control-Max-Age") == "" {
		t.Error("a preflight was answered without a Max-Age")
	}

	request := httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`))
	request.Header.Set("Origin", "https://app.example.test")
	answered := httptest.NewRecorder()
	h.handler.ServeHTTP(answered, request)
	if got := answered.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.test" {
		t.Errorf("delivery Allow-Origin = %q, want the caller's origin echoed", got)
	}
}

// An origin the trigger does not allow gets no CORS header, so the browser
// refuses the response rather than the workflow silently accepting it.
func TestWebhookRefusesAnOriginOutsideTheAllowList(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Locked", map[string]any{
		"path": "locked", "httpMethod": http.MethodPost, "responseMode": "immediate",
		"options": map[string]any{"allowedOrigins": "https://app.example.test"},
	}))

	request := httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`))
	request.Header.Set("Origin", "https://elsewhere.example.test")
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)

	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want none for an origin outside the allow-list", got)
	}
}

// An endpoint restricted by an IP allow-list used to arrive open after an
// import, which silently published a protected endpoint.
func TestWebhookIPAllowListRefusesOtherCallers(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Guarded", map[string]any{
		"path": "guarded", "httpMethod": http.MethodPost, "responseMode": "immediate",
		"options": map[string]any{"ipWhitelist": "10.0.0.1, 203.0.113.0/24"},
	}))

	refused := httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`))
	refused.RemoteAddr = "198.51.100.7:1234"
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, refused)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d (body: %s), want 403 for an address outside the list", recorder.Code, recorder.Body)
	}
	if h.queuedCount() != 0 {
		t.Error("a refused caller still queued an execution")
	}

	allowed := httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`))
	allowed.RemoteAddr = "203.0.113.9:1234"
	accepted := httptest.NewRecorder()
	h.handler.ServeHTTP(accepted, allowed)
	if accepted.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s), want an address inside the range accepted", accepted.Code, accepted.Body)
	}
}

// An immediate acknowledgement carries the configured code, body and headers,
// and defaults to n8n's own message.
func TestWebhookImmediateAcknowledgementCarriesTheConfiguredAnswer(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Ack", map[string]any{
		"path": "ack", "httpMethod": http.MethodPost, "responseMode": "immediate",
		"options": map[string]any{
			"responseCode":    float64(201),
			"responseData":    "thanks!",
			"responseHeaders": map[string]any{"X-Ack": "1"},
			"allowedOrigins":  "*",
		},
	}))

	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`)))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want the configured 201 (body: %s)", recorder.Code, recorder.Body)
	}
	if got := recorder.Body.String(); got != "thanks!" {
		t.Errorf("body = %q, want the configured acknowledgement", got)
	}
	if got := recorder.Header().Get("X-Ack"); got != "1" {
		t.Errorf("X-Ack = %q, want the configured header", got)
	}
}

// TestBranchedWebhookAnswersFromTheBranchThatRan is the second defect on this
// path.
//
// With a Respond to Webhook node on each arm of an IF, both used to run, both
// wrote a response, and which one answered the caller was decided by Go's
// randomised map iteration order. Pruning removes the ambiguity for this case —
// only the taken arm runs now — and the lookup walks execution order rather
// than a map, so a graph that legitimately produces two responses still answers
// with a defined one.
func TestBranchedWebhookAnswersFromTheBranchThatRan(t *testing.T) {
	h := newHarness(t)

	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		Name:          "Branched response",
		Nodes: []workflow.Node{
			{ID: "trigger", Name: "Webhook", Type: nodes.WebhookNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"path": "branched", "httpMethod": http.MethodPost, "responseMode": "responseNode"}},
			{ID: "if", Name: "IF", Type: "kilasflow.if", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"conditions": []any{map[string]any{
					"field": "body.tier", "operator": "equals", "value": "vip",
				}}}},
			{ID: "vip", Name: "VIP reply", Type: "kilasflow.respondToWebhook", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"respondWith": "text", "responseCode": float64(200), "responseBody": "vip-branch"}},
			{ID: "standard", Name: "Standard reply", Type: "kilasflow.respondToWebhook", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"respondWith": "text", "responseCode": float64(202), "responseBody": "standard-branch"}},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "trigger", Port: "main"},
				Target: workflow.Endpoint{NodeID: "if", Port: "main"}},
			{ID: "c2", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "if", Port: "true"},
				Target: workflow.Endpoint{NodeID: "vip", Port: "main"}},
			{ID: "c3", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "if", Port: "false"},
				Target: workflow.Endpoint{NodeID: "standard", Port: "main"}},
		},
		Settings: map[string]any{},
	}
	active := h.activate(t, document)

	// The handler waits for the run, so a worker has to be draining alongside.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if worked, _ := h.runtime.RunOnce(context.Background()); !worked {
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()
	defer func() { close(stop); <-done }()

	// Repeated executions of the same input must return the same response;
	// under map iteration this was a coin flip on every request.
	for attempt := range 6 {
		recorder := httptest.NewRecorder()
		h.handler.ServeHTTP(recorder, httptest.NewRequest(
			http.MethodPost, h.url(t, active), strings.NewReader(`{"tier":"vip"}`)))
		if recorder.Code != http.StatusOK {
			t.Fatalf("attempt %d: status = %d, want the VIP branch's 200 (body: %s)", attempt, recorder.Code, recorder.Body)
		}
		if body := recorder.Body.String(); body != "vip-branch" {
			t.Fatalf("attempt %d: body = %q, want the branch that ran", attempt, body)
		}
	}

	// And the other way, so the answer follows the data rather than the order.
	for attempt := range 6 {
		recorder := httptest.NewRecorder()
		h.handler.ServeHTTP(recorder, httptest.NewRequest(
			http.MethodPost, h.url(t, active), strings.NewReader(`{"tier":"standard"}`)))
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("attempt %d: status = %d, want the standard branch's 202 (body: %s)", attempt, recorder.Code, recorder.Body)
		}
		if body := recorder.Body.String(); body != "standard-branch" {
			t.Fatalf("attempt %d: body = %q, want the standard branch", attempt, body)
		}
	}
}

// TestTwoTenantsCanActivateTheSameTemplatePath is the business goal. An n8n
// template ships a hardcoded webhook path, so importing the official WAHA
// chatting template for a second client produced a second workflow with the
// same path — and one unique index made activation impossible. Replicating a
// client automation across customers is the reason V2 exists.
func TestTwoTenantsCanActivateTheSameTemplatePath(t *testing.T) {
	h := newHarness(t)
	other := repository.TenantScope{ID: "tenant-b"}

	document := webhookDocument("Imported template", map[string]any{
		"path": "waha-webhook", "httpMethod": http.MethodPost, "responseMode": "immediate", "responseCode": float64(202),
	})

	first := h.activate(t, document)

	// The same document, the same path, a different tenant.
	secondDraft, err := h.workflows.SaveDraft(context.Background(), other, document)
	if err != nil {
		t.Fatalf("SaveDraft(tenant-b) error = %v", err)
	}
	second, err := h.workflows.Activate(context.Background(), other, secondDraft.ID, h.registry)
	if err != nil {
		t.Fatalf("the same template must activate for a second tenant: %v", err)
	}

	firstRoutes, err := h.workflows.WebhookRoutes(context.Background(), h.tenant, first.ID)
	if err != nil {
		t.Fatalf("WebhookRoutes(first) error = %v", err)
	}
	secondRoutes, err := h.workflows.WebhookRoutes(context.Background(), other, second.ID)
	if err != nil {
		t.Fatalf("WebhookRoutes(second) error = %v", err)
	}
	if firstRoutes[0].Route == secondRoutes[0].Route {
		t.Fatal("both tenants were given the same route; deliveries would be ambiguous")
	}
	// Both keep the author's label, which is what the editor shows.
	for _, routes := range [][]repository.WebhookBinding{firstRoutes, secondRoutes} {
		if routes[0].Path != "waha-webhook" {
			t.Errorf("path label = %q, want the template's own path", routes[0].Path)
		}
	}

	// And each receives only its own deliveries.
	for name, testCase := range map[string]struct {
		url    string
		tenant repository.TenantScope
		want   string
	}{
		"first tenant":  {"/webhook/" + firstRoutes[0].Route, h.tenant, first.ID},
		"second tenant": {"/webhook/" + secondRoutes[0].Route, other, second.ID},
	} {
		recorder := httptest.NewRecorder()
		h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, testCase.url, strings.NewReader(`{}`)))
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("%s: status = %d (body: %s)", name, recorder.Code, recorder.Body)
		}
		record, err := h.runtime.Get(context.Background(), testCase.tenant, h.lastExecution(t))
		if err != nil {
			t.Fatalf("%s: Get() error = %v", name, err)
		}
		if record.WorkflowID != testCase.want {
			t.Errorf("%s: delivery ran workflow %s, want %s", name, record.WorkflowID, testCase.want)
		}
	}
}

// TestReactivationKeepsTheSameRoute is what makes an opaque route usable. A
// route minted per activation would change the public URL every time a workflow
// was deactivated and reactivated, breaking every sender already configured
// against it.
func TestReactivationKeepsTheSameRoute(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Stable", map[string]any{
		"path": "stable", "httpMethod": http.MethodPost, "responseMode": "immediate",
	}))
	before := h.url(t, active)

	if _, err := h.workflows.Deactivate(context.Background(), h.tenant, active.ID); err != nil {
		t.Fatalf("Deactivate() error = %v", err)
	}
	if _, err := h.workflows.Activate(context.Background(), h.tenant, active.ID, h.registry); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if after := h.url(t, active); after != before {
		t.Errorf("route changed across reactivation: %q then %q", before, after)
	}
}

// TestRepeatedDeliveryRunsTheWorkflowOnce is the second half of this ticket.
//
// WAHA retries a failed delivery fifteen times at two-second intervals and
// identifies each logical delivery with a header. KilasFlow queued an execution
// per HTTP request, so a slow workflow that eventually succeeded could send
// fifteen WhatsApp replies.
func TestRepeatedDeliveryRunsTheWorkflowOnce(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Deduped", map[string]any{
		"path": "waha", "httpMethod": http.MethodPost, "responseMode": "immediate",
		"responseCode": float64(202), "deliveryIdHeader": "X-Webhook-Request-Id",
	}))
	url := h.url(t, active)

	executions := map[string]bool{}
	for attempt := range 5 {
		request := httptest.NewRequest(http.MethodPost, url, strings.NewReader(`{"event":"message"}`))
		request.Header.Set("X-Webhook-Request-Id", "delivery-1")
		recorder := httptest.NewRecorder()
		h.handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusAccepted && recorder.Code != http.StatusOK {
			t.Fatalf("attempt %d: status = %d (body: %s)", attempt, recorder.Code, recorder.Body)
		}
		var body struct {
			ExecutionID string `json:"executionId"`
			Duplicate   bool   `json:"duplicate"`
		}
		_ = json.Unmarshal(recorder.Body.Bytes(), &body)
		if attempt > 0 && !body.Duplicate {
			t.Errorf("attempt %d was not reported as a duplicate", attempt)
		}
		if body.ExecutionID != "" {
			// A duplicate answer is KilasFlow's own, and it names the original
			// execution so a retrying sender can follow it.
			executions[body.ExecutionID] = true
			continue
		}
		if attempt == 0 {
			executions[h.lastExecution(t)] = true
		}
	}

	if len(executions) != 1 {
		t.Errorf("five retries produced %d executions, want 1: %v", len(executions), executions)
	}
}

// TestDistinctDeliveriesAreNeverCollapsed keeps the dedupe from going too far.
// Two genuinely different deliveries must each run.
func TestDistinctDeliveriesAreNeverCollapsed(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Distinct", map[string]any{
		"path": "waha", "httpMethod": http.MethodPost, "responseMode": "immediate",
		"responseCode": float64(202), "deliveryIdHeader": "X-Webhook-Request-Id",
	}))
	url := h.url(t, active)

	executions := map[string]bool{}
	for _, id := range []string{"delivery-1", "delivery-2", "delivery-3"} {
		request := httptest.NewRequest(http.MethodPost, url, strings.NewReader(`{"event":"message"}`))
		request.Header.Set("X-Webhook-Request-Id", id)
		recorder := httptest.NewRecorder()
		h.handler.ServeHTTP(recorder, request)
		executions[h.lastExecution(t)] = true
	}
	if len(executions) != 3 {
		t.Errorf("three distinct deliveries produced %d executions, want 3", len(executions))
	}
}

// TestADeliveryWithNoIdentifierIsNeverDeduped covers the sender that does not
// send the header, and the identical message a user genuinely sends twice.
func TestADeliveryWithNoIdentifierIsNeverDeduped(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("No identifier", map[string]any{
		"path": "waha", "httpMethod": http.MethodPost, "responseMode": "immediate",
		"responseCode": float64(202), "deliveryIdHeader": "X-Webhook-Request-Id",
	}))
	url := h.url(t, active)

	executions := map[string]bool{}
	for range 3 {
		// Byte-identical requests, no identifier header. Deduping on a body
		// hash would collapse these, and two identical messages sent twice by
		// a user are not a duplicate delivery.
		recorder := httptest.NewRecorder()
		h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, url, strings.NewReader(`{"text":"hi"}`)))
		executions[h.lastExecution(t)] = true
	}
	if len(executions) != 3 {
		t.Errorf("three unidentified deliveries produced %d executions, want 3", len(executions))
	}
}

// TestRawBytesNeverReachAStoredRecord keeps the capture from becoming a leak.
//
// The exact bytes are carried so a signature can be verified, and verification
// is the only thing that needs them. Putting them on the item would double
// every payload in the executions table, make redaction's job harder, and
// expose the body twice in the API.
func TestRawBytesNeverReachAStoredRecord(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Raw", map[string]any{
		"path": "raw", "httpMethod": http.MethodPost, "responseMode": "immediate", "responseCode": float64(202),
	}))

	// Whitespace that only survives in the raw bytes: a decoded-and-remarshalled
	// body loses it, so finding it in the record would prove the raw bytes were
	// stored.
	const body = `{"marker":"raw-only",   "spaced":true}`
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(body)))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d (body: %s)", recorder.Code, recorder.Body)
	}

	h.drain(t)
	record, err := h.runtime.Get(context.Background(), h.tenant, h.lastExecution(t))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	encoded, _ := json.Marshal(record)

	// The value is there, because the workflow runs on it.
	if !strings.Contains(string(encoded), "raw-only") {
		t.Errorf("the record lost the body the workflow runs on: %s", encoded)
	}
	// The bytes are not, because nothing put them there.
	if strings.Contains(string(encoded), `"marker":"raw-only",   "spaced"`) {
		t.Errorf("the raw request bytes were stored verbatim: %s", encoded)
	}
	if strings.Contains(string(encoded), "rawBody") {
		t.Errorf("the record carries a rawBody field: %s", encoded)
	}
}

// drainWhile runs the worker for as long as the request is in flight, since the
// lastNode and responseNode modes both wait for the execution to finish.
func drainWhile(t *testing.T, h harness, request func()) {
	t.Helper()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case <-stop:
				return
			default:
			}
			worked, _ := h.runtime.RunOnce(context.Background())
			if !worked {
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()
	request()
	close(stop)
	<-done
}

// A Respond node holding n8n's default respondWith (which an export omits)
// answers with the first incoming item, not an empty text response.
func TestRespondToWebhookDefaultsToTheFirstIncomingItem(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Default responder", map[string]any{
		"path": "default-respond", "httpMethod": http.MethodPost, "responseMode": "responseNode",
	}, workflow.Node{
		ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: workflow.V(1),
		Parameters: map[string]any{"assignments": map[string]any{"status": "ready"}},
	}, workflow.Node{
		ID: "respond", Name: "Respond to Webhook", Type: nodes.RespondNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{},
	}))

	recorder := httptest.NewRecorder()
	drainWhile(t, h, func() {
		h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`)))
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", recorder.Code, recorder.Body)
	}
	item := decodeBody[map[string]any](t, recorder)
	if item["status"] != "ready" {
		t.Fatalf("body = %s, want the first incoming item", recorder.Body)
	}
}

// The response must not travel on the items: n8n passes the Respond node's
// input through unchanged, and a `$response` field leaked into every downstream
// node's data and into the stored execution output.
func TestRespondToWebhookDoesNotLeakTheResponseIntoDownstreamItems(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Passthrough", map[string]any{
		"path": "passthrough", "httpMethod": http.MethodPost, "responseMode": "responseNode",
	}, workflow.Node{
		ID: "respond", Name: "Respond to Webhook", Type: nodes.RespondNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"respondWith": "text", "responseBody": "accepted"},
	}, workflow.Node{
		ID: "after", Name: "After", Type: "kilasflow.noOp", TypeVersion: workflow.V(1),
		Parameters: map[string]any{},
	}))

	recorder := httptest.NewRecorder()
	drainWhile(t, h, func() {
		h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{"id":3}`)))
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", recorder.Code, recorder.Body)
	}
	if got := recorder.Body.String(); got != "accepted" {
		t.Fatalf("body = %q, want the Respond node's text", got)
	}

	record, err := h.runtime.Get(context.Background(), h.tenant, h.lastExecution(t))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	encoded, _ := json.Marshal(record)
	if strings.Contains(string(encoded), "$response") {
		t.Fatalf("the stored execution carries $response as item data: %s", encoded)
	}
}

// formDocument is a workflow that starts from a hosted form.
func formDocument(parameters map[string]any) workflow.Document {
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		Name:          "Hosted form",
		Nodes: []workflow.Node{{
			ID: "trigger", Name: "Form", Type: nodes.FormTriggerNodeType, TypeVersion: workflow.V(1),
			Parameters: parameters,
		}},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	}
}

// The form trigger is a GET that renders the page and a POST that submits it,
// which is why both methods are bound on one route.
func TestFormTriggerServesItsPageAndStartsTheRunOnSubmit(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, formDocument(map[string]any{
		"path": "signup", "formTitle": "Sign up", "formDescription": "Tell us who you are.",
		"formFields": map[string]any{"values": []any{
			map[string]any{"fieldLabel": "Name", "fieldType": "text", "requiredField": true},
			map[string]any{"fieldLabel": "Plan", "fieldType": "dropdown",
				"fieldOptions": map[string]any{"values": []any{"free", "pro"}}},
		}},
		"responseMode": "immediate",
	}))
	url := h.url(t, active)

	// GET renders the page and runs nothing: a form is filled in before it is
	// submitted.
	page := httptest.NewRecorder()
	h.handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, url, nil))
	if page.Code != http.StatusOK {
		t.Fatalf("GET status = %d (body: %s), want the page", page.Code, page.Body)
	}
	if contentType := page.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Errorf("Content-Type = %q, want the page rendered as HTML", contentType)
	}
	for _, expected := range []string{"Sign up", "Tell us who you are.", `name="Name"`, `<option value="pro">pro</option>`} {
		if !strings.Contains(page.Body.String(), expected) {
			t.Errorf("page is missing %q:\n%s", expected, page.Body)
		}
	}
	if h.queuedCount() != 0 {
		t.Error("rendering the page queued an execution")
	}

	// A submission that omits a required field is refused and runs nothing.
	refused := httptest.NewRequest(http.MethodPost, url, strings.NewReader("Plan=pro"))
	refused.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	refusal := httptest.NewRecorder()
	h.handler.ServeHTTP(refusal, refused)
	if refusal.Code != http.StatusBadRequest {
		t.Fatalf("status = %d (body: %s), want a refusal", refusal.Code, refusal.Body)
	}
	if !strings.Contains(refusal.Body.String(), "Name") {
		t.Errorf("refusal = %s, want it to name the missing field", refusal.Body)
	}
	if h.queuedCount() != 0 {
		t.Error("a refused submission still queued an execution")
	}

	// A complete submission runs the workflow, and the item is the submitted
	// fields plus the two keys n8n's own form trigger adds.
	submission := httptest.NewRequest(http.MethodPost, url, strings.NewReader("Name=Ada&Plan=pro"))
	submission.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	accepted := httptest.NewRecorder()
	h.handler.ServeHTTP(accepted, submission)
	if accepted.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s), want the acknowledgement page", accepted.Code, accepted.Body)
	}
	if contentType := accepted.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Errorf("Content-Type = %q, want a page rather than a JSON body", contentType)
	}

	h.drain(t)
	record, err := h.runtime.Get(context.Background(), h.tenant, h.lastExecution(t))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	var item map[string]any
	if err := json.Unmarshal(record.Input, &item); err != nil {
		t.Fatalf("decode trigger input = %v", err)
	}
	if item["Name"] != "Ada" || item["Plan"] != "pro" {
		t.Errorf("item = %#v, want the submitted fields at the top level", item)
	}
	if item["formMode"] != "production" {
		t.Errorf("formMode = %#v, want production", item["formMode"])
	}
	if _, present := item["submittedAt"].(string); !present {
		t.Errorf("item = %#v, want a submittedAt stamp", item)
	}
}

// decodeBody reads a response body as JSON, whichever shape it is.
func decodeBody[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()
	var decoded T
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode body %s: %v", recorder.Body, err)
	}
	return decoded
}

func TestWebhookLastNodeReturnsTheLastNodesItemsAsN8NDoes(t *testing.T) {
	// This used to write KilasFlow's own envelope — {executionId, status, data}
	// keyed by node ID — so a workflow imported with responseMode "lastNode",
	// which is a common shape, activated, ran, returned 200 and handed the
	// caller the wrong body with nothing anywhere reporting it.
	for name, testCase := range map[string]struct {
		responseData string
		wantStatus   int
		// wantArray is the shape, which is the part n8n's own option
		// descriptions promise: allEntries is "always an array" and the
		// default is "always a JSON object".
		wantArray bool
		wantEmpty bool
	}{
		"the default is the first entry as an object": {wantStatus: http.StatusOK},
		"all entries is always an array":              {responseData: "allEntries", wantStatus: http.StatusOK, wantArray: true},
		"no data is an empty 200":                     {responseData: "noData", wantStatus: http.StatusOK, wantEmpty: true},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			parameters := map[string]any{
				"path": "last-" + strings.ReplaceAll(name, " ", "-"), "httpMethod": http.MethodPost,
				"responseMode": "lastNode",
			}
			if testCase.responseData != "" {
				parameters["responseData"] = testCase.responseData
			}
			active := h.activate(t, webhookDocument("Last node", parameters, workflow.Node{
				ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"status": "ready"}},
			}))

			recorder := httptest.NewRecorder()
			drainWhile(t, h, func() {
				h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`)))
			})

			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", recorder.Code, testCase.wantStatus, recorder.Body)
			}
			// Whatever the shape, it is the node's own data and not an
			// envelope: no executionId, no status, no data key.
			if strings.Contains(recorder.Body.String(), "executionId") {
				t.Fatalf("body = %s, want the last node's items rather than an envelope", recorder.Body)
			}
			switch {
			case testCase.wantEmpty:
				if got := strings.TrimSpace(recorder.Body.String()); got != "" {
					t.Errorf("body = %q, want nothing", got)
				}
			case testCase.wantArray:
				items := decodeBody[[]map[string]any](t, recorder)
				if len(items) != 1 || items[0]["status"] != "ready" {
					t.Errorf("body = %s, want an array holding the Set node's item", recorder.Body)
				}
			default:
				item := decodeBody[map[string]any](t, recorder)
				if item["status"] != "ready" {
					t.Errorf("body = %s, want the Set node's item as an object", recorder.Body)
				}
			}
		})
	}
}

func TestRespondToWebhookCoversN8NsRespondWithSet(t *testing.T) {
	for name, testCase := range map[string]struct {
		parameters map[string]any
		wantStatus int
		wantArray  bool
		wantObject bool
		wantBody   string
		wantHeader [2]string
	}{
		"all incoming items is the array": {
			parameters: map[string]any{"respondWith": "allIncomingItems"},
			wantStatus: http.StatusOK, wantArray: true,
		},
		"first incoming item is the object": {
			parameters: map[string]any{"respondWith": "firstIncomingItem"},
			wantStatus: http.StatusOK, wantObject: true,
		},
		"no data answers with nothing": {
			parameters: map[string]any{"respondWith": "noData", "responseCode": float64(204)},
			wantStatus: http.StatusNoContent, wantBody: "",
		},
		// A redirect with a 200 is not a redirect, so the code defaults to 307 —
		// n8n's own default, which keeps a POST a POST where a 302 turns it into
		// a GET in every browser.
		"redirect defaults to 307 and sets Location": {
			parameters: map[string]any{"respondWith": "redirect", "redirectURL": "https://example.test/thanks"},
			wantStatus: http.StatusTemporaryRedirect, wantBody: "",
			wantHeader: [2]string{"Location", "https://example.test/thanks"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			respond := workflow.Node{
				ID: "respond", Name: "Respond to Webhook", Type: nodes.RespondNodeType, TypeVersion: workflow.V(1),
				Parameters: testCase.parameters,
			}
			active := h.activate(t, webhookDocument("Responder", map[string]any{
				"path": "respond-" + strings.ReplaceAll(name, " ", "-"), "httpMethod": http.MethodPost,
				"responseMode": "responseNode",
			}, workflow.Node{
				ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"status": "ready"}},
			}, respond))

			recorder := httptest.NewRecorder()
			drainWhile(t, h, func() {
				h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`)))
			})

			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", recorder.Code, testCase.wantStatus, recorder.Body)
			}
			switch {
			case testCase.wantArray:
				items := decodeBody[[]map[string]any](t, recorder)
				if len(items) != 1 || items[0]["status"] != "ready" {
					t.Errorf("body = %s, want an array holding the incoming item", recorder.Body)
				}
			case testCase.wantObject:
				item := decodeBody[map[string]any](t, recorder)
				if item["status"] != "ready" {
					t.Errorf("body = %s, want the incoming item as an object", recorder.Body)
				}
			default:
				if got := strings.TrimSpace(recorder.Body.String()); got != testCase.wantBody {
					t.Errorf("body = %q, want %q", got, testCase.wantBody)
				}
			}
			if testCase.wantHeader[0] != "" {
				if got := recorder.Header().Get(testCase.wantHeader[0]); got != testCase.wantHeader[1] {
					t.Errorf("%s = %q, want %q", testCase.wantHeader[0], got, testCase.wantHeader[1])
				}
			}
		})
	}
}
