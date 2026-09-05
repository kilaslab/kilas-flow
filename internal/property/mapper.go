package property

import (
	"fmt"
	"sort"
	"strings"
)

// A resource mapper types the columns inside whatever a resource locator
// picked: the locator says *which* table, and this says what goes in it.
//
// What it replaces is a keyValue bag, which is worse than it looks — no column
// type, no required flag, no matching column, and no way to tell a column the
// table has from a key somebody mistyped. An insert form that offers an untyped
// bag is strictly worse than the raw SQL box it replaces, which is reason
// enough to keep writing SQL in a product bought to stop that.

// Mapping modes, using n8n's names so an imported mapper needs no translation.
const (
	// MappingAuto matches incoming fields to columns by name.
	MappingAuto = "autoMapInputData"
	// MappingManual sets each column explicitly.
	MappingManual = "defineBelow"
)

// ResourceMapperDeclaration describes a resourceMapper property.
type ResourceMapperDeclaration struct {
	// Schema names the loader that returns this mapper's columns.
	//
	// Internal only. An HTTP schema source would need a response shape nothing
	// yet describes, and inventing one for no caller is how a format gets
	// fixed before anybody has had to live with it.
	Schema *OptionsLoader `json:"schema"`
	// SupportsAutoMap offers the automatic mapping mode. An operation that can
	// only be written column by column — because it needs values the incoming
	// item does not carry — leaves it off rather than offering a mode that
	// cannot work.
	SupportsAutoMap bool `json:"supportsAutoMap,omitempty"`
	// MatchingColumnsRequired means the operation identifies existing rows and
	// therefore needs at least one column to match on. Update and upsert do;
	// insert does not.
	MatchingColumnsRequired bool `json:"matchingColumnsRequired,omitempty"`
	// ValuesLabel names the mapped-values section in the editor.
	ValuesLabel string `json:"valuesLabel,omitempty"`
}

// MapperField is one column a mapper may write.
//
// The field names are n8n's ResourceMapperField, so an imported mapper's stored
// schema copy reads back without translation.
type MapperField struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	// Type is the column's own type: string, number, boolean, dateTime, object
	// or array. An unrecognised type renders as text with a warning rather than
	// disappearing from the form.
	Type     string `json:"type,omitempty"`
	Required bool   `json:"required,omitempty"`
	// CanBeUsedToMatch marks a column that identifies a row — a key, or
	// something unique enough to act as one.
	CanBeUsedToMatch bool `json:"canBeUsedToMatch,omitempty"`
	// DefaultMatch preselects a column as the match, which is what a primary
	// key should be.
	DefaultMatch bool `json:"defaultMatch,omitempty"`
	// ReadOnly marks a column the database fills in — an identity, a default
	// timestamp — which may be matched on but never written.
	ReadOnly bool `json:"readOnly,omitempty"`
	// Options enumerate a column whose values are a fixed set.
	Options []PropertyOption `json:"options,omitempty"`
}

// MapperSchema is one loaded column list.
type MapperSchema struct {
	Fields []MapperField `json:"fields"`
	// Reason explains an empty list in terms the user can act on, the same way
	// an empty option list does.
	Reason string `json:"reason,omitempty"`
}

// KnownMapperTypes is the closed set a column may declare.
func KnownMapperTypes() []string {
	return []string{"string", "number", "boolean", "dateTime", "object", "array"}
}

// KnownMapperType reports a type the editor has a control for.
func KnownMapperType(name string) bool {
	for _, known := range KnownMapperTypes() {
		if name == known {
			return true
		}
	}
	return false
}

// ValidateMapper refuses a resourceMapper this server cannot render.
func ValidateMapper(kind Kind, mapper *ResourceMapperDeclaration) error {
	if kind != KindResourceMapper {
		if mapper != nil {
			return fmt.Errorf("only a resourceMapper may declare a mapper")
		}
		return nil
	}
	if mapper == nil || mapper.Schema == nil {
		return fmt.Errorf("a resourceMapper needs a schema source; without one it can only render an untyped bag")
	}
	if mapper.Schema.Source != LoaderInternal {
		return fmt.Errorf("a resourceMapper's schema source must be internal, not %q", mapper.Schema.Source)
	}
	return ValidateLoader(mapper.Schema)
}

// Mapping is a stored resource mapper value, decoded.
type Mapping struct {
	Mode string
	// Values are the columns the user set, by column ID.
	Values map[string]any
	// MatchingColumns identify the rows an update or upsert acts on.
	MatchingColumns []string
	// Schema is the copy persisted alongside the choices.
	//
	// n8n persists it and so does this, because an imported mapper carries one
	// and dropping it would make export lossy. It is display data only: the
	// executor re-reads the live schema, because a copy taken when the node was
	// last opened has no authority over a table that has changed since.
	Schema []MapperField
}

// Stored keys of a resource mapper value, as n8n names them.
const (
	mappingModeKey     = "mappingMode"
	mappingValueKey    = "value"
	mappingMatchingKey = "matchingColumns"
	mappingSchemaKey   = "schema"
)

