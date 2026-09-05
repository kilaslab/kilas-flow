// Package property is the shared description language for a configurable
// field.
//
// It lives in its own leaf package because two catalogues need it and neither
// may depend on the other: a node definition describes its parameters with it,
// and a credential type describes its fields with it. Keeping it here is what
// lets a credential type reuse conditional visibility and password masking
// without the node catalogue having to know the credential catalogue exists.
package property

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
)

// KnownKinds is the closed set, in a stable order.
func KnownKinds() []Kind {
	return []Kind{
		KindString, KindNumber, KindBoolean,
		KindOptions, KindMultiOptions,
		KindCollection, KindFixedCollection,
		KindNotice, KindJSON, KindDateTime,
		KindKeyValue, KindConditions,
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
// The old shape was single-value, AND-only, show-only and compared with strict
// JavaScript equality, so it could not express "one of these", could not hide,
// could not gate on version, and never matched a non-primitive at all. It is
// kept as a shorthand because it is genuinely the common case, and it means the
// existing definitions did not all have to be rewritten to say the same thing
// at greater length.
func visibilityOf(definition PropertyDefinition) Visibility {
	if !definition.DisplayOptions.IsEmpty() {
		return definition.DisplayOptions
	}
	if len(definition.VisibleWhen) == 0 {
		return Visibility{}
	}
	show := make([]Condition, 0, len(definition.VisibleWhen))
	for _, condition := range definition.VisibleWhen {
		show = append(show, Condition{Key: condition.Key, Values: []any{condition.Equals}})
	}
	return Visibility{Show: show}
}

// VisibleProperty reports whether one property is shown for a node's stored
// parameters.
func VisibleProperty(definition PropertyDefinition, parameters map[string]any, typeVersion string) bool {
	return Visible(visibilityOf(definition), parameters, typeVersion)
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
	// VisibleWhen is the shorthand: every condition must match by equality.
	VisibleWhen []VisibilityCondition `json:"visibleWhen,omitempty"`
	// DisplayOptions is the full rule — show and hide groups, several accepted
	// values per key, and operators beyond equality. When set it replaces
	// VisibleWhen rather than combining with it, so a property has exactly one
	// rule and there is never a question of which wins.
	DisplayOptions Visibility `json:"displayOptions,omitempty"`
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
