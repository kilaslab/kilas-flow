/**
 * Records the Node.js goldens for where goja and V8 differ as engines: the
 * wording of the errors the built-ins throw, and HTML-like comments.
 *
 * DEV-ONLY. CI never runs Node: internal/jsrun's tests compare the runtime
 * against the JSON files this script writes under
 * internal/jsrun/testdata/parity/, which are committed. Run it by hand, with
 * Node 24, only when a probe is added, and review the diff like any other
 * code change:
 *
 *   node scripts/js-parity/record-engine.mjs            # rewrite the goldens
 *   node scripts/js-parity/record-engine.mjs --check    # report drift, write nothing
 *
 * Every probe is a Code-node body, compiled as a script inside the same
 * wrapper internal/jsrun/wrapper.go puts around it (so its first line starts
 * a line, as it does there), in a fresh vm context, and called the way the
 * runtime calls it. What is recorded is what V8 answers: the items the body
 * returned, or the error it threw as "Name: message".
 */

import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const out = path.join(root, 'internal/jsrun/testdata/parity');
const check = process.argv.includes('--check');

if (!process.versions.node.startsWith('24.')) {
	throw new Error(`record-engine.mjs: run this with Node 24 (found ${process.versions.node})`);
}

// wrapped is the body inside the wrapper the runtime compiles, for "Run once
// for all items".
function wrapped(code) {
	return `(function (items, $input) { return (async function () {\n${code}\n}).call(this); })`;
}

// run compiles and runs one body, and returns what it returned as the items'
// json, or the error it threw.
async function run(code) {
	const context = vm.createContext({});
	let wrapper;
	try {
		wrapper = new vm.Script(wrapped(code)).runInContext(context);
	} catch (error) {
		return { error: String(error.name) };
	}
	try {
		const value = await wrapper.call({ helpers: {} }, [{ json: {} }], {});
		return { want: JSON.parse(JSON.stringify(value.map((item) => item.json))) };
	} catch (error) {
		return { error: String(error.name) + ': ' + String(error.message) };
	}
}

async function record(probes) {
	const recorded = [];
	for (const probe of probes) recorded.push({ name: probe.name, code: probe.code, ...(await run(probe.code)) });
	return recorded;
}

// ---- HTML-like comments -------------------------------------------------------
//
// A body that does not parse is recorded as the error's name alone: the
// words of a syntax error are the parser's own, and goja's are not V8's.

