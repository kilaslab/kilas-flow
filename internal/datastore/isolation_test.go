package datastore

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/kilaslab/kilas-flow/internal/database"
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
			// The neighbour's table outlives the purge by design, so the shared
			// PostgreSQL server is told to drop it at the end of the run.
			trackPhysical(t, db, neighbour.Table)
			// A catalogue row naming the neighbour's datastore under the purged
			// tenant. Reads already filter on the tenant, so nothing can reach
			// it; a purge that deleted columns by datastore id would leave it
			// behind for ever, which is why the delete is keyed on the tenant.
			if err := db.Exec(
				"INSERT INTO datastore_columns (tenant_id, datastore_id, name, type, position) VALUES (?, ?, ?, ?, ?)",
				"tenant-a", neighbour.ID, "smuggled", "string", 1,
			).Error; err != nil {
				t.Fatalf("seed a mis-attributed column row: %v", err)
			}
			// One of the tenant's tables is dropped by hand first. An
			// interrupted purge leaves exactly this, and a DROP without IF
			// EXISTS would make every retry fail on the table it already
			// removed.
			if err := db.Migrator().DropTable(inventory.Table); err != nil {
				t.Fatalf("drop inventory by hand: %v", err)
			}

			purged, err := eng.PurgeTenant(ctx, "tenant-a")
			if err != nil {
				t.Fatalf("PurgeTenant: %v", err)
			}
			if purged.Datastores != 2 || len(purged.Tables) != 2 {
				t.Errorf("PurgeTenant = %+v, want 2 datastores and 2 tables", purged)
			}
			if purged.Columns != 3 {
				t.Errorf("PurgeTenant = %+v, want the tenant's 3 catalogue columns", purged)
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
			var purgedColumns, keptColumns int64
			if err := db.Raw("SELECT COUNT(*) FROM datastore_columns WHERE tenant_id = ?", "tenant-a").Scan(&purgedColumns).Error; err != nil {
				t.Fatalf("count the purged tenant's columns: %v", err)
			}
			if err := db.Raw("SELECT COUNT(*) FROM datastore_columns WHERE tenant_id = ?", "tenant-b").Scan(&keptColumns).Error; err != nil {
				t.Fatalf("count the neighbour's columns: %v", err)
			}
			if purgedColumns != 0 || keptColumns != 1 {
				t.Errorf("catalogue columns after the purge = %d purged, %d kept, want 0 and 1", purgedColumns, keptColumns)
			}
			keptDefinition, err := eng.GetDatastore(ctx, "tenant-b", neighbour.ID)
			if err != nil || len(keptDefinition.Columns) != 1 {
				t.Errorf("GetDatastore(neighbour) = (%v, %v), want its one column", keptDefinition, err)
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
			if again, err := eng.PurgeTenant(ctx, "tenant-a"); err != nil || again.Datastores != 0 || again.Columns != 0 {
				t.Errorf("PurgeTenant again = (%+v, %v), want zero", again, err)
			}
			if _, err := eng.PurgeTenant(ctx, ""); err == nil {
				t.Error("PurgeTenant(\"\") = nil, want a refusal")
			}
		})
	}
}

