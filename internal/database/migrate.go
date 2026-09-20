package database

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/kilaslab/kilas-flow/migrations"
)

// schemaMigrationsTable records which migrations this database has run.
//
// It is created by the runner rather than by a migration of its own: the runner
// has to read it to decide what to run, so a chicken-and-egg version 000000
// would have to be special-cased anyway.
const schemaMigrationsTable = "schema_migrations"

// migration is one numbered up/down pair for one dialect.
type migration struct {
	version int64
	name    string
	// up and down are already split into single statements. The PostgreSQL
	// driver speaks the extended query protocol, which rejects more than one
	// statement per Exec, so a whole file handed over at once fails on
	// PostgreSQL while working on SQLite — the worst possible split.
	up   []string
	down []string
}

func (m migration) label() string { return fmt.Sprintf("%06d_%s", m.version, m.name) }

// Migrate brings the database up to the schema this binary carries.
//
// Nothing here reflects over Go structs. The schema is whatever the checked-in
// SQL says it is, which is the only form an operator can review before letting
// it run against a database they share with something else.
func Migrate(db *DB, log *slog.Logger) error {
	return migrateFS(db, migrations.FS, log)
}

// Rollback reverts the newest applied migration.
//
// The down files are only worth having if something runs them; an untested
// down file is a rollback plan that fails the first time it is needed.
func Rollback(db *DB, log *slog.Logger) error {
	dialect := db.Dialector.Name()
	all, err := loadMigrations(migrations.FS, dialect)
	if err != nil {
		return err
	}
	if err := ensureVersionTable(db, dialect); err != nil {
		return err
	}
	applied, err := appliedVersions(db)
	if err != nil {
		return err
	}
	newest := highestVersion(applied)
	if newest == 0 {
		return nil
	}

	index := -1
	for i, candidate := range all {
		if candidate.version == newest {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("cannot roll back schema version %d: this build of kilasflow does not carry that migration", newest)
	}
	target := all[index]
	if len(target.down) == 0 {
		return fmt.Errorf("cannot roll back migration %s: it has no down file", target.label())
	}

	err = db.Transaction(func(tx *gorm.DB) error {
		names := definedIdentifiers(all)
		prefix := tablePrefix(db)
		for _, statement := range target.down {
			if err := tx.Exec(prefixStatement(statement, prefix, names)).Error; err != nil {
				return err
			}
		}
		return tx.Exec("DELETE FROM "+quoteIdentifier(dialect, schemaMigrationsTable)+" WHERE version = ?", target.version).Error
	})
	if err != nil {
		return fmt.Errorf("roll back migration %s: %w", target.label(), err)
	}

	log.Info("rolled back migration", "version", target.version, "name", target.name)

	return nil
}

func migrateFS(db *DB, fsys fs.FS, log *slog.Logger) error {
	dialect := db.Dialector.Name()

	all, err := loadMigrations(fsys, dialect)
	if err != nil {
		return err
	}
	// An empty migration set would create no tables and report success, so the
	// installation would only fail later, on the first query, far from the
	// packaging mistake that caused it.
	if len(all) == 0 {
		return fmt.Errorf("this build of kilasflow carries no %s migrations", dialect)
	}

	if err := ensureVersionTable(db, dialect); err != nil {
		return err
	}

	applied, err := appliedVersions(db)
	if err != nil {
		return err
	}

	prefix := tablePrefix(db)
	names := definedIdentifiers(all)
	if err := checkTablePrefix(db, prefix, all, applied); err != nil {
		return err
	}

	if len(applied) == 0 {
		adopted, err := adoptExistingSchema(db, all[0], log)
		if err != nil {
			return err
		}
		if adopted {
			applied[all[0].version] = struct{}{}
		}
	}

	newest := all[len(all)-1].version
	if found := highestVersion(applied); found > newest {
		return fmt.Errorf(
			"database is at schema version %d but this build of kilasflow knows migrations only up to %d; run a build of kilasflow at or above schema version %d against this database",
			found, newest, found,
		)
	}

	// A PostgreSQL without pgvector cannot run migration 6, and must not be
	// left half-migrated at version 5: the vector tables are an optional
	// capability (nodes probe the extension and wire a DisabledVectorStore
	// that refuses with an install message), not a boot requirement. Skipping
	// records the version as applied so a later boot with the extension
	// installed still converges — the skip is re-evaluated per boot, not
	// stamped forever — and warns loudly so the operator knows vector nodes
	// will refuse until CREATE EXTENSION vector runs.
	vectorSkipped := map[int64]bool{}
	if dialect == "postgres" {
		for _, pending := range all {
			if _, done := applied[pending.version]; done {
				continue
			}
			if !needsVectorExtension(pending) {
				continue
			}
			if vectorAvailable(db) {
				continue
			}
			vectorSkipped[pending.version] = true
		}
	}

	for _, pending := range all {
		if _, done := applied[pending.version]; done {
			continue
		}
		if vectorSkipped[pending.version] {
			if err := recordSkippedVersion(db.DB, dialect, pending); err != nil {
				return fmt.Errorf("record skipped vector migration %s: %w", pending.label(), err)
			}
			log.Warn("skipped a vector migration: PostgreSQL has no pgvector extension; vector nodes will refuse until CREATE EXTENSION vector runs",
				"version", pending.version, "name", pending.name)
			continue
		}
		if err := apply(db, dialect, pending, prefix, names); err != nil {
			return err
		}
		log.Info("applied migration", "version", pending.version, "name", pending.name)
	}
	return nil
}

// needsVectorExtension reports whether a migration requires the pgvector
// extension. Detected from the statements rather than the version number, so
// a renumbered or added vector migration is still gated without a code
// change — and a non-vector migration never is.
func needsVectorExtension(pending migration) bool {
	for _, statement := range pending.up {
		upper := strings.ToUpper(strings.TrimSpace(statement))
		if strings.HasPrefix(upper, "CREATE EXTENSION") && strings.Contains(upper, "VECTOR") {
			return true
		}
	}
	return false
}

// vectorAvailable probes pg_extension for the vector extension. A probe
// failure reads as absent: refusing to boot on a transient error would turn
// a monitoring blip into an outage, while skipping leaves a loud warning
// and vector nodes refusing with the install message either way.
func vectorAvailable(db *DB) bool {
	var present int
	if err := db.Raw("SELECT 1 FROM pg_extension WHERE extname = 'vector'").Scan(&present).Error; err != nil {
		return false
	}
	return present == 1
}

// apply runs one migration's DDL and records it, atomically.
//
// The version row is inserted before the DDL runs, and that insert is what
// stops two processes started at the same moment from applying the same
// migration twice: the primary key on version makes the second process wait on
// the first one's uncommitted row and then fail on the duplicate, on both
// SQLite and PostgreSQL. Recording after the DDL instead would leave a window
// where both processes are running CREATE TABLE.
func apply(db *DB, dialect string, pending migration, prefix string, names map[string]struct{}) error {
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := recordVersion(tx, dialect, pending); err != nil {
			return err
		}
		for _, statement := range pending.up {
			if err := tx.Exec(prefixStatement(statement, prefix, names)).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		return nil
	}

	// The loser of the race sees a duplicate key, but so would a genuine
	// conflict with an operator's own table, so the claim is re-read rather
	// than the error inspected: the row is only there if somebody committed it,
	// and a rolled-back failure takes its own version row with it.
	if applied, readErr := appliedVersions(db); readErr == nil {
		if _, done := applied[pending.version]; done {
			return nil
		}
	}

	return fmt.Errorf("apply migration %s: %w", pending.label(), err)
}

// adoptExistingSchema stamps an install whose tables AutoMigrate created.
//
// It records the baseline as applied and runs no DDL. Running the baseline
// instead would mean CREATE TABLE over live data on the first upgrade in the
// field, which fails at best and is why this case is detected rather than left
// to the CREATE TABLE to discover.
func adoptExistingSchema(db *DB, baseline migration, log *slog.Logger) (bool, error) {
	tables := baseline.createdTables()
	if len(tables) == 0 {
		return false, nil
	}
	prefix := tablePrefix(db)
	for _, table := range tables {
		if !db.Migrator().HasTable(prefix + table) {
			continue
		}
		if err := recordVersion(db.DB, db.Dialector.Name(), baseline); err != nil {
			return false, fmt.Errorf("adopt existing schema as migration %s: %w", baseline.label(), err)
		}
		log.Info("adopted an existing schema as the migration baseline", "version", baseline.version, "table", prefix+table)
		return true, nil
	}
	return false, nil
}

func recordVersion(tx *gorm.DB, dialect string, applied migration) error {
	return tx.Exec(
		"INSERT INTO "+quoteIdentifier(dialect, schemaMigrationsTable)+" (version, name, applied_at) VALUES (?, ?, ?)",
		applied.version, applied.name, time.Now().UTC(),
	).Error
}

// recordSkippedVersion records a migration this boot decided not to run.
//
// The insert has to survive losing a race, because two processes started
// together make the same decision about the same migration and only one of them
// can insert the row. `apply` handles that collision by re-reading the table
// after the error, which works there because its insert is also the claim that
// keeps the DDL from running twice and the loser has something to wait for. A
// skipped migration has no DDL behind it and nothing to wait for, so the
// conflict is declared in the statement instead and the loser carries on. That
// is what the constants below spell per dialect: SQLite's INSERT OR IGNORE and
// PostgreSQL's ON CONFLICT DO NOTHING.
//
// Only the version row is tolerant, never the migration: a skipped migration is
// still recorded exactly once, and a database whose owner later installs the
// extension converges on the next boot that finds it already there.
func recordSkippedVersion(db *gorm.DB, dialect string, skipped migration) error {
	statement := "INSERT OR IGNORE INTO " + quoteIdentifier(dialect, schemaMigrationsTable) +
		" (version, name, applied_at) VALUES (?, ?, ?)"
	if dialect == "postgres" {
		statement = "INSERT INTO " + quoteIdentifier(dialect, schemaMigrationsTable) +
			" (version, name, applied_at) VALUES (?, ?, ?) ON CONFLICT (version) DO NOTHING"
	}
	return db.Exec(statement, skipped.version, skipped.name, time.Now().UTC()).Error
}

func ensureVersionTable(db *DB, dialect string) error {
	ddl, err := versionTableDDL(dialect)
	if err != nil {
		return err
	}
	if err := db.Exec(ddl).Error; err != nil {
		// PostgreSQL's CREATE TABLE IF NOT EXISTS is not safe against another
		// connection creating the same table at the same moment: the loser does
		// not see the table appear, it fails on a duplicate pg_type row. Two
		// processes starting together is the case this table exists to handle,
		// so the loser looks rather than gives up.
		if db.Migrator().HasTable(schemaMigrationsTable) {
			return nil
		}
		return fmt.Errorf("create the %s table: %w", schemaMigrationsTable, err)
	}
	return nil
}

func versionTableDDL(dialect string) (string, error) {
	switch dialect {
	case "sqlite":
		return "CREATE TABLE IF NOT EXISTS `schema_migrations` (" +
			"`version` integer NOT NULL," +
			"`name` text NOT NULL," +
			"`applied_at` datetime NOT NULL," +
			"PRIMARY KEY (`version`))", nil
	case "postgres":
		return `CREATE TABLE IF NOT EXISTS "schema_migrations" (` +
			`"version" bigint NOT NULL,` +
			`"name" varchar(255) NOT NULL,` +
			`"applied_at" timestamptz NOT NULL,` +
			`PRIMARY KEY ("version"))`, nil
	default:
		return "", fmt.Errorf("no migrations exist for the %q dialect", dialect)
	}
}

func appliedVersions(db *DB) (map[int64]struct{}, error) {
	var versions []int64
	query := "SELECT version FROM " + quoteIdentifier(db.Dialector.Name(), schemaMigrationsTable) + " ORDER BY version"
	if err := db.Raw(query).Scan(&versions).Error; err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	applied := make(map[int64]struct{}, len(versions))
	for _, version := range versions {
		applied[version] = struct{}{}
	}
	return applied, nil
}

func highestVersion(applied map[int64]struct{}) int64 {
	var highest int64
	for version := range applied {
		if version > highest {
			highest = version
		}
	}
	return highest
}

// quoteIdentifier quotes a bare identifier the way the dialect expects, so a
// later migration that renames these objects behind a prefix does not have to
// build the same string twice.
func quoteIdentifier(dialect, name string) string {
	if dialect == "sqlite" {
		return "`" + name + "`"
	}
	return `"` + name + `"`
}

// loadMigrations reads one dialect's directory, newest last.
//
// A dialect with no directory is not an error here; migrateFS reports the empty
// set, which names the actual problem rather than a missing path.
func loadMigrations(fsys fs.FS, dialect string) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, dialect)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the %s migrations: %w", dialect, err)
	}

	byVersion := map[int64]*migration{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, name, direction, err := parseMigrationName(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", dialect, entry.Name(), err)
		}
		body, err := fs.ReadFile(fsys, path.Join(dialect, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s/%s: %w", dialect, entry.Name(), err)
		}

		existing, ok := byVersion[version]
		if !ok {
			existing = &migration{version: version, name: name}
			byVersion[version] = existing
		}
		if existing.name != name {
			return nil, fmt.Errorf("%s: migration %06d is named both %q and %q", dialect, version, existing.name, name)
		}
		if direction == "up" {
			existing.up = splitStatements(string(body))
		} else {
			existing.down = splitStatements(string(body))
		}
	}

	all := make([]migration, 0, len(byVersion))
	for _, loaded := range byVersion {
		all = append(all, *loaded)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].version < all[j].version })

	return all, nil
}

