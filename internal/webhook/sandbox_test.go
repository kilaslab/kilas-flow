package webhook_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// sandboxTokens returns the flags of the policy's sandbox directive, and
// whether the policy has one at all. An empty sandbox directive is the
// strictest sandbox, so "present with no tokens" is a meaningful answer.
func sandboxTokens(policy string) ([]string, bool) {
	for _, directive := range strings.Split(policy, ";") {
		fields := strings.Fields(directive)
		if len(fields) > 0 && strings.EqualFold(fields[0], "sandbox") {
			return fields[1:], true
		}
	}
	return nil, false
}

// assertSandboxed checks the property BUG-x2sxzt is about: whatever the body,
// the browser renders it in an opaque origin, so a tenant-written page cannot
// act with the instance's own cookies or read its same-origin API.
func assertSandboxed(t *testing.T, recorder *httptest.ResponseRecorder) []string {
	t.Helper()
	policies := recorder.Header().Values("Content-Security-Policy")
	if len(policies) != 1 {
		t.Fatalf("Content-Security-Policy = %q, want exactly one policy", policies)
	}
	tokens, found := sandboxTokens(policies[0])
	if !found {
		t.Fatalf("Content-Security-Policy = %q, want a sandbox directive", policies[0])
	}
	for _, token := range tokens {
		if strings.EqualFold(token, "allow-same-origin") {
			t.Fatalf("Content-Security-Policy = %q grants allow-same-origin, which gives the page the instance's origin back", policies[0])
		}
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	return tokens
}

func respondDocument(path string, respond map[string]any) workflow.Document {
	return webhookDocument("Responder", map[string]any{
		"path": path, "httpMethod": http.MethodPost, "responseMode": "responseNode",
	}, workflow.Node{
		ID: "respond", Name: "Respond to Webhook", Type: nodes.RespondNodeType, TypeVersion: workflow.V(1),
		Parameters: respond,
	})
}

// A Respond to Webhook node's HTML is the case the ticket names: a script in
// it must not run as the instance.
func TestRespondToWebhookHTMLIsServedSandboxed(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, respondDocument("sandboxed-html", map[string]any{
		"respondWith":  "text",
		"responseBody": `<html><body><script>fetch("/api/v1/workflows")</script></body></html>`,
	}))

	recorder := httptest.NewRecorder()
	drainWhile(t, h, func() {
		h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`)))
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", recorder.Code, recorder.Body)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("Content-Type = %q, want the page rendered as HTML", got)
	}
	tokens := assertSandboxed(t, recorder)
	// The page is still a working page: its own script runs, in the opaque
	// origin, which is what an n8n author returning an HTML page expects.
	if !slices.Contains(tokens, "allow-scripts") {
		t.Errorf("sandbox = %q, want allow-scripts so a returned page keeps working", tokens)
	}
}

// A workflow sets its own headers, and a Content-Security-Policy is a header
// like any other: it must not be able to hand itself allow-same-origin, nor
// switch content sniffing back on.
func TestRespondToWebhookCannotWeakenTheSandbox(t *testing.T) {
	h := newHarness(t)
	weak := "sandbox allow-scripts allow-same-origin"
	active := h.activate(t, respondDocument("weakened-csp", map[string]any{
		"respondWith":  "text",
		"responseBody": `<script>alert(document.cookie)</script>`,
		"responseHeaders": map[string]any{
			"Content-Security-Policy": weak,
			"X-Content-Type-Options":  "sniff",
			"Content-Type":            "text/html",
			"X-Kept":                  "yes",
		},
	}))

	recorder := httptest.NewRecorder()
	drainWhile(t, h, func() {
		h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`)))
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", recorder.Code, recorder.Body)
	}
	assertSandboxed(t, recorder)
	if got := recorder.Header().Get("Content-Security-Policy"); got == weak {
		t.Fatalf("the workflow's own policy %q reached the browser", got)
	}
	// Only the security headers are forced; the rest of the workflow's answer
	// is still its own.
	if got := recorder.Header().Get("X-Kept"); got != "yes" {
		t.Errorf("X-Kept = %q, want the workflow's other headers kept", got)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/html" {
		t.Errorf("Content-Type = %q, want the workflow's own content type kept", got)
	}
}

