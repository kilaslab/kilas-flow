// Package n8n converts between n8n workflow JSON and KilasFlow's canonical
// document.
//
// This is a boundary adapter and nothing more. KilasFlow never executes n8n
// JSON and takes no runtime dependency on any n8n package: an import is
// translated into canonical nodes that the existing registry and compiler
// validate exactly as they would a hand-built workflow.
//
// A node type outside the advertised subset is never guessed at. It is
// imported as an explicit unsupported placeholder that keeps its original
// type and parameters, so it stays visible on the canvas — and it fails
// compilation, so a workflow containing one can be inspected and edited but
// never activated or run. Silently mapping an unknown node onto a similar one
// would be far worse than refusing it.
package n8n

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// UnsupportedNodeType is the canonical placeholder an unmappable n8n node
// becomes. It is registered in the node catalogue so the editor can render it.
const UnsupportedNodeType = "kilasflow.unsupported"

// StickyNoteNodeType is the canvas annotation an n8n sticky note becomes.
//
// Mirrored from nodes.StickyNoteNodeType rather than imported, for the same
// reason UnsupportedNodeType is: the adapter must not depend on the node pack.
// TestMirroredNodeTypesMatchTheNodePack keeps the two in step.
const StickyNoteNodeType = "kilasflow.stickyNote"

// Document is the subset of n8n workflow JSON this adapter reads and writes.
//
// Fields outside it are ignored on import and never invented on export; the
// lossy report names what was dropped.
type Document struct {
	Name        string         `json:"name"`
	Nodes       []Node         `json:"nodes"`
	Connections Connections    `json:"connections"`
	Settings    map[string]any `json:"settings,omitempty"`
	PinData     map[string]any `json:"pinData,omitempty"`
	Meta        map[string]any `json:"meta,omitempty"`
}

