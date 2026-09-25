import { execFile, spawn } from 'node:child_process';
import { promisify } from 'node:util';
import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures';
import { deliver, liveApi } from '../fixtures/live-backend';
import { activateWorkflow, importN8nTemplate } from '../fixtures/waha-migration';
import { descendants, isNodeJS, processTable, proveCodeNodeWorkflow, wholeTableSampler } from '../fixtures/epic-code';
import { exportN8nWorkflow, listExecutionIds, waitForExecution } from '../helpers/seed';

const execFileAsync = promisify(execFile);

// The Code (JavaScript) node on the built binary (EPIC-tjnr1z, FEAT-pxcbqj).
//
// Every case here is a live workflow: seeded over the public API or imported
// from n8n JSON, run for real, and asserted through the execution record, the
// webhook answer, the export, the process table or the browser. The code runs
// on the embedded engine in the server's worker processes (FEAT-g6k3y9), never
// in Node.js, and the cases wire it to the nodes an imported workflow puts
// around it: webhooks and Respond to Webhook, IF and Set, HTTP Request against
// the loopback stub, Split Out and Aggregate, error outputs.
//
// RUNTIME_CASES below is the one-line-per-feature table: a new shipped module
// or global is proven live by adding an entry, no new test needed.

interface SpecNode {
	id: string;
	name: string;
	type: string;
	typeVersion: number;
	position: { x: number; y: number };
	parameters?: Record<string, unknown>;
	settings?: Record<string, unknown>;
}

interface SpecConnection {
	id: string;
	kind: string;
	source: { nodeId: string; port: string };
	target: { nodeId: string; port: string };
}

let column = 0;

function node(id: string, name: string, type: string, parameters?: Record<string, unknown>, settings?: Record<string, unknown>): SpecNode {
	column += 1;
	const entry: SpecNode = { id, name, type, typeVersion: 1, position: { x: column * 220, y: 0 } };
	if (parameters !== undefined) entry.parameters = parameters;
	if (settings !== undefined) entry.settings = settings;
	return entry;
}

const manual = () => node('manual', 'Manual Trigger', 'kilasflow.manual');

// js is a Code (JavaScript) node. Mode defaults to n8n's own default.
function js(
	id: string,
	name: string,
	source: string,
	options: { mode?: 'runOnceForAllItems' | 'runOnceForEachItem'; timeoutSeconds?: number; settings?: Record<string, unknown> } = {}
): SpecNode {
	const parameters: Record<string, unknown> = { mode: options.mode ?? 'runOnceForAllItems', jsCode: source };
	if (options.timeoutSeconds !== undefined) parameters.scriptTimeoutSeconds = options.timeoutSeconds;
	return node(id, name, 'kilasflow.jsCode', parameters, options.settings);
}

function expression(value: string): Record<string, unknown> {
	return { mode: 'expression', value };
}

function conn(id: string, source: string, target: string, sourcePort = 'main', targetPort = 'main'): SpecConnection {
	return { id, kind: 'main', source: { nodeId: source, port: sourcePort }, target: { nodeId: target, port: targetPort } };
}

// chain wires nodes one after another on their main ports.
function chain(...ids: string[]): SpecConnection[] {
	return ids.slice(1).map((id, index) => conn(`c${index + 1}`, ids[index], id));
}

async function createWorkflow(baseURL: string, name: string, nodes: SpecNode[], connections: SpecConnection[]): Promise<string> {
	const created = await liveApi<{ id: string }>(baseURL, 'POST', '/workflows', { schemaVersion: 1, name, nodes, connections, settings: {} }, 201);
	return created.id;
}

async function run(baseURL: string, workflowId: string, input?: unknown): Promise<any> {
	const started = await liveApi<{ id: string }>(baseURL, 'POST', `/workflows/${workflowId}/run`, input === undefined ? {} : { input }, 202);
	return waitForExecution(baseURL, started.id);
}

async function runToSuccess(baseURL: string, workflowId: string, input?: unknown): Promise<any> {
	const record = await run(baseURL, workflowId, input);
	expect(record.status, `execution ${record.id}: ${JSON.stringify(record.error ?? record.nodeRuns?.find((entry: any) => entry.error)?.error)}`).toBe('succeeded');
	return record;
}

function nodeRun(record: any, nodeId: string): any {
	const found = (record.nodeRuns as any[]).find((entry) => entry.nodeId === nodeId);
	expect(found, `node ${nodeId} has a recorded run`).toBeDefined();
	return found;
}

// items is one output port's items as their json.
function items(record: any, nodeId: string, port = 0): any[] {
	const found = nodeRun(record, nodeId);
	return ((found.output?.[port] ?? []) as Array<{ json: any }>).map((item) => item.json);
}

