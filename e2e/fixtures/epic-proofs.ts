// FEAT-5fhj6p: the four epic proofs of EPIC-m42s3g, extracted from the
// hermetic spec so a second, rare suite can run the SAME code against a
// different HOST (the locally built binary, or a published image) and
// different third-party SIDES (loopback stubs, or the real Telegram Bot API,
// a real WAHA server, the npm registry).
//
// Why one implementation and two callers: a stub proves KilasFlow sends what
// it believes it should send; only the real service proves the wire agrees.
// Two implementations would drift and the rarely-run one would rot, so the
// skeleton here is written once, against the interfaces, and never branches on
// `host.kind` or `side.kind` — everything environment-specific is a call.
//
// The hermetic suite (e2e/tests/epic-acceptance.spec.ts) is the caller that
// runs on every PR against binaryHost() + the stub sides; the on-demand
// capstone (e2e/capstone) is the caller that runs against an image host and
// the real sides. This file moves the proof bodies verbatim and replaces only
// the environment-specific lines with host/side calls.
//
// The epic's no-Node.js criterion is asserted per server while it is live, in
// every proof — it is a constraint of the epic, not an implementation detail
// of one proof.
import { expect, type Page } from '@playwright/test';
import { createHmac, randomBytes } from 'node:crypto';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { createCredential, createWorkflow, waitForExecution } from '../helpers/seed';
import type { StubServer } from '../helpers/stub';
import {
	apiJson,
	addColumn,
	createDatastore,
	createWorkflowDocument,
	datastoreLocator,
	datastoreWriteReadDocument,
	exportCsv,
	fetchWorkflowDocument,
	getDatastore,
	importCsv,
	importN8nWorkflow,
	listDatastores,
	listRows,
	manualColumns,
	n8nDataTableExport,
	n8nStyleCsv,
	renameDatastore,
	saveWorkflowDocument,
	startWorkflowRun,
	uniqueName
} from './datastore';
import { PACK_TYPE } from './pack-install';
import { scaffoldExternalSource, startExternalHost, writeHostPage, type ExternalSource } from './epic-external';
import {
	deliverTelegramUpdate,
	epicUpdate,
	itemJsonEpic,
	readFullChannelEpic,
	telegramSecret
} from './epic-telegram';
import {
	bindWahaCredential,
	completeChattingMigration,
	deliverWebhook,
	fetchDocument,
	importN8nTemplate,
	listExecutions,
	saveDocument,
	wahaPingDelivery,
	waitForNewExecution,
	type WorkflowNode
} from './waha-migration';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');

// Static literals with a dynamic lookup: the statuses that end an execution.
const TERMINAL: Record<string, true> = { succeeded: true, failed: true, cancelled: true };

// --- The host contract -----------------------------------------------------
//
// A host boots KilasFlow somewhere the suite can reach it: the locally built
// binary (e2e/fixtures/epic-hermetic.ts) or a published image
// (e2e/capstone). `start` is the only place that knows how; the proofs only
// pass settings through it.

export interface EpicServer {
	baseURL: string;
	port: number;
	close(): Promise<void>;
	logs?(): Promise<string>;
	// Image host only: the container this server runs in, so the no-Node check
	// can tell the server's own container from the rest of the run's stack.
	container?: string;
}

export interface PostgresHandle {
	readonly label: string;
	close(): Promise<void>;
}

export interface HostStartOptions {
	// host:port via outbound.allowed_private_endpoints; never
	// allow_private_networks (see .pine/MEMORY.md).
	privateEndpoints?: string[];
	// outbound.allowed_hosts extras (the binary host ignores: the harness
	// already pins 127.0.0.1).
	allowedHosts?: string[];
	// embed.allowed_origins.
	allowedOrigins?: string[];
	// server.public_url.
	publicUrl?: string;
	// Image host only: a fixed publish port so a tunnel can target it first.
	hostPort?: number;
	// HOST path of a packs directory.
	packsDir?: string;
	// Start against this Postgres server instead of the default SQLite file.
	postgres?: PostgresHandle;
}

export interface NoNodeVerdict {
	checked: boolean;
	reason: string;
	scope: string;
	serverComm: string;
	processes: { pid: number; ppid: number; comm: string }[];
	nodeProcesses: { pid: number; ppid: number; comm: string }[];
}

// The evidence channel the report CLI fills in (stage 2). The hermetic suite
// passes nothing; every call is optional so the proof bodies stay identical.
export interface ProofContext {
	note(key: string, value: unknown): void;
	recordNoNode(v: NoNodeVerdict): void;
}

