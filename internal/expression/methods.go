package expression

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// arity is how many arguments a callable accepts. max of -1 means any number,
// which is what JavaScript's own variadic methods allow.
type arity struct{ min, max int }

func (bounds arity) check(name string, count int) error {
	switch {
	case count < bounds.min:
		return fmt.Errorf("%s() takes at least %d argument(s), got %d", name, bounds.min, count)
	case bounds.max >= 0 && count > bounds.max:
		return fmt.Errorf("%s() takes at most %d argument(s), got %d", name, bounds.max, count)
	default:
		return nil
	}
}

// method is one entry in the callable surface.
//
// Every name here is a name JavaScript already has (or an n8n extension such as
// toJsonString), and every one of them behaves the way the JavaScript one does.
// Where KilasFlow used to invent a different meaning under the same name — a
// replace that replaced everything, a join that required a separator, a length
// that was a call — the workflow kept running and produced the wrong answer,
// which is worse than refusing.
type method struct {
	name   string
	bounds arity
	// plain is the common shape: a transformation of the receiver and its
	// arguments. withEval is for the methods that run a caller-supplied
	// function, which needs the evaluator to call it.
	plain    func(any, []any) (any, error)
	withEval func(*evaluator, any, []any) (any, error)
}

func (entry method) apply(e *evaluator, receiver any, args []any) (any, error) {
	if entry.withEval != nil {
		return entry.withEval(e, receiver, args)
	}
	return entry.plain(receiver, args)
}

var methods = map[string]method{}

// register adds one method. Duplicates are a programming error, so they panic
// at init rather than shadowing silently.
func register(name string, bounds arity, call func(any, []any) (any, error)) {
	if _, exists := methods[name]; exists {
		panic("expression: duplicate method " + name)
	}
	methods[name] = method{name: name, bounds: bounds, plain: call}
}

// registerE adds a method that needs the evaluator, which is how the list
// methods run an arrow function the workflow supplied.
func registerE(name string, bounds arity, call func(*evaluator, any, []any) (any, error)) {
	if _, exists := methods[name]; exists {
		panic("expression: duplicate method " + name)
	}
	methods[name] = method{name: name, bounds: bounds, withEval: call}
}

func fixed(count int) arity { return arity{min: count, max: count} }

var (
	spaces        = arity{min: 0, max: 2}
	oneOrTwo      = arity{min: 1, max: 2}
	noneOrOne     = arity{min: 0, max: 1}
	listMethod    = arity{min: 1, max: 1}
	anyArguments  = arity{min: 0, max: -1}
	emailPattern  = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	domainPattern = regexp.MustCompile(`(?:https?://)?([A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)+)`)
	urlPattern    = regexp.MustCompile(`(?:https?://|www\.)[^\s"'<>]+`)
	numberPattern = regexp.MustCompile(`-?\d+(?:\.\d+)?`)
)

func init() {
	registerStrings()
	registerLists()
	registerDates()
	registerCommon()
}

