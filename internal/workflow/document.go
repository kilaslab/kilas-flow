package workflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

// CurrentSchemaVersion is the only canonical workflow document shape supported
// by this release. It is deliberately separate from a persisted workflow
// revision, which changes on every save.
const CurrentSchemaVersion = 1

// ConnectionKind identifies the data contract carried by a graph edge.
type ConnectionKind string

const (
	ConnectionMain          ConnectionKind = "main"
	ConnectionLanguageModel ConnectionKind = "ai_languageModel"
	ConnectionMemory        ConnectionKind = "ai_memory"
	ConnectionTool          ConnectionKind = "ai_tool"
)

// Document is KilasFlow's canonical, persisted workflow definition. Imported
// workflow formats must be converted to this shape before compilation.
type Document struct {
	SchemaVersion int            `json:"schemaVersion"`
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Nodes         []Node         `json:"nodes"`
	Connections   []Connection   `json:"connections"`
	Settings      map[string]any `json:"settings"`
}

// Node is a user-configured instance of a registered node definition.
type Node struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	TypeVersion TypeVersion       `json:"typeVersion"`
	Position    Position          `json:"position"`
	Parameters  map[string]any    `json:"parameters,omitempty"`
	Credentials map[string]string `json:"credentials,omitempty"`
	Settings    map[string]any    `json:"settings,omitempty"`
}

// Position is the canvas location owned by the canonical document.
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Endpoint names one node port.
type Endpoint struct {
	NodeID string `json:"nodeId"`
	Port   string `json:"port"`
}

// Connection is a directed edge from one node output to one node input.
type Connection struct {
	ID     string         `json:"id"`
	Kind   ConnectionKind `json:"kind"`
	Source Endpoint       `json:"source"`
	Target Endpoint       `json:"target"`
}

