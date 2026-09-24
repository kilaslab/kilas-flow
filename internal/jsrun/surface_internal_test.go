package jsrun

import (
	"context"
	"flag"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The security review's enumeration (FEAT-vjjs8t): everything a script can
// reach, walked from the global object, from `this` and the arguments the
// code is called with, and from an instance of everything the runtime or its
// libraries construct. Three things are proved over the whole walk:
//
//   - no Go value is exposed through reflection, so no Go method or field is
//     callable or readable from a script, whatever a library hands it;
//   - no host callback is reachable, even through the built-ins the runtime
//     calls while the code runs;
//   - the surface the runtime adds over a bare goja VM is exactly the
//     reviewed list in testdata/surface.txt. A property added, removed or
//     changed without a review fails here; rewrite the list with
//     `go test ./internal/jsrun -run TestTheScriptSurfaceIsTheReviewedOne -update-surface`
//     and review its diff.
//
// The vendored libraries, lodash and Luxon, are JavaScript that reaches only
// what the VM offers any script, so they are walked for Go values but listed
// by their root alone.

var updateSurface = flag.Bool("update-surface", false, "rewrite testdata/surface.txt from this run")

const surfacePath = "testdata/surface.txt"

// surfaceWalker walks breadth first, in a fixed order, so every object is
// named by the same first path on every run: the bare VM's globals first,
// then the globals the runtime adds, then the samples. A leaf (a vendored
// library) is listed by its root and walked last, silently.
const surfaceWalker = `(function (bareNames, samples, leaves) {
  var getOwnPropertyNames = Object.getOwnPropertyNames, getOwnPropertySymbols = Object.getOwnPropertySymbols;
  var getOwnPropertyDescriptor = Object.getOwnPropertyDescriptor, getPrototypeOf = Object.getPrototypeOf;
  var seen = new Map(), queue = [], deferred = [], lines = [], objects = [], paths = [], hostShaped = [];
  var hostNames = ['native', 'hostCall', 'timerStart', 'timerCancel', 'library', 'staticData', 'pair'];
  function kind(value) { return value === null ? 'null' : typeof value; }
  function isObject(value) { return value !== null && (typeof value === 'object' || typeof value === 'function'); }
  function visit(value, path, emit) {
    if (!isObject(value) || seen.has(value)) return;
    seen.set(value, path);
    objects.push(value);
    paths.push(path);
    if (leaves.indexOf(value) >= 0) {
      if (emit) lines.push(path + ' library');
      deferred.push([value, path]);
      return;
    }
    queue.push([value, path, emit]);
  }
  function describe(object, path, emit) {
    var prototype = getPrototypeOf(object);
    if (prototype !== null) visit(prototype, path + '.[[Prototype]]', emit);
    var keys = getOwnPropertyNames(object).sort();
    var symbols = getOwnPropertySymbols(object).sort(function (a, b) { return String(a) < String(b) ? -1 : 1; });
    keys.concat(symbols).forEach(function (key) {
      var name = typeof key === 'symbol' ? '[' + String(key) + ']' : key;
      var at = path + '.' + name;
      var descriptor = getOwnPropertyDescriptor(object, key);
      if (hostNames.indexOf(name) >= 0 && typeof descriptor.value === 'function') hostShaped.push(at);
      if (typeof object === 'function' && (name === 'length' || name === 'name')) return;
      if ('value' in descriptor) {
        // A function's own untouched prototype, holding nothing but its
        // constructor, is walked without being listed.
        if (typeof object === 'function' && name === 'prototype' && isObject(descriptor.value) &&
            getOwnPropertySymbols(descriptor.value).length === 0 &&
            getOwnPropertyNames(descriptor.value).join() === 'constructor' && descriptor.value.constructor === object) {
          visit(descriptor.value, at, false);
          return;
        }
        if (emit) lines.push(at + ' ' + kind(descriptor.value));
        visit(descriptor.value, at, emit);
        return;
      }
      if (emit) lines.push(at + ' ' + (descriptor.get ? 'get' : '') + (descriptor.get && descriptor.set ? '/' : '') + (descriptor.set ? 'set' : ''));
      visit(descriptor.get, at + '.get', emit);
      visit(descriptor.set, at + '.set', emit);
    });
  }
  function drain() {
    while (queue.length > 0) {
      var entry = queue.shift();
      describe(entry[0], entry[1], entry[2]);
    }
  }
  function root(object, path, key) {
    var descriptor = getOwnPropertyDescriptor(object, key);
    var at = path + key;
    if ('value' in descriptor) {
      lines.push(at + ' ' + kind(descriptor.value));
      visit(descriptor.value, at, true);
    } else {
      lines.push(at + ' ' + (descriptor.get ? 'get' : '') + (descriptor.get && descriptor.set ? '/' : '') + (descriptor.set ? 'set' : ''));
      visit(descriptor.get, at + '.get', true);
      visit(descriptor.set, at + '.set', true);
    }
  }
  seen.set(globalThis, 'globalThis');
  bareNames.slice().sort().forEach(function (name) {
    if (getOwnPropertyDescriptor(globalThis, name)) root(globalThis, '', name);
  });
  drain();
  getOwnPropertyNames(globalThis).sort().forEach(function (name) {
    if (bareNames.indexOf(name) < 0 && name.slice(0, 2) !== '__') root(globalThis, '', name);
  });
  drain();
  if (samples) {
    getOwnPropertyNames(samples).sort().forEach(function (name) { root(samples, 'sample.', name); });
    drain();
  }
  deferred.forEach(function (entry) { queue.push([entry[0], entry[1], false]); });
  drain();
  return { lines: lines, objects: objects, paths: paths, hostShaped: hostShaped };
})`

// surfaceSamples names an instance of everything a script can be handed or
// construct, so the walk sees what a global alone does not reach.
const surfaceSamples = `const crypto = require('crypto')
const luxon = require('luxon')
const lodash = require('lodash')
const searchParams = new URLSearchParams('a=1')
const key = Buffer.alloc(16)
let nativeError, urlError, relativeTimeError
try { crypto.createHash('no-such-hash') } catch (error) { nativeError = error }
try { new URL('not a url') } catch (error) { urlError = error }
try { new Intl.RelativeTimeFormat('en') } catch (error) { relativeTimeError = error }
const response = await this.helpers.httpRequest({ url: 'https://example.com/', returnFullResponse: true })
const stored = await this.helpers.prepareBinaryData(Buffer.from('x'), 'x.txt')
const read = await this.helpers.getBinaryDataBuffer(0, 'data')
const timer = setTimeout(() => {}, 1)
clearTimeout(timer)
const libraryObjects = [lodash, luxon, $now, $today, DateTime.now(), Duration.fromMillis(1), Interval.after(DateTime.now(), 1000)]
for (const name of Object.keys(luxon)) libraryObjects.push(luxon[name])
globalThis.__samples = {
  this: this, items, input: $input, item: $input.first(), node: $('Webhook'), nodeItem: $node['Webhook'],
  staticGlobal: $getWorkflowStaticData('global'), staticNode: $getWorkflowStaticData('node'),
  url: new URL('https://a.example/b?c=1'), searchParams, searchIterator: searchParams.entries(),
  buffer: Buffer.from('x'), textEncoder: new TextEncoder(), textDecoder: new TextDecoder(), domException: new DOMException('x'),
  hash: crypto.createHash('sha256'), hmac: crypto.createHmac('sha256', 'k'),
  cipher: crypto.createCipheriv('aes-128-cbc', key, key), decipher: crypto.createDecipheriv('aes-128-cbc', key, key),
  dateTimeFormat: new Intl.DateTimeFormat('en-US'), numberFormat: new Intl.NumberFormat('de-DE'), locale: new Intl.Locale('en-US'),
  modules: { util: require('util'), buffer: require('buffer'), url: require('url'), crypto, luxon, lodash },
  nativeError, urlError, relativeTimeError, response, stored, read, timer,
  captured: typeof captured === 'undefined' ? [] : captured,
}
globalThis.__leaves = libraryObjects
return items`

// surfaceHelpers answers the helpers the samples call.
type surfaceHelpers struct{}

func (surfaceHelpers) HTTPRequest(context.Context, HTTPRequest, []byte) (HTTPResponse, []byte, error) {
	return HTTPResponse{StatusCode: 200, StatusMessage: "OK", Headers: map[string]any{"content-type": "text/plain"}}, []byte("ok"), nil
}

func (surfaceHelpers) ReadFile(context.Context, int, string) ([]byte, error) { return []byte("x"), nil }

func (surfaceHelpers) WriteFile(_ context.Context, data []byte, fileName, mimeType string) (workflow.BinaryRef, error) {
	return workflow.BinaryRef{ID: "bin_stored", FileName: fileName, MediaType: mimeType, Size: int64(len(data))}, nil
}

func (surfaceHelpers) StaticData(string) (string, error) { return "{}", nil }

// arrayIndex matches an array or typed-array index in a path, which the list
// names once for all of them.
var arrayIndex = regexp.MustCompile(`\.\d+(\.| )`)

type walked struct {
	lines      []string
	objects    []*goja.Object
	paths      []string
	hostShaped []string
}

// walk runs surfaceWalker in rt.
func walk(t *testing.T, rt *goja.Runtime, bare []string, samples, leaves goja.Value) walked {
	t.Helper()
	walker, err := rt.RunString(surfaceWalker)
	if err != nil {
		t.Fatalf("compiling the walker: %v", err)
	}
	function, _ := goja.AssertFunction(walker)
	result, err := function(goja.Undefined(), rt.ToValue(bare), samples, leaves)
	if err != nil {
		t.Fatalf("walking: %v", err)
	}
	out := result.ToObject(rt)
	var w walked
	for _, line := range out.Get("lines").Export().([]any) {
		w.lines = append(w.lines, arrayIndex.ReplaceAllString(line.(string), ".[i]$1"))
	}
	for _, path := range out.Get("hostShaped").Export().([]any) {
		w.hostShaped = append(w.hostShaped, path.(string))
	}
	for _, path := range out.Get("paths").Export().([]any) {
		w.paths = append(w.paths, path.(string))
	}
	objects := out.Get("objects").ToObject(rt)
	for index := range objects.Get("length").ToInteger() {
		w.objects = append(w.objects, objects.Get(fmt.Sprint(index)).ToObject(rt))
	}
	return w
}

// scriptSurface runs the samples in a real run, in the given mode, and walks
// the VM once the code has returned.
func scriptSurface(t *testing.T, mode Mode, prefix string) (walked, *goja.Object) {
	t.Helper()
	runner := NewRunner(Options{})
	var surface walked
	var callbacks *goja.Object
	runner.inspect = func(v *vm) {
		samples := v.rt.Get("__samples")
		if samples == nil || goja.IsUndefined(samples) {
			// The run failed, and says why.
			return
		}
		if restore, ok := goja.AssertFunction(v.rt.Get("__restore")); ok {
			if _, err := restore(goja.Undefined()); err != nil {
				t.Fatalf("restoring the built-ins: %v", err)
			}
		}
		surface = walk(t, v.rt, BareGlobalsForTest(), samples, v.rt.Get("__leaves"))
		callbacks = v.callbacks
	}
	source := prefix + surfaceSamples
	if mode == ModeEachItem {
		source = strings.Replace(source, "this: this, items, input: $input, item: $input.first(),", "this: this, json: $json, input: $input, item: $input.item,", 1)
		source = strings.Replace(source, "return items", "return $input.item", 1)
	}
	_, err := runner.Run(context.Background(), Task{
		Source: source, Mode: mode,
		Items: []workflow.Item{{JSON: map[string]any{"n": 1}, Binary: map[string]workflow.BinaryRef{"data": {ID: "bin_in", FileName: "in.txt", MediaType: "text/plain", Size: 1}}}},
		Roots: Roots{
			Workflow: WorkflowInfo{ID: "wf", Name: "Surface"},
			Node: func(name string) (NodeView, bool) {
				return NodeView{Items: []map[string]any{{"from": "hook"}}}, name == "Webhook"
			},
			Pair:    func(string, int) (int, string) { return 0, "" },
			Helpers: surfaceHelpers{},
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return surface, callbacks
}

// reflectedGoValue says whether an object is a Go value goja exposes through
// reflection, rather than one of the engine's own kinds of object. goja
// exports its own objects as plain Go data (maps, slices, functions, time) or
// as its own types; anything else is a Go struct, map or slice handed to the
// VM.
func reflectedGoValue(object *goja.Object) (reflect.Type, bool) {
	exported := object.ExportType()
	if exported == nil {
		return nil, false
	}
	own := exported
	if own.Kind() == reflect.Pointer {
		own = own.Elem()
	}
	switch {
	case own.PkgPath() == "github.com/dop251/goja":
		return exported, false
	case exported == reflect.TypeFor[time.Time]():
		// A Date.
		return exported, false
	case exported.PkgPath() != "":
		return exported, true
	}
	switch exported.Kind() {
	case reflect.Map:
		// A plain object.
		return exported, exported != reflect.TypeFor[map[string]any]()
	case reflect.Slice, reflect.Func, reflect.String, reflect.Bool, reflect.Int64, reflect.Float64:
		// An array or a typed array, a function, a boxed primitive.
		return exported, false
	}
	return exported, true
}

// goja_nodejs keeps a Go value behind each URL, URLSearchParams and their
// iterator, and one behind Buffer under a symbol of its own; its functions
// find them again by exporting the object. Such a value may reach a script
// only as an opaque handle: no Go field or method of it, or of any other Go
// value, may be readable or callable. Buffer's handle once offered WrapBytes,
// which copies an array-like of any length in one uninterruptible call, past
// every time limit (BUG-h6tj4e).
func TestNoGoFieldOrMethodIsReachable(t *testing.T) {
	for _, mode := range []Mode{ModeAllItems, ModeEachItem} {
		surface, _ := scriptSurface(t, mode, "")
		reported := map[string]bool{}
		for index, object := range surface.objects {
			exported, reflected := reflectedGoValue(object)
			if !reflected || reported[exported.String()] {
				continue
			}
			var members []string
			members = append(members, object.GetOwnPropertyNames()...)
			for _, symbol := range object.Symbols() {
				members = append(members, symbol.String())
			}
			if len(members) > 0 {
				reported[exported.String()] = true
				t.Errorf("%s: %s is a %s whose Go members %v a script can reach", mode, surface.paths[index], exported, members)
			}
		}
	}
}

// The runtime's host callbacks, and the kit its modules are built with, stay
// inside the runtime's closures. None is reachable from anything a script
// can reach, by identity or by shape.
//
// The runtime's modules keep working with built-ins a script can replace, so
// the second pass replaces every built-in method first, with one that keeps
// its receiver and its arguments, runs every module, and walks what was kept
// too: nothing the runtime hands a built-in may be a host callback, or hold
// one.
func TestNoScriptReachesTheHostCallbacks(t *testing.T) {
	for _, prefix := range []string{"", hookEveryBuiltIn} {
		for _, mode := range []Mode{ModeAllItems, ModeEachItem} {
			surface, callbacks := scriptSurface(t, mode, prefix)
			if callbacks == nil {
				t.Fatal("the run kept no callbacks")
			}
			if prefix != "" && !slices.ContainsFunc(surface.paths, func(path string) bool { return strings.HasPrefix(path, "sample.captured.") }) {
				t.Fatal("the hooks kept nothing")
			}
			if len(surface.hostShaped) > 0 {
				t.Errorf("%s: a script reaches something shaped like the host callbacks at %v", mode, surface.hostShaped)
			}
			for _, name := range callbacks.GetOwnPropertyNames() {
				callback := callbacks.Get(name)
				for index, object := range surface.objects {
					if object.SameAs(callback) {
						t.Errorf("%s: %s is the host callback %q", mode, surface.paths[index], name)
					}
				}
			}
		}
	}
}

// hookEveryBuiltIn replaces every built-in method with one that keeps its
// receiver and arguments in `captured`, which the samples then carry, and
// exercises every module beyond what the samples construct. __restore puts the
// built-ins back before the walk.
const hookEveryBuiltIn = `const captured = []
const hooked = []
{
  const hookApply = Reflect.apply, ownNames = Object.getOwnPropertyNames
  const describeOwn = Object.getOwnPropertyDescriptor, defineOwn = Object.defineProperty
  const keep = value => { captured[captured.length] = value }
  const TypedArray = Object.getPrototypeOf(Uint8Array)
  for (const holder of [Object, Object.prototype, Array, Array.prototype, Function.prototype, String, String.prototype,
    Number, Number.prototype, BigInt.prototype, Boolean.prototype, Symbol, Symbol.prototype, Promise, Promise.prototype,
    Map.prototype, Set.prototype, WeakMap.prototype, WeakSet.prototype, RegExp.prototype, Date, Date.prototype,
    Error.prototype, ArrayBuffer, ArrayBuffer.prototype, DataView.prototype, TypedArray, TypedArray.prototype,
    Uint8Array.prototype, JSON, Reflect, Math]) {
    for (const key of ownNames(holder)) {
      const descriptor = describeOwn(holder, key)
      if (key === 'constructor' || typeof descriptor.value !== 'function' || !descriptor.configurable) continue
      const original = descriptor.value
      hooked[hooked.length] = [holder, key, descriptor]
      defineOwn(holder, key, {
        value: function (...args) {
          keep(this)
          for (let index = 0; index < args.length; index++) keep(args[index])
          return hookApply(original, this, args)
        },
        writable: true, configurable: true, enumerable: descriptor.enumerable,
      })
    }
  }
  globalThis.__restore = () => { for (const [holder, key, descriptor] of hooked) defineOwn(holder, key, descriptor) }
}
{
  const crypto = require('crypto'), util = require('util'), _ = require('lodash')
  console.log('a', { b: [1, { c: new Date(0) }] }, new Map([[1, 2]]), new Set([3]), Buffer.from('x'), [1, 2])
  console.warn(util.format('%s %d %j', 'x', 1, { y: 2 }), util.inspect({ deep: { deeper: { deepest: {} } } }))
  $('Webhook').item; $('Webhook').all(); $('Webhook').itemMatching(0); $node['Webhook'].json
  structuredClone({ a: [new Date(0), new Map([[1, { b: 2 }]]), new Set([1])] })
  const bytes = Buffer.concat([Buffer.from('ab'), Buffer.from('6364', 'hex')])
  bytes.toString('base64'); bytes.toString('latin1'); bytes.slice(1).toString('utf16le'); Buffer.compare(bytes, Buffer.alloc(4)); bytes.readUInt16BE(0); bytes.toJSON()
  crypto.createHash('md5').update('x').update(bytes).digest('base64'); crypto.createHmac('sha512', bytes).update('x').digest()
  const key = crypto.randomBytes(32), iv = crypto.randomBytes(12)
  const gcm = crypto.createCipheriv('aes-256-gcm', key, iv); gcm.setAAD(bytes); const sealed = Buffer.concat([gcm.update('secret'), gcm.final()])
  const open = crypto.createDecipheriv('aes-256-gcm', key, iv); open.setAAD(bytes); open.setAuthTag(gcm.getAuthTag()); open.update(sealed); open.final()
  crypto.pbkdf2Sync('p', 's', 2, 16, 'sha256'); crypto.scryptSync('p', 's', 16, { N: 16 }); crypto.timingSafeEqual(bytes, bytes)
  crypto.randomUUID(); crypto.randomInt(10); crypto.getRandomValues(new Uint8Array(4)); crypto.getHashes(); crypto.getCiphers()
  new Intl.DateTimeFormat('en-US', { dateStyle: 'full', timeStyle: 'long', timeZone: 'Europe/Berlin' }).formatToParts(new Date(0))
  new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).formatToParts(1234.5)
  new Intl.NumberFormat('en-IN').resolvedOptions(); Intl.getCanonicalLocales(['EN-us']); new Intl.Locale('en-GB').weekInfo
  new Date(0).toLocaleString('en-US', { timeZone: 'America/New_York' }); (1234.5).toLocaleString('id-ID'); 'a'.localeCompare('b', 'en')
  DateTime.fromISO('2026-03-29T01:30:00', { zone: 'Europe/London' }).plus({ hours: 1 }).setZone('Asia/Jakarta').toFormat('yyyy LLL dd HH:mm')
  $now.minus({ days: 1 }).toISO(); $today.startOf('month').toRelative(); Duration.fromObject({ hours: 2 }).toISO(); Interval.fromDateTimes($today, $now).length('hours')
  _.groupBy([{ a: 1 }, { a: 2 }], 'a'); _.cloneDeep({ a: [1] }); _.debounce(() => {}, 1); _.template('<%= x %>')({ x: 1 })
  JSON.parse(JSON.stringify({ a: 1 })); btoa(atob('eA==')); new TextDecoder('utf-8', { fatal: true }).decode(new TextEncoder().encode('é'))
  const url = new URL('https://user:pw@a.example:8080/p?q=1#h'); url.searchParams.append('r', '2'); url.hostname = 'b.example'; String(url); [...url.searchParams]
  'a-b-c'.replace(/-(\w)/g, (_, letter) => letter.toUpperCase()); 'abc'.match(/b/); [...'a1b2'.matchAll(/\d/g)]; 'a,b'.split(/,/)
  queueMicrotask(() => {}); await new Promise(resolve => setTimeout(resolve, 1)); await Promise.all([Promise.resolve(1), new Promise(resolve => setImmediate(resolve))])
  const state = $getWorkflowStaticData('global'); state.count = (state.count || 0) + 1; state.list = [1, { a: 2 }]
  await this.helpers.httpRequest({ method: 'POST', url: 'https://example.com/', qs: { a: [1, 2] }, headers: { 'x-a': 'b' }, body: { c: 1 }, json: true })
  await this.helpers.httpRequest({ url: 'https://example.com/', encoding: 'arraybuffer' })
  try { await this.helpers.httpRequest({ url: 'https://example.com/', proxy: 'http://p' }) } catch (error) { captured.push(error) }
}
`

// Every property the runtime adds to what a bare goja VM offers, and every
// property of what the runtime and its modules hand a script, is the reviewed
// list in testdata/surface.txt.
func TestTheScriptSurfaceIsTheReviewedOne(t *testing.T) {
	bareRT := goja.New()
	bare := walk(t, bareRT, BareGlobalsForTest(), goja.Undefined(), bareRT.ToValue([]any{}))
	builtIn := map[string]bool{}
	for _, line := range bare.lines {
		builtIn[line] = true
	}
	var added []string
	for _, mode := range []Mode{ModeAllItems, ModeEachItem} {
		surface, _ := scriptSurface(t, mode, "")
		for _, line := range surface.lines {
			if !builtIn[line] {
				added = append(added, line)
			}
		}
	}
	slices.Sort(added)
	added = slices.Compact(added)

	if *updateSurface {
		header := "# The script surface of a Code node (FEAT-vjjs8t): every property a script can\n" +
			"# reach that a bare goja VM does not have, with what it holds. Generated by\n" +
			"# go test ./internal/jsrun -run TestTheScriptSurfaceIsTheReviewedOne -update-surface\n" +
			"# Review every changed line before committing it: each is something a script can\n" +
			"# now read, call or replace. \"library\" is a vendored library, listed by its root.\n"
		if err := os.WriteFile(surfacePath, []byte(header+strings.Join(added, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	text, err := os.ReadFile(surfacePath)
	if err != nil {
		t.Fatal(err)
	}
	var reviewed []string
	for _, line := range strings.Split(strings.TrimSpace(string(text)), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			reviewed = append(reviewed, line)
		}
	}
	want := map[string]bool{}
	for _, line := range reviewed {
		want[line] = true
	}
	got := map[string]bool{}
	for _, line := range added {
		got[line] = true
		if !want[line] {
			t.Errorf("unreviewed: %s", line)
		}
	}
	for _, line := range reviewed {
		if !got[line] {
			t.Errorf("reviewed but gone: %s", line)
		}
	}
	if t.Failed() {
		t.Log("review the lines above, then rewrite the list with -update-surface")
	}
}
