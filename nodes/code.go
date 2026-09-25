package nodes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/runcode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// CodeExecutorID is the server-owned binding for the Go Code node.
const CodeExecutorID = "core.code"

// CodeNodeType is the Go Code node.
const CodeNodeType = "kilasflow.code"

// Code node modes, using n8n's names so an import needs no translation.
const (
	CodeModeAllItems = "runOnceForAllItems"
	CodeModeEachItem = "runOnceForEachItem"
)

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
				Default:     "return items, nil",
				TypeOptions: &node.TypeOptions{Rows: 12, Editor: node.EditorCode, EditorLanguage: node.EditorLanguageGo},
				Description: "The body of func run(items []Item) ([]Item, error). " +
					"The standard library is available; the filesystem, network, and environment are not.",
			},
			{
				Key: "mode", Label: "Mode", Kind: node.PropertyOptions, Default: CodeModeAllItems,
				Options: []node.PropertyOption{
					{Label: "Run once for all items", Value: CodeModeAllItems},
					{Label: "Run once for each item", Value: CodeModeEachItem},
				},
				Description: "Running once for each item calls the same compiled artifact per item, so it " +
					"costs one build either way — only the number of sandbox calls changes.",
			},
			{Key: "scriptTimeoutSeconds", Label: "Time limit (seconds)", Kind: node.PropertyNumber, Default: 10},
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
	switch mode := textParameter(n.Parameters, "mode"); mode {
	case "", CodeModeAllItems, CodeModeEachItem:
	default:
		return fmt.Errorf("mode %q is not supported", mode)
	}
	return runcode.ValidateSource(source)
}

// CodeExecutor compiles on demand and runs user code in the sandbox.
type CodeExecutor struct {
	compiler runcode.Compiler
	cache    runcode.Cache
	// modules outlives the per-call runners below, which is the whole point of
	// it: one executor serves every Code node in the process, so translated
	// machine code survives from one node execution to the next instead of
	// being thrown away and rebuilt each time.
	modules *runcode.ModuleCache
	limits  runcode.Limits
}

// NewCodeExecutor builds the Code node's executor.
func NewCodeExecutor(compiler runcode.Compiler, cache runcode.Cache, limits runcode.Limits) *CodeExecutor {
	return NewCodeExecutorWith(compiler, cache, nil, limits)
}

// NewCodeExecutorWith builds the Code node's executor with the deployment's
// caches.
//
// A nil cache of either kind is replaced rather than dereferenced, so this is
// the same executor NewCodeExecutor builds when a deployment has nothing
// durable to offer. The module cache is the one that is passed in from
// outside in preference to a fresh one: it has to outlive the per-call runners
// and it has to be the same object across every Code node in the process, or
// translated machine code is thrown away between two nodes running the same
// source.
func NewCodeExecutorWith(compiler runcode.Compiler, cache runcode.Cache, modules *runcode.ModuleCache, limits runcode.Limits) *CodeExecutor {
	if cache == nil {
		cache = runcode.NewMemoryCache()
	}
	if modules == nil {
		modules = runcode.NewModuleCache()
	}
	return &CodeExecutor{compiler: compiler, cache: cache, modules: modules, limits: limits}
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
	if seconds := timeoutParameter(ir.Parameters, "scriptTimeoutSeconds"); seconds > 0 {
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

	incoming := input["main"]
	runner := runcode.NewRunner(executor.compiler, executor.cache, executor.modules, limits)

	if textParameter(ir.Parameters, "mode") == CodeModeEachItem {
		// One call per item, against the same compiled artifact: the caches are
		// keyed by source hash and by module, so this multiplies sandbox calls
		// and neither builds nor translations. Each call sees a batch of one,
		// which is what makes the same body work in either mode.
		out := make([]workflow.Item, 0, len(incoming))
		for index, item := range incoming {
			produced, err := executor.call(ctx, ir, runner, source, []workflow.Item{item})
			if err != nil {
				return nil, err
			}
			for _, result := range produced {
				// Every item this call produced inherits the one item it was
				// given, because that is the only source it can have come from.
				out = append(out, withBinaryFrom(result, incoming, index))
			}
		}
		return workflow.NodeOutput{out}, nil
	}

	produced, err := executor.call(ctx, ir, runner, source, incoming)
	if err != nil {
		return nil, err
	}
	out := make([]workflow.Item, 0, len(produced))
	for index, item := range produced {
		out = append(out, withBinaryFrom(item, incoming, index))
	}
	return workflow.NodeOutput{out}, nil
}

// call runs one batch through the sandbox.
//
// Binary references are carried past the sandbox rather than through it. User
// code sees and edits JSON; a payload it never receives is one it cannot
// corrupt, and dropping the reference on the way out was silently losing an
// attachment the next node needed.
func (executor *CodeExecutor) call(ctx context.Context, ir workflow.IRNode, runner *runcode.Runner, source string, incoming []workflow.Item) ([]workflow.Item, error) {
	items := make([]runcode.Item, 0, len(incoming))
	for _, item := range incoming {
		items = append(items, runcode.Item{JSON: item.JSON})
	}
	result, err := runner.Run(ctx, source, items)
	if err != nil {
		// A compiler that is not installed is a deployment problem, not the
		// user's code being wrong, so it is reported as such — with what the
		// operator has to provide, since that is the only thing that fixes it.
		if errors.Is(err, runcode.ErrCompilerUnavailable) {
			return nil, fmt.Errorf("node %q: %s", ir.Name, runcode.DescribeUnavailable(executor.compiler))
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
	return out, nil
}

// withBinaryFrom re-attaches the binary the sandbox never saw.
//
// Positional, because that is the only correspondence there is: code that
// returns as many items as it received is the ordinary case, and code that
// reshapes the batch has no attachment to inherit.
func withBinaryFrom(item workflow.Item, incoming []workflow.Item, index int) workflow.Item {
	if index >= len(incoming) || len(incoming[index].Binary) == 0 {
		return item
	}
	item.Binary = make(map[string]workflow.BinaryRef, len(incoming[index].Binary))
	for key, reference := range incoming[index].Binary {
		item.Binary[key] = reference
	}
	return item
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
		status.Error = runcode.DescribeUnavailable(executor.compiler)
		return status
	}

	runner := runcode.NewRunner(executor.compiler, executor.cache, executor.modules, executor.limits)
	artifact, err := runner.Artifact(ctx, source)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Compiled = true
	status.CompiledAt = artifact.CompiledAt.Format(time.RFC3339)
	return status
}
