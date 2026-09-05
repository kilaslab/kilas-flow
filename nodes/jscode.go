package nodes

import (
	"context"
	"fmt"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// The foreign-code placeholder.
const (
	ForeignCodeNodeType    = "kilasflow.foreignCode"
	ForeignCodeExecutorID  = "core.foreignCode"
	ForeignCodeDisplayName = "Code (JavaScript or Python)"
)

// foreignCodeNode holds an imported Code node this runtime cannot run.
//
// A distinct node type rather than the generic unsupported placeholder,
// because the two are different problems with different answers. An unsupported
// node type is something this product has not built; a Code node is something
// it has deliberately not built — the source is right there, the user wrote it,
// and what they need is to be told which native node now does the same job.
//
// It is named for what it is rather than for JavaScript alone: n8n's Code node
// carries Python under the same type, and a node labelled JavaScript holding
// Python would be a small lie in the one place the user is reading closely.
//
// It refuses to compile, like the generic placeholder does. A Code node that
// quietly passed its items through would be worse than one that stops: the
// workflow would run, produce plausible output, and be missing whatever the
// code was there to do.
func foreignCodeNode() node.Definition {
	return node.Definition{
		Type:        ForeignCodeNodeType,
		Version:     workflow.V(1),
		DisplayName: ForeignCodeDisplayName,
		Description: "An imported Code node written in a language this server does not run. " +
			"Its source is kept so you can port it; the workflow cannot be activated until it is replaced.",
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
				Description: "The original source, kept exactly as it arrived. It is not run.",
			},
			{
				Key: "pythonCode", Label: "Python", Kind: node.PropertyString,
				TypeOptions: &node.TypeOptions{Rows: 12},
				Description: "The original source, kept exactly as it arrived. It is not run.",
			},
			{
				Key: "replacement", Label: "Suggested replacement", Kind: node.PropertyNotice,
				Description: "What this server suggests instead, worked out from the source when it recognised a common shape.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     ForeignCodeExecutorID,
		Validate:       validateForeignCodeConfiguration,
	}
}

func validateForeignCodeConfiguration(n workflow.Node) error {
	language := textParameter(n.Parameters, "language")
	if language == "" {
		language = "JavaScript"
	}
	suggestion := textParameter(n.Parameters, "replacement")
	if suggestion == "" {
		suggestion = SuggestReplacement(foreignSource(n.Parameters))
	}
	return fmt.Errorf("this node's code is written in %s, which this server does not run. %s",
		languageName(language), suggestion)
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

// executeForeignCode never runs. Registered so the node has a binding like any
// other — an unbound executor ID is refused at registration — and so the error
// is the same sentence whether it comes from validation or from a run.
func executeForeignCode(_ context.Context, ir workflow.IRNode, _ workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
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
// It suggests; it never rewrites. Translating JavaScript to Go is a compiler
// project with no correct stopping point — a one-line `items.map(…)` translates
// cleanly, and the next body, with a closure over `$input` or a regex whose
// semantics differ, translates into Go that compiles and computes something
// else. Silently different is the one outcome this codebase has consistently
// refused.
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
