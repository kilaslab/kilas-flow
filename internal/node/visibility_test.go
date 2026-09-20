package node_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// None of these tests run in parallel: TestTenantViewAddsNoAllocationToLookup
// uses testing.AllocsPerRun, which refuses to run inside a parallel test, and
// the registry is composition-time-mutable so sharing one across goroutines is
// exactly what the type forbids.

// scopedPack is a minimal pack-shaped definition. With no tenants it is
// unscoped.
func scopedPack(nodeType string, tenants ...string) node.Definition {
	return scopedPackAt(nodeType, workflow.V(1), tenants...)
}

func scopedPackAt(nodeType string, version workflow.TypeVersion, tenants ...string) node.Definition {
	return node.Definition{
		Type:        nodeType,
		Version:     version,
		DisplayName: "Scoped " + nodeType,
		Category:    "Test",
		Group:       []node.NodeGroup{node.GroupTransform},
		Inputs:      []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		ExecutorID:  "test.exec",
		VisibleTo:   tenants,
	}
}

func mustRegisterPack(t *testing.T, registry *node.Registry, definition node.Definition) {
	t.Helper()
	if err := registry.RegisterFrom(node.SourcePack, definition); err != nil {
		t.Fatalf("RegisterFrom(%s v%s) error = %v", definition.Type, definition.Version, err)
	}
}

// visibilityRegistry holds the built-ins, one pack scoped to acme and one pack
// nobody scoped.
func visibilityRegistry(t *testing.T) *node.Registry {
	t.Helper()
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	mustRegisterPack(t, registry, scopedPack("pack.acme.crm", "acme"))
	mustRegisterPack(t, registry, scopedPack("pack.shared.ping"))
	return registry
}

func typesOf(definitions []node.Definition) map[string]bool {
	seen := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		seen[definition.Type] = true
	}
	return seen
}

// The default deployment scopes nothing, and "nothing existing changes" is the
// requirement that matters most: an unscoped registry must answer every tenant,
// including one with no identity at all, exactly as the plain registry does.
func TestAnUnscopedNodeIsVisibleToEveryTenant(t *testing.T) {
	// No function-valued fields, so reflect.DeepEqual can compare definitions.
	plain := node.NewRegistry()
	mustRegisterPack(t, plain, scopedPack("pack.one"))
	mustRegisterPack(t, plain, scopedPack("pack.two"))

	full := node.NewRegistry()
	if err := nodes.RegisterAll(full); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	mustRegisterPack(t, full, scopedPack("pack.shared.ping"))

	for _, tenant := range []string{"acme", "globex", "default", ""} {
		if got, want := plain.ListFor(tenant), plain.List(); !reflect.DeepEqual(got, want) {
			t.Errorf("ListFor(%q) = %#v, want exactly List() when nothing is scoped", tenant, got)
		}
		listed, all := full.ListFor(tenant), full.List()
		if len(listed) != len(all) {
			t.Errorf("ListFor(%q) returned %d definitions, want all %d", tenant, len(listed), len(all))
		}
		if !full.VisibleTo("pack.shared.ping", tenant) || !full.VisibleTo("kilasflow.set", tenant) {
			t.Errorf("VisibleTo(%q) hid an unscoped type", tenant)
		}
		if _, found := full.ResolveFor(tenant, "pack.shared.ping", workflow.V(1)); !found {
			t.Errorf("ResolveFor(%q) did not find an unscoped type", tenant)
		}
		view := full.ForTenant(tenant)
		if _, found := view.Lookup("pack.shared.ping", workflow.V(1)); !found {
			t.Errorf("view.Lookup(%q) did not find an unscoped type", tenant)
		}
		if !view.(workflow.TypeCatalog).HasType("pack.shared.ping") {
			t.Errorf("view.HasType(%q) = false for an unscoped type", tenant)
		}
		if view.(workflow.RestrictedCatalog).Restricted("pack.shared.ping") {
			t.Errorf("view.Restricted(%q) = true for an unscoped type", tenant)
		}
	}
	if scopes := full.Scopes(); len(scopes) != 0 {
		t.Errorf("Scopes() = %v, want none", scopes)
	}
	if full.Scoped("pack.shared.ping") {
		t.Error("Scoped() = true for an unscoped type")
	}
}

