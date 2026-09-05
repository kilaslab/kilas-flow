package workflow

import (
	"fmt"
	"sort"
)

// Catalog supplies node metadata to the compiler. The later node registry
// implements this interface; workflow itself never depends on that package.
type Catalog interface {
	Lookup(nodeType string, version TypeVersion) (NodeDefinition, bool)
}

// TypeCatalog is an optional extension that lets a registry report an unknown
// version separately from a wholly unknown node type.
type TypeCatalog interface {
	Catalog
	HasType(nodeType string) bool
}

// NodeDefinition is the compiler-facing portion of registered node metadata.
type NodeDefinition struct {
	Type               string
	Version            TypeVersion
	Inputs             []Port
	Outputs            []Port
	RequiredParameters []string
	ExecutorID         string
	Validate           ConfigValidator
}

// ConfigValidator validates a node's server-owned configuration during
// compilation, before an execution record can be created.
type ConfigValidator func(Node) error

// Port is a typed input or output exposed by a node definition.
type Port struct {
	Name string
	Kind ConnectionKind
}

// IR is the internal, validated graph passed to the execution engine. Runtime
// code must execute this representation rather than JSON documents.
type IR struct {
	WorkflowID string
	Name       string
	Nodes      []IRNode
	Edges      []IREdge
	Settings   map[string]any
}

// IRNode is a validated, copied node. It intentionally does not embed Node so
// a later mutation of canonical JSON cannot change a compiled execution plan.
type IRNode struct {
	ID          string
	Name        string
	Type        string
	TypeVersion TypeVersion
	Position    Position
	Parameters  map[string]any
	Credentials map[string]string
	Settings    map[string]any
	Definition  NodeDefinition
}

// IREdge is a validated graph edge. SourceOutputIndex resolves a user-facing
// labelled output, such as IF's false port, to item-output semantics.
type IREdge struct {
	ID                string
	Kind              ConnectionKind
	Source            Endpoint
	SourceOutputIndex int
	Target            Endpoint
}

// ErrorCode is stable for REST problem responses and editor field mapping.
type ErrorCode string

const (
	ErrorInvalidTopology  ErrorCode = "workflow.invalid_topology"
	ErrorUnknownNode      ErrorCode = "node.unknown_type"
	ErrorUnknownVersion   ErrorCode = "node.unknown_version"
	ErrorUnknownPort      ErrorCode = "port.unknown"
	ErrorIncompatiblePort ErrorCode = "port.incompatible"
	ErrorRequiredConfig   ErrorCode = "config.required"
	ErrorInvalidConfig    ErrorCode = "config.invalid"
)

// ValidationError identifies one invalid executable concern without leaking
// persistence or implementation details.
type ValidationError struct {
	Code         ErrorCode `json:"code"`
	Path         string    `json:"path"`
	NodeID       string    `json:"nodeId,omitempty"`
	ConnectionID string    `json:"connectionId,omitempty"`
	Message      string    `json:"message"`
}

// ValidationErrors is returned when a draft is structurally readable but not
// executable. Issues are sorted deterministically before it is returned.
type ValidationErrors struct {
	Issues []ValidationError `json:"errors"`
}

func (errors *ValidationErrors) Error() string {
	if len(errors.Issues) == 0 {
		return "workflow validation failed"
	}
	return errors.Issues[0].Message
}

func (errors *ValidationErrors) add(issue ValidationError) {
	errors.Issues = append(errors.Issues, issue)
}

