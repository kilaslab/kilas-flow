package repository_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// A Google credential whose client secret is an ext:// reference refreshes
// with the resolved secret, and the row it writes back still holds the
// reference. Writing back the resolved field map sealed the manager's
// plaintext into the row, so the secret then outlived its rotation in the
// manager and no longer needed the manager at all.
func TestRefreshKeepsAnExternalReferenceSealed(t *testing.T) {
	var sentSecret atomic.Value
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		sentSecret.Store(r.Form.Get("client_secret"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "ya29.new", "expires_in": 3600, "token_type": "Bearer",
		})
	}))
	t.Cleanup(tokenServer.Close)

	db, inner, cipher := newCredentialFixture(t)
	ctx := context.Background()
	tenant := repository.TenantScope{ID: "tenant-a"}
	var fetches atomic.Int64
	wiredStore(inner, &fetches)
	mustBinding(t, inner, tenant, "prod-vault")
	store := repository.NewRefreshingCredentialStore(inner, tokenServer.Client(), tokenServer.URL, "", "")

	reference := "ext://prod-vault/google/client-secret"
	created, err := inner.Create(ctx, tenant, credentials.Record{
		Name: "Drive", Type: credentials.GoogleDriveOAuthType,
		Fields: map[string]string{
			"clientId":      "tenant-cid",
			"clientSecret":  reference,
			"refresh_token": "1//old",
			"access_token":  "ya29.old",
			"expiry":        time.Now().UTC().Add(time.Minute).Format(time.RFC3339),
		},
		AllowedDomains: credentials.GoogleDefaultDomains,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, fields, err := store.Resolve(ctx, tenant, created.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if fields["access_token"] != "ya29.new" || fields["clientSecret"] != "live-value:google/client-secret" {
		t.Fatalf("Resolve() = %#v, want the refreshed token and the resolved secret", fields)
	}
	if got, _ := sentSecret.Load().(string); got != "live-value:google/client-secret" {
		t.Fatalf("the refresh sent client_secret %q, want the manager's value", got)
	}

	sealed, err := cipher.Open(rawCredentialPayload(t, db, created.ID),
		credentials.CredentialAAD(tenant.ID, created.ID, created.Type))
	if err != nil {
		t.Fatalf("open the stored payload: %v", err)
	}
	if sealed["clientSecret"] != reference {
		t.Fatalf("stored clientSecret = %q, want the reference kept", sealed["clientSecret"])
	}
	if sealed["access_token"] != "ya29.new" {
		t.Fatalf("stored access_token = %q, want the refreshed token", sealed["access_token"])
	}
	for key, value := range sealed {
		if strings.Contains(value, "live-value") {
			t.Fatalf("stored field %q carries the manager's plaintext", key)
		}
	}
}
