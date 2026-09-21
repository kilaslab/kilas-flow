package nodepack_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/nodepack"
	"github.com/kilaslab/kilas-flow/internal/wasmpack"
	"github.com/kilaslab/kilas-flow/internal/wasmtest"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// A module pack loaded from a directory runs: the loader approves the bytes,
// the executor the composition root installed runs them, and the items the
// module wrote come out of the node — the whole chain, with no test double
// between the loader and the runtime.
func TestAModulePackFromDiskRunsThroughTheInstalledExecutor(t *testing.T) {
	dir := t.TempDir()
	module := wasmtest.MinimalModule(`[{"json":{"n":7}}]`)
	moduleManifest(t, dir, "module", module, nil)

	modulePacks := wasmpack.NewRegistry(nil)
	definitions := node.NewRegistry()
	executors := engine.NewRegistry()
	packExecutor := wasmpack.NewExecutor(modulePacks, definitions, wasmpack.HostDeps{})
	if err := executors.Register(wasmpack.ExecutorID, packExecutor); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodepack.LoadDir(nodepack.DirDeps{
		Definitions: definitions, Executors: executors, Modules: modulePacks,
	}, dir); err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}

	definition, found := definitions.Get("pack.module", workflow.V(1))
	if !found {
		t.Fatal("the module pack did not register")
	}
	if definition.Source != node.SourcePack {
		t.Errorf("Source = %q, want %q", definition.Source, node.SourcePack)
	}
	installed, found := executors.Lookup(definition.ExecutorID)
	if !found {
		t.Fatalf("the pack executor %q is not installed", definition.ExecutorID)
	}
	output, err := installed.Execute(context.Background(), workflow.IRNode{
		Name: "Module", Type: "pack.module", TypeVersion: workflow.V(1),
	}, workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output) != 1 || len(output[0]) != 1 {
		t.Fatalf("output = %+v, want one item on the main port", output)
	}
	if output[0][0].JSON["n"] != float64(7) {
		t.Errorf("item = %+v, want the module's own JSON", output[0][0].JSON)
	}
}

// A module that reaches past its manifest refuses the boot, and nothing is
// registered: the failure an operator sees is at load, not in a workflow.
func TestAModulePackThatReachesPastItsManifestRefusesTheBoot(t *testing.T) {
	dir := t.TempDir()
	// A module that imports the HTTP host function while its manifest declares
	// no capability at all.
	module := wasmtest.Build([]wasmtest.Import{{
		Module: "kilasflow_v1", Name: "http_request",
		Params:  []wasmtest.ValueType{wasmtest.I32, wasmtest.I32, wasmtest.I32, wasmtest.I32},
		Results: []wasmtest.ValueType{wasmtest.I32},
	}}, nil, 1, nil)
	moduleManifest(t, dir, "module", module, func(manifest map[string]any) {
		manifest["module"].(map[string]any)["capabilities"] = []any{}
	})

	modulePacks := wasmpack.NewRegistry(nil)
	definitions := node.NewRegistry()
	executors := engine.NewRegistry()
	if err := executors.Register(wasmpack.ExecutorID, wasmpack.NewExecutor(modulePacks, definitions, wasmpack.HostDeps{})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	err := nodepack.LoadDir(nodepack.DirDeps{
		Definitions: definitions, Executors: executors, Modules: modulePacks,
	}, dir)
	if err == nil {
		t.Fatal("LoadDir() accepted a module that reaches past its manifest")
	}
	if _, found := definitions.Get("pack.module", workflow.V(1)); found {
		t.Error("a refused module pack registered its node")
	}
	if modulePacks.Len() != 0 {
		t.Error("a refused module pack is reachable in the registry")
	}
	_ = os.Remove(filepath.Join(dir, "module", "module.wasm"))
}
