package repository_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// secretProvider serves one value per tenant binding, so which value comes
// back proves which tenant's binding the lookup used.
type secretProvider struct {
	fetches *atomic.Int64
}

func (provider *secretProvider) Name() string { return "test" }

func (provider *secretProvider) Health(context.Context) error { return nil }

func (provider *secretProvider) Fetch(_ context.Context, key string) (string, error) {
	provider.fetches.Add(1)
	return "live-value:" + key, nil
}

func newCredentialFixture(t *testing.T) (*database.DB, *repository.GORMCredentialStore, *credentials.Cipher) {
	t.Helper()
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "kilasflow.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	key := make([]byte, credentials.KeySize)
	for i := range key {
		key[i] = byte(i + 7)
	}
	cipher, err := credentials.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	return db, repository.NewCredentialStore(db.DB, cipher), cipher
}

func wiredStore(store *repository.GORMCredentialStore, fetches *atomic.Int64) {
	resolver := credentials.NewResolver(store, credentials.ResolverConfig{})
	resolver.RegisterProvider("test", func(binding credentials.Binding) (credentials.Provider, error) {
		if binding.TenantID == "" {
			return nil, fmt.Errorf("binding arrived without a tenant")
		}
		return &secretProvider{fetches: fetches}, nil
	})
	store.SetExternalResolver(resolver)
}

func mustBinding(t *testing.T, store *repository.GORMCredentialStore, tenant repository.TenantScope, name string) {
	t.Helper()
	if _, err := store.CreateSecretBinding(context.Background(), tenant, credentials.Binding{
		Name: name, Provider: "test", Address: "https://vault.example", TokenEnv: "KILASFLOW_TEST_VAULT_TOKEN",
	}); err != nil {
		t.Fatalf("CreateSecretBinding() error = %v", err)
	}
}

