package database

import (
	"fmt"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/migrations"
)

// The actor columns are new, so the rows that predate them have no value in
// them, and something has to say what an absent value means. The rule is the
// legacy label: created_by and actor are the only identity the old schema ever
// recorded, and only a signed-in person could have written one. A row with no
// label stays NULL rather than being called 'user', because an invented author
// in an audit trail is worse than an absent one.
//
// The migration is exercised the way a deployment meets it: a schema with the
// old columns, rows in it, and the migration applied over them. Rolling back
// first and re-applying is what makes the test able to write a row the
// migration has not seen yet — the same cycle an operator would use to test a
// rollback plan.
func TestTheActorColumnsAreBackfilledFromTheLegacyLabelOnSQLite(t *testing.T) {
	db := freshSQLite(t)
	assertActorBackfill(t, db)
}

func TestTheActorColumnsAreBackfilledFromTheLegacyLabelOnPostgres(t *testing.T) {
	db := openPostgres(t)
	assertActorBackfill(t, db)
}

func assertActorBackfill(t *testing.T, db *DB) {
	t.Helper()
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	seedPreActorRows(t, db)

	// Roll the actor migration back so the rows keep their legacy labels and
	// lose only the columns being tested. Anything newer has to come off first
	// because Rollback reverts the newest applied version, so this rolls back
	// by name rather than by count: a later migration must not quietly leave
	// the actor columns in place.
	actor := versionNamed(t, db.Dialector.Name(), "workflow_actor")
	all, err := loadMigrations(migrations.FS, db.Dialector.Name())
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	// Bounded by the number of migrations, so a Rollback that stops making
	// progress fails here instead of hanging the suite.
	for range all {
		applied, err := appliedVersions(db)
		if err != nil {
			t.Fatalf("appliedVersions: %v", err)
		}
		if highestVersion(applied) < actor {
			break
		}
		if err := Rollback(db, discardLogger()); err != nil {
			t.Fatalf("Rollback down to workflow_actor: %v", err)
		}
	}
	for _, column := range []string{"actor_kind", "actor_label", "actor_key_id", "actor_meta"} {
		if db.Migrator().HasColumn("workflow_versions", column) {
			t.Fatalf("rollback left workflow_versions.%s behind", column)
		}
	}
	for _, column := range []string{"actor_kind", "actor_label", "actor_key_id"} {
		if db.Migrator().HasColumn("workflow_publish_events", column) {
			t.Fatalf("rollback left workflow_publish_events.%s behind", column)
		}
	}

	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate after Rollback: %v", err)
	}

	t.Run("a labelled revision reads as the person who wrote it", func(t *testing.T) {
		kind, label, keyID := revisionActor(t, db, "wfv-labelled")
		if kind != "user" {
			t.Errorf("actor kind = %q, want user", kind)
		}
		if label != "ada@example.com" {
			t.Errorf("actor label = %q, want the legacy label", label)
		}
		if keyID != "" {
			t.Errorf("actor key id = %q, want none: no column ever held one", keyID)
		}
	})

	t.Run("an unlabelled revision is left unattributed", func(t *testing.T) {
		kind, label, _ := revisionActor(t, db, "wfv-anonymous")
		if kind != "" {
			t.Errorf("actor kind = %q, want none rather than a guessed user", kind)
		}
		if label != "" {
			t.Errorf("actor label = %q, want none", label)
		}
	})

	t.Run("a publish reads as the person who acted", func(t *testing.T) {
		kind, label, keyID := publishEventActor(t, db)
		if kind != "user" {
			t.Errorf("actor kind = %q, want user", kind)
		}
		if label != "ada@example.com" {
			t.Errorf("actor label = %q, want the legacy label", label)
		}
		if keyID != "" {
			t.Errorf("actor key id = %q, want none", keyID)
		}
	})
}

