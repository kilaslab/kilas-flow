package datastore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/database"
)

// Injection stages for Engine.inject, the test seam that proves the single
// transaction holds: failing StageCatalogue must leave no physical table,
// failing StageDDL must leave no catalogue row.
const (
	StageCatalogue = "catalogue"
	StageDDL       = "ddl"
)

// Datastore is the definition the engine created: the public id, the owner,
// the physical table, and the normalised columns in definition order.
type Datastore struct {
	ID            string
	TenantID      string
	Name          string
	Surrogate     string
	Table         string
	SchemaVersion int
	Columns       []ColumnDef
}

// Engine creates and drops one physical table per datastore. The prefix is
// threaded once at construction — database.table_prefix via the caller's
// config.Database, which is the single accessor the ticket asks for —
// rather than into every DDL call, so the DDL path can never disagree with
// itself about it.
type Engine struct {
	db     *database.DB
	prefix string
	// limits bounds per-tenant and per-table growth. It defaults to
	// DefaultLimits at construction and moves only through SetLimits, so no
	// call site can run unbounded by forgetting to pass a configuration.
	limits Limits

	// inject fails a stage inside the write transaction; composeHook sees
	// every DDL statement the engine composes. Both are nil in production
	// and exist so tests can prove atomicity and naming without a live
	// failure.
	inject      func(stage string) error
	composeHook func(statement string)
}

// NewEngine binds the engine to a handle Open built — which carries the
// prefix in its naming strategy — and the configured prefix itself, which
// the runtime DDL needs directly because it is not GORM. The two must be
// the same value; wiring passes cfg.TablePrefix for both.
func NewEngine(db *database.DB, prefix string) (*Engine, error) {
	if len(prefix) > config.MaxTablePrefixLength {
		return nil, fmt.Errorf("datastore: table prefix %q is %d bytes, past the %d-byte cap that keeps every identifier within PostgreSQL's 63-byte limit",
			prefix, len(prefix), config.MaxTablePrefixLength)
	}
	return &Engine{db: db, prefix: prefix, limits: DefaultLimits()}, nil
}

// dialect reports which DDL shape to compose. The handle only ever carries
// the two drivers the migrations ship.
func (e *Engine) dialect() string {
	return e.db.Dialector.Name()
}

// emit records a composed statement for the test seam.
func (e *Engine) emit(statement string) {
	if e.composeHook != nil {
		e.composeHook(statement)
	}
}

// fail runs the injected failure for a stage, if any.
func (e *Engine) fail(stage string) error {
	if e.inject != nil {
		return e.inject(stage)
	}
	return nil
}

// newPublicID mints the catalogue id in the same shape
// internal/workflow.NewID returns — a prefix plus a UUIDv7 — kept local so
// the storage engine does not depend on the workflow package. The id is
// carried by the API only; it never appears in a physical identifier.
func newPublicID() (string, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("datastore: generate datastore id: %w", err)
	}
	return "datastore_" + value.String(), nil
}

// Create writes the catalogue rows first and issues the CREATE TABLE
// second, in one transaction. A surrogate collision retries the whole
// operation with a fresh surrogate; anything else aborts and leaves
// neither a row nor a table.
func (e *Engine) Create(ctx context.Context, tenantID, name string, in []ColumnInput) (*Datastore, error) {
	if tenantID == "" {
		return nil, errors.New("datastore: tenant id is required")
	}
	if name == "" {
		return nil, errors.New("datastore: name is required")
	}
	// Counted before anything is minted or written, so a refused creation
	// leaves neither a catalogue row nor a table and names what is full.
	if err := e.checkDatastoreLimit(ctx, tenantID); err != nil {
		return nil, err
	}
	cols, err := normalizeColumns(in)
	if err != nil {
		return nil, err
	}
	if err := e.checkColumnLimit(name, len(cols)); err != nil {
		return nil, err
	}
	// A random collision is retried; a deterministic one would be
	// permanent, which is why the surrogate is random rather than hashed.
	for range 3 {
		id, err := newPublicID()
		if err != nil {
			return nil, err
		}
		surrogate, err := mintSurrogate()
		if err != nil {
			return nil, err
		}
		ds := &Datastore{
			ID:            id,
			TenantID:      tenantID,
			Name:          name,
			Surrogate:     surrogate,
			Table:         PhysicalTableName(e.prefix, surrogate),
			SchemaVersion: CurrentSchemaVersion,
			Columns:       cols,
		}
		err = e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			row := datastoreModel{
				ID:            ds.ID,
				TenantID:      ds.TenantID,
				Name:          ds.Name,
				Surrogate:     ds.Surrogate,
				SchemaVersion: ds.SchemaVersion,
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			for _, col := range cols {
				if err := tx.Create(&datastoreColumnModel{
					DatastoreID: ds.ID,
					Name:        col.Name,
					Type:        string(col.Type),
					Position:    col.Position,
				}).Error; err != nil {
					return err
				}
			}
			if err := e.fail(StageCatalogue); err != nil {
				return err
			}
			statement := createTableStatement(e.dialect(), e.prefix, surrogate, cols)
			e.emit(statement)
			if err := tx.Exec(statement).Error; err != nil {
				return fmt.Errorf("datastore: create table for %s: %w", ds.ID, err)
			}
			return e.fail(StageDDL)
		})
		if err == nil {
			return ds, nil
		}
		if !errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("datastore: surrogate kept colliding, mint another definition")
}

