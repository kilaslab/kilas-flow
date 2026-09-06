package repository_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

func openPrefixedSQLite(t *testing.T, prefix string) *database.DB {
	t.Helper()
	db, err := database.Open(context.Background(), config.Database{
		Driver:      "sqlite",
		DSN:         filepath.Join(t.TempDir(), "kilasflow.db"),
		TablePrefix: prefix,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}

// Every query the repository layer issues resolves to the prefixed tables: a
// draft saved and read back through the store touches kflow_workflows and
// kflow_workflow_versions, never the bare names. A Tabler model or a hardcoded
// table reference would fail here with no such table.
func TestPrefixedStoreReadsAndWritesPrefixedTables(t *testing.T) {
	t.Parallel()

	db := openPrefixedSQLite(t, "kflow_")

	for _, table := range []string{"workflows", "workflow_versions", "executions"} {
		if db.Migrator().HasTable(table) {
			t.Errorf("unprefixed table %q exists after a prefixed migration", table)
		}
		if !db.Migrator().HasTable("kflow_" + table) {
			t.Errorf("prefixed table %q is missing after migration", "kflow_"+table)
		}
	}

	tenant := repository.TenantScope{ID: "tenant-prefix"}
	store := repository.NewWorkflowStore(db.DB)
	stored, err := store.SaveDraft(context.Background(), tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_prefix_01",
		Name:          "Prefixed workflow",
		Nodes:         []workflow.Node{},
		Connections:   []workflow.Connection{},
		Settings:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}

	got, err := store.Get(context.Background(), tenant, stored.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != "Prefixed workflow" {
		t.Errorf("Get() name = %q, want %q", got.Name, "Prefixed workflow")
	}

	listed, err := store.List(context.Background(), tenant)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 {
		t.Errorf("List() returned %d workflows, want 1", len(listed))
	}
}
