// Package node owns the server-defined workflow node catalogue.
package node

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

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

	// Webhook declares that this node type binds an inbound HTTP path.
	//
	// Exactly one node type could do this before, and it was named at
	// composition — so a Telegram or WAHA trigger could be registered, placed
	// on a canvas, saved and activated, and would simply never receive a
	// request. No error, just an active workflow that is unreachable.
	Webhook *WebhookDeclaration `json:"webhook,omitempty"`
	// Credentials are the credential types this node can use.
	//
	// Named by string rather than by a typed reference, which is what n8n does
	// too: it keeps the node catalogue from having to know the credential
	// catalogue exists, and it is what would otherwise invert the dependency
	// the moment a credential type wanted to reference a node.
	Credentials []CredentialRequirement `json:"credentials,omitempty"`
	// LifecycleID binds this node's activate and deactivate hooks, by the same
	// opaque server-owned identifier pattern as ExecutorID: a trigger that
	// declares a hook nobody registered fails at startup rather than at
	// activation.
	LifecycleID string `json:"-"`
}

// WebhookDeclaration is how a node type says it answers an inbound request.
//
// Modelled on n8n's IWebhookDescription. The path and method are read from
// parameter *keys the definition names*, rather than from literal keys the
// extractor knows, so a trigger whose path lives under `chatPath` still binds.
type WebhookDeclaration struct {
	// Name identifies this webhook when a node declares more than one.
	Name string `json:"name"`
	// PathParameter is the parameter key holding the path.
	PathParameter string `json:"pathParameter,omitempty"`
	// MethodParameter is the parameter key holding the HTTP method, when the
	// node lets a user choose one.
	MethodParameter string `json:"methodParameter,omitempty"`
	// Method is the fixed method for a trigger that does not offer a choice —
	// a Telegram bot always receives POST.
	Method string `json:"method,omitempty"`
	// StaticPath binds a node with no path parameter at all, which is what a
	// trigger whose route is entirely minted needs.
	StaticPath string `json:"staticPath,omitempty"`
}

// CredentialRequirement is one credential type a node can use.
type CredentialRequirement struct {
	// Type is the credential type ID.
	Type string `json:"type"`
	// Required marks a credential the node cannot run without.
	Required bool `json:"required,omitempty"`
	// VisibleWhen shows this requirement only for some parameter values, so a
	// node offering several auth modes asks for the credential the chosen mode
	// actually needs.
	VisibleWhen []property.VisibilityCondition `json:"visibleWhen,omitempty"`
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

// LifecycleIDs lists every webhook lifecycle a registered node declares, so
// composition can prove each one is bound before the server starts.
//
// Discovering a missing binding at the first activation would mean a workflow
// that saves, activates, and silently never registers with its remote service —
// which is the exact failure this ticket exists to remove, reintroduced one
// level up.
func (registry *Registry) LifecycleIDs() []string {
	if registry == nil {
		return nil
	}
	seen := map[string]struct{}{}
	ids := make([]string, 0)
	for _, definition := range registry.definitions {
		if definition.LifecycleID == "" {
			continue
		}
		if _, already := seen[definition.LifecycleID]; already {
			continue
		}
		seen[definition.LifecycleID] = struct{}{}
		ids = append(ids, definition.LifecycleID)
	}
	sort.Strings(ids)
	return ids
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
		if port.Name == "" || !workflow.KnownConnectionKind(port.Kind) {
			return fmt.Errorf("node definition %q has invalid %s port", nodeType, direction)
		}
		if _, exists := seen[port.Name]; exists {
			return fmt.Errorf("node definition %q has duplicate %s port %q", nodeType, direction, port.Name)
		}
		seen[port.Name] = struct{}{}
	}
	return nil
}

// validateProperties checks one level and recurses into nested carriers.
//
// The `seen` map is per level, not shared across the recursion: a collection
// whose inner field is named `value` must not collide with an outer property
// also named `value`, because they live in different objects and never meet.
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

		if err := validateProperties(nodeType, group+"."+property.Key, property.Fields); err != nil {
			return err
		}
		groupKeys := make(map[string]struct{}, len(property.Groups))
		for _, nested := range property.Groups {
			if nested.Key == "" || nested.Label == "" {
				return fmt.Errorf("node definition %q %s %q has an unnamed group", nodeType, group, property.Key)
			}
			if _, exists := groupKeys[nested.Key]; exists {
				return fmt.Errorf("node definition %q %s %q has duplicate group %q", nodeType, group, property.Key, nested.Key)
			}
			groupKeys[nested.Key] = struct{}{}
			if err := validateProperties(nodeType, group+"."+property.Key+"."+nested.Key, nested.Fields); err != nil {
				return err
			}
		}
	}
	return nil
}

