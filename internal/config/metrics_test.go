package config

import (
	"path/filepath"
	"testing"
)

// The metrics endpoint is opt-in, and its defaults are the safe posture: off,
// loopback, and no tenant label — a cardinality decision only the operator
// can make.
func TestMetricsDefaultToOffLoopbackAndNoTenantLabel(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Metrics.Enabled {
		t.Error("Metrics.Enabled = true, want the endpoint off by default")
	}
	if cfg.Metrics.Host != "127.0.0.1" {
		t.Errorf("Metrics.Host = %q, want the loopback default", cfg.Metrics.Host)
	}
	if cfg.Metrics.Port != 9090 {
		t.Errorf("Metrics.Port = %d, want 9090", cfg.Metrics.Port)
	}
	if cfg.Metrics.TenantLabel != "off" {
		t.Errorf("Metrics.TenantLabel = %q, want off", cfg.Metrics.TenantLabel)
	}
	if cfg.Metrics.TopTenants != 10 {
		t.Errorf("Metrics.TopTenants = %d, want 10", cfg.Metrics.TopTenants)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() on the default metrics section = %v, want nil", err)
	}
}

func TestTheMetricsSectionIsReachableFromYAMLAndTheEnvironment(t *testing.T) {
	// The section is one word for exactly this reason: envKeyToPath treats the
	// first underscore as the section separator, so a section named
	// metrics_endpoint could never be set from the environment.
	t.Setenv("KILASFLOW_METRICS_ENABLED", "true")
	t.Setenv("KILASFLOW_METRICS_TENANT_LABEL", "top")
	t.Setenv("KILASFLOW_METRICS_TOP_TENANTS", "5")

	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Metrics.Enabled {
		t.Error("Metrics.Enabled = false, want the environment's true")
	}
	if cfg.Metrics.TenantLabel != "top" {
		t.Errorf("Metrics.TenantLabel = %q, want top", cfg.Metrics.TenantLabel)
	}
	if cfg.Metrics.TopTenants != 5 {
		t.Errorf("Metrics.TopTenants = %d, want 5", cfg.Metrics.TopTenants)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil: the section is consistent", err)
	}
	if got := envKeyToPath("KILASFLOW_METRICS_TENANT_LABEL"); got != "metrics.tenant_label" {
		t.Errorf("envKeyToPath = %q, want %q", got, "metrics.tenant_label")
	}
}

// A metrics configuration that cannot work is refused, but only when the
// endpoint is enabled: a declined endpoint is a deployment where none of
// these values is read, and a stray value there must not refuse a boot.
func TestValidateRefusesAMetricsSectionThatCannotWork(t *testing.T) {
	cases := map[string]func(*Metrics){
		"port out of range": func(section *Metrics) { section.Port = 0 },
		"unknown tenant label": func(section *Metrics) {
			section.TenantLabel = "some"
		},
		"top mode with no tenants": func(section *Metrics) {
			section.TenantLabel = "top"
			section.TopTenants = 0
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := Default()
			cfg.Metrics.Enabled = true
			mutate(&cfg.Metrics)
			if err := cfg.Validate(); err == nil {
				t.Error("Validate() = nil, want the refusal")
			}

			// The same value with the endpoint off changes nothing: none of
			// it is read.
			cfg = Default()
			mutate(&cfg.Metrics)
			if err := cfg.Validate(); err != nil {
				t.Errorf("Validate() with the endpoint off = %v, want nil", err)
			}
		})
	}
}

// The tenant-label vocabulary the configuration accepts is the metrics
// package's own, stated once: two spellings of the same mode would let a
// file say "on" while the registry heard "off".
func TestTheTenantLabelVocabularyMatchesTheMetricsPackage(t *testing.T) {
	cfg := Default()
	cfg.Metrics.Enabled = true
	for _, mode := range []string{"off", "on", "top"} {
		cfg.Metrics.TenantLabel = mode
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() with tenant_label %q = %v, want nil", mode, err)
		}
	}
}
