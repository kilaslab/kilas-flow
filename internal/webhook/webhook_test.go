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

	"github.com/kilaslabs/kilas-flow/internal/ai"
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
	active := h.activate(t, webhookDocument("Responder", map[string]any{
		"path": "reply", "httpMethod": http.MethodPost, "responseMode": "responseNode",
	}, workflow.Node{
		ID: "respond", Name: "Respond to Webhook", Type: nodes.RespondNodeType, TypeVersion: workflow.V(1),
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

func TestWebhookReportsWhenNoResponseNodeWasReached(t *testing.T) {
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

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body: %s)", recorder.Code, recorder.Body)
	}
	if !strings.Contains(recorder.Body.String(), "Respond to Webhook") {
		t.Errorf("body = %s, want it to name the missing node", recorder.Body)
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

func TestWebhookConfiguredToAuthenticateFailsClosedWithoutACredential(t *testing.T) {
	h := newHarness(t)
	// Authentication requested, but no credential bound.
	active := h.activate(t, webhookDocument("Misconfigured", map[string]any{
		"path": "half-secured", "httpMethod": http.MethodPost, "responseMode": "immediate", "authentication": "headerAuth",
	}))

	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`)))

	// Failing open would silently publish an unprotected endpoint.
	if recorder.Code == http.StatusOK {
		t.Fatalf("a webhook configured to authenticate answered without a credential: %s", recorder.Body)
	}
	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", recorder.Code)
	}
}

// TestWebhookRedactsInboundHeadersButKeepsTheBody pins the split this boundary
// now makes.
//
// A caller's Authorization or Cookie header is a credential and is never stored.
// The body is the caller's own data and the running workflow reads it straight
// back out of the record, so redacting it would put "[redacted]" on the wire in
// place of what actually arrived.
func TestWebhookRedactsInboundHeadersButKeepsTheBody(t *testing.T) {
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
	for _, secret := range []string{"inbound-secret", "inbound-cookie"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("the execution record retained the header credential %q: %s", secret, encoded)
		}
	}
	if !strings.Contains(string(encoded), "body-value") {
		t.Errorf("the execution record lost the request body, which the workflow runs on: %s", encoded)
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

	var body struct {
		ExecutionID string `json:"executionId"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &body)
	h.drain(t)
	record, err := h.runtime.Get(context.Background(), h.tenant, body.ExecutionID)
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
	active := h.activate(t, webhookDocument("Shape", map[string]any{
		"path": "shape", "httpMethod": http.MethodPost, "responseMode": "immediate",
	}))

	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, h.url(t, active)+"?tier=gold", strings.NewReader(`{"id":9}`)))
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
				Parameters: map[string]any{"responseCode": float64(200), "responseBody": "vip-branch"}},
			{ID: "standard", Name: "Standard reply", Type: "kilasflow.respondToWebhook", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"responseCode": float64(202), "responseBody": "standard-branch"}},
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
		var body struct {
			ExecutionID string `json:"executionId"`
		}
		_ = json.Unmarshal(recorder.Body.Bytes(), &body)
		record, err := h.runtime.Get(context.Background(), testCase.tenant, body.ExecutionID)
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
			executions[body.ExecutionID] = true
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
		var body struct {
			ExecutionID string `json:"executionId"`
		}
		_ = json.Unmarshal(recorder.Body.Bytes(), &body)
		executions[body.ExecutionID] = true
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
		var body struct {
			ExecutionID string `json:"executionId"`
		}
		_ = json.Unmarshal(recorder.Body.Bytes(), &body)
		executions[body.ExecutionID] = true
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

	var accepted struct {
		ExecutionID string `json:"executionId"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &accepted)
	h.drain(t)
	record, err := h.runtime.Get(context.Background(), h.tenant, accepted.ExecutionID)
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
		"no data is an empty 204":                     {responseData: "noData", wantStatus: http.StatusNoContent, wantEmpty: true},
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
		// A redirect with a 200 is not a redirect, so the code defaults to 302
		// rather than leaving a browser on a blank page.
		"redirect defaults to 302 and sets Location": {
			parameters: map[string]any{"respondWith": "redirect", "redirectURL": "https://example.test/thanks"},
			wantStatus: http.StatusFound, wantBody: "",
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