// parseMigrationName splits 000001_baseline.up.sql into its three parts.
func parseMigrationName(filename string) (version int64, name, direction string, err error) {
	trimmed := strings.TrimSuffix(filename, ".sql")
	switch {
	case strings.HasSuffix(trimmed, ".up"):
		direction = "up"
	case strings.HasSuffix(trimmed, ".down"):
		direction = "down"
	default:
		return 0, "", "", errors.New("a migration file must end in .up.sql or .down.sql")
	}
	trimmed = strings.TrimSuffix(trimmed, "."+direction)

	digits, name, found := strings.Cut(trimmed, "_")
	if !found || digits == "" || name == "" {
		return 0, "", "", errors.New("a migration file must be named <version>_<name>.<direction>.sql")
	}
	version, err = strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, "", "", fmt.Errorf("%q is not a version number", digits)
	}
	// Zero is what "nothing has been applied" reads as, so a migration numbered
	// zero would be indistinguishable from an empty database.
	if version <= 0 {
		return 0, "", "", errors.New("a migration version must be greater than zero")
	}

	return version, name, direction, nil
}

// createdTables names the tables a migration's up statements create, which is
// how an install created by AutoMigrate is recognised without hard-coding a
// table name here that could drift away from the baseline.
func (m migration) createdTables() []string {
	var tables []string
	for _, statement := range m.up {
		fields := strings.Fields(statement)
		if len(fields) < 3 || !strings.EqualFold(fields[0], "CREATE") || !strings.EqualFold(fields[1], "TABLE") {
			continue
		}
		rest := strings.Join(fields[2:], " ")
		rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), "IF NOT EXISTS"))
		name, _, _ := strings.Cut(rest, "(")
		name = strings.TrimSpace(name)
		name = strings.Trim(name, "`\"[]")
		if name != "" {
			tables = append(tables, name)
		}
	}
	return tables
}

