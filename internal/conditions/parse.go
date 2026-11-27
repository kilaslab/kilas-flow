package conditions

import "fmt"

// Parse reads a filter in n8n's rich shape: an object with an ordered
// `conditions` list of `{leftValue, operator: {type, operation}, rightValue}`,
// a `combinator` and an `options` bag.
//
// It reads the shape and nothing else. Left and right values arrive already
// resolved, so a caller that holds expression markers resolves them first; the
// IF, Filter and Switch nodes get that from the engine, and a trigger filter
// does it against the trigger item (see internal/triggerfilter).
//
// Case-sensitive matching is the default, because it is n8n's and because it
// is the one that does not quietly widen a match.
func Parse(declared map[string]any) (Filter, error) {
	filter := Filter{
		Combinator: Combinator(textOf(declared["combinator"])),
		Options:    Options{CaseSensitive: true},
	}
	if options, ok := declared["options"].(map[string]any); ok {
		if sensitive, present := options["caseSensitive"].(bool); present {
			filter.Options.CaseSensitive = sensitive
		}
		filter.Options.TypeValidation = Validation(textOf(options["typeValidation"]))
	}

	list, _ := declared["conditions"].([]any)
	for index, entry := range list {
		row, ok := entry.(map[string]any)
		if !ok {
			return Filter{}, fmt.Errorf("condition %d is not an object", index)
		}
		operator, ok := row["operator"].(map[string]any)
		if !ok {
			return Filter{}, fmt.Errorf("condition %d has no operator", index)
		}
		filter.Conditions = append(filter.Conditions, Condition{
			ID:         textOf(row["id"]),
			LeftValue:  row["leftValue"],
			RightValue: row["rightValue"],
			Operator: Operator{
				Type:        ValueType(textOf(operator["type"])),
				Operation:   textOf(operator["operation"]),
				SingleValue: operator["singleValue"] == true,
			},
		})
	}
	return filter, nil
}

func textOf(value any) string {
	text, _ := value.(string)
	return text
}
