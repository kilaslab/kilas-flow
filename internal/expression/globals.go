package expression

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// namespaceValue is a JavaScript global object whose members are functions:
// JSON, Object, Array, Math and Luxon's DateTime.
type namespaceValue struct{ name string }

// tag is what Object.prototype.toString reports for this namespace: JSON and
// Math carry a Symbol.toStringTag, the constructor-shaped names are functions
// in JavaScript and have no tag, and a plain object renders as Object.
func (namespace namespaceValue) tag() string {
	switch namespace.name {
	case "JSON", "Math":
		return namespace.name
	default:
		return "Object"
	}
}

// globals are the names an expression may read that are not roots. Arrow
// function parameters shadow them, and nothing else can.
var globals = map[string]any{
	"JSON":     namespaceValue{name: "JSON"},
	"Object":   namespaceValue{name: "Object"},
	"Array":    namespaceValue{name: "Array"},
	"Math":     namespaceValue{name: "Math"},
	"DateTime": namespaceValue{name: "DateTime"},
	// Number, String and Boolean are both a namespace (`Number.isInteger`) and
	// a function (`Number(x)`). A call is dispatched to the builtin before this
	// map is consulted, so both spellings work.
	"Number":  namespaceValue{name: "Number"},
	"String":  namespaceValue{name: "String"},
	"Boolean": namespaceValue{name: "Boolean"},
}

// builtinFunc is a global function, called without a receiver.
type builtinFunc struct {
	bounds arity
	call   func(*evaluator, []any) (any, error)
}

var builtins = map[string]builtinFunc{}

// namespaceFuncs are the members of each namespace object.
var namespaceFuncs = map[string]map[string]builtinFunc{}

// namespaceProps are the namespace members that are values rather than calls,
// such as Math.PI.
var namespaceProps = map[string]map[string]any{}

func init() {
	registerBuiltins()
	registerNamespaces()
}

func registerBuiltins() {
	define := func(name string, bounds arity, call func(*evaluator, []any) (any, error)) {
		if _, exists := builtins[name]; exists {
			panic("expression: duplicate builtin " + name)
		}
		builtins[name] = builtinFunc{bounds: bounds, call: call}
	}
	define("parseInt", oneOrTwo, func(_ *evaluator, args []any) (any, error) {
		text := strings.TrimSpace(jsString(args[0]))
		radix := 10
		if len(args) > 1 {
			radix, _ = indexOf(args[1])
		}
		if radix == 0 {
			radix = 10
		}
		// parseInt stops at the first character that is not a digit, which is
		// why `parseInt('12px')` is 12 rather than an error.
		end := 0
		for end < len(text) && isDigitInRadix(text[end], radix) {
			end++
		}
		if end == 0 {
			return math.NaN(), nil
		}
		parsed, err := strconv.ParseInt(text[:end], radix, 64)
		if err != nil {
			return math.NaN(), nil
		}
		return float64(parsed), nil
	})
	define("parseFloat", fixed(1), func(_ *evaluator, args []any) (any, error) {
		text := strings.TrimSpace(jsString(args[0]))
		number, ok := toNumber(text)
		if !ok {
			return math.NaN(), nil
		}
		return number, nil
	})
	define("Number", fixed(1), func(_ *evaluator, args []any) (any, error) {
		number, _ := toNumber(args[0])
		return number, nil
	})
	define("String", fixed(1), func(_ *evaluator, args []any) (any, error) {
		return jsString(args[0]), nil
	})
	define("Boolean", fixed(1), func(_ *evaluator, args []any) (any, error) {
		return truthy(args[0]), nil
	})
	define("isNaN", fixed(1), func(_ *evaluator, args []any) (any, error) {
		number, _ := toNumber(args[0])
		return math.IsNaN(number), nil
	})
	define("encodeURIComponent", fixed(1), func(_ *evaluator, args []any) (any, error) {
		return encodeComponent(jsString(args[0]), "!'()*-._~"), nil
	})
	define("encodeURI", fixed(1), func(_ *evaluator, args []any) (any, error) {
		return encodeComponent(jsString(args[0]), "!'()*-._~;/?:@&=+$,#"), nil
	})
	define("decodeURIComponent", fixed(1), func(_ *evaluator, args []any) (any, error) {
		decoded, err := decodeComponent(jsString(args[0]))
		if err != nil {
			return nil, fmt.Errorf("decodeURIComponent() was given a malformed escape")
		}
		return decoded, nil
	})
	define("decodeURI", fixed(1), func(_ *evaluator, args []any) (any, error) {
		decoded, err := decodeComponent(jsString(args[0]))
		if err != nil {
			return nil, fmt.Errorf("decodeURI() was given a malformed escape")
		}
		return decoded, nil
	})
	// $items() is n8n's way to read the items flowing into the current node.
	define("$items", arity{min: 0, max: 3}, func(e *evaluator, args []any) (any, error) {
		if len(args) > 0 && !isNullish(args[0]) {
			return nodeRootValueItems(jsString(args[0]), e.ctx)
		}
		return e.ctx.input().all(), nil
	})
	// Four arguments, as n8n spells it: key, description, type, default. The
	// last is only read when the agent's arguments are in the context.
	define("$fromAI", arity{min: 1, max: 4}, func(e *evaluator, args []any) (any, error) {
		return callRoot("$fromAI", e.ctx, args)
	})
	define("$jmespath", arity{min: 1, max: 2}, func(*evaluator, []any) (any, error) {
		return nil, fmt.Errorf("$jmespath is not available in this runtime; read the field directly or use Object.keys/map/filter")
	})
}

