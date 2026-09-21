package database

import (
	"io/fs"
	"path"
	"testing"
	"testing/fstest"
	"time"

	"github.com/kilaslab/kilas-flow/migrations"
)

// The two migrations that gave webhook_deliveries and datastore_columns a
// tenant, named rather than numbered.
//
// Every lookup here is by NAME, never by version: these files are numbered by
// whoever lands them, and a parallel branch that takes the same number forces a
// renumber. A test that spelled 16 and 17 would then either fail for the wrong
// reason or, worse, exclude a different migration and pass.
const (
	webhookDeliveriesTenantMigration = "webhook_deliveries_tenant"
	datastoreColumnsTenantMigration  = "datastore_columns_tenant"
)

// migrationsWithout returns the embedded migrations of one dialect minus the
// named ones, in the shape migrateFS takes.
//
// It refuses to run when a name matches nothing, because an exclusion that
// silently excludes nothing turns the whole test into a fresh install and the
// backfill under test never runs.
func migrationsWithout(t *testing.T, dialect string, names ...string) fstest.MapFS {
	t.Helper()
	excluded := map[string]bool{}
	for _, name := range names {
		excluded[name] = false
	}
	entries, err := fs.ReadDir(migrations.FS, dialect)
	if err != nil {
		t.Fatalf("read the embedded %s migrations: %v", dialect, err)
	}
	kept := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		_, name, _, err := parseMigrationName(entry.Name())
		if err != nil {
			t.Fatalf("%s/%s: %v", dialect, entry.Name(), err)
		}
		if _, drop := excluded[name]; drop {
			excluded[name] = true
			continue
		}
		body, err := fs.ReadFile(migrations.FS, path.Join(dialect, entry.Name()))
		if err != nil {
			t.Fatalf("read %s/%s: %v", dialect, entry.Name(), err)
		}
		kept[path.Join(dialect, entry.Name())] = &fstest.MapFile{Data: body}
	}
	for name, found := range excluded {
		if !found {
			t.Fatalf("there is no %s migration named %q to leave out", dialect, name)
		}
	}
	return kept
}

// versionNamed finds a migration's version by its name, which is what a test
// that must not care about numbering has to go through.
func versionNamed(t *testing.T, dialect, name string) int64 {
	t.Helper()
	all, err := loadMigrations(migrations.FS, dialect)
	if err != nil {
		t.Fatalf("loadMigrations(%s): %v", dialect, err)
	}
	for _, one := range all {
		if one.name == name {
			return one.version
		}
	}
	t.Fatalf("there is no %s migration named %q", dialect, name)
	return 0
}

