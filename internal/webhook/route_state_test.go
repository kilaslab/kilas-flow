package webhook_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/webhook"
)

// routeStateCipher is a lifecycle-state key made of one repeated byte.
func routeStateCipher(t *testing.T, fill byte) *credentials.Cipher {
	t.Helper()
	cipher, err := credentials.NewCipher(bytes.Repeat([]byte{fill}, credentials.KeySize))
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	return cipher
}

// TestADeliveryWhoseRouteStateCannotBeOpenedIsRefusedAndLogged: after the
// encryption key is rotated, a route whose registration kept a secret cannot
// open it, and is not routed — routed, it would read no secret and accept the
// delivery unsigned. The sender sees the same 404 as for any unknown route,
// but the server says why, naming the route and never a value. A route that
// simply does not exist is still a quiet 404: it is what every scanner sends.
func TestADeliveryWhoseRouteStateCannotBeOpenedIsRefusedAndLogged(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, webhookDocument("Sealed", map[string]any{"path": "sealed", "httpMethod": http.MethodPost}))
	target := h.url(t, active)
	route := strings.TrimPrefix(target, "/webhook/")

	sealing := repository.NewWorkflowStore(h.db.DB).WithLifecycleState(routeStateCipher(t, 1))
	if err := sealing.SaveLifecycleState(context.Background(), h.tenant, route, map[string]string{"secret": "whsec_1"}); err != nil {
		t.Fatalf("SaveLifecycleState() error = %v", err)
	}
	h.workflows.WithLifecycleState(routeStateCipher(t, 2))

	logs := &lockedBuffer{}
	handler := h.handler.(*webhook.Handler).WithLogger(slog.New(slog.NewTextHandler(logs, nil)))
	deliver := func(path string) int {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"id":7}`)))
		return recorder.Code
	}

	for _, path := range []string{target, target + "/below-the-route"} {
		if code := deliver(path); code != http.StatusNotFound {
			t.Fatalf("POST %s status = %d, want 404", path, code)
		}
	}
	if h.queuedCount() != 0 {
		t.Fatalf("queued %d executions, want none for a route whose state cannot be opened", h.queuedCount())
	}
	logged := logs.String()
	if strings.Count(logged, "level=ERROR") != 2 || strings.Count(logged, "route="+route) != 2 {
		t.Fatalf("log = %q, want one error naming the route for each refused delivery", logged)
	}
	if strings.Contains(logged, "whsec_1") || strings.Contains(logged, "below-the-route") {
		t.Fatalf("log = %q, want the route and nothing a sender or the service supplied", logged)
	}

	if code := deliver("/webhook/0123456789abcdef0123456789abcdef"); code != http.StatusNotFound {
		t.Fatalf("POST to an unknown route status = %d, want 404", code)
	}
	if after := logs.String(); after != logged {
		t.Fatalf("log after an unknown route = %q, want nothing new", strings.TrimPrefix(after, logged))
	}
}
