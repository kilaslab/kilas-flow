package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SQLite credentials are confined by default, to a root beside the default
// database, and both the root and the escape hatch are reachable from the
// environment.
func TestSQLiteCredentialsAreConfinedByDefaultAndConfigurable(t *testing.T) {
	cfg := Default()
	if strings.TrimSpace(cfg.SQL.SQLiteRoot) == "" {
		t.Fatal("sql.sqlite_root defaults to empty, so a fresh install has SQLite credentials disabled")
	}
	if filepath.Dir(cfg.SQL.SQLiteRoot) != filepath.Dir(cfg.Database.DSN) {
		t.Errorf("sql.sqlite_root %q is not beside the database %q, so the two do not share a volume",
			cfg.SQL.SQLiteRoot, cfg.Database.DSN)
	}
	if cfg.SQL.SQLiteUnconfined {
		t.Error("sql.sqlite_unconfined defaults to true, so every tenant can open every file")
	}

	t.Setenv("KILASFLOW_SQL_SQLITE_ROOT", "/srv/kilasflow/sqlite")
	t.Setenv("KILASFLOW_SQL_SQLITE_UNCONFINED", "true")
	loaded, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.SQL.SQLiteRoot != "/srv/kilasflow/sqlite" || !loaded.SQL.SQLiteUnconfined {
		t.Errorf("SQL = %+v, want the environment's root and unconfined", loaded.SQL)
	}
}

// An explicit empty root disables the credential type, the way an empty
// binary.root disables binary storage.
func TestAnEmptySQLiteRootDisablesSQLiteCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "off.yaml")
	if err := os.WriteFile(path, []byte("sql:\n  sqlite_root: \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if strings.TrimSpace(loaded.SQL.SQLiteRoot) != "" {
		t.Errorf("sql.sqlite_root = %q, want the explicit empty string to disable SQLite credentials", loaded.SQL.SQLiteRoot)
	}
}
