package database

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"testing"
	"testing/fstest"
	"time"

	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/migrations"
)

// backfillMigration is looked up by its name and never by its number: the number
// is assigned when the branch is landed and moves if another migration takes it,
// so nothing here may depend on it.
const backfillMigration = "webhook_route_backfill"

// mintedRoute is the shape mintWebhookRoute produces — sixteen random bytes,
// hex-encoded — which is the shape a backfilled route has to share so that a
// route cannot be told apart, or told to be second-class, by its origin.
var mintedRoute = regexp.MustCompile(`^[0-9a-f]{32}$`)

// backfillSeed is one webhook binding a test writes into a schema that predates
// the backfill.
type backfillSeed struct {
	tenant, workflow, node, method, route, path string
}

// seedBindings writes bindings by SQL. It goes around the store on purpose: the
// state under test is one the store can no longer produce, since every
// activation mints a route.
func seedBindings(t *testing.T, db *DB, seeds ...backfillSeed) {
	t.Helper()
	table := quoteIdentifier(db.Dialector.Name(), tablePrefix(db)+"webhook_bindings")
	for _, seed := range seeds {
		err := db.Exec(
			"INSERT INTO "+table+" (tenant_id, workflow_id, workflow_version_id, node_id, node_type, method, route, path, parameters, created_at)"+
				" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			seed.tenant, seed.workflow, "v-"+seed.workflow, seed.node, "kilasflow.webhook",
			seed.method, seed.route, seed.path, []byte(`{}`), time.Now().UTC(),
		).Error
		if err != nil {
			t.Fatalf("seed %+v: %v", seed, err)
		}
	}
}

// seedMintedRoute records a route in webhook_routes the way an import does,
// before any activation writes a binding.
func seedMintedRoute(t *testing.T, db *DB, tenant, workflowID, node, route string) {
	t.Helper()
	table := quoteIdentifier(db.Dialector.Name(), tablePrefix(db)+"webhook_routes")
	if err := db.Exec(
		"INSERT INTO "+table+" (tenant_id, workflow_id, node_id, route, created_at) VALUES (?, ?, ?, ?, ?)",
		tenant, workflowID, node, route, time.Now().UTC(),
	).Error; err != nil {
		t.Fatalf("seed the minted route for %s/%s/%s: %v", tenant, workflowID, node, err)
	}
}

// bindingRow is one stored binding, read back whole so a test can say that a
// migration changed the route column and nothing else.
type bindingRow struct {
	ID         uint
	TenantID   string
	WorkflowID string
	NodeID     string
	Method     string
	Route      string
	Path       string
	Parameters []byte
	CreatedAt  time.Time
}

func (row bindingRow) sameExceptRoute(other bindingRow) bool {
	return row.ID == other.ID && row.TenantID == other.TenantID && row.WorkflowID == other.WorkflowID &&
		row.NodeID == other.NodeID && row.Method == other.Method && row.Path == other.Path &&
		bytes.Equal(row.Parameters, other.Parameters) && row.CreatedAt.Equal(other.CreatedAt)
}

func readBindings(t *testing.T, db *DB) []bindingRow {
	t.Helper()
	table := quoteIdentifier(db.Dialector.Name(), tablePrefix(db)+"webhook_bindings")
	var rows []bindingRow
	if err := db.Raw(
		"SELECT id, tenant_id, workflow_id, node_id, method, route, path, parameters, created_at FROM " + table + " ORDER BY id",
	).Scan(&rows).Error; err != nil {
		t.Fatalf("read the bindings back: %v", err)
	}
	return rows
}

// nodeKey names one trigger node across tenants.
type nodeKey struct{ tenant, workflow, node string }

// routeRow is one webhook_routes row.
type routeRow struct {
	TenantID   string
	WorkflowID string
	NodeID     string
	Route      string
}

