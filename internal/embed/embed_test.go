package embed_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/embed"
)

func testKey() []byte {
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 7)
	}
	return key
}

func newIssuer(t *testing.T, now func() time.Time, origins ...string) *embed.Issuer {
	t.Helper()
	if len(origins) == 0 {
		origins = []string{"https://host.example"}
	}
	issuer, err := embed.NewIssuer(testKey(), origins, now)
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}
	return issuer
}

func validRequest() embed.Request {
	return embed.Request{
		TenantID: "tenant-a", WorkflowID: "wf-1",
		Scopes: []embed.Scope{embed.ScopeRead, embed.ScopeWrite},
		Origin: "https://host.example",
	}
}

func TestNewIssuerRequiresAStrongKey(t *testing.T) {
	t.Parallel()

	for _, size := range []int{0, 16, 31} {
		if _, err := embed.NewIssuer(make([]byte, size), nil, nil); err == nil {
			t.Errorf("NewIssuer() accepted a %d-byte key", size)
		}
	}
}

func TestIssueAndVerifyRoundTripASession(t *testing.T) {
	t.Parallel()

	issuer := newIssuer(t, nil)
	session, token, err := issuer.Issue(validRequest())
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if session.ExpiresAt.Sub(session.IssuedAt) != embed.DefaultLifetime {
		t.Errorf("lifetime = %s, want the default", session.ExpiresAt.Sub(session.IssuedAt))
	}

	verified, err := issuer.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if verified.WorkflowID != "wf-1" || verified.TenantID != "tenant-a" || verified.Origin != "https://host.example" {
		t.Fatalf("verified = %#v, want the issued session", verified)
	}
}

func TestOriginAllowlistIsEnforcedAtSessionCreation(t *testing.T) {
	t.Parallel()

	issuer := newIssuer(t, nil, "https://host.example")

	for name, origin := range map[string]string{
		"different host": "https://evil.example",
		"subdomain":      "https://sub.host.example",
		"suffix trick":   "https://host.example.evil.example",
		"wrong scheme":   "http://host.example",
		"empty":          "",
	} {
		request := validRequest()
		request.Origin = origin
		if _, _, err := issuer.Issue(request); err == nil {
			t.Errorf("%s origin %q was allowed", name, origin)
		}
	}
}

func TestAnEmptyAllowlistAllowsNothing(t *testing.T) {
	t.Parallel()

	// Defaulting to "everything" would silently publish the editor to any site
	// that framed it.
	issuer := newIssuer(t, nil, " ")
	if issuer.OriginAllowed("https://host.example") {
		t.Error("an empty allowlist allowed an origin")
	}
	if _, _, err := issuer.Issue(validRequest()); err == nil {
		t.Error("a session was minted with no configured origins")
	}
}

func TestMatchesOriginIsExactWithNoWildcard(t *testing.T) {
	t.Parallel()

	issuer := newIssuer(t, nil)
	session, _, err := issuer.Issue(validRequest())
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	if !session.MatchesOrigin("https://host.example") {
		t.Error("the issuing origin was rejected")
	}
	// "Trust anything under this domain" is the loophole a subdomain takeover
	// walks through.
	for _, origin := range []string{
		"https://sub.host.example", "https://host.example.evil.example",
		"http://host.example", "*", "null", "",
	} {
		if session.MatchesOrigin(origin) {
			t.Errorf("origin %q was accepted", origin)
		}
	}
}

func TestVerifyRejectsAForgedOrTamperedToken(t *testing.T) {
	t.Parallel()

	issuer := newIssuer(t, nil)
	_, token, err := issuer.Issue(validRequest())
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	parts := strings.Split(token, ".")
	tampered := parts[0] + "." + parts[1] + ".AAAA"
	if _, err := issuer.Verify(tampered); !errors.Is(err, embed.ErrInvalidSession) {
		t.Errorf("a tampered signature verified: %v", err)
	}

	// A token signed with another key must not verify here.
	otherKey := testKey()
	otherKey[0] ^= 0xff
	other, _ := embed.NewIssuer(otherKey, []string{"https://host.example"}, nil)
	_, foreign, _ := other.Issue(validRequest())
	if _, err := issuer.Verify(foreign); !errors.Is(err, embed.ErrInvalidSession) {
		t.Errorf("a foreign token verified: %v", err)
	}

	for _, malformed := range []string{"", "nonsense", "kfe1.only-two-parts", "kfe0.aaa.bbb"} {
		if _, err := issuer.Verify(malformed); !errors.Is(err, embed.ErrInvalidSession) {
			t.Errorf("malformed token %q verified", malformed)
		}
	}
}

