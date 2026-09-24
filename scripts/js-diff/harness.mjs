/**
 * The Node.js side of `make js-diff`: runs Code-node bodies under V8 with the
 * same roots the embedded runtime gives them, so what differs between the two
 * runs is the engine and nothing else.
 *
 * DEV-ONLY. The product never runs Node, and neither does CI. The Go test
 * TestJSDiff (internal/jsrun/corpus) writes the cases, runs this script, and
 * diffs the answers it writes against what internal/jsrun returned for the same body
 * and input:
 *
 *   node scripts/js-diff/harness.mjs <cases.json> <answers.json>
 *
 * The roots below are KilasFlow's own contract, written here from
 * internal/jsrun's documentation and behaviour (the wrapper, $input, items,
 * $json and $binary, $('Node'), $node, $items, the return normalisation,
 * console, require), not
 * n8n's task runner. Luxon and lodash are the same vendored bundles the
 * runtime embeds, configured the way it configures them: UTC and en-US.
 *
 * Every case runs twice in fresh contexts. A body whose two runs disagree
 * reads a clock or a random source, and is reported as nondeterministic
 * rather than as a difference between the engines.
 *
 * Each case runs in a worker thread of its own, which is ended when it passes
 * its limit: a vm timeout covers only synchronous code, and a loop that spins
 * after an await would otherwise hold the harness for good.
 */

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import util from 'node:util';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';
import { Worker, isMainThread, parentPort, workerData } from 'node:worker_threads';

process.env.TZ = 'UTC';

if (isMainThread && !process.versions.node.startsWith('24.')) {
	throw new Error(`harness.mjs: run this with Node 24 (found ${process.versions.node})`);
}

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const luxonSource = fs.readFileSync(path.join(root, 'third_party/luxon/luxon.min.js'), 'utf8');
const lodashSource = fs.readFileSync(path.join(root, 'third_party/lodash/lodash.min.js'), 'utf8');

// A body's promise that nothing settles, or a timer it never clears, must not
// hold the harness: each run is given this long, as the runtime's own limit.
const RUN_LIMIT_MS = 10_000;

// A rejection nobody handled belongs to the run that made it, which has
// already reported its result; it must not end the harness.
process.on('unhandledRejection', () => {});

class InvalidReturn extends Error {}

function refusal(subject) {
	return `this node's code ${subject}, which this server does not run. Rewrite that part of the code, or do the same work with native nodes.`;
}

function isObject(value) {
	return value !== null && typeof value === 'object' && !Array.isArray(value);
}

// toItem and normalise follow the runtime's return rules: a list of items or
// plain objects in all-items mode, one object (or null, to drop the item) in
// per-item mode.
function toItem(entry, where) {
	if (!isObject(entry)) throw new InvalidReturn(`${where} is not an object`);
	if (!('json' in entry)) return { json: entry };
	if (!isObject(entry.json)) throw new InvalidReturn(`${where} has a json that is not an object`);
	const item = { json: entry.json };
	if (entry.binary !== undefined && entry.binary !== null) {
		if (!isObject(entry.binary)) throw new InvalidReturn(`${where} has a binary that is not an object`);
		item.binary = entry.binary;
	}
	return item;
}

function normalise(result, eachItem) {
	if (eachItem) {
		if (result === null) return [];
		if (result === undefined) throw new InvalidReturn('the code returned nothing');
		if (Array.isArray(result)) throw new InvalidReturn('the code returned a list in per-item mode');
		return [toItem(result, 'the returned value')];
	}
	if (Array.isArray(result)) return result.map((entry, index) => toItem(entry, `item ${index}`));
	if (isObject(result)) return [toItem(result, 'the returned value')];
	throw new InvalidReturn('the code did not return a list of items');
}