export interface EpicHost {
	readonly kind: 'binary' | 'image';
	readonly label: string;
	start(o?: HostStartOptions): Promise<EpicServer>;
	// null = not configured; the caller skips with its own gate text.
	startPostgres(): Promise<PostgresHandle | null>;
	// As seen FROM a server: `reachableOrigin` is the origin to hand the
	// server, `reachableEndpoint` the host:port its outbound policy admits.
	startStub(): Promise<StubServer & { reachableOrigin: string; reachableEndpoint: string }>;
	assertNoNode(server: EpicServer): Promise<NoNodeVerdict>;
	// `args` may use `{root}`, substituted with packsRoot by the host (an
	// image host runs the tool inside its container).
	nodepackgen(packsRoot: string, args: string[]): Promise<{ stdout: string; stderr: string }>;
}

// The shared no-Node assertion: the server is the kilasflow process, its
// subtree holds no Node-like process, and the verdict was readable at all.
// The reason assertion is what makes a stack failure name the offending
// container or process rather than just showing a non-empty list.
export async function assertNoNodeOk(host: EpicHost, server: EpicServer, ctx?: ProofContext): Promise<void> {
	const verdict = await host.assertNoNode(server);
	expect(verdict.checked, verdict.reason).toBe(true);
	expect(verdict.serverComm).toContain('kilasflow');
	expect(verdict.reason, 'the process-table verdict carries no defect').toBe('');
	expect(verdict.nodeProcesses).toHaveLength(0);
	ctx?.recordNoNode(verdict);
}

// --- The third-party side contracts ----------------------------------------

export interface TelegramSide {
	readonly kind: 'stub' | 'real';
	prepare(): Promise<HostStartOptions>;
	botCredential(): { accessToken: string; baseUrl?: string };
	model(): { token: string; baseUrl: string; model: string };
	afterActivate(route: string, ctx?: ProofContext): Promise<void>;
	inbound(server: EpicServer, route: string, text: string): Promise<{ secret?: string; via: string }>;
	afterDeactivate(route: string): Promise<void>;
	close(): Promise<void>;
}

export interface WahaSide {
	readonly kind: 'stub' | 'real';
	prepare(tenantIndex: number): Promise<HostStartOptions>;
	credentialFields(): { baseUrl: string; apiKey: string };
	imageUrl(): string;
	// stub: hmacSecret; real: session + autoRegister + hmacSecret.
	configureTrigger(trigger: WorkflowNode, tenantIndex: number, secret: string): void;
	// Where the forged delivery is POSTed.
	publicBase(tenantIndex: number, server: EpicServer): string;
	afterActivate(tenantIndex: number, route: string, secret: string): Promise<void>;
	provoke(servers: EpicServer[], routes: string[], secrets: string[]): Promise<void>;
	awaitReplies(servers: EpicServer[], workflowIds: string[]): Promise<void>;
	afterDeactivate(tenantIndex: number): Promise<void>;
	close(): Promise<void>;
}

// --- Shared machinery ------------------------------------------------------

// A WAHA delivery signed over the exact bytes sent — the raw-body contract the
// trigger's HMAC verifier checks (hex HMAC-SHA512, X-Webhook-Hmac).
export async function deliverWahaSigned(
	baseURL: string,
	url: string,
	body: Record<string, unknown>,
	secret: string
): Promise<number> {
	const raw = JSON.stringify(body);
	const signature = createHmac('sha512', secret).update(raw, 'utf-8').digest('hex');
	const response = await fetch(`${baseURL}${url}`, {
		method: 'POST',
		headers: { 'content-type': 'application/json', 'X-Webhook-Hmac': signature },
		body: raw
	});
	return response.status;
}

// The minted route for a workflow, read from the API rather than from a stub's
// captured setWebhook body: the real side has no stub to capture it, and the
// API is the same surface in both suites.
export async function workflowWebhookRoute(baseURL: string, workflowId: string): Promise<string> {
	const webhooks = (await apiJson(baseURL, 'GET', `/workflows/${workflowId}/webhooks`)) as Array<{ url: string }>;
	expect(webhooks.length).toBeGreaterThan(0);
	return webhooks[0].url;
}

// The node runs of an execution record, validated at the boundary so the
// predicate below reads typed values rather than a bare `any`.
function nodeRunsOf(record: Record<string, unknown>): Array<{ nodeId?: unknown; output?: unknown }> {
	const runs = record.nodeRuns;
	if (!Array.isArray(runs)) return [];
	return runs.filter(
		(run): run is { nodeId?: unknown; output?: unknown } => typeof run === 'object' && run !== null
	);
}

