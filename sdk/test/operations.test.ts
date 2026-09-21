import { describe, expect, it, vi } from 'vitest';

import { KilasFlowClient } from '../src/server.js';

/** Records what the SDK sent so a test can assert on the request, not a mock. */
function recordingFetch(response: { status?: number; body?: unknown; headers?: Record<string, string> } = {}) {
	const calls: Array<{ url: string; init: RequestInit }> = [];
	const fetchImpl = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
		calls.push({ url: String(url), init: init ?? {} });
		const status = response.status ?? 200;
		const headers = new Headers({ 'Content-Type': 'application/json', ...(response.headers ?? {}) });
		if (status === 204) return new Response(null, { status: 204, headers });
		return new Response(JSON.stringify(response.body ?? {}), { status, headers });
	}) as unknown as typeof globalThis.fetch;
	return { calls, fetchImpl };
}

function client(fetchImpl: typeof globalThis.fetch) {
	return new KilasFlowClient({ baseUrl: 'https://flows.example', fetch: fetchImpl });
}

function pathOf(call: { url: string }): string {
	const url = new URL(call.url);
	return `${url.pathname}${url.search}`;
}

describe('workflow history', () => {
	it('targets the documented endpoints', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		const sdk = client(fetchImpl);

		await sdk.listWorkflowVersions('wf-1', { limit: 10, cursor: 'abc' });
		await sdk.publishWorkflowVersion('wf-1', 'wfv-1', 'ship it');
		await sdk.restoreWorkflowVersion('wf-1', 'wfv-1');
		await sdk.listWorkflowPublishEvents('wf-1');

		expect(calls.map((call) => `${call.init.method} ${pathOf(call)}`)).toEqual([
			'GET /api/v1/workflows/wf-1/versions?limit=10&cursor=abc',
			'POST /api/v1/workflows/wf-1/versions/wfv-1/publish',
			'POST /api/v1/workflows/wf-1/versions/wfv-1/restore',
			'GET /api/v1/workflows/wf-1/publish-events'
		]);
	});

	it('sends no body when a publish has no audit reason', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		await client(fetchImpl).publishWorkflowVersion('wf-1', 'wfv-1');

		expect(calls[0]!.init.body).toBeUndefined();
		expect(new Headers(calls[0]!.init.headers).get('Content-Type')).toBeNull();
	});

	it('sends the reason when one is given', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		await client(fetchImpl).restoreWorkflowVersion('wf-1', 'wfv-1', 'bad deploy');

		expect(JSON.parse(String(calls[0]!.init.body))).toEqual({ reason: 'bad deploy' });
	});
});

describe('credentials', () => {
	it('targets the documented endpoints', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		const sdk = client(fetchImpl);
		const body = { name: 'prod', type: 'api-key', fields: { token: 'secret' } };

		await sdk.listCredentialTypes();
		await sdk.createCredential(body);
		await sdk.getCredential('cred-1');
		await sdk.updateCredential('cred-1', body);
		await sdk.testCredential('cred-1');
		await sdk.testCredentialPayload('api-key', { fields: { token: 'candidate' } });

		expect(calls.map((call) => `${call.init.method} ${pathOf(call)}`)).toEqual([
			'GET /api/v1/credential-types',
			'POST /api/v1/credentials',
			'GET /api/v1/credentials/cred-1',
			'PUT /api/v1/credentials/cred-1',
			'POST /api/v1/credentials/cred-1/test',
			'POST /api/v1/credential-types/api-key/test'
		]);
	});

	it('returns nothing for a credential delete', async () => {
		const { fetchImpl } = recordingFetch({ status: 204 });
		await expect(client(fetchImpl).deleteCredential('cred-1')).resolves.toBeUndefined();
	});

	it('returns the typed probe result rather than an opaque body', async () => {
		const probe = { ok: false, detail: 'connection refused' };
		const { fetchImpl } = recordingFetch({ body: probe });
		await expect(client(fetchImpl).testCredential('cred-1')).resolves.toEqual(probe);
	});
});

describe('authentication and keys', () => {
	it('targets the documented endpoints', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		const sdk = client(fetchImpl);

		await sdk.login({ email: 'owner@example.com', password: 's3cret' });
		await sdk.getMe();
		await sdk.listApiKeys();
		await sdk.createApiKey('ci');
		await sdk.revokeApiKey('key-1');
		await sdk.createStreamTicket('exec-1');

		expect(calls.map((call) => `${call.init.method} ${pathOf(call)}`)).toEqual([
			'POST /api/v1/auth/login',
			'GET /api/v1/auth/me',
			'GET /api/v1/api-keys',
			'POST /api/v1/api-keys',
			'DELETE /api/v1/api-keys/key-1',
			'POST /api/v1/stream-tickets'
		]);
		expect(JSON.parse(String(calls[0]!.init.body))).toEqual({
			email: 'owner@example.com',
			password: 's3cret'
		});
		expect(JSON.parse(String(calls[3]!.init.body))).toEqual({ label: 'ci' });
		expect(JSON.parse(String(calls[5]!.init.body))).toEqual({ executionId: 'exec-1' });
	});

	it('returns nothing for a logout', async () => {
		const { fetchImpl } = recordingFetch({ status: 204 });
		await expect(client(fetchImpl).logout()).resolves.toBeUndefined();
	});
});