// A JSON answer carries the same headers. The workflow picks the body and can
// pick any Content-Type, so which responses are "HTML" is its choice rather
// than the handler's; the header is uniform on purpose.
func TestRespondToWebhookJSONCarriesTheSandboxToo(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, respondDocument("sandboxed-json", map[string]any{
		"respondWith": "json", "responseBody": `{"ok":true}`,
	}))

	recorder := httptest.NewRecorder()
	drainWhile(t, h, func() {
		h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`)))
	})

	if got := recorder.Body.String(); got != `{"ok":true}` {
		t.Fatalf("body = %q, want the node's JSON untouched", got)
	}
	assertSandboxed(t, recorder)
}

// The trigger's own responseData acknowledgement is text/html too, written by
// the same tenant, with headers the same tenant chose.
func TestImmediateAcknowledgementTextIsServedSandboxed(t *testing.T) {
	h := newHarness(t)
	weak := "sandbox allow-scripts allow-same-origin"
	active := h.activate(t, webhookDocument("Ack", map[string]any{
		"path": "sandboxed-ack", "httpMethod": http.MethodPost, "responseMode": "immediate",
		"options": map[string]any{
			"responseData":    `<script>fetch("/api/v1/credentials")</script>`,
			"responseHeaders": map[string]any{"Content-Security-Policy": weak},
		},
	}))

	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`)))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", recorder.Code, recorder.Body)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("Content-Type = %q, want the acknowledgement as HTML", got)
	}
	assertSandboxed(t, recorder)
	if got := recorder.Header().Get("Content-Security-Policy"); got == weak {
		t.Fatalf("the trigger's own policy %q reached the browser", got)
	}
}

// The hosted form is KilasFlow's own markup around tenant-written labels. It
// needs no script, so its sandbox grants none — only the form submission the
// page exists for — and a trigger's header cannot loosen it either.
func TestHostedFormPagesAreSandboxedWithoutScript(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, formDocument(map[string]any{
		"path": "sandboxed-form", "formTitle": "Sign up",
		"formFields": map[string]any{"values": []any{
			map[string]any{"fieldLabel": "Name", "fieldType": "text", "requiredField": true},
		}},
		"responseMode": "immediate",
		"options": map[string]any{
			"responseHeaders": map[string]any{"Content-Security-Policy": "sandbox allow-scripts allow-same-origin allow-forms"},
		},
	}))
	url := h.url(t, active)

	page := httptest.NewRecorder()
	h.handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, url, nil))
	refusal := httptest.NewRecorder()
	refused := httptest.NewRequest(http.MethodPost, url, strings.NewReader("Other=1"))
	refused.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handler.ServeHTTP(refusal, refused)
	accepted := httptest.NewRecorder()
	submission := httptest.NewRequest(http.MethodPost, url, strings.NewReader("Name=Ada"))
	submission.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handler.ServeHTTP(accepted, submission)

	for name, recorder := range map[string]*httptest.ResponseRecorder{
		"page": page, "refusal": refusal, "acknowledgement": accepted,
	} {
		t.Run(name, func(t *testing.T) {
			if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
				t.Fatalf("Content-Type = %q (status %d), want a page", got, recorder.Code)
			}
			tokens := assertSandboxed(t, recorder)
			if !slices.Contains(tokens, "allow-forms") {
				t.Errorf("sandbox = %q, want allow-forms so the form can still be submitted", tokens)
			}
			if slices.Contains(tokens, "allow-scripts") {
				t.Errorf("sandbox = %q grants script to a page that carries none", tokens)
			}
		})
	}
}

// The platform's own refusals go out through the same writer, so no path out
// of the handler is left without the headers.
func TestWebhookRefusalsCarryTheSandbox(t *testing.T) {
	h := newHarness(t)
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/webhook/"+strings.Repeat("0", 32), strings.NewReader(`{}`)))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an unknown route", recorder.Code)
	}
	assertSandboxed(t, recorder)
}
