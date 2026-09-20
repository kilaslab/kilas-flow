package expression

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// This file is the back half of the evaluator: it gives the syntax tree
// JavaScript's semantics.
//
// The point of matching JavaScript here rather than inventing something
// reasonable is that these bodies are JavaScript in every workflow that was
// written for n8n. `'a' + 1` is 'a1', `undefined` is falsy, `""` is falsy but
// `[]` is not, `??` looks past null and undefined but not past 0, and a
// comparison with `undefined` is false rather than an error. A workflow author
// who has to remember which of those this product does differently will get it
// wrong, silently.

// evaluator walks a parsed expression. It carries the runtime context and the
// arrow-function scope stack.
type evaluator struct {
	ctx  Context
	vars []map[string]any
}

// closure is an arrow function with the scope it was written in.
type closure struct {
	params []string
	body   node
	scope  []map[string]any
}

// evaluate parses and runs one `{{ … }}` body.
func evaluate(body string, ctx Context) (any, error) {
	parsed, err := parseExpression(body)
	if err != nil {
		return nil, err
	}
	engine := &evaluator{ctx: ctx}
	return engine.eval(parsed)
}

// eval runs one node of the tree.
//
// The wrapper exists for one rule: a lineage refusal is never a value. It
// carries why no single item could be chosen, so it becomes an error the moment
// it is produced, before it can be embedded in a list, concatenated into text
// or written to a parameter as `{}` — which is what dropping the refusal inside
// a container used to do.
func (e *evaluator) eval(expression node) (any, error) {
	value, err := e.evalNode(expression)
	if err != nil {
		return nil, err
	}
	if refusal, unavailable := value.(lineageError); unavailable {
		return nil, fmt.Errorf("%s", refusal.reason)
	}
	return value, nil
}

