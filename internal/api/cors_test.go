package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/config"
)

// corsServer is one server whose embed allowlist names a single host origin, so
// the difference between an allowlisted and a stranger origin is visible.
func corsServer(t *testing.T, origins []string) http.Handler {
	t.Helper()

	settings := config.Default()
	settings.Embed.AllowedOrigins = origins
	return newTestServer(t, api.Deps{DB: stubPinger{}, Version: "0.0.0-test", Config: settings})
}

func corsRequest(t *testing.T, handler http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(method, path, nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// The embedding guide has the host page stream execution events from a
// different origin, and the browser refuses that without these headers. The
// preflight is the case that used to fail hardest: it carries no credential by
// definition, so the auth gate answered it with a 401 the browser reports as a
// CORS failure — after the request had already been made.
func TestCORSPreflightIsAnsweredBeforeTheAuthGate(t *testing.T) {
	handler := corsServer(t, []string{"https://host.example"})

	recorder := corsRequest(t, handler, http.MethodOptions, "/api/v1/executions/exec_1/events", map[string]string{
		"Origin":                         "https://host.example",
		"Access-Control-Request-Method":  http.MethodGet,
		"Access-Control-Request-Headers": "Authorization",
	})
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204 (body: %s)", recorder.Code, recorder.Body)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://host.example" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the allowlisted origin reflected", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want the header absent", got)
	}
}

// A cross-origin event stream must be readable: the ticket travels in the query
// string, and the browser needs the origin reflected on the actual response too,
// not only on the preflight.
func TestCORSReflectsAnAllowlistedOriginOnTheStreamRequest(t *testing.T) {
	handler := corsServer(t, []string{"https://host.example"})

	recorder := corsRequest(t, handler, http.MethodGet, "/api/v1/executions/exec_1/events?ticket=abc", map[string]string{
		"Origin": "https://host.example",
	})
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://host.example" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the allowlisted origin reflected", got)
	}
	if got := recorder.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin", got)
	}
	if got := recorder.Header().Get("Access-Control-Expose-Headers"); got != "X-Request-ID, X-Next-Cursor, Idempotent-Replayed, Retry-After" {
		t.Errorf("Access-Control-Expose-Headers = %q, want the pagination, request-id, replay and retry headers readable", got)
	}
}

// An origin nobody allowlisted gets no CORS headers at all, which is what makes
// the browser block it.
func TestCORSLeavesAnUnlistedOriginWithoutHeaders(t *testing.T) {
	handler := corsServer(t, []string{"https://host.example"})

	recorder := corsRequest(t, handler, http.MethodGet, "/api/v1/executions/exec_1/events?ticket=abc", map[string]string{
		"Origin": "https://evil.example",
	})
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q for an unlisted origin, want none", got)
	}
}

// An instance that embeds nothing — the default — must not become an open door:
// the empty allowlist reflects nobody.
func TestCORSWithNoAllowlistAnswersNoOrigin(t *testing.T) {
	handler := corsServer(t, nil)

	recorder := corsRequest(t, handler, http.MethodGet, "/api/v1/health", map[string]string{
		"Origin": "https://host.example",
	})
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q with no configured origins, want none", got)
	}
}

// The layer exists for one endpoint — the execution event stream a host page
// opens against the API — and it is mounted on the API prefix alone.
//
// Mounted on the shared mux it also decorated the public webhook surface and
// the SPA's own responses, which widened a control that one route justifies:
// a host page's browser learned, from a response to a URL that is not the API,
// that this instance speaks cross-origin.
func TestCORSHeadersStayInsideTheAPIPrefix(t *testing.T) {
	handler := corsServer(t, []string{"https://host.example"})
	origin := map[string]string{"Origin": "https://host.example"}

	for _, path := range []string{
		"/webhook/incoming",
		"/resume/exec_1",
		"/embed/wf_1",
		"/app/workflows",
	} {
		recorder := corsRequest(t, handler, http.MethodGet, path, origin)
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("%s: Access-Control-Allow-Origin = %q, want none outside %s", path, got, api.APIPrefix)
		}
		if got := recorder.Header().Get("Vary"); got != "" {
			t.Errorf("%s: Vary = %q, want no CORS headers at all", path, got)
		}
	}

	// The endpoint the layer exists for still carries them, which is the half
	// that must not be lost with the narrowing.
	recorder := corsRequest(t, handler, http.MethodGet, api.APIPrefix+"/executions/exec_1/events?ticket=abc", origin)
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://host.example" {
		t.Errorf("events: Access-Control-Allow-Origin = %q, want the allowlisted origin reflected", got)
	}
}
