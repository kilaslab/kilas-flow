// Package property is the shared description language for a configurable
// field.
//
// It lives in its own leaf package because two catalogues need it and neither
// may depend on the other: a node definition describes its parameters with it,
// and a credential type describes its fields with it. Keeping it here is what
// lets a credential type reuse conditional visibility and password masking
// without the node catalogue having to know the credential catalogue exists.
package property

import (
	"fmt"
	"strings"
)

// Kind identifies a generic control the editor can render without a
// node-specific form implementation.
type Kind string

const (
	KindString  Kind = "string"
	KindNumber  Kind = "number"
	KindBoolean Kind = "boolean"
	// KindOptions is a single-choice select.
	//
	// It is spelled `options` rather than `select` because that is what n8n
	// calls it, and two names for one control would mean every generated pack
	// has to remember which one this server speaks. The rename is done now
	// because its blast radius is still entirely inside this repository; after
	// a pack ships it becomes a compatibility break for somebody else's node.
	KindOptions Kind = "options"
	// KindMultiOptions is a multi-choice select.
	KindMultiOptions Kind = "multiOptions"
	// KindCollection is an optional group of fields the user adds one at a
	// time.
	KindCollection Kind = "collection"
	// KindFixedCollection is a repeatable named group.
	KindFixedCollection Kind = "fixedCollection"
	// KindNotice is read-only guidance shown in the panel. It holds no
	// value: see the note on TypeOptions and requiredParameters.
	KindNotice Kind = "notice"
	// KindJSON is a raw JSON editor.
	KindJSON Kind = "json"
	// KindDateTime is a date and time picker.
	KindDateTime   Kind = "dateTime"
	KindKeyValue   Kind = "keyValue"
	KindConditions Kind = "conditions"
	// KindAssignmentCollection is an ordered list of `{name, type, value}`
	// rows whose value editor is chosen by each row's own type.
	//
	// It is its own kind rather than a preset over a fixed collection, which it
	// resembles from a distance. The difference is where it counts: a fixed
	// collection is a repeatable group of *declared* properties, while an
	// assignment's control is decided row by row by a sibling field, which a
	// generic group cannot express without the panel special-casing it anyway.
	KindAssignmentCollection Kind = "assignmentCollection"
	// KindResourceLocator picks one resource three ways — from a searched
	// list, by name, or by ID — and stores which way was used alongside the
	// value.
	//
	// The stored value is a self-describing object carrying an `__rl` sentinel,
	// exactly as n8n does, rather than a bare string with a sibling `…Mode`
	// parameter. A locator is imported and exported far more often than it is
	// authored, and the sibling form loses the pairing the moment a visibility
	// rule hides one half of it — which is precisely the form n8n used before
	// resource locators existed, and adopting it would mean writing a lossy
	// converter for every node that takes one.
	KindResourceLocator Kind = "resourceLocator"
	// KindResourceMapper types the columns inside whatever a locator picked.
	// See mapper.go for what it replaces and why an untyped bag is not enough.
	KindResourceMapper Kind = "resourceMapper"
)

// LocatorSentinel marks a stored resource locator value.
//
// The name is n8n's, so a document round-trips through this server unchanged.
const LocatorSentinel = "__rl"

// PropertyMode is one way a resource locator may name its resource.
type PropertyMode struct {
	// Name is the stored mode, and is what an importer matches on.
	Name  string `json:"name"`
	Label string `json:"label"`
	// Kind is the control this mode renders: a string field for a typed name
	// or ID, or an options select for a searched list.
	Kind Kind `json:"kind"`
	// LoadOptions supplies a list mode's values.
	LoadOptions *OptionsLoader `json:"loadOptions,omitempty"`
	Placeholder string         `json:"placeholder,omitempty"`
	Hint        string         `json:"hint,omitempty"`
	// Pattern validates a typed value, and PatternHint says what it wants.
	Pattern     string `json:"pattern,omitempty"`
	PatternHint string `json:"patternHint,omitempty"`
}

