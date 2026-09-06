// FEAT-5fhj6p: the epic acceptance scenario as an executable suite.
//
// The four proofs of EPIC-m42s3g, in order: (1) a Telegram bot answering
// through an AI agent; (2) the official WAHA chatting template imported,
// activated, webhook-delivered and replying, twice, for two tenants; (3) a
// datastore created, edited, workflow-written, n8n-imported and unreadable by
// a second tenant; (4) an external application with no checkout driving
// workflows and datastores through the packed SDK.
//
// Composition, not duplication: proofs 2 and 3 drive the same fixtures as
// the FEAT-gg85se (waha-migration) and FEAT-cpdp8y (datastore) suites — the
// epic file is the acceptance ORDER plus the two missing proofs (Telegram+AI,
// external host). Sibling suites own editor click-throughs, capsule refusals
// and concurrency; this file asserts the end-to-end order and the seams the
// ticket names (webhook registration/removal, HMAC over the raw body,
// cross-tenant refusal, packed-SDK consumption).
//
// Driver matrix: sqlite always; postgres joins when KILASFLOW_TEST_POSTGRES_DSN
// (or KILASFLOW_E2E_POSTGRES_DSN) names a live server; proof 1 joins when the
// pinned Ollama model is servable; proof 2 joins when the gitignored corpus
// is materialised. Every gate skips honestly with its recovery command.
//
// Deviations from FEAT-5fhj6p, stated rather than hidden (the ticket's own
// plan expects the stubbed suites to keep running often and the real-credential
// capstone rarely; this file is the former kind):
// - No real Telegram Bot API, WAHA server, published image or npm registry.
//   Third parties are loopback stubs reached through the instance's outbound
//   policy (one admitted endpoint; allow_private_networks never set).
// - The SDK installs from a packed tarball (byte-identical to the published
//   bytes) instead of the registry.
// - The no-Node.js criterion is enforced literally as written in the ticket's
//   acceptance box — no Node.js process in or beside the SERVER — via lsof+ps
//   while the proof servers are live. The SDK consumer and the harness are
//   Node by definition and are out of scope by that wording.
//
// New files only (this spec plus e2e/fixtures/epic-*); the harness is
// untouched. Observable waits throughout: executions, SSE channels and the
// editor handshake are awaited with assertions, never fixed sleeps.
import { test, expect } from '@playwright/test';
import { createHmac, randomBytes } from 'node:crypto';
import { readFile, stat } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { startServer } from '../helpers/server';
import { startStub } from '../helpers/stub';
import { createCredential, createWorkflow, waitForExecution } from '../helpers/seed';
import {
	activateWorkflow,
	bindWahaCredential,
	completeChattingMigration,
	createStubWahaCredential,
	deliverWebhook,
	fetchDocument,
	importN8nTemplate,
	listExecutions,
	loadChattingTemplate,
	saveDocument,
	wahaPingDelivery,
	waitForNewExecution
} from '../fixtures/waha-migration';
import {
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
	pgDsn,
	pgSkipReason,
	renameDatastore,
	saveWorkflowDocument,
	startPostgresServer,
	startWorkflowRun,
	uniqueName
} from '../fixtures/datastore';
import { PACKS_ENV_KEY, PACK_TYPE, nodepackgenBinary, runNodepackgen, scaffoldPack } from '../fixtures/pack-install';
import {
	EPIC_BOT_TOKEN,
	EPIC_PUBLIC_URL,
	OLLAMA_BASE_URL,
	OLLAMA_MODEL,
	deliverTelegramUpdate,
	epicUpdate,
	itemJsonEpic,
	probeOllama,
	readFullChannelEpic,
	startTelegramStub,
	telegramSecret
} from '../fixtures/epic-telegram';
import {
	assertNoNodeBesideServer,
	scaffoldExternalApp,
	startExternalHost,
	withEnv,
	writeHostPage
} from '../fixtures/epic-external';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
const TERMINAL = new Set(['succeeded', 'failed', 'cancelled']);

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

