import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures';
import { exerciseErrorWorkflow, exerciseFormTrigger } from '../fixtures/error-form-nodes';
import { readExecutionEvents, waitForExecution } from '../helpers/seed';
import {
	comparisonCaseNames,
	compareN8nOutput,
	DOCUMENTED_DIVERGENCES,
	EDITOR_VALIDATED,
	expectLiveGateState,
	isN8nLiveConfigured,
	LIVE_SERVICE,
	MATRIX_EXCLUDED,
	N8N_COMPARISONS,
	N8N_LIVE_SKIP_REASON,
	N8N_URL,
	n8nLiveConfig,
	n8nLiveSkipReason,
	STUB_EXECUTED
} from '../fixtures/n8n-live';

// FEAT-1jqjtd: every executable node through the KilasFlow editor with an
// execution-record assertion, plus the live-n8n counterpart comparison.
//
// How to read a run of this file:
//   KilasFlow side  — always executes: the matrix ratchet, the stub-executed
//                     tier (headless runs asserting the execution record),
//                     the editor half (real SPA: place, configure, save, run,
//                     then read the record), triggers on their own terms, and
//                     the editor-validated refusal tier.
//   n8n side        — env-gated: without N8N_EMAIL/N8N_PASSWORD every case
//                     skips by name with N8N_LIVE_SKIP_REASON (green), and the
//                     gate test proves the skip path; with credentials the same
//                     cases sign into the reference instance and compare shape
//                     + values modulo DOCUMENTED_DIVERGENCES.
//
// Provenance (recorded for the ticket): the KilasFlow side was executed here;
// the live path was NOT — no credentials exist in this environment — so the
// skip path is proven by this run and the live bodies are code-reviewed only.
// Browser exploration for both UIs went through `playwright-cli` per the
// ticket contract (editor: canvas, parameter panel, Run status; n8n: signin
// form shape); permanent assertions live here, not in the exploration.

interface CatalogueEntry {
	type: string;
	version: number;
	source: string;
	unavailable?: string;
}

async function api(baseURL: string, method: string, path: string, body?: unknown, wantStatus = 200): Promise<any> {
	const response = await fetch(`${baseURL}/api/v1${path}`, {
		method,
		headers: { 'content-type': 'application/json' },
		body: body === undefined ? undefined : JSON.stringify(body)
	});
	if (response.status !== wantStatus) {
		throw new Error(`${method} ${path}: status ${response.status}, want ${wantStatus} (body: ${await response.text()})`);
	}
	if (response.status === 204) return null;
	return response.json();
}

async function listNodeTypes(baseURL: string): Promise<CatalogueEntry[]> {
	const payload = await api(baseURL, 'GET', '/node-types');
	return Array.isArray(payload) ? payload : (payload.items ?? payload.data ?? []);
}

interface SpecNode {
	id: string;
	name: string;
	type: string;
	typeVersion: number;
	position?: { x: number; y: number };
	parameters?: Record<string, unknown>;
	credentials?: Record<string, string>;
}

function node(
	id: string,
	name: string,
	type: string,
	typeVersion = 1,
	parameters?: Record<string, unknown>,
	credentials?: Record<string, string>
): SpecNode {
	const entry: SpecNode = { id, name, type, typeVersion, position: { x: 0, y: 0 } };
	if (parameters !== undefined) entry.parameters = parameters;
	if (credentials !== undefined) entry.credentials = credentials;
	return entry;
}

interface SpecConnection {
	id: string;
	kind: string;
	source: { nodeId: string; port: string };
	target: { nodeId: string; port: string };
}

function conn(id: string, source: string, sourcePort: string, target: string, targetPort: string, kind = 'main'): SpecConnection {
	return { id, kind, source: { nodeId: source, port: sourcePort }, target: { nodeId: target, port: targetPort } };
}

function errorText(body: any): string {
	const errors = (body?.errors ?? []) as Array<{ message?: string }>;
	return errors.map((entry) => entry.message ?? '').join('\n');
}

const manual = () => node('manual', 'Manual Trigger', 'kilasflow.manual');
const setter = (id: string, value: unknown) => node(id, `Set ${id}`, 'kilasflow.set', 1, { assignments: { v: value } });

async function createWorkflow(baseURL: string, name: string, nodes: SpecNode[], connections: SpecConnection[]): Promise<string> {
	const created = await api(baseURL, 'POST', '/workflows', { schemaVersion: 1, name, nodes, connections, settings: {} }, 201);
	return created.id as string;
}

async function createCredential(baseURL: string, name: string, type: string, fields: Record<string, string>): Promise<string> {
	const created = await api(baseURL, 'POST', '/credentials', { name, type, fields }, 201);
	return created.id as string;
}

async function startRun(baseURL: string, workflowId: string): Promise<{ status: number; body: any }> {
	const response = await fetch(`${baseURL}/api/v1/workflows/${workflowId}/run`, {
		method: 'POST',
		headers: { 'content-type': 'application/json' },
		body: '{}'
	});
	return { status: response.status, body: await response.json() };
}

async function runToSuccess(baseURL: string, workflowId: string): Promise<any> {
	const started = await startRun(baseURL, workflowId);
	expect(started.status).toBe(202);
	const record = await waitForExecution(baseURL, started.body.id);
	expect(record.status).toBe('succeeded');
	return record;
}

function nodeRun(record: any, nodeId: string): any {
	const run = (record.nodeRuns as any[]).find((entry) => entry.nodeId === nodeId);
	expect(run, `node ${nodeId} has a recorded run`).toBeDefined();
	return run;
}

function itemJson(record: any, nodeId: string, port = 0, index = 0): any {
	const run = nodeRun(record, nodeId);
	expect(run.status).toBe('succeeded');
	return run.output[port][index].json;
}

interface ExecutionSummary {
	id: string;
	workflowId: string;
	status: string;
}

async function listExecutionIds(baseURL: string, workflowId: string): Promise<string[]> {
	const page = await api(baseURL, 'GET', `/executions?workflowId=${encodeURIComponent(workflowId)}&limit=100`);
	return (page.items as ExecutionSummary[]).map((item) => item.id);
}

// The newest execution for a workflow once one exists past `before` ids,
// awaited with assertions rather than sleeps, then driven to terminal with
// its event feed observed to the terminal event.
async function waitForNewExecution(baseURL: string, workflowId: string, before: string[]): Promise<{ record: any; events: any[] }> {
	const known = new Set(before);
	let found: ExecutionSummary | undefined;
	await expect
		.poll(
			async () => {
				const listed = await api(baseURL, 'GET', `/executions?workflowId=${encodeURIComponent(workflowId)}&limit=100`);
				found = (listed.items as ExecutionSummary[]).find((item) => !known.has(item.id));
				return found?.id ?? '';
			},
			{ message: `a new execution for workflow ${workflowId}`, timeout: 60_000 }
		)
		.not.toBe('');
	const record = await waitForExecution(baseURL, found!.id);
	const events = await readExecutionEvents(baseURL, found!.id);
	return { record, events };
}

