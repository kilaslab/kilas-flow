import { test, expect } from '../fixtures';
import { exerciseErrorWorkflow, exerciseFormTrigger } from '../fixtures/error-form-nodes';
import { readExecutionEvents, waitForExecution } from '../helpers/seed';

// Node-type coverage: every entry the server advertises on GET
// /api/v1/node-types is exercised through the public API — placed in a
// workflow document, saved, and either run to terminal success against the
// local stub or refused with the validation message a user would see.
//
// Tiers (stated so a green result is not read as more than it is):
//   run        — executed against the loopback stub through the instance's
//                outbound policy (allowed_hosts plus one
//                allowed_private_endpoints entry; allow_private_networks is
//                never set).
//   validated  — saved as a draft, then the run is refused with a named
//                configuration error. This is the correct behaviour for the
//                import capsules (which must refuse) and the most the
//                harness can say about nodes that need a third party: the AI
//                cluster needs a live LLM endpoint, the remote databases need
//                a live database. The last tier — executed against the real
//                service — belongs to the capstone suite, not this one.
//
// The catalogue is read live, so a node added by a later ticket appears as
// uncovered without anyone updating this file: the report test below fails on
// any type@version with neither a test nor an explicit, justified exclusion.

// Every catalogue entry this suite runs to success, as "type@version".
const RUN_COVERAGE = [
	'kilasflow.manual@1',
	'kilasflow.chatTrigger@1',
	'kilasflow.set@1',
	'kilasflow.if@1',
	'kilasflow.merge@1',
	'kilasflow.switch@1',
	'kilasflow.filter@1',
	'kilasflow.limit@1',
	'kilasflow.noOp@1',
	'kilasflow.httpRequest@1',
	'kilasflow.webhook@1',
	'kilasflow.respondToWebhook@1',
	'kilasflow.schedule@1',
	'kilasflow.sqlite@1',
	'kilasflow.code@1',
	'kilasflow.jsCode@1',
	'kilasflow.calculator@1',
	'kilasflow.stickyNote@1',
	'kilasflow.loop@1',
	'kilasflow.telegramTrigger@1',
	'kilasflow.aggregate@1',
	'kilasflow.splitOut@1',
	'kilasflow.sort@1',
	'kilasflow.summarize@1',
	'kilasflow.removeDuplicates@1',
	'kilasflow.dateTime@1',
	'kilasflow.wait@1',
	'kilasflow.executeWorkflow@1',
	'kilasflow.executeWorkflowTrigger@1',
	'kilasflow.datastore@1',
	'kilasflow.documentLoader@1',
	'kilasflow.textSplitter@1',
	// The error pair and the hosted form are exercised end-to-end by
	// fixtures/error-form-nodes.ts, called from the test below.
	'kilasflow.errorTrigger@1',
	'kilasflow.stopAndError@1',
	'kilasflow.formTrigger@1',
	'pack.telegram@1',
	'pack.waha@202409',
	'pack.waha@202502',
	'pack.wahaTrigger@202409',
	'pack.wahaTrigger@202502'
];

// Every catalogue entry this suite covers through its refusal path, as
// "type@version": saved as a draft, then the run is rejected with the
// validation message a user would see. Reasons live beside each test.
const VALIDATION_COVERAGE = [
	'kilasflow.foreignCode@1',
	'kilasflow.unsupported@1',
	'kilasflow.unsupported@2',
	'kilasflow.unsupported@4',
	'kilasflow.unsupported@8',
	'kilasflow.agent@1',
	'kilasflow.chainLlm@1',
	'kilasflow.chatModel@1',
	'kilasflow.lmChatOpenAi@1',
	'kilasflow.lmChatOpenRouter@1',
	'kilasflow.memoryBuffer@1',
	'kilasflow.httpTool@1',
	'kilasflow.calculatorTool@1',
	'kilasflow.workflowTool@1',
	'kilasflow.outputParser@1',
	'kilasflow.mcpClientTool@1',
	'kilasflow.embeddings@1',
	'kilasflow.vectorStore@1',
	'kilasflow.vectorStorePGVector@1',
	'kilasflow.extractFromFile@1',
	'kilasflow.googleDrive@1',
	'kilasflow.googleDriveTrigger@1',
	'kilasflow.gmail@1',
	'kilasflow.gmailTrigger@1',
	'kilasflow.datastoreTool@1',
	'kilasflow.postgres@1',
	'kilasflow.postgres@2',
	'kilasflow.mysql@1',
	'kilasflow.mysql@2'
];

// Explicit, justified exclusions from both tiers. Empty today: everything the
// server advertises is either run or refused above. An entry here must name
// its reason, so the list cannot silently accumulate bare node types.
const EXCLUDED: Record<string, string> = {
	// No exclusions. When a node needs one, add `"type@version": "why"`.
};

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

// The run refusal carries one entry per problem; matching against the joined
// messages keeps assertions readable (JSON.stringify would escape every quote
// in the message a user actually sees).
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

