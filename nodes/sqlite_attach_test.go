package nodes_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// The exploits that worked before the guard existed, asserted to fail now.
//
// Each of these was executed against the pinned driver and succeeded: the
// ATTACH read a seeded credentials row out of a second database file on the
// same connection, the VACUUM INTO wrote a complete copy of the connected
// database to a path the statement chose, and the CREATE through an attached
// alias wrote into a database the node was never given. Binding parameters did
// not stop any of them, and neither did going through a prepared handle.
//
// The seeded row is the point: the test builds the thing an attacker would be
// reading, so a regression that reopens the hole fails here by returning it
// rather than by some proxy for danger.
func TestTheStatementsThatOnceReadAnotherDatabaseAreRefused(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	victim := filepath.Join(dir, "internal.db")
	seedCredential(t, victim)

	own := filepath.Join(dir, "workflow.db")
	resolver := sqliteCredential(own)
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite", unconfinedSQLite(), sqlnode.DefaultCeiling())
	createTable(t, executor, resolver, `CREATE TABLE t (name TEXT)`)

	for name, row := range map[string]struct {
		operation string
		key       string
		statement string
	}{
		"attach then read, on the query operation": {
			operation: "query", key: "statement",
			statement: `ATTACH DATABASE '` + victim + `' AS k; SELECT payload FROM k.credentials`,
		},
		"attach hidden behind a comment": {
			operation: "query", key: "statement",
			statement: `/*x*/ATTACH DATABASE '` + victim + `' AS k; SELECT payload FROM k.credentials`,
		},
		"a benign statement first": {
			operation: "query", key: "statement",
			statement: `SELECT 1; ATTACH DATABASE '` + victim + `' AS k`,
		},
		"attach then write, on the execute operation": {
			operation: "execute", key: "executeStatement",
			statement: `ATTACH DATABASE '` + victim + `' AS k; CREATE TABLE k.pwned (x)`,
		},
		"vacuum into, which needs no semicolon at all": {
			operation: "execute", key: "executeStatement",
			statement: `VACUUM INTO '` + filepath.Join(dir, "exfil.db") + `'`,
		},
		"pragma, which reconfigures the connection": {
			operation: "query", key: "statement",
			statement: `PRAGMA writable_schema = ON`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ir := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
				"operation": row.operation, row.key: row.statement,
			})
			output, err := executor.Execute(context.Background(), ir,
				workflow.NodeInput{"main": {{JSON: map[string]any{}}}},
				engine.Request{Credentials: resolver})
			if err == nil {
				t.Fatalf("the statement ran and returned %#v", output)
			}
			if !strings.Contains(err.Error(), "refused") {
				t.Errorf("error = %v, want the guard's refusal", err)
			}
			// The seeded secret must not appear anywhere in what came back.
			if strings.Contains(err.Error(), "SUPER-SECRET") {
				t.Errorf("the refusal itself leaked the row: %v", err)
			}
		})
	}

	// Nothing reached the victim database, and no copy of anything was left
	// on disk beside it.
	if _, err := os.Stat(filepath.Join(dir, "exfil.db")); err == nil {
		t.Error("VACUUM INTO wrote a copy of the database despite being refused")
	}
	assertNoTable(t, victim, "pwned")
}

// The same statement through the transaction path, which prepares.
//
// A prepared handle is not a single-statement guard: PrepareContext on
// two-statement text succeeds against this driver, executing it runs both, and
// the ATTACH then persists on the connection. So the transaction list needs the
// same check as a bare query, and this is the test that says so.
func TestATransactionElementCannotCarryASecondStatement(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	victim := filepath.Join(dir, "internal.db")
	seedCredential(t, victim)

	own := filepath.Join(dir, "workflow.db")
	resolver := sqliteCredential(own)
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite", unconfinedSQLite(), sqlnode.DefaultCeiling())
	createTable(t, executor, resolver, `CREATE TABLE t (name TEXT)`)

	ir := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation": "transaction",
		"statements": `[{"sql":"INSERT INTO t (name) VALUES ('a')"},
		                {"sql":"ATTACH DATABASE '` + victim + `' AS k; CREATE TABLE k.pwned (x)"}]`,
	})
	if _, err := executor.Execute(context.Background(), ir,
		workflow.NodeInput{"main": {{JSON: map[string]any{}}}},
		engine.Request{Credentials: resolver}); err == nil {
		t.Fatal("a transaction element carrying two statements ran")
	}
	assertNoTable(t, victim, "pwned")
}

// seedCredential builds the database an attacker would be trying to read.
func seedCredential(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open the seeded database: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE credentials (payload TEXT)`); err != nil {
		t.Fatalf("create the seeded table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO credentials (payload) VALUES ('SUPER-SECRET-ENCRYPTED-BLOB')`); err != nil {
		t.Fatalf("seed the credential row: %v", err)
	}
}

func assertNoTable(t *testing.T, path, table string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen the seeded database: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
		t.Fatalf("read the seeded schema: %v", err)
	}
	if count != 0 {
		t.Errorf("table %q was created inside a database the node was never given", table)
	}
}