// requiredParameters lists the parameters a document must actually carry.
//
// A property that declares a default is not among them: the default is the
// server's answer for an absent value, so demanding the key as well would
// reject a perfectly valid hand-authored or imported document over a field the
// server already knows how to fill in.
// requiredParameters lists the properties a node cannot run without.
//
// A default normally satisfies a requirement — the node has a usable value
// without the user typing one. Two kinds break that reasoning:
//
// A `notice` holds no value at all. It must never be required, never written
// into a node's stored parameters, and never round-trip into the document,
// where it would fail validation on the next save.
//
// And under MultipleValues the default describes one *element*, so it says
// nothing about whether the list has any elements. A required list with an
// element default is still unsatisfied until the user adds one.
func requiredParameters(properties []PropertyDefinition) []string {
	parameters := make([]string, 0, len(properties))
	for _, property := range properties {
		if property.Kind == PropertyNotice {
			continue
		}
		if !property.Required {
			continue
		}
		repeated := property.TypeOptions != nil && property.TypeOptions.MultipleValues
		if property.Default == nil || repeated {
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
	definition.Credentials = append([]CredentialRequirement(nil), definition.Credentials...)
	for index := range definition.Credentials {
		definition.Credentials[index].VisibleWhen =
			append([]property.VisibilityCondition(nil), definition.Credentials[index].VisibleWhen...)
	}
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

// cloneProperties deep-copies a property tree, nested carriers included.
//
// A nested field that skips this aliases the registry's own storage at depth,
// which is the same bug as before but harder to see: a caller mutating an inner
// collection field would change what every other caller reads.
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
		cloned[index].Fields = cloneProperties(property.Fields)
		if property.TypeOptions != nil {
			options := *property.TypeOptions
			if property.TypeOptions.MinValue != nil {
				value := *property.TypeOptions.MinValue
				options.MinValue = &value
			}
			if property.TypeOptions.MaxValue != nil {
				value := *property.TypeOptions.MaxValue
				options.MaxValue = &value
			}
			if property.TypeOptions.NumberPrecision != nil {
				value := *property.TypeOptions.NumberPrecision
				options.NumberPrecision = &value
			}
			cloned[index].TypeOptions = &options
		}
		if property.Groups != nil {
			groups := make([]PropertyGroup, len(property.Groups))
			for groupIndex, group := range property.Groups {
				groups[groupIndex] = PropertyGroup{
					Key: group.Key, Label: group.Label, Fields: cloneProperties(group.Fields),
				}
			}
			cloned[index].Groups = groups
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

// The property language lives in its own leaf package so a credential type can
// describe its fields with it too, without either catalogue depending on the
// other. These aliases keep every existing call site — and every generated
// client field name — exactly as it was.
type (
	PropertyKind        = property.Kind
	PropertyOption      = property.PropertyOption
	PropertyDefinition  = property.PropertyDefinition
	PropertyGroup       = property.PropertyGroup
	TypeOptions         = property.TypeOptions
	VisibilityCondition = property.VisibilityCondition
)

const (
	PropertyString          = property.KindString
	PropertyNumber          = property.KindNumber
	PropertyBoolean         = property.KindBoolean
	PropertyOptions         = property.KindOptions
	PropertyMultiOptions    = property.KindMultiOptions
	PropertyCollection      = property.KindCollection
	PropertyFixedCollection = property.KindFixedCollection
	PropertyNotice          = property.KindNotice
	PropertyJSON            = property.KindJSON
	PropertyDateTime        = property.KindDateTime
	PropertyKeyValue        = property.KindKeyValue
	PropertyConditions      = property.KindConditions
)

// KnownPropertyKinds is the closed set, in a stable order.
func KnownPropertyKinds() []PropertyKind { return property.KnownKinds() }

func knownPropertyKind(kind PropertyKind) bool { return property.Known(kind) }
