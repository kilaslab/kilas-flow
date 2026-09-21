// FEAT-5fhj6p: the hermetic callers — the locally built binary as the host,
// and the loopback stubs as the third-party sides.
//
// This is what keeps e2e/tests/epic-acceptance.spec.ts on the per-PR path: the
// proofs in epic-proofs.ts know nothing about where KilasFlow runs or who
// answers on the other side, and this file supplies the arrangement that needs
// no credentials, no docker and no internet. The on-demand capstone supplies
// the other one (a published image, the real Bot API, a real WAHA server).
import { readFile } from 'node:fs/promises';

import { expect } from '@playwright/test';
import { startServer } from '../helpers/server';
import { startStub as startLoopbackStub, type StubServer } from '../helpers/stub';
import { pgDsn, startPostgresServer } from './datastore';
import { PACKS_ENV_KEY, nodepackgenBinary, runNodepackgen } from './pack-install';
import { assertNoNodeBesideServer, withEnv } from './epic-external';
import {
	EPIC_BOT_TOKEN,
	EPIC_PUBLIC_URL,
	OLLAMA_MODEL,
	deliverTelegramUpdate,
	epicUpdate,
	startTelegramStub,
	telegramSecret,
	type TelegramStub
} from './epic-telegram';
import {
	deliverWahaSigned,
	type EpicHost,
	type EpicServer,
	type HostStartOptions,
	type NoNodeVerdict,
	type PostgresHandle,
	type TelegramSide,
	type WahaSide
} from './epic-proofs';
import { wahaPingDelivery, type WorkflowNode } from './waha-migration';

// The locally built binary (global-setup runs `make build-all`) as the host.
// Everything a proof passes in a HostStartOptions maps onto the harness's own
// knobs: privateEndpoints and allowedOrigins are comma lists in the config, the
// public URL and packs directory are environment overrides around startServer,
// and a Postgres-backed server is the same binary with a different DSN.
export function binaryHost(): EpicHost {
	return {
		kind: 'binary',
		label: 'binary',
		async start(o: HostStartOptions = {}): Promise<EpicServer> {
			const env: Record<string, string> = {};
			if (o.publicUrl) env.KILASFLOW_SERVER_PUBLIC_URL = o.publicUrl;
			if (o.packsDir) env[PACKS_ENV_KEY] = o.packsDir;
			const stubEndpoint = o.privateEndpoints?.join(',');
			const allowedOrigin = o.allowedOrigins?.join(',');
			const server = await withEnv(env, async () => {
				if (!o.postgres) return startServer({ stubEndpoint, allowedOrigin });
				const dsn = pgDsn();
				if (!dsn) throw new Error('a postgres handle was supplied but no DSN is configured');
				return startPostgresServer(dsn, { stubEndpoint, allowedOrigin });
			});
			return {
				baseURL: server.baseURL,
				port: server.port,
				close: () => server.close(),
				logs: () => readFile(server.logPath, 'utf-8')
			};
		},
		async startPostgres(): Promise<PostgresHandle | null> {
			// Nothing to boot: the DSN names a server the operator already runs,
			// and the server under test is started against it by start().
			return pgDsn() ? { label: 'postgres (binary host)', close: async () => {} } : null;
		},
		async startStub() {
			const stub = await startLoopbackStub();
			return {
				...stub,
				reachableOrigin: stub.origin,
				reachableEndpoint: `127.0.0.1:${stub.port}`
			};
		},
		async assertNoNode(server: EpicServer): Promise<NoNodeVerdict> {
			return assertNoNodeBesideServer(server.port);
		},
		async nodepackgen(packsRoot: string, args: string[]) {
			// The Go toolchain, never a Node process serving anything; {root}
			// lets the caller name paths inside the packs directory without
			// knowing where this host put it.
			const binary = await nodepackgenBinary();
			return runNodepackgen(
				binary,
				args.map((arg) => arg.replaceAll('{root}', packsRoot))
			);
		}
	};
}

