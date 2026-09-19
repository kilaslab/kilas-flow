package safehttp_test

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/safehttp"
)

func TestCheckURLRejectsUnsupportedSchemesAndHostlessTargets(t *testing.T) {
	t.Parallel()

	policy := safehttp.DefaultPolicy()
	for _, raw := range []string{
		"file:///etc/passwd",
		"gopher://api.test/",
		"ftp://api.test/",
		"http:///nohost",
	} {
		target, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if err := policy.CheckURL(target); !errors.Is(err, safehttp.ErrBlocked) {
			t.Errorf("CheckURL(%q) = %v, want ErrBlocked", raw, err)
		}
	}

	target, _ := url.Parse("https://api.test/path")
	if err := policy.CheckURL(target); err != nil {
		t.Errorf("CheckURL(https) = %v, want allowed", err)
	}
}

func TestCheckURLHonoursAnExplicitHostAllowlist(t *testing.T) {
	t.Parallel()

	policy := safehttp.DefaultPolicy()
	policy.AllowedHosts = []string{"api.test", "*.partner.test"}

	for raw, want := range map[string]bool{
		"https://api.test/x":           true,
		"https://eu.partner.test/x":    true,
		"https://partner.test/x":       false,
		"https://api.test.evil.test/x": false,
		"https://evil.test/x":          false,
	} {
		target, _ := url.Parse(raw)
		err := policy.CheckURL(target)
		if allowed := err == nil; allowed != want {
			t.Errorf("CheckURL(%q) allowed = %v, want %v (err: %v)", raw, allowed, want, err)
		}
	}
}

func TestCheckAddressBlocksInternalRanges(t *testing.T) {
	t.Parallel()

	policy := safehttp.DefaultPolicy()
	for _, address := range []string{
		"127.0.0.1",
		"0.0.0.0",
		"10.1.2.3",
		"172.16.0.1",
		"192.168.1.1",
		"169.254.169.254", // cloud metadata
		"100.64.0.1",      // carrier-grade NAT
		"192.0.0.1",
		"240.0.0.1",
		"224.0.0.1", // multicast
		"::1",
		"fd00::1", // unique-local
		"fe80::1", // link-local
		"::ffff:127.0.0.1",
		"::ffff:10.0.0.1",
	} {
		if err := policy.CheckAddress(net.ParseIP(address)); !errors.Is(err, safehttp.ErrBlocked) {
			t.Errorf("CheckAddress(%s) = %v, want ErrBlocked", address, err)
		}
	}

	for _, address := range []string{"93.184.216.34", "8.8.8.8", "2606:2800:220:1:248:1893:25c8:1946"} {
		if err := policy.CheckAddress(net.ParseIP(address)); err != nil {
			t.Errorf("CheckAddress(%s) = %v, want allowed", address, err)
		}
	}
}

func TestCheckAddressCanBeRelaxedForSelfHostedNetworks(t *testing.T) {
	t.Parallel()

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true

	if err := policy.CheckAddress(net.ParseIP("10.0.0.5")); err != nil {
		t.Errorf("CheckAddress with private networks allowed = %v, want nil", err)
	}
}

func TestClientRefusesToDialALoopbackServer(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := safehttp.NewClient(safehttp.DefaultPolicy())
	_, err := client.Get(server.URL)
	if err == nil {
		t.Fatal("the client reached a loopback server")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("error = %v, want the policy rejection", err)
	}
}

func TestClientReachesALoopbackServerWhenPrivateNetworksAreAllowed(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	response, err := safehttp.NewClient(policy).Get(server.URL)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", response.StatusCode)
	}
}

func TestReadBodyStopsAtTheConfiguredLimit(t *testing.T) {
	t.Parallel()

	policy := safehttp.DefaultPolicy()
	policy.MaxResponseBytes = 16

	contents, truncated, err := policy.ReadBody(strings.NewReader(strings.Repeat("a", 100)))
	if err != nil {
		t.Fatalf("ReadBody() error = %v", err)
	}
	if !truncated || len(contents) != 16 {
		t.Errorf("ReadBody() = (%d bytes, truncated %v), want (16, true)", len(contents), truncated)
	}

	small, truncated, err := policy.ReadBody(strings.NewReader("short"))
	if err != nil || truncated || string(small) != "short" {
		t.Errorf("ReadBody(short) = (%q, %v, %v), want the whole body", small, truncated, err)
	}
}

