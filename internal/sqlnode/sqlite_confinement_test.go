package sqlnode_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/sqlnode"
)

// confinedGuard is the guard a multi-tenant install builds: a SQLite root,
// narrowed per call to the tenant the credential belongs to.
func confinedGuard(t *testing.T, tenant string) (string, sqlnode.Guard) {
	t.Helper()
	root := t.TempDir()
	return root, sqlnode.Guard{SQLite: sqlnode.SQLiteFiles{Root: root}}.ForTenant(tenant)
}

func openSQLitePath(guard sqlnode.Guard, path string) (*sqlnode.Connection, error) {
	return sqlnode.Open(context.Background(), sqlnode.DriverSQLite, map[string]string{"path": path}, guard)
}

// A relative path lands in the tenant's own directory under the root, which is
// created on first use so a fresh tenant can make its first database.
func TestAConfinedSQLitePathOpensInsideTheTenantsDirectory(t *testing.T) {
	t.Parallel()

	root, guard := confinedGuard(t, "acme")
	connection, err := openSQLitePath(guard, "orders.db")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := connection.Execute(context.Background(), `CREATE TABLE orders (id INTEGER)`, nil, sqlnode.DefaultLimits()); err != nil {
		t.Fatalf("create table: %v", err)
	}
	_ = connection.Close()

	info, err := os.Stat(filepath.Join(root, "acme", "orders.db"))
	if err != nil {
		t.Fatalf("the database was not created in the tenant's directory: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("orders.db is %v, want a regular file", info.Mode())
	}
	directory, err := os.Stat(filepath.Join(root, "acme"))
	if err != nil {
		t.Fatalf("stat the tenant directory: %v", err)
	}
	if perm := directory.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("tenant directory mode = %v, want no group or other access", perm)
	}

	// A subdirectory the operator made inside the tenant's directory is usable.
	if err := os.Mkdir(filepath.Join(root, "acme", "reports"), 0o700); err != nil {
		t.Fatalf("create a subdirectory: %v", err)
	}
	nested, err := openSQLitePath(guard, "reports/q1.db")
	if err != nil {
		t.Fatalf("Open(reports/q1.db) error = %v", err)
	}
	_ = nested.Close()
}

func TestAConfinedSQLitePathRefusesAnAbsolutePath(t *testing.T) {
	t.Parallel()

	root, guard := confinedGuard(t, "acme")
	outside := filepath.Join(t.TempDir(), "created-by-tenant.db")
	for name, path := range map[string]string{
		"a system file":                 "/etc/hosts",
		"a new file anywhere":           outside,
		"its own directory, absolutely": filepath.Join(root, "acme", "orders.db"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := openSQLitePath(guard, path)
			assertForbidden(t, err, "absolute")
		})
	}
	if _, err := os.Stat(outside); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused absolute path still created %s (stat err = %v)", outside, err)
	}
}

func TestAConfinedSQLitePathRefusesADotDotEscape(t *testing.T) {
	t.Parallel()

	root, guard := confinedGuard(t, "acme")
	if err := os.MkdirAll(filepath.Join(root, "globex"), 0o700); err != nil {
		t.Fatalf("create the neighbour's directory: %v", err)
	}
	for _, path := range []string{
		"..",
		"../globex/orders.db",
		"../../../../etc/hosts",
		"reports/../../globex/orders.db",
	} {
		t.Run(path, func(t *testing.T) {
			_, err := openSQLitePath(guard, path)
			assertForbidden(t, err, "inside this tenant's SQLite directory")
		})
	}
	if _, err := os.Stat(filepath.Join(root, "globex", "orders.db")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an escaping path created a file in the neighbour's directory (stat err = %v)", err)
	}
}

// A symlink inside the tenant's directory would carry an open anywhere the
// link points. No tenant can make one through a workflow, so any symlink found
// on the way is something the tenant must not be handed, and it is refused
// whether it points outside or not.
func TestAConfinedSQLitePathRefusesASymlinkEscape(t *testing.T) {
	t.Parallel()

	root, guard := confinedGuard(t, "acme")
	tenantDir := filepath.Join(root, "acme")
	neighbour := filepath.Join(root, "globex")
	for _, directory := range []string{tenantDir, neighbour} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create %s: %v", directory, err)
		}
	}
	secret := filepath.Join(neighbour, "secret.db")
	if err := os.WriteFile(secret, []byte("neighbour"), 0o600); err != nil {
		t.Fatalf("write the neighbour's database: %v", err)
	}
	own := filepath.Join(tenantDir, "own.db")
	if err := os.WriteFile(own, nil, 0o600); err != nil {
		t.Fatalf("write the tenant's own database: %v", err)
	}
	links := map[string]string{
		"to-neighbour.db": secret,
		"to-etc":          "/etc",
		"to-own.db":       own,
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(tenantDir, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}

	for _, path := range []string{"to-neighbour.db", "to-etc/hosts", "to-own.db"} {
		t.Run(path, func(t *testing.T) {
			_, err := openSQLitePath(guard, path)
			assertForbidden(t, err, "symbolic link")
		})
	}
}

// Only a regular file is a database. A directory cannot be one, and a device or
// a FIFO can block the open itself (see sqlite_fifo_test.go for the FIFO).
func TestAConfinedSQLitePathRefusesADirectory(t *testing.T) {
	t.Parallel()

	root, guard := confinedGuard(t, "acme")
	if err := os.MkdirAll(filepath.Join(root, "acme", "reports"), 0o700); err != nil {
		t.Fatalf("create a subdirectory: %v", err)
	}
	for _, path := range []string{"reports", ".", "reports/"} {
		t.Run(path, func(t *testing.T) {
			_, err := openSQLitePath(guard, path)
			assertForbidden(t, err, "regular file")
		})
	}
}