// Starts a run without asserting its outcome: success tests expect 202,
// refusal tests expect 422 with a validation message.
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

// The recorded output is one stream per output port; returns one item's json.
function itemJson(record: any, nodeId: string, port = 0, index = 0): any {
	const run = nodeRun(record, nodeId);
	expect(run.status).toBe('succeeded');
	return run.output[port][index].json;
}

test('the coverage report names every catalogue entry this suite touches', async ({ server }) => {
	const catalogue = await listNodeTypes(server.baseURL);
	expect(catalogue.length).toBeGreaterThan(0);
	const touched = new Set([...RUN_COVERAGE, ...VALIDATION_COVERAGE, ...Object.keys(EXCLUDED)]);
	const missing = catalogue.filter(
		(entry) => !entry.unavailable && !touched.has(`${entry.type}@${entry.version}`)
	);
	const lines = catalogue.map(
		(entry) =>
			`${entry.type}@${entry.version} [${entry.source}]` +
			(entry.unavailable ? ` unavailable: ${entry.unavailable}` : '')
	);
	// eslint-disable-next-line no-console
	console.log(`node coverage: ${catalogue.length} catalogue entries\n${lines.join('\n')}`);
	expect(
		missing.map((entry) => `${entry.type}@${entry.version} [${entry.source}]`),
		'catalogue entries with neither a test nor an exclusion'
	).toEqual([]);
	for (const entry of catalogue) {
		expect(['builtin', 'pack', 'sidecar']).toContain(entry.source);
	}
});

