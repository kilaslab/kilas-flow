package nodes

import (
	"fmt"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/conditions"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// readFilter reads a node's condition parameter in either shape.
//
// The rich shape is n8n's — an ordered list of
// `{leftValue, operator: {type, operation}, rightValue}` with a combinator and
// an options bag — and it is what an imported workflow carries and what a newly
// saved node writes. The flat shape is `[{field, operator, value}]`, which is
// what KilasFlow's IF stored when it accepted exactly one condition over four
// operators, and a workflow saved then still has to run.
//
// The two are told apart by shape: the rich one is an object with a
// `conditions` key, the flat one is a bare array. There is no version flag,
// because a flag is a thing every hand-authored document has to remember.
func readFilter(value any, item workflow.Item) (conditions.Filter, error) {
	switch typed := value.(type) {
	case map[string]any:
		return richFilter(typed)
	case []any:
		return flatFilter(typed, item)
	case nil:
		// No conditions is not an error: a half-built Filter lets everything
		// through rather than making the workflow unrunnable.
		return conditions.Filter{Options: conditions.Options{CaseSensitive: true}}, nil
	default:
		return conditions.Filter{}, fmt.Errorf("conditions must be a list or a filter object")
	}
}

func richFilter(declared map[string]any) (conditions.Filter, error) {
	filter := conditions.Filter{
		Combinator: conditions.Combinator(textOf(declared["combinator"])),
		// n8n's own default, and the one that does not quietly widen a match.
		Options: conditions.Options{CaseSensitive: true},
	}
	if options, ok := declared["options"].(map[string]any); ok {
		if sensitive, present := options["caseSensitive"].(bool); present {
			filter.Options.CaseSensitive = sensitive
		}
		filter.Options.TypeValidation = conditions.Validation(textOf(options["typeValidation"]))
	}

	list, _ := declared["conditions"].([]any)
	for index, entry := range list {
		row, ok := entry.(map[string]any)
		if !ok {
			return conditions.Filter{}, fmt.Errorf("condition %d is not an object", index)
		}
		operator, ok := row["operator"].(map[string]any)
		if !ok {
			return conditions.Filter{}, fmt.Errorf("condition %d has no operator", index)
		}
		filter.Conditions = append(filter.Conditions, conditions.Condition{
			ID:         textOf(row["id"]),
			LeftValue:  row["leftValue"],
			RightValue: row["rightValue"],
			Operator: conditions.Operator{
				Type:        conditions.ValueType(textOf(operator["type"])),
				Operation:   textOf(operator["operation"]),
				SingleValue: operator["singleValue"] == true,
			},
		})
	}
	return filter, nil
}

// flatFilter reads the shape IF stored before this family shared an evaluator.
//
// Its `field` is a dotted path into the item rather than a resolved value, so
// it is read here — the rich shape resolves its left value as an expression
// long before this function sees it.
func flatFilter(list []any, item workflow.Item) (conditions.Filter, error) {
	filter := conditions.Filter{
		Combinator: conditions.CombineAnd,
		Options:    conditions.Options{CaseSensitive: true},
	}
	for index, entry := range list {
		row, ok := entry.(map[string]any)
		if !ok {
			return conditions.Filter{}, fmt.Errorf("condition %d is not an object", index)
		}
		field := textOf(row["field"])
		operation := textOf(row["operator"])
		if field == "" || operation == "" {
			return conditions.Filter{}, fmt.Errorf("condition %d requires field and operator", index)
		}
		switch operation {
		case "equals", "notEquals":
			if _, found := row["value"]; !found {
				return conditions.Filter{}, fmt.Errorf("condition %q requires value", operation)
			}
		case "exists", "notExists":
		default:
			return conditions.Filter{}, fmt.Errorf("condition operator %q is unsupported", operation)
		}
		filter.Conditions = append(filter.Conditions, conditions.Condition{
			LeftValue:  itemPath(item.JSON, field),
			RightValue: row["value"],
			// The old shape had no declared type, and its comparison was
			// `reflect.DeepEqual`. `string` with loose validation is the
			// closest honest reading: it compares what a user sees.
			Operator: conditions.Operator{Type: conditions.TypeString, Operation: operation},
		})
	}
	if len(filter.Conditions) == 0 {
		return conditions.Filter{}, fmt.Errorf("conditions must contain at least one condition")
	}
	return filter, nil
}

// itemPath reads a dotted path out of an item, or nil.
func itemPath(item map[string]any, path string) any {
	current := any(item)
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		value, found := object[segment]
		if !found {
			return nil
		}
		current = value
	}
	return current
}

func textOf(value any) string {
	text, _ := value.(string)
	return text
}