// Drop removes the catalogue rows and the physical table together. Dropping
// a datastore that has no catalogue row is a no-op rather than an error, so
// a retried or doubled drop converges instead of failing.
func (e *Engine) Drop(ctx context.Context, tenantID, id string) error {
	row, _, err := e.lookup(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if row == nil {
		return nil
	}
	table := PhysicalTableName(e.prefix, row.Surrogate)
	return e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("datastore_id = ?", id).Delete(&datastoreColumnModel{}).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&datastoreModel{}).Error; err != nil {
			return err
		}
		statement := "DROP TABLE " + quoteIdent(e.dialect(), table)
		e.emit(statement)
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("datastore: drop table for %s: %w", id, err)
		}
		return nil
	})
}

// AddColumn appends one user column to the definition and the table. The
// position is one past the current maximum, so order is append-only and a
// later ticket can always reconstruct it.
func (e *Engine) AddColumn(ctx context.Context, tenantID, id string, in ColumnInput) error {
	row, existing, err := e.lookup(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if row == nil {
		return fmt.Errorf("datastore: unknown datastore %q", id)
	}
	cols, err := normalizeColumns(append(columnInputs(existing), in))
	if err != nil {
		return err
	}
	added := cols[len(cols)-1]
	added.Position = len(existing)
	// Refused before the catalogue write and the ALTER TABLE, so a breach
	// leaves both untouched.
	if err := e.checkColumnLimit(id, len(cols)); err != nil {
		return err
	}
	table := PhysicalTableName(e.prefix, row.Surrogate)
	return e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&datastoreColumnModel{
			DatastoreID: id,
			Name:        added.Name,
			Type:        string(added.Type),
			Position:    added.Position,
		}).Error; err != nil {
			return err
		}
		typ, err := physicalType(e.dialect(), added.Type)
		if err != nil {
			return err
		}
		statement := "ALTER TABLE " + quoteIdent(e.dialect(), table) +
			" ADD " + quoteIdent(e.dialect(), added.Name) + " " + typ
		e.emit(statement)
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("datastore: add column %s to %s: %w", added.Name, id, err)
		}
		return nil
	})
}

// RenameColumn renames one user column in the catalogue and the table. The
// new name passes the same validation as a created one, minus the column
// being renamed.
func (e *Engine) RenameColumn(ctx context.Context, tenantID, id, oldName, newName string) error {
	row, existing, err := e.lookup(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if row == nil {
		return fmt.Errorf("datastore: unknown datastore %q", id)
	}
	var target *ColumnDef
	others := make([]ColumnInput, 0, len(existing))
	for _, col := range existing {
		if strings.EqualFold(col.Name, oldName) {
			c := col
			target = &c
			continue
		}
		others = append(others, ColumnInput{Name: col.Name, Type: string(col.Type)})
	}
	if target == nil {
		if _, reserved := reservedColumns[strings.ToLower(oldName)]; reserved {
			return fmt.Errorf("datastore: column name %q is reserved (reserved word %q)", oldName, reservedColumns[strings.ToLower(oldName)])
		}
		return fmt.Errorf("datastore: unknown column %q in %s", oldName, id)
	}
	renamed, err := normalizeColumns(append(others, ColumnInput{Name: newName, Type: string(target.Type)}))
	if err != nil {
		return err
	}
	_ = renamed
	table := PhysicalTableName(e.prefix, row.Surrogate)
	return e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&datastoreColumnModel{}).
			Where("datastore_id = ? AND name = ?", id, target.Name).
			Update("name", newName).Error; err != nil {
			return err
		}
		statement := "ALTER TABLE " + quoteIdent(e.dialect(), table) +
			" RENAME COLUMN " + quoteIdent(e.dialect(), target.Name) +
			" TO " + quoteIdent(e.dialect(), newName)
		e.emit(statement)
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("datastore: rename column %s to %s in %s: %w", oldName, newName, id, err)
		}
		return nil
	})
}

