package jsrun_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// tooLargePattern matches the shape every bound in js/runtime.js's tooLarge
// throws: "<what> of <size> is more than the <limit> one call may handle
// here". size is what the check measured before refusing: an exact input
// property (an array's length, a buffer's byte length) for a check that
// reads one number and refuses before ever calling the native built-in, or
// the running total at the moment a check that accumulates as it goes
// (join, replace, JSON.stringify) crossed the limit. A message this doesn't
// match, such as a construct refused for being unsupported rather than
// oversized, carries no such number.
var tooLargePattern = regexp.MustCompile(`of (\d+) is more than the \d+ one call may handle here`)

// materialisedSize reads the size a tooLarge message reports, or false for
// a message that isn't shaped that way.
func materialisedSize(message string) (size uint64, ok bool) {
	m := tooLargePattern.FindStringSubmatch(message)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseUint(m[1], 10, 64)
	return n, err == nil
}

// defaultAllocRatio is how many times the reported size a refusal may
// legitimately allocate before refusedQuickly treats it as materialising
// its output rather than refusing before building it. Calibrated from this
// file's own sources under -race: every one-number check (an array's or a
// buffer's own length, read before the native built-in is ever called)
// costs at most ~2.1x its size, all of it the setup needed to construct a
// legal input that size; the accumulate-as-you-go checks (join, replace,
// JSON.stringify) cost more per byte of running total because building the
// pieces that passed the check before the one that didn't is real,
// necessary work, up to ~8x for this file's ordinary cases. 12x clears both
// with room to spare, while staying far under what materialising the whole
// output first would cost for every one-number check (~40-50x, since each
// of 16.7M elements becomes its own value, not just a byte) — see
// TestArrayFromRefusesBeforeMaterialising for that shown directly.
const defaultAllocRatio = 12

// refusedQuickly runs a body that must fail, with an error whose text
// contains want. The per-call bounds this proves refuse a request up front,
// before doing any of the work a legal call of the same shape would do:
// goja cannot interrupt one built-in call (.pine/memory/code-node.md), so a
// bound that checked only after building its output would still finish and
// still report the same message, just after allocating what it refused to
// allocate. A wall clock can't tell those two apart on a loaded machine
// (BUG-k99658): the elapsed time for either is comfortably under the run's
// own limit either way, since the whole point of these fixtures (BUG-k99658
// fix round 1) is that they're cheap. Bytes allocated is the load-independent
// stand-in — a process's byte counters are moved only by what it itself
// allocates, never by what else the machine is doing — so this measures the
// allocation delta around the run and compares it with defaultAllocRatio (or
// allocRatio, when the call names one: a few checks that accumulate a real
// partial result before detecting the crossing legitimately cost more, see
// their call sites) times the size the refused message reports.
func refusedQuickly(t *testing.T, source, want string, allocRatio ...uint64) {
	t.Helper()
	ratio := uint64(defaultAllocRatio)
	if len(allocRatio) > 0 {
		ratio = allocRatio[0]
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := runAll(t, newRunner(), source, nil)
	runtime.ReadMemStats(&after)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("%q: Run() error = %v, want one saying %q", source, err, want)
		return
	}
	if size, ok := materialisedSize(err.Error()); ok {
		if allocated, ceiling := after.TotalAlloc-before.TotalAlloc, size*ratio; allocated > ceiling {
			t.Errorf("%q: refusing allocated %d bytes, more than %dx the %d the message reports; the bound looks checked after building (some of) the output, not before", source, allocated, ratio, size)
		}
	}
}

// A check that reads a number, and a built-in that reads it again, could be
// told two different numbers by a valueOf. The number is read once and the
// built-in is handed what was read.
func TestALengthIsReadOnceAndHandedOn(t *testing.T) {
	expectJSON(t, strings.Join([]string{
		"let calls = 0",
		"// Each answers small the first time it is asked, and huge after.",
		"function sly(small, huge) {",
		"  let asked = false",
		"  const answer = () => { calls++; const value = asked ? huge : small; asked = true; return value }",
		"  return { valueOf: answer, toString: answer }",
		"}",
		"const repeated = 'x'.repeat(sly(2, 2 ** 30))",
		"const padded = 'x'.padStart(sly(2, 2 ** 30), 'ab')",
		"const buffer = new ArrayBuffer(sly(8, 2 ** 30))",
		"const joined = ['a', 'b'].join(sly('-', 'x'.repeat(2 ** 24)))",
		"return [{ json: { repeated, padded, bytes: buffer.byteLength, joined, calls } }]",
	}, "\n"), map[string]any{"repeated": "xx", "padded": "ax", "bytes": 8, "joined": "a-b", "calls": 4})
}