func TestAScopedNodeIsVisibleOnlyToItsTenants(t *testing.T) {
	registry := visibilityRegistry(t)

	cases := []struct {
		tenant  string
		visible bool
	}{
		{"acme", true},
		{"globex", false},
		// An empty ID is an unidentified caller, never a wildcard.
		{"", false},
	}
	for _, tc := range cases {
		name := tc.tenant
		if name == "" {
			name = "(empty)"
		}
		t.Run(name, func(t *testing.T) {
			listed := typesOf(registry.ListFor(tc.tenant))
			if listed["pack.acme.crm"] != tc.visible {
				t.Errorf("ListFor lists pack.acme.crm = %v, want %v", listed["pack.acme.crm"], tc.visible)
			}
			// Everything that is not scoped stays visible to everyone.
			for _, always := range []string{"pack.shared.ping", "kilasflow.set", "kilasflow.manual"} {
				if !listed[always] {
					t.Errorf("ListFor dropped the unscoped type %s", always)
				}
			}
			if got := registry.VisibleTo("pack.acme.crm", tc.tenant); got != tc.visible {
				t.Errorf("VisibleTo = %v, want %v", got, tc.visible)
			}
			if _, found := registry.ResolveFor(tc.tenant, "pack.acme.crm", workflow.V(1)); found != tc.visible {
				t.Errorf("ResolveFor found = %v, want %v", found, tc.visible)
			}

			view := registry.ForTenant(tc.tenant)
			if _, found := view.Lookup("pack.acme.crm", workflow.V(1)); found != tc.visible {
				t.Errorf("view.Lookup found = %v, want %v", found, tc.visible)
			}
			if got := view.(workflow.TypeCatalog).HasType("pack.acme.crm"); got != tc.visible {
				t.Errorf("view.HasType = %v, want %v", got, tc.visible)
			}
			// Restricted is exactly "registered but not yours".
			if got := view.(workflow.RestrictedCatalog).Restricted("pack.acme.crm"); got == tc.visible {
				t.Errorf("view.Restricted = %v, want %v", got, !tc.visible)
			}
			// A type that does not exist is not restricted, it is unknown.
			if view.(workflow.RestrictedCatalog).Restricted("pack.acme.crmm") {
				t.Error("view.Restricted = true for a type that is not registered")
			}
			if view.(workflow.TypeCatalog).HasType("pack.acme.crmm") {
				t.Error("view.HasType = true for a type that is not registered")
			}
		})
	}

	// The registry itself is the deployment's catalogue and is never narrowed.
	if _, found := registry.Lookup("pack.acme.crm", workflow.V(1)); !found {
		t.Error("the plain registry no longer finds a scoped type; only the tenant view may narrow")
	}
	if !registry.Scoped("pack.acme.crm") {
		t.Error("Scoped(pack.acme.crm) = false")
	}
}

