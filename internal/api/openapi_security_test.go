package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/api"
	"github.com/kilaslabs/kilas-flow/internal/config"
)

// openAPIDocument is only the part of the document these tests read.
type openAPIDocument struct {
	Components struct {
		SecuritySchemes map[string]struct {
			Type   string `json:"type"`
			Scheme string `json:"scheme"`
			In     string `json:"in"`
			Name   string `json:"name"`
		} `json:"securitySchemes"`
	} `json:"components"`
	Security []map[string][]string `json:"security"`
	Paths    map[string]map[string]struct {
		Security *[]map[string][]string `json:"security"`
	} `json:"paths"`
}

// request drives one call through the router, so a test can assert what the
// middleware did with a method and path rather than what a list says.
func request(t *testing.T, handler http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func fetchOpenAPIDocument(t *testing.T, handler http.Handler) openAPIDocument {
	t.Helper()

	rec := get(t, handler, "/api/openapi.json")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/openapi.json = %d, want 200", rec.Code)
	}

	var document openAPIDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode the document: %v", err)
	}
	return document
}

// authEnabledDeps is the configuration an operator runs when the instance is
// reachable by anyone, which is when the document has to be explicit about
// credentials.
func authEnabledDeps() api.Deps {
	deps := api.Deps{DB: stubPinger{}, Version: "1.2.3"}
	deps.Config = config.Default()
	deps.Config.Auth.Enabled = true
	return deps
}

// With auth on, every operation needs an API key or a session cookie. A document
// that does not say so is why /docs offered nowhere to enter a key and generated
// clients carried no auth typing.
func TestOpenAPIDeclaresBothCredentialsWhenAuthIsEnabled(t *testing.T) {
	h := newTestServer(t, authEnabledDeps())
	document := fetchOpenAPIDocument(t, h)

	bearer, ok := document.Components.SecuritySchemes["apiKey"]
	if !ok {
		t.Fatalf("no apiKey security scheme; schemes = %v", document.Components.SecuritySchemes)
	}
	if bearer.Type != "http" || bearer.Scheme != "bearer" {
		t.Errorf("apiKey scheme = %+v, want an HTTP bearer scheme", bearer)
	}

	session, ok := document.Components.SecuritySchemes["session"]
	if !ok {
		t.Fatalf("no session security scheme; schemes = %v", document.Components.SecuritySchemes)
	}
	if session.Type != "apiKey" || session.In != "cookie" || session.Name == "" {
		t.Errorf("session scheme = %+v, want an apiKey scheme in a named cookie", session)
	}

	// Two entries rather than one object holding both: alternatives are ORed,
	// and a client holding only a cookie must not be told to send a bearer token
	// as well.
	if len(document.Security) != 2 {
		t.Fatalf("document security = %v, want two alternatives", document.Security)
	}
	for _, requirement := range document.Security {
		if len(requirement) != 1 {
			t.Errorf("security requirement %v names %d schemes, want exactly one", requirement, len(requirement))
		}
	}

	// The requirement is inherited, so an operation that says nothing is
	// documented as closed rather than as open.
	if operation := document.Paths["/api/v1/workflows"]["get"]; operation.Security != nil {
		t.Errorf("GET /api/v1/workflows overrides the security requirement with %v", *operation.Security)
	}
}

// The public operations must say so in the document, or a generated client
// demands a key to read /health.
func TestOpenAPIMarksThePublicOperations(t *testing.T) {
	h := newTestServer(t, authEnabledDeps())
	document := fetchOpenAPIDocument(t, h)

	public := []string{
		"/api/v1/health",
		"/api/v1/ready",
		"/api/v1/auth/login",
		"/api/v1/auth/logout",
	}

	for _, route := range public {
		methods, ok := document.Paths[route]
		if !ok {
			t.Errorf("the document has no %s", route)
			continue
		}
		for method, operation := range methods {
			if operation.Security == nil {
				t.Errorf("%s %s inherits the credential requirement", method, route)
				continue
			}
			if len(*operation.Security) != 0 {
				t.Errorf("%s %s security = %v, want an empty array", method, route, *operation.Security)
			}
		}
	}
}

// The document's public list is a copy of the middleware's, and the two drifting
// apart is the failure this test exists for: it asks the server instead of
// reading either list, so an operation the middleware refuses while the document
// calls public — or the reverse — fails here.
func TestOpenAPIPublicOperationsAreActuallyServedWithoutACredential(t *testing.T) {
	h := newTestServer(t, authEnabledDeps())
	document := fetchOpenAPIDocument(t, h)

	requests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/health"},
		{http.MethodGet, "/api/v1/ready"},
		{http.MethodPost, "/api/v1/auth/login"},
		{http.MethodPost, "/api/v1/auth/logout"},
	}

	for _, public := range requests {
		rec := request(t, h, public.method, public.path)
		if rec.Code == http.StatusUnauthorized {
			t.Errorf("%s %s = 401: the middleware demands a credential the document says it does not", public.method, public.path)
		}
	}

	// The other half of the contract: an operation that is not exempt must be
	// refused without a credential, or the document would understate rather than
	// overstate what is needed.
	rec := request(t, h, http.MethodGet, "/api/v1/workflows")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/workflows = %d, want 401 without a credential", rec.Code)
	}

	// And every operation the document marks public must be one this server
	// really does special-case, rather than a stale entry.
	for route, methods := range document.Paths {
		for method, operation := range methods {
			if operation.Security == nil || len(*operation.Security) != 0 {
				continue
			}
			rec := request(t, h, method, route)
			if rec.Code == http.StatusUnauthorized {
				t.Errorf("%s %s is documented as public but answers 401", method, route)
			}
		}
	}
}

// With auth off the API is open, and a document claiming otherwise would send
// every reader looking for a key that does not exist.
func TestOpenAPIDeclaresNoRequirementWhenAuthIsDisabled(t *testing.T) {
	deps := api.Deps{DB: stubPinger{}, Version: "1.2.3"}
	deps.Config = config.Default()
	deps.Config.Auth.Enabled = false

	document := fetchOpenAPIDocument(t, newTestServer(t, deps))
	if len(document.Security) != 0 {
		t.Fatalf("document security = %v with auth disabled, want none", document.Security)
	}
}