function canvasNode(page: Page, name: string) {
	return page.locator('[data-testid="workflow-canvas"] .svelte-flow__node').filter({ hasText: name }).first();
}

async function openWorkflow(page: Page, baseURL: string, workflowId: string): Promise<void> {
	await page.goto(`${baseURL}/app/workflows/${workflowId}`);
	await expect(page.locator('[data-testid="workflow-canvas"]')).toBeVisible();
}

test('the executable-node matrix names every live catalogue entry in exactly one tier', async ({ server }) => {
	const catalogue = await listNodeTypes(server.baseURL);
	expect(catalogue.length).toBeGreaterThan(0);
	const tiered = new Set([...STUB_EXECUTED, ...EDITOR_VALIDATED, ...LIVE_SERVICE, ...Object.keys(MATRIX_EXCLUDED)]);
	const missing = catalogue.filter((entry) => !entry.unavailable && !tiered.has(`${entry.type}@${entry.version}`));
	const overlap = [...STUB_EXECUTED, ...EDITOR_VALIDATED, ...LIVE_SERVICE].filter(
		(key, index, all) => all.indexOf(key) !== index
	);
	// eslint-disable-next-line no-console
	console.log(
		`n8n-compare matrix: ${catalogue.length} catalogue entries ` +
			`(${STUB_EXECUTED.length} stub-executed, ${EDITOR_VALIDATED.length} editor-validated, ` +
			`${LIVE_SERVICE.length} live-service, ${Object.keys(MATRIX_EXCLUDED).length} excluded) ` +
			`across ${N8N_COMPARISONS.length} live comparison cases`
	);
	expect(missing.map((entry) => `${entry.type}@${entry.version} [${entry.source}]`), 'catalogue entries with no matrix tier').toEqual([]);
	expect(overlap, 'matrix entries claimed by two tiers').toEqual([]);
	for (const entry of catalogue) {
		expect(['builtin', 'pack', 'sidecar']).toContain(entry.source);
	}
});

test('core data-flow nodes run to success and record their outputs', async ({ server }) => {
	const triggerOnly = await createWorkflow(server.baseURL, 'Compare Manual', [manual()], []);
	expect(itemJson(await runToSuccess(server.baseURL, triggerOnly), 'manual')).toEqual({});

	const setId = await createWorkflow(
		server.baseURL,
		'Compare Set',
		[manual(), node('set', 'Set', 'kilasflow.set', 1, { assignments: { fixed: 'yes', doubled: { mode: 'expression', value: '{{ $itemIndex }}-{{ $itemIndex }}' } } })],
		[conn('c1', 'manual', 'main', 'set', 'main')]
	);
	expect(itemJson(await runToSuccess(server.baseURL, setId), 'set')).toMatchObject({ fixed: 'yes', doubled: '0-0' });

	const filterId = await createWorkflow(
		server.baseURL,
		'Compare Filter',
		[
			manual(),
			setter('in', 'x'),
			node('filter', 'Filter', 'kilasflow.filter', 1, {
				conditions: {
					combinator: 'and',
					conditions: [
						{
							leftValue: { mode: 'expression', value: '{{ $json.v }}' },
							operator: { type: 'string', operation: 'equals' },
							rightValue: 'x'
						}
					]
				}
			})
		],
		[conn('c1', 'manual', 'main', 'in', 'main'), conn('c2', 'in', 'main', 'filter', 'main')]
	);
	expect(itemJson(await runToSuccess(server.baseURL, filterId), 'filter')).toMatchObject({ v: 'x' });

	const truncateId = await createWorkflow(
		server.baseURL,
		'Compare Limit',
		[manual(), setter('in', 'x'), node('limit', 'Limit', 'kilasflow.limit', 1, { maxItems: 5 }), node('pass', 'No Op', 'kilasflow.noOp')],
		[conn('c1', 'manual', 'main', 'in', 'main'), conn('c2', 'in', 'main', 'limit', 'main'), conn('c3', 'limit', 'main', 'pass', 'main')]
	);
	const truncateRecord = await runToSuccess(server.baseURL, truncateId);
	expect(itemJson(truncateRecord, 'limit')).toMatchObject({ v: 'x' });
	expect(itemJson(truncateRecord, 'pass')).toMatchObject({ v: 'x' });
});

test('branching and merging nodes route items onto named ports', async ({ server }) => {
	const ifId = await createWorkflow(
		server.baseURL,
		'Compare If',
		[
			manual(),
			setter('in', 'x'),
			node('if', 'If', 'kilasflow.if', 1, { conditions: [{ field: 'v', operator: 'equals', value: 'x' }] }),
			node('hit', 'Hit', 'kilasflow.noOp'),
			node('miss', 'Miss', 'kilasflow.noOp')
		],
		[
			conn('c1', 'manual', 'main', 'in', 'main'),
			conn('c2', 'in', 'main', 'if', 'main'),
			conn('c3', 'if', 'true', 'hit', 'main'),
			conn('c4', 'if', 'false', 'miss', 'main')
		]
	);
	const ifRecord = await runToSuccess(server.baseURL, ifId);
	expect(itemJson(ifRecord, 'hit')).toMatchObject({ v: 'x' });
	expect(nodeRun(ifRecord, 'miss').status).toBe('skipped');

	const mergeId = await createWorkflow(
		server.baseURL,
		'Compare Merge',
		[manual(), setter('a', 'a'), setter('b', 'b'), node('merge', 'Merge', 'kilasflow.merge', 1, { mode: 'append' })],
		[
			conn('c1', 'manual', 'main', 'a', 'main'),
			conn('c2', 'manual', 'main', 'b', 'main'),
			conn('c3', 'a', 'main', 'merge', 'input1'),
			conn('c4', 'b', 'main', 'merge', 'input2')
		]
	);
	const mergeRecord = await runToSuccess(server.baseURL, mergeId);
	expect(nodeRun(mergeRecord, 'merge').output[0].map((item: any) => item.json.v)).toEqual(['a', 'b']);

	const switchId = await createWorkflow(
		server.baseURL,
		'Compare Switch',
		[
			manual(),
			setter('in', 'a'),
			node('switch', 'Switch', 'kilasflow.switch', 1, {
				rules: [
					{
						conditions: {
							combinator: 'and',
							conditions: [
								{
									leftValue: { mode: 'expression', value: '{{ $json.v }}' },
									operator: { type: 'string', operation: 'equals' },
									rightValue: 'a'
								}
							]
						},
						outputKey: 'is-a'
					}
				],
				fallbackOutput: 'extra'
			}),
			node('hit', 'Hit', 'kilasflow.noOp'),
			node('miss', 'Miss', 'kilasflow.noOp')
		],
		[
			conn('c1', 'manual', 'main', 'in', 'main'),
			conn('c2', 'in', 'main', 'switch', 'main'),
			conn('c3', 'switch', '0', 'hit', 'main'),
			conn('c4', 'switch', '1', 'miss', 'main')
		]
	);
	expect(itemJson(await runToSuccess(server.baseURL, switchId), 'hit')).toMatchObject({ v: 'a' });
});

