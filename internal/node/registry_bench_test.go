package node_test

import (
	"fmt"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// benchTenant is the tenant the tenant-view sub-benchmarks compile for. It sees
// the scoped type and not the hidden one, so one run covers both memberships.
const benchTenant = "acme"

// benchRegistry is what a busy deployment's compiler sees: every built-in plus
// a couple of hundred pack types. Lookup is called once per node of every
// document compiled, on the execution hot path, so the cost of a lookup is a
// property worth pinning rather than assuming.
func benchRegistry(b *testing.B) *node.Registry {
	b.Helper()
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		b.Fatalf("RegisterAll() error = %v", err)
	}
	for index := range 200 {
		definition := node.Definition{
			Type:        fmt.Sprintf("pack.bench.%d", index),
			Version:     workflow.V(1),
			DisplayName: "Bench",
			Category:    "Bench",
			Group:       []node.NodeGroup{node.GroupTransform},
			Inputs:      []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			ExecutorID:  "bench.exec",
		}
		if err := registry.RegisterFrom(node.SourcePack, definition); err != nil {
			b.Fatalf("RegisterFrom(%s) error = %v", definition.Type, err)
		}
	}
	// Two scoped types, so the view's scoped path is measured on the same
	// definitions the raw path is: one the tenant sees, one it does not.
	for _, scoped := range []struct {
		nodeType string
		tenants  []string
	}{
		{"pack.bench.scoped", []string{benchTenant}},
		{"pack.bench.hidden", []string{"globex"}},
	} {
		definition := node.Definition{
			Type:        scoped.nodeType,
			Version:     workflow.V(1),
			DisplayName: "Bench",
			Category:    "Bench",
			Group:       []node.NodeGroup{node.GroupTransform},
			Inputs:      []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			ExecutorID:  "bench.exec",
			VisibleTo:   scoped.tenants,
		}
		if err := registry.RegisterFrom(node.SourcePack, definition); err != nil {
			b.Fatalf("RegisterFrom(%s) error = %v", definition.Type, err)
		}
	}
	return registry
}

// BenchmarkRegistryLookup compares a raw registry lookup with the same lookup
// through a tenant view, one pair of sub-benchmarks per kind of node.
//
// Read the numbers from ONE run. allocs/op and B/op are deterministic and must
// be equal within each pair (raw/builtin with tenant/builtin, raw/pack with
// tenant/unscoped, raw/scoped with tenant/scoped-visible): the view adds one map
// lookup, never an allocation, and a refused lookup allocates nothing at all
// because it never resolves and clones the definition. ns/op should sit within
// noise of its partner. Wall-clock numbers taken minutes apart on a shared
// machine — about a dozen worktrees build at once — are not comparable, so
// before.txt is context for the raw sub-benchmarks only.
func BenchmarkRegistryLookup(b *testing.B) {
	registry := benchRegistry(b)
	version := workflow.V(1)
	view := registry.ForTenant(benchTenant)

	lookups := []struct {
		name     string
		registry *node.Registry
		nodeType string
		found    bool
	}{
		{"raw/builtin", registry, "kilasflow.set", true},
		{"raw/pack", registry, "pack.bench.150", true},
		{"raw/scoped", registry, "pack.bench.scoped", true},
		{"tenant/builtin", nil, "kilasflow.set", true},
		{"tenant/unscoped", nil, "pack.bench.150", true},
		{"tenant/scoped-visible", nil, "pack.bench.scoped", true},
		{"tenant/scoped-hidden", nil, "pack.bench.hidden", false},
	}
	for _, lookup := range lookups {
		b.Run(lookup.name, func(b *testing.B) {
			b.ReportAllocs()
			catalog := workflow.Catalog(registry)
			if lookup.registry == nil {
				catalog = view
			}
			for range b.N {
				if _, found := catalog.Lookup(lookup.nodeType, version); found != lookup.found {
					b.Fatalf("Lookup(%s) found = %v, want %v", lookup.nodeType, found, lookup.found)
				}
			}
		})
	}
}
