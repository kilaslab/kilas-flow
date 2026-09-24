package jsrun_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// goja folds a chain of constant ||, && and ?? at compile time, re-evaluating
// the left side at every level. 40 levels would hold a core for hours, off the
// clock and at save time, and they fit in 147 bytes.
func TestAConstantChainThatFoldsExponentiallyIsRefused(t *testing.T) {
	for _, operator := range []string{"||0", "&&1", "??0"} {
		source := "return [{ json: { x: 0" + strings.Repeat(operator, 40) + " } }]"
		start := time.Now()
		_, err := jsrun.Analyze(source, jsrun.ModeAllItems)
		if !errors.Is(err, jsrun.ErrUnsupported) || !strings.Contains(err.Error(), "constant expression too deeply nested to compile") {
			t.Errorf("%q chain: Analyze() error = %v, want it refused", operator, err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("%q chain: refusing took %v", operator, elapsed)
		}
	}
	// A chain over a variable folds nothing, and real code writes long ones.
	accepted(t, "const x = $input.first().json.kind\nreturn [{ json: { match: x === 'a' || x === 'b' || x === 'c' || x === 'd' || x === 'e' || x === 'f' || x === 'g' || x === 'h' || x === 'i' || x === 'j' || x === 'k' || x === 'l' || x === 'm' || x === 'n' || x === 'o' || x === 'p' || x === 'q' || x === 'r' } }]")
	accepted(t, "return [{ json: { week: 1000 * 60 * 60 * 24 * 7 } }]")
	// Other operators fold in polynomial time, so literals joined with + are
	// ordinary code however many there are.
	accepted(t, "const query = 'SELECT '"+strings.Repeat(" +\n  'column, '", 60)+" + 'id FROM t'\nreturn [{ json: { query } }]")
	accepted(t, "return [{ json: { n: -(-(-(-(1"+strings.Repeat(" + 1", 40)+")))) } }]")
	// Arithmetic between the levels does not hide the chain.
	for _, source := range []string{
		"return [{ json: { x: 0" + strings.Repeat("||0)+0", 20) + " } }]",
		"return [{ json: { x: 1" + strings.Repeat("||a", 40) + " } }]",
	} {
		source = strings.Replace(source, "x: ", "x: "+strings.Repeat("(", strings.Count(source, ")")), 1)
		start := time.Now()
		if _, err := jsrun.Analyze(source, jsrun.ModeAllItems); !errors.Is(err, jsrun.ErrUnsupported) {
			t.Errorf("Analyze(%.60q…) error = %v, want it refused", source, err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("refusing took %v", elapsed)
		}
	}
}

// goja works out constant BigInt arithmetic while compiling, with no limit
// on the size of the numbers, in setup that nothing interrupts: one line
// could take gigabytes or hours. Such a constant is refused, quickly, and a
// real one is not. Analyze never compiles, so validating in the server works
// nothing out at all.
func TestAHugeBigIntConstantIsRefusedWithoutWorkingItOut(t *testing.T) {
	for _, expression := range []string{
		"1n << 68719476736n",
		"3n ** 3000000000n",
		"'' + (1n << 20000000n)",
		"(-(2n ** 64n)) ** 100000n",
		"(1n << 900000n) * (1n << 900000n)",
		"0n || 7n ** 99999999n",
	} {
		start := time.Now()
		_, err := jsrun.Analyze("const x = "+expression+"\nreturn items", jsrun.ModeAllItems)
		if !errors.Is(err, jsrun.ErrUnsupported) || !strings.Contains(err.Error(), "BigInt constant too large") {
			t.Errorf("%s: Analyze() error = %v, want it refused", expression, err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("%s: refusing took %v", expression, elapsed)
		}
	}
	for _, expression := range []string{"2n ** 64n", "(1n << 256n) - 1n", "12345678901234567890n * 98765432109876543210n", "-1n >> 3n"} {
		accepted(t, "const x = "+expression+"\nreturn [{ json: { x: String(x) } }]")
	}
	// Without compiling, Analyze accepts what only the compiler rejects; the
	// run reports it.
	accepted(t, "let twice = 1\nlet twice = 2\nreturn items")
	_, err := newRunner().Run(context.Background(), jsrun.Task{Source: "let twice = 1\nlet twice = 2\nreturn items"})
	var syntax *jsrun.SyntaxError
	if !errors.As(err, &syntax) {
		t.Fatalf("Run() error = %v, want the compiler's SyntaxError", err)
	}
}

// goja passes a constructor's new.target on to every plain call made inside
// it. The stand-ins for RegExp and the typed arrays still tell a call from a
// construction, so a class that calls RegExp() in its constructor gets a
// regular expression, as Luxon's format parser does.
func TestAConstructorCallingRegExpPlainlyGetsARegularExpression(t *testing.T) {
	result, err := runAll(t, newRunner(), "class Parser { constructor() { this.re = RegExp('a+', 'g') } }\n"+
		"class Word extends RegExp {}\n"+
		"const parser = new Parser()\n"+
		"return [{ json: { match: 'caaab'.match(parser.re)[0], isRegExp: parser.re instanceof RegExp, word: new Word('x') instanceof Word, source: new Word('x').source } }]", nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	got := result.Items[0].JSON
	if got["match"] != "aaa" || got["isRegExp"] != true || got["word"] != true || got["source"] != "x" {
		t.Fatalf("got %#v", got)
	}
}

// A single built-in call runs to completion whatever the clock or the
// watchdog say, so the ones that allocate or loop as far as a number tells
// them refuse a huge number up front. Each of these took seconds and
// gigabytes before.
func TestBuiltinsThatAllocateFromANumberAreBounded(t *testing.T) {
	for _, source := range []string{
		"return [{ json: { n: [...Array(2**26).keys()].length } }]",
		"return [{ json: { n: Array.from({ length: 2**26 }).length } }]",
		"return [{ json: { n: new Array(2**26).fill(0).length } }]",
		"return [{ json: { n: Array(2**28).join('x').length } }]",
		"return [{ json: { n: Array.prototype.indexOf.call({ length: 2**50 }, 1) } }]",
		"return [{ json: { n: Math.max.apply(null, { length: 2**30 }) } }]",
		"return [{ json: { n: new Uint8Array(2**30).length } }]",
		"return [{ json: { n: new ArrayBuffer(2**30).byteLength } }]",
		"return [{ json: { n: new Float64Array({ length: 2**28 }).length } }]",
		"return [{ json: { n: 'x'.repeat(2**28).length } }]",
		"return [{ json: { n: ''.padStart(2**28).length } }]",
	} {
		start := time.Now()
		_, err := runAll(t, newRunner(), source, nil)
		if err == nil || !strings.Contains(err.Error(), "RangeError") || !strings.Contains(err.Error(), "one call may handle here") {
			t.Errorf("%q: Run() error = %v, want a RangeError naming the bound", source, err)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("%q: refusing took %v", source, elapsed)
		}
	}
}

// Stringifying a deeply nested array recursed natively with a quadratic cycle
// check: 500,000 levels ran 80 seconds past a 10 second limit. The array
// methods are the code's own calls now, so the depth counts against the
// call-depth limit instead. Nesting twice the default MaxCallDepth trips that
// limit exactly as 500,000 levels did, with a fraction of the building and,
// under the race detector on a loaded machine, none of the time-limit risk a
// wall clock on the whole run would carry (BUG-k99658).
func TestDeeplyNestedArraysCannotHoldTheCPU(t *testing.T) {
	_, err := runAll(t, newRunner(), "let nested = []\nfor (let i = 0; i < 20000; i++) nested = [nested]\nreturn [{ json: { text: String(nested) } }]", nil)
	if !errors.Is(err, jsrun.ErrCallDepth) {
		t.Fatalf("Run() error = %v, want the call-depth limit", err)
	}
}

func TestOrdinaryArrayAndStringWorkIsUnaffected(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: strings.Join([]string{
		"const list = [3, 1, 2]",
		"const sorted = [...list].sort((a, b) => a - b)",
		"const doubled = list.map(n => n * 2).filter(n => n > 2)",
		"const bytes = new Uint8Array([1, 2, 3])",
		"const buffer = new ArrayBuffer(8)",
		"const from = Array.from(new Set([1, 1, 2]))",
		"return [{ json: {",
		"  sorted: sorted.join(','), doubled, from, total: list.reduce((a, b) => a + b, 0),",
		"  padded: '7'.padStart(3, '0'), repeated: 'ab'.repeat(3), max: Math.max.apply(null, list),",
		"  bytes: bytes.length, buffer: buffer.byteLength, typed: bytes instanceof Uint8Array, isArray: Array.isArray(list),",
		"  name: Array.prototype.map.name, spread: [...'héllo'].length, text: String([1, [2, 3]]),",
		"} }]",
	}, "\n")})
	got := result.Items[0].JSON
	for key, want := range map[string]any{
		"sorted": "1,2,3", "total": float64(6), "padded": "007", "repeated": "ababab", "max": float64(3),
		"bytes": float64(3), "buffer": float64(8), "typed": true, "isArray": true, "name": "map", "spread": float64(5), "text": "1,2,3",
	} {
		if got[key] != want {
			t.Errorf("%s = %#v, want %#v", key, got[key], want)
		}
	}
}
