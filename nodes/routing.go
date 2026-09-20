package nodes

import (
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/routing"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

// RoutingExecutorID is the binding a declarative node pack names instead of
// shipping Go. It is re-exported here so a pack registers against the same
// constant the executor is installed under.
const RoutingExecutorID = routing.ExecutorID

// RegisterRoutingExecutor installs the declarative routing interpreter.
//
// It is registered separately from RegisterExecutors because the nodes it runs
// do not come from this repository: a pack supplies both a definition and a
// routing description, and pairing them is the composition root's job, not this
// package's. Registering it here would also mean every test that wanted a plain
// HTTP node had to supply a pack registry it has no use for.
func RegisterRoutingExecutor(registry *engine.Registry, policy safehttp.Policy, routes *routing.Registry, catalog routing.Catalog) error {
	return registry.Register(routing.ExecutorID, routing.NewExecutor(policy, routes, catalog))
}