// Which tenants a node is scoped to is operator information. The catalogue is
// served to every tenant, so the definition must not carry the list.
func TestVisibleToIsNeverSerialised(t *testing.T) {
	registry := visibilityRegistry(t)
	definition, found := registry.Get("pack.acme.crm", workflow.V(1))
	if !found {
		t.Fatal("pack.acme.crm is not registered")
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, leaked := range []string{"acme", "visibleTo", "VisibleTo"} {
		// "acme" also appears in the type name; look for it as a tenant entry.
		if leaked == "acme" {
			if strings.Contains(string(encoded), `"acme"`) {
				t.Errorf("serialised definition leaks a tenant ID: %s", encoded)
			}
			continue
		}
		if strings.Contains(string(encoded), leaked) {
			t.Errorf("serialised definition mentions %q: %s", leaked, encoded)
		}
	}
	listed, err := json.Marshal(registry.ListFor("acme"))
	if err != nil {
		t.Fatalf("Marshal(list) error = %v", err)
	}
	if strings.Contains(string(listed), "visibleTo") || strings.Contains(string(listed), `"acme"`) {
		t.Errorf("the catalogue leaks a scope: %s", listed)
	}
}

// One node type is one thing to a user however many versions of it are
// registered, so it has one scope. Resolving downward must not become a way
// around it: a workflow written against version 1 of a scoped type resolves to
// a scoped definition just as one written against version 2 does.
func TestAScopedTypeIsScopedAtEveryVersion(t *testing.T) {
	registry := node.NewRegistry()
	mustRegisterPack(t, registry, scopedPackAt("pack.acme.crm", workflow.V(1), "acme"))
	mustRegisterPack(t, registry, scopedPackAt("pack.acme.crm", workflow.V(2), "acme"))

	for _, version := range []workflow.TypeVersion{workflow.V(1), workflow.V(2), workflow.V(3)} {
		if _, found := registry.ResolveFor("globex", "pack.acme.crm", version); found {
			t.Errorf("ResolveFor(globex) resolved version %s of a scoped type", version)
		}
		if _, found := registry.ForTenant("globex").Lookup("pack.acme.crm", version); found {
			t.Errorf("Lookup(globex) resolved version %s of a scoped type", version)
		}
	}
	// Downward resolution still works for the tenant that may see the type.
	got, found := registry.ResolveFor("acme", "pack.acme.crm", workflow.V(3))
	if !found || got.Version.Compare(workflow.V(2)) != 0 {
		t.Errorf("ResolveFor(acme, v3) = %v (found %v), want version 2", got.Version, found)
	}
	if listed := registry.ListFor("globex"); len(listed) != 0 {
		t.Errorf("ListFor(globex) = %d definitions, want none of either version", len(listed))
	}
	if listed := registry.ListFor("acme"); len(listed) != 2 {
		t.Errorf("ListFor(acme) = %d definitions, want both versions", len(listed))
	}

	t.Run("a version with a different set is refused", func(t *testing.T) {
		err := registry.RegisterFrom(node.SourcePack, scopedPackAt("pack.acme.crm", workflow.V(3), "globex"))
		if err == nil {
			t.Fatal("RegisterFrom() accepted a third version scoped to a different tenant")
		}
		// Naming both versions is what makes the message actionable, and the
		// candidate iterated first must be the lowest so the text is stable.
		for _, want := range []string{`"pack.acme.crm"`, "version 3", "version 1"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want it to contain %q", err, want)
			}
		}
		for attempt := 0; attempt < 20; attempt++ {
			again := registry.RegisterFrom(node.SourcePack, scopedPackAt("pack.acme.crm", workflow.V(3), "globex"))
			if again == nil || again.Error() != err.Error() {
				t.Fatalf("attempt %d error = %v, want the identical message every time (%q)", attempt, again, err)
			}
		}
		if _, found := registry.Get("pack.acme.crm", workflow.V(3)); found {
			t.Error("the refused version was registered anyway")
		}
	})

	t.Run("a version with no scope is refused when the others have one", func(t *testing.T) {
		err := registry.RegisterFrom(node.SourcePack, scopedPackAt("pack.acme.crm", workflow.V(3)))
		if err == nil {
			t.Fatal("RegisterFrom() accepted an unscoped version of a scoped type")
		}
		if !strings.Contains(err.Error(), "version 3") || !strings.Contains(err.Error(), "version 1") {
			t.Errorf("error = %q, want both versions named", err)
		}
	})

	t.Run("a scoped version is refused when the others have none", func(t *testing.T) {
		open := node.NewRegistry()
		mustRegisterPack(t, open, scopedPackAt("pack.open", workflow.V(1)))
		err := open.RegisterFrom(node.SourcePack, scopedPackAt("pack.open", workflow.V(2), "acme"))
		if err == nil {
			t.Fatal("RegisterFrom() accepted a scoped version beside an unscoped one")
		}
		if !strings.Contains(err.Error(), "version 2") || !strings.Contains(err.Error(), "version 1") {
			t.Errorf("error = %q, want both versions named", err)
		}
		if open.Scoped("pack.open") {
			t.Error("a refused registration left the type scoped")
		}
	})

	t.Run("the same set in another order is the same set", func(t *testing.T) {
		both := node.NewRegistry()
		mustRegisterPack(t, both, scopedPackAt("pack.x", workflow.V(1), "b", "a"))
		if err := both.RegisterFrom(node.SourcePack, scopedPackAt("pack.x", workflow.V(2), "a", "b", "a")); err != nil {
			t.Fatalf("RegisterFrom() refused an equal set written differently: %v", err)
		}
	})
}

