package wasmpack

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/kilaslab/kilas-flow/internal/runcode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Registry holds the module packs this process may run.
//
// It is the one place a module becomes reachable, and it audits on the way in:
// adding a spec compiles it once through the deployment's shared translation
// cache and refuses anything whose imports do not match the capabilities its
// manifest declared. That is deliberate — a pack that would fail at run time
// fails at load time instead, where the operator can read the reason and the
// workflow never sees a node that cannot work.
type Registry struct {
	mu      sync.RWMutex
	modules *runcode.ModuleCache
	specs   map[string]Spec
}

// NewRegistry builds a registry over one deployment's translation cache.
//
// A nil cache is allowed and means every audit and every first run translates
// again, which is correct and slower.
func NewRegistry(modules *runcode.ModuleCache) *Registry {
	return &Registry{modules: modules, specs: map[string]Spec{}}
}

// key is how a spec is indexed: a node type at one version.
func key(nodeType string, version workflow.TypeVersion) string {
	return nodeType + "@" + version.String()
}

// Add audits one module pack and makes it reachable.
//
// The audit is the same check the Code node's module goes through: the module
// is compiled, its imports are read, and every host function it reaches must be
// one its capabilities grant. A duplicate {type, version} is refused rather
// than replaced, because two packs claiming one node type is an installation
// mistake an operator has to see.
func (registry *Registry) Add(ctx context.Context, spec Spec) error {
	if err := spec.validate(); err != nil {
		return err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, duplicate := registry.specs[key(spec.Type, spec.Version)]; duplicate {
		return fmt.Errorf("node type %q version %d is already registered by another module pack", spec.Type, spec.Version)
	}
	if _, err := Audit(ctx, registry.modules, spec.Module, spec.Caps, spec.Limits); err != nil {
		return fmt.Errorf("module pack %q: %w", spec.Type, err)
	}
	registry.specs[key(spec.Type, spec.Version)] = spec
	return nil
}

// Lookup returns the spec for one node type at one version.
func (registry *Registry) Lookup(nodeType string, version workflow.TypeVersion) (Spec, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	spec, ok := registry.specs[key(nodeType, version)]
	return spec, ok
}

// Len is how many module packs are registered.
func (registry *Registry) Len() int {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return len(registry.specs)
}

// Types lists the registered node types in a stable order, which is what a
// boot log and a test both want.
func (registry *Registry) Types() []string {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	types := make([]string, 0, len(registry.specs))
	for nodeKey := range registry.specs {
		types = append(types, nodeKey)
	}
	sort.Strings(types)
	return types
}
