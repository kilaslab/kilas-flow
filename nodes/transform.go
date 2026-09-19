package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// The data-shaping family's server-owned bindings.
const (
	AggregateExecutorID        = "core.aggregate"
	SortExecutorID             = "core.sort"
	SplitOutExecutorID         = "core.splitOut"
	SummarizeExecutorID        = "core.summarize"
	RemoveDuplicatesExecutorID = "core.removeDuplicates"
)

// The data-shaping family's node types.
const (
	AggregateNodeType        = "kilasflow.aggregate"
	SortNodeType             = "kilasflow.sort"
	SplitOutNodeType         = "kilasflow.splitOut"
	SummarizeNodeType        = "kilasflow.summarize"
	RemoveDuplicatesNodeType = "kilasflow.removeDuplicates"
)

// --- Aggregate ---------------------------------------------------------------

// aggregateNode collapses a stream into one item.
//
// It is Split Out's inverse, and the two were written together so the round
// trip is testable in one fixture.
func aggregateNode() node.Definition {
	return node.Definition{
		Type:        AggregateNodeType,
		Version:     workflow.V(1),
		DisplayName: "Aggregate",
		Description: "Collapses many items into one, gathering a field or whole items into a list.",
		Category:    "Transform",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:archive"},
		IconColor:   "#0ea5e9",
		Subtitle:    "{{ $parameter.aggregate }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "aggregate", Label: "Aggregate", Kind: node.PropertyOptions, Default: "aggregateIndividualFields",
				Options: []node.PropertyOption{
					{Label: "Individual Fields", Value: "aggregateIndividualFields"},
					{Label: "All Item Data (into a single list)", Value: "aggregateAllItemData"},
				},
			},
			{
				Key: "fieldsToAggregate", Label: "Fields to Aggregate", Kind: node.PropertyString,
				Description: "Comma-separated field names. Each becomes a list on the single output item.",
				VisibleWhen: []node.VisibilityCondition{{Key: "aggregate", Equals: "aggregateIndividualFields"}},
			},
			{
				Key: "destinationFieldName", Label: "Put Output in Field", Kind: node.PropertyString, Default: "data",
				VisibleWhen: []node.VisibilityCondition{{Key: "aggregate", Equals: "aggregateAllItemData"}},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     AggregateExecutorID,
		Validate:       validateAggregateConfiguration,
	}
}

func validateAggregateConfiguration(n workflow.Node) error {
	switch mode := textOf(n.Parameters["aggregate"]); mode {
	case "", "aggregateIndividualFields":
		if len(splitFieldList(textOf(n.Parameters["fieldsToAggregate"]))) == 0 {
			return fmt.Errorf("aggregating individual fields needs at least one field name")
		}
	case "aggregateAllItemData":
	default:
		return fmt.Errorf("aggregate %q is not supported", mode)
	}
	return nil
}

func executeAggregate(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := input["main"]
	built := workflow.Item{JSON: map[string]any{}}

	if textOf(ir.Parameters["aggregate"]) == "aggregateAllItemData" {
		destination := defaultString(textOf(ir.Parameters["destinationFieldName"]), "data")
		gathered := make([]any, 0, len(items))
		for _, item := range items {
			gathered = append(gathered, cloneMap(item.JSON))
		}
		built.JSON[destination] = gathered
		return workflow.NodeOutput{{built}}, nil
	}

	for _, field := range splitFieldList(textOf(ir.Parameters["fieldsToAggregate"])) {
		gathered := make([]any, 0, len(items))
		for _, item := range items {
			// A field an item does not have contributes nothing rather than a
			// null: a list with holes in it is worse than a shorter list.
			if value := itemPath(item.JSON, field); value != nil {
				gathered = append(gathered, cloneValue(value))
			}
		}
		built.JSON[lastSegment(field)] = gathered
	}
	// One item out of many: the correspondence is genuinely gone, and the
	// runner's own rule says so when the counts differ.
	return workflow.NodeOutput{{built}}, nil
}

