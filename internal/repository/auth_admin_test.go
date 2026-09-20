package repository_test

// Operator-surface persistence proof: the store calls the admin endpoints make,
// and the one behaviour that decides whether onboarding actually works — an
// account created through it can sign in.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// newAdminStore is the identity store with the operator tenant in place, which
// is what the boot path ensures before any admin request can arrive.
func newAdminStore(t *testing.T) (*repository.GORMAuthStore, repository.TenantScope) {
	t.Helper()
	store := newAuthStore(t)
	if _, err := store.EnsureTenant(context.Background(), repository.OperatorTenantID, "operator"); err != nil {
		t.Fatalf("EnsureTenant(%q) error = %v", repository.OperatorTenantID, err)
	}
	return store, repository.TenantScope{ID: repository.OperatorTenantID}
}

// hashOnce is a password hash reused across accounts that a test only needs to
// exist. PBKDF2 at the deployed work factor is deliberately slow, so hashing
// per fixture would spend seconds proving nothing.
func hashOnce(t *testing.T) string {
	t.Helper()
	hash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	return hash
}

func TestCreatingATenantTwiceIsRefused(t *testing.T) {
	store := newAuthStore(t)
	ctx := context.Background()

	created, err := store.CreateTenant(ctx, "acme", "Acme")
	if err != nil {
		t.Fatalf("CreateTenant() error = %v", err)
	}
	if created.ID != "acme" || created.Name != "Acme" {
		t.Fatalf("CreateTenant() = %#v, want the requested tenant", created)
	}

	// An operator repeating a create has to be told the ID is taken. Answering
	// with the existing row would look like a second customer had been
	// provisioned when none had.
	if _, err := store.CreateTenant(ctx, "acme", "Acme Again"); !errors.Is(err, repository.ErrAlreadyExists) {
		t.Fatalf("second CreateTenant() error = %v, want ErrAlreadyExists", err)
	}
	stored, err := store.GetTenant(ctx, "acme")
	if err != nil {
		t.Fatalf("GetTenant() error = %v", err)
	}
	if stored.Name != "Acme" {
		t.Errorf("tenant name = %q after a refused create, want it unchanged", stored.Name)
	}
}

func TestCreatingATenantWithoutANameFallsBackToTheID(t *testing.T) {
	store := newAuthStore(t)

	// A name is a label the operator can add later; refusing the create over a
	// missing one would block the provisioning path for no gain.
	created, err := store.CreateTenant(context.Background(), "birch", "  ")
	if err != nil {
		t.Fatalf("CreateTenant() error = %v", err)
	}
	if created.Name != "birch" {
		t.Errorf("tenant name = %q, want the ID", created.Name)
	}
}

func TestListingTenantsCountsEachTenantsUsers(t *testing.T) {
	store := newAuthStore(t)
	ctx := context.Background()
	hash := hashOnce(t)
	for _, id := range []string{"acme", "birch"} {
		if _, err := store.CreateTenant(ctx, id, id); err != nil {
			t.Fatalf("CreateTenant(%q) error = %v", id, err)
		}
	}
	for _, email := range []string{"one@acme.example", "two@acme.example"} {
		if _, err := store.CreateUser(ctx, repository.TenantScope{ID: "acme"}, email, "", hash); err != nil {
			t.Fatalf("CreateUser(%q) error = %v", email, err)
		}
	}

	summaries, err := store.ListTenants(ctx)
	if err != nil {
		t.Fatalf("ListTenants() error = %v", err)
	}
	counts := make(map[string]int64, len(summaries))
	for _, summary := range summaries {
		counts[summary.ID] = summary.UserCount
	}
	if counts["acme"] != 2 {
		t.Errorf("acme user count = %d, want 2", counts["acme"])
	}
	// A tenant with no accounts has to appear as zero rather than be missing
	// from a listing an operator reads to see who exists.
	if total, found := counts["birch"]; !found || total != 0 {
		t.Errorf("birch = (%d, %v), want (0, true)", total, found)
	}
}