// context builds one run's globals over a case's input.
function context(testCase, lines) {
	const sandbox = vm.createContext({});
	const inContext = (code) => vm.runInContext(code, sandbox);
	// The input is parsed inside the context, so `x instanceof Array` and
	// Array.isArray agree for the body as they do in the runtime.
	const parse = (value) => inContext(`JSON.parse(${JSON.stringify(JSON.stringify(value))})`);
	const input = parse(testCase.items.map((item) => ({ json: item })));
	const views = {};
	for (const [name, items] of Object.entries(testCase.nodes)) views[name] = parse(items.map((json) => ({ json })));

	// Inside a function, so the bundle's own `var luxon` is not a global:
	// the runtime offers Luxon as DateTime and its siblings, and through
	// require('luxon'), never as a global named luxon.
	const luxon = inContext('(function () {\n' + luxonSource + '\n;return luxon; })()');
	luxon.Settings.defaultZone = 'UTC';
	luxon.Settings.defaultLocale = 'en-US';
	const lodash = inContext(lodashSource + '\n;_.noConflict()');

	let current = 0;
	// read is one output of a node's latest run, as the runtime reads it. A
	// case's nodes have one output and one run, run 0.
	const read = (name, label, output, run) => {
		name = String(name);
		if (!views[name]) throw new Error(`node "${name}" has not run in this execution, or there is no node by that name`);
		if (output !== undefined && output !== 0) {
			throw new Error(`${label} names output ${String(output)} of node "${name}", which has no such output`);
		}
		if (run !== undefined && run !== -1 && run !== 0) {
			throw new Error(`${label} names run ${String(run)} of node "${name}", which has no such run`);
		}
		return views[name];
	};
	const nodeView = (name) => {
		name = String(name);
		const items = views[name];
		const executed = () => {
			if (!items) throw new Error(`node "${name}" has not run in this execution, or there is no node by that name`);
		};
		const paired = (index) => {
			executed();
			if (items.length === 0) throw new Error(`$("${name}").item: node "${name}" produced no items`);
			return items[Math.min(index, items.length - 1)];
		};
		const view = {
			all: (branch, run) => read(name, `$("${name}").all()`, branch ?? undefined, run),
			first: () => (executed(), items[0]),
			last: () => (executed(), items[items.length - 1]),
			itemMatching: (index) => paired(index),
			params: items ? {} : undefined,
			isExecuted: !!items,
		};
		Object.defineProperty(view, 'item', { get: () => paired(current), enumerable: true });
		return view;
	};
	const inputRoot = { all: () => input, first: () => input[0], last: () => input[input.length - 1] };
	Object.defineProperty(inputRoot, 'item', { get: () => input[current], enumerable: true });

	const staticData = {};
	const printed = (level) => (...args) => lines.push(`${level}: ${util.format(...args)}`);
	const globals = {
		$: nodeView,
		$items: (name, output, run) => (name === undefined || name === null ? input : read(name, '$items()', output || 0, run)),
		$node: new Proxy({}, {
			get: (_, name) => {
				if (typeof name !== 'string' || !views[name]) return undefined;
				const all = views[name];
				const chosen = all[Math.min(current, all.length - 1)];
				return { json: chosen ? chosen.json : {}, parameter: {}, params: {}, isExecuted: true };
			},
		}),
		$workflow: parse(testCase.workflow),
		$execution: parse(testCase.execution),
		$env: parse({}),
		$vars: parse({}),
		$runIndex: 0,
		$nodeVersion: 1,
		$getWorkflowStaticData: (type) => {
			if (type !== 'global' && type !== 'node') throw new Error(`$getWorkflowStaticData takes 'global' or 'node', not ${String(type)}`);
			staticData[type] ??= parse({});
			return staticData[type];
		},
		console: { log: printed('log'), info: printed('info'), warn: printed('warn'), error: printed('error'), debug: printed('debug') },
		require: (request) => {
			const name = String(request).replace(/^node:/, '');
			switch (name) {
				case 'crypto': return crypto;
				case 'luxon': return luxon;
				case 'lodash': return lodash;
				case 'buffer': return { Buffer };
				case 'util': return util;
				case 'url': return { URL, URLSearchParams };
			}
			throw new Error(refusal(`requires the module "${name}"`));
		},
		Buffer, URL, URLSearchParams, TextEncoder, TextDecoder, atob, btoa, structuredClone, queueMicrotask,
		setTimeout, setInterval, setImmediate, clearTimeout, clearInterval, clearImmediate,
		crypto: crypto.webcrypto,
		DateTime: luxon.DateTime, Duration: luxon.Duration, Interval: luxon.Interval, Info: luxon.Info, Settings: luxon.Settings,
	};
	for (const [name, value] of Object.entries(globals)) sandbox[name] = value;
	Object.defineProperty(sandbox, '$now', { get: () => luxon.DateTime.now() });
	Object.defineProperty(sandbox, '$today', { get: () => luxon.DateTime.now().startOf('day') });
	for (const name of ['$jmespath', '$evaluateExpression']) {
		sandbox[name] = () => { throw new Error(refusal(`uses ${name}`)); };
	}
	Object.defineProperty(sandbox, '$prevNode', { get: () => { throw new Error(refusal('uses $prevNode')); } });

	const helpers = {};
	for (const name of ['httpRequest', 'httpRequestWithAuthentication', 'request', 'getBinaryDataBuffer', 'prepareBinaryData']) {
		helpers[name] = () => Promise.reject(new Error(`this.helpers.${name} is not available here`));
	}
	// $binary is an item's files' metadata; a case's items carry none.
	const binaryOf = (item) => (item === undefined ? undefined : parse({}));
	return { sandbox, input, inputRoot, helpers, binaryOf, setCurrent: (index) => { current = index; } };
}