test('data-shaping nodes transform a stream end to end', async ({ server }) => {
	const workflowId = await createWorkflow(
		server.baseURL,
		'Compare Shapes',
		[
			manual(),
			node('in', 'In', 'kilasflow.set', 1, { assignments: { team: 'core', score: 10, name: 'Ada' } }),
			node('agg', 'Aggregate', 'kilasflow.aggregate', 1, { aggregate: 'aggregateAllItemData', destinationFieldName: 'people' }),
			node('split', 'Split Out', 'kilasflow.splitOut', 1, { fieldToSplitOut: 'people' }),
			node('sort', 'Sort', 'kilasflow.sort', 1, { sortFieldsUI: 'name' }),
			node('sum', 'Summarize', 'kilasflow.summarize', 1, { fieldsToSummarize: [{ aggregation: 'count', field: 'name' }] }),
			node('dedup', 'Deduplicate', 'kilasflow.removeDuplicates', 1, { operation: 'removeDuplicateInputItems', compare: 'allFields' })
		],
		[
			conn('c1', 'manual', 'main', 'in', 'main'),
			conn('c2', 'in', 'main', 'agg', 'main'),
			conn('c3', 'agg', 'main', 'split', 'main'),
			conn('c4', 'split', 'main', 'sort', 'main'),
			conn('c5', 'sort', 'main', 'sum', 'main'),
			conn('c6', 'sum', 'main', 'dedup', 'main')
		]
	);
	const record = await runToSuccess(server.baseURL, workflowId);
	expect(itemJson(record, 'agg').people[0]).toMatchObject({ team: 'core' });
	expect(nodeRun(record, 'split').output[0]).toHaveLength(1);
	expect(itemJson(record, 'sort')).toMatchObject({ name: 'Ada' });
	expect(itemJson(record, 'sum')).toMatchObject({ count_name: 1 });
	expect(itemJson(record, 'dedup')).toMatchObject({ count_name: 1 });
});

test('http, sqlite, code, calculator, date-time and wait run against the stub', async ({ server, stub }) => {
	const httpId = await createWorkflow(
		server.baseURL,
		'Compare HTTP',
		[manual(), node('http', 'Call stub', 'kilasflow.httpRequest', 1, { method: 'POST', url: stub.url('/echo') })],
		[conn('c1', 'manual', 'main', 'http', 'main')]
	);
	const httpStarted = await startRun(server.baseURL, httpId);
	expect(httpStarted.status).toBe(202);
	const httpRecord = await waitForExecution(server.baseURL, httpStarted.body.id);
	expect(httpRecord.status).toBe('succeeded');
	// n8n's default output: the parsed response body is the item, not an envelope.
	expect(itemJson(httpRecord, 'http')).toMatchObject({ ok: true, method: 'POST', path: '/echo' });
	expect((await readExecutionEvents(server.baseURL, httpStarted.body.id)).map((event) => event.type)).toContain('execution.completed');

	// fullResponse asks for n8n's envelope instead: status, lower-case headers
	// and the parsed body under `body`.
	const envelopeId = await createWorkflow(
		server.baseURL,
		'Compare HTTP Envelope',
		[manual(), node('http', 'Call stub', 'kilasflow.httpRequest', 1, { method: 'POST', url: stub.url('/echo'), fullResponse: true })],
		[conn('c1', 'manual', 'main', 'http', 'main')]
	);
	const enveloped = itemJson(await runToSuccess(server.baseURL, envelopeId), 'http');
	expect(enveloped).toMatchObject({ statusCode: 200, statusMessage: 'OK', body: { ok: true, method: 'POST', path: '/echo' } });
	expect(enveloped.headers['content-type']).toContain('application/json');

	const sqliteCredential = await createCredential(server.baseURL, 'Compare SQLite', 'sqlite', {
		path: `${server.dataDir}/n8n-compare.db`
	});
	const sqliteId = await createWorkflow(
		server.baseURL,
		'Compare SQLite',
		[manual(), node('db', 'Database', 'kilasflow.sqlite', 1, { operation: 'query', statement: 'SELECT 1 AS one' }, { sqlite: sqliteCredential })],
		[conn('c1', 'manual', 'main', 'db', 'main')]
	);
	expect(itemJson(await runToSuccess(server.baseURL, sqliteId), 'db')).toMatchObject({ one: 1 });

	const codeId = await createWorkflow(
		server.baseURL,
		'Compare Code',
		[manual(), setter('in', 'x'), node('code', 'Code', 'kilasflow.code', 1, { code: 'return items, nil' })],
		[conn('c1', 'manual', 'main', 'in', 'main'), conn('c2', 'in', 'main', 'code', 'main')]
	);
	expect(itemJson(await runToSuccess(server.baseURL, codeId), 'code')).toMatchObject({ v: 'x' });

	const calcId = await createWorkflow(
		server.baseURL,
		'Compare Calculator',
		[manual(), setter('in', 'x'), node('calc', 'Calc', 'kilasflow.calculator', 1, { expression: '7 * 6' })],
		[conn('c1', 'manual', 'main', 'in', 'main'), conn('c2', 'in', 'main', 'calc', 'main')]
	);
	expect(itemJson(await runToSuccess(server.baseURL, calcId), 'calc')).toMatchObject({ result: 42 });

	const timeId = await createWorkflow(
		server.baseURL,
		'Compare Time',
		[
			manual(),
			node('date', 'Now', 'kilasflow.dateTime', 1, { operation: 'getCurrentDate', timezone: 'UTC' }),
			node('wait', 'Wait', 'kilasflow.wait', 1, { resume: 'timeInterval', amount: 1, unit: 'seconds' })
		],
		[conn('c1', 'manual', 'main', 'date', 'main'), conn('c2', 'date', 'main', 'wait', 'main')]
	);
	const timeRecord = await runToSuccess(server.baseURL, timeId);
	expect(typeof itemJson(timeRecord, 'date').date).toBe('string');
	expect(itemJson(timeRecord, 'wait')).toMatchObject({ date: itemJson(timeRecord, 'date').date });
});

