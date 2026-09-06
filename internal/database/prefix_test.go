package database

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/migrations"
)

// openPrefixedSQLite opens a handle whose namer and migration runner both see
// the prefix — the same way Open with database.table_prefix set builds it.
func openPrefixedSQLite(t *testing.T, dsn, prefix string) *DB {
	t.Helper()
	db, err := Open(context.Background(), config.Database{Driver: "sqlite", DSN: dsn, TablePrefix: prefix}, discardLogger())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func prefixedTables(t *testing.T, dialect string, prefix string) []string {
	t.Helper()
	all, err := loadMigrations(migrations.FS, dialect)
	if err != nil {
		t.Fatalf("loadMigrations(%s): %v", dialect, err)
	}
	seen := map[string]struct{}{}
	var tables []string
	for _, m := range all {
		for _, table := range m.createdTables() {
			if _, dup := seen[table]; dup {
				continue
			}
			seen[table] = struct{}{}
			tables = append(tables, prefix+table)
		}
	}
	return tables
}

// An empty prefix runs the checked-in DDL verbatim, so the default install is
// byte-identical to before the prefix existed.
func TestAnEmptyPrefixRunsTheMigrationsVerbatim(t *testing.T) {
	t.Parallel()

	for _, dialect := range []string{"sqlite", "postgres"} {
		all, err := loadMigrations(migrations.FS, dialect)
		if err != nil {
			t.Fatalf("loadMigrations(%s): %v", dialect, err)
		}
		names := definedIdentifiers(all)
		for _, m := range all {
			for _, statements := range [][]string{m.up, m.down} {
				for _, statement := range statements {
					if got := prefixStatement(statement, "", names); got != statement {
						t.Errorf("%s %s: empty prefix rewrote %q", dialect, m.label(), statement)
					}
				}
			}
		}
	}
}

// Columns are never in the defined set, so prefixing cannot touch them: only
// quoted table, index and constraint names move.
func TestPrefixingLeavesColumnsAndLiteralsAlone(t *testing.T) {
	t.Parallel()

	all, err := loadMigrations(migrations.FS, "sqlite")
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	names := definedIdentifiers(all)

	statement := "CREATE INDEX `idx_workflows_tenant_updated` ON `workflows`(`tenant_id`,`updated_at`)"
	got := prefixStatement(statement, "kflow_", names)
	for _, want := range []string{"`kflow_idx_workflows_tenant_updated`", "`kflow_workflows`", "`tenant_id`", "`updated_at`"} {
		if !strings.Contains(got, want) {
			t.Errorf("prefixed %q, want it to contain %q", got, want)
		}
	}
}

// The thirteen literal index names the ticket calls out must all be defined
// names, or the runner would leave them — the actual collision — unprefixed.
func TestDefinedIdentifiersCoverTheLiteralIndexNames(t *testing.T) {
	t.Parallel()

	literals := []string{
		"idx_webhook_bindings_workflow",
		"uidx_webhook_bindings_route",
		"idx_schedules_tenant_workflow",
		"idx_schedules_next_run",
		"idx_credentials_tenant_name",
		"idx_workflows_tenant_updated",
		"idx_workflow_versions_tenant_workflow",
		"uidx_workflow_versions_revision",
		"idx_executions_tenant_started",
		"idx_executions_tenant_workflow",
		"idx_node_runs_tenant_execution",
		"uidx_node_runs_attempt",
		"uidx_node_runs_sequence",
	}

	for _, dialect := range []string{"sqlite", "postgres"} {
		all, err := loadMigrations(migrations.FS, dialect)
		if err != nil {
			t.Fatalf("loadMigrations(%s): %v", dialect, err)
		}
		names := definedIdentifiers(all)
		for _, literal := range literals {
			if _, ok := names[literal]; !ok {
				t.Errorf("%s: literal index %q is not a defined identifier and would escape the prefix", dialect, literal)
			}
		}
	}
}

// Every identifier the migrations define must fit PostgreSQL's 63-byte limit
// once the longest allowed prefix is applied. PostgreSQL truncates silently
// rather than erroring, so an overlong name would collapse two indexes into
// one and CREATE INDEX IF NOT EXISTS would skip the second without complaint.
func TestEveryIdentifierFitsPostgresWithTheLongestPrefix(t *testing.T) {
	t.Parallel()

	for _, dialect := range []string{"sqlite", "postgres"} {
		all, err := loadMigrations(migrations.FS, dialect)
		if err != nil {
			t.Fatalf("loadMigrations(%s): %v", dialect, err)
		}
		for name := range definedIdentifiers(all) {
			if got := config.MaxTablePrefixLength + len(name); got > 63 {
				t.Errorf("%s: %q with the longest prefix is %d bytes, past the 63-byte limit", dialect, name, got)
			}
		}
	}
}

// With kflow_ set, KilasFlow coexists in one database with a host application
// that owns its own workflows, executions and credentials tables and an index
// literally named idx_workflows_tenant_updated: neither side names the
// other's objects afterwards.
func TestAPrefixedInstallCoexistsWithHostTables(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "shared.db")

	host := openSQLite(t, path)
	if err := host.Exec(`CREATE TABLE workflows (id TEXT PRIMARY KEY, tenant_id TEXT)`).Error; err != nil {
		t.Fatalf("create the host workflows table: %v", err)
	}
	if err := host.Exec(`CREATE TABLE executions (id TEXT PRIMARY KEY)`).Error; err != nil {
		t.Fatalf("create the host executions table: %v", err)
	}
	if err := host.Exec(`CREATE TABLE credentials (id TEXT PRIMARY KEY)`).Error; err != nil {
		t.Fatalf("create the host credentials table: %v", err)
	}
	if err := host.Exec(`CREATE INDEX idx_workflows_tenant_updated ON workflows (tenant_id)`).Error; err != nil {
		t.Fatalf("create the host index: %v", err)
	}
	if err := host.Exec(`INSERT INTO workflows (id, tenant_id) VALUES ('host-1', 'host-tenant')`).Error; err != nil {
		t.Fatalf("seed the host table: %v", err)
	}

	db := openPrefixedSQLite(t, path, "kflow_")
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// The host's objects are untouched: same table, same row, same index on
	// the host's table rather than ours.
	var hostRows int64
	if err := host.Raw(`SELECT COUNT(*) FROM workflows`).Scan(&hostRows).Error; err != nil {
		t.Fatalf("read the host table: %v", err)
	}
	if hostRows != 1 {
		t.Errorf("host workflows holds %d rows, want the 1 it started with", hostRows)
	}
	type hostIndex struct {
		Name  string
		Table string
	}
	var indexes []hostIndex
	if err := host.Raw(`SELECT name, tbl_name AS "table" FROM sqlite_master WHERE type = 'index' AND name = 'idx_workflows_tenant_updated'`).Scan(&indexes).Error; err != nil {
		t.Fatalf("list the host index: %v", err)
	}
	if len(indexes) != 1 || indexes[0].Table != "workflows" {
		t.Fatalf("host index = %+v, want the one on the host workflows table", indexes)
	}
	if !host.Migrator().HasIndex("workflows", "idx_workflows_tenant_updated") {
		t.Error("the host's idx_workflows_tenant_updated is gone")
	}

	assertPrefixedSchema(t, db, "kflow_", []string{"workflows", "executions", "credentials"})
}

