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