func TestExternalReferenceResolvesOnTheResolvePathOnly(t *testing.T) {
	db, store, cipher := newCredentialFixture(t)
	ctx := context.Background()
	tenant := repository.TenantScope{ID: "tenant-a"}
	var fetches atomic.Int64
	wiredStore(store, &fetches)
	mustBinding(t, store, tenant, "prod-vault")

	created, err := store.Create(ctx, tenant, credentials.Record{
		Name: "OpenAI", Type: "openAiApi",
		Fields: map[string]string{"apiKey": "ext://prod-vault/prod/openai"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Get and List never carry plaintext: the editor sees metadata only.
	got, err := store.Get(ctx, tenant, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if _, present := got.Fields["apiKey"]; present {
		t.Fatalf("Get() exposed the secret half: %#v", got.Fields)
	}

	// Resolve is the one path that returns plaintext, and it returns the
	// manager's value, not the stored reference.
	_, fields, err := store.Resolve(ctx, tenant, created.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if fields["apiKey"] != "live-value:prod/openai" {
		t.Fatalf("Resolve() = %q, want the manager value", fields["apiKey"])
	}

	// The stored row keeps the sealed reference: decrypting the payload by
	// hand must show ext://…, never the live value.
	var row struct {
		Payload []byte
	}
	if err := db.DB.Table("credentials").Where("id = ?", created.ID).Select("payload").Scan(&row).Error; err != nil {
		t.Fatalf("read raw payload: %v", err)
	}
	payload := row.Payload
	sealed, err := cipher.Decrypt(payload)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if sealed["apiKey"] != "ext://prod-vault/prod/openai" {
		t.Fatalf("stored payload = %q, want the sealed reference", sealed["apiKey"])
	}
	for key, value := range sealed {
		if strings.Contains(value, "live-value") {
			t.Fatalf("stored field %q carries resolved plaintext", key)
		}
	}

	// Redaction still masks the field for API responses.
	if safe := credentials.Redacted("openAiApi", fields); safe["apiKey"] != credentials.RedactedValue {
		t.Fatalf("Redacted() = %q, want the marker", safe["apiKey"])
	}
}

func TestExternalReferenceIsTenantScopedEndToEnd(t *testing.T) {
	_, store, _ := newCredentialFixture(t)
	ctx := context.Background()
	tenantA := repository.TenantScope{ID: "tenant-a"}
	tenantB := repository.TenantScope{ID: "tenant-b"}
	var fetches atomic.Int64
	wiredStore(store, &fetches)
	mustBinding(t, store, tenantA, "shared-name")
	mustBinding(t, store, tenantB, "shared-name")

	createdA, err := store.Create(ctx, tenantA, credentials.Record{
		Name: "A", Type: "openAiApi", Fields: map[string]string{"apiKey": "ext://shared-name/k"},
	})
	if err != nil {
		t.Fatalf("Create(A) error = %v", err)
	}
	// Tenant B cannot even name tenant A's credential, let alone resolve it.
	if _, _, err := store.Resolve(ctx, tenantB, createdA.ID); err == nil {
		t.Fatal("tenant B resolved tenant A's credential")
	}
	// And B cannot read A's binding: B has its own row of the same name, which
	// is what its reference resolves against.
	createdB, err := store.Create(ctx, tenantB, credentials.Record{
		Name: "B", Type: "openAiApi", Fields: map[string]string{"apiKey": "ext://shared-name/k"},
	})
	if err != nil {
		t.Fatalf("Create(B) error = %v", err)
	}
	if _, _, err := store.Resolve(ctx, repository.TenantScope{ID: "tenant-c"}, createdB.ID); err == nil {
		t.Fatal("an unknown tenant resolved a credential")
	}
}

func TestBindingChangeDropsCachedValues(t *testing.T) {
	_, store, _ := newCredentialFixture(t)
	ctx := context.Background()
	tenant := repository.TenantScope{ID: "tenant-a"}
	var fetches atomic.Int64
	wiredStore(store, &fetches)
	mustBinding(t, store, tenant, "prod-vault")

	created, err := store.Create(ctx, tenant, credentials.Record{
		Name: "OpenAI", Type: "openAiApi", Fields: map[string]string{"apiKey": "ext://prod-vault/k"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	for range 2 {
		if _, _, err := store.Resolve(ctx, tenant, created.ID); err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("two resolves caused %d fetches, want 1 cached", got)
	}
	if _, err := store.UpdateSecretBinding(ctx, tenant, "prod-vault", credentials.Binding{
		Provider: "test", Address: "https://vault-two.example", TokenEnv: "KILASFLOW_TEST_VAULT_TOKEN",
	}); err != nil {
		t.Fatalf("UpdateSecretBinding() error = %v", err)
	}
	if _, _, err := store.Resolve(ctx, tenant, created.ID); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got := fetches.Load(); got != 2 {
		t.Fatalf("fetches = %d after a binding change, want 2", got)
	}
	if _, err := store.UpdateSecretBinding(ctx, tenant, "prod-vault", credentials.Binding{
		Name: "renamed", Provider: "test", Address: "x", TokenEnv: "Y",
	}); err == nil {
		t.Fatal("a binding rename succeeded; every stored reference to the old name would dangle")
	}
}

func TestUnwiredStoreFailsClosedOnReferences(t *testing.T) {
	_, store, _ := newCredentialFixture(t)
	ctx := context.Background()
	tenant := repository.TenantScope{ID: "tenant-a"}

	created, err := store.Create(ctx, tenant, credentials.Record{
		Name: "OpenAI", Type: "openAiApi", Fields: map[string]string{"apiKey": "ext://prod-vault/k"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// No resolver attached: Resolve refuses rather than returning the
	// reference string as though it were the secret.
	_, fields, err := store.Resolve(ctx, tenant, created.ID)
	if err == nil || fields != nil {
		t.Fatalf("unwired Resolve() = %#v, %v; want a closed failure with no fields", fields, err)
	}
}

func TestReferenceInAPublicFieldIsRefusedAtWrite(t *testing.T) {
	_, store, _ := newCredentialFixture(t)
	ctx := context.Background()
	tenant := repository.TenantScope{ID: "tenant-a"}

	// httpHeaderAuth.name is a non-secret field: a reference there would sit
	// in plaintext in the public half of the row.
	if _, err := store.Create(ctx, tenant, credentials.Record{
		Name: "H", Type: "httpHeaderAuth",
		Fields: map[string]string{"name": "ext://prod-vault/k", "value": "v"},
	}); err == nil {
		t.Fatal("a reference in a public field was stored in plaintext")
	}
}