// A directory in the path that does not exist is not created: a typo in a
// credential should fail the test, not grow a tree under the root.
func TestAConfinedSQLitePathDoesNotCreateMissingDirectories(t *testing.T) {
	t.Parallel()

	root, guard := confinedGuard(t, "acme")
	if _, err := openSQLitePath(guard, "no/such/directory/workflow.db"); err == nil {
		t.Fatal("a path through a missing directory opened")
	}
	if _, err := os.Stat(filepath.Join(root, "acme", "no")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the missing directory was created (stat err = %v)", err)
	}
}

// Two tenants naming the same relative path reach two different files.
func TestTenantsNamingTheSameSQLitePathReachDifferentFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	base := sqlnode.Guard{SQLite: sqlnode.SQLiteFiles{Root: root}}

	acme, err := openSQLitePath(base.ForTenant("acme"), "shared.db")
	if err != nil {
		t.Fatalf("Open(acme) error = %v", err)
	}
	defer acme.Close()
	if _, err := acme.Execute(context.Background(), `CREATE TABLE secrets (value TEXT)`, nil, sqlnode.DefaultLimits()); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := acme.Execute(context.Background(), `INSERT INTO secrets (value) VALUES ('acme only')`, nil, sqlnode.DefaultLimits()); err != nil {
		t.Fatalf("insert: %v", err)
	}

	globex, err := openSQLitePath(base.ForTenant("globex"), "shared.db")
	if err != nil {
		t.Fatalf("Open(globex) error = %v", err)
	}
	defer globex.Close()
	result, err := globex.Query(context.Background(),
		`SELECT count(*) AS n FROM sqlite_master WHERE name = 'secrets'`, nil, sqlnode.DefaultLimits())
	if err != nil {
		t.Fatalf("query globex: %v", err)
	}
	if len(result.Rows) != 1 || result.Rows[0]["n"] != int64(0) {
		t.Fatalf("globex sees %v, want no secrets table: the tenants share a file", result.Rows)
	}
	for _, tenant := range []string{"acme", "globex"} {
		if _, err := os.Stat(filepath.Join(root, tenant, "shared.db")); err != nil {
			t.Errorf("%s's file is missing: %v", tenant, err)
		}
	}
}

// Without a tenant there is no directory to confine to, and a tenant ID that is
// not one plain path segment would pick a directory other than its own.
func TestAConfinedSQLitePathNeedsAPlainTenant(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	base := sqlnode.Guard{SQLite: sqlnode.SQLiteFiles{Root: root}}
	for name, tenant := range map[string]string{
		"no tenant":        "",
		"a parent segment": "..",
		"a nested path":    "acme/../globex",
		"a separator":      "acme/x",
		"the root itself":  ".",
		"an upper case ID": "Acme",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := openSQLitePath(base.ForTenant(tenant), "orders.db")
			assertForbidden(t, err, "tenant")
		})
	}
}

// The zero guard refuses every SQLite credential: a guard built by hand gets
// the strict answer, and an operator who empties sql.sqlite_root turns the
// type off.
func TestSQLiteIsRefusedWhenNoRootIsConfigured(t *testing.T) {
	t.Parallel()

	_, err := openSQLitePath(sqlnode.Guard{}.ForTenant("acme"), "orders.db")
	assertForbidden(t, err, "sql.sqlite_root")
	if (sqlnode.SQLiteFiles{}).Enabled() {
		t.Error("an empty SQLiteFiles reports itself enabled")
	}
}

// The single-tenant escape hatch reads the path as the old guard did, but
// KilasFlow's own database and non-regular files stay refused.
func TestUnconfinedSQLiteKeepsTheOldPathsButNotTheInternalDatabase(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	internal := filepath.Join(directory, "kilasflow.db")
	if err := os.WriteFile(internal, []byte("internal"), 0o600); err != nil {
		t.Fatalf("write the internal database: %v", err)
	}
	guard := sqlnode.Guard{InternalPaths: []string{internal}, SQLite: sqlnode.SQLiteFiles{Unconfined: true}}

	connection, err := openSQLitePath(guard, filepath.Join(directory, "customer.db"))
	if err != nil {
		t.Fatalf("an absolute path under the escape hatch was refused: %v", err)
	}
	_ = connection.Close()

	_, err = openSQLitePath(guard, internal)
	assertForbidden(t, err, "KilasFlow's own database")

	_, err = openSQLitePath(guard, directory)
	assertForbidden(t, err, "regular file")
}

// The root's own spelling does not matter: a relative root and one reached
// through a symlink (macOS's /var → /private/var) confine the same way.
func TestTheSQLiteRootMayBeReachedThroughASymlink(t *testing.T) {
	t.Parallel()

	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	guard := sqlnode.Guard{SQLite: sqlnode.SQLiteFiles{Root: link}}.ForTenant("acme")
	connection, err := openSQLitePath(guard, "orders.db")
	if err != nil {
		t.Fatalf("a root reached through a symlink refused a plain path: %v", err)
	}
	_ = connection.Close()
	if _, err := os.Stat(filepath.Join(real, "acme", "orders.db")); err != nil {
		t.Fatalf("the file is not under the real root: %v", err)
	}
}