// readRoutes reads webhook_routes, and fails on a node that holds two: the
// table is unique on (tenant, workflow, node), so two rows would mean the
// backfill minted a second route for a node that already had one.
func readRoutes(t *testing.T, db *DB) map[nodeKey]string {
	t.Helper()
	table := quoteIdentifier(db.Dialector.Name(), tablePrefix(db)+"webhook_routes")
	var rows []routeRow
	if err := db.Raw("SELECT tenant_id, workflow_id, node_id, route FROM " + table + " ORDER BY id").Scan(&rows).Error; err != nil {
		t.Fatalf("read webhook_routes: %v", err)
	}
	routes := map[nodeKey]string{}
	for _, row := range rows {
		key := nodeKey{row.TenantID, row.WorkflowID, row.NodeID}
		if _, dup := routes[key]; dup {
			t.Errorf("webhook_routes holds more than one row for %+v", key)
		}
		routes[key] = row.Route
	}
	return routes
}

// rerunBackfill executes the backfill's own statements again, which is what a
// boot that died between the statements and the version row would do. It has to
// change nothing.
func rerunBackfill(t *testing.T, db *DB) {
	t.Helper()
	all, err := loadMigrations(migrations.FS, db.Dialector.Name())
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	prefix, names := tablePrefix(db), definedIdentifiers(all)
	for _, one := range all {
		if one.name != backfillMigration {
			continue
		}
		for _, statement := range one.up {
			if err := db.Exec(prefixStatement(statement, prefix, names)).Error; err != nil {
				t.Fatalf("run the backfill a second time: %v", err)
			}
		}
		return
	}
	t.Fatalf("no migration named %s", backfillMigration)
}

