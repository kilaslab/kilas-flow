package jsrun_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// expectJSON runs a body returning one item and checks its fields.
func expectJSON(t *testing.T, source string, want map[string]any) {
	t.Helper()
	result := mustRun(t, newRunner(), jsrun.Task{Source: source})
	got := result.Items[0].JSON
	for key, value := range want {
		if fmt.Sprint(got[key]) != fmt.Sprint(value) {
			t.Errorf("%s = %#v, want %#v", key, got[key], value)
		}
	}
}

// The expected values are Node 24's for the same calls.
func TestBufferRoundTripsEveryEncoding(t *testing.T) {
	expectJSON(t, strings.Join([]string{
		"const text = 'héllo'",
		"return [{ json: {",
		"  base64: Buffer.from(text).toString('base64'),",
		"  base64url: Buffer.from([0xfb, 0xff, 0x01]).toString('base64url'),",
		"  hex: Buffer.from(text, 'utf8').toString('hex'),",
		"  latin1: Buffer.from('é', 'latin1')[0], binary: Buffer.from([0xe9]).toString('binary'),",
		"  utf16: Buffer.from('hé', 'utf16le').toString('hex'), ucs2: Buffer.from('6800e900', 'hex').toString('ucs2'),",
		"  ascii: Buffer.from([0xc1]).toString('ascii'), upper: Buffer.from('aGk=', 'BASE64').toString('UTF-8'),",
		"  back: Buffer.from(Buffer.from(text).toString('base64'), 'base64').toString() === text,",
		"  length: Buffer.byteLength(text), bytes: Buffer.byteLength('aGk=', 'base64'),",
		"} }]",
	}, "\n"), map[string]any{
		"base64": "aMOpbGxv", "base64url": "-_8B", "hex": "68c3a96c6c6f", "latin1": 233, "binary": "é",
		"utf16": "6800e900", "ucs2": "hé", "ascii": "A", "upper": "hi", "back": true, "length": 6, "bytes": 2,
	})
	_, err := runAll(t, newRunner(), "return [{ json: { x: Buffer.from('a', 'klingon') } }]", nil)
	if err == nil || !strings.Contains(err.Error(), "Unknown encoding: klingon") {
		t.Fatalf("Run() error = %v, want an unknown encoding named", err)
	}
}

func TestBufferBehavesAsNodesDoes(t *testing.T) {
	expectJSON(t, strings.Join([]string{
		"const buf = Buffer.from('hello world')",
		"const view = buf.slice(0, 5)",
		"view[0] = 72",
		"const written = Buffer.alloc(8)",
		"const count = written.write('hi', 2)",
		"return [{ json: {",
		"  view: buf.toString(), isBuffer: Buffer.isBuffer(view), plain: Buffer.isBuffer(new Uint8Array(1)),",
		"  json: JSON.stringify(Buffer.from([1, 2])), index: buf.indexOf('world'), last: buf.lastIndexOf('o'), has: buf.includes('lo w'),",
		"  count, written: written.toString('hex'), filled: Buffer.alloc(5, 'ab').toString(),",
		"  concat: Buffer.concat([Buffer.from('a'), Buffer.from('b')]).toString(), compare: Buffer.compare(Buffer.from('a'), Buffer.from('b')),",
		"  equals: Buffer.from('x').equals(Buffer.from('x')), number: Buffer.from([0, 0, 1, 0]).readUInt32BE(0),",
		"  range: buf.toString('utf8', 6), encoding: Buffer.isEncoding('Latin1'), typed: buf instanceof Uint8Array,",
		"} }]",
	}, "\n"), map[string]any{
		"view": "Hello world", "isBuffer": true, "plain": false, "json": `{"type":"Buffer","data":[1,2]}`,
		"index": 6, "last": 7, "has": true, "count": 2, "written": "0000686900000000", "filled": "ababa",
		"concat": "ab", "compare": -1, "equals": true, "number": 256, "range": "world", "encoding": true, "typed": true,
	})
	for _, source := range []string{
		"return [{ json: { n: Buffer.alloc(2**30).length } }]",
		"return [{ json: { n: Buffer.allocUnsafe(2**30).length } }]",
		"return [{ json: { n: Buffer.from({ length: 2**30 }).length } }]",
		"return [{ json: { n: Buffer.concat([], 2**30).length } }]",
	} {
		_, err := runAll(t, newRunner(), source, nil)
		if err == nil || !strings.Contains(err.Error(), "one call may handle here") {
			t.Errorf("%q: Run() error = %v, want the allocation bounded", source, err)
		}
	}
}