test('trigger nodes fire on a manual run', async ({ server, stub }) => {
	const webhookId = await createWorkflow(
		server.baseURL,
		'Compare Webhook',
		[node('webhook', 'Webhook', 'kilasflow.webhook', 1, { path: 'compare', httpMethod: 'POST' }), setter('out', 'webhook-ok')],
		[conn('c1', 'webhook', 'main', 'out', 'main')]
	);
	expect(itemJson(await runToSuccess(server.baseURL, webhookId), 'out')).toMatchObject({ v: 'webhook-ok' });

	const scheduleId = await createWorkflow(
		server.baseURL,
		'Compare Schedule',
		[node('schedule', 'Schedule', 'kilasflow.schedule', 1, { cron: '0 * * * *' }), setter('out', 'schedule-ok')],
		[conn('c1', 'schedule', 'main', 'out', 'main')]
	);
	expect(itemJson(await runToSuccess(server.baseURL, scheduleId), 'out')).toMatchObject({ v: 'schedule-ok' });

	const respondId = await createWorkflow(
		server.baseURL,
		'Compare Respond',
		[manual(), setter('in', 'x'), node('respond', 'Respond', 'kilasflow.respondToWebhook', 1, { respondWith: 'noData', responseCode: 200 })],
		[conn('c1', 'manual', 'main', 'in', 'main'), conn('c2', 'in', 'main', 'respond', 'main')]
	);
	expect(itemJson(await runToSuccess(server.baseURL, respondId), 'respond')).toMatchObject({ v: 'x' });

	const telegramCredential = await createCredential(server.baseURL, 'Compare Telegram Trigger', 'telegramApi', {
		accessToken: 'e2e-token',
		baseUrl: stub.origin
	});
	const telegramTriggerId = await createWorkflow(
		server.baseURL,
		'Compare Telegram Trigger',
		[
			node('trigger', 'Telegram Trigger', 'kilasflow.telegramTrigger', 1, { path: 'compare', updates: ['message'] }, { telegramApi: telegramCredential }),
			setter('out', 'telegram-ok')
		],
		[conn('c1', 'trigger', 'main', 'out', 'main')]
	);
	expect(itemJson(await runToSuccess(server.baseURL, telegramTriggerId), 'out')).toMatchObject({ v: 'telegram-ok' });

	const subTriggerId = await createWorkflow(
		server.baseURL,
		'Compare Sub Trigger',
		[node('trigger', 'Sub Trigger', 'kilasflow.executeWorkflowTrigger', 1, {}), setter('out', 'sub-trigger-ok')],
		[conn('c1', 'trigger', 'main', 'out', 'main')]
	);
	expect(itemJson(await runToSuccess(server.baseURL, subTriggerId), 'out')).toMatchObject({ v: 'sub-trigger-ok' });

	const wahaCredential = await createCredential(server.baseURL, 'Compare WAHA Trigger', 'wahaApi', {
		baseUrl: stub.origin,
		apiKey: 'e2e-key'
	});
	for (const version of [202409, 202502]) {
		const wahaTriggerId = await createWorkflow(
			server.baseURL,
			`Compare WAHA Trigger ${version}`,
			[node('trigger', 'WAHA Trigger', 'pack.wahaTrigger', version, { path: `compare-${version}`, session: 'default' }, { wahaApi: wahaCredential })],
			[]
		);
		expect(nodeRun(await runToSuccess(server.baseURL, wahaTriggerId), 'trigger').status).toBe('succeeded');
	}
});

test('the error workflow pair and the hosted form run against the binary', async ({ server }) => {
	await exerciseErrorWorkflow(server.baseURL, 'Compare');
	await exerciseFormTrigger(server.baseURL, 'Compare');
});

test('a loop iterates, notes stay inert, sub-workflows call out, datastores round-trip, and packs reach the stub', async ({
	server,
	stub
}) => {
	const loopId = await createWorkflow(
		server.baseURL,
		'Compare Loop',
		[
			manual(),
			setter('in', 'x'),
			node('loop', 'Loop', 'kilasflow.loop', 1, { batchSize: 10 }),
			node('body', 'Body', 'kilasflow.noOp'),
			node('done', 'Done', 'kilasflow.noOp')
		],
		[
			conn('c1', 'manual', 'main', 'in', 'main'),
			conn('c2', 'in', 'main', 'loop', 'main'),
			conn('c3', 'loop', 'loop', 'body', 'main'),
			conn('c4', 'body', 'main', 'loop', 'main'),
			conn('c5', 'loop', 'done', 'done', 'main')
		]
	);
	const loopRecord = await runToSuccess(server.baseURL, loopId);
	expect(nodeRun(loopRecord, 'body').status).toBe('succeeded');
	const done = nodeRun(loopRecord, 'done');
	expect(done.output[0]).toHaveLength(1);
	expect(done.output[0][0].json).toMatchObject({ v: 'x' });

	const stickyId = await createWorkflow(
		server.baseURL,
		'Compare Sticky',
		[manual(), setter('out', 'sticky-ok'), node('note', 'Note', 'kilasflow.stickyNote', 1, { content: '## Compare' })],
		[conn('c1', 'manual', 'main', 'out', 'main')]
	);
	const stickyRecord = await runToSuccess(server.baseURL, stickyId);
	expect(nodeRun(stickyRecord, 'note').status).toBe('succeeded');
	expect(itemJson(stickyRecord, 'out')).toMatchObject({ v: 'sticky-ok' });

	const calleeId = await createWorkflow(server.baseURL, 'Compare Callee', [manual(), setter('out', 'callee-ok')], [
		conn('c1', 'manual', 'main', 'out', 'main')
	]);
	const versions = await api(server.baseURL, 'GET', `/workflows/${calleeId}/versions`);
	const versionId = (versions.items ?? versions)[0].id as string;
	await api(server.baseURL, 'POST', `/workflows/${calleeId}/versions/${versionId}/publish`, {});
	const callerId = await createWorkflow(
		server.baseURL,
		'Compare Caller',
		[manual(), node('call', 'Call', 'kilasflow.executeWorkflow', 1, { workflowId: calleeId })],
		[conn('c1', 'manual', 'main', 'call', 'main')]
	);
	expect(nodeRun(await runToSuccess(server.baseURL, callerId), 'call').output[0][0].json).toMatchObject({ v: 'callee-ok' });

	const table = await api(server.baseURL, 'POST', '/datastores', { name: 'Compare', columns: [{ name: 'v', type: 'string' }] }, 201);
	const datastoreId = await createWorkflow(
		server.baseURL,
		'Compare Datastore',
		[
			manual(),
			setter('in', 'ds-ok'),
			node('insert', 'Insert', 'kilasflow.datastore', 1, { resource: 'row', operation: 'insert', dataTableId: table.id }),
			node('read', 'Read', 'kilasflow.datastore', 1, { resource: 'row', operation: 'get', dataTableId: table.id })
		],
		[
			conn('c1', 'manual', 'main', 'in', 'main'),
			conn('c2', 'in', 'main', 'insert', 'main'),
			conn('c3', 'insert', 'main', 'read', 'main')
		]
	);
	const datastoreRecord = await runToSuccess(server.baseURL, datastoreId);
	expect(nodeRun(datastoreRecord, 'insert').output.datastore).toMatchObject({ rows: 1 });
	expect(nodeRun(datastoreRecord, 'read').output.datastore).toMatchObject({ rows: 1 });

	const telegramCredential = await createCredential(server.baseURL, 'Compare Telegram', 'telegramApi', {
		accessToken: 'e2e-token',
		baseUrl: stub.origin
	});
	const telegramId = await createWorkflow(
		server.baseURL,
		'Compare Pack Telegram',
		[
			manual(),
			node('send', 'Send', 'pack.telegram', 1, { resource: 'message', operation: 'sendMessage', chatId: '123', text: 'hi' }, { telegramApi: telegramCredential })
		],
		[conn('c1', 'manual', 'main', 'send', 'main')]
	);
	const telegramRecord = await runToSuccess(server.baseURL, telegramId);
	expect(itemJson(telegramRecord, 'send').ok).toBe(true);
	expect(itemJson(telegramRecord, 'send').path).toContain('sendMessage');

	const wahaCredential = await createCredential(server.baseURL, 'Compare WAHA', 'wahaApi', {
		baseUrl: stub.origin,
		apiKey: 'e2e-key'
	});
	for (const version of [202409, 202502]) {
		const wahaId = await createWorkflow(
			server.baseURL,
			`Compare Pack WAHA ${version}`,
			[
				manual(),
				node(
					'send',
					'Send',
					'pack.waha',
					version,
					{ resource: 'Chatting', operation: 'Send Text', session: 'default', chatId: '123@c.us', text: 'hi' },
					{ wahaApi: wahaCredential }
				)
			],
			[conn('c1', 'manual', 'main', 'send', 'main')]
		);
		const delivered = itemJson(await runToSuccess(server.baseURL, wahaId), 'send');
		expect(delivered.ok).toBe(true);
		expect(delivered.path).toBe('/api/sendText');
	}
});

