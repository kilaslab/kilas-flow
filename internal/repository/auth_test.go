package repository_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/auth"
	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/repository"
)

func newAuthStore(t *testing.T) *repository.GORMAuthStore {
	t.Helper()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "auth.db"),
	}, discard)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, discard); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return repository.NewAuthStore(db.DB)
}

// The upgrade path: rows already written under "default" have to belong to a
// tenant that exists, or the foreign keys added with identity would orphan them.
func TestTheMigrationGivesTheDefaultTenantARealRecord(t *testing.T) {
	store := newAuthStore(t)

	tenant, err := store.GetTenant(context.Background(), repository.DefaultTenantID)
	if err != nil {
		t.Fatalf("GetTenant(%q) error = %v", repository.DefaultTenantID, err)
	}
	if tenant.ID != repository.DefaultTenantID {
		t.Errorf("tenant ID = %q, want %q", tenant.ID, repository.DefaultTenantID)
	}
}

func TestEnsuringATenantTwiceLeavesOne(t *testing.T) {
	store := newAuthStore(t)
	ctx := context.Background()

	first, err := store.EnsureTenant(ctx, "acme", "Acme")
	if err != nil {
		t.Fatalf("EnsureTenant() error = %v", err)
	}
	// A restart runs the bootstrap path again; it must not fail and must not
	// produce a second tenant.
	second, err := store.EnsureTenant(ctx, "acme", "Acme Renamed")
	if err != nil {
		t.Fatalf("EnsureTenant() second call error = %v", err)
	}
	if second.ID != first.ID || second.Name != "Acme" {
		t.Errorf("second EnsureTenant = %#v, want the first tenant unchanged", second)
	}
}

func TestAnAPIKeyIsReturnedInFullExactlyOnce(t *testing.T) {
	store := newAuthStore(t)
	ctx := context.Background()
	tenant := repository.TenantScope{ID: repository.DefaultTenantID}

	key, token, err := store.CreateAPIKey(ctx, tenant, "ci")
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	if token == "" {
		t.Fatal("CreateAPIKey returned no token")
	}
	if !strings.HasPrefix(token, auth.KeyVersion+"_") {
		t.Errorf("token %q does not carry the key version prefix", token)
	}

	listed, err := store.ListAPIKeys(ctx, tenant)
	if err != nil {
		t.Fatalf("ListAPIKeys() error = %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed %d keys, want 1", len(listed))
	}
	// The listing carries the public handle and nothing that could be replayed.
	if listed[0].ID != key.ID || listed[0].Prefix != key.Prefix {
		t.Errorf("listed key = %#v, want the created one", listed[0])
	}
	_, secret, ok := auth.SplitKey(token)
	if !ok {
		t.Fatalf("SplitKey(%q) failed", token)
	}
	if strings.Contains(listed[0].Prefix, secret) || strings.Contains(listed[0].Label, secret) {
		t.Error("a listing disclosed part of the key's secret")
	}
}

func TestAnAPIKeyAuthenticatesItsOwnTenant(t *testing.T) {
	store := newAuthStore(t)
	ctx := context.Background()
	if _, err := store.EnsureTenant(ctx, "acme", "Acme"); err != nil {
		t.Fatalf("EnsureTenant() error = %v", err)
	}

	created, token, err := store.CreateAPIKey(ctx, repository.TenantScope{ID: "acme"}, "host backend")
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}

	authenticated, err := store.AuthenticateAPIKey(ctx, token)
	if err != nil {
		t.Fatalf("AuthenticateAPIKey() error = %v", err)
	}
	if authenticated.ID != created.ID || authenticated.TenantID != "acme" {
		t.Errorf("authenticated key = %#v, want the created key under acme", authenticated)
	}
}

func TestARevokedKeyStopsAuthenticating(t *testing.T) {
	store := newAuthStore(t)
	ctx := context.Background()
	tenant := repository.TenantScope{ID: repository.DefaultTenantID}

	created, token, err := store.CreateAPIKey(ctx, tenant, "leaked")
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	if _, err := store.AuthenticateAPIKey(ctx, token); err != nil {
		t.Fatalf("AuthenticateAPIKey() before revocation error = %v", err)
	}

	revoked, err := store.RevokeAPIKey(ctx, tenant, created.ID)
	if err != nil {
		t.Fatalf("RevokeAPIKey() error = %v", err)
	}
	if !revoked.Revoked() {
		t.Error("the revoked key does not report itself revoked")
	}

	if _, err := store.AuthenticateAPIKey(ctx, token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("AuthenticateAPIKey() after revocation error = %v, want ErrUnauthenticated", err)
	}
	// The row survives revocation so an audit still has something to name.
	listed, err := store.ListAPIKeys(ctx, tenant)
	if err != nil {
		t.Fatalf("ListAPIKeys() error = %v", err)
	}
	if len(listed) != 1 || !listed[0].Revoked() {
		t.Errorf("listing after revocation = %#v, want the key kept and marked revoked", listed)
	}
}