// ExpressionModeName is the one mode name a locator may never use.
//
// A locator's stored value has `mode` and `value` keys, and so does the
// expression marker. A mode literally named "expression" would make the two
// indistinguishable: Resolve would replace the whole locator with the evaluated
// string, the executor would receive a bare string where it expects an object,
// and nothing would report anything — the node would simply read an empty table
// name. An expression goes *inside* the locator's value slot, where the
// existing recursion resolves it in place and the sentinel survives.
const ExpressionModeName = "expression"

// ValidateModes refuses a resource locator this server cannot render.
func ValidateModes(kind Kind, modes []PropertyMode) error {
	if kind != KindResourceLocator {
		if len(modes) > 0 {
			return fmt.Errorf("only a resourceLocator may declare modes")
		}
		return nil
	}
	if len(modes) == 0 {
		return fmt.Errorf("a resourceLocator needs at least one mode")
	}
	seen := make(map[string]struct{}, len(modes))
	for _, mode := range modes {
		if strings.TrimSpace(mode.Name) == "" || strings.TrimSpace(mode.Label) == "" {
			return fmt.Errorf("every resourceLocator mode needs a name and a label")
		}
		if mode.Name == ExpressionModeName {
			return fmt.Errorf("a resourceLocator mode may not be named %q; it would be indistinguishable "+
				"from the expression marker, which shares the mode and value keys", ExpressionModeName)
		}
		if _, exists := seen[mode.Name]; exists {
			return fmt.Errorf("resourceLocator mode %q is declared twice", mode.Name)
		}
		seen[mode.Name] = struct{}{}
		switch mode.Kind {
		case KindString:
		case KindOptions:
			if mode.LoadOptions == nil {
				return fmt.Errorf("resourceLocator mode %q offers a list and declares no loader", mode.Name)
			}
		default:
			return fmt.Errorf("resourceLocator mode %q must render as a string or an options list", mode.Name)
		}
		if err := ValidateLoader(mode.LoadOptions); err != nil {
			return fmt.Errorf("resourceLocator mode %q: %w", mode.Name, err)
		}
	}
	return nil
}

// Locator is a stored resource locator, decoded.
type Locator struct {
	Mode string
	// Value is the resource itself, still unresolved: it may hold an
	// expression marker, which the caller resolves like any other parameter.
	Value any
	// CachedResultName is what the user last saw in the picker. It is display
	// only and never used to resolve anything, because a name cached on one
	// server has no authority on another.
	CachedResultName string
}

// ReadLocator decodes a stored resource locator.
//
// A bare string is accepted as a locator whose mode is unknown, because that is
// what a document written before this kind existed carries, and refusing it
// would break every such node on load rather than at the point it matters.
func ReadLocator(value any) (Locator, bool) {
	switch typed := value.(type) {
	case string:
		return Locator{Value: typed}, typed != ""
	case map[string]any:
		if sentinel, _ := typed[LocatorSentinel].(bool); !sentinel {
			return Locator{}, false
		}
		mode, _ := typed["mode"].(string)
		name, _ := typed["cachedResultName"].(string)
		return Locator{Mode: mode, Value: typed["value"], CachedResultName: name}, true
	default:
		return Locator{}, false
	}
}

// LocatorIsSet reports whether a locator names anything yet.
//
// One function, because "not chosen yet" arrives in four shapes — absent, an
// empty string, a locator with an empty value, and something that is not a
// locator at all — and a validator that checked only one of them would pass a
// node that cannot run.
func LocatorIsSet(value any) bool {
	locator, ok := ReadLocator(value)
	if !ok {
		return false
	}
	switch inner := locator.Value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(inner) != ""
	default:
		return true
	}
}

// WriteLocator renders a locator back into its stored shape.
func WriteLocator(locator Locator) map[string]any {
	stored := map[string]any{LocatorSentinel: true, "mode": locator.Mode, "value": locator.Value}
	if locator.CachedResultName != "" {
		stored["cachedResultName"] = locator.CachedResultName
	}
	return stored
}

