package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// TenantLockOut names what locking a tenant out did: the credentials it
// revoked and the accounts it disabled. It is deliberately not a deletion
// count — nothing is removed here.
type TenantLockOut struct {
	APIKeys int
	Users   int
}

// GORMTenantPurger deletes the rows one tenant owns outside the execution
// trace and the datastore catalogue. It is the row half of a tenant deletion:
// GORMExecutionStore.PurgeTenant owns the trace, the datastore engine owns the
// catalogue and the physical tables, and this owns everything else — the
// trigger rows that feed the tenant work, the definitions that describe it and
// the identity that names it.
//
// It is split into steps rather than one transaction because each step is a
// different kind of evidence and a different kind of risk: a definition that
// refuses to go is a foreign key somebody else owns, while a trigger row that
// refuses to go is intake that must be stopped before anything else is
// deleted. The caller runs them in order and reports each count.
//
// Every method validates the tenant and runs one transaction. Nothing inside
// uses the outer handle: SQLite has a single connection, so a statement issued
// outside the transaction while one is open deadlocks against itself.
type GORMTenantPurger struct {
	db *gorm.DB
}

// NewTenantPurger constructs the tenant purge boundary.
func NewTenantPurger(db *gorm.DB) *GORMTenantPurger {
	return &GORMTenantPurger{db: db}
}

// LockOut revokes the tenant's API keys and disables its accounts, without
// deleting either. It runs before any row is removed so a tenant that is being
// deleted cannot write while the purge is in progress — and a purge that fails
// half way leaves it locked out, which is the safe direction to fail in.
//
// A retried lock-out counts nothing: only rows that are not already revoked or
// disabled are stamped, so the numbers are what this call changed rather than
// what the tenant owns.
func (purger *GORMTenantPurger) LockOut(ctx context.Context, tenant TenantScope) (TenantLockOut, error) {
	var result TenantLockOut
	err := purger.transaction(ctx, tenant, func(tx *gorm.DB, tenantID string) error {
		now := time.Now().UTC()
		keys := tx.Model(&apiKeyModel{}).
			Where("tenant_id = ? AND revoked_at IS NULL", tenantID).
			Update("revoked_at", now)
		if keys.Error != nil {
			return fmt.Errorf("lock out tenant api keys: %w", keys.Error)
		}
		users := tx.Model(&userModel{}).
			Where("tenant_id = ? AND disabled_at IS NULL", tenantID).
			Update("disabled_at", now)
		if users.Error != nil {
			return fmt.Errorf("lock out tenant users: %w", users.Error)
		}
		result = TenantLockOut{APIKeys: int(keys.RowsAffected), Users: int(users.RowsAffected)}
		return nil
	})
	if err != nil {
		return TenantLockOut{}, err
	}
	return result, nil
}