// --- Split Out ---------------------------------------------------------------

// splitOutNode turns a list on one item into many items.
func splitOutNode() node.Definition {
	return node.Definition{
		Type:        SplitOutNodeType,
		Version:     workflow.V(1),
		DisplayName: "Split Out",
		Description: "Turns a list field into one item per element.",
		Category:    "Transform",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:git-branch"},
		IconColor:   "#0ea5e9",
		Subtitle:    "{{ $parameter.fieldToSplitOut }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "fieldToSplitOut", Label: "Fields to Split Out", Kind: node.PropertyString, Required: true,
				Description: "The list field to split. Dot notation reaches into nested objects.",
			},
			{
				Key: "include", Label: "Include", Kind: node.PropertyOptions, Default: "noOtherFields",
				Options: []node.PropertyOption{
					{Label: "No Other Fields", Value: "noOtherFields"},
					{Label: "All Other Fields", Value: "allOtherFields"},
					{Label: "Selected Other Fields", Value: "selectedOtherFields"},
				},
				Description: "Which of the source item's other fields each split item carries.",
			},
			{
				Key: "fieldsToInclude", Label: "Fields to Include", Kind: node.PropertyString,
				VisibleWhen: []node.VisibilityCondition{{Key: "include", Equals: "selectedOtherFields"}},
			},
			{
				Key: "destinationFieldName", Label: "Destination Field Name", Kind: node.PropertyString,
				Description: "Where a non-object element lands on the output item. Empty uses the source field's own name.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     SplitOutExecutorID,
		Validate:       validateSplitOutConfiguration,
	}
}

func validateSplitOutConfiguration(n workflow.Node) error {
	if len(splitFieldList(textOf(n.Parameters["fieldToSplitOut"]))) == 0 {
		return fmt.Errorf("a split needs at least one field name")
	}
	return nil
}

func executeSplitOut(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fields := splitFieldList(textOf(ir.Parameters["fieldToSplitOut"]))
	if len(fields) == 0 {
		return nil, fmt.Errorf("node %q: a split needs at least one field name", ir.Name)
	}
	destination := textOf(ir.Parameters["destinationFieldName"])

	items := []workflow.Item{}
	for index, item := range input["main"] {
		carried := splitOutCarried(ir.Parameters, item, fields)
		// n8n splits on the first field and carries the rest as context; a
		// second listed field is read from the same item rather than crossed
		// with the first, which is what "fields to split out" means there.
		primary := fields[0]
		elements := splitOutElements(item.JSON, primary)
		for _, element := range elements {
			built := workflow.Item{JSON: map[string]any{}}
			for key, value := range carried {
				built.JSON[key] = cloneValue(value)
			}
			if object, ok := element.(map[string]any); ok && destination == "" {
				for key, value := range object {
					built.JSON[key] = cloneValue(value)
				}
			} else {
				built.JSON[defaultString(destination, lastSegment(primary))] = cloneValue(element)
			}
			// Every split item descends from the item that carried the list,
			// which the runner cannot infer because the count changed.
			built.Paired = &workflow.PairedItem{SourceNodeID: ir.ID, SourcePort: "main", ItemIndex: index}
			if item.Paired != nil && !item.Paired.Lost {
				inherited := *item.Paired
				built.Paired = &inherited
			}
			items = append(items, built)
		}
	}
	return workflow.NodeOutput{items}, nil
}

// splitOutElements reads the list a field holds.
//
// A field that is not a list is one element, not an error: an API that returns
// a single object where it usually returns an array is ordinary, and failing
// there would break the workflow on the one-result case.
func splitOutElements(item map[string]any, field string) []any {
	value := itemPath(item, field)
	switch typed := value.(type) {
	case []any:
		return typed
	case nil:
		return nil
	default:
		return []any{typed}
	}
}

