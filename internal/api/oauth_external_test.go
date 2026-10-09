package api_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// rotatingSecretProvider answers every key with whatever value is current, so
// a test can tell a reference that is still resolved from a value that was
// sealed into the row once.
type rotatingSecretProvider struct{ value *atomic.Value }

func (rotatingSecretProvider) Name() string                 { return "test" }
func (rotatingSecretProvider) Health(context.Context) error { return nil }
func (provider rotatingSecretProvider) Fetch(context.Context, string) (string, error) {
	return provider.value.Load().(string), nil
}

func externalResolver(store *repository.GORMCredentialStore, value *atomic.Value) {
	resolver := credentials.NewResolver(store, credentials.ResolverConfig{})
	resolver.RegisterProvider("test", func(credentials.Binding) (credentials.Provider, error) {
		return rotatingSecretProvider{value: value}, nil
	})
	store.SetExternalResolver(resolver)
}

// Connect exchanges the code with the resolved client secret and writes back
// only what Google returned. Writing back the resolved field map would seal
// the manager's plaintext over the ext:// reference, and a later rotation in
// the manager would never reach this credential again.
func TestOAuthCallbackKeepsAnExternalClientSecretSealed(t *testing.T) {
	fake := newFakeGoogle(t)
	handler, store := oauthAPI(t, "https://app.test", fake)
	var value atomic.Value
	value.Store("first-secret")
	externalResolver(store, &value)
	tenant := repository.TenantScope{ID: repository.DefaultTenantID}
	if _, err := store.CreateSecretBinding(t.Context(), tenant, credentials.Binding{
		Name: "prod-vault", Provider: "test", Address: "https://vault.example",
		TokenEnv: "KILASFLOW_EXTERNAL_SECRET_TOKEN_PROD_VAULT",
	}); err != nil {
		t.Fatalf("CreateSecretBinding() error = %v", err)
	}

	created := storeCredential(t, handler, "Drive", credentials.GoogleDriveOAuthType, map[string]string{
		"clientId": "tenant-cid", "clientSecret": "ext://prod-vault/google/client-secret",
	})
	start := startConnect(t, handler, fake, created.ID)
	if callback := finishConnect(t, handler, start.state(), start.cookies); !connected(callback) {
		t.Fatalf("callback = %s, want connected", callback.Body)
	}

	// The manager rotates the secret, and a fresh resolver (no cache) reads
	// the row again: a kept reference resolves to the new value, a sealed
	// plaintext would still say the old one.
	value.Store("second-secret")
	externalResolver(store, &value)
	fields := storedGoogleTokens(t, store, created.ID)
	if fields["access_token"] != "ya29.secret" || fields["refresh_token"] != "1//refresh" {
		t.Fatalf("stored tokens = %#v, want the exchanged ones", fields)
	}
	if fields["clientSecret"] != "second-secret" {
		t.Fatalf("clientSecret = %q after the manager rotated it: the callback sealed the resolved value over the reference", fields["clientSecret"])
	}
}
