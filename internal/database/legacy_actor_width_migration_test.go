package database

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// legacyActorWidthMigration is looked up by its name and never by its number,
// for the reason backfillMigration gives.
const legacyActorWidthMigration = "legacy_actor_width"

// longActorMigrationLabel is a 255-character actor label, the longest the API
// accepts, and multibyte so that a byte-counting truncation would show.
var longActorMigrationLabel = strings.Repeat("ключ-", 51)

// The legacy actor copies, workflow_versions.created_by and
// workflow_publish_events.actor, must hold a 255-character label once the
// migration has run (BUG-ykxfbe). Rolling it back narrows them to 64 again on
// PostgreSQL and has to truncate a longer value first, or the rollback would
// fail on exactly the rows the migration made possible; SQLite's columns are
// text with no length, so its rollback leaves the value alone.
func TestTheLegacyActorColumnsHoldALongLabelOnSQLite(t *testing.T) {
	assertLegacyActorWidth(t, freshSQLite(t), false)
}

func TestTheLegacyActorColumnsHoldALongLabelOnPostgres(t *testing.T) {
	assertLegacyActorWidth(t, openPostgres(t), true)
}

func assertLegacyActorWidth(t *testing.T, db *DB, narrowsOnRollback bool) {
	t.Helper()
	if got := utf8.RuneCountInString(longActorMigrationLabel); got != 255 {
		t.Fatalf("test label is %d characters, want 255", got)
	}
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if narrowsOnRollback {
		longActorAssertWidth(t, db, 255)
	}
	longActorSeedRows(t, db, longActorMigrationLabel)
	createdBy, actor := longActorReadRows(t, db)
	if createdBy != longActorMigrationLabel || actor != longActorMigrationLabel {
		t.Fatalf("after Migrate created_by = %d characters and actor = %d, want both whole at 255",
			utf8.RuneCountInString(createdBy), utf8.RuneCountInString(actor))
	}

	rollBackBelow(t, db, legacyActorWidthMigration)
	createdBy, actor = longActorReadRows(t, db)
	want := longActorMigrationLabel
	if narrowsOnRollback {
		longActorAssertWidth(t, db, 64)
		want = string([]rune(longActorMigrationLabel)[:64])
	}
	if createdBy != want {
		t.Errorf("after rollback created_by = %q, want %q", createdBy, want)
	}
	if actor != want {
		t.Errorf("after rollback actor = %q, want %q", actor, want)
	}

	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate again: %v", err)
	}
	if narrowsOnRollback {
		longActorAssertWidth(t, db, 255)
	}
}

// longActorSeedRows writes one workflow, one revision and one publish event
// whose legacy actor columns hold label.
func longActorSeedRows(t *testing.T, db *DB, label string) {
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
			args: []any{"wf-long-actor", "tenant-long-actor", "long actor", false, 1, now, now},
		},
		{
			sql: fmt.Sprintf("INSERT INTO %s (%s,%s,%s,%s,%s,%s,%s,%s) VALUES (?,?,?,?,?,?,?,?)",
				quote("workflow_versions"), quote("id"), quote("tenant_id"), quote("workflow_id"),
				quote("revision"), quote("schema_version"), quote("definition"), quote("created_by"), quote("created_at")),
			args: []any{"wfv-long-actor", "tenant-long-actor", "wf-long-actor", 1, 1, []byte("{}"), label, now},
		},
		{
			sql: fmt.Sprintf("INSERT INTO %s (%s,%s,%s,%s,%s,%s,%s) VALUES (?,?,?,?,?,?,?)",
				quote("workflow_publish_events"), quote("tenant_id"), quote("workflow_id"),
				quote("version_id"), quote("action"), quote("actor"), quote("reason"), quote("created_at")),
			args: []any{"tenant-long-actor", "wf-long-actor", "wfv-long-actor", "published", label, "", now},
		},
	}
	for _, statement := range statements {
		if err := db.Exec(statement.sql, statement.args...).Error; err != nil {
			t.Fatalf("seed a row with a 255-character actor: %v", err)
		}
	}
}

// longActorReadRows reads back the two legacy actor values longActorSeedRows
// wrote.
//
// Each value is cast to text because the same query runs before and after the
// column's type changes: pgx caches the prepared statement, and PostgreSQL
// refuses to reuse a plan whose result type went from varchar(255) to
// varchar(64) ("cached plan must not change result type"). text has no
// length, so the cast keeps the result type the same on both sides.
func longActorReadRows(t *testing.T, db *DB) (createdBy, actor string) {
	t.Helper()
	quote := func(name string) string { return quoteIdentifier(db.Dialector.Name(), name) }
	if err := db.Raw(fmt.Sprintf("SELECT CAST(%s AS text) FROM %s WHERE %s = ?",
		quote("created_by"), quote("workflow_versions"), quote("id")), "wfv-long-actor").
		Row().Scan(&createdBy); err != nil {
		t.Fatalf("read created_by: %v", err)
	}
	if err := db.Raw(fmt.Sprintf("SELECT CAST(%s AS text) FROM %s WHERE %s = ?",
		quote("actor"), quote("workflow_publish_events"), quote("version_id")), "wfv-long-actor").
		Row().Scan(&actor); err != nil {
		t.Fatalf("read actor: %v", err)
	}
	return createdBy, actor
}

// longActorAssertWidth checks the declared varchar length of both legacy
// actor columns. Only meaningful on PostgreSQL: SQLite declares them text.
func longActorAssertWidth(t *testing.T, db *DB, want int64) {
	t.Helper()
	for table, column := range map[string]string{
		"workflow_versions":       "created_by",
		"workflow_publish_events": "actor",
	} {
		types, err := db.Migrator().ColumnTypes(table)
		if err != nil {
			t.Fatalf("ColumnTypes(%s): %v", table, err)
		}
		found := false
		for _, columnType := range types {
			if columnType.Name() != column {
				continue
			}
			found = true
			length, ok := columnType.Length()
			if !ok || length != want {
				t.Errorf("%s.%s length = %d (reported %v), want %d", table, column, length, ok, want)
			}
		}
		if !found {
			t.Errorf("%s has no %s column", table, column)
		}
	}
}