func registerNamespaces() {
	namespace := func(owner, name string, bounds arity, call func(*evaluator, []any) (any, error)) {
		if namespaceFuncs[owner] == nil {
			namespaceFuncs[owner] = map[string]builtinFunc{}
		}
		namespaceFuncs[owner][name] = builtinFunc{bounds: bounds, call: call}
	}
	namespace("JSON", "stringify", arity{min: 1, max: 3}, func(_ *evaluator, args []any) (any, error) {
		indent := ""
		if len(args) > 2 && !isNullish(args[2]) {
			if text, ok := args[2].(string); ok {
				indent = text
			} else if count, ok := indexOf(args[2]); ok && count > 0 {
				indent = strings.Repeat(" ", count)
			}
		}
		if indent == "" {
			encoded, err := jsonEncode(args[0])
			if err != nil {
				return nil, fmt.Errorf("JSON.stringify() cannot encode this value")
			}
			return encoded, nil
		}
		encoded, err := jsonEncodeIndent(args[0], indent)
		if err != nil {
			return nil, fmt.Errorf("JSON.stringify() cannot encode this value")
		}
		return encoded, nil
	})
	namespace("JSON", "parse", fixed(1), func(_ *evaluator, args []any) (any, error) {
		text, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("JSON.parse() needs text")
		}
		value, err := decodeJSON(text)
		if err != nil {
			return nil, fmt.Errorf("JSON.parse() could not read the text: %w", err)
		}
		return value, nil
	})
	namespace("Object", "keys", fixed(1), func(_ *evaluator, args []any) (any, error) {
		object, err := needObject(args[0], "Object.keys")
		if err != nil {
			return nil, err
		}
		keys := make([]any, 0, len(object))
		for _, key := range objectKeys(args[0], object) {
			keys = append(keys, key)
		}
		return keys, nil
	})
	namespace("Object", "values", fixed(1), func(_ *evaluator, args []any) (any, error) {
		object, err := needObject(args[0], "Object.values")
		if err != nil {
			return nil, err
		}
		values := make([]any, 0, len(object))
		for _, key := range objectKeys(args[0], object) {
			values = append(values, object[key])
		}
		return values, nil
	})
	namespace("Object", "entries", fixed(1), func(_ *evaluator, args []any) (any, error) {
		object, err := needObject(args[0], "Object.entries")
		if err != nil {
			return nil, err
		}
		entries := make([]any, 0, len(object))
		for _, key := range objectKeys(args[0], object) {
			entries = append(entries, []any{key, object[key]})
		}
		return entries, nil
	})
	namespace("Object", "fromEntries", fixed(1), func(_ *evaluator, args []any) (any, error) {
		list, err := needList(args[0], "Object.fromEntries")
		if err != nil {
			return nil, err
		}
		object := make(map[string]any, len(list))
		for _, entry := range list {
			pair, ok := entry.([]any)
			if !ok || len(pair) != 2 {
				return nil, fmt.Errorf("Object.fromEntries() needs a list of key/value pairs")
			}
			object[jsString(pair[0])] = pair[1]
		}
		return object, nil
	})
	namespace("Array", "isArray", fixed(1), func(_ *evaluator, args []any) (any, error) {
		_, isList := args[0].([]any)
		return isList, nil
	})
	namespace("Array", "from", oneOrTwo, func(_ *evaluator, args []any) (any, error) {
		if list, ok := args[0].([]any); ok {
			return append([]any{}, list...), nil
		}
		if text, ok := args[0].(string); ok {
			runes := []rune(text)
			values := make([]any, len(runes))
			for index, char := range runes {
				values[index] = string(char)
			}
			return values, nil
		}
		return nil, fmt.Errorf("Array.from() needs a list or a string")
	})
	namespace("Number", "isInteger", fixed(1), func(_ *evaluator, args []any) (any, error) {
		number, ok := args[0].(float64)
		return ok && number == math.Trunc(number), nil
	})
	namespace("Number", "isFinite", fixed(1), func(_ *evaluator, args []any) (any, error) {
		number, ok := args[0].(float64)
		return ok && !math.IsInf(number, 0) && !math.IsNaN(number), nil
	})
	namespace("Number", "parseFloat", fixed(1), func(e *evaluator, args []any) (any, error) {
		return builtins["parseFloat"].call(e, args)
	})
	namespace("Number", "parseInt", oneOrTwo, func(e *evaluator, args []any) (any, error) {
		return builtins["parseInt"].call(e, args)
	})

	mathOne := func(name string, apply func(float64) float64) {
		namespace("Math", name, fixed(1), func(_ *evaluator, args []any) (any, error) {
			number, _ := toNumber(args[0])
			return apply(number), nil
		})
	}
	mathOne("abs", math.Abs)
	mathOne("ceil", math.Ceil)
	mathOne("floor", math.Floor)
	// JavaScript's Math.round is floor(x + 0.5), so a negative half goes up
	// towards zero: -2.5 is -2 where Go's math.Round gives -3.
	mathOne("round", func(value float64) float64 {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return value
		}
		return math.Floor(value + 0.5)
	})
	mathOne("trunc", math.Trunc)
	mathOne("sqrt", math.Sqrt)
	mathOne("cbrt", math.Cbrt)
	mathOne("sign", func(value float64) float64 {
		switch {
		case value > 0:
			return 1
		case value < 0:
			return -1
		default:
			return value
		}
	})
	mathOne("log", math.Log)
	mathOne("log2", math.Log2)
	mathOne("log10", math.Log10)
	mathOne("exp", math.Exp)
	mathOne("sin", math.Sin)
	mathOne("cos", math.Cos)
	mathOne("tan", math.Tan)
	for _, entry := range []struct {
		name  string
		apply func([]float64) float64
	}{
		{name: "min", apply: func(values []float64) float64 {
			if len(values) == 0 {
				return math.Inf(1)
			}
			smallest := values[0]
			for _, value := range values[1:] {
				if value < smallest {
					smallest = value
				}
			}
			return smallest
		}},
		{name: "max", apply: func(values []float64) float64 {
			if len(values) == 0 {
				return math.Inf(-1)
			}
			largest := values[0]
			for _, value := range values[1:] {
				if value > largest {
					largest = value
				}
			}
			return largest
		}},
	} {
		bounds := arity{min: 0, max: -1}
		entry := entry
		namespace("Math", entry.name, bounds, func(_ *evaluator, args []any) (any, error) {
			values := make([]float64, len(args))
			for index, argument := range args {
				values[index], _ = toNumber(argument)
			}
			return entry.apply(values), nil
		})
	}
	namespace("Math", "pow", fixed(2), func(_ *evaluator, args []any) (any, error) {
		base, _ := toNumber(args[0])
		exponent, _ := toNumber(args[1])
		return math.Pow(base, exponent), nil
	})
	namespace("Math", "hypot", anyArguments, func(_ *evaluator, args []any) (any, error) {
		total := 0.0
		for _, argument := range args {
			number, _ := toNumber(argument)
			total += number * number
		}
		return math.Sqrt(total), nil
	})
	namespaceProps["Math"] = map[string]any{"PI": math.Pi, "E": math.E}

	dateConstructor := func(name string, bounds arity, make func(*evaluator, []any) (dateValue, error)) {
		namespace("DateTime", name, bounds, func(e *evaluator, args []any) (any, error) {
			value, err := make(e, args)
			if err != nil {
				return nil, err
			}
			return value, nil
		})
	}
	dateConstructor("now", fixed(0), func(e *evaluator, _ []any) (dateValue, error) {
		return dateValue{at: e.ctx.clock()}, nil
	})
	dateConstructor("local", fixed(0), func(e *evaluator, _ []any) (dateValue, error) {
		return dateValue{at: e.ctx.clock().In(e.ctx.location())}, nil
	})
	dateConstructor("utc", fixed(0), func(e *evaluator, _ []any) (dateValue, error) {
		return dateValue{at: e.ctx.clock().UTC()}, nil
	})
	dateConstructor("fromISO", oneOrTwo, func(_ *evaluator, args []any) (dateValue, error) {
		text, ok := args[0].(string)
		if !ok {
			return dateValue{}, fmt.Errorf("DateTime.fromISO() needs an ISO string")
		}
		return parseDate(text)
	})
	dateConstructor("fromMillis", fixed(1), func(_ *evaluator, args []any) (dateValue, error) {
		millis, ok := toNumber(args[0])
		if !ok {
			return dateValue{}, fmt.Errorf("DateTime.fromMillis() needs milliseconds")
		}
		return dateValue{at: time.UnixMilli(int64(millis)).UTC()}, nil
	})
	dateConstructor("fromSeconds", fixed(1), func(_ *evaluator, args []any) (dateValue, error) {
		seconds, ok := toNumber(args[0])
		if !ok {
			return dateValue{}, fmt.Errorf("DateTime.fromSeconds() needs seconds")
		}
		return dateValue{at: time.Unix(int64(seconds), 0).UTC()}, nil
	})
	dateConstructor("fromFormat", arity{min: 2, max: 3}, func(e *evaluator, args []any) (dateValue, error) {
		text := jsString(args[0])
		pattern := luxonLayout(jsString(args[1]))
		parsed, err := time.ParseInLocation(pattern, text, e.ctx.location())
		if err != nil {
			return dateValue{}, fmt.Errorf("DateTime.fromFormat(%q, %q) could not read the date", text, jsString(args[1]))
		}
		return dateValue{at: parsed}, nil
	})
	dateConstructor("fromJSDate", fixed(1), func(_ *evaluator, args []any) (dateValue, error) {
		return parseDate(args[0])
	})
}

