package runcode_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/runcode"
	"github.com/kilaslab/kilas-flow/internal/wasmtest"
)

// An audit of a module is only worth anything if it is reading what the module
// actually declares. These tests clear PATH: nothing here may need a Go
// toolchain, and the one case that does build a guest says so through
// requireToolchain.
func TestInspectReportsWhatAModuleImportsAndExports(t *testing.T) {
	t.Setenv("PATH", "")

	modules := runcode.NewModuleCache()
	t.Cleanup(func() { _ = modules.Close(context.Background()) })

	inspection, err := modules.Inspect(context.Background(), wasmtest.MinimalModule("{}"), 0)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	want := []runcode.Import{{Module: "wasi_snapshot_preview1", Name: "fd_write"}}
	if !slices.Equal(inspection.Imports, want) {
		t.Errorf("Imports = %#v, want %#v", inspection.Imports, want)
	}
	if !slices.Equal(inspection.Exports, []string{"_start", "memory"}) {
		t.Errorf("Exports = %#v, want the two a command module has", inspection.Exports)
	}
}

// A Go guest is the case the audit exists for, and the one wazero answers
// misleadingly: a FunctionDefinition's ModuleName and Name are the *defining*
// module's, which are empty for every wasi_snapshot_preview1 import of a
// Go-built module. Import() is the only accessor that names the module being
// imported from, so this asserts on names rather than on a count that a
// toolchain update would change.
func TestInspectNamesTheWasiImportsOfAGoGuest(t *testing.T) {
	compiler := requireToolchain(t)

	module, err := compiler.Compile(context.Background(), "return items, nil")
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	modules := runcode.NewModuleCache()
	t.Cleanup(func() { _ = modules.Close(context.Background()) })

	inspection, err := modules.Inspect(context.Background(), module, 0)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if len(inspection.Imports) == 0 {
		t.Fatal("a Go guest imports WASI; the audit found none")
	}
	// Logged rather than asserted: the set is the linker's business, and a
	// toolchain that imports a different number of them is not a regression.
	t.Logf("a Go guest imports %d WASI functions: %#v", len(inspection.Imports), inspection.Imports)
	for _, imported := range inspection.Imports {
		if imported.Module != "wasi_snapshot_preview1" {
			t.Errorf("import = %#v, want it to name the WASI module", imported)
		}
		if imported.Name == "" {
			t.Errorf("import = %#v, want the imported function named", imported)
		}
	}
	if !slices.Equal(inspection.Exports, []string{"_start", "memory"}) {
		t.Errorf("Exports = %#v, want the two a wasip1 command module has", inspection.Exports)
	}
}

// Inspecting compiles the module, and it does that through the cache it is
// called on: the audit warms the translation the first real call would
// otherwise pay for. wazero writes a file cache's translations to disk, which
// is what makes the warm-up observable rather than assumed.
func TestInspectWarmsTheTranslationCache(t *testing.T) {
	t.Setenv("PATH", "")

	dir := t.TempDir()
	modules := newPersistentModuleCache(t, dir, runcode.RuntimeVersion)

	if files := translationFiles(t, dir, runcode.RuntimeVersion); len(files) != 0 {
		t.Fatalf("the translation cache started with %v", files)
	}
	if _, err := modules.Inspect(context.Background(), wasmtest.MinimalModule("{}"), 0); err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}

	files := translationFiles(t, dir, runcode.RuntimeVersion)
	if len(files) == 0 {
		// The interpreter engine does not write translations at all. Skipping
		// with a reason is honest; asserting anything here would be a lie.
		t.Skipf("wazero wrote no translation files under %s; this platform uses the interpreter", dir)
	}
	if len(files) != 1 {
		t.Errorf("Inspect() left %d translation files, want the one it compiled: %v", len(files), files)
	}
}

// The audit states the same limit a run would: a module that could never start
// inside the deployment's memory limit is refused while compiling, before
// anything is granted to it.
func TestInspectRefusesAModuleThatCannotFitTheMemoryLimit(t *testing.T) {
	t.Setenv("PATH", "")

	modules := runcode.NewModuleCache()
	t.Cleanup(func() { _ = modules.Close(context.Background()) })

	_, err := modules.Inspect(context.Background(), wasmtest.Build(nil, nil, 40, nil), 32)
	if err == nil {
		t.Fatal("a module that cannot fit a 32-page limit was inspected as if it could")
	}
	if !strings.Contains(err.Error(), "over limit of") {
		t.Errorf("error = %v, want the compile-time memory refusal", err)
	}
}