// Some built-ins are named by the engine itself (the sub-workflow trigger, the
// error trigger). Hiding one from a tenant would break that tenant's runs from
// the inside, so the door is closed at both entrances.
func TestABuiltinCannotBeScoped(t *testing.T) {
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	t.Run("Register with a scope is refused", func(t *testing.T) {
		definition := scopedPack("kilasflow.scoped", "acme")
		if err := node.NewRegistry().Register(definition); err == nil {
			t.Fatal("Register() accepted a built-in with VisibleTo")
		}
		other := scopedPack("test.builtin.scoped", "acme")
		if err := node.NewRegistry().Register(other); err == nil {
			t.Fatal("Register() accepted a built-in outside the kilasflow namespace with VisibleTo")
		}
	})

	t.Run("ScopeTo a kilasflow type is refused", func(t *testing.T) {
		err := registry.ScopeTo("kilasflow.set", []string{"acme"})
		if err == nil {
			t.Fatal("ScopeTo() scoped a kilasflow.* node")
		}
		if !strings.Contains(err.Error(), "kilasflow.set") {
			t.Errorf("error = %q, want it to name the type", err)
		}
		if registry.Scoped("kilasflow.set") || !registry.VisibleTo("kilasflow.set", "globex") {
			t.Error("a refused ScopeTo changed the built-in's visibility")
		}
	})

	t.Run("ScopeTo any SourceBuiltin type is refused", func(t *testing.T) {
		own := node.NewRegistry()
		if err := own.Register(scopedPack("test.builtin.plain")); err != nil {
			t.Fatalf("Register() error = %v", err)
		}
		if err := own.ScopeTo("test.builtin.plain", []string{"acme"}); err == nil {
			t.Fatal("ScopeTo() scoped a SourceBuiltin node outside the kilasflow namespace")
		}
		if own.Scoped("test.builtin.plain") {
			t.Error("a refused ScopeTo left the built-in scoped")
		}
	})

	t.Run("a pack may be scoped", func(t *testing.T) {
		own := node.NewRegistry()
		mustRegisterPack(t, own, scopedPack("pack.fine"))
		if err := own.ScopeTo("pack.fine", []string{"acme"}); err != nil {
			t.Fatalf("ScopeTo() refused a pack: %v", err)
		}
	})
}

