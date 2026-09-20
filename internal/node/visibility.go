package node

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Per-tenant visibility.
//
// A host that ships its own nodes, its CRM or its WhatsApp integration, does
// not want them offered to every other tenant of the deployment. A node type
// can therefore be scoped to a set of tenants; it is then absent from the
// catalogue, the icon route, the option loaders and the compiler for anyone
// outside the set.
//
// The unit is the node TYPE, not the {type, version} pair. To a user
// "pack.acme.crm" is one node however many versions of it are registered, and a
// scope per version would let a workflow written against version 1 of a scoped
// type resolve, downward, to something its tenant was never given.
//
// The default is unscoped, and an unscoped registry pays nothing: the hot path
// checks one map length. A type is never scoped implicitly.

// maxTenantIDBytes bounds one tenant ID in a scope. Generous on purpose: the
// grammar is looser than the admin create-tenant pattern because
// auth.bootstrap_tenant is not validated by that pattern, and a scope that
// could not name a tenant that exists would be a scope nobody can write.
const maxTenantIDBytes = 128

// CheckTenantID reports why id cannot appear in a visibility scope, or nil.
//
// It refuses what would make a scope ambiguous or unreadable: nothing at all,
// something absurdly long, whitespace or control characters (which an operator
// cannot see in a log line), and the two separators the `type=tenant` grammar of
// the packs.visible_to configuration key uses. It does not impose the admin
// create-tenant pattern; see maxTenantIDBytes.
//
// The node package owns the rule so the pack manifest loader, the operator
// override and the registry can never disagree about what a tenant ID is.
func CheckTenantID(id string) error {
	if id == "" {
		return fmt.Errorf("a tenant ID must not be empty")
	}
	if len(id) > maxTenantIDBytes {
		return fmt.Errorf("a tenant ID is %d bytes long, the limit is %d", len(id), maxTenantIDBytes)
	}
	if !utf8.ValidString(id) {
		return fmt.Errorf("tenant ID %q is not valid UTF-8", id)
	}
	for _, character := range id {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return fmt.Errorf("tenant ID %q must not contain whitespace or control characters", id)
		}
		if character == ',' || character == '=' {
			return fmt.Errorf("tenant ID %q must not contain %q, which the packs.visible_to grammar reserves", id, string(character))
		}
	}
	return nil
}

// normaliseTenants validates a declared scope and returns it sorted and
// de-duplicated. An empty scope is unscoped and comes back as nil, never as an
// empty slice, so a definition with no scope compares equal to one that never
// declared any.
func normaliseTenants(declared []string) ([]string, error) {
	if len(declared) == 0 {
		return nil, nil
	}
	tenants := make([]string, 0, len(declared))
	for _, id := range declared {
		if err := CheckTenantID(id); err != nil {
			return nil, err
		}
		tenants = append(tenants, id)
	}
	slices.Sort(tenants)
	return slices.Compact(tenants), nil
}

// describeScope renders a scope for an error message.
func describeScope(tenants []string) string {
	if len(tenants) == 0 {
		return "no scope (every tenant)"
	}
	return fmt.Sprintf("tenants %v", tenants)
}

// checkScopeAgrees refuses a version whose scope differs from a version of the
// same type that is already registered, naming both. Candidate versions are
// visited in ascending order so the message is the same on every boot.
//
// Composition time only: it scans the definitions, which the hot path never
// does.
func (registry *Registry) checkScopeAgrees(nodeType string, version workflow.TypeVersion, tenants []string) error {
	// An unscoped registration can only disagree with a scoped type.
	if len(tenants) == 0 && len(registry.scopes[nodeType]) == 0 {
		return nil
	}
	versions := make([]workflow.TypeVersion, 0, 2)
	for key := range registry.definitions {
		if key.nodeType == nodeType {
			versions = append(versions, key.version)
		}
	}
	slices.SortFunc(versions, workflow.TypeVersion.Compare)
	for _, other := range versions {
		existing := registry.definitions[definitionKey{nodeType: nodeType, version: other}]
		if !slices.Equal(existing.VisibleTo, tenants) {
			return fmt.Errorf("node type %q version %s declares %s but version %s declares %s: every version of a type shares one scope",
				nodeType, version, describeScope(tenants), other, describeScope(existing.VisibleTo))
		}
	}
	return nil
}

