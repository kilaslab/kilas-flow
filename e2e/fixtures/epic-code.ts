// The Code (JavaScript) node, proven end to end with no Node.js process
// (EPIC-tjnr1z P7, FEAT-afkx3k).
//
// One scenario, used by two callers: e2e/tests/js-code.spec.ts runs it on its
// own server and checks the whole process table, and the epic acceptance
// proofs 2 and 4 (e2e/fixtures/epic-proofs.ts, FEAT-5fhj6p) run it on their
// live servers through the host's own no-Node check, so the scenario also
// holds on a published image.
//
// The scenario imports an n8n workflow whose Code nodes use both modes, Luxon,
// console.log, $('Node').first(), Buffer and crypto; activates it; triggers
// it through its webhook; and asserts what it answered, what each node
// returned and what each printed. For the whole of the run the caller's
// sampler reads the process table, and the scenario fails if any sample finds
// a Node.js process where there must be none. The per-item node waits a
// little on a timer for each item, so the run is still live across several
// samples rather than over before the first.

import { execFile } from 'node:child_process';
import { createHash, createHmac, randomBytes } from 'node:crypto';
import { promisify } from 'node:util';
import { expect } from '@playwright/test';

import { listExecutionIds, waitForExecution } from '../helpers/seed';
import { deliver } from './live-backend';
import { activateWorkflow, importN8nTemplate } from './waha-migration';

const execFileAsync = promisify(execFile);

// ---- The process table ---------------------------------------------------------

export interface ProcessRow {
	pid: number;
	ppid: number;
	command: string;
}

export async function processTable(): Promise<ProcessRow[]> {
	const { stdout } = await execFileAsync('ps', ['-A', '-o', 'pid=,ppid=,command=']);
	return stdout
		.split('\n')
		.map((line) => line.trim().match(/^(\d+)\s+(\d+)\s+(.*)$/))
		.filter((match): match is RegExpMatchArray => match !== null)
		.map((match) => ({ pid: Number(match[1]), ppid: Number(match[2]), command: match[3] }));
}

export function descendants(table: ProcessRow[], root: number): ProcessRow[] {
	const found: ProcessRow[] = [];
	const queue = [root];
	while (queue.length > 0) {
		const parent = queue.shift()!;
		for (const row of table) {
			if (row.ppid === parent) {
				found.push(row);
				queue.push(row.pid);
			}
		}
	}
	return found;
}

// isNodeJS judges a process by its executable, the first word of its command
// line, so a kilasflow worker whose arguments mention a path with "node" in
// it is not mistaken for one.
export function isNodeJS(command: string): boolean {
	const executable = command.split(/\s+/)[0] ?? '';
	return /(^|\/)(node|nodejs)$/.test(executable);
}

// A sampler reads the process table once and returns a description of every
// Node.js process it found where there must be none.
export type NodeSampler = () => Promise<string[]>;

// wholeTableSampler is the strictest check a machine running the suite can
// make without being fooled by its neighbours: no Node.js process under the
// server, and no Node.js process that appeared during the run and belongs to
// nobody else. A new process is traced up through the other processes that
// also appeared during the run, to the first one that was already running.
// It is reported when that is the server, or pid 1: a process whose parent
// exited is adopted by pid 1, which is how a child the server started and
// abandoned (daemonised) would look. When that ancestor is anything else (the
// Playwright runner packing the SDK, another agent's shell on a shared
// machine) the process is somebody else's, and it is left alone.
export async function wholeTableSampler(serverPid: number): Promise<NodeSampler> {
	const before = new Set((await processTable()).map((row) => row.pid));
	return async () => {
		const table = await processTable();
		const byPid = new Map(table.map((row) => [row.pid, row]));
		// origin is the first already-running ancestor of a new process, or
		// undefined when the chain breaks (a parent exited between reads).
		const origin = (row: ProcessRow): number | undefined => {
			let current: ProcessRow | undefined = row;
			for (let hops = 0; current && hops < 64; hops++) {
				if (current.ppid === serverPid || current.ppid === 1 || before.has(current.ppid)) return current.ppid;
				current = byPid.get(current.ppid);
			}
			return undefined;
		};
		const underServer = descendants(table, serverPid).filter((row) => isNodeJS(row.command));
		const appeared = table.filter((row) => {
			if (before.has(row.pid) || !isNodeJS(row.command)) return false;
			const from = origin(row);
			return from === serverPid || from === 1;
		});
		return [
			...underServer.map((row) => `under the server: ${row.pid} ${row.command}`),
			...appeared
				.filter((row) => !underServer.some((other) => other.pid === row.pid))
				.map((row) => `appeared during the run: ${row.pid} (parent ${row.ppid}) ${row.command}`)
		];
	};
}

