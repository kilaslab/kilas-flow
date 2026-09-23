package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/jsrun"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The JavaScript Code node, and the placeholder imported Code nodes had
// before JavaScript ran.
const (
	JSCodeNodeType    = "kilasflow.jsCode"
	JSCodeExecutorID  = "core.jsCode"
	JSCodeDisplayName = "Code (JavaScript)"

	ForeignCodeNodeType    = "kilasflow.foreignCode"
	ForeignCodeExecutorID  = "core.foreignCode"
	ForeignCodeDisplayName = "Code (JavaScript or Python)"
)

// JavaScriptDisabled is what a JavaScript Code node says on a deployment that
// turned the runtime off, in the one sentence every code refusal uses.
var JavaScriptDisabled = jsrun.Refusal("is written in JavaScript",
	"The operator has turned the JavaScript runtime off on this server (code.javascript_enabled).")

// defaultJSCode is what a new Code (JavaScript) node starts with.
const defaultJSCode = "// The items this node receives are in `items`. Change them, or build new\n" +
	"// ones, and return the list to pass on.\n" +
	"for (const item of items) {\n" +
	"  item.json.checked = true;\n" +
	"}\n" +
	"return items;\n"

// jsCodeNode runs JavaScript the way n8n's Code node does: the same modes,
// the same globals, the same return shapes, so an imported node runs as
// written. It runs inside the server, on an embedded engine, and never in a
// Node.js process.
//
// Its parameters carry n8n's own names, so importing one is a copy and
// exporting it back gives the source byte for byte.
func jsCodeNode() node.Definition {
	return node.Definition{
		Type:        JSCodeNodeType,
		Version:     workflow.V(1),
		DisplayName: JSCodeDisplayName,
		Description: "Runs JavaScript over the node's items, with n8n's Code-node globals, inside the server.",
		Category:    "Core",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:code"},
		IconColor:   "#f59e0b",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "mode", Label: "Mode", Kind: node.PropertyOptions, Default: CodeModeAllItems,
				Options: []node.PropertyOption{
					{Label: "Run once for all items", Value: CodeModeAllItems},
					{Label: "Run once for each item", Value: CodeModeEachItem},
				},
				Description: "Once for all items gives the code `items` and `$input.all()`; once for each item gives it `$json` and `$itemIndex` and calls it per item.",
			},
			{
				Key: "jsCode", Label: "JavaScript", Kind: node.PropertyString, Required: true,
				Default:     defaultJSCode,
				TypeOptions: &node.TypeOptions{Rows: 16},
				Description: "The body of an async function: `await` works, and what it returns becomes the node's items. " +
					"require() offers crypto, lodash, luxon, util, buffer and url; there is no npm, filesystem or network, " +
					"and no Node.js process.",
			},
			{
				Key: "scriptTimeoutSeconds", Label: "Time limit (seconds)", Kind: node.PropertyNumber, Default: 10,
				Description: "Bounds the code's own running time. It can lower the server's limit, never raise it.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     JSCodeExecutorID,
		Validate:       validateJSCodeConfiguration,
		// The body sees its whole batch, so the runner must never split it
		// into one-item calls, not even to tolerate failures item by item.
		WholeBatch: true,
	}
}

// validateJSCodeConfiguration checks the code without running it: it must
// parse, and use nothing the runtime refuses.
func validateJSCodeConfiguration(n workflow.Node) error {
	source := textParameter(n.Parameters, "jsCode")
	if strings.TrimSpace(source) == "" {
		return errors.New("the code is empty")
	}
	mode := textParameter(n.Parameters, "mode")
	if mode != "" && mode != CodeModeAllItems && mode != CodeModeEachItem {
		return fmt.Errorf("mode %q is neither %s nor %s", mode, CodeModeAllItems, CodeModeEachItem)
	}
	_, err := jsrun.Analyze(source, jsrun.Mode(mode))
	return err
}

// JSCodeExecutor runs Code (JavaScript) nodes on the deployment's runtime.
type JSCodeExecutor struct {
	runner jsrun.Engine
	// disabled is why the deployment turned JavaScript off; empty when it
	// runs.
	disabled string
}