func splitOutCarried(parameters map[string]any, item workflow.Item, splitting []string) map[string]any {
	switch textOf(parameters["include"]) {
	case "allOtherFields":
		carried := make(map[string]any, len(item.JSON))
		dropped := map[string]bool{}
		for _, field := range splitting {
			dropped[lastSegment(field)] = true
		}
		for key, value := range item.JSON {
			if !dropped[key] {
				carried[key] = value
			}
		}
		return carried
	case "selectedOtherFields":
		wanted := splitFieldList(textOf(parameters["fieldsToInclude"]))
		carried := make(map[string]any, len(wanted))
		for _, name := range wanted {
			if value := itemPath(item.JSON, name); value != nil {
				carried[lastSegment(name)] = value
			}
		}
		return carried
	default:
		return map[string]any{}
	}
}

// --- Sort ---------------------------------------------------------------------

func sortNode() node.Definition {
	return node.Definition{
		Type:        SortNodeType,
		Version:     workflow.V(1),
		DisplayName: "Sort",
		Description: "Reorders items by one or more fields, or shuffles them.",
		Category:    "Transform",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:arrow-down-up"},
		IconColor:   "#0ea5e9",
		Subtitle:    "{{ $parameter.type }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "type", Label: "Sort Type", Kind: node.PropertyOptions, Default: "simple",
				Options: []node.PropertyOption{
					{Label: "Simple", Value: "simple"},
					{Label: "Random", Value: "random"},
				},
				Description: "n8n also offers a JavaScript comparator. KilasFlow does not run JavaScript, so that mode is refused at import rather than approximated.",
			},
			{
				Key: "sortFieldsUI", Label: "Fields to Sort By", Kind: node.PropertyString,
				Description: "Comma-separated field names, each optionally suffixed with `:desc`.",
				VisibleWhen: []node.VisibilityCondition{{Key: "type", Equals: "simple"}},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     SortExecutorID,
		Validate:       validateSortConfiguration,
	}
}

func validateSortConfiguration(n workflow.Node) error {
	switch mode := textOf(n.Parameters["type"]); mode {
	case "", "simple":
		if len(splitFieldList(textOf(n.Parameters["sortFieldsUI"]))) == 0 {
			return fmt.Errorf("a simple sort needs at least one field name")
		}
	case "random":
	case "code":
		return fmt.Errorf("sorting with a JavaScript comparator is not supported; use a Code node or sort by fields")
	default:
		return fmt.Errorf("sort type %q is not supported", mode)
	}
	return nil
}

func executeSort(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := cloneItems(input["main"])
	if textOf(ir.Parameters["type"]) == "random" {
		rand.Shuffle(len(items), func(left, right int) { items[left], items[right] = items[right], items[left] })
		return workflow.NodeOutput{items}, nil
	}

	keys := splitFieldList(textOf(ir.Parameters["sortFieldsUI"]))
	if len(keys) == 0 {
		return nil, fmt.Errorf("node %q: a simple sort needs at least one field name", ir.Name)
	}
	// SliceStable, so items the sort cannot tell apart keep the order they
	// arrived in — an unstable sort makes a workflow's output vary run to run
	// for no reason a reader can see.
	sort.SliceStable(items, func(left, right int) bool {
		for _, key := range keys {
			field, descending := sortDirection(key)
			order := compareValues(itemPath(items[left].JSON, field), itemPath(items[right].JSON, field))
			if order == 0 {
				continue
			}
			if descending {
				return order > 0
			}
			return order < 0
		}
		return false
	})
	return workflow.NodeOutput{items}, nil
}

func sortDirection(key string) (field string, descending bool) {
	if name, suffix, found := strings.Cut(key, ":"); found {
		return strings.TrimSpace(name), strings.EqualFold(strings.TrimSpace(suffix), "desc")
	}
	return key, false
}

