package handlers

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/runcode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// stubCodeChecker answers every build with one canned outcome and records what
// it was asked to build, so the dry run's handling of a compile error is
// tested without a Go toolchain.
type stubCodeChecker struct {
	err     error
	checked []string
}

func (stub *stubCodeChecker) CheckCode(_ context.Context, source string) error {
	stub.checked = append(stub.checked, source)
	return stub.err
}

// explodeSource is the ticket's reproduction: four lines, the third of which
// names a variable that does not exist.
const explodeSource = "out := items\n_ = out\nreturn itemz, nil\n// end"

func validationHandler(t *testing.T, checker CodeChecker) *Workflows {
	t.Helper()
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	handler := NewWorkflows(&stubWorkflowRepository{}, nil, registry, nil, nil)
	if checker != nil {
		handler = handler.WithCodeChecker(checker)
	}
	return handler
}

func codeNodeDocument(source string) *validateWorkflowInput {
	return &validateWorkflowInput{Body: workflowDocumentInput{
		SchemaVersion: workflow.CurrentSchemaVersion,
		Name:          "Explode",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{
				ID: "explode", Name: "Explode", Type: nodes.CodeNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"code": source},
			},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "explode", Port: "main"},
		}},
		Settings: map[string]any{},
	}}
}

// A Go Code node that does not compile is reported by the dry run, at the
// user's own line, before anything runs — not discovered by the first run.
func TestValidateReportsAGoCodeNodesCompileErrorAtTheUsersLine(t *testing.T) {
	checker := &stubCodeChecker{err: &runcode.CompileError{Detail: "undefined: itemz (line 3, column 8)"}}
	output, err := validationHandler(t, checker).ValidateDocument(context.Background(), codeNodeDocument(explodeSource))
	if err != nil {
		t.Fatalf("ValidateDocument() error = %v", err)
	}
	if output.Body.Valid {
		t.Fatalf("validate(a Code node that does not compile) = %#v, want invalid", output.Body)
	}
	if len(checker.checked) != 1 || checker.checked[0] != explodeSource {
		t.Errorf("built %q, want the node's source once", checker.checked)
	}
	if len(output.Body.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v, want one", output.Body.Diagnostics)
	}
	diagnostic := output.Body.Diagnostics[0]
	if diagnostic.Severity != "blocking" || diagnostic.NodeID != "explode" || diagnostic.Field != "/nodes/1/parameters/code" {
		t.Errorf("diagnostic = %#v, want a blocking one on the Code node's code", diagnostic)
	}
	if !strings.Contains(diagnostic.Reason, `node "explode"`) || !strings.Contains(diagnostic.Reason, "undefined: itemz (line 3") {
		t.Errorf("reason = %q, want the node and the user's line 3", diagnostic.Reason)
	}
}

// A deployment that cannot compile says so as a warning: the document may well
// run (an artifact can be cached), and a dry run that refused every Code node
// on a toolchain-less install would refuse documents that work.
func TestValidateWarnsRatherThanBlocksWhenCodeCannotBeCompiledHere(t *testing.T) {
	checker := &stubCodeChecker{err: fmt.Errorf("%w: %s", runcode.ErrCompilerUnavailable, runcode.UnavailableMessage("go"))}
	output, err := validationHandler(t, checker).ValidateDocument(context.Background(), codeNodeDocument(explodeSource))
	if err != nil {
		t.Fatalf("ValidateDocument() error = %v", err)
	}
	if !output.Body.Valid || len(output.Body.Diagnostics) != 0 {
		t.Fatalf("validate(no toolchain) = %#v, want valid with no diagnostics", output.Body)
	}
	if len(output.Body.Warnings) != 1 || output.Body.Warnings[0].NodeID != "explode" ||
		!strings.Contains(output.Body.Warnings[0].Reason, "cannot compile Code nodes") {
		t.Errorf("warnings = %#v, want one naming the Code node and the missing toolchain", output.Body.Warnings)
	}
}

// Only Code nodes that would run are built: a node the compiler already
// refused is reported once, a disabled node never runs, and a handler with no
// checker answers exactly as it did before one existed.
func TestValidateBuildsOnlyCodeThatWouldRun(t *testing.T) {
	checker := &stubCodeChecker{err: &runcode.CompileError{Detail: "unreachable"}}

	empty := codeNodeDocument("")
	output, err := validationHandler(t, checker).ValidateDocument(context.Background(), empty)
	if err != nil {
		t.Fatalf("ValidateDocument() error = %v", err)
	}
	if len(output.Body.Diagnostics) != 1 || len(checker.checked) != 0 {
		t.Errorf("validate(empty code) diagnostics = %#v, built %q; want the compiler's one issue and no build",
			output.Body.Diagnostics, checker.checked)
	}

	disabled := codeNodeDocument(explodeSource)
	disabled.Body.Nodes[1].Disabled = true
	output, err = validationHandler(t, checker).ValidateDocument(context.Background(), disabled)
	if err != nil {
		t.Fatalf("ValidateDocument() error = %v", err)
	}
	if !output.Body.Valid || len(checker.checked) != 0 {
		t.Errorf("validate(a disabled Code node) = %#v, built %q; want valid and no build", output.Body, checker.checked)
	}

	output, err = validationHandler(t, nil).ValidateDocument(context.Background(), codeNodeDocument(explodeSource))
	if err != nil {
		t.Fatalf("ValidateDocument() error = %v", err)
	}
	if !output.Body.Valid || len(output.Body.Warnings) != 0 {
		t.Errorf("validate(no checker) = %#v, want valid and silent", output.Body)
	}
}

// The real executor, on a machine with a Go toolchain: the line in the
// diagnostic is the user's, not the wrapper's.
func TestValidateCompilesAGoCodeNodeWithTheToolchain(t *testing.T) {
	compiler := runcode.NewToolchainCompiler()
	if !compiler.Available() {
		t.Skip("go toolchain is not available")
	}
	checker := nodes.NewCodeExecutor(compiler, nil, runcode.DefaultLimits())
	output, err := validationHandler(t, checker).ValidateDocument(context.Background(), codeNodeDocument(explodeSource))
	if err != nil {
		t.Fatalf("ValidateDocument() error = %v", err)
	}
	if output.Body.Valid || len(output.Body.Diagnostics) != 1 {
		t.Fatalf("validate(undefined: itemz) = %#v, want one diagnostic", output.Body)
	}
	if reason := output.Body.Diagnostics[0].Reason; !strings.Contains(reason, "undefined: itemz (line 3") {
		t.Errorf("reason = %q, want undefined: itemz at the user's line 3", reason)
	}

	fixed := strings.Replace(explodeSource, "itemz", "items", 1)
	output, err = validationHandler(t, checker).ValidateDocument(context.Background(), codeNodeDocument(fixed))
	if err != nil {
		t.Fatalf("ValidateDocument() error = %v", err)
	}
	if !output.Body.Valid || len(output.Body.Warnings) != 0 {
		t.Errorf("validate(code that compiles) = %#v, want valid and silent", output.Body)
	}
}