func TestOneTenantCannotRevokeAnothersKey(t *testing.T) {
	store := newAuthStore(t)
	ctx := context.Background()
	for _, id := range []string{"acme", "globex"} {
		if _, err := store.EnsureTenant(ctx, id, id); err != nil {
			t.Fatalf("EnsureTenant(%q) error = %v", id, err)
		}
	}
	acmeKey, token, err := store.CreateAPIKey(ctx, repository.TenantScope{ID: "acme"}, "acme key")
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}

	// Not found rather than forbidden: a refusal would confirm the ID exists.
	if _, err := store.RevokeAPIKey(ctx, repository.TenantScope{ID: "globex"}, acmeKey.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("RevokeAPIKey() across tenants error = %v, want ErrNotFound", err)
	}
	if _, err := store.AuthenticateAPIKey(ctx, token); err != nil {
		t.Errorf("the key stopped working after another tenant tried to revoke it: %v", err)
	}
}

func TestAKeyListingNeverCrossesTenants(t *testing.T) {
	store := newAuthStore(t)
	ctx := context.Background()
	for _, id := range []string{"acme", "globex"} {
		if _, err := store.EnsureTenant(ctx, id, id); err != nil {
			t.Fatalf("EnsureTenant(%q) error = %v", id, err)
		}
		if _, _, err := store.CreateAPIKey(ctx, repository.TenantScope{ID: id}, id+" key"); err != nil {
			t.Fatalf("CreateAPIKey(%q) error = %v", id, err)
		}
	}

	listed, err := store.ListAPIKeys(ctx, repository.TenantScope{ID: "acme"})
	if err != nil {
		t.Fatalf("ListAPIKeys() error = %v", err)
	}
	if len(listed) != 1 || listed[0].TenantID != "acme" {
		t.Errorf("acme's listing = %#v, want exactly its own key", listed)
	}
}

func TestAForgedKeyIsRefusedWithoutRevealingWhetherThePrefixExists(t *testing.T) {
	store := newAuthStore(t)
	ctx := context.Background()
	tenant := repository.TenantScope{ID: repository.DefaultTenantID}
	_, token, err := store.CreateAPIKey(ctx, tenant, "real")
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	prefix, _, _ := auth.SplitKey(token)

	forged := map[string]string{
		"a real prefix with a wrong secret": auth.KeyVersion + "_" + prefix + "_wrong",
		"a prefix that does not exist":      auth.KeyVersion + "_00112233aabb_wrong",
		"an embed token":                    "kfe1.payload.signature",
		"nothing at all":                    "",
	}
	for name, presented := range forged {
		if _, err := store.AuthenticateAPIKey(ctx, presented); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Errorf("AuthenticateAPIKey with %s: error = %v, want ErrUnauthenticated", name, err)
		}
	}
}

func TestAnAccountIsFoundForLoginAndItsHashStaysInside(t *testing.T) {
	store := newAuthStore(t)
	ctx := context.Background()
	hash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	created, err := store.CreateUser(ctx, repository.TenantScope{ID: repository.DefaultTenantID}, "Owner@Example.COM", "Owner", hash)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	// Addresses are folded on the way in, so signing in with different case
	// reaches the same account rather than reporting no such user.
	if created.Email != "owner@example.com" {
		t.Errorf("stored email = %q, want it normalized", created.Email)
	}
	if created.PasswordHash != "" {
		t.Error("CreateUser returned the password hash to its caller")
	}

	found, err := store.FindUserForLogin(ctx, "OWNER@example.com")
	if err != nil {
		t.Fatalf("FindUserForLogin() error = %v", err)
	}
	if found.ID != created.ID {
		t.Errorf("found user %q, want %q", found.ID, created.ID)
	}
	if !auth.MatchPassword("hunter2", found.PasswordHash) {
		t.Error("the hash returned for login does not verify the password")
	}
}

func TestAnAccountCannotBeCreatedTwiceForOneAddress(t *testing.T) {
	store := newAuthStore(t)
	ctx := context.Background()
	tenant := repository.TenantScope{ID: repository.DefaultTenantID}
	hash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if _, err := store.CreateUser(ctx, tenant, "owner@example.com", "Owner", hash); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	// Two accounts on one address would leave login guessing which password
	// to check, so the database refuses rather than the handler remembering to.
	if _, err := store.CreateUser(ctx, tenant, "owner@example.com", "Impostor", hash); err == nil {
		t.Error("CreateUser accepted a second account for one address")
	}
}

func TestAnAccountCannotBeCreatedForATenantThatDoesNotExist(t *testing.T) {
	store := newAuthStore(t)
	hash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	// The foreign key is what stops a typo in a tenant ID from producing an
	// account nobody can reach and no listing will ever show.
	if _, err := store.CreateUser(context.Background(), repository.TenantScope{ID: "typo"}, "owner@example.com", "Owner", hash); err == nil {
		t.Error("CreateUser accepted an account under a tenant with no record")
	}
}

func TestCountingUsersSeesEveryTenant(t *testing.T) {
	store := newAuthStore(t)
	ctx := context.Background()
	hash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if _, err := store.EnsureTenant(ctx, "acme", "Acme"); err != nil {
		t.Fatalf("EnsureTenant() error = %v", err)
	}

	total, err := store.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers() error = %v", err)
	}
	if total != 0 {
		t.Fatalf("CountUsers() on a fresh install = %d, want 0", total)
	}

	if _, err := store.CreateUser(ctx, repository.TenantScope{ID: "acme"}, "owner@acme.example", "Owner", hash); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	// Bootstrap asks this question to decide whether an installation is fresh,
	// so an account in any tenant has to count.
	total, err = store.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers() error = %v", err)
	}
	if total != 1 {
		t.Errorf("CountUsers() = %d, want 1", total)
	}
}
