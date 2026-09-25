package webhook_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/execution"
)

// BUG-cmnsfz: a header-auth trigger whose credential names a custom header,
// such as X-Hook-Pass, kept the shared secret in the stored trigger payload and
// showed it in the execution view, because key-based redaction knows common
// header names and not this one. The header the trigger just verified is
// withheld by the name its credential gives before the payload is stored; the
// caller's other headers are kept for the run as they always were.
func TestAHeaderAuthSecretIsWithheldByItsConfiguredName(t *testing.T) {
	h := newHarness(t)
	const secret = "hook-pass-shared-secret-51"
	credential, err := h.credentials.Create(context.Background(), h.tenant, credentials.Record{
		Name: "Hook pass", Type: "httpHeaderAuth", Fields: map[string]string{"name": "X-Hook-Pass", "value": secret},
	})
	if err != nil {
		t.Fatalf("Create() credential error = %v", err)
	}
	document := webhookDocument("Custom header auth", map[string]any{
		"path": "custom-header", "httpMethod": http.MethodPost, "responseMode": "immediate", "authentication": "headerAuth",
	})
	document.Nodes[0].Credentials = map[string]string{"httpHeaderAuth": credential.ID}
	active := h.activate(t, document)

	request := httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{"note":"body-value"}`))
	request.Header.Set("X-Hook-Pass", secret)
	request.Header.Set("X-Request-Tag", "kept-header-value")
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
	stored, _ := json.Marshal(record)
	if strings.Contains(string(stored), secret) {
		t.Errorf("the stored execution carries the header-auth secret: %s", stored)
	}
	for _, kept := range []string{"kept-header-value", "body-value", "x-hook-pass"} {
		if !strings.Contains(string(record.Input), kept) {
			t.Errorf("the stored trigger input lost %q: %s", kept, record.Input)
		}
	}
	if !strings.Contains(string(record.Input), execution.RedactedValue) {
		t.Errorf("the stored trigger input does not mark the withheld header: %s", record.Input)
	}

	// A wrong secret is still refused: withholding the header is what happens
	// after it was verified, not instead of verifying it.
	refused := httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`))
	refused.Header.Set("X-Hook-Pass", "wrong")
	recorder = httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, refused)
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("a wrong header secret got status %d, want 401", recorder.Code)
	}
}
