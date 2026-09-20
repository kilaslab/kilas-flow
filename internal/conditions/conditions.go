package conditions

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kilaslab/kilas-flow/internal/expression"
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
	// would answer a different question. Presence is n8n's own test — null and
	// undefined are both absent, and so is NaN, which is in the item but is not
	// a value a comparison can use.
	present := !absent(condition.LeftValue) && !notANumber(condition.LeftValue)
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

// --- absence -----------------------------------------------------------------
//
// n8n hands a null or an undefined to a comparison without converting it, and
// calls both of them "no value" for `exists` and `empty`. They are still not
// the same thing: JavaScript coerces null to zero in a relational comparison
// and undefined to NaN, so `field <= 0` is true of a null field and false of a
// field that is not there at all. The expression engine keeps that distinction
// (an absent path is its Undefined sentinel, never nil), and collapsing the two
// here would answer a different question than the workflow asked.

// absence is which of the three states a value is in.
type absence int

const (
	// kindValue is a value that is there to compare.
	kindValue absence = iota
	// kindNull is JSON null.
	kindNull
	// kindUndefined is the expression engine's sentinel for a path that does
	// not exist.
	kindUndefined
)

func absenceOf(value any) absence {
	switch {
	case value == nil:
		return kindNull
	case expression.IsUndefined(value):
		return kindUndefined
	default:
		return kindValue
	}
}

// absent reports n8n's "no value": null and undefined both count.
func absent(value any) bool {
	return absenceOf(value) != kindValue
}

// notANumber reports a value n8n's `exists` refuses. NaN arrives from an
// expression (`{{ 0 / 0 }}`) and is in the item, but no comparison can use it.
func notANumber(value any) bool {
	switch typed := value.(type) {
	case float64:
		return math.IsNaN(typed)
	case float32:
		return math.IsNaN(float64(typed))
	default:
		return false
	}
}