// tablePrefix is the configured table prefix read back off the handle Open
// built, so the migration runner and the ORM resolve every name through the
// same value and can never disagree about it. A handle Open did not build —
// one with no naming strategy — reads as no prefix, which is what every
// existing install has.
func tablePrefix(db *DB) string {
	if db == nil || db.Config == nil {
		return ""
	}
	if namer, ok := db.Config.NamingStrategy.(schema.NamingStrategy); ok {
		return namer.TablePrefix
	}
	return ""
}

// checkTablePrefix refuses to run when the database already holds a KilasFlow
// schema under a different prefix than the one configured. Without it, an
// install populated without a prefix and then restarted with table_prefix set
// would silently create a second empty schema alongside the populated one,
// and every query would read the empty one.
//
// A prefix is a naming convention, not an isolation boundary: anything holding
// the connection can still read every table. This check stops accidents, not
// adversaries.
func checkTablePrefix(db *DB, prefix string, all []migration, applied map[int64]struct{}) error {
	var tables []string
	seen := map[string]struct{}{}
	for _, m := range all {
		for _, table := range m.createdTables() {
			if _, dup := seen[table]; dup {
				continue
			}
			seen[table] = struct{}{}
			tables = append(tables, table)
		}
	}
	for _, table := range tables {
		if db.Migrator().HasTable(prefix + table) {
			return nil
		}
	}
	if prefix != "" && holdsBareBaseline(db, all) {
		return fmt.Errorf("database already holds KilasFlow tables without a prefix but database.table_prefix is %q: refusing to create a second schema alongside them",
			prefix)
	}
	// A version row is written in the same transaction as its DDL, so history
	// without matching tables means the tables live under another prefix.
	if len(applied) > 0 {
		return fmt.Errorf("database has schema version history but no tables under table_prefix %q: it was migrated with a different prefix; refusing to create a second schema alongside it",
			prefix)
	}
	return nil
}

