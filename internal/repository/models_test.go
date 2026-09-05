package repository_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

type activationCatalog map[string]workflow.NodeDefinition

func (c activationCatalog) Lookup(nodeType string, version int) (workflow.NodeDefinition, bool) {
	definition, found := c[nodeType]
	return definition, found && definition.Version == version
}

func (c activationCatalog) HasType(nodeType string) bool {
	_, found := c[nodeType]
	return found
}

func TestMigrateCreatesWorkflowAndExecutionTables(t *testing.T) {
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "kilasflow.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := database.Migrate(db, repository.Models()...); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	for _, table := range []string{
		"workflows",
		"workflow_versions",
		"executions",
		"execution_node_runs",
	} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("migration did not create %q", table)
		}
	}
	assertSQLiteIndexColumns(t, db, "idx_executions_tenant_workflow", []string{"tenant_id", "workflow_id"})
	assertSQLiteIndexColumns(t, db, "uidx_node_runs_sequence", []string{"execution_id", "sequence"})
	assertSQLiteIndexColumns(t, db, "uidx_workflow_versions_revision", []string{"tenant_id", "workflow_id", "revision"})
	assertSQLiteForeignKey(t, db, "workflow_versions", "workflow_id", "workflows")
	assertSQLiteForeignKey(t, db, "executions", "workflow_id", "workflows")
	assertSQLiteForeignKey(t, db, "executions", "workflow_version_id", "workflow_versions")
	assertSQLiteForeignKey(t, db, "execution_node_runs", "execution_id", "executions")
}

func assertSQLiteIndexColumns(t *testing.T, db *database.DB, index string, want []string) {
	t.Helper()
	var columns []struct {
		Sequence int    `gorm:"column:seqno"`
		Name     string `gorm:"column:name"`
	}
	if err := db.Raw("PRAGMA index_info(" + index + ")").Scan(&columns).Error; err != nil {
		t.Fatalf("read index %q: %v", index, err)
	}
	if len(columns) != len(want) {
		t.Fatalf("index %q columns = %#v, want %v", index, columns, want)
	}
	for position, column := range columns {
		if column.Name != want[position] {
			t.Errorf("index %q column %d = %q, want %q", index, position, column.Name, want[position])
		}
	}
}

func assertSQLiteForeignKey(t *testing.T, db *database.DB, table, from, targetTable string) {
	t.Helper()
	var foreignKeys []struct {
		Table string `gorm:"column:table"`
		From  string `gorm:"column:from"`
	}
	if err := db.Raw("PRAGMA foreign_key_list(" + table + ")").Scan(&foreignKeys).Error; err != nil {
		t.Fatalf("read foreign keys for %q: %v", table, err)
	}
	for _, foreignKey := range foreignKeys {
		if foreignKey.Table == targetTable && foreignKey.From == from {
			return
		}
	}
	t.Fatalf("foreign keys for %q = %#v, want %s -> %s", table, foreignKeys, from, targetTable)
}

