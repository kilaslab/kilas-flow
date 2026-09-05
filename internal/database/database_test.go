package database

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/config"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestOpenSQLiteAppliesPragmas(t *testing.T) {
	cfg := config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "nested", "kilasflow.db"),
	}

	db, err := Open(context.Background(), cfg, discardLogger())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var journalMode string
	if err := db.Raw("PRAGMA journal_mode").Scan(&journalMode).Error; err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}

	var foreignKeys int
	if err := db.Raw("PRAGMA foreign_keys").Scan(&foreignKeys).Error; err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d, want 1", foreignKeys)
	}
}

// Open must create the parent directory, so a first run with the default
// ./data/kilasflow.db works without the user creating anything.
func TestOpenSQLiteCreatesParentDirectory(t *testing.T) {
	cfg := config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "a", "b", "c", "kilasflow.db"),
	}

	db, err := Open(context.Background(), cfg, discardLogger())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

func TestPing(t *testing.T) {
	cfg := config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "kilasflow.db")}

	db, err := Open(context.Background(), cfg, discardLogger())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := db.Ping(context.Background()); err != nil {
		t.Errorf("Ping on an open database: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := db.Ping(context.Background()); err == nil {
		t.Error("Ping after Close = nil, want error")
	}
}

func TestUnsupportedDriver(t *testing.T) {
	_, err := Open(context.Background(), config.Database{Driver: "mongodb", DSN: "x"}, discardLogger())
	if err == nil {
		t.Fatal("Open with unknown driver = nil, want error")
	}
}

// The pool the configuration describes is the pool the handle actually gets.
//
// SQLite is pinned to one whatever it was told, because the single-writer pin
// and the WAL pragma set are a pair and unpinning one without the other brings
// SQLITE_BUSY straight back.
func TestTheSQLiteHandleHoldsExactlyOneConnection(t *testing.T) {
	cfg := config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "kilasflow.db"),
		MaxOpenConns: 25, MaxIdleConns: 25,
	}

	db, err := Open(context.Background(), cfg, discardLogger())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sqlDB, err := db.DB.DB()
	if err != nil {
		t.Fatalf("access the underlying sql.DB: %v", err)
	}
	if got := sqlDB.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("MaxOpenConnections = %d, want the SQLite pin of 1", got)
	}
}

// A pool nobody sized is bounded rather than unlimited.
//
// database/sql reads SetMaxOpenConns(0) as "no limit", so passing an unset
// config.Database straight through — which every caller that builds one by hand
// does — used to leave a PostgreSQL handle willing to open as many backends as
// the server would accept.
func TestAnUnsizedPoolIsBoundedRatherThanUnlimited(t *testing.T) {
	open, idle := config.Database{Driver: "postgres"}.PoolSize(0)
	if open <= 0 {
		t.Errorf("PoolSize() open = %d, which database/sql reads as unlimited", open)
	}
	if idle <= 0 {
		t.Errorf("PoolSize() idle = %d, which keeps no connection at all", idle)
	}
}

// Migration coverage lives in migrate_test.go; the schema is no longer built
// from the models this package is handed.
