package runcode_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/runcode"
)

// The Code node is unusable in the shipped image, and the only honest
// alternative to shipping a toolchain is a message that names exactly what the
// operator has to provide. This pins the parts of that message an operator
// acts on, and the two strings that would make it wrong: a machine path from
// somebody's home directory, and the word that would make it read like the
// user's code was at fault.
func TestUnavailableMessageNamesWhatTheOperatorMustProvide(t *testing.T) {
	t.Parallel()

	message := runcode.UnavailableMessage("go")
	for _, wanted := range []string{
		"cannot compile Code nodes",
		runcode.MinimumGoVersion,
		"KILASFLOW_CODE_GO_BINARY",
		"code.go_binary",
		"PATH",
		"standard library",
		"native nodes",
	} {
		if !strings.Contains(message, wanted) {
			t.Errorf("UnavailableMessage() = %q, want it to name %q", message, wanted)
		}
	}
	// The two paths are assembled from fragments at run time for the same
	// reason internal/guardrails assembles its own patterns: a build input that
	// must not contain a home-directory path must not contain one itself, and
	// the guardrail scans every Go file in the repository.
	for _, forbidden := range []string{"/ho" + "me/", "/Us" + "ers/"} {
		if strings.Contains(message, forbidden) {
			t.Errorf("UnavailableMessage() carries the machine path %q", forbidden)
		}
	}
}

// A deployment that moved the toolchain somewhere else must be told where the
// server looked, not told about `go` on PATH it does not use.
func TestAToolchainCompilerNamesTheBinaryItLookedFor(t *testing.T) {
	t.Parallel()

	compiler := &runcode.ToolchainCompiler{GoBinary: "/opt/golang/bin/go"}
	message := runcode.DescribeUnavailable(compiler)
	if !strings.Contains(message, "/opt/golang/bin/go") {
		t.Errorf("DescribeUnavailable() = %q, want the configured binary named", message)
	}
	if !strings.Contains(message, runcode.MinimumGoVersion) {
		t.Errorf("DescribeUnavailable() = %q, want the minimum Go version", message)
	}

	// A compiler that cannot explain itself, and no compiler at all, both fall
	// back to the default rather than to an empty string: the node's message
	// must never be blank.
	for name, compiler := range map[string]runcode.Compiler{
		"nil":            nil,
		"not a reporter": silentCompiler{},
		"typed nil":      (*runcode.ToolchainCompiler)(nil),
		"empty GoBinary": &runcode.ToolchainCompiler{},
	} {
		if message := runcode.DescribeUnavailable(compiler); !strings.Contains(message, "cannot compile Code nodes") {
			t.Errorf("DescribeUnavailable(%s) = %q, want the default explanation", name, message)
		}
	}
}

// silentCompiler is a compiler that offers no explanation of its own.
type silentCompiler struct{}

func (silentCompiler) Compile(ctx context.Context, source string) ([]byte, error) { return nil, nil }
func (silentCompiler) Available() bool                                            { return false }
