package node

import (
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
)

// Built-in node availability.
//
// packs.visible_to scopes a node PACK to the tenants that may see it. A built-in
// cannot be scoped that way (the engine names some of them), yet a SaaS has good
// reasons to keep some away from its customers: SQLite opens a file path on the
// server, Code runs a script, Postgres and MySQL dial out, HTTP Request reaches
// any URL the outbound policy lets through. Availability is the built-in
// counterpart: an allow list and a deny list, each deployment-wide or for one
// tenant, from the deployment configuration (nodes.allow, nodes.deny) and from a
// tenant's own settings (the "nodes" section).
//
// A type hidden this way is hidden exactly as a type scoped to other tenants is:
// it goes through VisibleTo, so the catalogue, the icon and option routes, the
// compiler (node.not_available), activation and the worker's claim all answer
// the same way, and none of them needed to learn a second rule.
//
// The rules for one tenant:
//
//   - The allow list that applies to a tenant is every deployment-wide entry, every
//     entry naming the tenant and the tenant's own settings, added together. When it
//     is not empty, a built-in outside it is hidden; when it is empty, nothing is
//     hidden by it.
//   - The deny list is added up the same way, and a built-in on it is hidden.
//   - Deny wins over allow: a type on both lists is hidden.
//   - The types the engine names itself (EngineRequiredTypes) are never hidden,
//     by either list, and cannot be put on a deny list.
//   - Only built-ins are affected. A pack's nodes keep packs.visible_to.
//
// The default is no rule at all, and a registry with none pays one nil check.

// engineRequiredTypes are the built-ins the engine names, so hiding one from a
// tenant would break that tenant's runs from the inside: a called workflow
// starts from the sub-workflow trigger, and an error workflow from the error
// trigger. nodes.ExecuteWorkflowTriggerType and nodes.ErrorTriggerNodeType hold
// the same strings; a test in the nodes package pins the two together.
var engineRequiredTypes = []string{
	"kilasflow.errorTrigger",
	"kilasflow.executeWorkflowTrigger",
}

// EngineRequiredTypes lists the built-in node types no allow or deny rule can
// hide. The result is a copy.
func EngineRequiredTypes() []string {
	return slices.Clone(engineRequiredTypes)
}

// IsEngineRequired reports whether nodeType is one the engine names itself.
func IsEngineRequired(nodeType string) bool {
	return slices.Contains(engineRequiredTypes, nodeType)
}

// AvailabilityRule is one allow or deny entry. An empty Tenant applies to every
// tenant of the deployment.
type AvailabilityRule struct {
	Type   string
	Tenant string
}

// AvailabilityRules are the deployment configuration's two lists.
type AvailabilityRules struct {
	Allow []AvailabilityRule
	Deny  []AvailabilityRule
}

// TenantAvailability is one tenant's own lists, read from its settings. The
// types are built-in node types; the tenant is implied.
type TenantAvailability struct {
	Allow []string
	Deny  []string
}

// TenantAvailabilitySource supplies a tenant's own lists. The registry asks it
// once per catalogue it narrows to a tenant, and an implementation is expected to
// answer from a cache: it runs on the compile and claim paths.
//
// An error means the lists could not be read. The registry then hides every
// built-in it can hide from that tenant rather than showing them all, because a
// deny list that fails open is a deny list a settings outage switches off.
type TenantAvailabilitySource interface {
	TenantAvailability(tenantID string) (TenantAvailability, error)
}

type typeSet map[string]struct{}

func newTypeSet(types []string) typeSet {
	set := make(typeSet, len(types))
	for _, nodeType := range types {
		set[nodeType] = struct{}{}
	}
	return set
}

// union returns a set holding every member of the given ones. With one non-empty
// set it returns that set itself, so a tenant with nothing of its own shares the
// deployment-wide sets and allocates nothing.
func union(sets ...typeSet) typeSet {
	var only typeSet
	count := 0
	for _, set := range sets {
		if len(set) > 0 {
			only = set
			count++
		}
	}
	if count <= 1 {
		return only
	}
	merged := make(typeSet)
	for _, set := range sets {
		for nodeType := range set {
			merged[nodeType] = struct{}{}
		}
	}
	return merged
}

type ruleSet struct {
	allow typeSet
	deny  typeSet
}

