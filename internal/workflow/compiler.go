package workflow

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
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
	Type    string
	Version TypeVersion
	Inputs  []Port
	Outputs []Port
	// RequiredParameters is the static list, kept for a catalogue that does not
	// supply the callback below.
	RequiredParameters []string
	// RequiredFor computes which parameters a *particular* node must have.
	//
	// Required-ness stopped being a static property of the definition the
	// moment visibility became conditional: a node shaped like a real n8n node
	// — where chatId is required only when resource is message — would
	// otherwise be unactivatable in every other configuration. The shape
	// follows Validate, which is a callback for the same reason.
	RequiredFor func(parameters map[string]any, typeVersion TypeVersion) []string
	// PortsFor computes a node's ports from its own configuration.
	//
	// Some nodes' ports are not a property of the type. A Switch has one output
	// per rule the user wrote, and a Merge has as many inputs as it was told to
	// take — declaring a fixed maximum instead would show dead ports on the
	// canvas and refuse to import a workflow with one rule more than the
	// maximum. When set, the result replaces Inputs and Outputs for *this*
	// node, before any connection is resolved against them.
	PortsFor func(parameters map[string]any, typeVersion TypeVersion) (inputs, outputs []Port)
	// RequiredCredentials are the credential types the node cannot run without.
	//
	// It is checked here rather than in each node's Validate because a
	// generated pack has no Validate and cannot have one — that is the point of
	// a pack being data. Without this, a node whose whole request is built from
	// a credential compiles, activates, and fails at its first outbound call
	// with an error about a URL rather than about a missing credential.
	RequiredCredentials []string
	ExecutorID          string
	Validate            ConfigValidator
	// WebhookPathParameter is the parameter key holding this trigger's route
	// label, when it declares an inbound webhook.
	//
	// The compiler does not read it. It is here because the n8n adapter has to
	// know that a node it is importing needs a label — n8n mints its own route
	// and carries none — and asking the catalogue is what keeps that from
	// becoming a list of trigger type names in the adapter.
	WebhookPathParameter string
	// LoopEntry marks a node a back edge may legally close onto.
	//
	// It is a property of the definition rather than a node type the compiler
	// knows by name, so a pack-supplied loop node gets the same treatment
	// without the compiler learning a string. A general cycle stays rejected:
	// only an edge whose target is one of these is allowed to point backwards.
	LoopEntry bool
	// WholeBatch marks a node the runner must never split into one-item
	// calls; see node.Definition.
	WholeBatch bool
	// ErrorAsMessage marks a node whose tolerated failures carry `error` as
	// the message alone; see node.Definition.
	ErrorAsMessage bool
}

// ConfigValidator validates a node's server-owned configuration during
// compilation, before an execution record can be created.
type ConfigValidator func(Node) error

