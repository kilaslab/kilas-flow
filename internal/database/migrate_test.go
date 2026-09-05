package database

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/migrations"
)

// baselineTables is what the schema looked like when migrations took over from
// AutoMigrate. Spelled out here rather than derived from the migration files so
// that a migration which quietly stops creating a table fails a test.
var baselineTables = []string{
	"workflows",
	"workflow_versions",
	"executions",
	"execution_node_runs",
	"credentials",
	"webhook_bindings",
	"webhook_routes",
	"webhook_deliveries",
	"schedules",
}

// postBaselineTables are the tables migrations added after the baseline, child
// first so dropping them satisfies their foreign keys.
//
// Two tests need them named. The adoption tests build their "legacy" fixture
// from repository.Models(), which is today's schema rather than the one
// AutoMigrate left behind, so the fixture has to be reduced to the baseline
// before it can stand in for a pre-migration install. The PostgreSQL helper
// needs them because it shares one live database between runs, and a table left
// behind by the previous run makes the next run's CREATE TABLE fail.
var postBaselineTables = []string{
	"api_keys",
	"users",
	"tenants",
}

// baselineIndexes are the generated names GORM's naming strategy produced and
// which no model spells out. A migration that renames one of these breaks every
// later migration that has to find the object by name.
var baselineIndexes = []string{
	"idx_workflows_deleted_at",
	"idx_workflows_tenant_updated",
	"uidx_workflow_versions_revision",
	"idx_executions_lease_owner",
	"idx_executions_lease_expires_at",
	"idx_executions_cancellation_requested_at",
	"idx_executions_workflow_version_id",
	"uidx_node_runs_attempt",
	"idx_webhook_routes_route",
	"uidx_webhook_deliveries",
	"idx_schedules_next_run",
}

// reduceToBaseline turns a schema built from today's models into the one
// AutoMigrate left behind when migrations took over.
//
// Without it the adoption fixture carries tables a later migration has not been
// applied yet, and that migration's CREATE TABLE then fails against a table the
// fixture built for it — a failure about the fixture, not about adoption.
func reduceToBaseline(t *testing.T, db *DB) {
	t.Helper()
	for _, table := range postBaselineTables {
		if !db.Migrator().HasTable(table) {
			continue
		}
		if err := db.Migrator().DropTable(table); err != nil {
			t.Fatalf("reduce the fixture to the baseline schema: drop %q: %v", table, err)
		}
	}
}

// rollbackAll reverts every applied migration, newest first.
//
// The rollback tests used to call Rollback once, which was the whole schema
// while the baseline was the only migration. Now that it is not, a single call
// would only ever exercise the newest one and would report the tables an older
// migration created as "left behind".
func rollbackAll(t *testing.T, db *DB) {
	t.Helper()
	all, err := loadMigrations(migrations.FS, db.Dialector.Name())
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	// Bounded by the number of migrations rather than looping until the table
	// is empty, so a Rollback that stops making progress fails the test instead
	// of hanging it.
	for range all {
		applied, err := appliedVersions(db)
		if err != nil {
			t.Fatalf("appliedVersions: %v", err)
		}
		if len(applied) == 0 {
			return
		}
		if err := Rollback(db, discardLogger()); err != nil {
			t.Fatalf("Rollback: %v", err)
		}
	}
	applied, err := appliedVersions(db)
	if err != nil {
		t.Fatalf("appliedVersions: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("rolling back %d times left versions %v applied", len(all), applied)
	}
}

// sqlRecorder captures every statement GORM issues, which is how these tests
// tell "the schema already matched" apart from "the schema was rebuilt".
type sqlRecorder struct {
	mu         sync.Mutex
	statements []string
}

func (r *sqlRecorder) LogMode(gormlogger.LogLevel) gormlogger.Interface { return r }
func (r *sqlRecorder) Info(context.Context, string, ...any)             {}
func (r *sqlRecorder) Warn(context.Context, string, ...any)             {}
func (r *sqlRecorder) Error(context.Context, string, ...any)            {}
func (r *sqlRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	statement, _ := fc()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statements = append(r.statements, statement)
}

// ddl returns only the schema-changing statements that were issued.
func (r *sqlRecorder) ddl() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var found []string
	for _, statement := range r.statements {
		switch strings.ToUpper(strings.Fields(strings.TrimSpace(statement))[0]) {
		case "CREATE", "ALTER", "DROP":
			found = append(found, strings.TrimSpace(statement))
		}
	}
	return found
}

// watch returns a handle whose statements land in the recorder.
func watch(db *DB, recorder *sqlRecorder) *DB {
	return &DB{db.Session(&gorm.Session{Logger: recorder, NewDB: true})}
}