func matchNumber(condition Condition, options Options, present bool) (bool, error) {
	operation := numberOperation(condition.Operator.Operation)
	switch operation {
	case "empty":
		return !present, nil
	case "notEmpty":
		return present, nil
	}
	left, leftKind, err := numberOperand(condition.LeftValue, options, "left")
	if err != nil {
		return false, err
	}
	right, rightKind, err := numberOperand(condition.RightValue, options, "right")
	if err != nil {
		return false, err
	}

	if operation == "equals" || operation == "notEquals" {
		// JavaScript's `===`: a null equals only a null, an undefined only an
		// undefined, and a value only an equal value. That is why a comparison
		// against a field the item does not have is false whichever way round
		// it is written, rather than an error that stops the run.
		equal := false
		switch {
		case leftKind == kindValue && rightKind == kindValue:
			equal = left == right
		case leftKind == kindNull && rightKind == kindNull:
			equal = true
		case leftKind == kindUndefined && rightKind == kindUndefined:
			equal = true
		}
		return equal == (operation == "equals"), nil
	}

	// A relational comparison: null counts as zero, undefined satisfies
	// nothing.
	if leftKind == kindUndefined || rightKind == kindUndefined {
		return false, nil
	}
	if leftKind == kindNull {
		left = 0
	}
	if rightKind == kindNull {
		right = 0
	}
	switch operation {
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

// numberOperation folds the two spellings of the same four comparisons.
//
// n8n's own filter language uses gt, gte, lt and lte, and its v1 If used
// larger, largerEqual, smaller and smallerEqual for exactly those. Both reach
// this evaluator — an imported v1 IF or a v1 Switch rule carries the v1 names,
// and KilasFlow's own condition editor offers them too, while an imported v2
// filter carries n8n's — so a document can hold either spelling and has to run
// as written. The v1 names are also the ones already saved in existing
// documents, which is why this is a translation at the comparison rather than a
// rewrite of the document: an operation name this evaluator refused used to fail
// the run outright, which is a workflow that cannot run at all rather than one
// that takes the wrong branch.
func numberOperation(operation string) string {
	switch operation {
	case "larger":
		return "gt"
	case "largerEqual":
		return "gte"
	case "smaller":
		return "lt"
	case "smallerEqual":
		return "lte"
	default:
		return operation
	}
}

// numberOperand reads one side of a number comparison. An absent value is not
// an error — n8n passes it through and lets the comparison decide.
func numberOperand(value any, options Options, side string) (float64, absence, error) {
	if kind := absenceOf(value); kind != kindValue {
		return 0, kind, nil
	}
	number, err := asNumber(value, options, side)
	if err != nil {
		return 0, kindValue, err
	}
	return number, kindValue, nil
}

func matchBoolean(condition Condition, options Options, present bool) (bool, error) {
	operation := condition.Operator.Operation
	switch operation {
	case "empty":
		return !present, nil
	case "notEmpty":
		return present, nil
	}
	left, leftKind, err := booleanOperand(condition.LeftValue, options, "left")
	if err != nil {
		return false, err
	}
	switch operation {
	case "true":
		// A null or an undefined left value is falsy in n8n's own code path,
		// so `is true` routes those items to the false branch rather than
		// failing the run.
		return leftKind == kindValue && left, nil
	case "false":
		return leftKind != kindValue || !left, nil
	}
	right, rightKind, err := booleanOperand(condition.RightValue, options, "right")
	if err != nil {
		return false, err
	}
	equal := leftKind == rightKind && (leftKind != kindValue || left == right)
	switch operation {
	case "equals":
		return equal, nil
	case "notEquals":
		return !equal, nil
	default:
		return false, unsupported(condition)
	}
}

// booleanOperand reads one side of a boolean comparison, absent values
// included.
func booleanOperand(value any, options Options, side string) (bool, absence, error) {
	if kind := absenceOf(value); kind != kindValue {
		return false, kind, nil
	}
	decided, err := asBoolean(value, options, side)
	if err != nil {
		return false, kindValue, err
	}
	return decided, kindValue, nil
}

func matchDateTime(condition Condition, options Options, present bool) (bool, error) {
	switch condition.Operator.Operation {
	case "empty":
		return !present, nil
	case "notEmpty":
		return present, nil
	}
	// Both sides are read first, so a value that is neither a date nor empty
	// still names itself instead of being hidden by the other side's absence.
	left, err := asTime(condition.LeftValue, options, "left")
	if err != nil {
		return false, err
	}
	right, err := asTime(condition.RightValue, options, "right")
	if err != nil {
		return false, err
	}
	// n8n refuses to compare a date it does not have.
	if absent(condition.LeftValue) || absent(condition.RightValue) {
		return false, nil
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
	left, err := asArray(condition.LeftValue, options)
	if err != nil {
		return false, err
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

// asArray reads the left value as a list.
//
// A string that carries a JSON list is a list: n8n parses it, and a webhook
// body or a sheet column that holds "[1,2]" means the two items, not the text.
// Anything else keeps the reading this family already had — not a list is a
// list of nothing, which makes `empty` true and `contains` false, both honest
// answers, where n8n refuses the comparison outright. Strict refuses anything
// but a list, exactly as it did before.
func asArray(value any, options Options) ([]any, error) {
	if list, ok := value.([]any); ok {
		return list, nil
	}
	if absent(value) {
		return nil, nil
	}
	if options.TypeValidation == ValidationStrict {
		return nil, fmt.Errorf("condition left value is not an array")
	}
	if text, ok := value.(string); ok {
		if list, parsed := parseArrayText(text); parsed {
			return list, nil
		}
	}
	return nil, nil
}

// parseArrayText reads text that carries a JSON list, the way n8n does: JSON
// first, then the same text with single quotes swapped for double ones, which
// is how a list copied out of JavaScript gets written by hand.
func parseArrayText(text string) ([]any, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "[") {
		return nil, false
	}
	for _, candidate := range []string{trimmed, strings.ReplaceAll(trimmed, "'", `"`)} {
		var list []any
		if err := json.Unmarshal([]byte(candidate), &list); err == nil {
			return list, true
		}
	}
	return nil, false
}

func matchObject(condition Condition) (bool, error) {
	object := asObject(condition.LeftValue)
	switch condition.Operator.Operation {
	case "empty":
		return len(object) == 0, nil
	case "notEmpty":
		return len(object) != 0, nil
	default:
		return false, unsupported(condition)
	}
}

// asObject reads a value as an object, decoding text that carries JSON — n8n
// parses that too, and `{"a":1}` in a webhook body is an object with one field
// rather than a value that is not an object. It does not decode a
// JavaScript-shaped literal (`{a: 1}`), which n8n's own parser accepts: a
// guess about a value that will not parse is worse than no guess, and the
// literal is rare beside JSON. A value that is not an object is read as an
// empty one, which is this family's existing answer to "not the type asked
// for".
func asObject(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case string:
		trimmed := strings.TrimSpace(typed)
		if !strings.HasPrefix(trimmed, "{") {
			return nil
		}
		var object map[string]any
		if json.Unmarshal([]byte(trimmed), &object) != nil {
			return nil
		}
		return object
	default:
		return nil
	}
}

func containsValue(list []any, wanted any, caseSensitive bool) bool {
	if absent(wanted) {
		// JavaScript's `includes` compares with `===`: a null in the list is
		// the only thing a null matches, and an undefined matches nothing that
		// arrived as JSON.
		for _, entry := range list {
			if wanted == nil && entry == nil {
				return true
			}
		}
		return false
	}
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
// mean what the author meant. The table is n8n's own — JavaScript's `Number()`
// and `String()`, and its `tryToParseBoolean`, which recognises the boolean
// spellings rather than the truthiness of a string — and a value that is not
// there at all is never a conversion error, because n8n passes null and
// undefined straight through to the comparison.
//
// Strict refuses a value of the wrong type instead, which is the only useful
// thing to say about it. An absent value is not of the wrong type: n8n's own
// strict path accepts it too, and refusing it would fail a run over an optional
// field.

func asString(value any, options Options, side string) (string, error) {
	if absent(value) {
		// n8n reads an absent value as empty text (`leftValue ?? ''`).
		return "", nil
	}
	if text, ok := value.(string); ok {
		return text, nil
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

// Number reads a value the way n8n's own number conversion does: JavaScript's
// `Number()`, with the case n8n calls out in its own source — an empty list is
// zero, like an empty string.
//
// It reports false for what JavaScript answers NaN for, and for the
// single-element lists where JavaScript's answer (`[5]` is 5) is a detail no
// workflow means. It is exported because the Set node's typed assignments
// follow the same table, and two tables is how `"5"` stops being a number in
// one node and is one in the next.
func Number(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	case bool:
		if typed {
			return 1, true
		}
		return 0, true
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			// JavaScript: Number('') and Number('  ') are both zero. A form or
			// sheet field left blank is routine, and n8n compares it as zero
			// rather than refusing the row.
			return 0, true
		}
		// JavaScript's spelling of a number is wider than Go's — it also reads
		// '0x10' as 16. A workflow carrying one gets the honest error instead
		// of a number Go would have to guess at.
		number, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return math.NaN(), false
		}
		return number, true
	case []any:
		if len(typed) == 0 {
			return 0, true
		}
		return math.NaN(), false
	default:
		return math.NaN(), false
	}
}

// Boolean reads a value the way n8n's own boolean conversion does: its
// `tryToParseBoolean`, which is not JavaScript's truthiness. `"true"` and
// `"false"` in any case are booleans, and so is anything JavaScript's `Number()`
// reads as exactly 0 or 1 — `"0"`, `"1"`, `0`, `1`, an empty list. Everything
// else is reported as no boolean, and the caller decides what that means: a Set
// assignment refuses it, while an IF condition falls back to truthiness, which
// is why `"yes"` matches there and not here.
func Boolean(value any) (bool, bool) {
	if typed, ok := value.(bool); ok {
		return typed, true
	}
	if text, ok := value.(string); ok {
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
		// n8n's own rule: the string is read through Number(), so "1" and "0"
		// are booleans and "" is not one.
		if strings.TrimSpace(text) == "" {
			return false, false
		}
	}
	if number, ok := Number(value); ok {
		switch number {
		case 0:
			return false, true
		case 1:
			return true, true
		}
	}
	return false, false
}

// truthy is JavaScript's truth table, which is what n8n falls back to when a
// value is not one of the boolean spellings it recognises.
func truthy(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case float64:
		return typed != 0 && !math.IsNaN(typed)
	case string:
		return typed != ""
	default:
		return true
	}
}

func asNumber(value any, options Options, side string) (float64, error) {
	if options.TypeValidation == ValidationStrict {
		switch typed := value.(type) {
		case float64:
			return typed, nil
		case int:
			return float64(typed), nil
		case json.Number:
			return typed.Float64()
		}
		return 0, fmt.Errorf("condition %s value is not a number", side)
	}
	if number, ok := Number(value); ok {
		return number, nil
	}
	if text, isText := value.(string); isText {
		return 0, fmt.Errorf("condition %s value %q is not a number", side, text)
	}
	return 0, fmt.Errorf("condition %s value is not a number", side)
}

func asBoolean(value any, options Options, side string) (bool, error) {
	if options.TypeValidation == ValidationStrict {
		if typed, ok := value.(bool); ok {
			return typed, nil
		}
		return false, fmt.Errorf("condition %s value is not a boolean", side)
	}
	if decided, ok := Boolean(value); ok {
		return decided, nil
	}
	// n8n's own fallback when its boolean conversion refuses a value is
	// JavaScript truthiness — which is why "yes" is true there, and why a
	// value it cannot read as a boolean routes the item instead of failing the
	// run.
	return truthy(value), nil
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
	if absent(value) {
		// An absent value is not a date-format error: n8n accepts it and then
		// refuses to compare, which matchDateTime does.
		return time.Time{}, nil
	}
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
	if typed, ok := value.(float64); ok {
		// A number is milliseconds since the epoch, which is what every
		// JavaScript source in this ecosystem produces.
		return time.UnixMilli(int64(typed)).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("condition %s value is not a date", side)
}
