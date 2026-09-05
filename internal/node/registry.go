// Package node owns the server-defined workflow node catalogue.
package node

import (
	"fmt"
	"sort"
	"strings"

	propertypkg "github.com/kilaslabs/kilas-flow/internal/property"
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
	// IconLight and IconDark are a node's own artwork, served by the icon
	// route. A node using a `builtin:` glyph ships none: that name resolves in
	// the editor to a component it already imports.
	IconLight *IconAsset `json:"-"`
	IconDark  *IconAsset `json:"-"`
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
	// Source says where this definition came from.
	//
	// It is set by the registration path, never read from the definition: a
	// pack that could declare itself built-in would inherit the built-in
	// namespace and win every precedence contest, so the tag has to be a
	// property of *how* something was registered rather than of what it
	// claims.
	Source Source `json:"source"`
	// PortsFor computes this node's ports from its own parameters.
	//
	// A Switch has one output per rule the user wrote and a Merge has as many
	// inputs as it was told to take, so their ports are not a property of the
	// type. It is a callback for the same reason Validate is: the answer
	// depends on the node, not on the definition.
	//
	// The static Inputs and Outputs stay, and are what the editor shows for an
	// unconfigured node and what the catalogue advertises — a picker cannot ask
	// a node that does not exist yet how many ports it will have.
	PortsFor func(parameters map[string]any, version workflow.TypeVersion) (inputs, outputs []workflow.Port) `json:"-"`
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

// Source says where a registry entry came from.
type Source string

const (
	// SourceBuiltin is a node compiled into this binary.
	SourceBuiltin Source = "builtin"
	// SourcePack is a node from a generated or installed node pack.
	SourcePack Source = "pack"
	// SourceSidecar is a node whose implementation runs outside this process.
	SourceSidecar Source = "sidecar"
)

// BuiltinPrefix is the namespace only built-in registration may claim.
//
// A pack registering into it could shadow — or be mistaken for — a node this
// project ships, which is a supply-chain problem rather than a naming one.
const BuiltinPrefix = "kilasflow."

// CredentialRequirement is one credential type a node can use.
type CredentialRequirement struct {
	// Type is the credential type ID.
	Type string `json:"type"`
	// Required marks a credential the node cannot run without.
	Required bool `json:"required,omitempty"`
	// VisibleWhen shows this requirement only for some parameter values, so a
	// node offering several auth modes asks for the credential the chosen mode
	// actually needs.
	VisibleWhen []propertypkg.VisibilityCondition `json:"visibleWhen,omitempty"`
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
// Register adds a built-in definition.
//
// Built-ins register first and always win a collision, which is what makes the
// catalogue independent of load order — a catalogue that depended on it would
// change when a directory listing did.
func (registry *Registry) Register(definition Definition) error {
	return registry.register(definition, SourceBuiltin)
}

// RegisterFrom adds a definition from a pack or a sidecar.
//
// A separate entry point rather than a field on the definition: the source is a
// property of how something was registered, and a pack able to declare itself
// built-in would claim the built-in namespace and win every precedence contest.
func (registry *Registry) RegisterFrom(source Source, definition Definition) error {
	switch source {
	case SourcePack, SourceSidecar:
	case SourceBuiltin:
		return fmt.Errorf("only built-in registration may claim source %q", SourceBuiltin)
	default:
		return fmt.Errorf("node source %q is not supported", source)
	}
	return registry.register(definition, source)
}

func (registry *Registry) register(definition Definition, source Source) error {
	if registry == nil {
		return fmt.Errorf("node registry is required")
	}
	// Set here, never read from the definition.
	definition.Source = source

	if source != SourceBuiltin && strings.HasPrefix(definition.Type, BuiltinPrefix) {
		return fmt.Errorf("node type %q claims the reserved %q namespace, which only built-in nodes may use", definition.Type, BuiltinPrefix)
	}
	if err := validateDefinition(definition); err != nil {
		return err
	}
	for _, icon := range []*IconAsset{definition.IconLight, definition.IconDark} {
		if err := ValidateIcon(icon); err != nil {
			return fmt.Errorf("node definition %q: %w", definition.Type, err)
		}
	}
	key := definitionKey{nodeType: definition.Type, version: definition.Version}
	if existing, exists := registry.definitions[key]; exists {
		// Refused rather than silently replaced, and naming both sources: a
		// pack quietly shadowing a built-in is a supply-chain problem, and
		// "last wins" would make the catalogue depend on load order.
		return fmt.Errorf("node type %q version %s is already registered by a %s node; the %s registration was refused",
			definition.Type, definition.Version, existing.Source, source)
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
		// The compiler asks this per node, so a parameter hidden by that node's
		// own configuration is not demanded of it.
		RequiredFor: func(parameters map[string]any, typeVersion workflow.TypeVersion) []string {
			return visibleRequiredParameters(definition.Parameters, parameters, typeVersion.String())
		},
		PortsFor:             definition.PortsFor,
		RequiredCredentials:  requiredCredentials(definition),
		ExecutorID:           definition.ExecutorID,
		Validate:             definition.Validate,
		WebhookPathParameter: webhookPathParameter(definition),
	}, true
}

// requiredCredentials lists the credential types a node cannot run without.
func requiredCredentials(definition Definition) []string {
	required := make([]string, 0, len(definition.Credentials))
	for _, declared := range definition.Credentials {
		if declared.Required {
			required = append(required, declared.Type)
		}
	}
	if len(required) == 0 {
		return nil
	}
	return required
}

func webhookPathParameter(definition Definition) string {
	if definition.Webhook == nil {
		return ""
	}
	return definition.Webhook.PathParameter
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
	for _, declared := range properties {
		if declared.Key == "" || declared.Label == "" || !knownPropertyKind(declared.Kind) {
			return fmt.Errorf("node definition %q has invalid %s metadata", nodeType, group)
		}
		if _, exists := seen[declared.Key]; exists {
			return fmt.Errorf("node definition %q has duplicate %s %q", nodeType, group, declared.Key)
		}
		seen[declared.Key] = struct{}{}

		if err := propertypkg.ValidateLoader(declared.LoadOptions); err != nil {
			return fmt.Errorf("node definition %q %s %q: %w", nodeType, group, declared.Key, err)
		}
		if err := propertypkg.ValidateVisibility(declared.DisplayOptions); err != nil {
			return fmt.Errorf("node definition %q %s %q: %w", nodeType, group, declared.Key, err)
		}
		if err := propertypkg.ValidateAssignments(declared.Assignments); err != nil {
			return fmt.Errorf("node definition %q %s %q: %w", nodeType, group, declared.Key, err)
		}
		if err := validateProperties(nodeType, group+"."+declared.Key, declared.Fields); err != nil {
			return err
		}
		groupKeys := make(map[string]struct{}, len(declared.Groups))
		for _, nested := range declared.Groups {
			if nested.Key == "" || nested.Label == "" {
				return fmt.Errorf("node definition %q %s %q has an unnamed group", nodeType, group, declared.Key)
			}
			if _, exists := groupKeys[nested.Key]; exists {
				return fmt.Errorf("node definition %q %s %q has duplicate group %q", nodeType, group, declared.Key, nested.Key)
			}
			groupKeys[nested.Key] = struct{}{}
			if err := validateProperties(nodeType, group+"."+declared.Key+"."+nested.Key, nested.Fields); err != nil {
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
	return visibleRequiredParameters(properties, nil, "")
}

// visibleRequiredParameters lists the properties a node must have *given its
// configuration*.
//
// A hidden property is not required. A node shaped like a real n8n node — where
// chatId is required only when resource is message — would otherwise be
// unactivatable in every other configuration, which is what blocks every
// declarative node pack.
//
// Passing nil parameters evaluates visibility against an empty configuration,
// which is what the static list means: what a fresh node requires.
func visibleRequiredParameters(properties []PropertyDefinition, parameters map[string]any, typeVersion string) []string {
	required := make([]string, 0, len(properties))
	for _, declared := range properties {
		if declared.Kind == PropertyNotice {
			continue
		}
		if !declared.Required {
			continue
		}
		if !propertypkg.VisibleProperty(declared, parameters, typeVersion) {
			continue
		}
		repeated := declared.TypeOptions != nil && declared.TypeOptions.MultipleValues
		if declared.Default == nil || repeated {
			required = append(required, declared.Key)
		}
	}
	return required
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
			append([]propertypkg.VisibilityCondition(nil), definition.Credentials[index].VisibleWhen...)
	}
	if definition.Icon != nil {
		icon := *definition.Icon
		definition.Icon = &icon
	}
	definition.IconLight = cloneAsset(definition.IconLight)
	definition.IconDark = cloneAsset(definition.IconDark)
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
	for index, declared := range properties {
		cloned[index] = declared
		cloned[index].Default = cloneValue(declared.Default)
		cloned[index].Options = append([]PropertyOption(nil), declared.Options...)
		cloned[index].VisibleWhen = append([]VisibilityCondition(nil), declared.VisibleWhen...)
		for visibilityIndex := range cloned[index].VisibleWhen {
			cloned[index].VisibleWhen[visibilityIndex].Equals = cloneValue(cloned[index].VisibleWhen[visibilityIndex].Equals)
		}
		cloned[index].Fields = cloneProperties(declared.Fields)
		// Deep, not a slice copy: an assignment's Value can be a map, and a
		// caller mutating one it was handed would reach into the registry's own
		// storage.
		if declared.Assignments != nil {
			assignments := make([]propertypkg.Assignment, len(declared.Assignments))
			for assignmentIndex, assignment := range declared.Assignments {
				assignment.Value = cloneValue(assignment.Value)
				assignments[assignmentIndex] = assignment
			}
			cloned[index].Assignments = assignments
		}
		if declared.LoadOptions != nil {
			loader := *declared.LoadOptions
			loader.DependsOn = append([]string(nil), declared.LoadOptions.DependsOn...)
			cloned[index].LoadOptions = &loader
		}
		if declared.TypeOptions != nil {
			options := *declared.TypeOptions
			if declared.TypeOptions.MinValue != nil {
				value := *declared.TypeOptions.MinValue
				options.MinValue = &value
			}
			if declared.TypeOptions.MaxValue != nil {
				value := *declared.TypeOptions.MaxValue
				options.MaxValue = &value
			}
			if declared.TypeOptions.NumberPrecision != nil {
				value := *declared.TypeOptions.NumberPrecision
				options.NumberPrecision = &value
			}
			cloned[index].TypeOptions = &options
		}
		if declared.Groups != nil {
			groups := make([]PropertyGroup, len(declared.Groups))
			for groupIndex, group := range declared.Groups {
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
	PropertyKind        = propertypkg.Kind
	PropertyOption      = propertypkg.PropertyOption
	PropertyDefinition  = propertypkg.PropertyDefinition
	PropertyGroup       = propertypkg.PropertyGroup
	TypeOptions         = propertypkg.TypeOptions
	VisibilityCondition = propertypkg.VisibilityCondition
	// Assignment is one row of an assignmentCollection.
	Assignment = propertypkg.Assignment
	// AssignmentType is the declared type of an assignment's value.
	AssignmentType = propertypkg.AssignmentType
)

const (
	PropertyString          = propertypkg.KindString
	PropertyNumber          = propertypkg.KindNumber
	PropertyBoolean         = propertypkg.KindBoolean
	PropertyOptions         = propertypkg.KindOptions
	PropertyMultiOptions    = propertypkg.KindMultiOptions
	PropertyCollection      = propertypkg.KindCollection
	PropertyFixedCollection = propertypkg.KindFixedCollection
	PropertyNotice          = propertypkg.KindNotice
	PropertyJSON            = propertypkg.KindJSON
	PropertyDateTime        = propertypkg.KindDateTime
	PropertyKeyValue        = propertypkg.KindKeyValue
	PropertyConditions      = propertypkg.KindConditions
	PropertyAssignments     = propertypkg.KindAssignmentCollection
)

// KnownPropertyKinds is the closed set, in a stable order.
func KnownPropertyKinds() []PropertyKind { return propertypkg.KnownKinds() }

func knownPropertyKind(kind PropertyKind) bool { return propertypkg.Known(kind) }