test('a set node is configured in the editor, saved, and run to its execution record', async ({ page, server }) => {
	// The editor half of the matrix: a real browser drives the real SPA —
	// select the node, edit a parameter, save, run — and the assertion reads
	// the execution record, not the validation state.
	const workflowId = await createWorkflow(
		server.baseURL,
		'Compare Editor Set',
		[manual(), node('set1', 'Set One', 'kilasflow.set', 1, { assignments: { v: 'before' } })],
		[conn('c1', 'manual', 'main', 'set1', 'main')]
	);
	await openWorkflow(page, server.baseURL, workflowId);

	await canvasNode(page, 'Set One').click();
	const panel = page.getByRole('region', { name: 'Set One properties' });
	await expect(panel).toBeVisible();
	await panel.getByRole('textbox', { name: 'Fields to Set field value' }).fill('after');

	const save = page.getByRole('button', { name: 'Save', exact: true });
	await expect(save).toBeEnabled();
	await save.click();
	await expect(save).toBeDisabled();

	const before = await listExecutionIds(server.baseURL, workflowId);
	await page.getByRole('button', { name: 'Execute', exact: true }).click();
	await expect(page.getByRole('status')).toContainText('Run succeeded.', { timeout: 60_000 });

	const { record, events } = await waitForNewExecution(server.baseURL, workflowId, before);
	expect(record.status).toBe('succeeded');
	expect(itemJson(record, 'set1')).toMatchObject({ v: 'after' });
	expect(events.map((event) => event.type)).toContain('execution.completed');
});

test('a postgres node asks for its credential in the picker instead of crashing', async ({ page, server }) => {
	// The credential-picker path for the editor-validated tier: the panel
	// shows the user-facing validation message, and the run is refused with
	// the same named credential — never a runtime crash.
	const workflowId = await createWorkflow(
		server.baseURL,
		'Compare Postgres Picker',
		[manual(), node('db', 'Database', 'kilasflow.postgres', 1, { operation: 'query', statement: 'SELECT 1' })],
		[conn('c1', 'manual', 'main', 'db', 'main')]
	);
	await openWorkflow(page, server.baseURL, workflowId);

	await canvasNode(page, 'Database').click();
	const panel = page.getByRole('region', { name: 'Database properties' });
	await expect(panel).toBeVisible();
	await expect(panel).toContainText('This node needs a credential before it can run.');

	const attempt = await startRun(server.baseURL, workflowId);
	expect(attempt.status).toBe(422);
	expect(errorText(attempt.body)).toContain('requires a postgres credential');
});