func registerStrings() {
	register("toUpperCase", fixed(0), stringMethod("toUpperCase", func(text string, _ []any) (any, error) {
		return strings.ToUpper(text), nil
	}))
	register("toLowerCase", fixed(0), stringMethod("toLowerCase", func(text string, _ []any) (any, error) {
		return strings.ToLower(text), nil
	}))
	register("trim", fixed(0), stringMethod("trim", func(text string, _ []any) (any, error) {
		return strings.TrimSpace(text), nil
	}))
	register("trimStart", fixed(0), stringMethod("trimStart", func(text string, _ []any) (any, error) {
		return strings.TrimLeft(text, " \t\n\r\v\f"), nil
	}))
	register("trimEnd", fixed(0), stringMethod("trimEnd", func(text string, _ []any) (any, error) {
		return strings.TrimRight(text, " \t\n\r\v\f"), nil
	}))
	register("split", spaces, func(receiver any, args []any) (any, error) {
		if text, ok := receiver.(string); ok {
			separator := ""
			if len(args) > 0 && !isNullish(args[0]) {
				separator = jsString(args[0])
			}
			parts := strings.Split(text, separator)
			values := make([]any, 0, len(parts))
			for _, part := range parts {
				values = append(values, part)
			}
			if len(args) > 1 {
				if limit, ok := indexOf(args[1]); ok && limit >= 0 && limit < len(values) {
					values = values[:limit]
				}
			}
			return values, nil
		}
		return nil, fmt.Errorf("split() needs a string")
	})
	// replace replaces the FIRST occurrence, which is what JavaScript does and
	// what the imported expressions assume; replaceAll replaces every one.
	register("replace", fixed(2), stringMethod("replace", func(text string, args []any) (any, error) {
		return strings.Replace(text, jsString(args[0]), jsString(args[1]), 1), nil
	}))
	register("replaceAll", fixed(2), stringMethod("replaceAll", func(text string, args []any) (any, error) {
		search := jsString(args[0])
		if search == "" {
			return text, nil
		}
		return strings.ReplaceAll(text, search, jsString(args[1])), nil
	}))
	register("startsWith", oneOrTwo, stringMethod("startsWith", func(text string, args []any) (any, error) {
		return strings.HasPrefix(text, jsString(args[0])), nil
	}))
	register("endsWith", oneOrTwo, stringMethod("endsWith", func(text string, args []any) (any, error) {
		return strings.HasSuffix(text, jsString(args[0])), nil
	}))
	register("indexOf", oneOrTwo, func(receiver any, args []any) (any, error) {
		switch typed := receiver.(type) {
		case string:
			return float64(strings.Index(typed, jsString(args[0]))), nil
		case []any:
			return float64(indexInList(typed, args[0])), nil
		default:
			return nil, fmt.Errorf("indexOf() needs a string or a list")
		}
	})
	register("lastIndexOf", oneOrTwo, func(receiver any, args []any) (any, error) {
		switch typed := receiver.(type) {
		case string:
			return float64(strings.LastIndex(typed, jsString(args[0]))), nil
		case []any:
			return float64(lastIndexInList(typed, args[0])), nil
		default:
			return nil, fmt.Errorf("lastIndexOf() needs a string or a list")
		}
	})
	register("slice", spaces, func(receiver any, args []any) (any, error) {
		switch typed := receiver.(type) {
		case string:
			start, end := sliceBounds(args, len([]rune(typed)))
			return string([]rune(typed)[start:end]), nil
		case []any:
			start, end := sliceBounds(args, len(typed))
			return append([]any{}, typed[start:end]...), nil
		default:
			return nil, fmt.Errorf("slice() needs a string or a list")
		}
	})
	register("substring", spaces, stringMethod("substring", func(text string, args []any) (any, error) {
		runes := []rune(text)
		start := clampIndex(numberOr(args, 0, 0), len(runes))
		end := clampIndex(numberOr(args, 1, len(runes)), len(runes))
		if start > end {
			start, end = end, start
		}
		return string(runes[start:end]), nil
	}))
	register("charAt", fixed(1), stringMethod("charAt", func(text string, args []any) (any, error) {
		runes := []rune(text)
		position, _ := indexOf(args[0])
		if position < 0 || position >= len(runes) {
			return "", nil
		}
		return string(runes[position]), nil
	}))
	register("at", fixed(1), func(receiver any, args []any) (any, error) {
		switch typed := receiver.(type) {
		case string:
			runes := []rune(typed)
			position, ok := indexOf(args[0])
			if !ok {
				return Undefined, nil
			}
			if position < 0 {
				position += len(runes)
			}
			if position < 0 || position >= len(runes) {
				return Undefined, nil
			}
			return string(runes[position]), nil
		case []any:
			position, ok := indexOf(args[0])
			if !ok {
				return Undefined, nil
			}
			if position < 0 {
				position += len(typed)
			}
			if position < 0 || position >= len(typed) {
				return Undefined, nil
			}
			return typed[position], nil
		default:
			return Undefined, nil
		}
	})
	register("padStart", oneOrTwo, padMethod(true))
	register("padEnd", oneOrTwo, padMethod(false))
	register("repeat", fixed(1), stringMethod("repeat", func(text string, args []any) (any, error) {
		count, _ := indexOf(args[0])
		if count <= 0 {
			return "", nil
		}
		return strings.Repeat(text, count), nil
	}))
	register("includes", oneOrTwo, func(receiver any, args []any) (any, error) {
		switch typed := receiver.(type) {
		case string:
			return strings.Contains(typed, jsString(args[0])), nil
		case []any:
			// SameValueZero, like JavaScript: `[3,1,2,2].includes(2)` is true and
			// `includes('2')` is false. Comparing every entry as text made both
			// answers the opposite of n8n's.
			for _, entry := range typed {
				if sameValueZero(entry, args[0]) {
					return true, nil
				}
			}
			return false, nil
		default:
			return nil, fmt.Errorf("includes() needs a string or a list")
		}
	})
	register("match", fixed(1), stringMethod("match", func(text string, args []any) (any, error) {
		pattern, err := regexp.Compile(jsString(args[0]))
		if err != nil {
			return nil, fmt.Errorf("match() pattern is not a valid regular expression")
		}
		found := pattern.FindStringSubmatch(text)
		if found == nil {
			return nil, nil
		}
		values := make([]any, len(found))
		for index, entry := range found {
			values[index] = entry
		}
		return values, nil
	}))
}

