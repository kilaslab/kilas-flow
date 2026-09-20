package main

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/config"
)

func testRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	return filepath.Dir(filepath.Dir(file))
}

// The generator must see every section and key: a struct change with no
// regenerated reference fails here the same way --check fails in CI.
func TestReferenceModelCoversConfig(t *testing.T) {
	root := testRepoRoot(t)
	sections, err := buildModel(filepath.Join(root, "internal", "config", "config.go"))
	if err != nil {
		t.Fatalf("buildModel: %v", err)
	}
	if len(sections) == 0 {
		t.Fatal("no sections generated")
	}
	bySection := map[string]map[string]field{}
	for _, sec := range sections {
		keys := map[string]field{}
		for _, f := range sec.fields {
			if f.doc == "" {
				t.Errorf("%s.%s has no prose", sec.name, f.key)
			}
			if f.env == "" || f.typeName == "" {
				t.Errorf("%s.%s is missing derived metadata", sec.name, f.key)
			}
			keys[f.key] = f
		}
		bySection[sec.name] = keys
	}
	// The SSRF egress policy is the security boundary: it must never go
	// missing from the reference again.
	outbound, ok := bySection["outbound"]
	if !ok {
		t.Fatal("outbound section missing from reference")
	}
	egress, ok := outbound["allow_private_networks"]
	if !ok {
		t.Fatal("outbound.allow_private_networks missing from reference")
	}
	if egress.env != "KILASFLOW_OUTBOUND_ALLOW_PRIVATE_NETWORKS" {
		t.Errorf("wrong env for allow_private_networks: %q", egress.env)
	}
	if egress.defValue != "false" {
		t.Errorf("wrong default for allow_private_networks: %q", egress.defValue)
	}
	embed, ok := bySection["embed"]
	if !ok {
		t.Fatal("embed section missing from reference")
	}
	if _, ok := embed["allowed_origins"]; !ok {
		t.Fatal("embed.allowed_origins missing from reference")
	}
}

// The generated example file is a copy-edit starting point: it must always
// load, and loading it must produce the documented defaults.
func TestGeneratedExampleLoads(t *testing.T) {
	root := testRepoRoot(t)
	cfg, err := config.Load(filepath.Join(root, "config.example.yaml"))
	if err != nil {
		t.Fatalf("generated config.example.yaml does not load: %v", err)
	}
	if cfg.Database.Driver != "sqlite" {
		t.Errorf("example driver = %q, want sqlite", cfg.Database.Driver)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("example server.port = %d, want 8080", cfg.Server.Port)
	}
	if cfg.Execution.MaxConcurrent != 10 {
		t.Errorf("example execution.max_concurrent = %d, want 10", cfg.Execution.MaxConcurrent)
	}
}