func TestURLFollowsWhatwg(t *testing.T) {
	expectJSON(t, strings.Join([]string{
		"const url = new URL('https://user@Example.com:8080/a/../b?x=1&y=two%20words#top')",
		"const relative = new URL('../c?q', 'https://a.test/b/d')",
		"const params = new URLSearchParams({ q: 'a b', n: '1' })",
		"params.append('q', 'é')",
		"let invalid = null",
		"try { new URL('not a url') } catch (error) { invalid = error.name }",
		"const { URL: Same } = require('url')",
		"return [{ json: {",
		"  host: url.host, path: url.pathname, y: url.searchParams.get('y'), hash: url.hash, relative: relative.href,",
		"  params: params.toString(), all: params.getAll('q').join('|'), invalid, same: Same === URL,",
		"} }]",
	}, "\n"), map[string]any{
		"host": "example.com:8080", "path": "/b", "y": "two words", "hash": "#top", "relative": "https://a.test/c?q",
		"params": "q=a+b&n=1&q=%C3%A9", "all": "a b|é", "invalid": "TypeError", "same": true,
	})
}

func TestTextEncoderAndDecoderFollowTheEncodingStandard(t *testing.T) {
	expectJSON(t, strings.Join([]string{
		"const bytes = new TextEncoder().encode('héllo 😀')",
		"const decoder = new TextDecoder()",
		"let fatal = null",
		"try { new TextDecoder('utf-8', { fatal: true }).decode(new Uint8Array([0xff])) } catch (error) { fatal = error.name }",
		"let label = null",
		"try { new TextDecoder('klingon') } catch (error) { label = error.name }",
		"return [{ json: {",
		"  length: bytes.length, typed: bytes instanceof Uint8Array, back: decoder.decode(bytes), encoding: decoder.encoding,",
		"  lenient: new TextDecoder().decode(new Uint8Array([0x68, 0xff])), bom: new TextDecoder().decode(new Uint8Array([0xef, 0xbb, 0xbf, 0x61])),",
		"  utf16: new TextDecoder('utf-16le').decode(new Uint8Array([0x68, 0, 0xe9, 0])), fatal, label,",
		// BUG-46g75c: TextDecoder shares Buffer#toString('utf8')'s decoder, so
		// a run of bytes that cannot start or continue a sequence becomes one
		// U+FFFD per byte, not one U+FFFD for the whole run.
		"  invalidRun: new TextDecoder().decode(new Uint8Array([0xb1, 0xa9, 0xa9, 0x95])),",
		"  invalidRunLength: new TextDecoder().decode(new Uint8Array([0xb1, 0xa9, 0xa9, 0x95])).length,",
		"} }]",
	}, "\n"), map[string]any{
		"length": 11, "typed": true, "back": "héllo 😀", "encoding": "utf-8", "lenient": "h�", "bom": "a",
		"utf16": "hé", "fatal": "TypeError", "label": "RangeError",
		"invalidRun": "����", "invalidRunLength": 4,
	})
}

