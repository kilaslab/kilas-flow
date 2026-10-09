package database

import (
	"fmt"
	"testing"
	"time"
)

// credentialEventsMigration is looked up by its name and never by its number,
// for the reason backfillMigration gives.
const credentialEventsMigration = "credential_events"

// Credential writes, tests and connects leave an audit row. The table is
// tenant-scoped like workflow_publish_events and comes and goes with its
// migration; every descriptive column defaults to empty, so a row written
// with only what is known still lands.
func TestTheCredentialEventsTableComesAndGoesWithItsMigrationOnSQLite(t *testing.T) {
	assertCredentialEventsMigration(t, freshSQLite(t))
}

func TestTheCredentialEventsTableComesAndGoesWithItsMigrationOnPostgres(t *testing.T) {
	assertCredentialEventsMigration(t, openPostgres(t))
}

func assertCredentialEventsMigration(t *testing.T, db *DB) {
	t.Helper()
	quote := func(name string) string { return quoteIdentifier(db.Dialector.Name(), name) }
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !db.Migrator().HasTable("credential_events") {
		t.Fatal("the migration did not create credential_events")
	}
	if !db.Migrator().HasIndex("credential_events", "idx_credential_events_credential") {
		t.Fatal("credential_events has no (tenant_id, credential_id, created_at) index")
	}
	if err := db.Exec(fmt.Sprintf("INSERT INTO %s (%s,%s,%s,%s) VALUES (?,?,?,?)",
		quote("credential_events"), quote("tenant_id"), quote("credential_id"), quote("action"), quote("created_at")),
		"tenant-events", "cred_1", "created", time.Now().UTC()).Error; err != nil {
		t.Fatalf("insert a row with only the required columns: %v", err)
	}
	var label, detail string
	if err := db.Raw(fmt.Sprintf("SELECT %s, %s FROM %s WHERE %s = ?", quote("actor_label"), quote("detail"),
		quote("credential_events"), quote("tenant_id")), "tenant-events").Row().Scan(&label, &detail); err != nil {
		t.Fatalf("read the row back: %v", err)
	}
	if label != "" || detail != "" {
		t.Fatalf("defaults = %q, %q, want empty strings", label, detail)
	}

	rollBackBelow(t, db, credentialEventsMigration)
	if db.Migrator().HasTable("credential_events") {
		t.Fatal("rolling the migration back left credential_events behind")
	}
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate again: %v", err)
	}
	if !db.Migrator().HasTable("credential_events") {
		t.Fatal("migrating again did not recreate credential_events")
	}
}
