package repository_test

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// draftWorkflowFixture saves one draft for the tenant, so the tables whose
// foreign keys point at workflows have a row to point at.
func draftWorkflowFixture(t *testing.T, db *database.DB, tenant repository.TenantScope, workflowID string) workflow.StoredWorkflow {
	t.Helper()
	saved, err := repository.NewWorkflowStore(db.DB).SaveDraft(context.Background(), tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            workflowID,
		Name:          "Purge fixture",
		Nodes: []workflow.Node{{
			ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1),
		}},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	return saved
}

// seedWebhookTrigger writes one binding and the route it answers on. A binding
// exists only while a workflow is active, and it is the row the coordinator
// reads to decide which workflows a tenant has live triggers for.
func seedWebhookTrigger(t *testing.T, db *database.DB, tenantID, workflowID, route, method string) {
	t.Helper()
	now := time.Now().UTC()
	if err := db.Exec(
		"INSERT INTO webhook_bindings (tenant_id, workflow_id, workflow_version_id, node_id, node_type, method, route, path, parameters, created_at)"+
			" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		tenantID, workflowID, "v_"+workflowID, "hook", "kilasflow.webhook", method, route, "hook", []byte(`{}`), now,
	).Error; err != nil {
		t.Fatalf("seed webhook binding: %v", err)
	}
	if err := db.Exec(
		"INSERT INTO webhook_routes (tenant_id, workflow_id, node_id, route, created_at) VALUES (?, ?, ?, ?, ?)",
		tenantID, workflowID, "hook", route, now,
	).Error; err != nil {
		t.Fatalf("seed webhook route: %v", err)
	}
}

func seedWebhookDelivery(t *testing.T, db *database.DB, tenantID, route, deliveryID, executionID string) {
	t.Helper()
	if err := db.Exec(
		"INSERT INTO webhook_deliveries (tenant_id, route, delivery_id, execution_id, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		tenantID, route, deliveryID, executionID, time.Now().UTC().Add(time.Minute), time.Now().UTC(),
	).Error; err != nil {
		t.Fatalf("seed webhook delivery: %v", err)
	}
}

// A soft-deleted workflow is still a row: the model carries gorm.DeletedAt, so
// the store's Delete leaves it in the table with a timestamp, and a purge that
// used the same store would report a removal that never happened and leave the
// customer's workflow name behind.
func TestPurgeDefinitionsHardDeletesSoftDeletedWorkflows(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-purge-defs-a"}
		neighbour := repository.TenantScope{ID: "drv-purge-defs-b"}
		workflows := repository.NewWorkflowStore(db.DB)
		saved := draftWorkflowFixture(t, db, tenant, "purge_defs_wf_a")
		draftWorkflowFixture(t, db, neighbour, "purge_defs_wf_b")

		if err := workflows.Delete(ctx, tenant, saved.ID); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if counts := rawTenantCounts(t, db, tenant.ID, "workflows"); counts["workflows"] != 1 {
			t.Fatalf("a soft delete left %d workflow rows, want the 1 that makes this a hard-delete test", counts["workflows"])
		}

		removed, err := repository.NewTenantPurger(db.DB).PurgeDefinitions(ctx, tenant)
		if err != nil {
			t.Fatalf("PurgeDefinitions() error = %v", err)
		}
		if removed["workflows"] != 1 {
			t.Errorf("PurgeDefinitions() = %v, want 1 workflow", removed)
		}
		for _, table := range []string{"workflows", "workflow_versions", "workflow_publish_events", "secret_bindings", "credentials"} {
			if _, ok := removed[table]; !ok {
				t.Errorf("PurgeDefinitions() = %v, want a count for %s", removed, table)
			}
		}
		if counts := rawTenantCounts(t, db, tenant.ID,
			"workflows", "workflow_versions", "workflow_publish_events"); counts["workflows"] != 0 ||
			counts["workflow_versions"] != 0 || counts["workflow_publish_events"] != 0 {
			t.Errorf("the purged tenant still holds %v", counts)
		}
		if counts := rawTenantCounts(t, db, neighbour.ID, "workflows", "workflow_versions"); counts["workflows"] != 1 ||
			counts["workflow_versions"] != 1 {
			t.Errorf("the neighbour lost rows: %v", counts)
		}
	})
}

