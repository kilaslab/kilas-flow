package datastore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func openHandle(t *testing.T, driver, dsn, prefix string) *database.DB {
	t.Helper()
	db, err := database.Open(context.Background(), config.Database{Driver: driver, DSN: dsn, TablePrefix: prefix}, discardLogger())
	if err != nil {
		t.Fatalf("Open %s: %v", driver, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// testDriver is one dialect the engine tests run against. SQLite always
// runs; PostgreSQL joins when KILASFLOW_TEST_POSTGRES_DSN names a live
// server, the same gate the database package uses.
type testDriver struct {
	name string
	open func(t *testing.T, prefix string) (*database.DB, *Engine)
}

func testDrivers() []testDriver {
	drivers := []testDriver{{
		name: "sqlite",
		open: func(t *testing.T, prefix string) (*database.DB, *Engine) {
			db := openHandle(t, "sqlite", filepath.Join(t.TempDir(), "datastore.db"), prefix)
			if err := database.Migrate(db, discardLogger()); err != nil {
				t.Fatalf("Migrate sqlite: %v", err)
			}
			eng, err := NewEngine(db, prefix)
			if err != nil {
				t.Fatalf("NewEngine: %v", err)
			}
			return db, eng
		},
	}}
	if os.Getenv("KILASFLOW_TEST_POSTGRES_DSN") != "" {
		drivers = append(drivers, testDriver{
			name: "postgres",
			open: func(t *testing.T, prefix string) (*database.DB, *Engine) {
				db := openHandle(t, "postgres", os.Getenv("KILASFLOW_TEST_POSTGRES_DSN"), prefix)
				// The server is shared between runs, so reset this
				// prefix's catalogue and re-run only 000005: earlier
				// versions stay applied and are never rebuilt. On a
				// fresh server there is no version history yet, so the
				// re-arm is skipped rather than failed.
				for _, table := range []string{prefix + "datastores", prefix + "datastore_columns"} {
					if err := db.Exec(`DROP TABLE IF EXISTS "` + table + `" CASCADE`).Error; err != nil {
						t.Fatalf("drop leftover %s: %v", table, err)
					}
				}
				if db.Migrator().HasTable("schema_migrations") {
					if err := db.Exec(`DELETE FROM "schema_migrations" WHERE version = 5`).Error; err != nil {
						t.Fatalf("re-arm 000005: %v", err)
					}
				}
				if err := database.Migrate(db, discardLogger()); err != nil {
					t.Fatalf("Migrate postgres: %v", err)
				}
				eng, err := NewEngine(db, prefix)
				if err != nil {
					t.Fatalf("NewEngine: %v", err)
				}
				return db, eng
			},
		})
	}
	return drivers
}

// trackPhysical drops a physical table at test end, so runs against the
// shared PostgreSQL server never pollute each other with random-named
// leftovers.
func trackPhysical(t *testing.T, db *database.DB, table string) {
	t.Helper()
	t.Cleanup(func() {
		_ = db.Exec("DROP TABLE IF EXISTS " + quoteIdent(db.Dialector.Name(), table)).Error
	})
}

var fullDefinition = []ColumnInput{
	{Name: "title", Type: "string"},
	{Name: "score", Type: "number"},
	{Name: "flag", Type: "boolean"},
	{Name: "happened", Type: "date"},
}

// Creating a datastore produces one physical table named from the short
// opaque surrogate; the public id appears in no table, index or constraint
// identifier. A row round-trips with its Go types, the id auto-increments
// from 1, and both timestamps arrive set by the database.
func TestCreateInsertReadDropRoundTrips(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			ctx := context.Background()
			dialect := db.Dialector.Name()

			var composed []string
			eng.composeHook = func(statement string) { composed = append(composed, statement) }

			ds, err := eng.Create(ctx, "tenant-1", "contacts", fullDefinition)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			trackPhysical(t, db, ds.Table)
			if ds.Table != "ds_"+ds.Surrogate {
				t.Errorf("table = %q, want ds_ plus the surrogate", ds.Table)
			}
			if matched, _ := regexp.MatchString(`^ds_[0-9a-f]{16}$`, ds.Table); !matched {
				t.Errorf("table = %q, want ds_ plus sixteen hex characters", ds.Table)
			}
			for _, statement := range composed {
				if strings.Contains(statement, ds.ID) {
					t.Errorf("public id leaks into DDL: %s", statement)
				}
			}
			if exists, err := eng.TableExists(ctx, "tenant-1", ds.ID); err != nil || !exists {
				t.Fatalf("TableExists = %v, %v, want true, nil", exists, err)
			}

			assertLiveNaming(t, db, ds)

			// One row through raw SQL: the row store owns reads and
			// writes, so the test speaks SQL directly rather than
			// through an engine API that is not this ticket's to build.
			when := time.Date(2026, time.September, 6, 12, 34, 56, 789000000, time.UTC)
			cols := []string{"title", "score", "flag", "happened"}
			placeholders := make([]string, len(cols))
			for i := range placeholders {
				placeholders[i] = "?"
			}
			insert := "INSERT INTO " + quoteIdent(dialect, ds.Table) +
				" (" + quotedList(dialect, cols) + ") VALUES (" + strings.Join(placeholders, ",") + ")"
			if err := db.Exec(insert, "hello", 1.5, true, when).Error; err != nil {
				t.Fatalf("insert row: %v", err)
			}

			row := readRow(t, db, dialect, ds.Table)
			if row["id"] != int64(1) {
				t.Errorf("id = %#v, want int64(1): a new row arrives as id 1", row["id"])
			}
			if got := asString(t, row["title"]); got != "hello" {
				t.Errorf("title = %q, want hello", got)
			}
			if got := asFloat(t, row["score"]); got != 1.5 {
				t.Errorf("score = %v, want 1.5", got)
			}
			flag := asBool(t, row["flag"])
			if !flag {
				t.Errorf("flag = %#v, want a Go true", row["flag"])
			}
			if got := asTime(t, row["happened"]); got.IsZero() || got.Sub(when) > 2*time.Second || got.Sub(when) < -2*time.Second {
				t.Errorf("happened = %v, want within seconds of %v", got, when)
			}
			for _, stamp := range []string{"createdAt", "updatedAt"} {
				if got := asTime(t, row[stamp]); got.IsZero() {
					t.Errorf("%s = zero, want it set by the database", stamp)
				}
			}

			if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
				t.Fatalf("Drop: %v", err)
			}
			if exists, err := eng.TableExists(ctx, "tenant-1", ds.ID); err != nil || exists {
				t.Errorf("TableExists after drop = %v, %v, want false, nil", exists, err)
			}
			if db.Migrator().HasTable(ds.Table) {
				t.Errorf("physical table %q survives the drop", ds.Table)
			}
			// A second drop of the same datastore is a no-op, so a
			// retried drop converges instead of failing.
			if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
				t.Errorf("second Drop = %v, want nil", err)
			}
			if err := eng.Drop(ctx, "tenant-1", "datastore_does-not-exist"); err != nil {
				t.Errorf("Drop of an unknown id = %v, want nil", err)
			}
		})
	}
}