func openSQLite(t *testing.T, dsn string) *DB {
	t.Helper()
	db, err := Open(context.Background(), config.Database{Driver: "sqlite", DSN: dsn}, discardLogger())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func freshSQLite(t *testing.T) *DB {
	t.Helper()
	return openSQLite(t, filepath.Join(t.TempDir(), "kilasflow.db"))
}

// openPostgres skips unless a live server is configured, and hands back a
// database with none of KilasFlow's tables in it.
func openPostgres(t *testing.T) *DB {
	t.Helper()
	dsn := os.Getenv("KILASFLOW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set KILASFLOW_TEST_POSTGRES_DSN to run PostgreSQL migration integration coverage")
	}

	db, err := Open(context.Background(), config.Database{Driver: "postgres", DSN: dsn}, discardLogger())
	if err != nil {
		t.Fatalf("Open PostgreSQL: %v", err)
	}
	drop := func() {
		for _, table := range append(append([]string{schemaMigrationsTable}, postBaselineTables...), baselineTables...) {
			_ = db.Exec(`DROP TABLE IF EXISTS "` + table + `" CASCADE`).Error
		}
	}
	drop()
	t.Cleanup(func() {
		drop()
		_ = db.Close()
	})
	return db
}

func assertBaselineSchema(t *testing.T, db *DB) {
	t.Helper()
	for _, table := range baselineTables {
		if !db.Migrator().HasTable(table) {
			t.Errorf("the baseline did not create the %q table", table)
		}
	}
	for _, index := range baselineIndexes {
		if !db.Migrator().HasIndex("workflows", index) &&
			!hasIndexOnAnyBaselineTable(db, index) {
			t.Errorf("the baseline did not create the %q index", index)
		}
	}
}

func hasIndexOnAnyBaselineTable(db *DB, index string) bool {
	for _, table := range baselineTables {
		if db.Migrator().HasIndex(table, index) {
			return true
		}
	}
	return false
}

func TestAFreshSQLiteDatabaseGetsTheWholeBaselineSchema(t *testing.T) {
	db := freshSQLite(t)

	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	assertBaselineSchema(t, db)
}

func TestAFreshPostgresDatabaseGetsTheWholeBaselineSchema(t *testing.T) {
	db := openPostgres(t)

	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	assertBaselineSchema(t, db)
}

// The baseline has to reproduce what AutoMigrate used to build, identifier for
// identifier. AutoMigrate is additive and idempotent, so if the two agree it
// issues no DDL at all — and if a model gains a field the baseline does not
// carry, it issues the ALTER TABLE this test then reports.
func TestBaselineLeavesAutoMigrateNothingToDoOnSQLite(t *testing.T) {
	db := freshSQLite(t)
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	assertModelsMatchBaseline(t, db)
}

func TestBaselineLeavesAutoMigrateNothingToDoOnPostgres(t *testing.T) {
	db := openPostgres(t)
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	assertModelsMatchBaseline(t, db)
}

func assertModelsMatchBaseline(t *testing.T, db *DB) {
	t.Helper()
	recorder := &sqlRecorder{}
	if err := watch(db, recorder).AutoMigrate(repository.Models()...); err != nil {
		t.Fatalf("AutoMigrate over the migrated schema: %v", err)
	}
	for _, statement := range recorder.ddl() {
		t.Errorf("repository.Models() has drifted from the migration baseline; AutoMigrate wanted to run: %s", statement)
	}
}

// The first upgrade in the field meets a database AutoMigrate built. Adoption
// has to record the baseline and touch nothing: running it would mean CREATE
// TABLE over live data.
func TestAnAutoMigratedInstallIsStampedRatherThanRebuilt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kilasflow.db")
	legacy := openSQLite(t, path)
	if err := legacy.AutoMigrate(repository.Models()...); err != nil {
		t.Fatalf("build the legacy schema: %v", err)
	}
	reduceToBaseline(t, legacy)
	if err := legacy.Exec(
		"INSERT INTO `workflows` (`id`,`tenant_id`,`name`,`active`,`latest_revision`,`created_at`,`updated_at`) VALUES (?,?,?,?,?,?,?)",
		"wf-1", "tenant-1", "before the upgrade", false, 1, time.Now(), time.Now(),
	).Error; err != nil {
		t.Fatalf("write a row the upgrade must not lose: %v", err)
	}

	recorder := &sqlRecorder{}
	if err := Migrate(watch(legacy, recorder), discardLogger()); err != nil {
		t.Fatalf("Migrate over an AutoMigrate-created schema: %v", err)
	}

	for _, statement := range recorder.ddl() {
		for _, table := range baselineTables {
			if strings.Contains(statement, "`"+table+"`") {
				t.Errorf("adoption touched the existing %q table: %s", table, statement)
			}
		}
	}

	var name string
	if err := legacy.Raw("SELECT name FROM `workflows` WHERE id = ?", "wf-1").Scan(&name).Error; err != nil {
		t.Fatalf("read the row back: %v", err)
	}
	if name != "before the upgrade" {
		t.Errorf("workflow name after adoption = %q, want %q", name, "before the upgrade")
	}

	applied, err := appliedVersions(legacy)
	if err != nil {
		t.Fatalf("appliedVersions: %v", err)
	}
	// Only the baseline is asserted. Migrations after it legitimately run
	// against the adopted database, which is the whole point of stamping it.
	if _, stamped := applied[1]; !stamped {
		t.Errorf("applied versions after adoption = %v, want the baseline among them", applied)
	}
}

