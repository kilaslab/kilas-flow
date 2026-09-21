package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// A key minted with scopes keeps them, and the legacy mint is unchanged: the
// difference between an agent token and the tenant's own key is one column,
// and a key minted without it has exactly the authority every key had before
// scopes existed.
func TestAScopedKeyKeepsItsScopesAndALegacyKeyHasNone(t *testing.T) {
	store := newAuthStore(t)
	ctx := context.Background()
	tenant := repository.TenantScope{ID: repository.DefaultTenantID}

	legacy, _, err := store.CreateAPIKey(ctx, tenant, "legacy")
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	if legacy.Scoped() {
		t.Errorf("a key minted without scopes reports scopes %v", legacy.Scopes)
	}
	if legacy.WorkflowID != "" || legacy.ExpiresAt != nil {
		t.Errorf("a legacy key carries %q / %v, want no binding and no expiry", legacy.WorkflowID, legacy.ExpiresAt)
	}

	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	scoped, token, err := store.CreateScopedAPIKey(ctx, tenant, "agent",
		[]embed.Scope{embed.ScopeRun, embed.ScopeDatastoreRead}, "wf_1", &expires)
	if err != nil {
		t.Fatalf("CreateScopedAPIKey() error = %v", err)
	}
	if !scoped.Scoped() {
		t.Fatal("a key minted with scopes reports none")
	}
	if len(scoped.Scopes) != 2 || scoped.Scopes[0] != embed.ScopeDatastoreRead || scoped.Scopes[1] != embed.ScopeRun {
		t.Errorf("Scopes = %v, want the two asked for, normalised", scoped.Scopes)
	}
	if scoped.WorkflowID != "wf_1" {
		t.Errorf("WorkflowID = %q, want the binding", scoped.WorkflowID)
	}
	if scoped.ExpiresAt == nil || !scoped.ExpiresAt.Equal(expires) {
		t.Errorf("ExpiresAt = %v, want %v", scoped.ExpiresAt, expires)
	}

	// The scopes survive the round trip through authentication, which is the
	// path a request actually takes.
	authenticated, err := store.AuthenticateAPIKey(ctx, token)
	if err != nil {
		t.Fatalf("AuthenticateAPIKey() error = %v", err)
	}
	if len(authenticated.Scopes) != 2 || authenticated.WorkflowID != "wf_1" {
		t.Errorf("authenticated key = %+v, want the stored scopes and binding", authenticated)
	}
	if !authenticated.Expired(expires.Add(time.Second)) {
		t.Error("a key is not reported expired after its expiry")
	}
	if authenticated.Expired(expires.Add(-time.Second)) {
		t.Error("a key is reported expired before its expiry")
	}
}

// A scope this build does not know is refused rather than stored: a row
// written by a future version must not hand a key an authority this one cannot
// reason about.
func TestAMintRefusesAScopeThisBuildDoesNotKnow(t *testing.T) {
	store := newAuthStore(t)
	ctx := context.Background()
	tenant := repository.TenantScope{ID: repository.DefaultTenantID}
	if _, _, err := store.CreateScopedAPIKey(ctx, tenant, "agent", []embed.Scope{"workflow:everything"}, "", nil); err == nil {
		t.Fatal("CreateScopedAPIKey() accepted a scope this build does not have")
	}
}