func isDigitInRadix(char byte, radix int) bool {
	switch {
	case char >= '0' && char <= '9':
		return int(char-'0') < radix
	case char >= 'a' && char <= 'z':
		return int(char-'a')+10 < radix
	case char >= 'A' && char <= 'Z':
		return int(char-'A')+10 < radix
	default:
		return false
	}
}

func needObject(value any, name string) (map[string]any, error) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, nil
	case []any:
		// Object.keys of an array is its indices, which is what JavaScript
		// does and what a workflow walking `Object.keys($json.rows)` expects.
		converted := make(map[string]any, len(typed))
		for index, entry := range typed {
			converted[strconv.Itoa(index)] = entry
		}
		return converted, nil
	case envSource:
		converted := make(map[string]any, len(typed))
		for key, entry := range typed {
			converted[key] = entry
		}
		return converted, nil
	default:
		return nil, fmt.Errorf("%s() needs an object", name)
	}
}

func sortedMapKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	return sortedStrings(keys)
}

// objectKeys is the key order Object.keys/values/entries read a receiver in.
//
// A list is turned into a map keyed by index by needObject, and those keys are
// indices: sorting them as text gives [0,1,10,2,…] for a list of eleven, which
// silently permutes the data a workflow walks. A genuine map keeps the
// deterministic sorted order — Object.keys on a JSON object has no ordering
// guarantee in JavaScript at all, and sorted is the stable choice.
func objectKeys(value any, object map[string]any) []string {
	if list, isList := value.([]any); isList {
		keys := make([]string, len(list))
		for index := range list {
			keys[index] = strconv.Itoa(index)
		}
		return keys
	}
	return sortedMapKeys(object)
}