// BUG-0592hz: TextDecoder's fatal mode refuses malformed UTF-16 as it does
// malformed UTF-8, and its lenient mode puts U+FFFD where Node does. The
// expected values are Node 24.16's, run by hand: a lone surrogate either way
// round, a lead surrogate followed by something else, and an odd byte left at
// the end are each an error, and a lead surrogate and an odd byte both left at
// the end are one U+FFFD between them, not two.
func TestTextDecoderFatalModeRefusesMalformedUTF16(t *testing.T) {
	expectJSON(t, strings.Join([]string{
		"const cases = { loneLead: [0x00, 0xd8], loneTrail: [0x00, 0xdc, 0x41, 0], leadThenA: [0x00, 0xd8, 0x41, 0], odd: [0x41, 0, 0x42], leadAndOdd: [0x00, 0xd8, 0x42] }",
		"function strict(label, bytes) {",
		"  try { return new TextDecoder(label, { fatal: true }).decode(new Uint8Array(bytes)) }",
		"  catch (error) { return error.name + ' ' + error.code + ' ' + error.message }",
		"}",
		"function codes(text) { return Array.from(text, (char) => char.codePointAt(0).toString(16)).join(',') }",
		"const out = {}",
		"for (const [name, bytes] of Object.entries(cases)) {",
		"  out[name] = strict('utf-16le', bytes)",
		"  out[name + 'Alias'] = strict('utf-16', bytes)",
		"  out[name + 'Lenient'] = codes(new TextDecoder('utf-16le').decode(new Uint8Array(bytes)))",
		"}",
		"out.pair = codes(strict('utf-16le', [0x3d, 0xd8, 0x00, 0xde]))",
		"out.utf8 = strict('utf-8', [0xff])",
		"return [{ json: out }]",
	}, "\n"), map[string]any{
		"loneLead":          "TypeError ERR_ENCODING_INVALID_ENCODED_DATA The encoded data was not valid for encoding utf-16le",
		"loneTrail":         "TypeError ERR_ENCODING_INVALID_ENCODED_DATA The encoded data was not valid for encoding utf-16le",
		"leadThenA":         "TypeError ERR_ENCODING_INVALID_ENCODED_DATA The encoded data was not valid for encoding utf-16le",
		"odd":               "TypeError ERR_ENCODING_INVALID_ENCODED_DATA The encoded data was not valid for encoding utf-16le",
		"leadAndOdd":        "TypeError ERR_ENCODING_INVALID_ENCODED_DATA The encoded data was not valid for encoding utf-16le",
		"loneLeadAlias":     "TypeError ERR_ENCODING_INVALID_ENCODED_DATA The encoded data was not valid for encoding utf-16le",
		"loneTrailAlias":    "TypeError ERR_ENCODING_INVALID_ENCODED_DATA The encoded data was not valid for encoding utf-16le",
		"leadThenAAlias":    "TypeError ERR_ENCODING_INVALID_ENCODED_DATA The encoded data was not valid for encoding utf-16le",
		"oddAlias":          "TypeError ERR_ENCODING_INVALID_ENCODED_DATA The encoded data was not valid for encoding utf-16le",
		"leadAndOddAlias":   "TypeError ERR_ENCODING_INVALID_ENCODED_DATA The encoded data was not valid for encoding utf-16le",
		"loneLeadLenient":   "fffd",
		"loneTrailLenient":  "fffd,41",
		"leadThenALenient":  "fffd,41",
		"oddLenient":        "41,fffd",
		"leadAndOddLenient": "fffd",
		"pair":              "1f600",
		"utf8":              "TypeError ERR_ENCODING_INVALID_ENCODED_DATA The encoded data was not valid for encoding utf-8",
	})
}

func TestAtobAndBtoaFollowTheWebSpec(t *testing.T) {
	expectJSON(t, strings.Join([]string{
		"let wide = null",
		"try { btoa('😀') } catch (error) { wide = error.name }",
		"let broken = null",
		"try { atob('a') } catch (error) { broken = error.name }",
		"return [{ json: { encoded: btoa('héllo'), decoded: atob('aOls bG8='), round: atob(btoa('\\u00ff\\u0000')) === '\\u00ff\\u0000', wide, broken } }]",
	}, "\n"), map[string]any{
		"encoded": "aOlsbG8=", "decoded": "héllo", "round": true, "wide": "InvalidCharacterError", "broken": "InvalidCharacterError",
	})
}

