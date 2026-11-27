package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/node"
)

// These tests cover the operator's half of built-in node availability: the
// nodes.allow and nodes.deny keys. Whether a type is a registered built-in, and
// what the lists do to a tenant, are the node registry's business; what is tested
// here is that the keys read from both sources, that the entry grammar mirrors
// packs.visible_to, that a malformed entry or a denied engine-required type refuses
// the boot, and that the default limits nothing.

func availabilityRules(t *testing.T, cfg Config) node.AvailabilityRules {
	t.Helper()
	rules, err := cfg.Nodes.AvailabilityRules()
	if err != nil {
		t.Fatalf("AvailabilityRules() error = %v", err)
	}
	return rules
}

// A container deployment has no mounted file, so both keys have to be settable
// from the environment, with and without a tenant on an entry.
func TestAvailabilityRulesComeFromTheEnvironment(t *testing.T) {
	t.Setenv("KILASFLOW_NODES_DENY", "kilasflow.sqlite, kilasflow.code=acme")
	t.Setenv("KILASFLOW_NODES_ALLOW", "kilasflow.set=globex")

	warnings := captureWarnings(t, func() {
		rules := availabilityRules(t, loadAbsentFile(t))
		wantDeny := []node.AvailabilityRule{{Type: "kilasflow.sqlite"}, {Type: "kilasflow.code", Tenant: "acme"}}
		wantAllow := []node.AvailabilityRule{{Type: "kilasflow.set", Tenant: "globex"}}
		if !reflect.DeepEqual(rules.Deny, wantDeny) {
			t.Errorf("Deny = %v, want %v", rules.Deny, wantDeny)
		}
		if !reflect.DeepEqual(rules.Allow, wantAllow) {
			t.Errorf("Allow = %v, want %v", rules.Allow, wantAllow)
		}
	})
	// A key that is read but not known warns, and an operator who reads that
	// warning about a key that works learns to ignore warnings.
	if strings.Contains(warnings, "nodes") {
		t.Errorf("the new keys were reported as unknown:\n%s", warnings)
	}
}

func TestAvailabilityRulesComeFromTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.yaml")
	writeVisibilityConfig(t, path, "nodes:\n  deny:\n    - kilasflow.sqlite\n    - kilasflow.code=acme\n  allow:\n    - kilasflow.set\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	rules := availabilityRules(t, cfg)
	if want := []node.AvailabilityRule{{Type: "kilasflow.sqlite"}, {Type: "kilasflow.code", Tenant: "acme"}}; !reflect.DeepEqual(rules.Deny, want) {
		t.Errorf("Deny = %v, want %v", rules.Deny, want)
	}
	if want := []node.AvailabilityRule{{Type: "kilasflow.set"}}; !reflect.DeepEqual(rules.Allow, want) {
		t.Errorf("Allow = %v, want %v", rules.Allow, want)
	}
}

// Surrounding space is what a hand-written list has; both halves are trimmed.
func TestAvailabilityRulesTrimBothHalves(t *testing.T) {
	t.Setenv("KILASFLOW_NODES_DENY", "  kilasflow.code = acme  ")

	rules := availabilityRules(t, loadAbsentFile(t))
	if want := []node.AvailabilityRule{{Type: "kilasflow.code", Tenant: "acme"}}; !reflect.DeepEqual(rules.Deny, want) {
		t.Errorf("Deny = %v, want %v", rules.Deny, want)
	}
}

// A malformed entry is refused at boot, naming its key and index: a rule that
// silently matched nothing would leave a node available that the operator meant
// to hide.
func TestAMalformedAvailabilityEntryRefusesTheBoot(t *testing.T) {
	for name, test := range map[string]struct {
		key, env, want string
	}{
		"empty type":         {"nodes.deny", "=acme", "nodes.deny[0]"},
		"empty tenant":       {"nodes.deny", "kilasflow.code=", "the tenant ID is empty"},
		"only spaces":        {"nodes.deny", " = ", "nodes.deny[0]"},
		"second separator":   {"nodes.deny", "kilasflow.code=a=b", "contains \"=\""},
		"allow empty tenant": {"nodes.allow", "kilasflow.set=", "nodes.allow[0]"},
		"allow empty type":   {"nodes.allow", "=acme", "nodes.allow[0]"},
	} {
		t.Run(name, func(t *testing.T) {
			envName := "KILASFLOW_NODES_DENY"
			if test.key == "nodes.allow" {
				envName = "KILASFLOW_NODES_ALLOW"
			}
			t.Setenv(envName, test.env)

			cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
			if err == nil {
				// Validate is what load() calls; calling it again proves the
				// refusal is in the validator rather than in the loader alone.
				if err = cfg.Validate(); err == nil {
					t.Fatalf("Load() accepted the entry %q", test.env)
				}
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %v, want it to contain %q", err, test.want)
			}
		})
	}
}

// The engine names these two built-ins, so a deny entry for one, for every tenant
// or for one, refuses the boot with a message that says why.
func TestADeniedEngineRequiredTypeRefusesTheBoot(t *testing.T) {
	for _, nodeType := range node.EngineRequiredTypes() {
		for _, entry := range []string{nodeType, nodeType + "=acme"} {
			t.Run(entry, func(t *testing.T) {
				t.Setenv("KILASFLOW_NODES_DENY", "kilasflow.sqlite,"+entry)

				cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
				if err == nil {
					err = cfg.Validate()
				}
				if err == nil {
					t.Fatalf("a config denying %q was accepted", entry)
				}
				for _, want := range []string{"nodes.deny[1]", nodeType, "cannot be denied"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error = %v, want it to contain %q", err, want)
					}
				}
			})
		}
	}
}

// Listing an engine-required type in an allow list is redundant, not harmful: it
// is always available, so the config accepts it.
func TestAnEngineRequiredTypeMayBeAllowed(t *testing.T) {
	t.Setenv("KILASFLOW_NODES_ALLOW", "kilasflow.errorTrigger")

	cfg := loadAbsentFile(t)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

// The default deployment hides nothing, so the default is empty lists that cost
// the registry nothing.
func TestTheDefaultHidesNothing(t *testing.T) {
	cfg := Default()
	if len(cfg.Nodes.Allow) != 0 || len(cfg.Nodes.Deny) != 0 {
		t.Errorf("Default().Nodes = %+v, want empty lists", cfg.Nodes)
	}
	rules := availabilityRules(t, cfg)
	if err := node.NewRegistry().ApplyAvailability(rules); err != nil {
		t.Errorf("applying the default rules error = %v, want a no-op", err)
	}
}