func TestExecutionStorePinsWorkflowVersionAndPersistsNodeRuns(t *testing.T) {
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "kilasflow.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, repository.Models()...); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	tenant := repository.TenantScope{ID: "tenant-a"}
	workflowStore := repository.NewWorkflowStore(db.DB)
	stored, err := workflowStore.SaveDraft(context.Background(), tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Execution source",
		Nodes:         []workflow.Node{},
		Connections:   []workflow.Connection{},
		Settings:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}

	store := repository.NewExecutionStore(db.DB)
	created, err := store.Create(context.Background(), tenant, execution.Record{
		WorkflowID:        stored.ID,
		WorkflowVersionID: stored.LatestVersion.ID,
		Status:            execution.StatusQueued,
		Trigger:           execution.TriggerManual,
		Input:             json.RawMessage(`{"customer":"Ada"}`),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got, want := created.WorkflowVersionID, stored.LatestVersion.ID; got != want {
		t.Fatalf("execution version = %q, want %q", got, want)
	}

	claimed, _, found, err := store.ClaimNext(context.Background(), "test-worker", time.Now().Add(time.Second))
	if err != nil || !found {
		t.Fatalf("ClaimNext() = (%#v, %v, %v), want claimed", claimed, found, err)
	}
	_, err = store.CreateNodeRun(context.Background(), tenant, execution.NodeRun{
		ExecutionID: created.ID,
		NodeID:      "manual",
		Attempt:     1,
		Sequence:    1,
		Status:      execution.StatusSucceeded,
		Output:      json.RawMessage(`[[{"json":{"customer":"Ada"}}]]`),
		LeaseOwner:  claimed.LeaseOwner,
	})
	if err != nil {
		t.Fatalf("CreateNodeRun() error = %v", err)
	}

	loaded, err := store.Get(context.Background(), tenant, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got, want := len(loaded.NodeRuns), 1; got != want {
		t.Fatalf("node runs = %d, want %d", got, want)
	}
	if got, want := loaded.NodeRuns[0].NodeID, "manual"; got != want {
		t.Errorf("node run node ID = %q, want %q", got, want)
	}
}

func TestExecutionStoreReclaimsAnExpiredWorkerLease(t *testing.T) {
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "kilasflow.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, repository.Models()...); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	tenant := repository.TenantScope{ID: "tenant-a"}
	workflows := repository.NewWorkflowStore(db.DB)
	stored, err := workflows.SaveDraft(context.Background(), tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_027",
		Name:          "Lease recovery",
		Nodes: []workflow.Node{{
			ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: 1,
		}},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	store := repository.NewExecutionStore(db.DB)
	queued, err := store.QueueManualLatest(context.Background(), tenant, stored.ID, activationCatalog{
		"kilasflow.manual": {Type: "kilasflow.manual", Version: 1, Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}},
	}, nil)
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	first, _, claimed, err := store.ClaimNext(context.Background(), "stopped-worker", time.Now().Add(-time.Second))
	if err != nil || !claimed {
		t.Fatalf("first ClaimNext() = (%#v, %v, %v), want claimed", first, claimed, err)
	}
	second, _, claimed, err := store.ClaimNext(context.Background(), "recovery-worker", time.Now().Add(time.Second))
	if err != nil || !claimed {
		t.Fatalf("second ClaimNext() = (%#v, %v, %v), want expired lease reclaimed", second, claimed, err)
	}
	if got, want := second.ID, queued.ID; got != want {
		t.Errorf("reclaimed execution = %q, want %q", got, want)
	}
	if first.LeaseOwner == second.LeaseOwner {
		t.Fatalf("reclaimed lease owner = %q, want a distinct fencing token", second.LeaseOwner)
	}
	now := time.Now().UTC()
	_, err = store.CreateNodeRun(context.Background(), tenant, execution.NodeRun{
		ExecutionID: queued.ID,
		NodeID:      "manual",
		Attempt:     1,
		Sequence:    1,
		Status:      execution.StatusSucceeded,
		StartedAt:   now,
		FinishedAt:  &now,
		LeaseOwner:  first.LeaseOwner,
	})
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("CreateNodeRun(stale lease) error = %v, want ErrNotFound", err)
	}
	first.Status = execution.StatusSucceeded
	first.FinishedAt = &now
	if _, err := store.UpdateRuntime(context.Background(), tenant, first); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("UpdateRuntime(stale lease) error = %v, want ErrNotFound", err)
	}
	persisted, err := store.Get(context.Background(), tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get(reclaimed) error = %v", err)
	}
	if got, want := persisted.Status, execution.StatusRunning; got != want {
		t.Errorf("reclaimed execution status = %q, want %q", got, want)
	}
}

func TestWorkflowStoreCreatesImmutableTenantScopedVersions(t *testing.T) {
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "kilasflow.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, repository.Models()...); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	store := repository.NewWorkflowStore(db.DB)
	tenantA := repository.TenantScope{ID: "tenant-a"}
	draft := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Original name",
		Nodes: []workflow.Node{{
			ID:          "manual",
			Name:        "Manual Trigger",
			Type:        "kilasflow.manual",
			TypeVersion: 1,
			Position:    workflow.Position{},
		}},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	}

	first, err := store.SaveDraft(context.Background(), tenantA, draft)
	if err != nil {
		t.Fatalf("SaveDraft(first) error = %v", err)
	}
	if got, want := first.LatestVersion.Revision, 1; got != want {
		t.Fatalf("first revision = %d, want %d", got, want)
	}

	draft.Name = "Updated name"
	second, err := store.SaveDraft(context.Background(), tenantA, draft)
	if err != nil {
		t.Fatalf("SaveDraft(second) error = %v", err)
	}
	if got, want := second.LatestVersion.Revision, 2; got != want {
		t.Fatalf("second revision = %d, want %d", got, want)
	}

	original, err := store.GetVersion(context.Background(), tenantA, draft.ID, 1)
	if err != nil {
		t.Fatalf("GetVersion(first) error = %v", err)
	}
	if got, want := original.Document.Name, "Original name"; got != want {
		t.Errorf("immutable first document name = %q, want %q", got, want)
	}

	_, err = store.Get(context.Background(), repository.TenantScope{ID: "tenant-b"}, draft.ID)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("Get(other tenant) error = %v, want ErrNotFound", err)
	}

	activated, err := store.Activate(context.Background(), tenantA, draft.ID, activationCatalog{
		"kilasflow.manual": {
			Type: "kilasflow.manual", Version: 1,
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
	})
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if !activated.Active {
		t.Fatal("activated workflow is not active")
	}
	if activated.ActiveVersion == nil || activated.ActiveVersion.ID != second.LatestVersion.ID {
		t.Fatalf("active version = %#v, want %q", activated.ActiveVersion, second.LatestVersion.ID)
	}

	draft.Name = "Unpublished third draft"
	third, err := store.SaveDraft(context.Background(), tenantA, draft)
	if err != nil {
		t.Fatalf("SaveDraft(third) error = %v", err)
	}
	if got, want := third.LatestVersion.Revision, 3; got != want {
		t.Errorf("third revision = %d, want %d", got, want)
	}
	if third.ActiveVersion == nil || third.ActiveVersion.ID != second.LatestVersion.ID {
		t.Errorf("new draft moved active version to %#v, want %q", third.ActiveVersion, second.LatestVersion.ID)
	}
}

