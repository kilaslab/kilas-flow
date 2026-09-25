package credentials_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/credentials"
)

func TestOAuthStateRoundTripsAndRejectsTampering(t *testing.T) {
	t.Parallel()
	secret := []byte("oauth-signing-key-32-bytes-long!!")
	state, err := credentials.SignOAuthState(secret, credentials.OAuthState{
		TenantID: "ten_1", CredentialID: "cred_1", Origin: "https://app.example.test",
	})
	if err != nil {
		t.Fatalf("SignOAuthState() error = %v", err)
	}
	parsed, err := credentials.ParseOAuthState(secret, state)
	if err != nil {
		t.Fatalf("ParseOAuthState() error = %v", err)
	}
	if parsed.TenantID != "ten_1" || parsed.CredentialID != "cred_1" || parsed.Origin != "https://app.example.test" {
		t.Fatalf("parsed = %#v", parsed)
	}
	if _, err := credentials.ParseOAuthState(secret, state+"x"); err == nil {
		t.Fatal("tampered state was accepted")
	}
	if _, err := credentials.ParseOAuthState([]byte("other-key-32-bytes-long-value!!"), state); err == nil {
		t.Fatal("state signed with another key was accepted")
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
		if r.Form.Get("code") != "auth-code" {
			t.Errorf("code = %q", r.Form.Get("code"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "ya29.access", "refresh_token": "1//refresh",
			"expires_in": 3600, "token_type": "Bearer",
		})
	}))
	t.Cleanup(server.Close)

	fields, err := credentials.ExchangeGoogleCode(server.Client(), server.URL, "cid", "csec", "https://app.test/oauth/callback", "auth-code")
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
	_, err := credentials.ExchangeGoogleCode(nil, "https://oauth2.googleapis.com/token", "cid", "csec", "https://app.test/oauth/callback", "auth-code")
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
