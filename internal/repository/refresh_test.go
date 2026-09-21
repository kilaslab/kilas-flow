package repository_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

func TestRefreshingCredentialStoreRefreshesAnExpiringGoogleToken(t *testing.T) {
	t.Parallel()

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "1//old" {
			t.Errorf("token form = %v", r.Form)
		}
		if r.Form.Get("client_id") != "platform-cid" {
			t.Errorf("platform client was not used: %v", r.Form)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "ya29.new", "expires_in": 3600, "token_type": "Bearer",
		})
	}))
	t.Cleanup(tokenServer.Close)

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "refresh.db"),
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
		key[index] = byte(index + 9)
	}
	cipher, err := credentials.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	inner := repository.NewCredentialStore(db.DB, cipher)
	store := repository.NewRefreshingCredentialStore(inner, tokenServer.Client(), tokenServer.URL, "platform-cid", "platform-csec")
	tenant := repository.TenantScope{ID: "default"}
	created, err := inner.Create(context.Background(), tenant, credentials.Record{
		Name: "Drive", Type: credentials.GoogleDriveOAuthType,
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
	if fields["access_token"] != "ya29.new" {
		t.Fatalf("refreshed access_token = %q", fields["access_token"])
	}

	listed, err := inner.Get(context.Background(), tenant, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if listed.Fields["access_token"] == "ya29.new" || listed.Fields["access_token"] == "ya29.old" {
		t.Fatalf("Get leaked the token: %#v", listed.Fields)
	}
}

func TestRefreshingCredentialStoreRefusesANilHTTPClient(t *testing.T) {
	t.Parallel()

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "refresh-nil.db"),
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
		key[index] = byte(index + 11)
	}
	cipher, err := credentials.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	inner := repository.NewCredentialStore(db.DB, cipher)
	store := repository.NewRefreshingCredentialStore(inner, nil, "", "platform-cid", "platform-csec")
	tenant := repository.TenantScope{ID: "default"}
	created, err := inner.Create(context.Background(), tenant, credentials.Record{
		Name: "Drive", Type: credentials.GoogleDriveOAuthType,
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
	_, _, err = store.Resolve(context.Background(), tenant, created.ID)
	if err == nil || !strings.Contains(err.Error(), "http client is not configured") {
		t.Fatalf("Resolve() = %v, want a refused client", err)
	}
}