func (e *evaluator) evalNode(expression node) (any, error) {
	switch typed := expression.(type) {
	case literalNode:
		return typed.value, nil
	case identNode:
		return e.identifier(typed.name)
	case memberNode:
		return e.member(typed)
	case indexNode:
		return e.index(typed)
	case callNode:
		return e.call(typed)
	case unaryNode:
		return e.unary(typed)
	case binaryNode:
		return e.binary(typed)
	case conditionalNode:
		test, err := e.eval(typed.test)
		if err != nil {
			return nil, err
		}
		if truthy(test) {
			return e.eval(typed.then)
		}
		return e.eval(typed.otherwise)
	case arrayNode:
		values := make([]any, 0, len(typed.elements))
		for _, element := range typed.elements {
			value, err := e.eval(element)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return values, nil
	case objectNode:
		object := make(map[string]any, len(typed.keys))
		for index, key := range typed.keys {
			value, err := e.eval(typed.values[index])
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		return object, nil
	case templateNode:
		var builder strings.Builder
		for index, text := range typed.texts {
			builder.WriteString(text)
			if index < len(typed.exprs) {
				value, err := e.eval(typed.exprs[index])
				if err != nil {
					return nil, err
				}
				builder.WriteString(jsString(value))
			}
		}
		return builder.String(), nil
	case arrowNode:
		return closure{params: typed.params, body: typed.body, scope: e.snapshot()}, nil
	case nodeRefNode:
		name, err := e.eval(typed.name)
		if err != nil {
			return nil, err
		}
		text, ok := name.(string)
		if !ok {
			return nil, fmt.Errorf("$(…) needs a node name as text")
		}
		return nodeRootValue(text, e.ctx)
	case spreadNode:
		return nil, fmt.Errorf("... is only meaningful inside a function call")
	default:
		return nil, fmt.Errorf("expression node %T is not supported", expression)
	}
}

func (e *evaluator) snapshot() []map[string]any {
	copied := make([]map[string]any, len(e.vars))
	copy(copied, e.vars)
	return copied
}

func (e *evaluator) identifier(name string) (any, error) {
	for index := len(e.vars) - 1; index >= 0; index-- {
		if value, found := e.vars[index][name]; found {
			return value, nil
		}
	}
	if global, found := globals[name]; found {
		return global, nil
	}
	if strings.HasPrefix(name, "$") {
		return resolveRoot(name, e.ctx)
	}
	if _, found := builtins[name]; found {
		return nil, fmt.Errorf("%s is a function and has to be called: %s(…)", name, name)
	}
	return nil, fmt.Errorf("%s is not defined", name)
}

func (e *evaluator) member(expression memberNode) (any, error) {
	receiver, err := e.eval(expression.object)
	if err != nil {
		return nil, err
	}
	if expression.optional && isNullish(receiver) {
		return Undefined, nil
	}
	return readMember(receiver, expression.name)
}

// readMember is one property read with JavaScript's rules.
//
// A property that is not there is undefined, not an error — including on a
// scalar, where the old evaluator failed the whole node. n8n returns undefined
// for `$json.name.missingProp` and `$json.tags[5]`, and a workflow that reads an
// optional field off a value that turned out to be a string is a workflow that
// should keep running.
func readMember(receiver any, name string) (any, error) {
	switch typed := receiver.(type) {
	case nil, undefinedValue:
		return Undefined, nil
	case map[string]any:
		if value, found := typed[name]; found {
			return value, nil
		}
		return Undefined, nil
	case envSource:
		value, found := typed[name]
		if !found {
			return nil, fmt.Errorf("$env.%s is not set; only variables exported as KILASFLOW_WORKFLOW_ENV_%s reach a workflow", name, name)
		}
		return value, nil
	case inputSource:
		return typed.member(name)
	case []any:
		if name == "length" {
			return float64(len(typed)), nil
		}
		return Undefined, nil
	case string:
		if name == "length" {
			return float64(len([]rune(typed))), nil
		}
		return Undefined, nil
	case namespaceValue:
		if members, found := namespaceProps[typed.name]; found {
			if value, found := members[name]; found {
				return value, nil
			}
		}
		return Undefined, nil
	case dateValue:
		return typed.property(name)
	case time.Time:
		return dateValue{at: typed}.property(name)
	default:
		return Undefined, nil
	}
}

func (e *evaluator) index(expression indexNode) (any, error) {
	receiver, err := e.eval(expression.object)
	if err != nil {
		return nil, err
	}
	if expression.optional && isNullish(receiver) {
		return Undefined, nil
	}
	key, err := e.eval(expression.index)
	if err != nil {
		return nil, err
	}
	return readIndex(receiver, key)
}

func readIndex(receiver, key any) (any, error) {
	switch typed := receiver.(type) {
	case nil, undefinedValue:
		return Undefined, nil
	case map[string]any:
		if text, ok := key.(string); ok {
			if value, found := typed[text]; found {
				return value, nil
			}
			return Undefined, nil
		}
		return Undefined, nil
	case envSource:
		text, ok := key.(string)
		if !ok {
			return Undefined, nil
		}
		if value, found := typed[text]; found {
			return value, nil
		}
		return nil, fmt.Errorf("$env[%q] is not set; only variables exported as KILASFLOW_WORKFLOW_ENV_%s reach a workflow", text, text)
	case inputSource:
		if text, ok := key.(string); ok {
			return typed.member(text)
		}
		return Undefined, nil
	case []any:
		// `['length']` is the same read as `.length`: a key that arrives from
		// the item, or a computed property name, is how a workflow reaches the
		// length dynamically.
		if name, ok := key.(string); ok && name == "length" {
			return float64(len(typed)), nil
		}
		position, ok := indexOf(key)
		if !ok || position < 0 || position >= len(typed) {
			return Undefined, nil
		}
		return typed[position], nil
	case string:
		if name, ok := key.(string); ok && name == "length" {
			return float64(len([]rune(typed))), nil
		}
		position, ok := indexOf(key)
		if !ok || position < 0 {
			return Undefined, nil
		}
		runes := []rune(typed)
		if position >= len(runes) {
			return Undefined, nil
		}
		return string(runes[position]), nil
	case dateValue:
		return typed.property(jsString(key))
	case time.Time:
		return dateValue{at: typed}.property(jsString(key))
	default:
		return Undefined, nil
	}
}

// indexOf reads an array index, which JavaScript allows as a number or as the
// text of one: `arr['0']` and `arr[0]` are the same element.
func indexOf(key any) (int, bool) {
	switch typed := key.(type) {
	case float64:
		if typed != math.Trunc(typed) {
			return 0, false
		}
		return int(typed), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func (e *evaluator) call(expression callNode) (any, error) {
	values, err := e.arguments(expression.args)
	if err != nil {
		return nil, err
	}
	switch target := expression.callee.(type) {
	case memberNode:
		receiver, err := e.eval(target.object)
		if err != nil {
			return nil, err
		}
		if target.optional && isNullish(receiver) {
			return Undefined, nil
		}
		return e.method(target.name, receiver, values)
	case identNode:
		if builtin, found := builtins[target.name]; found {
			return builtin.call(e, values)
		}
		if strings.HasPrefix(target.name, "$") {
			return callRoot(target.name, e.ctx, values)
		}
		return nil, fmt.Errorf("%s is not a function", target.name)
	}
	callee, err := e.eval(expression.callee)
	if err != nil {
		return nil, err
	}
	if fn, ok := callee.(closure); ok {
		return e.callClosure(fn, values)
	}
	return nil, fmt.Errorf("expression calls a value that is not a function")
}

func (e *evaluator) arguments(args []node) ([]any, error) {
	values := make([]any, 0, len(args))
	for _, argument := range args {
		if spread, ok := argument.(spreadNode); ok {
			value, err := e.eval(spread.value)
			if err != nil {
				return nil, err
			}
			list, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("... needs a list to expand")
			}
			values = append(values, list...)
			continue
		}
		value, err := e.eval(argument)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func (e *evaluator) callClosure(fn closure, args []any) (any, error) {
	bound := make(map[string]any, len(fn.params))
	for index, name := range fn.params {
		if index < len(args) {
			bound[name] = args[index]
			continue
		}
		bound[name] = Undefined
	}
	saved := e.vars
	e.vars = append(e.snapshotOf(fn.scope), bound)
	defer func() { e.vars = saved }()
	return e.eval(fn.body)
}

func (e *evaluator) snapshotOf(scope []map[string]any) []map[string]any {
	copied := make([]map[string]any, len(scope))
	copy(copied, scope)
	return copied
}

func (e *evaluator) method(name string, receiver any, args []any) (any, error) {
	if fn, ok := receiver.(closure); ok && name == "call" {
		return e.callClosure(fn, args)
	}
	// JSON.stringify, Object.keys, Math.round and DateTime.fromISO are members
	// of a namespace object rather than methods of a value.
	if namespace, ok := receiver.(namespaceValue); ok {
		members, found := namespaceFuncs[namespace.name]
		if !found {
			return nil, fmt.Errorf("%s.%s is not a function", namespace.name, name)
		}
		entry, found := members[name]
		if !found {
			return nil, fmt.Errorf("%s.%s is not a function", namespace.name, name)
		}
		return entry.call(e, args)
	}
	entry, found := methods[name]
	if !found {
		return nil, fmt.Errorf("%s is not a function", name)
	}
	if receiver == nil || IsUndefined(receiver) {
		return nil, fmt.Errorf("cannot read %s() of %s", name, describeValue(receiver))
	}
	return entry.apply(e, receiver, args)
}

func (e *evaluator) unary(expression unaryNode) (any, error) {
	value, err := e.eval(expression.operand)
	if err != nil {
		return nil, err
	}
	switch expression.op {
	case "!":
		return !truthy(value), nil
	case "-":
		number, _ := toNumber(value)
		return -number, nil
	default:
		number, _ := toNumber(value)
		return number, nil
	}
}

func (e *evaluator) binary(expression binaryNode) (any, error) {
	switch expression.op {
	case "&&":
		left, err := e.eval(expression.left)
		if err != nil {
			return nil, err
		}
		if !truthy(left) {
			return left, nil
		}
		return e.eval(expression.right)
	case "||":
		left, err := e.eval(expression.left)
		if err != nil {
			return nil, err
		}
		if truthy(left) {
			return left, nil
		}
		return e.eval(expression.right)
	case "??":
		left, err := e.eval(expression.left)
		if err != nil {
			return nil, err
		}
		if !isNullish(left) {
			return left, nil
		}
		return e.eval(expression.right)
	}

	left, err := e.eval(expression.left)
	if err != nil {
		return nil, err
	}
	right, err := e.eval(expression.right)
	if err != nil {
		return nil, err
	}
	switch expression.op {
	case "+":
		return addValues(left, right), nil
	case "-", "*", "/", "%":
		return arithmetic(expression.op, left, right), nil
	case "==", "!=":
		equal := looselyEqual(left, right)
		return equal == (expression.op == "=="), nil
	case "===", "!==":
		equal := strictlyEqual(left, right)
		return equal == (expression.op == "==="), nil
	case "<", "<=", ">", ">=":
		return compare(expression.op, left, right), nil
	}
	return nil, fmt.Errorf("operator %q is not supported", expression.op)
}

// addValues is JavaScript's `+`: text when either side is text, arithmetic
// otherwise.
func addValues(left, right any) any {
	if isNumericOperand(left) && isNumericOperand(right) {
		leftNumber, _ := toNumber(left)
		rightNumber, _ := toNumber(right)
		return leftNumber + rightNumber
	}
	return jsString(left) + jsString(right)
}

// isNumericOperand reports whether a value takes part in `+` as a number.
// Strings never do — `1 + '1'` is '11' in JavaScript — and neither do lists or
// objects, which stringify.
func isNumericOperand(value any) bool {
	switch value.(type) {
	case float64, bool, nil, undefinedValue:
		return true
	default:
		return false
	}
}

func arithmetic(operator string, left, right any) float64 {
	leftNumber, _ := toNumber(left)
	rightNumber, _ := toNumber(right)
	switch operator {
	case "-":
		return leftNumber - rightNumber
	case "*":
		return leftNumber * rightNumber
	case "/":
		return leftNumber / rightNumber
	default:
		return math.Mod(leftNumber, rightNumber)
	}
}

// compare matches JavaScript's ordering: text against text is lexicographic,
// anything else is numeric, and anything involving undefined is false.
func compare(operator string, left, right any) bool {
	leftText, leftIsText := left.(string)
	rightText, rightIsText := right.(string)
	if leftIsText && rightIsText {
		switch operator {
		case "<":
			return leftText < rightText
		case "<=":
			return leftText <= rightText
		case ">":
			return leftText > rightText
		default:
			return leftText >= rightText
		}
	}
	leftNumber, _ := toNumber(left)
	rightNumber, _ := toNumber(right)
	if math.IsNaN(leftNumber) || math.IsNaN(rightNumber) {
		return false
	}
	switch operator {
	case "<":
		return leftNumber < rightNumber
	case "<=":
		return leftNumber <= rightNumber
	case ">":
		return leftNumber > rightNumber
	default:
		return leftNumber >= rightNumber
	}
}

func strictlyEqual(left, right any) bool {
	if isNullish(left) || isNullish(right) {
		return isNullish(left) && isNullish(right) && IsUndefined(left) == IsUndefined(right)
	}
	switch leftValue := left.(type) {
	case string:
		rightValue, ok := right.(string)
		return ok && leftValue == rightValue
	case float64:
		rightValue, ok := right.(float64)
		return ok && leftValue == rightValue
	case bool:
		rightValue, ok := right.(bool)
		return ok && leftValue == rightValue
	case dateValue:
		rightValue, ok := right.(dateValue)
		return ok && leftValue.at.Equal(rightValue.at)
	}
	return false
}

// looselyEqual is JavaScript's abstract equality (`==`).
//
// It is not "strict equality with a text conversion": a boolean is a number
// here, and a collection becomes a primitive, so `true == 1`, `false == 0`,
// `[] == false` and `[1,2] == '1,2'` are all true. The old version fell through
// to strict equality for everything that was not text, which turned every one
// of those into a false that a workflow branched on.
func looselyEqual(left, right any) bool {
	if isNullish(left) || isNullish(right) {
		// null and undefined are equal to each other and to nothing else —
		// in particular not to 0 or to ''.
		return isNullish(left) && isNullish(right)
	}
	if _, isBool := left.(bool); isBool {
		number, _ := toNumber(left)
		return looselyEqual(number, right)
	}
	if _, isBool := right.(bool); isBool {
		number, _ := toNumber(right)
		return looselyEqual(left, number)
	}
	leftNumber, leftIsNumber := numberValue(left)
	rightNumber, rightIsNumber := numberValue(right)
	if leftIsNumber && rightIsNumber {
		return leftNumber == rightNumber
	}
	leftText, leftIsText := left.(string)
	rightText, rightIsText := right.(string)
	switch {
	case leftIsText && rightIsText:
		return leftText == rightText
	case leftIsText && rightIsNumber:
		number, ok := toNumber(leftText)
		return ok && number == rightNumber
	case leftIsNumber && rightIsText:
		number, ok := toNumber(rightText)
		return ok && leftNumber == number
	case isObjectValue(left) && isObjectValue(right):
		// Two objects are equal only when they are the same object, and this
		// evaluator never hands out two references to one object.
		return strictlyEqual(left, right)
	case isObjectValue(left):
		// ToPrimitive: a collection is compared as the text it renders as.
		return looselyEqual(jsString(left), right)
	case isObjectValue(right):
		return looselyEqual(left, jsString(right))
	}
	return strictlyEqual(left, right)
}

// numberValue reads the numeric types an expression can hold. Text is
// deliberately excluded: `'42'` is only a number once the other side is one.
func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	default:
		return 0, false
	}
}

// isObjectValue reports whether a value is a collection or a wrapper rather
// than a primitive, which is what decides whether `==` has to convert it.
func isObjectValue(value any) bool {
	switch value.(type) {
	case []any, map[string]any, dateValue, time.Time, closure, inputSource, envSource, namespaceValue, FromAIRequest:
		return true
	default:
		return false
	}
}

func isNullish(value any) bool {
	return value == nil || IsUndefined(value)
}

// truthy is JavaScript's truth table. An empty list and an empty object are
// both true, which is where a Go-shaped intuition gets it wrong.
func truthy(value any) bool {
	switch typed := value.(type) {
	case nil, undefinedValue:
		return false
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

func toNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case bool:
		if typed {
			return 1, true
		}
		return 0, true
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return 0, true
		}
		number, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return math.NaN(), false
		}
		return number, true
	case nil:
		return 0, true
	default:
		return math.NaN(), false
	}
}

// jsString renders a value the way JavaScript's string conversion does.
//
// Arrays join with commas and objects are JSON, which is not what `String({})`
// does in a browser — but it is what n8n produces when an object lands in a
// prompt or a request body, and it is never the Go syntax `map[city:London]`
// that KilasFlow used to emit.
func jsString(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case undefinedValue:
		return "undefined"
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		return jsNumber(typed)
	case int:
		return strconv.Itoa(typed)
	case dateValue:
		return typed.String()
	case time.Time:
		return dateValue{at: typed}.String()
	case []any:
		parts := make([]string, len(typed))
		for index, entry := range typed {
			parts[index] = jsString(entry)
		}
		return strings.Join(parts, ",")
	case map[string]any:
		if encoded, err := jsonEncode(typed); err == nil {
			return encoded
		}
		return "[object Object]"
	case inputSource:
		// An object of ports rather than a value, so it follows the same rule
		// as any other object here: JSON, never Go's own map syntax.
		return jsString(typed.plain())
	case envSource:
		return jsString(fieldsOfEnv(typed))
	case namespaceValue:
		return "[object " + typed.tag() + "]"
	case closure:
		return "function"
	default:
		return fmt.Sprintf("%v", typed)
	}
}