func TestStructuredCloneKeepsDatesMapsAndSets(t *testing.T) {
	expectJSON(t, strings.Join([]string{
		"const shared = { n: 1 }",
		"const source = { when: new Date(0), map: new Map([['k', shared]]), set: new Set([1, 2]), list: [shared, shared], bytes: new Uint8Array([1, 2]) }",
		"source.self = source",
		"const copy = structuredClone(source)",
		"let refused = null",
		"try { structuredClone({ fn() {} }) } catch (error) { refused = error.name }",
		"return [{ json: {",
		"  date: copy.when instanceof Date && copy.when.getTime() === 0 && copy.when !== source.when,",
		"  map: copy.map.get('k').n, set: copy.set.has(2), shared: copy.list[0] === copy.list[1] && copy.list[0] !== shared,",
		"  cycle: copy.self === copy, bytes: copy.bytes[1] + (copy.bytes.buffer !== source.bytes.buffer ? 10 : 0), refused,",
		"} }]",
	}, "\n"), map[string]any{
		"date": true, "map": 1, "set": true, "shared": true, "cycle": true, "bytes": 12, "refused": "DataCloneError",
	})
}

func TestObjectGroupBy(t *testing.T) {
	expectJSON(t, strings.Join([]string{
		"const people = [{ name: 'a', team: 'x' }, { name: 'b', team: 'y' }, { name: 'c', team: 'x' }]",
		"const byTeam = Object.groupBy(people, person => person.team)",
		"const byParity = Map.groupBy([1, 2, 3, 4], n => n % 2 === 0 ? 'even' : 'odd')",
		"return [{ json: { x: byTeam.x.map(p => p.name).join(','), proto: Object.getPrototypeOf(byTeam), even: byParity.get('even').join(',') } }]",
	}, "\n"), map[string]any{"x": "a,c", "proto": nil, "even": "2,4"})
}

func TestTimersRunOnTheExecutionsLoop(t *testing.T) {
	expectJSON(t, strings.Join([]string{
		"const order = []",
		"await new Promise(resolve => setTimeout(resolve, 20))",
		"order.push('slept')",
		"let ticks = 0",
		"await new Promise(resolve => { const handle = setInterval(() => { ticks++; if (ticks === 3) { clearInterval(handle); resolve() } }, 5) })",
		"const cancelled = setTimeout(() => order.push('never'), 10)",
		"clearTimeout(cancelled)",
		"await new Promise(resolve => setImmediate(resolve))",
		"const args = await new Promise(resolve => setTimeout((a, b) => resolve(a + b), 1, 2, 3))",
		"await new Promise(resolve => setTimeout(resolve, 20))",
		"let refused = null",
		"try { setTimeout('order.push(1)', 1) } catch (error) { refused = error.name }",
		"return [{ json: { order: order.join(','), ticks, args, refused } }]",
	}, "\n"), map[string]any{"order": "slept", "ticks": 3, "args": 5, "refused": "TypeError"})

	_, err := runAll(t, newRunner(), "await new Promise(resolve => setTimeout(() => { throw new Error('in a timer') }, 1))", nil)
	if err == nil || !strings.Contains(err.Error(), "in a timer") {
		t.Fatalf("Run() error = %v, want the timer's error to fail the run", err)
	}
}

// A timer the code does not wait for is dropped when the code finishes; one
// it waits for counts against its time limit.
func TestTimersDieWithTheExecution(t *testing.T) {
	start := time.Now()
	mustRun(t, newRunner(), jsrun.Task{Source: "setTimeout(() => {}, 10000)\nsetInterval(() => {}, 5)\nreturn []"})
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("the run took %v; pending timers must not keep it alive", elapsed)
	}
	_, err := newRunner().Run(context.Background(), jsrun.Task{
		Source: "await new Promise(resolve => setTimeout(resolve, 5000))\nreturn []",
		Limits: jsrun.Limits{Timeout: 100 * time.Millisecond},
	})
	if !errors.Is(err, jsrun.ErrTimeLimit) {
		t.Fatalf("Run() error = %v, want a long wait stopped at the time limit", err)
	}
	_, err = runAll(t, newRunner(), "for (let i = 0; i <= 10000; i++) setTimeout(() => {}, 100000)\nreturn []", nil)
	if err == nil || !strings.Contains(err.Error(), "more than 10000 timers") {
		t.Fatalf("Run() error = %v, want the timer count bounded", err)
	}
}
