package runcode

import "fmt"

// MinimumGoVersion is the oldest Go toolchain that can build a Code node.
//
// It is the language version the wrapper's module declares, so a toolchain
// below it refuses the source the wrapper generates. Naming it in the
// diagnostic matters: "install Go" is not actionable advice for an operator
// who has Go 1.21 and an error they cannot read.
const MinimumGoVersion = "1.24"

// UnavailableMessage is what an operator is told when this deployment cannot
// compile Code nodes.
//
// It says what to provide, where the server looked, the two ways to provide
// it, that the default image carries no toolchain on purpose, and what still
// works without one. The last two are the difference between a diagnostic and
// a dead end: an operator who reads it must not conclude that Code nodes have
// stopped working, when the ones already compiled keep running from the
// artifact cache.
func UnavailableMessage(goBinary string) string {
	if goBinary == "" {
		goBinary = "go"
	}
	return fmt.Sprintf("This deployment cannot compile Code nodes: it looked for the Go toolchain "+
		"by running %q and found no toolchain there. Compiling a Code node needs the go command of "+
		"Go %s or newer together with its standard library, because every Code node is built to "+
		"WebAssembly on the machine that runs it. Provide one in either of two ways: mount a Go "+
		"toolchain into the container and point KILASFLOW_CODE_GO_BINARY — the code.go_binary "+
		"configuration key — at its go binary, or put the toolchain's bin directory on the PATH of "+
		"the kilasflow process. The default image carries no toolchain on purpose, to keep the image "+
		"small and free of a compiler on every host that runs a workflow; Code nodes that were "+
		"already compiled keep running from the artifact cache, and the native nodes need no "+
		"toolchain at all.", goBinary, MinimumGoVersion)
}

// UnavailableReporter is implemented by a Compiler that can explain, in terms
// of its own configuration, why it is not available.
type UnavailableReporter interface {
	WhyUnavailable() string
}

// DescribeUnavailable is the one sentence a deployment shows when a Code node
// cannot be compiled.
//
// It is used in three places that must agree — the node catalogue's
// `unavailable` field, the error a Code node run returns, and the compilation
// status the editor shows — so the explanation is written once, here. A
// compiler that cannot describe itself, including a nil one, falls back to the
// default rather than to an empty string.
func DescribeUnavailable(compiler Compiler) string {
	if reporter, ok := compiler.(UnavailableReporter); ok {
		if message := reporter.WhyUnavailable(); message != "" {
			return message
		}
	}
	return UnavailableMessage("go")
}

// WhyUnavailable names the binary this compiler looked for.
func (compiler *ToolchainCompiler) WhyUnavailable() string {
	return UnavailableMessage(compiler.binaryName())
}

// binaryName is the go command to run, defaulting to the one on PATH.
func (compiler *ToolchainCompiler) binaryName() string {
	if compiler == nil || compiler.GoBinary == "" {
		return "go"
	}
	return compiler.GoBinary
}
