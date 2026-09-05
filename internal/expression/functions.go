package expression

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// dateValue is what `$now` and `$today` produce.
//
// A distinct type rather than a bare string, so a date function can refuse a
// receiver that is not a date instead of silently parsing whatever it was
// given. It stringifies as RFC 3339, which both formats readably and compares
// correctly against another timestamp in the same zone.
type dateValue struct{ at time.Time }

func (value dateValue) String() string { return value.at.Format(time.RFC3339) }

// argument is a literal supplied to a function call. Only literals are
// accepted: allowing a nested expression as an argument would turn the grammar
// into a general call expression, which is exactly what it must not become.
type argument struct {
	text     string
	number   float64
	isNumber bool
}

// function is one entry in the closed allowlist.
//
// Resolution happens at parse time, so an unknown name is a parse error rather
// than a runtime one — a workflow that names a function that does not exist
// fails at save, not on the first item that reaches it.
type function struct {
	name  string
	arity int
	apply func(receiver any, args []argument) (any, error)
}

// functions is the whole callable surface. Nothing here reaches the host, the
// filesystem, the network or another tenant's data: each one is a pure
// transformation of a value the expression already produced.
var functions = map[string]function{
	"toUpperCase": {name: "toUpperCase", arity: 0, apply: stringFunction(func(text string, _ []argument) (any, error) {
		return strings.ToUpper(text), nil
	})},
	"toLowerCase": {name: "toLowerCase", arity: 0, apply: stringFunction(func(text string, _ []argument) (any, error) {
		return strings.ToLower(text), nil
	})},
	"trim": {name: "trim", arity: 0, apply: stringFunction(func(text string, _ []argument) (any, error) {
		return strings.TrimSpace(text), nil
	})},
	"split": {name: "split", arity: 1, apply: stringFunction(func(text string, args []argument) (any, error) {
		parts := strings.Split(text, args[0].text)
		values := make([]any, len(parts))
		for index, part := range parts {
			values[index] = part
		}
		return values, nil
	})},
	"replace": {name: "replace", arity: 2, apply: stringFunction(func(text string, args []argument) (any, error) {
		return strings.ReplaceAll(text, args[0].text, args[1].text), nil
	})},
	"startsWith": {name: "startsWith", arity: 1, apply: stringFunction(func(text string, args []argument) (any, error) {
		return strings.HasPrefix(text, args[0].text), nil
	})},
	"endsWith": {name: "endsWith", arity: 1, apply: stringFunction(func(text string, args []argument) (any, error) {
		return strings.HasSuffix(text, args[0].text), nil
	})},
	"includes": {name: "includes", arity: 1, apply: func(receiver any, args []argument) (any, error) {
		switch typed := receiver.(type) {
		case string:
			return strings.Contains(typed, args[0].text), nil
		case []any:
			for _, entry := range typed {
				if stringify(entry) == args[0].text {
					return true, nil
				}
			}
			return false, nil
		default:
			return nil, fmt.Errorf("includes() needs a string or a list")
		}
	}},
	"length": {name: "length", arity: 0, apply: func(receiver any, _ []argument) (any, error) {
		switch typed := receiver.(type) {
		case string:
			return float64(len([]rune(typed))), nil
		case []any:
			return float64(len(typed)), nil
		case map[string]any:
			return float64(len(typed)), nil
		default:
			return nil, fmt.Errorf("length() needs a string, a list or an object")
		}
	}},
	"toString": {name: "toString", arity: 0, apply: func(receiver any, _ []argument) (any, error) {
		return stringify(receiver), nil
	}},
	"toNumber": {name: "toNumber", arity: 0, apply: func(receiver any, _ []argument) (any, error) {
		switch typed := receiver.(type) {
		case float64:
			return typed, nil
		case string:
			parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
			if err != nil {
				return nil, fmt.Errorf("toNumber() cannot read %q as a number", typed)
			}
			return parsed, nil
		default:
			return nil, fmt.Errorf("toNumber() needs a string or a number")
		}
	}},
	"join": {name: "join", arity: 1, apply: func(receiver any, args []argument) (any, error) {
		list, ok := receiver.([]any)
		if !ok {
			return nil, fmt.Errorf("join() needs a list")
		}
		parts := make([]string, len(list))
		for index, entry := range list {
			parts[index] = stringify(entry)
		}
		return strings.Join(parts, args[0].text), nil
	}},
	"first": {name: "first", arity: 0, apply: func(receiver any, _ []argument) (any, error) {
		list, ok := listFrom(receiver)
		if !ok {
			return nil, fmt.Errorf("first() needs a list or a node")
		}
		if len(list) == 0 {
			return Undefined, nil
		}
		return list[0], nil
	}},
	"last": {name: "last", arity: 0, apply: func(receiver any, _ []argument) (any, error) {
		list, ok := listFrom(receiver)
		if !ok {
			return nil, fmt.Errorf("last() needs a list or a node")
		}
		if len(list) == 0 {
			return Undefined, nil
		}
		return list[len(list)-1], nil
	}},
	"all": {name: "all", arity: 0, apply: func(receiver any, _ []argument) (any, error) {
		list, ok := listFrom(receiver)
		if !ok {
			return nil, fmt.Errorf("all() needs a list or a node")
		}
		return list, nil
	}},
	"format": {name: "format", arity: 1, apply: func(receiver any, args []argument) (any, error) {
		date, ok := receiver.(dateValue)
		if !ok {
			return nil, fmt.Errorf("format() needs a date such as $now")
		}
		return date.at.Format(args[0].text), nil
	}},
	"toISOString": {name: "toISOString", arity: 0, apply: func(receiver any, _ []argument) (any, error) {
		date, ok := receiver.(dateValue)
		if !ok {
			return nil, fmt.Errorf("toISOString() needs a date such as $now")
		}
		return date.at.UTC().Format(time.RFC3339), nil
	}},
	"plusDays":  {name: "plusDays", arity: 1, apply: dateShift(1)},
	"minusDays": {name: "minusDays", arity: 1, apply: dateShift(-1)},
}

func dateShift(sign int) func(any, []argument) (any, error) {
	return func(receiver any, args []argument) (any, error) {
		date, ok := receiver.(dateValue)
		if !ok {
			return nil, fmt.Errorf("this function needs a date such as $now")
		}
		if !args[0].isNumber {
			return nil, fmt.Errorf("a day count must be a number")
		}
		return dateValue{at: date.at.AddDate(0, 0, sign*int(args[0].number))}, nil
	}
}

func stringFunction(apply func(string, []argument) (any, error)) func(any, []argument) (any, error) {
	return func(receiver any, args []argument) (any, error) {
		text, ok := receiver.(string)
		if !ok {
			return nil, fmt.Errorf("this function needs a string")
		}
		return apply(text, args)
	}
}

// FunctionNames is the callable allowlist, served to the editor so the client
// does not keep a second copy that drifts from this one.
func FunctionNames() []string {
	names := make([]string, 0, len(functions))
	for name := range functions {
		names = append(names, name)
	}
	return sortedStrings(names)
}

// listFrom accepts either a plain list or a node.
//
// `$('Name').first()` reads a node while `$json.tags.first()` reads a list, and
// both are forms an imported workflow uses. A node carries its items under the
// key nodeItemsKey, so unwrapping it here keeps one function serving both
// rather than two spellings of the same idea.
func listFrom(receiver any) ([]any, bool) {
	if list, ok := receiver.([]any); ok {
		return list, true
	}
	if object, ok := receiver.(map[string]any); ok {
		if list, ok := object[nodeItemsKey].([]any); ok {
			return list, true
		}
	}
	return nil, false
}