// ActiveTriggerWorkflows lists the tenant's workflows that have a live
// trigger. It is what a caller needs before deleting the credentials those
// triggers authenticate with: a webhook binding exists only while a workflow
// is active, so a binding is the activation, and a route row on its own is a
// workflow that was deactivated and kept its URL.
//
// A PostgreSQL poller (Telegram, for one) keeps running in this process with
// the tenant's bot token after its rows are gone, so the list is read first
// and handed to whatever stops them. Best effort: a failure to stop a trigger
// must not be what leaves a tenant undeletable.
func (purger *GORMTenantPurger) ActiveTriggerWorkflows(ctx context.Context, tenant TenantScope) ([]string, error) {
	var workflowIDs []string
	err := purger.transaction(ctx, tenant, func(tx *gorm.DB, tenantID string) error {
		statement := "SELECT DISTINCT workflow_id FROM " + tx.NamingStrategy.TableName("webhook_bindings") + " WHERE tenant_id = ?"
		if err := tx.Raw(statement, tenantID).Scan(&workflowIDs).Error; err != nil {
			return fmt.Errorf("list tenant trigger workflows: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return workflowIDs, nil
}

// PurgeTriggers deletes the rows that feed a tenant work: its cron schedules,
// the deliveries it has already claimed, the routes it answers on and the
// bindings that make those routes live.
//
// It runs before anything else is deleted, not after the executions are gone.
// A webhook arriving while a purge is in progress queues an execution against
// a workflow version that is about to be deleted, and the definitions step
// then fails on a foreign key — for a busy tenant, on most first attempts.
// None of these four tables is referenced by a foreign key, so they are safe
// to remove first.
func (purger *GORMTenantPurger) PurgeTriggers(ctx context.Context, tenant TenantScope) (map[string]int64, error) {
	return purger.purge(ctx, tenant, []purgeStep{
		{table: "schedules", model: &scheduleModel{}},
		{table: "webhook_deliveries", model: &webhookDeliveryModel{}},
		{table: "webhook_routes", model: &webhookRouteModel{}},
		{table: "webhook_bindings", model: &webhookBindingModel{}},
	})
}

// PurgeDefinitions deletes what the tenant authored: its secret bindings, its
// credentials, every workflow version and publish event, and the workflows
// themselves.
//
// Workflows go last and are hard-deleted. workflowModel carries
// gorm.DeletedAt, so an ordinary Delete stamps the row and reports one row
// affected while the row — the customer's workflow name included — stays in
// the table; a deletion request that left that behind would not be honoured.
// Versions go before workflows because their foreign key is ON DELETE RESTRICT.
func (purger *GORMTenantPurger) PurgeDefinitions(ctx context.Context, tenant TenantScope) (map[string]int64, error) {
	return purger.purge(ctx, tenant, []purgeStep{
		{table: "secret_bindings", model: &secretBindingModel{}},
		{table: "credentials", model: &credentialModel{}},
		{table: "workflow_versions", model: &workflowVersionModel{}},
		{table: "workflow_publish_events", model: &workflowPublishEventModel{}},
		{table: "workflows", model: &workflowModel{}, unscoped: true},
	})
}

// PurgeVectors deletes the tenant's vector collections and their documents.
//
// The document tables are optional infrastructure: SQLite has none of them,
// and a PostgreSQL where migration 6 was skipped for a missing pgvector
// extension has none of the five, so every one is guarded. An unguarded
// DELETE against a table that is not there aborts the transaction, which would
// make the tenant undeletable on exactly the installations that never used
// vectors.
//
// The document tables go first so they can be counted; the foreign key from a
// document to its collection is ON DELETE CASCADE, so the order is not
// required for correctness. They are not in Models(): the drift test compares
// repository.Models() against the migration baseline, and migration 6's
// vector columns cannot be expressed as a GORM model without teaching the
// model a type only one driver has.
func (purger *GORMTenantPurger) PurgeVectors(ctx context.Context, tenant TenantScope) (map[string]int64, error) {
	names := []string{
		"vector_documents_384", "vector_documents_768", "vector_documents_1024", "vector_documents_1536",
		"vector_collections",
	}
	removed := zeroed(names)
	err := purger.transaction(ctx, tenant, func(tx *gorm.DB, tenantID string) error {
		for _, name := range names {
			table := tx.NamingStrategy.TableName(name)
			if !tx.Migrator().HasTable(table) {
				continue
			}
			deleted := tx.Exec("DELETE FROM "+table+" WHERE tenant_id = ?", tenantID)
			if deleted.Error != nil {
				return fmt.Errorf("purge tenant %s: %w", name, deleted.Error)
			}
			removed[name] = deleted.RowsAffected
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}

// PurgeIdentity deletes the tenant's API keys, its accounts and then the
// tenant row itself.
//
// The order is forced: users and api_keys reference tenants ON DELETE RESTRICT,
// so the tenant row can only be the last thing to go. After this the tenant id
// names nothing, which is what makes a second purge a no-op rather than an
// error.
func (purger *GORMTenantPurger) PurgeIdentity(ctx context.Context, tenant TenantScope) (map[string]int64, error) {
	removed := zeroed([]string{"api_keys", "users", "tenants"})
	err := purger.transaction(ctx, tenant, func(tx *gorm.DB, tenantID string) error {
		for _, step := range []struct {
			table string
			model any
		}{
			{"api_keys", &apiKeyModel{}},
			{"users", &userModel{}},
		} {
			deleted := tx.Where("tenant_id = ?", tenantID).Delete(step.model)
			if deleted.Error != nil {
				return fmt.Errorf("purge tenant %s: %w", step.table, deleted.Error)
			}
			removed[step.table] = deleted.RowsAffected
		}
		// The tenant row is keyed by id rather than by tenant_id: it is the
		// only row in the schema whose tenant is itself.
		deleted := tx.Where("id = ?", tenantID).Delete(&tenantModel{})
		if deleted.Error != nil {
			return fmt.Errorf("purge tenant row: %w", deleted.Error)
		}
		removed["tenants"] = deleted.RowsAffected
		return nil
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}

// purgeStep is one table in one step: the name it is reported under, the model
// that names it in the configured prefix, and how it is deleted.
type purgeStep struct {
	table string
	model any
	// unscoped is set for a model that soft-deletes. Without it the DELETE
	// reports a row it did not remove.
	unscoped bool
}

// purge deletes a list of tables by tenant in one transaction and reports one
// count per table, including a zero for every table it handled: a caller that
// has to distinguish "nothing was there" from "this table was not covered"
// cannot do it with a missing key.
func (purger *GORMTenantPurger) purge(ctx context.Context, tenant TenantScope, steps []purgeStep) (map[string]int64, error) {
	removed := make(map[string]int64, len(steps))
	for _, step := range steps {
		removed[step.table] = 0
	}
	err := purger.transaction(ctx, tenant, func(tx *gorm.DB, tenantID string) error {
		for _, step := range steps {
			query := tx.Where("tenant_id = ?", tenantID)
			if step.unscoped {
				query = query.Unscoped()
			}
			deleted := query.Delete(step.model)
			if deleted.Error != nil {
				return fmt.Errorf("purge tenant %s: %w", step.table, deleted.Error)
			}
			removed[step.table] = deleted.RowsAffected
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}

// transaction refuses an empty tenant and runs one step in a single
// transaction against the transaction's own handle.
//
// The tenant is trimmed first: a caller that sent " acme " meant acme, and
// matching the raw string would delete nothing while reporting success. What
// is left after trimming is what every statement in the step is scoped by.
func (purger *GORMTenantPurger) transaction(ctx context.Context, tenant TenantScope, run func(tx *gorm.DB, tenantID string) error) error {
	tenantID := strings.TrimSpace(tenant.ID)
	if tenantID == "" {
		return ErrTenantRequired
	}
	return purger.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return run(tx, tenantID)
	})
}

// zeroed seeds the per-table counts, so a table that is skipped is reported as
// zero rather than left out of the answer.
func zeroed(names []string) map[string]int64 {
	counts := make(map[string]int64, len(names))
	for _, name := range names {
		counts[name] = 0
	}
	return counts
}
