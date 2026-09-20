package sqlnode_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/sqlnode"
)

func TestSQLiteRequiresAnExplicitPath(t *testing.T) {
	t.Parallel()

	for name, fields := range map[string]map[string]string{
		"absent":    {},
		"empty":     {"path": "   "},
		"in-memory": {"path": ":memory:"},
		"uri form":  {"path": "file:data.db?mode=ro"},
		"query":     {"path": "data.db?cache=shared"},
	} {
		_, err := sqlnode.Open(context.Background(), sqlnode.DriverSQLite, fields, sqlnode.Guard{})
		if !errors.Is(err, sqlnode.ErrForbiddenTarget) {
			t.Errorf("%s path = %v, want ErrForbiddenTarget", name, err)
		}
	}
}

func TestSQLiteRefusesKilasFlowsOwnDatabase(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	internal := filepath.Join(directory, "kilasflow.db")
	if err := os.WriteFile(internal, []byte("internal"), 0o600); err != nil {
		t.Fatalf("write internal database: %v", err)
	}
	guard := sqlnode.Guard{InternalPaths: []string{internal}}

	// The exact path, its WAL sidecar, and a relative spelling of the same file
	// all reach the same database, so all three must be refused.
	for name, path := range map[string]string{
		"exact":    internal,
		"wal":      internal + "-wal",
		"shm":      internal + "-shm",
		"relative": filepath.Join(directory, ".", "kilasflow.db"),
		"dotted":   filepath.Join(directory, "sub", "..", "kilasflow.db"),
	} {
		_, err := sqlnode.Open(context.Background(), sqlnode.DriverSQLite, map[string]string{"path": path}, guard)
		if !errors.Is(err, sqlnode.ErrForbiddenTarget) {
			t.Errorf("%s (%s) = %v, want ErrForbiddenTarget", name, path, err)
		}
	}
}