// Assignment is one row of an assignment collection.
//
// The shape is n8n's — `{id, name, type, value}` — because an imported Set node
// carries exactly this and a round trip has to give it back unchanged, order
// included. A Go map cannot: it has no order, and two rows writing the same
// field are indistinguishable from one.
type Assignment struct {
	// ID is n8n's own row identity. It is carried rather than regenerated so a
	// round trip is byte-identical; an empty one is filled in on import.
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	// Type decides the value editor and how the value is read at run time.
	Type AssignmentType `json:"type"`
	// Value is the row's value, which may be an expression marker.
	Value any `json:"value,omitempty"`
}

// AssignmentType is the declared type of one assignment's value.
type AssignmentType string

// The types an assignment may declare.
//
// These five are what n8n's Set node's own type picker offers, verbatim from
// the reference checkout's `Set/v2/manual.mode.ts` — its pre-assignment
// `fields` control lists String, Number, Boolean, Array and Object, and the
// assignment collection replaced that control without widening it. n8n's
// `FieldType` union is wider (dateTime, url, jwt and more), but those belong to
// other controls; accepting them here would be accepting a type this product
// has no editor for.
const (
	AssignmentString  AssignmentType = "string"
	AssignmentNumber  AssignmentType = "number"
	AssignmentBoolean AssignmentType = "boolean"
	AssignmentArray   AssignmentType = "array"
	AssignmentObject  AssignmentType = "object"
)

// AssignmentTypes is the closed set, in a stable order.
func AssignmentTypes() []AssignmentType {
	return []AssignmentType{
		AssignmentString, AssignmentNumber, AssignmentBoolean, AssignmentArray, AssignmentObject,
	}
}

// KnownAssignmentType reports whether a type may be declared.
func KnownAssignmentType(declared AssignmentType) bool {
	for _, known := range AssignmentTypes() {
		if declared == known {
			return true
		}
	}
	return false
}

// ValidateAssignments refuses a malformed row.
//
// An unnamed row writes nothing and an unknown type has no editor, so both are
// refused where they are declared rather than becoming a surprise at run time.
func ValidateAssignments(assignments []Assignment) error {
	for index, assignment := range assignments {
		if strings.TrimSpace(assignment.Name) == "" {
			return fmt.Errorf("assignment %d has no name, so it would write nothing", index)
		}
		if !KnownAssignmentType(assignment.Type) {
			return fmt.Errorf("assignment %q declares type %q, which is not one of %v",
				assignment.Name, assignment.Type, AssignmentTypes())
		}
	}
	return nil
}

// KnownKinds is the closed set, in a stable order.
func KnownKinds() []Kind {
	return []Kind{
		KindString, KindNumber, KindBoolean,
		KindOptions, KindMultiOptions,
		KindCollection, KindFixedCollection,
		KindNotice, KindJSON, KindDateTime, KindResourceLocator, KindResourceMapper,
		KindKeyValue, KindConditions, KindAssignmentCollection,
	}
}

// TypeOptions refines how a control behaves without multiplying kinds.
//
// Unknown keys are rejected at registration rather than passed through: a bag
// that accepts anything is a bag whose contents nothing can rely on, and a
// generated pack emitting a key this server ignores would produce a control
// that silently does not behave as its author intended.
type TypeOptions struct {
	// Password masks the field.
	Password bool `json:"password,omitempty"`
	// Rows makes a string field multi-line. Zero is single-line.
	Rows int `json:"rows,omitempty"`
	// Editor asks for a dedicated editor instead of a text box. "code" is a
	// source editor: monospace, line numbers, indentation that Tab and Enter
	// keep, and highlighting for EditorLanguage. A code field is never an
	// expression, since a template marker there is part of the program.
	Editor Editor `json:"editor,omitempty" enum:"code"`
	// EditorLanguage is the language a code editor highlights and completes.
	EditorLanguage EditorLanguage `json:"editorLanguage,omitempty" enum:"javaScript,go,python,json"`
	// MinValue and MaxValue bound a number.
	MinValue *float64 `json:"minValue,omitempty"`
	MaxValue *float64 `json:"maxValue,omitempty"`
	// NumberPrecision is how many decimal places a number keeps.
	NumberPrecision *int `json:"numberPrecision,omitempty"`
	// MultipleValues makes the property a list.
	//
	// This one semantic has to be carried across exactly, because getting it
	// wrong silently corrupts every imported node: under MultipleValues the
	// property's Default describes **one element**, not the collection. A
	// property with MultipleValues and `default: {}` defaults to an empty list
	// whose elements look like `{}` — it does not default to `{}`.
	MultipleValues bool `json:"multipleValues,omitempty"`
	// MultipleValueButtonText labels the add button.
	MultipleValueButtonText string `json:"multipleValueButtonText,omitempty"`
}