// The items one node produced, flattened across output ports. The recorded
// output is one stream per port; each entry carries its json.
function triggerItems(run: { output?: unknown } | undefined): unknown[] {
	const items: unknown[] = [];
	if (!run || !Array.isArray(run.output)) return items;
	for (const port of run.output) {
		if (!Array.isArray(port)) continue;
		for (const entry of port) {
			items.push(entry && typeof entry === 'object' && 'json' in entry ? (entry as { json: unknown }).json : entry);
		}
	}
	return items;
}

// The newest terminal execution for a workflow past `before` whose TRIGGER run
// emitted an item the predicate accepts. The hermetic predicate is `() => true`
// — any new execution will do — but a real WAHA server also delivers
// session.status and message.ack events, so "any new execution" stops being
// enough once the side is real; the trigger node's own output is where the
// delivered message is identifiable.
export async function waitForMatchingExecution(
	baseURL: string,
	workflowId: string,
	before: string[],
	predicate: (triggerItem: unknown) => boolean,
	triggerNodeId: string
): Promise<Record<string, unknown>> {
	const known = new Set(before);
	let match: Record<string, unknown> | undefined;
	await expect
		.poll(
			async () => {
				const items = await listExecutions(baseURL, workflowId);
				for (const item of items) {
					if (known.has(item.id)) continue;
					const record = (await apiJson(baseURL, 'GET', `/executions/${item.id}`)) as Record<string, unknown>;
					const status = typeof record.status === 'string' ? record.status : '';
					if (TERMINAL[status] !== true) continue;
					const run = nodeRunsOf(record).find((entry) => entry.nodeId === triggerNodeId);
					if (triggerItems(run).some((entry) => predicate(entry))) {
						match = record;
						return true;
					}
				}
				return false;
			},
			{ message: `an execution of ${workflowId} matching the trigger predicate`, timeout: 120_000 }
		)
		.toBe(true);
	if (!match) throw new Error(`no matching execution of ${workflowId} was found`);
	return match;
}

// --- Proof 1: Telegram + AI agent ------------------------------------------

