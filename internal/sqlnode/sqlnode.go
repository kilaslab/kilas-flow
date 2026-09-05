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

	columns, err := rows.Columns()
	if err != nil {
		return Result{}, fmt.Errorf("read columns: %w", err)
	}

	result := Result{Rows: make([]map[string]any, 0, 16)}
	for rows.Next() {
		if len(result.Rows) >= limits.MaxRows {
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
	results := make([]Result, 0, len(statements))
	for _, statement := range statements {
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