// A datastore created while its own tenant is being purged keeps both its
// catalogue row and its physical table, or loses both. It must never lose the
// row and keep the table. The purge's catalogue read is what names the tables
// it drops, so a table the read never listed can be found by no later purge:
// the deletion reports success over cells that are still on disk, and every
// retry after it reports zeros.
//
// The interleave is deterministic rather than a create loop racing the purge.
// The hook fires when the purge composes its first DROP — the moment it has
// committed to the tables it read — and the create is committed by a second
// handle on the same database (the purge holds one connection for the whole
// of its transaction) before the purge runs another statement.
func TestPurgeTenantNeverOrphansATableCreatedWhileItRuns(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			ctx := context.Background()
			// A random tenant, so that rows an interrupted earlier run left
			// behind cannot be mistaken for this run's.
			tenant := "tenant-race-" + uuid.NewString()
			// The datastore tables present before this test. The PostgreSQL
			// server is shared, so a sweep for orphans has to be about the
			// tables this test created, and everything it creates appears
			// after this snapshot.
			before := physicalTables(t, db, "")

			owner, err := eng.Create(ctx, tenant, "owner", []ColumnInput{{Name: "title", Type: "string"}})
			if err != nil {
				t.Fatalf("Create owner: %v", err)
			}
			trackPhysical(t, db, owner.Table)

			rivalEngine, err := NewEngine(openHandle(t, drv.driver, drv.dsn(t), ""), "")
			if err != nil {
				t.Fatalf("NewEngine (second handle): %v", err)
			}

			var once sync.Once
			var rival *Datastore
			var rivalErr error
			eng.composeHook = func(string) {
				once.Do(func() {
					rival, rivalErr = rivalEngine.Create(ctx, tenant, "rival", []ColumnInput{{Name: "title", Type: "string"}})
				})
			}
			t.Cleanup(func() { eng.composeHook = nil })

			purged, purgeErr := eng.PurgeTenant(ctx, tenant)
			if rivalErr != nil || rival == nil {
				t.Fatalf("the concurrent Create did not commit: (%v, %v)", rival, rivalErr)
			}
			trackPhysical(t, db, rival.Table)

			// The invariant, whatever the purge managed to do with it. SQLite
			// can refuse the purge's own write once the create has committed
			// on another connection, because the snapshot it read has moved
			// under it; that failure rolls back rather than half-applying.
			assertNoOrphanTable(t, db, before)
			assertTenantCatalogueComplete(t, db, tenant)
			if purgeErr != nil {
				t.Logf("the purge refused to write while the create committed on the other connection (%v); its work rolled back and the repeat converges", purgeErr)
			}
			if purgeErr == nil {
				// The purge reports what it read. The rival was created after
				// that read, so it is the next call's work, not this one's:
				// it keeps its row and its table.
				if purged.Datastores != 1 || len(purged.Tables) != 1 {
					t.Errorf("PurgeTenant = %+v, want the one datastore it read", purged)
				}
				if !db.Migrator().HasTable(rival.Table) {
					t.Errorf("the purge dropped %q, a table it never read, while reporting %+v", rival.Table, purged)
				}
				if rows := countRows(t, db, "datastores", "surrogate", rival.Surrogate); rows != 1 {
					t.Errorf("datastores rows naming the rival's table = %d, want the 1 that names it", rows)
				}
			}

			// Repeating the deletion converges: the second call removes
			// exactly what the first did not reach.
			wantRepeat := 1
			if purgeErr != nil {
				wantRepeat = 2
			}
			again, err := eng.PurgeTenant(ctx, tenant)
			if err != nil {
				t.Fatalf("PurgeTenant (repeat): %v", err)
			}
			if again.Datastores != wantRepeat || len(again.Tables) != wantRepeat {
				t.Errorf("PurgeTenant (repeat) = %+v, want the %d datastores the first call left", again, wantRepeat)
			}
			assertNoOrphanTable(t, db, before)
			assertTenantCatalogueComplete(t, db, tenant)

			// And nothing this test created is left: no table, no catalogue
			// row, no column.
			if left := appearedDuring(physicalTables(t, db, ""), before); len(left) != 0 {
				t.Errorf("physical datastore tables left after the repeat: %v", left)
			}
			for _, table := range []string{"datastores", "datastore_columns"} {
				if rows := countRows(t, db, table, "tenant_id", tenant); rows != 0 {
					t.Errorf("%s rows for the purged tenant = %d, want 0", table, rows)
				}
			}
		})
	}
}

