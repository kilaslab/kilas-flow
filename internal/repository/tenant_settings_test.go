package repository_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// settingsTenant creates a tenant for one settings test, under the drv- prefix
// the PostgreSQL half of eachDriver cleans up.
func settingsTenant(t *testing.T, db *database.DB, id string) *repository.GORMAuthStore {
	t.Helper()
	store := repository.NewAuthStore(db.DB)
	if _, err := store.CreateTenant(context.Background(), id, "Settings "+id); err != nil {
		t.Fatalf("CreateTenant(%s) error = %v", id, err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM tenants WHERE id = ?`, id) })
	return store
}

func settingsSetTo(doc string) func([]byte) ([]byte, error) {
	return func([]byte) ([]byte, error) { return []byte(doc), nil }
}

func TestTenantSettingsStartEmptyAtVersionZero(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		store := settingsTenant(t, db, "drv-settings-empty")
		doc, version, err := store.ReadTenantSettings(ctx, "drv-settings-empty")
		if err != nil || doc != nil || version != 0 {
			t.Errorf("ReadTenantSettings() = %q, %d, %v; want nil, 0, nil", doc, version, err)
		}
		doc, version, err = store.ReadTenantSettings(ctx, "drv-settings-nobody")
		if err != nil || doc != nil || version != 0 {
			t.Errorf("unknown tenant ReadTenantSettings() = %q, %d, %v; want nil, 0, nil", doc, version, err)
		}
		tenant, err := store.GetTenant(ctx, "drv-settings-empty")
		if err != nil || tenant.Settings != nil || tenant.SettingsVersion != 0 {
			t.Errorf("GetTenant() = %+v, %v", tenant, err)
		}
	})
}

func TestTenantSettingsWritesAndBumpsTheVersion(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		const id = "drv-settings-write"
		store := settingsTenant(t, db, id)
		updated, err := store.UpdateTenantSettings(ctx, id, settingsSetTo(`{"rateLimit":{"runPerMinute":5}}`))
		if err != nil {
			t.Fatalf("UpdateTenantSettings() error = %v", err)
		}
		if string(updated.Settings) != `{"rateLimit":{"runPerMinute":5}}` || updated.SettingsVersion != 1 {
			t.Errorf("updated = %q v%d", updated.Settings, updated.SettingsVersion)
		}
		updated, err = store.UpdateTenantSettings(ctx, id, settingsSetTo(`{"rateLimit":{"runPerMinute":6}}`))
		if err != nil || updated.SettingsVersion != 2 {
			t.Fatalf("second update = v%d, %v; want v2", updated.SettingsVersion, err)
		}
		doc, version, err := store.ReadTenantSettings(ctx, id)
		if err != nil || string(doc) != `{"rateLimit":{"runPerMinute":6}}` || version != 2 {
			t.Errorf("ReadTenantSettings() = %q, %d, %v", doc, version, err)
		}
		tenant, err := store.GetTenant(ctx, id)
		if err != nil || string(tenant.Settings) != string(doc) || tenant.SettingsVersion != 2 {
			t.Errorf("GetTenant() = %+v, %v", tenant, err)
		}
		summaries, err := store.ListTenants(ctx)
		if err != nil {
			t.Fatalf("ListTenants() error = %v", err)
		}
		found := false
		for _, summary := range summaries {
			if summary.ID == id {
				found = true
				if string(summary.Settings) != string(doc) || summary.SettingsVersion != 2 {
					t.Errorf("ListTenants() row = %+v", summary.Tenant)
				}
			}
		}
		if !found {
			t.Error("ListTenants() is missing the tenant")
		}
		// Emptying the document stores NULL and still counts as a change.
		updated, err = store.UpdateTenantSettings(ctx, id, settingsSetTo(""))
		if err != nil || updated.Settings != nil || updated.SettingsVersion != 3 {
			t.Errorf("emptied = %q v%d, %v", updated.Settings, updated.SettingsVersion, err)
		}
		doc, version, _ = store.ReadTenantSettings(ctx, id)
		if doc != nil || version != 3 {
			t.Errorf("after emptying ReadTenantSettings() = %q, %d; want nil, 3", doc, version)
		}
	})
}

func TestTenantSettingsSkipsANoOpWrite(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		const id = "drv-settings-noop"
		store := settingsTenant(t, db, id)
		if _, err := store.UpdateTenantSettings(ctx, id, settingsSetTo(`{"a":{}}`)); err != nil {
			t.Fatal(err)
		}
		same, err := store.UpdateTenantSettings(ctx, id, func(current []byte) ([]byte, error) { return current, nil })
		if err != nil || same.SettingsVersion != 1 {
			t.Errorf("no-op update = v%d, %v; want v1", same.SettingsVersion, err)
		}
	})
}

func TestTenantSettingsRetriesWhenAnotherWriterWins(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		const id = "drv-settings-race"
		store := settingsTenant(t, db, id)
		calls := 0
		updated, err := store.UpdateTenantSettings(ctx, id, func(current []byte) ([]byte, error) {
			calls++
			if calls == 1 {
				// Another writer lands between this read and this write.
				if _, err := store.UpdateTenantSettings(ctx, id, settingsSetTo(`{"other":{}}`)); err != nil {
					t.Fatalf("nested update error = %v", err)
				}
			}
			return append([]byte(nil), `{"mine":{}}`...), nil
		})
		if err != nil {
			t.Fatalf("UpdateTenantSettings() error = %v", err)
		}
		if calls != 2 || updated.SettingsVersion != 2 || string(updated.Settings) != `{"mine":{}}` {
			t.Errorf("apply calls = %d, updated = %q v%d; want 2 calls and v2", calls, updated.Settings, updated.SettingsVersion)
		}
	})
}

func TestTenantSettingsGiveUpWhenEveryAttemptLosesTheRace(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		const id = "drv-settings-contention"
		store := settingsTenant(t, db, id)
		calls := 0
		_, err := store.UpdateTenantSettings(ctx, id, func([]byte) ([]byte, error) {
			calls++
			// Another writer bumps the version between every read and write.
			if err := db.Exec(`UPDATE tenants SET settings_version = settings_version + 1 WHERE id = ?`, id).Error; err != nil {
				t.Fatalf("bump the version: %v", err)
			}
			return []byte(`{"mine":{}}`), nil
		})
		if !errors.Is(err, repository.ErrSettingsContention) {
			t.Fatalf("error = %v, want ErrSettingsContention", err)
		}
		if calls != 5 {
			t.Errorf("apply calls = %d, want 5 attempts", calls)
		}
		doc, _, _ := store.ReadTenantSettings(ctx, id)
		if doc != nil {
			t.Errorf("a lost race wrote %q", doc)
		}
	})
}

func TestTenantSettingsReportsAnUnknownTenant(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		store := repository.NewAuthStore(db.DB)
		_, err := store.UpdateTenantSettings(context.Background(), "drv-settings-missing", settingsSetTo(`{}`))
		if !errors.Is(err, repository.ErrNotFound) {
			t.Errorf("error = %v, want ErrNotFound", err)
		}
	})
}

func TestTenantSettingsWritesNothingWhenApplyRefuses(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		const id = "drv-settings-refused"
		store := settingsTenant(t, db, id)
		refusal := errors.New("refused")
		_, err := store.UpdateTenantSettings(ctx, id, func([]byte) ([]byte, error) { return nil, refusal })
		if !errors.Is(err, refusal) {
			t.Errorf("error = %v, want the apply error wrapped", err)
		}
		doc, version, _ := store.ReadTenantSettings(ctx, id)
		if doc != nil || version != 0 {
			t.Errorf("after a refusal = %q, %d; want nil, 0", doc, version)
		}
	})
}