func padMethod(start bool) func(any, []any) (any, error) {
	return stringMethod("pad", func(text string, args []any) (any, error) {
		length, _ := indexOf(args[0])
		padding := " "
		if len(args) > 1 && !isNullish(args[1]) {
			padding = jsString(args[1])
		}
		if padding == "" || len([]rune(text)) >= length {
			return text, nil
		}
		needed := length - len([]rune(text))
		pad := strings.Repeat(padding, needed/len([]rune(padding))+1)
		pad = string([]rune(pad)[:needed])
		if start {
			return pad + text, nil
		}
		return text + pad, nil
	})
}

func registerLists() {
	register("join", noneOrOne, func(receiver any, args []any) (any, error) {
		list, ok := receiver.([]any)
		if !ok {
			return nil, fmt.Errorf("join() needs a list")
		}
		// JavaScript's default separator is a comma. n8n's `join()` — no
		// argument — is the same, and KilasFlow refused it as an arity error.
		return joinList(list, jsString(firstOr(args, ","))), nil
	})
	registerE("map", listMethod, func(e *evaluator, receiver any, args []any) (any, error) {
		list, err := needList(receiver, "map")
		if err != nil {
			return nil, err
		}
		fn, err := needClosure(args[0], "map")
		if err != nil {
			return nil, err
		}
		values := make([]any, 0, len(list))
		for index, entry := range list {
			value, err := e.callClosure(fn, []any{entry, float64(index), list})
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return values, nil
	})
	registerE("filter", listMethod, func(e *evaluator, receiver any, args []any) (any, error) {
		list, err := needList(receiver, "filter")
		if err != nil {
			return nil, err
		}
		fn, err := needClosure(args[0], "filter")
		if err != nil {
			return nil, err
		}
		values := make([]any, 0, len(list))
		for index, entry := range list {
			keep, err := e.callClosure(fn, []any{entry, float64(index), list})
			if err != nil {
				return nil, err
			}
			if truthy(keep) {
				values = append(values, entry)
			}
		}
		return values, nil
	})
	registerE("find", listMethod, func(e *evaluator, receiver any, args []any) (any, error) {
		list, err := needList(receiver, "find")
		if err != nil {
			return nil, err
		}
		fn, err := needClosure(args[0], "find")
		if err != nil {
			return nil, err
		}
		for index, entry := range list {
			found, err := e.callClosure(fn, []any{entry, float64(index), list})
			if err != nil {
				return nil, err
			}
			if truthy(found) {
				return entry, nil
			}
		}
		return Undefined, nil
	})
	registerE("findIndex", listMethod, func(e *evaluator, receiver any, args []any) (any, error) {
		list, err := needList(receiver, "findIndex")
		if err != nil {
			return nil, err
		}
		fn, err := needClosure(args[0], "findIndex")
		if err != nil {
			return nil, err
		}
		for index, entry := range list {
			found, err := e.callClosure(fn, []any{entry, float64(index), list})
			if err != nil {
				return nil, err
			}
			if truthy(found) {
				return float64(index), nil
			}
		}
		return float64(-1), nil
	})
	registerE("some", listMethod, listPredicate(true))
	registerE("every", listMethod, listPredicate(false))
	registerE("reduce", oneOrTwo, func(e *evaluator, receiver any, args []any) (any, error) {
		list, err := needList(receiver, "reduce")
		if err != nil {
			return nil, err
		}
		fn, err := needClosure(args[0], "reduce")
		if err != nil {
			return nil, err
		}
		var accumulator any = Undefined
		start := 0
		if len(args) > 1 {
			accumulator = args[1]
		} else if len(list) > 0 {
			accumulator = list[0]
			start = 1
		}
		for index := start; index < len(list); index++ {
			value, err := e.callClosure(fn, []any{accumulator, list[index], float64(index), list})
			if err != nil {
				return nil, err
			}
			accumulator = value
		}
		return accumulator, nil
	})
	register("flat", noneOrOne, func(receiver any, args []any) (any, error) {
		list, err := needList(receiver, "flat")
		if err != nil {
			return nil, err
		}
		return flatten(list, 1), nil
	})
	registerE("flatMap", listMethod, func(e *evaluator, receiver any, args []any) (any, error) {
		mapped, err := methods["map"].apply(e, receiver, args)
		if err != nil {
			return nil, err
		}
		list, _ := mapped.([]any)
		return flatten(list, 1), nil
	})
	register("reverse", fixed(0), func(receiver any, _ []any) (any, error) {
		list, err := needList(receiver, "reverse")
		if err != nil {
			return nil, err
		}
		reversed := make([]any, len(list))
		for index, entry := range list {
			reversed[len(list)-1-index] = entry
		}
		return reversed, nil
	})
	register("concat", anyArguments, func(receiver any, args []any) (any, error) {
		if text, ok := receiver.(string); ok {
			var builder strings.Builder
			builder.WriteString(text)
			for _, argument := range args {
				builder.WriteString(jsString(argument))
			}
			return builder.String(), nil
		}
		list, err := needList(receiver, "concat")
		if err != nil {
			return nil, err
		}
		joined := append([]any{}, list...)
		for _, argument := range args {
			if nested, ok := argument.([]any); ok {
				joined = append(joined, nested...)
				continue
			}
			joined = append(joined, argument)
		}
		return joined, nil
	})
	registerE("sort", noneOrOne, func(e *evaluator, receiver any, args []any) (any, error) {
		list, err := needList(receiver, "sort")
		if err != nil {
			return nil, err
		}
		sorted := append([]any{}, list...)
		if len(args) == 0 || isNullish(args[0]) {
			// With no comparator the order is JavaScript's default: by the text
			// of each element, which is what an imported workflow expects.
			sort.SliceStable(sorted, func(left, right int) bool {
				return jsString(sorted[left]) < jsString(sorted[right])
			})
			return sorted, nil
		}
		// A comparator is a caller-supplied function, so it runs through the
		// evaluator and the sign of its result decides the order. Accepting the
		// argument and then sorting by the default order returned a differently
		// ordered list that the next node consumed as data.
		compare, err := needClosure(args[0], "sort")
		if err != nil {
			return nil, err
		}
		var failure error
		sort.SliceStable(sorted, func(left, right int) bool {
			if failure != nil {
				return false
			}
			result, err := e.callClosure(compare, []any{sorted[left], sorted[right]})
			if err != nil {
				failure = err
				return false
			}
			// A comparator that answers with something that is not a number
			// orders the pair as equal, which is JavaScript's own reading of
			// NaN and keeps the sort stable.
			order, _ := toNumber(result)
			return order < 0
		})
		if failure != nil {
			return nil, failure
		}
		return sorted, nil
	})
	register("first", fixed(0), func(receiver any, _ []any) (any, error) {
		list, ok := listFrom(receiver)
		if !ok {
			return nil, fmt.Errorf("first() needs a list or a node")
		}
		if len(list) == 0 {
			return Undefined, nil
		}
		return list[0], nil
	})
	register("last", fixed(0), func(receiver any, _ []any) (any, error) {
		list, ok := listFrom(receiver)
		if !ok {
			return nil, fmt.Errorf("last() needs a list or a node")
		}
		if len(list) == 0 {
			return Undefined, nil
		}
		return list[len(list)-1], nil
	})
	register("all", fixed(0), func(receiver any, _ []any) (any, error) {
		list, ok := listFrom(receiver)
		if !ok {
			return nil, fmt.Errorf("all() needs a list or a node")
		}
		return list, nil
	})
	register("sum", fixed(0), func(receiver any, _ []any) (any, error) {
		list, err := needList(receiver, "sum")
		if err != nil {
			return nil, err
		}
		total := 0.0
		for _, entry := range list {
			number, _ := toNumber(entry)
			total += number
		}
		return total, nil
	})
	// removeDuplicates and unique are n8n's names for the same idea.
	removeDuplicates := func(receiver any, _ []any) (any, error) {
		list, err := needList(receiver, "removeDuplicates")
		if err != nil {
			return nil, err
		}
		unique := make([]any, 0, len(list))
		for _, entry := range list {
			duplicate := false
			for _, seen := range unique {
				if sameValueZero(seen, entry) {
					duplicate = true
					break
				}
			}
			if !duplicate {
				unique = append(unique, entry)
			}
		}
		return unique, nil
	}
	register("removeDuplicates", fixed(0), removeDuplicates)
	register("unique", fixed(0), removeDuplicates)
	register("count", fixed(0), func(receiver any, _ []any) (any, error) {
		list, err := needList(receiver, "count")
		if err != nil {
			return nil, err
		}
		return float64(len(list)), nil
	})
}

func listPredicate(want bool) func(*evaluator, any, []any) (any, error) {
	return func(e *evaluator, receiver any, args []any) (any, error) {
		list, err := needList(receiver, "some")
		if err != nil {
			return nil, err
		}
		fn, err := needClosure(args[0], "some")
		if err != nil {
			return nil, err
		}
		for index, entry := range list {
			value, err := e.callClosure(fn, []any{entry, float64(index), list})
			if err != nil {
				return nil, err
			}
			if truthy(value) == want {
				return want, nil
			}
		}
		return !want, nil
	}
}

func registerDates() {
	format := func(receiver any, args []any, name string) (any, error) {
		date, err := needDate(receiver, name)
		if err != nil {
			return nil, err
		}
		return luxonFormat(date.at, jsString(args[0])), nil
	}
	register("format", fixed(1), func(receiver any, args []any) (any, error) {
		return format(receiver, args, "format")
	})
	register("toFormat", fixed(1), func(receiver any, args []any) (any, error) {
		return format(receiver, args, "toFormat")
	})
	register("plus", oneOrTwo, dateShift(+1))
	register("minus", oneOrTwo, dateShift(-1))
	register("plusDays", fixed(1), dayShift(+1))
	register("minusDays", fixed(1), dayShift(-1))
	register("startOf", noneOrOne, func(receiver any, args []any) (any, error) {
		date, err := needDate(receiver, "startOf")
		if err != nil {
			return nil, err
		}
		unit := "millisecond"
		if len(args) > 0 && !isNullish(args[0]) {
			unit = jsString(args[0])
		}
		return dateValue{at: boundOf(date.at, unit, false)}, nil
	})
	register("endOf", noneOrOne, func(receiver any, args []any) (any, error) {
		date, err := needDate(receiver, "endOf")
		if err != nil {
			return nil, err
		}
		unit := "millisecond"
		if len(args) > 0 && !isNullish(args[0]) {
			unit = jsString(args[0])
		}
		return dateValue{at: boundOf(date.at, unit, true)}, nil
	})
	register("toISO", fixed(0), func(receiver any, _ []any) (any, error) {
		date, err := needDate(receiver, "toISO")
		if err != nil {
			return nil, err
		}
		return date.String(), nil
	})
	register("toISODate", fixed(0), func(receiver any, _ []any) (any, error) {
		date, err := needDate(receiver, "toISODate")
		if err != nil {
			return nil, err
		}
		return date.at.Format("2006-01-02"), nil
	})
	register("toISOTime", fixed(0), func(receiver any, _ []any) (any, error) {
		date, err := needDate(receiver, "toISOTime")
		if err != nil {
			return nil, err
		}
		return date.at.Format("15:04:05.000-07:00"), nil
	})
	// toISOString is JavaScript's Date method: UTC, milliseconds, `Z`.
	register("toISOString", fixed(0), func(receiver any, _ []any) (any, error) {
		date, err := needDate(receiver, "toISOString")
		if err != nil {
			return nil, err
		}
		return date.at.UTC().Format("2006-01-02T15:04:05.000Z"), nil
	})
	register("toMillis", fixed(0), func(receiver any, _ []any) (any, error) {
		date, err := needDate(receiver, "toMillis")
		if err != nil {
			return nil, err
		}
		return float64(date.at.UnixMilli()), nil
	})
	register("toSeconds", fixed(0), func(receiver any, _ []any) (any, error) {
		date, err := needDate(receiver, "toSeconds")
		if err != nil {
			return nil, err
		}
		return float64(date.at.Unix()), nil
	})
	register("valueOf", fixed(0), func(receiver any, _ []any) (any, error) {
		date, err := needDate(receiver, "valueOf")
		if err != nil {
			return nil, err
		}
		return float64(date.at.UnixMilli()), nil
	})
	// toJSON, toUnixInteger and toObject are Luxon's own names for values the
	// methods above already produce.
	register("toJSON", fixed(0), func(receiver any, _ []any) (any, error) {
		date, err := needDate(receiver, "toJSON")
		if err != nil {
			return nil, err
		}
		return date.String(), nil
	})
	register("toUnixInteger", fixed(0), func(receiver any, _ []any) (any, error) {
		date, err := needDate(receiver, "toUnixInteger")
		if err != nil {
			return nil, err
		}
		return float64(date.at.Unix()), nil
	})
	register("toObject", fixed(0), func(receiver any, _ []any) (any, error) {
		date, err := needDate(receiver, "toObject")
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"year":        float64(date.at.Year()),
			"month":       float64(date.at.Month()),
			"day":         float64(date.at.Day()),
			"hour":        float64(date.at.Hour()),
			"minute":      float64(date.at.Minute()),
			"second":      float64(date.at.Second()),
			"millisecond": float64(date.at.Nanosecond() / int(time.Millisecond)),
		}, nil
	})
	register("toJSDate", fixed(0), func(receiver any, _ []any) (any, error) {
		date, err := needDate(receiver, "toJSDate")
		if err != nil {
			return nil, err
		}
		return date.at, nil
	})
	register("toUTC", arity{min: 0, max: 2}, func(receiver any, args []any) (any, error) {
		date, err := needDate(receiver, "toUTC")
		if err != nil {
			return nil, err
		}
		_ = args
		return dateValue{at: date.at.UTC()}, nil
	})
	register("setZone", oneOrTwo, func(receiver any, args []any) (any, error) {
		date, err := needDate(receiver, "setZone")
		if err != nil {
			return nil, err
		}
		zone := jsString(args[0])
		location, err := time.LoadLocation(zone)
		if err != nil {
			return nil, fmt.Errorf("setZone(%q) is not a known IANA zone", zone)
		}
		return dateValue{at: date.at.In(location)}, nil
	})
	register("diff", oneOrTwo, func(receiver any, args []any) (any, error) {
		date, err := needDate(receiver, "diff")
		if err != nil {
			return nil, err
		}
		other, err := parseDate(args[0])
		if err != nil {
			return nil, err
		}
		unit := "milliseconds"
		if len(args) > 1 {
			unit = jsString(args[1])
		}
		return float64(unitDuration(date.at.Sub(other.at), unit)), nil
	})
	register("isValid", fixed(0), func(receiver any, _ []any) (any, error) {
		return !isNullish(receiver), nil
	})
}