const htmlComments = [
	{ name: 'the ticket: an opening line and a closing line', code: '<!-- an HTML-style comment left in pasted code\nreturn [{ json: { ok: true } }]\n--> and one closing it' },
	{ name: '<!-- after code ends the line', code: 'const a = 1 <!-- the rest of this line is a comment\nreturn [{ json: { a } }]' },
	{ name: 'x<!--y is x followed by a comment, not x < !--y', code: 'let y = 5\nconst a = 3<!--y\nreturn [{ json: { a, y } }]' },
	{ name: '--> after spaces and tabs starts a comment', code: 'const a = 1\n  \t--> a closing comment\nreturn [{ json: { a } }]' },
	{ name: '--> after a one-line block comment starts a comment', code: 'const a = 1\n/* first */ /* second */ --> a closing comment\nreturn [{ json: { a } }]' },
	{ name: '--> after a block comment that spans lines starts a comment', code: 'const a = 1\n/* a comment\n over two lines */ --> a closing comment\nreturn [{ json: { a } }]' },
	{ name: '--> on the body\'s first line starts a comment', code: '--> the first line\nreturn [{ json: { ok: 1 } }]' },
	{ name: '--> after \\r\\n starts a comment', code: 'const a = 1\r\n--> a closing comment\r\nreturn [{ json: { a } }]' },
	{ name: 'i-->0 in the middle of a line is a decrement and a comparison', code: 'let i = 3\nlet n = 0\nwhile (i-->0) n++\nreturn [{ json: { i, n } }]' },
	{ name: '--> after code on its line is a decrement and a comparison', code: 'let i = 3\nconst more = i --> 1\nreturn [{ json: { i, more } }]' },
	{ name: '<!-- and --> inside strings', code: "return [{ json: { single: '<!-- not a comment', double: \"--> nor this\", escaped: '\\'<!--' } }]" },
	{ name: '<!-- and --> inside a template literal', code: 'return [{ json: { t: `<!-- one\n--> two` } }]' },
	{ name: '<!-- inside a template\'s ${} is a comment', code: 'const t = `a${ 1 <!-- the rest of the line\n}b`\nreturn [{ json: { t } }]' },
	{ name: 'a nested template inside ${} keeps its <!--', code: 'const t = `a${ `<!--${ 2 }` }b`\nreturn [{ json: { t } }]' },
	{ name: '<!-- and --> inside regular expressions', code: "return [{ json: { open: /<!--/.test('x<!--'), close: /-->/.source, klass: /[<!--]/.test('!') } }]" },
	{ name: 'a regular expression after `if (…)` keeps its <!--', code: "let hit = false\nif (true) /x<!--/.test('x<!--') && (hit = true)\nreturn [{ json: { hit } }]" },
	{ name: 'a regular expression on its own line after a block keeps its -->', code: "let hit = false\n{ }\n/-->/.test('-->') && (hit = true)\nreturn [{ json: { hit } }]" },
	{ name: 'division followed by <!--', code: 'const a = 4, b = 2, g = 1\nconst q = a / b <!-- / g\nreturn [{ json: { q } }]' },
	{ name: '<!-- and --> inside line and block comments', code: '// <!-- a line comment\n/* <!--\n--> */ const a = 1\nreturn [{ json: { a } }]' },
	{ name: '<!-- ends at a line separator', code: 'let b = 0\nconst a = 1 <!-- a comment\u2028 b = 2\nreturn [{ json: { a, b } }]' },
	{ name: '<!-- comments out a closing brace', code: 'let a = 0\nif (true) { <!-- }\n a = 1 }\nreturn [{ json: { a } }]' },
	{ name: '<!-- in a strict body', code: "'use strict'\n<!-- a comment\nreturn [{ json: { ok: 1 } }]" },
	{ name: '<!-- inside a function keeps the function\'s source', code: 'function f() {\n  <!-- inside\n  return 1\n}\nreturn [{ json: { source: f.toString(), value: f() } }]' },
	{ name: 'a comment that eats what an array needs is a syntax error', code: 'const a = [1, <!-- 2]\nreturn [{ json: { a } }]' },
	{ name: '<<!-- is a shift, then !--y', code: 'let y = 5\nconst a = 1 <<!--y\nreturn [{ json: { a, y } }]' },
	{ name: 'a regular expression with escaped slashes keeps its <!--', code: "return [{ json: { hit: /\\/\\/<!--/.test('//<!--') } }]" },
	{ name: '-- > with a space is not a comment', code: 'let i = 3\n-- > 1\nreturn [{ json: { i } }]' },
];

// ---- The errors the built-ins throw -------------------------------------------
//
// Each body throws; the test runs it uncaught and caught.

