package engine_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// stubCredentials resolves one credential, whatever it is asked for.
type stubCredentials struct{ credential engine.Credential }

func (stub stubCredentials) ResolveCredential(context.Context, string) (engine.Credential, error) {
	return stub.credential, nil
}

func scopedRequest(allowedDomains []string) engine.Request {
	return engine.Request{Credentials: stubCredentials{credential: engine.Credential{
		ID:             "cred_1",
		Name:           "API key",
		Type:           "httpHeaderAuth",
		Fields:         map[string]string{"name": "X-Api-Key", "value": "s3cret"},
		AllowedDomains: allowedDomains,
	}}}
}

func httpNode() workflow.IRNode {
	return workflow.IRNode{Name: "HTTP Request", Credentials: map[string]string{"httpHeaderAuth": "cred_1"}}
}

// TestAuthenticateScopesTheCredentialToItsAllowedDomains is the half that used
// to be missing: the domain bound was checked against the first URL and then
// dropped, so a redirect could carry the secret anywhere.
func TestAuthenticateScopesTheCredentialToItsAllowedDomains(t *testing.T) {
	t.Parallel()

	request := scopedRequest([]string{"api.example.com"})
	httpRequest, err := http.NewRequest(http.MethodGet, "https://api.example.com/v1/items", nil)
	if err != nil {
		t.Fatalf("building the request failed: %v", err)
	}
	if err := request.Authenticate(context.Background(), httpNode(), httpRequest); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if got := httpRequest.Header.Get("X-Api-Key"); got != "s3cret" {
		t.Fatalf("header = %q, want the credential applied", got)
	}

	// The scope has to be on the request context, because that is what the
	// HTTP client carries into the redirect chain and what CheckRedirect reads.
	scope, carried := safehttp.CredentialScopeFrom(httpRequest.Context())
	if !carried {
		t.Fatal("no credential scope on the request context: a redirect would carry the secret to any host")
	}
	if !scope.AllowsHost("api.example.com:443") {
		t.Error("the scope refuses the host the credential was checked against")
	}
	if scope.AllowsHost("evil.example.com") {
		t.Error("the scope allows a host AllowedDomains never named")
	}

	// The first URL is still refused before the secret is applied.
	outside, err := http.NewRequest(http.MethodGet, "https://evil.example.com/v1/items", nil)
	if err != nil {
		t.Fatalf("building the request failed: %v", err)
	}
	if err := request.Authenticate(context.Background(), httpNode(), outside); err == nil {
		t.Fatal("Authenticate() allowed a host outside AllowedDomains")
	}
	if got := outside.Header.Get("X-Api-Key"); got != "" {
		t.Errorf("header = %q, want no secret on a refused host", got)
	}
}

// TestARedirectOutsideTheScopeStopsTheChainWithoutTheSecret drives the whole
// path — Authenticate, the request context, safehttp's CheckRedirect — through
// a real HTTP client and two real servers. The redirect target differs only by
// hostname (`localhost` against `127.0.0.1`), which is exactly the hop the
// scope exists to refuse.
func TestARedirectOutsideTheScopeStopsTheChainWithoutTheSecret(t *testing.T) {
	t.Parallel()

	var leaked string
	destination := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		leaked = request.Header.Get("X-Api-Key")
		writer.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "http://"+localHostPort(t, destination.URL)+"/steal", http.StatusFound)
	}))
	defer redirector.Close()

	request := scopedRequest([]string{"127.0.0.1"})
	httpRequest, err := http.NewRequest(http.MethodGet, redirector.URL+"/start", nil)
	if err != nil {
		t.Fatalf("building the request failed: %v", err)
	}
	if err := request.Authenticate(context.Background(), httpNode(), httpRequest); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	response, err := safehttp.NewClient(policy).Do(httpRequest)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer response.Body.Close()

	// The chain stops at the redirect: the last in-scope response is returned
	// rather than the secret following the hop.
	if response.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want the in-scope redirect response", response.StatusCode)
	}
	if leaked != "" {
		t.Errorf("the destination received the credential header %q", leaked)
	}
}

// localHostPort rewrites a 127.0.0.1 URL to the same port on localhost, so the
// redirect crosses a hostname boundary while staying on the test machine.
func localHostPort(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("parsing %q failed: %v", rawURL, err)
	}
	return "localhost:" + parsed.URL.Port()
}

// countingCredentials resolves by ID and counts what it was asked for, so a
// test can prove a refusal happened before the secret was ever looked up.
type countingCredentials struct {
	byID  map[string]engine.Credential
	calls int
}

func (resolver *countingCredentials) ResolveCredential(_ context.Context, credentialID string) (engine.Credential, error) {
	resolver.calls++
	credential, found := resolver.byID[credentialID]
	if !found {
		return engine.Credential{}, errors.New("no credential " + credentialID)
	}
	return credential, nil
}

// twoCredentialNode attaches one credential of each of two types, which is what
// the type-keyed lookup exists for.
func twoCredentialNode() workflow.IRNode {
	return workflow.IRNode{Name: "Fetch", Credentials: map[string]string{
		"httpHeaderAuth": "cred_header",
		"httpBearerAuth": "cred_bearer",
	}}
}