// Where the length itself can only be read by running the code's own
// functions, the built-in would read it again and could be told something
// else, so the call is refused. Plain array-likes, strings and typed arrays
// are read without running anything, and still work.
func TestALengthTheBuiltinWouldReadAgainIsRefused(t *testing.T) {
	for source, want := range map[string]string{
		"return [{ json: { n: Array.prototype.fill.call({ get length() { return 5 } }, 0) } }]":             "whose length is a getter",
		"return [{ json: { n: Array.prototype.indexOf.call({ length: { valueOf() { return 1 } } }, 1) } }]": "whose length is itself an object",
		"return [{ json: { n: new Proxy([1, 2], {}).map(x => x) } }]":                                       "passes a Proxy to a built-in",
		"return [{ json: { n: Array.from(Proxy.revocable({ length: 2 }, {}).proxy) } }]":                    "passes a Proxy to a built-in",
		"return [{ json: { n: Math.max.apply(null, new Proxy([1], {})) } }]":                                "passes a Proxy to a built-in",
		"return [{ json: { n: new Uint8Array(Object.create({ get length() { return 2 } })) } }]":            "whose length is a getter",
	} {
		refusedQuickly(t, source, "which this server does not run")
		refusedQuickly(t, source, want)
	}
	expectJSON(t, strings.Join([]string{
		"function count() { return Array.prototype.slice.call(arguments).length }",
		"const proxied = new Proxy({ a: 1 }, {})",
		"return [{ json: {",
		"  args: count(1, 2, 3), string: Array.prototype.map.call('ab', c => c + c).join(),",
		"  typed: Array.prototype.join.call(new Uint8Array([1, 2]), '+'), like: Array.from({ length: 2, 0: 'x' }).length,",
		"  proxied: proxied.a, keys: Object.keys(proxied).join(),",
		"} }]",
	}, "\n"), map[string]any{"args": 3, "string": "aa,bb", "typed": "1+2", "like": 2, "proxied": 1, "keys": "a"})
}

// concat spreads every list it is given, so the lists are counted together,
// not only the one it was called on.
func TestConcatCountsEveryList(t *testing.T) {
	refusedQuickly(t, "const a = Array(2 ** 22)\nreturn [{ json: { n: a.concat(a, a).length } }]", "an array length of 12582912")
	refusedQuickly(t, "return [{ json: { n: [].concat({ length: 2 ** 40, [Symbol.isConcatSpreadable]: true }).length } }]", "one call may handle here")
	expectJSON(t, "return [{ json: { n: JSON.stringify([1].concat([2], 3, [[4]], 'ab')) } }]", map[string]any{"n": `[1,2,3,[4],"ab"]`})
}

// Every way back to an unchecked constructor is closed: the constructors'
// own [[Prototype]], an instance's constructor, and a species. What the
// stand-ins answer is what the originals answered.
func TestTheUncheckedConstructorsAreOutOfReach(t *testing.T) {
	expectJSON(t, strings.Join([]string{
		"const TypedArray = Object.getPrototypeOf(Uint8Array)",
		"return [{ json: {",
		"  shared: TypedArray === Object.getPrototypeOf(Int8Array), instance: new Uint8Array(1).constructor === Uint8Array,",
		"  buffer: new ArrayBuffer(1).constructor === ArrayBuffer, regexp: /a/.constructor === RegExp && RegExp.prototype.constructor === RegExp,",
		"  nodeBuffer: Object.getPrototypeOf(Buffer) === Uint8Array, width: Uint8Array.BYTES_PER_ELEMENT + Float64Array.BYTES_PER_ELEMENT,",
		"  isView: ArrayBuffer.isView(new Uint8Array(1)), name: Uint8Array.name + ArrayBuffer.name + RegExp.name,",
		"  sameToString: Uint8Array.prototype.toString === Array.prototype.toString,",
		"} }]",
	}, "\n"), map[string]any{
		"shared": true, "instance": true, "buffer": true, "regexp": true, "nodeBuffer": true, "width": 9,
		"isView": true, "name": "Uint8ArrayArrayBufferRegExp", "sameToString": true,
	})
	for _, source := range []string{
		"return [{ json: { n: new (new Uint8Array(1).constructor)(2 ** 30).length } }]",
		"return [{ json: { n: new (new ArrayBuffer(1).constructor)(2 ** 30).byteLength } }]",
		"return [{ json: { n: Object.getPrototypeOf(Uint8Array).from.call(Uint8Array, { length: 2 ** 30 }).length } }]",
		"return [{ json: { n: Uint8Array.from({ length: 2 ** 30 }).length } }]",
		"return [{ json: { n: new (new Float64Array(4).map(x => x).constructor)(2 ** 30).length } }]",
		"class Big extends Uint8Array {}\nreturn [{ json: { n: new Big(2 ** 30).length } }]",
	} {
		refusedQuickly(t, source, "one call may handle here")
	}
	_, err := runAll(t, newRunner(), "return [{ json: { n: new (/a/.constructor)('\\\\p{L}', 'u').test('é') } }]", nil)
	if err == nil || !strings.Contains(err.Error(), "property escapes") {
		t.Errorf("Run() error = %v, want a regular expression built through /a/.constructor checked too", err)
	}
	expectJSON(t, strings.Join([]string{
		"class Bytes extends Uint8Array { first() { return this[0] } }",
		"const bytes = new Bytes([7, 8])",
		"return [{ json: {",
		"  first: bytes.first(), kind: bytes instanceof Uint8Array && bytes instanceof Bytes, sliced: bytes.slice(1) instanceof Bytes,",
		"  split: 'a1b2c'.split(/\\d/).join(), matched: [...'a1b2'.matchAll(/\\d/g)].length, hex: Buffer.from('hi').toString('hex'),",
		"} }]",
	}, "\n"), map[string]any{"first": 7, "kind": true, "sliced": true, "split": "a,b,c", "matched": 2, "hex": "6869"})
}

