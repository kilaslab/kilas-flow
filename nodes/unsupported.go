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

// UnsupportedArities are the port counts the placeholder is registered at.
//
// The registry is keyed by {type, version} and immutable for the life of the
// process, so one definition cannot have a variable port count. An imported
// node's arity is only known from the edges the source workflow drew, and a
// placeholder whose declared ports do not cover those edges is rejected for an
// unknown port before its own always-fails validator is ever reached — which
// reads as a confusing topology error rather than as "this node is
// unsupported". Registering a small family and choosing the smallest member
// that covers the observed arity keeps the honest error and needs no new
// compiler capability.
//
// The version number *is* the port count, which is why these are not
// contiguous: a placeholder at version 4 has four inputs and four outputs. Eight
// covers every multi-output node in the corpus with room to spare; beyond that
// the import falls back to the largest member and reports the truncation.
var UnsupportedArities = []int{1, 2, 4, 8}

// UnsupportedArityFor is the smallest registered arity that covers a node with
// the given number of input and output slots.
func UnsupportedArityFor(inputs, outputs int) int {
	needed := inputs
	if outputs > needed {
		needed = outputs
	}
	for _, arity := range UnsupportedArities {
		if arity >= needed {
			return arity
		}
	}
	return UnsupportedArities[len(UnsupportedArities)-1]
}

// unsupportedAIPorts are the typed attachment ports every placeholder declares,
// in both directions, regardless of arity.
//
// An imported LangChain node has no mapping yet, so it becomes a placeholder —
// and the ai_languageModel, ai_memory and ai_tool edges around it have to land
// somewhere or the document will not compile at all, which is a worse outcome
// than losing the edges. Declaring them costs nothing: the compiler requires
// only incoming `main` connections, and treats a typed attachment port as
// optional by nature. Both directions are declared because a placeholder may
// stand in for either half of an AI edge — the agent that consumes a model, or
// the model that supplies one.
func unsupportedAIPorts(prefix string) []workflow.Port {
	return []workflow.Port{
		{Name: prefix + "Model", Kind: workflow.ConnectionLanguageModel},
		{Name: prefix + "Memory", Kind: workflow.ConnectionMemory},
		{Name: prefix + "Tool", Kind: workflow.ConnectionTool},
	}
}

// UnsupportedOutputPorts names a placeholder's output ports at one arity.
//
// The names match what the n8n adapter derives from an output index, so the
// mapping from an n8n output slot to a KilasFlow port is its own inverse. The
// first is "main" rather than "output0" because every single-output node in the
// system calls its one output that, and a placeholder should not be the
// exception.
func UnsupportedOutputPorts(arity int) []workflow.Port {
	ports := make([]workflow.Port, 0, arity+3)
	for index := 0; index < arity; index++ {
		ports = append(ports, workflow.Port{Name: unsupportedOutputPortName(index), Kind: workflow.ConnectionMain})
	}
	return append(ports, unsupportedAIPorts("out")...)
}

// UnsupportedInputPorts names a placeholder's input ports at one arity.
func UnsupportedInputPorts(arity int) []workflow.Port {
	ports := make([]workflow.Port, 0, arity+3)
	for index := 0; index < arity; index++ {
		ports = append(ports, workflow.Port{Name: unsupportedInputPortName(index), Kind: workflow.ConnectionMain})
	}
	return append(ports, unsupportedAIPorts("in")...)
}

func unsupportedOutputPortName(index int) string {
	if index == 0 {
		return "main"
	}
	return fmt.Sprintf("output%d", index)
}

func unsupportedInputPortName(index int) string {
	if index == 0 {
		return "main"
	}
	return fmt.Sprintf("input%d", index+1)
}

// unsupportedNode keeps an imported node visible without letting it run.
//
// It exists so an import never has to choose between dropping a node the user
// can no longer see and silently mapping it onto a different node that would
// do something else. The placeholder preserves the original identity and the
// whole original node, renders on the canvas, and fails compilation — so a
// workflow containing one can be opened and edited but never activated or run.
func unsupportedNode(arity int) node.Definition {
	return node.Definition{
		Type:        UnsupportedNodeType,
		Version:     workflow.V(arity),
		DisplayName: "Unsupported node",
		Description: "An imported node KilasFlow has no equivalent for. Replace it before running this workflow.",
		Category:    "Imported",
		Inputs:      UnsupportedInputPorts(arity),
		Outputs:     UnsupportedOutputPorts(arity),
		Parameters: []node.PropertyDefinition{
			{Key: "originalType", Label: "Original node type", Kind: node.PropertyString, Required: true},
			{Key: "originalTypeVersion", Label: "Original type version", Kind: node.PropertyNumber},
			{
				// The capsule is a nested object, and the property model has
				// no object kind yet; keyValue is the closest that exists and
				// the value is not checked against the declared kind. Widening
				// the model belongs to the node-metadata phase, not here.
				Key: "original", Label: "Original definition", Kind: node.PropertyKeyValue,
				Description: "The imported node exactly as n8n wrote it, kept so an export returns it whole.",
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