// ReadMapping decodes a stored mapper value.
func ReadMapping(value any) (Mapping, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return Mapping{}, false
	}
	mode, _ := object[mappingModeKey].(string)
	if mode == "" {
		return Mapping{}, false
	}
	mapping := Mapping{Mode: mode, Values: map[string]any{}}
	if values, ok := object[mappingValueKey].(map[string]any); ok {
		mapping.Values = values
	}
	if columns, ok := object[mappingMatchingKey].([]any); ok {
		for _, column := range columns {
			if name, ok := column.(string); ok {
				mapping.MatchingColumns = append(mapping.MatchingColumns, name)
			}
		}
	}
	if fields, ok := object[mappingSchemaKey].([]any); ok {
		for _, entry := range fields {
			row, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			mapping.Schema = append(mapping.Schema, MapperField{
				ID:               fieldText(row["id"]),
				DisplayName:      fieldText(row["displayName"]),
				Type:             fieldText(row["type"]),
				Required:         fieldFlag(row["required"]),
				CanBeUsedToMatch: fieldFlag(row["canBeUsedToMatch"]),
				DefaultMatch:     fieldFlag(row["defaultMatch"]),
				ReadOnly:         fieldFlag(row["readOnly"]),
			})
		}
	}
	return mapping, true
}

// WriteMapping renders a mapping back into its stored shape.
func WriteMapping(mapping Mapping) map[string]any {
	columns := make([]any, 0, len(mapping.MatchingColumns))
	for _, column := range mapping.MatchingColumns {
		columns = append(columns, column)
	}
	values := mapping.Values
	if values == nil {
		values = map[string]any{}
	}
	stored := map[string]any{
		mappingModeKey:     mapping.Mode,
		mappingValueKey:    values,
		mappingMatchingKey: columns,
	}
	if len(mapping.Schema) > 0 {
		fields := make([]any, 0, len(mapping.Schema))
		for _, field := range mapping.Schema {
			fields = append(fields, map[string]any{
				"id": field.ID, "displayName": field.DisplayName, "type": field.Type,
				"required": field.Required, "canBeUsedToMatch": field.CanBeUsedToMatch,
				"defaultMatch": field.DefaultMatch, "readOnly": field.ReadOnly,
			})
		}
		stored[mappingSchemaKey] = fields
	}
	return stored
}

// ValidateMapping checks a mapping against the schema that is live *now*.
//
// Called by an executor after parameters resolve, never at registration: a
// column's required-ness lives in the loaded schema rather than in the property,
// so registration-time validation has nothing to check it against and a stored
// schema copy is only what the table looked like when the node was last opened.
func ValidateMapping(declaration ResourceMapperDeclaration, schema []MapperField, mapping Mapping, operation string) error {
	if declaration.MatchingColumnsRequired && len(mapping.MatchingColumns) == 0 {
		return fmt.Errorf("%s identifies existing rows, so it needs at least one column to match on", operation)
	}
	known := make(map[string]MapperField, len(schema))
	for _, field := range schema {
		known[field.ID] = field
	}
	for _, column := range mapping.MatchingColumns {
		field, exists := known[column]
		if !exists {
			return fmt.Errorf("the matching column %q is not in this table", column)
		}
		if !field.CanBeUsedToMatch {
			return fmt.Errorf("the column %q cannot be used to match rows", column)
		}
	}
	if mapping.Mode != MappingManual {
		// Automatic mapping takes what the item carries, so a required column
		// is only knowable per item and is checked as the write is built.
		return nil
	}
	missing := make([]string, 0, 2)
	for _, field := range schema {
		if !field.Required || field.ReadOnly {
			continue
		}
		if isMatching(mapping.MatchingColumns, field.ID) {
			// A matching column identifies the row rather than supplying it,
			// so it satisfies its own required-ness.
			continue
		}
		if value, present := mapping.Values[field.ID]; !present || value == nil || value == "" {
			missing = append(missing, field.DisplayName)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("these columns are required and have no value: %s", strings.Join(missing, ", "))
	}
	return nil
}

// MappedColumns builds one row's write from a mapping and an incoming item.
//
// Under automatic mapping the columns come from the **intersection** of the
// item's own keys and the schema, never from the schema alone. Building it from
// the schema would turn a column the item does not carry into an explicit null,
// which on an update silently blanks a column the user never touched — no
// error, no diagnostic, and the damage visible only in the customer's data. The
// mirror mistake, sending every key the item carries, fails loudly at the
// database on the first unknown column, and loud is the one this chooses.
//
// The dropped keys are returned rather than discarded, so an unmapped field is
// visible as a diagnostic instead of merely absent.
func MappedColumns(schema []MapperField, mapping Mapping, item map[string]any) (map[string]any, []string) {
	known := make(map[string]MapperField, len(schema))
	for _, field := range schema {
		known[field.ID] = field
	}

	if mapping.Mode != MappingAuto {
		values := make(map[string]any, len(mapping.Values))
		for column, value := range mapping.Values {
			field, exists := known[column]
			if !exists || field.ReadOnly {
				continue
			}
			values[column] = value
		}
		return values, nil
	}

	values := make(map[string]any, len(item))
	dropped := make([]string, 0, 2)
	for key, value := range item {
		field, exists := known[key]
		if !exists {
			dropped = append(dropped, key)
			continue
		}
		if field.ReadOnly {
			continue
		}
		values[key] = value
	}
	sort.Strings(dropped)
	if len(dropped) == 0 {
		dropped = nil
	}
	return values, dropped
}

func isMatching(columns []string, id string) bool {
	for _, column := range columns {
		if column == id {
			return true
		}
	}
	return false
}

func fieldText(value any) string {
	name, _ := value.(string)
	return name
}

func fieldFlag(value any) bool {
	flag, _ := value.(bool)
	return flag
}