// jsNumber renders a float the way JavaScript's Number::toString does.
//
// Two spellings come out of the magnitude: decimal notation while the value is
// at least 1e-6 and below 1e21, and exponent notation outside it — so 1e-5 is
// "0.00001" and 1e-7 is "1e-7", where Go's shortest form would give "1e-05" and
// "1e-07". Go pads an exponent to two digits and JavaScript does not, so the
// exponent is rewritten; and negative zero prints as "0", which is what
// String(-0) is.
func jsNumber(value float64) string {
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "Infinity"
	case math.IsInf(value, -1):
		return "-Infinity"
	case value == 0:
		return "0"
	case math.Abs(value) < 1e-6 || math.Abs(value) >= 1e21:
		return trimExponent(strconv.FormatFloat(value, 'e', -1, 64))
	default:
		return strconv.FormatFloat(value, 'f', -1, 64)
	}
}

// trimExponent drops the zero padding Go writes into an exponent: "1e-07" is
// JavaScript's "1e-7", and the sign JavaScript writes is kept either way.
func trimExponent(text string) string {
	marker := strings.IndexByte(text, 'e')
	if marker < 0 {
		return text
	}
	mantissa, exponent := text[:marker], text[marker+1:]
	sign := ""
	if exponent != "" && (exponent[0] == '+' || exponent[0] == '-') {
		sign, exponent = exponent[:1], exponent[1:]
	}
	exponent = strings.TrimLeft(exponent, "0")
	if exponent == "" {
		exponent = "0"
	}
	return mantissa + "e" + sign + exponent
}

