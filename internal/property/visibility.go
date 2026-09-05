package property

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// VersionKey is the pseudo-key a condition uses to gate on the node's type
// version, so one definition can present different parameters per version.
//
// n8n also has `@feature` and `@tool`. They gate on instance features that have
// no KilasFlow equivalent, so accepting them would mean inventing semantics —
// they are refused by name at registration instead.
const VersionKey = "@version"

// Operator compares a controlling value against a condition's operands.
type Operator string

const (
	OperatorEquals     Operator = "eq"
	OperatorNotEquals  Operator = "not"
	OperatorGreater    Operator = "gt"
	OperatorGreaterEq  Operator = "gte"
	OperatorLess       Operator = "lt"
	OperatorLessEq     Operator = "lte"
	OperatorBetween    Operator = "between"
	OperatorStartsWith Operator = "startsWith"
	OperatorEndsWith   Operator = "endsWith"
	OperatorIncludes   Operator = "includes"
	OperatorRegex      Operator = "regex"
	OperatorExists     Operator = "exists"
)

// KnownOperators is the closed set.
func KnownOperators() []Operator {
	return []Operator{
		OperatorEquals, OperatorNotEquals,
		OperatorGreater, OperatorGreaterEq, OperatorLess, OperatorLessEq,
		OperatorBetween, OperatorStartsWith, OperatorEndsWith,
		OperatorIncludes, OperatorRegex, OperatorExists,
	}
}

// Condition is one key and the values it accepts.
//
// Within a single condition the values are OR'd: "operation is one of send,
// sendPhoto or sendDocument" is one condition with three values. Across
// conditions in a `show` group they are AND'd. That asymmetry is n8n's and is
// what makes the common shapes expressible without nesting.
type Condition struct {
	Key string `json:"key"`
	// Values are the literals this key may hold. Empty with an operator means
	// the operator needs no operand, as `exists` does.
	Values []any `json:"values,omitempty"`
	// Operator defaults to equality.
	Operator Operator `json:"operator,omitempty"`
}

// Visibility decides whether a property is shown.
//
// Under Show, **every** listed key must match. Under Hide, **any** matching key
// hides — but only when the controlling parameter actually has a value, so an
// absent value never hides. Both rules are n8n's, copied deliberately: a node
// imported from n8n presents its parameters by them, and a near-miss here shows
// the wrong fields on every imported node.
type Visibility struct {
	Show []Condition `json:"show,omitempty"`
	Hide []Condition `json:"hide,omitempty"`
}

// IsEmpty reports a property with no visibility rule, which is always shown.
func (visibility Visibility) IsEmpty() bool {
	return len(visibility.Show) == 0 && len(visibility.Hide) == 0
}

// ExpressionMarker reports a value that is a KilasFlow expression.
//
// n8n marks one with a leading `=` on a plain string; KilasFlow uses the
// explicit object. The marker differs, the rule does not.
func ExpressionMarker(value any) bool {
	object, isObject := value.(map[string]any)
	if !isObject {
		return false
	}
	mode, _ := object["mode"].(string)
	if mode != "expression" {
		return false
	}
	_, hasValue := object["value"].(string)
	return hasValue
}

// Visible evaluates a property's rule against a node's stored parameters.
//
// A hidden parent does **not** suppress its children. Every property is
// evaluated independently against the stored parameters, so a property whose
// controlling parameter is itself hidden is still evaluated on that parameter's
// stored value. Imported nodes depend on this — n8n behaves the same way, and a
// cascade would hide parameters an imported workflow legitimately sets.
func Visible(visibility Visibility, parameters map[string]any, typeVersion string) bool {
	if visibility.IsEmpty() {
		return true
	}

	for _, condition := range visibility.Show {
		value, present := controllingValue(condition.Key, parameters, typeVersion)
		// The escape hatch, and it lives in the show branch only. If the
		// controlling parameter holds an expression, the dependent property is
		// always shown: nothing can know at edit time what that expression will
		// evaluate to, and hiding a field the user may need is worse than
		// showing one they may not.
		if present && ExpressionMarker(value) {
			continue
		}
		if !matches(condition, value, present) {
			return false
		}
	}

	for _, condition := range visibility.Hide {
		value, present := controllingValue(condition.Key, parameters, typeVersion)
		// An absent value never hides. n8n checks the value list is non-empty
		// before comparing, which is the same rule stated from the other side:
		// a property is not hidden by a parameter nobody has set.
		if !present || value == nil {
			continue
		}
		if matches(condition, value, present) {
			return false
		}
	}
	return true
}