// DropColumn removes one user column from the catalogue and the table.
// System columns are refused with the reserved-word error, not "unknown".
func (e *Engine) DropColumn(ctx context.Context, tenantID, id, name string) error {
	row, existing, err := e.lookup(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if row == nil {
		return fmt.Errorf("datastore: unknown datastore %q", id)
	}
	var target *ColumnDef
	for _, col := range existing {
		if strings.EqualFold(col.Name, name) {
			c := col
			target = &c
			break
		}
	}
	if target == nil {
		if canonical, reserved := reservedColumns[strings.ToLower(name)]; reserved {
			return fmt.Errorf("datastore: column name %q is reserved (reserved word %q)", name, canonical)
		}
		return fmt.Errorf("datastore: unknown column %q in %s", name, id)
	}
	table := PhysicalTableName(e.prefix, row.Surrogate)
	return e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("datastore_id = ? AND name = ?", id, target.Name).Delete(&datastoreColumnModel{}).Error; err != nil {
			return err
		}
		statement := "ALTER TABLE " + quoteIdent(e.dialect(), table) +
			" DROP COLUMN " + quoteIdent(e.dialect(), target.Name)
		e.emit(statement)
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("datastore: drop column %s from %s: %w", name, id, err)
		}
		return nil
	})
}

// TableExists reports whether the datastore's physical table is present. A
// datastore with no catalogue row has no table.
func (e *Engine) TableExists(ctx context.Context, tenantID, id string) (bool, error) {
	row, _, err := e.lookup(ctx, tenantID, id)
	if err != nil {
		return false, err
	}
	if row == nil {
		return false, nil
	}
	return e.db.Migrator().HasTable(PhysicalTableName(e.prefix, row.Surrogate)), nil
}

// lookup returns the catalogue row and its user columns in position order,
// or nils when the datastore is unknown. Unknown is not an error: Drop and
// TableExists treat it as absent.
func (e *Engine) lookup(ctx context.Context, tenantID, id string) (*datastoreModel, []ColumnDef, error) {
	var row datastoreModel
	err := e.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var stored []datastoreColumnModel
	if err := e.db.WithContext(ctx).Where("datastore_id = ?", id).Order("position").Find(&stored).Error; err != nil {
		return nil, nil, err
	}
	cols := make([]ColumnDef, 0, len(stored))
	for _, col := range stored {
		cols = append(cols, ColumnDef{Name: col.Name, Type: ColumnType(col.Type), Position: col.Position})
	}
	return &row, cols, nil
}

// columnInputs renders stored columns back into inputs for revalidation.
func columnInputs(cols []ColumnDef) []ColumnInput {
	in := make([]ColumnInput, 0, len(cols))
	for _, col := range cols {
		in = append(in, ColumnInput{Name: col.Name, Type: string(col.Type)})
	}
	return in
}

// createTableStatement composes the CREATE TABLE for a datastore: the
// auto-increment id first, the user columns in definition order, and the
// database-set timestamps last. The primary key carries an explicit name so
// no index is ever left for PostgreSQL to name. It is a pure function of
// its arguments so both dialects are assertable without a live server.
func createTableStatement(dialect, prefix, surrogate string, cols []ColumnDef) string {
	table := PhysicalTableName(prefix, surrogate)
	q := func(name string) string { return quoteIdent(dialect, name) }

	lines := []string{"CREATE TABLE " + q(table) + " ("}
	if dialect == "sqlite" {
		// The rowid alias with AUTOINCREMENT, the same shape the
		// webhook tables use: deleted ids are never reused, and a new
		// row arrives as id 1 with both timestamps set by the database.
		lines = append(lines, "  "+q("id")+" INTEGER PRIMARY KEY AUTOINCREMENT,")
	} else {
		// bigserial, the shape the webhook tables use on this driver.
		// The key constraint is named here rather than left inline so
		// the service chooses every name the statement introduces.
		lines = append(lines, "  "+q("id")+" bigserial,")
	}
	for _, col := range cols {
		typ, err := physicalType(dialect, col.Type)
		if err != nil {
			// Unreachable: normalizeColumns accepted every type before
			// the statement was composed.
			typ = "TEXT"
		}
		// Nullable with no default, matching the Add Column dialog,
		// which offers no nullability, uniqueness or default.
		lines = append(lines, "  "+q(col.Name)+" "+typ+",")
	}
	// Both timestamps default at the database, so a row inserted with no
	// values still arrives stamped. Writers set updatedAt on update; that
	// write path belongs to the row store, not to this DDL.
	if dialect == "sqlite" {
		lines = append(lines, "  "+q("createdAt")+" DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,")
		lines = append(lines, "  "+q("updatedAt")+" DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP")
	} else {
		lines = append(lines, "  "+q("createdAt")+" TIMESTAMPTZ(3) NOT NULL DEFAULT now(),")
		lines = append(lines, "  "+q("updatedAt")+" TIMESTAMPTZ(3) NOT NULL DEFAULT now(),")
		lines = append(lines, "  CONSTRAINT "+q(PhysicalPKName(prefix, surrogate))+" PRIMARY KEY ("+q("id")+")")
	}
	return strings.Join(lines, "\n") + "\n)"
}
