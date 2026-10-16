package nodes

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The Google Sheets tool is the Google Sheets node offered to an AI Agent
// (tool_form.go has the one table row; there is no tool-specific executor). What
// a model can be offered of a spreadsheet is narrower than what a workflow step
// does with it, and the rules below are that difference.
//
// Operations. read (the default), append, appendOrUpdate, update and delete.
// clear is not offered: it empties a whole tab or a range in one call and has
// no row to bound it, so a model steered by text in a cell, or simply wrong,
// would lose a table in one step. delete is offered because its reach is what
// the author fixes: whether rows or columns, and how many, are not the
// model's, so one call removes a span of known size and the model picks only
// where it starts.
//
// Author-only. Which spreadsheet and tab the tool works on (documentId,
// sheetName: a credential can see many spreadsheets), the options, how filters
// combine, what a delete removes and how much, the column a filter looks in, the
// mapping mode, the column that finds the row and the names of the columns that
// are written are all the author's. The model supplies values: what a filter
// looks for, what each column holds, where a delete starts.
//
// No item-shaped writes. Automatic column mapping writes the running item's
// fields as the row, and a tool runs on the agent's input item, which is the
// conversation. The tool refuses it when it is validated and the executor
// refuses it again on every tool call. For the same reason the rule that
// lets an empty sheet take its columns from the first item does not apply to a
// tool, and a tool call stays below the sheet's column names: it neither
// updates by a row number above the first data row nor deletes row 1.

// sheetsToolStructuralKeys are the parameters the generic tool-form rule keeps
// out of the model's hands: no $fromAI, no expression, no {{ }}.
var sheetsToolStructuralKeys = []string{
	"documentId", "sheetName", "options", "combineFilters", "toDelete", "numberToDelete",
}

// validateGoogleSheetsTool is the tool form's own rule, run before the generic
// ones.
func validateGoogleSheetsTool(n workflow.Node) error {
	operation := sheetsLiteral(n.Parameters, "operation")
	if operation == SheetsOperationClear {
		return fmt.Errorf("operation clear is not offered to an AI Agent: it empties a whole tab or range in one call. " +
			"Use delete to remove rows of a size you set, or use the Google Sheets node as a step")
	}
	if operation == SheetsOperationDelete && sheetsLiteral(n.Parameters, "toDelete") == "columns" {
		return errSheetsToolDeletesColumns
	}
	if err := checkSheetsToolFilters(n.Parameters["filtersUI"]); err != nil {
		return err
	}
	if err := checkSheetsToolColumns(n.Parameters["columns"]); err != nil {
		return err
	}
	return refuseSheetsToolAutoMapping(operation, n.Parameters)
}

// errSheetsToolDeletesColumns: a start column the model chooses can be the one
// that holds the column names, and a deleted column takes its whole history
// with it.
var errSheetsToolDeletesColumns = fmt.Errorf("deleting columns (toDelete columns) is not offered to an AI Agent: a start " +
	"column the model chooses can reach the column that holds the names. A tool deletes rows; use the Google Sheets " +
	"node as a step to delete columns")

// plainSheetsMap is a value that is an object and not an expression marker.
func plainSheetsMap(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	if !ok || expression.IsExpression(object) {
		return nil, false
	}
	if mode, _ := object["mode"].(string); mode == "expression" {
		return nil, false
	}
	return object, true
}

// checkSheetsToolFilters keeps the shape of the filters the author's: the list,
// each entry and the column a filter looks in. Only a filter's value
// (lookupValue) is what the model supplies.
func checkSheetsToolFilters(raw any) error {
	if raw == nil {
		return nil
	}
	refuse := func(what string) error {
		return fmt.Errorf("filtersUI %s is the workflow author's, so it cannot call $fromAI, be an expression or hold "+
			"{{ }}. Only a filter's value (lookupValue) is the model's: write that as {{ $fromAI('key', 'what it is') }}", what)
	}
	filters, ok := plainSheetsMap(raw)
	if !ok {
		return refuse("itself")
	}
	values, present := filters["values"]
	if !present || values == nil {
		return nil
	}
	list, ok := values.([]any)
	if !ok {
		return refuse("values (the list of filters)")
	}
	for _, entry := range list {
		fields, ok := plainSheetsMap(entry)
		if !ok {
			return refuse("each filter")
		}
		for key, value := range fields {
			if key != "lookupValue" && authorChoiceIsOpen(value) {
				return refuse("the column to look in (lookupColumn) and every field of a filter but its value")
			}
		}
	}
	return nil
}

