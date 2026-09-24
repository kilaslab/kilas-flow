package jsrun_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The P8 security review (FEAT-vjjs8t) in tests. What a script can reach at
// all is enumerated in surface_internal_test.go; these prove what it can do
// with it. That a worker is treated as hostile is proved in
// internal/jsworker (lyingWorker and the tests that use it).

// pollution changes everything a script shares with the engine and the
// runtime: every built-in prototype a body touches, the global object, the
// symbol registry, a library's module object, Luxon's settings and the
// runtime's own modules.
const pollution = `Object.prototype.polluted = 'object'
Array.prototype.polluted = 'array'
Function.prototype.polluted = 'function'
String.prototype.trim = function () { return 'polluted' }
Array.prototype.map = function () { return ['polluted'] }
JSON.parse = function () { return 'polluted' }
JSON.stringify = function () { return 'polluted' }
Promise.prototype.then = function () { return 'polluted' }
Math.random = function () { return 4 }
Date.now = function () { return 0 }
globalThis.leftBehind = 'global'
Symbol.for('kilasflow.test').description
Symbol.for = function () { return 'polluted' }
Buffer.prototype.toString = function () { return 'polluted' }
Buffer.from = function () { return 'polluted' }
URL.prototype.toString = function () { return 'polluted' }
require('lodash').chunk = function () { return 'polluted' }
require('crypto').createHash = function () { return 'polluted' }
require('util').format = function () { return 'polluted' }
Settings.defaultZone = 'Asia/Tokyo'
Intl.NumberFormat.prototype.format = function () { return 'polluted' }
console.log = function () { throw new Error('polluted') }
Object.freeze(Object.prototype)
return []`

// untouched reads, in a fresh execution, everything pollution changed.
const untouched = `const _ = require('lodash')
const text = JSON.stringify({ a: [1, 2].map(n => n * 2) })
console.log('printed')
return [{ json: {
  object: typeof ({}).polluted, array: typeof [].polluted, fn: typeof (function () {}).polluted,
  trim: ' x '.trim(), parsed: JSON.parse(text).a.join(), random: Math.random() < 1, now: Date.now() > 0,
  global: typeof leftBehind, symbol: typeof Symbol.for('x'), buffer: Buffer.from('hi').toString('hex'),
  url: new URL('https://a.example/b').toString(), chunk: _.chunk([1, 2, 3], 2).length,
  hash: require('crypto').createHash('sha256').update('').digest('hex').slice(0, 8),
  format: require('util').format('%d', 7), zone: DateTime.now().zoneName, number: new Intl.NumberFormat('en-US').format(1234.5),
  frozen: Object.isFrozen(Object.prototype), awaited: await Promise.resolve('then'),
} }]`

func wantUntouched(t *testing.T, got map[string]any, console []jsrun.ConsoleLine) {
	t.Helper()
	want := map[string]any{
		"object": "undefined", "array": "undefined", "fn": "undefined", "trim": "x", "parsed": "2,4", "random": true, "now": true,
		"global": "undefined", "symbol": "symbol", "buffer": "6869", "url": "https://a.example/b", "chunk": float64(2),
		"hash": "e3b0c442", "format": "7", "zone": "UTC", "number": "1,234.5", "frozen": false, "awaited": "then",
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %#v, want %#v: the next execution saw what the last one changed", key, got[key], value)
		}
	}
	if len(console) != 1 || console[0].Text != "printed" {
		t.Errorf("console = %#v, want the one line", console)
	}
}

