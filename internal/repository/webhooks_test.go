package repository_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// pathLabelTrigger is one webhook node a fixture document binds: a node, a
// method, and the path label the author gave it.
type pathLabelTrigger struct {
	nodeID string
	method string
	path   string
}

// pathLabelDocument is a document whose nodes are all webhook triggers, each
// carrying the path and method the extractor below reads back.
func pathLabelDocument(id, name string, triggers ...pathLabelTrigger) workflow.Document {
	nodes := make([]workflow.Node, 0, len(triggers))
	for _, trigger := range triggers {
		nodes = append(nodes, workflow.Node{
			ID: trigger.nodeID, Name: trigger.nodeID, Type: "kilasflow.webhook",
			TypeVersion: workflow.V(1), Position: workflow.Position{},
			Parameters: map[string]any{"path": trigger.path, "httpMethod": trigger.method},
		})
	}
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            id,
		Name:          name,
		Nodes:         nodes,
		Connections:   []workflow.Connection{},
		Settings:      map[string]any{},
	}
}

// pathLabelTriggers stands in for internal/webhook.Extract, so these tests
// exercise activation without depending on the node packages.
func pathLabelTriggers(document workflow.Document) []repository.WebhookTrigger {
	triggers := make([]repository.WebhookTrigger, 0, len(document.Nodes))
	for _, node := range document.Nodes {
		if node.Type != "kilasflow.webhook" {
			continue
		}
		path, _ := node.Parameters["path"].(string)
		method, _ := node.Parameters["httpMethod"].(string)
		triggers = append(triggers, repository.WebhookTrigger{
			NodeID: node.ID, NodeType: node.Type, Method: method, Path: path,
		})
	}
	return triggers
}

