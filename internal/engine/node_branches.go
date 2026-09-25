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
// definition gives them, each input's connections in turn — and taking the
// output on the first connection out of X the walk meets, however many nodes
// sit in between. A node that X does not feed is not in the map, and reads
// X's first output.
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
		for _, edge := range inputOrder(graph.nodes[current], graph.incoming[current]) {
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

// inputOrder is a node's incoming edges by the position of the input they
// reach in its definition. The prepared graph orders them by the input's
// name, which is the same order only while the names happen to sort that way.
func inputOrder(node workflow.IRNode, edges []workflow.IREdge) []workflow.IREdge {
	position := make(map[string]int, len(node.Definition.Inputs))
	for index, port := range node.Definition.Inputs {
		position[port.Name] = index
	}
	ordered := append([]workflow.IREdge(nil), edges...)
	sort.SliceStable(ordered, func(left, right int) bool {
		return position[ordered[left].Target.Port] < position[ordered[right].Target.Port]
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