func registerCommon() {
	register("toString", fixed(0), func(receiver any, _ []any) (any, error) {
		return jsString(receiver), nil
	})
	register("toNumber", fixed(0), func(receiver any, _ []any) (any, error) {
		number, ok := toNumber(receiver)
		if !ok {
			return math.NaN(), nil
		}
		return number, nil
	})
	register("toBoolean", fixed(0), func(receiver any, _ []any) (any, error) {
		return truthy(receiver), nil
	})
	register("toFixed", oneOrTwo, func(receiver any, args []any) (any, error) {
		number, ok := toNumber(receiver)
		if !ok {
			return nil, fmt.Errorf("toFixed() needs a number")
		}
		digits, _ := indexOf(args[0])
		if digits < 0 || digits > 100 {
			return nil, fmt.Errorf("toFixed() needs a digit count between 0 and 100")
		}
		return fixedDecimal(number, digits), nil
	})
	// toJsonString is n8n's extension: the object as JSON text, which is how a
	// workflow builds an LLM prompt or an API body out of a value.
	register("toJsonString", noneOrOne, func(receiver any, args []any) (any, error) {
		indent := false
		if len(args) > 0 {
			indent = truthy(args[0])
		}
		if indent {
			encoded, err := jsonEncodeIndent(receiver, "  ")
			if err != nil {
				return nil, fmt.Errorf("toJsonString() cannot encode this value")
			}
			return encoded, nil
		}
		encoded, err := jsonEncode(receiver)
		if err != nil {
			return nil, fmt.Errorf("toJsonString() cannot encode this value")
		}
		return encoded, nil
	})
	register("toDateTime", noneOrOne, func(receiver any, _ []any) (any, error) {
		date, err := parseDate(receiver)
		if err != nil {
			return nil, fmt.Errorf("toDateTime() needs a date, an ISO string or epoch milliseconds: %w", err)
		}
		return date, nil
	})
	register("isEmpty", fixed(0), func(receiver any, _ []any) (any, error) {
		switch typed := receiver.(type) {
		case nil, undefinedValue:
			return true, nil
		case string:
			return typed == "", nil
		case []any:
			return len(typed) == 0, nil
		case map[string]any:
			return len(typed) == 0, nil
		default:
			return false, nil
		}
	})
	register("isNumeric", fixed(0), func(receiver any, _ []any) (any, error) {
		if _, ok := receiver.(float64); ok {
			return true, nil
		}
		if text, ok := receiver.(string); ok {
			_, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
			return err == nil, nil
		}
		return false, nil
	})
	register("extractEmail", fixed(0), func(receiver any, _ []any) (any, error) {
		return firstMatch(emailPattern, receiver, "extractEmail")
	})
	register("extractDomain", fixed(0), func(receiver any, _ []any) (any, error) {
		return firstMatch(domainPattern, receiver, "extractDomain")
	})
	register("extractUrl", fixed(0), func(receiver any, _ []any) (any, error) {
		return firstMatch(urlPattern, receiver, "extractUrl")
	})
	register("extractNumber", fixed(0), func(receiver any, _ []any) (any, error) {
		found, err := firstMatch(numberPattern, receiver, "extractNumber")
		if err != nil || found == nil {
			return found, err
		}
		number, err := strconv.ParseFloat(found.(string), 64)
		if err != nil {
			return Undefined, nil
		}
		return number, nil
	})
}

