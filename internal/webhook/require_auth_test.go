package webhook_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/webhook"
	"github.com/kilaslab/kilas-flow/nodes"
)

// authHandler is the harness's wiring with the deployment switch set the way a
// test needs it. It shares the harness's stores and runner so a refusal is
// observable as "nothing was queued".
func authHandler(t *testing.T, h harness, required bool) *webhook.Handler {
	t.Helper()
	triggers := webhook.NewRegistry()
	if err := nodes.RegisterTriggerKinds(triggers); err != nil {
		t.Fatalf("RegisterTriggerKinds() error = %v", err)
	}
	return webhook.NewHandler(h.workflows, h.runner, h.credentials, nil, webhook.Limits{
		MaxBodyBytes: 512, ResponseTimeout: 2 * time.Second,
	}).WithTriggers(triggers).RequireAuthentication(required)
}

// strictHandler is composition with webhook.require_auth on.
func strictHandler(t *testing.T, h harness) *webhook.Handler {
	t.Helper()
	return authHandler(t, h, true)
}

// The whole point of the setting: an operator can refuse every trigger that
// does not authenticate its callers, and the caller is told which workflow and
// what to change.
func TestRequireAuthRefusesADeliveryToATriggerWithNoAuthentication(t *testing.T) {
	h := newHarness(t)
	strict := strictHandler(t, h)

	for _, parameters := range []map[string]any{
		{"path": "open", "httpMethod": http.MethodPost, "responseMode": "immediate"},
		{"path": "open-explicit", "httpMethod": http.MethodPost, "responseMode": "immediate", "authentication": "none"},
	} {
		active := h.activate(t, webhookDocument("Open", parameters))
		recorder := httptest.NewRecorder()
		strict.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`)))

		if recorder.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body: %s)", recorder.Code, recorder.Body)
		}
		if contentType := recorder.Header().Get("Content-Type"); contentType != "application/problem+json" {
			t.Errorf("Content-Type = %q, want application/problem+json", contentType)
		}
		if challenge := recorder.Header().Get("WWW-Authenticate"); challenge != "" {
			t.Errorf("WWW-Authenticate = %q, want none: a 403 is a policy refusal, not a challenge", challenge)
		}
		var problem struct {
			Detail string `json:"detail"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
			t.Fatalf("decode body = %v (body: %s)", err, recorder.Body)
		}
		for _, expected := range []string{active.ID, "webhook.require_auth", "Authentication"} {
			if !strings.Contains(problem.Detail, expected) {
				t.Errorf("detail is missing %q:\n%s", expected, problem.Detail)
			}
		}
	}

	if queued := h.queuedCount(); queued != 0 {
		t.Errorf("refused deliveries queued %d execution(s), want none", queued)
	}
}

// The default posture is unchanged: the same open trigger is admitted through
// the harness handler and through a handler that was explicitly relaxed.
func TestRequireAuthLeavesTheDefaultPostureUnchanged(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Open", map[string]any{
		"path": "open-default", "httpMethod": http.MethodPost, "responseMode": "immediate",
	}))

	for name, handler := range map[string]http.Handler{
		"composition":    h.handler,
		"explicitly off": authHandler(t, h, false),
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`)))
		if recorder.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200 (body: %s)", name, recorder.Code, recorder.Body)
		}
	}
}

// A trigger that authenticates its callers is unaffected, and keeps its own
// answers: a missing credential is still 401 with a challenge, never 403.
func TestRequireAuthAdmitsAnAuthenticatedTrigger(t *testing.T) {
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
	strict := strictHandler(t, h)

	anonymous := httptest.NewRecorder()
	strict.ServeHTTP(anonymous, httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`)))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401 (body: %s)", anonymous.Code, anonymous.Body)
	}
	if anonymous.Header().Get("WWW-Authenticate") == "" {
		t.Error("a 401 was returned without a WWW-Authenticate challenge")
	}

	wrong := httptest.NewRecorder()
	badRequest := httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`))
	badRequest.SetBasicAuth("ada", "wrong")
	strict.ServeHTTP(wrong, badRequest)
	if wrong.Code != http.StatusUnauthorized {
		t.Errorf("wrong password status = %d, want 401", wrong.Code)
	}

	authorized := httptest.NewRecorder()
	goodRequest := httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`))
	goodRequest.SetBasicAuth("ada", "hunter2")
	strict.ServeHTTP(authorized, goodRequest)
	if authorized.Code != http.StatusOK {
		t.Errorf("authorized status = %d, want 200 (body: %s)", authorized.Code, authorized.Body)
	}
}