// Node is one n8n node.
type Node struct {
	ID          string         `json:"id,omitempty"`
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	TypeVersion float64        `json:"typeVersion"`
	Position    []float64      `json:"position,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Credentials map[string]any `json:"credentials,omitempty"`
	Disabled    bool           `json:"disabled,omitempty"`
	Notes       string         `json:"notes,omitempty"`
	// WebhookID is n8n's per-node webhook identity. A node imported as a
	// placeholder must carry it back out, or the exported workflow claims a
	// different endpoint than the one it came from.
	WebhookID string `json:"webhookId,omitempty"`
	// The error-handling set. KilasFlow does not honour these yet — that is a
	// separate ticket — but the placeholder must not lose them, because a node
	// that round-trips without its retry policy silently changes behaviour in
	// the instance it returns to.
	ContinueOnFail   bool    `json:"continueOnFail,omitempty"`
	RetryOnFail      bool    `json:"retryOnFail,omitempty"`
	MaxTries         float64 `json:"maxTries,omitempty"`
	WaitBetweenTries float64 `json:"waitBetweenTries,omitempty"`
	AlwaysOutputData bool    `json:"alwaysOutputData,omitempty"`
	ExecuteOnce      bool    `json:"executeOnce,omitempty"`
	OnError          string  `json:"onError,omitempty"`
}

// Connections is n8n's shape: source node *name* → connection kind → one slot
// per output index → the targets on that output.
type Connections map[string]map[string][][]Target

// Target is one end of an n8n connection.
type Target struct {
	Node  string `json:"node"`
	Type  string `json:"type"`
	Index int    `json:"index"`
}

// Unsupported reports one element the adapter refused to map.
type Unsupported struct {
	// NodeName is the n8n node's name, which is what a user sees in n8n.
	NodeName string `json:"nodeName"`
	NodeID   string `json:"nodeId,omitempty"`
	// Type and TypeVersion are the original n8n identity, preserved so the
	// message can name exactly what was not supported.
	Type string `json:"type"`
	// TypeVersion is the source version exactly as n8n wrote it. It was an int
	// and truncated through a cast, so a node on version 4.2 was reported as
	// version 4 — which is a different node with a different parameter shape,
	// and the diagnostic pointed at the wrong one.
	TypeVersion workflow.TypeVersion `json:"typeVersion,omitempty"`
	Reason      string               `json:"reason"`
}

// Lossy reports one thing an export could not represent.
type Lossy struct {
	NodeName string `json:"nodeName,omitempty"`
	Field    string `json:"field,omitempty"`
	Reason   string `json:"reason"`
}

// mapping is one entry in the advertised node subset.
type mapping struct {
	n8nType      string
	kilasType    string
	kilasVersion int
	// toKilas translates n8n parameters. A nil translator means the node has
	// no parameters worth carrying.
	toKilas func(node Node) (map[string]any, []Unsupported)
	// toN8N translates back. A nil translator exports no parameters.
	toN8N func(node workflow.Node) (map[string]any, []Lossy)
	// exportTypeVersion is the n8n typeVersion written on export.
	exportTypeVersion float64
	// exportOnly marks a canonical node with no n8n equivalent to import from.
	exportOnly bool
	// importOnly marks an n8n node with no faithful export.
	importOnly bool
}

// SupportedMappings is the advertised subset, in stable order.
//
// It is deliberately explicit rather than derived: interoperability claims are
// only meaningful if the exact list is written down and testable.
func SupportedMappings() []string {
	names := make([]string, 0, len(mappings))
	for _, entry := range mappings {
		names = append(names, entry.n8nType+" ↔ "+entry.kilasType)
	}
	sort.Strings(names)
	return names
}

var mappings = []mapping{
	{
		n8nType: "n8n-nodes-base.manualTrigger", kilasType: "kilasflow.manual", kilasVersion: 1,
		exportTypeVersion: 1,
	},
	{
		n8nType: "n8n-nodes-base.set", kilasType: "kilasflow.set", kilasVersion: 1,
		exportTypeVersion: 3.4, toKilas: setToKilas, toN8N: setToN8N,
	},
	{
		n8nType: "n8n-nodes-base.if", kilasType: "kilasflow.if", kilasVersion: 1,
		exportTypeVersion: 2, toKilas: ifToKilas, toN8N: ifToN8N,
	},
	{
		n8nType: "n8n-nodes-base.merge", kilasType: "kilasflow.merge", kilasVersion: 1,
		exportTypeVersion: 3, toKilas: mergeToKilas, toN8N: mergeToN8N,
	},
	{
		n8nType: "n8n-nodes-base.httpRequest", kilasType: "kilasflow.httpRequest", kilasVersion: 1,
		exportTypeVersion: 4.2, toKilas: httpToKilas, toN8N: httpToN8N,
	},
	{
		n8nType: "n8n-nodes-base.webhook", kilasType: "kilasflow.webhook", kilasVersion: 1,
		exportTypeVersion: 2, toKilas: webhookToKilas, toN8N: webhookToN8N,
	},
	{
		n8nType: "n8n-nodes-base.respondToWebhook", kilasType: "kilasflow.respondToWebhook", kilasVersion: 1,
		exportTypeVersion: 1.1, toKilas: respondToKilas, toN8N: respondToN8N,
	},
	{
		n8nType: "n8n-nodes-base.scheduleTrigger", kilasType: "kilasflow.schedule", kilasVersion: 1,
		exportTypeVersion: 1.2, toKilas: scheduleToKilas, toN8N: scheduleToN8N,
	},
	{
		n8nType: "n8n-nodes-base.postgres", kilasType: "kilasflow.postgres", kilasVersion: 1,
		exportTypeVersion: 2.4, toKilas: sqlToKilas, toN8N: sqlToN8N,
	},
	{
		n8nType: "n8n-nodes-base.mySql", kilasType: "kilasflow.mysql", kilasVersion: 1,
		exportTypeVersion: 2.4, toKilas: sqlToKilas, toN8N: sqlToN8N,
	},
	{
		n8nType: "n8n-nodes-base.stickyNote", kilasType: StickyNoteNodeType, kilasVersion: 1,
		exportTypeVersion: 1, toKilas: stickyToKilas, toN8N: stickyToN8N,
	},
}

func byN8NType(nodeType string) (mapping, bool) {
	for _, entry := range mappings {
		if entry.n8nType == nodeType && !entry.exportOnly {
			return entry, true
		}
	}
	return mapping{}, false
}

func byKilasType(nodeType string) (mapping, bool) {
	for _, entry := range mappings {
		if entry.kilasType == nodeType && !entry.importOnly {
			return entry, true
		}
	}
	return mapping{}, false
}

// ImportResult is one converted workflow plus everything the adapter refused.
type ImportResult struct {
	Document    workflow.Document
	Unsupported []Unsupported
}

// Import converts n8n workflow JSON into a canonical KilasFlow document.
//
// The result is a *draft*. It is not compiled here: the caller saves it
// through the normal repository path, and the existing compiler is what
// decides whether it can be activated or run. That keeps one validation
// authority rather than a second, weaker one inside the adapter.
//
// The catalogue is read, never validated against. n8n identifies a connection
// endpoint by kind and index; KilasFlow identifies it by port name, and the
// only authority on what ports a node type declares is the registry. Asking it
// is what lets a typed AI edge land on the right port without a second
// hardcoded table to drift from the definitions. A nil catalogue falls back to
// the positional names, which can only resolve the item channel.
func Import(payload []byte, catalog workflow.Catalog) (ImportResult, error) {
	if len(payload) == 0 {
		return ImportResult{}, fmt.Errorf("no workflow JSON was supplied")
	}
	var source Document
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	if err := decoder.Decode(&source); err != nil {
		return ImportResult{}, fmt.Errorf("this is not valid n8n workflow JSON: %w", err)
	}
	if len(source.Nodes) == 0 {
		return ImportResult{}, fmt.Errorf("the workflow contains no nodes")
	}

	name := strings.TrimSpace(source.Name)
	if name == "" {
		name = "Imported workflow"
	}

	// A placeholder's declared ports must cover the edges the source workflow
	// drew, so the arity has to be known before the node is converted — which
	// means reading the connections first. Getting this wrong is not a subtle
	// failure: the compiler rejects the edge for an unknown port before the
	// placeholder's own validator runs, so the user is told their topology is
	// broken rather than that a node is unsupported.
	arityByName := observedArity(source.Connections)

	unsupported := make([]Unsupported, 0)
	nodes := make([]workflow.Node, 0, len(source.Nodes))
	// n8n connections are keyed by node *name*; KilasFlow's are keyed by ID.
	idByName := make(map[string]string, len(source.Nodes))
	seenName := make(map[string]bool, len(source.Nodes))

	for index, node := range source.Nodes {
		name := strings.TrimSpace(node.Name)
		if name == "" {
			name = fmt.Sprintf("Node %d", index+1)
		}
		if seenName[name] {
			// n8n keys connections by name, so duplicates would make the graph
			// ambiguous. Refusing beats importing something that routes wrongly.
			return ImportResult{}, fmt.Errorf("two nodes are both named %q; n8n connections are keyed by name, so this workflow cannot be imported unambiguously", name)
		}
		seenName[name] = true

		id := strings.TrimSpace(node.ID)
		if id == "" {
			id = fmt.Sprintf("n8n-%d", index+1)
		}
		idByName[name] = id

		converted := workflow.Node{
			ID: id, Name: name,
			Position: positionFrom(node.Position),
		}

		entry, supported := byN8NType(node.Type)
		if !supported {
			// Preserved rather than dropped: the node stays visible with its
			// original identity, and the placeholder refuses to compile.
			//
			// The capsule is the whole source node, not a summary of it. A
			// placeholder exists so a node "came from n8n and belongs there",
			// and a round trip that returned it stripped of its credentials,
			// its notes or its retry policy would defeat exactly that.
			arity := arityByName[name]
			converted.Type = UnsupportedNodeType
			converted.TypeVersion = workflow.V(unsupportedArityFor(arity.inputs, arity.outputs))
			converted.Parameters = map[string]any{
				"originalType":        node.Type,
				"originalTypeVersion": node.TypeVersion,
				"original":            capsule(node),
			}
			nodes = append(nodes, converted)
			unsupported = append(unsupported, Unsupported{
				NodeName: name, NodeID: id, Type: node.Type, TypeVersion: sourceTypeVersion(node.TypeVersion),
				Reason: fmt.Sprintf("KilasFlow has no equivalent of the n8n node %q. It was imported as an unsupported placeholder: the workflow can be edited, but it cannot run until this node is replaced.", node.Type),
			})
			continue
		}

		converted.Type = entry.kilasType
		// n8n's own typeVersion is preserved rather than replaced with the
		// mapping's target. KilasFlow's node versions mirror n8n's, so keeping
		// the source version means an imported node lands on the right
		// parameter shape the moment that shape is registered — and until then
		// the registry resolves down to the highest version it does have.
		converted.TypeVersion = workflow.V(entry.kilasVersion)
		if sourceVersion := sourceTypeVersion(node.TypeVersion); !sourceVersion.IsZero() {
			converted.TypeVersion = sourceVersion
		}
		if entry.toKilas != nil {
			parameters, issues := entry.toKilas(node)
			converted.Parameters = parameters
			for _, issue := range issues {
				issue.NodeName = name
				issue.NodeID = id
				unsupported = append(unsupported, issue)
			}
		}
		if node.Disabled {
			unsupported = append(unsupported, Unsupported{
				NodeName: name, NodeID: id, Type: node.Type,
				Reason: "KilasFlow has no disabled-node concept, so this node was imported as active. Remove it if it should not run.",
			})
		}
		nodes = append(nodes, converted)
	}

	typeByID := make(map[string]string, len(nodes))
	versionByID := make(map[string]workflow.TypeVersion, len(nodes))
	for _, converted := range nodes {
		typeByID[converted.ID] = converted.Type
		versionByID[converted.ID] = converted.TypeVersion
	}
	connections, connectionIssues := importConnections(source.Connections, idByName, typeByID, versionByID, catalog)
	unsupported = append(unsupported, connectionIssues...)

	return ImportResult{
		Document: workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			Name:          name,
			Nodes:         nodes,
			Connections:   connections,
			Settings:      map[string]any{},
		},
		Unsupported: unsupported,
	}, nil
}

// importConnections maps n8n's name-keyed, index-positional edges onto
// canonical ID-keyed, named-port ones.
// importConnections converts n8n's name-keyed connection map into canonical
// edges.
//
// n8n keys connections by the *source* node's name, and for a typed AI channel
// the source is the sub-node and the target is the node it configures — a chat
// model emits ai_languageModel, an agent receives it. KilasFlow declares the
// same shape, so the loop is already directionally correct for every kind; what
// it needed was the kind itself and ports that exist on both endpoints.
func importConnections(source Connections, idByName, typeByID map[string]string, versionByID map[string]workflow.TypeVersion, catalog workflow.Catalog) ([]workflow.Connection, []Unsupported) {
	connections := make([]workflow.Connection, 0)
	issues := make([]Unsupported, 0)

	sourceNames := make([]string, 0, len(source))
	for name := range source {
		sourceNames = append(sourceNames, name)
	}
	sort.Strings(sourceNames)

	counter := 0
	for _, sourceName := range sourceNames {
		sourceID, known := idByName[sourceName]
		if !known {
			issues = append(issues, Unsupported{
				NodeName: sourceName,
				Reason:   fmt.Sprintf("a connection starts at %q, which is not a node in this workflow; it was dropped", sourceName),
			})
			continue
		}
		kinds := source[sourceName]
		kindNames := make([]string, 0, len(kinds))
		for kind := range kinds {
			kindNames = append(kindNames, kind)
		}
		sort.Strings(kindNames)

		for _, kindName := range kindNames {
			// n8n's connection-kind strings are exactly KilasFlow's
			// ConnectionKind values, so this is an identity check against a
			// closed set rather than a translation. The casing matters:
			// ai_languageModel is camel-cased after the underscore, and nothing
			// in this adapter may normalise a kind or type string.
			kind := workflow.ConnectionKind(kindName)
			if !workflow.KnownConnectionKind(kind) {
				for _, targets := range kinds[kindName] {
					for _, target := range targets {
						issues = append(issues, Unsupported{
							NodeName: sourceName,
							Reason: fmt.Sprintf("the connection from %q to %q on channel %q has no equivalent in KilasFlow and was dropped",
								sourceName, target.Node, kindName),
						})
					}
				}
				continue
			}

			for outputIndex, targets := range kinds[kindName] {
				for _, target := range targets {
					targetID, found := idByName[target.Node]
					if !found {
						issues = append(issues, Unsupported{
							NodeName: sourceName,
							Reason:   fmt.Sprintf("a connection points at %q, which is not a node in this workflow; it was dropped", target.Node),
						})
						continue
					}

					sourcePort, sourceOK := resolvePort(catalog, typeByID[sourceID], versionByID[sourceID], kind, outputIndex, portOutput)
					targetPort, targetOK := resolvePort(catalog, typeByID[targetID], versionByID[targetID], kind, target.Index, portInput)
					if !sourceOK || !targetOK {
						// Held back rather than dropped silently: recording an
						// edge onto a port that does not exist would fail
						// compilation with an unknown-port error, which reads
						// as a broken graph rather than as a missing node type.
						missing := sourceName
						if !targetOK {
							missing = target.Node
						}
						issues = append(issues, Unsupported{
							NodeName: sourceName,
							Reason: fmt.Sprintf("the %q connection from %q to %q was held back because %q declares no %s port for it",
								kindName, sourceName, target.Node, missing, kindName),
						})
						continue
					}

					counter++
					connections = append(connections, workflow.Connection{
						ID:     fmt.Sprintf("n8n-c%d", counter),
						Kind:   kind,
						Source: workflow.Endpoint{NodeID: sourceID, Port: sourcePort},
						Target: workflow.Endpoint{NodeID: targetID, Port: targetPort},
					})
				}
			}
		}
	}
	return connections, issues
}

// portDirection selects which side of a definition resolvePort reads.
type portDirection int

const (
	portOutput portDirection = iota
	portInput
)

// resolvePort finds the canonical port an n8n endpoint refers to.
//
// n8n identifies an endpoint by kind and index. For the item channel the index
// is positional and meaningful — IF's second output is its false branch. For a
// typed AI channel it is not: a node has exactly one port per AI kind, and n8n
// itself always writes index 0, so the first declared port of that kind is the
// answer.
//
// It asks the registry rather than a table. The adapter already has to know
// canonical port names to be correct, and a second hardcoded table is how the
// existing pair of helpers came to need a comment explaining that one is the
// inverse of the other. Going through the catalogue is also what keeps this
// working when generated node packs arrive with ports nobody hardcoded.
func resolvePort(catalog workflow.Catalog, nodeType string, version workflow.TypeVersion, kind workflow.ConnectionKind, index int, direction portDirection) (string, bool) {
	if catalog == nil {
		// No catalogue: fall back to the positional names, which is what the
		// adapter did before it could ask. Only the item channel is nameable
		// this way.
		if kind != workflow.ConnectionMain {
			return "", false
		}
		if direction == portOutput {
			return outputPortName(nodeType, index), true
		}
		return inputPortName(nodeType, index), true
	}
	definition, found := catalog.Lookup(nodeType, version)
	if !found {
		return "", false
	}
	declared := definition.Outputs
	if direction == portInput {
		declared = definition.Inputs
	}
	matching := make([]workflow.Port, 0, len(declared))
	for _, port := range declared {
		if port.Kind == kind {
			matching = append(matching, port)
		}
	}
	if len(matching) == 0 {
		return "", false
	}
	if kind != workflow.ConnectionMain {
		// One port per AI kind; n8n always writes index 0.
		return matching[0].Name, true
	}
	if index < 0 || index >= len(matching) {
		return "", false
	}
	return matching[index].Name, true
}

// outputPortName maps an n8n output index onto a canonical port name.
//
// n8n identifies outputs positionally; KilasFlow names them, and the names
// differ per node — IF has `true`/`false` where everything else has `main`.
// The mapping is therefore driven by the node's canonical type rather than by
// the index alone, so a branch cannot be wired to a port that does not exist.
// It is the exact inverse of outputIndexesFor, used on export.
func outputPortName(kilasType string, index int) string {
	ports := outputPortsFor(kilasType)
	if index >= 0 && index < len(ports) {
		return ports[index]
	}
	return fmt.Sprintf("output%d", index)
}

func outputPortsFor(kilasType string) []string {
	if kilasType == "kilasflow.if" {
		return []string{"true", "false"}
	}
	return []string{"main"}
}

func inputPortName(kilasType string, index int) string {
	ports := inputPortsFor(kilasType)
	if index >= 0 && index < len(ports) {
		return ports[index]
	}
	return fmt.Sprintf("input%d", index+1)
}

func inputPortsFor(kilasType string) []string {
	if kilasType == "kilasflow.merge" {
		return []string{"input1", "input2"}
	}
	return []string{"main"}
}

func positionFrom(position []float64) workflow.Position {
	if len(position) < 2 {
		return workflow.Position{}
	}
	return workflow.Position{X: position[0], Y: position[1]}
}

// ExportResult is one converted document plus everything it could not carry.
type ExportResult struct {
	Document Document
	Lossy    []Lossy
}

// Export converts a canonical KilasFlow document into n8n workflow JSON.
func Export(document workflow.Document) (ExportResult, error) {
	result := ExportResult{
		Document: Document{
			Name:        document.Name,
			Nodes:       make([]Node, 0, len(document.Nodes)),
			Connections: Connections{},
			Settings:    map[string]any{},
		},
		Lossy: make([]Lossy, 0),
	}

	nameByID := make(map[string]string, len(document.Nodes))
	typeByID := make(map[string]string, len(document.Nodes))
	portIndex := make(map[string]map[string]int, len(document.Nodes))

	for _, node := range document.Nodes {
		nameByID[node.ID] = node.Name
		typeByID[node.ID] = node.Type

		if node.Type == UnsupportedNodeType {
			// Round-tripping the placeholder back to its original n8n identity
			// is the honest thing: the node came from n8n and belongs there.
			// Everything the capsule kept is handed back, not just the
			// parameters — a node that returns without its credentials, its
			// notes or its retry policy is a node that quietly changed.
			originalType, _ := node.Parameters["originalType"].(string)
			originalVersion, _ := node.Parameters["originalTypeVersion"].(float64)
			exported := restoreCapsule(node.Parameters["original"])
			exported.ID = node.ID
			exported.Name = node.Name
			exported.Position = []float64{node.Position.X, node.Position.Y}
			if exported.Type == "" {
				exported.Type = originalType
			}
			if exported.TypeVersion == 0 {
				exported.TypeVersion = originalVersion
			}
			result.Document.Nodes = append(result.Document.Nodes, exported)
			result.Lossy = append(result.Lossy, Lossy{
				NodeName: node.Name,
				Reason:   fmt.Sprintf("this node was imported from n8n as unsupported; it was exported back as %q with everything the import preserved", originalType),
			})
			// The placeholder's own ports, so a multi-output node keeps its
			// branches. Falling through without this sent every outgoing edge
			// to n8n output 0, silently rewiring a Switch so that all its
			// branches left the first slot.
			portIndex[node.ID] = placeholderOutputIndexes(node)
			continue
		}

		entry, supported := byKilasType(node.Type)
		if !supported {
			result.Lossy = append(result.Lossy, Lossy{
				NodeName: node.Name,
				Reason:   fmt.Sprintf("n8n has no equivalent of the KilasFlow node %q, so it was omitted from the export", node.Type),
			})
			continue
		}

		exported := Node{
			ID: node.ID, Name: node.Name, Type: entry.n8nType, TypeVersion: entry.exportTypeVersion,
			Position: []float64{node.Position.X, node.Position.Y},
		}
		if entry.toN8N != nil {
			parameters, issues := entry.toN8N(node)
			exported.Parameters = parameters
			for _, issue := range issues {
				issue.NodeName = node.Name
				result.Lossy = append(result.Lossy, issue)
			}
		}
		if len(node.Credentials) > 0 {
			// Credential *references* are KilasFlow IDs and mean nothing in an
			// n8n instance, so they are named as lost rather than emitted as
			// broken references.
			result.Lossy = append(result.Lossy, Lossy{
				NodeName: node.Name, Field: "credentials",
				Reason: "credential references are KilasFlow identifiers and were not exported; reattach credentials in n8n",
			})
		}
		result.Document.Nodes = append(result.Document.Nodes, exported)
		portIndex[node.ID] = outputIndexesFor(node.Type)
	}

	exported := map[string]bool{}
	for _, node := range result.Document.Nodes {
		exported[node.Name] = true
	}

	for _, connection := range document.Connections {
		sourceName, sourceKnown := nameByID[connection.Source.NodeID]
		targetName, targetKnown := nameByID[connection.Target.NodeID]
		if !sourceKnown || !targetKnown || !exported[sourceName] || !exported[targetName] {
			result.Lossy = append(result.Lossy, Lossy{
				Reason: "a connection referenced a node that was not exported and was dropped with it",
			})
			continue
		}
		if !workflow.KnownConnectionKind(connection.Kind) {
			result.Lossy = append(result.Lossy, Lossy{
				NodeName: sourceName,
				Reason: fmt.Sprintf("the connection from %q to %q on channel %q has no n8n equivalent and was dropped",
					sourceName, targetName, connection.Kind),
			})
			continue
		}

		channel := string(connection.Kind)
		// A typed AI channel carries exactly one port per kind and n8n always
		// writes slot zero, so only the item channel is positional.
		index := 0
		targetIndex := 0
		if connection.Kind == workflow.ConnectionMain {
			if indexes, known := portIndex[connection.Source.NodeID]; known {
				if position, found := indexes[connection.Source.Port]; found {
					index = position
				}
			}
			targetIndex = inputIndexFor(typeByID[connection.Target.NodeID], connection.Target.Port)
		}
		if result.Document.Connections[sourceName] == nil {
			result.Document.Connections[sourceName] = map[string][][]Target{}
		}
		slots := result.Document.Connections[sourceName][channel]
		for len(slots) <= index {
			slots = append(slots, []Target{})
		}
		slots[index] = append(slots[index], Target{
			Node: targetName, Type: channel, Index: targetIndex,
		})
		result.Document.Connections[sourceName][channel] = slots
	}

	return result, nil
}

// outputIndexesFor maps a canonical node's named output ports onto n8n's
// positional ones. It is the inverse of outputPortsFor, so a round trip lands
// on the same branch it started from.
func outputIndexesFor(nodeType string) map[string]int {
	indexes := map[string]int{}
	for index, port := range outputPortsFor(nodeType) {
		indexes[port] = index
	}
	return indexes
}

func inputIndexFor(nodeType, port string) int {
	for index, candidate := range inputPortsFor(nodeType) {
		if candidate == port {
			return index
		}
	}
	return 0
}

// nodeArity is how many input and output slots a workflow's connections
// actually use on one node.
type nodeArity struct {
	inputs  int
	outputs int
}

// observedArity counts the slots each node is wired on.
//
// n8n identifies a slot positionally, so the only evidence of how many a node
// has is the highest index some edge uses. A node wired on outputs 0 and 2 has
// at least three, even though nothing touches output 1.
func observedArity(connections Connections) map[string]nodeArity {
	arity := make(map[string]nodeArity, len(connections))
	widen := func(name string, inputs, outputs int) {
		current := arity[name]
		if inputs > current.inputs {
			current.inputs = inputs
		}
		if outputs > current.outputs {
			current.outputs = outputs
		}
		arity[name] = current
	}
	for sourceName, kinds := range connections {
		for _, slots := range kinds {
			for outputIndex, targets := range slots {
				if len(targets) > 0 {
					widen(sourceName, 0, outputIndex+1)
				}
				for _, target := range targets {
					widen(target.Node, target.Index+1, 0)
				}
			}
		}
	}
	return arity
}

// unsupportedArities mirrors nodes.UnsupportedArities. The adapter must not
// depend on the node pack, so the family is duplicated and pinned by
// TestPlaceholderArityFamilyMatchesTheNodePack.
var unsupportedArities = []int{1, 2, 4, 8}

// unsupportedArityFor is the smallest registered placeholder arity covering a
// node wired on this many slots. A node beyond the largest member is clamped;
// import reports the truncation rather than emitting an edge the compiler will
// reject.
func unsupportedArityFor(inputs, outputs int) int {
	needed := inputs
	if outputs > needed {
		needed = outputs
	}
	if needed < 1 {
		needed = 1
	}
	for _, arity := range unsupportedArities {
		if arity >= needed {
			return arity
		}
	}
	return unsupportedArities[len(unsupportedArities)-1]
}

// capsule is the whole source node as a structured value.
//
// It is stored as an object rather than a marshalled string: the previous shape
// escaped JSON inside JSON, which made the value unreadable in the editor and
// bought nothing. Round-tripping through the node's own JSON tags keeps the
// field names identical to what n8n wrote, so an export can hand them straight
// back.
func capsule(node Node) map[string]any {
	encoded, err := json.Marshal(node)
	if err != nil {
		// Node holds only JSON-native types, so this cannot fail; falling back
		// to identity alone still preserves more than dropping the node.
		return map[string]any{"type": node.Type, "typeVersion": node.TypeVersion}
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return map[string]any{"type": node.Type, "typeVersion": node.TypeVersion}
	}
	return decoded
}

// restoreCapsule turns a stored capsule back into the node n8n wrote.
//
// It round-trips through the same JSON tags the capsule was built from, so a
// field added to Node is carried in both directions without a second list to
// keep in step. An unreadable capsule yields a zero Node and the caller falls
// back to the identity parameters, which is why those are still stored
// separately.
func restoreCapsule(stored any) Node {
	if stored == nil {
		return Node{}
	}
	// Workflows imported before the capsule became structured hold it as a
	// JSON *string*. Reading both shapes costs one type switch and is the
	// difference between those workflows exporting whole and exporting
	// stripped of their parameters.
	if text, ok := stored.(string); ok {
		var node Node
		if err := json.Unmarshal([]byte(text), &node); err != nil {
			return Node{}
		}
		return node
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		return Node{}
	}
	var node Node
	if err := json.Unmarshal(encoded, &node); err != nil {
		return Node{}
	}
	return node
}

// placeholderOutputIndexes maps a placeholder's port names back to n8n output
// slots. Its arity is its type version, which is how the family is registered.
func placeholderOutputIndexes(node workflow.Node) map[string]int {
	arity, err := strconv.Atoi(node.TypeVersion.String())
	if err != nil || arity < 1 {
		arity = 1
	}
	indexes := make(map[string]int, arity)
	for index := 0; index < arity; index++ {
		indexes[outputPortName(UnsupportedNodeType, index)] = index
	}
	return indexes
}

// formatN8NVersion renders an n8n typeVersion as the decimal text
// workflow.ParseTypeVersion reads.
//
// n8n's typeVersion arrives as a JSON number and is held as a float64, which is
// the one place a float is unavoidable. Formatting it with %g rather than
// reading the float directly keeps 4.2 from becoming 4.199999999999999 on the
// way into a fixed-point version.
func formatN8NVersion(version float64) string {
	if version <= 0 {
		return ""
	}
	return strconv.FormatFloat(version, 'g', -1, 64)
}

// sourceTypeVersion reads an n8n typeVersion into KilasFlow's fixed-point form,
// yielding the unset version when n8n gave nothing usable.
func sourceTypeVersion(version float64) workflow.TypeVersion {
	parsed, err := workflow.ParseTypeVersion(formatN8NVersion(version))
	if err != nil {
		return workflow.TypeVersion{}
	}
	return parsed
}