// A typed array's first argument, when it is not an object, is a length
// however it is spelled.
func TestATypedArrayLengthThatIsNotAnObjectIsCheckedAsALength(t *testing.T) {
	refusedQuickly(t, "return [{ json: { n: new Uint8Array('1e9').length } }]", "a buffer of bytes of 1000000000")
	refusedQuickly(t, "return [{ json: { n: new Float64Array('   16777216   ').length } }]", "a buffer of bytes of 134217728")
	expectJSON(t, "return [{ json: { n: new Uint8Array('3').length + new Uint8Array(true).length + new Uint8Array(null).length } }]", map[string]any{"n": 4})
}

// A built-in that turns a small input into a large output in one call is
// bounded by the output: a long separator, a template that repeats the rest
// of the string at every match, one large value serialized many times, or a
// typed array's every index as a key.
func TestBuiltinsThatAmplifyTheirInputAreBounded(t *testing.T) {
	for _, source := range []string{
		"return [{ json: { n: Array(2 ** 20).join('x'.repeat(2 ** 10)).length } }]",
		// join('') and toLocaleString have no glue to check up front, so they
		// total each element as they go; a short array of doubled elements
		// crosses the bound in tens of steps instead of the tens of
		// thousands a long array of small ones needs.
		"let big = 'x'\nfor (let i = 0; i < 20; i++) big = big + big\nreturn [{ json: { n: Array(40).fill(big).join('').length } }]",
		"let big = 'x'\nfor (let i = 0; i < 20; i++) big = big + big\nreturn [{ json: { n: Array(40).fill(big).toLocaleString().length } }]",
		"return [{ json: { n: new Uint8Array(2 ** 20).join('x'.repeat(2 ** 10)).length } }]",
		"return [{ json: { n: 'x'.repeat(2 ** 15).replaceAll('x', 'y'.repeat(2 ** 11)).length } }]",
		"return [{ json: { n: 'x'.repeat(2 ** 12).replace(/x/g, 'y'.repeat(2 ** 14)).length } }]",
		// big is built by doubling, not repeat: goja's repeat writes one
		// character at a time, which the race detector slows enough on its
		// own to threaten the time limit (BUG-k99658); doubling is a copy
		// per step and reaches the same 16 MiB in 24 steps.
		"let big = 'x'\nfor (let i = 0; i < 24; i++) big = big + big\nreturn [{ json: { n: JSON.stringify(Array(4).fill(big)).length } }]",
		"let nested = 0\nfor (let n = 0; n < 3000; n++) nested = [nested]\nreturn [{ json: { n: JSON.stringify(nested, null, 10).length } }]",
		// concat's check totals its arguments' lengths before it concatenates
		// anything, so a small legal string spread many times over-refuses
		// without ever building or copying a large one.
		"const s = 'x'.repeat(2 ** 16)\nreturn [{ json: { n: s.concat(...Array(600).fill(s)).length } }]",
	} {
		refusedQuickly(t, source, "a string length of")
	}
	// "$'" names everything after the match, so replacing every character of
	// an all-matching subject makes each replacement almost as long as the
	// subject itself — the classic quadratic replace pattern. Detecting the
	// crossing still means having built the replacements that passed before
	// it, which for this shape is a genuinely larger multiple of the size
	// reported (measured under -race: ~24x, against ~8x for this test's other
	// replace cases), so it gets its own, wider ratio rather than loosening
	// the default for everything else. Object.assign's Echo class exercises
	// the same "a whole match's worth of prior text is real, legitimate
	// output" shape through a custom RegExp subclass.
	refusedQuickly(t, "return [{ json: { n: 'x'.repeat(2 ** 16).replace(/x/g, \"$'\").length } }]", "a string length of", 32)
	refusedQuickly(t, "class Echo extends RegExp { exec(s) { return this.done ? null : (this.done = true, Object.assign(['x'.repeat(2 ** 20)], { index: 0 })) } }\nreturn [{ json: { n: 'x'.replace(new Echo('x'), '$&'.repeat(64)).length } }]", "a string length of", 32)
	for _, source := range []string{
		"return [{ json: { n: Object.keys(new Uint8Array(2 ** 24)).length } }]",
		"let big = 'x'\nfor (let i = 0; i < 24; i++) big = big + big\nreturn [{ json: { n: Object.entries(big).length } }]",
		"return [{ json: { n: Object.assign({}, new Uint8Array(2 ** 24)).length } }]",
	} {
		refusedQuickly(t, source, "a list of keys of 16777216")
	}
	for _, source := range []string{
		"return [{ json: { n: Array.from(new Uint8Array(2 ** 24)).length } }]",
		"return [{ json: { n: [...new Uint8Array(2 ** 24)].length } }]",
		"return [{ json: { n: [...new Uint8Array(2 ** 24).entries()].length } }]",
		"return [{ json: { n: new Uint8Array(2 ** 24).sort()[0] } }]",
		"let big = 'x'\nfor (let i = 0; i < 24; i++) big = big + big\nreturn [{ json: { n: Array.from(big).length } }]",
	} {
		refusedQuickly(t, source, "one call may handle here")
	}
	refusedQuickly(t, "let big = 'x'\nfor (let i = 0; i < 24; i++) big = big + big\nreturn [{ json: { n: [...big].length } }]", "a string to iterate over, with a length of 16777216")
}

