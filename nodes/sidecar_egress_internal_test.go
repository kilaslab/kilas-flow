package nodes

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/sidecar"
)

// These tests call the egress handler directly. They are the policy's own unit
// tests: the end-to-end proof that a real package's request reaches the host
// and nowhere else is in sidecar_egress_test.go, which runs the real runner.
//
// Every one of them grants the loopback test server through
// Policy.AllowedPrivateEndpoints — the narrow mechanism — and never by setting
// AllowPrivateNetworks.

// loopbackPolicy grants exactly one test server and keeps every other guard.
func loopbackPolicy(servers ...*httptest.Server) safehttp.Policy {
	policy := safehttp.DefaultPolicy()
	for _, server := range servers {
		policy.AllowedPrivateEndpoints = append(policy.AllowedPrivateEndpoints, strings.TrimPrefix(server.URL, "http://"))
	}
	return policy
}

func egressFor(policy safehttp.Policy, held ...engine.Credential) *sidecarEgress {
	return newSidecarEgress(policy, held, newScrubber(held), slog.New(slog.NewTextHandler(io.Discard, nil)), "tenant-a", "Fixture Relay")
}

func heldCredential(name string, allowedDomains ...string) engine.Credential {
	return engine.Credential{ID: name, Name: name, Type: "fixtureApi", AllowedDomains: allowedDomains,
		Fields: map[string]string{"apiKey": "secret-" + name}}
}

// TestSidecarEgressRequiresEveryHeldCredentialToAllowTheHost is the
// intersection rule: the host cannot know which secret the package is about to
// send, so a run holding one credential scoped elsewhere may not reach a host
// that credential is not scoped for.
func TestSidecarEgressRequiresEveryHeldCredentialToAllowTheHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	policy := loopbackPolicy(server)

	_, err := egressFor(policy, heldCredential("scoped", "api.example.com"), heldCredential("unscoped")).HTTP(
		context.Background(), sidecar.HTTPRequest{Method: "GET", URL: server.URL})
	if err == nil {
		t.Fatal("HTTP() error = nil, want the call refused while one held credential is scoped elsewhere")
	}
	if !strings.Contains(err.Error(), "scoped") {
		t.Errorf("error = %v, want it to name the credential that refused", err)
	}

	allowed, err := egressFor(policy, heldCredential("scoped", "127.0.0.1"), heldCredential("unscoped")).HTTP(
		context.Background(), sidecar.HTTPRequest{Method: "GET", URL: server.URL})
	if err != nil {
		t.Fatalf("HTTP() error = %v, want the call allowed when every credential permits the host", err)
	}
	if allowed.Status != http.StatusOK {
		t.Errorf("status = %d, want 200", allowed.Status)
	}
}

// TestSidecarEgressRefusesAHostOutsideTheCredentialsAllowedDomains is the same
// rule with one credential: a scope that names another API refuses this host
// even though the network path is open.
func TestSidecarEgressRefusesAHostOutsideTheCredentialsAllowedDomains(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	policy := loopbackPolicy(server)

	if _, err := egressFor(policy, heldCredential("api", "api.example.com")).HTTP(
		context.Background(), sidecar.HTTPRequest{URL: server.URL}); err == nil {
		t.Fatal("HTTP() error = nil, want a host outside the credential's domains refused")
	}
	if _, err := egressFor(policy, heldCredential("api", "127.0.0.1")).HTTP(
		context.Background(), sidecar.HTTPRequest{URL: server.URL}); err != nil {
		t.Fatalf("HTTP() error = %v, want the credential's own host allowed", err)
	}
}

// TestSidecarEgressStopsARedirectOutsideTheScope proves the scope is attached
// to the request context, so safehttp's own redirect check stops the chain
// rather than following it with the secret in hand.
//
// The redirect target is granted to the *dial* guard on purpose: if the scope
// check were missing, the request would be made, so the target's hit count is
// what distinguishes the two.
func TestSidecarEgressStopsARedirectOutsideTheScope(t *testing.T) {
	var hits int
	var mu sync.Mutex
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		_, _ = writer.Write([]byte(`{"reached":true}`))
	}))
	defer target.Close()

	// The same server, addressed by a different host name: the credential
	// allows 127.0.0.1 and not localhost, so the hop leaves its scope.
	redirector := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		location := "http://localhost:" + portOf(t, target.URL) + request.URL.Path
		http.Redirect(writer, request, location, http.StatusFound)
	}))
	defer redirector.Close()

	policy := loopbackPolicy(redirector, target)
	policy.AllowedPrivateEndpoints = append(policy.AllowedPrivateEndpoints, "localhost:"+portOf(t, target.URL))

	response, err := egressFor(policy, heldCredential("api", "127.0.0.1")).HTTP(
		context.Background(), sidecar.HTTPRequest{URL: redirector.URL})
	if err != nil {
		t.Fatalf("HTTP() error = %v, want the in-scope response returned", err)
	}
	if response.Status != http.StatusFound {
		t.Errorf("status = %d, want the redirect itself: the chain stops at a hop outside the scope", response.Status)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Errorf("the redirect target was reached %d times, want 0", hits)
	}
}

