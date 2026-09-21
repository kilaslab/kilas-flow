package datastore

import (
	"time"

	"gorm.io/gorm/schema"
)

// The catalogue models. They name their tables through the namer rather
// than returning a literal, for the same reason repository.Models does: a
// Tabler implementation would bypass GORM's NamingStrategy and silently
// ignore database.table_prefix, while the migration DDL — which the runner
// rewrites under the same prefix — would not. The two must never disagree.
//
// These models stay out of repository.Models on purpose: that list is the
// drift test's input, and these tables are created by migration 000005
// rather than by the baseline the drift test compares against.

// datastoreModel is one row of the datastores catalogue: the public id the
// API carries, the tenant that owns it, the surrogate that names the
// physical table, and the schema version the fleet runner (FEAT-gxppx1)
// reads without opening the physical table.
type datastoreModel struct {
	ID            string    `gorm:"primaryKey;size:64"`
	TenantID      string    `gorm:"not null;size:64"`
	Name          string    `gorm:"not null;size:255"`
	Surrogate     string    `gorm:"not null;size:16;uniqueIndex:uidx_datastores_surrogate"`
	SchemaVersion int       `gorm:"not null;default:1"`
	CreatedAt     time.Time `gorm:"not null"`
	UpdatedAt     time.Time `gorm:"not null"`
}

func (datastoreModel) TableName(namer schema.Namer) string {
	return namer.TableName("datastores")
}

// datastoreColumnModel is one row of the datastore_columns catalogue: the
// user columns of one datastore in definition order. Position is the
// explicit integer the ticket asks for, so column order survives drivers
// that return catalogue rows in any order they like.
//
// TenantID repeats the owning datastore's tenant on the row itself. The
// catalogue used to be safe only because every read came after a tenant-scoped
// lookup of the datastore; carrying the tenant here means a column read that
// forgets that lookup still cannot cross tenants, and a tenant purge can delete
// the columns by tenant rather than by a list of ids.
type datastoreColumnModel struct {
	ID          uint   `gorm:"primaryKey;autoIncrement"`
	TenantID    string `gorm:"not null;size:64;default:'';index:idx_datastore_columns_tenant,priority:1"`
	DatastoreID string `gorm:"not null;size:64;index:idx_datastore_columns_datastore,priority:1;uniqueIndex:uidx_datastore_columns_datastore_name,priority:1;index:idx_datastore_columns_tenant,priority:2"`
	Name        string `gorm:"not null;size:255;uniqueIndex:uidx_datastore_columns_datastore_name,priority:2"`
	Type        string `gorm:"not null;size:16"`
	Position    int    `gorm:"not null;default:0"`
}

func (datastoreColumnModel) TableName(namer schema.Namer) string {
	return namer.TableName("datastore_columns")
}