func TestRedirectsAreBoundedAndRechecked(t *testing.T) {
	t.Parallel()

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	policy.MaxRedirects = 1

	hops := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hops++
		http.Redirect(w, &http.Request{}, server.URL, http.StatusFound)
	}))
	defer server.Close()

	_, err := safehttp.NewClient(policy).Get(server.URL)
	if err == nil {
		t.Fatal("an unbounded redirect loop completed")
	}
	if !strings.Contains(err.Error(), "redirects") {
		t.Errorf("error = %v, want the redirect limit", err)
	}
}

func TestRedirectToADisallowedSchemeIsRejected(t *testing.T) {
	t.Parallel()

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	policy.AllowedHosts = []string{"127.0.0.1"}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, "https://elsewhere.test/", http.StatusFound)
	}))
	defer server.Close()

	if _, err := safehttp.NewClient(policy).Get(server.URL); err == nil {
		t.Fatal("a redirect escaped the host allowlist")
	}
}

// endpointOf is the `host:port` a policy entry has to name to admit a server.
func endpointOf(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %q: %v", rawURL, err)
	}
	return parsed.Host
}

// TestOneNamedPrivateEndpointIsReachedWhileEveryOtherOneStaysBlocked is the
// capability. A deployment that runs a model server on loopback has to reach
// it, and the only lever before this was AllowPrivateNetworks — which hands
// every outbound request in the installation the whole internal network to
// solve a problem with one address.
func TestOneNamedPrivateEndpointIsReachedWhileEveryOtherOneStaysBlocked(t *testing.T) {
	t.Parallel()

	named := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer named.Close()

	reached := false
	neighbour := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		_, _ = w.Write([]byte("ok"))
	}))
	defer neighbour.Close()

	policy := safehttp.DefaultPolicy()
	if policy.AllowPrivateNetworks {
		t.Fatal("the default policy allows private networks, which would make this test prove nothing")
	}
	policy.AllowedPrivateEndpoints = []string{endpointOf(t, named.URL)}
	client := safehttp.NewClient(policy)

	response, err := client.Get(named.URL)
	if err != nil {
		t.Fatalf("Get(named endpoint) = %v, want the request to go through", err)
	}
	_ = response.Body.Close()

	// The neighbour is the same loopback host on a different port, which is
	// why the port is part of the grant: an allowance for a model server must
	// not also be an allowance for whatever else the machine is listening on.
	if _, err := client.Get(neighbour.URL); !errors.Is(err, safehttp.ErrBlocked) {
		t.Errorf("Get(unnamed endpoint) = %v, want ErrBlocked", err)
	}
	if reached {
		t.Error("a refused request still reached the neighbouring server")
	}
}

// TestAPrivateEndpointEntryThatIsNotAHostAndPortGrantsNothing pins the failure
// mode. A misconfigured allowance has to narrow the policy, never widen it, and
// the operator has to be told rather than left with a grant that silently does
// nothing.
func TestAPrivateEndpointEntryThatIsNotAHostAndPortGrantsNothing(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse %q: %v", server.URL, err)
	}
	for _, entry := range []string{
		"",
		"   ",
		// A host with no port would be a grant over every service on the box.
		parsed.Hostname(),
		// AllowedHosts honours this shape, which is exactly why an operator
		// might reach for it here.
		"*.test:" + parsed.Port(),
		"*:" + parsed.Port(),
		parsed.Hostname() + ":not-a-port",
		parsed.Hostname() + ":0",
		parsed.Hostname() + ":70000",
		// A pasted URL rather than an endpoint.
		server.URL,
		"http://" + parsed.Host + "/v1",
	} {
		if err := safehttp.CheckPrivateEndpoint(entry); err == nil {
			t.Errorf("CheckPrivateEndpoint(%q) = nil, want a refusal", entry)
		}
		policy := safehttp.DefaultPolicy()
		policy.AllowedPrivateEndpoints = []string{entry}
		if _, err := safehttp.NewClient(policy).Get(server.URL); !errors.Is(err, safehttp.ErrBlocked) {
			t.Errorf("entry %q admitted %s: %v", entry, server.URL, err)
		}
	}

	for _, entry := range []string{"127.0.0.1:11434", "[::1]:11434", "localhost:80", "ollama.internal:11434"} {
		if err := safehttp.CheckPrivateEndpoint(entry); err != nil {
			t.Errorf("CheckPrivateEndpoint(%q) = %v, want it accepted", entry, err)
		}
	}
}

