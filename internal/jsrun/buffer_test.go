package jsrun_test

import (
	"fmt"
	"testing"
)

// Buffer's deprecated constructor, and Buffer.from given an object, reached
// goja_nodejs's own conversion, which allocates as far as an array-like's
// length says. Both go through the bounded alloc and from now, and from reads
// an object the way Node's does.
func TestBufferFromAnObjectIsBoundedAndReadsItAsNodeDoes(t *testing.T) {
	for _, source := range []string{
		"return [{ json: { n: new Buffer({ length: 2**27 }).length } }]",
		"return [{ json: { n: Buffer({ length: 2**27 }).length } }]",
		"return [{ json: { n: Buffer.from({ valueOf() { return { length: 2**27 } } }).length } }]",
		"return [{ json: { n: Buffer.from({ type: 'Buffer', data: (() => { const sparse = []; sparse.length = 2**27; return sparse })() }).length } }]",
	} {
		refusedQuickly(t, source, "one call may handle here")
	}
	result, err := runAll(t, newRunner(), `const bytes = (value) => Array.from(Buffer.from(value))
let refused
try { Buffer.from({}) } catch (error) { refused = error.code + ': ' + error.message }
return [{ json: {
  arrayLike: bytes({ length: 3, 0: 1, 1: 257, 2: -1 }), json: bytes({ type: 'Buffer', data: [7, 8] }),
  primitive: bytes({ [Symbol.toPrimitive]() { return 'ab' } }), valueOf: bytes({ valueOf() { return 'hi' } }),
  coerced: bytes([1.5, NaN, 300]), badLength: bytes({ length: 'x' }), refused,
  called: Array.from(Buffer(3)), constructed: new Buffer('ab').toString(),
  identity: Buffer.prototype.constructor === Buffer && Buffer.name === 'Buffer' && require('buffer').Buffer === Buffer,
  instance: Buffer.from('a') instanceof Buffer && Buffer.isBuffer(new Buffer(2)) && Buffer.from('a') instanceof Uint8Array,
} }]`, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	got := fmt.Sprint(result.Items[0].JSON)
	want := "map[arrayLike:[1 1 255] badLength:[] called:[0 0 0] coerced:[1 0 44] constructed:ab identity:true instance:true json:[7 8] primitive:[97 98] " +
		"refused:ERR_INVALID_ARG_TYPE: The first argument must be of type string or an instance of Buffer, ArrayBuffer, or Array or an Array-like Object. Received an instance of Object valueOf:[104 105]]"
	if got != want {
		t.Fatalf("got\n%s\nwant (Node 24)\n%s", got, want)
	}
}

// BUG-46g75c: Buffer#toString('utf8') must replace invalid UTF-8 the way
// Node 24 does, the WHATWG "maximal subpart" rule (codec_internal_test.go
// pins internal/jsrun's own decoder against a full Node golden; this proves
// the JS-visible Buffer#toString and Buffer.from(...).toString() round trip
// reach that same decoder). Go's strings.ToValidUTF8, which encodeBytes used
// before the fix, instead collapses a whole run of bad bytes into one
// U+FFFD.
func TestBufferToStringReplacesInvalidUTF8AsNodeDoes(t *testing.T) {
	result, err := runAll(t, newRunner(), `return [{ json: {
  // The ticket's own repro: Buffer.from('sample', 'base64') decodes to the
  // bytes b1 a9 a9 95, none of which can start or continue a sequence.
  repro: Buffer.from('sample', 'base64').toString(),
  reproLength: Buffer.from('sample', 'base64').toString().length,
  loneContinuation: Buffer.from([0xb1, 0xa9]).toString(),
  overlong: Buffer.from([0xc0, 0x80]).toString(),
  surrogate: Buffer.from([0xed, 0xa0, 0x80]).toString(),
  truncated: Buffer.from([0xe0, 0xa0]).toString(),
  mixed: Buffer.from([0x61, 0xff, 0x62]).toString('utf8'),
  valid: Buffer.from('héllo 😀').toString(),
} }]`, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := map[string]any{
		"repro": "����", "reproLength": float64(4),
		"loneContinuation": "��", "overlong": "��",
		"surrogate": "���", "truncated": "�",
		"mixed": "a�b", "valid": "héllo 😀",
	}
	got := result.Items[0].JSON
	for key, value := range want {
		if fmt.Sprint(got[key]) != fmt.Sprint(value) {
			t.Errorf("%s = %#v, want %#v (Node 24)", key, got[key], value)
		}
	}
}
