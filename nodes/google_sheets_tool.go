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
			"Use delete to remove rows or columns of a size you set, or use the Google Sheets node as a step")
	}
	if err := checkSheetsToolFilters(n.Parameters["filtersUI"]); err != nil {
		return err
	}
	if err := checkSheetsToolColumns(n.Parameters["columns"]); err != nil {
		return err
	}
	return refuseSheetsToolAutoMapping(operation, n.Parameters)
}

// checkSheetsToolFilters keeps the column a filter looks in the author's. Its
// value is what the model supplies.
func checkSheetsToolFilters(raw any) error {
	filters, _ := sheetsMap(raw)["values"].([]any)
	for _, entry := range filters {
		if column := sheetsMap(entry)["lookupColumn"]; authorChoiceIsOpen(column) {
			return fmt.Errorf("filtersUI names the column to look in (lookupColumn), so the workflow author sets it: " +
				"it cannot call $fromAI, be an expression or hold {{ }}. The model supplies the value to look for, " +
				"so write that as {{ $fromAI('key', 'what it is') }}")
		}
	}
	return nil
}

// checkSheetsToolColumns keeps the shape of the mapping the author's: its mode,
// the column that finds a row and the names of the columns written. Only the
// values are the model's.
func checkSheetsToolColumns(raw any) error {
	if raw == nil {
		return nil
	}
	if expression.IsExpression(raw) {
		return fmt.Errorf("columns decides which columns the tool writes and which one finds the row, so the workflow author " +
			"sets it: it cannot call $fromAI or be an expression. Map each column in the list instead, using $fromAI " +
			"for the values the model supplies")
	}
	columns, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{"mappingMode", "matchingColumns"} {
		if value, present := columns[key]; present && authorChoiceIsOpen(value) {
			return fmt.Errorf("columns.%s decides how the tool writes, so the workflow author sets it: "+
				"it cannot call $fromAI, be an expression or hold {{ }}", key)
		}
	}
	names := make([]string, 0)
	for name := range sheetsMap(columns["value"]) {
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
// descriptor is data, and the executor does not take the agent's word.
func refuseSheetsToolRun(operation string, resolved []workflow.IRNode) error {
	if operation == SheetsOperationClear {
		return fmt.Errorf("operation clear is not offered to an AI Agent")
	}
	for _, node := range resolved {
		if err := refuseSheetsToolAutoMapping(operation, node.Parameters); err != nil {
			return err
		}
	}
	return nil
}
