package tenantpurge_test

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/database"
)

// The completeness proof is schema-driven on purpose.
//
// A hand-written list of tables is a list somebody has to remember to extend,
// and the failure it produces is silent: a new tenant-scoped table keeps its
// rows, the purge reports success, and nobody learns until a customer's data
// turns up after they asked for it to be gone. FEAT-hj8pyx lands an
// idempotency_keys table with a tenant_id column in a parallel worktree, so
// this test is the mechanism that makes the merged result self-checking: it
// introspects the live schema, and every tenant_id table the migrations define
// must either be covered by a purge step or exempted with a reason.
func TestEveryTenantTableIsPurgedOrExplicitlyExempt(t *testing.T) {
	for _, drv := range purgeDialects(t) {
		t.Run(drv.name, func(t *testing.T) {
			db := drv.open(t, "")
			service := fakes(&recorder{}, stubRows{}).service(t)

			live := introspectTenantTables(t, db, "")
			if len(live) == 0 {
				t.Fatal("the schema has no tenant_id table at all: the introspection query has stopped working, and an empty set passes this test vacuously")
			}
			// The two columns stage 1 added are the reason a purge can reach a
			// delivery or a catalogue column at all, so their presence here is
			// part of the proof rather than a detail of it.
			for _, table := range []string{"webhook_deliveries", "datastore_columns"} {
				if !slices.Contains(live, table) {
					t.Errorf("%s is not among the introspected tenant tables, want it: the introspection or the migration is wrong", table)
				}
			}

			covered := map[string]string{}
			for _, step := range service.Steps() {
				for _, table := range step.Tables {
					covered[table] = step.Name
				}
			}
			exempt := service.Exempt()
			for _, table := range live {
				if _, ok := covered[table]; ok {
					continue
				}
				if reason, ok := exempt[table]; ok {
					if strings.TrimSpace(reason) == "" {
						t.Errorf("%s is exempted from the purge with an empty reason; an exemption is a claim that has to be readable", table)
					}
					continue
				}
				t.Errorf("the %s schema has a tenant_id table the purge does not cover: %s. Delete its rows in the step that owns them in internal/tenantpurge/purge.go, or add it to Exempt() with the reason it holds no tenant data", drv.name, table)
			}

			defined := migrationTables(t, drv.name)
			for table, reason := range exempt {
				if strings.TrimSpace(reason) == "" {
					t.Errorf("%s is exempted from the purge with an empty reason", table)
				}
				if !defined[table] {
					t.Errorf("the purge exempts %q, which no %s migration defines: an exemption has to name a real table", table, drv.name)
				}
			}
			for _, table := range service.Tables() {
				// SQLite carries no vector document tables: the embeddings and
				// vector store nodes refuse to run there, and 000006 ships the
				// catalogue alone.
				if drv.name == "sqlite" && strings.HasPrefix(table, "vector_documents_") {
					continue
				}
				if !defined[table] {
					t.Errorf("the purge covers %q, which no %s migration defines: a covered table has to name a real table", table, drv.name)
				}
			}
		})
	}
}