func (errors *ValidationErrors) sort() {
	sort.Slice(errors.Issues, func(i, j int) bool {
		left, right := errors.Issues[i], errors.Issues[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		return left.Message < right.Message
	})
}

// Compile translates a canonical document into IR and performs all registry-
// dependent validation. It never executes the document directly.
func Compile(document Document, catalog Catalog) (IR, error) {
	if err := ValidateDraft(document); err != nil {
		return IR{}, err
	}

	issues := &ValidationErrors{}
	if len(document.Nodes) == 0 {
		issues.add(ValidationError{
			Code:    ErrorInvalidTopology,
			Path:    "/nodes",
			Message: "workflow graph must contain at least one node",
		})
	}
	definitions := make(map[string]NodeDefinition, len(document.Nodes))
	nodes := make(map[string]Node, len(document.Nodes))
	ir := IR{
		WorkflowID: document.ID,
		Name:       document.Name,
		Nodes:      make([]IRNode, 0, len(document.Nodes)),
		Edges:      make([]IREdge, 0, len(document.Connections)),
		Settings:   cloneAnyMap(document.Settings),
	}
	type edgeKey struct {
		kind                   ConnectionKind
		sourceNode, sourcePort string
		targetNode, targetPort string
	}
	edgeKeys := make(map[edgeKey]struct{}, len(document.Connections))

	for index, node := range document.Nodes {
		definition, found := catalog.Lookup(node.Type, node.TypeVersion)
		if !found {
			code := ErrorUnknownNode
			message := fmt.Sprintf("node type %q version %s is not registered", node.Type, node.TypeVersion)
			if typedCatalog, known := catalog.(TypeCatalog); known && typedCatalog.HasType(node.Type) {
				code = ErrorUnknownVersion
				message = fmt.Sprintf("node type %q does not support version %s", node.Type, node.TypeVersion)
			}
			issues.add(ValidationError{
				Code: code, Path: fmt.Sprintf("/nodes/%d/type", index), NodeID: node.ID,
				Message: message,
			})
			continue
		}
		definitions[node.ID] = definition
		nodes[node.ID] = node
		ir.Nodes = append(ir.Nodes, IRNode{
			ID:          node.ID,
			Name:        node.Name,
			Type:        node.Type,
			TypeVersion: definition.Version,
			Position:    node.Position,
			Parameters:  cloneAnyMap(node.Parameters),
			Credentials: cloneStringMap(node.Credentials),
			Settings:    cloneAnyMap(node.Settings),
			Definition:  cloneNodeDefinition(definition),
		})
		for _, required := range definition.RequiredParameters {
			if node.Parameters == nil || node.Parameters[required] == nil {
				issues.add(ValidationError{
					Code: ErrorRequiredConfig, Path: fmt.Sprintf("/nodes/%d/parameters/%s", index, required), NodeID: node.ID,
					Message: fmt.Sprintf("node %q requires parameter %q", node.ID, required),
				})
			}
		}
		if definition.Validate != nil {
			if err := definition.Validate(node); err != nil {
				issues.add(ValidationError{
					Code: ErrorInvalidConfig, Path: fmt.Sprintf("/nodes/%d/parameters", index), NodeID: node.ID,
					Message: fmt.Sprintf("node %q configuration is invalid: %v", node.ID, err),
				})
			}
		}
	}

	for index, connection := range document.Connections {
		sourceNode, sourceFound := nodes[connection.Source.NodeID]
		targetNode, targetFound := nodes[connection.Target.NodeID]
		if !sourceFound || !targetFound {
			issues.add(ValidationError{
				Code: ErrorInvalidTopology, Path: fmt.Sprintf("/connections/%d", index), ConnectionID: connection.ID,
				Message: "connection must reference registered source and target nodes",
			})
			continue
		}

		sourcePort, sourceIndex, sourcePortFound := outputPort(definitions[sourceNode.ID], connection.Source.Port)
		targetPort, targetPortFound := inputPort(definitions[targetNode.ID], connection.Target.Port)
		if !sourcePortFound || !targetPortFound {
			issues.add(ValidationError{
				Code: ErrorUnknownPort, Path: fmt.Sprintf("/connections/%d", index), ConnectionID: connection.ID,
				Message: "connection must reference declared source and target ports",
			})
			continue
		}
		if connection.Kind != sourcePort.Kind || connection.Kind != targetPort.Kind {
			issues.add(ValidationError{
				Code: ErrorIncompatiblePort, Path: fmt.Sprintf("/connections/%d/kind", index), ConnectionID: connection.ID,
				Message: "connection kind must match both endpoint ports",
			})
			continue
		}
		key := edgeKey{
			kind:       connection.Kind,
			sourceNode: connection.Source.NodeID,
			sourcePort: connection.Source.Port,
			targetNode: connection.Target.NodeID,
			targetPort: connection.Target.Port,
		}
		if _, duplicate := edgeKeys[key]; duplicate {
			issues.add(ValidationError{
				Code: ErrorInvalidTopology, Path: fmt.Sprintf("/connections/%d", index), ConnectionID: connection.ID,
				Message: "workflow graph must not contain duplicate connections",
			})
			continue
		}
		edgeKeys[key] = struct{}{}

		ir.Edges = append(ir.Edges, IREdge{
			ID: connection.ID, Kind: connection.Kind, Source: connection.Source,
			SourceOutputIndex: sourceIndex, Target: connection.Target,
		})
	}

	if len(issues.Issues) == 0 {
		if hasCycle(ir.Edges) {
			issues.add(ValidationError{
				Code:    ErrorInvalidTopology,
				Path:    "/connections",
				Message: "workflow graph must not contain a cycle",
			})
		}
		validateExecutableTopology(ir, issues)
	}

	if len(issues.Issues) > 0 {
		issues.sort()
		return IR{}, issues
	}

	return ir, nil
}

// validateExecutableTopology rejects drafts that can be saved but cannot be
// run by the native graph engine. A runnable graph has one trigger-like root
// (a registered node with no declared inputs), every non-root node receives
// data on each declared input port, and every node is reachable from that
// root: a node with no inputs that begins item flow.
// producesItems reports whether a node emits on the item channel, which is
// what separates a trigger from a configuration provider.
// isAnnotation reports a node that declares no ports in either direction. Such
// a node cannot be connected to anything, so it neither begins a run nor
// belongs to one; it is drawn on the canvas and otherwise ignored.
func isAnnotation(definition NodeDefinition) bool {
	return len(definition.Inputs) == 0 && len(definition.Outputs) == 0
}

func producesItems(ports []Port) bool {
	for _, port := range ports {
		if port.Kind == ConnectionMain {
			return true
		}
	}
	return false
}

func validateExecutableTopology(ir IR, issues *ValidationErrors) {
	incoming := make(map[string]map[string]int, len(ir.Nodes))
	adjacent := make(map[string][]string, len(ir.Nodes))
	for _, edge := range ir.Edges {
		if incoming[edge.Target.NodeID] == nil {
			incoming[edge.Target.NodeID] = make(map[string]int)
		}
		incoming[edge.Target.NodeID][edge.Target.Port]++
		adjacent[edge.Source.NodeID] = append(adjacent[edge.Source.NodeID], edge.Target.NodeID)
	}

	roots := make([]IRNode, 0, 1)
	for index, node := range ir.Nodes {
		if len(node.Definition.Inputs) == 0 {
			if len(incoming[node.ID]) > 0 {
				issues.add(ValidationError{
					Code: ErrorInvalidTopology, Path: fmt.Sprintf("/nodes/%d", index), NodeID: node.ID,
					Message: "workflow trigger must not have incoming connections",
				})
			}
			// A node with no inputs is a trigger only if it starts item flow.
			// A chat model, a memory, or a tool also has no inputs, but it
			// supplies configuration to the node it attaches to rather than
			// beginning a run — counting one as a trigger would make every
			// agent graph look like it had several roots.
			if producesItems(node.Definition.Outputs) {
				roots = append(roots, node)
			}
			continue
		}
		for _, port := range node.Definition.Inputs {
			// Only the item channel is required. A typed attachment port such as
			// ai_memory or ai_tool is optional by nature: an agent with no
			// memory and no tools is a perfectly valid agent.
			if port.Kind != ConnectionMain {
				continue
			}
			if incoming[node.ID][port.Name] == 0 {
				issues.add(ValidationError{
					Code: ErrorInvalidTopology, Path: fmt.Sprintf("/nodes/%d", index), NodeID: node.ID,
					Message: fmt.Sprintf("workflow node %q requires an incoming %q connection", node.ID, port.Name),
				})
			}
		}
	}

	if len(roots) != 1 {
		issues.add(ValidationError{
			Code: ErrorInvalidTopology, Path: "/nodes",
			Message: "workflow graph must contain exactly one trigger root",
		})
		return
	}

	reachable := map[string]struct{}{roots[0].ID: {}}
	queue := []string{roots[0].ID}
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		for _, targetID := range adjacent[nodeID] {
			if _, found := reachable[targetID]; found {
				continue
			}
			reachable[targetID] = struct{}{}
			queue = append(queue, targetID)
		}
	}
	// An attachment provider is reached through the node it configures, not
	// from the trigger: a chat model is downstream of nothing, yet it is
	// plainly part of the graph. Walking attachment edges backwards from
	// reachable nodes is what makes an agent's model, memory, and tools count
	// as connected. Repeating until nothing changes covers a provider that
	// attaches to another provider.
	for {
		grew := false
		for _, edge := range ir.Edges {
			if edge.Kind == ConnectionMain {
				continue
			}
			if _, targetReached := reachable[edge.Target.NodeID]; !targetReached {
				continue
			}
			if _, sourceReached := reachable[edge.Source.NodeID]; sourceReached {
				continue
			}
			reachable[edge.Source.NodeID] = struct{}{}
			grew = true
		}
		if !grew {
			break
		}
	}
	for index, node := range ir.Nodes {
		// A node with no ports at all takes part in no connection, so it can
		// never be reached and is not an orphan. An annotation is the case
		// that matters: a sticky note is documentation drawn on the canvas,
		// and requiring it to be wired to the trigger would make every
		// annotated workflow permanently unactivatable.
		if isAnnotation(node.Definition) {
			continue
		}
		if _, found := reachable[node.ID]; !found {
			issues.add(ValidationError{
				Code: ErrorInvalidTopology, Path: fmt.Sprintf("/nodes/%d", index), NodeID: node.ID,
				Message: fmt.Sprintf("workflow node %q is disconnected from the trigger", node.ID),
			})
		}
	}
}