// compareValues orders two field values.
//
// Numbers compare as numbers and everything else as its text, which is what a
// user sorting a mixed column means — and comparing a number's text would put
// 10 before 9.
func compareValues(left, right any) int {
	leftNumber, leftIsNumber := numericValue(left)
	rightNumber, rightIsNumber := numericValue(right)
	if leftIsNumber && rightIsNumber {
		switch {
		case leftNumber < rightNumber:
			return -1
		case leftNumber > rightNumber:
			return 1
		default:
			return 0
		}
	}
	return strings.Compare(textValue(left, ""), textValue(right, ""))
}

func numericValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	default:
		return 0, false
	}
}

// --- Summarize ----------------------------------------------------------------

// summarizeAggregations are the operations one summary column may use.
var summarizeAggregations = []node.PropertyOption{
	{Label: "Append", Value: "append"},
	{Label: "Average", Value: "average"},
	{Label: "Concatenate", Value: "concatenate"},
	{Label: "Count", Value: "count"},
	{Label: "Count Unique", Value: "countUnique"},
	{Label: "Max", Value: "max"},
	{Label: "Min", Value: "min"},
	{Label: "Sum", Value: "sum"},
}

func summarizeNode() node.Definition {
	return node.Definition{
		Type:        SummarizeNodeType,
		Version:     workflow.V(1),
		DisplayName: "Summarize",
		Description: "Groups items and reduces each group to one row, like a pivot table.",
		Category:    "Transform",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:archive"},
		IconColor:   "#0ea5e9",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "fieldsToSummarize", Label: "Fields to Summarize", Kind: node.PropertyJSON, Required: true,
				Description: "A list of `{aggregation, field, includeEmpty}` rows, one per summary column.",
			},
			{
				Key: "fieldsToSplitBy", Label: "Fields to Split By", Kind: node.PropertyString,
				Description: "Comma-separated field names to group on. Empty summarises everything into one row.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     SummarizeExecutorID,
		Validate:       validateSummarizeConfiguration,
	}
}

// summarizeColumn is one summary column.
type summarizeColumn struct {
	aggregation string
	field       string
}

// summarizeOutputName is n8n's output key for one aggregation: count_ and
// sum_ are bare, unique counts and concatenations carry n8n's own prefixes.
func summarizeOutputName(aggregation, field string) string {
	suffix := lastSegment(field)
	switch aggregation {
	case "countUnique":
		return "unique_count_" + suffix
	case "concatenate":
		return "concatenated_" + suffix
	case "append":
		return "appended_" + suffix
	default:
		return aggregation + "_" + suffix
	}
}

func summarizeColumns(parameters map[string]any) ([]summarizeColumn, error) {
	list, ok := parameters["fieldsToSummarize"].([]any)
	if !ok {
		// n8n nests them under `values`; both forms are read so an import needs
		// no rewriting.
		if wrapper, wrapped := parameters["fieldsToSummarize"].(map[string]any); wrapped {
			list, ok = wrapper["values"].([]any)
		}
		if !ok {
			return nil, fmt.Errorf("fields to summarize must be a list")
		}
	}
	columns := make([]summarizeColumn, 0, len(list))
	for index, entry := range list {
		declared, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("summary column %d is not an object", index)
		}
		column := summarizeColumn{
			aggregation: defaultString(textOf(declared["aggregation"]), "count"),
			field:       textOf(declared["field"]),
		}
		if !knownAggregation(column.aggregation) {
			return nil, fmt.Errorf("aggregation %q is not supported", column.aggregation)
		}
		if column.field == "" {
			return nil, fmt.Errorf("summary column %d names no field", index)
		}
		columns = append(columns, column)
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("a summary needs at least one column")
	}
	return columns, nil
}

func knownAggregation(name string) bool {
	for _, option := range summarizeAggregations {
		if option.Value == name {
			return true
		}
	}
	return false
}