func TestAnAutoMigratedPostgresInstallIsStampedRatherThanRebuilt(t *testing.T) {
	db := openPostgres(t)
	if err := db.AutoMigrate(repository.Models()...); err != nil {
		t.Fatalf("build the legacy schema: %v", err)
	}
	reduceToBaseline(t, db)
	if err := db.Exec(
		`INSERT INTO "workflows" ("id","tenant_id","name","active","latest_revision","created_at","updated_at") VALUES (?,?,?,?,?,?,?)`,
		"wf-1", "tenant-1", "before the upgrade", false, 1, time.Now(), time.Now(),
	).Error; err != nil {
		t.Fatalf("write a row the upgrade must not lose: %v", err)
	}

	recorder := &sqlRecorder{}
	if err := Migrate(watch(db, recorder), discardLogger()); err != nil {
		t.Fatalf("Migrate over an AutoMigrate-created schema: %v", err)
	}

	for _, statement := range recorder.ddl() {
		for _, table := range baselineTables {
			if strings.Contains(statement, `"`+table+`"`) {
				t.Errorf("adoption touched the existing %q table: %s", table, statement)
			}
		}
	}

	var name string
	if err := db.Raw(`SELECT name FROM "workflows" WHERE id = ?`, "wf-1").Scan(&name).Error; err != nil {
		t.Fatalf("read the row back: %v", err)
	}
	if name != "before the upgrade" {
		t.Errorf("workflow name after adoption = %q, want %q", name, "before the upgrade")
	}

	applied, err := appliedVersions(db)
	if err != nil {
		t.Fatalf("appliedVersions: %v", err)
	}
	// Only the baseline is asserted. Migrations after it legitimately run
	// against the adopted database, which is the whole point of stamping it.
	if _, stamped := applied[1]; !stamped {
		t.Errorf("applied versions after adoption = %v, want the baseline among them", applied)
	}
}

func TestMigratingTwiceChangesNothingTheSecondTime(t *testing.T) {
	db := freshSQLite(t)
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}

	recorder := &sqlRecorder{}
	if err := Migrate(watch(db, recorder), discardLogger()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	for _, statement := range recorder.ddl() {
		for _, table := range baselineTables {
			if strings.Contains(statement, "`"+table+"`") {
				t.Errorf("the second Migrate touched %q: %s", table, statement)
			}
		}
	}
}

// Two processes started at the same moment must not both run the baseline. The
// loser has to see the migration as already applied, not as a failure.
func TestConcurrentStartsApplyTheBaselineExactlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kilasflow.db")

	const starters = 4
	handles := make([]*DB, starters)
	for i := range handles {
		handles[i] = openSQLite(t, path)
	}

	var (
		wait   sync.WaitGroup
		start  = make(chan struct{})
		errs   = make([]error, starters)
		logger = discardLogger()
	)
	for i, db := range handles {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			errs[i] = Migrate(db, logger)
		}()
	}
	close(start)
	wait.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent starter %d: %v", i, err)
		}
	}

	var applications int64
	if err := handles[0].Raw("SELECT COUNT(*) FROM `schema_migrations` WHERE version = 1").Scan(&applications).Error; err != nil {
		t.Fatalf("count applied migrations: %v", err)
	}
	if applications != 1 {
		t.Errorf("schema_migrations rows for version 1 = %d, want 1", applications)
	}
	assertBaselineSchema(t, handles[0])
}

