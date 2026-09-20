package nodes

import (
	"context"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// StickyNoteExecutorID is the server-owned binding for the annotation node.
const StickyNoteExecutorID = "core.stickyNote"

// StickyNoteNodeType is a canvas annotation: a coloured rectangle carrying
// markdown, which the editor draws behind the graph.
const StickyNoteNodeType = "kilasflow.stickyNote"

// stickyNoteNode is documentation on the canvas, not a step in the graph.
//
// It has no ports at all, which is the whole point. It takes part in no
// connection, so it is neither a trigger root nor reachable from one, and the
// compiler exempts a portless node from both checks rather than treating it as
// an orphan. It is the most widely deployed node in n8n and almost every real
// workflow carries several, so without it no annotated workflow could ever be
// activated here.
func stickyNoteNode() node.Definition {
	return node.Definition{
		Type:        StickyNoteNodeType,
		Version:     workflow.V(1),
		DisplayName: "Sticky Note",
		Description: "A note on the canvas. It never runs and never affects a workflow's result.",
		Category:    "Annotation",
		Group:       []node.NodeGroup{node.GroupOrganization},
		Icon:        &node.NodeIcon{Light: "builtin:sticky-note"},
		IconColor:   "#eab308",
		Inputs:      []workflow.Port{},
		Outputs:     []workflow.Port{},
		Parameters: []node.PropertyDefinition{
			{
				Key: "content", Label: "Content", Kind: node.PropertyString,
				Description: "Markdown shown on the canvas.",
				Default:     "## Note",
			},
			{Key: "width", Label: "Width", Kind: node.PropertyNumber, Default: 240},
			{Key: "height", Label: "Height", Kind: node.PropertyNumber, Default: 160},
			{
				Key: "color", Label: "Colour", Kind: node.PropertyNumber, Default: 1,
				Description: "Palette index, carried through from n8n so an imported note keeps its colour.",
			},
		},
		ExecutorID: StickyNoteExecutorID,
	}
}

// executeStickyNote is unreachable through a compiled graph, because a note has
// no ports and so is never scheduled. It exists so the node satisfies the
// registry's rule that every definition binds a real executor, which is cheaper
// than teaching the compiler and the runner that some nodes have no executor at
// all.
func executeStickyNote(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
	return workflow.NodeOutput{}, nil
}