func TestVisibleToRejectsMalformedTenants(t *testing.T) {
	bad := map[string]string{
		"empty":                    "",
		"leading space":            " a",
		"trailing space":           "a ",
		"inner space":              "a b",
		"tab":                      "a\tb",
		"newline":                  "a\nb",
		"comma":                    "a,b",
		"equals":                   "a=b",
		"control character":        "a\x01b",
		"delete":                   "a\x7fb",
		"unicode space":            "a b",
		"invalid utf-8":            "a\xffb",
		"longer than 128 bytes":    strings.Repeat("a", 129),
		"only whitespace":          "   ",
		"a line separator":         "a b",
		"the reserved separators":  ",=",
		"a leading equals":         "=acme",
		"a trailing comma":         "acme,",
		"much longer than allowed": strings.Repeat("tenant-", 100),
	}
	for name, tenant := range bad {
		t.Run("refuses "+name, func(t *testing.T) {
			if err := node.CheckTenantID(tenant); err == nil {
				t.Errorf("CheckTenantID(%q) accepted it", tenant)
			}
			registry := node.NewRegistry()
			if err := registry.RegisterFrom(node.SourcePack, scopedPack("pack.bad", "acme", tenant)); err == nil {
				t.Errorf("RegisterFrom() accepted VisibleTo containing %q", tenant)
			}
			if _, found := registry.Get("pack.bad", workflow.V(1)); found {
				t.Error("a refused registration left the definition behind")
			}
			good := node.NewRegistry()
			mustRegisterPack(t, good, scopedPack("pack.good"))
			if err := good.ScopeTo("pack.good", []string{tenant}); err == nil {
				t.Errorf("ScopeTo() accepted %q", tenant)
			}
			if good.Scoped("pack.good") {
				t.Error("a refused ScopeTo left the type scoped")
			}
		})
	}

	for name, tenant := range map[string]string{
		"a plain word":            "acme",
		"exactly 128 bytes":       strings.Repeat("a", 128),
		"the admin pattern":       "a0_b-c",
		"uppercase":               "Acme",
		"dots and colons":         "acme.example:8080",
		"non-ASCII letters":       "tenant-é",
		"the default tenant":      "default",
		"the operator tenant":     "operator",
		"a looser bootstrap name": "Tenant/One",
	} {
		if err := node.CheckTenantID(tenant); err != nil {
			t.Errorf("CheckTenantID(%s = %q) = %v, want accepted (the grammar is looser than the admin pattern on purpose)", name, tenant, err)
		}
	}

	t.Run("duplicates collapse and the set is sorted", func(t *testing.T) {
		registry := node.NewRegistry()
		mustRegisterPack(t, registry, scopedPack("pack.dupes", "globex", "acme", "globex", "acme", "initech"))
		definition, _ := registry.Get("pack.dupes", workflow.V(1))
		if want := []string{"acme", "globex", "initech"}; !reflect.DeepEqual(definition.VisibleTo, want) {
			t.Errorf("VisibleTo = %v, want %v", definition.VisibleTo, want)
		}
		if got := registry.Scopes()["pack.dupes"]; !reflect.DeepEqual(got, []string{"acme", "globex", "initech"}) {
			t.Errorf("Scopes()[pack.dupes] = %v", got)
		}
	})

	t.Run("nil and empty mean unscoped and are stored as nil", func(t *testing.T) {
		registry := node.NewRegistry()
		mustRegisterPack(t, registry, scopedPack("pack.nil"))
		empty := scopedPack("pack.empty")
		empty.VisibleTo = []string{}
		mustRegisterPack(t, registry, empty)
		for _, nodeType := range []string{"pack.nil", "pack.empty"} {
			definition, found := registry.Get(nodeType, workflow.V(1))
			if !found {
				t.Fatalf("%s is not registered", nodeType)
			}
			if definition.VisibleTo != nil {
				t.Errorf("%s VisibleTo = %#v, want nil so deep-equality with an unscoped definition holds", nodeType, definition.VisibleTo)
			}
			if registry.Scoped(nodeType) || !registry.VisibleTo(nodeType, "anyone") {
				t.Errorf("%s was treated as scoped", nodeType)
			}
		}
	})

	t.Run("the caller's slice is copied, not aliased", func(t *testing.T) {
		mine := []string{"acme", "globex"}
		registry := node.NewRegistry()
		mustRegisterPack(t, registry, scopedPack("pack.alias", mine...))
		mine[0] = "mallory"
		if !registry.VisibleTo("pack.alias", "acme") || registry.VisibleTo("pack.alias", "mallory") {
			t.Error("mutating the slice handed to Register changed the registry")
		}
		got, _ := registry.Get("pack.alias", workflow.V(1))
		got.VisibleTo[0] = "mallory"
		if !registry.VisibleTo("pack.alias", "acme") || registry.VisibleTo("pack.alias", "mallory") {
			t.Error("mutating a Get() result changed the registry")
		}
	})
}

func TestScopeToRefusesAnUnknownTypeAndChangesNothing(t *testing.T) {
	registry := visibilityRegistry(t)
	before := registry.Scopes()

	err := registry.ScopeTo("pack.nope", []string{"acme"})
	if err == nil {
		t.Fatal("ScopeTo() accepted a type that is not registered")
	}
	if !strings.Contains(err.Error(), "pack.nope") {
		t.Errorf("error = %q, want it to name the type", err)
	}
	if after := registry.Scopes(); !reflect.DeepEqual(before, after) {
		t.Errorf("Scopes() changed from %v to %v after a refused ScopeTo", before, after)
	}
	if registry.Scoped("pack.nope") {
		t.Error("a type that was never registered is now scoped")
	}

	// An override with no tenants would read as "nobody" or as "everybody", and
	// neither is a safe guess.
	for _, empty := range [][]string{nil, {}} {
		if err := registry.ScopeTo("pack.shared.ping", empty); err == nil {
			t.Errorf("ScopeTo(%#v) accepted an empty tenant list", empty)
		}
	}
	if registry.Scoped("pack.shared.ping") {
		t.Error("an empty override scoped the type")
	}
}

