package node_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// Built-in availability is the allow and deny state of one registry. These tests
// drive the registry directly: the catalogue (ListFor, ResolveFor), the compiler's
// view (ForTenant: Lookup, HasType, Restricted) and the type-level answer
// (VisibleTo) must agree, because compile, activation and claim all read the
// view and the catalogue routes read the first two.

const (
	denied  = "kilasflow.sqlite"
	allowed = "kilasflow.set"
)

func rule(nodeType, tenant string) node.AvailabilityRule {
	return node.AvailabilityRule{Type: nodeType, Tenant: tenant}
}

func mustApply(t *testing.T, registry *node.Registry, rules node.AvailabilityRules) {
	t.Helper()
	if err := registry.ApplyAvailability(rules); err != nil {
		t.Fatalf("ApplyAvailability(%+v) error = %v", rules, err)
	}
}

// settingsSource is a fake supplier of tenants' own lists.
type settingsSource struct {
	byTenant map[string]node.TenantAvailability
	err      error
}

func (source settingsSource) TenantAvailability(tenantID string) (node.TenantAvailability, error) {
	if source.err != nil {
		return node.TenantAvailability{}, source.err
	}
	return source.byTenant[tenantID], nil
}

// expectVisible asserts one type's answer on every surface that reads it.
func expectVisible(t *testing.T, registry *node.Registry, tenant, nodeType string, want bool) {
	t.Helper()
	if got := registry.VisibleTo(nodeType, tenant); got != want {
		t.Errorf("VisibleTo(%q, %q) = %v, want %v", nodeType, tenant, got, want)
	}
	if got := typesOf(registry.ListFor(tenant))[nodeType]; got != want {
		t.Errorf("ListFor(%q) lists %q = %v, want %v", tenant, nodeType, got, want)
	}
	if _, got := registry.ResolveFor(tenant, nodeType, workflow.V(1)); got != want {
		t.Errorf("ResolveFor(%q, %q) found = %v, want %v", tenant, nodeType, got, want)
	}
	view := registry.ForTenant(tenant)
	if _, got := view.Lookup(nodeType, workflow.V(1)); got != want {
		t.Errorf("view.Lookup(%q) for %q found = %v, want %v", nodeType, tenant, got, want)
	}
	if got := view.(workflow.TypeCatalog).HasType(nodeType); got != want {
		t.Errorf("view.HasType(%q) for %q = %v, want %v", nodeType, tenant, got, want)
	}
	// A hidden type reads as "not available", the diagnostic that is not
	// "unknown type": it is registered, just withheld.
	if got := view.(workflow.RestrictedCatalog).Restricted(nodeType); got != !want {
		t.Errorf("view.Restricted(%q) for %q = %v, want %v", nodeType, tenant, got, !want)
	}
}

// A deny entry with no tenant hides the type from everybody, including a caller
// with no identity, and touches nothing else: not another built-in, not a pack.
func TestADeniedBuiltinIsHiddenFromEveryTenant(t *testing.T) {
	registry := visibilityRegistry(t)
	mustApply(t, registry, node.AvailabilityRules{Deny: []node.AvailabilityRule{rule(denied, "")}})

	for _, tenant := range []string{"acme", "globex", "default", ""} {
		expectVisible(t, registry, tenant, denied, false)
		expectVisible(t, registry, tenant, allowed, true)
		expectVisible(t, registry, tenant, "pack.shared.ping", true)
	}
	// The pack scope is unchanged by the deny list.
	expectVisible(t, registry, "acme", "pack.acme.crm", true)
	expectVisible(t, registry, "globex", "pack.acme.crm", false)
}

// A deny entry naming a tenant hides the type from that tenant only.
func TestATenantScopedDenyHidesOnlyThatTenant(t *testing.T) {
	registry := visibilityRegistry(t)
	mustApply(t, registry, node.AvailabilityRules{Deny: []node.AvailabilityRule{rule(denied, "acme")}})

	expectVisible(t, registry, "acme", denied, false)
	expectVisible(t, registry, "globex", denied, true)
	expectVisible(t, registry, "", denied, true)
	expectVisible(t, registry, "acme", allowed, true)
}

// With an allow list, only the listed built-ins, the engine-required types and
// the packs' nodes are visible to a tenant it applies to.
func TestAnAllowListLimitsTheBuiltinsOfItsTenant(t *testing.T) {
	registry := visibilityRegistry(t)
	mustApply(t, registry, node.AvailabilityRules{Allow: []node.AvailabilityRule{rule(allowed, "acme")}})

	expectVisible(t, registry, "acme", allowed, true)
	expectVisible(t, registry, "acme", denied, false)
	expectVisible(t, registry, "acme", "kilasflow.manual", false)
	// Engine-required types are always visible, listed or not.
	for _, required := range node.EngineRequiredTypes() {
		expectVisible(t, registry, "acme", required, true)
	}
	// A pack's nodes keep packs.visible_to; the allow list is about built-ins.
	expectVisible(t, registry, "acme", "pack.acme.crm", true)
	expectVisible(t, registry, "acme", "pack.shared.ping", true)
	// Another tenant has no allow list that applies, so nothing is limited.
	expectVisible(t, registry, "globex", denied, true)
	expectVisible(t, registry, "globex", "kilasflow.manual", true)
}

