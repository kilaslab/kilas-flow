package engine_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
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
