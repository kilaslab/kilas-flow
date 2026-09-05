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

// Migration coverage lives in migrate_test.go; the schema is no longer built
// from the models this package is handed.