// Whatever one execution changes of what it shares with the engine is gone
// in the next, which gets a fresh VM, including one running the same body
// for another tenant. Only compiled programs are shared, and a program is
// never changed by running it.
func TestPrototypePollutionIsConfinedToOneExecution(t *testing.T) {
	runner := newRunner()
	for _, mode := range []jsrun.Mode{jsrun.ModeAllItems, jsrun.ModeEachItem} {
		if _, err := runner.Run(context.Background(), jsrun.Task{Source: pollution, Mode: jsrun.ModeAllItems, Items: numbered(1)}); err != nil {
			t.Fatalf("polluting: Run() error = %v", err)
		}
		source := untouched
		if mode == jsrun.ModeEachItem {
			source = strings.Replace(strings.Replace(untouched, "return [{ json: {", "return { json: {", 1), "} }]", "} }", 1)
		}
		result := mustRun(t, runner, jsrun.Task{Source: source, Mode: mode, Items: numbered(1),
			Roots: jsrun.Roots{Workflow: jsrun.WorkflowInfo{ID: "wf_other_tenant"}}})
		wantUntouched(t, result.Items[0].JSON, result.Console)
	}
}

// Within its own execution, pollution can change what the code computes,
// which is its own business, but not what the server accepts from it: a
// result still names only files the node was given, whatever the code does
// to the built-ins the runtime reads its result with.
func TestPollutionCannotMakeAResultNameAnotherFile(t *testing.T) {
	given := workflow.Item{JSON: map[string]any{}, Binary: map[string]workflow.BinaryRef{"data": {ID: "bin_given", FileName: "a.txt", MediaType: "text/plain", Size: 1}}}
	for _, source := range []string{
		"Object.prototype.binary = { data: { id: 'bin_someone_elses' } }\nreturn [{ json: {} }]",
		"Object.defineProperty(Object.prototype, 'binary', { get() { return { stolen: { id: 'bin_someone_elses' } } } })\nreturn [{ json: {} }]",
		"JSON.stringify = () => '[{\"json\":{},\"binary\":{\"data\":{\"id\":\"bin_someone_elses\"}},\"paired\":null}]'\nreturn [{ json: {} }]",
		"Array.prototype.toJSON = function () { return [{ json: {}, binary: { data: { id: 'bin_someone_elses' } }, paired: null }] }\nreturn [{ json: {} }]",
		"const item = $input.first()\nitem.binary.data.id = 'bin_someone_elses'\nreturn [item]",
		"Object.prototype.toJSON = function () { return this && this.json ? { json: {}, binary: { data: { id: 'bin_someone_elses' } } } : this }\nreturn [{ json: {} }]",
	} {
		result, err := runAll(t, newRunner(), source, []workflow.Item{given})
		for _, item := range result.Items {
			for property, ref := range item.Binary {
				if ref.ID != "bin_given" {
					t.Errorf("%q: item has %s = %s, a file the node was never given", source, property, ref.ID)
				}
			}
		}
		// Breaking the built-ins the runtime reads a result with may fail the
		// run, which is the code's own doing, but only as the code's error.
		if errors.Is(err, jsrun.ErrEngineFault) {
			t.Errorf("%q: Run() error = %v, a fault in the server", source, err)
		}
	}
}

