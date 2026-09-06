// Package datastore turns a datastore definition into one physical table.
package datastore

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// List returns every datastore the tenant owns, in name order, with live
// columns in definition order. It opens no physical table: the catalogue
// rows are the whole answer, which is what makes the management list cheap
// and what the From-list resource locator reads at edit time.
func (e *Engine) ListDatastores(ctx context.Context, tenantID string) ([]Datastore, error) {
	if e == nil || e.db == nil {
		return nil, errors.New("datastore: engine is not configured")
	}
	var rows []datastoreModel
	if err := e.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("name ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("datastore: list datastores: %w", err)
	}
	out := make([]Datastore, 0, len(rows))
	for _, row := range rows {
		cols, err := e.columnsOf(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, Datastore{
			ID: row.ID, TenantID: row.TenantID, Name: row.Name,
			Surrogate: row.Surrogate, Table: PhysicalTableName(e.prefix, row.Surrogate),
			SchemaVersion: row.SchemaVersion, Columns: cols,
		})
	}
	return out, nil
}

// Get returns one datastore with its live columns, or an unknown-datastore
// error when the tenant owns no datastore of that id. Unknown is an error
// here rather than the nils lookup returns: a management read names its
// subject, and callers that want absence-as-nil (Drop, TableExists) already
// have lookup.
func (e *Engine) GetDatastore(ctx context.Context, tenantID, id string) (*Datastore, error) {
	if e == nil || e.db == nil {
		return nil, errors.New("datastore: engine is not configured")
	}
	row, cols, err := e.lookup(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("datastore: unknown datastore %q", id)
	}
	return &Datastore{
		ID: row.ID, TenantID: row.TenantID, Name: row.Name,
		Surrogate: row.Surrogate, Table: PhysicalTableName(e.prefix, row.Surrogate),
		SchemaVersion: row.SchemaVersion, Columns: cols,
	}, nil
}

// Rename changes a datastore's name and nothing else. It is the only
// table-level update the management API exposes: n8n surfaces this operation
// as Rename a data table, and a generic update would suggest columns can be
// rewritten through it when they each have their own endpoint.
func (e *Engine) RenameDatastore(ctx context.Context, tenantID, id, name string) error {
	if e == nil || e.db == nil {
		return errors.New("datastore: engine is not configured")
	}
	if strings.TrimSpace(name) == "" {
		return errors.New("datastore: name is required")
	}
	row, _, err := e.lookup(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if row == nil {
		return fmt.Errorf("datastore: unknown datastore %q", id)
	}
	if err := e.db.WithContext(ctx).
		Model(&datastoreModel{}).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Update("name", name).Error; err != nil {
		return fmt.Errorf("datastore: rename %s: %w", id, err)
	}
	return nil
}

// columnsOf reads one datastore's live columns in position order.
func (e *Engine) columnsOf(ctx context.Context, id string) ([]ColumnDef, error) {
	var stored []datastoreColumnModel
	if err := e.db.WithContext(ctx).
		Where("datastore_id = ?", id).
		Order("position ASC").
		Find(&stored).Error; err != nil {
		return nil, fmt.Errorf("datastore: read columns of %s: %w", id, err)
	}
	cols := make([]ColumnDef, 0, len(stored))
	for _, col := range stored {
		cols = append(cols, ColumnDef{Name: col.Name, Type: ColumnType(col.Type), Position: col.Position})
	}
	return cols, nil
}

// IsUnknown reports whether err names an unknown datastore. The engine
// phrases that refusal as "unknown datastore %q" in every path that can
// produce it, so one matcher covers tables, columns and rows alike, and the
// HTTP layer maps it to 404 without learning each call site.
func IsUnknown(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unknown datastore")
}