func TestConcurrentPostgresStartsApplyTheBaselineExactlyOnce(t *testing.T) {
	first := openPostgres(t)
	dsn := os.Getenv("KILASFLOW_TEST_POSTGRES_DSN")

	const starters = 4
	handles := []*DB{first}
	for len(handles) < starters {
		db, err := Open(context.Background(), config.Database{Driver: "postgres", DSN: dsn}, discardLogger())
		if err != nil {
			t.Fatalf("Open PostgreSQL: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		handles = append(handles, db)
	}

	var (
		wait   sync.WaitGroup
		start  = make(chan struct{})
		errs   = make([]error, starters)
		logger = discardLogger()
	)
	for i, db := range handles {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			errs[i] = Migrate(db, logger)
		}()
	}
	close(start)
	wait.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent starter %d: %v", i, err)
		}
	}

	var applications int64
	if err := first.Raw(`SELECT COUNT(*) FROM "schema_migrations" WHERE version = 1`).Scan(&applications).Error; err != nil {
		t.Fatalf("count applied migrations: %v", err)
	}
	if applications != 1 {
		t.Errorf("schema_migrations rows for version 1 = %d, want 1", applications)
	}
	assertBaselineSchema(t, first)
}

// An operator who downgrades the binary but not the database gets a refusal
// naming both versions, rather than a server that runs against a schema it does
// not understand.
func TestADatabaseAheadOfTheBinaryRefusesToStart(t *testing.T) {
	db := freshSQLite(t)
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := db.Exec(
		"INSERT INTO `schema_migrations` (version, name, applied_at) VALUES (?, ?, ?)",
		42, "from_a_newer_build", time.Now().UTC(),
	).Error; err != nil {
		t.Fatalf("stamp a future version: %v", err)
	}

	err := Migrate(db, discardLogger())
	if err == nil {
		t.Fatal("Migrate against a newer schema = nil, want a refusal")
	}
	// The newest version is read from the embedded set rather than written
	// here, so adding a migration does not silently turn this into a test that
	// the refusal names some older version.
	all, loadErr := loadMigrations(migrations.FS, db.Dialector.Name())
	if loadErr != nil {
		t.Fatalf("loadMigrations: %v", loadErr)
	}
	newest := fmt.Sprintf("%d", all[len(all)-1].version)
	for _, want := range []string{"42", newest} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name version %s", err, want)
		}
	}
}

func TestRollingBackEveryMigrationLeavesNoKilasFlowTables(t *testing.T) {
	db := freshSQLite(t)
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	rollbackAll(t, db)

	for _, table := range append(append([]string(nil), baselineTables...), postBaselineTables...) {
		if db.Migrator().HasTable(table) {
			t.Errorf("rollback left the %q table behind", table)
		}
	}
	applied, err := appliedVersions(db)
	if err != nil {
		t.Fatalf("appliedVersions: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("applied versions after rollback = %v, want none", applied)
	}
}

func TestRollingBackEveryPostgresMigrationLeavesNoKilasFlowTables(t *testing.T) {
	db := openPostgres(t)
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	rollbackAll(t, db)

	for _, table := range append(append([]string(nil), baselineTables...), postBaselineTables...) {
		if db.Migrator().HasTable(table) {
			t.Errorf("rollback left the %q table behind", table)
		}
	}
}

// Migrating again after a rollback has to rebuild the schema, which is what
// makes a rollback recoverable rather than terminal.
func TestMigratingAfterARollbackRebuildsTheSchema(t *testing.T) {
	db := freshSQLite(t)
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	rollbackAll(t, db)

	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate after Rollback: %v", err)
	}

	assertBaselineSchema(t, db)
}

// A migration that fails partway must leave neither half a schema nor a version
// row claiming it succeeded.
func TestAFailedMigrationRecordsNothing(t *testing.T) {
	db := freshSQLite(t)
	broken := fstest.MapFS{
		"sqlite/000001_broken.up.sql":   {Data: []byte("CREATE TABLE `first` (`id` text);\nCREATE TABLE `first` (`id` text);\n")},
		"sqlite/000001_broken.down.sql": {Data: []byte("DROP TABLE `first`;\n")},
	}

	err := migrateFS(db, broken, discardLogger())
	if err == nil {
		t.Fatal("migrateFS over a broken migration = nil, want an error")
	}

	if db.Migrator().HasTable("first") {
		t.Error("the failed migration left its first table behind")
	}
	applied, readErr := appliedVersions(db)
	if readErr != nil {
		t.Fatalf("appliedVersions: %v", readErr)
	}
	if len(applied) != 0 {
		t.Errorf("applied versions after a failed migration = %v, want none", applied)
	}
}

