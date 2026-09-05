// Package sqlnode connects workflows to databases the user configures.
//
// These connections are deliberately built from scratch out of credential
// fields and opened through database/sql, never through the internal GORM
// handle. There is no default, inferred, or selectable connection: a workflow
// can only reach a database whose credential someone deliberately created, so
// the SQL nodes can never become a backdoor into KilasFlow's own storage.
package sqlnode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	// Registered for their database/sql driver names only.
	_ "github.com/glebarez/go-sqlite"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Driver names one supported external database.
type Driver string

const (
	DriverPostgres Driver = "postgres"
	DriverMySQL    Driver = "mysql"
	DriverSQLite   Driver = "sqlite"
)

// ErrForbiddenTarget reports a connection the policy refuses to open.
var ErrForbiddenTarget = errors.New("database target is not allowed")

// Limits bound one query.
type Limits struct {
	// Timeout bounds a single statement.
	Timeout time.Duration
	// MaxRows bounds how many rows are read into items.
	MaxRows int
}

// DefaultLimits are used when a node configures nothing.
func DefaultLimits() Limits {
	return Limits{Timeout: 30 * time.Second, MaxRows: 10_000}
}

// Ceiling bounds what a workflow document is allowed to ask for.
//
// A node's limits come from its parameters, and a parameter may be an
// expression over the incoming item — so `{{ $json.maxRows }}` behind a webhook
// lets whoever calls that webhook choose how much of the customer's database
// this server buffers into memory. Limits are what a workflow wants; the
// ceiling is what the deployment allows, and a document cannot raise it.
type Ceiling struct {
	// MaxRows bounds MaxRows. Zero means DefaultCeiling's value; there is no
	// spelling for "unbounded", because an unbounded row buffer is the defect.
	MaxRows int
	// MaxTimeout bounds Timeout, so a statement cannot hold a connection to
	// the user's database open indefinitely.
	MaxTimeout time.Duration
}

// DefaultCeiling is the bound applied when a deployment configures nothing.
//
// Both values sit well above the node defaults (10,000 rows, 30 seconds) on
// purpose: the ceiling exists to stop a document asking for something absurd,
// not to second-guess an author who knows their own data.
func DefaultCeiling() Ceiling {
	return Ceiling{MaxRows: 50_000, MaxTimeout: 5 * time.Minute}
}

// Apply clamps limits to the ceiling and reports what it clamped.
//
// It clamps rather than refusing. A workflow asking for more rows than the
// deployment allows still wants the rows it can have, and failing the run
// outright teaches the author nothing about where the boundary is. The
// returned map is the clamped-to values, so the node can say on its output
// that it did not read everything — silently returning fewer rows than were
// asked for is how a partial read gets mistaken for a complete one.
func (ceiling Ceiling) Apply(limits Limits) (Limits, map[string]any) {
	fallback := DefaultCeiling()
	if ceiling.MaxRows <= 0 {
		ceiling.MaxRows = fallback.MaxRows
	}
	if ceiling.MaxTimeout <= 0 {
		ceiling.MaxTimeout = fallback.MaxTimeout
	}

	var clamped map[string]any
	if limits.MaxRows > ceiling.MaxRows {
		limits.MaxRows = ceiling.MaxRows
		clamped = map[string]any{"maxRows": float64(ceiling.MaxRows)}
	}
	if limits.Timeout > ceiling.MaxTimeout {
		limits.Timeout = ceiling.MaxTimeout
		if clamped == nil {
			clamped = map[string]any{}
		}
		clamped["timeoutSeconds"] = ceiling.MaxTimeout.Seconds()
	}
	return limits, clamped
}

// Guard describes paths a SQLite credential must never be able to open.
type Guard struct {
	// InternalPaths are KilasFlow's own database files. A workflow that could
	// open one would be able to read every credential, workflow, and execution
	// in the installation.
	InternalPaths []string
}

// Result is one executed statement's outcome.
type Result struct {
	// Rows are the returned rows as item JSON, for a query.
	Rows []map[string]any
	// RowsAffected is set for a statement that returns no rows.
	RowsAffected int64
	// Truncated reports that MaxRows stopped the read.
	Truncated bool
}

// Connection is an open external database handle.
type Connection struct {
	db     *sql.DB
	driver Driver
}

// Close releases the connection. Every Open must be paired with one, which is
// why Open returns a closer rather than caching handles: a workflow's database
// credential can change or be revoked between runs, so a cached pool would
// keep using access that was withdrawn.
func (connection *Connection) Close() error {
	if connection == nil || connection.db == nil {
		return nil
	}
	return connection.db.Close()
}