// belowBackfill is the migration set as it stood one step before the backfill:
// every migration of the dialect whose version is lower than the backfill's.
//
// It exists because the frozen baseline still carries the unique index on
// (tenant_id, path) that a later migration drops, so a node bound on two methods
// under one path — legal today, and the case the backfill has to share a route
// across — cannot be written into a schema that has only the baseline. Migrating
// to just below the backfill, seeding, and migrating on gives a schema that
// accepts the seed and still has the backfill to run.
func belowBackfill(t *testing.T, dialect string) fstest.MapFS {
	t.Helper()
	all, err := loadMigrations(migrations.FS, dialect)
	if err != nil {
		t.Fatalf("loadMigrations(%s): %v", dialect, err)
	}
	var backfill int64
	for _, one := range all {
		if one.name == backfillMigration {
			backfill = one.version
		}
	}
	if backfill == 0 {
		t.Fatalf("the %s migrations carry no migration named %s", dialect, backfillMigration)
	}
	entries, err := fs.ReadDir(migrations.FS, dialect)
	if err != nil {
		t.Fatalf("read the %s migrations: %v", dialect, err)
	}
	below := fstest.MapFS{}
	for _, entry := range entries {
		version, _, _, err := parseMigrationName(entry.Name())
		if err != nil {
			t.Fatalf("%s/%s: %v", dialect, entry.Name(), err)
		}
		if version >= backfill {
			continue
		}
		file := path.Join(dialect, entry.Name())
		body, err := fs.ReadFile(migrations.FS, file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		below[file] = &fstest.MapFile{Data: body}
	}
	return below
}

// assertWebhookRouteBackfill adopts a schema that predates routes exactly as an
// upgrade does — the baseline tables exist, no version history — holding
// bindings with no route, and migrates it.
func assertWebhookRouteBackfill(t *testing.T, db *DB) {
	t.Helper()
	const keptRoute = "deadbeefdeadbeefdeadbeefdeadbeef"
	const importedRoute = "11111111111111111111111111111111"

	buildLegacySchema(t, db)
	// The frozen baseline is unique on (tenant_id, path) as well as on
	// (method, route), and (method, route) is unique with an empty route too, so
	// each empty-route row here has its own method, and no tenant repeats a path.
	seedBindings(t, db,
		// The same label in two tenants: the collision the fallback made
		// dangerous.
		backfillSeed{"tenant-a", "wf-a", "n1", "POST", "", "shared"},
		backfillSeed{"tenant-b", "wf-b", "n1", "GET", "", "shared"},
		// A node an import already gave a route, before it was ever activated.
		backfillSeed{"tenant-a", "wf-a", "n2", "PUT", "", "other"},
		// A binding that already has its route, which nothing may touch.
		backfillSeed{"tenant-a", "wf-c", "n1", "POST", keptRoute, "kept"},
	)
	seedMintedRoute(t, db, "tenant-a", "wf-a", "n2", importedRoute)
	seedMintedRoute(t, db, "tenant-a", "wf-c", "n1", keptRoute)

	before := readBindings(t, db)
	if len(before) != 4 {
		t.Fatalf("seeded %d bindings, want 4", len(before))
	}
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate over a schema whose bindings have no route: %v", err)
	}
	after := readBindings(t, db)
	routes := readRoutes(t, db)

	if len(after) != len(before) {
		t.Fatalf("the backfill left %d bindings, want the same %d", len(after), len(before))
	}
	backfilled := map[string]bool{}
	for index := range before {
		was, now := before[index], after[index]
		if !was.sameExceptRoute(now) {
			t.Errorf("the backfill changed more than the route: %+v -> %+v", was, now)
		}
		if now.Route == "" {
			t.Errorf("binding %d (%s/%s/%s) still has no route", now.ID, now.TenantID, now.WorkflowID, now.NodeID)
		}
		if was.Route != "" {
			// A row that already had a route is untouched.
			if now.Route != was.Route {
				t.Errorf("a binding that had route %q was changed to %q", was.Route, now.Route)
			}
			continue
		}
		if !mintedRoute.MatchString(now.Route) {
			t.Errorf("backfilled route %q is not in the minted format (32 lowercase hex characters)", now.Route)
		}
		if backfilled[now.Route] {
			t.Errorf("route %q was given to more than one node", now.Route)
		}
		backfilled[now.Route] = true
		// Whatever a binding's route is, webhook_routes has to name the same
		// one for its node: mintWebhookRoute reads that table, so a mismatch
		// changes the address the next time the workflow is reactivated.
		if recorded := routes[nodeKey{now.TenantID, now.WorkflowID, now.NodeID}]; recorded != now.Route {
			t.Errorf("binding for %s/%s/%s carries route %q but webhook_routes records %q",
				now.TenantID, now.WorkflowID, now.NodeID, now.Route, recorded)
		}
	}
	if len(backfilled) != 3 {
		t.Errorf("%d routes were backfilled, want 3", len(backfilled))
	}

	// The route an import recorded is reused, not replaced by a second one.
	for _, row := range after {
		if row.WorkflowID == "wf-a" && row.NodeID == "n2" && row.Route != importedRoute {
			t.Errorf("wf-a n2 route = %q, want the route webhook_routes already held (%q)", row.Route, importedRoute)
		}
	}
	// One route per node among the seeds, and nothing else in the table.
	wantNodes := []nodeKey{
		{"tenant-a", "wf-a", "n1"}, {"tenant-b", "wf-b", "n1"}, {"tenant-a", "wf-a", "n2"}, {"tenant-a", "wf-c", "n1"},
	}
	if len(routes) != len(wantNodes) {
		t.Errorf("webhook_routes holds %d rows (%v), want one per node: %d", len(routes), routes, len(wantNodes))
	}
	for _, key := range wantNodes {
		if _, ok := routes[key]; !ok {
			t.Errorf("webhook_routes has no row for %+v", key)
		}
	}

	// The label survives: it is what the editor shows, and it stays non-unique.
	sharedBy := map[string]bool{}
	for _, row := range after {
		if row.Path == "shared" {
			sharedBy[row.TenantID] = true
		}
	}
	if !sharedBy["tenant-a"] || !sharedBy["tenant-b"] {
		t.Errorf("the label %q is held by %v after the backfill, want both tenants", "shared", sharedBy)
	}

	// A second boot is a no-op, and so is running the migration's own SQL again.
	appliedBefore, err := appliedVersions(db)
	if err != nil {
		t.Fatalf("appliedVersions: %v", err)
	}
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	rerunBackfill(t, db)
	appliedAfter, err := appliedVersions(db)
	if err != nil {
		t.Fatalf("appliedVersions: %v", err)
	}
	if fmt.Sprint(appliedBefore) != fmt.Sprint(appliedAfter) {
		t.Errorf("applied versions changed on the second boot: %v -> %v", appliedBefore, appliedAfter)
	}
	again := readBindings(t, db)
	for index := range after {
		if after[index].Route != again[index].Route || !after[index].sameExceptRoute(again[index]) {
			t.Errorf("running the backfill again changed a binding: %+v -> %+v", after[index], again[index])
		}
	}
	if fmt.Sprint(readRoutes(t, db)) != fmt.Sprint(routes) {
		t.Errorf("running the backfill again changed webhook_routes")
	}

	assertBackfilledRowsRouteThroughTheStore(t, db, after)
}

