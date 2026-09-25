package credentials_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/credentials"
)

func TestOAuthStateRoundTripsAndRejectsTampering(t *testing.T) {
	t.Parallel()
	secret := []byte("oauth-signing-key-32-bytes-long!!")
	nonce, err := credentials.NewOAuthNonce()
	if err != nil {
		t.Fatalf("NewOAuthNonce() error = %v", err)
	}
	state, err := credentials.SignOAuthState(secret, credentials.OAuthState{
		TenantID: "ten_1", CredentialID: "cred_1", Origin: "https://app.example.test",
		NonceHash: credentials.OAuthNonceHash(nonce),
	})
	if err != nil {
		t.Fatalf("SignOAuthState() error = %v", err)
	}
	if strings.Contains(state, nonce) {
		t.Fatal("the state carries the nonce itself; it must carry only its hash, since the state travels in a URL")
	}
	parsed, err := credentials.ParseOAuthState(secret, state)
	if err != nil {
		t.Fatalf("ParseOAuthState() error = %v", err)
	}
	if parsed.TenantID != "ten_1" || parsed.CredentialID != "cred_1" || parsed.Origin != "https://app.example.test" {
		t.Fatalf("parsed = %#v", parsed)
	}
	if !parsed.MatchesNonce(nonce) {
		t.Fatal("the state does not match the nonce it was signed with")
	}
	other, _ := credentials.NewOAuthNonce()
	if parsed.MatchesNonce(other) || parsed.MatchesNonce("") {
		t.Fatal("the state matched a nonce it was not signed with")
	}
	if _, err := credentials.ParseOAuthState(secret, state+"x"); err == nil {
		t.Fatal("tampered state was accepted")
	}
	if _, err := credentials.ParseOAuthState([]byte("other-key-32-bytes-long-value!!"), state); err == nil {
		t.Fatal("state signed with another key was accepted")
	}
}

// A state from before the browser binding existed carries no nonce, and with
// no nonce there is nothing to bind it to the browser that started it.
func TestAnOAuthStateWithoutANonceIsRefused(t *testing.T) {
	t.Parallel()
	secret := []byte("oauth-signing-key-32-bytes-long!!")
	state, err := credentials.SignOAuthState(secret, credentials.OAuthState{TenantID: "ten_1", CredentialID: "cred_1"})
	if err != nil {
		t.Fatalf("SignOAuthState() error = %v", err)
	}
	if _, err := credentials.ParseOAuthState(secret, state); err == nil {
		t.Fatal("a state without a nonce was accepted")
	}
}

func TestThePKCEVerifierIsDerivedFromTheNonceAndTheServerKey(t *testing.T) {
	t.Parallel()
	secret := []byte("oauth-signing-key-32-bytes-long!!")
	nonce, _ := credentials.NewOAuthNonce()
	verifier := credentials.PKCEVerifier(secret, nonce)
	// RFC 7636 section 4.1: 43 to 128 unreserved characters.
	if len(verifier) < 43 || len(verifier) > 128 {
		t.Fatalf("verifier length = %d", len(verifier))
	}
	if verifier != credentials.PKCEVerifier(secret, nonce) {
		t.Fatal("the verifier is not reproducible at the callback")
	}
	other, _ := credentials.NewOAuthNonce()
	if verifier == credentials.PKCEVerifier(secret, other) {
		t.Fatal("two nonces gave one verifier")
	}
	if verifier == credentials.PKCEVerifier([]byte("another-key-32-bytes-long-value!"), nonce) {
		t.Fatal("the verifier does not depend on the server key, so anyone holding the nonce could compute it")
	}
	sum := sha256.Sum256([]byte(verifier))
	if got, want := credentials.PKCEChallenge(verifier), base64.RawURLEncoding.EncodeToString(sum[:]); got != want {
		t.Fatalf("challenge = %q, want the S256 transform %q", got, want)
	}
}

