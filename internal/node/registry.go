// Package node owns the server-defined workflow node catalogue.
package node

import (
	"fmt"
	"sort"
	"strings"

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
	Type    string               `json:"type"`
	Version workflow.TypeVersion `json:"version"`
	// LoopEntry marks a node a back edge may close onto, so the compiler can
	// accept a bounded loop without learning a node type by name.
	LoopEntry      bool                     `json:"loopEntry,omitempty"`
	DisplayName    string                   `json:"displayName"`
	Description    string                   `json:"description,omitempty"`
	Category       string                   `json:"category"`
	Inputs         []workflow.Port          `json:"inputs"`
	Outputs        []workflow.Port          `json:"outputs"`
	Parameters     []PropertyDefinition     `json:"parameters"`
	SharedSettings []PropertyDefinition     `json:"sharedSettings"`
	ExecutorID     string                   `json:"-"`
	Validate       workflow.ConfigValidator `json:"-"`

	// --- Presentation ------------------------------------------------------
	//
	// Everything visual used to live in the SPA, hardcoded against KilasFlow's
	// own node types, so a node the server added arrived on the canvas as a
	// grey box with no subtitle. That is fine for a closed list written in this
	// repository and stops being fine the moment a generated pack brings a
	// hundred operations nobody will hand-write frontend entries for.

	// Group is behavioural and multi-valued: what a node *does*, as against
	// where it is filed. It is deliberately separate from Category, which is a
	// panel-grouping label — the picker used to decide whether a node could
	// start a workflow by comparing that display string, which is behaviour
	// inferred from a caption.
	Group []NodeGroup `json:"group"`
	// Icon names the glyph in each theme. See NodeIcon for the two forms.
	Icon *NodeIcon `json:"icon,omitempty"`
	// IconColor is the accent the canvas draws the node with.
	IconColor string `json:"iconColor,omitempty"`
	// Subtitle is a `{{ $parameter.key }}` template the editor renders beneath
	// the node's name, so a configured node says what it will do. See
	// SubtitleRoot for why the dialect is restricted.
	Subtitle string `json:"subtitle,omitempty"`
	// DocumentationURL points at this node's reference page.
	DocumentationURL string `json:"documentationUrl,omitempty"`
	// Codex carries the picker's own metadata, kept in a block of its own so
	// panel arrangement can change without disturbing the node's identity.
	Codex *NodeCodex `json:"codex,omitempty"`
}

// NodeGroup is a behavioural classification. The set is closed: a definition
// declaring anything else is refused at registration, so a typo cannot quietly
// produce a node the picker files nowhere.
type NodeGroup string

const (
	// GroupTrigger starts a workflow.
	GroupTrigger NodeGroup = "trigger"
	// GroupInput brings data in.
	GroupInput NodeGroup = "input"
	// GroupOutput sends data out.
	GroupOutput NodeGroup = "output"
	// GroupTransform reshapes data without leaving the instance.
	GroupTransform NodeGroup = "transform"
	// GroupSchedule runs on a timer.
	GroupSchedule NodeGroup = "schedule"
	// GroupOrganization annotates the canvas and never executes.
	GroupOrganization NodeGroup = "organization"
)

// KnownGroups is the closed set, in a stable order for documentation.
func KnownGroups() []NodeGroup {
	return []NodeGroup{
		GroupTrigger, GroupInput, GroupOutput,
		GroupTransform, GroupSchedule, GroupOrganization,
	}
}

// BuiltinIconPrefix marks an icon the client already ships.
//
// Icons take two forms, discriminated by prefix. `builtin:<name>` names a glyph
// the SPA already imports, which keeps first-party nodes shipping no new bytes;
// anything else is a path the server answers with the node's own artwork, which
// is what a generated pack needs to bring its own. Choosing only one form would
// either force an asset pipeline for glyphs that already exist as components,
// or leave a generated pack with no way to ship artwork at all.
const BuiltinIconPrefix = "builtin:"

// NodeIcon names a node's glyph per theme. Both variants are optional; a node
// that sets only Light uses it in both.
type NodeIcon struct {
	Light string `json:"light,omitempty"`
	Dark  string `json:"dark,omitempty"`
}

// SubtitleRoot is the only root a subtitle template may use.
//
// A subtitle is rendered by the editor against parameters that have not been
// saved, so the server cannot evaluate it and it cannot read run-time data. It
// is a template over the node's own parameters and nothing else — restricting
// it here is what stops it becoming a second expression dialect with its own
// grammar and its own surprises.
const SubtitleRoot = "$parameter"

// NodeCodex is the picker's metadata: how a node is filed and found.
type NodeCodex struct {
	// Categories are the top-level sections a node appears under.
	Categories []string `json:"categories,omitempty"`
	// Subcategories group within a category, keyed by category name.
	Subcategories map[string][]string `json:"subcategories,omitempty"`
	// Aliases are extra search terms, so a node is found by the name its users
	// call it rather than only the one it is registered under.
	Aliases []string `json:"aliases,omitempty"`
}

// Registry is the single process-local catalogue of node definitions. It is
// assembled at startup and is read-only once the server begins handling work.
type Registry struct {
	definitions map[definitionKey]Definition
}