func firstMatch(pattern *regexp.Regexp, receiver any, name string) (any, error) {
	text, ok := receiver.(string)
	if !ok {
		return nil, fmt.Errorf("%s() needs a string", name)
	}
	found := pattern.FindStringSubmatch(text)
	if found == nil {
		return nil, nil
	}
	if len(found) > 1 {
		return found[1], nil
	}
	return found[0], nil
}

func stringMethod(name string, apply func(string, []any) (any, error)) func(any, []any) (any, error) {
	return func(receiver any, args []any) (any, error) {
		text, ok := receiver.(string)
		if !ok {
			return nil, fmt.Errorf("%s() needs a string", name)
		}
		return apply(text, args)
	}
}

func needList(receiver any, name string) ([]any, error) {
	list, ok := receiver.([]any)
	if !ok {
		return nil, fmt.Errorf("%s() needs a list", name)
	}
	return list, nil
}

func needClosure(value any, name string) (closure, error) {
	fn, ok := value.(closure)
	if !ok {
		return closure{}, fmt.Errorf("%s() needs a function such as item => item.name", name)
	}
	return fn, nil
}

func needDate(receiver any, name string) (dateValue, error) {
	switch typed := receiver.(type) {
	case dateValue:
		return typed, nil
	case time.Time:
		return dateValue{at: typed}, nil
	default:
		return dateValue{}, fmt.Errorf("%s() needs a date such as $now, DateTime.now() or .toDateTime()", name)
	}
}