// holdsBareBaseline reports whether the database holds every table of the
// baseline migration without a prefix. All of them, not any one: a shared
// database's host application may own workflows, executions or credentials
// under those exact names, and one overlapping name is coexistence, not a
// second install. A full bare baseline with no version history is an install
// AutoMigrate built, which a prefix must never adopt as its own.
func holdsBareBaseline(db *DB, all []migration) bool {
	if len(all) == 0 {
		return false
	}
	tables := all[0].createdTables()
	if len(tables) == 0 {
		return false
	}
	for _, table := range tables {
		if !db.Migrator().HasTable(table) {
			return false
		}
	}
	return true
}

// definedIdentifiers collects every table, index and constraint name the
// migrations define, so prefixStatement can rewrite each where it is
// referenced. Collected from the same files that are run rather than written
// out here, so a later migration that adds an identifier is picked up without
// anyone remembering to extend a list.
func definedIdentifiers(all []migration) map[string]struct{} {
	names := map[string]struct{}{}
	for _, m := range all {
		for _, name := range m.definedNames() {
			names[name] = struct{}{}
		}
	}
	return names
}

// definedNames is createdTables plus the index and constraint names the same
// statements declare.
func (m migration) definedNames() []string {
	var names []string
	names = append(names, m.createdTables()...)
	for _, statement := range m.up {
		if index := statementIndexName(statement); index != "" {
			names = append(names, index)
		}
		names = append(names, statementConstraintNames(statement)...)
	}
	return names
}