func twoCredentialResolver() *countingCredentials {
	return &countingCredentials{byID: map[string]engine.Credential{
		"cred_header": {
			ID: "cred_header", Name: "Header key", Type: "httpHeaderAuth",
			Fields: map[string]string{"name": "X-Api-Key", "value": "header-secret"},
		},
		"cred_bearer": {
			ID: "cred_bearer", Name: "Bearer token", Type: "httpBearerAuth",
			Fields: map[string]string{"token": "bearer-secret"},
		},
	}}
}

// TestAuthenticateAsAppliesOnlyTheNamedCredential is the pack seam's core
// promise: a pack names a credential type and the host applies that one, not
// whichever credential the node happened to attach first.
func TestAuthenticateAsAppliesOnlyTheNamedCredential(t *testing.T) {
	t.Parallel()

	resolver := twoCredentialResolver()
	request := engine.Request{Credentials: resolver}
	httpRequest, err := http.NewRequest(http.MethodGet, "https://api.example.test/v1/items", nil)
	if err != nil {
		t.Fatalf("building the request failed: %v", err)
	}
	if err := request.AuthenticateAs(context.Background(), twoCredentialNode(), "httpBearerAuth", httpRequest); err != nil {
		t.Fatalf("AuthenticateAs() error = %v", err)
	}
	if got := httpRequest.Header.Get("Authorization"); got != "Bearer bearer-secret" {
		t.Errorf("Authorization = %q, want the named credential's", got)
	}
	if got := httpRequest.Header.Get("X-Api-Key"); got != "" {
		t.Errorf("X-Api-Key = %q, want the credential that was not named left alone", got)
	}
	if resolver.calls != 1 {
		t.Errorf("the resolver was called %d times, want once for the named credential", resolver.calls)
	}
}

// TestAuthenticateAsRefusesATypeThatIsNotAttached proves naming a credential
// the node does not carry is a refusal, not a request sent anonymously.
func TestAuthenticateAsRefusesATypeThatIsNotAttached(t *testing.T) {
	t.Parallel()

	resolver := twoCredentialResolver()
	request := engine.Request{Credentials: resolver}
	node := workflow.IRNode{Name: "Fetch", Credentials: map[string]string{"httpHeaderAuth": "cred_header"}}
	httpRequest, err := http.NewRequest(http.MethodGet, "https://api.example.test/v1/items", nil)
	if err != nil {
		t.Fatalf("building the request failed: %v", err)
	}
	if err := request.AuthenticateAs(context.Background(), node, "httpBearerAuth", httpRequest); err == nil {
		t.Fatal("AuthenticateAs() applied a credential type the node does not carry")
	}
	if resolver.calls != 0 {
		t.Errorf("the resolver was called %d times, want none: the type is not attached", resolver.calls)
	}
	if len(httpRequest.Header) != 0 {
		t.Errorf("headers = %v, want a request with nothing applied", httpRequest.Header)
	}
}

// TestAuthenticateAsHonoursTheCredentialDomainScope proves the domain bound
// applies to the named credential exactly as it does to Authenticate's: the
// secret is not applied to a host AllowedDomains never named, and the scope
// rides on the request for the redirect chain.
func TestAuthenticateAsHonoursTheCredentialDomainScope(t *testing.T) {
	t.Parallel()

	resolver := twoCredentialResolver()
	resolver.byID["cred_bearer"] = engine.Credential{
		ID: "cred_bearer", Name: "Bearer token", Type: "httpBearerAuth",
		Fields:         map[string]string{"token": "bearer-secret"},
		AllowedDomains: []string{"api.example.test"},
	}
	request := engine.Request{Credentials: resolver}

	refused, err := http.NewRequest(http.MethodGet, "https://evil.example.test/v1/items", nil)
	if err != nil {
		t.Fatalf("building the request failed: %v", err)
	}
	if err := request.AuthenticateAs(context.Background(), twoCredentialNode(), "httpBearerAuth", refused); err == nil {
		t.Fatal("AuthenticateAs() applied the credential to a host outside AllowedDomains")
	}
	if got := refused.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want no secret on a refused host", got)
	}

	allowed, err := http.NewRequest(http.MethodGet, "https://api.example.test/v1/items", nil)
	if err != nil {
		t.Fatalf("building the request failed: %v", err)
	}
	if err := request.AuthenticateAs(context.Background(), twoCredentialNode(), "httpBearerAuth", allowed); err != nil {
		t.Fatalf("AuthenticateAs() error = %v", err)
	}
	scope, carried := safehttp.CredentialScopeFrom(allowed.Context())
	if !carried {
		t.Fatal("no credential scope on the request context: a redirect would carry the secret to any host")
	}
	if !scope.AllowsHost("api.example.test:443") || scope.AllowsHost("evil.example.test") {
		t.Error("the scope does not match the credential's AllowedDomains")
	}
}
