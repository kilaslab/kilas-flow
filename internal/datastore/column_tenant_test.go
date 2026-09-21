package datastore

import (
	"context"
	"testing"
)

// countColumns counts a datastore's catalogue rows through raw SQL, so the
// answer does not depend on any read path under test.
func countColumns(t *testing.T, eng *Engine, datastoreID, tenantClause string, args ...any) int64 {
	t.Helper()
	var n int64
	query := `SELECT COUNT(*) FROM datastore_columns WHERE datastore_id = ? ` + tenantClause
	if err := eng.db.Raw(query, append([]any{datastoreID}, args...)...).Scan(&n).Error; err != nil {
		t.Fatalf("count the columns of %s: %v", datastoreID, err)
	}
	return n
}

// Every catalogue row the engine writes carries its datastore's tenant, so a
// purge can delete the columns by tenant and a read has something to filter on.
func TestDatastoreColumnsCarryTheirTenant(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			ctx := context.Background()

			ds, err := eng.Create(ctx, "tenant-1", "contacts", fullDefinition)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			trackPhysical(t, db, ds.Table)
			if got := countColumns(t, eng, ds.ID, `AND tenant_id = ?`, "tenant-1"); got != int64(len(fullDefinition)) {
				t.Errorf("Create stamped %d of %d columns with the datastore's tenant", got, len(fullDefinition))
			}

			if err := eng.AddColumn(ctx, "tenant-1", ds.ID, ColumnInput{Name: "extra", Type: "string"}); err != nil {
				t.Fatalf("AddColumn: %v", err)
			}
			want := int64(len(fullDefinition)) + 1
			if got := countColumns(t, eng, ds.ID, `AND tenant_id = ?`, "tenant-1"); got != want {
				t.Errorf("after AddColumn, %d of %d columns carry the datastore's tenant", got, want)
			}
			if got := countColumns(t, eng, ds.ID, `AND tenant_id <> ?`, "tenant-1"); got != 0 {
				t.Errorf("%d columns carry another tenant", got)
			}

			// The writes that follow are filtered on the tenant too; they must
			// still reach the rows they mean to.
			if err := eng.RenameColumn(ctx, "tenant-1", ds.ID, "extra", "more"); err != nil {
				t.Fatalf("RenameColumn: %v", err)
			}
			if err := eng.DropColumn(ctx, "tenant-1", ds.ID, "more"); err != nil {
				t.Fatalf("DropColumn: %v", err)
			}
			if got := countColumns(t, eng, ds.ID, ``); got != int64(len(fullDefinition)) {
				t.Errorf("after rename and drop, %d columns remain, want %d", got, len(fullDefinition))
			}

			if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
				t.Fatalf("Drop: %v", err)
			}
			if got := countColumns(t, eng, ds.ID, ``); got != 0 {
				t.Errorf("Drop left %d catalogue columns behind", got)
			}
		})
	}
}

// A column row stamped with another tenant is invisible to every read of the
// datastore, even though its datastore_id names a datastore the caller owns.
// Before the tenant column, only the tenant-scoped lookup of the datastore
// stood between a column read and another tenant's rows.
func TestColumnReadsAreTenantScoped(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			ctx := context.Background()

			ds, err := eng.Create(ctx, "tenant-a", "contacts", []ColumnInput{{Name: "title", Type: "string"}})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			trackPhysical(t, db, ds.Table)

			if err := db.Exec(
				`INSERT INTO datastore_columns (tenant_id, datastore_id, name, type, position) VALUES (?, ?, ?, ?, ?)`,
				"tb", ds.ID, "stray", "string", 9,
			).Error; err != nil {
				t.Fatalf("insert the stray column: %v", err)
			}

			assertOnlyTitle := func(read string, cols []ColumnDef) {
				t.Helper()
				if len(cols) != 1 || cols[0].Name != "title" {
					t.Errorf("%s returned columns %+v, want only the tenant's own title", read, cols)
				}
			}

			got, err := eng.GetDatastore(ctx, "tenant-a", ds.ID)
			if err != nil {
				t.Fatalf("GetDatastore: %v", err)
			}
			assertOnlyTitle("GetDatastore", got.Columns)

			listed, err := eng.ListDatastores(ctx, "tenant-a")
			if err != nil {
				t.Fatalf("ListDatastores: %v", err)
			}
			if len(listed) != 1 {
				t.Fatalf("ListDatastores = %d datastores, want 1", len(listed))
			}
			assertOnlyTitle("ListDatastores", listed[0].Columns)

			page, err := eng.ListDatastoresPage(ctx, "tenant-a", DatastoreQuery{})
			if err != nil {
				t.Fatalf("ListDatastoresPage: %v", err)
			}
			if len(page.Datastores) != 1 {
				t.Fatalf("ListDatastoresPage = %d datastores, want 1", len(page.Datastores))
			}
			assertOnlyTitle("ListDatastoresPage", page.Datastores[0].Columns)

			_, cols, err := eng.lookup(ctx, "tenant-a", ds.ID)
			if err != nil {
				t.Fatalf("lookup: %v", err)
			}
			assertOnlyTitle("lookup", cols)

			// The engine derives the next position from what it can see, so a
			// stray row it could see would push the new column past position 1.
			if err := eng.AddColumn(ctx, "tenant-a", ds.ID, ColumnInput{Name: "extra", Type: "string"}); err != nil {
				t.Fatalf("AddColumn: %v", err)
			}
			var position int
			if err := db.Raw(`SELECT position FROM datastore_columns WHERE datastore_id = ? AND name = ?`, ds.ID, "extra").Scan(&position).Error; err != nil {
				t.Fatalf("read the new column's position: %v", err)
			}
			if position != 1 {
				t.Errorf("the new column landed at position %d, want 1: the engine counted a column that is not the tenant's", position)
			}

			// A column the tenant cannot see cannot be renamed or dropped through it.
			if err := eng.DropColumn(ctx, "tenant-a", ds.ID, "stray"); err == nil {
				t.Error("DropColumn(a column of another tenant) = nil, want unknown column")
			}
			if got := countColumns(t, eng, ds.ID, `AND name = ?`, "stray"); got != 1 {
				t.Errorf("the stray column rows = %d, want it untouched", got)
			}
		})
	}
}