func TestWorkflowStoreActivatesOnlyLatestExecutableRevision(t *testing.T) {
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "kilasflow.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, repository.Models()...); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	tenant := repository.TenantScope{ID: "tenant-a"}
	store := repository.NewWorkflowStore(db.DB)
	draft := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Activation source",
		Nodes: []workflow.Node{{
			ID:          "manual",
			Name:        "Manual Trigger",
			Type:        "kilasflow.manual",
			TypeVersion: 1,
			Position:    workflow.Position{},
		}},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	}
	if _, err := store.SaveDraft(context.Background(), tenant, draft); err != nil {
		t.Fatalf("SaveDraft(valid) error = %v", err)
	}

	draft.Nodes[0].Type = "kilasflow.unknown"
	if _, err := store.SaveDraft(context.Background(), tenant, draft); err != nil {
		t.Fatalf("SaveDraft(invalid latest) error = %v", err)
	}

	_, err = store.Activate(context.Background(), tenant, draft.ID, activationCatalog{
		"kilasflow.manual": {
			Type: "kilasflow.manual", Version: 1,
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
	})
	var validationErrors *workflow.ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("Activate() error = %v, want ValidationErrors", err)
	}

	stored, err := store.Get(context.Background(), tenant, draft.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.Active || stored.ActiveVersion != nil {
		t.Fatalf("invalid latest revision activated workflow = %#v", stored)
	}
}
