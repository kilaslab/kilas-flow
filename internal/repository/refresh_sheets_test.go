package repository_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// A Sheets or Sheets Trigger credential is a Google credential: a token close
// to its expiry is refreshed on resolve, the way Drive's and Gmail's are.
func TestRefreshingCredentialStoreRefreshesAnExpiringSheetsToken(t *testing.T) {
	t.Parallel()

	for _, typeID := range []string{credentials.GoogleSheetsOAuthType, credentials.GoogleSheetsTriggerOAuthType} {
		t.Run(typeID, func(t *testing.T) {
			t.Parallel()

			tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Errorf("ParseForm: %v", err)
				}
				if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "1//old" {
					t.Errorf("token form = %v", r.Form)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token": "ya29.sheets", "expires_in": 3600, "token_type": "Bearer",
				})
			}))
			t.Cleanup(tokenServer.Close)

			quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
			db, err := database.Open(context.Background(), config.Database{
				Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "refresh-sheets.db"),
			}, quiet)
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := database.Migrate(db, quiet); err != nil {
				t.Fatalf("Migrate() error = %v", err)
			}
			key := make([]byte, credentials.KeySize)
			for index := range key {
				key[index] = byte(index + 17)
			}
			cipher, err := credentials.NewCipher(key)
			if err != nil {
				t.Fatalf("NewCipher() error = %v", err)
			}
			inner := repository.NewCredentialStore(db.DB, cipher)
			store := repository.NewRefreshingCredentialStore(inner, tokenServer.Client(), tokenServer.URL, "platform-cid", "platform-csec")
			tenant := repository.TenantScope{ID: "default"}
			created, err := inner.Create(context.Background(), tenant, credentials.Record{
				Name: "Sheets", Type: typeID,
				Fields: map[string]string{
					"refresh_token": "1//old",
					"access_token":  "ya29.old",
					"expiry":        time.Now().UTC().Add(time.Minute).Format(time.RFC3339),
				},
				AllowedDomains: credentials.GoogleDefaultDomains,
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}

			_, fields, err := store.Resolve(context.Background(), tenant, created.ID)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if fields["access_token"] != "ya29.sheets" {
				t.Fatalf("access_token = %q, want the refreshed token", fields["access_token"])
			}
		})
	}
}