// A WAHA delivery signed over the exact bytes sent — the raw-body contract
// the trigger's HMAC verifier checks (hex HMAC-SHA512, X-Webhook-Hmac).
async function deliverWahaSigned(
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

test.describe.serial('epic acceptance', () => {
	test('proof 1 — a Telegram bot answers through an AI agent', async () => {
		test.setTimeout(600_000);
		test.slow();
		const probe = await probeOllama();
		test.skip(!probe.ready, `epic proof 1 needs the local model: ${probe.reason}`);
		const tg = await startTelegramStub(OLLAMA_BASE_URL);
		let server: Awaited<ReturnType<typeof startServer>> | undefined;
		try {
			server = await withEnv({ KILASFLOW_SERVER_PUBLIC_URL: EPIC_PUBLIC_URL }, () =>
				startServer({ stubEndpoint: tg.endpoint })
			);
			const baseURL = server.baseURL;

			const botId = (
				await createCredential(baseURL, {
					name: 'Epic Bot',
					type: 'telegramApi',
					fields: { accessToken: EPIC_BOT_TOKEN, baseUrl: tg.origin }
				})
			).id;
			const llmId = (
				await createCredential(baseURL, {
					name: 'Epic Ollama',
					type: 'httpBearerAuth',
					fields: { token: 'local' }
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
						parameters: { model: OLLAMA_MODEL, baseUrl: tg.modelBaseURL, temperature: 0, stream: true },
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

			// Activation registers the trigger's own webhook: existence is
			// checked first (getWebhookInfo), then setWebhook carries the
			// minted route. No notices — registration was attempted, not
			// deferred to the user.
			const activated = await api(baseURL, 'POST', `/workflows/${workflow.id}/activate`, {});
			expect(activated.active).toBe(true);
			expect(activated.notices ?? []).toHaveLength(0);
			expect(tg.getWebhookInfoCalls).toBeGreaterThanOrEqual(1);
			expect(tg.setWebhookBodies).toHaveLength(1);
			const setBody = JSON.parse(tg.setWebhookBodies[0]) as {
				url: string;
				secret_token: string;
				allowed_updates?: string[];
			};
			expect(setBody.url).toMatch(/^https:\/\/epic-external\.invalid\/webhook\/[0-9a-f]{32}$/);
			expect(setBody.allowed_updates ?? []).toContain('message');
			const route = new URL(setBody.url).pathname;
			// The secret the service was told matches the verifier's
			// derivation from the same token and route — the fragile half.
			const secret = telegramSecret(EPIC_BOT_TOKEN, route.split('/').pop() ?? '');
			expect(setBody.secret_token).toBe(secret);

			// A delivery with the wrong secret is refused before an execution
			// exists; one with none at all is refused too.
			const knownBefore = await listExecutions(baseURL, workflow.id);
			const denied = await deliverTelegramUpdate(baseURL, route, epicUpdate('hello?'), 'wrong-secret');
			expect(denied.status).toBe(401);
			const unsigned = await deliverTelegramUpdate(baseURL, route, epicUpdate('hello?'));
			expect(unsigned.status).toBe(401);
			expect((await listExecutions(baseURL, workflow.id)).map((item) => item.id)).toEqual(
				knownBefore.map((item) => item.id)
			);

		// A real message is delivered, the agent answers, and the reply
		// arrives at the Bot API — observed at the stub, not asserted by
		// existence.
		const update = epicUpdate('What is the capital of France? Answer in ten words or fewer.');
		expect((await deliverTelegramUpdate(baseURL, route, update, secret)).status).toBeLessThan(300);
		const { record } = await waitForNewExecution(
			baseURL,
			workflow.id,
			knownBefore.map((item) => item.id)
		);
		expect(record.status).toBe('succeeded');
		const agentOutput = itemJsonEpic(record, 'agent');
		expect(typeof agentOutput.output === 'string' && agentOutput.output.trim().length > 0).toBe(true);

			// The same loop observed live: the model streamed and the run
			// completed on one channel. Structural assertions only — never
			// the words the model chose.
			const events = await readFullChannelEpic(baseURL, record.id);
			const types = events.map((event) => event.type);
			expect(types).toContain('ai.model.delta');
			expect(types).toContain('execution.completed');

			expect(tg.sentMessages).toHaveLength(1);
			expect(String(tg.sentMessages[0].chatId)).toContain('774411');
			expect(typeof tg.sentMessages[0].text === 'string' && (tg.sentMessages[0].text as string).trim().length > 0).toBe(true);
			expect(tg.sentMessages[0].text).toBe(agentOutput.output);

			// No Node.js process in or beside the server, checked live.
			const verdict = await assertNoNodeBesideServer(server.port);
			expect(verdict.checked, verdict.reason).toBe(true);
			expect(verdict.serverComm).toContain('kilasflow');
			expect(verdict.nodeChildren).toHaveLength(0);

			// Deactivation removes the webhook, and the route goes dark.
			await api(baseURL, 'POST', `/workflows/${workflow.id}/deactivate`, {});
			expect(tg.deleteWebhookCalls).toBe(1);
			const afterOff = await deliverTelegramUpdate(baseURL, route, update, secret);
			expect(afterOff.status).toBe(404);
		} finally {
			await server?.close();
			await tg.close();
		}
	});

	test('proof 2 — the WAHA chatting template serves two tenants, HMAC-verified', async () => {
		test.setTimeout(300_000);
		const template = await loadChattingTemplate();
		test.skip(
			template === null,
			'WAHA corpus absent: run `make corpus` (KILASFLOW_N8N_REFERENCE must point at the read-only n8n checkout) and retry'
		);

		const stub = await startStub();
		const server = await startServer({ stubEndpoint: `127.0.0.1:${stub.port}`, allowedOrigin: stub.origin });
		const tenantB = await startServer({ stubEndpoint: `127.0.0.1:${stub.port}`, allowedOrigin: stub.origin });
		try {
			const tenants = [
				{ baseURL: server.baseURL, label: 'client A', secret: randomBytes(24).toString('hex') },
				{ baseURL: tenantB.baseURL, label: 'client B', secret: randomBytes(24).toString('hex') }
			] as const;
			const imported: Array<{ workflow: { id: string }; url: string }> = [];
			for (const tenant of tenants) {
				const result = await importN8nTemplate(tenant.baseURL, template!, `WAHA Chatting (epic ${tenant.label})`);
				expect(result.workflow.id.length).toBeGreaterThan(0);
				expect(result.webhooks).toHaveLength(1);
				expect(result.webhooks[0].url).toMatch(/^\/webhook\/[0-9a-f]{32}$/);
				const credentialId = await createStubWahaCredential(tenant.baseURL, `WAHA stub (${tenant.label})`, stub.origin);
				const document = await fetchDocument(tenant.baseURL, result.workflow.id);
				bindWahaCredential(document, credentialId);
				completeChattingMigration(document, stub.url('/waha-image.png'));
				const trigger = (document.nodes as any[]).find((node) => node.type === 'pack.wahaTrigger');
				expect(trigger, 'the imported workflow carries a WAHA trigger').toBeDefined();
				trigger.parameters = { ...(trigger.parameters ?? {}), hmacSecret: tenant.secret };
				await saveDocument(tenant.baseURL, result.workflow.id, document);
				await activateWorkflow(tenant.baseURL, result.workflow.id);
				imported.push({ workflow: result.workflow, url: result.webhooks[0].url });
			}
			expect(imported[0].url).not.toBe(imported[1].url);

			// A route minted on one tenant means nothing on the other.
			expect(await deliverWebhook(tenantB.baseURL, imported[0].url, wahaPingDelivery())).toBe(404);

			// HMAC over the raw body: a wrong signature is refused before an
			// execution exists; the right one runs to a reply.
			const [first, second] = tenants;
			const knownFirst = await listExecutions(first.baseURL, imported[0].workflow.id);
			const forged = await deliverWahaSigned(first.baseURL, imported[0].url, wahaPingDelivery(), 'not-the-secret');
			expect(forged).toBe(401);
			expect((await listExecutions(first.baseURL, imported[0].workflow.id)).map((item) => item.id)).toEqual(
				knownFirst.map((item) => item.id)
			);

			expect(await deliverWahaSigned(first.baseURL, imported[0].url, wahaPingDelivery(), first.secret)).toBeLessThan(300);
			const one = await waitForNewExecution(
				first.baseURL,
				imported[0].workflow.id,
				knownFirst.map((item) => item.id)
			);
			expect(one.record.status).toBe('succeeded');
			expect(one.record.workflowId).toBe(imported[0].workflow.id);
			// Client A's delivery reached only client A's execution.
			expect(await listExecutions(second.baseURL, imported[1].workflow.id)).toHaveLength(0);

			const knownSecond = await listExecutions(second.baseURL, imported[1].workflow.id);
			expect(await deliverWahaSigned(second.baseURL, imported[1].url, wahaPingDelivery('other'), second.secret)).toBeLessThan(
				300
			);
			const two = await waitForNewExecution(
				second.baseURL,
				imported[1].workflow.id,
				knownSecond.map((item) => item.id)
			);
			expect(two.record.status).toBe('succeeded');
			expect(two.record.workflowId).toBe(imported[1].workflow.id);

			// Both tenants' replies passed through the WAHA endpoint: each
			// workflow answered "pong" through Send Text.
			const replies = stub.requests.filter((request) => request.path === '/api/sendText');
			expect(replies.length).toBeGreaterThanOrEqual(2);
			expect(replies.some((request) => request.body.includes('pong'))).toBe(true);
		} finally {
			await tenantB.close();
			await server.close();
			await stub.close();
		}
	});

	test('proof 3 — a datastore is created, edited, workflow-written and n8n-imported', async () => {
		test.setTimeout(300_000);
		const stub = await startStub();
		const server = await startServer({ stubEndpoint: `127.0.0.1:${stub.port}`, allowedOrigin: stub.origin });
		try {
			// Created through the name-only dialog shape, then edited: two
			// user columns and a rename.
			const store = await createDatastore(server.baseURL, uniqueName('Epic Store'));
			await addColumn(server.baseURL, store.id, 'email', 'string');
			await addColumn(server.baseURL, store.id, 'score', 'number');
			const renamed = await renameDatastore(server.baseURL, store.id, `${store.name} (edited)`);
			expect(renamed.name).toContain('(edited)');
			const definition = await getDatastore(server.baseURL, store.id);
			expect((definition.columns as Array<{ name: string }>).map((column) => column.name)).toEqual(
				expect.arrayContaining(['email', 'score'])
			);

			// Written by a workflow node and read back with a filter — the
			// filtered read is proven by the execution record, not the API.
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
			expect(
				(stored.items as Array<{ email: string }>).some((row) => row.email === 'epic@example.com')
			).toBe(true);

			// CSV round trip on its own table: an n8n-style export imports
			// and re-exports byte-identical.
			const csvStore = await createDatastore(server.baseURL, uniqueName('Epic CSV'));
			await addColumn(server.baseURL, csvStore.id, 'email', 'string');
			await addColumn(server.baseURL, csvStore.id, 'score', 'number');
			await importCsv(server.baseURL, csvStore.id, n8nStyleCsv());
			expect(await exportCsv(server.baseURL, csvStore.id)).toBe(n8nStyleCsv());

			// The same workflow imported from an n8n Data Table export binds
			// to the datastore and runs; the unbound twin refuses.
			const n8nImported = await importN8nWorkflow(server.baseURL, n8nDataTableExport(), uniqueName('Epic N8N Table'));
			const blocking = n8nImported.unsupported.filter((issue) => issue.severity === 'blocking');
			expect(blocking.some((issue) => (issue.reason ?? '').includes('dt_metrics_01'))).toBe(true);
			const n8nDocument = await fetchWorkflowDocument(server.baseURL, n8nImported.workflow.id);
			const table = (n8nDocument.nodes as any[]).find((node) => node.name === 'Table');
			expect(table?.type).toBe('kilasflow.datastore');
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
		} finally {
			await server.close();
			await stub.close();
		}
	});

	test('proof 3 — a second tenant reads nothing', async () => {
		test.setTimeout(300_000);
		const stub = await startStub();
		const server = await startServer({ stubEndpoint: `127.0.0.1:${stub.port}`, allowedOrigin: stub.origin });
		// Tenant B is a second instance with its own database: the strongest
		// isolation the harness offers (auth off means each instance is its
		// own default tenant). Same-DB multi-tenancy is pinned by the Go
		// fixedTenant suites; this proves the cross-instance surface.
		const tenantB = await startServer({ stubEndpoint: `127.0.0.1:${stub.port}`, allowedOrigin: stub.origin });
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
		} finally {
			await tenantB.close();
			await server.close();
			await stub.close();
		}
	});

	test('proof 3 — the datastore path holds on postgres', async () => {
		test.setTimeout(300_000);
		const dsn = pgDsn();
		test.skip(!dsn, pgSkipReason());
		const stub = await startStub();
		const tenantA = await startPostgresServer(dsn!, {
			stubEndpoint: `127.0.0.1:${stub.port}`,
			allowedOrigin: stub.origin
		});
		const tenantB = await startServer({ stubEndpoint: `127.0.0.1:${stub.port}`, allowedOrigin: stub.origin });
		try {
			const store = await createDatastore(tenantA.baseURL, uniqueName('Epic PG Store'));
			await addColumn(tenantA.baseURL, store.id, 'email', 'string');
			const writer = await createWorkflowDocument(
				tenantA.baseURL,
				datastoreWriteReadDocument(uniqueName('Epic PG Writer'), store.id, { email: 'pg@example.com' }, 'email', 'pg@example.com')
			);
			const started = await startWorkflowRun(tenantA.baseURL, writer.id);
			expect(started.status).toBe(202);
			expect((await waitForExecution(tenantA.baseURL, started.body.id)).status).toBe('succeeded');
			expect(
				((await listRows(tenantA.baseURL, store.id)).items as Array<{ email: string }>).some(
					(row) => row.email === 'pg@example.com'
				)
			).toBe(true);

			// Tenant B is a separate sqlite instance: the postgres victim id
			// reads as unknown there, same indistinguishability contract.
			const victimOnB = await fetch(`${tenantB.baseURL}/api/v1/datastores/${store.id}`);
			expect(victimOnB.status).toBe(404);
			expect(await victimOnB.text()).toContain('not found');
		} finally {
			await tenantB.close();
			await tenantA.close();
			await stub.close();
		}
	});

	test('proof 4 — an external app with no checkout drives KilasFlow through the packed SDK', async ({
		page
	}) => {
		test.setTimeout(600_000);
		test.slow();
		// The external application: a scratch project outside the repository
		// with the SDK installed from a packed tarball — the operator's own
		// `npm install`, run for real.
		const app = await scaffoldExternalApp();
		await writeHostPage(app.dir);
		expect(app.dir.startsWith(`${repoRoot}/`)).toBe(false);
		expect((await stat(app.tarball)).isFile()).toBe(true);

		// A community pack installed by placing it in a directory — authored
		// with the operator tooling into the external app's own packs dir.
		const nodepackgen = await nodepackgenBinary();
		const packsDir = join(app.dir, 'packs', 'hello');
		await scaffoldPack(nodepackgen, join(app.dir, 'packs'), 'hello', PACK_TYPE);
		expect((await runNodepackgen(nodepackgen, ['validate', packsDir])).stderr).toContain('packs are valid');
		expect((await runNodepackgen(nodepackgen, ['pack', '-dir', packsDir])).stderr).toContain('pack.sha256');

		const stub = await startStub();
		const host = await startExternalHost(app.dir);
		const server = await withEnv({ [PACKS_ENV_KEY]: join(app.dir, 'packs') }, () =>
			startServer({ stubEndpoint: `127.0.0.1:${stub.port}`, allowedOrigin: host.origin })
		);
		try {
			const client = new app.KilasFlowClient({ baseUrl: server.baseURL });
			expect((await client.getReady()) != null).toBe(true);

			// The directory-installed pack is visible to the external
			// consumer, tagged with its source.
			const catalogue = (await client.listNodeTypes()) as Array<{ type: string; source: string }>;
			const installedPack = catalogue.find((entry) => entry.type === PACK_TYPE);
			expect(installedPack, 'the directory-installed pack is listed').toBeDefined();
			expect(installedPack?.source).toBe('pack');

			// A workflow through the SDK runs a pack node against the stub.
			const packCredential = await client.createCredential({
				name: 'External Pack WAHA',
				type: 'wahaApi',
				fields: { baseUrl: stub.origin, apiKey: 'e2e-key' }
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
			const packRun = (await client.runWorkflow(packWorkflow.id)) as { id: string };
			await expect
				.poll(async () => TERMINAL.has((await client.getExecution(packRun.id)).status), {
					message: 'the pack workflow reaches a terminal status',
					timeout: 120_000
				})
				.toBe(true);
			expect((await client.getExecution(packRun.id)).status).toBe('succeeded');
			const packCall = stub.requests.find((request) => request.method === 'POST' && request.path === '/sendMessage');
			expect(packCall, 'the pack node reached the stub').toBeDefined();
			expect(JSON.parse(packCall?.body ?? '{}')).toMatchObject({ chatId: 'external-chat', text: 'hello packs' });

			// A datastore through the SDK: create with columns, edit with
			// another, write, filtered update, required-filter delete, and a
			// cursor walk to exhaustion.
			const store = await client.createDatastore({
				name: `External Store ${randomBytes(4).toString('hex')}`,
				columns: [{ name: 'email', type: 'string' }]
			});
			await client.addDatastoreColumn(store.id, { name: 'score', type: 'number' });
			await client.insertDatastoreRow(store.id, { email: 'host@example.com', score: 8 });
			await client.insertDatastoreRow(store.id, { email: 'other@example.com', score: 3 });
			const filter = app.datastoreFilter('and', [{ columnName: 'email', condition: 'eq', value: 'host@example.com' }]);
			await client.updateDatastoreRows(store.id, filter as never, { score: 10 });
			const seen: Array<{ email?: unknown; score?: unknown }> = [];
			for await (const row of client.iterateDatastoreRows(store.id)) seen.push(row as { email?: unknown });
			expect(seen.length).toBe(2);
			expect(seen.find((row) => row.email === 'host@example.com')?.score).toBe(10);
			// The required-filter rule is enforced client-side: an empty
			// filter never reaches the network.
			await expect(client.deleteDatastoreRows(store.id, { type: 'and', filters: [] } as never)).rejects.toThrow();
			await client.deleteDatastoreRows(store.id, filter as never);
			const remaining: unknown[] = [];
			for await (const row of client.iterateDatastoreRows(store.id)) remaining.push(row);
			expect(remaining.length).toBe(1);

			// Import, the way the reference host provisions: the only call
			// reporting the minted webhook address back.
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

			// The embedded editor mounts in the external host's own page with
			// an SDK-minted session — the reference-host loop, end to end.
			const session = await client.createEmbedSession({
				workflowId: packWorkflow.id,
				origin: host.origin,
				scopes: ['workflow:read', 'workflow:write', 'workflow:run']
			});
			const hostURL =
				`${host.url('/host.html')}?server=${encodeURIComponent(server.baseURL)}` +
				`&workflow=${encodeURIComponent(packWorkflow.id)}` +
				`&token=${encodeURIComponent(session.token)}` +
				`&scopes=${encodeURIComponent(session.scopes.join(','))}`;
			await page.goto(hostURL);
			await expect
				.poll(
					async () =>
						page.evaluate(
							() => (window as unknown as { __externalEvents: { type: string }[] }).__externalEvents.filter(
								(event) => event.type === 'kilasflow:embed-ready'
							).length
						),
					{ message: 'the embed frame announces readiness in the external page' }
				)
				.toBeGreaterThan(0);
			const frame = page.frameLocator('#external-frame');
			await expect(frame.getByText('Waiting for the host application…')).toBeHidden({ timeout: 30_000 });
			await expect(frame.getByRole('alert')).toBeHidden();

			// No Node.js process in or beside the server, checked live while
			// the external consumer is connected.
			const verdict = await assertNoNodeBesideServer(server.port);
			expect(verdict.checked, verdict.reason).toBe(true);
			expect(verdict.serverComm).toContain('kilasflow');
			expect(verdict.nodeChildren).toHaveLength(0);

			// Evidence for the ticket: what the external app consumed.
			// eslint-disable-next-line no-console
			console.log(
				`epic proof 4: sdk ${app.sdkVersion} from ${app.tarball} drove ${server.baseURL} ` +
					`(pack ${PACK_TYPE}, datastore ${store.id}, workflow ${packWorkflow.id}, import ${n8nImported.workflow.id})`
			);
		} finally {
			await server.close();
			await host.close();
			await stub.close();
		}
	});

	test('report — versions, catalogue and corpus fidelity', async () => {
		const stub = await startStub();
		const server = await startServer({ stubEndpoint: `127.0.0.1:${stub.port}`, allowedOrigin: stub.origin });
		try {
			const health = await api(server.baseURL, 'GET', '/health');
			expect(typeof health.version === 'string' && health.version.length > 0).toBe(true);
			const catalogue = (await api(server.baseURL, 'GET', '/node-types')) as Array<{
				type: string;
				version: unknown;
				source: string;
			}>;
			expect(catalogue.length).toBeGreaterThan(0);
			const bySource = new Map<string, number>();
			for (const entry of catalogue) bySource.set(entry.source, (bySource.get(entry.source) ?? 0) + 1);
			// The acceptance surface the scenario touches must be registered.
			for (const required of ['kilasflow.telegramTrigger', 'pack.telegram', 'pack.wahaTrigger', 'kilasflow.agent', 'kilasflow.datastore']) {
				expect(catalogue.some((entry) => entry.type === required), `${required} is registered`).toBe(true);
			}
			const sdkPackage = JSON.parse(await readFile(join(repoRoot, 'sdk', 'package.json'), 'utf-8')) as {
				version: string;
			};
			expect(sdkPackage.version).toMatch(/^\d+\.\d+\.\d+/);
			const corpusPresent = await stat(join(repoRoot, '.corpus')).then(
				() => true,
				() => false
			);
			// eslint-disable-next-line no-console
			console.log(
				`epic report: server ${health.version}, sdk ${sdkPackage.version}, ` +
					`node-types ${catalogue.length} (${[...bySource.entries()].map(([source, count]) => `${source}:${count}`).join(' ')}), ` +
					`corpus ${corpusPresent ? 'present' : 'absent'}, postgres ${pgDsn() ? 'present' : 'absent'}, ` +
					`ollama model ${(await probeOllama()).ready ? OLLAMA_MODEL : 'absent'}`
			);
		} finally {
			await server.close();
			await stub.close();
		}
	});
});