export async function proofTelegramAgent(host: EpicHost, side: TelegramSide, ctx?: ProofContext): Promise<void> {
	const options = await side.prepare();
	const bot = side.botCredential();
	const model = side.model();
	const server = await host.start(options);
	try {
		const baseURL = server.baseURL;

		const botId = (
			await createCredential(baseURL, {
				name: 'Epic Bot',
				type: 'telegramApi',
				fields: { accessToken: bot.accessToken, ...(bot.baseUrl ? { baseUrl: bot.baseUrl } : {}) }
			})
		).id;
		const llmId = (
			await createCredential(baseURL, {
				name: 'Epic Ollama',
				type: 'httpBearerAuth',
				fields: { token: model.token }
			})
		).id;

		const workflow = await createWorkflow(baseURL, {
			schemaVersion: 1,
			name: 'Epic Telegram Agent',
			nodes: [
				{
					id: 'trigger',
					name: 'Telegram Trigger',
					type: 'kilasflow.telegramTrigger',
					typeVersion: 1,
					position: { x: 0, y: 0 },
					parameters: { path: 'epic-bot', updates: ['message'] },
					credentials: { telegramApi: botId }
				},
				{
					id: 'model',
					name: 'Chat Model',
					type: 'kilasflow.chatModel',
					typeVersion: 1,
					position: { x: 0, y: 240 },
					parameters: { model: model.model, baseUrl: model.baseUrl, temperature: 0, stream: true },
					credentials: { httpBearerAuth: llmId }
				},
				{
					id: 'agent',
					name: 'Agent',
					type: 'kilasflow.agent',
					typeVersion: 1,
					position: { x: 240, y: 0 },
					parameters: {
						prompt: {
							mode: 'expression',
							value: 'Reply in ten words or fewer to this Telegram message: {{ $json.message.text }}'
						},
						returnIntermediateSteps: true
					}
				},
				{
					id: 'send',
					name: 'Send Reply',
					type: 'pack.telegram',
					typeVersion: 1,
					position: { x: 480, y: 0 },
					parameters: {
						resource: 'message',
						operation: 'sendMessage',
						chatId: { mode: 'expression', value: "{{ $('Telegram Trigger').item.json.message.chat.id }}" },
						text: { mode: 'expression', value: '{{ $json.output }}' }
					},
					credentials: { telegramApi: botId }
				}
			],
			connections: [
				{ id: 'c1', kind: 'main', source: { nodeId: 'trigger', port: 'main' }, target: { nodeId: 'agent', port: 'main' } },
				{ id: 'c2', kind: 'ai_languageModel', source: { nodeId: 'model', port: 'model' }, target: { nodeId: 'agent', port: 'model' } },
				{ id: 'c3', kind: 'main', source: { nodeId: 'agent', port: 'main' }, target: { nodeId: 'send', port: 'main' } }
			],
			settings: {}
		});

		// Activation registers the trigger's own webhook: existence is checked
		// first (getWebhookInfo), then setWebhook carries the minted route. No
		// notices — registration was attempted, not deferred to the user.
		const activated = await apiJson(baseURL, 'POST', `/workflows/${workflow.id}/activate`, {});
		expect(activated.active).toBe(true);
		expect(activated.notices ?? []).toHaveLength(0);
		const route = await workflowWebhookRoute(baseURL, workflow.id);
		expect(route).toMatch(/^\/webhook\/[0-9a-f]{32}$/);
		await side.afterActivate(route, ctx);

		// The secret the service was told matches the verifier's derivation from
		// the same token and route — the fragile half.
		const secret = telegramSecret(bot.accessToken, route.split('/').pop() ?? '');

		// A delivery with the wrong secret is refused before an execution
		// exists; one with none at all is refused too.
		const knownBefore = await listExecutions(baseURL, workflow.id);
		expect((await deliverTelegramUpdate(baseURL, route, epicUpdate('hello?'), 'wrong-secret')).status).toBe(401);
		expect((await deliverTelegramUpdate(baseURL, route, epicUpdate('hello?'))).status).toBe(401);
		expect((await listExecutions(baseURL, workflow.id)).map((item) => item.id)).toEqual(
			knownBefore.map((item) => item.id)
		);

		// A real message is delivered, the agent answers, and the reply arrives
		// at the bot API — observed at the side, not asserted by existence.
		await side.inbound(server, route, 'What is the capital of France? Answer in ten words or fewer.');
		const { record } = await waitForNewExecution(
			baseURL,
			workflow.id,
			knownBefore.map((item) => item.id)
		);
		expect(record.status).toBe('succeeded');
		const agentOutput = itemJsonEpic(record, 'agent');
		expect(typeof agentOutput.output === 'string' && agentOutput.output.trim().length > 0).toBe(true);

		// The Send Reply node's output is the bot API's own response, so this
		// one assertion proves the third party accepted the send, answered,
		// addressed the trigger's chat and carried the agent's own words —
		// identical for the stub and the real Bot API.
		const sent = itemJsonEpic(record, 'send');
		expect(sent.ok).toBe(true);
		expect(typeof sent.result?.message_id).toBe('number');
		expect(String(sent.result?.chat?.id)).toContain('774411');
		expect(sent.result?.text).toBe(agentOutput.output);

		// The same loop observed live: the model streamed and the run completed
		// on one channel. Structural assertions only — never the words the
		// model chose.
		const events = await readFullChannelEpic(baseURL, record.id);
		const types = events.map((event) => event.type);
		expect(types).toContain('ai.model.delta');
		expect(types).toContain('execution.completed');

		// No Node.js process in or beside the server, checked live.
		await assertNoNodeOk(host, server, ctx);

		// Deactivation removes the webhook, and the route goes dark.
		await apiJson(baseURL, 'POST', `/workflows/${workflow.id}/deactivate`, {});
		await side.afterDeactivate(route);
		const update = epicUpdate('What is the capital of France? Answer in ten words or fewer.');
		expect((await deliverTelegramUpdate(baseURL, route, update, secret)).status).toBe(404);
	} finally {
		await server.close();
		await side.close();
	}
}

// --- Proof 2: WAHA chatting template, two tenants, HMAC --------------------