func pathLabelCatalog() activationCatalog {
	return activationCatalog{
		"kilasflow.webhook": {
			Type: "kilasflow.webhook", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
	}
}

// newPathLabelStore boots a migrated SQLite database, so the indexes under test
// are the ones the migrations create rather than the ones GORM would guess.
func newPathLabelStore(t *testing.T) (*database.DB, *repository.GORMWorkflowStore) {
	t.Helper()
	db := newHistoryDB(t)
	return db, repository.NewWorkflowStore(db.DB).WithWebhooks(pathLabelTriggers)
}

// activatePathLabelWorkflow saves and activates one fixture document, failing
// the test if activation is refused.
func activatePathLabelWorkflow(t *testing.T, store *repository.GORMWorkflowStore, workflowID string, document workflow.Document) {
	t.Helper()
	ctx := context.Background()
	tenant := repository.TenantScope{ID: "tenant-a"}
	if _, err := store.SaveDraft(ctx, tenant, document); err != nil {
		t.Fatalf("SaveDraft(%s) error = %v", workflowID, err)
	}
	if _, err := store.Activate(ctx, tenant, workflowID, pathLabelCatalog()); err != nil {
		t.Fatalf("Activate(%s) error = %v", workflowID, err)
	}
}

// pathLabelRouteFor returns the route one node of a workflow answers on.
func pathLabelRouteFor(t *testing.T, store *repository.GORMWorkflowStore, workflowID, nodeID string) repository.WebhookBinding {
	t.Helper()
	bindings, err := store.WebhookRoutes(context.Background(), repository.TenantScope{ID: "tenant-a"}, workflowID)
	if err != nil {
		t.Fatalf("WebhookRoutes(%s) error = %v", workflowID, err)
	}
	for _, binding := range bindings {
		if binding.NodeID == nodeID {
			return binding
		}
	}
	t.Fatalf("workflow %s has no binding for node %s; bindings = %#v", workflowID, nodeID, bindings)
	return repository.WebhookBinding{}
}

// One n8n document may bind several methods on one path — a REST endpoint's
// GET and POST are separate Webhook nodes — and every one of them has to
// survive activation. The path is a label; the per-node route is the identity.
func TestOnePathMayBindSeveralMethods(t *testing.T) {
	t.Parallel()

	_, store := newPathLabelStore(t)
	document := pathLabelDocument("wf_rest", "wtp-items pair",
		pathLabelTrigger{nodeID: "get", method: "GET", path: "wtp-items"},
		pathLabelTrigger{nodeID: "post", method: "POST", path: "wtp-items"},
	)
	activatePathLabelWorkflow(t, store, document.ID, document)

	bindings, err := store.WebhookRoutes(context.Background(), repository.TenantScope{ID: "tenant-a"}, document.ID)
	if err != nil {
		t.Fatalf("WebhookRoutes() error = %v", err)
	}
	if len(bindings) != 2 {
		t.Fatalf("bindings after activating GET and POST on one path = %d (%#v), want 2", len(bindings), bindings)
	}
	if bindings[0].Route == bindings[1].Route {
		t.Errorf("two nodes share route %q; routes are per node", bindings[0].Route)
	}
	// Both endpoints must actually answer: a binding row that exists but does
	// not resolve is the same outage as a refused activation.
	for _, binding := range bindings {
		resolved, err := store.Resolve(context.Background(), binding.Method, binding.Route)
		if err != nil {
			t.Errorf("Resolve(%s, %s) error = %v", binding.Method, binding.Route, err)
			continue
		}
		if resolved.NodeID != binding.NodeID {
			t.Errorf("Resolve(%s, %s) resolved node %q, want %q", binding.Method, binding.Route, resolved.NodeID, binding.NodeID)
		}
		if resolved.Path != "wtp-items" {
			t.Errorf("Resolve(%s, %s) path = %q, want %q", binding.Method, binding.Route, resolved.Path, "wtp-items")
		}
	}
}

// Importing the same webhook workflow twice gives two workflows with two
// routes, and the path label they share is not a claim on anything. Both keep
// answering, including after one of them is activated again.
func TestTwoWorkflowsMayShareOnePathLabel(t *testing.T) {
	t.Parallel()

	_, store := newPathLabelStore(t)
	first := pathLabelDocument("wf_first", "Imported template",
		pathLabelTrigger{nodeID: "hook", method: "POST", path: "waha"})
	second := pathLabelDocument("wf_second", "Imported template again",
		pathLabelTrigger{nodeID: "hook", method: "POST", path: "waha"})

	activatePathLabelWorkflow(t, store, first.ID, first)
	activatePathLabelWorkflow(t, store, second.ID, second)

	firstBinding := pathLabelRouteFor(t, store, first.ID, "hook")
	secondBinding := pathLabelRouteFor(t, store, second.ID, "hook")
	if firstBinding.Route == secondBinding.Route {
		t.Fatalf("two workflows share route %q", firstBinding.Route)
	}

	// Re-activating one of them replaces its own binding only: the other
	// workflow's endpoint must keep answering.
	activatePathLabelWorkflow(t, store, first.ID, first)
	if got := pathLabelRouteFor(t, store, first.ID, "hook").Route; got != firstBinding.Route {
		t.Errorf("route after reactivation = %q, want the reused %q", got, firstBinding.Route)
	}
	resolved, err := store.Resolve(context.Background(), "POST", secondBinding.Route)
	if err != nil {
		t.Fatalf("Resolve() on the other workflow's route error = %v", err)
	}
	if resolved.WorkflowID != second.ID {
		t.Errorf("route %q resolved workflow %q, want %q", secondBinding.Route, resolved.WorkflowID, second.ID)
	}
}

// The one collision that remains is a route already bound to another
// workflow. It has to reach the HTTP boundary as a conflict the caller can act
// on — errors.Is for the sentinel, errors.As for the workflow to name — rather
// than a generic server fault.
func TestClaimedRouteIsReportedAsAConflict(t *testing.T) {
	t.Parallel()

	db, store := newPathLabelStore(t)
	ctx := context.Background()
	tenant := repository.TenantScope{ID: "tenant-a"}

	// The owner's row and the route the claimant will be handed, written
	// directly: a random 16-byte route cannot be made to collide on purpose.
	now := time.Now().UTC()
	if err := db.Exec(
		"INSERT INTO webhook_bindings (tenant_id, workflow_id, workflow_version_id, node_id, node_type, method, route, path, parameters, created_at)"+
			" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		tenant.ID, "wf_owner", "v_owner", "owner-node", "kilasflow.webhook", "POST", "route-claimed", "waha", []byte(`{}`), now,
	).Error; err != nil {
		t.Fatalf("seed the claiming binding: %v", err)
	}
	if err := db.Exec(
		"INSERT INTO webhook_routes (tenant_id, workflow_id, node_id, route, created_at) VALUES (?, ?, ?, ?, ?)",
		tenant.ID, "wf_claimant", "hook", "route-claimed", now,
	).Error; err != nil {
		t.Fatalf("seed the claiming route: %v", err)
	}

	claimant := pathLabelDocument("wf_claimant", "Would take the route",
		pathLabelTrigger{nodeID: "hook", method: "POST", path: "waha"})
	if _, err := store.SaveDraft(ctx, tenant, claimant); err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	_, err := store.Activate(ctx, tenant, claimant.ID, pathLabelCatalog())
	if err == nil {
		t.Fatal("Activate() on a claimed route = nil, want a conflict")
	}
	if !errors.Is(err, repository.ErrWebhookPathClaimed) {
		t.Errorf("Activate() error = %v, want it to wrap ErrWebhookPathClaimed", err)
	}
	var conflict *repository.WebhookConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Activate() error = %v, want *WebhookConflictError", err)
	}
	if conflict.WorkflowID != "wf_owner" {
		t.Errorf("conflict names workflow %q, want %q", conflict.WorkflowID, "wf_owner")
	}
	if conflict.Path != "waha" || conflict.Method != "POST" || conflict.Route != "route-claimed" {
		t.Errorf("conflict = %#v, want the refused POST waha on route-claimed", conflict)
	}

	// The failed activation rolls back whole: the claimant owns no binding and
	// the owner's endpoint still answers.
	bindings, err := store.WebhookRoutes(ctx, tenant, claimant.ID)
	if err != nil {
		t.Fatalf("WebhookRoutes(claimant) error = %v", err)
	}
	if len(bindings) != 0 {
		t.Errorf("refused activation left %#v behind", bindings)
	}
	resolved, err := store.Resolve(ctx, "POST", "route-claimed")
	if err != nil {
		t.Fatalf("Resolve(owner route) error = %v", err)
	}
	if resolved.WorkflowID != "wf_owner" {
		t.Errorf("route-claimed resolved workflow %q, want wf_owner", resolved.WorkflowID)
	}
}

