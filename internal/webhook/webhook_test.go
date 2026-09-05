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

	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
	"github.com/kilaslabs/kilas-flow/internal/webhook"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

type harness struct {
	handler     http.Handler
	workflows   *repository.GORMWorkflowStore
	credentials *repository.GORMCredentialStore
	runtime     *engine.Service
	registry    *node.Registry
	tenant      repository.TenantScope
}

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
	if err := database.Migrate(db, repository.Models()...); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	executors := engine.NewRegistry()
	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	if err := nodes.RegisterExecutors(executors, policy, sqlnode.Guard{}); err != nil {
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
		WithWebhooks(webhook.Extract(nodes.WebhookNodeType, nodes.WebhookPath))
	credentialStore := repository.NewCredentialStore(db.DB, cipher)
	executions := repository.NewExecutionStore(db.DB)
	runtime, err := engine.NewService(engine.ServiceDeps{
		Executions: executions, Catalog: registry, Runner: engine.NewRunner(executors),
		Credentials: credentialStore, WorkerID: "webhook-test", DefaultTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	return harness{
		handler:     webhook.NewHandler(workflows, runtime, credentialStore, nil, webhook.Limits{MaxBodyBytes: 512, ResponseTimeout: 2 * time.Second}),
		workflows:   workflows,
		credentials: credentialStore,
		runtime:     runtime,
		registry:    registry,
		tenant:      repository.TenantScope{ID: repository.DefaultTenantID},
	}
}

func webhookDocument(name string, triggerParameters map[string]any, extra ...workflow.Node) workflow.Document {
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		Name:          name,
		Nodes: append([]workflow.Node{{
			ID: "trigger", Name: "Webhook", Type: nodes.WebhookNodeType, TypeVersion: 1,
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

func TestWebhookAnswersImmediatelyAndQueuesANormalExecution(t *testing.T) {
	h := newHarness(t)
	h.activate(t, webhookDocument("Immediate", map[string]any{
		"path": "orders", "httpMethod": http.MethodPost, "responseMode": "immediate", "responseCode": float64(202),
	}))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/webhook/orders", strings.NewReader(`{"id":7}`))
	h.handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want the configured 202 (body: %s)", recorder.Code, recorder.Body)
	}
	var body struct {
		ExecutionID string `json:"executionId"`
		Status      string `json:"status"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response = %v", err)
	}
	if body.ExecutionID == "" || body.Status != "queued" {
		t.Fatalf("response = %#v, want a queued execution record", body)
	}

	// The webhook must go through the same durable queue as a manual run.
	h.drain(t)
	record, err := h.runtime.Get(context.Background(), h.tenant, body.ExecutionID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if record.Status != "succeeded" || record.Trigger != "webhook" {
		t.Fatalf("execution = %s/%s, want a succeeded webhook run", record.Status, record.Trigger)
	}
}

func TestWebhookRespondsFromARespondToWebhookNode(t *testing.T) {
	h := newHarness(t)
	h.activate(t, webhookDocument("Responder", map[string]any{
		"path": "reply", "httpMethod": http.MethodPost, "responseMode": "responseNode",
	}, workflow.Node{
		ID: "respond", Name: "Respond to Webhook", Type: nodes.RespondNodeType, TypeVersion: 1,
		Parameters: map[string]any{
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
	h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/webhook/reply", strings.NewReader(`{}`)))
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

func TestWebhookReportsWhenNoResponseNodeWasReached(t *testing.T) {
	h := newHarness(t)
	// Configured to answer from a node, but the graph has none.
	h.activate(t, webhookDocument("No responder", map[string]any{
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
	h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/webhook/silent", strings.NewReader(`{}`)))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body: %s)", recorder.Code, recorder.Body)
	}
	if !strings.Contains(recorder.Body.String(), "Respond to Webhook") {
		t.Errorf("body = %s, want it to name the missing node", recorder.Body)
	}
}

func TestWebhookTimesOutRatherThanHangingForever(t *testing.T) {
	h := newHarness(t)
	h.activate(t, webhookDocument("Slow", map[string]any{
		"path": "slow", "httpMethod": http.MethodPost, "responseMode": "lastNode",
	}))

	// No worker drains the queue, so the run never finishes.
	recorder := httptest.NewRecorder()
	start := time.Now()
	h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/webhook/slow", strings.NewReader(`{}`)))

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

	for name, request := range map[string]*http.Request{
		"unknown path": httptest.NewRequest(http.MethodPost, "/webhook/nothing-here", nil),
		"wrong method": httptest.NewRequest(http.MethodGet, "/webhook/bound", nil),
		"empty path":   httptest.NewRequest(http.MethodPost, "/webhook/", nil),
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
	h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/webhook/bound", nil))
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
	h.activate(t, document)

	anonymous := httptest.NewRecorder()
	h.handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodPost, "/webhook/secure", strings.NewReader(`{}`)))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401 (body: %s)", anonymous.Code, anonymous.Body)
	}
	if anonymous.Header().Get("WWW-Authenticate") == "" {
		t.Error("a 401 was returned without a WWW-Authenticate challenge")
	}

	wrong := httptest.NewRecorder()
	badRequest := httptest.NewRequest(http.MethodPost, "/webhook/secure", strings.NewReader(`{}`))
	badRequest.SetBasicAuth("ada", "wrong")
	h.handler.ServeHTTP(wrong, badRequest)
	if wrong.Code != http.StatusUnauthorized {
		t.Errorf("wrong password status = %d, want 401", wrong.Code)
	}

	authorized := httptest.NewRecorder()
	goodRequest := httptest.NewRequest(http.MethodPost, "/webhook/secure", strings.NewReader(`{}`))
	goodRequest.SetBasicAuth("ada", "hunter2")
	h.handler.ServeHTTP(authorized, goodRequest)
	if authorized.Code != http.StatusOK {
		t.Errorf("authorized status = %d, want 200 (body: %s)", authorized.Code, authorized.Body)
	}
}

func TestWebhookConfiguredToAuthenticateFailsClosedWithoutACredential(t *testing.T) {
	h := newHarness(t)
	// Authentication requested, but no credential bound.
	h.activate(t, webhookDocument("Misconfigured", map[string]any{
		"path": "half-secured", "httpMethod": http.MethodPost, "responseMode": "immediate", "authentication": "headerAuth",
	}))

	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/webhook/half-secured", strings.NewReader(`{}`)))

	// Failing open would silently publish an unprotected endpoint.
	if recorder.Code == http.StatusOK {
		t.Fatalf("a webhook configured to authenticate answered without a credential: %s", recorder.Body)
	}
	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", recorder.Code)
	}
}

func TestWebhookRedactsInboundCredentialsBeforePersistence(t *testing.T) {
	h := newHarness(t)
	h.activate(t, webhookDocument("Redacting", map[string]any{
		"path": "redact", "httpMethod": http.MethodPost, "responseMode": "immediate",
	}))

	request := httptest.NewRequest(http.MethodPost, "/webhook/redact", strings.NewReader(`{"token":"body-secret"}`))
	request.Header.Set("Authorization", "Bearer inbound-secret")
	request.Header.Set("Cookie", "sid=inbound-cookie")
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", recorder.Code, recorder.Body)
	}

	var body struct {
		ExecutionID string `json:"executionId"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &body)
	h.drain(t)
	record, err := h.runtime.Get(context.Background(), h.tenant, body.ExecutionID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	encoded, _ := json.Marshal(record)
	for _, secret := range []string{"inbound-secret", "inbound-cookie", "body-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("the execution record retained %q: %s", secret, encoded)
		}
	}
}

func TestWebhookEnforcesTheBodyLimit(t *testing.T) {
	h := newHarness(t)
	h.activate(t, webhookDocument("Bounded", map[string]any{
		"path": "bounded", "httpMethod": http.MethodPost, "responseMode": "immediate",
	}))

	recorder := httptest.NewRecorder()
	oversized := strings.NewReader(`{"data":"` + strings.Repeat("a", 1000) + `"}`)
	h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/webhook/bounded", oversized))

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body: %s)", recorder.Code, recorder.Body)
	}
}

func TestActivationRefusesTwoWorkflowsClaimingTheSameEndpoint(t *testing.T) {
	h := newHarness(t)
	h.activate(t, webhookDocument("First", map[string]any{
		"path": "shared", "httpMethod": http.MethodPost, "responseMode": "immediate",
	}))

	second, err := h.workflows.SaveDraft(context.Background(), h.tenant, webhookDocument("Second", map[string]any{
		"path": "shared", "httpMethod": http.MethodPost, "responseMode": "immediate",
	}))
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	// An ambiguous route has no correct destination, so activation must fail
	// rather than leaving the winner to chance.
	if _, err := h.workflows.Activate(context.Background(), h.tenant, second.ID, h.registry); err == nil {
		t.Fatal("two active workflows were allowed to claim the same endpoint")
	}
}

func TestWebhookPayloadCarriesTheRequestShape(t *testing.T) {
	h := newHarness(t)
	h.activate(t, webhookDocument("Shape", map[string]any{
		"path": "shape", "httpMethod": http.MethodPost, "responseMode": "immediate",
	}))

	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/webhook/shape?tier=gold", strings.NewReader(`{"id":9}`)))
	var body struct {
		ExecutionID string `json:"executionId"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &body)

	h.drain(t)
	record, err := h.runtime.Get(context.Background(), h.tenant, body.ExecutionID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	var input struct {
		Method string            `json:"method"`
		Path   string            `json:"path"`
		Query  map[string]string `json:"query"`
		Body   map[string]any    `json:"body"`
	}
	if err := json.Unmarshal(record.Input, &input); err != nil {
		t.Fatalf("decode trigger input = %v", err)
	}
	if input.Method != http.MethodPost || input.Path != "shape" || input.Query["tier"] != "gold" || input.Body["id"] != float64(9) {
		t.Fatalf("trigger input = %#v, want the request shape", input)
	}
}
