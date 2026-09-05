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
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// UnsupportedNodeType is the canonical placeholder an unmappable n8n node
// becomes. It is registered in the node catalogue so the editor can render it.
const UnsupportedNodeType = "kilasflow.unsupported"

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
	Type        string `json:"type"`
	TypeVersion int    `json:"typeVersion,omitempty"`
	Reason      string `json:"reason"`
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
func Import(payload []byte) (ImportResult, error) {
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
			original, _ := json.Marshal(map[string]any{
				"type": node.Type, "typeVersion": node.TypeVersion, "parameters": node.Parameters,
			})
			converted.Type = UnsupportedNodeType
			converted.TypeVersion = 1
			converted.Parameters = map[string]any{
				"originalType":        node.Type,
				"originalTypeVersion": node.TypeVersion,
				"original":            string(original),
			}
			nodes = append(nodes, converted)
			unsupported = append(unsupported, Unsupported{
				NodeName: name, NodeID: id, Type: node.Type, TypeVersion: int(node.TypeVersion),
				Reason: fmt.Sprintf("KilasFlow has no equivalent of the n8n node %q. It was imported as an unsupported placeholder: the workflow can be edited, but it cannot run until this node is replaced.", node.Type),
			})
			continue
		}

		converted.Type = entry.kilasType
		converted.TypeVersion = entry.kilasVersion
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
	for _, converted := range nodes {
		typeByID[converted.ID] = converted.Type
	}
	connections, connectionIssues := importConnections(source.Connections, idByName, typeByID)
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
func importConnections(source Connections, idByName, typeByID map[string]string) ([]workflow.Connection, []Unsupported) {
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

		for _, kind := range kindNames {
			if kind != "main" {
				// n8n's ai_* channels exist but their node family is outside the
				// advertised subset, so importing the edge would connect nothing.
				issues = append(issues, Unsupported{
					NodeName: sourceName,
					Reason:   fmt.Sprintf("connection kind %q is outside the supported subset and was dropped", kind),
				})
				continue
			}
			for outputIndex, targets := range kinds[kind] {
				for _, target := range targets {
					targetID, found := idByName[target.Node]
					if !found {
						issues = append(issues, Unsupported{
							NodeName: sourceName,
							Reason:   fmt.Sprintf("a connection points at %q, which is not a node in this workflow; it was dropped", target.Node),
						})
						continue
					}
					counter++
					connections = append(connections, workflow.Connection{
						ID:     fmt.Sprintf("n8n-c%d", counter),
						Kind:   workflow.ConnectionMain,
						Source: workflow.Endpoint{NodeID: sourceID, Port: outputPortName(typeByID[sourceID], outputIndex)},
						Target: workflow.Endpoint{NodeID: targetID, Port: inputPortName(typeByID[targetID], target.Index)},
					})
				}
			}
		}
	}
	return connections, issues
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
			originalType, _ := node.Parameters["originalType"].(string)
			originalVersion, _ := node.Parameters["originalTypeVersion"].(float64)
			exported := Node{
				ID: node.ID, Name: node.Name, Type: originalType, TypeVersion: originalVersion,
				Position: []float64{node.Position.X, node.Position.Y},
			}
			if raw, ok := node.Parameters["original"].(string); ok {
				var original struct {
					Parameters map[string]any `json:"parameters"`
				}
				if err := json.Unmarshal([]byte(raw), &original); err == nil {
					exported.Parameters = original.Parameters
				}
			}
			result.Document.Nodes = append(result.Document.Nodes, exported)
			result.Lossy = append(result.Lossy, Lossy{
				NodeName: node.Name,
				Reason:   fmt.Sprintf("this node was imported from n8n as unsupported; it was exported back as %q with its original parameters", originalType),
			})
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
		if connection.Kind != workflow.ConnectionMain {
			result.Lossy = append(result.Lossy, Lossy{
				NodeName: sourceName,
				Reason:   fmt.Sprintf("connection kind %q has no n8n equivalent in the supported subset and was dropped", connection.Kind),
			})
			continue
		}

		index := 0
		if indexes, known := portIndex[connection.Source.NodeID]; known {
			if position, found := indexes[connection.Source.Port]; found {
				index = position
			}
		}
		if result.Document.Connections[sourceName] == nil {
			result.Document.Connections[sourceName] = map[string][][]Target{}
		}
		slots := result.Document.Connections[sourceName]["main"]
		for len(slots) <= index {
			slots = append(slots, []Target{})
		}
		slots[index] = append(slots[index], Target{
			Node: targetName, Type: "main", Index: inputIndexFor(typeByID[connection.Target.NodeID], connection.Target.Port),
		})
		result.Document.Connections[sourceName]["main"] = slots
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
