package n8n

import (
	"strings"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// Workflow Tool inputs, in both directions.
//
// n8n's Workflow Tool v2 stores its inputs as a resource mapper:
// {mappingMode, value: {<column>: <value>}, matchingColumns, schema: [...]}.
// A column the author handed to the model is the string
// `={{ /*n8n-auto-generated-fromAI-override*/ $fromAI('<id>', ``, '<type>') }}`;
// every other value is fixed. KilasFlow stores the same mapper, so the shape
// crosses whole instead of being flattened into a bare object, which is what
// lets a column's "model" or "fixed" state survive an import.

// readN8NWorkflowInputs reads n8n's mapper value: the columns, each converted
// to a KilasFlow value, and the live part of its schema copy. Anything
// unreadable present is reported so the caller can name it, never carried
// half-read.
//
// The results are the columns, the schema copy, whether anything was present
// and whether what was present could not be read.
func readN8NWorkflowInputs(value any) (map[string]any, []property.MapperField, []string, bool, bool) {
	collection, ok := value.(map[string]any)
	if !ok || collection == nil {
		return nil, nil, nil, value != nil, value != nil
	}
	inner, _ := collection["value"].(map[string]any)
	entries := inner
	if wrapper, isWrapper := inner["mapping"].(map[string]any); isWrapper && (len(wrapper) > 0 || len(inner) == 1) {
		// The modern resource mapper holds the fields directly under `value`;
		// the `mapping` wrapper is an older shape. Reading only the wrapper
		// imported every current n8n mapper as empty.
		entries = wrapper
	}
	schema, removed := n8nWorkflowInputSchema(collection["schema"])
	if len(entries) == 0 && len(schema) == 0 && len(removed) == 0 {
		// Absent, or a mapper the author never filled in. Optional either
		// way; an empty mapper and no mapper are the same tool.
		empty := len(collection) > 0
		return nil, nil, nil, empty, false
	}
	columns := make(map[string]any, len(entries))
	for _, key := range sortedKeys(entries) {
		columns[key] = fromN8NValue(entries[key])
	}
	return columns, schema, removed, true, false
}

// workflowInputsToKilas carries a mapper's columns as a plain field map, the
// shape the Execute Workflow node's input fields take.
//
// The three results are the value to store, whether anything was present, and
// whether what was present could not be read.
func workflowInputsToKilas(value any) (map[string]any, bool, bool) {
	columns, _, _, present, reported := readN8NWorkflowInputs(value)
	if len(columns) == 0 {
		return nil, present, reported
	}
	return columns, present, reported
}

// workflowToolInputsToKilas carries a Workflow Tool's mapper as the mapper
// KilasFlow stores, with the same three results.
func workflowToolInputsToKilas(value any) (map[string]any, bool, bool) {
	columns, schema, removed, present, reported := readN8NWorkflowInputs(value)
	if columns == nil && len(schema) == 0 && len(removed) == 0 {
		return nil, present, reported
	}
	// n8n offers the model only the columns written as a $fromAI call; the
	// others it lists but leaves unset. Here a column with no entry is the
	// model's, so an untouched n8n column is stored as an explicit null: a
	// fixed value, which keeps it out of the model's schema as it was in n8n.
	if columns == nil {
		columns = map[string]any{}
	}
	for _, field := range schema {
		if _, set := columns[field.ID]; !set {
			columns[field.ID] = nil
		}
	}
	// A column the author removed from the form is not mapped in n8n, so the
	// model is not offered it there either. It stays out of the schema copy, but
	// without an entry here it would read as a column the model fills.
	for _, id := range removed {
		if _, set := columns[id]; !set {
			columns[id] = nil
		}
	}
	return property.WriteMapping(property.Mapping{Mode: property.MappingManual, Values: columns, Schema: schema}), true, false
}

// n8nWorkflowInputSchema reads the schema copy n8n stores beside a mapper. The
// copy is display data here too, so only the live columns' ids, names and
// types cross. A column the author removed from the form is not one: its id is
// returned separately, so the caller can keep it out of the model's hands.
func n8nWorkflowInputSchema(raw any) ([]property.MapperField, []string) {
	rows, _ := raw.([]any)
	fields := make([]property.MapperField, 0, len(rows))
	var removedIDs []string
	seen := make(map[string]bool, len(rows))
	for _, entry := range rows {
		row, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		id := textOf(row["id"])
		if id == "" || seen[id] {
			continue
		}
		if removed, _ := row["removed"].(bool); removed {
			seen[id] = true
			removedIDs = append(removedIDs, id)
			continue
		}
		seen[id] = true
		name := textOf(row["displayName"])
		if name == "" {
			name = id
		}
		fields = append(fields, property.MapperField{ID: id, DisplayName: name, Type: workflowInputTypeFromN8N(textOf(row["type"]))})
	}
	return fields, removedIDs
}

// workflowInputTypeFromN8N renders a mapper column type in this server's
// vocabulary. n8n's time and options columns are text; anything unrecognised
// is left untyped rather than guessed.
func workflowInputTypeFromN8N(declared string) string {
	switch declared {
	case "string", "number", "boolean", "dateTime", "object", "array":
		return declared
	case "time", "options":
		return "string"
	default:
		return ""
	}
}

// workflowInputsToN8N writes a workflow tool's stored inputs back as n8n's
// mapper. A mapper-shaped value keeps its schema copy, and a schema column with
// no entry leaves as the model's own, because here that is what it is. The
// plain object an earlier version stored exports as the bare mapper it always
// did.
func workflowInputsToN8N(raw any, entries map[string]any) map[string]any {
	value := make(map[string]any, len(entries))
	for _, key := range sortedKeys(entries) {
		value[key] = workflowInputColumnToN8N(entries[key])
	}
	mapping, isMapper := property.ReadMapping(raw)
	if !isMapper {
		if len(value) == 0 {
			return nil
		}
		return map[string]any{"mappingMode": "defineBelow", "value": value}
	}
	schema := make([]any, 0, len(mapping.Schema))
	for _, field := range mapping.Schema {
		kind := field.Type
		if kind == "" {
			kind = "string"
		}
		schema = append(schema, map[string]any{
			"id": field.ID, "displayName": field.DisplayName, "required": false, "defaultMatch": false,
			"display": true, "canBeUsedToMatch": false, "type": kind, "removed": false,
		})
		if _, written := entries[field.ID]; !written {
			value[field.ID] = n8nModelColumn(field.ID, n8nFromAIType(field.Type))
		}
	}
	if len(value) == 0 && len(schema) == 0 {
		return nil
	}
	return map[string]any{
		"mappingMode": "defineBelow", "value": value, "matchingColumns": []any{}, "schema": schema,
		"attemptToConvertTypes": false, "convertFieldsToString": false,
	}
}

// workflowInputColumnToN8N writes one stored column. A column that is nothing
// but a $fromAI call is the model's, and n8n tells that apart from an
// expression that merely contains a call by the marker comment in front of it.
func workflowInputColumnToN8N(value any) any {
	object, isObject := value.(map[string]any)
	if !isObject || !expression.IsExpression(object) {
		return toN8NValue(value)
	}
	template, _ := object["value"].(string)
	if _, whole := ai.WholeFromAICall(template); !whole {
		return toN8NValue(value)
	}
	trimmed := strings.TrimSpace(template)
	body := strings.TrimSpace(trimmed[2 : len(trimmed)-2])
	return "={{ " + n8nFromAIMarker + " " + body + " }}"
}

// n8nModelColumn is n8n's own spelling of "let the model define this column".
func n8nModelColumn(id, kind string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(id)
	return "={{ " + n8nFromAIMarker + " $fromAI('" + escaped + "', ``, '" + kind + "') }}"
}

// n8nFromAIType is the type a $fromAI call names for a column: the four n8n
// spells. An object, an array and an untyped column are all json.
func n8nFromAIType(columnType string) string {
	switch columnType {
	case "string", "number", "boolean":
		return columnType
	default:
		return "json"
	}
}

// workflowToolInputsToN8N is the exporter's entry point for the tool's inputs.
// A value written as one expression has no column map to export, so it is
// reported and left out rather than written as a column named "mode".
func workflowToolInputsToN8N(node workflow.Node, parameters map[string]any) []Lossy {
	raw := node.Parameters["workflowInputs"]
	entries, whole, present := nodes.WorkflowToolInputs(raw)
	switch {
	case !present:
		return nil
	case whole:
		return []Lossy{{
			Severity: SeverityDropped, Field: "workflowInputs",
			Reason: "this tool's workflow inputs were one expression, which n8n's per-column mapper cannot hold. " +
				"They were left out of the export: map the inputs column by column in n8n.",
		}}
	}
	if inputs := workflowInputsToN8N(raw, entries); inputs != nil {
		parameters["workflowInputs"] = inputs
	}
	return nil
}
