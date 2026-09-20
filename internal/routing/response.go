package routing

import (
	"fmt"

	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// postProcess turns one decoded response into items.
//
// The actions run in the order the pack declared them, because they compose:
// `rootProperty` narrows to a list, `setKeyValue` reshapes each element,
// `limit` truncates. Reordering them changes the answer, so the order is the
// pack's to choose rather than this function's to normalise.
func postProcess(decoded any, actions []PostReceive, base expression.Context) ([]workflow.Item, error) {
	current := decoded
	for _, action := range actions {
		next, err := applyPostReceive(current, action, base)
		if err != nil {
			return nil, err
		}
		current = next
	}
	return itemsFrom(current), nil
}

func applyPostReceive(current any, action PostReceive, base expression.Context) (any, error) {
	switch action.Type {
	case PostReceiveRootProperty:
		path, _ := action.Properties["property"].(string)
		if path == "" {
			return nil, fmt.Errorf("postReceive rootProperty needs a property name")
		}
		extracted, found := readPath(current, path)
		if !found {
			// An absent root is an empty result, not a failure: an API that
			// answers `{"items": []}` on some calls and omits the key entirely
			// on others is ordinary, and failing on the second shape would make
			// the node work only for non-empty answers.
			return []any{}, nil
		}
		return extracted, nil

	case PostReceiveSetKeyValue:
		return mapEach(current, func(element any) (any, error) {
			fields := make(map[string]any, len(action.Properties))
			// Templates resolve with `$json` bound to the element being
			// rewritten, so a pack writes `{{ $json.id }}` here exactly as a
			// user would write it anywhere else.
			elementContext := base
			elementContext.JSON = asObject(element)
			for key, template := range action.Properties {
				value, err := templateEvaluator(elementContext)(template)
				if err != nil {
					return nil, fmt.Errorf("postReceive setKeyValue %q: %w", key, err)
				}
				fields[key] = value
			}
			return fields, nil
		})

	case PostReceiveLimit:
		maximum, ok := intOf(action.Properties["maxResults"])
		if !ok || maximum < 0 {
			return nil, fmt.Errorf("postReceive limit needs a non-negative maxResults")
		}
		list, isList := current.([]any)
		if !isList {
			return current, nil
		}
		if len(list) > maximum {
			return list[:maximum], nil
		}
		return list, nil

	case PostReceiveBinaryData:
		// Handled after the items exist, because it makes a second network
		// call and this function is otherwise pure.
		return current, nil

	default:
		// Unreachable: Registry.Register refuses an unknown action. Kept so
		// that adding a constant without adding a case fails here rather than
		// silently passing the response through untouched.
		return nil, fmt.Errorf("postReceive action %q is not implemented", action.Type)
	}
}

// itemsFrom is the one place a response becomes items.
//
// A list produces one item per element; anything else produces exactly one
// item. That is what makes the node's output count predictable from the
// response rather than from the shape of whatever the server happened to send.
func itemsFrom(value any) []workflow.Item {
	if list, ok := value.([]any); ok {
		items := make([]workflow.Item, 0, len(list))
		for _, element := range list {
			items = append(items, workflow.Item{JSON: asObject(element)})
		}
		return items
	}
	return []workflow.Item{{JSON: asObject(value)}}
}

// asObject wraps a non-object under `data`, so an item always has fields.
//
// An API that answers with a bare array of strings is real, and an item whose
// JSON is a string is not something an expression can read.
func asObject(value any) map[string]any {
	switch typed := value.(type) {
	case nil:
		return map[string]any{}
	case map[string]any:
		return typed
	default:
		return map[string]any{"data": typed}
	}
}

func mapEach(current any, transform func(any) (any, error)) (any, error) {
	list, ok := current.([]any)
	if !ok {
		return transform(current)
	}
	mapped := make([]any, 0, len(list))
	for _, element := range list {
		transformed, err := transform(element)
		if err != nil {
			return nil, err
		}
		mapped = append(mapped, transformed)
	}
	return mapped, nil
}

// readPath walks a dotted path into a decoded response.
func readPath(value any, path string) (any, bool) {
	current := value
	for _, segment := range splitPath(path) {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, present := object[segment]
		if !present {
			return nil, false
		}
		current = next
	}
	return current, true
}

func splitPath(path string) []string {
	segments := make([]string, 0, 4)
	start := 0
	for index := 0; index < len(path); index++ {
		if path[index] == '.' {
			segments = append(segments, path[start:index])
			start = index + 1
		}
	}
	return append(segments, path[start:])
}

func intOf(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}
