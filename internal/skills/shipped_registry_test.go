package skills_test

import (
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/loadoptions"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/nodepack"
	"github.com/kilaslab/kilas-flow/internal/routing"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/webhook"
	"github.com/kilaslab/kilas-flow/nodes"
	"github.com/kilaslab/kilas-flow/packs/telegram"
	"github.com/kilaslab/kilas-flow/packs/waha"
)

// registerShipped registers every node a default install serves into one
// registry, in the calls run() makes: the compiled nodes, the vector nodes
// (which RegisterAll does not carry) and the two embedded packs.
//
// The drift gates and the example harness both need the whole catalogue — an AI
// skill declares kilasflow.vectorStore, and the Telegram skill example uses a
// pack node — so a registry that stopped at nodes.RegisterAll would call both
// "the product does not have it".
//
// TODO: use internal/shippedcatalogue.Register(registry) once it is on main;
// it is the same list, owned by the docs-facts work, and this copy is the one
// place the skills tests would otherwise duplicate run()'s registration calls.
func registerShipped(t testing.TB, registry *node.Registry) {
	t.Helper()

	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("register the built-in nodes: %v", err)
	}
	if err := nodes.RegisterVectorNodes(registry, ""); err != nil {
		t.Fatalf("register the vector nodes: %v", err)
	}

	routes := routing.NewRegistry()
	executors := engine.NewRegistry()
	options := loadoptions.NewResolver(safehttp.DefaultPolicy(), 0)
	packTriggers := nodepack.NewTriggerRegistry()

	// A pack refuses to register against an executor the server has not
	// installed, so the two it binds to are installed first, as run() does.
	if err := nodes.RegisterRoutingExecutor(executors, safehttp.DefaultPolicy(), routes, registry); err != nil {
		t.Fatalf("register the routing executor: %v", err)
	}
	if err := executors.Register(nodepack.TriggerExecutorID,
		nodepack.NewTriggerExecutor(packTriggers, safehttp.DefaultPolicy())); err != nil {
		t.Fatalf("register the pack trigger executor: %v", err)
	}
	if err := telegram.Register(registry, routes, executors, options); err != nil {
		t.Fatalf("register the Telegram pack: %v", err)
	}
	if err := waha.Register(waha.Deps{
		Definitions: registry, Routes: routes, Triggers: packTriggers,
		Deliveries: webhook.NewRegistry(), Lifecycles: webhook.NewLifecycleRegistry(),
		Executors: executors, Options: options,
	}); err != nil {
		t.Fatalf("register the WAHA pack: %v", err)
	}
}