// availability is the registry's built-in allow and deny state.
type availability struct {
	// decider maps every type the rules can hide to the type whose rules decide
	// it: a built-in decides for itself, and a tool form of a built-in follows
	// its base, because the tool runs the base's executor. A type absent here is
	// a pack's or a sidecar's and is never touched.
	decider  map[string]string
	global   ruleSet
	byTenant map[string]ruleSet
	source   TenantAvailabilitySource
}

// resolvedAvailability is what applies to one tenant, worked out once per
// catalogue view so a compile does not recompute it for every node.
type resolvedAvailability struct {
	state *availability
	allow typeSet
	deny  typeSet
	// closed hides every type the rules can hide: the tenant's own lists could
	// not be read.
	closed bool
}

// hides reports whether the rules withhold nodeType from the tenant.
func (resolved resolvedAvailability) hides(nodeType string) bool {
	if resolved.state == nil {
		return false
	}
	if len(resolved.allow) == 0 && len(resolved.deny) == 0 && !resolved.closed {
		return false
	}
	decider, managed := resolved.state.decider[nodeType]
	if !managed || IsEngineRequired(decider) {
		return false
	}
	if resolved.closed {
		return true
	}
	if _, denied := resolved.deny[decider]; denied {
		return true
	}
	if len(resolved.allow) > 0 {
		_, allowed := resolved.allow[decider]
		return !allowed
	}
	return false
}

// resolve works out what applies to tenantID.
func (state *availability) resolve(tenantID string) resolvedAvailability {
	if state == nil {
		return resolvedAvailability{}
	}
	own, hasOwn := state.byTenant[tenantID]
	var settings TenantAvailability
	if state.source != nil && tenantID != "" {
		var err error
		settings, err = state.source.TenantAvailability(tenantID)
		if err != nil {
			slog.Error("tenant node availability unavailable; every built-in that can be hidden is hidden from this tenant until it can be read",
				"tenant", tenantID, "error", err)
			return resolvedAvailability{state: state, closed: true}
		}
	}
	if !hasOwn && len(settings.Allow) == 0 && len(settings.Deny) == 0 {
		return resolvedAvailability{state: state, allow: state.global.allow, deny: state.global.deny}
	}
	return resolvedAvailability{
		state: state,
		allow: union(state.global.allow, own.allow, newTypeSet(settings.Allow)),
		deny:  union(state.global.deny, own.deny, newTypeSet(settings.Deny)),
	}
}

// indexAvailability rebuilds the map of types the rules can hide. Composition
// time: it scans every definition.
func (registry *Registry) indexAvailability() map[string]string {
	builtins := make(map[string]struct{})
	for key, definition := range registry.definitions {
		if definition.Source == SourceBuiltin && definition.ToolFormOf == "" {
			builtins[key.nodeType] = struct{}{}
		}
	}
	decider := make(map[string]string, len(builtins))
	for nodeType := range builtins {
		decider[nodeType] = nodeType
	}
	// A tool form, built in or a pack's, follows a built-in base: the tool runs
	// the base's executor with the base's credentials.
	for key, definition := range registry.definitions {
		if definition.ToolFormOf == "" {
			continue
		}
		if _, builtinBase := builtins[definition.ToolFormOf]; builtinBase {
			decider[key.nodeType] = definition.ToolFormOf
		}
	}
	return decider
}

// checkAvailabilityType refuses a type no rule may name, with the reason an
// operator can act on. deny is true for the deny list, which also refuses the
// engine-required types.
func (registry *Registry) checkAvailabilityType(nodeType string, deny bool) error {
	if nodeType == "" {
		return fmt.Errorf("the node type is empty")
	}
	if deny && IsEngineRequired(nodeType) {
		return fmt.Errorf("node type %q is named by the engine itself (%s), so it cannot be denied", nodeType, strings.Join(engineRequiredTypes, ", "))
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
		return fmt.Errorf("node type %q is not registered, so it cannot be allowed or denied", nodeType)
	}
	if !builtin {
		return fmt.Errorf("node type %q is not a built-in: scope a pack's node to tenants with packs.visible_to", nodeType)
	}
	if base := registry.toolFormBase(nodeType); base != "" {
		return fmt.Errorf("node type %q is the agent-tool form of %q and always follows it: name %q instead", nodeType, base, base)
	}
	return nil
}