// Port is a typed input or output exposed by a node definition.
type Port struct {
	// Name is the port's stable identifier, used in a connection endpoint.
	Name string `json:"name"`
	// Kind is the channel this port speaks.
	Kind ConnectionKind `json:"kind"`
	// DisplayName is what the editor labels the port with. Empty falls back to
	// Name, so a port that needs no separate label declares none.
	DisplayName string `json:"displayName,omitempty"`
	// Required marks a port that must be connected for the workflow to run. An
	// agent with no language model cannot do anything, and saying so at compile
	// time beats failing on the first item.
	Required bool `json:"required,omitempty"`
	// MaxConnections bounds how many edges a port accepts. Zero means
	// unbounded, which is what a tool slot needs; one is what a language model
	// slot needs, and without it the compiler would accept three models on one
	// agent.
	MaxConnections int `json:"maxConnections,omitempty"`
	// AllowedNodeTypes, when non-empty, restricts which node types may connect
	// here. Enforced at compile time rather than only in the editor, so an
	// imported document cannot bypass it.
	AllowedNodeTypes []string `json:"allowedNodeTypes,omitempty"`
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
	// Disabled is the author's switch. The runtime never invokes such a node;
	// it passes its first item input through, and a disabled trigger is never
	// started.
	Disabled   bool
	Definition NodeDefinition
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
	ErrorNodeNotAvailable ErrorCode = "node.not_available" // a registered type scoped to other tenants; see RestrictedCatalog
	ErrorUnknownPort      ErrorCode = "port.unknown"
	ErrorIncompatiblePort ErrorCode = "port.incompatible"
	// Named separately from ErrorUnknownPort on purpose: "this port is full",
	// "this port must be connected" and "this port does not exist" are
	// different problems for a user to fix, and one code for all three tells
	// them nothing.
	ErrorPortFull       ErrorCode = "port.full"
	ErrorPortRequired   ErrorCode = "port.required"
	ErrorPortNotAllowed ErrorCode = "port.node_not_allowed"
	ErrorRequiredConfig ErrorCode = "config.required"
	ErrorInvalidConfig  ErrorCode = "config.invalid"
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
	if err := validateDocumentTimezone(document.Settings); err != nil {
		issues.add(ValidationError{
			Code:    ErrorInvalidConfig,
			Path:    "/settings/timezone",
			Message: err.Error(),
		})
	}
	if len(document.Nodes) == 0 {
		issues.add(ValidationError{
			Code:    ErrorInvalidTopology,
			Path:    "/nodes",
			Message: "workflow graph must contain at least one node",
		})
	}
	definitions := make(map[string]NodeDefinition, len(document.Nodes))
	nodes := make(map[string]Node, len(document.Nodes))
	// refused records the nodes whose type the catalogue would not resolve, so
	// the connection pass can leave their wires to the issue that explains
	// them.
	refused := make(map[string]bool, len(document.Nodes))
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
		definition, found := resolveDefinition(catalog, node)
		if !found {
			code := ErrorUnknownNode
			message := fmt.Sprintf("node type %q version %s is not registered", node.Type, node.TypeVersion)
			// Restricted is asked first: a type withheld from this tenant is not
			// visible to it, so HasType is false for it and would otherwise
			// report it as simply unregistered. The message says nothing about
			// which tenants the type is scoped to.
			if restricted, known := catalog.(RestrictedCatalog); known && restricted.Restricted(node.Type) {
				code = ErrorNodeNotAvailable
				message = fmt.Sprintf("node type %q is not available to this workspace", node.Type)
			} else if typedCatalog, known := catalog.(TypeCatalog); known && typedCatalog.HasType(node.Type) {
				code = ErrorUnknownVersion
				message = fmt.Sprintf("node type %q does not support version %s", node.Type, node.TypeVersion)
			}
			issues.add(ValidationError{
				Code: code, Path: fmt.Sprintf("/nodes/%d/type", index), NodeID: node.ID,
				Message: message,
			})
			refused[node.ID] = true
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
			Disabled:    node.Disabled,
			Definition:  cloneNodeDefinition(definition),
		})
		// A disabled node is never invoked, so what it would need in order to
		// run is not a reason to refuse the workflow: n8n imports routinely
		// carry a switched-off Gmail or HTTP node whose credential is long
		// gone, and refusing those would make the workflow unactivatable.
		if node.Disabled {
			continue
		}
		required := definition.RequiredParameters
		if definition.RequiredFor != nil {
			// Only the parameters this node's configuration actually shows.
			required = definition.RequiredFor(node.Parameters, node.TypeVersion)
		}
		for _, required := range required {
			if node.Parameters == nil || node.Parameters[required] == nil {
				issues.add(ValidationError{
					Code: ErrorRequiredConfig, Path: fmt.Sprintf("/nodes/%d/parameters/%s", index, required), NodeID: node.ID,
					Message: fmt.Sprintf("node %q requires parameter %q", node.ID, required),
				})
			}
		}
		for _, credentialType := range definition.RequiredCredentials {
			if attached, _ := node.Credentials[credentialType]; strings.TrimSpace(attached) == "" {
				issues.add(ValidationError{
					Code: ErrorRequiredConfig, Path: fmt.Sprintf("/nodes/%d/credentials/%s", index, credentialType),
					NodeID:  node.ID,
					Message: fmt.Sprintf("node %q requires a %s credential", node.ID, credentialType),
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
		// Shared settings are static, so checking them here gives the editor an
		// error before activation rather than a failure part-way through a run.
		for _, err := range validateSharedSettings(node.Settings) {
			issues.add(ValidationError{
				Code: ErrorInvalidConfig, Path: fmt.Sprintf("/nodes/%d/settings", index), NodeID: node.ID,
				Message: fmt.Sprintf("node %q setting is invalid: %v", node.ID, err),
			})
		}
	}

	for index, connection := range document.Connections {
		edge, targetPort, fault := resolveEdge(connection, nodes, definitions)
		switch fault {
		case edgeUnknownNode:
			// A node whose type was refused already carries the issue that
			// explains it — "not available to this workspace" is the sentence
			// the author has to read. This one sorts before it (connections
			// come first) and would be read instead, sending them hunting for a
			// wiring mistake: the endpoints ARE registered, it is the tenant
			// that may not use one of them.
			if refused[connection.Source.NodeID] || refused[connection.Target.NodeID] {
				continue
			}
			issues.add(ValidationError{
				Code: ErrorInvalidTopology, Path: fmt.Sprintf("/connections/%d", index), ConnectionID: connection.ID,
				Message: "connection must reference registered source and target nodes",
			})
			continue
		case edgeUnknownPort:
			issues.add(ValidationError{
				Code: ErrorUnknownPort, Path: fmt.Sprintf("/connections/%d", index), ConnectionID: connection.ID,
				Message: "connection must reference declared source and target ports",
			})
			continue
		case edgeIncompatibleKind:
			issues.add(ValidationError{
				Code: ErrorIncompatiblePort, Path: fmt.Sprintf("/connections/%d/kind", index), ConnectionID: connection.ID,
				Message: "connection kind must match both endpoint ports",
			})
			continue
		case edgeNodeNotAllowed:
			issues.add(ValidationError{
				Code: ErrorPortNotAllowed, Path: fmt.Sprintf("/connections/%d", index), ConnectionID: connection.ID,
				Message: fmt.Sprintf("port %q does not accept a connection from node type %q",
					targetPort.Name, nodes[connection.Source.NodeID].Type),
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

		ir.Edges = append(ir.Edges, edge)
	}

	if len(issues.Issues) == 0 {
		// Only whether there is a cycle matters here, so no node order is
		// given; IllegalCycles is what names them.
		if len(illegalCycles(nil, ir.Edges, definitions)) > 0 {
			issues.add(ValidationError{
				Code:    ErrorInvalidTopology,
				Path:    "/connections",
				Message: "workflow graph must not contain a cycle, except a back edge closing onto a loop node",
			})
		}
		validateExecutableTopology(ir, issues)
		validateChatTriggerUniqueness(ir, issues)
		validatePortCardinality(ir, issues)
		validateAgentToolNames(ir, issues)
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

// chatTriggerNodeType is the editor chat start. The compiler, not the node's
// own Validate, refuses a second copy: Validate sees one node, and n8n's
// maxNodes: 1 is a graph rule.
const chatTriggerNodeType = "kilasflow.chatTrigger"

func validateChatTriggerUniqueness(ir IR, issues *ValidationErrors) {
	seen := 0
	for index, node := range ir.Nodes {
		if node.Type != chatTriggerNodeType {
			continue
		}
		seen++
		if seen < 2 {
			continue
		}
		issues.add(ValidationError{
			Code: ErrorInvalidTopology, Path: fmt.Sprintf("/nodes/%d", index), NodeID: node.ID,
			Message: "a workflow may contain only one When chat message received node",
		})
	}
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

	// One trigger was never a property of workflows, only of the runtime that
	// executed them. Real n8n workflows routinely carry several — a webhook for
	// live traffic beside a schedule for a nightly catch-up, or a manual
	// trigger left in so the author can test by hand — and over half the import
	// corpus failed this rule before its node types were even considered.
	//
	// What must still hold is that there is somewhere to start and that nothing
	// is stranded, so a graph with no root is still refused and every node must
	// still be reachable from *some* root. Which root a given execution starts
	// from is the runner's business, not the compiler's.
	if len(roots) == 0 {
		issues.add(ValidationError{
			Code: ErrorInvalidTopology, Path: "/nodes",
			Message: "workflow graph must contain at least one trigger root",
		})
		return
	}

	reachable := make(map[string]struct{}, len(ir.Nodes))
	queue := make([]string, 0, len(roots))
	for _, root := range roots {
		reachable[root.ID] = struct{}{}
		queue = append(queue, root.ID)
	}
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

// illegalCycles finds the cycles that are not a declared loop, one for each
// group of nodes that loop among themselves (see cyclesAmong). Compile refuses
// the workflow when there is any; IllegalCycles names them to an importer.
//
// n8n's Split In Batches pattern is a cycle by construction: the loop node's
// body wires back into its input so the next batch is dispatched, and refusing
// every cycle made every workflow built on it unrepresentable. What is wanted is
// not general cycles — it is a *designated* loop node with a finite, knowable
// iteration bound, so an arbitrary back edge between two ordinary nodes stays
// rejected exactly as it was.
//
// The loop's closing edges are identified first and removed, then the remainder
// is checked for any cycle at all. Deciding edge by edge during one DFS looked
// simpler and was wrong: which edge of a cycle appears to be the back edge
// depends on where the traversal happens to start, so a graph could be accepted
// or rejected according to Go's map iteration order.
func illegalCycles(order []string, edges []IREdge, definitions map[string]NodeDefinition) [][]string {
	closing := loopClosingEdges(edges, definitions)
	remaining := make([]IREdge, 0, len(edges))
	for _, edge := range edges {
		if !closing[edge.ID] {
			remaining = append(remaining, edge)
		}
	}
	return cyclesAmong(order, remaining)
}

// loopClosingEdges finds the edges that close a declared loop: an edge whose
// target is a loop entry and whose source that entry can reach.
func loopClosingEdges(edges []IREdge, definitions map[string]NodeDefinition) map[string]bool {
	forward := make(map[string][]string, len(edges))
	for _, edge := range edges {
		forward[edge.Source.NodeID] = append(forward[edge.Source.NodeID], edge.Target.NodeID)
	}

	closing := map[string]bool{}
	for _, edge := range edges {
		if !definitions[edge.Target.NodeID].LoopEntry {
			continue
		}
		if reaches(forward, edge.Target.NodeID, edge.Source.NodeID) {
			closing[edge.ID] = true
		}
	}
	return closing
}

// reaches reports whether `to` is reachable from `from`.
func reaches(forward map[string][]string, from, to string) bool {
	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range forward[current] {
			if next == to {
				return true
			}
			if seen[next] {
				continue
			}
			seen[next] = true
			queue = append(queue, next)
		}
	}
	return false
}

func cloneNodeDefinition(definition NodeDefinition) NodeDefinition {
	return NodeDefinition{
		Type:                definition.Type,
		Version:             definition.Version,
		LoopEntry:           definition.LoopEntry,
		WholeBatch:          definition.WholeBatch,
		ErrorAsMessage:      definition.ErrorAsMessage,
		Inputs:              append([]Port(nil), definition.Inputs...),
		Outputs:             append([]Port(nil), definition.Outputs...),
		RequiredParameters:  append([]string(nil), definition.RequiredParameters...),
		RequiredFor:         definition.RequiredFor,
		PortsFor:            definition.PortsFor,
		RequiredCredentials: append([]string(nil), definition.RequiredCredentials...),
		ExecutorID:          definition.ExecutorID,
		Validate:            definition.Validate,
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

// resolveDefinition is the definition Compile checks a node against: the
// catalogue's, with the ports the node's own configuration gives it.
//
// It is shared with IllegalCycles so that a cycle named to an importer is
// found on exactly the graph Compile builds.
func resolveDefinition(catalog Catalog, node Node) (NodeDefinition, bool) {
	definition, found := catalog.Lookup(node.Type, node.TypeVersion)
	if !found {
		return NodeDefinition{}, false
	}
	// A node whose ports depend on its own parameters is resolved here,
	// once, before anything reads them: the connection check, the runner's
	// output arity and the editor all have to see the same list.
	if definition.PortsFor != nil {
		inputs, outputs := definition.PortsFor(node.Parameters, node.TypeVersion)
		definition.Inputs, definition.Outputs = inputs, outputs
	}
	// A node that routes failed items to its own error branch declares a
	// port for it. The port is the runner's — an executor knows nothing
	// about error routing — so the compiler adds it here, before any
	// connection is resolved against the node's ports.
	return withErrorPort(definition, node.Settings), true
}

// edgeFault is why a connection could not become an edge of the graph.
type edgeFault int

const (
	edgeResolved edgeFault = iota
	edgeUnknownNode
	edgeUnknownPort
	edgeIncompatibleKind
	edgeNodeNotAllowed
)

// resolveEdge turns a connection into the edge Compile adds to the graph, or
// says why it cannot. The target port is returned for the message a refusal
// needs. nodes and definitions hold only the nodes the catalogue resolved.
func resolveEdge(connection Connection, nodes map[string]Node, definitions map[string]NodeDefinition) (IREdge, Port, edgeFault) {
	sourceNode, sourceFound := nodes[connection.Source.NodeID]
	targetNode, targetFound := nodes[connection.Target.NodeID]
	if !sourceFound || !targetFound {
		return IREdge{}, Port{}, edgeUnknownNode
	}
	sourcePort, sourceIndex, sourcePortFound := outputPort(definitions[sourceNode.ID], connection.Source.Port)
	targetPort, targetPortFound := inputPort(definitions[targetNode.ID], connection.Target.Port)
	if !sourcePortFound || !targetPortFound {
		return IREdge{}, Port{}, edgeUnknownPort
	}
	if connection.Kind != sourcePort.Kind || connection.Kind != targetPort.Kind {
		return IREdge{}, targetPort, edgeIncompatibleKind
	}
	// A port may name which node types it accepts. Checking it here rather
	// than only in the editor is what stops an imported document from
	// bypassing the rule.
	if !portAcceptsNodeType(targetPort, sourceNode.Type) {
		return IREdge{}, targetPort, edgeNodeNotAllowed
	}
	return IREdge{
		ID: connection.ID, Kind: connection.Kind, Source: connection.Source,
		SourceOutputIndex: sourceIndex, Target: connection.Target,
	}, targetPort, edgeResolved
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

// MaxRetryAttempts bounds `maxTries`.
//
// The cap exists so a typo cannot turn one failing node into thousands of calls
// against an upstream that is already failing. Eight is well past any retry
// budget that is still a retry rather than a queue.
const MaxRetryAttempts = 8

// MaxLoopIterations is the instance-wide ceiling on a loop's iteration count.
//
// A per-node maximum is what a workflow author sets; this is what stops a
// mistaken or malicious workflow in a shared install from spinning regardless
// of what it asked for.
const MaxLoopIterations = 10_000

// MaxRetryWaitMilliseconds bounds `waitBetweenTries`, so a node cannot hold an
// execution slot open for the better part of an hour between attempts.
const MaxRetryWaitMilliseconds = 300_000

// validateDocumentTimezone checks the zone a workflow's schedules are read in.
//
// Refused at compile time rather than discovered when a schedule fires, because
// a bad zone name is silent otherwise: the trigger would fall back to UTC and
// run at the wrong hour every day, which is the kind of wrongness people
// attribute to anything but a typo in a settings field.
func validateDocumentTimezone(settings map[string]any) error {
	value, present := settings["timezone"]
	if !present || value == nil {
		return nil
	}
	name, ok := value.(string)
	if !ok {
		return fmt.Errorf("timezone must be an IANA zone name such as UTC or Asia/Jakarta")
	}
	if strings.TrimSpace(name) == "" {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(name), "Local") {
		return fmt.Errorf("timezone %q depends on where this server runs; name a zone such as UTC or Asia/Jakarta", name)
	}
	// n8n's "DEFAULT" is the sentinel for "whatever this instance is set to".
	// It is a real, resolvable answer rather than a typo, so it is accepted
	// here and resolved by the runtime; refusing it would leave an already
	// saved workflow unactivatable.
	if strings.EqualFold(strings.TrimSpace(name), "DEFAULT") {
		return nil
	}
	if _, err := time.LoadLocation(strings.TrimSpace(name)); err != nil {
		return fmt.Errorf("timezone %q is not a known IANA zone name", name)
	}
	return nil
}

// validateSharedSettings checks the settings every node declares.
//
// These are static values, unlike parameters that may hold expressions, so
// there is no reason to discover a bad one at run time. A non-numeric,
// negative or absurd `maxTries` is refused before the workflow can be
// activated.
func validateSharedSettings(settings map[string]any) []error {
	var problems []error
	for _, rule := range []struct {
		key      string
		min, max float64
	}{
		{"timeoutSeconds", 0, 86_400},
		{"maxTries", 1, MaxRetryAttempts},
		{"waitBetweenTries", 0, MaxRetryWaitMilliseconds},
	} {
		value, present := settings[rule.key]
		if !present || value == nil {
			continue
		}
		number, ok := settingNumber(value)
		if !ok {
			problems = append(problems, fmt.Errorf("%s must be a number", rule.key))
			continue
		}
		if number < rule.min || number > rule.max {
			problems = append(problems, fmt.Errorf("%s must be between %g and %g, got %g", rule.key, rule.min, rule.max, number))
		}
	}
	if value, present := settings["onError"]; present && value != nil {
		mode, ok := value.(string)
		if !ok {
			problems = append(problems, fmt.Errorf("onError must be one of stopWorkflow, continueRegularOutput or continueErrorOutput"))
		} else if !knownOnError(mode) {
			problems = append(problems, fmt.Errorf("onError %q is not one of stopWorkflow, continueRegularOutput or continueErrorOutput", mode))
		}
	}
	return problems
}

// The three ways a node may handle a failure, spelled as n8n spells them.
const (
	OnErrorStop            = "stopWorkflow"
	OnErrorContinueRegular = "continueRegularOutput"
	OnErrorContinueBranch  = "continueErrorOutput"
)

// errorPortName is the extra output a node that continues on a separate error
// branch declares. It is the runner that fills it; the compiler declares it so
// a connection from that port is a connection to a real port.
const errorPortName = "error"

// withErrorPort adds a node's own error output when it routes failed items to
// one.
//
// Without this a workflow with a wired error branch does not compile at all —
// the connection names a port the node does not declare — which is how 14 of
// the 100 most-viewed templates became unrunnable on import.
func withErrorPort(definition NodeDefinition, settings map[string]any) NodeDefinition {
	if mode, _ := settings["onError"].(string); mode != OnErrorContinueBranch {
		return definition
	}
	for _, port := range definition.Outputs {
		if port.Name == errorPortName {
			return definition
		}
	}
	// Copied rather than appended in place: the definition a catalogue handed
	// out may be shared, and a port added to it would follow the node type
	// rather than this node.
	outputs := make([]Port, 0, len(definition.Outputs)+1)
	outputs = append(outputs, definition.Outputs...)
	outputs = append(outputs, Port{Name: errorPortName, Kind: ConnectionMain, DisplayName: "Error"})
	definition.Outputs = outputs
	return definition
}

// knownOnError reports whether a node's onError setting names a mode.
func knownOnError(mode string) bool {
	switch mode {
	case OnErrorStop, OnErrorContinueRegular, OnErrorContinueBranch:
		return true
	default:
		return false
	}
}

// settingNumber coerces a node setting to a number. Settings arrive from JSON,
// so an integer is a float64 and a json.Number is possible.
func settingNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func portAcceptsNodeType(port Port, nodeType string) bool {
	if len(port.AllowedNodeTypes) == 0 {
		return true
	}
	for _, allowed := range port.AllowedNodeTypes {
		if allowed == nodeType {
			return true
		}
	}
	return false
}

// validatePortCardinality enforces the connection limits and requirements a
// port declares.
//
// Without it the AI Agent's slots are unenforceable: the compiler would accept
// three language models on one agent, and an agent with none — which cannot do
// anything — would compile and fail on the first item instead.
func validatePortCardinality(ir IR, issues *ValidationErrors) {
	incoming := make(map[string]map[string]int, len(ir.Nodes))
	for _, edge := range ir.Edges {
		if incoming[edge.Target.NodeID] == nil {
			incoming[edge.Target.NodeID] = map[string]int{}
		}
		incoming[edge.Target.NodeID][edge.Target.Port]++
	}

	for index, node := range ir.Nodes {
		for _, port := range node.Definition.Inputs {
			count := incoming[node.ID][port.Name]
			if port.Required && count == 0 {
				issues.add(ValidationError{
					Code: ErrorPortRequired, Path: fmt.Sprintf("/nodes/%d", index), NodeID: node.ID,
					Message: fmt.Sprintf("node %q requires a connection on port %q", node.ID, portLabel(port)),
				})
			}
			if port.MaxConnections > 0 && count > port.MaxConnections {
				issues.add(ValidationError{
					Code: ErrorPortFull, Path: fmt.Sprintf("/nodes/%d", index), NodeID: node.ID,
					Message: fmt.Sprintf("port %q on node %q accepts at most %d connection(s), got %d",
						portLabel(port), node.ID, port.MaxConnections, count),
				})
			}
		}
	}
}

// NormalizeToolName derives the model-facing tool name from a canvas name.
// The model API accepts letters, digits, and underscores, so anything else
// becomes an underscore rather than failing the run.
//
// It lives in the graph package rather than the node one so the compiler can
// refuse a duplicate name before activation without importing the catalogue
// it validates against. nodes.NormalizeToolName is the same rule for
// runtime use; the two must stay identical.
func NormalizeToolName(name string) string {
	var builder strings.Builder
	for _, letter := range name {
		switch {
		case letter >= 'a' && letter <= 'z', letter >= 'A' && letter <= 'Z',
			letter >= '0' && letter <= '9', letter == '_':
			builder.WriteRune(letter)
		default:
			builder.WriteRune('_')
		}
	}
	if builder.Len() == 0 {
		return "http_request"
	}
	return builder.String()
}

// validateAgentToolNames refuses two tools claiming one model-facing name on
// the same agent, naming both canvas nodes.
//
// The tool channel's naming rule is stated here because only the compiler
// sees the whole graph: a tool without an explicit `toolName` override is
// called by its canvas name normalised through NormalizeToolName, exactly
// as the runtime derives it. Waiting for the run would fail an activated
// workflow mid-execution on a graph the compiler already accepted.
func validateAgentToolNames(ir IR, issues *ValidationErrors) {
	byID := make(map[string]IRNode, len(ir.Nodes))
	indexByID := make(map[string]int, len(ir.Nodes))
	for index, node := range ir.Nodes {
		byID[node.ID] = node
		indexByID[node.ID] = index
	}
	claimed := make(map[string]map[string]string)
	for _, edge := range ir.Edges {
		if edge.Kind != ConnectionTool {
			continue
		}
		source, sourceFound := byID[edge.Source.NodeID]
		target, targetFound := byID[edge.Target.NodeID]
		if !sourceFound || !targetFound {
			continue
		}
		name, _ := source.Parameters["toolName"].(string)
		if strings.TrimSpace(name) == "" {
			name = NormalizeToolName(source.Name)
		} else {
			name = strings.TrimSpace(name)
		}
		agents := claimed[edge.Target.NodeID]
		if agents == nil {
			agents = map[string]string{}
			claimed[edge.Target.NodeID] = agents
		}
		if first, duplicate := agents[name]; duplicate {
			issues.add(ValidationError{
				Code: ErrorInvalidTopology, Path: fmt.Sprintf("/nodes/%d", indexByID[target.ID]), NodeID: target.ID,
				Message: fmt.Sprintf("tool name %q is used by both node %q and node %q; rename one of them",
					name, first, source.Name),
			})
			continue
		}
		agents[name] = source.Name
	}
}

// portLabel is what a user is shown: the display name where one is declared,
// the port's own name otherwise.
func portLabel(port Port) string {
	if port.DisplayName != "" {
		return port.DisplayName
	}
	return port.Name
}