// dateShift is Luxon's plus/minus: a duration object (`{days: 1}`) or a number
// with a unit (`plus(1, 'day')`). Both spellings are in imported workflows.
func dateShift(sign int) func(any, []any) (any, error) {
	return func(receiver any, args []any) (any, error) {
		name := "plus"
		if sign < 0 {
			name = "minus"
		}
		date, err := needDate(receiver, name)
		if err != nil {
			return nil, err
		}
		duration, ok := args[0].(map[string]any)
		if !ok {
			amount, _ := toNumber(args[0])
			unit := "milliseconds"
			if len(args) > 1 {
				unit = jsString(args[1])
			}
			return dateValue{at: shiftBy(date.at, float64(sign)*amount, unit)}, nil
		}
		shifted := date.at
		for unit, value := range duration {
			amount, _ := toNumber(value)
			shifted = shiftBy(shifted, float64(sign)*amount, unit)
		}
		return dateValue{at: shifted}, nil
	}
}

func dayShift(sign int) func(any, []any) (any, error) {
	return func(receiver any, args []any) (any, error) {
		name := "plusDays"
		if sign < 0 {
			name = "minusDays"
		}
		date, err := needDate(receiver, name)
		if err != nil {
			return nil, err
		}
		amount, ok := toNumber(args[0])
		if !ok {
			return nil, fmt.Errorf("%s() needs a number of days", name)
		}
		return dateValue{at: date.at.AddDate(0, 0, sign*int(amount))}, nil
	}
}