func TestSQLiteRefusesASymlinkPointingAtTheInternalDatabase(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	internal := filepath.Join(directory, "kilasflow.db")
	if err := os.WriteFile(internal, []byte("internal"), 0o600); err != nil {
		t.Fatalf("write internal database: %v", err)
	}
	link := filepath.Join(directory, "innocent.db")
	if err := os.Symlink(internal, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// A string comparison alone would let this through.
	_, err := sqlnode.Open(context.Background(), sqlnode.DriverSQLite, map[string]string{"path": link},
		sqlnode.Guard{InternalPaths: []string{internal}})
	if !errors.Is(err, sqlnode.ErrForbiddenTarget) {
		t.Errorf("symlinked path = %v, want ErrForbiddenTarget", err)
	}
}

func TestSQLiteOpensAWorkflowOwnedDatabase(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	guard := sqlnode.Guard{InternalPaths: []string{filepath.Join(directory, "kilasflow.db")}}
	target := filepath.Join(directory, "customer.db")

	connection, err := sqlnode.Open(context.Background(), sqlnode.DriverSQLite, map[string]string{"path": target}, guard)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer connection.Close()

	if _, err := connection.Execute(context.Background(),
		`CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT, joined_at TEXT)`, nil, sqlnode.DefaultLimits()); err != nil {
		t.Fatalf("Execute(create) error = %v", err)
	}
}

func newSQLite(t *testing.T) *sqlnode.Connection {
	t.Helper()
	connection, err := sqlnode.Open(context.Background(), sqlnode.DriverSQLite,
		map[string]string{"path": filepath.Join(t.TempDir(), "workflow.db")}, sqlnode.Guard{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if _, err := connection.Execute(context.Background(),
		`CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT, tier TEXT)`, nil, sqlnode.DefaultLimits()); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return connection
}

func TestQueryBindsParametersRatherThanInterpolatingThem(t *testing.T) {
	t.Parallel()

	connection := newSQLite(t)
	if _, err := connection.Execute(context.Background(),
		`INSERT INTO customers (name, tier) VALUES (?, ?)`, []any{"Ada", "gold"}, sqlnode.DefaultLimits()); err != nil {
		t.Fatalf("Execute(insert) error = %v", err)
	}

	// A classic injection payload must be matched as data, not executed.
	injection := "Ada'; DROP TABLE customers; --"
	result, err := connection.Query(context.Background(),
		`SELECT id, name, tier FROM customers WHERE name = ?`, []any{injection}, sqlnode.DefaultLimits())
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(result.Rows) != 0 {
		t.Fatalf("injection payload matched %d rows, want 0", len(result.Rows))
	}

	// The table must still exist, which it would not if the payload had run.
	survived, err := connection.Query(context.Background(), `SELECT name, tier FROM customers`, nil, sqlnode.DefaultLimits())
	if err != nil {
		t.Fatalf("Query(after injection) error = %v", err)
	}
	if len(survived.Rows) != 1 || survived.Rows[0]["name"] != "Ada" || survived.Rows[0]["tier"] != "gold" {
		t.Fatalf("rows = %#v, want the original row intact", survived.Rows)
	}
}

func TestQueryMapsRowsToItemFriendlyValues(t *testing.T) {
	t.Parallel()

	connection := newSQLite(t)
	if _, err := connection.Execute(context.Background(),
		`INSERT INTO customers (id, name, tier) VALUES (?, ?, ?)`, []any{7, "Grace", nil}, sqlnode.DefaultLimits()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	result, err := connection.Query(context.Background(), `SELECT id, name, tier FROM customers`, nil, sqlnode.DefaultLimits())
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(result.Rows))
	}
	row := result.Rows[0]
	// Text columns arrive as []byte from some drivers; JSON would encode those
	// as base64 and make a plain string unrecognizable in an item.
	if name, ok := row["name"].(string); !ok || name != "Grace" {
		t.Errorf("name = %#v, want the string \"Grace\"", row["name"])
	}
	if row["tier"] != nil {
		t.Errorf("tier = %#v, want nil for a NULL column", row["tier"])
	}
}

func TestQueryStopsAtTheRowLimit(t *testing.T) {
	t.Parallel()

	connection := newSQLite(t)
	for index := range 10 {
		if _, err := connection.Execute(context.Background(),
			`INSERT INTO customers (name, tier) VALUES (?, ?)`, []any{"c", index}, sqlnode.DefaultLimits()); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
	}

	result, err := connection.Query(context.Background(), `SELECT * FROM customers`, nil, sqlnode.Limits{MaxRows: 3})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(result.Rows) != 3 || !result.Truncated {
		t.Fatalf("result = (%d rows, truncated %v), want (3, true)", len(result.Rows), result.Truncated)
	}
}

func TestExecuteReportsRowsAffected(t *testing.T) {
	t.Parallel()

	connection := newSQLite(t)
	for range 3 {
		if _, err := connection.Execute(context.Background(),
			`INSERT INTO customers (name, tier) VALUES (?, ?)`, []any{"c", "silver"}, sqlnode.DefaultLimits()); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
	}

	result, err := connection.Execute(context.Background(),
		`UPDATE customers SET tier = ? WHERE tier = ?`, []any{"gold", "silver"}, sqlnode.DefaultLimits())
	if err != nil {
		t.Fatalf("Execute(update) error = %v", err)
	}
	if result.RowsAffected != 3 {
		t.Errorf("rowsAffected = %d, want 3", result.RowsAffected)
	}
}

func TestTransactionCommitsTogetherAndRollsBackTogether(t *testing.T) {
	t.Parallel()

	connection := newSQLite(t)
	if _, err := connection.Transaction(context.Background(), []sqlnode.Statement{
		{SQL: `INSERT INTO customers (name, tier) VALUES (?, ?)`, Parameters: []any{"Ada", "gold"}},
		{SQL: `INSERT INTO customers (name, tier) VALUES (?, ?)`, Parameters: []any{"Grace", "gold"}},
	}, sqlnode.DefaultLimits()); err != nil {
		t.Fatalf("Transaction() error = %v", err)
	}
	committed, _ := connection.Query(context.Background(), `SELECT COUNT(*) AS total FROM customers`, nil, sqlnode.DefaultLimits())
	if total := committed.Rows[0]["total"]; total != int64(2) {
		t.Fatalf("committed rows = %#v, want 2", total)
	}

	// The second statement is invalid, so neither may survive.
	_, err := connection.Transaction(context.Background(), []sqlnode.Statement{
		{SQL: `INSERT INTO customers (name, tier) VALUES (?, ?)`, Parameters: []any{"Alan", "gold"}},
		{SQL: `INSERT INTO no_such_table (x) VALUES (?)`, Parameters: []any{1}},
	}, sqlnode.DefaultLimits())
	if err == nil {
		t.Fatal("a failing transaction reported success")
	}
	if !strings.Contains(err.Error(), "rolled back") {
		t.Errorf("error = %v, want it to say the transaction rolled back", err)
	}
	after, _ := connection.Query(context.Background(), `SELECT COUNT(*) AS total FROM customers`, nil, sqlnode.DefaultLimits())
	if total := after.Rows[0]["total"]; total != int64(2) {
		t.Fatalf("rows after rollback = %#v, want the original 2", total)
	}
}

func TestQueryReportsAFailingStatement(t *testing.T) {
	t.Parallel()

	connection := newSQLite(t)
	if _, err := connection.Query(context.Background(), `SELECT * FROM no_such_table`, nil, sqlnode.DefaultLimits()); err == nil {
		t.Error("a query against a missing table reported success")
	}
	if _, err := connection.Query(context.Background(), "   ", nil, sqlnode.DefaultLimits()); err == nil {
		t.Error("an empty statement was accepted")
	}
}

func TestQueryHonoursItsTimeout(t *testing.T) {
	t.Parallel()

	connection := newSQLite(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := connection.Query(ctx, `SELECT 1`, nil, sqlnode.Limits{Timeout: time.Second}); err == nil {
		t.Error("a cancelled context still ran a query")
	}
}

func TestClosedConnectionRefusesFurtherWork(t *testing.T) {
	t.Parallel()

	connection, err := sqlnode.Open(context.Background(), sqlnode.DriverSQLite,
		map[string]string{"path": filepath.Join(t.TempDir(), "closed.db")}, sqlnode.Guard{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	// Close is idempotent so a deferred close after an early return is safe.
	if err := connection.Close(); err != nil {
		t.Errorf("second Close() error = %v, want nil", err)
	}
	if _, err := connection.Query(context.Background(), `SELECT 1`, nil, sqlnode.DefaultLimits()); err == nil {
		t.Error("a closed connection still ran a query")
	}
}

func TestUnsupportedDriverIsRejected(t *testing.T) {
	t.Parallel()

	if _, err := sqlnode.Open(context.Background(), sqlnode.Driver("oracle"), map[string]string{}, sqlnode.Guard{}); err == nil {
		t.Error("an unsupported driver was accepted")
	}
}

func TestPostgresAndMySQLFailToConnectWithoutLeakingTheirPassword(t *testing.T) {
	t.Parallel()

	// Port 1 is reserved and refuses connections, so this exercises the failure
	// path without needing a server.
	for _, driver := range []sqlnode.Driver{sqlnode.DriverPostgres, sqlnode.DriverMySQL} {
		_, err := sqlnode.Open(context.Background(), driver, map[string]string{
			"host": "127.0.0.1", "port": "1", "database": "app",
			"user": "ada", "password": "hunter2", "sslMode": "disable",
		}, sqlnode.Guard{})
		if err == nil {
			t.Fatalf("%s connected to a closed port", driver)
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s connection error leaked the password: %v", driver, err)
		}
	}
}

func TestACeilingClampsWhatADocumentAsksForAndNamesIt(t *testing.T) {
	t.Parallel()

	ceiling := sqlnode.Ceiling{MaxRows: 100, MaxTimeout: 10 * time.Second}
	for name, testCase := range map[string]struct {
		limits  sqlnode.Limits
		want    sqlnode.Limits
		clamped map[string]any
	}{
		"under the ceiling is left alone": {
			limits: sqlnode.Limits{MaxRows: 10, Timeout: time.Second},
			want:   sqlnode.Limits{MaxRows: 10, Timeout: time.Second},
		},
		"an expression-sized row count is cut to the ceiling": {
			limits:  sqlnode.Limits{MaxRows: 500_000_000, Timeout: time.Second},
			want:    sqlnode.Limits{MaxRows: 100, Timeout: time.Second},
			clamped: map[string]any{"maxRows": float64(100)},
		},
		"a long timeout is cut too": {
			limits:  sqlnode.Limits{MaxRows: 10, Timeout: time.Hour},
			want:    sqlnode.Limits{MaxRows: 10, Timeout: 10 * time.Second},
			clamped: map[string]any{"timeoutSeconds": float64(10)},
		},
		"both at once are both reported": {
			limits:  sqlnode.Limits{MaxRows: 1 << 30, Timeout: time.Hour},
			want:    sqlnode.Limits{MaxRows: 100, Timeout: 10 * time.Second},
			clamped: map[string]any{"maxRows": float64(100), "timeoutSeconds": float64(10)},
		},
		// Zero is "the node configured nothing", which the statement paths
		// already turn into their own defaults. Clamping it would turn an
		// unset limit into a ceiling-sized one.
		"an unset limit is not raised to the ceiling": {
			limits: sqlnode.Limits{},
			want:   sqlnode.Limits{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, clamped := ceiling.Apply(testCase.limits)
			if got != testCase.want {
				t.Errorf("limits = %#v, want %#v", got, testCase.want)
			}
			if !reflect.DeepEqual(clamped, testCase.clamped) {
				t.Errorf("clamped = %#v, want %#v", clamped, testCase.clamped)
			}
		})
	}

	t.Run("a zero ceiling falls back rather than meaning unbounded", func(t *testing.T) {
		// An unbounded row buffer is the defect the ceiling exists to stop, so
		// there is deliberately no spelling for it.
		got, clamped := sqlnode.Ceiling{}.Apply(sqlnode.Limits{MaxRows: 500_000_000, Timeout: 24 * time.Hour})
		if got.MaxRows != sqlnode.DefaultCeiling().MaxRows || got.Timeout != sqlnode.DefaultCeiling().MaxTimeout {
			t.Errorf("limits = %#v, want the default ceiling", got)
		}
		if len(clamped) != 2 {
			t.Errorf("clamped = %#v, want both limits reported", clamped)
		}
	})
}

func TestSanitizeRemovesEveryDSNFormADriverEchoes(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct{ in, want string }{
		"postgres url": {
			in:   `failed to connect to postgres://ada:hunter2@db.internal:5432/app: refused`,
			want: `failed to connect to postgres://[redacted]@db.internal:5432/app: refused`,
		},
		"postgresql url": {
			in:   `dial postgresql://ada:hunter2@db.internal:5432/app`,
			want: `dial postgresql://[redacted]@db.internal:5432/app`,
		},
		// MySQL's DSN carries no scheme at all, which is the form a second
		// copy of this function in another package would have missed.
		"schemeless mysql": {
			in:   `dial ada:hunter2@tcp(db.internal:3306)/app: refused`,
			want: `dial [redacted]@tcp(db.internal:3306)/app: refused`,
		},
		"nothing to redact": {
			in:   `relation "customers" does not exist`,
			want: `relation "customers" does not exist`,
		},
		// A bare "postgres://" in prose is not a DSN, and eating the rest of
		// the sentence would destroy the diagnosis it was part of.
		"a scheme with no credentials": {
			in:   `set sslmode on postgres:// urls and retry`,
			want: `set sslmode on postgres:// urls and retry`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := sqlnode.Sanitize(errors.New(testCase.in))
			if got.Error() != testCase.want {
				t.Errorf("Sanitize(%q) = %q, want %q", testCase.in, got, testCase.want)
			}
			if strings.Contains(got.Error(), "hunter2") {
				t.Errorf("Sanitize left the password in %q", got)
			}
		})
	}

	if sqlnode.Sanitize(nil) != nil {
		t.Error("Sanitize(nil) invented an error")
	}
}