// ---- The workflow ----------------------------------------------------------------

const ORDERS = [
	{ orderId: 'K-100', amount: 125.5, placedAt: '2026-03-08T06:30:00Z' },
	{ orderId: 'K-101', amount: 40, placedAt: '2026-11-01T20:15:00Z' }
];

const SIGNING_KEY = 'e2e-code-key';

// The n8n export the scenario imports: a webhook, three Code nodes and a
// Respond to Webhook node.
function codeWorkflow(path: string): Record<string, unknown> {
	const orders = [
		"const { DateTime } = require('luxon');",
		'const orders = $input.first().json.body.orders;',
		"console.log('received', orders.length, 'orders');",
		'return orders.map((order) => {',
		"  const placed = DateTime.fromISO(order.placedAt, { zone: 'utc' }).setZone('Asia/Jakarta');",
		"  return { json: { orderId: order.orderId, amount: order.amount, local: placed.toFormat('yyyy-LL-dd HH:mm'), weekday: placed.toFormat('cccc') } };",
		'});'
	].join('\n');
	const sign = [
		"const crypto = require('crypto');",
		"const batch = $('Orders').first().json.orderId;",
		'const text = `${$json.orderId}:${$json.amount}`;',
		'// Keep the run live across several process-table samples.',
		'await new Promise((resolve) => setTimeout(resolve, 400));',
		"console.log('signed', $json.orderId, 'as item', $itemIndex);",
		'return { json: { ...$json, batch, encoded: Buffer.from(text).toString(\'base64\'), ' +
			"sha: crypto.createHash('sha256').update(text).digest('hex'), " +
			`mac: crypto.createHmac('sha256', '${SIGNING_KEY}').update(text).digest('hex'), ` +
			"due: DateTime.fromISO('2026-01-31', { zone: 'utc' }).plus({ months: 1 }).toISODate() } };"
	].join('\n');
	const collect = [
		'const total = items.reduce((sum, item) => sum + item.json.amount, 0);',
		"console.log('collected', items.length, 'orders totalling', total);",
		'return [{ json: { count: items.length, total, orders: items.map((item) => item.json) } }];'
	].join('\n');
	return {
		name: 'Code Node Orders',
		nodes: [
			{ id: 'a', name: 'Webhook', type: 'n8n-nodes-base.webhook', typeVersion: 2, position: [0, 0], parameters: { path, httpMethod: 'POST', responseMode: 'responseNode', authentication: 'none' } },
			{ id: 'b', name: 'Orders', type: 'n8n-nodes-base.code', typeVersion: 2, position: [220, 0], parameters: { mode: 'runOnceForAllItems', jsCode: orders } },
			{ id: 'c', name: 'Sign', type: 'n8n-nodes-base.code', typeVersion: 2, position: [440, 0], parameters: { mode: 'runOnceForEachItem', jsCode: sign } },
			{ id: 'd', name: 'Collect', type: 'n8n-nodes-base.code', typeVersion: 2, position: [660, 0], parameters: { jsCode: collect } },
			{ id: 'e', name: 'Respond', type: 'n8n-nodes-base.respondToWebhook', typeVersion: 1.1, position: [880, 0], parameters: { respondWith: 'json', responseBody: '={{ $json }}' } }
		],
		connections: {
			Webhook: { main: [[{ node: 'Orders', type: 'main', index: 0 }]] },
			Orders: { main: [[{ node: 'Sign', type: 'main', index: 0 }]] },
			Sign: { main: [[{ node: 'Collect', type: 'main', index: 0 }]] },
			Collect: { main: [[{ node: 'Respond', type: 'main', index: 0 }]] }
		},
		settings: {}
	};
}

// What the Sign node returns for one order, worked out here in the harness,
// which is Node by definition and allowed to be. The base64 field is named
// `encoded`: the execution record redacts a field whose name reads as a
// secret, such as `token`, and the assertion is about the bytes.
function signed(order: (typeof ORDERS)[number], local: string, weekday: string) {
	const text = `${order.orderId}:${order.amount}`;
	return {
		orderId: order.orderId,
		amount: order.amount,
		local,
		weekday,
		batch: ORDERS[0].orderId,
		encoded: Buffer.from(text).toString('base64'),
		sha: createHash('sha256').update(text).digest('hex'),
		mac: createHmac('sha256', SIGNING_KEY).update(text).digest('hex'),
		due: '2026-02-28'
	};
}

