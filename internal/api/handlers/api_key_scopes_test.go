package handlers

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// keyMintHandler is the auth surface over a real store, which is what makes
// these tests exercise the row rather than a stub.
func keyMintHandler(t *testing.T) *Auth {
	t.Helper()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "keys.db"),
	}, discard)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, discard); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return loginTestHandler(t, repository.NewAuthStore(db.DB))
}

// tenantWideCaller is the principal an ordinary API key authenticates as: no
// scopes, which is the authority every key had before scopes existed.
func tenantWideCaller() context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		TenantID: repository.DefaultTenantID, KeyID: "key_tenant",
		Kind: auth.KindAPIKey, Label: "host",
	})
}

// A scoped key may never mint another key: minting is how authority is handed
// out, and an agent that could do it would escalate past its own list.
func TestAScopedKeyCannotMintAnotherKey(t *testing.T) {
	handler := keyMintHandler(t)
	scoped := auth.WithPrincipal(context.Background(), auth.Principal{
		TenantID: repository.DefaultTenantID, KeyID: "key_agent",
		Kind: auth.KindAPIKey, Label: "agent",
		Scopes: []embed.Scope{embed.ScopeWrite},
	})
	input := &createAPIKeyInput{}
	input.Body.Label = "escalation"
	_, err := handler.CreateKey(scoped, input)
	if err == nil {
		t.Fatal("CreateKey() minted a key from a scoped caller")
	}
	if status := statusOf(t, err); status != 403 {
		t.Errorf("status = %d, want 403", status)
	}
	if !strings.Contains(err.Error(), "scope_denied") {
		t.Errorf("error = %v, want the scope_denied code", err)
	}
}

// The three optional fields are stored and returned, and the response carries
// the authority the key was given rather than only its label.
func TestCreateKeyStoresTheScopeBindingAndExpiry(t *testing.T) {
	handler := keyMintHandler(t)
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	input := &createAPIKeyInput{}
	input.Body.Label = "agent"
	input.Body.Scopes = []string{"workflow:run", "datastore:read"}
	input.Body.WorkflowID = "wf_1"
	input.Body.ExpiresAt = &expires

	output, err := handler.CreateKey(tenantWideCaller(), input)
	if err != nil {
		t.Fatalf("CreateKey() error = %v", err)
	}
	if len(output.Body.Scopes) != 2 || output.Body.WorkflowID != "wf_1" {
		t.Errorf("body = %+v, want the scopes and the binding", output.Body)
	}
	if output.Body.ExpiresAt == nil || !output.Body.ExpiresAt.Equal(expires) {
		t.Errorf("ExpiresAt = %v, want %v", output.Body.ExpiresAt, expires)
	}
	if output.Body.Token == "" {
		t.Error("the mint response carries no token")
	}
}

// The refusals a caller can fix: an unknown scope, a binding with no scopes,
// an expiry in the past.
func TestCreateKeyRefusesWhatCannotWork(t *testing.T) {
	handler := keyMintHandler(t)
	past := time.Now().UTC().Add(-time.Minute)
	cases := []struct {
		name   string
		mutate func(*createAPIKeyInput)
		want   string
	}{
		{
			name:   "unknown scope",
			mutate: func(input *createAPIKeyInput) { input.Body.Scopes = []string{"workflow:everything"} },
			want:   "not supported",
		},
		{
			name:   "binding with no scopes",
			mutate: func(input *createAPIKeyInput) { input.Body.WorkflowID = "wf_1" },
			want:   "needs scopes",
		},
		{
			name:   "expiry in the past",
			mutate: func(input *createAPIKeyInput) { input.Body.ExpiresAt = &past },
			want:   "in the past",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			input := &createAPIKeyInput{}
			input.Body.Label = "agent"
			testCase.mutate(input)
			_, err := handler.CreateKey(tenantWideCaller(), input)
			if err == nil {
				t.Fatal("CreateKey() accepted an input that cannot work")
			}
			if status := statusOf(t, err); status != 422 {
				t.Errorf("status = %d, want 422", status)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("error = %v, want it to mention %q", err, testCase.want)
			}
		})
	}
}