// Open builds a connection from credential fields.
func Open(ctx context.Context, driver Driver, fields map[string]string, guard Guard) (*Connection, error) {
	name, dsn, err := dataSource(driver, fields, guard)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(name, dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s connection: %w", driver, err)
	}
	// One connection per node run keeps transaction scope obvious and avoids a
	// pool that outlives the credential it was built from.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to %s database: %w", driver, err)
	}
	return &Connection{db: db, driver: driver}, nil
}

// dataSource turns credential fields into a driver name and DSN.
func dataSource(driver Driver, fields map[string]string, guard Guard) (string, string, error) {
	switch driver {
	case DriverPostgres:
		return "pgx", postgresDSN(fields), nil
	case DriverMySQL:
		return "mysql", mysqlDSN(fields), nil
	case DriverSQLite:
		path, err := sqlitePath(fields, guard)
		if err != nil {
			return "", "", err
		}
		return "sqlite", path, nil
	default:
		return "", "", fmt.Errorf("database driver %q is not supported", driver)
	}
}

func postgresDSN(fields map[string]string) string {
	values := url.Values{}
	sslMode := strings.TrimSpace(fields["sslMode"])
	if sslMode == "" {
		// Defaulting to require rather than disable: a workflow database is
		// usually remote, and silently sending credentials in the clear is not
		// a default anyone would choose deliberately.
		sslMode = "require"
	}
	values.Set("sslmode", sslMode)

	target := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(fields["user"], fields["password"]),
		Host:     hostPort(fields, "5432"),
		Path:     "/" + strings.TrimPrefix(strings.TrimSpace(fields["database"]), "/"),
		RawQuery: values.Encode(),
	}
	return target.String()
}

func mysqlDSN(fields map[string]string) string {
	parameters := "?parseTime=true"
	if tls := strings.TrimSpace(fields["tls"]); tls != "" {
		parameters += "&tls=" + url.QueryEscape(tls)
	}
	return fmt.Sprintf("%s:%s@tcp(%s)/%s%s",
		fields["user"], fields["password"], hostPort(fields, "3306"),
		strings.TrimSpace(fields["database"]), parameters)
}

func hostPort(fields map[string]string, fallbackPort string) string {
	host := strings.TrimSpace(fields["host"])
	if host == "" {
		host = "localhost"
	}
	port := strings.TrimSpace(fields["port"])
	if port == "" {
		port = fallbackPort
	}
	if _, err := strconv.Atoi(port); err != nil {
		port = fallbackPort
	}
	return host + ":" + port
}

// sqlitePath resolves and guards a SQLite credential's file.
//
// A SQLite credential names a file on the server's own disk, so it is the one
// driver where a mistake reaches KilasFlow's own data. The path must be given
// explicitly, must be absolute after resolution, and must not be an internal
// database file.
func sqlitePath(fields map[string]string, guard Guard) (string, error) {
	raw := strings.TrimSpace(fields["path"])
	if raw == "" {
		return "", fmt.Errorf("%w: a SQLite credential must name an explicit file path", ErrForbiddenTarget)
	}
	// A URI form could carry ?mode= or an attached database, so only plain
	// paths are accepted.
	if strings.Contains(raw, "?") || strings.HasPrefix(raw, "file:") {
		return "", fmt.Errorf("%w: SQLite path must be a plain file path", ErrForbiddenTarget)
	}
	if strings.EqualFold(raw, ":memory:") {
		return "", fmt.Errorf("%w: an in-memory SQLite database is not a durable target", ErrForbiddenTarget)
	}

	resolved, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("%w: SQLite path could not be resolved", ErrForbiddenTarget)
	}
	resolved = canonical(resolved)

	for _, internal := range guard.InternalPaths {
		if internal == "" {
			continue
		}
		candidate, err := filepath.Abs(internal)
		if err != nil {
			continue
		}
		candidate = canonical(candidate)
		if sameFile(resolved, candidate) {
			return "", fmt.Errorf("%w: that path is KilasFlow's own database", ErrForbiddenTarget)
		}
		// SQLite writes -wal and -shm siblings; naming one of those reaches the
		// same database.
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			if sameFile(resolved, candidate+suffix) {
				return "", fmt.Errorf("%w: that path is part of KilasFlow's own database", ErrForbiddenTarget)
			}
		}
	}
	return resolved, nil
}