// eval, new Function and every other way to compile code at run time stay
// enabled, as in n8n, because what they compile reaches exactly what the body
// reaches: the same global object, with the same require, and no Node or
// host handle besides.
func TestCodeCompiledAtRunTimeReachesNothingTheBodyCannot(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: strings.Join([]string{
		"const global = globalThis",
		"const compilers = {",
		"  indirectEval: (0, eval),",
		"  newFunction: source => new Function(source)(),",
		"  functionConstructor: source => Function.prototype.constructor(source)(),",
		"  generatorConstructor: source => (function* () {}).constructor(source)().next().value,",
		"  hostFunctionConstructor: source => $input.all.constructor(source)(),",
		"  bufferConstructor: source => Buffer.from.constructor(source)(),",
		"  errorChain: source => new Error('x').constructor.constructor(source)(),",
		"}",
		"const found = {}",
		"for (const [name, compile] of Object.entries(compilers)) {",
		"  const run = source => name === 'indirectEval' ? compile(source) : compile(name === 'generatorConstructor' ? 'yield ' + source : 'return ' + source)",
		"  found[name] = [",
		"    run('globalThis') === global,",
		"    run('[typeof process, typeof module, typeof exports, typeof global, typeof __dirname, typeof Deno, typeof Bun, typeof WebAssembly, typeof fetch, typeof XMLHttpRequest, typeof SharedArrayBuffer].join()'),",
		"    run('require') === require,",
		"  ].join(' ')",
		"}",
		"let direct = eval('[typeof process, typeof require, this === globalThis].join()')",
		"let refused = ''",
		"try { new Function(\"return require('child_' + 'process')\")() } catch (error) { refused = error.message }",
		"return [{ json: { ...found, direct, refused } }]",
	}, "\n")})
	got := result.Items[0].JSON
	for _, name := range []string{"indirectEval", "newFunction", "functionConstructor", "generatorConstructor", "hostFunctionConstructor", "bufferConstructor", "errorChain"} {
		if want := "true " + strings.Repeat("undefined,", 10) + "undefined true"; got[name] != want {
			t.Errorf("%s: got %q, want %q", name, got[name], want)
		}
	}
	// A direct eval sees the body's own scope, whose this is the Code node's.
	if got["direct"] != "undefined,function,false" {
		t.Errorf("direct eval: got %q", got["direct"])
	}
	if !strings.Contains(fmt.Sprint(got["refused"]), `requires the module "child_process", which this server does not run`) {
		t.Errorf("require from new Function: got %q, want the refusal", got["refused"])
	}
}

// V8's stack-trace hooks hand code the frames themselves, receivers and
// functions included. The engine has no such hook: prepareStackTrace is never
// called, captureStackTrace does not exist, and a stack is only ever text.
func TestStackTracesAreTextNeverFrames(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: strings.Join([]string{
		"let called = 0",
		"Error.prepareStackTrace = (error, frames) => { called++; return frames }",
		"Error.stackTraceLimit = Infinity",
		"let stacks = []",
		"for (const make of [() => new Error('x'), () => new TypeError('y'), () => { try { null.x } catch (e) { return e } }, () => { try { require('crypto').createHash('nope') } catch (e) { return e } }]) {",
		"  stacks.push(typeof make().stack)",
		"}",
		"return [{ json: { called, stacks: stacks.join(), capture: typeof Error.captureStackTrace } }]",
	}, "\n")})
	if got := fmt.Sprint(result.Items[0].JSON); got != "map[called:0 capture:undefined stacks:string,string,string,string]" {
		t.Fatalf("got %s", got)
	}
}