func TestScopeToReplacesTheDeclaredScope(t *testing.T) {
	registry := node.NewRegistry()
	mustRegisterPack(t, registry, scopedPackAt("pack.acme.crm", workflow.V(1), "acme"))
	mustRegisterPack(t, registry, scopedPackAt("pack.acme.crm", workflow.V(2), "acme"))

	if err := registry.ScopeTo("pack.acme.crm", []string{"globex"}); err != nil {
		t.Fatalf("ScopeTo() error = %v", err)
	}
	if registry.VisibleTo("pack.acme.crm", "acme") {
		t.Error("acme still sees the type: the override must replace the manifest's set, not merge with it")
	}
	if !registry.VisibleTo("pack.acme.crm", "globex") {
		t.Error("globex does not see the type after the override named it")
	}
	// Both the index and every stored definition carry the new set, or Get and
	// the lookup path would disagree.
	for _, version := range []workflow.TypeVersion{workflow.V(1), workflow.V(2)} {
		definition, found := registry.Get("pack.acme.crm", version)
		if !found {
			t.Fatalf("version %s vanished", version)
		}
		if !reflect.DeepEqual(definition.VisibleTo, []string{"globex"}) {
			t.Errorf("version %s VisibleTo = %v, want [globex]", version, definition.VisibleTo)
		}
	}
	if got := registry.Scopes()["pack.acme.crm"]; !reflect.DeepEqual(got, []string{"globex"}) {
		t.Errorf("Scopes() = %v, want [globex]", got)
	}
	if _, found := registry.ResolveFor("acme", "pack.acme.crm", workflow.V(2)); found {
		t.Error("acme still resolves the type")
	}

	t.Run("an override can widen the set", func(t *testing.T) {
		if err := registry.ScopeTo("pack.acme.crm", []string{"globex", "acme", "initech"}); err != nil {
			t.Fatalf("ScopeTo() error = %v", err)
		}
		for _, tenant := range []string{"acme", "globex", "initech"} {
			if !registry.VisibleTo("pack.acme.crm", tenant) {
				t.Errorf("%s does not see the widened type", tenant)
			}
		}
		if registry.VisibleTo("pack.acme.crm", "hooli") {
			t.Error("an unlisted tenant sees the type")
		}
	})

	t.Run("an override can scope a type its manifest left open", func(t *testing.T) {
		open := node.NewRegistry()
		mustRegisterPack(t, open, scopedPack("pack.open"))
		if !open.VisibleTo("pack.open", "globex") {
			t.Fatal("precondition: the type should start visible to everyone")
		}
		if err := open.ScopeTo("pack.open", []string{"acme"}); err != nil {
			t.Fatalf("ScopeTo() error = %v", err)
		}
		if open.VisibleTo("pack.open", "globex") || !open.VisibleTo("pack.open", "acme") {
			t.Error("the override did not narrow an unscoped type to acme")
		}
	})
}

func TestApplyVisibilityIsAllOrNothing(t *testing.T) {
	newRegistry := func() *node.Registry {
		registry := node.NewRegistry()
		if err := nodes.RegisterAll(registry); err != nil {
			t.Fatalf("RegisterAll() error = %v", err)
		}
		mustRegisterPack(t, registry, scopedPack("pack.a", "acme"))
		mustRegisterPack(t, registry, scopedPack("pack.b"))
		mustRegisterPack(t, registry, scopedPack("pack.c"))
		return registry
	}

	bad := map[string]map[string][]string{
		"a bad tenant":    {"pack.a": {"globex"}, "pack.b": {"initech"}, "pack.c": {"a b"}},
		"an unknown type": {"pack.a": {"globex"}, "pack.b": {"initech"}, "pack.nope": {"acme"}},
		"a built-in":      {"pack.a": {"globex"}, "pack.b": {"initech"}, "kilasflow.set": {"acme"}},
		"an empty list":   {"pack.a": {"globex"}, "pack.b": {"initech"}, "pack.c": {}},
	}
	for name, grants := range bad {
		t.Run(name, func(t *testing.T) {
			registry := newRegistry()
			before := registry.Scopes()
			if err := registry.ApplyVisibility(grants); err == nil {
				t.Fatal("ApplyVisibility() accepted the bad entry")
			}
			if after := registry.Scopes(); !reflect.DeepEqual(before, after) {
				t.Errorf("Scopes() = %v after a refused call, want the untouched %v", after, before)
			}
			if !registry.VisibleTo("pack.a", "acme") || registry.VisibleTo("pack.a", "globex") {
				t.Error("pack.a was changed although the call was refused")
			}
			if !registry.VisibleTo("pack.b", "globex") {
				t.Error("pack.b was scoped although the call was refused")
			}
			definition, _ := registry.Get("pack.b", workflow.V(1))
			if definition.VisibleTo != nil {
				t.Errorf("pack.b's stored definition = %v, want it untouched", definition.VisibleTo)
			}
		})
	}

	t.Run("a good set is applied in full", func(t *testing.T) {
		registry := newRegistry()
		err := registry.ApplyVisibility(map[string][]string{
			"pack.a": {"globex"},
			"pack.b": {"initech", "acme"},
		})
		if err != nil {
			t.Fatalf("ApplyVisibility() error = %v", err)
		}
		want := map[string][]string{"pack.a": {"globex"}, "pack.b": {"acme", "initech"}}
		if got := registry.Scopes(); !reflect.DeepEqual(got, want) {
			t.Errorf("Scopes() = %v, want %v", got, want)
		}
	})

	t.Run("nothing to apply is a no-op", func(t *testing.T) {
		registry := newRegistry()
		before := registry.Scopes()
		for _, grants := range []map[string][]string{nil, {}} {
			if err := registry.ApplyVisibility(grants); err != nil {
				t.Fatalf("ApplyVisibility(%v) error = %v", grants, err)
			}
		}
		if after := registry.Scopes(); !reflect.DeepEqual(before, after) {
			t.Errorf("Scopes() changed from %v to %v", before, after)
		}
	})

	t.Run("the error names the entry that was refused", func(t *testing.T) {
		registry := newRegistry()
		err := registry.ApplyVisibility(map[string][]string{"pack.nope": {"acme"}})
		if err == nil || !strings.Contains(err.Error(), "pack.nope") {
			t.Fatalf("error = %v, want it to name pack.nope", err)
		}
		err = registry.ApplyVisibility(map[string][]string{"pack.c": {"a b"}})
		if err == nil || !strings.Contains(err.Error(), "pack.c") {
			t.Fatalf("error = %v, want it to name pack.c", err)
		}
	})
}

