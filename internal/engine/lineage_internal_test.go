package engine

import (
	"fmt"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

func lineageDepth(origin *workflow.PairedItem) int {
	depth := 0
	for level := origin; level != nil; level = level.Parent {
		depth++
	}
	return depth
}

// A loop that splits its items on every pass must not grow every item's stamp
// with every pass: a fan-out adds a level only up to maxLineageDepth, and what
// it drops is the middle, never the item's own origin, the nearest fan-outs or
// the root.
func TestAFanOutAddsALevelOnlyUpToTheBound(t *testing.T) {
	root := &workflow.PairedItem{SourceNodeID: "trigger", SourcePort: "main", ItemIndex: 3}
	origin := root
	var nearest []string
	for pass := 0; pass < 3*maxLineageDepth; pass++ {
		node := workflow.IRNode{ID: fmt.Sprintf("split-%d", pass), Definition: workflow.NodeDefinition{
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		}}
		output := workflow.NodeOutput{{{Paired: origin}, {Paired: origin}}}
		before := lineageDepth(origin)
		anchorFanOuts(node, output, 0)
		if lineageDepth(origin) != before {
			t.Fatalf("pass %d changed the lineage it was given, which other items share", pass)
		}
		origin = output[0][1].Paired
		if origin.SourceNodeID != node.ID || origin.ItemIndex != 1 {
			t.Fatalf("pass %d: the split item is %+v, want item 1 of %s", pass, origin, node.ID)
		}
		nearest = append([]string{node.ID}, nearest...)
	}

	if got := lineageDepth(origin); got != maxLineageDepth {
		t.Fatalf("lineage depth = %d after %d fan-outs, want the bound %d", got, 3*maxLineageDepth, maxLineageDepth)
	}
	level := origin
	for index := 0; index < maxLineageDepth-1; index++ {
		if level.SourceNodeID != nearest[index] {
			t.Fatalf("level %d names %s, want the fan-out %s", index, level.SourceNodeID, nearest[index])
		}
		level = level.Parent
	}
	if *level != *root {
		t.Fatalf("the last level is %+v, want the root %+v", level, root)
	}
}

// An item whose origin is its own is left alone: a one-to-one chain keeps the
// flat origins it always had.
func TestAnchorFanOutsLeavesUniqueAndLostOriginsAlone(t *testing.T) {
	node := workflow.IRNode{ID: "set", Definition: workflow.NodeDefinition{
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
	}}
	first := &workflow.PairedItem{SourceNodeID: "trigger", ItemIndex: 0}
	second := &workflow.PairedItem{SourceNodeID: "trigger", ItemIndex: 1}
	lost := &workflow.PairedItem{SourceNodeID: "code", ItemIndex: 0, Lost: true}
	output := workflow.NodeOutput{{{Paired: first}, {Paired: second}, {Paired: lost}, {Paired: lost}, {}}}
	anchorFanOuts(node, output, 0)
	for index, want := range []*workflow.PairedItem{first, second, lost, lost, nil} {
		if output[0][index].Paired != want {
			t.Errorf("item %d stamped %+v, want it left as %+v", index, output[0][index].Paired, want)
		}
	}
}