func TestEveryMigrationShipsBothDirectionsForBothDialects(t *testing.T) {
	versions := map[string][]int64{}
	for _, dialect := range []string{"sqlite", "postgres"} {
		all, err := loadMigrations(migrations.FS, dialect)
		if err != nil {
			t.Fatalf("loadMigrations(%s): %v", dialect, err)
		}
		if len(all) == 0 {
			t.Fatalf("no %s migrations are embedded", dialect)
		}
		for _, one := range all {
			if len(one.up) == 0 {
				t.Errorf("%s/%s has no up statements", dialect, one.label())
			}
			if len(one.down) == 0 {
				t.Errorf("%s/%s has no down file", dialect, one.label())
			}
			versions[dialect] = append(versions[dialect], one.version)
		}
	}

	// The dialects share version numbers. A version present in one and missing
	// from the other would mean the same binary is at two different schema
	// versions depending on which database it opened.
	if fmt.Sprint(versions["sqlite"]) != fmt.Sprint(versions["postgres"]) {
		t.Errorf("sqlite migrations %v do not match postgres migrations %v", versions["sqlite"], versions["postgres"])
	}
}

func TestTheBaselineCreatesTheSameTablesInBothDialects(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		all, err := loadMigrations(migrations.FS, dialect)
		if err != nil {
			t.Fatalf("loadMigrations(%s): %v", dialect, err)
		}
		if got := fmt.Sprint(all[0].createdTables()); got != fmt.Sprint(baselineTables) {
			t.Errorf("the %s baseline creates %s, want %v", dialect, got, baselineTables)
		}
	}
}

func TestParseMigrationNameReadsVersionNameAndDirection(t *testing.T) {
	version, name, direction, err := parseMigrationName("000012_add_status_index.down.sql")
	if err != nil {
		t.Fatalf("parseMigrationName: %v", err)
	}
	if version != 12 || name != "add_status_index" || direction != "down" {
		t.Errorf("parseMigrationName = (%d, %q, %q), want (12, %q, %q)", version, name, direction, "add_status_index", "down")
	}
}

func TestParseMigrationNameRejectsUnusableFilenames(t *testing.T) {
	for _, filename := range []string{
		"baseline.up.sql",            // no version
		"000001_baseline.sql",        // no direction
		"000001_.up.sql",             // no name
		"notanumber_baseline.up.sql", // version is not a number
		"000000_baseline.up.sql",     // zero is reserved for "nothing applied"
	} {
		if _, _, _, err := parseMigrationName(filename); err == nil {
			t.Errorf("parseMigrationName(%q) = nil, want an error", filename)
		}
	}
}

func TestSplitStatementsKeepsSemicolonsInsideLiteralsAndComments(t *testing.T) {
	const file = `-- A leading comment; with a semicolon in it.
CREATE TABLE "t" ("a" varchar(8) DEFAULT 'x;y', "b" text DEFAULT "");

/* A block comment; also with one. */
CREATE INDEX "idx_t_a" ON "t" ("a"); -- trailing
`
	got := splitStatements(file)
	want := []string{
		`CREATE TABLE "t" ("a" varchar(8) DEFAULT 'x;y', "b" text DEFAULT "")`,
		`CREATE INDEX "idx_t_a" ON "t" ("a")`,
	}
	if len(got) != len(want) {
		t.Fatalf("splitStatements returned %d statements %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if strings.Join(strings.Fields(got[i]), " ") != want[i] {
			t.Errorf("statement %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSplitStatementsKeepsEscapedQuotesInsideLiterals(t *testing.T) {
	got := splitStatements(`INSERT INTO "t" VALUES ('it''s; fine');`)
	if len(got) != 1 {
		t.Fatalf("splitStatements returned %d statements %q, want 1", len(got), got)
	}
	if !strings.Contains(got[0], "'it''s; fine'") {
		t.Errorf("statement = %q, want the literal preserved", got[0])
	}
}

// glebarez/sqlite's DDL parser counts a tab as a quote character, so a
// tab-indented CREATE TABLE parses as a table with no columns. AutoMigrate then
// concludes every column is missing and rebuilds the table — copy, drop,
// rename — on every single boot. Nothing about that failure points at
// whitespace, so it is pinned here rather than left to be rediscovered.
func TestMigrationFilesIndentWithSpacesRatherThanTabs(t *testing.T) {
	err := fs.WalkDir(migrations.FS, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		body, err := migrations.FS.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(body), "\t") {
			t.Errorf("%s is indented with tabs, which makes SQLite's DDL parser read the table as having no columns", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the embedded migrations: %v", err)
	}
}

func TestAnUnknownDialectHasNoMigrations(t *testing.T) {
	if _, err := versionTableDDL("mysql"); err == nil {
		t.Error("versionTableDDL(\"mysql\") = nil, want an error naming the dialect")
	}
}