func TestListingATenantsUsersNeverCarriesAPasswordHash(t *testing.T) {
	store := newAuthStore(t)
	ctx := context.Background()
	hash := hashOnce(t)
	if _, err := store.CreateTenant(ctx, "acme", "Acme"); err != nil {
		t.Fatalf("CreateTenant() error = %v", err)
	}
	created, err := store.CreateUser(ctx, repository.TenantScope{ID: "acme"}, "owner@acme.example", "Owner", hash)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	users, err := store.ListUsers(ctx, repository.TenantScope{ID: "acme"})
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("ListUsers() returned %d accounts, want 1", len(users))
	}
	if users[0].ID != created.ID || users[0].Email != "owner@acme.example" || users[0].Name != "Owner" {
		t.Errorf("listed account = %#v, want the created one", users[0])
	}
	// The hash is the one value that must never travel out of the store on a
	// read that is not the login lookup.
	if users[0].PasswordHash != "" {
		t.Error("ListUsers() returned a password hash")
	}

	// Another tenant's listing does not show it either.
	if other, err := store.ListUsers(ctx, repository.TenantScope{ID: repository.OperatorTenantID}); err != nil {
		t.Fatalf("ListUsers(operator) error = %v", err)
	} else if len(other) != 0 {
		t.Errorf("the operator tenant listed %d accounts, want 0", len(other))
	}
}

func TestDisablingAUserStopsItSigningInAndEnablingRestoresIt(t *testing.T) {
	store := newAuthStore(t)
	ctx := context.Background()
	hash := hashOnce(t)
	if _, err := store.CreateTenant(ctx, "acme", "Acme"); err != nil {
		t.Fatalf("CreateTenant() error = %v", err)
	}
	tenant := repository.TenantScope{ID: "acme"}
	created, err := store.CreateUser(ctx, tenant, "owner@acme.example", "Owner", hash)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	disabled, err := store.SetUserDisabled(ctx, tenant, created.ID, true)
	if err != nil {
		t.Fatalf("SetUserDisabled(true) error = %v", err)
	}
	if disabled.DisabledAt == nil {
		t.Fatal("SetUserDisabled(true) left the account enabled")
	}
	// Read back, because the session revalidation a disabled account depends on
	// reads the row rather than this call's return value.
	stored, err := store.FindUserForLogin(ctx, "owner@acme.example")
	if err != nil {
		t.Fatalf("FindUserForLogin() error = %v", err)
	}
	if stored.DisabledAt == nil {
		t.Fatal("the account is not disabled in the store")
	}

	enabled, err := store.SetUserDisabled(ctx, tenant, created.ID, false)
	if err != nil {
		t.Fatalf("SetUserDisabled(false) error = %v", err)
	}
	if enabled.DisabledAt != nil {
		t.Errorf("SetUserDisabled(false) left DisabledAt = %v", enabled.DisabledAt)
	}
	stored, err = store.FindUserForLogin(ctx, "owner@acme.example")
	if err != nil {
		t.Fatalf("FindUserForLogin() error = %v", err)
	}
	if stored.DisabledAt != nil {
		t.Errorf("the account is still disabled in the store: %v", stored.DisabledAt)
	}
}

func TestDisablingAUserInAnotherTenantIsUnknown(t *testing.T) {
	store := newAuthStore(t)
	ctx := context.Background()
	hash := hashOnce(t)
	if _, err := store.CreateTenant(ctx, "acme", "Acme"); err != nil {
		t.Fatalf("CreateTenant() error = %v", err)
	}
	created, err := store.CreateUser(ctx, repository.TenantScope{ID: "acme"}, "owner@acme.example", "Owner", hash)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	// Another tenant's account reads as unknown rather than as a refusal, so
	// the operator surface cannot be used to enumerate accounts that exist
	// outside the tenant it named.
	other := repository.TenantScope{ID: repository.OperatorTenantID}
	if _, err := store.SetUserDisabled(ctx, other, created.ID, true); !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("SetUserDisabled across tenants: error = %v, want ErrNotFound", err)
	}
	if _, err := store.SetUserPassword(ctx, other, created.ID, hash); !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("SetUserPassword across tenants: error = %v, want ErrNotFound", err)
	}
}

func TestChangingAUserPasswordReplacesTheStoredHash(t *testing.T) {
	store := newAuthStore(t)
	ctx := context.Background()
	oldHash, err := auth.HashPassword("old-password")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if _, err := store.CreateTenant(ctx, "acme", "Acme"); err != nil {
		t.Fatalf("CreateTenant() error = %v", err)
	}
	tenant := repository.TenantScope{ID: "acme"}
	created, err := store.CreateUser(ctx, tenant, "owner@acme.example", "Owner", oldHash)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	newHash, err := auth.HashPassword("new-password")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	updated, err := store.SetUserPassword(ctx, tenant, created.ID, newHash)
	if err != nil {
		t.Fatalf("SetUserPassword() error = %v", err)
	}
	if updated.PasswordHash != "" {
		t.Error("SetUserPassword returned the password hash to its caller")
	}

	found, err := store.FindUserForLogin(ctx, "owner@acme.example")
	if err != nil {
		t.Fatalf("FindUserForLogin() error = %v", err)
	}
	if !auth.MatchPassword("new-password", found.PasswordHash) {
		t.Error("the new password does not verify after the reset")
	}
	// The point of a reset is that the old password stops working.
	if auth.MatchPassword("old-password", found.PasswordHash) {
		t.Error("the old password still verifies after the reset")
	}
}

