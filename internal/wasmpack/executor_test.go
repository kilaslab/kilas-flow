package wasmpack_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/wasmpack"
	"github.com/kilaslab/kilas-flow/internal/wasmtest"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// stubCatalog answers with one definition, which is what the executor reads
// the output ports from.
type stubCatalog struct {
	definition node.Definition
	found      bool
}

func (catalog stubCatalog) Get(string, workflow.TypeVersion) (node.Definition, bool) {
	return catalog.definition, catalog.found
}

func executorFor(t *testing.T, spec wasmpack.Spec, definition node.Definition) *wasmpack.Executor {
	t.Helper()
	registry := wasmpack.NewRegistry(testModules())
	if err := registry.Add(context.Background(), spec); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	return wasmpack.NewExecutor(registry, stubCatalog{definition: definition, found: true}, wasmpack.HostDeps{})
}

// The executor refuses a node type no module registered, rather than running
// nothing and reporting success.
func TestTheExecutorRefusesAnUnregisteredModule(t *testing.T) {
	registry := wasmpack.NewRegistry(testModules())
	executor := wasmpack.NewExecutor(registry, stubCatalog{}, wasmpack.HostDeps{})
	_, err := executor.Execute(context.Background(), workflow.IRNode{Type: "pack.nope", TypeVersion: workflow.V(1)}, nil, engineRequest(nil))
	if err == nil || !strings.Contains(err.Error(), "no module is registered for pack.nope v1") {
		t.Fatalf("Execute() error = %v, want the unregistered refusal", err)
	}
}

// A pack that writes to more ports than its manifest declared is refused: the
// operator approved a port list, and a node that writes past it would land
// items on a connection the compiler never resolved.
func TestTheExecutorRefusesAnUndeclaredOutputPort(t *testing.T) {
	module := wasmtest.MinimalModule(`[[],[]]`)
	spec := specOf(t, module, func(spec *wasmpack.Spec) { spec.Outputs = []string{"main"} })
	definition := node.Definition{
		Type: "pack.module", Version: workflow.V(1),
		Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
	}
	executor := executorFor(t, spec, definition)
	_, err := executor.Execute(context.Background(), workflow.IRNode{
		Name: "module", Type: "pack.module", TypeVersion: workflow.V(1),
	}, workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, engineRequest(nil))
	if err == nil || !strings.Contains(err.Error(), "wrote to 2 ports") {
		t.Fatalf("Execute() error = %v, want the undeclared-port refusal", err)
	}
}

// An empty standard output is a pack that answered nothing, which is a failure
// rather than an empty success: a node that silently drops every item is the
// hardest kind of workflow bug to find.
func TestTheExecutorRefusesAnEmptyAnswer(t *testing.T) {
	module := wasmtest.MinimalModule("")
	spec := specOf(t, module, nil)
	definition := node.Definition{
		Type: "pack.module", Version: workflow.V(1),
		Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
	}
	executor := executorFor(t, spec, definition)
	_, err := executor.Execute(context.Background(), workflow.IRNode{
		Name: "module", Type: "pack.module", TypeVersion: workflow.V(1),
	}, workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, engineRequest(nil))
	if err == nil || !strings.Contains(err.Error(), "wrote nothing to standard output") {
		t.Fatalf("Execute() error = %v, want the empty-answer refusal", err)
	}
}

// A module that writes one list of items lands them on the first port, which is
// the shape the SDK's MainCall writes.
func TestTheExecutorMapsOneListOntoTheFirstPort(t *testing.T) {
	module := wasmtest.MinimalModule(`[{"json":{"n":1}},{"json":{"n":2}}]`)
	spec := specOf(t, module, nil)
	definition := node.Definition{
		Type: "pack.module", Version: workflow.V(1),
		Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
	}
	executor := executorFor(t, spec, definition)
	output, err := executor.Execute(context.Background(), workflow.IRNode{
		Name: "module", Type: "pack.module", TypeVersion: workflow.V(1),
	}, workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, engineRequest(nil))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output) != 1 || len(output[0]) != 2 {
		t.Fatalf("output = %+v, want two items on the first port", output)
	}
	if output[0][0].JSON["n"] != float64(1) {
		t.Errorf("first item = %+v, want the module's own JSON", output[0][0].JSON)
	}
}