// seedPreActorRows writes rows the way the schema allowed before this
// migration: an identity in the legacy label column, and no actor columns.
func seedPreActorRows(t *testing.T, db *DB) {
	t.Helper()
	quote := func(name string) string { return quoteIdentifier(db.Dialector.Name(), name) }
	now := time.Now().UTC()
	statements := []struct {
		sql  string
		args []any
	}{
		{
			sql: fmt.Sprintf("INSERT INTO %s (%s,%s,%s,%s,%s,%s,%s) VALUES (?,?,?,?,?,?,?)",
				quote("workflows"), quote("id"), quote("tenant_id"), quote("name"),
				quote("active"), quote("latest_revision"), quote("created_at"), quote("updated_at")),
			args: []any{"wf-actor", "tenant-actor", "before attribution", false, 2, now, now},
		},
		{
			sql: fmt.Sprintf("INSERT INTO %s (%s,%s,%s,%s,%s,%s,%s,%s) VALUES (?,?,?,?,?,?,?,?)",
				quote("workflow_versions"), quote("id"), quote("tenant_id"), quote("workflow_id"),
				quote("revision"), quote("schema_version"), quote("definition"), quote("created_by"), quote("created_at")),
			args: []any{"wfv-labelled", "tenant-actor", "wf-actor", 1, 1, []byte("{}"), "ada@example.com", now},
		},
		{
			sql: fmt.Sprintf("INSERT INTO %s (%s,%s,%s,%s,%s,%s,%s) VALUES (?,?,?,?,?,?,?)",
				quote("workflow_versions"), quote("id"), quote("tenant_id"), quote("workflow_id"),
				quote("revision"), quote("schema_version"), quote("definition"), quote("created_at")),
			args: []any{"wfv-anonymous", "tenant-actor", "wf-actor", 2, 1, []byte("{}"), now},
		},
		{
			sql: fmt.Sprintf("INSERT INTO %s (%s,%s,%s,%s,%s,%s,%s) VALUES (?,?,?,?,?,?,?)",
				quote("workflow_publish_events"), quote("tenant_id"), quote("workflow_id"),
				quote("version_id"), quote("action"), quote("actor"), quote("reason"), quote("created_at")),
			args: []any{"tenant-actor", "wf-actor", "wfv-labelled", "published", "ada@example.com", "", now},
		},
	}
	for _, statement := range statements {
		if err := db.Exec(statement.sql, statement.args...).Error; err != nil {
			t.Fatalf("seed a pre-attribution row: %v", err)
		}
	}
}

// revisionActor reads one revision's actor columns back, with an empty string
// standing for NULL: absent is the answer this test is about.
func revisionActor(t *testing.T, db *DB, id string) (kind, label, keyID string) {
	t.Helper()
	var row struct {
		Kind  *string
		Label *string
		KeyID *string
	}
	if err := db.Raw(fmt.Sprintf("SELECT %s AS kind, %s AS label, %s AS key_id FROM %s WHERE %s = ?",
		quoteIdentifier(db.Dialector.Name(), "actor_kind"),
		quoteIdentifier(db.Dialector.Name(), "actor_label"),
		quoteIdentifier(db.Dialector.Name(), "actor_key_id"),
		quoteIdentifier(db.Dialector.Name(), "workflow_versions"),
		quoteIdentifier(db.Dialector.Name(), "id")), id).
		Scan(&row).Error; err != nil {
		t.Fatalf("read revision %s's actor: %v", id, err)
	}
	return deref(row.Kind), deref(row.Label), deref(row.KeyID)
}

func publishEventActor(t *testing.T, db *DB) (kind, label, keyID string) {
	t.Helper()
	var row struct {
		Kind  string
		Label string
		KeyID string
	}
	if err := db.Raw(fmt.Sprintf("SELECT %s AS kind, %s AS label, %s AS key_id FROM %s",
		quoteIdentifier(db.Dialector.Name(), "actor_kind"),
		quoteIdentifier(db.Dialector.Name(), "actor_label"),
		quoteIdentifier(db.Dialector.Name(), "actor_key_id"),
		quoteIdentifier(db.Dialector.Name(), "workflow_publish_events"))).
		Scan(&row).Error; err != nil {
		t.Fatalf("read the publish event's actor: %v", err)
	}
	return row.Kind, row.Label, row.KeyID
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
