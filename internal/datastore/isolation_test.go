package datastore

import (
	"context"
	"testing"
	"time"
)

// A caller holding another tenant's datastore id — guessed, leaked or
// enumerated — learns nothing and touches nothing. Every entry point
// resolves the physical table through the catalogue under the caller's
// tenant, so a foreign id fails exactly like an absent one: the same
// "unknown datastore" refusal, never a "forbidden" that confirms existence.
func TestCrossTenantAccessRefused(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			_, eng := drv.open(t, "")
			ctx := context.Background()
			victim, err := eng.Create(ctx, "tenant-a", "secrets", []ColumnInput{
				{Name: "api_key", Type: "string"},
				{Name: "score", Type: "number"},
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			inserted, err := eng.Insert(ctx, "tenant-a", victim.ID, map[string]any{
				"api_key": "live-secret",
				"score":   41.0,
			})
			if err != nil {
				t.Fatalf("Insert: %v", err)
			}
			id := inserted["id"].(int64)
			filter := &Filter{Type: "and", Conditions: []FilterCondition{
				{Column: "api_key", Condition: CondEq, Value: "live-secret"},
			}}

			attacker, foreign := "tenant-b", victim.ID
			refusals := map[string]error{}
			_, err = eng.GetDatastore(ctx, attacker, foreign)
			refusals["GetDatastore"] = err
			_, err = eng.Get(ctx, attacker, foreign, id)
			refusals["Get"] = err
			_, err = eng.List(ctx, attacker, foreign, RowQuery{})
			refusals["List"] = err
			_, err = eng.Insert(ctx, attacker, foreign, map[string]any{"api_key": "x"})
			refusals["Insert"] = err
			_, err = eng.Update(ctx, attacker, foreign, filter, map[string]any{"score": 1.0}, false)
			refusals["Update"] = err
			_, err = eng.Delete(ctx, attacker, foreign, filter, false)
			refusals["Delete"] = err
			_, err = eng.Upsert(ctx, attacker, foreign, filter, map[string]any{"score": 1.0}, false)
			refusals["Upsert"] = err
			_, err = eng.Clear(ctx, attacker, foreign)
			refusals["Clear"] = err
			_, err = eng.Usage(ctx, attacker, foreign)
			refusals["Usage"] = err
			_, err = eng.Increment(ctx, attacker, foreign, filter, "score", 1)
			refusals["Increment"] = err
			stamp := inserted["updatedAt"].(time.Time)
			_, err = eng.UpdateWithPrecondition(ctx, attacker, foreign, filter, map[string]any{"score": 1.0}, stamp)
			refusals["UpdateWithPrecondition"] = err
			_, err = eng.DeleteWithPrecondition(ctx, attacker, foreign, filter, stamp)
			refusals["DeleteWithPrecondition"] = err
			err = eng.AddColumn(ctx, attacker, foreign, ColumnInput{Name: "extra", Type: "string"})
			refusals["AddColumn"] = err

			// Absent and foreign refuse alike: the messages share their shape,
			// so one cannot be told from the other.
			_, absentErr := eng.Get(ctx, attacker, foreign, 999999)
			_, missingErr := eng.Get(ctx, attacker, "datastore_00000000-0000-7000-8000-000000000000", id)
			if !IsUnknown(absentErr) || !IsUnknown(missingErr) {
				t.Fatalf("absent = %v, missing = %v, want both unknown", absentErr, missingErr)
			}

			// A foreign drop converges to nothing instead of failing — and
			// changes nothing. The victim's table still exists afterwards.
			if err := eng.Drop(ctx, attacker, foreign); err != nil {
				t.Errorf("Drop with a foreign id = %v, want the convergent nil", err)
			}
			if exists, err := eng.TableExists(ctx, "tenant-a", foreign); err != nil || !exists {
				t.Errorf("TableExists(owner) = (%v, %v), want (true, nil)", exists, err)
			}
			if exists, err := eng.TableExists(ctx, attacker, foreign); err != nil || exists {
				t.Errorf("TableExists(attacker) = (%v, %v), want (false, nil)", exists, err)
			}

			// The victim's row survived every refused call byte-identical.
			got, err := eng.Get(ctx, "tenant-a", foreign, id)
			if err != nil {
				t.Fatalf("Get(owner) after refused calls: %v", err)
			}
			if got["api_key"] != "live-secret" || got["score"] != 41.0 {
				t.Errorf("Get(owner) = %v, want the untouched row", got)
			}
		})
	}
}