// assertPrefixedSchema enumerates the tables, indexes and constraints the
// migrations actually created and requires every one to carry the prefix.
// hostTables are a coexisting application's own tables under colliding names,
// which the install must leave alone rather than claim.
func assertPrefixedSchema(t *testing.T, db *DB, prefix string, hostTables []string) {
	t.Helper()

	for _, table := range prefixedTables(t, db.Dialector.Name(), prefix) {
		if !db.Migrator().HasTable(table) {
			t.Errorf("prefixed table %q is missing", table)
		}
		if !db.Migrator().HasIndex(table, prefix+"idx_workflows_tenant_updated") && table == prefix+"workflows" {
			t.Errorf("prefixed collision index on %q is missing", table)
		}
	}

	type tableRow struct {
		Name string
	}
	var tables []tableRow
	if err := db.Raw(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&tables).Error; err != nil {
		t.Fatalf("list tables: %v", err)
	}
	for _, table := range tables {
		// The version bookkeeping is per database, not per install: two
		// prefixes sharing one database must not fork its history.
		if table.Name == schemaMigrationsTable {
			continue
		}
		if slices.Contains(hostTables, table.Name) {
			continue
		}
		if !strings.HasPrefix(table.Name, prefix) {
			t.Errorf("table %q does not carry the %q prefix", table.Name, prefix)
		}
	}

	type indexRow struct {
		Name  string
		Table string
	}
	var indexRows []indexRow
	if err := db.Raw(`SELECT name, tbl_name AS "table" FROM sqlite_master WHERE type = 'index'`).Scan(&indexRows).Error; err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	for _, index := range indexRows {
		if strings.HasPrefix(index.Name, "sqlite_autoindex_") {
			// SQLite's own rowid bookkeeping, table-scoped and invisible to
			// any other application sharing the database.
			continue
		}
		if !strings.HasPrefix(index.Table, prefix) {
			continue
		}
		if !strings.HasPrefix(index.Name, prefix) {
			t.Errorf("index %q on %q does not carry the %q prefix", index.Name, index.Table, prefix)
		}
	}

	type ddlRow struct {
		SQL string
	}
	var ddls []ddlRow
	if err := db.Raw(`SELECT sql AS "sql" FROM sqlite_master WHERE type = 'table' AND name LIKE '` + prefix + `%'`).Scan(&ddls).Error; err != nil {
		t.Fatalf("read table definitions: %v", err)
	}
	for _, ddl := range ddls {
		for _, reference := range quotedReferences(ddl.SQL) {
			if (strings.HasPrefix(reference, "idx_") || strings.HasPrefix(reference, "uidx_") || strings.HasPrefix(reference, "fk_")) &&
				!strings.HasPrefix(reference, prefix) {
				t.Errorf("definition names %q without the %q prefix: %s", reference, prefix, ddl.SQL)
			}
		}
		for _, target := range foreignTargets(ddl.SQL) {
			if !strings.HasPrefix(target, prefix) {
				t.Errorf("foreign key reaches unprefixed table %q: %s", target, ddl.SQL)
			}
		}
	}
}