func hasCycle(edges []IREdge) bool {
	adjacent := make(map[string][]string)
	for _, edge := range edges {
		adjacent[edge.Source.NodeID] = append(adjacent[edge.Source.NodeID], edge.Target.NodeID)
	}

	const (
		unvisited = iota
		visiting
		visited
	)
	states := make(map[string]int, len(adjacent))
	var visit func(string) bool
	visit = func(nodeID string) bool {
		states[nodeID] = visiting
		for _, targetID := range adjacent[nodeID] {
			switch states[targetID] {
			case visiting:
				return true
			case unvisited:
				if visit(targetID) {
					return true
				}
			}
		}
		states[nodeID] = visited
		return false
	}

	for nodeID := range adjacent {
		if states[nodeID] == unvisited && visit(nodeID) {
			return true
		}
	}
	return false
}

func cloneNodeDefinition(definition NodeDefinition) NodeDefinition {
	return NodeDefinition{
		Type:               definition.Type,
		Version:            definition.Version,
		Inputs:             append([]Port(nil), definition.Inputs...),
		Outputs:            append([]Port(nil), definition.Outputs...),
		RequiredParameters: append([]string(nil), definition.RequiredParameters...),
		ExecutorID:         definition.ExecutorID,
		Validate:           definition.Validate,
	}
}

func cloneAnyMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = cloneJSONValue(value)
	}
	return cloned
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAnyMap(typed)
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = cloneJSONValue(item)
		}
		return cloned
	default:
		return value
	}
}

func inputPort(definition NodeDefinition, name string) (Port, bool) {
	for _, port := range definition.Inputs {
		if port.Name == name {
			return port, true
		}
	}
	return Port{}, false
}

func outputPort(definition NodeDefinition, name string) (Port, int, bool) {
	for index, port := range definition.Outputs {
		if port.Name == name {
			return port, index, true
		}
	}
	return Port{}, 0, false
}
