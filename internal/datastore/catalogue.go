// Package datastore turns a datastore definition into one physical table.
package datastore

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// ListDatastores returns every datastore the tenant owns, in name order, with
// live columns in definition order. It opens no physical table: the catalogue
// rows are the whole answer, which is what makes the management list cheap
// and what the From-list resource locator reads at edit time.
//
// Unbounded on purpose, and kept for the locator: it reads the whole catalogue
// to resolve a name to an id at edit time, in process, for one tenant. The HTTP
// listing is ListDatastoresPage, which is what a client-facing request uses so
// it cannot ask for every table a tenant owns (BUG-fv5fer).
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
		cols, err := e.columnsOf(ctx, row.TenantID, row.ID)
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

// DefaultDatastorePageSize and MaxDatastorePageSize bound the catalogue
// listing. They are wider than the row-page bounds because a datastore is a
// management object, not a data row: a tenant with more than five hundred of
// them has a naming problem the API should not solve by loading all of them.
const (
	DefaultDatastorePageSize = 100
	MaxDatastorePageSize     = 500
)

// DatastoreQuery is one catalogue listing: the keyset cursor pinning the last
// (name, id) pair seen, and the page size.
type DatastoreQuery struct {
	Cursor string
	Limit  int
}

// DatastorePage is one page of datastores in name order with the cursor for the
// next.
type DatastorePage struct {
	Datastores []Datastore
	NextCursor string
}

// ListDatastoresPage returns one page of the tenant's datastores, in name
// order.
//
// Pagination is keyset on (name, id). A name is unique within its tenant, but
// the id tiebreaker keeps the order total and the cursor exact without leaning
// on that, so no change to which names the catalogue accepts can make a cursor
// skip or repeat a row. A datastore created while a client pages sorts into its
// own position rather than shifting rows onto a page already read.
//
// Columns are read for the whole page in one query rather than one per
// datastore: the page is bounded at MaxDatastorePageSize, and a per-row read
// would be a hundred extra round trips through SQLite's single connection to
// render a list.
func (e *Engine) ListDatastoresPage(ctx context.Context, tenantID string, q DatastoreQuery) (DatastorePage, error) {
	if e == nil || e.db == nil {
		return DatastorePage{}, errors.New("datastore: engine is not configured")
	}

	limit := q.Limit
	if limit <= 0 {
		limit = DefaultDatastorePageSize
	}
	if limit > MaxDatastorePageSize {
		limit = MaxDatastorePageSize
	}

	query := e.db.WithContext(ctx).Model(&datastoreModel{}).Where("tenant_id = ?", tenantID)
	if q.Cursor != "" {
		name, id, err := decodeDatastoreCursor(q.Cursor)
		if err != nil {
			return DatastorePage{}, err
		}
		query = query.Where("(name > ?) OR (name = ? AND id > ?)", name, name, id)
	}

	// Read one extra row to learn whether another page exists without a second
	// COUNT over the same predicate.
	var rows []datastoreModel
	if err := query.Order("name ASC, id ASC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return DatastorePage{}, fmt.Errorf("datastore: list datastores: %w", err)
	}
	page := DatastorePage{Datastores: make([]Datastore, 0, limit)}
	if len(rows) > limit {
		last := rows[limit-1]
		page.NextCursor = encodeDatastoreCursor(last.Name, last.ID)
		rows = rows[:limit]
	}

	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	columns, err := e.columnsByDatastore(ctx, tenantID, ids)
	if err != nil {
		return DatastorePage{}, err
	}
	for _, row := range rows {
		page.Datastores = append(page.Datastores, Datastore{
			ID: row.ID, TenantID: row.TenantID, Name: row.Name,
			Surrogate: row.Surrogate, Table: PhysicalTableName(e.prefix, row.Surrogate),
			SchemaVersion: row.SchemaVersion, Columns: columns[row.ID],
		})
	}
	return page, nil
}

// datastoreCursorPrefix versions the catalogue cursor wire format, so carrying
// another sort value later is a new version rather than a silent reinterpretation
// of cursors already in a client's hands.
const datastoreCursorPrefix = "ds-v1"

