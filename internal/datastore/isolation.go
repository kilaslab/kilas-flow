package datastore

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// PurgeResult names what purging one tenant removed: the catalogue count and
// the physical tables dropped. It is evidence for a deletion request, which
// is honoured only when the caller can see that it was.
type PurgeResult struct {
	Datastores int
	Tables     []string
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
// An empty tenant purges to zero rather than failing, so a retried deletion
// converges. An empty tenant id is refused outright: it matches nothing, and
// a purge that matches nothing by accident is one typo away from a purge
// that matches everything by accident.
func (e *Engine) PurgeTenant(ctx context.Context, tenantID string) (PurgeResult, error) {
	if tenantID == "" {
		return PurgeResult{}, errors.New("datastore: tenant id is required")
	}
	var rows []datastoreModel
	if err := e.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).Find(&rows).Error; err != nil {
		return PurgeResult{}, fmt.Errorf("datastore: list tenant datastores: %w", err)
	}
	if len(rows) == 0 {
		return PurgeResult{}, nil
	}
	ids := make([]string, 0, len(rows))
	tables := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
		tables = append(tables, PhysicalTableName(e.prefix, row.Surrogate))
	}
	err := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, table := range tables {
			statement := "DROP TABLE " + quoteIdent(e.dialect(), table)
			e.emit(statement)
			if err := tx.Exec(statement).Error; err != nil {
				return fmt.Errorf("datastore: drop table for purge: %w", err)
			}
		}
		if err := tx.Where("datastore_id IN ?", ids).Delete(&datastoreColumnModel{}).Error; err != nil {
			return fmt.Errorf("datastore: purge columns: %w", err)
		}
		if err := tx.Where("tenant_id = ?", tenantID).Delete(&datastoreModel{}).Error; err != nil {
			return fmt.Errorf("datastore: purge datastores: %w", err)
		}
		return nil
	})
	if err != nil {
		return PurgeResult{}, err
	}
	return PurgeResult{Datastores: len(rows), Tables: tables}, nil
}