// seedBeforeTenantColumns writes the rows an install had while
// webhook_deliveries and datastore_columns carried no tenant.
//
// Every delivery is one that names its owner through a different route, so the
// backfill's COALESCE has to walk each branch:
//
//	r-exec       -> the execution it queued          (ta)
//	routeb       -> a webhook_routes row             (tb)
//	routec       -> a webhook_bindings row, by route (tc)
//	legacy-path  -> a binding with no route, by path (td)
//	orphan       -> nothing: no execution, no route, no binding
//
// Raw SQL with unquoted names on purpose: the repository models already carry
// the new columns, so a fixture built through them would not describe a legacy
// install at all.
func seedBeforeTenantColumns(t *testing.T, db *DB) {
	t.Helper()
	exec := func(query string, args ...any) {
		t.Helper()
		if err := db.Exec(query, args...).Error; err != nil {
			t.Fatalf("seed %q: %v", query, err)
		}
	}
	now := time.Now().UTC()
	expires := now.Add(5 * time.Minute)
	document := []byte("{}")

	exec(`INSERT INTO workflows (id, tenant_id, name, latest_revision, created_at, updated_at) VALUES ('wa', 'ta', 'wf', 1, ?, ?)`, now, now)
	exec(`INSERT INTO workflow_versions (id, tenant_id, workflow_id, revision, schema_version, definition, created_at) VALUES ('va', 'ta', 'wa', 1, 1, ?, ?)`, document, now)
	exec(`INSERT INTO executions (id, tenant_id, workflow_id, workflow_version_id, status, "trigger", input, output, error, started_at) VALUES ('e1', 'ta', 'wa', 'va', 'succeeded', 'manual', ?, ?, ?, ?)`,
		document, document, document, now)

	exec(`INSERT INTO webhook_routes (tenant_id, workflow_id, node_id, route, created_at) VALUES ('tb', 'wb', 'n1', 'routeb', ?)`, now)
	exec(`INSERT INTO webhook_bindings (tenant_id, workflow_id, workflow_version_id, node_id, node_type, method, route, path, parameters, created_at) VALUES ('tc', 'wc', 'vc', 'n1', '', 'POST', 'routec', 'pc', ?, ?)`, document, now)
	exec(`INSERT INTO webhook_bindings (tenant_id, workflow_id, workflow_version_id, node_id, node_type, method, route, path, parameters, created_at) VALUES ('td', 'wd', 'vd', 'n1', '', 'POST', '', 'legacy-path', ?, ?)`, document, now)

	for _, delivery := range []struct{ route, id, execution string }{
		{"r-exec", "d1", "e1"},
		{"routeb", "d2", ""},
		{"routec", "d3", ""},
		{"legacy-path", "d4", ""},
		{"orphan", "d5", ""},
	} {
		exec(`INSERT INTO webhook_deliveries (route, delivery_id, execution_id, expires_at, created_at) VALUES (?, ?, ?, ?, ?)`,
			delivery.route, delivery.id, delivery.execution, expires, now)
	}

	exec(`INSERT INTO datastores (id, tenant_id, name, surrogate, created_at, updated_at) VALUES ('dsa', 'ta', 'first', '0000000000000001', ?, ?)`, now, now)
	exec(`INSERT INTO datastores (id, tenant_id, name, surrogate, created_at, updated_at) VALUES ('dsb', 'tb', 'second', '0000000000000002', ?, ?)`, now, now)
	exec(`INSERT INTO datastore_columns (datastore_id, name, type, position) VALUES ('dsa', 'title', 'string', 0)`)
	exec(`INSERT INTO datastore_columns (datastore_id, name, type, position) VALUES ('dsb', 'title', 'string', 0)`)
}

// assertTenantColumnBackfill runs the two migrations over the legacy rows
// above and checks what they made of them.
func assertTenantColumnBackfill(t *testing.T, db *DB) {
	t.Helper()
	dialect := db.Dialector.Name()

	if err := migrateFS(db, migrationsWithout(t, dialect, webhookDeliveriesTenantMigration, datastoreColumnsTenantMigration), discardLogger()); err != nil {
		t.Fatalf("migrate to the schema before the tenant columns: %v", err)
	}
	for _, table := range []string{"webhook_deliveries", "datastore_columns"} {
		if db.Migrator().HasColumn(table, "tenant_id") {
			t.Fatalf("%s already has a tenant_id column before its migration ran: the exclusion left it in", table)
		}
	}
	seedBeforeTenantColumns(t, db)

	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate over the legacy rows: %v", err)
	}

	type deliveryRow struct {
		Route    string
		TenantID string
	}
	var deliveries []deliveryRow
	if err := db.Raw(`SELECT route, tenant_id FROM webhook_deliveries ORDER BY route`).Scan(&deliveries).Error; err != nil {
		t.Fatalf("read the backfilled deliveries: %v", err)
	}
	want := map[string]string{"r-exec": "ta", "routeb": "tb", "routec": "tc", "legacy-path": "td"}
	if len(deliveries) != len(want) {
		t.Errorf("deliveries after the backfill = %+v, want exactly %d rows and no orphan", deliveries, len(want))
	}
	for _, row := range deliveries {
		owner, known := want[row.Route]
		if !known {
			t.Errorf("delivery %q survived the backfill, want it deleted: nothing names its tenant", row.Route)
			continue
		}
		if row.TenantID != owner {
			t.Errorf("delivery %q got tenant %q, want %q", row.Route, row.TenantID, owner)
		}
	}

	type columnRow struct {
		DatastoreID string
		TenantID    string
	}
	var columns []columnRow
	if err := db.Raw(`SELECT datastore_id, tenant_id FROM datastore_columns ORDER BY datastore_id`).Scan(&columns).Error; err != nil {
		t.Fatalf("read the backfilled columns: %v", err)
	}
	wantColumns := map[string]string{"dsa": "ta", "dsb": "tb"}
	if len(columns) != len(wantColumns) {
		t.Errorf("datastore columns after the backfill = %+v, want %d rows", columns, len(wantColumns))
	}
	for _, row := range columns {
		if row.TenantID != wantColumns[row.DatastoreID] {
			t.Errorf("column of %q got tenant %q, want its datastore's %q", row.DatastoreID, row.TenantID, wantColumns[row.DatastoreID])
		}
	}

	// The whole point of the backfill: nothing is left that a tenant purge
	// could never reach.
	for _, table := range []string{"webhook_deliveries", "datastore_columns"} {
		var empty int64
		if err := db.Raw(`SELECT COUNT(*) FROM ` + table + ` WHERE tenant_id = ''`).Scan(&empty).Error; err != nil {
			t.Fatalf("count the empty tenants in %s: %v", table, err)
		}
		if empty != 0 {
			t.Errorf("%s holds %d rows with an empty tenant_id after the backfill, want none", table, empty)
		}
	}

	for table, index := range map[string]string{
		"webhook_deliveries": "idx_webhook_deliveries_tenant",
		"datastore_columns":  "idx_datastore_columns_tenant",
	} {
		if !db.Migrator().HasIndex(table, index) {
			t.Errorf("migrating did not create %s on %s, so the tenant purge and the scoped read scan the table", index, table)
		}
	}
}