// ResolveRoute answers a request that names a route but not the method it will
// use, which is what a preflight is. A route bound by several methods has to
// come back whole. It answers on the minted route alone: a row with no route is
// not routable, and its path — a label, not an address — is never a key, even
// when a caller names a route that happens to spell one.
//
// This replaces a test that also pinned the opposite for rows minted before
// routes existed ("a row with no minted route answers on its path"). That
// fallback is what let one tenant's label reach another tenant's workflow, so
// the clause is gone and its inverse is asserted below.
func TestResolveRouteReturnsEveryMethodOnOneRoute(t *testing.T) {
	t.Parallel()

	const route = "0123456789abcdef0123456789abcdef"
	db, store := newPathLabelStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	insert := func(nodeID, method, route, path string) {
		t.Helper()
		if err := db.Exec(
			"INSERT INTO webhook_bindings (tenant_id, workflow_id, workflow_version_id, node_id, node_type, method, route, path, parameters, created_at)"+
				" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			"tenant-a", "wf_routed", "v_routed", nodeID, "kilasflow.webhook", method, route, path, []byte(`{}`), now,
		).Error; err != nil {
			t.Fatalf("seed the %s binding: %v", method, err)
		}
	}
	// One node bound on two methods shares the one route the node was minted:
	// that is what a REST endpoint's GET and POST are.
	for _, method := range []string{"GET", "POST"} {
		insert("n1", method, route, "items")
	}
	// A row with no minted route whose label spells the route above. It must
	// not answer, whatever it is called.
	insert("n-unrouted", "PUT", "", route)

	bindings, err := store.ResolveRoute(ctx, route)
	if err != nil {
		t.Fatalf("ResolveRoute() error = %v", err)
	}
	if len(bindings) != 2 {
		t.Fatalf("ResolveRoute(%s) returned %d bindings (%#v), want 2", route, len(bindings), bindings)
	}
	if bindings[0].Method != "GET" || bindings[1].Method != "POST" {
		t.Errorf("methods = [%s %s], want [GET POST] in order", bindings[0].Method, bindings[1].Method)
	}
	for _, binding := range bindings {
		if binding.Route != route {
			t.Errorf("binding route = %q, want %q", binding.Route, route)
		}
		if binding.NodeID != "n1" {
			t.Errorf("binding node = %q, want the routed node n1 and not the unrouted row", binding.NodeID)
		}
	}
	// The method-specific lookup still picks exactly one of them.
	for _, method := range []string{"GET", "POST"} {
		resolved, err := store.Resolve(ctx, method, route)
		if err != nil {
			t.Fatalf("Resolve(%s, %s) error = %v", method, route, err)
		}
		if resolved.Method != method {
			t.Errorf("Resolve(%s, %s) method = %q", method, route, resolved.Method)
		}
	}
	// The unrouted row is not reachable by the route its label spells.
	if _, err := store.Resolve(ctx, "PUT", route); !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("Resolve(PUT, %s) error = %v, want ErrNotFound for a row that has no route", route, err)
	}

	if _, err := store.ResolveRoute(ctx, "no-such-route"); !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("ResolveRoute(unknown) error = %v, want ErrNotFound", err)
	}
	if _, err := store.ResolveRoute(ctx, ""); !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("ResolveRoute(empty) error = %v, want ErrNotFound", err)
	}
}

