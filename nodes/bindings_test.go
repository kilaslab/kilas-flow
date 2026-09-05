package nodes_test

import (
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/ai"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/webhook"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// composition builds both registries exactly as cmd/kilasflow/main.go does.
//
// Building them the same way is the whole point: a test that assembled a
// different catalogue would prove something about the test rather than about
// what the server actually runs.
func composition(t *testing.T) (*node.Registry, *engine.Registry) {
	t.Helper()
	catalogue := node.NewRegistry()
	if err := nodes.RegisterAll(catalogue); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	return catalogue, executors
}

// TestEveryDefinitionsExecutorIsRegistered is the defect that already existed.
//
// A definition naming an executor nobody registered fails at *run* time, on the
// first item to reach that node, in whatever workflow a customer happens to be
// running. Nothing checked it, so adding a node and forgetting its executor was
// a mistake the compiler could not see and no test would catch.
func TestEveryDefinitionsExecutorIsRegistered(t *testing.T) {
	catalogue, executors := composition(t)

	for _, definition := range catalogue.List() {
		if definition.ExecutorID == "" {
			// validateDefinition already refuses this; asserted here so the two
			// checks cannot drift apart silently.
			t.Errorf("%s version %s declares no executor", definition.Type, definition.Version)
			continue
		}
		if _, found := executors.Lookup(definition.ExecutorID); !found {
			t.Errorf("%s version %s names executor %q, which is not registered; this node would fail on its first item",
				definition.Type, definition.Version, definition.ExecutorID)
		}
	}
}

// TestEveryBuiltinExecutorIsReferenced is the reverse, and it holds only for
// built-ins.
//
// An executor nobody points at is dead code — a node renamed without its
// executor being renamed, or removed without its executor being removed. The
// same assertion is deliberately *not* made for packs: a pack may expose one
// executor under several definitions, and a sidecar may register a generic
// executor before its definitions arrive, so the invariant genuinely does not
// hold there. Weakening it to a warning nobody reads would be worse than
// scoping it to where it is true.
func TestEveryBuiltinExecutorIsReferenced(t *testing.T) {
	catalogue, executors := composition(t)

	referenced := map[string]bool{}
	for _, definition := range catalogue.List() {
		referenced[definition.ExecutorID] = true
	}
	for _, executorID := range executors.Registered() {
		if !referenced[executorID] {
			t.Errorf("executor %q is registered but no node definition names it; it is dead code", executorID)
		}
	}
}

// TestRegistrationOrderIsDeterministic is what makes the catalogue reproducible.
//
// Two runs of the same binary must produce the same catalogue. One that
// depended on map iteration or a directory listing would change between runs
// for no reason anyone could see, and a diff of the node-types response would
// be noise rather than signal.
func TestRegistrationOrderIsDeterministic(t *testing.T) {
	snapshot := func() []string {
		catalogue := node.NewRegistry()
		if err := nodes.RegisterAll(catalogue); err != nil {
			t.Fatalf("RegisterAll() error = %v", err)
		}
		listed := catalogue.List()
		order := make([]string, 0, len(listed))
		for _, definition := range listed {
			order = append(order, definition.Type+"@"+definition.Version.String())
		}
		return order
	}

	first := snapshot()
	for range 5 {
		again := snapshot()
		if len(again) != len(first) {
			t.Fatalf("catalogue size changed between runs: %d then %d", len(first), len(again))
		}
		for index := range first {
			if again[index] != first[index] {
				t.Fatalf("catalogue order changed between runs at %d: %q then %q", index, first[index], again[index])
			}
		}
	}
}

// TestEveryDeclaredLifecycleIsBound covers the other binding a definition can
// name.
//
// It is the same class of defect as a missing executor: a trigger declaring a
// hook nobody registered activates and silently never registers with its remote
// service, which is worse than failing, because the workflow looks live.
func TestEveryDeclaredLifecycleIsBound(t *testing.T) {
	catalogue, _ := composition(t)
	lifecycles := webhook.NewLifecycleRegistry()
	// The same function composition calls, so this asserts that the catalogue
	// and the hooks agree rather than that a test agrees with itself.
	if err := nodes.RegisterLifecycles(lifecycles, nil); err != nil {
		t.Fatalf("RegisterLifecycles() error = %v", err)
	}
	if err := webhook.VerifyLifecycleBindings(catalogue.LifecycleIDs(), lifecycles); err != nil {
		t.Errorf("a node declares a webhook lifecycle that composition does not register: %v", err)
	}
}