func TestTenantColumnMigrationsBackfillExistingRows(t *testing.T) {
	assertTenantColumnBackfill(t, freshSQLite(t))
}

func TestTenantColumnMigrationsBackfillExistingRowsOnPostgres(t *testing.T) {
	assertTenantColumnBackfill(t, openPostgres(t))
}

// assertTenantColumns checks that both tables carry the tenant and its index.
func assertTenantColumns(t *testing.T, db *DB) {
	t.Helper()
	for table, index := range map[string]string{
		"webhook_deliveries": "idx_webhook_deliveries_tenant",
		"datastore_columns":  "idx_datastore_columns_tenant",
	} {
		if !db.Migrator().HasColumn(table, "tenant_id") {
			t.Errorf("%s has no tenant_id column", table)
		}
		if !db.Migrator().HasIndex(table, index) {
			t.Errorf("%s has no %s index", table, index)
		}
	}
}

// Rolling back to before the two migrations must take the columns and their
// indexes and leave the tables, and migrating again must bring them back.
//
// rollbackAll drops every table, so on its own it cannot tell a down file that
// removes the column from one that removes nothing and runs before the table
// goes anyway. This stops in the middle, where the difference is visible.
func assertTenantColumnMigrationsRoundTrip(t *testing.T, db *DB) {
	t.Helper()
	dialect := db.Dialector.Name()
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	assertTenantColumns(t, db)

	all, err := loadMigrations(migrations.FS, dialect)
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	oldest := min(versionNamed(t, dialect, webhookDeliveriesTenantMigration), versionNamed(t, dialect, datastoreColumnsTenantMigration))
	// Bounded by the number of migrations, so a Rollback that stops making
	// progress fails here instead of hanging the suite.
	for range all {
		applied, err := appliedVersions(db)
		if err != nil {
			t.Fatalf("appliedVersions: %v", err)
		}
		if highestVersion(applied) < oldest {
			break
		}
		if err := Rollback(db, discardLogger()); err != nil {
			t.Fatalf("Rollback: %v", err)
		}
	}

	for _, table := range []string{"webhook_deliveries", "datastore_columns"} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("rolling back the tenant column migrations dropped the %s table itself", table)
			continue
		}
		if db.Migrator().HasColumn(table, "tenant_id") {
			t.Errorf("%s still has its tenant_id column after the rollback", table)
		}
	}
	for table, index := range map[string]string{
		"webhook_deliveries": "idx_webhook_deliveries_tenant",
		"datastore_columns":  "idx_datastore_columns_tenant",
	} {
		if db.Migrator().HasIndex(table, index) {
			t.Errorf("%s still has %s after the rollback", table, index)
		}
	}

	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate after the partial rollback: %v", err)
	}
	assertTenantColumns(t, db)
}

func TestRollingBackTheTenantColumnMigrationsDropsTheColumnsAndKeepsTheTables(t *testing.T) {
	assertTenantColumnMigrationsRoundTrip(t, freshSQLite(t))
}

func TestRollingBackTheTenantColumnMigrationsDropsTheColumnsAndKeepsTheTablesOnPostgres(t *testing.T) {
	assertTenantColumnMigrationsRoundTrip(t, openPostgres(t))
}