// checkSheetsToolColumns keeps the shape of the mapping the author's: its mode,
// the column that finds a row, the schema and the names of the columns written.
// Only the values beside each column name are the model's.
func checkSheetsToolColumns(raw any) error {
	if raw == nil {
		return nil
	}
	columns, ok := plainSheetsMap(raw)
	if !ok {
		return fmt.Errorf("columns decides which columns the tool writes and which one finds the row, so the workflow author " +
			"sets it: it must be the mapping itself, not an expression, text or a list. Map each column in the list " +
			"instead, using $fromAI for the values the model supplies")
	}
	for _, key := range []string{"mappingMode", "matchingColumns", "schema"} {
		if value, present := columns[key]; present && authorChoiceIsOpen(value) {
			return fmt.Errorf("columns.%s decides how the tool writes, so the workflow author sets it: "+
				"it cannot call $fromAI, be an expression or hold {{ }}", key)
		}
	}
	mapped, present := columns["value"]
	if !present || mapped == nil {
		return nil
	}
	values, ok := plainSheetsMap(mapped)
	if !ok {
		return fmt.Errorf("columns.value is the author's list of columns, so it cannot be an expression, text holding " +
			"{{ }} or a list: a model that chose it would choose which columns are written. Name each column, and put " +
			"$fromAI in the value beside it")
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.Contains(name, "{{") {
			return fmt.Errorf("the column name %q holds {{ }}. Column names are the author's: write the name itself, "+
				"and put the value, with $fromAI where the model supplies it, beside it", name)
		}
	}
	return nil
}

// refuseSheetsToolAutoMapping refuses automatic column mapping on a write made
// as an agent's tool. parameters are the tool's, or the resolved ones at run
// time.
func refuseSheetsToolAutoMapping(operation string, parameters map[string]any) error {
	switch operation {
	case SheetsOperationAppend, SheetsOperationAppendOrUpdate, SheetsOperationUpdate:
	default:
		return nil
	}
	if mapping, ok := property.ReadMapping(parameters["columns"]); ok && mapping.Mode == property.MappingAuto {
		return fmt.Errorf("as an agent's tool this node cannot map its columns automatically: the item it runs on " +
			"is the agent's input, not a row to write. Map each column in the list instead, using $fromAI for the " +
			"values the model supplies")
	}
	return nil
}

// refuseSheetsToolRun is the executor's own check of a tool call, made before
// any request leaves for Google. The agent has validated the form already; a
// descriptor is data, and the executor does not take the agent's word. It
// judges the authored parameters with the form's own rule, and the resolved
// ones for what an expression chose.
func refuseSheetsToolRun(operation string, authored map[string]any, resolved []workflow.IRNode) error {
	judged := make(map[string]any, len(authored)+1)
	for key, value := range authored {
		judged[key] = value
	}
	judged["operation"] = operation
	// The form's own rule, then the generic one every tool form has: the
	// author-only keys, and no $fromAI in plain text.
	form, _ := toolFormFor(GoogleSheetsToolNodeType)
	if err := checkToolFormParameters(form, judged); err != nil {
		return err
	}
	for _, node := range resolved {
		if err := refuseSheetsToolAutoMapping(operation, node.Parameters); err != nil {
			return err
		}
		if operation == SheetsOperationDelete && textValue(node.Parameters["toDelete"], "rows") == "columns" {
			return errSheetsToolDeletesColumns
		}
	}
	return nil
}
