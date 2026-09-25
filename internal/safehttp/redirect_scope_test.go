package safehttp_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

func loopbackPolicy() safehttp.Policy {
	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	return policy
}

// onLocalhost is the same loopback server under the name `localhost`: one
// socket, but a different host to anything that compares hostnames.
func onLocalhost(serverURL string) string {
	return strings.Replace(serverURL, "127.0.0.1", "localhost", 1)
}

// scopedGet sends one request carrying a header secret under a scope, and
// answers the status the client ended on.
func scopedGet(t *testing.T, target string, scope safehttp.CredentialScope) int {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request = request.WithContext(safehttp.WithCredentialScope(request.Context(), scope))
	request.Header.Set("X-Api-Key", "HEADER-SECRET")
	response, err := safehttp.NewClient(loopbackPolicy()).Do(request)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	_ = response.Body.Close()
	return response.StatusCode
}

// BUG-0bzsa1: a credential that names no domains allows every host, so the
// domain check alone let a redirect carry X-Api-Key to any host at all — Go
// drops only Authorization and Cookie when the host changes. With a credential
// attached and no domains named, a redirect may stay on the first request's
// host and go nowhere else.
func TestAnUnboundedCredentialRedirectStaysOnTheFirstHost(t *testing.T) {
	t.Parallel()

	var leaked, landed string
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("X-Api-Key")
	}))
	defer elsewhere.Close()
	sameHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		landed = r.Header.Get("X-Api-Key")
	}))
	defer sameHost.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/away":
			http.Redirect(w, r, onLocalhost(elsewhere.URL)+"/stolen", http.StatusFound)
		case "/canonical":
			http.Redirect(w, r, "/v2", http.StatusFound)
		case "/port":
			// Another port on the same host is the same host, as it is to
			// the domain check.
			http.Redirect(w, r, sameHost.URL+"/moved", http.StatusFound)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer origin.Close()

	unbounded := safehttp.CredentialScope{AllowsHost: func(string) bool { return true }, Unbounded: true}

	if status := scopedGet(t, origin.URL+"/away", unbounded); status != http.StatusFound || leaked != "" {
		t.Errorf("cross-host hop: status %d, destination saw %q; want the 302 kept and no secret sent", status, leaked)
	}
	if status := scopedGet(t, origin.URL+"/canonical", unbounded); status != http.StatusOK {
		t.Errorf("same-host hop: status %d, want the redirect followed", status)
	}
	if status := scopedGet(t, origin.URL+"/port", unbounded); status != http.StatusOK || landed != "HEADER-SECRET" {
		t.Errorf("same host, other port: status %d, landed %q; want the redirect followed", status, landed)
	}

	// A credential that names its domains is held to those, not to the first
	// host: a hop to another host it names is still followed.
	leaked = ""
	named := safehttp.CredentialScope{AllowsHost: func(host string) bool {
		return strings.HasPrefix(host, "127.0.0.1:") || strings.HasPrefix(host, "localhost:")
	}}
	if status := scopedGet(t, origin.URL+"/away", named); status != http.StatusOK || leaked != "HEADER-SECRET" {
		t.Errorf("scoped cross-host hop to a named host: status %d, saw %q; want it followed", status, leaked)
	}
}

// A credential attached to an https request must not follow a redirect down
// to plain http, where the secret it carries crosses the network in the clear.
// Without a credential the policy's own scheme check is all that applies.
func TestADowngradeIsRefusedWhileACredentialIsAttached(t *testing.T) {
	t.Parallel()

	client := safehttp.NewClient(loopbackPolicy())
	first, err := http.NewRequest(http.MethodGet, "https://api.example.test/start", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	hop := func(ctx context.Context, target string) error {
		next, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			t.Fatalf("NewRequest() error = %v", err)
		}
		return client.CheckRedirect(next, []*http.Request{first})
	}
	scoped := safehttp.WithCredentialScope(context.Background(), safehttp.CredentialScope{
		AllowsHost: func(string) bool { return true },
	})

	if err := hop(scoped, "http://api.example.test/next"); !errors.Is(err, http.ErrUseLastResponse) {
		t.Errorf("https to http with a credential: CheckRedirect() = %v, want the chain stopped", err)
	}
	if err := hop(scoped, "https://api.example.test/next"); err != nil {
		t.Errorf("https to https with a credential: CheckRedirect() = %v, want it followed", err)
	}
	if err := hop(context.Background(), "http://api.example.test/next"); err != nil {
		t.Errorf("https to http without a credential: CheckRedirect() = %v, want it followed", err)
	}

	// A scope without a host rule is still a credential on the request: the
	// downgrade is refused and nothing panics on the missing rule.
	bare := safehttp.WithCredentialScope(context.Background(), safehttp.CredentialScope{})
	if err := hop(bare, "http://api.example.test/next"); !errors.Is(err, http.ErrUseLastResponse) {
		t.Errorf("https to http with a rule-less scope: CheckRedirect() = %v, want the chain stopped", err)
	}
	if err := hop(bare, "https://elsewhere.example.test/next"); err != nil {
		t.Errorf("a rule-less, bounded scope: CheckRedirect() = %v, want no domain check applied", err)
	}
}