describe('schedules', () => {
	it('completes the surface listSchedules starts', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		const sdk = client(fetchImpl);
		const schedule = { workflowId: 'wf-1', cron: '*/5 * * * *', active: true };

		await sdk.createSchedule(schedule);
		await sdk.updateSchedule('sched-1', schedule);
		await sdk.deleteSchedule('sched-1');

		expect(calls.map((call) => `${call.init.method} ${pathOf(call)}`)).toEqual([
			'POST /api/v1/schedules',
			'PUT /api/v1/schedules/sched-1',
			'DELETE /api/v1/schedules/sched-1'
		]);
	});

	it('returns nothing for a schedule delete', async () => {
		const { fetchImpl } = recordingFetch({ status: 204 });
		await expect(client(fetchImpl).deleteSchedule('sched-1')).resolves.toBeUndefined();
	});
});

describe('node catalogue', () => {
	it('targets the documented endpoints', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		const sdk = client(fetchImpl);
		const input = { property: 'table', parameters: { database: 'app' } };

		await sdk.listNodeTypes();
		await sdk.loadNodePropertyOptions('postgres', input);
		await sdk.loadNodePropertySchema('postgres', input);
		await sdk.getExpressionGrammar();

		expect(calls.map((call) => `${call.init.method} ${pathOf(call)}`)).toEqual([
			'GET /api/v1/node-types',
			'POST /api/v1/node-types/postgres/load-options',
			'POST /api/v1/node-types/postgres/load-schema',
			'GET /api/v1/expression-grammar'
		]);
		expect(JSON.parse(String(calls[1]!.init.body))).toEqual(input);
	});

	it('builds icon URLs without touching the network', () => {
		const { fetchImpl } = recordingFetch();
		const sdk = client(fetchImpl);

		expect(sdk.nodeIconUrl('postgres')).toBe('https://flows.example/api/v1/node-types/postgres/icon');
		expect(sdk.nodeIconUrl('postgres', { theme: 'dark', version: '1' })).toBe(
			'https://flows.example/api/v1/node-types/postgres/icon?version=1&theme=dark'
		);
		// A type name is a path segment, not a path.
		expect(sdk.nodeIconUrl('../../etc')).toBe('https://flows.example/api/v1/node-types/..%2F..%2Fetc/icon');
	});
});

describe('interop', () => {
	it('passes the foreign document through untouched inside a typed envelope', async () => {
		const n8n = { nodes: [{ name: 'Start' }], connections: {} };
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		await client(fetchImpl).importWorkflow({ workflow: n8n, name: 'Migrated' });

		const [call] = calls;
		expect(`${call!.init.method} ${new URL(call!.url).pathname}`).toBe('POST /api/v1/workflows/import');
		expect(JSON.parse(String(call!.init.body))).toEqual({ workflow: n8n, name: 'Migrated' });
	});

	it('exports as n8n JSON', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		await client(fetchImpl).exportWorkflow('wf-1');

		expect(calls.map((call) => `${call.init.method} ${pathOf(call)}`)).toEqual([
			'GET /api/v1/workflows/wf-1/export?format=n8n'
		]);
	});
});

describe('system', () => {
	it('targets the liveness and readiness probes', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		const sdk = client(fetchImpl);

		await sdk.getHealth();
		await sdk.getReady();

		expect(calls.map((call) => `${call.init.method} ${new URL(call.url).pathname}`)).toEqual([
			'GET /api/v1/health',
			'GET /api/v1/ready'
		]);
	});
});

describe('tenant deletion', () => {
	it('targets the documented endpoint and returns the typed removal counts', async () => {
		const { calls, fetchImpl } = recordingFetch({
			body: {
				tenantId: 'acme',
				tenantRemoved: true,
				removed: { workflows: 2, tenants: 1, schedules: 0 },
				datastoreTables: 1,
				binaries: { executions: 1, files: 3, bytes: 4096 }
			}
		});

		// A tenant id is a path segment, not a path: the id reaches the server
		// exactly as it was given, so a slash cannot name the tenant next door.
		const deletion = await client(fetchImpl).deleteTenant('acme/../globex');

		expect(calls.map((call) => `${call.init.method} ${pathOf(call)}`)).toEqual([
			'DELETE /api/v1/tenants/acme%2F..%2Fglobex'
		]);
		// The counts are what makes a deletion verifiable, so the typed result
		// has to be the server's document rather than an opaque body.
		expect(deletion).toEqual({
			tenantId: 'acme',
			tenantRemoved: true,
			removed: { workflows: 2, tenants: 1, schedules: 0 },
			datastoreTables: 1,
			binaries: { executions: 1, files: 3, bytes: 4096 }
		});
	});
});
