package conditions

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Combinator joins a filter's conditions.
type Combinator string

const (
	// CombineAnd requires every condition. It is the default: a filter with no
	// combinator that silently ORed would pass rows nobody asked for.
	CombineAnd Combinator = "and"
	CombineOr  Combinator = "or"
)

// Validation decides what happens when a value is not the operator's type.
type Validation string

const (
	// ValidationLoose converts before comparing, which is n8n's default and
	// what a webhook body — all strings — needs to be comparable at all.
	ValidationLoose Validation = "loose"
	// ValidationStrict refuses instead, naming the value that was wrong.
	ValidationStrict Validation = "strict"
)

// ValueType is what an operator compares.
type ValueType string

const (
	TypeString   ValueType = "string"
	TypeNumber   ValueType = "number"
	TypeBoolean  ValueType = "boolean"
	TypeDateTime ValueType = "dateTime"
	TypeArray    ValueType = "array"
	TypeObject   ValueType = "object"
)

// Operator names one comparison.
type Operator struct {
	Type      ValueType `json:"type"`
	Operation string    `json:"operation"`
	// SingleValue marks an operation that reads only the left value —
	// `exists`, `empty`, `true`. A right value is not required for one, and
	// demanding it would make every "is this set" condition unwritable.
	SingleValue bool `json:"singleValue,omitempty"`
}

// Condition is one comparison.
type Condition struct {
	ID         string   `json:"id,omitempty"`
	LeftValue  any      `json:"leftValue"`
	Operator   Operator `json:"operator"`
	RightValue any      `json:"rightValue,omitempty"`
}

// Options refine the whole filter.
type Options struct {
	// CaseSensitive is n8n's own default: true. A filter that lower-cased by
	// default would quietly match rows the author did not mean.
	CaseSensitive  bool       `json:"caseSensitive"`
	TypeValidation Validation `json:"typeValidation,omitempty"`
}

// Filter is an ordered list of conditions and how to join them.
type Filter struct {
	Conditions []Condition `json:"conditions"`
	Combinator Combinator  `json:"combinator,omitempty"`
	Options    Options     `json:"options"`
}

// Evaluate reports whether a filter passes.
//
// An empty filter passes. That is what "no conditions" means to every node in
// this family — a Filter with nothing configured lets everything through and a
// Switch rule with nothing configured matches — and refusing would make a
// half-built workflow unrunnable rather than permissive.
func Evaluate(filter Filter) (bool, error) {
	if len(filter.Conditions) == 0 {
		return true, nil
	}
	combinator := filter.Combinator
	if combinator == "" {
		combinator = CombineAnd
	}

	for _, condition := range filter.Conditions {
		matched, err := Match(condition, filter.Options)
		if err != nil {
			return false, err
		}
		if combinator == CombineOr && matched {
			return true, nil
		}
		if combinator == CombineAnd && !matched {
			return false, nil
		}
	}
	return combinator == CombineAnd, nil
}

// Match evaluates one condition.
func Match(condition Condition, options Options) (bool, error) {
	if options.TypeValidation == "" {
		options.TypeValidation = ValidationLoose
	}

	// `exists` and `notExists` are asked before any conversion: they are about
	// whether the value is there at all, and converting an absent value first
	// would answer a different question.
	present := condition.LeftValue != nil
	switch condition.Operator.Operation {
	case "exists":
		return present, nil
	case "notExists":
		return !present, nil
	}

	switch condition.Operator.Type {
	case TypeString:
		return matchString(condition, options)
	case TypeNumber:
		return matchNumber(condition, options, present)
	case TypeBoolean:
		return matchBoolean(condition, options, present)
	case TypeDateTime:
		return matchDateTime(condition, options, present)
	case TypeArray:
		return matchArray(condition, options)
	case TypeObject:
		return matchObject(condition)
	default:
		return false, fmt.Errorf("condition type %q is not supported", condition.Operator.Type)
	}
}

func matchString(condition Condition, options Options) (bool, error) {
	left, err := asString(condition.LeftValue, options, "left")
	if err != nil {
		return false, err
	}
	right, err := asString(condition.RightValue, options, "right")
	if err != nil {
		return false, err
	}
	if !options.CaseSensitive {
		left = strings.ToLower(left)
		right = strings.ToLower(right)
	}

	switch condition.Operator.Operation {
	case "empty":
		return left == "", nil
	case "notEmpty":
		return left != "", nil
	case "equals":
		return left == right, nil
	case "notEquals":
		return left != right, nil
	case "contains":
		return strings.Contains(left, right), nil
	case "notContains":
		return !strings.Contains(left, right), nil
	case "startsWith":
		return strings.HasPrefix(left, right), nil
	case "notStartsWith":
		return !strings.HasPrefix(left, right), nil
	case "endsWith":
		return strings.HasSuffix(left, right), nil
	case "notEndsWith":
		return !strings.HasSuffix(left, right), nil
	case "regex", "notRegex":
		// The pattern is lower-cased along with everything else when the
		// filter is case-insensitive, which would corrupt a character class —
		// so it is taken from the original value and the insensitivity is
		// expressed as a flag instead.
		pattern, _ := asString(condition.RightValue, options, "right")
		matched, err := matchRegex(pattern, left, options.CaseSensitive)
		if err != nil {
			return false, err
		}
		return matched == (condition.Operator.Operation == "regex"), nil
	default:
		return false, unsupported(condition)
	}
}