// No entry point accepts a physical table name: ids resolve through the
// catalogue only, so naming the table directly — or smuggling SQL where an
// id goes — is the same unknown-datastore refusal.
func TestCallerSuppliedTableNamesRefused(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			ctx := context.Background()
			ds, err := eng.Create(ctx, "tenant-1", "contacts", []ColumnInput{{Name: "title", Type: "string"}})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			for _, supplied := range []string{
				ds.Table,
				ds.Table + `"`,
				ds.Table + `"; DROP TABLE "` + ds.Table,
				"",
				"datastores",
			} {
				if _, err := eng.GetDatastore(ctx, "tenant-1", supplied); !IsUnknown(err) {
					t.Errorf("GetDatastore(%q) = %v, want the unknown-datastore refusal", supplied, err)
				}
				if _, err := eng.List(ctx, "tenant-1", supplied, RowQuery{}); !IsUnknown(err) {
					t.Errorf("List(%q) = %v, want the unknown-datastore refusal", supplied, err)
				}
			}
			// The table the injection named is still there with its definition.
			if !db.Migrator().HasTable(ds.Table) {
				t.Errorf("physical table %q is gone after refused names", ds.Table)
			}
			stored, err := eng.GetDatastore(ctx, "tenant-1", ds.ID)
			if err != nil || len(stored.Columns) != 1 {
				t.Errorf("GetDatastore(id) = (%v, %v), want the intact definition", stored, err)
			}
		})
	}
}

// Purging a tenant drops its physical tables and removes its catalogue rows
// while a neighbour tenant keeps serving. The trace needs no scrubbing on
// top: it never held cells (see trace.go), so dropping the tables drops the
// last copy of every value.
func TestPurgeTenantDropsTablesAndKeepsNeighbours(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			ctx := context.Background()
			orders, err := eng.Create(ctx, "tenant-a", "orders", []ColumnInput{{Name: "api_key", Type: "string"}})
			if err != nil {
				t.Fatalf("Create orders: %v", err)
			}
			inventory, err := eng.Create(ctx, "tenant-a", "inventory", []ColumnInput{{Name: "title", Type: "string"}})
			if err != nil {
				t.Fatalf("Create inventory: %v", err)
			}
			if _, err := eng.Insert(ctx, "tenant-a", orders.ID, map[string]any{"api_key": "order-secret"}); err != nil {
				t.Fatalf("Insert orders: %v", err)
			}
			neighbour, err := eng.Create(ctx, "tenant-b", "private", []ColumnInput{{Name: "title", Type: "string"}})
			if err != nil {
				t.Fatalf("Create neighbour: %v", err)
			}
			neighbourRow, err := eng.Insert(ctx, "tenant-b", neighbour.ID, map[string]any{"title": "keep me"})
			if err != nil {
				t.Fatalf("Insert neighbour: %v", err)
			}

			purged, err := eng.PurgeTenant(ctx, "tenant-a")
			if err != nil {
				t.Fatalf("PurgeTenant: %v", err)
			}
			if purged.Datastores != 2 || len(purged.Tables) != 2 {
				t.Errorf("PurgeTenant = %+v, want 2 datastores and 2 tables", purged)
			}
			for _, table := range purged.Tables {
				if db.Migrator().HasTable(table) {
					t.Errorf("physical table %q survives the purge", table)
				}
			}
			for _, id := range []string{orders.ID, inventory.ID} {
				if _, err := eng.GetDatastore(ctx, "tenant-a", id); !IsUnknown(err) {
					t.Errorf("GetDatastore(purged %q) = %v, want unknown", id, err)
				}
			}
			if listed, err := eng.ListDatastores(ctx, "tenant-a"); err != nil || len(listed) != 0 {
				t.Errorf("ListDatastores(purged tenant) = (%v, %v), want empty", listed, err)
			}

			// The neighbour never noticed: definition, table and row intact.
			kept, err := eng.Get(ctx, "tenant-b", neighbour.ID, neighbourRow["id"].(int64))
			if err != nil {
				t.Fatalf("Get(neighbour) after purge: %v", err)
			}
			if kept["title"] != "keep me" {
				t.Errorf("Get(neighbour) = %v, want the intact row", kept)
			}

			// A retried purge converges to zero, and an empty tenant id is
			// refused rather than matched against nothing.
			if again, err := eng.PurgeTenant(ctx, "tenant-a"); err != nil || again.Datastores != 0 {
				t.Errorf("PurgeTenant again = (%+v, %v), want zero", again, err)
			}
			if _, err := eng.PurgeTenant(ctx, ""); err == nil {
				t.Error("PurgeTenant(\"\") = nil, want a refusal")
			}
		})
	}
}