// The ticket's seeding test: two tenants across every table the purge covers,
// one purged, and the survivor proved intact by its values rather than only by
// its row counts.
//
// The proof shape is introspection-driven in both directions. Before the purge
// every introspected table must hold a row for each tenant — which is what stops
// the test passing because a seed forgot a table — and after it the purged
// tenant must have zero rows in every one of them while the survivor's counts
// are exactly what they were.
func TestPurgeRemovesOneTenantAndLeavesTheOtherIntact(t *testing.T) {
	for _, drv := range purgeDialects(t) {
		t.Run(drv.name, func(t *testing.T) {
			ctx := context.Background()
			db := drv.open(t, "")
			env := newPurgeEnv(t, db, "")
			service := env.service(nil)

			purgedID, keptID := randomTenantID("pt-a-"), randomTenantID("pt-b-")
			// The purge is its own cleanup: a crashed earlier run leaves the
			// same tenant id behind and would otherwise fail this run on a
			// leftover row nothing points at any more.
			for _, id := range []string{purgedID, keptID} {
				if _, err := service.Purge(ctx, id); err != nil {
					t.Fatalf("pre-purge of %s error = %v", id, err)
				}
			}

			purged := env.seedTenant(purgedID)
			kept := env.seedTenant(keptID)
			t.Cleanup(func() {
				if _, err := service.Purge(context.Background(), keptID); err != nil {
					t.Errorf("cleanup purge of %s error = %v", keptID, err)
				}
				for table, count := range tenantCounts(t, db, introspectTenantTables(t, db, ""), keptID) {
					if count != 0 {
						t.Errorf("cleanup left %d rows for %s in %s", count, keptID, table)
					}
				}
				dropLeftoverTables(t, db, kept)
			})

			tables := introspectTenantTables(t, db, "")
			t.Logf("seeded and asserting %d tenant_id tables: %v (vector documents on this server: %v)", len(tables), tables, env.vectorDocs)
			for _, table := range tables {
				for _, id := range []string{purgedID, keptID} {
					if count := rawTenantCount(t, db, table, id); count == 0 {
						t.Fatalf("%s holds no rows for %s before the purge: the seed does not cover every table the purge claims to, so the rest of this test would pass vacuously", table, id)
					}
				}
			}
			before := tenantCounts(t, db, tables, keptID)

			result, err := service.Purge(ctx, purgedID)
			if err != nil {
				t.Fatalf("Purge(%s) error = %v", purgedID, err)
			}

			for _, table := range tables {
				if count := rawTenantCount(t, db, table, purgedID); count != 0 {
					t.Errorf("%s still holds %d rows of the purged tenant %s", table, count, purgedID)
				}
				if count := rawTenantCount(t, db, table, keptID); count != before[table] {
					t.Errorf("%s holds %d rows of the surviving tenant, want the %d it had before the purge", table, count, before[table])
				}
			}
			if count := rawIDCount(t, db, "tenants", "id", purgedID); count != 0 {
				t.Errorf("tenants still holds the deleted tenant's row (%d rows)", count)
			}
			if count := rawIDCount(t, db, "tenants", "id", keptID); count != 1 {
				t.Errorf("the surviving tenant has %d tenant rows, want 1", count)
			}
			if leaked := cellRowCount(t, db, "execution_node_runs", purged.cell); leaked != 0 {
				t.Errorf("%d node-run rows anywhere still hold the purged tenant's cell value %q", leaked, purged.cell)
			}

			// The survivor is intact by value, not only by count: its datastore
			// cell, the cell in its trace and its payload bytes read back.
			row, err := env.engine.Get(ctx, keptID, kept.dsID, kept.dsRowID)
			if err != nil {
				t.Fatalf("Get(surviving datastore row) error = %v", err)
			}
			if row["cell"] != kept.cell {
				t.Errorf("the surviving datastore cell = %v, want %q", row["cell"], kept.cell)
			}
			if got := nodeRunOutput(t, db, keptID); !strings.Contains(got, kept.cell) {
				t.Errorf("the surviving trace output = %q, want it to hold %q", got, kept.cell)
			}
			body, _, err := env.files.Get(kept.binaryScope, kept.binaryID)
			if err != nil {
				t.Fatalf("Get(surviving payload) error = %v", err)
			}
			payload, err := io.ReadAll(body)
			_ = body.Close()
			if err != nil || string(payload) != kept.binaryBody {
				t.Errorf("the surviving payload = (%q, %v), want %q", payload, err, kept.binaryBody)
			}
			// The purged tenant's own artefacts are gone: the payload
			// directory, the physical datastore table, and the in-process
			// session that is not in any table at all.
			if _, _, err := env.files.Get(purged.binaryScope, purged.binaryID); err == nil {
				t.Error("the purged tenant's payload is still readable through the store")
			}
			if db.Migrator().HasTable(purged.dsTable) {
				t.Errorf("the purged tenant's physical table %s survived", purged.dsTable)
			}
			if messages, err := env.memory.Load(ctx, purged.session); err != nil || len(messages) != 0 {
				t.Errorf("Load(purged session) = (%v, %v), want it forgotten", messages, err)
			}
			if messages, err := env.memory.Load(ctx, kept.session); err != nil || len(messages) != 1 {
				t.Errorf("Load(surviving session) = (%v, %v), want its one turn", messages, err)
			}

			if result.TenantID != purgedID || result.Removed["tenants"] != 1 {
				t.Errorf("Purge() = {tenant %q, tenants %d}, want {%q, 1}", result.TenantID, result.Removed["tenants"], purgedID)
			}
			if result.DatastoreTables != 1 {
				t.Errorf("DatastoreTables = %d, want the 1 table the tenant owned", result.DatastoreTables)
			}
			if result.Binaries.Executions != 1 || result.Binaries.Files != 1 || result.Binaries.Bytes != int64(len(purged.binaryBody)) {
				t.Errorf("Binaries = %+v, want 1 execution, 1 file and %d bytes", result.Binaries, len(purged.binaryBody))
			}

			// A retry converges to nothing at all, and reports every covered
			// table as zero rather than reporting only the ones it touched.
			again, err := service.Purge(ctx, purgedID)
			if err != nil {
				t.Fatalf("Purge(retry) error = %v", err)
			}
			if len(again.Removed) != len(service.Tables()) {
				t.Errorf("the retry reported %d tables, want every covered table (%d)", len(again.Removed), len(service.Tables()))
			}
			for table, count := range again.Removed {
				if count != 0 {
					t.Errorf("the retry removed %d rows from %s, want 0", count, table)
				}
			}
			unknown, err := service.Purge(ctx, randomTenantID("pt-unknown-"))
			if err != nil {
				t.Fatalf("Purge(unknown tenant) error = %v", err)
			}
			for table, count := range unknown.Removed {
				if count != 0 {
					t.Errorf("purging an unknown tenant removed %d rows from %s, want 0", count, table)
				}
			}
		})
	}
}