export async function proofWahaTwoTenants(
	host: EpicHost,
	side: WahaSide,
	template: Record<string, unknown>,
	ctx?: ProofContext
): Promise<void> {
	const serverA = await host.start(await side.prepare(0));
	const serverB = await host.start(await side.prepare(1));
	try {
		const tenants = [
			{ server: serverA, label: 'client A', secret: randomBytes(24).toString('hex') },
			{ server: serverB, label: 'client B', secret: randomBytes(24).toString('hex') }
		] as const;
		const imported: Array<{ workflow: { id: string }; url: string; triggerId: string }> = [];
		for (const [index, tenant] of tenants.entries()) {
			const result = await importN8nTemplate(tenant.server.baseURL, template, `WAHA Chatting (epic ${tenant.label})`);
			expect(result.workflow.id.length).toBeGreaterThan(0);
			expect(result.webhooks).toHaveLength(1);
			expect(result.webhooks[0].url).toMatch(/^\/webhook\/[0-9a-f]{32}$/);
			const credentialId = (
				await createCredential(tenant.server.baseURL, {
					name: `WAHA (${tenant.label})`,
					type: 'wahaApi',
					fields: side.credentialFields()
				})
			).id;
			const document = await fetchDocument(tenant.server.baseURL, result.workflow.id);
			bindWahaCredential(document, credentialId);
			completeChattingMigration(document, side.imageUrl());
			const trigger = document.nodes.find((node) => node.type === 'pack.wahaTrigger');
			expect(trigger, 'the imported workflow carries a WAHA trigger').toBeDefined();
			if (!trigger) throw new Error('the imported workflow carries no WAHA trigger');
			trigger.parameters = { ...(trigger.parameters ?? {}) };
			side.configureTrigger(trigger, index, tenant.secret);
			await saveDocument(tenant.server.baseURL, result.workflow.id, document);
			await apiJson(tenant.server.baseURL, 'POST', `/workflows/${result.workflow.id}/activate`, {});
			await side.afterActivate(index, result.webhooks[0].url, tenant.secret);
			imported.push({ workflow: result.workflow, url: result.webhooks[0].url, triggerId: trigger.id });
		}
		expect(imported[0].url).not.toBe(imported[1].url);

		// A route minted on one tenant means nothing on the other.
		expect(await deliverWebhook(serverB.baseURL, imported[0].url, wahaPingDelivery())).toBe(404);

		// HMAC over the raw body: a wrong signature is refused before an
		// execution exists; the right one runs to a reply.
		const knownFirst = await listExecutions(serverA.baseURL, imported[0].workflow.id);
		const knownSecond = await listExecutions(serverB.baseURL, imported[1].workflow.id);
		expect(await deliverWahaSigned(serverA.baseURL, imported[0].url, wahaPingDelivery(), 'not-the-secret')).toBe(401);
		expect((await listExecutions(serverA.baseURL, imported[0].workflow.id)).map((item) => item.id)).toEqual(
			knownFirst.map((item) => item.id)
		);

		await side.provoke(
			[serverA, serverB],
			[imported[0].url, imported[1].url],
			[tenants[0].secret, tenants[1].secret]
		);
		const one = await waitForMatchingExecution(
			serverA.baseURL,
			imported[0].workflow.id,
			knownFirst.map((item) => item.id),
			() => true,
			imported[0].triggerId
		);
		expect(one.status).toBe('succeeded');
		expect(one.workflowId).toBe(imported[0].workflow.id);
		const two = await waitForMatchingExecution(
			serverB.baseURL,
			imported[1].workflow.id,
			knownSecond.map((item) => item.id),
			() => true,
			imported[1].triggerId
		);
		expect(two.status).toBe('succeeded');
		expect(two.workflowId).toBe(imported[1].workflow.id);

		// Both tenants' replies reached the side: each workflow answered "pong".
		await side.awaitReplies([serverA, serverB], [imported[0].workflow.id, imported[1].workflow.id]);

		// No Node.js process beside either server, checked while they are live.
		await assertNoNodeOk(host, serverA, ctx);
		await assertNoNodeOk(host, serverB, ctx);
	} finally {
		await side.afterDeactivate(1);
		await side.afterDeactivate(0);
		await side.close();
		await serverB.close();
		await serverA.close();
	}
}

// --- Proof 3a: the datastore lifecycle -------------------------------------

