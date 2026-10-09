package node_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// A tool form is the same node offered to an agent: it runs its base's
// executor with its base's credentials. A tenant that may not see the base
// must not reach it through the tool, so the two share one scope, always.

func toolFormOf(base node.Definition, toolType string) node.Definition {
	tool := base
	tool.Type = toolType
	tool.DisplayName = base.DisplayName + " Tool"
	tool.Inputs = nil
	tool.Outputs = []workflow.Port{{Name: "tool", Kind: workflow.ConnectionTool}}
	tool.ToolFormOf = base.Type
	return tool
}

func TestAToolFormIsRefusedUnlessItsBaseIsRegistered(t *testing.T) {
	registry := node.NewRegistry()
	err := registry.RegisterFrom(node.SourcePack, toolFormOf(scopedPack("pack.acme.crm"), "pack.acme.crmTool"))
	if err == nil || !strings.Contains(err.Error(), `"pack.acme.crm"`) {
		t.Fatalf("RegisterFrom(tool before base) error = %v, want a refusal naming the base", err)
	}
}

func TestAToolFormWhoseScopeDiffersFromItsBaseIsRefused(t *testing.T) {
	registry := node.NewRegistry()
	base := scopedPack("pack.acme.crm", "acme")
	mustRegisterPack(t, registry, base)

	widened := toolFormOf(base, "pack.acme.crmTool")
	widened.VisibleTo = nil
	err := registry.RegisterFrom(node.SourcePack, widened)
	if err == nil || !strings.Contains(err.Error(), "pack.acme.crm") || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("RegisterFrom(unscoped tool of a scoped base) error = %v, want a scope refusal naming the base", err)
	}

	other := toolFormOf(base, "pack.acme.crmTool")
	other.VisibleTo = []string{"globex"}
	if err := registry.RegisterFrom(node.SourcePack, other); err == nil {
		t.Fatal("RegisterFrom(tool scoped to another tenant) succeeded")
	}

	mustRegisterPack(t, registry, toolFormOf(base, "pack.acme.crmTool"))
	if registry.VisibleTo("pack.acme.crmTool", "globex") || !registry.VisibleTo("pack.acme.crmTool", "acme") {
		t.Fatal("the tool form does not carry its base's scope")
	}
}

func TestScopingABaseAlsoScopesItsToolForm(t *testing.T) {
	registry := node.NewRegistry()
	base := scopedPack("pack.telegramish")
	mustRegisterPack(t, registry, base)
	mustRegisterPack(t, registry, toolFormOf(base, "pack.telegramishTool"))

	if err := registry.ApplyVisibility(map[string][]string{"pack.telegramish": {"acme"}}); err != nil {
		t.Fatalf("ApplyVisibility() error = %v", err)
	}
	for _, nodeType := range []string{"pack.telegramish", "pack.telegramishTool"} {
		if registry.VisibleTo(nodeType, "globex") {
			t.Errorf("%s is visible to a tenant outside the base's grant", nodeType)
		}
		if !registry.VisibleTo(nodeType, "acme") {
			t.Errorf("%s is hidden from the tenant the base was granted to", nodeType)
		}
		if _, found := registry.ResolveFor("globex", nodeType, workflow.V(1)); found {
			t.Errorf("%s resolves for a tenant outside the grant", nodeType)
		}
	}
	tool, _ := registry.Get("pack.telegramishTool", workflow.V(1))
	if len(tool.VisibleTo) != 1 || tool.VisibleTo[0] != "acme" {
		t.Fatalf("tool VisibleTo = %v, want [acme]", tool.VisibleTo)
	}
	if typesOf(registry.ListFor("globex"))["pack.telegramishTool"] {
		t.Fatal("ListFor(globex) offers the tool form of a base it cannot see")
	}
}

func TestAGrantNamingAToolFormIsRefusedAndNamesTheBase(t *testing.T) {
	registry := node.NewRegistry()
	base := scopedPack("pack.telegramish")
	mustRegisterPack(t, registry, base)
	mustRegisterPack(t, registry, toolFormOf(base, "pack.telegramishTool"))

	err := registry.ApplyVisibility(map[string][]string{"pack.telegramishTool": {"acme"}})
	if err == nil || !strings.Contains(err.Error(), `"pack.telegramish"`) {
		t.Fatalf("ApplyVisibility(tool form) error = %v, want a refusal naming the base", err)
	}
	if registry.Scoped("pack.telegramishTool") || registry.Scoped("pack.telegramish") {
		t.Fatal("a refused grant changed the registry")
	}
}