// indexScope records a type's scope in the lookup index. A type with no scope
// has no entry, which is what keeps an unscoped registry on the one-length-check
// fast path.
func (registry *Registry) indexScope(nodeType string, tenants []string) {
	if len(tenants) == 0 {
		return
	}
	registry.setScope(nodeType, tenants)
}

func (registry *Registry) setScope(nodeType string, tenants []string) {
	if registry.scopes == nil {
		registry.scopes = make(map[string]map[string]struct{})
	}
	set := make(map[string]struct{}, len(tenants))
	for _, id := range tenants {
		set[id] = struct{}{}
	}
	registry.scopes[nodeType] = set
}

// VisibleTo reports whether tenantID may see nodeType.
//
// A type with no scope, and a registry with no scopes at all, is visible to
// everyone — including an empty tenant ID. A scoped type is visible only to the
// tenants in its set, and an empty or unknown ID is never in it: an unidentified
// caller sees only what is unscoped. It answers true for a type that is not
// registered at all, because hiding is a property of scoped types; whether a
// type exists is the catalogue's own question.
//
// This is the whole cost visibility adds to a lookup: one length check and, for
// a registry that scopes something, one map lookup by type. It allocates
// nothing.
func (registry *Registry) VisibleTo(nodeType, tenantID string) bool {
	if registry == nil || len(registry.scopes) == 0 {
		return true
	}
	allowed, scoped := registry.scopes[nodeType]
	if !scoped {
		return true
	}
	_, member := allowed[tenantID]
	return member
}

// Scoped reports whether nodeType has a visibility scope, so a caller that
// serves something type-specific can avoid letting a shared cache hold it.
func (registry *Registry) Scoped(nodeType string) bool {
	if registry == nil {
		return false
	}
	_, scoped := registry.scopes[nodeType]
	return scoped
}

// Scopes returns a copy of every scoped type and its sorted tenant list, for
// the boot log. Mutating the result changes nothing the registry enforces.
func (registry *Registry) Scopes() map[string][]string {
	scopes := make(map[string][]string)
	if registry == nil {
		return scopes
	}
	for nodeType, set := range registry.scopes {
		tenants := make([]string, 0, len(set))
		for id := range set {
			tenants = append(tenants, id)
		}
		sort.Strings(tenants)
		scopes[nodeType] = tenants
	}
	return scopes
}

// ListFor returns what tenantID may see of the catalogue, in the same order as
// List. It is never nil, so it serialises as an empty array rather than null.
func (registry *Registry) ListFor(tenantID string) []Definition {
	definitions := registry.List()
	if registry == nil || len(registry.scopes) == 0 {
		return definitions
	}
	visible := definitions[:0]
	for _, definition := range definitions {
		if registry.VisibleTo(definition.Type, tenantID) {
			visible = append(visible, definition)
		}
	}
	return visible
}

// ResolveFor is Resolve for one tenant: a type the tenant may not see does not
// resolve, at any version.
func (registry *Registry) ResolveFor(tenantID, nodeType string, version workflow.TypeVersion) (Definition, bool) {
	if !registry.VisibleTo(nodeType, tenantID) {
		return Definition{}, false
	}
	return registry.Resolve(nodeType, version)
}

var (
	_ workflow.TenantScoper      = (*Registry)(nil)
	_ workflow.TypeCatalog       = TenantView{}
	_ workflow.RestrictedCatalog = TenantView{}
)

// ForTenant narrows the registry to what one tenant may see. It is the catalogue
// a compiler working on that tenant's behalf must be given; workflow.CatalogFor
// finds it through workflow.TenantScoper.
//
// The view is a small value over the registry, not a copy: building one is a
// single boxed struct, once per compile, and looking a node up through it
// allocates nothing beyond what the registry's own Lookup does. It does not
// implement workflow.TenantScoper, so a narrowed view can never be widened by
// scoping it again.
func (registry *Registry) ForTenant(tenantID string) workflow.Catalog {
	return TenantView{registry: registry, tenantID: tenantID}
}