// TestAPrivateEndpointAllowanceIsNotAlsoAHostAllowlistEntry keeps the two
// guards independent in the direction that matters. Widening one of them by
// writing in the other is how an allowlist stops meaning what it says.
func TestAPrivateEndpointAllowanceIsNotAlsoAHostAllowlistEntry(t *testing.T) {
	t.Parallel()

	policy := safehttp.DefaultPolicy()
	policy.AllowedHosts = []string{"api.test"}
	policy.AllowedPrivateEndpoints = []string{"127.0.0.1:11434"}

	target, err := url.Parse("http://127.0.0.1:11434/v1/models")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := policy.CheckURL(target); !errors.Is(err, safehttp.ErrBlocked) {
		t.Errorf("CheckURL(allowed private endpoint) = %v, want the host allowlist to still refuse it", err)
	}
}

// TestAnAllowedPrivateEndpointDoesNotCarryARedirectSomewhereElse closes the
// obvious way to turn one grant into many: reach the endpoint you are allowed
// to reach and have it point you at the one you are not.
func TestAnAllowedPrivateEndpointDoesNotCarryARedirectSomewhereElse(t *testing.T) {
	t.Parallel()

	reached := false
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		_, _ = w.Write([]byte("secrets"))
	}))
	defer internal.Close()

	named := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer named.Close()

	policy := safehttp.DefaultPolicy()
	policy.AllowedPrivateEndpoints = []string{endpointOf(t, named.URL)}

	if _, err := safehttp.NewClient(policy).Get(named.URL); !errors.Is(err, safehttp.ErrBlocked) {
		t.Errorf("Get(redirecting endpoint) = %v, want ErrBlocked on the hop", err)
	}
	if reached {
		t.Error("a redirect carried the allowance to an endpoint nobody named")
	}
}

// TestCheckEndpointAddressExemptsOnlyTheEndpointItWasAskedAbout is the unit
// underneath the client tests: the exemption is keyed on the host and port that
// were dialled, so it cannot leak to a different one.
func TestCheckEndpointAddressExemptsOnlyTheEndpointItWasAskedAbout(t *testing.T) {
	t.Parallel()

	policy := safehttp.DefaultPolicy()
	policy.AllowedPrivateEndpoints = []string{"localhost:11434", "[::1]:11434"}

	if err := policy.CheckEndpointAddress("localhost", "11434", net.ParseIP("127.0.0.1")); err != nil {
		t.Errorf("CheckEndpointAddress(named endpoint) = %v, want it admitted", err)
	}
	// Spelt with brackets in the entry and without them at the dial, and still
	// one grant rather than two near-misses.
	if err := policy.CheckEndpointAddress("::1", "11434", net.ParseIP("::1")); err != nil {
		t.Errorf("CheckEndpointAddress(IPv6 endpoint) = %v, want it admitted", err)
	}
	for _, refused := range []struct{ host, port, ip string }{
		// The same host on another port: a database beside the model server.
		{"localhost", "5432", "127.0.0.1"},
		// The same port on a host the entry never named.
		{"metadata.internal", "11434", "169.254.169.254"},
		// The address the named host resolves to, written directly. The
		// comparison is on the host as the URL wrote it, so this is a
		// different endpoint and gets no exemption.
		{"127.0.0.1", "11434", "127.0.0.1"},
	} {
		err := policy.CheckEndpointAddress(refused.host, refused.port, net.ParseIP(refused.ip))
		if !errors.Is(err, safehttp.ErrBlocked) {
			t.Errorf("CheckEndpointAddress(%s:%s -> %s) = %v, want ErrBlocked",
				refused.host, refused.port, refused.ip, err)
		}
	}
	if err := policy.CheckEndpointAddress("localhost", "11434", nil); !errors.Is(err, safehttp.ErrBlocked) {
		t.Errorf("CheckEndpointAddress(unparsed address) = %v, want ErrBlocked even for a named endpoint", err)
	}
	// A public address needs no exemption and must not be refused for lacking one.
	if err := policy.CheckEndpointAddress("api.test", "443", net.ParseIP("93.184.216.34")); err != nil {
		t.Errorf("CheckEndpointAddress(public address) = %v, want allowed", err)
	}
}