// statementIndexName names the index a CREATE INDEX statement defines, or
// reports none. Field-based like createdTables: migration statements are
// machine-shaped, and a regular expression would only restate the same shape
// less readably.
func statementIndexName(statement string) string {
	fields := strings.Fields(statement)
	pos := 0
	take := func() string {
		if pos >= len(fields) {
			return ""
		}
		token := fields[pos]
		pos++
		return token
	}
	peek := func() string {
		if pos >= len(fields) {
			return ""
		}
		return fields[pos]
	}
	if !strings.EqualFold(take(), "CREATE") {
		return ""
	}
	if strings.EqualFold(peek(), "UNIQUE") {
		pos++
	}
	if !strings.EqualFold(take(), "INDEX") {
		return ""
	}
	if strings.EqualFold(peek(), "IF") {
		pos += 3
	}
	return strings.Trim(take(), "`\"[]")
}

// statementConstraintNames names every CONSTRAINT a statement declares. A
// CREATE TABLE carries one per foreign key, so there can be several.
func statementConstraintNames(statement string) []string {
	var names []string
	fields := strings.Fields(statement)
	for i, token := range fields {
		if strings.EqualFold(token, "CONSTRAINT") && i+1 < len(fields) {
			if name := strings.Trim(fields[i+1], "`\"[]"); name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

// prefixStatement rewrites every quoted table, index and constraint name in
// one migration statement under the configured prefix. Only quoted
// occurrences move: string literals stay single-quoted in both dialects, and
// every table, index and constraint reference the migrations carry is quoted,
// while column names are never in the defined set. An empty prefix returns the
// statement untouched, so the default install runs byte-identical DDL.
func prefixStatement(statement, prefix string, names map[string]struct{}) string {
	if prefix == "" {
		return statement
	}
	for name := range names {
		statement = strings.ReplaceAll(statement, "`"+name+"`", "`"+prefix+name+"`")
		statement = strings.ReplaceAll(statement, `"`+name+`"`, `"`+prefix+name+`"`)
	}
	return statement
}

// splitStatements breaks a migration file into the statements to run one at a
// time, and drops its comments.
//
// Quoting is tracked rather than the file split on every semicolon, because a
// semicolon inside a string literal or a quoted identifier would otherwise cut
// a statement in half and the halves would fail with a syntax error that says
// nothing about why.
func splitStatements(sql string) []string {
	var (
		statements []string
		current    strings.Builder
	)
	flush := func() {
		statement := strings.TrimSpace(current.String())
		current.Reset()
		if statement != "" {
			statements = append(statements, statement)
		}
	}

	runes := []rune(sql)
	for i := 0; i < len(runes); i++ {
		char := runes[i]
		switch {
		case char == '-' && i+1 < len(runes) && runes[i+1] == '-':
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
			current.WriteRune('\n')

		case char == '/' && i+1 < len(runes) && runes[i+1] == '*':
			i += 2
			for i+1 < len(runes) && !(runes[i] == '*' && runes[i+1] == '/') {
				i++
			}
			i++
			current.WriteRune(' ')

		case char == '\'' || char == '"' || char == '`':
			quote := char
			current.WriteRune(char)
			i++
			for i < len(runes) {
				current.WriteRune(runes[i])
				if runes[i] == quote {
					// A doubled quote is an escaped one, not the end.
					if i+1 < len(runes) && runes[i+1] == quote {
						i++
						current.WriteRune(runes[i])
					} else {
						break
					}
				}
				i++
			}

		case char == ';':
			flush()

		default:
			current.WriteRune(char)
		}
	}
	flush()

	return statements
}