func TestScopesSnapshotIsACopy(t *testing.T) {
	registry := node.NewRegistry()
	mustRegisterPack(t, registry, scopedPack("pack.b", "globex", "acme"))
	mustRegisterPack(t, registry, scopedPack("pack.a", "initech"))
	mustRegisterPack(t, registry, scopedPack("pack.open"))

	snapshot := registry.Scopes()
	want := map[string][]string{"pack.a": {"initech"}, "pack.b": {"acme", "globex"}}
	if !reflect.DeepEqual(snapshot, want) {
		t.Fatalf("Scopes() = %v, want %v with each tenant list sorted", snapshot, want)
	}

	snapshot["pack.b"][0] = "mallory"
	snapshot["pack.injected"] = []string{"mallory"}
	delete(snapshot, "pack.a")
	if again := registry.Scopes(); !reflect.DeepEqual(again, want) {
		t.Errorf("Scopes() after mutating a snapshot = %v, want %v: the snapshot aliases the registry", again, want)
	}
	if registry.VisibleTo("pack.b", "mallory") || registry.Scoped("pack.injected") {
		t.Error("mutating a snapshot changed what the registry enforces")
	}

	if !registry.Scoped("pack.a") || !registry.Scoped("pack.b") {
		t.Error("Scoped() = false for a scoped type")
	}
	if registry.Scoped("pack.open") || registry.Scoped("pack.absent") {
		t.Error("Scoped() = true for a type that is unscoped or absent")
	}
}