// TenantView is a registry narrowed to one tenant.
type TenantView struct {
	registry *Registry
	tenantID string
}

// Lookup implements workflow.Catalog. A type the tenant may not see is refused
// before it is resolved, so the refusal costs no clone of the definition.
func (view TenantView) Lookup(nodeType string, version workflow.TypeVersion) (workflow.NodeDefinition, bool) {
	if !view.registry.VisibleTo(nodeType, view.tenantID) {
		return workflow.NodeDefinition{}, false
	}
	return view.registry.Lookup(nodeType, version)
}

// HasType implements workflow.TypeCatalog: the type is registered AND this
// tenant may see it. A hidden type is therefore reported exactly like one that
// does not exist, unless the caller asks Restricted.
func (view TenantView) HasType(nodeType string) bool {
	return view.registry.VisibleTo(nodeType, view.tenantID) && view.registry.HasType(nodeType)
}

// Restricted implements workflow.RestrictedCatalog: the type is registered but
// scoped away from this tenant. Only a registered type can have a scope, so
// this is a single lookup and a type that does not exist is not restricted.
func (view TenantView) Restricted(nodeType string) bool {
	return !view.registry.VisibleTo(nodeType, view.tenantID)
}

// ScopeTo replaces the visibility scope of one registered type. It is the
// operator's override of what a pack's manifest declared: the set REPLACES the
// manifest's, so an operator can narrow it or widen it. There is no wildcard.
//
// It is a composition-time operation, like Register. The registry is read-only
// once the server is handling work, and this writes the scope index and every
// stored version of the type without a lock; a test that calls it between phases
// must not have worker goroutines running.
func (registry *Registry) ScopeTo(nodeType string, tenants []string) error {
	return registry.ApplyVisibility(map[string][]string{nodeType: tenants})
}

// ApplyVisibility applies operator overrides, one entry per node type. Every
// entry is validated before any is applied, so a bad entry leaves the registry
// exactly as it was: a partly applied override would leave some types scoped and
// others open, which is worse than either.
//
// An entry is refused when its type is not registered (a typo must refuse boot,
// not scope nothing), when it names a built-in, when its tenant list is empty (it
// would read as "nobody" or as "everybody"), or when any tenant ID is malformed.
// Built-ins can never be scoped: the engine names some of them.
//
// Composition time only, like Register and ScopeTo: it must run after the last
// node registration, and before the registry is shared.
func (registry *Registry) ApplyVisibility(grants map[string][]string) error {
	if len(grants) == 0 {
		return nil
	}
	if registry == nil {
		return fmt.Errorf("node registry is required")
	}
	types := make([]string, 0, len(grants))
	for nodeType := range grants {
		types = append(types, nodeType)
	}
	sort.Strings(types)

	normalised := make(map[string][]string, len(grants))
	for _, nodeType := range types {
		if strings.HasPrefix(nodeType, BuiltinPrefix) {
			return fmt.Errorf("node type %q is built in and cannot be scoped to tenants: built-in nodes are visible to every tenant", nodeType)
		}
		registered, builtin := false, false
		for key, definition := range registry.definitions {
			if key.nodeType != nodeType {
				continue
			}
			registered = true
			if definition.Source == SourceBuiltin {
				builtin = true
			}
		}
		if !registered {
			return fmt.Errorf("node type %q is not registered, so it cannot be scoped to tenants", nodeType)
		}
		if builtin {
			return fmt.Errorf("node type %q is built in and cannot be scoped to tenants: built-in nodes are visible to every tenant", nodeType)
		}
		if len(grants[nodeType]) == 0 {
			return fmt.Errorf("node type %q: name at least one tenant; an empty list would read as both nobody and everybody", nodeType)
		}
		tenants, err := normaliseTenants(grants[nodeType])
		if err != nil {
			return fmt.Errorf("node type %q: %w", nodeType, err)
		}
		normalised[nodeType] = tenants
	}

	for _, nodeType := range types {
		tenants := normalised[nodeType]
		registry.setScope(nodeType, tenants)
		for key, definition := range registry.definitions {
			if key.nodeType == nodeType {
				definition.VisibleTo = append([]string(nil), tenants...)
				registry.definitions[key] = definition
			}
		}
	}
	return nil
}
