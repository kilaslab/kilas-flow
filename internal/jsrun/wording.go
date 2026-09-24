package jsrun

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// The TypeErrors goja throws, in V8's words (BUG-jwhj6y).
//
// A user moving a workflow from n8n should read the same words for the same
// mistake, and code that tests error.message should take the same branch.
// goja words these errors its own way and gives no hook where they are made,
// so js/modules/errors.js rewrites one where the code can first read it: at
// the start of each catch clause in the code (the compiled body calls it
// there, see instrumentCatches) and on the way out of an uncaught error. The
// words come from here.
//
// Only what can be said faithfully is reworded:
//
//   - a property read of undefined. goja never tells a null base from an
//     undefined one; it says "undefined" for both (and "undefined or null"
//     when the read is a method's), so the V8 wording says undefined, as it
//     does in the far commoner case, and a null base reads "of undefined"
//     where V8 says "of null";
//   - a call or `new` of what is not a function or a constructor, where V8
//     names the callee by its source text. The text comes from the parsed
//     body (see calleeText), for the forms V8's printing was recorded for;
//     any other form keeps goja's words.
//
// What stays goja's is listed in BUG-jwhj6y and pinned by keptWording in
// wording_test.go.
//
// Only errors the engine made are reworded, never one the code built itself,
// even with the same words (Node 14 and earlier said "Cannot read property
// 'x' of undefined" too, so older code may throw exactly that). goja gives
// no mark on the errors it makes, so the error's innermost frame tells them
// apart: an error the code built was made where the code called a
// constructor, which is a native frame (`TypeError(…)`) or a `new`, or a
// call of an error constructor or Reflect.construct, and the engine never
// reports a failed property read or call at any of those. An error built
// through an alias or a subclass of an error constructor, with one of goja's
// "is not a function" messages, is the one case this cannot tell.

// callSite is a call or `new` in the code, by the line and column (in the
// wrapped text) goja reports an error there at: a call's opening
// parenthesis, or a new's keyword.
type callSite struct {
	// text is the callee as V8 prints it, such as `items.map` or `f(...).q`,
	// or "" when V8's printing of it was not recorded.
	text string
	// construct says it is a `new`.
	construct bool
	// buildsError says the callee is an error constructor, or
	// Reflect.construct: what it throws, the code built.
	buildsError bool
}

// errorConstructors are the built-in error constructors, by name.
var errorConstructors = map[string]bool{
	"Error": true, "TypeError": true, "RangeError": true, "SyntaxError": true, "ReferenceError": true,
	"EvalError": true, "URIError": true, "AggregateError": true,
}

type position struct{ line, column int }

var (
	readOfUndefined = regexp.MustCompile(`^Cannot read property '(.*)' of undefined(?: or null)?$`)
	// firstFrame is the innermost frame of a stack, when it is the user's
	// code: "at Code:3:14(8)" or "at name (Code:3:14(8))".
	firstFrame = regexp.MustCompile(`^\n\s*at (?:[^\n]* \()?` + sourceName + `:(\d+):(\d+)\(\d+\)\)?(?:\n|$)`)
	// nativeFrame is an innermost frame in a built-in.
	nativeFrame = regexp.MustCompile(`^\n\s*at [^\n]*\(native\)(?:\n|$)`)
)

// v8Wording is V8's message for one goja threw, or "" when there is none to
// give, or when the code built the error itself. frames is the error's
// stack below its first line.
func v8Wording(message, frames string, sites map[position]callSite) string {
	if nativeFrame.MatchString(frames) {
		return ""
	}
	site, atSite := callSite{}, false
	if match := firstFrame.FindStringSubmatch(frames); match != nil {
		line, _ := strconv.Atoi(match[1])
		column, _ := strconv.Atoi(match[2])
		site, atSite = sites[position{line, column}]
	}
	if atSite && site.buildsError {
		return ""
	}
	if match := readOfUndefined.FindStringSubmatch(message); match != nil {
		if atSite && site.construct {
			return ""
		}
		return "Cannot read properties of undefined (reading '" + match[1] + "')"
	}
	if !atSite || site.text == "" {
		return ""
	}
	switch {
	case site.construct && (message == "Value is not a constructor" || strings.HasPrefix(message, "Value is not an object: ")):
		return site.text + " is not a constructor"
	case !site.construct && (strings.HasPrefix(message, "Object has no member '") || strings.HasPrefix(message, "Value is not an object: ") || strings.HasPrefix(message, "Not a function: ")):
		return site.text + " is not a function"
	}
	return ""
}

// jsNumber is a number as JavaScript's Number.prototype.toString writes it.
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
	case value < 0:
		return "-" + jsNumber(-value)
	}
	// The shortest digits that read back as the value, and the exponent n
	// with value = 0.digits × 10^n.
	scientific := strconv.FormatFloat(value, 'e', -1, 64)
	mantissa, exponent, _ := strings.Cut(scientific, "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	power, _ := strconv.Atoi(exponent)
	n, k := power+1, len(digits)
	switch {
	case k <= n && n <= 21:
		return digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return "0." + strings.Repeat("0", -n) + digits
	}
	sign := "+"
	if n-1 < 0 {
		sign = "-"
	}
	written := digits[:1]
	if k > 1 {
		written += "." + digits[1:]
	}
	return written + "e" + sign + strconv.Itoa(int(math.Abs(float64(n-1))))
}