func controllingValue(key string, parameters map[string]any, typeVersion string) (any, bool) {
	if key == VersionKey {
		return typeVersion, typeVersion != ""
	}
	value, present := parameters[key]
	return value, present
}

// matches applies one condition. Values within it are OR'd.
func matches(condition Condition, value any, present bool) bool {
	operator := condition.Operator
	if operator == "" {
		operator = OperatorEquals
	}
	if operator == OperatorExists {
		return present && value != nil
	}
	if operator == OperatorBetween {
		if len(condition.Values) != 2 {
			return false
		}
		return compareNumbers(value, condition.Values[0]) >= 0 &&
			compareNumbers(value, condition.Values[1]) <= 0
	}
	if operator == OperatorNotEquals {
		// Negation is over the whole list: "not one of these", not "not the
		// first and not the second considered separately".
		for _, operand := range condition.Values {
			if equal(value, operand) {
				return false
			}
		}
		return true
	}
	for _, operand := range condition.Values {
		if applyOperator(operator, value, operand) {
			return true
		}
	}
	return false
}

func applyOperator(operator Operator, value, operand any) bool {
	switch operator {
	case OperatorEquals:
		return equal(value, operand)
	case OperatorGreater:
		return compareNumbers(value, operand) > 0
	case OperatorGreaterEq:
		return compareNumbers(value, operand) >= 0
	case OperatorLess:
		return compareNumbers(value, operand) < 0
	case OperatorLessEq:
		return compareNumbers(value, operand) <= 0
	case OperatorStartsWith:
		return strings.HasPrefix(text(value), text(operand))
	case OperatorEndsWith:
		return strings.HasSuffix(text(value), text(operand))
	case OperatorIncludes:
		if list, isList := value.([]any); isList {
			for _, entry := range list {
				if equal(entry, operand) {
					return true
				}
			}
			return false
		}
		return strings.Contains(text(value), text(operand))
	case OperatorRegex:
		pattern, err := regexp.Compile(text(operand))
		if err != nil {
			return false
		}
		return pattern.MatchString(text(value))
	default:
		return false
	}
}

// equal compares by value, not by identity.
//
// This is precisely what the previous `===` got wrong: it never matched an
// object or an array, so a condition on anything but a primitive was silently
// always false. Numbers are compared as float64 because a value arriving
// through encoding/json is one while a condition written in Go is an int, and
// they must agree.
func equal(value, operand any) bool {
	left, leftNumeric := numeric(value)
	right, rightNumeric := numeric(operand)
	if leftNumeric && rightNumeric {
		return left == right
	}
	// A JSON number and its string form are the same value to a user reading a
	// dropdown, so they compare equal — otherwise a select whose options are
	// strings can never gate on a numeric parameter.
	if leftNumeric != rightNumeric {
		return text(value) == text(operand)
	}
	return reflect.DeepEqual(value, operand)
}

func compareNumbers(value, operand any) int {
	left, leftNumeric := numeric(value)
	right, rightNumeric := numeric(operand)
	if !leftNumeric || !rightNumeric {
		return strings.Compare(text(value), text(operand))
	}
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func numeric(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func text(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return fmt.Sprint(typed)
	}
}

// ValidateVisibility refuses a rule this server cannot honour.
func ValidateVisibility(visibility Visibility) error {
	for _, group := range [][]Condition{visibility.Show, visibility.Hide} {
		for _, condition := range group {
			if strings.TrimSpace(condition.Key) == "" {
				return fmt.Errorf("a visibility condition names no key")
			}
			if strings.HasPrefix(condition.Key, "@") && condition.Key != VersionKey {
				// Accepting and ignoring would present the wrong fields
				// silently; naming it is the honest failure.
				return fmt.Errorf("visibility key %q is not supported; only %s is", condition.Key, VersionKey)
			}
			if condition.Operator == "" {
				continue
			}
			var known bool
			for _, operator := range KnownOperators() {
				if condition.Operator == operator {
					known = true
				}
			}
			if !known {
				return fmt.Errorf("visibility operator %q is not supported", condition.Operator)
			}
		}
	}
	return nil
}