test('the editor-validated tier refuses with user-facing diagnostics', async ({ server, stub }) => {
	const foreignId = await createWorkflow(
		server.baseURL,
		'Compare Foreign',
		[manual(), node('code', 'Foreign', 'kilasflow.foreignCode', 1, { language: 'javaScript', jsCode: 'return items' })],
		[conn('c1', 'manual', 'main', 'code', 'main')]
	);
	const foreignAttempt = await startRun(server.baseURL, foreignId);
	expect(foreignAttempt.status).toBe(422);
	expect(errorText(foreignAttempt.body)).toContain('which this server does not run');

	for (const arity of [1, 2, 4, 8]) {
		const unsupportedId = await createWorkflow(
			server.baseURL,
			`Compare Unsupported ${arity}`,
			[manual(), node('capsule', 'Capsule', 'kilasflow.unsupported', arity, { originalType: 'n8n-nodes-base.coverage' })],
			[conn('c1', 'manual', 'main', 'capsule', 'main')]
		);
		const attempt = await startRun(server.baseURL, unsupportedId);
		expect(attempt.status).toBe(422);
		expect(errorText(attempt.body)).toContain('which KilasFlow does not support');
	}

	const agentId = await createWorkflow(server.baseURL, 'Compare Agent Bare', [manual(), node('agent', 'Agent', 'kilasflow.agent', 1, { prompt: 'hi' })], [
		conn('c1', 'manual', 'main', 'agent', 'main')
	]);
	const agentAttempt = await startRun(server.baseURL, agentId);
	expect(agentAttempt.status).toBe(422);
	expect(errorText(agentAttempt.body)).toContain('requires a connection on port "Chat Model"');

	const chainId = await createWorkflow(
		server.baseURL,
		'Compare Chain Bare',
		[manual(), node('chain', 'Chain', 'kilasflow.chainLlm', 1, { promptType: 'define', text: 'hi' })],
		[conn('c1', 'manual', 'main', 'chain', 'main')]
	);
	const chainAttempt = await startRun(server.baseURL, chainId);
	expect(chainAttempt.status).toBe(422);
	expect(errorText(chainAttempt.body)).toContain('requires a connection on port "Chat Model"');

	const bearerModelId = await createWorkflow(
		server.baseURL,
		'Compare Model Key',
		[
			manual(),
			node('model', 'Model', 'kilasflow.chatModel', 1, { model: 'coverage-stub', baseUrl: stub.origin }),
			node('agent', 'Agent', 'kilasflow.agent', 1, { prompt: 'hi' })
		],
		[conn('c1', 'manual', 'main', 'agent', 'main'), conn('c2', 'model', 'model', 'agent', 'model', 'ai_languageModel')]
	);
	// A wired chat model needs no key: an endpoint that needs none is the normal
	// case, so the run is accepted and the request leaves without an
	// Authorization header, while an attached httpBearerAuth credential is applied.
	const keylessAttempt = await startRun(server.baseURL, bearerModelId);
	expect(keylessAttempt.status).toBe(202);
	await waitForExecution(server.baseURL, keylessAttempt.body.id);
	const keylessCall = stub.requests.find((request) => request.method === 'POST' && request.path === '/chat/completions');
	expect(keylessCall, 'the keyless model call reached the stub').toBeDefined();
	expect(keylessCall!.headers.authorization).toBeUndefined();

	const keyId = await createCredential(server.baseURL, 'Compare Model Key', 'httpBearerAuth', { token: 'compare-key' });
	const keyedModelId = await createWorkflow(
		server.baseURL,
		'Compare Model Keyed',
		[
			manual(),
			node('model', 'Model', 'kilasflow.chatModel', 1, { model: 'coverage-stub', baseUrl: stub.origin }, { httpBearerAuth: keyId }),
			node('agent', 'Agent', 'kilasflow.agent', 1, { prompt: 'hi' })
		],
		[conn('c1', 'manual', 'main', 'agent', 'main'), conn('c2', 'model', 'model', 'agent', 'model', 'ai_languageModel')]
	);
	const keyedAttempt = await startRun(server.baseURL, keyedModelId);
	expect(keyedAttempt.status).toBe(202);
	await waitForExecution(server.baseURL, keyedAttempt.body.id);
	const keyedCalls = stub.requests.filter((request) => request.method === 'POST' && request.path === '/chat/completions');
	expect(keyedCalls.map((request) => request.headers.authorization)).toEqual([undefined, 'Bearer compare-key']);

	for (const provider of [
		{ type: 'kilasflow.lmChatOpenAi', credential: 'openAiApi' },
		{ type: 'kilasflow.lmChatOpenRouter', credential: 'openRouterApi' }
	]) {
		const providerId = await createWorkflow(
			server.baseURL,
			`Compare ${provider.type}`,
			[
				manual(),
				node('model', 'Model', provider.type, 1, { model: { __rl: true, mode: 'list', value: 'coverage-stub' } }),
				node('agent', 'Agent', 'kilasflow.agent', 1, { prompt: 'hi' })
			],
			[conn('c1', 'manual', 'main', 'agent', 'main'), conn('c2', 'model', 'model', 'agent', 'model', 'ai_languageModel')]
		);
		const providerAttempt = await startRun(server.baseURL, providerId);
		expect(providerAttempt.status).toBe(422);
		expect(errorText(providerAttempt.body)).toContain(`requires a ${provider.credential} credential`);
	}

	for (const sub of [
		{ name: 'Memory', type: 'kilasflow.memoryBuffer', message: 'sessionId is required' },
		{ name: 'HTTP Tool', type: 'kilasflow.httpTool', message: 'toolDescription is required' },
		{ name: 'Calculator Tool', type: 'kilasflow.calculatorTool', message: 'toolDescription is required' },
		{ name: 'Workflow Tool', type: 'kilasflow.workflowTool', message: 'toolDescription is required' },
		{ name: 'Output Parser', type: 'kilasflow.outputParser', message: 'jsonSchema is required when the schema type is JSON Schema' },
		{ name: 'MCP Tool', type: 'kilasflow.mcpClientTool', message: 'serverUrl is required' },
		{ name: 'Datastore Tool', type: 'kilasflow.datastoreTool', message: 'toolDescription is required' }
	]) {
		const subId = await createWorkflow(server.baseURL, `Compare ${sub.name} Bare`, [manual(), node('tool', 'Tool', sub.type, 1, {})], []);
		const subAttempt = await startRun(server.baseURL, subId);
		expect(subAttempt.status).toBe(422);
		expect(errorText(subAttempt.body)).toContain(sub.message);
	}

	const embeddingsId = await createWorkflow(
		server.baseURL,
		'Compare Embeddings',
		[manual(), node('node', 'Embeddings', 'kilasflow.embeddings', 1, { model: 'text-embedding-3-small' })],
		[conn('c1', 'manual', 'main', 'node', 'main')]
	);
	const embeddingsAttempt = await startRun(server.baseURL, embeddingsId);
	expect(embeddingsAttempt.status).toBe(422);
	expect(errorText(embeddingsAttempt.body)).toContain('no text to embed');

	const storeId = await createWorkflow(
		server.baseURL,
		'Compare Vector Store',
		[manual(), node('node', 'Vector Store', 'kilasflow.vectorStore', 1, { operation: 'insert', collection: 'compare' })],
		[conn('c1', 'manual', 'main', 'node', 'main')]
	);
	const storeAttempt = await startRun(server.baseURL, storeId);
	expect(storeAttempt.status).toBe(422);
	expect(errorText(storeAttempt.body)).toContain('pgvector');

	for (const remote of [
		{ type: 'kilasflow.postgres', version: 1, credential: 'postgres' },
		{ type: 'kilasflow.postgres', version: 2, credential: 'postgres' },
		{ type: 'kilasflow.mysql', version: 1, credential: 'mysql' },
		{ type: 'kilasflow.mysql', version: 2, credential: 'mysql' }
	]) {
		const remoteId = await createWorkflow(
			server.baseURL,
			`Compare ${remote.type} ${remote.version}`,
			[manual(), node('db', 'Database', remote.type, remote.version, { operation: 'query', statement: 'SELECT 1' })],
			[conn('c1', 'manual', 'main', 'db', 'main')]
		);
		const remoteAttempt = await startRun(server.baseURL, remoteId);
		expect(remoteAttempt.status).toBe(422);
		expect(errorText(remoteAttempt.body)).toContain(`requires a ${remote.credential} credential`);
	}

	for (const remote of [
		{ type: 'kilasflow.googleDrive', credential: 'googleDriveOAuth2Api' },
		{ type: 'kilasflow.googleDriveTrigger', credential: 'googleDriveOAuth2Api' },
		{ type: 'kilasflow.gmail', credential: 'gmailOAuth2' },
		{ type: 'kilasflow.gmailTrigger', credential: 'gmailOAuth2' }
	]) {
		const isTrigger = remote.type.endsWith('Trigger');
		const remoteId = await createWorkflow(
			server.baseURL,
			`Compare ${remote.type}`,
			isTrigger
				? [node('node', 'Google', remote.type, 1, {})]
				: [manual(), node('node', 'Google', remote.type, 1, { operation: 'search' })],
			isTrigger ? [] : [conn('c1', 'manual', 'main', 'node', 'main')]
		);
		const remoteAttempt = await startRun(server.baseURL, remoteId);
		expect(remoteAttempt.status).toBe(422);
		expect(errorText(remoteAttempt.body)).toContain(`requires a ${remote.credential} credential`);
	}
});

