package nodes_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/runcode"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

func codeIR(t *testing.T, parameters map[string]any) workflow.IRNode {
	t.Helper()
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Lookup(nodes.CodeNodeType, workflow.V(1))
	if !found {
		t.Fatal("the Code node is not registered")
	}
	return workflow.IRNode{
		ID: "code-1", Name: "Code", Type: nodes.CodeNodeType, TypeVersion: workflow.V(1),
		Parameters: parameters, Definition: definition,
	}
}

// generousLimits give WASM execution room under the race detector, which slows
// it well past the product's 10s default.
func generousLimits() runcode.Limits {
	limits := runcode.DefaultLimits()
	limits.Timeout = 120 * time.Second
	return limits
}

func toolchainOrSkip(t *testing.T) runcode.Compiler {
	t.Helper()
	compiler := runcode.NewToolchainCompiler()
	if !compiler.Available() {
		t.Skip("go toolchain is not available")
	}
	return compiler
}

func TestCodeNodeIsRegisteredWithAnEditorForm(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Get(nodes.CodeNodeType, workflow.V(1))
	if !found {
		t.Fatal("the Code node is not registered")
	}
	keys := map[string]bool{}
	for _, parameter := range definition.Parameters {
		keys[parameter.Key] = true
	}
	for _, required := range []string{"code", "timeoutSeconds", "memoryMB"} {
		if !keys[required] {
			t.Errorf("Code node is missing parameter %q", required)
		}
	}
}

func TestCodeNodeValidatesSourceAtSaveTimeWithoutCompiling(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Lookup(nodes.CodeNodeType, workflow.V(1))

	// A save must not block on a build, and the compiler may not even be
	// present, so validation is a shape check only.
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"code": "package main"}}); err == nil {
		t.Error("a whole file was accepted as a function body")
	}
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{}}); err == nil {
		t.Error("a node with no code was accepted")
	}
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"code": "return items, nil"}}); err != nil {
		t.Errorf("a valid body was rejected: %v", err)
	}
}

func TestCodeNodeTransformsTheWholeBatch(t *testing.T) {
	compiler := toolchainOrSkip(t)
	executor := nodes.NewCodeExecutor(compiler, runcode.NewMemoryCache(), generousLimits())

	// Seeing the whole batch is what lets a Code node filter or aggregate,
	// which is most of why someone reaches for one.
	output, err := executor.Execute(context.Background(), codeIR(t, map[string]any{
		"code": `
	total := 0.0
	for _, item := range items {
		total += item.JSON["n"].(float64)
	}
	return []Item{{JSON: map[string]any{"total": total, "count": len(items)}}}, nil
`,
	}), workflow.NodeInput{"main": {
		{JSON: map[string]any{"n": float64(1)}},
		{JSON: map[string]any{"n": float64(2)}},
		{JSON: map[string]any{"n": float64(4)}},
	}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output[0]) != 1 {
		t.Fatalf("output = %#v, want one aggregated item", output[0])
	}
	if output[0][0].JSON["total"] != float64(7) || output[0][0].JSON["count"] != float64(3) {
		t.Fatalf("item = %#v, want the aggregate over the batch", output[0][0].JSON)
	}
}

func TestCodeNodeReportsAUserErrorStructurally(t *testing.T) {
	compiler := toolchainOrSkip(t)
	executor := nodes.NewCodeExecutor(compiler, runcode.NewMemoryCache(), generousLimits())

	_, err := executor.Execute(context.Background(), codeIR(t, map[string]any{
		"code": `return nil, errors.New("record 7 is missing a customer")`,
	}), workflow.NodeInput{}, engine.Request{})
	if err == nil {
		t.Fatal("code returning an error reported success")
	}
	if !strings.Contains(err.Error(), "record 7 is missing a customer") {
		t.Errorf("error = %v, want the user's message surfaced", err)
	}
}

func TestCodeNodeWithoutACompilerSaysSoPlainly(t *testing.T) {
	t.Parallel()

	executor := nodes.NewCodeExecutor(nil, runcode.NewMemoryCache(), runcode.DefaultLimits())
	_, err := executor.Execute(context.Background(), codeIR(t, map[string]any{"code": "return items, nil"}),
		workflow.NodeInput{}, engine.Request{})
	if err == nil {
		t.Fatal("a node with no compiler reported success")
	}
	// A missing toolchain is a deployment problem, not the user's code being
	// wrong, so the message must not read like a syntax error.
	if !strings.Contains(err.Error(), "cannot compile Code nodes") {
		t.Errorf("error = %v, want a deployment-level explanation", err)
	}
}

func TestCodeNodeStatusReportsCompilationWithoutRunningTheWorkflow(t *testing.T) {
	compiler := toolchainOrSkip(t)
	executor := nodes.NewCodeExecutor(compiler, runcode.NewMemoryCache(), generousLimits())

	good := executor.Status(context.Background(), "return items, nil")
	if !good.Compiled || good.Error != "" || good.Hash == "" || good.CompiledAt == "" {
		t.Fatalf("status = %#v, want a successful compilation", good)
	}

	bad := executor.Status(context.Background(), "this is not go; return items, nil")
	if bad.Compiled || bad.Error == "" {
		t.Fatalf("status = %#v, want a reported compilation failure", bad)
	}
	if bad.Hash == good.Hash {
		t.Error("different source produced the same artifact hash")
	}
}

func TestCodeNodeCannotRaiseTheDeploymentsLimits(t *testing.T) {
	compiler := toolchainOrSkip(t)
	ceiling := runcode.DefaultLimits()
	ceiling.Timeout = 500 * 1000 * 1000 // 500ms
	executor := nodes.NewCodeExecutor(compiler, runcode.NewMemoryCache(), ceiling)

	// The node asks for 60 seconds; the deployment allows 500ms.
	_, err := executor.Execute(context.Background(), codeIR(t, map[string]any{
		"code":           "for { _ = 1 }\n\treturn items, nil",
		"timeoutSeconds": float64(60),
	}), workflow.NodeInput{}, engine.Request{})
	if err == nil {
		t.Fatal("an endless loop completed")
	}
	if !strings.Contains(err.Error(), "time limit") {
		t.Errorf("error = %v, want the deployment ceiling to apply", err)
	}
}