function consoleTexts(record: any, nodeId: string): string[] {
	const printed = nodeRun(record, nodeId).console as { lines?: Array<{ text: string }> } | undefined;
	return (printed?.lines ?? []).map((line) => line.text);
}

// The refusal a run answers with: the joined validation messages.
async function refusedRun(baseURL: string, workflowId: string): Promise<string> {
	const response = await fetch(`${baseURL}/api/v1/workflows/${workflowId}/run`, {
		method: 'POST',
		headers: { 'content-type': 'application/json' },
		body: '{}'
	});
	expect(response.status).toBe(422);
	const body = (await response.json()) as { errors?: Array<{ message?: string }> };
	return (body.errors ?? []).map((entry) => entry.message ?? '').join('\n');
}

async function newestExecution(baseURL: string, workflowId: string, before: string[]): Promise<any> {
	let found = '';
	await expect
		.poll(
			async () => {
				found = (await listExecutionIds(baseURL, workflowId)).find((id) => !before.includes(id)) ?? '';
				return found;
			},
			{ message: `a new execution of ${workflowId}`, timeout: 30_000 }
		)
		.not.toBe('');
	return waitForExecution(baseURL, found);
}

// ---------------------------------------------------------------------------
// The shipped modules and globals, one live run each. Add a case here to prove
// a new one: `source` is the node's code (all-items mode, one manual item in),
// `expect` is matched against the first returned item.

interface RuntimeCase {
	name: string;
	source: string;
	expect: Record<string, unknown>;
}

const RUNTIME_CASES: RuntimeCase[] = [
	{
		name: 'lodash from require',
		source: "const _ = require('lodash')\nreturn [{ json: { chunks: _.chunk([1, 2, 3, 4, 5], 2), picked: _.pick({ a: 1, b: 2, c: 3 }, ['a', 'c']), global: typeof _ } }]",
		expect: { chunks: [[1, 2], [3, 4], [5]], picked: { a: 1, c: 3 }, global: 'function' }
	},
	{
		name: 'Buffer encodings',
		source: "return [{ json: { hex: Buffer.from('KilasFlow').toString('hex'), b64: Buffer.from('héllo').toString('base64'), back: Buffer.from('aMOpbGxv', 'base64').toString('utf8') } }]",
		expect: { hex: '4b696c6173466c6f77', b64: 'aMOpbGxv', back: 'héllo' }
	},
	{
		name: 'WHATWG URL',
		source:
			"const url = new URL('https://example.com/a/b?x=1&y=two#frag')\nurl.searchParams.append('z', '3')\n" +
			'return [{ json: { host: url.host, path: url.pathname, query: url.search, y: url.searchParams.get(\'y\'), hash: url.hash } }]',
		expect: { host: 'example.com', path: '/a/b', query: '?x=1&y=two&z=3', y: 'two', hash: '#frag' }
	},
	{
		name: 'TextEncoder, atob and btoa',
		source: "return [{ json: { bytes: Array.from(new TextEncoder().encode('é')), text: new TextDecoder().decode(new Uint8Array([111, 107])), round: atob(btoa('ok')) } }]",
		expect: { bytes: [195, 169], text: 'ok', round: 'ok' }
	},
	{
		name: 'timers and await',
		source: 'const started = Date.now()\nawait new Promise((resolve) => setTimeout(resolve, 25))\nreturn [{ json: { waited: Date.now() - started >= 20 } }]',
		expect: { waited: true }
	},
	{
		name: 'structuredClone',
		source:
			"const original = { when: new Date(0), tags: new Set(['a']), nested: { n: 1 } }\nconst copy = structuredClone(original)\ncopy.nested.n = 2\n" +
			'return [{ json: { date: copy.when.getTime(), tag: copy.tags.has(\'a\'), untouched: original.nested.n } }]',
		expect: { date: 0, tag: true, untouched: 1 }
	},
	{
		name: "Node's crypto",
		source:
			"const crypto = require('crypto')\nconst key = crypto.createHash('sha256').update('k').digest()\nconst iv = Buffer.alloc(12, 1)\n" +
			"const cipher = crypto.createCipheriv('aes-256-gcm', key, iv)\nconst sealed = Buffer.concat([cipher.update('secret', 'utf8'), cipher.final()])\n" +
			"const decipher = crypto.createDecipheriv('aes-256-gcm', key, iv)\ndecipher.setAuthTag(cipher.getAuthTag())\n" +
			"return [{ json: { sha: crypto.createHash('sha256').update('abc').digest('hex'), hmac: crypto.createHmac('sha256', 'key').update('The quick brown fox jumps over the lazy dog').digest('base64'), " +
			"uuid: /^[0-9a-f-]{36}$/.test(crypto.randomUUID()), opened: decipher.update(sealed, undefined, 'utf8') + decipher.final('utf8'), web: typeof globalThis.crypto.getRandomValues } }]",
		expect: {
			sha: 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad',
			hmac: '97yD9DBThCSxMpjmqm+xQ+9NWaFJRhdZl0edvC0aPNg=',
			uuid: true,
			opened: 'secret',
			web: 'function'
		}
	},
	{
		name: 'Luxon and $now',
		source:
			"const { DateTime } = require('luxon')\n" +
			"return [{ json: { dst: DateTime.fromISO('2026-03-08T01:30:00', { zone: 'America/New_York' }).plus({ hours: 1 }).toISO(), " +
			"parsed: DateTime.fromFormat('23/09/2026', 'dd/MM/yyyy', { zone: 'UTC' }).toISODate(), " +
			"shown: DateTime.fromISO('2026-09-23T15:04:00Z', { zone: 'Asia/Jakarta' }).toFormat('cccc d LLLL yyyy HH:mm ZZZZ'), " +
			'now: $now.isValid && $today.hour === 0, same: DateTime === require(\'luxon\').DateTime } }]',
		expect: {
			dst: '2026-03-08T03:30:00.000-04:00',
			parsed: '2026-09-23',
			shown: 'Wednesday 23 September 2026 22:04 GMT+7',
			now: true,
			same: true
		}
	},
	{
		name: 'Intl and locale formatting',
		source:
			"return [{ json: { eur: new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).format(1234.5), en: (1234567.891).toLocaleString('en-US'), " +
			"date: new Date(Date.UTC(2026, 8, 23, 15, 4)).toLocaleString('en-US', { timeZone: 'Asia/Jakarta' }) } }]",
		expect: { eur: '1.234,50 €', en: '1,234,567.891', date: '9/23/2026, 10:04:00 PM' }
	},
	{
		name: 'workflow and execution roots',
		source: "return [{ json: { workflow: $workflow.name, execution: typeof $execution.id, runIndex: $runIndex, input: $input.first().json.from } }]",
		expect: { workflow: 'JS Runtime Case', execution: 'string', runIndex: 0, input: 'the run request' }
	}
];

