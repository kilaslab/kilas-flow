package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// corsProbe drives one request through the middleware and reports the response,
// whether the wrapped handler ran, and the headers it was answered with.
func corsProbe(t *testing.T, allowed []string, method string, headers map[string]string) (*httptest.ResponseRecorder, bool) {
	t.Helper()

	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	request := httptest.NewRequest(method, "/api/v1/executions/exec_1/events", nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}

	recorder := httptest.NewRecorder()
	CORS(allowed)(next).ServeHTTP(recorder, request)
	return recorder, reached
}

func TestCORSReflectsAnAllowlistedOrigin(t *testing.T) {
	t.Parallel()

	recorder, reached := corsProbe(t, []string{"https://host.example"}, http.MethodGet, map[string]string{
		"Origin": "https://host.example",
	})
	if !reached {
		t.Fatal("an ordinary cross-origin request must still reach the handler")
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://host.example" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the request's own origin", got)
	}
	if got := recorder.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin so a shared cache cannot serve a headerless answer", got)
	}
	if got := recorder.Header().Get("Access-Control-Expose-Headers"); got != "X-Request-ID, X-Next-Cursor" {
		t.Errorf("Access-Control-Expose-Headers = %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Headers"); got != corsDefaultHeaders {
		t.Errorf("Access-Control-Allow-Headers = %q, want the default list", got)
	}
}

// The allowlist is normalised the same way embed sessions are, so an operator
// who writes the configured origin with a trailing path or in upper case still
// gets a working embed page.
func TestCORSMatchesAfterNormalisingBothSides(t *testing.T) {
	t.Parallel()

	recorder, _ := corsProbe(t, []string{" HTTPS://Host.Example/embed "}, http.MethodGet, map[string]string{
		"Origin": "https://host.example",
	})
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://host.example" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the normalised origin to match", got)
	}
}

func TestCORSLeavesADisallowedOriginUnanswered(t *testing.T) {
	t.Parallel()

	recorder, reached := corsProbe(t, []string{"https://host.example"}, http.MethodGet, map[string]string{
		"Origin": "https://evil.example",
	})
	if !reached {
		t.Error("a disallowed origin is refused by the browser, not by this middleware")
	}
	for _, name := range []string{
		"Access-Control-Allow-Origin", "Access-Control-Allow-Methods",
		"Access-Control-Allow-Headers", "Access-Control-Expose-Headers",
	} {
		if got := recorder.Header().Get(name); got != "" {
			t.Errorf("%s = %q for a disallowed origin, want no CORS headers at all", name, got)
		}
	}
}

func TestCORSPreflightAnswersWithoutReachingTheHandler(t *testing.T) {
	t.Parallel()

	recorder, reached := corsProbe(t, []string{"https://host.example"}, http.MethodOptions, map[string]string{
		"Origin":                         "https://host.example",
		"Access-Control-Request-Method":  http.MethodPost,
		"Access-Control-Request-Headers": "Authorization, Content-Type",
	})
	if reached {
		t.Fatal("a preflight carries no credential, so it must be answered before the auth gate")
	}
	if recorder.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://host.example" {
		t.Errorf("Access-Control-Allow-Origin = %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Methods"); got != corsMethods {
		t.Errorf("Access-Control-Allow-Methods = %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Headers"); got != "Authorization, Content-Type" {
		t.Errorf("Access-Control-Allow-Headers = %q, want the browser's own list echoed", got)
	}
	if got := recorder.Header().Get("Access-Control-Max-Age"); got != corsMaxAge {
		t.Errorf("Access-Control-Max-Age = %q, want %q", got, corsMaxAge)
	}
}

// A disallowed origin gets no preflight answer either: the 204 would otherwise
// tell it the route exists and is reachable.
func TestCORSPreflightOfADisallowedOriginIsNotAnswered(t *testing.T) {
	t.Parallel()

	recorder, reached := corsProbe(t, []string{"https://host.example"}, http.MethodOptions, map[string]string{
		"Origin":                        "https://evil.example",
		"Access-Control-Request-Method": http.MethodPost,
	})
	if !reached {
		t.Error("the middleware must not answer a preflight for an origin it does not know")
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want none", got)
	}
	if recorder.Code == http.StatusNoContent {
		t.Error("a disallowed preflight must not be answered with 204")
	}
}

func TestCORSPassesThroughARequestWithNoOrigin(t *testing.T) {
	t.Parallel()

	recorder, reached := corsProbe(t, []string{"https://host.example"}, http.MethodGet, nil)
	if !reached {
		t.Fatal("a request without an Origin must reach the handler")
	}
	for _, name := range []string{"Access-Control-Allow-Origin", "Vary", "Access-Control-Allow-Methods"} {
		if got := recorder.Header().Get(name); got != "" {
			t.Errorf("%s = %q for a same-origin request, want none", name, got)
		}
	}
}

// Cookies must never become readable cross-origin: with no
// Access-Control-Allow-Credentials a browser refuses to send the session cookie
// at all, which is the whole reason the API is bearer and ticket based.
func TestCORSNeverAllowsCredentials(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		method  string
		headers map[string]string
	}{
		{"simple request", http.MethodGet, map[string]string{"Origin": "https://host.example", "Cookie": "kilasflow_session=secret"}},
		{"api key request", http.MethodPost, map[string]string{"Origin": "https://host.example", "Authorization": "Bearer kf_secret"}},
		{"preflight", http.MethodOptions, map[string]string{
			"Origin":                        "https://host.example",
			"Access-Control-Request-Method": http.MethodGet,
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			recorder, _ := corsProbe(t, []string{"https://host.example"}, testCase.method, testCase.headers)
			if got := recorder.Header().Get("Access-Control-Allow-Credentials"); got != "" {
				t.Errorf("Access-Control-Allow-Credentials = %q, want the header absent", got)
			}
		})
	}
}

// An empty allowlist — the default for a deployment that embeds nothing — must
// not become an open door through a reflected "*".
func TestCORSWithNoConfiguredOriginsAnswersNobody(t *testing.T) {
	t.Parallel()

	for _, configured := range [][]string{nil, {}, {"", "not-a-url"}} {
		recorder, _ := corsProbe(t, configured, http.MethodGet, map[string]string{"Origin": "https://host.example"})
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("Access-Control-Allow-Origin = %q with allowlist %q, want none", got, configured)
		}
	}
}
