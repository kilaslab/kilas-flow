package workflow

// RestrictedCatalog is implemented by a catalogue narrowed to one tenant, so
// the compiler can tell "no such node" from "a node this tenant may not use".
//
// The two need different words. An unregistered type is a mistake the author
// can fix; a restricted one is a request to make of whoever runs the
// deployment, and a diagnostic that called it "not registered" would send the
// author hunting for a spelling error that is not there.
type RestrictedCatalog interface {
	Catalog
	// Restricted reports that nodeType is registered but not available to the
	// tenant this catalogue was narrowed to. It is false for a type that does
	// not exist at all, and for a type the tenant may use.
	Restricted(nodeType string) bool
}

// TenantScoper is implemented by a catalogue that can narrow itself to one
// tenant.
//
// The narrowed view it returns deliberately does not implement TenantScoper
// itself, so a view can never be widened by asking it to scope again.
type TenantScoper interface {
	ForTenant(tenantID string) Catalog
}

// CatalogFor returns the catalogue tenantID may compile against.
//
// A catalogue that cannot scope is returned unchanged, so callers with a stub
// or a fake need no special case. Every place that compiles a document on a
// tenant's behalf obtains its catalogue through this function: it is the one
// spot where "which nodes may this tenant use" is decided, and a call site that
// bypasses it compiles against the whole deployment's catalogue.
func CatalogFor(catalog Catalog, tenantID string) Catalog {
	if scoper, ok := catalog.(TenantScoper); ok {
		return scoper.ForTenant(tenantID)
	}
	return catalog
}