// assertLiveNaming checks the catalogues both drivers keep: the table
// exists, no secondary index was left for either driver to name, and on
// PostgreSQL the primary-key backing index carries the service-chosen
// constraint name.
func assertLiveNaming(t *testing.T, db *database.DB, ds *Datastore) {
	t.Helper()
	dialect := db.Dialector.Name()
	if !db.Migrator().HasTable(ds.Table) {
		t.Fatalf("physical table %q is missing", ds.Table)
	}
	var indexes []string
	if dialect == "sqlite" {
		if err := db.Raw(
			`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND name NOT LIKE 'sqlite_%'`,
			ds.Table).Scan(&indexes).Error; err != nil {
			t.Fatalf("list indexes: %v", err)
		}
		if len(indexes) != 0 {
			t.Errorf("unexpected indexes on %q: %v", ds.Table, indexes)
		}
		return
	}
	if err := db.Raw(`SELECT indexname FROM pg_indexes WHERE tablename = ?`, ds.Table).Scan(&indexes).Error; err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	want := PhysicalPKName("", ds.Surrogate)
	if len(indexes) != 1 || indexes[0] != want {
		t.Errorf("postgres indexes on %q = %v, want exactly [%s]", ds.Table, indexes, want)
	}
}

func quotedList(dialect string, names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = quoteIdent(dialect, name)
	}
	return strings.Join(quoted, ",")
}

// readRow returns one row by id as driver values. Normalisation lives in
// the as* helpers below; the engine itself owns DDL, not reads.
func readRow(t *testing.T, db *database.DB, dialect, table string) map[string]any {
	t.Helper()
	names := []string{"id", "title", "score", "flag", "happened", "createdAt", "updatedAt"}
	query := "SELECT " + quotedList(dialect, names) + " FROM " + quoteIdent(dialect, table) + " WHERE " + quoteIdent(dialect, "id") + " = ?"
	holders := make([]any, len(names))
	for i := range holders {
		var v any
		holders[i] = &v
	}
	if err := db.Raw(query, 1).Row().Scan(holders...); err != nil {
		t.Fatalf("read row: %v", err)
	}
	row := map[string]any{}
	for i, name := range names {
		row[name] = *(holders[i].(*any))
	}
	return row
}