const scope = "const a = { b: { c: {} }, 0: {} }; const k = 'k'; const f = () => ({}); const g = () => () => 1; const X = 5; const arr = [1]; const p = Promise.resolve({});\n";
const errors = [
	// Reading a property of undefined.
	{ name: 'a property of undefined', code: 'const none = undefined\nreturn [{ json: { v: none.field } }]' },
	{ name: 'a property of a missing property', code: scope + 'return a.q.r' },
	{ name: 'an index of undefined', code: 'const none = undefined\nreturn none[0]' },
	{ name: 'a computed property of undefined', code: "const none = undefined\nconst key = 'name'\nreturn none[key]" },
	{ name: 'a property of undefined inside a template', code: scope + 'return `${a.q.r}`' },
	{ name: 'a method of a missing property', code: scope + 'return a.q.r()' },
	{ name: 'a method of a property three deep', code: scope + 'return a.b.c.q.r()' },
	{ name: 'a method of what a call returned', code: "const $ = () => ({ first: () => ({ json: {} }) })\nreturn $('x').first().json.data.reduce(1)" },
	{ name: 'a method of an awaited undefined', code: 'async function h() {}\nreturn (await h()).map(1)' },
	{ name: 'a property of a missing index', code: scope + 'return arr[5].q' },
	{ name: 'a property of undefined in a nested function', code: 'function read(value) { return value.name }\nreturn read(undefined)' },
	{ name: 'a property of undefined in a callback', code: 'return [undefined].map((value) => value.name)' },
	// Calling what is not a function.
	{ name: 'a missing method', code: 'const value = {}\nreturn value.map((x) => x)' },
	{ name: 'a missing method two deep', code: scope + 'return a.b.q()' },
	{ name: 'a missing method on this', code: 'return this.nothing()' },
	{ name: 'a missing method through ?.', code: scope + 'return a?.q()' },
	{ name: 'a missing method after ?.', code: scope + 'return a?.b.q()' },
	{ name: 'a missing method after ?. deeper', code: scope + 'return a.b?.q()' },
	{ name: 'a missing method after two ?.', code: scope + 'return a?.b?.q()' },
	{ name: 'a missing index through ?.', code: scope + 'return a?.[0]()' },
	{ name: 'a missing method by a string key', code: scope + "return a['q']()" },
	{ name: 'a missing method by a string key with a dash', code: scope + "return a['q-r']()" },
	{ name: 'a missing method by an empty string key', code: scope + "return a['']()" },
	{ name: 'a missing method by a numeric string key', code: scope + "return a['0']()" },
	{ name: 'a missing method by an integer key', code: scope + 'return a[1]()' },
	{ name: 'a missing method by a fractional key', code: scope + 'return a[1.5]()' },
	{ name: 'a missing method by a hexadecimal key', code: scope + 'return a[0x10]()' },
	{ name: 'a missing method by a large key', code: scope + 'return a[1e21]()' },
	{ name: 'a missing method by a variable key', code: scope + 'return a[k]()' },
	{ name: 'a missing method by a member key', code: scope + 'return a[k.length]()' },
	{ name: 'a missing method by an indexed key', code: scope + 'return a[a[k]]()' },
	{ name: 'a missing method by a called key', code: scope + 'return a[f()]()' },
	{ name: 'a missing method by keys true, null and this', code: scope + 'const tries = []\nfor (const call of [() => a[true](), () => a[null](), () => a[this](), () => a[undefined]()]) { try { call() } catch (error) { tries.push(error.message) } }\nthrow new TypeError(tries.join(\' | \'))' },
	{ name: 'a missing method by a symbol key', code: scope + 'return a[Symbol.iterator]()' },
	{ name: 'a missing method after an index', code: scope + 'return a[0].q()' },
	{ name: 'a missing method after two string keys', code: scope + "return a['b']['q']()" },
	{ name: 'a missing method after a call', code: scope + 'return f().q()' },
	{ name: 'a missing method after a call with arguments', code: scope + 'return f(1, 2).q()' },
	{ name: 'a missing method after a map', code: scope + 'return arr.map((x) => x).q()' },
	{ name: 'a missing method in parentheses', code: scope + 'return (a.q)()' },
	{ name: 'a missing method inside typeof', code: scope + 'return typeof a.q()' },
	{ name: 'a missing method in an argument', code: scope + 'return a.b.c.toString(a.q())' },
	{ name: 'a missing method on the input root', code: 'return $input.q()' },
	{ name: 'a missing method on items', code: 'return items.q()' },
	{ name: 'a missing method on a global', code: 'return Math.q()' },
	{ name: 'a missing method on a string', code: "const s = 'abc'\nreturn s.foo()" },
	{ name: 'a missing method with a Unicode name', code: 'const é = {}\nreturn é.ü()' },
	{ name: 'a missing method after a Unicode string', code: "const s = 'ü😀'\nconst o = {}\nreturn o.q()" },
	{ name: 'a missing method in a nested function', code: 'function call(value) {\n  return value.run()\n}\nreturn call({})' },
	{ name: 'a missing method in a callback', code: 'return [{}].map((value) => value.run())' },
	{ name: 'a missing method on a later line of a chain', code: 'const list = {}\nreturn list\n  .filter((x) => x)\n  .map((x) => x)' },
	{ name: 'a property that is a number, called', code: 'const o = { f: 3 }\nreturn o.f()' },
	{ name: 'a property that is an object, called', code: 'const o = { f: {} }\nreturn o.f()' },
	{ name: 'a variable that is a number, called', code: scope + 'return X()' },
	{ name: 'a call that returned a number, called', code: scope + 'return g()()()' },
	{ name: 'a call that returned an object, called', code: scope + 'return f()()' },
	{ name: 'this, called', code: 'return this()' },
	// What goja says in its own words, recorded so the difference is listed.
	{ name: 'a missing method of what await returned', code: scope + 'return (await p).q()' },
	{ name: 'a missing method of what new returned', code: 'return new Date().q()' },
	{ name: 'a missing method of a string literal', code: "return 's'.q()" },
	{ name: 'a missing method of an array literal', code: 'return [1, 2].q()' },
	{ name: 'a missing method through a comma', code: scope + 'return (0, a.q)()' },
	{ name: 'a missing method by a computed sum', code: scope + "return a[k + 'x']()" },
	{ name: 'a tagged template of a missing function', code: scope + 'return a.q`t`' },
	{ name: 'a callback that is not a function', code: 'return [1].map(1)' },
	{ name: 'a property of null', code: 'const none = null\nreturn none.field' },
	{ name: 'a method of null', code: "const found = 'abc'.match(/x/)\nreturn found.map((x) => x)" },
	{ name: 'a symbol property of undefined', code: "const none = undefined\nreturn none[Symbol('k')]" },
	{ name: 'setting a property of undefined', code: 'const none = undefined\nnone.field = 1' },
	{ name: 'destructuring undefined', code: 'const { field } = undefined' },
	{ name: 'iterating undefined', code: 'for (const x of undefined) {}' },
	{ name: 'iterating an object', code: 'const input = {}\nfor (const x of input) {}' },
	{ name: "the 'in' operator on a string", code: "return 'k' in 's'" },
	// Constructing what is not a constructor.
	{ name: 'new of a missing property', code: scope + 'return new a.q()' },
	{ name: 'new of a number', code: scope + 'return new X()' },
	{ name: 'new of an object', code: scope + 'return new a.b()' },
	{ name: 'new of an index', code: scope + 'return new a[0]()' },
	{ name: 'new of an arrow function', code: scope + 'return new f()' },
	{ name: 'new of a call', code: scope + 'return new (f())()' },
	{ name: 'new without parentheses', code: scope + 'return new a.b' },
	// Errors that are not the engine's stay as they were thrown.
	{ name: "the code's own TypeError", code: "throw new TypeError('value.map is not a function')" },
	{ name: "the code's own Error", code: "throw new Error(\"Cannot read property 'x' of undefined\")" },
	{ name: 'a ReferenceError', code: 'return undefinedFunction()' },
];