test('the runtime ships its modules and globals to a live run', async ({ server }) => {
	for (const entry of RUNTIME_CASES) {
		const workflowId = await createWorkflow(server.baseURL, 'JS Runtime Case', [manual(), js('js', 'Case', entry.source)], chain('manual', 'js'));
		const record = await runToSuccess(server.baseURL, workflowId, { from: 'the run request' });
		expect(items(record, 'js')[0], entry.name).toMatchObject(entry.expect);
	}
});

// ---------------------------------------------------------------------------

test('an imported n8n webhook API scores an order in JavaScript, branches on it, and answers from its result', async ({ server }) => {
	const score = [
		"const _ = require('lodash');",
		'const order = $input.first().json.body;',
		'const total = _.round(_.sumBy(order.lines, (line) => line.qty * line.price), 2);',
		'const skus = _.uniq(order.lines.map((line) => line.sku)).sort();',
		"console.log('scored', order.orderId, 'total', total);",
		"return [{ json: { orderId: order.orderId, customer: _.startCase(order.customer), total, skus, tier: total >= 100 ? 'priority' : 'standard' } }];"
	].join('\n');
	const setLane = (lane: string, etaDays: number) => ({
		includeOtherFields: true,
		assignments: {
			assignments: [
				{ id: '1', name: 'lane', type: 'string', value: lane },
				{ id: '2', name: 'etaDays', type: 'number', value: etaDays }
			]
		}
	});
	const exported = {
		name: 'Order Scoring API',
		nodes: [
			{ id: 'a', name: 'Webhook', type: 'n8n-nodes-base.webhook', typeVersion: 2, position: [0, 0], parameters: { path: 'score-order', httpMethod: 'POST', responseMode: 'responseNode', authentication: 'none' } },
			{ id: 'b', name: 'Score Order', type: 'n8n-nodes-base.code', typeVersion: 2, position: [220, 0], parameters: { mode: 'runOnceForAllItems', jsCode: score } },
			{
				id: 'c',
				name: 'Priority?',
				type: 'n8n-nodes-base.if',
				typeVersion: 2,
				position: [440, 0],
				parameters: { conditions: { conditions: [{ id: '1', leftValue: '={{ $json.tier }}', rightValue: 'priority', operator: { type: 'string', operation: 'equals' } }] } }
			},
			{ id: 'd', name: 'Express', type: 'n8n-nodes-base.set', typeVersion: 3.4, position: [660, -80], parameters: setLane('express', 1) },
			{ id: 'e', name: 'Ground', type: 'n8n-nodes-base.set', typeVersion: 3.4, position: [660, 80], parameters: setLane('ground', 5) },
			{ id: 'f', name: 'Respond', type: 'n8n-nodes-base.respondToWebhook', typeVersion: 1.1, position: [880, 0], parameters: { respondWith: 'json', responseBody: '={{ $json }}' } }
		],
		connections: {
			Webhook: { main: [[{ node: 'Score Order', type: 'main', index: 0 }]] },
			'Score Order': { main: [[{ node: 'Priority?', type: 'main', index: 0 }]] },
			'Priority?': { main: [[{ node: 'Express', type: 'main', index: 0 }], [{ node: 'Ground', type: 'main', index: 0 }]] },
			Express: { main: [[{ node: 'Respond', type: 'main', index: 0 }]] },
			Ground: { main: [[{ node: 'Respond', type: 'main', index: 0 }]] }
		},
		settings: {}
	};

	const imported = await importN8nTemplate(server.baseURL, exported);
	expect(imported.unsupported.filter((issue) => issue.severity === 'blocking'), 'nothing blocks the import').toEqual([]);
	expect(imported.workflow.latestVersion.document.nodes.find((entry) => entry.name === 'Score Order')?.type).toBe('kilasflow.jsCode');
	expect(imported.webhooks).toHaveLength(1);
	await activateWorkflow(server.baseURL, imported.workflow.id);

	const priority = await deliver(server.baseURL, imported.webhooks[0].url, {
		json: {
			orderId: 'A-1',
			customer: 'ada lovelace',
			lines: [
				{ sku: 'kb-2', qty: 1, price: 89.5 },
				{ sku: 'ms-1', qty: 2, price: 20 },
				{ sku: 'kb-2', qty: 0, price: 89.5 }
			]
		}
	});
	expect(priority.status, priority.text).toBe(200);
	expect(priority.json).toMatchObject({ orderId: 'A-1', customer: 'Ada Lovelace', total: 129.5, skus: ['kb-2', 'ms-1'], tier: 'priority', lane: 'express', etaDays: 1 });

	const standard = await deliver(server.baseURL, imported.webhooks[0].url, {
		json: { orderId: 'B-2', customer: 'grace_hopper', lines: [{ sku: 'cb-9', qty: 3, price: 4 }] }
	});
	expect(standard.status, standard.text).toBe(200);
	expect(standard.json).toMatchObject({ orderId: 'B-2', customer: 'Grace Hopper', total: 12, tier: 'standard', lane: 'ground', etaDays: 5 });

	// The two deliveries are two executions, and each kept what its code
	// printed with the node's run.
	const ids = await listExecutionIds(server.baseURL, imported.workflow.id);
	expect(ids).toHaveLength(2);
	const printed = await Promise.all(
		ids.map(async (id) => {
			const record = await waitForExecution(server.baseURL, id);
			const scoreNode = imported.workflow.latestVersion.document.nodes.find((entry) => entry.name === 'Score Order')!;
			return consoleTexts(record, scoreNode.id);
		})
	);
	expect(printed.flat().sort()).toEqual(['scored A-1 total 129.5', 'scored B-2 total 12']);
});