// matchRegex compiles and applies a pattern.
//
// Go's regexp is RE2: it runs in time linear in the input and cannot backtrack
// catastrophically, so a pattern taken from a workflow document is not a denial
// of service the way the same pattern would be in a backtracking engine. That
// is why this is implemented rather than refused.
//
// A `/pattern/flags` literal is accepted because that is how n8n's own filter
// writes one, and a workflow carrying one has to keep working.
func matchRegex(pattern, input string, caseSensitive bool) (bool, error) {
	source, flags := parseRegexLiteral(pattern)
	if !caseSensitive && !strings.Contains(flags, "i") {
		flags += "i"
	}
	if flags != "" {
		source = "(?" + flags + ")" + source
	}
	compiled, err := regexp.Compile(source)
	if err != nil {
		return false, fmt.Errorf("condition pattern %q is not a valid regular expression: %w", pattern, err)
	}
	return compiled.MatchString(input), nil
}

// parseRegexLiteral splits `/pattern/flags` into its halves, keeping only the
// flags Go's regexp understands — `g` and `y` are about iteration, which a
// match has no use for, and passing them through would fail to compile.
func parseRegexLiteral(pattern string) (source, flags string) {
	if len(pattern) < 2 || !strings.HasPrefix(pattern, "/") {
		return pattern, ""
	}
	end := strings.LastIndex(pattern, "/")
	if end <= 0 {
		return pattern, ""
	}
	source = pattern[1:end]
	for _, flag := range pattern[end+1:] {
		switch flag {
		case 'i', 's', 'm':
			flags += string(flag)
		}
	}
	return source, flags
}

func matchNumber(condition Condition, options Options, present bool) (bool, error) {
	switch condition.Operator.Operation {
	case "empty":
		return !present, nil
	case "notEmpty":
		return present, nil
	}
	left, err := asNumber(condition.LeftValue, options, "left")
	if err != nil {
		return false, err
	}
	right, err := asNumber(condition.RightValue, options, "right")
	if err != nil {
		return false, err
	}
	switch condition.Operator.Operation {
	case "equals":
		return left == right, nil
	case "notEquals":
		return left != right, nil
	case "gt":
		return left > right, nil
	case "lt":
		return left < right, nil
	case "gte":
		return left >= right, nil
	case "lte":
		return left <= right, nil
	default:
		return false, unsupported(condition)
	}
}

func matchBoolean(condition Condition, options Options, present bool) (bool, error) {
	switch condition.Operator.Operation {
	case "empty":
		return !present, nil
	case "notEmpty":
		return present, nil
	}
	left, err := asBoolean(condition.LeftValue, options, "left")
	if err != nil {
		return false, err
	}
	switch condition.Operator.Operation {
	case "true":
		return left, nil
	case "false":
		return !left, nil
	}
	right, err := asBoolean(condition.RightValue, options, "right")
	if err != nil {
		return false, err
	}
	switch condition.Operator.Operation {
	case "equals":
		return left == right, nil
	case "notEquals":
		return left != right, nil
	default:
		return false, unsupported(condition)
	}
}

func matchDateTime(condition Condition, options Options, present bool) (bool, error) {
	switch condition.Operator.Operation {
	case "empty":
		return !present, nil
	case "notEmpty":
		return present, nil
	}
	left, err := asTime(condition.LeftValue, options, "left")
	if err != nil {
		return false, err
	}
	right, err := asTime(condition.RightValue, options, "right")
	if err != nil {
		return false, err
	}
	switch condition.Operator.Operation {
	case "equals":
		return left.Equal(right), nil
	case "notEquals":
		return !left.Equal(right), nil
	case "after":
		return left.After(right), nil
	case "before":
		return left.Before(right), nil
	case "afterOrEquals":
		return !left.Before(right), nil
	case "beforeOrEquals":
		return !left.After(right), nil
	default:
		return false, unsupported(condition)
	}
}