const EXPECTED = [
	// 06:30 UTC is 13:30 in Jakarta (UTC+7), the same Sunday.
	signed(ORDERS[0], '2026-03-08 13:30', 'Sunday'),
	// 20:15 UTC on a Sunday is 03:15 on Monday in Jakarta.
	signed(ORDERS[1], '2026-11-02 03:15', 'Monday')
];

export interface CodeScenarioResult {
	workflowId: string;
	executionId: string;
	samples: number;
}

// proveCodeNodeWorkflow runs the scenario on a live server. `sample` is
// called repeatedly for the whole of the run, and every Node.js process it
// reports fails the scenario.
export async function proveCodeNodeWorkflow(baseURL: string, sample: NodeSampler): Promise<CodeScenarioResult> {
	const imported = await importN8nTemplate(baseURL, codeWorkflow(`code-orders-${randomBytes(4).toString('hex')}`));
	expect(imported.unsupported.filter((issue) => issue.severity === 'blocking'), 'nothing blocks the import').toEqual([]);
	const nodes = imported.workflow.latestVersion.document.nodes;
	const idOf = (name: string) => {
		const found = nodes.find((entry) => entry.name === name);
		expect(found, `the imported workflow has a node named ${name}`).toBeDefined();
		return found!.id;
	};
	for (const name of ['Orders', 'Sign', 'Collect']) {
		expect(nodes.find((entry) => entry.name === name)?.type, `${name} imports as runnable JavaScript`).toBe('kilasflow.jsCode');
	}
	expect(imported.webhooks).toHaveLength(1);
	await activateWorkflow(baseURL, imported.workflow.id);

	// Sample before the trigger, all through the run, and once after it.
	const found: string[] = [];
	let samples = 0;
	let live = true;
	// A sampler that throws (the host could not read its process table) ends
	// the loop at once; the error is kept and thrown once the delivery is
	// answered, so it is never an unhandled rejection and never lost.
	let samplingFailed: unknown;
	const sampling = (async () => {
		while (live) {
			found.push(...(await sample()));
			samples++;
			await new Promise((resolve) => setTimeout(resolve, 100));
		}
	})().catch((error: unknown) => {
		samplingFailed = error ?? new Error('the process-table sampler failed');
	});
	let answer: Awaited<ReturnType<typeof deliver>>;
	try {
		answer = await deliver(baseURL, imported.webhooks[0].url, { json: { orders: ORDERS } });
	} finally {
		live = false;
		await sampling;
	}
	if (samplingFailed !== undefined) throw samplingFailed;
	found.push(...(await sample()));
	samples++;
	expect(found, 'no Node.js process while the Code nodes ran').toEqual([]);
	// Two items each wait 400 ms, so a sampler that never saw the run live
	// has not checked anything.
	expect(samples, 'the process table was read while the run was live').toBeGreaterThanOrEqual(3);

	expect(answer.status, answer.text).toBe(200);
	expect(answer.json).toEqual({ count: 2, total: 165.5, orders: EXPECTED });

	const ids = await listExecutionIds(baseURL, imported.workflow.id);
	expect(ids).toHaveLength(1);
	const record = await waitForExecution(baseURL, ids[0]);
	expect(record.status).toBe('succeeded');
	const run = (name: string) => (record.nodeRuns as any[]).find((entry) => entry.nodeId === idOf(name));
	const output = (name: string) => ((run(name)?.output?.[0] ?? []) as Array<{ json: unknown }>).map((item) => item.json);
	const printed = (name: string) => ((run(name)?.console?.lines ?? []) as Array<{ text: string }>).map((line) => line.text);

	expect(output('Orders')).toEqual(EXPECTED.map(({ orderId, amount, local, weekday }) => ({ orderId, amount, local, weekday })));
	expect(output('Sign')).toEqual(EXPECTED);
	expect(printed('Orders')).toEqual(['received 2 orders']);
	expect(printed('Sign')).toEqual(['signed K-100 as item 0', 'signed K-101 as item 1']);
	expect(printed('Collect')).toEqual(['collected 2 orders totalling 165.5']);

	return { workflowId: imported.workflow.id, executionId: ids[0], samples };
}