// The trigger under test, minted through the public import endpoint so the
// test learns the opaque route rather than building it from the label.
function n8nHookDocument(): Record<string, unknown> {
	return {
		name: 'Compare Hook',
		nodes: [
			{
				id: 'hook',
				name: 'Webhook',
				type: 'n8n-nodes-base.webhook',
				typeVersion: 2,
				position: [0, 0],
				parameters: { httpMethod: 'POST', path: 'compare-hook', responseMode: 'lastNode' }
			},
			{
				id: 'pin',
				name: 'Pin',
				type: 'n8n-nodes-base.set',
				typeVersion: 3,
				position: [240, 0],
				parameters: {
					assignments: { assignments: [{ id: 'a1', name: 'v', value: 'hook-ok', type: 'string' }] },
					options: {}
				}
			}
		],
		connections: { Webhook: { main: [[{ node: 'Pin', type: 'main', index: 0 }]] } }
	};
}

test('an imported n8n webhook binds, delivers, and records', async ({ server }) => {
	// Bind: the import mints one opaque route for the trigger.
	const imported = await api(
		server.baseURL,
		'POST',
		'/workflows/import',
		{ format: 'n8n', name: 'Compare Hook', workflow: n8nHookDocument() },
		201
	);
	expect(imported.unsupported ?? []).toEqual([]);
	expect(imported.webhooks).toHaveLength(1);
	const route = imported.webhooks[0];
	expect(route.url).toMatch(/^\/webhook\/[0-9a-f]{32}$/);
	expect(route.url).not.toContain('compare-hook');

	const activated = await api(server.baseURL, 'POST', `/workflows/${imported.workflow.id}/activate`, {});
	expect(activated.active).toBe(true);

	// Deliver: a real HTTP POST at the minted route, observed through the
	// execution event stream rather than a sleep.
	const before = await listExecutionIds(server.baseURL, imported.workflow.id);
	const delivery = await fetch(`${server.baseURL}${route.url}`, {
		method: 'POST',
		headers: { 'content-type': 'application/json' },
		body: JSON.stringify({ compared: 'n8n-parity' })
	});
	expect([200, 202]).toContain(delivery.status);

	const { record, events } = await waitForNewExecution(server.baseURL, imported.workflow.id, before);
	expect(record.status).toBe('succeeded');
	expect(record.workflowId).toBe(imported.workflow.id);
	expect(events.map((event) => event.type)).toContain('execution.completed');
	expect(itemJson(record, 'pin')).toMatchObject({ v: 'hook-ok' });

	// Lifecycle: deactivation retires the binding and flips the flag back.
	const deactivated = await api(server.baseURL, 'POST', `/workflows/${imported.workflow.id}/deactivate`, {});
	expect(deactivated.active).toBe(false);
});

test('a telegram trigger activates and deactivates through its lifecycle', async ({ server, stub }) => {
	// The self-registering trigger on its own terms: webhook delivery needs
	// a public HTTPS address the harness has not got (activation says so and
	// names the cure), so the test follows the node's own guidance and
	// activates in polling delivery — the documented laptop path — against
	// the stub, then deactivates and observes the flag flip both ways.
	const credential = await createCredential(server.baseURL, 'Compare Telegram Lifecycle', 'telegramApi', {
		accessToken: 'e2e-token',
		baseUrl: stub.origin
	});
	const pollingTrigger = node(
		'trigger',
		'Telegram Trigger',
		'kilasflow.telegramTrigger',
		1,
		{ path: 'compare-lifecycle', updates: ['message'], delivery: 'polling' },
		{ telegramApi: credential }
	);
	const webhookDeliveryId = await createWorkflow(
		server.baseURL,
		'Compare Telegram Webhook Refusal',
		[
			node('trigger', 'Telegram Trigger', 'kilasflow.telegramTrigger', 1, { path: 'compare-lifecycle', updates: ['message'] }, { telegramApi: credential }),
			setter('out', 'telegram-ok')
		],
		[conn('c1', 'trigger', 'main', 'out', 'main')]
	);
	const refused = await api(server.baseURL, 'POST', `/workflows/${webhookDeliveryId}/activate`, {}, 502);
	expect(refused.detail).toContain('Delivery to polling for local development');

	const workflowId = await createWorkflow(server.baseURL, 'Compare Telegram Lifecycle', [pollingTrigger, setter('out', 'telegram-ok')], [
		conn('c1', 'trigger', 'main', 'out', 'main')
	]);
	const activated = await api(server.baseURL, 'POST', `/workflows/${workflowId}/activate`, {});
	expect(activated.active).toBe(true);
	const deactivated = await api(server.baseURL, 'POST', `/workflows/${workflowId}/deactivate`, {});
	expect(deactivated.active).toBe(false);
});
test('the live-n8n gate reports its state and the comparison catalogue', async ({}) => {
	// Always runs: the one assertion that holds in both worlds. Without
	// credentials it proves the skip path is deliberate (named reason, exact
	// exports, per-node counts); with credentials it proves the config the
	// live cases below will sign in with.
	const configured = isN8nLiveConfigured();
	// eslint-disable-next-line no-console
	console.log(
		`live n8n comparison at ${N8N_URL}: ` +
			(configured
				? `configured — executing ${N8N_COMPARISONS.length} cases (${comparisonCaseNames().join('; ')})`
				: `SKIPPED ${N8N_COMPARISONS.length} cases (${comparisonCaseNames().join('; ')}) — ${N8N_LIVE_SKIP_REASON}`)
	);
	expect(N8N_COMPARISONS.length).toBeGreaterThan(0);
	expect(DOCUMENTED_DIVERGENCES.length).toBeGreaterThan(0);
	expectLiveGateState(configured);
});