// CheckTenantAvailability validates a tenant's own lists against the registry:
// every type must be a registered built-in, and the engine-required types cannot
// be denied. Each problem is returned against the list and index it came from,
// so a caller can locate it.
func (registry *Registry) CheckTenantAvailability(settings TenantAvailability) []AvailabilityProblem {
	var problems []AvailabilityProblem
	for index, nodeType := range settings.Allow {
		if err := registry.checkAvailabilityType(nodeType, false); err != nil {
			problems = append(problems, AvailabilityProblem{List: "allow", Index: index, Err: err})
		}
	}
	for index, nodeType := range settings.Deny {
		if err := registry.checkAvailabilityType(nodeType, true); err != nil {
			problems = append(problems, AvailabilityProblem{List: "deny", Index: index, Err: err})
		}
	}
	return problems
}

// AvailabilityProblem is one refused entry of a tenant's lists.
type AvailabilityProblem struct {
	// List is "allow" or "deny".
	List string
	// Index is the entry's position in that list.
	Index int
	Err   error
}

// ApplyAvailability applies the deployment configuration's allow and deny lists.
// Every entry is validated before any is applied, so a bad entry leaves the
// registry as it was.
//
// An entry is refused when its type is not registered (a typo must refuse boot,
// not hide nothing), is not a built-in, is the agent-tool form of a node (name
// the node), is an engine-required type on a deny list, or names a malformed
// tenant ID.
//
// Composition time only, like ApplyVisibility: after the last node registration
// and before the registry is shared.
func (registry *Registry) ApplyAvailability(rules AvailabilityRules) error {
	if registry == nil {
		return fmt.Errorf("node registry is required")
	}
	if len(rules.Allow) == 0 && len(rules.Deny) == 0 {
		return nil
	}
	built := ruleSet{allow: typeSet{}, deny: typeSet{}}
	byTenant := map[string]ruleSet{}
	for _, list := range []struct {
		name    string
		entries []AvailabilityRule
		deny    bool
	}{{"allow", rules.Allow, false}, {"deny", rules.Deny, true}} {
		for index, rule := range list.entries {
			if err := registry.checkAvailabilityType(rule.Type, list.deny); err != nil {
				return fmt.Errorf("nodes.%s[%d]: %w", list.name, index, err)
			}
			target := built
			if rule.Tenant != "" {
				if err := CheckTenantID(rule.Tenant); err != nil {
					return fmt.Errorf("nodes.%s[%d]: %w", list.name, index, err)
				}
				scoped, ok := byTenant[rule.Tenant]
				if !ok {
					scoped = ruleSet{allow: typeSet{}, deny: typeSet{}}
					byTenant[rule.Tenant] = scoped
				}
				target = scoped
			}
			if list.deny {
				target.deny[rule.Type] = struct{}{}
			} else {
				target.allow[rule.Type] = struct{}{}
			}
		}
	}
	state := registry.ensureAvailability()
	state.global = built
	state.byTenant = byTenant
	return nil
}

// SetTenantAvailabilitySource attaches the supplier of tenants' own lists. It is
// a composition-time call like ApplyAvailability, made once the tenant settings
// are wired and after the last node registration.
func (registry *Registry) SetTenantAvailabilitySource(source TenantAvailabilitySource) {
	if registry == nil || source == nil {
		return
	}
	registry.ensureAvailability().source = source
}

func (registry *Registry) ensureAvailability() *availability {
	if registry.availability == nil {
		registry.availability = &availability{}
	}
	registry.availability.decider = registry.indexAvailability()
	return registry.availability
}

// AvailabilityRulesApplied returns the deployment-wide and per-tenant rules in
// effect, as sorted `type` and `type=tenant` entries per list, for the boot log.
func (registry *Registry) AvailabilityRulesApplied() (allow, deny []string) {
	if registry == nil || registry.availability == nil {
		return nil, nil
	}
	state := registry.availability
	collect := func(global typeSet, pick func(ruleSet) typeSet) []string {
		var entries []string
		for nodeType := range global {
			entries = append(entries, nodeType)
		}
		for tenant, set := range state.byTenant {
			for nodeType := range pick(set) {
				entries = append(entries, nodeType+"="+tenant)
			}
		}
		sort.Strings(entries)
		return entries
	}
	return collect(state.global.allow, func(set ruleSet) typeSet { return set.allow }),
		collect(state.global.deny, func(set ruleSet) typeSet { return set.deny })
}