// checkArity refuses a call that cannot be right before anything runs.
//
// A name that is not on any list is refused here rather than at evaluation, so
// a workflow that names a function this runtime does not have fails at save
// time — and the safety property that `$json.name.exec("rm -rf /")` cannot be
// written at all is preserved by the same check.
func checkArity(name string, args []node) error {
	if name == "" {
		return nil
	}
	count := len(args)
	if entry, found := methods[name]; found {
		return entry.bounds.check(name, count)
	}
	if entry, found := builtins[name]; found {
		return entry.bounds.check(name, count)
	}
	for _, members := range namespaceFuncs {
		if entry, found := members[name]; found {
			return entry.bounds.check(name, count)
		}
	}
	return fmt.Errorf("expression calls %s(), which is not an allowed function", name)
}

func callName(callee node) string {
	switch typed := callee.(type) {
	case memberNode:
		return typed.name
	case identNode:
		return typed.name
	default:
		return ""
	}
}

// encodeComponent is JavaScript's encodeURIComponent: unreserved characters
// pass through, everything else becomes UTF-8 percent escapes.
func encodeComponent(text, unreserved string) string {
	var builder strings.Builder
	for index := 0; index < len(text); index++ {
		char := text[index]
		if isUnreservedByte(char, unreserved) {
			builder.WriteByte(char)
			continue
		}
		fmt.Fprintf(&builder, "%%%02X", char)
	}
	return builder.String()
}

func isUnreservedByte(char byte, unreserved string) bool {
	if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
		return true
	}
	return strings.IndexByte(unreserved, char) >= 0
}

func decodeComponent(text string) (string, error) {
	var builder strings.Builder
	for index := 0; index < len(text); index++ {
		if text[index] != '%' {
			builder.WriteByte(text[index])
			continue
		}
		if index+2 >= len(text) {
			return "", fmt.Errorf("short escape")
		}
		value, err := strconv.ParseUint(text[index+1:index+3], 16, 8)
		if err != nil {
			return "", err
		}
		builder.WriteByte(byte(value))
		index += 2
	}
	return builder.String(), nil
}

// decodeJSON reads JSON text, keeping numbers as float64 so an expression
// cannot tell a parsed number from a literal one.
func decodeJSON(text string) (any, error) {
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return nil, err
	}
	return value, nil
}