// The prefix is a deployment decision: a host application sharing the database
// renames every KilasFlow table. The purge names most of its tables through
// GORM, but the vector tables and the prefix-guarded deletes are composed from
// strings, so they are the ones that can silently address the wrong table.
func TestPurgeHonoursTheTablePrefix(t *testing.T) {
	const prefix = "kflow_"
	db := openSQLite(t, prefix)
	env := newPurgeEnv(t, db, prefix)
	service := env.service(nil)
	ctx := context.Background()

	tenantID := randomTenantID("pt-prefix-")
	if _, err := service.Purge(ctx, tenantID); err != nil {
		t.Fatalf("pre-purge error = %v", err)
	}
	seed := env.seedTenant(tenantID)
	t.Cleanup(func() {
		if _, err := service.Purge(context.Background(), tenantID); err != nil {
			t.Errorf("cleanup purge error = %v", err)
		}
		dropLeftoverTables(t, db, seed)
	})

	result, err := service.Purge(ctx, tenantID)
	if err != nil {
		t.Fatalf("Purge() error = %v", err)
	}
	for table, count := range tenantCounts(t, db, introspectTenantTables(t, db, prefix), tenantID) {
		if count != 0 {
			t.Errorf("%s still holds %d rows of the purged tenant", table, count)
		}
	}
	// The vector catalogue is deleted by a table name composed from a string
	// rather than through a model, so its count is what proves the prefix was
	// applied to the name the purge actually deleted from.
	if result.Removed["vector_collections"] != 1 {
		t.Errorf("Removed[vector_collections] = %d, want the 1 prefixed catalogue row the purge deleted by name", result.Removed["vector_collections"])
	}
	if result.Removed["datastore_columns"] != 1 {
		t.Errorf("Removed[datastore_columns] = %d, want 1", result.Removed["datastore_columns"])
	}
	if db.Migrator().HasTable(seed.dsTable) {
		t.Errorf("the purged tenant's physical table %s survived", seed.dsTable)
	}
}

