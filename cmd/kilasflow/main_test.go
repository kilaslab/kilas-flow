package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/database"
)

// The startup path is Open followed by Migrate and nothing else. The schema
// used to come from the models main handed the database package, so this test
// guards the replacement: a default SQLite install has to reach a usable schema
// from the migration files alone.
func TestADefaultInstallBootsToAMigratedSchema(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "kilasflow.db"),
	}, log)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := database.Migrate(db, log); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	for _, table := range []string{"workflows", "executions", "credentials", "schedules"} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("a default install did not create the %q table", table)
		}
	}
}