func matchArray(condition Condition, options Options) (bool, error) {
	left, ok := condition.LeftValue.([]any)
	if !ok && condition.LeftValue != nil {
		if options.TypeValidation == ValidationStrict {
			return false, fmt.Errorf("condition left value is not an array")
		}
		// Loose: a value that is not a list is a list of nothing, which makes
		// `empty` true and `contains` false — both the honest answers.
		left = nil
	}

	switch condition.Operator.Operation {
	case "empty":
		return len(left) == 0, nil
	case "notEmpty":
		return len(left) != 0, nil
	case "contains", "notContains":
		found := containsValue(left, condition.RightValue, options.CaseSensitive)
		return found == (condition.Operator.Operation == "contains"), nil
	}

	length := float64(len(left))
	right, err := asNumber(condition.RightValue, options, "right")
	if err != nil {
		return false, err
	}
	switch condition.Operator.Operation {
	case "lengthEquals":
		return length == right, nil
	case "lengthNotEquals":
		return length != right, nil
	case "lengthGt":
		return length > right, nil
	case "lengthLt":
		return length < right, nil
	case "lengthGte":
		return length >= right, nil
	case "lengthLte":
		return length <= right, nil
	default:
		return false, unsupported(condition)
	}
}

func matchObject(condition Condition) (bool, error) {
	object, _ := condition.LeftValue.(map[string]any)
	switch condition.Operator.Operation {
	case "empty":
		return len(object) == 0, nil
	case "notEmpty":
		return len(object) != 0, nil
	default:
		return false, unsupported(condition)
	}
}

func containsValue(list []any, wanted any, caseSensitive bool) bool {
	text, isText := wanted.(string)
	for _, entry := range list {
		if isText && !caseSensitive {
			if candidate, ok := entry.(string); ok && strings.EqualFold(candidate, text) {
				return true
			}
			continue
		}
		if sameValue(entry, wanted) {
			return true
		}
	}
	return false
}

func sameValue(left, right any) bool {
	if left == nil || right == nil {
		return left == right
	}
	leftEncoded, leftErr := json.Marshal(left)
	rightEncoded, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return string(leftEncoded) == string(rightEncoded)
}

func unsupported(condition Condition) error {
	return fmt.Errorf("condition operation %q is not supported for a %s value",
		condition.Operator.Operation, condition.Operator.Type)
}

// --- conversions -------------------------------------------------------------
//
// Loose conversion is the whole reason a webhook workflow works: an inbound
// body is text, and a condition comparing one of its fields to a number has to
// mean what the author meant. Strict refuses and names the side that was wrong,
// which is the only useful thing to say about it.

func asString(value any, options Options, side string) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	}
	if options.TypeValidation == ValidationStrict {
		return "", fmt.Errorf("condition %s value is not text", side)
	}
	switch typed := value.(type) {
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), nil
	case bool:
		return strconv.FormatBool(typed), nil
	case json.Number:
		return typed.String(), nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("condition %s value cannot be read as text", side)
	}
	return string(encoded), nil
}

func asNumber(value any, options Options, side string) (float64, error) {
	switch typed := value.(type) {
	case float64:
		return typed, nil
	case int:
		return float64(typed), nil
	case json.Number:
		return typed.Float64()
	}
	if options.TypeValidation == ValidationStrict {
		return 0, fmt.Errorf("condition %s value is not a number", side)
	}
	switch typed := value.(type) {
	case nil:
		return 0, nil
	case string:
		number, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, fmt.Errorf("condition %s value %q is not a number", side, typed)
		}
		return number, nil
	case bool:
		if typed {
			return 1, nil
		}
		return 0, nil
	}
	return 0, fmt.Errorf("condition %s value is not a number", side)
}

func asBoolean(value any, options Options, side string) (bool, error) {
	if typed, ok := value.(bool); ok {
		return typed, nil
	}
	if options.TypeValidation == ValidationStrict {
		return false, fmt.Errorf("condition %s value is not a boolean", side)
	}
	switch typed := value.(type) {
	case nil:
		return false, nil
	case string:
		decided, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err != nil {
			return false, fmt.Errorf("condition %s value %q is not a boolean", side, typed)
		}
		return decided, nil
	case float64:
		return typed != 0, nil
	}
	return false, fmt.Errorf("condition %s value is not a boolean", side)
}

// timeLayouts are the forms a workflow actually carries.
//
// RFC 3339 first because that is what every API and every expression's
// `.toISOString()` produces; the date-only forms because a user types those.
var timeLayouts = []string{
	time.RFC3339Nano, time.RFC3339,
	"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02",
}

func asTime(value any, options Options, side string) (time.Time, error) {
	switch typed := value.(type) {
	case time.Time:
		return typed, nil
	case string:
		for _, layout := range timeLayouts {
			if parsed, err := time.Parse(layout, strings.TrimSpace(typed)); err == nil {
				return parsed.UTC(), nil
			}
		}
		return time.Time{}, fmt.Errorf("condition %s value %q is not a date", side, typed)
	}
	if options.TypeValidation == ValidationStrict {
		return time.Time{}, fmt.Errorf("condition %s value is not a date", side)
	}
	switch typed := value.(type) {
	case nil:
		return time.Time{}, nil
	case float64:
		// A number is milliseconds since the epoch, which is what every
		// JavaScript source in this ecosystem produces.
		return time.UnixMilli(int64(typed)).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("condition %s value is not a date", side)
}