func validateSummarizeConfiguration(n workflow.Node) error {
	_, err := summarizeColumns(n.Parameters)
	return err
}

func executeSummarize(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	columns, err := summarizeColumns(ir.Parameters)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	splitBy := splitFieldList(textOf(ir.Parameters["fieldsToSplitBy"]))

	// Groups are kept in first-seen order rather than sorted, so the output is
	// deterministic and reads the way the input did.
	order := make([]string, 0, 8)
	groups := map[string][]workflow.Item{}
	keys := map[string]map[string]any{}
	for _, item := range input["main"] {
		key, values := summarizeKey(item, splitBy)
		if _, seen := groups[key]; !seen {
			order = append(order, key)
			keys[key] = values
		}
		groups[key] = append(groups[key], item)
	}

	items := make([]workflow.Item, 0, len(order))
	for _, key := range order {
		built := workflow.Item{JSON: map[string]any{}}
		for field, value := range keys[key] {
			built.JSON[field] = cloneValue(value)
		}
		for _, column := range columns {
			built.JSON[summarizeOutputName(column.aggregation, column.field)] = summarize(ir.Parameters, column, groups[key])
		}
		items = append(items, built)
	}
	return workflow.NodeOutput{items}, nil
}

func summarizeKey(item workflow.Item, splitBy []string) (string, map[string]any) {
	if len(splitBy) == 0 {
		return "", map[string]any{}
	}
	values := make(map[string]any, len(splitBy))
	parts := make([]string, 0, len(splitBy))
	for _, field := range splitBy {
		value := itemPath(item.JSON, field)
		values[lastSegment(field)] = value
		encoded, _ := json.Marshal(value)
		parts = append(parts, string(encoded))
	}
	return strings.Join(parts, "\x00"), values
}

func summarize(parameters map[string]any, column summarizeColumn, items []workflow.Item) any {
	switch column.aggregation {
	case "count":
		count := 0
		for _, item := range items {
			if itemPath(item.JSON, column.field) != nil {
				count++
			}
		}
		return float64(count)
	case "countUnique":
		seen := map[string]bool{}
		for _, item := range items {
			value := itemPath(item.JSON, column.field)
			if value == nil {
				continue
			}
			encoded, _ := json.Marshal(value)
			seen[string(encoded)] = true
		}
		return float64(len(seen))
	case "sum", "average":
		total, counted := 0.0, 0
		for _, item := range items {
			if number, ok := numericValue(itemPath(item.JSON, column.field)); ok {
				total += number
				counted++
			}
		}
		if column.aggregation == "sum" {
			return total
		}
		if counted == 0 {
			// No numbers to average is not zero: zero is an answer, and this
			// is the absence of one.
			return nil
		}
		return total / float64(counted)
	case "min", "max":
		var best any
		for _, item := range items {
			value := itemPath(item.JSON, column.field)
			if value == nil {
				continue
			}
			if best == nil {
				best = value
				continue
			}
			order := compareValues(value, best)
			if (column.aggregation == "min" && order < 0) || (column.aggregation == "max" && order > 0) {
				best = value
			}
		}
		return best
	case "concatenate":
		// n8n's default separator is a bare comma, and empty values are
		// skipped rather than joined as empty segments.
		separator := textOf(parameters["separator"])
		if separator == "" {
			separator = ","
		}
		parts := make([]string, 0, len(items))
		for _, item := range items {
			if value := itemPath(item.JSON, column.field); value != nil && textValue(value, "") != "" {
				parts = append(parts, textValue(value, ""))
			}
		}
		return strings.Join(parts, separator)
	default: // append
		gathered := make([]any, 0, len(items))
		for _, item := range items {
			if value := itemPath(item.JSON, column.field); value != nil && textValue(value, "") != "" {
				gathered = append(gathered, cloneValue(value))
			}
		}
		return gathered
	}
}

// --- Remove Duplicates ---------------------------------------------------------

