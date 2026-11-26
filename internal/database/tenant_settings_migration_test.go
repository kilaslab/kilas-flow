package database

import (
	"fmt"
	"testing"
	"time"
)

// tenantSettingsMigration is looked up by its name and never by its number,
// for the reason backfillMigration gives.
const tenantSettingsMigration = "tenant_settings"

// A tenant's settings are two columns on tenants: a nullable document and a
// version that defaults to 0. A tenant row written without them reads as no
// settings at version 0, rolling back removes both columns and keeps the row,
// and migrating again restores them.
func TestTheTenantSettingsColumnsComeAndGoWithTheirMigrationOnSQLite(t *testing.T) {
	assertTenantSettingsMigration(t, freshSQLite(t))
}

func TestTheTenantSettingsColumnsComeAndGoWithTheirMigrationOnPostgres(t *testing.T) {
	assertTenantSettingsMigration(t, openPostgres(t))
}

func assertTenantSettingsMigration(t *testing.T, db *DB) {
	t.Helper()
	quote := func(name string) string { return quoteIdentifier(db.Dialector.Name(), name) }
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	for _, column := range []string{"settings", "settings_version"} {
		if !db.Migrator().HasColumn("tenants", column) {
			t.Fatalf("the migration did not add tenants.%s", column)
		}
	}
	const id = "tenant-settings-migration"
	t.Cleanup(func() { db.Exec(fmt.Sprintf("DELETE FROM %s WHERE %s = ?", quote("tenants"), quote("id")), id) })
	now := time.Now().UTC()
	if err := db.Exec(fmt.Sprintf("INSERT INTO %s (%s,%s,%s,%s) VALUES (?,?,?,?)",
		quote("tenants"), quote("id"), quote("name"), quote("created_at"), quote("updated_at")),
		id, "Settings migration", now, now).Error; err != nil {
		t.Fatalf("insert a tenant without the settings columns: %v", err)
	}
	var settings []byte
	var version int64
	if err := db.Raw(fmt.Sprintf("SELECT %s, %s FROM %s WHERE %s = ?", quote("settings"), quote("settings_version"),
		quote("tenants"), quote("id")), id).Row().Scan(&settings, &version); err != nil {
		t.Fatalf("read the row back: %v", err)
	}
	if settings != nil || version != 0 {
		t.Fatalf("defaults = %q, %d, want NULL and 0", settings, version)
	}

	rollBackBelow(t, db, tenantSettingsMigration)
	for _, column := range []string{"settings", "settings_version"} {
		if db.Migrator().HasColumn("tenants", column) {
			t.Fatalf("rolling the migration back left tenants.%s behind", column)
		}
	}
	var count int64
	if err := db.Raw(fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = ?", quote("tenants"), quote("id")), id).
		Row().Scan(&count); err != nil || count != 1 {
		t.Fatalf("after rollback the tenant row count = %d, %v; want 1", count, err)
	}
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate again: %v", err)
	}
	for _, column := range []string{"settings", "settings_version"} {
		if !db.Migrator().HasColumn("tenants", column) {
			t.Fatalf("migrating again did not restore tenants.%s", column)
		}
	}
}