func TestAnAccountCreatedThroughTheAdminPathCanSignIn(t *testing.T) {
	store, _ := newAdminStore(t)
	ctx := context.Background()

	// The whole onboarding path, in the order the operator surface performs it:
	// a tenant that did not exist, then its first account, then a sign-in. This
	// is the behaviour the finding said could not be reached through the
	// product at all.
	if _, err := store.CreateTenant(ctx, "acme", "Acme"); err != nil {
		t.Fatalf("CreateTenant() error = %v", err)
	}
	hash, err := auth.HashPassword("acme-password")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	created, err := store.CreateUser(ctx, repository.TenantScope{ID: "acme"}, "Owner@Acme.Example", "Owner", hash)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	found, err := store.FindUserForLogin(ctx, "owner@acme.example")
	if err != nil {
		t.Fatalf("FindUserForLogin() error = %v", err)
	}
	if found.ID != created.ID || found.TenantID != "acme" {
		t.Fatalf("login resolved %#v, want the account created under acme", found)
	}
	if !auth.MatchPassword("acme-password", found.PasswordHash) {
		t.Error("the password set at creation does not verify")
	}
}

func TestAnAdminKeyMustBeShapedLikeAKey(t *testing.T) {
	store, operator := newAdminStore(t)

	// A malformed value would be a key that silently never authenticates, so
	// the boot path has to be told at registration rather than at first use.
	if _, err := store.EnsureAPIKey(context.Background(), operator, "operator", "not-a-key"); err == nil {
		t.Fatal("EnsureAPIKey accepted a token that is not shaped like an API key")
	}
}