// The hosted page faces the same gate as the submission it exists for.
func TestRequireAuthGatesAFormPageLikeItsSubmission(t *testing.T) {
	h := newHarness(t)
	strict := strictHandler(t, h)

	open := h.activate(t, formDocument(map[string]any{
		"path": "open-form", "formTitle": "Open", "responseMode": "immediate",
		"formFields": map[string]any{"values": []any{map[string]any{"fieldLabel": "Name", "fieldType": "text"}}},
	}))
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		recorder := httptest.NewRecorder()
		strict.ServeHTTP(recorder, httptest.NewRequest(method, h.url(t, open), strings.NewReader(`{}`)))
		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s status = %d, want 403 (body: %s)", method, recorder.Code, recorder.Body)
		}
	}

	credential, err := h.credentials.Create(context.Background(), h.tenant, credentials.Record{
		Name: "Form", Type: "httpBasicAuth", Fields: map[string]string{"user": "ada", "password": "hunter2"},
	})
	if err != nil {
		t.Fatalf("Create() credential error = %v", err)
	}
	document := formDocument(map[string]any{
		"path": "secured-form", "formTitle": "Secured", "responseMode": "immediate", "authentication": "basicAuth",
		"formFields": map[string]any{"values": []any{map[string]any{"fieldLabel": "Name", "fieldType": "text"}}},
	})
	document.Nodes[0].Credentials = map[string]string{"httpBasicAuth": credential.ID}
	secured := h.activate(t, document)

	page := httptest.NewRecorder()
	pageRequest := httptest.NewRequest(http.MethodGet, h.url(t, secured), nil)
	pageRequest.SetBasicAuth("ada", "hunter2")
	strict.ServeHTTP(page, pageRequest)
	if page.Code != http.StatusOK {
		t.Errorf("GET with credentials status = %d, want the page (body: %s)", page.Code, page.Body)
	}
}

// A preflight cannot carry a credential, so gating it would break the browser
// CORS flow the route exists for.
func TestRequireAuthDoesNotGateAPreflight(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Open", map[string]any{
		"path": "preflight", "httpMethod": http.MethodPost, "responseMode": "immediate",
	}))

	recorder := httptest.NewRecorder()
	strictHandler(t, h).ServeHTTP(recorder, httptest.NewRequest(http.MethodOptions, h.url(t, active), nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS status = %d, want 204 (body: %s)", recorder.Code, recorder.Body)
	}
}

// fakeBindings embeds the repository interface so a parallel ticket changing the
// delivery-claim surface does not break this fake: only the two lookups the
// handler actually makes are written out.
type fakeBindings struct {
	repository.WebhookRepository
	binding repository.WebhookBinding
}

func (f fakeBindings) Resolve(context.Context, string, string) (repository.WebhookBinding, error) {
	return f.binding, nil
}

func (f fakeBindings) ResolveRoute(context.Context, string) ([]repository.WebhookBinding, error) {
	return []repository.WebhookBinding{f.binding}, nil
}

// fakeRunner embeds webhook.Runner for the same reason.
type fakeRunner struct{ webhook.Runner }

func (fakeRunner) QueueWebhook(context.Context, repository.WebhookBinding, json.RawMessage) (execution.Record, error) {
	return execution.Record{ID: "e1"}, nil
}

func (fakeRunner) Get(context.Context, repository.TenantScope, string) (execution.Record, error) {
	return execution.Record{}, nil
}

// selfVerifyingHandler is composition with the deployment switch on and the
// three trigger shapes a kind can have: no verification, unconditional
// verification, and verification that depends on the binding.
func selfVerifyingHandler(t *testing.T, binding repository.WebhookBinding) http.Handler {
	t.Helper()
	verifySignature := func(delivery webhook.Delivery) error {
		if delivery.Request.Header.Get("X-Sig") != "ok" {
			return errors.New("This endpoint requires a matching X-Sig header.")
		}
		return nil
	}
	triggers := webhook.NewRegistry()
	for nodeType, kind := range map[string]webhook.TriggerKind{
		"test.plain":     {Shape: webhook.ShapeEnvelope},
		"test.verifying": {Shape: webhook.ShapeEnvelope, Verify: verifySignature},
		"test.optional": {
			Shape:  webhook.ShapeEnvelope,
			Verify: verifySignature,
			Verifies: func(delivery webhook.Delivery) bool {
				secret, _ := delivery.Binding.Parameters["secret"].(string)
				return strings.TrimSpace(secret) != ""
			},
		},
	} {
		if err := triggers.Register(nodeType, kind); err != nil {
			t.Fatalf("Register(%q) error = %v", nodeType, err)
		}
	}
	return webhook.NewHandler(fakeBindings{binding: binding}, fakeRunner{}, nil, nil, webhook.Limits{
		MaxBodyBytes: 512, ResponseTimeout: time.Second,
	}).WithTriggers(triggers).RequireAuthentication(true)
}