func TestTheAuthorizeURLAsksForAnS256Challenge(t *testing.T) {
	t.Parallel()
	raw := credentials.GoogleAuthorizeURL("cid", "https://app.test/oauth/callback", "scope", "state", "the-challenge")
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	if parsed.Query().Get("code_challenge") != "the-challenge" || parsed.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("authorize URL = %s, want an S256 code challenge", raw)
	}
}

// Without the verifier the code is refused by Google, so a code exchanged
// without one is a bug: it is refused here, before any request is made.
func TestExchangeGoogleCodeRefusesAMissingVerifier(t *testing.T) {
	t.Parallel()
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	t.Cleanup(server.Close)
	_, err := credentials.ExchangeGoogleCode(server.Client(), server.URL, "cid", "csec", "https://app.test/oauth/callback", "auth-code", "")
	if err == nil || !strings.Contains(err.Error(), "verifier") {
		t.Fatalf("ExchangeGoogleCode(no verifier) = %v, want it refused", err)
	}
	if called {
		t.Fatal("a code was sent to the token endpoint without a verifier")
	}
}

func TestTokenNeedsRefresh(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	if credentials.TokenNeedsRefresh(map[string]string{
		"refresh_token": "r",
		"access_token":  "a",
		"expiry":        now.Add(10 * time.Minute).Format(time.RFC3339),
	}, now) {
		t.Fatal("a token with ten minutes left should not refresh")
	}
	if !credentials.TokenNeedsRefresh(map[string]string{
		"refresh_token": "r",
		"access_token":  "a",
		"expiry":        now.Add(time.Minute).Format(time.RFC3339),
	}, now) {
		t.Fatal("a token inside the two-minute window should refresh")
	}
	if credentials.TokenNeedsRefresh(map[string]string{"access_token": "a"}, now) {
		t.Fatal("a credential with no refresh token cannot refresh")
	}
}

func TestExchangeGoogleCodeStoresTokens(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if r.Form.Get("code") != "auth-code" || r.Form.Get("code_verifier") != "the-verifier" {
			t.Errorf("code = %q", r.Form.Get("code"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "ya29.access", "refresh_token": "1//refresh",
			"expires_in": 3600, "token_type": "Bearer",
		})
	}))
	t.Cleanup(server.Close)

	fields, err := credentials.ExchangeGoogleCode(server.Client(), server.URL, "cid", "csec", "https://app.test/oauth/callback", "auth-code", "the-verifier")
	if err != nil {
		t.Fatalf("ExchangeGoogleCode() error = %v", err)
	}
	if fields["access_token"] != "ya29.access" || fields["refresh_token"] != "1//refresh" {
		t.Fatalf("fields = %#v", fields)
	}
	if strings.TrimSpace(fields["expiry"]) == "" {
		t.Fatal("expiry was not written")
	}
}

func TestExchangeGoogleCodeRefusesANilClient(t *testing.T) {
	t.Parallel()
	_, err := credentials.ExchangeGoogleCode(nil, "https://oauth2.googleapis.com/token", "cid", "csec", "https://app.test/oauth/callback", "auth-code", "the-verifier")
	if err == nil || !strings.Contains(err.Error(), "http client is not configured") {
		t.Fatalf("ExchangeGoogleCode(nil) = %v, want a refused client", err)
	}
}

func TestGoogleDefaultsFillAnEmptyAllowlist(t *testing.T) {
	t.Parallel()
	record := credentials.Record{Type: credentials.GoogleDriveOAuthType}
	credentials.ApplyDefaultDomains(&record)
	if len(record.AllowedDomains) == 0 {
		t.Fatal("Google credentials must default to googleapis hosts")
	}
	scoped := credentials.Record{Type: credentials.GmailOAuthType, AllowedDomains: record.AllowedDomains}
	if !scoped.AllowsHost("gmail.googleapis.com") {
		t.Fatal("gmail.googleapis.com must be allowed by the default list")
	}
	if !scoped.AllowsHost("www.googleapis.com") {
		t.Fatal("www.googleapis.com must be allowed by the default list")
	}
	if !scoped.AllowsHost("lh3.googleusercontent.com") {
		t.Fatal("googleusercontent hosts must be allowed by the default list")
	}
}
