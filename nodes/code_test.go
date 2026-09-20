package nodes_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/runcode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
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
	for _, required := range []string{"code", "scriptTimeoutSeconds", "memoryMB"} {
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
	executor := nodes.NewCodeExecutor(compiler, runcode.NewMemoryCache(), runcode.DefaultLimits())

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
	executor := nodes.NewCodeExecutor(compiler, runcode.NewMemoryCache(), runcode.DefaultLimits())

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
	executor := nodes.NewCodeExecutor(compiler, runcode.NewMemoryCache(), runcode.DefaultLimits())

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
	ceiling.Timeout = 500 * time.Millisecond
	executor := nodes.NewCodeExecutor(compiler, runcode.NewMemoryCache(), ceiling)

	// The node asks for 60 seconds; the deployment allows 500ms.
	_, err := executor.Execute(context.Background(), codeIR(t, map[string]any{
		"code":                 "for { _ = 1 }\n\treturn items, nil",
		"scriptTimeoutSeconds": float64(60),
	}), workflow.NodeInput{}, engine.Request{})
	if err == nil {
		t.Fatal("an endless loop completed")
	}
	if !strings.Contains(err.Error(), "time limit") {
		t.Errorf("error = %v, want the deployment ceiling to apply", err)
	}
}

// Binary references are carried past the sandbox rather than through it: user
// code sees JSON only, and a payload it never receives is one it cannot
// corrupt. Before this, any attachment was gone the moment an item passed
// through a Code node, with no error and no diagnostic.
func TestCodeNodeCarriesBinaryReferencesThrough(t *testing.T) {
	compiler := toolchainOrSkip(t)
	executor := nodes.NewCodeExecutor(compiler, runcode.NewMemoryCache(), runcode.DefaultLimits())

	attachment := workflow.BinaryRef{ID: "bin-1", FileName: "photo.jpg", MediaType: "image/jpeg", Size: 2048}
	output, err := executor.Execute(context.Background(), codeIR(t, map[string]any{
		"code": `
	for index := range items {
		items[index].JSON["seen"] = true
	}
	return items, nil
`,
	}), workflow.NodeInput{"main": {
		{JSON: map[string]any{"caption": "hello"}, Binary: map[string]workflow.BinaryRef{"data": attachment}},
	}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output) != 1 || len(output[0]) != 1 {
		t.Fatalf("output = %#v, want one item", output)
	}
	item := output[0][0]
	if item.JSON["seen"] != true {
		t.Fatalf("JSON = %#v, want the user's edit applied", item.JSON)
	}
	if got := item.Binary["data"]; got != attachment {
		t.Fatalf("Binary[\"data\"] = %#v, want the incoming reference %#v", got, attachment)
	}
}

// Code that reshapes the batch has no attachment to inherit — the
// correspondence is positional because that is the only correspondence there
// is, and inventing one would attach the wrong file to the wrong item.
func TestCodeNodeDoesNotInventBinaryForItemsItDidNotReceive(t *testing.T) {
	compiler := toolchainOrSkip(t)
	executor := nodes.NewCodeExecutor(compiler, runcode.NewMemoryCache(), runcode.DefaultLimits())

	output, err := executor.Execute(context.Background(), codeIR(t, map[string]any{
		"code": `return []Item{items[0], {JSON: map[string]any{"extra": true}}}, nil`,
	}), workflow.NodeInput{"main": {
		{JSON: map[string]any{"n": float64(1)}, Binary: map[string]workflow.BinaryRef{"data": {ID: "bin-1"}}},
	}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output[0]) != 2 {
		t.Fatalf("output = %#v, want two items", output)
	}
	if output[0][0].Binary["data"].ID != "bin-1" {
		t.Fatalf("first item lost its reference: %#v", output[0][0].Binary)
	}
	if output[0][1].Binary != nil {
		t.Fatalf("second item Binary = %#v, want none", output[0][1].Binary)
	}
}

func TestTheGoCodeNodeRunsOncePerItemWhenAsked(t *testing.T) {
	t.Parallel()

	// Deliberately the product's own default limit, and the same executor for
	// both modes, because that is what makes this a check on the shipped
	// configuration: four sandbox calls over one artifact have to fit inside
	// the 10 seconds a real deployment gives them. They did not when every call
	// re-translated the module and was charged for it.
	executor := nodes.NewCodeExecutor(runcode.NewToolchainCompiler(), runcode.NewMemoryCache(), runcode.DefaultLimits())
	if status := executor.Status(context.Background(), "return items, nil"); !status.Available {
		t.Skip("this machine has no Go toolchain, so Code nodes cannot be compiled")
	}

	// The body reports how many items it was given. Once for all items sees
	// three; once per item sees one, three times.
	const source = `
	out := make([]Item, 0, len(items))
	for _, item := range items {
		copied := map[string]any{}
		for key, value := range item.JSON {
			copied[key] = value
		}
		copied["batch"] = float64(len(items))
		out = append(out, Item{JSON: copied})
	}
	return out, nil`

	incoming := workflow.NodeInput{"main": {
		{JSON: map[string]any{"id": float64(1)}, Binary: map[string]workflow.BinaryRef{"data": {ID: "k1"}}},
		{JSON: map[string]any{"id": float64(2)}},
		{JSON: map[string]any{"id": float64(3)}},
	}}

	for mode, wantBatch := range map[string]float64{
		"runOnceForAllItems": 3,
		"runOnceForEachItem": 1,
	} {
		t.Run(mode, func(t *testing.T) {
			output, err := executor.Execute(context.Background(), codeIR(t, map[string]any{
				"code": source, "mode": mode,
			}), incoming, engine.Request{})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if len(output[0]) != 3 {
				t.Fatalf("output = %d items, want 3", len(output[0]))
			}
			for index, item := range output[0] {
				if item.JSON["batch"] != wantBatch {
					t.Errorf("item %d batch = %#v, want %v", index, item.JSON["batch"], wantBatch)
				}
			}
			// The binary reference is carried past the sandbox, not through it:
			// user code never sees a payload it could corrupt, and dropping the
			// reference used to lose an attachment the next node needed.
			if got := output[0][0].Binary["data"].ID; got != "k1" {
				t.Errorf("binary = %#v, want the incoming reference kept", output[0][0].Binary)
			}
			if len(output[0][1].Binary) != 0 {
				t.Errorf("item 1 gained a binary it never had: %#v", output[0][1].Binary)
			}
		})
	}
}