func selfVerifyingBinding(t *testing.T, nodeType string, parameters map[string]any) repository.WebhookBinding {
	t.Helper()
	if parameters == nil {
		parameters = map[string]any{}
	}
	parameters["responseMode"] = "immediate"
	return repository.WebhookBinding{
		TenantID: "t1", WorkflowID: "wf_self", NodeID: "n1", NodeType: nodeType,
		Method: http.MethodPost, Route: "self-route", Parameters: parameters,
	}
}

// A kind that verifies its own senders is authenticated, but only while it
// actually checks them: a pack trigger with no secret verifies nothing, and
// "has a verifier" is not the question the flag asks.
func TestRequireAuthTreatsASelfVerifyingTriggerAsAuthenticated(t *testing.T) {
	post := func(handler http.Handler, signature string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/webhook/self-route", strings.NewReader(`{}`))
		if signature != "" {
			request.Header.Set("X-Sig", signature)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	t.Run("plain kind", func(t *testing.T) {
		handler := selfVerifyingHandler(t, selfVerifyingBinding(t, "test.plain", nil))
		if recorder := post(handler, ""); recorder.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403 (body: %s)", recorder.Code, recorder.Body)
		}
	})

	t.Run("verifying kind", func(t *testing.T) {
		handler := selfVerifyingHandler(t, selfVerifyingBinding(t, "test.verifying", nil))
		if recorder := post(handler, "ok"); recorder.Code != http.StatusOK {
			t.Errorf("with a valid signature status = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
		}
		// The verifier's own answer is kept: a failed signature is 401, which
		// says "do not send this", not 403, which says "this deployment
		// refuses unauthenticated triggers".
		if recorder := post(handler, ""); recorder.Code != http.StatusUnauthorized {
			t.Errorf("with no signature status = %d, want 401 (body: %s)", recorder.Code, recorder.Body)
		}
	})

	t.Run("optional kind", func(t *testing.T) {
		without := selfVerifyingHandler(t, selfVerifyingBinding(t, "test.optional", nil))
		if recorder := post(without, "ok"); recorder.Code != http.StatusForbidden {
			t.Errorf("without a secret status = %d, want 403 (body: %s)", recorder.Code, recorder.Body)
		}

		with := selfVerifyingHandler(t, selfVerifyingBinding(t, "test.optional", map[string]any{"secret": "s3cret"}))
		if recorder := post(with, "ok"); recorder.Code != http.StatusOK {
			t.Errorf("with a secret and a valid signature status = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
		}
	})

	t.Run("unsupported mode", func(t *testing.T) {
		// An unknown authentication mode is not "none": it keeps its own
		// answer, which is the endpoint's configuration being wrong.
		handler := selfVerifyingHandler(t, selfVerifyingBinding(t, "test.plain", map[string]any{"authentication": "bogus"}))
		if recorder := post(handler, ""); recorder.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500 (body: %s)", recorder.Code, recorder.Body)
		}
	})

	t.Run("allow-list before the flag", func(t *testing.T) {
		// A caller the address allow-list already refused must not be handed
		// the require_auth diagnostic, which names the workflow.
		handler := selfVerifyingHandler(t, selfVerifyingBinding(t, "test.plain", map[string]any{
			"options": map[string]any{"ipWhitelist": "10.0.0.0/8"},
		}))
		recorder := post(handler, "")
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body: %s)", recorder.Code, recorder.Body)
		}
		body := recorder.Body.String()
		if !strings.Contains(body, "does not accept requests from your address") {
			t.Errorf("body is not the address refusal:\n%s", body)
		}
		if strings.Contains(body, "wf_self") {
			t.Errorf("the address refusal disclosed the workflow id:\n%s", body)
		}
	})
}

// The boot line states the posture in both directions, so an operator reading
// the logs knows which one they are running.
func TestPostureLineStatesWhetherAuthenticationIsRequired(t *testing.T) {
	var buffer bytes.Buffer
	handler := webhook.NewHandler(nil, nil, nil, nil, webhook.Limits{}).
		WithLogger(slog.New(slog.NewTextHandler(&buffer, nil)))

	handler.RequireAuthentication(true).LogPosture()
	on := buffer.String()
	if !strings.Contains(on, "require_auth=true") || !strings.Contains(on, "inbound webhooks require authentication") {
		t.Errorf("log with the flag on = %q, want the requiring posture", on)
	}

	buffer.Reset()
	handler.RequireAuthentication(false).LogPosture()
	off := buffer.String()
	if !strings.Contains(off, "require_auth=false") || !strings.Contains(off, "accept unauthenticated") {
		t.Errorf("log with the flag off = %q, want the open posture", off)
	}
}
