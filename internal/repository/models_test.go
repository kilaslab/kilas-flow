package repository_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

type activationCatalog map[string]workflow.NodeDefinition

func (c activationCatalog) Lookup(nodeType string, version workflow.TypeVersion) (workflow.NodeDefinition, bool) {
	definition, found := c[nodeType]
	return definition, found && definition.Version.Compare(version) == 0
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

	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
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
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
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
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	tenant := repository.TenantScope{ID: "tenant-a"}
	workflows := repository.NewWorkflowStore(db.DB)
	stored, err := workflows.SaveDraft(context.Background(), tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_027",
		Name:          "Lease recovery",
		Nodes: []workflow.Node{{
			ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1),
		}},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	store := repository.NewExecutionStore(db.DB)
	queued, err := store.QueueManualLatest(context.Background(), tenant, stored.ID, activationCatalog{
		"kilasflow.manual": {Type: "kilasflow.manual", Version: workflow.V(1), Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}},
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
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
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
			TypeVersion: workflow.V(1),
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
			Type: "kilasflow.manual", Version: workflow.V(1),
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
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
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
			TypeVersion: workflow.V(1),
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
			Type: "kilasflow.manual", Version: workflow.V(1),
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

func TestExecutionStoreRedactsCredentialsBeforeStorage(t *testing.T) {
	db, tenant, stored := newExecutionFixture(t)
	store := repository.NewExecutionStore(db.DB)

	created, err := store.Create(context.Background(), tenant, execution.Record{
		WorkflowID:        stored.ID,
		WorkflowVersionID: stored.LatestVersion.ID,
		Status:            execution.StatusQueued,
		Trigger:           execution.TriggerWebhook,
		Input:             json.RawMessage(`{"headers":{"Authorization":"Basic c3VwZXItc2VjcmV0","Accept":"application/json"}}`),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// The stored column, not just the returned record, must be clean: a later
	// reader, a database dump, and a support export all bypass the API mapper.
	var raw string
	if err := db.Raw("SELECT input FROM executions WHERE id = ?", created.ID).Scan(&raw).Error; err != nil {
		t.Fatalf("read stored input: %v", err)
	}
	if strings.Contains(raw, "c3VwZXItc2VjcmV0") {
		t.Fatalf("stored execution input still contains the Basic credential: %s", raw)
	}
	if !strings.Contains(raw, "application/json") {
		t.Fatalf("redaction dropped a safe header from storage: %s", raw)
	}

	claimed, _, found, err := store.ClaimNext(context.Background(), "redaction-worker", time.Now().Add(time.Second))
	if err != nil || !found {
		t.Fatalf("ClaimNext() = (%v, %v), want claimed", found, err)
	}
	if _, err := store.CreateNodeRun(context.Background(), tenant, execution.NodeRun{
		ExecutionID: created.ID,
		NodeID:      "http",
		Attempt:     1,
		Sequence:    1,
		Status:      execution.StatusSucceeded,
		Input:       json.RawMessage(`{"main":[{"json":{"token":"sk-live-4242"}}]}`),
		Output:      json.RawMessage(`[[{"json":{"setCookie":"sid=abc"}}]]`),
		LeaseOwner:  claimed.LeaseOwner,
	}); err != nil {
		t.Fatalf("CreateNodeRun() error = %v", err)
	}

	var runInput, runOutput string
	if err := db.Raw("SELECT input, output FROM execution_node_runs WHERE execution_id = ?", created.ID).Row().Scan(&runInput, &runOutput); err != nil {
		t.Fatalf("read stored node run: %v", err)
	}
	if strings.Contains(runInput, "sk-live-4242") {
		t.Fatalf("stored node-run input still contains the token: %s", runInput)
	}
	if strings.Contains(runOutput, "sid=abc") {
		t.Fatalf("stored node-run output still contains the cookie: %s", runOutput)
	}
}

func TestExecutionStoreListsNewestFirstWithFiltersAndCursor(t *testing.T) {
	db, tenant, stored := newExecutionFixture(t)
	store := repository.NewExecutionStore(db.DB)

	base := time.Now().UTC().Add(-time.Hour)
	triggers := []execution.Trigger{execution.TriggerManual, execution.TriggerWebhook, execution.TriggerManual, execution.TriggerSchedule}
	statuses := []execution.Status{execution.StatusSucceeded, execution.StatusFailed, execution.StatusSucceeded, execution.StatusQueued}
	created := make([]string, 0, len(triggers))
	for index := range triggers {
		record, err := store.Create(context.Background(), tenant, execution.Record{
			WorkflowID:        stored.ID,
			WorkflowVersionID: stored.LatestVersion.ID,
			Status:            statuses[index],
			Trigger:           triggers[index],
			StartedAt:         base.Add(time.Duration(index) * time.Minute),
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		created = append(created, record.ID)
	}

	page, err := store.List(context.Background(), tenant, repository.ExecutionFilter{Limit: 2})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Records) != 2 {
		t.Fatalf("first page size = %d, want 2", len(page.Records))
	}
	if page.Records[0].ID != created[3] || page.Records[1].ID != created[2] {
		t.Fatalf("first page = %v, want newest first %v", []string{page.Records[0].ID, page.Records[1].ID}, []string{created[3], created[2]})
	}
	if page.NextCursor == "" {
		t.Fatal("expected a next cursor while more executions remain")
	}
	// A summary listing must not drag every node-run trace into memory.
	if len(page.Records[0].NodeRuns) != 0 {
		t.Errorf("list returned %d node runs, want a summary without a trace", len(page.Records[0].NodeRuns))
	}

	second, err := store.List(context.Background(), tenant, repository.ExecutionFilter{Limit: 2, Cursor: page.NextCursor})
	if err != nil {
		t.Fatalf("List() second page error = %v", err)
	}
	if len(second.Records) != 2 || second.Records[0].ID != created[1] || second.Records[1].ID != created[0] {
		t.Fatalf("second page = %#v, want the two oldest executions", second.Records)
	}
	if second.NextCursor != "" {
		t.Errorf("next cursor = %q, want empty on the final page", second.NextCursor)
	}

	filtered, err := store.List(context.Background(), tenant, repository.ExecutionFilter{
		Statuses: []execution.Status{execution.StatusSucceeded},
		Trigger:  execution.TriggerManual,
	})
	if err != nil {
		t.Fatalf("List() filtered error = %v", err)
	}
	if len(filtered.Records) != 2 {
		t.Fatalf("filtered records = %d, want 2", len(filtered.Records))
	}
	for _, record := range filtered.Records {
		if record.Status != execution.StatusSucceeded || record.Trigger != execution.TriggerManual {
			t.Errorf("filter leaked %s/%s", record.Status, record.Trigger)
		}
	}

	other, err := store.List(context.Background(), repository.TenantScope{ID: "tenant-other"}, repository.ExecutionFilter{})
	if err != nil {
		t.Fatalf("List() other tenant error = %v", err)
	}
	if len(other.Records) != 0 {
		t.Fatalf("another tenant saw %d executions", len(other.Records))
	}

	if _, err := store.List(context.Background(), tenant, repository.ExecutionFilter{Cursor: "not-a-cursor"}); err == nil {
		t.Error("expected a malformed cursor to be rejected")
	}
}

func TestExecutionStoreFiltersListByWorkflow(t *testing.T) {
	db, tenant, stored := newExecutionFixture(t)
	workflowStore := repository.NewWorkflowStore(db.DB)
	otherWorkflow, err := workflowStore.SaveDraft(context.Background(), tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		Name:          "Other workflow",
		Nodes:         []workflow.Node{},
		Connections:   []workflow.Connection{},
		Settings:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}

	store := repository.NewExecutionStore(db.DB)
	for _, source := range []struct {
		workflowID string
		versionID  string
	}{
		{stored.ID, stored.LatestVersion.ID},
		{otherWorkflow.ID, otherWorkflow.LatestVersion.ID},
	} {
		if _, err := store.Create(context.Background(), tenant, execution.Record{
			WorkflowID:        source.workflowID,
			WorkflowVersionID: source.versionID,
			Status:            execution.StatusSucceeded,
			Trigger:           execution.TriggerManual,
		}); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	page, err := store.List(context.Background(), tenant, repository.ExecutionFilter{WorkflowID: stored.ID})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Records) != 1 || page.Records[0].WorkflowID != stored.ID {
		t.Fatalf("workflow filter returned %#v", page.Records)
	}
}

// newExecutionFixture builds a migrated SQLite database with one saved
// workflow revision that executions can legally reference.
func newExecutionFixture(t *testing.T) (*database.DB, repository.TenantScope, workflow.StoredWorkflow) {
	t.Helper()
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "kilasflow.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	tenant := repository.TenantScope{ID: "tenant-a"}
	stored, err := repository.NewWorkflowStore(db.DB).SaveDraft(context.Background(), tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		Name:          "Execution source",
		Nodes:         []workflow.Node{},
		Connections:   []workflow.Connection{},
		Settings:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	return db, tenant, stored
}