export async function proofDatastoreLifecycle(
	host: EpicHost,
	pg: PostgresHandle | null,
	ctx?: ProofContext
): Promise<void> {
	const server = await host.start(pg ? { postgres: pg } : {});
	try {
		// Created through the name-only dialog shape, then edited: two user
		// columns and a rename.
		const store = await createDatastore(server.baseURL, uniqueName('Epic Store'));
		await addColumn(server.baseURL, store.id, 'email', 'string');
		await addColumn(server.baseURL, store.id, 'score', 'number');
		const renamed = await renameDatastore(server.baseURL, store.id, `${store.name} (edited)`);
		expect(renamed.name).toContain('(edited)');
		const definition = await getDatastore(server.baseURL, store.id);
		expect((definition.columns as Array<{ name: string }>).map((column) => column.name)).toEqual(
			expect.arrayContaining(['email', 'score'])
		);

		// Written by a workflow node and read back with a filter — the filtered
		// read is proven by the execution record, not the API.
		const document = datastoreWriteReadDocument(
			uniqueName('Epic Store Writer'),
			store.id,
			{ email: 'epic@example.com', score: 9 },
			'email',
			'epic@example.com'
		);
		const writer = await createWorkflowDocument(server.baseURL, document);
		const started = await startWorkflowRun(server.baseURL, writer.id);
		expect(started.status).toBe(202);
		const record = await waitForExecution(server.baseURL, started.body.id);
		expect(record.status).toBe('succeeded');
		const stored = await listRows(server.baseURL, store.id);
		expect((stored.items as Array<{ email: string }>).some((row) => row.email === 'epic@example.com')).toBe(true);

		// CSV round trip on its own table: an n8n-style export imports and
		// re-exports byte-identical.
		const csvStore = await createDatastore(server.baseURL, uniqueName('Epic CSV'));
		await addColumn(server.baseURL, csvStore.id, 'email', 'string');
		await addColumn(server.baseURL, csvStore.id, 'score', 'number');
		await importCsv(server.baseURL, csvStore.id, n8nStyleCsv());
		expect(await exportCsv(server.baseURL, csvStore.id)).toBe(n8nStyleCsv());

		// The same workflow imported from an n8n Data Table export binds to the
		// datastore and runs; the unbound twin refuses.
		const n8nImported = await importN8nWorkflow(server.baseURL, n8nDataTableExport(), uniqueName('Epic N8N Table'));
		const blocking = n8nImported.unsupported.filter((issue) => issue.severity === 'blocking');
		expect(blocking.some((issue) => (issue.reason ?? '').includes('dt_metrics_01'))).toBe(true);
		const n8nDocument = (await fetchWorkflowDocument(server.baseURL, n8nImported.workflow.id)) as {
			nodes: Array<{ name: string; type?: string; parameters: Record<string, unknown> }>;
		};
		const table = n8nDocument.nodes.find((node) => node.name === 'Table');
		expect(table?.type).toBe('kilasflow.datastore');
		if (!table) throw new Error('the imported n8n workflow carries no Table node');
		table.parameters.dataTableId = datastoreLocator('id', store.id);
		table.parameters.columns = manualColumns({ email: 'bound@example.com', score: 7 });
		await saveWorkflowDocument(server.baseURL, n8nImported.workflow.id, n8nDocument);
		const boundStart = await startWorkflowRun(server.baseURL, n8nImported.workflow.id);
		expect(boundStart.status).toBe(202);
		expect((await waitForExecution(server.baseURL, boundStart.body.id)).status).toBe('succeeded');
		expect(
			((await listRows(server.baseURL, store.id)).items as Array<{ email: string }>).some(
				(row) => row.email === 'bound@example.com'
			)
		).toBe(true);

		await assertNoNodeOk(host, server, ctx);
	} finally {
		await server.close();
	}
}

// --- Proof 3b: cross-tenant isolation --------------------------------------

export async function proofDatastoreIsolation(
	host: EpicHost,
	pg: PostgresHandle | null,
	ctx?: ProofContext
): Promise<void> {
	const server = await host.start(pg ? { postgres: pg } : {});
	// The second tenant is a second instance with its own database: the
	// strongest isolation the harness offers (auth off means each instance is
	// its own default tenant). Same-DB multi-tenancy is pinned by the Go
	// fixedTenant suites; this proves the cross-instance surface.
	const tenantB = await host.start();
	try {
		const store = await createDatastore(server.baseURL, uniqueName('Epic Tenant A'));
		await addColumn(server.baseURL, store.id, 'email', 'string');
		await importCsv(server.baseURL, store.id, 'email\nvictim@example.com\n');

		const missingId = 'datastore_00000000-0000-7000-8000-000000000000';
		const victimOnB = await fetch(`${tenantB.baseURL}/api/v1/datastores/${store.id}`);
		const missingOnB = await fetch(`${tenantB.baseURL}/api/v1/datastores/${missingId}`);
		expect(victimOnB.status).toBe(404);
		expect(missingOnB.status).toBe(404);
		// Indistinguishable: existence is not oracle-able.
		expect(await victimOnB.text()).toContain('not found');
		expect(await missingOnB.text()).toContain('not found');
		expect(await (await fetch(`${tenantB.baseURL}/api/v1/datastores/${store.id}/rows?limit=20`)).status).toBe(404);
		expect(
			await (
				await fetch(`${tenantB.baseURL}/api/v1/datastores/${store.id}/rows`, {
					method: 'POST',
					headers: { 'content-type': 'application/json' },
					body: JSON.stringify({ values: { email: 'intruder@example.com' } })
				})
			).status
		).toBe(404);
		expect((await listDatastores(tenantB.baseURL)).map((entry) => entry.id)).not.toContain(store.id);

		const probe = await createWorkflowDocument(
			tenantB.baseURL,
			datastoreWriteReadDocument(
				uniqueName('Epic Tenant B Probe'),
				store.id,
				{ email: 'intruder@example.com' },
				'email',
				'intruder@example.com'
			)
		);
		const started = await startWorkflowRun(tenantB.baseURL, probe.id);
		expect(started.status).toBe(202);
		const record = await waitForExecution(tenantB.baseURL, started.body.id);
		expect(record.status).toBe('failed');
		expect(JSON.stringify(record)).toContain('unknown datastore');
		expect(
			((await listRows(server.baseURL, store.id)).items as Array<{ email: string }>).some(
				(row) => row.email === 'intruder@example.com'
			)
		).toBe(false);

		await assertNoNodeOk(host, server, ctx);
		await assertNoNodeOk(host, tenantB, ctx);
	} finally {
		await tenantB.close();
		await server.close();
	}
}