test('JavaScript between an HTTP call and an expression keeps every item paired with the request it came from', async ({ server, stub }) => {
	const workflowId = await createWorkflow(
		server.baseURL,
		'JS Around HTTP',
		[
			manual(),
			js('users', 'Users', "return [3, 1, 2].map((id) => ({ json: { userId: id, name: 'user-' + id } }))"),
			node('http', 'Fetch User', 'kilasflow.httpRequest', { method: 'GET', url: expression(`${stub.origin}/users/{{ $json.userId }}`) }),
			js(
				'enrich',
				'Enrich',
				[
					"const user = $('Users').item.json",
					'const badge = Buffer.from(`${user.name}:${$json.path}`).toString(\'base64\')',
					"return { json: { index: $itemIndex, userId: user.userId, fetched: $json.path, badge, roundTrip: Buffer.from(badge, 'base64').toString('utf8') } }"
				].join('\n'),
				{ mode: 'runOnceForEachItem' }
			),
			node('label', 'Label', 'kilasflow.set', {
				assignments: {
					label: expression('{{ $json.userId }}@{{ $json.fetched }}'),
					origin: expression("{{ $('Users').item.json.name }}"),
					badge: expression('{{ $json.badge }}')
				}
			})
		],
		chain('manual', 'users', 'http', 'enrich', 'label')
	);
	const record = await runToSuccess(server.baseURL, workflowId);

	expect(stub.requests.filter((request) => request.path.startsWith('/users/')).map((request) => request.path)).toEqual(['/users/3', '/users/1', '/users/2']);
	const enriched = items(record, 'enrich');
	expect(enriched.map((item) => [item.index, item.userId, item.fetched])).toEqual([
		[0, 3, '/users/3'],
		[1, 1, '/users/1'],
		[2, 2, '/users/2']
	]);
	for (const item of enriched) {
		expect(item.badge).toBe(Buffer.from(`user-${item.userId}:${item.fetched}`).toString('base64'));
		expect(item.roundTrip).toBe(`user-${item.userId}:${item.fetched}`);
	}
	// A downstream expression reads the Code node's output, and reaches back
	// through it to the item it came from.
	expect(items(record, 'label').map((item) => [item.label, item.origin])).toEqual([
		['3@/users/3', 'user-3'],
		['1@/users/1', 'user-1'],
		['2@/users/2', 'user-2']
	]);
});