func asString(t *testing.T, v any) string {
	t.Helper()
	switch v := v.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		t.Fatalf("string column read back as %T(%v)", v, v)
		return ""
	}
}

func asFloat(t *testing.T, v any) float64 {
	t.Helper()
	switch v := v.(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case []byte:
		f, err := strconv.ParseFloat(string(v), 64)
		if err != nil {
			t.Fatalf("number column %q does not parse: %v", v, err)
		}
		return f
	default:
		t.Fatalf("number column read back as %T(%v)", v, v)
		return 0
	}
}

// asBool proves the ticket's round-trip: whatever the driver stored — a Go
// bool on PostgreSQL, 0 or 1 on SQLite — comes back as a Go bool.
func asBool(t *testing.T, v any) bool {
	t.Helper()
	switch v := v.(type) {
	case bool:
		return v
	case int64:
		if v != 0 && v != 1 {
			t.Fatalf("boolean column read back as %d, want 0 or 1", v)
		}
		return v == 1
	case []byte:
		if string(v) == "1" || string(v) == "true" {
			return true
		}
		if string(v) == "0" || string(v) == "false" {
			return false
		}
		t.Fatalf("boolean column read back as %q", v)
		return false
	default:
		t.Fatalf("boolean column read back as %T(%v), want a Go bool", v, v)
		return false
	}
}

var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
}

func asTime(t *testing.T, v any) time.Time {
	t.Helper()
	switch v := v.(type) {
	case time.Time:
		return v
	case string:
		return parseTime(t, v)
	case []byte:
		return parseTime(t, string(v))
	default:
		t.Fatalf("datetime column read back as %T(%v)", v, v)
		return time.Time{}
	}
}

func parseTime(t *testing.T, s string) time.Time {
	t.Helper()
	for _, layout := range timeLayouts {
		if got, err := time.Parse(layout, s); err == nil {
			return got
		}
	}
	t.Fatalf("datetime column %q matches no known layout", s)
	return time.Time{}
}

// With kflow_ set, the install coexists with a host application holding its
// own tables and an index literally named idx_workflows_tenant_updated:
// every table the engine touches carries the prefix, and the host's objects
// are untouched.
func TestAPrefixedInstallCoexistsWithHostTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	host := openHandle(t, "sqlite", path, "")
	if err := host.Exec(`CREATE TABLE workflows (id TEXT PRIMARY KEY, tenant_id TEXT)`).Error; err != nil {
		t.Fatalf("create the host workflows table: %v", err)
	}
	if err := host.Exec(`CREATE INDEX idx_workflows_tenant_updated ON workflows (tenant_id)`).Error; err != nil {
		t.Fatalf("create the host index: %v", err)
	}
	if err := host.Exec(`INSERT INTO workflows (id, tenant_id) VALUES ('host-1', 'host-tenant')`).Error; err != nil {
		t.Fatalf("seed the host table: %v", err)
	}

	db, eng := func() (*database.DB, *Engine) {
		db := openHandle(t, "sqlite", path, "kflow_")
		if err := database.Migrate(db, discardLogger()); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		eng, err := NewEngine(db, "kflow_")
		if err != nil {
			t.Fatalf("NewEngine: %v", err)
		}
		return db, eng
	}()

	ctx := context.Background()
	ds, err := eng.Create(ctx, "tenant-1", "contacts", []ColumnInput{{Name: "title", Type: "string"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(ds.Table, "kflow_") {
		t.Errorf("table = %q, want the kflow_ prefix", ds.Table)
	}
	for _, table := range []string{"kflow_datastores", "kflow_datastore_columns", ds.Table} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("prefixed table %q is missing", table)
		}
	}
	for _, table := range []string{"datastores", "datastore_columns"} {
		if db.Migrator().HasTable(table) {
			t.Errorf("unprefixed table %q exists alongside the prefixed install", table)
		}
	}

	var hostRows int64
	if err := host.Raw(`SELECT COUNT(*) FROM workflows`).Scan(&hostRows).Error; err != nil {
		t.Fatalf("read the host table: %v", err)
	}
	if hostRows != 1 {
		t.Errorf("host workflows holds %d rows, want the 1 it started with", hostRows)
	}
	if !host.Migrator().HasIndex("workflows", "idx_workflows_tenant_updated") {
		t.Error("the host's idx_workflows_tenant_updated is gone")
	}

	if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
		t.Fatalf("Drop: %v", err)
	}
}

