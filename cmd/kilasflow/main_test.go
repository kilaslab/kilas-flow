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

func TestMigrateRegistersDomainModels(t *testing.T) {
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "kilasflow.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := migrate(db); err != nil {
		t.Fatalf("migrate() error = %v", err)
	}
	if !db.Migrator().HasTable("workflows") {
		t.Fatal("migrate() did not register workflow models")
	}
}