// defaultJSRunner is the runtime a caller that configures none gets, with
// the shipped limits, in this process. One per process, since a runner
// bounds concurrency across everything it runs. The server configures a
// worker pool instead (internal/jsworker); tests and tools use this.
var defaultJSRunner = sync.OnceValue(func() *jsrun.Runner { return jsrun.NewRunner(jsrun.Options{}) })

// Execute runs the node's code over its input. What the code printed is
// handed to the runner's console capture even when the code failed, which is
// usually when its author needs it.
func (executor *JSCodeExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if executor.disabled != "" {
		return nil, fmt.Errorf("node %q: %s", ir.Name, executor.disabled)
	}
	source := textParameter(ir.Parameters, "jsCode")
	if strings.TrimSpace(source) == "" {
		return nil, fmt.Errorf("node %q: the code is empty", ir.Name)
	}
	var limits jsrun.Limits
	if seconds := timeoutParameter(ir.Parameters, "scriptTimeoutSeconds"); seconds > 0 {
		// The runner keeps whichever is lower, so a node can tighten the
		// deployment's ceiling and never raise it.
		limits.Timeout = time.Duration(seconds * float64(time.Second))
	}
	result, err := executor.runner.Run(ctx, jsrun.Task{
		Source: source,
		Mode:   jsrun.Mode(textParameter(ir.Parameters, "mode")),
		Items:  input["main"],
		Roots:  jsRootsOf(ir, input, request),
		Limits: limits,
	})
	emitConsole(request, ir, result)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	items := result.Items
	if items == nil {
		items = []workflow.Item{}
	}
	return workflow.NodeOutput{items}, nil
}

// emitConsole hands what the code printed to the runner, which keeps it with
// the node's run and passes it to anyone watching live.
func emitConsole(request engine.Request, ir workflow.IRNode, result jsrun.Result) {
	if len(result.Console) == 0 && !result.ConsoleTruncated {
		return
	}
	detail := engine.ConsoleDetail{Lines: make([]engine.ConsoleLine, 0, len(result.Console)), Truncated: result.ConsoleTruncated}
	for _, line := range result.Console {
		detail.Lines = append(detail.Lines, engine.ConsoleLine{Level: line.Level, Text: line.Text, At: line.At})
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return
	}
	request.Events.Emit(engine.NodeEvent{NodeID: ir.ID, Name: engine.ConsoleEventName, Detail: encoded})
}

// foreignCodeNode is what an imported n8n Code node became before JavaScript
// ran, and what one written in Python still is.
//
// A distinct node type rather than the generic unsupported placeholder,
// because the two are different problems with different answers. An
// unsupported node type is something this product has not built; a Python
// Code node is something it deliberately does not run, with the source right
// there for the user to port.
//
// Workflows imported before the JavaScript runtime keep their JavaScript Code
// nodes as this type, so a JavaScript body here runs exactly as a Code
// (JavaScript) node does, without re-importing. Python refuses to compile: a
// node that quietly passed its items through would let the workflow run and
// be missing whatever the code was there to do.
func foreignCodeNode() node.Definition {
	return node.Definition{
		Type:        ForeignCodeNodeType,
		Version:     workflow.V(1),
		DisplayName: ForeignCodeDisplayName,
		Description: "An imported n8n Code node. JavaScript runs as a Code (JavaScript) node does; " +
			"Python is kept so you can port it, and the workflow cannot run until it is replaced.",
		Category:  "Core",
		Group:     []node.NodeGroup{node.GroupTransform},
		Icon:      &node.NodeIcon{Light: "builtin:code"},
		IconColor: "#f59e0b",
		Subtitle:  "{{ $parameter.language }}",
		Inputs:    mainInput(),
		Outputs:   mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "language", Label: "Language", Kind: node.PropertyString, Default: "javaScript",
				Description: "The language the imported node was written in.",
			},
			{
				Key: "mode", Label: "Mode", Kind: node.PropertyString,
				Description: "n8n's runOnceForAllItems or runOnceForEachItem, kept as it was written.",
			},
			{
				Key: "jsCode", Label: "JavaScript", Kind: node.PropertyString,
				TypeOptions: &node.TypeOptions{Rows: 12},
				Description: "The original JavaScript, kept exactly as it arrived. It runs.",
			},
			{
				Key: "pythonCode", Label: "Python", Kind: node.PropertyString,
				TypeOptions: &node.TypeOptions{Rows: 12},
				Description: "The original Python, kept exactly as it arrived. It is not run.",
			},
			{
				Key: "replacement", Label: "Suggested replacement", Kind: node.PropertyNotice,
				Description: "What this server suggests instead of the Python, worked out from the source when it recognised a common shape.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     ForeignCodeExecutorID,
		Validate:       validateForeignCodeConfiguration,
		WholeBatch:     true,
	}
}