func shiftBy(at time.Time, amount float64, unit string) time.Time {
	switch strings.TrimSuffix(strings.ToLower(unit), "s") {
	case "year":
		return at.AddDate(int(amount), 0, 0)
	case "quarter":
		return at.AddDate(0, 3*int(amount), 0)
	case "month":
		return at.AddDate(0, int(amount), 0)
	case "week":
		return at.AddDate(0, 0, 7*int(amount))
	case "day", "date":
		return at.AddDate(0, 0, int(amount))
	case "hour":
		return at.Add(time.Duration(amount * float64(time.Hour)))
	case "minute":
		return at.Add(time.Duration(amount * float64(time.Minute)))
	case "second":
		return at.Add(time.Duration(amount * float64(time.Second)))
	case "millisecond":
		return at.Add(time.Duration(amount * float64(time.Millisecond)))
	default:
		return at.AddDate(0, 0, int(amount))
	}
}

// boundOf is Luxon's startOf/endOf. Weeks start on Monday, as Luxon's do.
func boundOf(at time.Time, unit string, end bool) time.Time {
	location := at.Location()
	day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, location)
	switch strings.TrimSuffix(strings.ToLower(unit), "s") {
	case "year":
		day = time.Date(at.Year(), 1, 1, 0, 0, 0, 0, location)
		if end {
			return time.Date(at.Year(), 12, 31, 23, 59, 59, int(999*time.Millisecond), location)
		}
		return day
	case "quarter":
		firstMonth := time.Month((int(at.Month())-1)/3*3 + 1)
		start := time.Date(at.Year(), firstMonth, 1, 0, 0, 0, 0, location)
		if end {
			return start.AddDate(0, 3, 0).Add(-time.Millisecond)
		}
		return start
	case "month":
		start := time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, location)
		if end {
			return start.AddDate(0, 1, 0).Add(-time.Millisecond)
		}
		return start
	case "week":
		offset := (int(at.Weekday()) + 6) % 7
		start := day.AddDate(0, 0, -offset)
		if end {
			return start.AddDate(0, 0, 7).Add(-time.Millisecond)
		}
		return start
	case "day":
		if end {
			return day.AddDate(0, 0, 1).Add(-time.Millisecond)
		}
		return day
	case "hour":
		hour := time.Date(at.Year(), at.Month(), at.Day(), at.Hour(), 0, 0, 0, location)
		if end {
			return hour.Add(time.Hour).Add(-time.Millisecond)
		}
		return hour
	case "minute":
		minute := time.Date(at.Year(), at.Month(), at.Day(), at.Hour(), at.Minute(), 0, 0, location)
		if end {
			return minute.Add(time.Minute).Add(-time.Millisecond)
		}
		return minute
	case "second":
		second := time.Date(at.Year(), at.Month(), at.Day(), at.Hour(), at.Minute(), at.Second(), 0, location)
		if end {
			return second.Add(time.Second).Add(-time.Millisecond)
		}
		return second
	default:
		if end {
			return at
		}
		return at.Truncate(time.Millisecond)
	}
}

