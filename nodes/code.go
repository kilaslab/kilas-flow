package nodes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/runcode"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// CodeExecutorID is the server-owned binding for the Go Code node.
const CodeExecutorID = "core.code"

// CodeNodeType is the Go Code node.
const CodeNodeType = "kilasflow.code"

func codeNode() node.Definition {
	return node.Definition{
		Type:        CodeNodeType,
		Version:     workflow.V(1),
		DisplayName: "Code",
		Description: "Runs restricted Go in an isolated WebAssembly sandbox.",
		Category:    "Core",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:code"},
		IconColor:   "#64748b",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "code", Label: "Go code", Kind: node.PropertyString, Required: true,
				Default: "return items, nil",
				Description: "The body of func run(items []Item) ([]Item, error). " +
					"The standard library is available; the filesystem, network, and environment are not.",
			},
			{Key: "timeoutSeconds", Label: "Time limit (seconds)", Kind: node.PropertyNumber, Default: 10},
			{Key: "memoryMB", Label: "Memory limit (MB)", Kind: node.PropertyNumber, Default: 16},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     CodeExecutorID,
		Validate:       validateCodeConfiguration,
	}
}

// validateCodeConfiguration checks the shape of the source.
//
// Compilation deliberately does not happen here: a save must not block on a
// build, and the compiler may not even be present in this deployment.
func validateCodeConfiguration(n workflow.Node) error {
	source, _ := n.Parameters["code"].(string)
	if source == "" {
		return fmt.Errorf("code is required")
	}
	return runcode.ValidateSource(source)
}

// CodeExecutor compiles on demand and runs user code in the sandbox.
type CodeExecutor struct {
	compiler runcode.Compiler
	cache    runcode.Cache
	limits   runcode.Limits
}

// NewCodeExecutor builds the Code node's executor.
func NewCodeExecutor(compiler runcode.Compiler, cache runcode.Cache, limits runcode.Limits) *CodeExecutor {
	if cache == nil {
		cache = runcode.NewMemoryCache()
	}
	return &CodeExecutor{compiler: compiler, cache: cache, limits: limits}
}

// Execute runs the node's code once over all incoming items.
//
// Unlike the per-item nodes, code sees the whole batch: that is what lets a
// Code node filter, group, or aggregate, which is most of why someone reaches
// for one.
func (executor *CodeExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	source, _ := ir.Parameters["code"].(string)
	if source == "" {
		return nil, fmt.Errorf("node %q: code is required", ir.Name)
	}

	limits := executor.limits
	if seconds := numberValue(ir.Parameters["timeoutSeconds"]); seconds > 0 {
		requested := time.Duration(seconds * float64(time.Second))
		// A node may tighten the deployment's ceiling, never raise it.
		if limits.Timeout <= 0 || requested < limits.Timeout {
			limits.Timeout = requested
		}
	}
	if megabytes := numberValue(ir.Parameters["memoryMB"]); megabytes > 0 {
		pages := uint32(megabytes * 16) // 1 MiB is 16 pages of 64 KiB.
		if limits.MemoryPages == 0 || pages < limits.MemoryPages {
			limits.MemoryPages = pages
		}
	}

	items := make([]runcode.Item, 0, len(input["main"]))
	for _, item := range input["main"] {
		items = append(items, runcode.Item{JSON: item.JSON})
	}

	runner := runcode.NewRunner(executor.compiler, executor.cache, limits)
	result, err := runner.Run(ctx, source, items)
	if err != nil {
		// A compiler that is not installed is a deployment problem, not the
		// user's code being wrong, so it is reported as such.
		if errors.Is(err, runcode.ErrCompilerUnavailable) {
			return nil, fmt.Errorf("node %q: this deployment cannot compile Code nodes", ir.Name)
		}
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}

	out := make([]workflow.Item, 0, len(result.Items))
	for _, item := range result.Items {
		json := item.JSON
		if json == nil {
			json = map[string]any{}
		}
		out = append(out, workflow.Item{JSON: json})
	}
	return workflow.NodeOutput{out}, nil
}

// CompilationStatus reports whether one piece of source has a usable artifact.
//
// The editor uses it to show compilation state without running the workflow,
// which is what keeps compilation an artifact lifecycle in the user's mental
// model too.
type CompilationStatus struct {
	Hash       string `json:"hash"`
	Compiled   bool   `json:"compiled"`
	Available  bool   `json:"compilerAvailable"`
	CompiledAt string `json:"compiledAt,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Status compiles if needed and reports the outcome.
func (executor *CodeExecutor) Status(ctx context.Context, source string) CompilationStatus {
	status := CompilationStatus{
		Hash:      runcode.SourceHash(source),
		Available: executor.compiler != nil && executor.compiler.Available(),
	}
	if err := runcode.ValidateSource(source); err != nil {
		status.Error = err.Error()
		return status
	}
	if !status.Available {
		if _, found := executor.cache.Get(status.Hash); found {
			status.Compiled = true
			return status
		}
		status.Error = runcode.ErrCompilerUnavailable.Error()
		return status
	}

	runner := runcode.NewRunner(executor.compiler, executor.cache, executor.limits)
	artifact, err := runner.Artifact(ctx, source)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Compiled = true
	status.CompiledAt = artifact.CompiledAt.Format(time.RFC3339)
	return status
}