// users and api_keys reference tenants ON DELETE RESTRICT, so the tenant row
// can only go last. The whole purge succeeding is the proof of that order: an
// implementation that deleted the tenant first fails on the foreign key.
func TestPurgeIdentityDeletesKeysAndUsersBeforeTheTenantRow(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-purge-id-a"}
		neighbour := repository.TenantScope{ID: "drv-purge-id-b"}
		identities := repository.NewAuthStore(db.DB)

		for _, scope := range []repository.TenantScope{tenant, neighbour} {
			if _, err := identities.CreateTenant(ctx, scope.ID, "Purge identity "+scope.ID); err != nil {
				t.Fatalf("CreateTenant(%s) error = %v", scope.ID, err)
			}
			if _, err := identities.CreateUser(ctx, scope, scope.ID+"@example.test", "Purge Fixture", "hash"); err != nil {
				t.Fatalf("CreateUser(%s) error = %v", scope.ID, err)
			}
			if _, _, err := identities.CreateAPIKey(ctx, scope, "purge fixture key"); err != nil {
				t.Fatalf("CreateAPIKey(%s) error = %v", scope.ID, err)
			}
		}

		removed, err := repository.NewTenantPurger(db.DB).PurgeIdentity(ctx, tenant)
		if err != nil {
			t.Fatalf("PurgeIdentity() error = %v", err)
		}
		for table, want := range map[string]int64{"api_keys": 1, "users": 1, "tenants": 1} {
			if removed[table] != want {
				t.Errorf("PurgeIdentity() %s = %d, want %d (all: %v)", table, removed[table], want, removed)
			}
		}
		for table, count := range rawTenantCounts(t, db, tenant.ID, "api_keys", "users") {
			if count != 0 {
				t.Errorf("%s holds %d of the purged tenant's rows, want 0", table, count)
			}
		}
		var tenants int64
		if err := db.Raw("SELECT COUNT(*) FROM tenants WHERE id = ?", tenant.ID).Scan(&tenants).Error; err != nil {
			t.Fatalf("count tenants: %v", err)
		}
		if tenants != 0 {
			t.Errorf("the tenant row survives the purge")
		}
		for table, count := range rawTenantCounts(t, db, neighbour.ID, "api_keys", "users") {
			if count != 1 {
				t.Errorf("the neighbour's %s holds %d rows, want 1", table, count)
			}
		}
	})
}

// Lock-out is not a delete: the rows stay so the audit of what that key did
// still has something to name, but the credential stops working and the
// account stops signing in. A retry changes nothing, which is what lets a
// failed purge be repeated.
func TestLockOutRevokesKeysAndDisablesUsers(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-purge-lock-a"}
		identities := repository.NewAuthStore(db.DB)
		if _, err := identities.CreateTenant(ctx, tenant.ID, "Lock out fixture"); err != nil {
			t.Fatalf("CreateTenant() error = %v", err)
		}
		if _, err := identities.CreateUser(ctx, tenant, "drv-purge-lock-a@example.test", "Locked Out", "hash"); err != nil {
			t.Fatalf("CreateUser() error = %v", err)
		}
		_, token, err := identities.CreateAPIKey(ctx, tenant, "lock out fixture key")
		if err != nil {
			t.Fatalf("CreateAPIKey() error = %v", err)
		}
		if _, err := identities.AuthenticateAPIKey(ctx, token); err != nil {
			t.Fatalf("AuthenticateAPIKey() before the lock-out error = %v, want the key to work", err)
		}

		purger := repository.NewTenantPurger(db.DB)
		locked, err := purger.LockOut(ctx, tenant)
		if err != nil {
			t.Fatalf("LockOut() error = %v", err)
		}
		if locked.APIKeys != 1 || locked.Users != 1 {
			t.Errorf("LockOut() = %+v, want one key and one user", locked)
		}
		if _, err := identities.AuthenticateAPIKey(ctx, token); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Errorf("AuthenticateAPIKey() after the lock-out error = %v, want ErrUnauthenticated", err)
		}
		var disabled int64
		if err := db.Raw("SELECT COUNT(*) FROM users WHERE tenant_id = ? AND disabled_at IS NOT NULL", tenant.ID).Scan(&disabled).Error; err != nil {
			t.Fatalf("count disabled users: %v", err)
		}
		if disabled != 1 {
			t.Errorf("%d users are disabled, want the tenant's single account", disabled)
		}

		// Nothing is removed, and a second lock-out has nothing left to do.
		again, err := purger.LockOut(ctx, tenant)
		if err != nil {
			t.Fatalf("LockOut() retry error = %v", err)
		}
		if again.APIKeys != 0 || again.Users != 0 {
			t.Errorf("LockOut() retry = %+v, want zero: already-revoked rows are not removals", again)
		}
		if counts := rawTenantCounts(t, db, tenant.ID, "api_keys", "users"); counts["api_keys"] != 1 || counts["users"] != 1 {
			t.Errorf("lock-out deleted rows: %v", counts)
		}
	})
}