test('manual, set, filter, limit and no-op run headless', async ({ server }) => {
	// A bare trigger runs and emits its (empty) trigger item.
	const triggerOnly = await createWorkflow(server.baseURL, 'Coverage Manual', [manual()], []);
	const triggerRecord = await runToSuccess(server.baseURL, triggerOnly);
	expect(itemJson(triggerRecord, 'manual')).toEqual({});

	// Set writes fixed values and resolves expressions per item.
	const setId = await createWorkflow(
		server.baseURL,
		'Coverage Set',
		[manual(), node('set', 'Set', 'kilasflow.set', 1, { assignments: { fixed: 'yes', doubled: { mode: 'expression', value: '{{ $itemIndex }}-{{ $itemIndex }}' } } })],
		[conn('c1', 'manual', 'main', 'set', 'main')]
	);
	const setRecord = await runToSuccess(server.baseURL, setId);
	expect(itemJson(setRecord, 'set')).toMatchObject({ fixed: 'yes', doubled: '0-0' });

	// Filter keeps the items its conditions match.
	const filterId = await createWorkflow(
		server.baseURL,
		'Coverage Filter',
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
	const filterRecord = await runToSuccess(server.baseURL, filterId);
	expect(itemJson(filterRecord, 'filter')).toMatchObject({ v: 'x' });

	// Limit truncates the stream; no-op passes it through unchanged.
	const truncateId = await createWorkflow(
		server.baseURL,
		'Coverage Limit',
		[manual(), setter('in', 'x'), node('limit', 'Limit', 'kilasflow.limit', 1, { maxItems: 5 }), node('pass', 'No Op', 'kilasflow.noOp')],
		[conn('c1', 'manual', 'main', 'in', 'main'), conn('c2', 'in', 'main', 'limit', 'main'), conn('c3', 'limit', 'main', 'pass', 'main')]
	);
	const truncateRecord = await runToSuccess(server.baseURL, truncateId);
	expect(itemJson(truncateRecord, 'limit')).toMatchObject({ v: 'x' });
	expect(itemJson(truncateRecord, 'pass')).toMatchObject({ v: 'x' });
});

test('branching and merging nodes route items onto named ports', async ({ server }) => {
	// IF sends matching items to `true` and the rest to `false`.
	const ifId = await createWorkflow(
		server.baseURL,
		'Coverage If',
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

	// Merge appends both input streams in port order.
	const mergeId = await createWorkflow(
		server.baseURL,
		'Coverage Merge',
		[manual(), setter('a', 'a'), setter('b', 'b'), node('merge', 'Merge', 'kilasflow.merge', 1, { mode: 'append' })],
		[
			conn('c1', 'manual', 'main', 'a', 'main'),
			conn('c2', 'manual', 'main', 'b', 'main'),
			conn('c3', 'a', 'main', 'merge', 'input1'),
			conn('c4', 'b', 'main', 'merge', 'input2')
		]
	);
	const mergeRecord = await runToSuccess(server.baseURL, mergeId);
	const merged = nodeRun(mergeRecord, 'merge');
	expect(merged.status).toBe('succeeded');
	expect(merged.output[0].map((item: any) => item.json.v)).toEqual(['a', 'b']);

	// Switch routes to the first matching rule and the fallback to its extra port.
	const switchId = await createWorkflow(
		server.baseURL,
		'Coverage Switch',
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
	// A second item exercises the fallback port in the same run.
	const switchRecord = await runToSuccess(server.baseURL, switchId);
	expect(itemJson(switchRecord, 'hit')).toMatchObject({ v: 'a' });
});

test('data-shaping nodes transform a stream end to end', async ({ server }) => {
	const workflowId = await createWorkflow(
		server.baseURL,
		'Coverage Shapes',
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
	const gathered = itemJson(record, 'agg');
	expect(gathered.people).toHaveLength(1);
	expect(gathered.people[0]).toMatchObject({ team: 'core' });
	const split = nodeRun(record, 'split');
	expect(split.output[0]).toHaveLength(1);
	expect(itemJson(record, 'sort')).toMatchObject({ name: 'Ada' });
	expect(itemJson(record, 'sum')).toMatchObject({ count_name: 1 });
	expect(itemJson(record, 'dedup')).toMatchObject({ count_name: 1 });
});

test('http, sqlite, code, calculator, date-time and wait run against the stub', async ({ server, stub }) => {
	// HTTP reaches the loopback stub through the outbound policy and the
	// recorded response carries the stub's answer.
	const httpId = await createWorkflow(
		server.baseURL,
		'Coverage HTTP',
		[manual(), node('http', 'Call stub', 'kilasflow.httpRequest', 1, { method: 'POST', url: stub.url('/echo') })],
		[conn('c1', 'manual', 'main', 'http', 'main')]
	);
	const httpStarted = await startRun(server.baseURL, httpId);
	expect(httpStarted.status).toBe(202);
	const httpRecord = await waitForExecution(server.baseURL, httpStarted.body.id);
	expect(httpRecord.status).toBe('succeeded');
	const answered = itemJson(httpRecord, 'http');
	// n8n's default output: the parsed response body is the item, not an envelope.
	expect(answered).toMatchObject({ ok: true, method: 'POST', path: '/echo' });
	expect(stub.requests.some((request) => request.path === '/echo')).toBe(true);
	// fullResponse asks for n8n's envelope instead: status, lower-case headers and
	// the parsed body under `body`.
	const envelopeId = await createWorkflow(
		server.baseURL,
		'Coverage HTTP Envelope',
		[manual(), node('http', 'Call stub', 'kilasflow.httpRequest', 1, { method: 'POST', url: stub.url('/echo'), fullResponse: true })],
		[conn('c1', 'manual', 'main', 'http', 'main')]
	);
	const enveloped = itemJson(await runToSuccess(server.baseURL, envelopeId), 'http');
	expect(enveloped).toMatchObject({ statusCode: 200, statusMessage: 'OK', body: { ok: true, method: 'POST', path: '/echo' } });
	expect(enveloped.headers['content-type']).toContain('application/json');
	// The live feed observed to its terminal event, not assumed from the poll.
	const events = await readExecutionEvents(server.baseURL, httpStarted.body.id);
	expect(events.map((event) => event.type)).toContain('execution.completed');

	// SQLite runs a real query against a file beside the instance's database.
	const sqliteCredential = await createCredential(server.baseURL, 'Coverage SQLite', 'sqlite', {
		path: `${server.dataDir}/node-coverage.db`
	});
	const sqliteId = await createWorkflow(
		server.baseURL,
		'Coverage SQLite',
		[
			manual(),
			node('db', 'Database', 'kilasflow.sqlite', 1, { operation: 'query', statement: 'SELECT 1 AS one' }, { sqlite: sqliteCredential })
		],
		[conn('c1', 'manual', 'main', 'db', 'main')]
	);
	const sqliteRecord = await runToSuccess(server.baseURL, sqliteId);
	expect(itemJson(sqliteRecord, 'db')).toMatchObject({ one: 1 });

	// Code runs its Go snippet in the sandbox and passes items through.
	const codeId = await createWorkflow(
		server.baseURL,
		'Coverage Code',
		[manual(), setter('in', 'x'), node('code', 'Code', 'kilasflow.code', 1, { code: 'return items, nil' })],
		[conn('c1', 'manual', 'main', 'in', 'main'), conn('c2', 'in', 'main', 'code', 'main')]
	);
	const codeRecord = await runToSuccess(server.baseURL, codeId);
	expect(itemJson(codeRecord, 'code')).toMatchObject({ v: 'x' });

	// Code (JavaScript) runs n8n-style JavaScript in a worker process and
	// returns the items it built. tests/js-code.spec.ts wires it to the rest.
	const jsCodeId = await createWorkflow(
		server.baseURL,
		'Coverage JavaScript Code',
		[
			manual(),
			setter('in', 'x'),
			node('js', 'JS', 'kilasflow.jsCode', 1, {
				mode: 'runOnceForAllItems',
				jsCode: 'return items.map((item) => ({ json: { ...item.json, upper: item.json.v.toUpperCase() } }))'
			})
		],
		[conn('c1', 'manual', 'main', 'in', 'main'), conn('c2', 'in', 'main', 'js', 'main')]
	);
	expect(itemJson(await runToSuccess(server.baseURL, jsCodeId), 'js')).toMatchObject({ v: 'x', upper: 'X' });

	// Calculator evaluates its expression per item with no network involved.
	const calcId = await createWorkflow(
		server.baseURL,
		'Coverage Calculator',
		[manual(), setter('in', 'x'), node('calc', 'Calc', 'kilasflow.calculator', 1, { expression: '7 * 6' })],
		[conn('c1', 'manual', 'main', 'in', 'main'), conn('c2', 'in', 'main', 'calc', 'main')]
	);
	const calcRecord = await runToSuccess(server.baseURL, calcId);
	expect(itemJson(calcRecord, 'calc')).toMatchObject({ result: 42 });

	// Date & Time answers the current date; Wait pauses and passes items on.
	const timeId = await createWorkflow(
		server.baseURL,
		'Coverage Time',
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
	// A webhook trigger emits the inbound request as its item; on a manual
	// run that item is empty and the downstream node still runs.
	const webhookId = await createWorkflow(
		server.baseURL,
		'Coverage Webhook',
		[node('webhook', 'Webhook', 'kilasflow.webhook', 1, { path: 'coverage', httpMethod: 'POST' }), setter('out', 'webhook-ok')],
		[conn('c1', 'webhook', 'main', 'out', 'main')]
	);
	const webhookRecord = await runToSuccess(server.baseURL, webhookId);
	expect(itemJson(webhookRecord, 'out')).toMatchObject({ v: 'webhook-ok' });

	// A schedule trigger emits the fire time; the legacy cron shape still validates.
	const scheduleId = await createWorkflow(
		server.baseURL,
		'Coverage Schedule',
		[node('schedule', 'Schedule', 'kilasflow.schedule', 1, { cron: '0 * * * *' }), setter('out', 'schedule-ok')],
		[conn('c1', 'schedule', 'main', 'out', 'main')]
	);
	const scheduleRecord = await runToSuccess(server.baseURL, scheduleId);
	expect(itemJson(scheduleRecord, 'out')).toMatchObject({ v: 'schedule-ok' });

	// Respond to Webhook shapes the response and passes items through.
	const respondId = await createWorkflow(
		server.baseURL,
		'Coverage Respond',
		[manual(), setter('in', 'x'), node('respond', 'Respond', 'kilasflow.respondToWebhook', 1, { respondWith: 'noData', responseCode: 200 })],
		[conn('c1', 'manual', 'main', 'in', 'main'), conn('c2', 'in', 'main', 'respond', 'main')]
	);
	const respondRecord = await runToSuccess(server.baseURL, respondId);
	expect(itemJson(respondRecord, 'respond')).toMatchObject({ v: 'x' });

	// The Telegram trigger fires with its required credential; the run never
	// calls Telegram because nothing is activated, only executed.
	const telegramCredential = await createCredential(server.baseURL, 'Coverage Telegram Trigger', 'telegramApi', {
		accessToken: 'e2e-token',
		baseUrl: stub.origin
	});
	const telegramTriggerId = await createWorkflow(
		server.baseURL,
		'Coverage Telegram Trigger',
		[
			node('trigger', 'Telegram Trigger', 'kilasflow.telegramTrigger', 1, { path: 'coverage', updates: ['message'] }, { telegramApi: telegramCredential }),
			setter('out', 'telegram-ok')
		],
		[conn('c1', 'trigger', 'main', 'out', 'main')]
	);
	const telegramTriggerRecord = await runToSuccess(server.baseURL, telegramTriggerId);
	expect(itemJson(telegramTriggerRecord, 'out')).toMatchObject({ v: 'telegram-ok' });

	// An Execute Workflow Trigger emits the caller's items.
	const subTriggerId = await createWorkflow(
		server.baseURL,
		'Coverage Sub Trigger',
		[node('trigger', 'Sub Trigger', 'kilasflow.executeWorkflowTrigger', 1, {}), setter('out', 'sub-trigger-ok')],
		[conn('c1', 'trigger', 'main', 'out', 'main')]
	);
	const subTriggerRecord = await runToSuccess(server.baseURL, subTriggerId);
	expect(itemJson(subTriggerRecord, 'out')).toMatchObject({ v: 'sub-trigger-ok' });

	const chatId = await createWorkflow(
		server.baseURL,
		'Coverage Chat Trigger',
		[node('chat', 'When chat message received', 'kilasflow.chatTrigger'), setter('out', 'chat-ok')],
		[conn('c1', 'chat', 'main', 'out', 'main')]
	);
	const chatStarted = await fetch(`${server.baseURL}/api/v1/workflows/${chatId}/run`, {
		method: 'POST',
		headers: { 'content-type': 'application/json' },
		body: JSON.stringify({
			triggerNodeId: 'chat',
			input: { action: 'sendMessage', sessionId: 'coverage-session', chatInput: 'hello coverage' }
		})
	});
	expect(chatStarted.status).toBe(202);
	const chatQueued = await chatStarted.json();
	const chatRecord = await waitForExecution(server.baseURL, chatQueued.id);
	expect(chatRecord.status).toBe('succeeded');
	expect(itemJson(chatRecord, 'chat')).toMatchObject({
		action: 'sendMessage',
		sessionId: 'coverage-session',
		chatInput: 'hello coverage'
	});
	expect(itemJson(chatRecord, 'out')).toMatchObject({ v: 'chat-ok' });

	// WAHA triggers expose one port per event; on a manual run the trigger
	// itself succeeds and the run completes.
	const wahaCredential = await createCredential(server.baseURL, 'Coverage WAHA Trigger', 'wahaApi', {
		baseUrl: stub.origin,
		apiKey: 'e2e-key'
	});
	for (const version of [202409, 202502]) {
		const wahaTriggerId = await createWorkflow(
			server.baseURL,
			`Coverage WAHA Trigger ${version}`,
			[
				node('trigger', 'WAHA Trigger', 'pack.wahaTrigger', version, { path: `coverage-${version}`, session: 'default' }, { wahaApi: wahaCredential })
			],
			[]
		);
		const wahaTriggerRecord = await runToSuccess(server.baseURL, wahaTriggerId);
		expect(nodeRun(wahaTriggerRecord, 'trigger').status).toBe('succeeded');
	}
});

test('the error workflow pair and the hosted form run against the binary', async ({ server }) => {
	await exerciseErrorWorkflow(server.baseURL, 'Coverage');
	await exerciseFormTrigger(server.baseURL, 'Coverage');
});

test('a loop iterates its body and collects on done', async ({ server }) => {
	const workflowId = await createWorkflow(
		server.baseURL,
		'Coverage Loop',
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
	const record = await runToSuccess(server.baseURL, workflowId);
	expect(nodeRun(record, 'body').status).toBe('succeeded');
	const done = nodeRun(record, 'done');
	expect(done.output[0]).toHaveLength(1);
	expect(done.output[0][0].json).toMatchObject({ v: 'x' });
});

test('a sticky note saves and runs without affecting the result', async ({ server }) => {
	const workflowId = await createWorkflow(
		server.baseURL,
		'Coverage Sticky',
		[manual(), setter('out', 'sticky-ok'), node('note', 'Note', 'kilasflow.stickyNote', 1, { content: '## Coverage' })],
		[conn('c1', 'manual', 'main', 'out', 'main')]
	);
	const record = await runToSuccess(server.baseURL, workflowId);
	expect(nodeRun(record, 'note').status).toBe('succeeded');
	expect(itemJson(record, 'out')).toMatchObject({ v: 'sticky-ok' });
});

test('a sub-workflow call runs the published callee', async ({ server }) => {
	const calleeId = await createWorkflow(server.baseURL, 'Coverage Callee', [manual(), setter('out', 'callee-ok')], [
		conn('c1', 'manual', 'main', 'out', 'main')
	]);
	const versions = await api(server.baseURL, 'GET', `/workflows/${calleeId}/versions`);
	const versionId = (versions.items ?? versions)[0].id as string;
	await api(server.baseURL, 'POST', `/workflows/${calleeId}/versions/${versionId}/publish`, {});

	const callerId = await createWorkflow(
		server.baseURL,
		'Coverage Caller',
		[manual(), node('call', 'Call', 'kilasflow.executeWorkflow', 1, { workflowId: calleeId })],
		[conn('c1', 'manual', 'main', 'call', 'main')]
	);
	const record = await runToSuccess(server.baseURL, callerId);
	const called = nodeRun(record, 'call');
	expect(called.output[0].length).toBeGreaterThan(0);
	expect(called.output[0][0].json).toMatchObject({ v: 'callee-ok' });
});

test('pack action nodes reach the stub through the outbound policy', async ({ server, stub }) => {
	const telegramCredential = await createCredential(server.baseURL, 'Coverage Telegram', 'telegramApi', {
		accessToken: 'e2e-token',
		baseUrl: stub.origin
	});
	const telegramId = await createWorkflow(
		server.baseURL,
		'Coverage Pack Telegram',
		[
			manual(),
			node(
				'send',
				'Send',
				'pack.telegram',
				1,
				{ resource: 'message', operation: 'sendMessage', chatId: '123', text: 'hi' },
				{ telegramApi: telegramCredential }
			)
		],
		[conn('c1', 'manual', 'main', 'send', 'main')]
	);
	const telegramStarted = await startRun(server.baseURL, telegramId);
	expect(telegramStarted.status).toBe(202);
	const telegramRecord = await waitForExecution(server.baseURL, telegramStarted.body.id);
	expect(telegramRecord.status).toBe('succeeded');
	const sent = itemJson(telegramRecord, 'send');
	expect(sent.ok).toBe(true);
	expect(sent.path).toContain('sendMessage');
	const telegramEvents = await readExecutionEvents(server.baseURL, telegramStarted.body.id);
	expect(telegramEvents.map((event) => event.type)).toContain('execution.completed');

	const wahaCredential = await createCredential(server.baseURL, 'Coverage WAHA', 'wahaApi', {
		baseUrl: stub.origin,
		apiKey: 'e2e-key'
	});
	for (const version of [202409, 202502]) {
		const wahaId = await createWorkflow(
			server.baseURL,
			`Coverage Pack WAHA ${version}`,
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
		const wahaRecord = await runToSuccess(server.baseURL, wahaId);
		const delivered = itemJson(wahaRecord, 'send');
		expect(delivered.ok).toBe(true);
		expect(delivered.path).toBe('/api/sendText');
	}
});

test('import capsules save but refuse to run', async ({ server }) => {
	// Python foreign code is kept for porting and can never run here.
	// (JavaScript foreign code runs now: tests/js-code.spec.ts covers it.)
	const foreignId = await createWorkflow(
		server.baseURL,
		'Coverage Foreign',
		[manual(), node('code', 'Foreign', 'kilasflow.foreignCode', 1, { language: 'python', pythonCode: 'return _input.all()' })],
		[conn('c1', 'manual', 'main', 'code', 'main')]
	);
	const foreignAttempt = await startRun(server.baseURL, foreignId);
	expect(foreignAttempt.status).toBe(422);
	expect(errorText(foreignAttempt.body)).toContain("this node's code is written in Python, which this server does not run.");

	// The unsupported capsule refuses at every registered arity.
	for (const arity of [1, 2, 4, 8]) {
		const unsupportedId = await createWorkflow(
			server.baseURL,
			`Coverage Unsupported ${arity}`,
			[manual(), node('capsule', 'Capsule', 'kilasflow.unsupported', arity, { originalType: 'n8n-nodes-base.coverage' })],
			[conn('c1', 'manual', 'main', 'capsule', 'main')]
		);
		const attempt = await startRun(server.baseURL, unsupportedId);
		expect(attempt.status).toBe(422);
		expect(errorText(attempt.body)).toContain('which KilasFlow does not support');
	}
});

test('the agent cluster fails closed without wiring or credentials', async ({ server, stub }) => {
	// An agent with no model wired names the missing port.
	const agentId = await createWorkflow(server.baseURL, 'Coverage Agent Bare', [manual(), node('agent', 'Agent', 'kilasflow.agent', 1, { prompt: 'hi' })], [
		conn('c1', 'manual', 'main', 'agent', 'main')
	]);
	const agentAttempt = await startRun(server.baseURL, agentId);
	expect(agentAttempt.status).toBe(422);
	expect(errorText(agentAttempt.body)).toContain('requires a connection on port "Chat Model"');

	// A chain without its model names the same missing port.
	const chainId = await createWorkflow(
		server.baseURL,
		'Coverage Chain Bare',
		[manual(), node('chain', 'Chain', 'kilasflow.chainLlm', 1, { promptType: 'define', text: 'hi' })],
		[conn('c1', 'manual', 'main', 'chain', 'main')]
	);
	const chainAttempt = await startRun(server.baseURL, chainId);
	expect(chainAttempt.status).toBe(422);
	expect(errorText(chainAttempt.body)).toContain('requires a connection on port "Chat Model"');

	// A wired chat model needs no key: an endpoint that needs none is the normal
	// case, so the run is accepted and the request leaves without an
	// Authorization header, while an attached httpBearerAuth credential is applied.
	const bearerModelId = await createWorkflow(
		server.baseURL,
		'Coverage Model Key',
		[
			manual(),
			node('model', 'Model', 'kilasflow.chatModel', 1, { model: 'coverage-stub', baseUrl: stub.origin }),
			node('agent', 'Agent', 'kilasflow.agent', 1, { prompt: 'hi' })
		],
		[conn('c1', 'manual', 'main', 'agent', 'main'), conn('c2', 'model', 'model', 'agent', 'model', 'ai_languageModel')]
	);
	const keylessAttempt = await startRun(server.baseURL, bearerModelId);
	expect(keylessAttempt.status).toBe(202);
	await waitForExecution(server.baseURL, keylessAttempt.body.id);
	const keylessCall = stub.requests.find((request) => request.method === 'POST' && request.path === '/chat/completions');
	expect(keylessCall, 'the keyless model call reached the stub').toBeDefined();
	expect(keylessCall!.headers.authorization).toBeUndefined();

	const keyId = await createCredential(server.baseURL, 'Coverage Model Key', 'httpBearerAuth', { token: 'coverage-key' });
	const keyedModelId = await createWorkflow(
		server.baseURL,
		'Coverage Model Keyed',
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
	expect(keyedCalls.map((request) => request.headers.authorization)).toEqual([undefined, 'Bearer coverage-key']);

	// Each provider model names its own credential type when none is attached.
	const providers: Array<{ type: string; credential: string }> = [
		{ type: 'kilasflow.lmChatOpenAi', credential: 'openAiApi' },
		{ type: 'kilasflow.lmChatOpenRouter', credential: 'openRouterApi' }
	];
	for (const provider of providers) {
		const providerId = await createWorkflow(
			server.baseURL,
			`Coverage ${provider.type}`,
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

	// Memory without a session key and a tool without its description fail at save-time validation.
	const memoryId = await createWorkflow(server.baseURL, 'Coverage Memory Bare', [manual(), node('memory', 'Memory', 'kilasflow.memoryBuffer', 1, {})], []);
	const memoryAttempt = await startRun(server.baseURL, memoryId);
	expect(memoryAttempt.status).toBe(422);
	expect(errorText(memoryAttempt.body)).toContain('sessionId is required');

	const toolId = await createWorkflow(server.baseURL, 'Coverage Tool Bare', [manual(), node('tool', 'Tool', 'kilasflow.httpTool', 1, {})], []);
	const toolAttempt = await startRun(server.baseURL, toolId);
	expect(toolAttempt.status).toBe(422);
	expect(errorText(toolAttempt.body)).toContain('toolDescription is required');

	// The calculator tool, workflow tool and output parser are agent-side
	// sub-nodes with no main ports, so they are covered the same way: the
	// draft saves, and the run names the missing configuration.
	const calcToolId = await createWorkflow(server.baseURL, 'Coverage Calc Tool Bare', [manual(), node('tool', 'Tool', 'kilasflow.calculatorTool', 1, {})], []);
	const calcToolAttempt = await startRun(server.baseURL, calcToolId);
	expect(calcToolAttempt.status).toBe(422);
	expect(errorText(calcToolAttempt.body)).toContain('toolDescription is required');

	const wfToolId = await createWorkflow(server.baseURL, 'Coverage Workflow Tool Bare', [manual(), node('tool', 'Tool', 'kilasflow.workflowTool', 1, {})], []);
	const wfToolAttempt = await startRun(server.baseURL, wfToolId);
	expect(wfToolAttempt.status).toBe(422);
	expect(errorText(wfToolAttempt.body)).toContain('toolDescription is required');

	const parserId = await createWorkflow(server.baseURL, 'Coverage Parser Bare', [manual(), node('parser', 'Parser', 'kilasflow.outputParser', 1, {})], []);
	const parserAttempt = await startRun(server.baseURL, parserId);
	expect(parserAttempt.status).toBe(422);
	expect(errorText(parserAttempt.body)).toContain('jsonSchema is required when the schema type is JSON Schema');

	// An MCP client tool without its server URL names the missing endpoint.
	// It needs a live MCP server to run, so validation is its headless tier.
	const mcpId = await createWorkflow(server.baseURL, 'Coverage MCP Bare', [manual(), node('tool', 'Tool', 'kilasflow.mcpClientTool', 1, {})], []);
	const mcpAttempt = await startRun(server.baseURL, mcpId);
	expect(mcpAttempt.status).toBe(422);
	expect(errorText(mcpAttempt.body)).toContain('serverUrl is required');

	// A datastore tool without its description names it before anything else.
	// It is an agent-side sub-node, so validation is its headless tier.
	const dsToolId = await createWorkflow(server.baseURL, 'Coverage Datastore Tool Bare', [manual(), node('tool', 'Tool', 'kilasflow.datastoreTool', 1, {})], []);
	const dsToolAttempt = await startRun(server.baseURL, dsToolId);
	expect(dsToolAttempt.status).toBe(422);
	expect(errorText(dsToolAttempt.body)).toContain('toolDescription is required');
});

test('datastore rows insert and read back headless', async ({ server }) => {
	// Data tables are server-owned: no credential, just the tables API plus
	// the node. The insert auto-maps the incoming item onto the live schema.
	const table = await api(server.baseURL, 'POST', '/datastores', { name: 'Coverage', columns: [{ name: 'v', type: 'string' }] }, 201);
	const workflowId = await createWorkflow(
		server.baseURL,
		'Coverage Datastore',
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
	const record = await runToSuccess(server.baseURL, workflowId);
	expect(nodeRun(record, 'insert').output.datastore).toMatchObject({ rows: 1 });
	expect(nodeRun(record, 'read').output.datastore).toMatchObject({ rows: 1 });
});

test('embeddings refuse without text; the internal vector store still needs pgvector', async ({ server }) => {
	const embeddingsId = await createWorkflow(
		server.baseURL,
		'Coverage Embeddings',
		[manual(), node('emb', 'Embeddings', 'kilasflow.embeddings', 1, { model: 'text-embedding-3-small' })],
		[conn('c1', 'manual', 'main', 'emb', 'main')]
	);
	const embeddingsAttempt = await startRun(server.baseURL, embeddingsId);
	expect(embeddingsAttempt.status).toBe(422);
	expect(errorText(embeddingsAttempt.body)).toContain('no text to embed');

	const storeId = await createWorkflow(
		server.baseURL,
		'Coverage Vector Store',
		[manual(), node('store', 'Store', 'kilasflow.vectorStore', 1, { operation: 'insert', collection: 'coverage' })],
		[conn('c1', 'manual', 'main', 'store', 'main')]
	);
	const storeAttempt = await startRun(server.baseURL, storeId);
	expect(storeAttempt.status).toBe(422);
	expect(errorText(storeAttempt.body)).toContain('pgvector');
});

test('document loader and text splitter run as a cluster', async ({ server }) => {
	const workflowId = await createWorkflow(
		server.baseURL,
		'Coverage RAG Cluster',
		[
			manual(),
			node('in', 'Set', 'kilasflow.set', 1, { assignments: { text: 'hello rag documents' } }),
			node('split', 'Splitter', 'kilasflow.textSplitter', 1, { chunkSize: 40, chunkOverlap: 4 }),
			node('load', 'Loader', 'kilasflow.documentLoader', 1, { textField: 'text' })
		],
		[
			conn('c1', 'manual', 'main', 'in', 'main'),
			conn('c2', 'in', 'main', 'load', 'main'),
			conn('c3', 'split', 'splitter', 'load', 'splitter', 'ai_textSplitter')
		]
	);
	const record = await runToSuccess(server.baseURL, workflowId);
	expect(itemJson(record, 'load').text).toContain('hello rag');
});

test('extract from file, customer pgvector and google nodes fail closed without their inputs', async ({ server }) => {
	const extractId = await createWorkflow(
		server.baseURL,
		'Coverage Extract',
		[manual(), node('extract', 'Extract', 'kilasflow.extractFromFile', 1, { operation: 'text' })],
		[conn('c1', 'manual', 'main', 'extract', 'main')]
	);
	const extractAttempt = await startRun(server.baseURL, extractId);
	expect(extractAttempt.status).toBe(422);
	expect(errorText(extractAttempt.body)).toMatch(/binary property|binary storage/i);

	const pgId = await createWorkflow(
		server.baseURL,
		'Coverage PGVector',
		[manual(), node('store', 'PGVector', 'kilasflow.vectorStorePGVector', 1, { tableName: 'documents' })],
		[conn('c1', 'manual', 'main', 'store', 'main')]
	);
	const pgAttempt = await startRun(server.baseURL, pgId);
	expect(pgAttempt.status).toBe(422);
	expect(errorText(pgAttempt.body)).toContain('postgres');

	const remotes: Array<{ type: string; credential: string }> = [
		{ type: 'kilasflow.googleDrive', credential: 'googleDriveOAuth2Api' },
		{ type: 'kilasflow.googleDriveTrigger', credential: 'googleDriveOAuth2Api' },
		{ type: 'kilasflow.gmail', credential: 'gmailOAuth2' },
		{ type: 'kilasflow.gmailTrigger', credential: 'gmailOAuth2' }
	];
	for (const remote of remotes) {
		const isTrigger = remote.type.endsWith('Trigger');
		const workflowId = await createWorkflow(
			server.baseURL,
			`Coverage ${remote.type}`,
			isTrigger
				? [node('node', 'Google', remote.type, 1, {})]
				: [manual(), node('node', 'Google', remote.type, 1, { operation: 'search' })],
			isTrigger ? [] : [conn('c1', 'manual', 'main', 'node', 'main')]
		);
		const attempt = await startRun(server.baseURL, workflowId);
		expect(attempt.status).toBe(422);
		expect(errorText(attempt.body)).toContain(`requires a ${remote.credential} credential`);
	}
});

test('remote database nodes fail closed without their credential', async ({ server }) => {
	// No database service ships with the harness, so the remote drivers are
	// covered at the validation tier: the missing credential is refused with
	// the picker message instead of a run-time failure. SQLite above covers
	// the SQL path that does run headless.
	const remotes: Array<{ type: string; version: number; credential: string }> = [
		{ type: 'kilasflow.postgres', version: 1, credential: 'postgres' },
		{ type: 'kilasflow.postgres', version: 2, credential: 'postgres' },
		{ type: 'kilasflow.mysql', version: 1, credential: 'mysql' },
		{ type: 'kilasflow.mysql', version: 2, credential: 'mysql' }
	];
	for (const remote of remotes) {
		const workflowId = await createWorkflow(
			server.baseURL,
			`Coverage ${remote.type} ${remote.version}`,
			[manual(), node('db', 'Database', remote.type, remote.version, { operation: 'query', statement: 'SELECT 1' })],
			[conn('c1', 'manual', 'main', 'db', 'main')]
		);
		const attempt = await startRun(server.baseURL, workflowId);
		expect(attempt.status).toBe(422);
		expect(errorText(attempt.body)).toContain(`requires a ${remote.credential} credential`);
	}
});