// BinaryRef is metadata for a binary item. Binary payload storage is outside
// the workflow contract and is intentionally not part of this milestone.
type BinaryRef struct {
	ID        string `json:"id"`
	FileName  string `json:"fileName,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
	Size      int64  `json:"size,omitempty"`
}

// PairedItem names the input item an output item descends from.
//
// n8n expresses this as `{item, input?}` pointing at an incoming item index.
// KilasFlow needs slightly more because its ports are named rather than
// positional, and because a node can run several times in one execution — so
// the origin is a node, a port, a run and an item.
//
// It is a single origin rather than n8n's list of candidates. One origin plus an
// explicit "lineage lost" marker is easier to reason about and is enough for
// every expression in the import corpus; a list would make every consumer
// handle an ambiguity that only aggregating nodes can produce.
type PairedItem struct {
	SourceNodeID string `json:"sourceNodeId"`
	SourcePort   string `json:"sourcePort,omitempty"`
	// RunIndex identifies which run of the source node this came from. A node
	// inside a loop or a fan-out produces several distinct runs.
	RunIndex int `json:"runIndex"`
	// ItemIndex is the position of the originating item in that run's port.
	ItemIndex int `json:"itemIndex"`
	// Lost marks an item whose lineage genuinely cannot be established — after
	// a merge of unrelated streams, or a node that changed the item count.
	// Recording that explicitly is what lets a lookup fail with a reason
	// instead of quietly returning the first item, which is correct only when
	// every node processed exactly one item and silently wrong otherwise.
	Lost bool `json:"lost,omitempty"`
}

// Item is the unit a node receives and emits.
type Item struct {
	JSON   map[string]any       `json:"json"`
	Binary map[string]BinaryRef `json:"binary,omitempty"`
	// Paired is where this item came from. Optional, and omitted when absent,
	// so every already-persisted item decodes unchanged.
	Paired *PairedItem `json:"pairedItem,omitempty"`
}

// NodeInput contains incoming items grouped by the destination port.
type NodeInput map[string][]Item

// NodeOutput contains one item stream for every declared output port. The
// compiled IR resolves the stable output-port order.
type NodeOutput [][]Item

// DecodeDocument decodes one canonical JSON document with strict structural
// fields. Dynamic parameter and settings maps remain deliberately open for the
// server-defined node registry to validate later.
func DecodeDocument(reader io.Reader) (Document, error) {
	contents, err := io.ReadAll(reader)
	if err != nil {
		return Document{}, fmt.Errorf("read workflow document: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()

	var document Document
	if err := decoder.Decode(&document); err != nil {
		return Document{}, fmt.Errorf("decode workflow document: %w", err)
	}
	if err := ensureDocumentEOF(decoder); err != nil {
		return Document{}, err
	}
	if err := validateJSONSchemaShape(contents); err != nil {
		return Document{}, err
	}
	if err := ValidateDraft(document); err != nil {
		return Document{}, err
	}
	return document, nil
}

// validateJSONSchemaShape enforces the required object/array fields whose
// absence cannot be represented by a zero-valued Go struct. Field names remain
// strict through Decoder.DisallowUnknownFields above; parameter and setting
// values intentionally stay open for the node registry.
func validateJSONSchemaShape(contents []byte) error {
	object, err := rawObject(contents, "workflow document")
	if err != nil {
		return err
	}
	if err := requireFields(object, "workflow document", "schemaVersion", "id", "name", "nodes", "connections", "settings"); err != nil {
		return err
	}

	nodes, err := rawArray(object["nodes"], "workflow document nodes")
	if err != nil {
		return err
	}
	if _, err := rawArray(object["connections"], "workflow document connections"); err != nil {
		return err
	}
	if _, err := rawObject(object["settings"], "workflow document settings"); err != nil {
		return err
	}

	for index, rawNode := range nodes {
		node, err := rawObject(rawNode, fmt.Sprintf("workflow node %d", index))
		if err != nil {
			return err
		}
		if err := requireFields(node, fmt.Sprintf("workflow node %d", index), "id", "name", "type", "typeVersion", "position"); err != nil {
			return err
		}
		position, err := rawObject(node["position"], fmt.Sprintf("workflow node %d position", index))
		if err != nil {
			return err
		}
		if err := requireFields(position, fmt.Sprintf("workflow node %d position", index), "x", "y"); err != nil {
			return err
		}
		for _, field := range []string{"parameters", "credentials", "settings"} {
			if raw, exists := node[field]; exists {
				if _, err := rawObject(raw, fmt.Sprintf("workflow node %d %s", index, field)); err != nil {
					return err
				}
			}
		}
	}

	connections, _ := rawArray(object["connections"], "workflow document connections")
	for index, rawConnection := range connections {
		connection, err := rawObject(rawConnection, fmt.Sprintf("workflow connection %d", index))
		if err != nil {
			return err
		}
		if err := requireFields(connection, fmt.Sprintf("workflow connection %d", index), "id", "kind", "source", "target"); err != nil {
			return err
		}
		for _, endpointField := range []string{"source", "target"} {
			endpoint, err := rawObject(connection[endpointField], fmt.Sprintf("workflow connection %d %s", index, endpointField))
			if err != nil {
				return err
			}
			if err := requireFields(endpoint, fmt.Sprintf("workflow connection %d %s", index, endpointField), "nodeId", "port"); err != nil {
				return err
			}
		}
	}
	return nil
}

func rawObject(raw []byte, name string) (map[string]json.RawMessage, error) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("workflow schema requires %s to be an object", name)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("decode workflow schema %s: %w", name, err)
	}
	return object, nil
}

func rawArray(raw []byte, name string) ([]json.RawMessage, error) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, fmt.Errorf("workflow schema requires %s to be an array", name)
	}
	var array []json.RawMessage
	if err := json.Unmarshal(raw, &array); err != nil {
		return nil, fmt.Errorf("decode workflow schema %s: %w", name, err)
	}
	return array, nil
}

func requireFields(object map[string]json.RawMessage, name string, fields ...string) error {
	for _, field := range fields {
		raw, found := object[field]
		if !found || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("workflow schema requires field %q in %s", field, name)
		}
	}
	return nil
}

func ensureDocumentEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode workflow document: multiple JSON values")
		}
		return fmt.Errorf("decode workflow document: %w", err)
	}
	return nil
}

// ValidateDraft checks only document-level structure. It intentionally does
// not require a complete graph or registered node configuration, so editors can
// save a work-in-progress draft.
func ValidateDraft(document Document) error {
	if document.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("workflow schemaVersion %d is unsupported", document.SchemaVersion)
	}
	if document.ID == "" {
		return fmt.Errorf("workflow id is required")
	}
	if document.Name == "" {
		return fmt.Errorf("workflow name is required")
	}
	if utf8.RuneCountInString(document.Name) > 255 {
		return fmt.Errorf("workflow name must not exceed 255 characters")
	}
	if document.Nodes == nil || document.Connections == nil || document.Settings == nil {
		return fmt.Errorf("workflow nodes, connections, and settings are required")
	}

	nodeIDs := make(map[string]struct{}, len(document.Nodes))
	for _, node := range document.Nodes {
		if node.ID == "" {
			return fmt.Errorf("workflow node id is required")
		}
		if node.Name == "" {
			return fmt.Errorf("workflow node %q name is required", node.ID)
		}
		if _, exists := nodeIDs[node.ID]; exists {
			return fmt.Errorf("workflow node id %q is duplicated", node.ID)
		}
		nodeIDs[node.ID] = struct{}{}
		if node.Type == "" {
			return fmt.Errorf("workflow node %q type is required", node.ID)
		}
		// An unset typeVersion is a valid draft: it means "whichever version of
		// this node type the registry says is current", and the compiler
		// resolves it. A negative or malformed one never reaches here, because
		// TypeVersion refuses to decode from one.
		for credentialName, credentialID := range node.Credentials {
			if credentialID == "" {
				return fmt.Errorf("workflow node %q credential %q reference is required", node.ID, credentialName)
			}
		}
	}

	connectionIDs := make(map[string]struct{}, len(document.Connections))
	for _, connection := range document.Connections {
		if connection.ID == "" {
			return fmt.Errorf("workflow connection id is required")
		}
		if _, exists := connectionIDs[connection.ID]; exists {
			return fmt.Errorf("workflow connection id %q is duplicated", connection.ID)
		}
		connectionIDs[connection.ID] = struct{}{}
		if !knownConnectionKind(connection.Kind) {
			return fmt.Errorf("workflow connection %q kind is invalid", connection.ID)
		}
		if connection.Source.NodeID == "" || connection.Source.Port == "" || connection.Target.NodeID == "" || connection.Target.Port == "" {
			return fmt.Errorf("workflow connection %q source and target endpoints are required", connection.ID)
		}
	}

	return nil
}

// ValidateDraftWithServerID applies draft validation before a create handler
// assigns its server-owned workflow identity.
func ValidateDraftWithServerID(document Document) error {
	document.ID = "workflow-server-assigned"
	return ValidateDraft(document)
}

// KnownConnectionKind reports whether KilasFlow models this channel.
//
// It is exported because the n8n adapter needs the same closed set: n8n's
// connection-kind strings are exactly these values, so importing an edge is an
// identity check against this list rather than a translation table that could
// drift from it.
func KnownConnectionKind(kind ConnectionKind) bool {
	switch kind {
	case ConnectionMain, ConnectionLanguageModel, ConnectionMemory, ConnectionTool:
		return true
	default:
		return false
	}
}

func knownConnectionKind(kind ConnectionKind) bool { return KnownConnectionKind(kind) }