// ErrInvalidDatastoreCursor reports a catalogue Listing cursor this engine did
// not issue. The HTTP layer maps it to a 400, never a 500: a cursor a client
// invented names no position, and answering with the first page instead would
// silently repeat rows.
var ErrInvalidDatastoreCursor = errors.New("datastore: invalid datastore cursor")

func encodeDatastoreCursor(name, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(datastoreCursorPrefix + "\x00" + name + "\x00" + id))
}

// decodeDatastoreCursor inverts encodeDatastoreCursor. A cursor this engine did
// not issue is ErrInvalidDatastoreCursor rather than a position, which the API
// answers as a 400.
func decodeDatastoreCursor(cursor string) (string, string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", fmt.Errorf("%w: datastore cursor is malformed", ErrInvalidDatastoreCursor)
	}
	prefix, rest, found := strings.Cut(string(decoded), "\x00")
	if !found || prefix != datastoreCursorPrefix {
		return "", "", fmt.Errorf("%w: datastore cursor is malformed", ErrInvalidDatastoreCursor)
	}
	name, id, found := strings.Cut(rest, "\x00")
	if !found || id == "" {
		return "", "", fmt.Errorf("%w: datastore cursor is malformed", ErrInvalidDatastoreCursor)
	}
	return name, id, nil
}

// columnsByDatastore reads the live columns of every named datastore in one
// query, in position order, grouped by datastore. A datastore with no columns
// maps to a nil slice, which is the same empty answer columnsOf gives it.
func (e *Engine) columnsByDatastore(ctx context.Context, tenantID string, ids []string) (map[string][]ColumnDef, error) {
	grouped := make(map[string][]ColumnDef, len(ids))
	if len(ids) == 0 {
		return grouped, nil
	}
	var stored []datastoreColumnModel
	if err := e.db.WithContext(ctx).
		Where("datastore_id IN ? AND tenant_id = ?", ids, tenantID).
		Order("datastore_id ASC, position ASC").
		Find(&stored).Error; err != nil {
		return nil, fmt.Errorf("datastore: read columns: %w", err)
	}
	for _, col := range stored {
		grouped[col.DatastoreID] = append(grouped[col.DatastoreID],
			ColumnDef{Name: col.Name, Type: ColumnType(col.Type), Position: col.Position})
	}
	return grouped, nil
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
//
// A name another of the tenant's tables holds is refused with a
// *NameTakenError, by the same rule Create applies; the table's own name in
// another case is not another table's, and is allowed.
func (e *Engine) RenameDatastore(ctx context.Context, tenantID, id, name string) error {
	if e == nil || e.db == nil {
		return errors.New("datastore: engine is not configured")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("datastore: name is required")
	}
	row, _, err := e.lookup(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if row == nil {
		return fmt.Errorf("datastore: unknown datastore %q", id)
	}
	err = e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := nameHolder(tx, tenantID, id, name); err != nil {
			return err
		}
		if err := e.fail(StageName); err != nil {
			return err
		}
		return tx.Model(&datastoreModel{}).
			Where("tenant_id = ? AND id = ?", tenantID, id).
			Update("name", name).Error
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		// The only unique key a rename writes is the name: a writer took it
		// after the check, or the database folds it where Go does not. Asking
		// the index's own question is what names the table in the way.
		if recheck := indexedNameHolder(e.db.WithContext(ctx), tenantID, id, name); recheck != nil {
			return recheck
		}
	}
	if err == nil || errors.Is(err, ErrNameTaken) {
		return err
	}
	return fmt.Errorf("datastore: rename %s: %w", id, err)
}

// columnsOf reads one datastore's live columns in position order.
//
// Scoped by the tenant as well as the datastore, so a catalogue row that names
// a datastore its tenant does not own is invisible here rather than rendered as
// one of that datastore's columns.
func (e *Engine) columnsOf(ctx context.Context, tenantID, id string) ([]ColumnDef, error) {
	var stored []datastoreColumnModel
	if err := e.db.WithContext(ctx).
		Where("datastore_id = ? AND tenant_id = ?", id, tenantID).
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