type definitionKey struct {
	nodeType string
	version  workflow.TypeVersion
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
func (registry *Registry) Get(nodeType string, version workflow.TypeVersion) (Definition, bool) {
	if registry == nil {
		return Definition{}, false
	}
	definition, found := registry.definitions[definitionKey{nodeType: nodeType, version: version}]
	return cloneDefinition(definition), found
}

// Resolve picks the definition a document's requested version should run
// against.
//
// The rule is: the highest registered version that is less than or equal to the
// one asked for; and when nothing is asked for, the highest registered version
// there is. It fails when every registered version is higher than the request.
//
// That is how n8n treats an older workflow against a newer node, and the
// direction matters. Resolving upward would silently run a workflow written for
// version 2 against version 3's parameter shape, which is a behaviour change
// disguised as a lookup. Resolving downward can only ever give a workflow the
// shape it was written for or an older one, and failing outright when even the
// oldest registered version is newer says plainly that this installation cannot
// run this workflow rather than guessing.
//
// It is also what lets an import preserve n8n's own typeVersion. A node
// imported as Set 3.4 keeps that version in the document even while only
// version 1 is registered, so it resolves to 1 today and lands on the right
// shape the moment a 3.4 is registered — without rewriting the document.
func (registry *Registry) Resolve(nodeType string, version workflow.TypeVersion) (Definition, bool) {
	if registry == nil {
		return Definition{}, false
	}
	var best Definition
	var chosen bool
	for key, candidate := range registry.definitions {
		if key.nodeType != nodeType {
			continue
		}
		if !version.IsZero() && key.version.Compare(version) > 0 {
			continue
		}
		if chosen && best.Version.Compare(key.version) >= 0 {
			continue
		}
		best, chosen = candidate, true
	}
	if !chosen {
		return Definition{}, false
	}
	return cloneDefinition(best), true
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
		return definitions[left].Version.Compare(definitions[right].Version) < 0
	})
	return definitions
}

// Lookup implements workflow.Catalog for graph compilation.
func (registry *Registry) Lookup(nodeType string, version workflow.TypeVersion) (workflow.NodeDefinition, bool) {
	definition, found := registry.Resolve(nodeType, version)
	if !found {
		return workflow.NodeDefinition{}, false
	}
	return workflow.NodeDefinition{
		Type:               definition.Type,
		Version:            definition.Version,
		LoopEntry:          definition.LoopEntry,
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
	if definition.Type == "" || definition.Version.IsZero() {
		return fmt.Errorf("node definition type and positive version are required")
	}
	if definition.DisplayName == "" || definition.Category == "" {
		return fmt.Errorf("node definition %q requires display name and category", definition.Type)
	}
	if len(definition.Group) == 0 {
		return fmt.Errorf("node definition %q must declare at least one group", definition.Type)
	}
	for _, group := range definition.Group {
		if !knownGroup(group) {
			return fmt.Errorf("node definition %q declares unknown group %q", definition.Type, group)
		}
	}
	if err := validateSubtitle(definition.Type, definition.Subtitle); err != nil {
		return err
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

// requiredParameters lists the parameters a document must actually carry.
//
// A property that declares a default is not among them: the default is the
// server's answer for an absent value, so demanding the key as well would
// reject a perfectly valid hand-authored or imported document over a field the
// server already knows how to fill in.
func requiredParameters(properties []PropertyDefinition) []string {
	parameters := make([]string, 0, len(properties))
	for _, property := range properties {
		if property.Required && property.Default == nil {
			parameters = append(parameters, property.Key)
		}
	}
	return parameters
}

// cloneDefinition deep-copies everything a caller could mutate.
//
// The registry hands out copies, so a new slice or map that skips this
// reintroduces aliasing between every caller and the registry's own storage —
// silently, and only visible once something mutates what it was given.
func cloneDefinition(definition Definition) Definition {
	definition.Inputs = append([]workflow.Port(nil), definition.Inputs...)
	definition.Outputs = append([]workflow.Port(nil), definition.Outputs...)
	definition.Parameters = cloneProperties(definition.Parameters)
	definition.SharedSettings = cloneProperties(definition.SharedSettings)
	definition.Group = append([]NodeGroup(nil), definition.Group...)
	if definition.Icon != nil {
		icon := *definition.Icon
		definition.Icon = &icon
	}
	if definition.Codex != nil {
		codex := NodeCodex{
			Categories: append([]string(nil), definition.Codex.Categories...),
			Aliases:    append([]string(nil), definition.Codex.Aliases...),
		}
		if definition.Codex.Subcategories != nil {
			codex.Subcategories = make(map[string][]string, len(definition.Codex.Subcategories))
			for key, values := range definition.Codex.Subcategories {
				codex.Subcategories[key] = append([]string(nil), values...)
			}
		}
		definition.Codex = &codex
	}
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

func knownGroup(group NodeGroup) bool {
	for _, known := range KnownGroups() {
		if group == known {
			return true
		}
	}
	return false
}

// validateSubtitle refuses a template that reads anything but the node's own
// parameters.
//
// The editor renders a subtitle against parameters the user has not saved, so
// it cannot reach run-time data even in principle. Enforcing that here means a
// definition cannot ship a subtitle the editor will silently fail to render,
// and keeps the dialect from growing into a second expression language.
func validateSubtitle(nodeType, subtitle string) error {
	if strings.TrimSpace(subtitle) == "" {
		return nil
	}
	rest := subtitle
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			return nil
		}
		remainder := rest[start+2:]
		end := strings.Index(remainder, "}}")
		if end < 0 {
			return fmt.Errorf("node definition %q has a subtitle with an unclosed {{", nodeType)
		}
		body := strings.TrimSpace(remainder[:end])
		if !strings.HasPrefix(body, SubtitleRoot+".") {
			return fmt.Errorf("node definition %q subtitle may only read %s.<key>, got %q", nodeType, SubtitleRoot, body)
		}
		if strings.TrimSpace(strings.TrimPrefix(body, SubtitleRoot+".")) == "" {
			return fmt.Errorf("node definition %q subtitle names no parameter", nodeType)
		}
		rest = remainder[end+2:]
	}
}
