package loadoptions

import (
	"context"
	"fmt"
	"sync"

	"github.com/kilaslabs/kilas-flow/internal/property"
)

// SchemaLoader answers a resource mapper's column list.
//
// A sibling of InternalLoader rather than a widening of it. The option result
// is fixed at {label, value} by its own committed contract, and a column needs
// a type, a required flag and match eligibility — none of which fit there.
// Widening it into a discriminated union is precisely the shape this codebase
// rejected for a property's Options, on the grounds that the generated
// TypeScript client cannot express it as anything better than `unknown`.
//
// Internal only, for now, and deliberately: an HTTP schema source would need a
// response shape nothing yet describes.
type SchemaLoader func(ctx context.Context, scope Scope) (property.MapperSchema, error)

// schemaRegistry holds the registered schema loaders.
type schemaRegistry struct {
	mu      sync.RWMutex
	loaders map[string]SchemaLoader
}

// RegisterSchema binds a schema loader by name.
func (resolver *Resolver) RegisterSchema(name string, loader SchemaLoader) error {
	if resolver == nil || name == "" || loader == nil {
		return fmt.Errorf("schema loader name and implementation are required")
	}
	resolver.schemas.mu.Lock()
	defer resolver.schemas.mu.Unlock()
	if resolver.schemas.loaders == nil {
		resolver.schemas.loaders = map[string]SchemaLoader{}
	}
	if _, exists := resolver.schemas.loaders[name]; exists {
		return fmt.Errorf("schema loader %q is already registered", name)
	}
	resolver.schemas.loaders[name] = loader
	return nil
}

// LoadSchema resolves one resource mapper's columns.
//
// Uncached, unlike the option list. A column set is read once when a node is
// opened and again when the table changes, and a stale one is far more
// expensive than a repeated read: a mapping validated against a cached schema
// would refuse a column that exists or accept one that no longer does.
func (resolver *Resolver) LoadSchema(ctx context.Context, loader property.OptionsLoader, scope Scope) (property.MapperSchema, error) {
	if loader.Source != property.LoaderInternal {
		return property.MapperSchema{}, fmt.Errorf("this field's schema source is not supported")
	}
	resolver.schemas.mu.RLock()
	schema, registered := resolver.schemas.loaders[loader.Name]
	resolver.schemas.mu.RUnlock()
	if !registered {
		return property.MapperSchema{}, fmt.Errorf("this field's schema source is not available on this server")
	}
	return schema(ctx, scope)
}