// A Proxy handed to a host function is only a value: a trap runs as the
// code's own JavaScript, charged to its own clock, and one that lies or
// throws makes the call fail as an error the code can catch, never a fault
// in the server.
func TestAProxyHandedToTheHostIsTreatedAsData(t *testing.T) {
	result, err := helperRun(t, &fakeHelpers{}, strings.Join([]string{
		"const trap = { get(target, key) { if (key === 'boom') throw new Error('trap'); return Reflect.get(target, key) } }",
		"const hostile = new Proxy({}, { get() { throw new Error('trap') }, ownKeys() { throw new Error('trap') }, getPrototypeOf() { throw new Error('trap') } })",
		"const revocable = Proxy.revocable({}, {}); revocable.revoke()",
		"const calls = {",
		"  bufferFromArray: () => Buffer.from(new Proxy([104, 105], trap)).toString(),",
		"  bufferFromHostile: () => Buffer.from(hostile),",
		"  bufferFromRevoked: () => Buffer.from(revocable.proxy),",
		"  hashUpdate: () => require('crypto').createHash('sha256').update(new Proxy(Buffer.from('x'), trap)).digest('hex').length,",
		"  hashName: () => require('crypto').createHash(hostile),",
		"  hmacKey: () => require('crypto').createHmac('sha256', revocable.proxy),",
		"  node: () => $(hostile),",
		"  staticData: () => $getWorkflowStaticData(hostile),",
		"  numberFormat: () => new Intl.NumberFormat(hostile).format(1),",
		"  dateTimeFormat: () => new Intl.DateTimeFormat('en-US', hostile).format(0),",
		"  url: () => new URL(hostile),",
		"  searchParams: () => new URLSearchParams(hostile).toString(),",
		"  textEncoder: () => new TextEncoder().encode(hostile),",
		"  structuredClone: () => structuredClone(hostile),",
		"  inspect: () => require('util').inspect(hostile),",
		"  console: () => console.log(hostile, revocable.proxy),",
		"  input: () => new Proxy($input, trap).all().length,",
		"  timer: () => setTimeout(hostile, hostile),",
		"}",
		"const outcome = {}",
		"for (const [name, call] of Object.entries(calls)) {",
		"  try { outcome[name] = 'returned ' + typeof call() } catch (error) { outcome[name] = 'threw ' + (error && error.name) + ': ' + (error && error.message) }",
		"}",
		"for (const [name, call] of Object.entries({",
		"  request: () => this.helpers.httpRequest(new Proxy({ url: 'https://example.com/' }, trap)),",
		"  hostileRequest: () => this.helpers.httpRequest(hostile),",
		"  revokedRequest: () => this.helpers.httpRequest(revocable.proxy),",
		"  file: () => this.helpers.prepareBinaryData(new Proxy(Buffer.from('x'), trap), hostile),",
		"})) {",
		"  try { outcome[name] = 'resolved ' + typeof (await call()) } catch (error) { outcome[name] = 'rejected ' + (error && error.name) }",
		"}",
		"return [{ json: outcome }]",
	}, "\n"))
	if err != nil {
		t.Fatalf("Run() error = %v, want every call to return or throw inside the code", err)
	}
	for name, outcome := range result.Items[0].JSON {
		text := fmt.Sprint(outcome)
		if strings.Contains(text, "GoError") || strings.HasPrefix(text, "threw undefined") || strings.HasPrefix(text, "rejected undefined") {
			t.Errorf("%s: %s, want a value or a JavaScript error", name, text)
		}
	}
	// A built-in that reads a length would call the trap once per element
	// inside one call nothing interrupts, so the runtime refuses the Proxy.
	if got := fmt.Sprint(result.Items[0].JSON["bufferFromArray"]); !strings.Contains(got, "passes a Proxy to a built-in that reads its length, which this server does not run") {
		t.Errorf("a Proxy over an array handed to Buffer.from: got %v, want the refusal", got)
	}
	if got := result.Items[0].JSON["input"]; got != "returned number" {
		t.Errorf("a Proxy over $input should work as $input: got %v", got)
	}
}

// goja_nodejs keeps Go values behind Buffer and URL; a script sees them as
// handles with nothing on them (BUG-h6tj4e), and both still work.
func TestGoHandlesOfferNothingToCall(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: strings.Join([]string{
		"const handles = Object.getOwnPropertySymbols(Buffer).map(symbol => Buffer[symbol])",
		"const url = new URL('https://a.example/b?c=1')",
		"return [{ json: {",
		"  handles: handles.map(handle => Object.getOwnPropertyNames(handle).length + Object.getOwnPropertySymbols(handle).length).join(),",
		"  wrapBytes: handles.map(handle => typeof handle.WrapBytes).join(),",
		"  urlString: typeof url.String, urlOwn: Object.getOwnPropertyNames(url).length,",
		"  href: url.href, param: url.searchParams.get('c'), buffer: Buffer.from('hi').toString('base64'),",
		"} }]",
	}, "\n")})
	if got := fmt.Sprint(result.Items[0].JSON); got != "map[buffer:aGk= handles:0 href:https://a.example/b?c=1 param:1 urlOwn:0 urlString:undefined wrapBytes:undefined]" {
		t.Fatalf("got %s", got)
	}
}
