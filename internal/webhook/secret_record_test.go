package webhook_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// BUG-g9zf51, end to end: an HTTP Request node signing with an httpQueryAuth
// credential fails against a closed port. Go's transport error printed the
// whole URL, query and secret included, and it was stored as the execution's
// error and as the error item a tolerated failure hands downstream. Neither the
// stored record nor anything served from it may carry the secret.
func TestAQueryCredentialNeverReachesTheExecutionRecord(t *testing.T) {
	h := newHarness(t)
	const secret = "QUERY-SECRET-e2e-51c0"
	credential, err := h.credentials.Create(context.Background(), h.tenant, credentials.Record{
		Name: "Query key", Type: "httpQueryAuth", Fields: map[string]string{"name": "api_key", "value": secret},
	})
	if err != nil {
		t.Fatalf("Create() credential error = %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	closed := "http://" + listener.Addr().String()
	_ = listener.Close()

	for _, onError := range []string{"stopWorkflow", "continueRegularOutput"} {
		call := workflow.Node{
			ID: "call", Name: "Call API", Type: "kilasflow.httpRequest", TypeVersion: workflow.V(1),
			Parameters:  map[string]any{"method": "GET", "url": closed + "/closed"},
			Credentials: map[string]string{"httpQueryAuth": credential.ID},
			Settings:    map[string]any{"onError": onError},
		}
		active := h.activate(t, webhookDocument("Leaky "+onError, map[string]any{
			"path": "leaky-" + onError, "httpMethod": http.MethodPost, "responseMode": "immediate",
		}, call))

		recorder := httptest.NewRecorder()
		h.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, h.url(t, active), strings.NewReader(`{}`)))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: status = %d (body: %s)", onError, recorder.Code, recorder.Body)
		}
		h.drain(t)
		record, err := h.runtime.Get(context.Background(), h.tenant, h.lastExecution(t))
		if err != nil {
			t.Fatalf("%s: Get() error = %v", onError, err)
		}
		stored, _ := json.Marshal(record)
		if strings.Contains(string(stored), secret) {
			t.Errorf("%s: the execution record carries the secret: %s", onError, stored)
		}
		if !strings.Contains(string(stored), "connection refused") && !strings.Contains(string(stored), "connect:") {
			t.Errorf("%s: the execution record lost what the failure was: %s", onError, stored)
		}
	}
}
