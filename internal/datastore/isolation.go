package datastore

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// PurgeResult names what purging one tenant removed: its datastores, the
// catalogue columns that described them and the physical tables dropped. It
// is evidence for a deletion request, which is honoured only when the caller
// can see that it was.
type PurgeResult struct {
	Datastores int
	Tables     []string
	Columns    int
}

// PurgeTenant drops every physical table the tenant owns and removes its
// catalogue rows, in one transaction. Dropping and deleting together is what
// makes a deletion request real: stopping at DROP TABLE would leave the
// catalogue naming tables that no longer exist, and stopping at the
// catalogue would leave the cells on disk.
//
// The trace needs no scrubbing because it never holds cells: datastore node
// runs record counts and row identifiers (see trace.go), so no cell value
// survives in any node-run row after the tables are gone. Execution and
// node-run rows for the tenant live in the repository layer and are deleted
// there, not here — this purge owns the catalogue and the physical tables
// only.
//
// The catalogue read is part of that transaction, and the tables this call
// drops are exactly the tables it reads: the rows it deletes are the rows it
// read. A datastore created while the purge runs — there is always another
// connection, and a worker running a datastore node's create operation is not
// stopped by a deletion — therefore keeps its row, its columns and its table,
// and the next call converges on it. Reading outside the transaction could
// instead delete a row whose table the read never listed, and a table named by
// no row is a table no later purge can find.
//
// An empty tenant purges to zero rather than failing, so a retried deletion
// converges. An empty tenant id is refused outright: it matches nothing, and
// a purge that matches nothing by accident is one typo away from a purge
// that matches everything by accident.
func (e *Engine) PurgeTenant(ctx context.Context, tenantID string) (PurgeResult, error) {
	if tenantID == "" {
		return PurgeResult{}, errors.New("datastore: tenant id is required")
	}
	var result PurgeResult
	err := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The catalogue read is inside the transaction, on tx, and everything
		// this call deletes is keyed on what that read returned. The pairing
		// is the invariant: the rows a purge deletes are the rows it read, so
		// a datastore created while it runs — a worker executing a datastore
		// node's create operation, a session inside the middleware's
		// revalidation window — keeps both its row and its table, and the
		// next call converges on it.
		//
		// Reading outside the transaction and deleting by tenant breaks that
		// in the one direction that cannot be repaired: the read is what
		// names the tables to drop, so a row that appears after it is deleted
		// without its table, that table is then named by nothing at all, and
		// no later purge can find it — while the deletion reports success
		// over cells that are still on disk. The read goes through tx rather
		// than the outer handle because SQLite's handle carries a single
		// connection and tx is holding it.
		var rows []datastoreModel
		if err := tx.Where("tenant_id = ?", tenantID).Find(&rows).Error; err != nil {
			return fmt.Errorf("datastore: list tenant datastores: %w", err)
		}
		ids := make([]string, 0, len(rows))
		tables := make([]string, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
			tables = append(tables, PhysicalTableName(e.prefix, row.Surrogate))
		}
		for _, table := range tables {
			// IF EXISTS because an interrupted purge leaves the catalogue
			// naming a table that is already gone: a retry that failed on the
			// table it removed itself would never converge.
			statement := "DROP TABLE IF EXISTS " + quoteIdent(e.dialect(), table)
			e.emit(statement)
			if err := tx.Exec(statement).Error; err != nil {
				return fmt.Errorf("datastore: drop table for purge: %w", err)
			}
		}
		// The columns of the datastores just read, by their ids, so the count
		// is exactly what this call removed. The list is bounded by the
		// per-tenant datastore limit Create enforces.
		own := tx.Where("tenant_id = ? AND datastore_id IN ?", tenantID, ids).
			Delete(&datastoreColumnModel{})
		if own.Error != nil {
			return fmt.Errorf("datastore: purge columns: %w", own.Error)
		}
		// Then the tenant's leftovers: a column row naming a datastore this
		// tenant does not own is invisible to every read (reads filter on the
		// tenant) and a delete keyed on the ids above would leave it behind
		// for ever. A column row naming a datastore this tenant does own and
		// this call did not read is a datastore created while the purge ran,
		// and it keeps its columns with its row and its table.
		leftover := tx.Where("tenant_id = ? AND datastore_id NOT IN (?)",
			tenantID, tx.Model(&datastoreModel{}).Select("id").Where("tenant_id = ?", tenantID),
		).Delete(&datastoreColumnModel{})
		if leftover.Error != nil {
			return fmt.Errorf("datastore: purge leftover columns: %w", leftover.Error)
		}
		// The catalogue rows go last, keyed on the same read: the cascade
		// from them would remove columns the count above has already taken.
		if err := tx.Where("tenant_id = ? AND id IN ?", tenantID, ids).
			Delete(&datastoreModel{}).Error; err != nil {
			return fmt.Errorf("datastore: purge datastores: %w", err)
		}
		result = PurgeResult{
			Datastores: len(rows),
			Tables:     tables,
			Columns:    int(own.RowsAffected + leftover.RowsAffected),
		}
		return nil
	})
	if err != nil {
		return PurgeResult{}, err
	}
	return result, nil
}