// A deployment-wide allow list applies to every tenant, and a tenant's own entry
// adds to it rather than replacing it.
func TestAllowEntriesAreAddedTogether(t *testing.T) {
	registry := visibilityRegistry(t)
	mustApply(t, registry, node.AvailabilityRules{Allow: []node.AvailabilityRule{
		rule("kilasflow.manual", ""), rule(allowed, ""), rule(denied, "acme"),
	}})

	for _, tenant := range []string{"acme", "globex"} {
		expectVisible(t, registry, tenant, "kilasflow.manual", true)
		expectVisible(t, registry, tenant, allowed, true)
		expectVisible(t, registry, tenant, "kilasflow.code", false)
	}
	expectVisible(t, registry, "acme", denied, true)
	expectVisible(t, registry, "globex", denied, false)
}

// Deny wins over allow, whichever scope each came from.
func TestDenyWinsOverAllow(t *testing.T) {
	registry := visibilityRegistry(t)
	mustApply(t, registry, node.AvailabilityRules{
		Allow: []node.AvailabilityRule{rule(allowed, ""), rule(denied, "")},
		Deny:  []node.AvailabilityRule{rule(denied, "acme")},
	})

	expectVisible(t, registry, "acme", denied, false)
	expectVisible(t, registry, "globex", denied, true)
	expectVisible(t, registry, "acme", allowed, true)
}

// The engine-required types are never hidden: an allow list that omits them does
// not hide them, and no deny entry may name them.
func TestEngineRequiredTypesCannotBeDenied(t *testing.T) {
	registry := visibilityRegistry(t)
	for _, required := range node.EngineRequiredTypes() {
		for _, tenant := range []string{"", "acme"} {
			err := registry.ApplyAvailability(node.AvailabilityRules{Deny: []node.AvailabilityRule{rule(required, tenant)}})
			if err == nil || !strings.Contains(err.Error(), required) || !strings.Contains(err.Error(), "cannot be denied") {
				t.Errorf("ApplyAvailability(deny %q for %q) error = %v, want a refusal that names the type", required, tenant, err)
			}
		}
		problems := registry.CheckTenantAvailability(node.TenantAvailability{Deny: []string{required}})
		if len(problems) != 1 || problems[0].List != "deny" || problems[0].Index != 0 {
			t.Errorf("CheckTenantAvailability(deny %q) = %+v, want one problem at deny[0]", required, problems)
		}
	}
	// A refused apply leaves the registry as it was.
	for _, required := range node.EngineRequiredTypes() {
		expectVisible(t, registry, "acme", required, true)
	}
}

// The engine-required list names the types the engine is configured with: the
// composition root hands nodes.ExecuteWorkflowTriggerType and
// nodes.ErrorTriggerNodeType to the engine, so a drift here would let a deny
// entry break runs from the inside.
func TestEngineRequiredTypesMatchWhatTheEngineNames(t *testing.T) {
	got := node.EngineRequiredTypes()
	want := []string{nodes.ErrorTriggerNodeType, nodes.ExecuteWorkflowTriggerType}
	if !reflect.DeepEqual(sorted(got), sorted(want)) {
		t.Errorf("EngineRequiredTypes() = %v, want %v", got, want)
	}
	registry := visibilityRegistry(t)
	for _, required := range got {
		if !registry.VisibleTo(required, "acme") {
			t.Errorf("%q is not visible to a tenant", required)
		}
		if _, found := registry.Get(required, workflow.V(1)); !found {
			t.Errorf("%q is named as engine-required but is not a registered built-in", required)
		}
	}
	// The returned list is a copy.
	got[0] = "tampered"
	if node.EngineRequiredTypes()[0] == "tampered" {
		t.Error("EngineRequiredTypes() shares its backing array with the caller")
	}
}

