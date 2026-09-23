package database

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/migrations"
)

// lifecycleStateMigration is looked up by its name and never by its number,
// for the reason backfillMigration gives.
const lifecycleStateMigration = "webhook_route_lifecycle_state"

// rollBackBelow reverts migrations, newest first, until the one named is no
// longer applied. By name rather than by a count of calls: a count is right
// only until the next migration lands, and then it stops one short and the
// test goes on to read a schema it did not mean to.
func rollBackBelow(t *testing.T, db *DB, name string) {
	t.Helper()
	target := versionNamed(t, db.Dialector.Name(), name)
	all, err := loadMigrations(migrations.FS, db.Dialector.Name())
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	// Bounded by the number of migrations, so a Rollback that stops making
	// progress fails here instead of hanging the suite.
	for range len(all) + 1 {
		applied, err := appliedVersions(db)
		if err != nil {
			t.Fatalf("appliedVersions: %v", err)
		}
		if highestVersion(applied) < target {
			return
		}
		if err := Rollback(db, discardLogger()); err != nil {
			t.Fatalf("Rollback towards %s: %v", name, err)
		}
	}
	t.Fatalf("rolling back %d times left %s applied", len(all)+1, name)
}

// A route keeps what its trigger's registration answered with, sealed, in a
// column of its own. The column is nullable because every route minted before
// it — and every route whose trigger captures nothing — has no state, and a
// rollback drops the column while the route, and so the public URL a sender is
// configured with, stays.
func TestTheRouteLifecycleStateColumnComesAndGoesWithItsMigrationOnSQLite(t *testing.T) {
	assertLifecycleStateMigration(t, freshSQLite(t))
}

func TestTheRouteLifecycleStateColumnComesAndGoesWithItsMigrationOnPostgres(t *testing.T) {
	assertLifecycleStateMigration(t, openPostgres(t))
}

func assertLifecycleStateMigration(t *testing.T, db *DB) {
	t.Helper()
	quote := func(name string) string { return quoteIdentifier(db.Dialector.Name(), name) }
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	rollBackBelow(t, db, lifecycleStateMigration)
	if db.Migrator().HasColumn("webhook_routes", "lifecycle_state") {
		t.Fatal("rolling the migration back left webhook_routes.lifecycle_state behind")
	}
	if err := db.Exec(fmt.Sprintf("INSERT INTO %s (%s,%s,%s,%s,%s) VALUES (?,?,?,?,?)",
		quote("webhook_routes"), quote("tenant_id"), quote("workflow_id"), quote("node_id"), quote("route"), quote("created_at")),
		"tenant-state", "wf-state", "hook", "route-before", time.Now().UTC()).Error; err != nil {
		t.Fatalf("seed a route minted before the column: %v", err)
	}

	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate over a route with no state: %v", err)
	}
	if !db.Migrator().HasColumn("webhook_routes", "lifecycle_state") {
		t.Fatal("the migration did not add webhook_routes.lifecycle_state")
	}
	readState := func() []byte {
		t.Helper()
		var state []byte
		if err := db.Raw(fmt.Sprintf("SELECT %s FROM %s WHERE %s = ?", quote("lifecycle_state"), quote("webhook_routes"), quote("route")),
			"route-before").Row().Scan(&state); err != nil {
			t.Fatalf("read the route's state: %v", err)
		}
		return state
	}
	if state := readState(); state != nil {
		t.Fatalf("a route minted before the column has state %q, want NULL", state)
	}
	sealed := []byte{0x00, 0x9f, 0xff, 'x'}
	if err := db.Exec(fmt.Sprintf("UPDATE %s SET %s = ? WHERE %s = ?", quote("webhook_routes"), quote("lifecycle_state"), quote("route")),
		sealed, "route-before").Error; err != nil {
		t.Fatalf("write sealed state: %v", err)
	}
	if state := readState(); !bytes.Equal(state, sealed) {
		t.Fatalf("state read back = %v, want the bytes written, %v", state, sealed)
	}

	rollBackBelow(t, db, lifecycleStateMigration)
	var routes int64
	if err := db.Table("webhook_routes").Where("route = ?", "route-before").Count(&routes).Error; err != nil {
		t.Fatalf("count routes: %v", err)
	}
	if routes != 1 {
		t.Fatalf("routes after the rollback = %d, want the route kept", routes)
	}
}