// ---- JSON.parse -----------------------------------------------------------------
//
// Each text is parsed by JSON.parse; the golden records V8's message for the
// ones that fail, and null for the ones that parse.

const long = '{"key":"value","other":[1,2,3],"x":tru, "and more text": "here to make it long enough"}';
const jsonTexts = [
	// What the corpus and the ticket found.
	'sample', '{"a":', '',
	// Where a value was expected.
	' ', 'undefined', 'NaN', 'Infinity', '[object Object]', ' undefined', 'undefinedx', '[object Array]', 'u', 'True', '.5', '+1', '/', "'a'", '`a`',
	']', '}', ':', ',', '[1,]', '[,]', '[,1]', '[1,,2]', '{"a"::1}', '{"a":}', '{"a":,}', '[1, x]', '[undefined]', '\u00a0 1', '\u000b1', '\f1', '\ufeff{}', 'Ω', '😀', '[😀]',
	'[', '[1,', '{"a":[', '{"a":{"b":',
	// Literals.
	't', 'tr', 'tru', 'nul', 'fals', 'nx', 'trux', 'truee', 'nulll', 'falsey', 'null,', '{"a" : tru }',
	// Objects.
	'{', '{"a"', '{"a" 1}', '{"a"}', '{"a",}', '{"a" }', '{"a" x}', '{a:1}', "{'a':1}", '{1:2}', '{,}', '{]', '[{]', '{"a":{',
	'{"a":1,}', '{"a":1,,}', '{"a":1,', '{"":1,x}', '{"a":1 "b"}', '{"a":1', '{"a":1 ', '{"a":1 x}', '{"a":1 ,"b":2 x}', '{"a":"b" "c"}', '{"a":1,"b"}',
	// Arrays.
	'[1 2]', '[1', '[1,2', '["a"', '[1}', '{"a":[1,2}', '["a" "b"]', '["a":1]', '[null x]', '[true false]',
	// Strings.
	'"abc', '"😀', '"a\nb"', '"\t"', '"\u0000"', '"\u001f"', '"\\x"', '{"a":"\\x', '"\\u12"', '"\\uZZZZ"', '"\\uDZ"', '"\\u', '"\\u00"', '"\\', '"a\\', '{"a":"\\', '"abc\\"', '"\\u0041', '"abc\\u00e9',
	// Numbers.
	'-', '-a', '-.', '[-]', '[-', '{"a":-}', '-Infinity', '00', '-01', '[01]', '{"a":01}', '1.', '1.e', '1.a', '1..', '[1.]', '1e', '1ex', '1e+', '1e+x', '-1.5e-', '[1e', '[1.5e]',
	// After the value.
	'1 2', '0 1', '{} x', '{"a":1}}', '"a":1', '"a" "b"', '{"a":1}"x"', '[1]2', '1x', '0x10', '-0x', '1e5.5', '"undefined"x', '12345678901234567890123x',
	// Positions across lines.
	'{\r\n"a":1,}', '{"a":1,\n\n\n}', '{\r"a":1,}', '{"a":1,\n\r\n}', '{"a":1}\n\nz', '\n\n  {\n "a": x}', '[\n1,\n2,\nx]', 'a\r\nb', '{\u2028"a":1,}',
	// The context a long text is shown with.
	'abcdefghijklmnopqrstuvwxyz0123456789', 'a'.repeat(100), '{"key":"value","other":[1,2,3],"x":tru}', long, '[1,2,3,4,5,6,7,8,9,10,x]',
	'x' + 'a'.repeat(19), 'x' + 'a'.repeat(20),
	...[0, 8, 9, 10, 11, 20, 21, 29].map((spaces) => '[' + ' '.repeat(spaces) + 'x' + ' '.repeat(30 - spaces)),
	// Texts that parse.
	'null', '1', '"x"', '{"a":[1,{"b":null}],"c":"\\u00e9"}', ' [ ] ', '-0.5e+10', '"\\uD83D"', '"\u007f"', '{ "a":1 }  \t',
];

