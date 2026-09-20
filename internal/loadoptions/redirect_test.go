package loadoptions_test

// Regression proof for the same leak on the other call site: a field's option
// loader checked the credential's AllowedDomains against the URL it was given,
// then followed a redirect anywhere the egress policy allowed — carrying the
// credential's secret to a host the credential itself never named.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/loadoptions"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
)

// twoNamePolicy allows the test servers under both spellings of loopback, so
// nothing but the credential's own scope can stop the redirect.
func twoNamePolicy() safehttp.Policy {
	return safehttp.Policy{
		AllowPrivateNetworks: true,
		AllowedHosts:         []string{"127.0.0.1", "localhost"},
		MaxResponseBytes:     1 << 20,
		MaxRedirects:         5,
		Timeout:              5 * time.Second,
	}
}

// outsideName is the same loopback server under a name the credential's
// AllowedDomains does not contain: AllowedDomains compares hostnames, so
// "localhost" and "127.0.0.1" are two different hosts to it even though they
// are one socket.
func outsideName(t *testing.T, serverURL string) string {
	t.Helper()
	if !strings.Contains(serverURL, "127.0.0.1") {
		t.Fatalf("test server URL %q does not name 127.0.0.1", serverURL)
	}
	return strings.Replace(serverURL, "127.0.0.1", "localhost", 1)
}

func TestOptionLoaderDoesNotFollowARedirectOffItsCredentialsDomains(t *testing.T) {
	var leaked http.Header
	outsider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Clone()
		_, _ = w.Write([]byte(`[{"id":"stolen"}]`))
	}))
	defer outsider.Close()

	insider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, outsideName(t, outsider.URL)+"/api/sessions", http.StatusFound)
	}))
	defer insider.Close()

	resolver := loadoptions.NewResolver(twoNamePolicy(), time.Minute)
	credential := &fakeCredentials{
		id: "cred_1",
		record: credentials.Record{
			ID: "cred_1", Type: "httpHeaderAuth",
			AllowedDomains: []string{"127.0.0.1"},
		},
		fields: map[string]string{"name": "X-Api-Key", "value": "SCOPED-SECRET"},
	}

	_, err := resolver.Load(context.Background(), property.OptionsLoader{
		Source: property.LoaderHTTP, Method: http.MethodGet,
		Endpoint:       insider.URL + "/api/sessions",
		CredentialType: "httpHeaderAuth",
		ValueField:     "id",
	}, loadoptions.Scope{TenantID: "t1"}, "cred_1", credential)

	// The loader is expected to fail or to return nothing: what it must never
	// do is hand the secret to the host the redirect named.
	if leaked != nil {
		t.Errorf("the option loader delivered the credential to a host outside its domains: %v (err: %v)", leaked, err)
	}
}

func TestOptionLoaderStillFollowsARedirectInsideItsCredentialsDomains(t *testing.T) {
	reached := false
	insider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sessions" {
			http.Redirect(w, r, "/v2/sessions", http.StatusFound)
			return
		}
		reached = true
		_, _ = w.Write([]byte(`[{"id":"default"}]`))
	}))
	defer insider.Close()

	resolver := loadoptions.NewResolver(twoNamePolicy(), time.Minute)
	credential := &fakeCredentials{
		id:     "cred_2",
		record: credentials.Record{ID: "cred_2", Type: "httpHeaderAuth", AllowedDomains: []string{"127.0.0.1"}},
		fields: map[string]string{"name": "X-Api-Key", "value": "k"},
	}

	result, err := resolver.Load(context.Background(), property.OptionsLoader{
		Source: property.LoaderHTTP, Method: http.MethodGet,
		Endpoint:       insider.URL + "/api/sessions",
		CredentialType: "httpHeaderAuth",
		ValueField:     "id",
	}, loadoptions.Scope{TenantID: "t1"}, "cred_2", credential)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// A service that redirects its own endpoint — a canonical path, or an
	// http-to-https hop — has to keep working, so the scope is a bound rather
	// than a ban on redirects.
	if !reached || len(result.Options) != 1 || result.Options[0].Value != "default" {
		t.Errorf("Load() = %#v (reached: %t), want the in-scope redirect followed", result, reached)
	}
}