test('lineage survives a Code node that filters, reorders and rebuilds items', async ({ server }) => {
	// What n8n answers. Each item Split Out makes is an origin of its own
	// (BUG-14gp8r), so the filter and the sort after it cannot move an item
	// away from the order it came from, as a pairing by position would.
	const workflowId = await createWorkflow(
		server.baseURL,
		'JS Lineage',
		[
			manual(),
			js(
				'orders',
				'Orders',
				"return [{ json: { orders: [ { id: 'o1', amount: 40, status: 'paid' }, { id: 'o2', amount: 250, status: 'paid' }, { id: 'o3', amount: 90, status: 'cancelled' }, { id: 'o4', amount: 120, status: 'paid' } ] } }]"
			),
			node('split', 'Split Out', 'kilasflow.splitOut', { fieldToSplitOut: 'orders' }),
			js(
				'rank',
				'Rank',
				[
					"const kept = items.filter((item) => item.json.status !== 'cancelled')",
					'kept.sort((a, b) => b.json.amount - a.json.amount)',
					'// The top order is a new object that names its source; the rest are',
					'// the input items themselves, changed in place.',
					'return kept.map((item, rank) => rank === 0',
					'  ? { json: { id: item.json.id, amount: item.json.amount, rank, top: true }, pairedItem: items.indexOf(item) }',
					'  : Object.assign(item, { json: { ...item.json, rank } }))'
				].join('\n')
			),
			node('trace', 'Trace', 'kilasflow.set', {
				assignments: {
					id: expression('{{ $json.id }}'),
					rank: expression('{{ $json.rank }}'),
					origin: expression("{{ $('Split Out').item.json.id }}"),
					originStatus: expression("{{ $('Split Out').item.json.status }}")
				}
			}),
			node('gather', 'Gather', 'kilasflow.aggregate', { aggregate: 'aggregateAllItemData', destinationFieldName: 'ranked' })
		],
		chain('manual', 'orders', 'split', 'rank', 'trace', 'gather')
	);
	const record = await runToSuccess(server.baseURL, workflowId);
	const ranked = items(record, 'gather')[0].ranked as Array<Record<string, unknown>>;
	expect(ranked.map((entry) => [entry.id, String(entry.rank), entry.origin, entry.originStatus])).toEqual([
		['o2', '0', 'o2', 'paid'],
		['o4', '1', 'o4', 'paid'],
		['o1', '2', 'o1', 'paid']
	]);
});

