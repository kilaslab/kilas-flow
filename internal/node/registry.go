// Package node owns the server-defined workflow node catalogue.
package node

import (
	"fmt"
	"sort"

	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// PropertyKind identifies a generic control the editor can render without a
// node-specific form implementation.
type PropertyKind string

const (
	PropertyString     PropertyKind = "string"
	PropertyNumber     PropertyKind = "number"
	PropertyBoolean    PropertyKind = "boolean"
	PropertySelect     PropertyKind = "select"
	PropertyKeyValue   PropertyKind = "keyValue"
	PropertyConditions PropertyKind = "conditions"
)

// PropertyOption is one selectable value for a PropertySelect control.
type PropertyOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// VisibilityCondition lets a dynamic property form hide a field until one
// other field has the specified value.
type VisibilityCondition struct {
	Key    string `json:"key"`
	Equals any    `json:"equals"`
}

// PropertyDefinition describes one node parameter or shared setting.
type PropertyDefinition struct {
	Key         string                `json:"key"`
	Label       string                `json:"label"`
	Description string                `json:"description,omitempty"`
	Kind        PropertyKind          `json:"kind"`
	Required    bool                  `json:"required"`
	Default     any                   `json:"default,omitempty"`
	Options     []PropertyOption      `json:"options,omitempty"`
	VisibleWhen []VisibilityCondition `json:"visibleWhen,omitempty"`
}

// Definition is the complete server-owned description of a supported node.
// ExecutorID is intentionally an opaque server-only binding and is omitted
// from API responses.
type Definition struct {
	Type           string                   `json:"type"`
	Version        int                      `json:"version"`
	DisplayName    string                   `json:"displayName"`
	Description    string                   `json:"description,omitempty"`
	Category       string                   `json:"category"`
	Inputs         []workflow.Port          `json:"inputs"`
	Outputs        []workflow.Port          `json:"outputs"`
	Parameters     []PropertyDefinition     `json:"parameters"`
	SharedSettings []PropertyDefinition     `json:"sharedSettings"`
	ExecutorID     string                   `json:"-"`
	Validate       workflow.ConfigValidator `json:"-"`
}

// Registry is the single process-local catalogue of node definitions. It is
// assembled at startup and is read-only once the server begins handling work.
type Registry struct {
	definitions map[definitionKey]Definition
}

type definitionKey struct {
	nodeType string
	version  int
}

var _ workflow.TypeCatalog = (*Registry)(nil)

// NewRegistry creates an empty registry. Callers register all built-ins during
// composition before sharing it with API, compiler, and runtime services.
func NewRegistry() *Registry {
	return &Registry{definitions: make(map[definitionKey]Definition)}
}

// Register adds one immutable node definition. A type/version pair can never
// be replaced at runtime, which keeps compiled graphs and client metadata
// deterministic for the life of a process.
func (registry *Registry) Register(definition Definition) error {
	if registry == nil {
		return fmt.Errorf("node registry is required")
	}
	if err := validateDefinition(definition); err != nil {
		return err
	}
	key := definitionKey{nodeType: definition.Type, version: definition.Version}
	if _, exists := registry.definitions[key]; exists {
		return fmt.Errorf("node type %q version %d is already registered", definition.Type, definition.Version)
	}
	registry.definitions[key] = cloneDefinition(definition)
	return nil
}

// Get returns presentation and editor metadata for one exact node version.
func (registry *Registry) Get(nodeType string, version int) (Definition, bool) {
	if registry == nil {
		return Definition{}, false
	}
	definition, found := registry.definitions[definitionKey{nodeType: nodeType, version: version}]
	return cloneDefinition(definition), found
}

// List returns every definition in stable type/version order.
func (registry *Registry) List() []Definition {
	if registry == nil {
		return []Definition{}
	}
	definitions := make([]Definition, 0, len(registry.definitions))
	for _, definition := range registry.definitions {
		definitions = append(definitions, cloneDefinition(definition))
	}
	sort.Slice(definitions, func(left, right int) bool {
		if definitions[left].Type != definitions[right].Type {
			return definitions[left].Type < definitions[right].Type
		}
		return definitions[left].Version < definitions[right].Version
	})
	return definitions
}

// Lookup implements workflow.Catalog for graph compilation.
func (registry *Registry) Lookup(nodeType string, version int) (workflow.NodeDefinition, bool) {
	definition, found := registry.Get(nodeType, version)
	if !found {
		return workflow.NodeDefinition{}, false
	}
	return workflow.NodeDefinition{
		Type:               definition.Type,
		Version:            definition.Version,
		Inputs:             append([]workflow.Port(nil), definition.Inputs...),
		Outputs:            append([]workflow.Port(nil), definition.Outputs...),
		RequiredParameters: requiredParameters(definition.Parameters),
		ExecutorID:         definition.ExecutorID,
		Validate:           definition.Validate,
	}, true
}

// HasType implements workflow.TypeCatalog, allowing the compiler to report an
// unknown node version separately from an unknown node type.
func (registry *Registry) HasType(nodeType string) bool {
	if registry == nil {
		return false
	}
	for key := range registry.definitions {
		if key.nodeType == nodeType {
			return true
		}
	}
	return false
}

func validateDefinition(definition Definition) error {
	if definition.Type == "" || definition.Version < 1 {
		return fmt.Errorf("node definition type and positive version are required")
	}
	if definition.DisplayName == "" || definition.Category == "" {
		return fmt.Errorf("node definition %q requires display name and category", definition.Type)
	}
	if definition.ExecutorID == "" {
		return fmt.Errorf("node definition %q requires executor binding", definition.Type)
	}
	if err := validatePorts(definition.Type, "input", definition.Inputs); err != nil {
		return err
	}
	if err := validatePorts(definition.Type, "output", definition.Outputs); err != nil {
		return err
	}
	if err := validateProperties(definition.Type, "parameter", definition.Parameters); err != nil {
		return err
	}
	return validateProperties(definition.Type, "shared setting", definition.SharedSettings)
}

func validatePorts(nodeType, direction string, ports []workflow.Port) error {
	seen := make(map[string]struct{}, len(ports))
	for _, port := range ports {
		if port.Name == "" || !knownConnectionKind(port.Kind) {
			return fmt.Errorf("node definition %q has invalid %s port", nodeType, direction)
		}
		if _, exists := seen[port.Name]; exists {
			return fmt.Errorf("node definition %q has duplicate %s port %q", nodeType, direction, port.Name)
		}
		seen[port.Name] = struct{}{}
	}
	return nil
}

func validateProperties(nodeType, group string, properties []PropertyDefinition) error {
	seen := make(map[string]struct{}, len(properties))
	for _, property := range properties {
		if property.Key == "" || property.Label == "" || !knownPropertyKind(property.Kind) {
			return fmt.Errorf("node definition %q has invalid %s metadata", nodeType, group)
		}
		if _, exists := seen[property.Key]; exists {
			return fmt.Errorf("node definition %q has duplicate %s %q", nodeType, group, property.Key)
		}
		seen[property.Key] = struct{}{}
	}
	return nil
}

func knownConnectionKind(kind workflow.ConnectionKind) bool {
	switch kind {
	case workflow.ConnectionMain, workflow.ConnectionLanguageModel, workflow.ConnectionMemory, workflow.ConnectionTool:
		return true
	default:
		return false
	}
}

func knownPropertyKind(kind PropertyKind) bool {
	switch kind {
	case PropertyString, PropertyNumber, PropertyBoolean, PropertySelect, PropertyKeyValue, PropertyConditions:
		return true
	default:
		return false
	}
}

func requiredParameters(properties []PropertyDefinition) []string {
	parameters := make([]string, 0, len(properties))
	for _, property := range properties {
		if property.Required {
			parameters = append(parameters, property.Key)
		}
	}
	return parameters
}

func cloneDefinition(definition Definition) Definition {
	definition.Inputs = append([]workflow.Port(nil), definition.Inputs...)
	definition.Outputs = append([]workflow.Port(nil), definition.Outputs...)
	definition.Parameters = cloneProperties(definition.Parameters)
	definition.SharedSettings = cloneProperties(definition.SharedSettings)
	return definition
}

func cloneProperties(properties []PropertyDefinition) []PropertyDefinition {
	cloned := make([]PropertyDefinition, len(properties))
	for index, property := range properties {
		cloned[index] = property
		cloned[index].Default = cloneValue(property.Default)
		cloned[index].Options = append([]PropertyOption(nil), property.Options...)
		cloned[index].VisibleWhen = append([]VisibilityCondition(nil), property.VisibleWhen...)
		for visibilityIndex := range cloned[index].VisibleWhen {
			cloned[index].VisibleWhen[visibilityIndex].Equals = cloneValue(cloned[index].VisibleWhen[visibilityIndex].Equals)
		}
	}
	return cloned
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, item := range typed {
			cloned[key] = cloneValue(item)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = cloneValue(item)
		}
		return cloned
	default:
		return value
	}
}
