package wasmpack_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/wasmpack"
	"github.com/kilaslab/kilas-flow/internal/wasmtest"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

// specOf is one module pack the registry can accept: a module that imports the
// result slots, which every capability set grants.
func specOf(t *testing.T, module []byte, mutate func(*wasmpack.Spec)) wasmpack.Spec {
	t.Helper()
	spec := wasmpack.Spec{
		Type: "pack.module", Version: workflow.V(1),
		Module: module, Mode: wasmpack.ModeItem,
		Caps: wasmpack.Capabilities{HTTP: true}, Limits: wasmpack.DefaultLimits(),
		Outputs: []string{"main"},
	}
	if mutate != nil {
		mutate(&spec)
	}
	return spec
}

// A module that declares a capability it uses is accepted, and is then
// reachable by type and version.
func TestTheRegistryAcceptsWhatTheAuditAccepts(t *testing.T) {
	module := guestCall(t, "result_len", []int{sdk.SlotResult}, 0, nil, pageCount)
	registry := wasmpack.NewRegistry(testModules())
	if err := registry.Add(context.Background(), specOf(t, module, nil)); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	spec, found := registry.Lookup("pack.module", workflow.V(1))
	if !found {
		t.Fatal("the spec was not reachable after Add")
	}
	if spec.Mode != wasmpack.ModeItem || len(spec.Outputs) != 1 {
		t.Errorf("spec = %+v, want what was added", spec)
	}
	if types := registry.Types(); len(types) != 1 || types[0] != "pack.module@1" {
		t.Errorf("Types() = %v, want the registered node key", types)
	}
}

// A module that reaches past its declaration is refused by the registry, which
// is where the operator sees it: at load, not at run.
func TestTheRegistryRefusesAModuleThatReachesPastItsDeclaration(t *testing.T) {
	module := guestCall(t, "http_request", []int{0, 0, 0, 0}, 0, nil, pageCount)
	registry := wasmpack.NewRegistry(testModules())
	err := registry.Add(context.Background(), specOf(t, module, func(spec *wasmpack.Spec) {
		spec.Caps = wasmpack.Capabilities{}
	}))
	if err == nil || !strings.Contains(err.Error(), "http_request") {
		t.Fatalf("Add() error = %v, want the capability refusal", err)
	}
	if registry.Len() != 0 {
		t.Error("a refused module pack is reachable")
	}
}

// A module whose bytes do not match the digest its manifest pinned is refused:
// the operator approved a digest, not a filename.
func TestTheRegistryRefusesAModuleThatDoesNotMatchItsDigest(t *testing.T) {
	module := guestCall(t, "result_len", []int{sdk.SlotResult}, 0, nil, pageCount)
	registry := wasmpack.NewRegistry(testModules())
	err := registry.Add(context.Background(), specOf(t, module, func(spec *wasmpack.Spec) {
		spec.Digest = strings.Repeat("0", 64)
	}))
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("Add() error = %v, want the digest refusal", err)
	}
}

// Two packs claiming one node type is an installation mistake the operator has
// to see rather than a silent replacement.
func TestTheRegistryRefusesADuplicateNodeType(t *testing.T) {
	module := guestCall(t, "result_len", []int{sdk.SlotResult}, 0, nil, pageCount)
	registry := wasmpack.NewRegistry(testModules())
	if err := registry.Add(context.Background(), specOf(t, module, nil)); err != nil {
		t.Fatalf("first Add() error = %v", err)
	}
	err := registry.Add(context.Background(), specOf(t, module, nil))
	if err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("second Add() error = %v, want the duplicate refusal", err)
	}
}

// A spec the loader could not have built is refused before anything compiles:
// no module bytes, no outputs, an unknown mode.
func TestTheRegistryRefusesAnUnusableSpec(t *testing.T) {
	module := wasmtest.MinimalModule("")
	cases := []struct {
		name   string
		mutate func(*wasmpack.Spec)
		want   string
	}{
		{name: "no module", mutate: func(spec *wasmpack.Spec) { spec.Module = nil }, want: "ships no module bytes"},
		{name: "no outputs", mutate: func(spec *wasmpack.Spec) { spec.Outputs = nil }, want: "no output ports"},
		{name: "unknown mode", mutate: func(spec *wasmpack.Spec) { spec.Mode = "parallel" }, want: "unknown mode"},
		{name: "no type", mutate: func(spec *wasmpack.Spec) { spec.Type = "" }, want: "needs a node type"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			registry := wasmpack.NewRegistry(testModules())
			err := registry.Add(context.Background(), specOf(t, module, testCase.mutate))
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("Add() error = %v, want it to mention %q", err, testCase.want)
			}
		})
	}
}