// The Telegram Bot API stub plus the byte-transparent Ollama proxy, as the
// Telegram side. One loopback endpoint covers the bot and the model together.
export function stubTelegramSide(ollamaBaseUrl: string): TelegramSide {
	let stub: TelegramStub | undefined;
	const current = (): TelegramStub => {
		if (!stub) throw new Error('the telegram stub was used before prepare()');
		return stub;
	};
	return {
		kind: 'stub',
		async prepare(): Promise<HostStartOptions> {
			stub = await startTelegramStub(ollamaBaseUrl);
			return { privateEndpoints: [stub.endpoint], publicUrl: EPIC_PUBLIC_URL };
		},
		botCredential: () => ({ accessToken: EPIC_BOT_TOKEN, baseUrl: current().origin }),
		model: () => ({ token: 'local', baseUrl: current().modelBaseURL, model: OLLAMA_MODEL }),
		async afterActivate(route: string): Promise<void> {
			// The registration the service made, asserted at the stub: the
			// existence probe ran first, then exactly one setWebhook carried the
			// public URL, the message update filter and the derived secret.
			const tg = current();
			expect(tg.getWebhookInfoCalls).toBeGreaterThanOrEqual(1);
			expect(tg.setWebhookBodies).toHaveLength(1);
			const setBody = JSON.parse(tg.setWebhookBodies[0]) as {
				url: string;
				secret_token: string;
				allowed_updates?: string[];
			};
			expect(setBody.url).toBe(`${EPIC_PUBLIC_URL}${route}`);
			expect(setBody.allowed_updates ?? []).toContain('message');
			expect(setBody.secret_token).toBe(telegramSecret(EPIC_BOT_TOKEN, route.split('/').pop() ?? ''));
		},
		async inbound(server: EpicServer, route: string, text: string): Promise<{ secret?: string; via: string }> {
			const secret = telegramSecret(EPIC_BOT_TOKEN, route.split('/').pop() ?? '');
			const delivered = await deliverTelegramUpdate(server.baseURL, route, epicUpdate(text), secret);
			expect(delivered.status).toBeLessThan(300);
			return { secret, via: 'stub Bot API update to the minted route' };
		},
		async afterDeactivate(_route: string): Promise<void> {
			const tg = current();
			expect(tg.deleteWebhookCalls).toBe(1);
			// The side saw exactly one send: the reply, delivered by the agent.
			expect(tg.sentMessages).toHaveLength(1);
		},
		async close(): Promise<void> {
			await stub?.close();
		}
	};
}

// The shared loopback stub as the WAHA side: the pack builds every request URL
// from the credential's baseUrl, so the stub stands in for a real WAHA server
// and a signed delivery is forged at its own loopback route. A real WAHA server
// is the capstone's arrangement; this proves the path on every PR.
export function stubWahaSide(): WahaSide {
	let stub: StubServer | undefined;
	const current = (): StubServer => {
		if (!stub) throw new Error('the WAHA stub was used before prepare()');
		return stub;
	};
	return {
		kind: 'stub',
		async prepare(): Promise<HostStartOptions> {
			stub ??= await startLoopbackStub();
			return { privateEndpoints: [`127.0.0.1:${stub.port}`], allowedOrigins: [stub.origin] };
		},
		credentialFields: () => ({ baseUrl: current().origin, apiKey: 'e2e-stub-key' }),
		imageUrl: () => current().url('/waha-image.png'),
		configureTrigger(trigger: WorkflowNode, _tenantIndex: number, secret: string): void {
			trigger.parameters = { ...(trigger.parameters ?? {}), hmacSecret: secret };
		},
		publicBase(_tenantIndex: number, server: EpicServer): string {
			// Forged locally: the trick a real WAHA server cannot be asked for.
			return server.baseURL;
		},
		async afterActivate(): Promise<void> {
			// A stub needs no session registration; the signed delivery below is
			// the whole contract the trigger checks.
		},
		async provoke(servers: EpicServer[], routes: string[], secrets: string[]): Promise<void> {
			for (const [index, server] of servers.entries()) {
				const session = index === 0 ? 'default' : 'other';
				const status = await deliverWahaSigned(
					server.baseURL,
					routes[index],
					wahaPingDelivery(session),
					secrets[index]
				);
				expect(status, `tenant ${index + 1} accepted its signed delivery`).toBeLessThan(300);
			}
		},
		async awaitReplies(): Promise<void> {
			// Both tenants' replies passed through the WAHA endpoint: each
			// workflow answered "pong" through Send Text.
			const replies = current().requests.filter((request) => request.path === '/api/sendText');
			expect(replies.length).toBeGreaterThanOrEqual(2);
			expect(replies.some((request) => request.body.includes('pong'))).toBe(true);
		},
		async afterDeactivate(): Promise<void> {
			// Nothing registered, nothing to remove.
		},
		async close(): Promise<void> {
			await stub?.close();
		}
	};
}
