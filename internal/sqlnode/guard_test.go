package sqlnode_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
)

// The guard must refuse the internal database however the operator spelled its
// DSN.
//
// The `file:` spelling is the one that failed open. internal/database accepts
// it, so an install can legitimately be configured that way; the guard then
// held the literal string `file:./data/kilasflow.db`, filepath.Abs turned that
// into a path with a `file:` directory in it, os.Stat failed, and sameFile
// returned false — which is indistinguishable from "this credential is fine".
// A workflow could then read every credential in the installation with a plain
// SELECT, no ATTACH required.
func TestTheGuardRefusesTheInternalDatabaseHoweverItsDSNIsSpelled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	internal := filepath.Join(dir, "data", "kilasflow.db")
	if err := os.MkdirAll(filepath.Dir(internal), 0o750); err != nil {
		t.Fatalf("create the data directory: %v", err)
	}
	if err := os.WriteFile(internal, []byte("db"), 0o600); err != nil {
		t.Fatalf("write the internal database: %v", err)
	}

	for _, spelling := range []string{
		internal,
		"file:" + internal,
		"file:" + internal + "?_pragma=journal_mode(WAL)",
		internal + "?_pragma=busy_timeout(5000)",
	} {
		t.Run(spelling, func(t *testing.T) {
			resolved, err := database.SQLitePath(spelling)
			if err != nil {
				t.Fatalf("the resolver could not read a DSN this server accepts: %v", err)
			}
			guard := sqlnode.Guard{InternalPaths: []string{resolved}}

			// The credential names the same file plainly, which is how an
			// attacker would name it.
			_, err = sqlnode.OpenForTest(sqlnode.DriverSQLite, map[string]string{"path": internal}, guard)
			if err == nil {
				t.Fatalf("a credential naming the internal database was accepted under DSN %q", spelling)
			}
			if !strings.Contains(err.Error(), "KilasFlow's own database") {
				t.Errorf("error = %v, want it to say the path is KilasFlow's own database", err)
			}
		})
	}
}

// A DSN the resolver cannot read is an error, not an empty guard.
//
// This is the failure mode that made the defect invisible: a guard holding a
// path that resolves to nothing still answers "allowed" for every credential,
// and nothing anywhere reports it. The caller has to be able to tell.
func TestADSNThatCannotBeResolvedIsAnError(t *testing.T) {
	t.Parallel()

	if _, err := database.SQLitePath(""); err == nil {
		t.Error("an empty DSN resolved without complaint")
	}
	if _, err := database.SQLitePath("file:?_pragma=x"); err == nil {
		t.Error("a DSN naming no file resolved without complaint")
	}
	// An in-memory database has no file to guard, which is a real answer
	// rather than a failure.
	path, err := database.SQLitePath(":memory:")
	if err != nil {
		t.Errorf("an in-memory DSN was treated as unreadable: %v", err)
	}
	if path != "" {
		t.Errorf("an in-memory DSN resolved to %q, want no path at all", path)
	}
}

// A credential field must not be able to rewrite the driver's own DSN grammar.
//
// go-sql-driver splits its DSN at the first `?` after the last `/`, so a
// database named `app?multiStatements=true&` turned that option on — verified
// against the pinned driver. Multi-statement is precisely what the statement
// guard exists to prevent, so this field could have disabled the control that
// protects every other field.
func TestAMySQLCredentialCannotRewriteTheDriversDSN(t *testing.T) {
	t.Parallel()

	base := map[string]string{
		"host": "127.0.0.1", "port": "3306",
		"user": "kilas", "password": "hunter2", "database": "app",
	}
	for name, override := range map[string]map[string]string{
		"multi-statement through the database name": {"database": "app?multiStatements=true&"},
		"sql_mode through the database name":        {"database": "app?sql_mode=%27%27%23"},
		"a slash in the database name":              {"database": "app/other"},
		"an at sign in the user name":               {"user": "kilas@elsewhere"},
		"a closing parenthesis in the host":         {"host": "127.0.0.1)/x?multiStatements=true&y=("},
	} {
		t.Run(name, func(t *testing.T) {
			fields := map[string]string{}
			for key, value := range base {
				fields[key] = value
			}
			for key, value := range override {
				fields[key] = value
			}
			// No server is contacted: the refusal happens while the DSN is
			// being built, before any dial.
			_, err := sqlnode.OpenForTest(sqlnode.DriverMySQL, fields, sqlnode.Guard{})
			if err == nil {
				t.Fatalf("a credential carrying %v was accepted", override)
			}
			if strings.Contains(err.Error(), "connect to mysql") {
				t.Fatalf("the credential reached the dialler instead of being refused: %v", err)
			}
		})
	}
}