// assertBackfilledRowsRouteThroughTheStore drives the real repository over the
// migrated rows: they answer on their minted route and no longer on their label,
// and activating the workflow again would mint the route it was just given.
func assertBackfilledRowsRouteThroughTheStore(t *testing.T, db *DB, migrated []bindingRow) {
	t.Helper()
	ctx := context.Background()
	store := repository.NewWorkflowStore(db.DB).WithWebhooks(func(workflow.Document) []repository.WebhookTrigger {
		return []repository.WebhookTrigger{{NodeID: "n1", NodeType: "kilasflow.webhook", Method: "POST", Path: "shared"}}
	})

	var route string
	for _, row := range migrated {
		if row.TenantID == "tenant-a" && row.WorkflowID == "wf-a" && row.NodeID == "n1" {
			route = row.Route
		}
	}
	if route == "" {
		t.Fatal("the tenant-a wf-a n1 binding was not found after the migration")
	}

	resolved, err := store.Resolve(ctx, "POST", route)
	if err != nil {
		t.Fatalf("Resolve(POST, the backfilled route) error = %v", err)
	}
	if resolved.TenantID != "tenant-a" || resolved.WorkflowID != "wf-a" || resolved.Route != route {
		t.Errorf("Resolve(POST, the backfilled route) = %+v, want tenant-a's wf-a binding", resolved)
	}

	for _, method := range []string{"POST", "GET"} {
		if got, err := store.Resolve(ctx, method, "shared"); !errors.Is(err, repository.ErrNotFound) {
			t.Errorf("Resolve(%s, the label) = %+v, %v; want ErrNotFound", method, got, err)
		}
	}
	if got, err := store.ResolveRoute(ctx, "shared"); !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("ResolveRoute(the label) = %+v, %v; want ErrNotFound", got, err)
	}

	ensured, err := store.EnsureWebhookRoutes(ctx, repository.TenantScope{ID: "tenant-a"}, "wf-a", workflow.Document{})
	if err != nil {
		t.Fatalf("EnsureWebhookRoutes error = %v", err)
	}
	if len(ensured) != 1 || ensured[0].Route != route {
		t.Errorf("EnsureWebhookRoutes = %+v, want the migrated route %q: the address must survive a reactivation", ensured, route)
	}
}

// TestBackfillGivesEveryEmptyRouteBindingAMintedRouteOnSQLite is the pre-route
// schema proof: rows with no route, in a schema an older build created, come out
// of an ordinary boot with a minted route each.
func TestBackfillGivesEveryEmptyRouteBindingAMintedRouteOnSQLite(t *testing.T) {
	assertWebhookRouteBackfill(t, freshSQLite(t))
}

func TestBackfillGivesEveryEmptyRouteBindingAMintedRouteOnPostgres(t *testing.T) {
	assertWebhookRouteBackfill(t, openPostgres(t))
}

