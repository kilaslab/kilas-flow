package main

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/config"
)

// The boot turns the sql section's SQLite keys into the guard's confinement:
// a relative root is pinned to an absolute one, an empty root disables the
// type, and the escape hatch is warned about every time the server starts.
func TestSQLiteFilesFollowTheSQLSection(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))

	confined, err := sqliteFiles(config.Default().SQL, log)
	if err != nil {
		t.Fatalf("sqliteFiles(default) error = %v", err)
	}
	if !filepath.IsAbs(confined.Root) || confined.Unconfined {
		t.Fatalf("default = %+v, want an absolute root and confinement on", confined)
	}
	if filepath.Base(confined.Root) != "sqlite" {
		t.Errorf("default root = %q, want the configured ./data/sqlite resolved", confined.Root)
	}

	disabled, err := sqliteFiles(config.SQLNodes{SQLiteRoot: "  "}, log)
	if err != nil {
		t.Fatalf("sqliteFiles(empty) error = %v", err)
	}
	if disabled.Enabled() {
		t.Errorf("an empty root = %+v, want SQLite credentials disabled", disabled)
	}

	logs.Reset()
	unconfined, err := sqliteFiles(config.SQLNodes{SQLiteRoot: "./data/sqlite", SQLiteUnconfined: true}, log)
	if err != nil {
		t.Fatalf("sqliteFiles(unconfined) error = %v", err)
	}
	if !unconfined.Unconfined {
		t.Errorf("unconfined = %+v, want the escape hatch on", unconfined)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "sql.sqlite_unconfined") {
		t.Errorf("the escape hatch booted without a warning naming its key: %q", logs.String())
	}
}