function withLimit(promise) {
	let timer;
	const limit = new Promise((_, reject) => {
		timer = setTimeout(() => reject(new Error(`the run passed its ${RUN_LIMIT_MS} ms limit`)), RUN_LIMIT_MS);
	});
	return Promise.race([promise, limit]).finally(() => clearTimeout(timer));
}

// wrapperText is the body inside the function the runtime wraps it in.
function wrapperText(testCase) {
	if (testCase.mode === 'sortComparator') return `(function (items, $input) { return function (a, b) {\n${testCase.source}\n}; })`;
	// All-items code has the per-item roots too, at the first item.
	const parameters = testCase.mode === 'runOnceForEachItem' ? '$json, $binary, $itemIndex, $position, $input' : 'items, $input, $json, $binary, $itemIndex, $position';
	return `(function (${parameters}) { return (async function () {\n${testCase.source}\n}).call(this); })`;
}

// parses answers a ParseOnly case: whether V8 parses the wrapped body as a
// script, which is how the runtime and n8n both compile it.
function parses(testCase) {
	try {
		new vm.Script(wrapperText(testCase));
		return { key: testCase.key, parsed: true, console: [], deterministic: true };
	} catch (thrown) {
		return { key: testCase.key, parsed: false, error: { name: String(thrown?.name), message: String(thrown?.message) }, console: [], deterministic: true };
	}
}

// runOnce runs a case in a fresh context and returns what it produced.
async function runOnce(testCase) {
	const lines = [];
	const { sandbox, input, inputRoot, helpers, binaryOf, setCurrent } = context(testCase, lines);
	const options = { timeout: RUN_LIMIT_MS };
	try {
		if (testCase.mode === 'sortComparator') {
			const factory = vm.runInContext(wrapperText(testCase), sandbox, options);
			const compare = factory.call({ helpers }, input, inputRoot);
			const order = input.map((_, index) => index);
			order.sort((left, right) => {
				const answer = compare.call({ helpers }, input[left], input[right]);
				if (typeof answer !== 'number' || Number.isNaN(answer)) throw new InvalidReturn('the comparator did not return a number');
				return answer;
			});
			return { order, console: lines };
		}
		const eachItem = testCase.mode === 'runOnceForEachItem';
		const wrapper = vm.runInContext(wrapperText(testCase), sandbox, options);
		const items = [];
		if (eachItem) {
			for (let index = 0; index < input.length; index++) {
				setCurrent(index);
				items.push(...normalise(await withLimit(wrapper.call({ helpers }, input[index].json, binaryOf(input[index]), index, index, inputRoot)), true));
			}
		} else {
			items.push(...normalise(await withLimit(wrapper.call({ helpers }, input, inputRoot, input[0]?.json, binaryOf(input[0]), 0, 0)), false));
		}
		// Through JSON, as the runtime hands items back: a Date becomes its
		// ISO text, undefined disappears.
		return { items: JSON.parse(JSON.stringify(items.map((item) => item.json))), console: lines };
	} catch (thrown) {
		const error = thrown instanceof InvalidReturn
			? { name: 'InvalidReturn', message: thrown.message }
			: { name: String(thrown?.name ?? ''), message: String(thrown?.message ?? thrown) };
		return { error, console: lines };
	}
}

// runCase runs one case, twice, in a worker thread, and ends the thread when
// it passes its limit.
function runCase(testCase) {
	return new Promise((resolve) => {
		const worker = new Worker(fileURLToPath(import.meta.url), { workerData: testCase });
		const timer = setTimeout(() => {
			worker.terminate();
			resolve({ key: testCase.key, error: { name: 'Timeout', message: `the run passed its ${RUN_LIMIT_MS} ms limit` }, console: [], deterministic: true });
		}, 2 * RUN_LIMIT_MS + 5_000);
		worker.once('message', (answer) => {
			clearTimeout(timer);
			worker.terminate();
			resolve(answer);
		});
		worker.once('error', (error) => {
			clearTimeout(timer);
			resolve({ key: testCase.key, error: { name: 'HarnessError', message: String(error?.message ?? error) }, console: [], deterministic: true });
		});
	});
}

if (isMainThread) {
	const cases = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
	const results = [];
	for (const testCase of cases) results.push(testCase.parseOnly ? parses(testCase) : await runCase(testCase));
	// Written synchronously: the process exits next, which would cut a
	// write to a pipe short, and a timer a body left behind must not keep
	// it alive.
	fs.writeFileSync(process.argv[3], JSON.stringify(results));
	process.exit(0);
} else {
	const first = await runOnce(workerData);
	const second = await runOnce(workerData);
	const same = JSON.stringify({ ...first, console: undefined }) === JSON.stringify({ ...second, console: undefined });
	parentPort.postMessage({ key: workerData.key, ...first, deterministic: same });
}