// quotedReferences names every `quoted` or "quoted" identifier in DDL.
func quotedReferences(ddl string) []string {
	var names []string
	for i := 0; i < len(ddl); i++ {
		if ddl[i] != '`' && ddl[i] != '"' {
			continue
		}
		quote := ddl[i]
		end := strings.IndexByte(ddl[i+1:], quote)
		if end < 0 {
			break
		}
		if name := ddl[i+1 : i+1+end]; name != "" {
			names = append(names, name)
		}
		i += end + 1
	}
	return names
}

// foreignTargets names the tables REFERENCES clauses point at.
func foreignTargets(ddl string) []string {
	var targets []string
	upper := strings.ToUpper(ddl)
	for i := 0; i+len("REFERENCES") <= len(upper); i++ {
		if upper[i:i+len("REFERENCES")] != "REFERENCES" {
			continue
		}
		rest := strings.TrimSpace(ddl[i+len("REFERENCES"):])
		if rest == "" {
			continue
		}
		quote := rest[0]
		if quote != '`' && quote != '"' {
			continue
		}
		end := strings.IndexByte(rest[1:], quote)
		if end < 0 {
			continue
		}
		targets = append(targets, rest[1:1+end])
	}
	return targets
}

// The prefix is fixed for the life of an install: a configured prefix that
// does not match the database refuses to boot instead of silently creating a
// second empty schema alongside the populated one — in both directions.
func TestPrefixMismatchRefusesToStart(t *testing.T) {
	t.Parallel()

	bare := filepath.Join(t.TempDir(), "bare.db")
	if err := Migrate(openSQLite(t, bare), discardLogger()); err != nil {
		t.Fatalf("bare Migrate: %v", err)
	}
	if err := Migrate(openPrefixedSQLite(t, bare, "kflow_"), discardLogger()); err == nil {
		t.Fatal("a kflow_ install over bare tables was accepted")
	} else if !strings.Contains(err.Error(), "table_prefix") {
		t.Fatalf("mismatch error = %v, want it to name table_prefix", err)
	}

	prefixed := filepath.Join(t.TempDir(), "prefixed.db")
	if err := Migrate(openPrefixedSQLite(t, prefixed, "kflow_"), discardLogger()); err != nil {
		t.Fatalf("prefixed Migrate: %v", err)
	}
	if err := Migrate(openSQLite(t, prefixed), discardLogger()); err == nil {
		t.Fatal("a bare install over kflow_ tables was accepted")
	} else if !strings.Contains(err.Error(), "table_prefix") {
		t.Fatalf("mismatch error = %v, want it to name table_prefix", err)
	}
}

// Rolling back a prefixed install drops the prefixed objects and nothing
// else: the down migrations run through the same substitution.
func TestRollingBackAPrefixedInstallLeavesNoKilasFlowTables(t *testing.T) {
	t.Parallel()

	db := openPrefixedSQLite(t, filepath.Join(t.TempDir(), "rollback.db"), "kflow_")
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	rollbackAll(t, db)

	for _, table := range prefixedTables(t, db.Dialector.Name(), "kflow_") {
		if db.Migrator().HasTable(table) {
			t.Errorf("table %q survived the rollback", table)
		}
	}
}