func removeDuplicatesNode() node.Definition {
	return node.Definition{
		Type:        RemoveDuplicatesNodeType,
		Version:     workflow.V(1),
		DisplayName: "Remove Duplicates",
		Description: "Drops items that repeat within this run.",
		Category:    "Transform",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:archive"},
		IconColor:   "#0ea5e9",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Default: "removeDuplicateInputItems",
				Options: []node.PropertyOption{
					{Label: "Remove Items Repeated Within Current Input", Value: "removeDuplicateInputItems"},
				},
				Description: "n8n can also remove items seen in *previous* executions, which needs durable per-workflow state this deployment does not have yet.",
			},
			{
				Key: "compare", Label: "Compare", Kind: node.PropertyOptions, Default: "allFields",
				Options: []node.PropertyOption{
					{Label: "All Fields", Value: "allFields"},
					{Label: "All Fields Except", Value: "allFieldsExcept"},
					{Label: "Selected Fields", Value: "selectedFields"},
				},
			},
			{
				Key: "fieldsToExclude", Label: "Fields to Exclude", Kind: node.PropertyString,
				VisibleWhen: []node.VisibilityCondition{{Key: "compare", Equals: "allFieldsExcept"}},
			},
			{
				Key: "fieldsToCompare", Label: "Fields to Compare", Kind: node.PropertyString,
				VisibleWhen: []node.VisibilityCondition{{Key: "compare", Equals: "selectedFields"}},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     RemoveDuplicatesExecutorID,
		Validate:       validateRemoveDuplicatesConfiguration,
	}
}

func validateRemoveDuplicatesConfiguration(n workflow.Node) error {
	switch operation := textOf(n.Parameters["operation"]); operation {
	case "", "removeDuplicateInputItems":
	case "removeItemsSeenInPreviousExecutions":
		return fmt.Errorf("removing items seen in previous executions needs durable per-workflow state this deployment does not have")
	default:
		return fmt.Errorf("operation %q is not supported", operation)
	}
	switch compare := textOf(n.Parameters["compare"]); compare {
	case "", "allFields", "allFieldsExcept", "selectedFields":
		return nil
	default:
		return fmt.Errorf("compare %q is not supported", compare)
	}
}

func executeRemoveDuplicates(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateRemoveDuplicatesConfiguration(workflow.Node{Parameters: ir.Parameters}); err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}

	seen := map[string]bool{}
	kept := []workflow.Item{}
	for _, item := range input["main"] {
		key, err := duplicateKey(ir.Parameters, item)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		kept = append(kept, cloneItem(item))
	}
	return workflow.NodeOutput{kept}, nil
}

// duplicateKey renders the part of an item two rows must share to be the same.
func duplicateKey(parameters map[string]any, item workflow.Item) (string, error) {
	compared := map[string]any{}
	switch textOf(parameters["compare"]) {
	case "allFieldsExcept":
		dropped := map[string]bool{}
		for _, name := range splitFieldList(textOf(parameters["fieldsToExclude"])) {
			dropped[name] = true
		}
		for key, value := range item.JSON {
			if !dropped[key] {
				compared[key] = value
			}
		}
	case "selectedFields":
		for _, name := range splitFieldList(textOf(parameters["fieldsToCompare"])) {
			compared[name] = itemPath(item.JSON, name)
		}
	default:
		compared = item.JSON
	}
	// Marshalling sorts object keys, so two items with the same fields in a
	// different order render the same — which is what "the same item" means.
	encoded, err := json.Marshal(compared)
	if err != nil {
		return "", fmt.Errorf("an item could not be compared: %w", err)
	}
	return string(encoded), nil
}

// lastSegment is the name a dotted path ends in, which is what a produced field
// is called.
func lastSegment(path string) string {
	if index := strings.LastIndex(path, "."); index >= 0 {
		return path[index+1:]
	}
	return path
}

// defaultString is the value, or the fallback when it is empty.
func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