// The workflows a tenant has live triggers for are the ones whose bindings
// exist. It is what the coordinator is asked to stop before the credentials
// those triggers authenticate with are deleted, so a route row without a
// binding must not be reported — the binding is the activation.
func TestActiveTriggerWorkflowsListsOnlyBoundWorkflows(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-purge-live-a"}
		neighbour := repository.TenantScope{ID: "drv-purge-live-b"}
		seedWebhookTrigger(t, db, tenant.ID, "wf_bound", "route-live-a", "POST")
		seedWebhookTrigger(t, db, neighbour.ID, "wf_neighbour", "route-live-b", "POST")
		// A route outlives the binding on purpose, so a workflow can be
		// deactivated and keep its URL. Only the binding is live.
		if err := db.Exec(
			"INSERT INTO webhook_routes (tenant_id, workflow_id, node_id, route, created_at) VALUES (?, ?, ?, ?, ?)",
			tenant.ID, "wf_dormant", "hook", "route-live-dormant", time.Now().UTC(),
		).Error; err != nil {
			t.Fatalf("seed dormant route: %v", err)
		}

		live, err := repository.NewTenantPurger(db.DB).ActiveTriggerWorkflows(ctx, tenant)
		if err != nil {
			t.Fatalf("ActiveTriggerWorkflows() error = %v", err)
		}
		sort.Strings(live)
		if len(live) != 1 || live[0] != "wf_bound" {
			t.Errorf("ActiveTriggerWorkflows() = %v, want only the bound workflow", live)
		}

		none, err := repository.NewTenantPurger(db.DB).ActiveTriggerWorkflows(ctx, repository.TenantScope{ID: "drv-purge-live-none"})
		if err != nil {
			t.Fatalf("ActiveTriggerWorkflows(unknown tenant) error = %v", err)
		}
		if len(none) != 0 {
			t.Errorf("ActiveTriggerWorkflows(unknown tenant) = %v, want empty", none)
		}
	})
}

// Every trigger row is deleted by tenant, and a delivery row is the one the
// ticket's completeness criterion names explicitly.
func TestPurgeTriggersRemovesSchedulesDeliveriesRoutesAndBindings(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-purge-triggers-a"}
		neighbour := repository.TenantScope{ID: "drv-purge-triggers-b"}
		saved := draftWorkflowFixture(t, db, tenant, "purge_triggers_wf_a")
		kept := draftWorkflowFixture(t, db, neighbour, "purge_triggers_wf_b")
		if _, err := repository.NewScheduleStore(db.DB).Create(ctx, tenant, repository.Schedule{
			WorkflowID: saved.ID, NodeID: "cron", Cron: "0 9 * * *", Active: true,
		}); err != nil {
			t.Fatalf("Create schedule error = %v", err)
		}
		if _, err := repository.NewScheduleStore(db.DB).Create(ctx, neighbour, repository.Schedule{
			WorkflowID: kept.ID, NodeID: "cron", Cron: "0 9 * * *", Active: true,
		}); err != nil {
			t.Fatalf("Create neighbour schedule error = %v", err)
		}
		seedWebhookTrigger(t, db, tenant.ID, saved.ID, "route-triggers-a", "POST")
		seedWebhookDelivery(t, db, tenant.ID, "route-triggers-a", "delivery-triggers-a", "exec-triggers-a")
		seedWebhookTrigger(t, db, neighbour.ID, kept.ID, "route-triggers-b", "POST")

		removed, err := repository.NewTenantPurger(db.DB).PurgeTriggers(ctx, tenant)
		if err != nil {
			t.Fatalf("PurgeTriggers() error = %v", err)
		}
		for table, want := range map[string]int64{
			"schedules": 1, "webhook_deliveries": 1, "webhook_routes": 1, "webhook_bindings": 1,
		} {
			if removed[table] != want {
				t.Errorf("PurgeTriggers() %s = %d, want %d (all: %v)", table, removed[table], want, removed)
			}
		}
		for table, count := range rawTenantCounts(t, db, tenant.ID,
			"schedules", "webhook_deliveries", "webhook_routes", "webhook_bindings") {
			if count != 0 {
				t.Errorf("%s holds %d of the purged tenant's rows, want 0", table, count)
			}
		}
		for table, count := range rawTenantCounts(t, db, neighbour.ID,
			"schedules", "webhook_routes", "webhook_bindings") {
			if count != 1 {
				t.Errorf("the neighbour's %s holds %d rows, want 1", table, count)
			}
		}
	})
}