// Under an onError setting, per-item mode goes on past the item that threw, as
// n8n's item loop does: that item goes to the error output (or on as an error
// item in its place) and the others pass through. All-items mode is one call
// over the whole batch, so there the node fails as a whole, with one error
// item, as in n8n. Either way `error` is n8n's text: the message and the line,
// without the error's type or the item.
test('a throwing item goes to the error output alone, naming its line, and keeps what it printed', async ({ page, server }) => {
	const emit = "return [{ json: { raw: '{\"n\":1}' } }, { json: { raw: '{broken' } }, { json: { raw: '{\"n\":3}' } }]";
	const parse = "console.log('parsing item', $itemIndex, $json.raw)\nconst parsed = JSON.parse($json.raw)\nreturn { json: { n: parsed.n } }";

	const branchId = await createWorkflow(
		server.baseURL,
		'JS Error Branch',
		[
			manual(),
			js('emit', 'Emit', emit),
			js('parse', 'Parse', parse, { mode: 'runOnceForEachItem', settings: { onError: 'continueErrorOutput' } }),
			node('ok', 'Parsed', 'kilasflow.noOp'),
			node('failed', 'Failed', 'kilasflow.set', { assignments: { raw: expression('{{ $json.raw }}'), why: expression('{{ $json.error }}') } })
		],
		[...chain('manual', 'emit', 'parse'), conn('ok', 'parse', 'ok'), conn('err', 'parse', 'failed', 'error')]
	);
	const record = await runToSuccess(server.baseURL, branchId);
	const failed = items(record, 'failed');
	expect(failed.map((item) => item.raw)).toEqual(['{broken']);
	expect(failed[0].why).toBe("Expected property name or '}' in JSON at position 1 (line 1 column 2) [line 2]");
	expect(items(record, 'ok').map((item) => item.n)).toEqual([1, 3]);
	// Every item ran, and what each printed is kept with the node's run.
	expect(consoleTexts(record, 'parse')).toEqual(['parsing item 0 {"n":1}', 'parsing item 1 {broken', 'parsing item 2 {"n":3}']);

	// All-items mode is one call over the batch: a throw is one error item,
	// with no one input item's fields, so the node after it runs once.
	const wholeId = await createWorkflow(
		server.baseURL,
		'JS Error Whole Batch',
		[
			manual(),
			js('emit', 'Emit', emit),
			js('parse', 'Parse', 'return items.map((item) => ({ json: JSON.parse(item.json.raw) }))', { settings: { onError: 'continueErrorOutput' } }),
			node('ok', 'Parsed', 'kilasflow.noOp'),
			node('failed', 'Failed', 'kilasflow.set', { assignments: { why: expression('{{ $json.error }}') } })
		],
		[...chain('manual', 'emit', 'parse'), conn('ok', 'parse', 'ok'), conn('err', 'parse', 'failed', 'error')]
	);
	const whole = await runToSuccess(server.baseURL, wholeId);
	expect(items(whole, 'failed')).toHaveLength(1);
	expect(items(whole, 'failed')[0].why).toBe("Expected property name or '}' in JSON at position 1 (line 1 column 2) [line 1]");
	expect(nodeRun(whole, 'ok').status).toBe('skipped');

	// continueRegularOutput passes the error items on the main output instead.
	const regularId = await createWorkflow(
		server.baseURL,
		'JS Error Regular',
		[
			manual(),
			js('emit', 'Emit', emit),
			js('parse', 'Parse', parse, { mode: 'runOnceForEachItem', settings: { onError: 'continueRegularOutput' } }),
			node('after', 'After', 'kilasflow.set', { assignments: { why: expression('{{ $json.error }}') } })
		],
		chain('manual', 'emit', 'parse', 'after')
	);
	const regular = await runToSuccess(server.baseURL, regularId);
	expect(items(regular, 'after')).toHaveLength(3);
	expect(items(regular, 'after')[1].why).toContain('[line 2]');

	// The execution page shows the node's console on its own Console tab
	// (FEAT-x9gq0s), offered only for a Code node.
	await page.goto(`${server.baseURL}/executions/${record.id}`);
	const canvas = page.locator('[data-testid="execution-canvas"]');
	await expect(canvas).toBeVisible();
	await canvas.locator('.svelte-flow__node').filter({ hasText: 'Parse' }).first().click();
	const inspector = page.getByRole('complementary', { name: 'Node data' });
	await inspector.getByRole('tab', { name: 'Console' }).click();
	const consoleTab = inspector.getByRole('tabpanel', { name: 'Console' });
	await expect(consoleTab).toContainText('parsing item 0 {"n":1}');
	await expect(consoleTab).toContainText('parsing item 1 {broken');
	await expect(consoleTab).toContainText('parsing item 2 {"n":3}');
});

test('a script past its time limit fails its run, and the next run is served', async ({ server }) => {
	const spinId = await createWorkflow(
		server.baseURL,
		'JS Spin',
		[manual(), js('spin', 'Spin', 'while (true) {}', { timeoutSeconds: 0.2 })],
		chain('manual', 'spin')
	);
	const started = Date.now();
	const spun = await run(server.baseURL, spinId);
	expect(spun.status).toBe('failed');
	expect(JSON.stringify(nodeRun(spun, 'spin').error)).toContain('code exceeded its 200ms time limit');
	expect(Date.now() - started, 'the limit, not a hang, ended the run').toBeLessThan(30_000);

	const nextId = await createWorkflow(server.baseURL, 'JS After Spin', [manual(), js('js', 'Next', "return [{ json: { alive: true } }]")], chain('manual', 'js'));
	expect(items(await runToSuccess(server.baseURL, nextId), 'js')).toEqual([{ alive: true }]);
});

test('constructs the server does not run are refused before the run, in one sentence', async ({ server }) => {
	for (const [label, source, subject] of [
		['module', "const fs = require('fs')\nreturn items", 'requires the module "fs"'],
		['async generator', 'async function* pages() { yield 1 }\nreturn items', 'async generator']
	]) {
		const workflowId = await createWorkflow(server.baseURL, `JS Refused ${label}`, [manual(), js('js', 'Refused', source)], chain('manual', 'js'));
		const refusal = await refusedRun(server.baseURL, workflowId);
		expect(refusal, label).toContain("this node's code");
		expect(refusal, label).toContain(subject);
		expect(refusal, label).toContain('which this server does not run');
	}
});

