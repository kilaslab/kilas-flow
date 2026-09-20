package credentials_test

// Regression proof for the credential-probe redirect leak: the credential's
// AllowedDomains bounded the URL its test named, but not where a 30x from that
// URL pointed, so a scoped credential could be walked off its domains and have
// its header or query secret delivered to whoever answered.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
)

// probeFields is the one secret a probe type needs in order to sign a request.
func probeFields() []property.PropertyDefinition {
	return []property.PropertyDefinition{
		{Key: "token", Label: "Token", Kind: property.KindString, Required: true},
	}
}

// outsideName is a second hostname for the same loopback server.
//
// The two test servers share 127.0.0.1, and AllowedDomains matches hostnames,
// so a second spelling of the same address is what makes "a host this
// credential never named" expressible on one machine: localhost is not
// 127.0.0.1 to a hostname comparison, and a request to it lands on exactly the
// same socket.
func outsideName(t *testing.T, serverURL string) string {
	t.Helper()
	if !strings.Contains(serverURL, "127.0.0.1") {
		t.Fatalf("test server URL %q does not name 127.0.0.1", serverURL)
	}
	return strings.Replace(serverURL, "127.0.0.1", "localhost", 1)
}

func TestCredentialProbeDoesNotFollowARedirectOffItsDomains(t *testing.T) {
	t.Parallel()

	var leaked http.Header
	outsider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Clone()
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer outsider.Close()

	insider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, outsideName(t, outsider.URL)+"/stolen", http.StatusFound)
	}))
	defer insider.Close()

	credentialType := credentials.Type{
		ID: "redirectProbe", DisplayName: "Redirect probe",
		Properties: probeFields(),
		Secrets:    []string{"token"},
		Authenticate: &credentials.Authentication{
			Placement: credentials.PlacementHeader,
			Name:      "X-Api-Key", Value: "{{ token }}",
		},
		Test: &credentials.TestRequest{URL: insider.URL + "/probe"},
	}
	record := credentials.Record{
		ID: "cred-1", Type: "redirectProbe",
		AllowedDomains: []string{"127.0.0.1"},
	}

	// Both loopback names are allowed by the egress policy, so the only thing
	// that can stop the hop is the credential's own scope. Without that, this
	// is the leak the finding describes.
	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	policy.AllowedHosts = []string{"127.0.0.1", "localhost"}

	if _, err := credentials.RunTest(context.Background(), credentialType, record,
		map[string]string{"token": "SCOPED-SECRET"}, policy); err != nil {
		t.Fatalf("RunTest() error = %v", err)
	}
	if leaked != nil {
		t.Errorf("the probe followed the redirect and delivered the credential to a host outside its domains: %v", leaked)
	}
}

func TestCredentialProbeStillFollowsARedirectInsideItsDomains(t *testing.T) {
	t.Parallel()

	reached := false
	insider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/probe" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		reached = true
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer insider.Close()

	credentialType := credentials.Type{
		ID: "sameHostProbe", DisplayName: "Same host probe",
		Properties: probeFields(),
		Test:       &credentials.TestRequest{URL: insider.URL + "/probe"},
	}
	record := credentials.Record{ID: "cred-2", Type: "sameHostProbe", AllowedDomains: []string{"127.0.0.1"}}

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	policy.AllowedHosts = []string{"127.0.0.1"}

	detail, err := credentials.RunTest(context.Background(), credentialType, record, map[string]string{"token": "t"}, policy)
	if err != nil {
		t.Fatalf("RunTest() error = %v", err)
	}
	// A service that redirects its own probe URL — http to a canonical path, or
	// a version bump — must still be testable, so the scope has to be a bound
	// and not a ban on redirects.
	if !reached {
		t.Errorf("the probe did not follow an in-scope redirect (detail: %s)", detail)
	}
}

func TestCredentialProbeRefusesAnOutOfScopeHostBeforeAnyRequest(t *testing.T) {
	t.Parallel()

	credentialType := credentials.Type{
		ID: "scopedProbe", DisplayName: "Scoped probe",
		Properties: probeFields(),
		Test:       &credentials.TestRequest{URL: "https://api.example.test/models"},
	}
	record := credentials.Record{ID: "cred-3", Type: "scopedProbe", AllowedDomains: []string{"other.example.test"}}

	policy := safehttp.DefaultPolicy()
	policy.Timeout = time.Second
	if _, err := credentials.RunTest(context.Background(), credentialType, record, map[string]string{"token": "t"}, policy); err == nil {
		t.Fatal("a credential probed a host its domains never named")
	}
}