// Editor names a dedicated editor a string field asks for.
type Editor string

// EditorCode is a source editor.
const EditorCode Editor = "code"

// EditorLanguage is the language a code editor treats its text as.
type EditorLanguage string

// The languages a code editor knows. javaScript is spelt as n8n spells its
// editorLanguage, so a converted pack's declaration reads the same here.
const (
	EditorLanguageJavaScript EditorLanguage = "javaScript"
	EditorLanguageGo         EditorLanguage = "go"
	EditorLanguagePython     EditorLanguage = "python"
	EditorLanguageJSON       EditorLanguage = "json"
)

// ValidateEditor checks a field's editor request.
//
// A language without an editor is refused rather than ignored: it would be
// a declaration that promises highlighting and renders a plain text box.
func ValidateEditor(kind Kind, options *TypeOptions) error {
	if options == nil || (options.Editor == "" && options.EditorLanguage == "") {
		return nil
	}
	if options.Editor != EditorCode {
		if options.Editor == "" {
			return fmt.Errorf("editorLanguage %q needs editor %q", options.EditorLanguage, EditorCode)
		}
		return fmt.Errorf("unknown editor %q; the only editor is %q", options.Editor, EditorCode)
	}
	if kind != KindString {
		return fmt.Errorf("a code editor holds text, so only a string field may ask for one")
	}
	switch options.EditorLanguage {
	case EditorLanguageJavaScript, EditorLanguageGo, EditorLanguagePython, EditorLanguageJSON:
		return nil
	case "":
		return fmt.Errorf("a code editor needs an editorLanguage")
	default:
		return fmt.Errorf("unknown editorLanguage %q", options.EditorLanguage)
	}
}

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

// visibilityOf reads a property's rule, translating the old single-value form.
//
// Same-key entries are merged into one condition whose values are OR'd, while
// different keys stay separate conditions that are AND'd — exactly n8n's
// displayOptions semantics. Without the merge, a shorthand like
// [{operation: insert}, {operation: update}] could never match, because one
// parameter cannot equal two values at once.
func visibilityOf(definition PropertyDefinition) Visibility {
	if !definition.DisplayOptions.IsEmpty() {
		return definition.DisplayOptions
	}
	if len(definition.VisibleWhen) == 0 {
		return Visibility{}
	}
	show := make([]Condition, 0, len(definition.VisibleWhen))
	indexByKey := make(map[string]int, len(definition.VisibleWhen))
	for _, condition := range definition.VisibleWhen {
		if index, exists := indexByKey[condition.Key]; exists {
			show[index].Values = append(show[index].Values, condition.Equals)
			continue
		}
		indexByKey[condition.Key] = len(show)
		show = append(show, Condition{Key: condition.Key, Values: []any{condition.Equals}})
	}
	return Visibility{Show: show}
}

// VisibleProperty reports whether one property is shown for a node's stored
// parameters.
func VisibleProperty(definition PropertyDefinition, parameters map[string]any, typeVersion string) bool {
	return Visible(visibilityOf(definition), parameters, typeVersion)
}

