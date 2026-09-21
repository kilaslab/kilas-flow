package runcode

import (
	"context"
	"sort"

	"github.com/tetratelabs/wazero"
)

// Import is one function a module imports, named the way the module names it.
type Import struct {
	// Module is the module the function is imported from.
	Module string
	// Name is the function's name inside that module.
	Name string
}

// Inspection is what a module declares about itself, read without running it
// and without granting it anything.
type Inspection struct {
	// Imports is every function the module imports, in the order the module
	// declares them, which is the order their indices are in.
	Imports []Import
	// Exports is every name the module exports, sorted, functions and memories
	// together: they share one namespace in WebAssembly, and a pack's manifest
	// and its module have to agree on it.
	Exports []string
}

// Inspect reports what a module imports and exports.
//
// Compiling is enough to know: wazero resolves imports only at instantiation
// ("module[x] not instantiated" appears then and not before), so a module can
// be audited before anything is decided about what to grant it. That is what
// makes an operator able to see what a pack's node would ask the host for
// before installing it, rather than discovering it from a failure at run time.
//
// The compilation goes through the cache this ModuleCache wraps, so auditing
// warms the translation the first real call would otherwise pay for. The
// CompiledModule is deliberately not closed — closing one evicts its
// translation from the shared cache, which is the one thing the cache exists
// to keep — and closing the throwaway runtime releases the instance without
// touching the cache.
//
// memoryPages is the limit the module has to fit in, so an audit meets the
// same refusal a run would; 0 asks for no limit at all.
func (cache *ModuleCache) Inspect(ctx context.Context, module []byte, memoryPages uint32) (Inspection, error) {
	config := wazero.NewRuntimeConfig()
	if memoryPages > 0 {
		config = config.WithMemoryLimitPages(memoryPages)
	}
	if cache != nil && cache.compilation != nil {
		config = config.WithCompilationCache(cache.compilation)
	}

	runtime := wazero.NewRuntimeWithConfig(ctx, config)
	defer runtime.Close(context.Background())

	compiled, err := runtime.CompileModule(ctx, module)
	if err != nil {
		return Inspection{}, err
	}

	inspection := Inspection{}
	for _, function := range compiled.ImportedFunctions() {
		// Import() reports the module being imported from; ModuleName and Name
		// report the module a binary was compiled as and the name in its name
		// section, and both are empty for the WASI imports of a Go-built wasip1
		// module. The audit has to name what the module asks for.
		importedModule, importedName, isImport := function.Import()
		if !isImport {
			continue
		}
		inspection.Imports = append(inspection.Imports, Import{Module: importedModule, Name: importedName})
	}

	// Functions and memories share one export namespace, so one name cannot be
	// both; the two maps are merged rather than one being preferred.
	exports := make([]string, 0, len(compiled.ExportedFunctions()))
	for name := range compiled.ExportedFunctions() {
		exports = append(exports, name)
	}
	for name := range compiled.ExportedMemories() {
		exports = append(exports, name)
	}
	sort.Strings(exports)
	inspection.Exports = exports

	return inspection, nil
}