// An invalid definition is refused before any SQL is composed: no statement
// recorded, no catalogue row, no table.
func TestInvalidDefinitionsComposeNoSQL(t *testing.T) {
	db, eng := testDrivers()[0].open(t, "")
	ctx := context.Background()

	var composed []string
	eng.composeHook = func(statement string) { composed = append(composed, statement) }

	for _, in := range [][]ColumnInput{
		{{Name: "id", Type: "string"}},
		{{Name: "CreatedAt", Type: "string"}},
		{{Name: `a"b`, Type: "string"}},
		{{Name: "ok", Type: "mystery"}},
		{{Name: "dup", Type: "string"}, {Name: "DUP", Type: "string"}},
	} {
		if _, err := eng.Create(ctx, "tenant-1", "bad", in); err == nil {
			t.Errorf("Create(%v) = nil, want refusal", in)
		}
	}
	if len(composed) != 0 {
		t.Errorf("composed %d statements for refused definitions: %v", len(composed), composed)
	}
	var rows int64
	if err := db.Raw(`SELECT COUNT(*) FROM datastores`).Scan(&rows).Error; err != nil {
		t.Fatalf("count catalogue rows: %v", err)
	}
	if rows != 0 {
		t.Errorf("catalogue holds %d rows after refused definitions, want 0", rows)
	}
}

