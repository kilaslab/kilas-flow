package nodepack_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/nodepack"
	"github.com/kilaslab/kilas-flow/internal/wasmpack"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

// moduleDeps is a loader environment with the pack executor installed, which
// is what a module pack's definition is checked against.
func moduleDeps(modules nodepack.ModuleRegistrar) (nodepack.DirDeps, *node.Registry) {
	definitions := node.NewRegistry()
	executors := engine.NewRegistry()
	if err := executors.Register(wasmpack.ExecutorID, engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return nil, nil
		})); err != nil {
		panic(err)
	}
	return nodepack.DirDeps{Definitions: definitions, Executors: executors, Modules: modules}, definitions
}

// recordingModules is a pack runtime that records what the loader approved
// without compiling anything, so the loader's own rules can be tested without
// a WebAssembly toolchain.
type recordingModules struct {
	specs []wasmpack.Spec
	err   error
}

func (modules *recordingModules) Add(_ context.Context, spec wasmpack.Spec) error {
	if modules.err != nil {
		return modules.err
	}
	modules.specs = append(modules.specs, spec)
	return nil
}

// moduleManifest writes a module pack directory and returns the manifest it
// laid down.
func moduleManifest(t *testing.T, dir, name string, module []byte, mutate func(map[string]any)) {
	t.Helper()
	packDir := filepath.Join(dir, name)
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "module.wasm"), module, 0o644); err != nil {
		t.Fatalf("WriteFile(module) error = %v", err)
	}
	sum := sha256Hex(module)
	manifest := map[string]any{
		"type": "pack.module", "version": 1, "displayName": "Module", "category": "Transform",
		"parameters": []any{},
		"module": map[string]any{
			"file": "module.wasm", "sha256": sum, "abi": sdk.ABIVersion,
			"capabilities": []any{"http"},
			"credentials":  []any{map[string]any{"type": "httpHeaderAuth", "required": true}},
			"limits":       map[string]any{"timeoutSeconds": 5, "maxHostCalls": 7},
		},
	}
	if mutate != nil {
		mutate(manifest)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal(manifest) error = %v", err)
	}
	writePack(t, dir, name, encoded)
}

func sha256Hex(contents []byte) string {
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:])
}

// A module pack in a directory loads: the loader reads the module, checks it
// against the digest the manifest pins, hands it to the pack runtime, and
// registers the node bound to the pack executor.
func TestLoadDirLoadsAModulePackFromDisk(t *testing.T) {
	dir := t.TempDir()
	module := []byte("\x00asm\x01\x00\x00\x00")
	moduleManifest(t, dir, "module", module, nil)

	modules := &recordingModules{}
	deps, definitions := moduleDeps(modules)
	if err := nodepack.LoadDir(deps, dir); err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}
	if len(modules.specs) != 1 {
		t.Fatalf("approved %d specs, want 1", len(modules.specs))
	}
	spec := modules.specs[0]
	if spec.Type != "pack.module" || spec.Version != workflow.V(1) {
		t.Errorf("spec = %s v%s, want the manifest's type and version", spec.Type, spec.Version)
	}
	if spec.Mode != wasmpack.ModeItem {
		t.Errorf("mode = %q, want the default item mode", spec.Mode)
	}
	if !spec.Caps.HTTP || len(spec.Caps.Credentials) != 1 {
		t.Errorf("caps = %+v, want http and the declared credential", spec.Caps)
	}
	if spec.Limits.Timeout.Seconds() != 5 || spec.Limits.MaxHostCalls != 7 {
		t.Errorf("limits = %+v, want the manifest's bounds", spec.Limits)
	}
	if len(spec.Outputs) != 1 || spec.Outputs[0] != "main" {
		t.Errorf("outputs = %v, want the default main port", spec.Outputs)
	}
	if _, ok := definitions.Get("pack.module", workflow.V(1)); !ok {
		t.Error("the module pack's node was not registered")
	}
}

// A module whose bytes do not match the digest its manifest pins refuses the
// boot: the operator approved a digest, and the file changed after that.
func TestLoadDirRefusesAModuleThatDoesNotMatchItsDigest(t *testing.T) {
	dir := t.TempDir()
	moduleManifest(t, dir, "module", []byte("\x00asm\x01\x00\x00\x00"), nil)
	if err := os.WriteFile(filepath.Join(dir, "module", "module.wasm"), []byte("tampered"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	deps, _ := moduleDeps(&recordingModules{})
	err := nodepack.LoadDir(deps, dir)
	if err == nil || !strings.Contains(err.Error(), "does not match the sha256") {
		t.Errorf("LoadDir() error = %v, want the digest refusal", err)
	}
}

// A manifest naming a module that is not in the directory refuses the boot
// rather than registering a node nothing can run.
func TestLoadDirRefusesAMissingModuleFile(t *testing.T) {
	dir := t.TempDir()
	moduleManifest(t, dir, "module", []byte("\x00asm\x01\x00\x00\x00"), func(manifest map[string]any) {
		manifest["module"].(map[string]any)["file"] = "elsewhere.wasm"
	})
	deps, _ := moduleDeps(&recordingModules{})
	err := nodepack.LoadDir(deps, dir)
	if err == nil || !strings.Contains(err.Error(), "not in the pack directory") {
		t.Errorf("LoadDir() error = %v, want the missing-module refusal", err)
	}
}

// A deployment with no pack runtime refuses a module pack instead of
// registering a node whose executor does not exist.
func TestLoadDirRefusesAModulePackWithoutARuntime(t *testing.T) {
	dir := t.TempDir()
	moduleManifest(t, dir, "module", []byte("\x00asm\x01\x00\x00\x00"), nil)
	deps, _ := moduleDeps(nil)
	err := nodepack.LoadDir(deps, dir)
	if err == nil || !strings.Contains(err.Error(), "no WebAssembly pack runtime") {
		t.Errorf("LoadDir() error = %v, want the no-runtime refusal", err)
	}
}

// The pack runtime's own refusal (an undeclared import, a module that does not
// compile) reaches the operator with the pack's name in front of it.
func TestLoadDirReportsThePackRuntimesRefusal(t *testing.T) {
	dir := t.TempDir()
	moduleManifest(t, dir, "module", []byte("\x00asm\x01\x00\x00\x00"), nil)
	modules := &recordingModules{err: os.ErrInvalid}
	deps, _ := moduleDeps(modules)
	err := nodepack.LoadDir(deps, dir)
	if err == nil || !strings.Contains(err.Error(), `load node pack "module"`) {
		t.Errorf("LoadDir() error = %v, want the pack named", err)
	}
}