test('an n8n Code node imports runnable, runs in both modes, and exports its source byte for byte', async ({ server }) => {
	// CRLF line ends, a tab, trailing spaces, non-ASCII and a `{{ }}` that must
	// stay a comment rather than become an expression.
	const tag = "// {{ $json.x }} stays a comment, never an expression  \r\nconst greeting = 'héllo 😀';\r\n\treturn [{ json: { greeting, crlf: true } }, { json: { greeting: greeting.toUpperCase(), crlf: true } }];\r\n";
	const each = 'return { json: { ...$json, position: $itemIndex, characters: [...$json.greeting].length } };';
	const imported = await importN8nTemplate(server.baseURL, {
		name: 'Imported Code Round Trip',
		nodes: [
			{ id: 'a', name: 'Manual', type: 'n8n-nodes-base.manualTrigger', typeVersion: 1, position: [0, 0], parameters: {} },
			{ id: 'b', name: 'Tag', type: 'n8n-nodes-base.code', typeVersion: 2, position: [220, 0], parameters: { language: 'javaScript', mode: 'runOnceForAllItems', jsCode: tag } },
			{ id: 'c', name: 'Each', type: 'n8n-nodes-base.code', typeVersion: 2, position: [440, 0], parameters: { mode: 'runOnceForEachItem', jsCode: each } }
		],
		connections: {
			Manual: { main: [[{ node: 'Tag', type: 'main', index: 0 }]] },
			Tag: { main: [[{ node: 'Each', type: 'main', index: 0 }]] }
		},
		settings: {}
	});
	expect(imported.unsupported.filter((issue) => issue.severity === 'blocking')).toEqual([]);
	const nodes = imported.workflow.latestVersion.document.nodes;
	const byName = (name: string) => nodes.find((entry) => entry.name === name)!;
	expect(byName('Tag').type).toBe('kilasflow.jsCode');
	expect(byName('Each').type).toBe('kilasflow.jsCode');
	expect(byName('Tag').parameters?.jsCode).toBe(tag);

	const record = await runToSuccess(server.baseURL, imported.workflow.id);
	expect(items(record, byName('Each').id)).toEqual([
		{ greeting: 'héllo 😀', crlf: true, position: 0, characters: 7 },
		{ greeting: 'HÉLLO 😀', crlf: true, position: 1, characters: 7 }
	]);

	const exported = await exportN8nWorkflow(server.baseURL, imported.workflow.id);
	const exportedNodes = exported.workflow.nodes as Array<{ name: string; type: string; parameters: Record<string, unknown> }>;
	const exportedTag = exportedNodes.find((entry) => entry.name === 'Tag')!;
	const exportedEach = exportedNodes.find((entry) => entry.name === 'Each')!;
	expect(exportedTag.type).toBe('n8n-nodes-base.code');
	expect(exportedTag.parameters.jsCode).toBe(tag);
	expect(exportedTag.parameters.language).toBe('javaScript');
	expect(exportedTag.parameters.mode).toBe('runOnceForAllItems');
	expect(exportedEach.parameters.jsCode).toBe(each);
	expect(exportedEach.parameters.mode).toBe('runOnceForEachItem');
});

test('a workflow saved with the old JavaScript placeholder runs without being imported again', async ({ server }) => {
	const workflowId = await createWorkflow(
		server.baseURL,
		'JS Legacy Placeholder',
		[
			manual(),
			node('legacy', 'Legacy Code', 'kilasflow.foreignCode', {
				language: 'javaScript',
				mode: 'runOnceForAllItems',
				jsCode: "return [{ json: { legacy: true, from: $input.first().json.from } }]"
			})
		],
		chain('manual', 'legacy')
	);
	expect(items(await runToSuccess(server.baseURL, workflowId, { from: 'before the runtime' }), 'legacy')).toEqual([{ legacy: true, from: 'before the runtime' }]);
});

test('code runs in worker processes of the server, never in Node.js, and the workers end with it', async ({ server }) => {
	const workflowId = await createWorkflow(
		server.baseURL,
		'JS Isolation',
		[manual(), js('js', 'Where', "return [{ json: { process: typeof process, require: typeof require, pid: typeof Deno } }]")],
		chain('manual', 'js')
	);
	// The code sees no Node.js process object: it is not running in one.
	expect(items(await runToSuccess(server.baseURL, workflowId), 'js')).toEqual([{ process: 'undefined', require: 'function', pid: 'undefined' }]);

	const spawned = descendants(await processTable(), server.pid);
	const workers = spawned.filter((row) => row.ppid === server.pid && /kilasflow/.test(row.command));
	expect(workers.length, `the server's children: ${JSON.stringify(spawned)}`).toBeGreaterThanOrEqual(1);
	expect(spawned.filter((row) => isNodeJS(row.command)), 'no Node.js process under the server').toEqual([]);

	await server.close();
	await expect
		.poll(async () => {
			const alive = new Set((await processTable()).map((row) => row.pid));
			return workers.filter((worker) => alive.has(worker.pid)).length;
		}, { message: 'the workers exit with the server', timeout: 15_000 })
		.toBe(0);
});