// --- Proof 4: an external consumer with no checkout ------------------------

export async function proofExternalConsumer(
	host: EpicHost,
	source: ExternalSource,
	page: Page,
	ctx?: ProofContext
): Promise<void> {
	// The external application: a scratch project outside the repository with
	// the SDK installed — the operator's own `npm install`, run for real. The
	// tarball-shape assertion lives in scaffoldExternalApp, which is where the
	// bytes are produced, so the proof itself works for either source.
	const app = await scaffoldExternalSource(source);
	await writeHostPage(app.dir);
	expect(app.dir.startsWith(`${repoRoot}/`)).toBe(false);

	// A community pack installed by placing it in a directory — authored with
	// the operator tooling into the external app's own packs dir.
	const packsRoot = join(app.dir, 'packs');
	await host.nodepackgen(packsRoot, [
		'scaffold',
		'-dir',
		'{root}/hello',
		'-type',
		PACK_TYPE,
		'-display-name',
		'E2E Hello',
		'-credential-type',
		'wahaApi'
	]);
	expect((await host.nodepackgen(packsRoot, ['validate', '{root}/hello'])).stderr).toContain('packs are valid');
	expect((await host.nodepackgen(packsRoot, ['pack', '-dir', '{root}/hello'])).stderr).toContain('pack.sha256');

	const stub = await host.startStub();
	const externalHost = await startExternalHost(app.dir);
	const server = await host.start({
		packsDir: packsRoot,
		privateEndpoints: [stub.reachableEndpoint],
		allowedOrigins: [externalHost.origin]
	});
	try {
		const client = new app.KilasFlowClient({ baseUrl: server.baseURL });
		expect((await client.getReady()) != null).toBe(true);

		// The directory-installed pack is visible to the external consumer,
		// tagged with its source.
		const catalogue = (await client.listNodeTypes()) as Array<{ type: string; source: string }>;
		const installedPack = catalogue.find((entry) => entry.type === PACK_TYPE);
		expect(installedPack, 'the directory-installed pack is listed').toBeDefined();
		expect(installedPack?.source).toBe('pack');

		// A workflow through the SDK runs a pack node against the stub.
		const packCredential = await client.createCredential({
			name: 'External Pack WAHA',
			type: 'wahaApi',
			fields: { baseUrl: stub.reachableOrigin, apiKey: 'e2e-key' }
		});
		const packWorkflow = await client.createWorkflow({
			schemaVersion: 1,
			name: 'External Pack Workflow',
			nodes: [
				{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
				{
					id: 'send',
					name: 'Send',
					type: PACK_TYPE,
					typeVersion: 1,
					position: { x: 240, y: 0 },
					parameters: { resource: 'message', operation: 'sendMessage', chatId: 'external-chat', text: 'hello packs' },
					credentials: { wahaApi: packCredential.id }
				}
			],
			connections: [
				{ id: 'c1', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'send', port: 'main' } }
			],
			settings: {}
		});
		const packRun = await client.runWorkflow(packWorkflow.id);
		await expect
			.poll(async () => TERMINAL[(await client.getExecution(packRun.id)).status] === true, {
				message: 'the pack workflow reaches a terminal status',
				timeout: 120_000
			})
			.toBe(true);
		expect((await client.getExecution(packRun.id)).status).toBe('succeeded');
		const packCall = stub.requests.find((request) => request.method === 'POST' && request.path === '/sendMessage');
		expect(packCall, 'the pack node reached the stub').toBeDefined();
		expect(JSON.parse(packCall?.body ?? '{}')).toMatchObject({ chatId: 'external-chat', text: 'hello packs' });

		// A datastore through the SDK: create with columns, edit with another,
		// write, filtered update, required-filter delete, and a cursor walk to
		// exhaustion.
		const store = await client.createDatastore({
			name: `External Store ${randomBytes(4).toString('hex')}`,
			columns: [{ name: 'email', type: 'string' }]
		});
		await client.addDatastoreColumn(store.id, { name: 'score', type: 'number' });
		await client.insertDatastoreRow(store.id, { email: 'host@example.com', score: 8 });
		await client.insertDatastoreRow(store.id, { email: 'other@example.com', score: 3 });
		const filter = app.datastoreFilter('and', [{ columnName: 'email', condition: 'eq', value: 'host@example.com' }]);
		await client.updateDatastoreRows(store.id, filter, { score: 10 });
		const seen: Array<{ email?: unknown; score?: unknown }> = [];
		for await (const row of client.iterateDatastoreRows(store.id)) seen.push(row as { email?: unknown });
		expect(seen.length).toBe(2);
		expect(seen.find((row) => row.email === 'host@example.com')?.score).toBe(10);
		// The required-filter rule is enforced client-side: an empty filter never
		// reaches the network.
		await expect(client.deleteDatastoreRows(store.id, { type: 'and', filters: [] })).rejects.toThrow();
		await client.deleteDatastoreRows(store.id, filter);
		const remaining: unknown[] = [];
		for await (const row of client.iterateDatastoreRows(store.id)) remaining.push(row);
		expect(remaining.length).toBe(1);

		// Import, the way the reference host provisions: the only call reporting
		// the minted webhook address back.
		const hookName = `External Hook ${randomBytes(4).toString('hex')}`;
		const n8nDoc = {
			name: hookName,
			nodes: [
				{
					id: 'hook',
					name: 'Signup webhook',
					type: 'n8n-nodes-base.webhook',
					typeVersion: 2,
					position: [80, 80],
					parameters: { path: `external-${randomBytes(4).toString('hex')}`, httpMethod: 'POST', responseMode: 'onReceived' }
				}
			],
			connections: {}
		};
		const n8nImported = await client.importWorkflow({ format: 'n8n', name: hookName, workflow: n8nDoc });
		expect(n8nImported.workflow.id.length).toBeGreaterThan(0);
		expect(n8nImported.webhooks?.[0]?.url).toMatch(/^\/webhook\/[0-9a-f]{32}$/);
		expect((await client.activateWorkflow(n8nImported.workflow.id)).active).toBe(true);

		// The embedded editor mounts in the external host's own page with an
		// SDK-minted session — the reference-host loop, end to end.
		const session = await client.createEmbedSession({
			workflowId: packWorkflow.id,
			origin: externalHost.origin,
			scopes: ['workflow:read', 'workflow:write', 'workflow:run']
		});
		const hostURL =
			`${externalHost.url('/host.html')}?server=${encodeURIComponent(server.baseURL)}` +
			`&workflow=${encodeURIComponent(packWorkflow.id)}` +
			`&token=${encodeURIComponent(session.token)}` +
			`&scopes=${encodeURIComponent(session.scopes.join(','))}`;
		await page.goto(hostURL);
		await expect
			.poll(
				async () =>
					page.evaluate(
						() =>
							(window as unknown as { __externalEvents: { type: string }[] }).__externalEvents.filter(
								(event) => event.type === 'kilasflow:embed-ready'
							).length
					),
				{ message: 'the embed frame announces readiness in the external page' }
			)
			.toBeGreaterThan(0);
		const frame = page.frameLocator('#external-frame');
		await expect(frame.getByText('Waiting for the host application…')).toBeHidden({ timeout: 30_000 });
		await expect(frame.getByRole('alert')).toBeHidden();

		// No Node.js process in or beside the server, checked live while the
		// external consumer is connected.
		await assertNoNodeOk(host, server, ctx);
		ctx?.note('external.sdkVersion', app.sdkVersion);
		ctx?.note('external.source', app.source);
		ctx?.note('external.integrity', app.integrity);
		ctx?.note('external.subpaths', app.subpaths);
		ctx?.note('external.tarball', app.tarball);
		ctx?.note('external.server', server.baseURL);
		// eslint-disable-next-line no-console
		console.log(
			`epic proof 4: sdk ${app.sdkVersion} from ${app.source} (${app.tarball ?? 'registry'}) drove ${server.baseURL} ` +
				`(pack ${PACK_TYPE}, datastore ${store.id}, workflow ${packWorkflow.id}, import ${n8nImported.workflow.id})`
		);
	} finally {
		await server.close();
		await externalHost.close();
		await stub.close();
	}
}