// isJavaScript reports n8n's language parameter naming JavaScript, which is
// also what a Code node without one is.
func isJavaScript(language string) bool {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "", "javascript":
		return true
	}
	return false
}

func validateForeignCodeConfiguration(n workflow.Node) error {
	language := textParameter(n.Parameters, "language")
	if isJavaScript(language) {
		return validateJSCodeConfiguration(n)
	}
	suggestion := textParameter(n.Parameters, "replacement")
	if suggestion == "" {
		suggestion = SuggestReplacement(foreignSource(n.Parameters))
	}
	return errors.New(jsrun.Refusal("is written in "+languageName(language), suggestion))
}

// languageName turns n8n's spelling into something a message can use.
func languageName(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "python", "pythonnative":
		return "Python"
	case "", "javascript":
		return "JavaScript"
	default:
		return language
	}
}

func foreignSource(parameters map[string]any) string {
	if source := textParameter(parameters, "jsCode"); source != "" {
		return source
	}
	return textParameter(parameters, "pythonCode")
}

// foreignCodeExecutor runs a placeholder's JavaScript and refuses its Python,
// in the same sentence validation gives.
type foreignCodeExecutor struct {
	javaScript *JSCodeExecutor
}

func (executor foreignCodeExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if isJavaScript(textParameter(ir.Parameters, "language")) {
		return executor.javaScript.Execute(ctx, ir, input, request)
	}
	return nil, fmt.Errorf("node %q: %w", ir.Name, validateForeignCodeConfiguration(workflow.Node{Parameters: ir.Parameters}))
}

// replacementPattern recognises one common shape of Code node body.
type replacementPattern struct {
	// needles must all appear for the pattern to match.
	needles []string
	// advice names the native node that does the same job.
	advice string
}

// replacementPatterns is a curated table, not a translator.
//
// It suggests; it never rewrites. Translating one language into another is a
// compiler project with no correct stopping point, and silently different is
// the one outcome this codebase has consistently refused. It serves the
// Python Code nodes this server does not run.
//
// Ordered most specific first, because a body doing two of these is best
// described by the narrower one.
var replacementPatterns = []replacementPattern{
	{needles: []string{".reduce("}, advice: "An Aggregate or Summarize node does this without code."},
	{needles: []string{".flatMap("}, advice: "A Split Out node does this without code."},
	{needles: []string{".sort("}, advice: "A Sort node does this without code."},
	{needles: []string{".filter("}, advice: "A Filter node does this without code."},
	{needles: []string{".slice("}, advice: "A Limit node does this without code."},
	{needles: []string{"new Set("}, advice: "A Remove Duplicates node does this without code."},
	{needles: []string{"fetch("}, advice: "An HTTP Request node does this without code, and under the instance's egress policy."},
	{needles: []string{"$http"}, advice: "An HTTP Request node does this without code, and under the instance's egress policy."},
	{needles: []string{"Date.now("}, advice: "A Date & Time node does this without code."},
	{needles: []string{"new Date("}, advice: "A Date & Time node does this without code."},
	{needles: []string{".map("}, advice: "A Set node with expressions does this without code."},
}

// SuggestReplacement names the native node that most likely replaces a body.
//
// Exported so the importer produces the same sentence the node's own validator
// does. One wording for one situation: a user who reads the diagnostic on
// import and then opens the node should not be told two different things.
func SuggestReplacement(source string) string {
	for _, pattern := range replacementPatterns {
		matched := true
		for _, needle := range pattern.needles {
			if !strings.Contains(source, needle) {
				matched = false
				break
			}
		}
		if matched {
			return pattern.advice
		}
	}
	return "Replace it with the native nodes that do the same work — Filter, Switch, Set, Sort, " +
		"Aggregate, Split Out, Summarize, Remove Duplicates — or rewrite it in the Go Code node."
}