// physicalTables names the physical datastore tables the engine's runtime DDL
// created under one prefix, as opposed to the catalogue the migrations own.
func physicalTables(t *testing.T, db *database.DB, prefix string) map[string]struct{} {
	t.Helper()
	query := "SELECT name FROM sqlite_master WHERE type = 'table'"
	if db.Dialector.Name() == "postgres" {
		query = "SELECT tablename FROM pg_tables WHERE schemaname = current_schema()"
	}
	var names []string
	if err := db.Raw(query).Scan(&names).Error; err != nil {
		t.Fatalf("list the %s tables: %v", db.Dialector.Name(), err)
	}
	marker := PhysicalTableName(prefix, "")
	found := make(map[string]struct{}, len(names))
	for _, name := range names {
		if strings.HasPrefix(name, marker) {
			found[name] = struct{}{}
		}
	}
	return found
}

// appearedDuring lists the tables present now that were not present when
// before was taken, in name order.
func appearedDuring(now, before map[string]struct{}) []string {
	fresh := make([]string, 0, len(now))
	for name := range now {
		if _, known := before[name]; !known {
			fresh = append(fresh, name)
		}
	}
	sort.Strings(fresh)
	return fresh
}

// assertNoOrphanTable fails for every datastore table created since before
// that no datastores row anywhere names. That is the state no purge can reach:
// the catalogue read is how a purge finds the tables it drops, so a table with
// no row is a table with customer cells on disk that the next deletion reports
// as already gone.
func assertNoOrphanTable(t *testing.T, db *database.DB, before map[string]struct{}) {
	t.Helper()
	marker := PhysicalTableName("", "")
	for _, table := range appearedDuring(physicalTables(t, db, ""), before) {
		if rows := countRows(t, db, "datastores", "surrogate", strings.TrimPrefix(table, marker)); rows == 0 {
			t.Errorf("physical table %q has no datastores row naming it, so no purge can reach it", table)
		}
	}
}

// countRows counts the rows of one catalogue table matching one column value.
func countRows(t *testing.T, db *database.DB, table, column, value string) int64 {
	t.Helper()
	query := "SELECT COUNT(*) FROM " + quoteIdent(db.Dialector.Name(), table) +
		" WHERE " + quoteIdent(db.Dialector.Name(), column) + " = ?"
	var count int64
	if err := db.Raw(query, value).Scan(&count).Error; err != nil {
		t.Fatalf("count %s where %s = %q: %v", table, column, value, err)
	}
	return count
}

// assertTenantCatalogueComplete is the tenant-scoped half of the invariant: no
// catalogue row of this tenant names a table that is not there, and no
// catalogue column of this tenant names a datastore it does not have.
func assertTenantCatalogueComplete(t *testing.T, db *database.DB, tenantID string) {
	t.Helper()
	quote := func(name string) string { return quoteIdent(db.Dialector.Name(), name) }

	var rows []datastoreModel
	if err := db.Raw("SELECT * FROM "+quote("datastores")+" WHERE tenant_id = ?", tenantID).Scan(&rows).Error; err != nil {
		t.Fatalf("read the datastores catalogue: %v", err)
	}
	for _, row := range rows {
		if table := PhysicalTableName("", row.Surrogate); !db.Migrator().HasTable(table) {
			t.Errorf("catalogue row %s names physical table %q, which does not exist", row.ID, table)
		}
	}

	orphanQuery := "SELECT COUNT(*) FROM " + quote("datastore_columns") +
		" WHERE tenant_id = ? AND datastore_id NOT IN (SELECT id FROM " + quote("datastores") + " WHERE tenant_id = ?)"
	var orphans int64
	if err := db.Raw(orphanQuery, tenantID, tenantID).Scan(&orphans).Error; err != nil {
		t.Fatalf("count the datastore_columns rows that name no datastore of %s: %v", tenantID, err)
	}
	if orphans != 0 {
		t.Errorf("%d datastore_columns rows for %s name no datastore of that tenant", orphans, tenantID)
	}
}
