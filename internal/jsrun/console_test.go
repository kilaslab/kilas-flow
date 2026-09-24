package jsrun_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// printed runs a body and returns the text of each console line.
func printed(t *testing.T, source string) []string {
	t.Helper()
	result := mustRun(t, newRunner(), jsrun.Task{Source: source + "\nreturn []"})
	lines := make([]string, len(result.Console))
	for index, line := range result.Console {
		lines[index] = line.Text
	}
	return lines
}

func expectPrinted(t *testing.T, source string, want ...string) {
	t.Helper()
	if got := printed(t, source); !slices.Equal(got, want) {
		t.Errorf("console =\n\t%s\nwant\n\t%s", strings.Join(got, "\n\t"), strings.Join(want, "\n\t"))
	}
}

// An error printed to the console shows its stack as Node's console does,
// in the code's own line numbers: the wrapper around the code, the runtime
// and the libraries under it are not the author's, and their frames are
// left out. A built-in the code called is kept where it sits. Columns are
// goja's, which places a call at its opening parenthesis where V8 places it
// at the callee.
func TestConsoleShowsAnErrorInTheCodesOwnLines(t *testing.T) {
	expectPrinted(t, "function inner() { return new Error('boom') }\nconst error = inner()\nconsole.log(error)",
		"Error: boom\n    at inner (Code:1:27)\n    at Code:2:20")
	expectPrinted(t, "const _ = require('lodash')\ntry { _.map([1], () => { throw new TypeError('in lodash') }) } catch (error) { console.log(error) }",
		"TypeError: in lodash\n    at Code:2:32\n    at Code:2:12")
	expectPrinted(t, "try { JSON.parse('{') } catch (error) { console.log(error) }",
		"SyntaxError: Expected property name or '}' in JSON at position 1 (line 1 column 2)\n    at parse (native)\n    at Code:1:17")
	// Node shows an error with no stack in brackets.
	expectPrinted(t, "const error = new Error('x')\nerror.stack = 'custom'\nconsole.log(error)\nconsole.log(Object.assign(new RangeError('y'), { stack: '' }))",
		"[custom]", "[RangeError: y]")
}

// The numeric placeholders convert as Node's do, a BigInt keeps its n, and a
// Symbol prints NaN rather than throwing. The expected lines are Node 24's.
func TestConsoleFormatsNumbersAsNodeDoes(t *testing.T) {
	expectPrinted(t, strings.Join([]string{
		"console.log('%d %i %d %i', Symbol('s'), Symbol('t'), 5n, 7n)",
		"console.log('%d|%d|%d|%d|%d', [5], new Date(0), null, undefined, -0)",
		"console.log('%i|%i|%i|%i', [5.5], -0, 5n, Symbol())",
		"console.log('%f|%f|%f', Symbol(), 5n, '2.5x')",
		"console.log('%d', { valueOf() { return 7 } })",
	}, "\n"), "NaN NaN 5n 7n", "5|0|0|NaN|-0", "5|0|5n|NaN", "NaN|5|2.5", "7")
}

// Showing an object never runs its getters: Node shows what kind of accessor
// each property is.
func TestConsoleShowsGettersWithoutCallingThem(t *testing.T) {
	expectPrinted(t, strings.Join([]string{
		"let called = false",
		"const value = { get a() { called = true; return 1 }, set b(v) {}, get c() { return 1 }, set c(v) {}, d: 4 }",
		"const list = [1]",
		"Object.defineProperty(list, 1, { get() { called = true; return 2 }, enumerable: true })",
		"console.log(value)",
		"console.log(list)",
		"console.log(called)",
	}, "\n"), "{ a: [Getter], b: [Setter], c: [Getter/Setter], d: 4 }", "[ 1, [Getter] ]", "false")
}

// Typed arrays and Buffers print as Node prints them, and printing a large
// one reads no more of it than is shown. The expected lines are Node 24's.
func TestConsoleShowsTypedArraysAndBuffersAsNodeDoes(t *testing.T) {
	expectPrinted(t, strings.Join([]string{
		"console.log(new Uint8Array([1, 2, 3]), new Uint8Array(0), Buffer.from('hi'), new Float64Array([1.5, -0]), new BigInt64Array([1n]))",
		"console.log(Buffer.alloc(60))",
		"console.log(Buffer.alloc(0))",
		"console.log({ b: Buffer.from('a'), t: new Int8Array([-1]) })",
	}, "\n"),
		"Uint8Array(3) [ 1, 2, 3 ] Uint8Array(0) [] <Buffer 68 69> Float64Array(2) [ 1.5, -0 ] BigInt64Array(1) [ 1n ]",
		"<Buffer "+strings.TrimSpace(strings.Repeat("00 ", 50))+" ... 10 more bytes>",
		"<Buffer >",
		"{ b: <Buffer 61>, t: Int8Array(1) [ -1 ] }")
	lines := printed(t, "console.log(new Uint8Array(2 ** 25))")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "Uint8Array(33554432) [ 0, 0,") || !strings.HasSuffix(lines[0], "... 33554332 more items ]") {
		t.Errorf("console = %.120q, want the first hundred items and a count of the rest", lines)
	}
}

// %j marks a cycle as Node does, and a value too large to write as JSON at
// all is marked rather than failing the code. big only needs to be over the
// characters bound, not built out of many bounded pieces: the check on
// %j's JSON.stringify runs before any of it is escaped, so building it by
// doubling (fast, one copy per step) rather than goja's repeat (one
// character at a time, slow enough alone under the race detector to
// threaten the time limit) keeps this well clear of BUG-k99658.
func TestConsoleJSONPlaceholderMarksCyclesAndTextTooLargeToBuild(t *testing.T) {
	expectPrinted(t, strings.Join([]string{
		"const cyclic = {}",
		"cyclic.self = cyclic",
		"console.log('%j', cyclic)",
		"let big = 'x'",
		"for (let i = 0; i < 26; i++) big = big + big",
		"console.log('%j done', big)",
		"console.log('%j', { a: [1, { b: undefined }] })",
	}, "\n"), "[Circular]", "[JSON too large to show] done", `{"a":[1,{}]}`)
}