// What the bounded built-ins return is what Node 24 returns for the same
// call, including where a replacement template runs through the replacer
// that counts rather than the built-in's own expansion.
func TestBoundedBuiltinsAnswerAsNodeDoes(t *testing.T) {
	for _, entry := range []struct{ expression, want string }{
		{"JSON.stringify({ a: [1, { b: 2 }], c: 'x', d: undefined, e: () => 1, f: Symbol('s') }, null, 2)", "{\n  \"a\": [\n    1,\n    {\n      \"b\": 2\n    }\n  ],\n  \"c\": \"x\"\n}"},
		{"JSON.stringify([undefined, () => 1, Symbol('s'), null, NaN, -0, 1e21])", "[null,null,null,null,null,0,1e+21]"},
		{"JSON.stringify({ a: 1, b: { c: 2, d: [3, 4] } }, (k, v) => typeof v === 'number' ? v * 10 : v)", "{\"a\":10,\"b\":{\"c\":20,\"d\":[30,40]}}"},
		{"JSON.stringify({ b: 1, 1: 2, a: 3, nested: { a: 1, b: 2, 1: 3 } }, ['b', 1, 'a', 'nested', new String('a'), 'b'])", "{\"b\":1,\"1\":2,\"a\":3,\"nested\":{\"b\":2,\"1\":3,\"a\":1}}"},
		{"JSON.stringify({ a: { b: { c: 1 } } }, null, new Number(3))", "{\n   \"a\": {\n      \"b\": {\n         \"c\": 1\n      }\n   }\n}"},
		{"JSON.stringify({ a: [1, 2] }, null, new String('--'))", "{\n--\"a\": [\n----1,\n----2\n--]\n}"},
		{"JSON.stringify({ a: 1 }, null, '0123456789abc')", "{\n0123456789\"a\": 1\n}"},
		{"JSON.stringify({ a: 1, b: [1] }, null, 20)", "{\n          \"a\": 1,\n          \"b\": [\n                    1\n          ]\n}"},
		{"JSON.stringify({ toJSON() { return { x: 1 } } })", "{\"x\":1}"},
		{"JSON.stringify({ d: new Date(0), n: new Number(5), s: new String('s'), b: new Boolean(false) })", "{\"d\":\"1970-01-01T00:00:00.000Z\",\"n\":5,\"s\":\"s\",\"b\":false}"},
		{"JSON.stringify([[], {}, [[]]], null, 1)", "[\n [],\n {},\n [\n  []\n ]\n]"},
		{"JSON.stringify('\\u2028\\ud800\"')", "\" \\ud800\\\"\""},
		{"JSON.stringify({ a: 1 }, ['a'], 2)", "{\n  \"a\": 1\n}"},
		{"JSON.stringify(undefined)", "undefined"},
		{"JSON.stringify([1, [2, [3]]], function (k, v) { return Array.isArray(v) && v.length === 1 ? v[0] : v })", "[1,[2,3]]"},
		{"JSON.stringify([{ a: 1, b: 2 }, [{ a: 3 }]], ['a'])", "[{\"a\":1},[{\"a\":3}]]"},
		{"JSON.stringify({ r: JSON.rawJSON('1e1000'), q: 1 }, ['r'])", "{\"r\":1e1000}"},
		{"(() => { const a = [1, [2, 3]]; a.push(a); return a.join('|') })()", "1|2,3|"},
		{"[null, undefined, 0, false, 'x', [1, [2]]].join()", ",,0,false,x,1,2"},
		{"Array.prototype.join.call({ length: 3, 0: 'a', 2: 'c' }, '-')", "a--c"},
		{"new Float64Array([1.5, -0, NaN]).join(' ')", "1.5 0 NaN"},
		{"[1.5, 'x', null, [2, 3]].toLocaleString()", "1.5,x,,2,3"},
		{"String([1, [2, [3, [4]]]])", "1,2,3,4"},
		{"[1, 2, 3].join(undefined) + '|' + [1, 2].join(null)", "1,2,3|1null2"},
		{"Array.prototype.toString.call({ join: () => 'custom' })", "custom"},
		{"'x'.replace(/(b)?x/, \"[$1|$01|$10|$0|$00|$<n>|$$|$&|$`|$']\")", "[||0|$0|$00|$<n>|$|x||]"},
		{"'axb'.replace(/(?<n>x)/, '[$<n>|$<m>|$<n|$1]')", "a[x||$<n|x]b"},
		{"'aXbXc'.replace(/X/g, '$`')", "aabaXbc"},
		{"(() => { const s = 'x'.repeat(40000) + 'Qyz' + 'w'.repeat(3); return s.replace(/Q(y)(?<n>z)?/, \"[$1|$01|$10|$0|$00|$<n>|$<m>|$<n|$$|$&|$'|$2|$3]\" + \"$'\".repeat(30)).slice(40000) })()", "[y|y|y0|$0|$00|z||$<n|$|Qyz|www|z|$3]wwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwww"},
		{"(() => { const s = 'x'.repeat(40000) + 'Qy'; return s.replace(/Q(y)(?<n>z)?/, \"[$1|$2|$<n>|$&]\" + '$$'.repeat(600)).slice(40000, 40020) })()", "[y|||Qy]$$$$$$$$$$$$"},
		{"(() => { const s = 'aQb' + 'x'.repeat(40000); const r = s.replaceAll('Q', '$$'.repeat(600) + '$&'); return r.slice(0, 3) + r.slice(600, 605) + r.length })()", "a$$$Qbxx40603"},
		{"(() => { const s = 'aXb' + 'y'.repeat(40000); const r = s.replace(/X/g, '$`' + '$$'.repeat(600)); return r.slice(0, 4) + r.slice(600, 606) + r.length })()", "aa$$$$byyy40603"},
		{"(() => { const s = 'x'.repeat(40000) + '2024-01'; return s.replace(/(?<y>\\d+)-(?<m>\\d+)/, '$<m>/$<y>' + '$$'.repeat(600)).slice(40000, 40010) })()", "01/2024$$$"},
		{"'2024-01-02'.replace(/(?<y>\\d+)-(?<m>\\d+)/, '$<m>/$<y>')", "01/2024-02"},
		{"'ab'.replace('b', () => '$&')", "a$&"},
		{"Uint8Array.from({ length: 3 }, (_, i) => i * 2).join()", "0,2,4"},
		{"Array.from({ length: 3 }, (_, i) => i).concat([3], 4).join()", "0,1,2,3,4"},
		{"[...new Uint8Array([1, 2])].join()", "1,2"},
		{"Object.keys(new Uint8Array(3)).join()", "0,1,2"},
		{"'ab'.concat(1, null, [2])", "ab1null2"},
		{"'5'.padStart(3, 0) + '|' + '5'.padEnd(4, 'ab') + '|' + 'x'.repeat('2') + '|' + 'abc'.padStart(2)", "005|5aba|xx|abc"},
		{"String.prototype.repeat.call(5, 2)", "55"},
		{"Reflect.apply(Math.max, null, [1, 3, 2]) + Reflect.construct(Date, [0]).getTime() + Math.max.apply(null, new Uint8Array([4, 9]))", "12"},
		{"JSON.stringify(Object.assign({}, 'ab', null, { c: 1 }))", "{\"0\":\"a\",\"1\":\"b\",\"c\":1}"},
		{"[...'héllo😀'].length", "6"},
		{"new Uint8Array(new ArrayBuffer(8), 2, 3).length + new Uint8Array('3').length + new Uint8Array(true).length", "7"},
		{"new ArrayBuffer('16').byteLength", "16"},
		{"[1, [2]].flat().join()", "1,2"},
		{"Array.prototype.map.call({ length: 2, 0: 'a', 1: 'b' }, (x) => x.toUpperCase()).join() + Array.prototype.slice.call('abc').join()", "A,Ba,b,c"},
	} {
		result, err := runAll(t, newRunner(), "return [{ json: { out: String("+entry.expression+") } }]", nil)
		if err != nil {
			t.Errorf("%s: Run() error = %v", entry.expression, err)
			continue
		}
		if got := result.Items[0].JSON["out"]; got != entry.want {
			t.Errorf("%s\n\t= %q\n\twant %q", entry.expression, got, entry.want)
		}
	}
	_, err := runAll(t, newRunner(), "return [{ json: { out: [Symbol('s')].join() } }]", nil)
	if err == nil || !strings.Contains(err.Error(), "TypeError: Cannot convert a Symbol value to a string") {
		t.Errorf("Run() error = %v, want the TypeError Node gives", err)
	}
}