func unitDuration(duration time.Duration, unit string) float64 {
	switch strings.TrimSuffix(strings.ToLower(unit), "s") {
	case "year":
		return duration.Hours() / (24 * 365)
	case "quarter":
		return duration.Hours() / (24 * 91)
	case "month":
		return duration.Hours() / (24 * 30)
	case "week":
		return duration.Hours() / (24 * 7)
	case "day":
		return duration.Hours() / 24
	case "hour":
		return duration.Hours()
	case "minute":
		return duration.Minutes()
	case "second":
		return duration.Seconds()
	default:
		return float64(duration.Milliseconds())
	}
}

// sameValueZero is the comparison Array.prototype.includes uses: strict
// equality, except that NaN matches NaN.
func sameValueZero(left, right any) bool {
	if strictlyEqual(left, right) {
		return true
	}
	leftNumber, leftIsNumber := left.(float64)
	rightNumber, rightIsNumber := right.(float64)
	return leftIsNumber && rightIsNumber && math.IsNaN(leftNumber) && math.IsNaN(rightNumber)
}

func indexInList(list []any, value any) int {
	for index, entry := range list {
		if sameValueZero(entry, value) {
			return index
		}
	}
	return -1
}

func lastIndexInList(list []any, value any) int {
	for index := len(list) - 1; index >= 0; index-- {
		if sameValueZero(list[index], value) {
			return index
		}
	}
	return -1
}

func joinList(list []any, separator string) string {
	parts := make([]string, len(list))
	for index, entry := range list {
		parts[index] = jsString(entry)
	}
	return strings.Join(parts, separator)
}

func flatten(list []any, depth int) []any {
	values := []any{}
	for _, entry := range list {
		if nested, ok := entry.([]any); ok && depth > 0 {
			values = append(values, flatten(nested, depth-1)...)
			continue
		}
		values = append(values, entry)
	}
	return values
}

func sliceBounds(args []any, length int) (int, int) {
	start := clampIndex(numberOr(args, 0, 0), length)
	end := clampIndex(numberOr(args, 1, length), length)
	if start > end {
		start = end
	}
	return start, end
}

// clampIndex applies JavaScript's negative-index rule: -1 is the last element,
// and anything beyond either end clamps.
func clampIndex(index, length int) int {
	if index < 0 {
		index += length
	}
	if index < 0 {
		return 0
	}
	if index > length {
		return length
	}
	return index
}

func numberOr(args []any, position, fallback int) int {
	if position >= len(args) || isNullish(args[position]) {
		return fallback
	}
	value, _ := toNumber(args[position])
	if value != math.Trunc(value) {
		return fallback
	}
	return int(value)
}

func firstOr(args []any, fallback string) string {
	if len(args) == 0 || isNullish(args[0]) {
		return fallback
	}
	return jsString(args[0])
}

// FunctionNames is the callable surface, served to the editor so the client
// does not keep a second copy that drifts from this one.
func FunctionNames() []string {
	seen := make(map[string]bool, len(methods)+len(builtins))
	names := make([]string, 0, len(methods)+len(builtins))
	add := func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}
	for name := range methods {
		add(name)
	}
	for name := range builtins {
		add(name)
	}
	// Namespace members are part of the accepted surface too — JSON.stringify,
	// Object.keys, Math.max, DateTime.fromISO — so a client that validates
	// against this list accepts what the server accepts.
	for _, members := range namespaceFuncs {
		for name := range members {
			add(name)
		}
	}
	for _, root := range Roots() {
		if IsCallableRoot(root) {
			add(root)
		}
	}
	return sortedStrings(names)
}

// listFrom accepts a plain list, a node wrapper or an input port.
//
// `$('Name').first()` reads a node while `$json.tags.first()` reads a list, and
// both are forms an imported workflow uses. A node carries its items under the
// key nodeItemsKey, so unwrapping it here keeps one function serving both
// rather than two spellings of the same idea.
func listFrom(receiver any) ([]any, bool) {
	switch typed := receiver.(type) {
	case []any:
		return typed, true
	case map[string]any:
		if list, ok := typed[nodeItemsKey].([]any); ok {
			return list, true
		}
		return nil, false
	case inputSource:
		return typed.all(), true
	default:
		return nil, false
	}
}