// refusingStopper is the fake that makes the stop-triggers ordering
// observable: at the moment it is called it looks at the database and records
// what it finds there.
type refusingStopper struct {
	t       *testing.T
	db      *database.DB
	prefix  string
	seen    []string
	missing []string
}

func (s *refusingStopper) Deactivated(_ context.Context, tenantID, workflowID string, _ map[string]string) {
	s.seen = append(s.seen, workflowID)
	for _, table := range []string{"webhook_bindings", "credentials"} {
		if rawTenantCount(s.t, s.db, s.prefix+table, tenantID) == 0 {
			s.missing = append(s.missing, table)
		}
	}
}

// Stopping the tenant's triggers only works while the rows that describe them
// are still there: webhook.Coordinator.Deactivated reads the tenant's bindings
// to know which remote registrations to undo, and the lifecycle hooks it calls
// resolve the tenant's credentials (a Telegram poller needs its bot token to
// call deleteWebhook). A purge that deleted either first would leave remote
// services delivering into a route that no longer exists.
func TestPurgeStopsTriggersBeforeTheirRowsAndCredentialsAreDeleted(t *testing.T) {
	for _, drv := range purgeDialects(t) {
		t.Run(drv.name, func(t *testing.T) {
			db := drv.open(t, "")
			env := newPurgeEnv(t, db, "")
			tenantID := randomTenantID("pt-stop-")
			if _, err := env.service(nil).Purge(context.Background(), tenantID); err != nil {
				t.Fatalf("pre-purge error = %v", err)
			}
			seed := env.seedTenant(tenantID)

			stopper := &refusingStopper{t: t, db: db}
			service := env.service(stopper)
			t.Cleanup(func() {
				if _, err := env.service(nil).Purge(context.Background(), tenantID); err != nil {
					t.Errorf("cleanup purge error = %v", err)
				}
				dropLeftoverTables(t, db, seed)
			})

			if _, err := service.Purge(context.Background(), tenantID); err != nil {
				t.Fatalf("Purge() error = %v", err)
			}

			if want := []string{seed.workflowID}; !slices.Equal(stopper.seen, want) {
				t.Errorf("the purge stopped triggers for %v, want exactly the tenant's active workflows %v", stopper.seen, want)
			}
			if len(stopper.missing) != 0 {
				t.Errorf("the purge had already deleted %v when it stopped the tenant's triggers; the bindings and the credentials the lifecycle resolves have to outlive that call", stopper.missing)
			}
			for _, table := range []string{"webhook_bindings", "credentials", "workflow_versions"} {
				if count := rawTenantCount(t, db, table, tenantID); count != 0 {
					t.Errorf("%s still holds %d rows after the purge", table, count)
				}
			}
		})
	}
}

// nodeRunOutput reads the surviving tenant's traced output back, decoded in Go
// so the assertion works on a blob column in both dialects.
func nodeRunOutput(t *testing.T, db *database.DB, tenantID string) string {
	t.Helper()
	rows, err := db.Raw("SELECT output FROM execution_node_runs WHERE tenant_id = ?", tenantID).Rows()
	if err != nil {
		t.Fatalf("read the trace of %s: %v", tenantID, err)
	}
	defer func() { _ = rows.Close() }()
	var joined strings.Builder
	for rows.Next() {
		var output []byte
		if err := rows.Scan(&output); err != nil {
			t.Fatalf("scan the trace of %s: %v", tenantID, err)
		}
		joined.Write(output)
		joined.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the trace of %s: %v", tenantID, err)
	}
	return joined.String()
}