// Every index the service creates carries a name the service chose: no
// emitted CREATE INDEX statement omits the index name. The negative control
// proves the check bites rather than passing vacuously.
func TestEveryCreatedIndexCarriesAChosenName(t *testing.T) {
	db, eng := testDrivers()[0].open(t, "")
	ctx := context.Background()

	var composed []string
	eng.composeHook = func(statement string) { composed = append(composed, statement) }

	ds, err := eng.Create(ctx, "tenant-1", "contacts", []ColumnInput{{Name: "title", Type: "string"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	trackPhysical(t, db, ds.Table)
	if err := eng.AddColumn(ctx, "tenant-1", ds.ID, ColumnInput{Name: "score", Type: "number"}); err != nil {
		t.Fatalf("AddColumn: %v", err)
	}
	if err := eng.RenameColumn(ctx, "tenant-1", ds.ID, "score", "rating"); err != nil {
		t.Fatalf("RenameColumn: %v", err)
	}
	if err := eng.DropColumn(ctx, "tenant-1", ds.ID, "rating"); err != nil {
		t.Fatalf("DropColumn: %v", err)
	}
	if len(composed) == 0 {
		t.Fatal("no statements composed: the check below would pass vacuously")
	}

	// After CREATE [UNIQUE] INDEX [IF NOT EXISTS] must come the quoted
	// name, never ON.
	named := regexp.MustCompile(`(?i)^CREATE\s+(UNIQUE\s+)?INDEX\s+(IF NOT EXISTS\s+)?["` + "`" + `]`)
	for _, statement := range composed {
		upper := strings.ToUpper(strings.TrimSpace(statement))
		if !strings.HasPrefix(upper, "CREATE INDEX") && !strings.HasPrefix(upper, "CREATE UNIQUE INDEX") {
			continue
		}
		if !named.MatchString(strings.TrimSpace(statement)) {
			t.Errorf("CREATE INDEX without a chosen name: %s", statement)
		}
	}
	if named.MatchString("CREATE INDEX ON contacts (title)") {
		t.Error("the name check accepts a nameless CREATE INDEX")
	}
	if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
		t.Fatalf("Drop: %v", err)
	}
}

// A DDL failure leaves no catalogue row and a catalogue failure leaves no
// physical table: both go through the single transaction.
func TestFailuresLeaveNeitherRowNorTable(t *testing.T) {
	for _, stage := range []string{StageCatalogue, StageDDL} {
		t.Run("fail at "+stage, func(t *testing.T) {
			db, eng := testDrivers()[0].open(t, "")
			ctx := context.Background()
			boom := errors.New("injected " + stage + " failure")
			eng.inject = func(got string) error {
				if got == stage {
					return boom
				}
				return nil
			}
			_, err := eng.Create(ctx, "tenant-1", "contacts", []ColumnInput{{Name: "title", Type: "string"}})
			if !errors.Is(err, boom) {
				t.Fatalf("Create = %v, want the injected failure", err)
			}
			var rows int64
			if err := db.Raw(`SELECT COUNT(*) FROM datastores`).Scan(&rows).Error; err != nil {
				t.Fatalf("count catalogue rows: %v", err)
			}
			if rows != 0 {
				t.Errorf("catalogue holds %d rows after a %s failure, want 0", rows, stage)
			}
			var tables []string
			if err := db.Raw(`SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE 'ds\_%' ESCAPE '\'`).Scan(&tables).Error; err != nil {
				t.Fatalf("list physical tables: %v", err)
			}
			if len(tables) != 0 {
				t.Errorf("physical tables survive a %s failure: %v", stage, tables)
			}
		})
	}
}

// Column lifecycles keep the catalogue and the table in step: add appends
// the position, rename keeps it, drop removes the row, and system columns
// are refused with the reserved-word error rather than "unknown".
func TestColumnLifecyclesKeepCatalogueAndTableInStep(t *testing.T) {
	db, eng := testDrivers()[0].open(t, "")
	ctx := context.Background()

	ds, err := eng.Create(ctx, "tenant-1", "contacts", []ColumnInput{{Name: "title", Type: "string"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	trackPhysical(t, db, ds.Table)

	if err := eng.AddColumn(ctx, "tenant-1", ds.ID, ColumnInput{Name: "score", Type: "datetime"}); err != nil {
		t.Fatalf("AddColumn: %v", err)
	}
	_, cols, err := eng.lookup(ctx, "tenant-1", ds.ID)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(cols) != 2 || cols[1].Name != "score" || cols[1].Type != ColumnDate || cols[1].Position != 1 {
		t.Errorf("columns after add = %+v, want title@0 and score:date@1", cols)
	}

	// The datetime label is stored as the date wire value, not the UI label.
	var storedType string
	if err := db.Raw(`SELECT type FROM datastore_columns WHERE datastore_id = ? AND name = ?`, ds.ID, "score").Scan(&storedType).Error; err != nil {
		t.Fatalf("read stored type: %v", err)
	}
	if storedType != "date" {
		t.Errorf("stored type = %q, want the date wire value", storedType)
	}

	if err := eng.RenameColumn(ctx, "tenant-1", ds.ID, "score", "rating"); err != nil {
		t.Fatalf("RenameColumn: %v", err)
	}
	_, cols, err = eng.lookup(ctx, "tenant-1", ds.ID)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(cols) != 2 || cols[1].Name != "rating" || cols[1].Position != 1 {
		t.Errorf("columns after rename = %+v, want the position kept", cols)
	}

	if err := eng.AddColumn(ctx, "tenant-1", ds.ID, ColumnInput{Name: "RATING", Type: "string"}); err == nil {
		t.Error("AddColumn colliding only in case = nil, want refusal")
	}
	if err := eng.RenameColumn(ctx, "tenant-1", ds.ID, "rating", "id"); err == nil ||
		!strings.Contains(fmt.Sprint(err), `"id"`) {
		t.Errorf("RenameColumn onto id = %v, want the reserved-word refusal", err)
	}
	if err := eng.RenameColumn(ctx, "tenant-1", ds.ID, "missing", "other"); err == nil {
		t.Error("RenameColumn of an unknown column = nil, want an error")
	}
	for _, name := range []string{"id", "createdAt", "dryRunState"} {
		if err := eng.DropColumn(ctx, "tenant-1", ds.ID, name); err == nil ||
			!strings.Contains(err.Error(), "reserved") {
			t.Errorf("DropColumn(%s) = %v, want the reserved-word refusal", name, err)
		}
	}

	if err := eng.DropColumn(ctx, "tenant-1", ds.ID, "rating"); err != nil {
		t.Fatalf("DropColumn: %v", err)
	}
	_, cols, err = eng.lookup(ctx, "tenant-1", ds.ID)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(cols) != 1 || cols[0].Name != "title" {
		t.Errorf("columns after drop = %+v, want only title", cols)
	}
	if db.Migrator().HasTable(ds.Table) {
		// The column is gone from the live table, not just the catalogue.
		type pragmaRow struct {
			Name string `gorm:"column:name"`
		}
		var pragma []pragmaRow
		if err := db.Raw(fmt.Sprintf("SELECT name FROM pragma_table_info('%s')", ds.Table)).Scan(&pragma).Error; err != nil {
			t.Fatalf("read table info: %v", err)
		}
		for _, col := range pragma {
			if col.Name == "rating" {
				t.Errorf("column rating survives the drop on the live table")
			}
		}
	}

	if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
		t.Fatalf("Drop: %v", err)
	}
}