// canonical resolves a path to a single comparable form.
//
// Symlinks are resolved through the *directory* rather than the file, so a
// target that does not exist yet — a WAL sidecar, a database about to be
// created — still normalizes the same way as one that does. Resolving only the
// existing file would leave `/var/...` and `/private/var/...` looking like
// different paths on macOS, which is exactly the gap a guard must not have.
func canonical(path string) string {
	path = filepath.Clean(path)
	directory, base := filepath.Split(path)
	if resolved, err := filepath.EvalSymlinks(filepath.Clean(directory)); err == nil {
		path = filepath.Join(resolved, base)
	}
	// A path that is itself a symlink to the guarded file must resolve to it.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// sameFile compares by inode when both exist, and by canonical path otherwise,
// so a hard link or a relative spelling cannot slip past.
func sameFile(left, right string) bool {
	if left == right {
		return true
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return os.SameFile(leftInfo, rightInfo)
}

// Query runs one statement with bound parameters.
//
// Parameters are always bound, never interpolated: a workflow's SQL comes from
// a document a tenant may author, so string-building the statement would make
// every workflow an injection vector into the user's own database.
func (connection *Connection) Query(ctx context.Context, statement string, parameters []any, limits Limits) (Result, error) {
	if connection == nil || connection.db == nil {
		return Result{}, fmt.Errorf("database connection is not open")
	}
	if strings.TrimSpace(statement) == "" {
		return Result{}, fmt.Errorf("statement is required")
	}
	if limits.Timeout <= 0 {
		limits.Timeout = DefaultLimits().Timeout
	}
	if limits.MaxRows <= 0 {
		limits.MaxRows = DefaultLimits().MaxRows
	}

	queryCtx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()

	rows, err := connection.db.QueryContext(queryCtx, statement, parameters...)
	if err != nil {
		return Result{}, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	return scanRows(rows, limits.MaxRows)
}

// scanRows reads a result set into item JSON, stopping at maxRows.
//
// Shared by Query and by a transaction statement declared as returning, so a
// row coming back out of a transaction is normalized exactly like one coming
// back out of a query — a driver value that became a string on one path and
// base64 on the other would be a difference nobody could explain.
func scanRows(rows *sql.Rows, maxRows int) (Result, error) {
	columns, err := rows.Columns()
	if err != nil {
		return Result{}, fmt.Errorf("read columns: %w", err)
	}

	result := Result{Rows: make([]map[string]any, 0, 16)}
	for rows.Next() {
		if len(result.Rows) >= maxRows {
			result.Truncated = true
			break
		}
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return Result{}, fmt.Errorf("scan row: %w", err)
		}
		row := make(map[string]any, len(columns))
		for index, column := range columns {
			row[column] = normalize(values[index])
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return Result{}, fmt.Errorf("read rows: %w", err)
	}
	return result, nil
}

// Execute runs a statement that returns no rows.
//
// It is a separate operation from Query so a node's configured intent is
// explicit: "execute" reports rows affected, "query" returns items.
func (connection *Connection) Execute(ctx context.Context, statement string, parameters []any, limits Limits) (Result, error) {
	if connection == nil || connection.db == nil {
		return Result{}, fmt.Errorf("database connection is not open")
	}
	if strings.TrimSpace(statement) == "" {
		return Result{}, fmt.Errorf("statement is required")
	}
	if limits.Timeout <= 0 {
		limits.Timeout = DefaultLimits().Timeout
	}
	executeCtx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()

	outcome, err := connection.db.ExecContext(executeCtx, statement, parameters...)
	if err != nil {
		return Result{}, fmt.Errorf("statement failed: %w", err)
	}
	affected, err := outcome.RowsAffected()
	if err != nil {
		// Some drivers cannot report this. Zero with no error is honest; the
		// statement still ran.
		affected = 0
	}
	return Result{RowsAffected: affected, Rows: []map[string]any{}}, nil
}

// ExecuteBatch runs one statement once per bound parameter set, atomically.
//
// A node used to run a statement per input item on its own, so a hundred-row
// insert was a hundred parses and a hundred round trips — and, because Open
// pins the pool to one connection, a hundred serial ones. Here the statement
// is prepared once and every set goes through the prepared handle.
//
// The whole batch is one transaction. A bulk write that fails halfway used to
// leave the first half applied and report only the error, which is the worst
// of the three possible outcomes: nothing tells the author how far it got, and
// re-running duplicates whatever did land.
//
// The timeout bounds each statement rather than the batch, which is what the
// node's "statement timeout" says it does. The batch as a whole is bounded by
// ctx — the node's own timeout setting and the execution's.
//
// Statements are prepared per distinct SQL text and reused while it stays the
// same, which is one prepare for the ordinary case where every item runs the
// same statement with different parameters. The text can differ per item only
// because it may be built from an expression; when it does, the batch is still
// one transaction.
func (connection *Connection) ExecuteBatch(ctx context.Context, statements []Statement, limits Limits) ([]Result, error) {
	if connection == nil || connection.db == nil {
		return nil, fmt.Errorf("database connection is not open")
	}
	if len(statements) == 0 {
		return nil, nil
	}
	for index, statement := range statements {
		if strings.TrimSpace(statement.SQL) == "" {
			return nil, fmt.Errorf("item %d: statement is required", index+1)
		}
	}
	if limits.Timeout <= 0 {
		limits.Timeout = DefaultLimits().Timeout
	}

	tx, err := connection.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}

	var (
		prepared    *sql.Stmt
		preparedSQL string
	)
	// Named so both the failure paths and the success path release it.
	closePrepared := func() {
		if prepared != nil {
			_ = prepared.Close()
			prepared = nil
		}
	}

	results := make([]Result, 0, len(statements))
	for index, statement := range statements {
		if prepared == nil || statement.SQL != preparedSQL {
			closePrepared()
			stmt, err := tx.PrepareContext(ctx, statement.SQL)
			if err != nil {
				_ = tx.Rollback()
				return nil, fmt.Errorf("prepare statement for item %d and the batch was rolled back: %w", index+1, err)
			}
			prepared, preparedSQL = stmt, statement.SQL
		}

		outcome, err := func() (sql.Result, error) {
			statementCtx, cancel := context.WithTimeout(ctx, limits.Timeout)
			defer cancel()
			return prepared.ExecContext(statementCtx, statement.Parameters...)
		}()
		if err != nil {
			closePrepared()
			_ = tx.Rollback()
			// The item number is the point of the message: "statement failed"
			// over five hundred items says nothing about which row was wrong.
			return nil, fmt.Errorf("item %d failed and the batch was rolled back: %w", index+1, err)
		}
		affected, _ := outcome.RowsAffected()
		results = append(results, Result{RowsAffected: affected, Rows: []map[string]any{}})
	}

	closePrepared()
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit batch: %w", err)
	}
	return results, nil
}

// Transaction runs several statements atomically.
//
// The transaction is scoped to one node execution: it commits when every
// statement succeeds and rolls back on the first failure, so a partially
// applied batch is never left behind.
func (connection *Connection) Transaction(ctx context.Context, statements []Statement, limits Limits) ([]Result, error) {
	if connection == nil || connection.db == nil {
		return nil, fmt.Errorf("database connection is not open")
	}
	if limits.Timeout <= 0 {
		limits.Timeout = DefaultLimits().Timeout
	}
	txCtx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()

	tx, err := connection.db.BeginTx(txCtx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	if limits.MaxRows <= 0 {
		limits.MaxRows = DefaultLimits().MaxRows
	}

	results := make([]Result, 0, len(statements))
	for index, statement := range statements {
		// Only a statement that declared itself returning goes through Query.
		// Routing every statement that way looks correct and is not: a
		// non-returning statement run through Query comes back as a result set
		// with no columns and no reachable RowsAffected, so every transaction
		// in the installation would quietly start reporting zero rows affected
		// while still saying it committed. Nothing errors, and the first report
		// is somebody reconciling counts weeks later.
		if statement.Returning {
			result, err := returningStatement(txCtx, tx, statement, limits.MaxRows)
			if err != nil {
				_ = tx.Rollback()
				return nil, fmt.Errorf("statement %d failed and the transaction was rolled back: %w", index+1, err)
			}
			results = append(results, result)
			continue
		}
		outcome, err := tx.ExecContext(txCtx, statement.SQL, statement.Parameters...)
		if err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("statement failed and the transaction was rolled back: %w", err)
		}
		affected, _ := outcome.RowsAffected()
		results = append(results, Result{RowsAffected: affected, Rows: []map[string]any{}})
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}
	return results, nil
}

// Statement is one SQL statement with its bound parameters.
type Statement struct {
	SQL        string
	Parameters []any
	// Returning marks a statement whose rows the caller wants back, so an
	// `INSERT … RETURNING id` inside a transaction hands the generated id on
	// instead of discarding it.
	//
	// It is declared, never sniffed from the SQL text. Looking for a leading
	// SELECT or a trailing RETURNING is defeated by a comment, a CTE, or a
	// `WITH … RETURNING`, and the right answer differs per driver — so the
	// heuristic would be wrong on exactly the statements worth writing.
	Returning bool
}

// returningStatement runs one declared-returning statement inside a transaction
// and reads its rows.
//
// RowsAffected is set from the row count rather than left at zero: a driver
// does not report it for a statement run through Query, and the summary a node
// builds from these results has to keep adding up. It therefore counts the rows
// that were read, which is the same thing unless MaxRows stopped the read — and
// that case sets Truncated, so the partial count is never presented as a whole
// one.
func returningStatement(ctx context.Context, tx *sql.Tx, statement Statement, maxRows int) (Result, error) {
	rows, err := tx.QueryContext(ctx, statement.SQL, statement.Parameters...)
	if err != nil {
		return Result{}, err
	}
	defer rows.Close()

	result, err := scanRows(rows, maxRows)
	if err != nil {
		return Result{}, err
	}
	result.RowsAffected = int64(len(result.Rows))
	return result, nil
}

// normalize converts driver values into JSON-safe item data.
func normalize(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []byte:
		// Drivers return text columns as bytes; JSON would otherwise encode
		// them as base64 and make a plain string unrecognizable.
		return string(typed)
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
	default:
		return typed
	}
}