function jsonMessage(text) {
	try {
		JSON.parse(text);
		return null;
	} catch (error) {
		return String(error.name) + ': ' + String(error.message);
	}
}

// What JSON.parse makes of values that are not strings.
const jsonValues = [
	{ name: 'undefined', code: 'return JSON.parse(undefined)' },
	{ name: 'no argument', code: 'return JSON.parse()' },
	{ name: 'an object', code: 'return JSON.parse({})' },
	{ name: 'a function', code: 'return JSON.parse(function () {})' },
	{ name: 'null', code: 'return [{ json: { v: JSON.parse(null) } }]' },
	{ name: 'a number', code: 'return [{ json: { v: JSON.parse(5) } }]' },
	{ name: 'an array', code: 'return [{ json: { v: JSON.parse([1, 2]) } }]' },
	{ name: 'an object whose toString is JSON', code: "return [{ json: { v: JSON.parse({ toString: () => '[3]' }) } }]" },
	{ name: 'a reviver', code: "return [{ json: JSON.parse('{\"a\":1,\"b\":2}', (key, value) => typeof value === 'number' ? value * 10 : value) }]" },
	{ name: 'a reviver that throws a SyntaxError', code: "return JSON.parse('[1]', () => { throw new SyntaxError('from the reviver') })" },
	{ name: "JSON.parse's name and length", code: 'return [{ json: { name: JSON.parse.name, length: JSON.parse.length } }]' },
	{ name: 'a caught message', code: "try { JSON.parse('{\"a\":') } catch (e) { return [{ json: { m: e.message, name: e.name, isSyntax: e instanceof SyntaxError } }] }" },
];

// ---- Writing ------------------------------------------------------------------

const meta = {
	recordedWith: `node ${process.versions.node}, v8 ${process.versions.v8}`,
	note: 'Recorded by scripts/js-parity/record-engine.mjs. CI compares against this file and never runs Node.',
};

const files = {
	'html-comments.json': { meta, probes: await record(htmlComments) },
	'errors.json': {
		meta,
		probes: await record(errors),
		json: jsonTexts.map((text) => ({ text, error: jsonMessage(text) })),
		jsonValues: await record(jsonValues),
	},
};

// serialize writes one probe per line, so a golden stays readable and its
// diff shows exactly what changed.
function serialize(content) {
	const lines = ['{'];
	const keys = Object.keys(content);
	keys.forEach((key, index) => {
		const value = content[key];
		const comma = index < keys.length - 1 ? ',' : '';
		if (!Array.isArray(value)) {
			lines.push('\t' + JSON.stringify(key) + ': ' + JSON.stringify(value) + comma);
			return;
		}
		lines.push('\t' + JSON.stringify(key) + ': [');
		value.forEach((entry, position) => lines.push('\t\t' + JSON.stringify(entry) + (position < value.length - 1 ? ',' : '')));
		lines.push('\t]' + comma);
	});
	lines.push('}');
	return lines.join('\n') + '\n';
}

let drift = false;
fs.mkdirSync(out, { recursive: true });
for (const [name, content] of Object.entries(files)) {
	const text = serialize(content);
	const target = path.join(out, name);
	const previous = fs.existsSync(target) ? fs.readFileSync(target, 'utf8') : '';
	if (previous === text) continue;
	drift = true;
	console.log((check ? 'drift: ' : 'wrote: ') + path.relative(root, target));
	if (!check) fs.writeFileSync(target, text);
}
if (check && drift) process.exitCode = 1;