// A workflow.Catalog held in an interface can be a typed nil: the API handlers
// keep whichever registry they were given, and a nil one must answer as an empty
// catalogue rather than panic in the middle of a request.
func TestAZeroValueAndNilRegistryAreSafe(t *testing.T) {
	var nilRegistry *node.Registry
	var zero node.Registry

	for name, registry := range map[string]*node.Registry{"nil": nilRegistry, "zero value": &zero} {
		t.Run(name, func(t *testing.T) {
			view := registry.ForTenant("x")
			if view == nil {
				t.Fatal("ForTenant() returned nil")
			}
			if _, found := view.Lookup("pack.any", workflow.V(1)); found {
				t.Error("an empty registry found a type")
			}
			if view.(workflow.TypeCatalog).HasType("pack.any") {
				t.Error("an empty registry reports a type")
			}
			if view.(workflow.RestrictedCatalog).Restricted("pack.any") {
				t.Error("an empty registry reports a restricted type")
			}
			if !registry.VisibleTo("pack.any", "x") {
				t.Error("VisibleTo() = false on a registry with no scopes: nothing is hidden")
			}
			if registry.Scoped("pack.any") {
				t.Error("Scoped() = true on an empty registry")
			}
			if got := registry.ListFor("x"); got == nil || len(got) != 0 {
				t.Errorf("ListFor() = %#v, want an empty non-nil list", got)
			}
			if _, found := registry.ResolveFor("x", "pack.any", workflow.V(1)); found {
				t.Error("ResolveFor() found a type")
			}
			if scopes := registry.Scopes(); len(scopes) != 0 {
				t.Errorf("Scopes() = %v, want none", scopes)
			}
			if err := registry.ScopeTo("pack.any", []string{"acme"}); err == nil {
				t.Error("ScopeTo() succeeded on a registry that holds no such type")
			}
			if err := registry.ApplyVisibility(map[string][]string{"pack.any": {"acme"}}); err == nil {
				t.Error("ApplyVisibility() succeeded on a registry that holds no such type")
			}
			if err := registry.ApplyVisibility(nil); err != nil {
				t.Errorf("ApplyVisibility(nil) = %v, want a no-op", err)
			}
		})
	}

	t.Run("a typed nil registry through workflow.CatalogFor", func(t *testing.T) {
		var catalog workflow.Catalog = nilRegistry
		view := workflow.CatalogFor(catalog, "x")
		if _, found := view.Lookup("pack.any", workflow.V(1)); found {
			t.Error("a typed-nil catalogue found a type")
		}
	})

	t.Run("an all-scoped registry lists nothing to a stranger, as an empty list", func(t *testing.T) {
		registry := node.NewRegistry()
		mustRegisterPack(t, registry, scopedPack("pack.only", "acme"))
		got := registry.ListFor("globex")
		if got == nil || len(got) != 0 {
			t.Errorf("ListFor(globex) = %#v, want an empty non-nil list", got)
		}
		encoded, err := json.Marshal(got)
		if err != nil || string(encoded) != "[]" {
			t.Errorf("json = %s (%v), want [] rather than null", encoded, err)
		}
	})
}

// The tenant view sits on the path the compiler walks once per node of every
// document. Its own cost must be a map lookup, not an allocation.
func TestTenantViewAddsNoAllocationToLookup(t *testing.T) {
	registry := visibilityRegistry(t)
	version := workflow.V(1)

	for _, tc := range []struct {
		name, nodeType, tenant string
	}{
		{"an unscoped type", "pack.shared.ping", "globex"},
		{"a built-in", "kilasflow.set", "globex"},
		{"a scoped type its tenant may see", "pack.acme.crm", "acme"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view := registry.ForTenant(tc.tenant)
			if _, found := view.Lookup(tc.nodeType, version); !found {
				t.Fatalf("view.Lookup(%s) found nothing", tc.nodeType)
			}
			raw := testing.AllocsPerRun(200, func() {
				if _, found := registry.Lookup(tc.nodeType, version); !found {
					t.Fatal("registry.Lookup found nothing")
				}
			})
			viaView := testing.AllocsPerRun(200, func() {
				if _, found := view.Lookup(tc.nodeType, version); !found {
					t.Fatal("view.Lookup found nothing")
				}
			})
			if viaView > raw {
				t.Errorf("view.Lookup allocates %v times per call, the registry's own Lookup %v: the view must add none", viaView, raw)
			}
		})
	}

	t.Run("a hidden type is refused without resolving it", func(t *testing.T) {
		view := registry.ForTenant("globex")
		hidden := testing.AllocsPerRun(200, func() {
			if _, found := view.Lookup("pack.acme.crm", version); found {
				t.Fatal("view.Lookup found a hidden type")
			}
		})
		if hidden != 0 {
			t.Errorf("a refused lookup allocates %v times, want none: it should not resolve and clone the definition first", hidden)
		}
	})
}

// The narrowed view must not be re-widenable: whoever holds it can only ever
// see what it was narrowed to.
func TestATenantViewCannotBeWidened(t *testing.T) {
	registry := visibilityRegistry(t)
	view := registry.ForTenant("globex")
	if _, ok := view.(workflow.TenantScoper); ok {
		t.Fatal("a tenant view implements TenantScoper, so CatalogFor could re-scope it")
	}
	widened := workflow.CatalogFor(view, "acme")
	if _, found := widened.Lookup("pack.acme.crm", workflow.V(1)); found {
		t.Error("asking a globex view to scope to acme exposed acme's node")
	}
}
