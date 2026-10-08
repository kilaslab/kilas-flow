package engine

import (
	"sort"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// connectedOutputs is, for every node upstream of nodeID, the output of it
// that nodeID is connected to, by the node's name: what `$('X').all()`,
// `.first()` and `.last()` read when they are not told which output. That is
// n8n's default, so a node on an IF's false branch reads the false items, not
// both branches joined, and not the true ones.
//
// The connection is found as n8n finds it: walking upstream from the node
// breadth first over item connections — its inputs in the order its
// definition gives them, each input's connections in the order the workflow
// lists them — and taking the output on the first connection out of X the
// walk meets, however many nodes sit in between. A node that X does not feed
// is not in the map, and reads X's first output.
//
// The walk stays inside the graph the run was prepared with. A node left out
// of it (another trigger's branch) never leads back to a node that ran: were
// it downstream of one, it would be in the graph.
func connectedOutputs(graph preparedGraph, nodeID string) map[string]int {
	outputs := make(map[string]int)
	visited := map[string]bool{nodeID: true}
	queue := []string{nodeID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range inputOrder(graph, current) {
			if edge.Kind != workflow.ConnectionMain {
				continue
			}
			source := edge.Source.NodeID
			if name := graph.nodes[source].Name; name != "" {
				if _, found := outputs[name]; !found {
					outputs[name] = edge.SourceOutputIndex
				}
			}
			if !visited[source] {
				visited[source] = true
				queue = append(queue, source)
			}
		}
	}
	return outputs
}

// inputOrder is a node's incoming edges in the order n8n walks them: by the
// position of the input they reach in its definition, and the edges into one
// input in the order the workflow lists them, which for an imported workflow
// is the order n8n's connections object gave them. The prepared graph orders
// them by the input's name and then by source node ID, which agrees only by
// chance: two paths back to X through one input can then meet X on another
// output than the one n8n reads.
func inputOrder(graph preparedGraph, nodeID string) []workflow.IREdge {
	node := graph.nodes[nodeID]
	position := make(map[string]int, len(node.Definition.Inputs))
	for index, port := range node.Definition.Inputs {
		position[port.Name] = index
	}
	ordered := append([]workflow.IREdge(nil), graph.incoming[nodeID]...)
	sort.SliceStable(ordered, func(left, right int) bool {
		a, b := ordered[left], ordered[right]
		if position[a.Target.Port] != position[b.Target.Port] {
			return position[a.Target.Port] < position[b.Target.Port]
		}
		return graph.listed[a] < graph.listed[b]
	})
	return ordered
}

// connectedOutputsFor is connectedOutputs for one node, worked out once per
// run: the graph does not change while it runs.
func (state *runState) connectedOutputsFor(graph preparedGraph, nodeID string) map[string]int {
	if outputs, found := state.branches[nodeID]; found {
		return outputs
	}
	if state.branches == nil {
		state.branches = make(map[string]map[string]int)
	}
	outputs := connectedOutputs(graph, nodeID)
	state.branches[nodeID] = outputs
	return outputs
}
