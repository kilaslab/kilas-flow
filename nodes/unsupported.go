package nodes

import (
	"context"
	"fmt"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// UnsupportedExecutorID is the server-owned binding for the import placeholder.
const UnsupportedExecutorID = "core.unsupported"

// UnsupportedNodeType is what an unmappable imported node becomes.
const UnsupportedNodeType = "kilasflow.unsupported"

// unsupportedNode keeps an imported node visible without letting it run.
//
// It exists so an import never has to choose between dropping a node the user
// can no longer see and silently mapping it onto a different node that would
// do something else. The placeholder preserves the original identity and
// parameters, renders on the canvas, and fails compilation — so a workflow
// containing one can be opened and edited but never activated or run.
func unsupportedNode() node.Definition {
	return node.Definition{
		Type:        UnsupportedNodeType,
		Version:     1,
		DisplayName: "Unsupported node",
		Description: "An imported node KilasFlow has no equivalent for. Replace it before running this workflow.",
		Category:    "Imported",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{Key: "originalType", Label: "Original node type", Kind: node.PropertyString, Required: true},
			{Key: "originalTypeVersion", Label: "Original type version", Kind: node.PropertyNumber},
			{
				Key: "original", Label: "Original definition", Kind: node.PropertyString,
				Description: "The imported node's original JSON, kept so nothing is lost.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     UnsupportedExecutorID,
		Validate:       validateUnsupportedConfiguration,
	}
}

// validateUnsupportedConfiguration always fails.
//
// Failing at compile time rather than at run time is deliberate: the compiler
// is what gates activation and running, so this placeholder blocks both while
// still allowing the draft to be saved, opened, and edited.
func validateUnsupportedConfiguration(n workflow.Node) error {
	originalType, _ := n.Parameters["originalType"].(string)
	if originalType == "" {
		originalType = "an unknown node"
	}
	return fmt.Errorf("this node was imported from %s, which KilasFlow does not support. Replace it before activating or running this workflow", originalType)
}

// executeUnsupported can never be reached through a compiled graph, because
// validation rejects the node first. It refuses loudly rather than silently
// passing items through, so a future path that skipped validation would fail
// visibly instead of quietly behaving like a no-op.
func executeUnsupported(_ context.Context, ir workflow.IRNode, _ workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	originalType, _ := ir.Parameters["originalType"].(string)
	return nil, fmt.Errorf("node %q was imported from %q and cannot run", ir.Name, originalType)
}