// Serial, because the second test starts a stray Node.js process on purpose,
// which the first would rightly report if the two ran at once.
test.describe.serial('no Node.js process', () => {
	test('an imported n8n workflow runs its Code nodes in both modes with no Node.js process anywhere during the run', async ({ server }) => {
		// The same scenario the epic acceptance proofs 2 and 4 run on their own
		// servers (e2e/fixtures/epic-code.ts). Here it checks the whole process
		// table: nothing Node under the server, and nothing Node on the machine
		// that was not already running when the run began.
		const result = await proveCodeNodeWorkflow(server.baseURL, await wholeTableSampler(server.pid));
		expect(result.samples).toBeGreaterThanOrEqual(3);
	});

	test('the process-table sampler reports a Node.js process that appears outside the harness', async ({ server }) => {
		// A check that finds nothing passes forever, so it is shown finding one: a
		// Node.js process started through a shell that exits at once, which leaves
		// it adopted by pid 1, as a process the server spawned and abandoned
		// would be. And it is shown not blaming the harness: a Node.js process
		// this test starts as its own child is somebody else's, as another
		// agent's on a shared machine is.
		const sample = await wholeTableSampler(server.pid);
		expect(await sample()).toEqual([]);
		const own = spawn(process.execPath, ['-e', 'setTimeout(() => {}, 20000)'], { stdio: 'ignore' });
		const { stdout } = await execFileAsync('sh', ['-c', `"${process.execPath}" -e "setTimeout(() => {}, 20000)" >/dev/null 2>&1 & echo $!`]);
		const stray = Number(stdout.trim());
		try {
			await expect.poll(async () => (await sample()).join('\n'), { message: 'the sampler reports the stray process' }).toContain(`appeared during the run: ${stray} `);
			expect((await sample()).join('\n'), "the harness's own child is not reported").not.toContain(`: ${own.pid} `);
		} finally {
			process.kill(stray);
			own.kill();
		}
	});
});

// ---------------------------------------------------------------------------
// The editor.

function canvasNode(page: Page, name: string) {
	return page.locator('[data-testid="workflow-canvas"] .svelte-flow__node').filter({ hasText: name }).first();
}

test('Code (JavaScript) is picked from the palette, written, saved and run from the editor', async ({ page, server }) => {
	const workflowId = await createWorkflow(server.baseURL, 'JS From The Palette', [manual()], []);
	await page.goto(`${server.baseURL}/app/workflows/${workflowId}`);
	await expect(page.locator('[data-testid="workflow-canvas"]')).toBeVisible();

	await canvasNode(page, 'Manual Trigger').hover();
	await page.getByRole('button', { name: 'Add a step after Manual Trigger', exact: true }).click();
	const dialog = page.getByRole('dialog');
	await expect(dialog).toBeVisible();
	await page.locator('#node-picker-search').fill('JavaScript');
	// Pickable, unlike the import-only placeholder it replaces.
	await expect(dialog.locator('[id="node-option-kilasflow.foreignCode@1"]')).toHaveCount(0);
	await dialog.locator('[id="node-option-kilasflow.jsCode@1"]').click();
	await expect(canvasNode(page, 'Code (JavaScript)')).toBeVisible();
	// Added after the trigger, so wired to it.
	await expect(page.getByRole('group', { name: 'Manual Trigger main to Code (JavaScript) main' })).toBeAttached();

	await canvasNode(page, 'Code (JavaScript)').click();
	const panel = page.getByRole('region', { name: 'Code (JavaScript) properties' });
	await expect(panel).toBeVisible();
	await panel.getByRole('tab', { name: 'Parameters' }).click();
	const code = panel.getByRole('textbox', { name: 'JavaScript', exact: true });
	// The node starts with a working example, in a multi-line code field.
	await expect(code).toHaveValue(/for \(const item of items\) \{\n {2}item\.json\.checked = true;\n\}\nreturn items;/);
	expect(await code.evaluate((element) => element.tagName)).toBe('TEXTAREA');
	await code.fill("const _ = require('lodash')\nreturn [{ json: { fromEditor: true, count: items.length, words: _.words('written in the editor') } }]");

	const save = page.getByRole('button', { name: 'Save', exact: true });
	await expect(save).toBeEnabled();
	await save.click();
	await expect(save).toBeDisabled();

	const before = await listExecutionIds(server.baseURL, workflowId);
	await page.getByRole('button', { name: 'Execute', exact: true }).click();
	await expect(page.getByRole('status')).toContainText('Run succeeded.', { timeout: 60_000 });
	const record = await newestExecution(server.baseURL, workflowId, before);
	expect(record.status).toBe('succeeded');
	const codeRun = (record.nodeRuns as any[]).find((entry) => entry.nodeId !== 'manual');
	expect(codeRun?.output?.[0]?.[0]?.json).toEqual({ fromEditor: true, count: 1, words: ['written', 'in', 'the', 'editor'] });
});