func TestVerifyRejectsAnExpiredSession(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	clock := &now
	issuer := newIssuer(t, func() time.Time { return *clock })

	request := validRequest()
	request.Lifetime = time.Minute
	_, token, err := issuer.Issue(request)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if _, err := issuer.Verify(token); err != nil {
		t.Fatalf("a fresh token failed to verify: %v", err)
	}

	*clock = now.Add(2 * time.Minute)
	if _, err := issuer.Verify(token); !errors.Is(err, embed.ErrInvalidSession) {
		t.Fatalf("an expired token verified: %v", err)
	}
}

func TestLifetimeIsCapped(t *testing.T) {
	t.Parallel()

	issuer := newIssuer(t, nil)
	request := validRequest()
	request.Lifetime = 24 * time.Hour

	session, _, err := issuer.Issue(request)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	// A token travels through a host page and sits in a browser, so a caller
	// must not be able to ask for a long-lived one.
	if got := session.ExpiresAt.Sub(session.IssuedAt); got != embed.MaxLifetime {
		t.Fatalf("lifetime = %s, want it capped at %s", got, embed.MaxLifetime)
	}
}

func TestScopesAreValidatedAndWriteImpliesRead(t *testing.T) {
	t.Parallel()

	issuer := newIssuer(t, nil)

	request := validRequest()
	request.Scopes = nil
	if _, _, err := issuer.Issue(request); err == nil {
		t.Error("a session with no scopes was minted")
	}

	request.Scopes = []embed.Scope{"workflow:delete"}
	if _, _, err := issuer.Issue(request); err == nil {
		t.Error("an unsupported scope was accepted")
	}

	request.Scopes = []embed.Scope{embed.ScopeWrite}
	session, _, err := issuer.Issue(request)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	// A session that can save must be able to load.
	if !session.Allows(embed.ScopeRead) || !session.Allows(embed.ScopeWrite) {
		t.Errorf("scopes = %#v, want write to imply read", session.Scopes)
	}
	if session.Allows(embed.ScopeRun) {
		t.Error("write implied run, which it must not")
	}
}

func TestBrandingCannotCarryMarkupOrScript(t *testing.T) {
	t.Parallel()

	for name, branding := range map[string]embed.Branding{
		"script in name":  {Name: `<script>alert(1)</script>`},
		"javascript logo": {LogoURL: "javascript:alert(1)"},
		"data logo":       {LogoURL: "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4="},
		"http logo":       {LogoURL: "http://host.example/logo.png"},
		"relative logo":   {LogoURL: "/logo.png"},
		"css escape":      {Accent: "red; background: url(javascript:alert(1))"},
		"expression":      {Accent: "expression(alert(1))"},
	} {
		if err := branding.Validate(); err == nil {
			t.Errorf("%s branding was accepted: %#v", name, branding)
		}
	}

	valid := embed.Branding{Name: "Acme Flows", LogoURL: "https://cdn.example/logo.png", Accent: "#0ea5e9", HideRun: true}
	if err := valid.Validate(); err != nil {
		t.Errorf("valid branding was rejected: %v", err)
	}
}

func TestBrandingCannotOverrideSecurity(t *testing.T) {
	t.Parallel()

	issuer := newIssuer(t, nil)
	request := validRequest()
	request.Scopes = []embed.Scope{embed.ScopeRead}
	request.Branding = embed.Branding{HideRun: false, HideSave: false}

	session, _, err := issuer.Issue(request)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	// Branding is presentation only. Showing a control cannot grant the scope
	// that control would need.
	if session.Allows(embed.ScopeWrite) || session.Allows(embed.ScopeRun) {
		t.Fatalf("branding granted scopes: %#v", session.Scopes)
	}
}

func TestNormalizeOriginRejectsNonHTTPSchemes(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"javascript:alert(1)", "file:///etc/passwd", "data:text/html,x", "null", "*", ""} {
		if got := embed.NormalizeOrigin(raw); got != "" {
			t.Errorf("NormalizeOrigin(%q) = %q, want empty", raw, got)
		}
	}
	if got := embed.NormalizeOrigin("HTTPS://Host.Example:443"); got != "https://host.example:443" {
		t.Errorf("NormalizeOrigin() = %q, want it lowercased", got)
	}
}