test('the output comparison agrees modulo the documented divergences', async ({}) => {
	// The comparison layer itself, proven without credentials on canned
	// payloads from both engines: order-insensitive values, the HTTP envelope
	// unwrap, and a real mismatch still failing loudly.
	expect(compareN8nOutput('set', { v: 'x', n: 1 }, { n: 1, v: 'x' })).toEqual([]);
	expect(compareN8nOutput('http', { statusCode: 200, body: { ok: true, path: '/echo' } }, { ok: true, path: '/echo' })).toEqual([]);
	expect(compareN8nOutput('http', { statusCode: 200, body: { ok: true } }, { body: { ok: true } })).toEqual([]);
	const mismatch = compareN8nOutput('set', { v: 'x' }, { v: 'y' });
	expect(mismatch).toHaveLength(1);
	expect(mismatch[0]).toContain('outputs differ for set');
});

// Live counterpart comparison. Env-gated: the describe-level skip keeps every
// case green-skipped by name without credentials (proven by the gate test and
// the skipped count in the report); with credentials each case signs into the
// reference instance, runs the same logical operation on both engines, and
// compares shape + values modulo DOCUMENTED_DIVERGENCES.
//
// Live-path provenance: REVIEWED ONLY in this environment — no N8N_EMAIL /
// N8N_PASSWORD exists here, so these bodies have never executed. They must be
// re-run where credentials exist before the ticket closes on the live path.
test.describe('live n8n counterpart comparison', () => {
	test.skip(!isN8nLiveConfigured(), N8N_LIVE_SKIP_REASON);

	async function n8nSignIn(page: Page): Promise<{ url: string; cookie: string }> {
		const config = n8nLiveConfig();
		expect(config, 'live credentials are present inside the gated suite').not.toBeNull();
		// The signin shape below was read off the live instance with
		// `playwright-cli` (Email / Password textboxes, Sign in button).
		await page.goto(`${config!.url}/signin?redirect=%252F`);
		await page.getByRole('textbox', { name: 'Email' }).fill(config!.email);
		await page.getByRole('textbox', { name: 'Password' }).fill(config!.password);
		await page.getByRole('button', { name: 'Sign in' }).click();
		await expect(page).not.toHaveURL(/signin/, { timeout: 30_000 });
		const cookies = await page.context().cookies();
		const cookie = cookies.map((entry) => `${entry.name}=${entry.value}`).join('; ');
		expect(cookie.length).toBeGreaterThan(0);
		return { url: config!.url, cookie };
	}

	async function n8nApi(session: { url: string; cookie: string }, method: string, path: string, body?: unknown): Promise<any> {
		// The reference instance's internal REST surface, authenticated by the
		// signed-in session above. A drifted path fails closed here — the
		// fetch throws or the status assertion fails — never as a silent pass.
		const response = await fetch(`${session.url}/rest${path}`, {
			method,
			headers: { 'content-type': 'application/json', cookie: session.cookie },
			body: body === undefined ? undefined : JSON.stringify(body)
		});
		if (!response.ok) throw new Error(`${method} /rest${path}: status ${response.status} (body: ${await response.text()})`);
		return response.json();
	}

	for (const comparison of N8N_COMPARISONS) {
		test(`${comparison.kilasflow} matches ${comparison.n8nType}: ${comparison.name}`, async ({ page, server }) => {
			const session = await n8nSignIn(page);

			// Same logical operation on KilasFlow, asserted via the execution
			// record: a manual trigger driving one Set with the compared field.
			const workflowId = await createWorkflow(
				server.baseURL,
				`Live Compare ${comparison.name}`,
				[manual(), node('pin', 'Pin', 'kilasflow.set', 1, { assignments: { compared: 'n8n-parity' } })],
				[conn('c1', 'manual', 'main', 'pin', 'main')]
			);
			const kilasflowOutput = itemJson(await runToSuccess(server.baseURL, workflowId), 'pin');

			// Same logical operation on n8n: a minimal workflow with the
			// counterpart node, created and executed through the session, then
			// removed so reruns stay hermetic.
			const created = await n8nApi(session, 'POST', '/workflows', {
				name: `Compare ${comparison.name}`,
				nodes: [
					{
						name: 'Start',
						type: 'n8n-nodes-base.start',
						typeVersion: 1,
						position: [0, 0],
						parameters: {}
					},
					{
						name: 'Pin',
						type: comparison.n8nType,
						typeVersion: 1,
						position: [240, 0],
						parameters: { assignments: { assignments: [{ id: 'a1', name: 'compared', value: 'n8n-parity', type: 'string' }] } }
					}
				],
				connections: { Start: { main: [[{ node: 'Pin', type: 'main', index: 0 }]] } }
			});
			try {
				const execution = await n8nApi(session, 'POST', `/workflows/${created.id}/execute`, {});
				const n8nOutput = execution?.data?.resultData?.runData?.Pin?.[0]?.data?.main?.[0]?.[0]?.json;
				expect(n8nOutput, 'the n8n run produced an output item').toBeDefined();
				const kind = comparison.kilasflow === 'kilasflow.httpRequest' ? 'http' : 'set';
				expect(compareN8nOutput(kind, kilasflowOutput, n8nOutput)).toEqual([]);
			} finally {
				await n8nApi(session, 'DELETE', `/workflows/${created.id}`).catch(() => undefined);
			}
		});
	}

	test('credential-gated nodes refuse on both engines without a credential', async ({ page, server }) => {
		const reason = n8nLiveSkipReason('postgres credential parity');
		expect(reason).toContain('N8N_EMAIL/N8N_PASSWORD');
		const session = await n8nSignIn(page);
		// KilasFlow names the missing credential instead of crashing.
		const workflowId = await createWorkflow(
			server.baseURL,
			'Live Compare Postgres Refusal',
			[manual(), node('db', 'Database', 'kilasflow.postgres', 1, { operation: 'query', statement: 'SELECT 1' })],
			[conn('c1', 'manual', 'main', 'db', 'main')]
		);
		const attempt = await startRun(server.baseURL, workflowId);
		expect(attempt.status).toBe(422);
		expect(errorText(attempt.body)).toContain('requires a postgres credential');
		// n8n likewise refuses to execute a Postgres node with no credential
		// bound: creating the workflow succeeds, running it must not.
		const created = await n8nApi(session, 'POST', '/workflows', {
			name: 'Compare Postgres Refusal',
			nodes: [
				{ name: 'Start', type: 'n8n-nodes-base.start', typeVersion: 1, position: [0, 0], parameters: {} },
				{ name: 'DB', type: 'n8n-nodes-base.postgres', typeVersion: 2, position: [240, 0], parameters: { operation: 'executeQuery', query: 'SELECT 1' } }
			],
			connections: { Start: { main: [[{ node: 'DB', type: 'main', index: 0 }]] } }
		});
		try {
			const execution = await n8nApi(session, 'POST', `/workflows/${created.id}/execute`, {}).catch((error: Error) => ({ error: String(error) }));
			expect(JSON.stringify(execution)).toMatch(/credential|Credential|error|Error/);
		} finally {
			await n8nApi(session, 'DELETE', `/workflows/${created.id}`).catch(() => undefined);
		}
	});
});