// The vector tables are optional: SQLite has the catalogue and none of the
// document tables, and a PostgreSQL where migration 6 was skipped for a
// missing pgvector extension has none of the five. An unguarded delete of a
// table that is not there aborts the transaction and the tenant can never be
// purged at all, so this runs on both shapes of database and says which one it
// saw.
func TestPurgeVectorsToleratesAbsentVectorTables(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-purge-vector-a"}
		neighbour := repository.TenantScope{ID: "drv-purge-vector-b"}
		hasCatalogue := db.Migrator().HasTable("vector_collections")
		if hasCatalogue {
			for _, scope := range []repository.TenantScope{tenant, neighbour} {
				if err := db.Exec(
					"INSERT INTO vector_collections (id, tenant_id, name, dimension, distance, index_type, table_name, created_at, updated_at)"+
						" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
					"vc_"+scope.ID, scope.ID, "purge fixture", 384, "cosine", "hnsw", "vector_documents_384",
					time.Now().UTC(), time.Now().UTC(),
				).Error; err != nil {
					t.Fatalf("seed vector collection: %v", err)
				}
			}
		} else {
			t.Log("this database has no vector tables at all (no pgvector extension), so the purge must converge on zeros")
		}

		removed, err := repository.NewTenantPurger(db.DB).PurgeVectors(ctx, tenant)
		if err != nil {
			t.Fatalf("PurgeVectors() error = %v", err)
		}
		want := int64(0)
		if hasCatalogue {
			want = 1
		}
		if removed["vector_collections"] != want {
			t.Errorf("PurgeVectors() = %v, want %d collections", removed, want)
		}
		for _, table := range []string{"vector_documents_384", "vector_documents_768", "vector_documents_1024", "vector_documents_1536"} {
			if removed[table] != 0 {
				t.Errorf("PurgeVectors() %s = %d, want 0: no document rows were seeded", table, removed[table])
			}
			if db.Migrator().HasTable(table) {
				t.Logf("%s exists here, so its guard was not the path this run took", table)
			}
		}
		if !hasCatalogue {
			return
		}
		if counts := rawTenantCounts(t, db, neighbour.ID, "vector_collections"); counts["vector_collections"] != 1 {
			t.Errorf("the neighbour's collection is gone: %v", counts)
		}
		var purged int64
		if err := db.Raw("SELECT COUNT(*) FROM vector_collections WHERE tenant_id = ?", tenant.ID).Scan(&purged).Error; err != nil {
			t.Fatalf("count vector collections: %v", err)
		}
		if purged != 0 {
			t.Errorf("the purged tenant still holds %d collections", purged)
		}
	})
}

// Every method refuses an empty tenant id before it touches the database: a
// purge keyed on "" matches nothing here, and one typo away from that it
// matches everything.
func TestEveryPurgeMethodRefusesAnEmptyTenant(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		purger := repository.NewTenantPurger(db.DB)
		tenant := repository.TenantScope{ID: "drv-purge-guard-a"}
		saved := draftWorkflowFixture(t, db, tenant, "purge_guard_wf_a")
		if _, err := repository.NewScheduleStore(db.DB).Create(ctx, tenant, repository.Schedule{
			WorkflowID: saved.ID, NodeID: "cron", Cron: "0 9 * * *",
		}); err != nil {
			t.Fatalf("Create schedule error = %v", err)
		}

		calls := map[string]func(repository.TenantScope) error{
			"LockOut": func(scope repository.TenantScope) error {
				_, err := purger.LockOut(ctx, scope)
				return err
			},
			"ActiveTriggerWorkflows": func(scope repository.TenantScope) error {
				_, err := purger.ActiveTriggerWorkflows(ctx, scope)
				return err
			},
			"PurgeTriggers": func(scope repository.TenantScope) error {
				_, err := purger.PurgeTriggers(ctx, scope)
				return err
			},
			"PurgeDefinitions": func(scope repository.TenantScope) error {
				_, err := purger.PurgeDefinitions(ctx, scope)
				return err
			},
			"PurgeVectors": func(scope repository.TenantScope) error {
				_, err := purger.PurgeVectors(ctx, scope)
				return err
			},
			"PurgeIdentity": func(scope repository.TenantScope) error {
				_, err := purger.PurgeIdentity(ctx, scope)
				return err
			},
		}
		for name, call := range calls {
			for _, id := range []string{"", "   ", "\t"} {
				if err := call(repository.TenantScope{ID: id}); !errors.Is(err, repository.ErrTenantRequired) {
					t.Errorf("%s(%q) error = %v, want ErrTenantRequired", name, id, err)
				}
			}
		}
		if counts := rawTenantCounts(t, db, tenant.ID, "schedules", "workflows"); counts["schedules"] != 1 || counts["workflows"] != 1 {
			t.Errorf("a refused purge removed rows: %v", counts)
		}
	})
}
