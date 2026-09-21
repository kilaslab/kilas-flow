// FEAT-5fhj6p: the epic acceptance scenario as an executable suite.
//
// The four proofs of EPIC-m42s3g, in order: (1) a Telegram bot answering
// through an AI agent; (2) the official WAHA chatting template imported,
// activated, webhook-delivered and replying, twice, for two tenants; (3) a
// datastore created, edited, workflow-written, n8n-imported and unreadable by
// a second tenant; (4) an external application with no checkout driving
// workflows and datastores through the packed SDK.
//
// This file is the HERMETIC caller and nothing else: the proof bodies live in
// e2e/fixtures/epic-proofs.ts behind the EpicHost / TelegramSide / WahaSide
// interfaces, and the arrangement that needs no credentials is supplied by
// e2e/fixtures/epic-hermetic.ts (the locally built binary, loopback stubs, a
// local Ollama, a scratch PostgreSQL). The rare, on-demand run against a
// published image, the real Telegram Bot API, a real WAHA server and the npm
// registry lives in e2e/capstone and calls the same proof bodies — one
// implementation, two callers, so the rarely-run one cannot rot.
//
// What each test therefore owns: the gate (its skip string is what
// e2e/skip-budget.json matches, so the text is part of the contract), the
// timeout, and one call into a proof. Everything environment-specific is a
// host or side argument.
//
// Driver matrix: sqlite always; postgres joins when KILASFLOW_TEST_POSTGRES_DSN
// (or KILASFLOW_E2E_POSTGRES_DSN) names a live server; proof 1 joins when the
// pinned Ollama model is servable; proof 2 joins when the gitignored corpus is
// materialised. Every gate skips honestly with its recovery command.
//
// Honest deviations from the ticket (the ticket's own plan expects the stubbed
// suites to keep running often and the real-credential capstone rarely; this
// file is the former kind):
// - No real Telegram Bot API, WAHA server, published image or npm registry.
//   Third parties are loopback stubs reached through the instance's outbound
//   policy (one admitted endpoint; allow_private_networks never set).
// - The SDK installs from a packed tarball (byte-identical to the published
//   bytes) instead of the registry.
// - The no-Node.js criterion is enforced literally as written in the ticket's
//   acceptance box — no Node.js process in or beside the SERVER — via lsof+ps
//   while the proof servers are live, in all four proofs. The SDK consumer and
//   the harness are Node by definition and are out of scope by that wording.
import { test, expect } from '@playwright/test';
import { readFile, stat } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { gate } from '../fixtures/gates';
import { pgDsn, pgSkipReason } from '../fixtures/datastore';
import { startServer } from '../helpers/server';
import { startStub } from '../helpers/stub';
import { loadChattingTemplate } from '../fixtures/waha-migration';
import { OLLAMA_BASE_URL, OLLAMA_MODEL, probeOllama } from '../fixtures/epic-telegram';
import { binaryHost, stubTelegramSide, stubWahaSide } from '../fixtures/epic-hermetic';
import {
	proofDatastoreIsolation,
	proofDatastoreLifecycle,
	proofExternalConsumer,
	proofTelegramAgent,
	proofWahaTwoTenants
} from '../fixtures/epic-proofs';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');

async function api(baseURL: string, method: string, path: string, body?: unknown, wantStatus = 200): Promise<unknown> {
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

test.describe.serial('epic acceptance', () => {
	test('proof 1 — a Telegram bot answers through an AI agent', async () => {
		test.setTimeout(600_000);
		test.slow();
		const probe = await probeOllama();
		test.skip(!probe.ready, gate('model', `epic proof 1: ${probe.reason}`));
		await proofTelegramAgent(binaryHost(), stubTelegramSide(OLLAMA_BASE_URL));
	});

	test('proof 2 — the WAHA chatting template serves two tenants, HMAC-verified', async () => {
		test.setTimeout(300_000);
		const template = await loadChattingTemplate();
		test.skip(
			template === null,
			gate(
				'corpus',
				'run `make corpus` (KILASFLOW_N8N_REFERENCE must point at the read-only n8n checkout) and retry'
			)
		);
		await proofWahaTwoTenants(binaryHost(), stubWahaSide(), template!);
	});

	test('proof 3 — a datastore is created, edited, workflow-written and n8n-imported', async () => {
		test.setTimeout(300_000);
		await proofDatastoreLifecycle(binaryHost(), null);
	});

	test('proof 3 — a second tenant reads nothing', async () => {
		test.setTimeout(300_000);
		await proofDatastoreIsolation(binaryHost(), null);
	});

	test('proof 3 — the datastore path holds on postgres', async () => {
		test.setTimeout(300_000);
		// The same lifecycle and isolation proofs the sqlite tests run, on a
		// Postgres-backed instance instead of the default SQLite file.
		const host = binaryHost();
		const pg = await host.startPostgres();
		test.skip(!pg, pgSkipReason());
		await proofDatastoreLifecycle(host, pg);
		await proofDatastoreIsolation(host, pg);
	});

	test('proof 4 — an external app with no checkout drives KilasFlow through the packed SDK', async ({
		page
	}) => {
		test.setTimeout(600_000);
		test.slow();
		await proofExternalConsumer(binaryHost(), { kind: 'tarball' }, page);
	});

	test('report — versions, catalogue and corpus fidelity', async () => {
		const stub = await startStub();
		const server = await startServer({ stubEndpoint: `127.0.0.1:${stub.port}`, allowedOrigin: stub.origin });
		try {
			const health = (await api(server.baseURL, 'GET', '/health')) as { version?: unknown };
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
