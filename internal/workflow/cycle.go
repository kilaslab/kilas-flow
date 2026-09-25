package workflow

import "sort"

// IllegalCycles returns the cycles Compile refuses a workflow for: one for
// each group of nodes that loop among themselves other than through a back
// edge onto a loop node. Each cycle is a list of node IDs in the order the
// flow runs, starting where the flow enters it; the edge back to the first
// node is implied.
//
// It is Compile's own rule on Compile's own graph, asked on its own. An
// importer uses it to report a cycle before anybody tries to run the
// workflow, and because it is the same rule the report and the run cannot
// disagree (BUG-gk7mf5). It differs from Compile in one way only, and on
// purpose: Compile looks for a cycle once nothing else is wrong, while this
// looks regardless — so a workflow with a credential to re-bind and a cycle
// is told about both at once, not about the cycle only after the credential
// is fixed.
func IllegalCycles(document Document, catalog Catalog) [][]string {
	nodes := make(map[string]Node, len(document.Nodes))
	definitions := make(map[string]NodeDefinition, len(document.Nodes))
	order := make([]string, 0, len(document.Nodes))
	for _, node := range document.Nodes {
		definition, found := resolveDefinition(catalog, node)
		if !found {
			continue
		}
		nodes[node.ID] = node
		definitions[node.ID] = definition
		order = append(order, node.ID)
	}
	edges := make([]IREdge, 0, len(document.Connections))
	for _, connection := range document.Connections {
		if edge, _, fault := resolveEdge(connection, nodes, definitions); fault == edgeResolved {
			edges = append(edges, edge)
		}
	}
	return illegalCycles(order, edges, definitions)
}

// cyclesAmong returns one cycle from each group of nodes that can reach one
// another — a strongly connected component with more than one node, or one
// node wired to itself.
//
// One per group rather than every cycle: the number of distinct cycles in a
// dense group grows exponentially, and one named path through each group is
// what an author needs to find it. Groups are ordered by their first node in
// order, the workflow's own node order; a node order does not list comes
// after those, in the order the edges name it. Either way the result never
// depends on map iteration.
func cyclesAmong(order []string, edges []IREdge) [][]string {
	adjacent := make(map[string][]string)
	position := make(map[string]int, len(order))
	sequence := make([]string, 0, len(order))
	place := func(nodeID string) {
		if _, placed := position[nodeID]; !placed {
			position[nodeID] = len(sequence)
			sequence = append(sequence, nodeID)
		}
	}
	for _, nodeID := range order {
		place(nodeID)
	}
	for _, edge := range edges {
		place(edge.Source.NodeID)
		place(edge.Target.NodeID)
		adjacent[edge.Source.NodeID] = append(adjacent[edge.Source.NodeID], edge.Target.NodeID)
	}

	var cycles [][]string
	for _, group := range stronglyConnected(sequence, adjacent) {
		member := make(map[string]bool, len(group))
		for _, nodeID := range group {
			member[nodeID] = true
		}
		start := entryOf(group, member, edges, position)
		if cycle := shortestCycleThrough(start, member, adjacent); len(cycle) > 0 {
			cycles = append(cycles, cycle)
		}
	}
	return cycles
}

// stronglyConnected is Tarjan's algorithm. Each group comes back sorted by
// position, and the groups by their first member.
func stronglyConnected(sequence []string, adjacent map[string][]string) [][]string {
	index := make(map[string]int, len(sequence))
	lowest := make(map[string]int, len(sequence))
	onStack := make(map[string]bool, len(sequence))
	stack := make([]string, 0, len(sequence))
	position := make(map[string]int, len(sequence))
	for at, nodeID := range sequence {
		position[nodeID] = at
	}
	groups := make([][]string, 0)
	counter := 0

	var visit func(string)
	visit = func(nodeID string) {
		index[nodeID], lowest[nodeID] = counter, counter
		counter++
		stack = append(stack, nodeID)
		onStack[nodeID] = true
		for _, next := range adjacent[nodeID] {
			if _, seen := index[next]; !seen {
				visit(next)
				lowest[nodeID] = min(lowest[nodeID], lowest[next])
			} else if onStack[next] {
				lowest[nodeID] = min(lowest[nodeID], index[next])
			}
		}
		if lowest[nodeID] != index[nodeID] {
			return
		}
		group := make([]string, 0, 1)
		for {
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[top] = false
			group = append(group, top)
			if top == nodeID {
				break
			}
		}
		groups = append(groups, group)
	}
	for _, nodeID := range sequence {
		if _, seen := index[nodeID]; !seen {
			visit(nodeID)
		}
	}

	for _, group := range groups {
		sort.Slice(group, func(i, j int) bool { return position[group[i]] < position[group[j]] })
	}
	sort.Slice(groups, func(i, j int) bool { return position[groups[i][0]] < position[groups[j][0]] })
	return groups
}

// entryOf is where the flow enters a group: its first member reached by an
// edge from outside it. A group nothing enters starts at its first member.
func entryOf(group []string, member map[string]bool, edges []IREdge, position map[string]int) string {
	entry := ""
	for _, edge := range edges {
		if member[edge.Source.NodeID] || !member[edge.Target.NodeID] {
			continue
		}
		if entry == "" || position[edge.Target.NodeID] < position[entry] {
			entry = edge.Target.NodeID
		}
	}
	if entry == "" {
		return group[0]
	}
	return entry
}

// shortestCycleThrough finds the shortest way from start back to itself
// without leaving the group, breadth first so the path named is the plainest
// one. It is empty when there is none, which for a group of one node means the
// node is not wired to itself and so is not a cycle at all.
func shortestCycleThrough(start string, member map[string]bool, adjacent map[string][]string) []string {
	parent := map[string]string{}
	seen := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range adjacent[current] {
			if !member[next] {
				continue
			}
			if next == start {
				path := []string{current}
				for path[len(path)-1] != start {
					path = append(path, parent[path[len(path)-1]])
				}
				for left, right := 0, len(path)-1; left < right; left, right = left+1, right-1 {
					path[left], path[right] = path[right], path[left]
				}
				return path
			}
			if seen[next] {
				continue
			}
			seen[next] = true
			parent[next] = current
			queue = append(queue, next)
		}
	}
	return nil
}