// assertOneRouteAcrossANodesMethods migrates to just below the backfill, seeds
// one node bound on two methods under one path with no route, and migrates on.
// webhook.Extract emits one trigger per method of a node, so a node bound on
// GET and POST is two bindings on one route: the backfill must give them the
// same route, and record one row for the node, not one per binding.
func assertOneRouteAcrossANodesMethods(t *testing.T, db *DB) {
	t.Helper()
	if err := migrateFS(db, belowBackfill(t, db.Dialector.Name()), discardLogger()); err != nil {
		t.Fatalf("migrate to the schema just below the backfill: %v", err)
	}
	seedBindings(t, db,
		backfillSeed{"tenant-a", "wf-d", "n1", "DELETE", "", "multi"},
		backfillSeed{"tenant-a", "wf-d", "n1", "PATCH", "", "multi"},
	)
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate onto the backfill: %v", err)
	}

	rows := readBindings(t, db)
	if len(rows) != 2 {
		t.Fatalf("%d bindings after the backfill, want 2", len(rows))
	}
	if !mintedRoute.MatchString(rows[0].Route) {
		t.Errorf("route %q is not in the minted format", rows[0].Route)
	}
	if rows[0].Route != rows[1].Route {
		t.Errorf("the node's two methods got routes %q and %q, want one shared route", rows[0].Route, rows[1].Route)
	}
	routes := readRoutes(t, db)
	if len(routes) != 1 || routes[nodeKey{"tenant-a", "wf-d", "n1"}] != rows[0].Route {
		t.Errorf("webhook_routes = %v, want exactly one row for the node, carrying %q", routes, rows[0].Route)
	}
}

func TestBackfillSharesOneRouteAcrossANodesMethodsOnSQLite(t *testing.T) {
	assertOneRouteAcrossANodesMethods(t, freshSQLite(t))
}

func TestBackfillSharesOneRouteAcrossANodesMethodsOnPostgres(t *testing.T) {
	assertOneRouteAcrossANodesMethods(t, openPostgres(t))
}

// The backfill names its tables in backticks so the runner's prefix rewrite finds
// them. A statement that spelled one bare would run against the wrong table, or
// none, on an install that uses database.table_prefix.
func TestBackfillHonoursTheTablePrefixOnSQLite(t *testing.T) {
	db := openPrefixedSQLite(t, filepath.Join(t.TempDir(), "prefixed.db"), "kf_")
	if err := migrateFS(db, belowBackfill(t, "sqlite"), discardLogger()); err != nil {
		t.Fatalf("migrate to the schema just below the backfill: %v", err)
	}
	seedBindings(t, db,
		backfillSeed{"tenant-a", "wf-d", "n1", "DELETE", "", "multi"},
		backfillSeed{"tenant-a", "wf-d", "n1", "PATCH", "", "multi"},
	)
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate onto the backfill under a table prefix: %v", err)
	}

	rows := readBindings(t, db)
	if len(rows) != 2 {
		t.Fatalf("%d kf_webhook_bindings rows after the backfill, want 2", len(rows))
	}
	for _, row := range rows {
		if !mintedRoute.MatchString(row.Route) {
			t.Errorf("kf_webhook_bindings route = %q, want a minted route", row.Route)
		}
	}
	routes := readRoutes(t, db)
	if len(routes) != 1 || routes[nodeKey{"tenant-a", "wf-d", "n1"}] != rows[0].Route {
		t.Errorf("kf_webhook_routes = %v, want the node's route %q", routes, rows[0].Route)
	}
	// No unprefixed table appeared beside the prefixed ones.
	for _, bare := range []string{"webhook_bindings", "webhook_routes"} {
		if db.Migrator().HasTable(bare) {
			t.Errorf("the backfill created or touched an unprefixed %q table", bare)
		}
	}
}

// The mid-schema helper is only as good as its cut: if it kept the backfill it
// would seed into a schema that has already been backfilled and every test above
// would pass for the wrong reason.
func TestBelowBackfillStopsBeforeTheBackfill(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		below := belowBackfill(t, dialect)
		all, err := loadMigrations(below, dialect)
		if err != nil {
			t.Fatalf("loadMigrations(%s): %v", dialect, err)
		}
		if len(all) == 0 {
			t.Fatalf("%s: nothing precedes the backfill", dialect)
		}
		for _, one := range all {
			if one.name == backfillMigration {
				t.Errorf("%s: belowBackfill kept the backfill itself", dialect)
			}
		}
	}
}
