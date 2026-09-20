package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/embed"
)

// writeEmbedConfig writes a YAML file into a temp dir and returns its path.
func writeEmbedConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestEmbedSessionTTLIsRefusedOutsideItsRange(t *testing.T) {
	refused := []struct {
		name string
		ttl  time.Duration
	}{
		{"zero", 0},
		{"negative", -time.Second},
		// A bare number in YAML decodes as nanoseconds: `session_ttl: 900`.
		{"a bare YAML number", 900 * time.Nanosecond},
		{"just under a second", 999 * time.Millisecond},
		{"over the hard cap", embed.MaxLifetime + time.Second},
	}
	for _, test := range refused {
		t.Run("refuses "+test.name, func(t *testing.T) {
			cfg := Default()
			cfg.Embed.SessionTTL = test.ttl
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("Validate() accepted embed.session_ttl = %s", test.ttl)
			}
			if !strings.Contains(err.Error(), "embed.session_ttl") {
				t.Errorf("Validate() error = %q, want it to name embed.session_ttl", err)
			}
		})
	}

	for _, ttl := range []time.Duration{embed.MaxLifetime, time.Minute, time.Second} {
		cfg := Default()
		cfg.Embed.SessionTTL = ttl
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() rejected embed.session_ttl = %s: %v", ttl, err)
		}
	}
}

func TestEmbedSessionTTLDefaultMatchesTheIssuerDefault(t *testing.T) {
	// config spells its default out so config.go needs no import of the embed
	// package; this pins the two together the way the SQL ceilings are pinned to
	// the node package. Without it, changing one silently changes what an
	// unconfigured deployment mints.
	if got := Default().Embed.SessionTTL; got != embed.DefaultLifetime {
		t.Errorf("Default().Embed.SessionTTL = %s, want embed.DefaultLifetime = %s", got, embed.DefaultLifetime)
	}
}

func TestEmbedSessionTTLIsReachableFromYAMLAndTheEnvironment(t *testing.T) {
	path := writeEmbedConfig(t, "embed:\n  session_ttl: 5m\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Embed.SessionTTL; got != 5*time.Minute {
		t.Fatalf("embed.session_ttl from YAML = %s, want 5m", got)
	}

	// The environment wins over the file, as for every key.
	t.Setenv("KILASFLOW_EMBED_SESSION_TTL", "10m")
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() with the env var error = %v", err)
	}
	if got := cfg.Embed.SessionTTL; got != 10*time.Minute {
		t.Fatalf("embed.session_ttl from the environment = %s, want 10m", got)
	}

	t.Setenv("KILASFLOW_EMBED_SESSION_TTL", "45m")
	if _, err = Load(path); err == nil || !strings.Contains(err.Error(), "embed.session_ttl") {
		t.Fatalf("Load() with 45m error = %v, want it to refuse and name embed.session_ttl", err)
	}
}

func TestABareNumberForTheEmbedSessionTTLIsRefused(t *testing.T) {
	// The loader reads an unquoted YAML number as nanoseconds without a word:
	// `session_ttl: 900` is 900ns, a token that expires before it is returned.
	// Refusing it here, with the unit hint, is what turns that silent lie into a
	// startup error an operator can act on.
	_, err := Load(writeEmbedConfig(t, "embed:\n  session_ttl: 900\n"))
	if err == nil {
		t.Fatal("Load() accepted embed.session_ttl: 900")
	}
	for _, want := range []string{"embed.session_ttl", "unit"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Load() error = %q, want it to contain %q", err, want)
		}
	}
}

func TestBrandingDefaultsToNothingSoEmbedsStayHeaderless(t *testing.T) {
	// The editor draws a header bar as soon as a name or a logo is present, so
	// a non-empty default would add one to every white-label embed that already
	// exists. branding.name used to default to "KilasFlow".
	if got := Default().Branding; got != (Branding{}) {
		t.Errorf("Default().Branding = %#v, want the zero value: the header is opt-in", got)
	}
}

func TestDeploymentBrandingIsRefusedWhenItCouldNotBeServed(t *testing.T) {
	refused := []struct {
		name     string
		branding Branding
	}{
		{"markup in the name", Branding{Name: "<b>x</b>"}},
		{"an http logo", Branding{Logo: "http://x.example/l.png"}},
		{"a javascript logo", Branding{Logo: "javascript:alert(1)"}},
		{"a relative logo", Branding{Logo: "/rel.png"}},
	}
	for _, test := range refused {
		cfg := Default()
		cfg.Branding = test.branding
		err := cfg.Validate()
		if err == nil {
			t.Errorf("Validate() accepted %s: %#v", test.name, test.branding)
			continue
		}
		// A bad deployment value must fail boot naming the keys, never 422 the
		// first mint of every session the deployment hosts.
		if !strings.Contains(err.Error(), "branding") {
			t.Errorf("Validate() error for %s = %q, want it to name branding.name / branding.logo", test.name, err)
		}
	}

	for _, accepted := range []Branding{{}, {Name: "Acme Flows", Logo: "https://cdn.example/l.png"}} {
		cfg := Default()
		cfg.Branding = accepted
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() rejected valid branding %#v: %v", accepted, err)
		}
	}
}

func TestBrandingIsReachableFromYAMLAndTheEnvironment(t *testing.T) {
	path := writeEmbedConfig(t, "branding:\n  name: Acme Flows\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Branding.Name; got != "Acme Flows" {
		t.Fatalf("branding.name from YAML = %q, want Acme Flows", got)
	}

	t.Setenv("KILASFLOW_BRANDING_LOGO", "https://cdn.example/l.png")
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() with the env var error = %v", err)
	}
	if got := cfg.Branding.Logo; got != "https://cdn.example/l.png" {
		t.Fatalf("branding.logo from the environment = %q, want the CDN URL", got)
	}
}

func TestRemovedBrandingKeysAreReportedAsIgnored(t *testing.T) {
	// branding.favicon and branding.powered_by were removed because nothing
	// read them and there is no "Powered by" mark to switch off. An operator who
	// still sets them must be told they do nothing — and must not be refused a
	// boot by a key this ticket took away.
	path := writeEmbedConfig(t, "branding:\n  favicon: https://cdn.example/icon.png\n  powered_by: false\n")

	var loadErr error
	warnings := captureWarnings(t, func() { _, loadErr = Load(path) })
	if loadErr != nil {
		t.Fatalf("Load() error = %v, want the removed keys ignored, not refused", loadErr)
	}
	for _, key := range []string{"branding.favicon", "branding.powered_by"} {
		if !strings.Contains(warnings, key) {
			t.Errorf("warnings = %q, want them to name %s", warnings, key)
		}
	}
}