// A replaced built-in is still one function under each of its names, is
// still named for itself, and, like the built-in, is not a constructor.
func TestAStandInIsOneFunctionUnderEveryNameAndNoConstructor(t *testing.T) {
	expectJSON(t, strings.Join([]string{
		"const TypedArray = Object.getPrototypeOf(Uint8Array)",
		"let constructed = false",
		"try { new Array.prototype.map(); constructed = true } catch (error) { constructed = !(error instanceof TypeError) }",
		"return [{ json: {",
		"  values: Array.prototype.values === Array.prototype[Symbol.iterator],",
		"  typedValues: TypedArray.prototype.values === TypedArray.prototype[Symbol.iterator],",
		"  names: [Array.prototype.map.name, Array.prototype.join.name, String.prototype.repeat.name, JSON.stringify.name, Array.prototype[Symbol.iterator].name].join(),",
		"  lengths: [Array.prototype.map.length, JSON.stringify.length, Function.prototype.apply.length].join(),",
		"  constructed,",
		"} }]",
	}, "\n"), map[string]any{
		"values": true, "typedValues": true, "names": "map,join,repeat,stringify,values", "lengths": "1,3,2", "constructed": false,
	})
}

// The early stop in stringify counts what stringify writes: a value it
// leaves out of an object costs nothing, so output that fits is never
// refused for keys that are never written.
func TestAValueStringifyLeavesOutCostsNothing(t *testing.T) {
	result, err := newRunner().Run(context.Background(), jsrun.Task{
		Source: "const json = {}\nfor (let n = 0; n < 500; n++) json['key' + n] = undefined\njson.kept = () => 1\nreturn [{ json }]",
		Limits: jsrun.Limits{MaxOutputBytes: 2000},
	})
	if err != nil || len(result.Items) != 1 || len(result.Items[0].JSON) != 0 {
		t.Fatalf("Run() = %#v, %v; want one empty item under the limit", result.Items, err)
	}
	_, err = newRunner().Run(context.Background(), jsrun.Task{
		Source: "return [{ json: { list: Array(2000).fill(undefined) } }]",
		Limits: jsrun.Limits{MaxOutputBytes: 2000},
	})
	if !errors.Is(err, jsrun.ErrOutputLimit) {
		t.Fatalf("Run() error = %v, want the output limit: in an array undefined is written as null", err)
	}
}

var _ = fmt.Sprint