func sorted(values []string) []string {
	out := append([]string(nil), values...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// An entry that cannot be honoured refuses the apply naming its list and index,
// and leaves nothing applied.
func TestAnUnusableAvailabilityEntryIsRefused(t *testing.T) {
	registry := visibilityRegistry(t)

	for name, test := range map[string]struct {
		rules node.AvailabilityRules
		want  string
	}{
		"unregistered":   {node.AvailabilityRules{Deny: []node.AvailabilityRule{rule("kilasflow.nope", "")}}, "nodes.deny[0]"},
		"a pack's type":  {node.AvailabilityRules{Deny: []node.AvailabilityRule{rule("pack.shared.ping", "")}}, "packs.visible_to"},
		"allow a pack":   {node.AvailabilityRules{Allow: []node.AvailabilityRule{rule("pack.acme.crm", "")}}, "packs.visible_to"},
		"a bad tenant":   {node.AvailabilityRules{Deny: []node.AvailabilityRule{rule(denied, "a b")}}, "tenant ID"},
		"second is bad":  {node.AvailabilityRules{Deny: []node.AvailabilityRule{rule(denied, ""), rule("kilasflow.nope", "")}}, "nodes.deny[1]"},
		"allow is typed": {node.AvailabilityRules{Allow: []node.AvailabilityRule{rule("kilasflow.nope", "acme")}}, "nodes.allow[0]"},
	} {
		t.Run(name, func(t *testing.T) {
			err := registry.ApplyAvailability(test.rules)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ApplyAvailability() error = %v, want a refusal containing %q", err, test.want)
			}
			expectVisible(t, registry, "acme", denied, true)
		})
	}
}

// A built-in's agent-tool form runs the base's executor, so it follows the base
// and cannot be named on its own.
func TestABuiltinToolFormFollowsItsBase(t *testing.T) {
	registry := visibilityRegistry(t)
	base := nodes.GmailNodeType
	if err := registry.ApplyAvailability(node.AvailabilityRules{Deny: []node.AvailabilityRule{rule(nodes.GmailToolNodeType, "")}}); err == nil || !strings.Contains(err.Error(), base) {
		t.Fatalf("ApplyAvailability(deny the tool form) error = %v, want a refusal naming the base %q", err, base)
	}

	mustApply(t, registry, node.AvailabilityRules{Deny: []node.AvailabilityRule{rule(base, "acme")}})
	expectVisible(t, registry, "acme", base, false)
	expectVisible(t, registry, "acme", nodes.GmailToolNodeType, false)
	expectVisible(t, registry, "globex", nodes.GmailToolNodeType, true)
}

// An allow list keeps a base's tool form with the base, not with the list.
func TestAToolFormFollowsItsBaseUnderAnAllowList(t *testing.T) {
	registry := visibilityRegistry(t)
	base := nodes.GmailNodeType
	mustApply(t, registry, node.AvailabilityRules{Allow: []node.AvailabilityRule{rule(base, "acme")}})

	expectVisible(t, registry, "acme", base, true)
	expectVisible(t, registry, "acme", nodes.GmailToolNodeType, true)
	expectVisible(t, registry, "acme", nodes.RssFeedReadToolNodeType, false)
}

// A tenant's own settings add to the deployment's lists, per tenant.
func TestATenantsOwnListsAreAddedToTheDeployments(t *testing.T) {
	registry := visibilityRegistry(t)
	mustApply(t, registry, node.AvailabilityRules{Deny: []node.AvailabilityRule{rule(denied, "")}})
	registry.SetTenantAvailabilitySource(settingsSource{byTenant: map[string]node.TenantAvailability{
		"acme":   {Deny: []string{"kilasflow.code"}},
		"globex": {Allow: []string{allowed}},
	}})

	// acme: the deployment's deny plus its own.
	expectVisible(t, registry, "acme", denied, false)
	expectVisible(t, registry, "acme", "kilasflow.code", false)
	expectVisible(t, registry, "acme", allowed, true)
	// globex: an allow list from its settings, with the deployment's deny
	// still winning.
	expectVisible(t, registry, "globex", allowed, true)
	expectVisible(t, registry, "globex", "kilasflow.manual", false)
	expectVisible(t, registry, "globex", denied, false)
	// A tenant with no settings is held to the deployment's lists only.
	expectVisible(t, registry, "initech", denied, false)
	expectVisible(t, registry, "initech", "kilasflow.code", true)
	// No identity, no settings read.
	expectVisible(t, registry, "", "kilasflow.code", true)
}

// A settings-only deployment, with no nodes.allow or nodes.deny at all, still
// honours a tenant's lists.
func TestATenantsListsApplyWithNoDeploymentRules(t *testing.T) {
	registry := visibilityRegistry(t)
	registry.SetTenantAvailabilitySource(settingsSource{byTenant: map[string]node.TenantAvailability{
		"acme": {Deny: []string{denied}},
	}})

	expectVisible(t, registry, "acme", denied, false)
	expectVisible(t, registry, "globex", denied, true)
	if !registry.Scoped(denied) || !registry.Scoped(allowed) {
		t.Error("Scoped() = false for a built-in a tenant's settings can hide")
	}
	if registry.Scoped("pack.shared.ping") {
		t.Error("Scoped() = true for an unscoped pack node")
	}
}

// A deny list that failed open would be switched off by a settings outage, so a
// tenant whose lists cannot be read loses every built-in that could be hidden,
// except the engine-required ones, and keeps the packs' nodes.
func TestAnUnreadableTenantListFailsClosed(t *testing.T) {
	registry := visibilityRegistry(t)
	registry.SetTenantAvailabilitySource(settingsSource{err: errors.New("store unavailable")})

	expectVisible(t, registry, "acme", denied, false)
	expectVisible(t, registry, "acme", allowed, false)
	for _, required := range node.EngineRequiredTypes() {
		expectVisible(t, registry, "acme", required, true)
	}
	expectVisible(t, registry, "acme", "pack.shared.ping", true)
	// An empty tenant ID is never read from the store.
	expectVisible(t, registry, "", denied, true)
}

// Settings entries are checked against the registry when written.
func TestCheckTenantAvailability(t *testing.T) {
	registry := visibilityRegistry(t)
	problems := registry.CheckTenantAvailability(node.TenantAvailability{
		Allow: []string{allowed, "kilasflow.nope"},
		Deny:  []string{denied, "pack.shared.ping", ""},
	})
	var got []string
	for _, problem := range problems {
		got = append(got, problem.List+"["+string(rune('0'+problem.Index))+"]")
	}
	if want := []string{"allow[1]", "deny[1]", "deny[2]"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("problems at %v, want %v: %+v", got, want, problems)
	}
	if problems := registry.CheckTenantAvailability(node.TenantAvailability{Allow: []string{allowed}, Deny: []string{denied}}); len(problems) != 0 {
		t.Errorf("CheckTenantAvailability(valid) = %+v, want none", problems)
	}
}

// The default deployment pays nothing and changes nothing: no rules, no source,
// no state.
func TestNoRulesChangeNothing(t *testing.T) {
	registry := visibilityRegistry(t)
	mustApply(t, registry, node.AvailabilityRules{})
	registry.SetTenantAvailabilitySource(nil)

	// pack.acme.crm is scoped to acme only and is the one thing that differs.
	if got, want := len(registry.ListFor("acme")), len(registry.List()); got != want {
		t.Errorf("ListFor(acme) = %d definitions, want all %d", got, want)
	}
	if got, want := len(registry.ListFor("")), len(registry.List())-1; got != want {
		t.Errorf("ListFor(\"\") = %d definitions, want %d", got, want)
	}
	if registry.Scoped(denied) {
		t.Error("Scoped() = true with no availability state")
	}
	allow, deny := registry.AvailabilityRulesApplied()
	if len(allow) != 0 || len(deny) != 0 {
		t.Errorf("AvailabilityRulesApplied() = %v, %v, want none", allow, deny)
	}
}

func TestAvailabilityRulesAppliedListsEntriesForTheBootLog(t *testing.T) {
	registry := visibilityRegistry(t)
	mustApply(t, registry, node.AvailabilityRules{
		Deny:  []node.AvailabilityRule{rule(denied, ""), rule("kilasflow.code", "acme")},
		Allow: []node.AvailabilityRule{rule(allowed, "globex")},
	})
	allow, deny := registry.AvailabilityRulesApplied()
	if want := []string{"kilasflow.code=acme", "kilasflow.sqlite"}; !reflect.DeepEqual(deny, want) {
		t.Errorf("deny = %v, want %v", deny, want)
	}
	if want := []string{"kilasflow.set=globex"}; !reflect.DeepEqual(allow, want) {
		t.Errorf("allow = %v, want %v", allow, want)
	}
}

// What a tenant is refused is what Compile reports: the denied node is
// node.not_available, a different code from a type that does not exist.
func TestCompileReportsADeniedBuiltinAsNotAvailable(t *testing.T) {
	registry := visibilityRegistry(t)
	mustApply(t, registry, node.AvailabilityRules{Deny: []node.AvailabilityRule{rule(allowed, "acme")}})

	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_denied", Name: "Denied",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "set", Name: "Set", Type: allowed, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"a": "b"}}},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "set", Port: "main"},
		}},
		Settings: map[string]any{},
	}
	if _, err := workflow.Compile(document, workflow.CatalogFor(registry, "globex")); err != nil {
		t.Fatalf("Compile() for an unaffected tenant error = %v", err)
	}
	_, err := workflow.Compile(document, workflow.CatalogFor(registry, "acme"))
	var validation *workflow.ValidationErrors
	if !errors.As(err, &validation) {
		t.Fatalf("Compile() for acme error = %v, want validation errors", err)
	}
	for _, issue := range validation.Issues {
		if issue.NodeID == "set" {
			if issue.Code != workflow.ErrorNodeNotAvailable {
				t.Errorf("issue code = %q, want %q", issue.Code, workflow.ErrorNodeNotAvailable)
			}
			return
		}
	}
	t.Fatalf("no issue is about the denied node: %#v", validation.Issues)
}