// TestSidecarEgressStripsHopByHopHeaders covers the headers that describe this
// connection rather than the request: forwarding them lets a package change how
// the host's client behaves.
func TestSidecarEgressStripsHopByHopHeaders(t *testing.T) {
	received := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		for name, values := range request.Header {
			received[strings.ToLower(name)] = strings.Join(values, ", ")
		}
		writer.Header().Set("X-Answer", "yes")
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	response, err := egressFor(loopbackPolicy(server)).HTTP(context.Background(), sidecar.HTTPRequest{
		Method: "GET", URL: server.URL,
		Headers: map[string]string{
			"Connection": "keep-alive", "Keep-Alive": "timeout=5", "Upgrade": "websocket",
			"X-Custom": "kept",
		},
	})
	if err != nil {
		t.Fatalf("HTTP() error = %v", err)
	}
	for _, stripped := range []string{"keep-alive", "upgrade"} {
		if _, present := received[stripped]; present {
			t.Errorf("the server received %q, which is a hop-by-hop header", stripped)
		}
	}
	if received["x-custom"] != "kept" {
		t.Errorf("x-custom = %q, want the package's own header forwarded", received["x-custom"])
	}
	// Response headers arrive lower-cased as one string per name: the child
	// hands them to the package as a plain object.
	if got := response.Headers["x-answer"]; got != "yes" {
		t.Errorf("response headers = %v, want a lower-cased x-answer", response.Headers)
	}
}

// TestSidecarEgressCapsTheResponseBody is the output bound: a package cannot
// make the host buffer an unbounded answer.
func TestSidecarEgressCapsTheResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(strings.Repeat("x", 4096)))
	}))
	defer server.Close()

	policy := loopbackPolicy(server)
	policy.MaxResponseBytes = 64
	_, err := egressFor(policy).HTTP(context.Background(), sidecar.HTTPRequest{URL: server.URL})
	if err == nil {
		t.Fatal("HTTP() error = nil, want an oversized answer refused")
	}
	var coded sidecar.HostCallCoder
	if !errors.As(err, &coded) {
		t.Fatalf("error = %v, want one the child can read a code from", err)
	}
	if code, _ := coded.HostCallCode(); code != "response-too-large" {
		t.Errorf("code = %q, want response-too-large", code)
	}
}

// TestSidecarEgressHonoursAllowedHosts is the deployment's own allowlist,
// applied before the dial.
func TestSidecarEgressHonoursAllowedHosts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	policy := loopbackPolicy(server)
	policy.AllowedHosts = []string{"api.example.com"}
	if _, err := egressFor(policy).HTTP(context.Background(), sidecar.HTTPRequest{URL: server.URL}); err == nil {
		t.Fatal("HTTP() error = nil, want a host outside AllowedHosts refused")
	}

	policy.AllowedHosts = []string{"127.0.0.1"}
	if _, err := egressFor(policy).HTTP(context.Background(), sidecar.HTTPRequest{URL: server.URL}); err != nil {
		t.Fatalf("HTTP() error = %v, want the allowed host reachable", err)
	}
}

// TestSidecarEgressRefusesAnUnusableURL keeps a malformed target from reaching
// the client as a nil-dereference or, worse, a request to nowhere.
func TestSidecarEgressRefusesAnUnusableURL(t *testing.T) {
	for _, target := range []string{"", "not-a-url", "/relative", "file:///etc/passwd"} {
		if _, err := egressFor(safehttp.DefaultPolicy()).HTTP(context.Background(), sidecar.HTTPRequest{URL: target}); err == nil {
			t.Errorf("HTTP(%q) error = nil, want it refused", target)
		}
	}
}

// TestSidecarEgressScrubsTheRefusalMessage: a package that puts its key in a
// URL has it echoed by the refusal, and a diagnostic is not a reason to move a
// credential into a log.
func TestSidecarEgressScrubsTheRefusalMessage(t *testing.T) {
	credential := heldCredential("api", "api.example.com")
	policy := safehttp.DefaultPolicy()
	policy.AllowedHosts = []string{"api.example.com"}

	_, err := egressFor(policy, credential).HTTP(context.Background(),
		sidecar.HTTPRequest{URL: "https://elsewhere.invalid/" + credential.Fields["apiKey"]})
	if err == nil {
		t.Fatal("HTTP() error = nil, want the call refused")
	}
	if strings.Contains(err.Error(), credential.Fields["apiKey"]) {
		t.Errorf("error = %v, want the credential value withheld", err)
	}
}

func portOf(t *testing.T, rawURL string) string {
	t.Helper()
	trimmed := strings.TrimPrefix(rawURL, "http://")
	_, port, found := strings.Cut(trimmed, ":")
	if !found {
		t.Fatalf("no port in %q", rawURL)
	}
	return port
}