// seedResolveBinding inserts one binding by SQL, so a test can hold a state the
// activation path never produces — a row with no route, or one route on two
// tenants — and ask what the lookups make of it.
func seedResolveBinding(t *testing.T, db *database.DB, tenant, workflowID, nodeID, method, route, path string) {
	t.Helper()
	if err := db.Exec(
		"INSERT INTO webhook_bindings (tenant_id, workflow_id, workflow_version_id, node_id, node_type, method, route, path, parameters, created_at)"+
			" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		tenant, workflowID, "v-"+workflowID, nodeID, "kilasflow.webhook", method, route, path, []byte(`{}`), time.Now().UTC(),
	).Error; err != nil {
		t.Fatalf("seed the %s binding for %s: %v", method, tenant, err)
	}
}

// resolveTenantPrefix marks the tenants the Resolve tests seed. The drv- prefix
// keeps them with the other driver-parity fixtures, and the rest is this
// file's own so a sweep here cannot delete another test's rows.
const resolveTenantPrefix = "drv-t9j-"

// cleanResolveBindings removes what the Resolve tests seed, now and when the
// test ends. Both, because the (method, route) index is global: on a shared
// PostgreSQL a row left by a run that crashed would make the next run's seed
// fail on the index rather than on anything about routing.
func cleanResolveBindings(t *testing.T, db *database.DB) {
	t.Helper()
	remove := func() error {
		return db.Exec("DELETE FROM webhook_bindings WHERE tenant_id LIKE ?", resolveTenantPrefix+"%").Error
	}
	if err := remove(); err != nil {
		t.Fatalf("clear earlier Resolve fixtures: %v", err)
	}
	t.Cleanup(func() {
		if err := remove(); err != nil {
			t.Errorf("remove the Resolve fixtures: %v", err)
		}
	})
}

