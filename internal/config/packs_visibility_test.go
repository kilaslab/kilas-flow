package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/node"
)

// These tests cover the operator's half of node visibility: the packs.visible_to
// key that replaces what a pack manifest declared. The tenant grammar and the
// effect on the registry are node's business; what is tested here is that the
// key reads from both sources, that a malformed entry refuses the boot, and that
// the default deploys exactly as it did before the key existed.

// writeVisibilityConfig writes a config file for one test, so a table of cases
// does not repeat the same three lines of file handling.
func writeVisibilityConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func loadAbsentFile(t *testing.T) Config {
	t.Helper()
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return cfg
}

func visibilityGrants(t *testing.T, cfg Config) map[string][]string {
	t.Helper()
	grants, err := cfg.Packs.VisibilityGrants()
	if err != nil {
		t.Fatalf("VisibilityGrants() error = %v", err)
	}
	return grants
}

// A container deployment has no mounted file, so the key has to be settable
// from the environment — including two grants for one type, which is the shape
// a WAHA install needs for pack.waha and pack.wahaTrigger.
func TestVisibilityGrantsComeFromTheEnvironment(t *testing.T) {
	t.Setenv("KILASFLOW_PACKS_VISIBLE_TO", "pack.telegram=acme, pack.telegram=globex,pack.waha=acme")

	warnings := captureWarnings(t, func() {
		cfg := loadAbsentFile(t)
		grants := visibilityGrants(t, cfg)
		want := map[string][]string{
			"pack.telegram": {"acme", "globex"},
			"pack.waha":     {"acme"},
		}
		if !reflect.DeepEqual(grants, want) {
			t.Errorf("VisibilityGrants() = %v, want %v", grants, want)
		}
	})
	// A key that is read but not known warns, and an operator who reads that
	// warning about a key that works learns to ignore warnings.
	if strings.Contains(warnings, "visible_to") {
		t.Errorf("the new key was reported as unknown:\n%s", warnings)
	}
}

func TestVisibilityGrantsComeFromTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "packs.yaml")
	body := "packs:\n  visible_to:\n    - pack.telegram=acme\n    - pack.waha=acme\n    - pack.waha=globex\n"
	writeVisibilityConfig(t, path, body)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := map[string][]string{
		"pack.telegram": {"acme"},
		"pack.waha":     {"acme", "globex"},
	}
	if got := visibilityGrants(t, cfg); !reflect.DeepEqual(got, want) {
		t.Errorf("VisibilityGrants() = %v, want %v", got, want)
	}
}

// One grant is the common case, and a one-element list is the shape an
// environment variable with no comma produces.
func TestASingleGrantIsOneEntry(t *testing.T) {
	t.Setenv("KILASFLOW_PACKS_VISIBLE_TO", "pack.telegram=acme")

	cfg := loadAbsentFile(t)
	if got, want := cfg.Packs.VisibleTo, []string{"pack.telegram=acme"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("VisibleTo = %v, want %v", got, want)
	}
	if got, want := visibilityGrants(t, cfg), map[string][]string{"pack.telegram": {"acme"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("VisibilityGrants() = %v, want %v", got, want)
	}
}

// Surrounding space is what a hand-written list has; the two halves of a grant
// are trimmed, and a type is never silently accepted as a tenant.
func TestVisibilityGrantsTrimBothHalves(t *testing.T) {
	t.Setenv("KILASFLOW_PACKS_VISIBLE_TO", "  pack.telegram = acme  ")

	cfg := loadAbsentFile(t)
	if got, want := visibilityGrants(t, cfg), map[string][]string{"pack.telegram": {"acme"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("VisibilityGrants() = %v, want %v", got, want)
	}
}

// A malformed entry is refused at boot, naming its index: a scope that silently
// matched nothing would hide a node from everyone, including the tenant it was
// meant for.
func TestAMalformedOverrideRefusesTheBoot(t *testing.T) {
	for name, entry := range map[string]string{
		"no separator": "pack.telegram",
		"empty type":   "=acme",
		"empty tenant": "pack.telegram=",
		"only spaces":  " = ",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("KILASFLOW_PACKS_VISIBLE_TO", entry)

			cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
			if err == nil {
				// Validate is what load() calls; calling it again proves the
				// refusal is in the validator rather than in the loader alone.
				if err = cfg.Validate(); err == nil {
					t.Fatalf("Load() accepted the entry %q", entry)
				}
			}
			if !strings.Contains(err.Error(), "packs.visible_to[0]") {
				t.Errorf("error = %v, want it to name packs.visible_to[0]", err)
			}
		})
	}
}

// The default deployment scopes nothing, so the default must be an empty list
// that costs the registry nothing.
func TestTheDefaultScopesNothing(t *testing.T) {
	cfg := Default()
	if len(cfg.Packs.VisibleTo) != 0 {
		t.Errorf("Default().Packs.VisibleTo = %v, want none", cfg.Packs.VisibleTo)
	}
	grants := visibilityGrants(t, cfg)
	if len(grants) != 0 {
		t.Errorf("VisibilityGrants() = %v, want none", grants)
	}
	if err := node.NewRegistry().ApplyVisibility(grants); err != nil {
		t.Errorf("applying the default grants error = %v, want a no-op", err)
	}
}

func TestAMalformedOverrideIsReportedWhateverTheSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "packs.yaml")
	writeVisibilityConfig(t, path, "packs:\n  visible_to:\n    - pack.telegram=acme\n    - pack.waha\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() accepted a file holding a malformed entry")
	}
	if !strings.Contains(err.Error(), "packs.visible_to[1]") {
		t.Errorf("error = %v, want it to name the second entry", err)
	}
}