// A credential's domain scope must survive redirects: Go strips only
// Authorization/Cookie on cross-host hops, so a scoped header secret would
// otherwise follow a 30x to a host its AllowedDomains never named.
// Regression test for the redirect secret-leak finding.
func TestCredentialScopeStopsARedirectOutsideItsDomains(t *testing.T) {
	t.Parallel()

	var leaked http.Header
	outsider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Clone()
		_, _ = w.Write([]byte("exfiltrated"))
	}))
	defer outsider.Close()

	insider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, outsider.URL+"/leaked", http.StatusFound)
	}))
	defer insider.Close()

	// Both httptest servers share 127.0.0.1, so the scope is port-aware the
	// way AllowedDomains matching is host-aware in production: the insider
	// port is in scope, the outsider port is not.
	insiderEndpoint := endpointOf(t, insider.URL)
	outsiderEndpoint := endpointOf(t, outsider.URL)
	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true

	// Without a scope the redirect is followed: the policy alone admits
	// both loopback servers, which is the baseline this test tightens.
	unscoped, err := safehttp.NewClient(policy).Get(insider.URL)
	if err != nil {
		t.Fatalf("Get(unscoped redirect) error = %v, want the hop followed", err)
	}
	_ = unscoped.Body.Close()
	if leaked == nil {
		t.Fatal("the unscoped redirect never reached the outsider")
	}

	// With a scope naming only the insider, the chain stops with the last
	// in-scope response instead of carrying the secret outward.
	leaked = nil
	_ = outsiderEndpoint
	scope := safehttp.CredentialScope{AllowsHost: func(host string) bool { return host == insiderEndpoint }}
	request, err := http.NewRequest("GET", insider.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	request = request.WithContext(safehttp.WithCredentialScope(request.Context(), scope))
	request.Header.Set("X-Api-Key", "SCOPED-SECRET")
	response, err := safehttp.NewClient(policy).Do(request)
	if err != nil {
		t.Fatalf("Do(scoped redirect) error = %v, want the last in-scope response", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Errorf("Do(scoped redirect) status = %d, want 302 (the un-followed hop)", response.StatusCode)
	}
	if leaked != nil {
		t.Error("the scoped secret reached a host outside its domains")
	}
}

// Tenant-authored egress must not honour proxy environment variables: with
// ProxyFromEnvironment the dialer validates only the proxy's address while
// the real target (here, a blocked host) is never resolved or checked.
func TestClientIgnoresProxyEnvironment(t *testing.T) {
	proxied := false
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied = true
		_, _ = w.Write([]byte("via proxy"))
	}))
	defer proxy.Close()

	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("http_proxy", proxy.URL)
	t.Setenv("https_proxy", proxy.URL)

	policy := safehttp.DefaultPolicy()
	policy.AllowedHosts = []string{"api.test"}
	if _, err := safehttp.NewClient(policy).Get("http://api.test/"); err == nil {
		t.Error("Get through a proxy env = nil, want the dial to fail without a proxy")
	}
	if proxied {
		t.Error("the request went through the proxy named by the environment")
	}
}

func mustHostname(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %q: %v", rawURL, err)
	}
	return parsed.Hostname()
}