// A path is a label, not an identity: migration 000011 made it non-unique, so
// two tenants can hold the same one. A request that names only a label must
// therefore match nothing — it used to match a row that had no minted route, and
// with no tenant in the URL that ran whichever tenant's workflow the row
// belonged to.
//
// No t.Parallel: on PostgreSQL the rows are shared with every other test in the
// database, and they are cleaned by this test alone.
func TestResolveNeverMatchesByPathLabel(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		cleanResolveBindings(t, db)
		store := repository.NewWorkflowStore(db.DB)
		ctx := context.Background()

		const minted = "r-b-minted-t9j"
		// Tenant A has a row with no route — what a database predating routes
		// holds — and tenant B has a routed row under the very same label.
		seedResolveBinding(t, db, resolveTenantPrefix+"a", "wf-a", "n1", "POST", "", "items")
		seedResolveBinding(t, db, resolveTenantPrefix+"b", "wf-b", "n1", "GET", minted, "items")

		// Each lookup answers with the tenant of the row it found, so a failure
		// says whose workflow a label reached.
		one := func(binding repository.WebhookBinding, err error) (string, error) { return binding.TenantID, err }
		for name, lookup := range map[string]func() (string, error){
			"Resolve(POST, label)": func() (string, error) { return one(store.Resolve(ctx, "POST", "items")) },
			"Resolve(GET, label)":  func() (string, error) { return one(store.Resolve(ctx, "GET", "items")) },
			"ResolveRoute(label)": func() (string, error) {
				bindings, err := store.ResolveRoute(ctx, "items")
				if len(bindings) > 0 {
					return bindings[0].TenantID, err
				}
				return "", err
			},
			// An empty route equals the column of every unrouted row, so a
			// lookup for nothing would answer with a row.
			"Resolve(POST, empty)": func() (string, error) { return one(store.Resolve(ctx, "POST", "")) },
			"ResolveRoute(empty)": func() (string, error) {
				bindings, err := store.ResolveRoute(ctx, "")
				if len(bindings) > 0 {
					return bindings[0].TenantID, err
				}
				return "", err
			},
		} {
			if tenant, err := lookup(); !errors.Is(err, repository.ErrNotFound) {
				t.Errorf("%s = tenant %q, error %v; want ErrNotFound: a label is not an address", name, tenant, err)
			}
		}

		resolved, err := store.Resolve(ctx, "GET", minted)
		if err != nil {
			t.Fatalf("Resolve(GET, minted route) error = %v", err)
		}
		if resolved.TenantID != resolveTenantPrefix+"b" || resolved.WorkflowID != "wf-b" || resolved.Route != minted {
			t.Errorf("Resolve(GET, minted route) = %#v, want tenant B's routed binding", resolved)
		}
	})
}

// One route names one node in one tenant: webhook_routes is unique on the route
// and on (tenant, workflow, node), and activation draws every binding's route
// from it. The bindings table cannot say so itself — its index is (method,
// route), because one node binds several methods on one route — so a route held
// by two tenants can only be hand-edited data. ResolveRoute is the lookup that
// would return both, one per method, and hand a preflight the answer of
// whichever came first, so it refuses.
//
// The guard checks that the bindings agree with each other. It does not consult
// webhook_routes, and it does not need to: two owners is already wrong.
func TestResolveRouteRefusesARouteBoundToTwoTenants(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		cleanResolveBindings(t, db)
		store := repository.NewWorkflowStore(db.DB)
		ctx := context.Background()

		const route = "r-shared-t9j"
		seedResolveBinding(t, db, resolveTenantPrefix+"a", "wf-a", "n1", "POST", route, "orders")
		seedResolveBinding(t, db, resolveTenantPrefix+"b", "wf-b", "n1", "GET", route, "orders")

		bindings, err := store.ResolveRoute(ctx, route)
		if !errors.Is(err, repository.ErrNotFound) {
			t.Fatalf("ResolveRoute(shared route) = %#v, %v; want ErrNotFound for a route with two owners", bindings, err)
		}
		if len(bindings) != 0 {
			t.Errorf("ResolveRoute(shared route) returned %#v alongside the refusal, want none", bindings)
		}

		// A request that names its method is still answered by the one row the
		// (method, route) index leaves for it.
		resolved, err := store.Resolve(ctx, "POST", route)
		if err != nil {
			t.Fatalf("Resolve(POST, shared route) error = %v", err)
		}
		if resolved.TenantID != resolveTenantPrefix+"a" {
			t.Errorf("Resolve(POST, shared route) tenant = %q, want %q", resolved.TenantID, resolveTenantPrefix+"a")
		}
	})
}