func TestRegisteringAnAdminKeyIsIdempotentAndAuthenticatesAsTheOperator(t *testing.T) {
	store, operator := newAdminStore(t)
	ctx := context.Background()
	minted, err := auth.MintKey()
	if err != nil {
		t.Fatalf("MintKey() error = %v", err)
	}

	first, err := store.EnsureAPIKey(ctx, operator, "operator", minted.Token)
	if err != nil {
		t.Fatalf("EnsureAPIKey() error = %v", err)
	}
	if first.Prefix != minted.Prefix {
		t.Errorf("registered prefix = %q, want %q", first.Prefix, minted.Prefix)
	}
	if first.TenantID != repository.OperatorTenantID {
		t.Errorf("registered key tenant = %q, want the operator tenant", first.TenantID)
	}

	// A restart re-runs the boot path. It must not create a second row, and it
	// must not disable the key the operator already wrote down.
	second, err := store.EnsureAPIKey(ctx, operator, "operator", minted.Token)
	if err != nil {
		t.Fatalf("second EnsureAPIKey() error = %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("second registration produced key %q, want the first %q", second.ID, first.ID)
	}
	keys, err := store.ListAPIKeys(ctx, operator)
	if err != nil {
		t.Fatalf("ListAPIKeys() error = %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("operator holds %d keys, want 1", len(keys))
	}

	// The authority the admin surface checks is the tenant this resolves to.
	authenticated, err := store.AuthenticateAPIKey(ctx, minted.Token)
	if err != nil {
		t.Fatalf("AuthenticateAPIKey() error = %v", err)
	}
	if authenticated.TenantID != repository.OperatorTenantID {
		t.Errorf("authenticated tenant = %q, want %q", authenticated.TenantID, repository.OperatorTenantID)
	}
}

func TestMintingAKeyForAnotherTenantIsScopedToThatTenant(t *testing.T) {
	store, operator := newAdminStore(t)
	ctx := context.Background()
	if _, err := store.CreateTenant(ctx, "acme", "Acme"); err != nil {
		t.Fatalf("CreateTenant() error = %v", err)
	}

	// The admin endpoint mints for a tenant it names rather than the caller's
	// own, which the existing /api-keys operation cannot do.
	key, token, err := store.CreateAPIKey(ctx, repository.TenantScope{ID: "acme"}, "reference host")
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	if key.TenantID != "acme" {
		t.Fatalf("minted key tenant = %q, want acme", key.TenantID)
	}
	authenticated, err := store.AuthenticateAPIKey(ctx, token)
	if err != nil {
		t.Fatalf("AuthenticateAPIKey() error = %v", err)
	}
	if authenticated.TenantID != "acme" {
		t.Errorf("authenticated tenant = %q, want acme: the key must not carry the operator's authority",
			authenticated.TenantID)
	}
	// And the operator's own listing does not show it.
	operatorKeys, err := store.ListAPIKeys(ctx, operator)
	if err != nil {
		t.Fatalf("ListAPIKeys() error = %v", err)
	}
	if len(operatorKeys) != 0 {
		t.Errorf("the operator tenant lists %d keys, want 0", len(operatorKeys))
	}
}

func TestAPIKeyPagesWalkEveryKeyExactlyOnce(t *testing.T) {
	store := newAuthStore(t)
	ctx := context.Background()
	tenant := repository.TenantScope{ID: repository.DefaultTenantID}
	// Distinct moments rather than the wall clock, so the tiebreaker on id is
	// exercised by the ordering rather than by luck.
	moment := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	store.WithClock(func() time.Time {
		moment = moment.Add(time.Millisecond)
		return moment
	})
	minted := make([]string, 0, 5)
	for index := range 5 {
		key, _, err := store.CreateAPIKey(ctx, tenant, fmt.Sprintf("key-%d", index))
		if err != nil {
			t.Fatalf("CreateAPIKey() error = %v", err)
		}
		minted = append(minted, key.ID)
	}

	seen := make([]string, 0, len(minted))
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > len(minted)+1 {
			t.Fatal("paging did not terminate")
		}
		page, err := store.ListAPIKeysPage(ctx, tenant, repository.APIKeyFilter{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("ListAPIKeysPage() error = %v", err)
		}
		if len(page.Keys) > 2 {
			t.Fatalf("page %d returned %d keys, want at most the limit", pages, len(page.Keys))
		}
		for _, key := range page.Keys {
			seen = append(seen, key.ID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}

	// Newest first, and every key exactly once: a cursor that loses the zone of
	// the row it pins would silently return an empty second page instead.
	if len(seen) != len(minted) {
		t.Fatalf("paging returned %d keys, want %d", len(seen), len(minted))
	}
	for index, id := range seen {
		want := minted[len(minted)-1-index]
		if id != want {
			t.Fatalf("key %d = %q, want %q (newest first, no repeats)", index, id, want)
		}
	}
}

func TestAPIKeyPagesRefuseACursorTheStoreDidNotIssue(t *testing.T) {
	store := newAuthStore(t)

	for name, cursor := range map[string]string{
		"not base64":        "!!!",
		"no separator":      "bm9zZXBhcmF0b3I",
		"no identifier":     "MjAyNi0wOS0yMFQwOTowMDowMFoA",
		"unparsable moment": "bm90LWEtdGltZQBpZA",
	} {
		_, err := store.ListAPIKeysPage(context.Background(),
			repository.TenantScope{ID: repository.DefaultTenantID},
			repository.APIKeyFilter{Limit: 2, Cursor: cursor})
		if !errors.Is(err, repository.ErrInvalidCursor) {
			t.Errorf("cursor %s: error = %v, want ErrInvalidCursor", name, err)
		}
	}
}

func TestAPIKeyPagesNeverCrossTenants(t *testing.T) {
	store := newAuthStore(t)
	ctx := context.Background()
	if _, err := store.CreateTenant(ctx, "acme", "Acme"); err != nil {
		t.Fatalf("CreateTenant() error = %v", err)
	}
	if _, _, err := store.CreateAPIKey(ctx, repository.TenantScope{ID: "acme"}, "acme key"); err != nil {
		t.Fatalf("CreateAPIKey(acme) error = %v", err)
	}
	if _, _, err := store.CreateAPIKey(ctx, repository.TenantScope{ID: repository.DefaultTenantID}, "default key"); err != nil {
		t.Fatalf("CreateAPIKey(default) error = %v", err)
	}

	page, err := store.ListAPIKeysPage(ctx, repository.TenantScope{ID: "acme"}, repository.APIKeyFilter{})
	if err != nil {
		t.Fatalf("ListAPIKeysPage() error = %v", err)
	}
	if len(page.Keys) != 1 || page.Keys[0].TenantID != "acme" {
		t.Errorf("page = %#v, want acme's one key", page.Keys)
	}
}