// fixedDecimal renders a number with a fixed number of decimals the way
// Number.prototype.toFixed does.
//
// strconv cannot: it rounds a tie to the even digit, so (2.5).toFixed(0) was
// "2" where JavaScript gives "3", and every exactly representable half was one
// unit off in the last digit. JavaScript picks the n-digit decimal nearest the
// exact value of the float and, when two are equally close, the larger one —
// with the sign taken off first, so a negative half goes away from zero:
// (-2.5).toFixed(0) is "-3" while (-0.4).toFixed(0) is "-0". big.Rat holds the
// float64 exactly, so no tie is decided by a binary approximation.
func fixedDecimal(value float64, digits int) string {
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "Infinity"
	case math.IsInf(value, -1):
		return "-Infinity"
	case math.Abs(value) >= 1e21:
		// JavaScript has no integer for a magnitude this large and falls back
		// to ToString.
		return jsNumber(value)
	}
	scaled := new(big.Rat).SetFloat64(math.Abs(value))
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	scaled.Mul(scaled, new(big.Rat).SetInt(scale))
	scaled.Add(scaled, big.NewRat(1, 2))
	// The denominator of the sum is positive, so Quo truncates towards zero,
	// which is the floor of the value the half already nudged upwards.
	text := new(big.Int).Quo(scaled.Num(), scaled.Denom()).String()
	if digits > 0 {
		if len(text) <= digits {
			text = strings.Repeat("0", digits-len(text)+1) + text
		}
		text = text[:len(text)-digits] + "." + text[len(text)-digits:]
	}
	if value < 0 {
		return "-" + text
	}
	return text
}

func describeValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case undefinedValue:
		return "undefined"
	case string:
		return strconv.Quote(typed)
	default:
		return jsString(typed)
	}
}

// jsonEncode is the JSON half of stringification, with the sentinels this
// package uses normalised to what JSON can carry.
func jsonEncode(value any) (string, error) {
	encoded, err := json.Marshal(normalizeJSON(value))
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func jsonEncodeIndent(value any, indent string) (string, error) {
	encoded, err := json.MarshalIndent(normalizeJSON(value), "", indent)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func normalizeJSON(value any) any {
	switch typed := value.(type) {
	case undefinedValue, lineageError, closure:
		// Only an array slot keeps a placeholder for these. An object property
		// whose value is undefined or a function is not written at all:
		// `JSON.stringify({a: undefined, b: 1})` is `{"b":1}` in JavaScript, and
		// writing an explicit null there sends an upstream a deliberate clear
		// that no workflow asked for.
		return nil
	case []any:
		normalized := make([]any, len(typed))
		for index, entry := range typed {
			normalized[index] = normalizeJSON(entry)
		}
		return normalized
	case map[string]any:
		normalized := make(map[string]any, len(typed))
		for key, entry := range typed {
			if dropped(entry) {
				continue
			}
			normalized[key] = normalizeJSON(entry)
		}
		return normalized
	case inputSource:
		return typed.plain()
	case envSource:
		return fieldsOfEnv(typed)
	default:
		return value
	}
}

// dropped reports a property value JSON has no place for, which is therefore
// left out of the object rather than written as null.
func dropped(value any) bool {
	switch value.(type) {
	case undefinedValue, lineageError, closure:
		return true
	default:
		return false
	}
}