// WithDefaults fills in the parameters a node never stored.
//
// A property the user never touched has its declared default, and every
// visibility rule has to be evaluated against that. Without it, a rule reading
// `mode` on a node whose `mode` was never written sees nothing and hides a
// field the user is looking at — which is how a Set node saved before it had a
// mode ends up with no visible fields at all.
//
// It is a separate function rather than folded into VisibleProperty because
// only a caller holding the whole property list can supply the defaults, and
// pretending otherwise would put a lie in the signature.
func WithDefaults(properties []PropertyDefinition, parameters map[string]any) map[string]any {
	filled := make(map[string]any, len(parameters)+len(properties))
	for key, value := range parameters {
		filled[key] = value
	}
	for _, declared := range properties {
		if declared.Default == nil {
			continue
		}
		if _, present := filled[declared.Key]; !present {
			filled[declared.Key] = declared.Default
		}
	}
	return filled
}

// PropertyDefinition describes one node parameter or shared setting.
type PropertyDefinition struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Kind        Kind   `json:"kind"`
	Required    bool   `json:"required"`
	// Default is the value a fresh node starts with.
	//
	// Under TypeOptions.MultipleValues it describes **one element** of the
	// list, not the list itself.
	Default any `json:"default,omitempty"`
	// Options are the selectable values of an options or multiOptions control.
	//
	// Deliberately *only* that. n8n overloads the same field to carry nested
	// properties for a collection and named groups for a fixedCollection, which
	// makes its meaning depend on the sibling kind and produces a JSON schema
	// the generated TypeScript cannot express usefully. The nested carriers
	// below are typed separately for that reason.
	Options []PropertyOption `json:"options,omitempty"`
	// Fields are the nested properties of a `collection`.
	Fields []PropertyDefinition `json:"fields,omitempty"`
	// Groups are the named property groups of a `fixedCollection`.
	Groups []PropertyGroup `json:"groups,omitempty"`
	// TypeOptions refines the control.
	TypeOptions *TypeOptions `json:"typeOptions,omitempty"`
	// LoadOptions fetches the selectable values at edit time, for a property
	// whose valid values live on the customer's own service rather than being
	// knowable when this binary was built.
	LoadOptions *OptionsLoader `json:"loadOptions,omitempty"`
	// VisibleWhen is the shorthand: entries on different keys must all match by
	// equality, while several entries on one key mean "one of these values".
	VisibleWhen []VisibilityCondition `json:"visibleWhen,omitempty"`
	// DisplayOptions is the full rule — show and hide groups, several accepted
	// values per key, and operators beyond equality. When set it replaces
	// VisibleWhen rather than combining with it, so a property has exactly one
	// rule and there is never a question of which wins.
	DisplayOptions Visibility `json:"displayOptions,omitempty"`
	// Modes are a resourceLocator's ways of naming its resource.
	//
	// Its own typed field rather than being overloaded onto Options, for the
	// reason every nested carrier here has one: a field whose meaning depends
	// on the sibling kind produces a JSON schema the generated TypeScript
	// cannot express as anything better than `unknown`.
	Modes []PropertyMode `json:"modes,omitempty"`
	// Mapper describes a resourceMapper's schema source and its modes.
	Mapper *ResourceMapperDeclaration `json:"mapper,omitempty"`
	// Assignments is the default rows of an `assignmentCollection`.
	//
	// Its own field rather than overloaded onto Options, for the reason every
	// other nested carrier here has one: a field whose meaning depends on the
	// sibling kind produces a JSON schema the generated TypeScript cannot
	// express as anything better than `unknown`.
	Assignments []Assignment `json:"assignments,omitempty"`
}

// PropertyGroup is one named group inside a fixedCollection.
type PropertyGroup struct {
	Key    string               `json:"key"`
	Label  string               `json:"label"`
	Fields []PropertyDefinition `json:"fields"`
}

// Known reports whether a kind is in the closed set. It is the single authority
// both catalogues validate against.
func Known(kind Kind) bool {
	for _, known := range KnownKinds() {
		if kind == known {
			return true
		}
	}
	return false
}
