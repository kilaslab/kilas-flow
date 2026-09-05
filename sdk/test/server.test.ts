import { describe, expect, it, vi } from 'vitest';

import { KilasFlowClient, KilasFlowError } from '../src/server.js';

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

function client(fetchImpl: typeof globalThis.fetch, headers?: Record<string, string>) {
	return new KilasFlowClient({ baseUrl: 'https://flows.example', fetch: fetchImpl, headers });
}

describe('client configuration', () => {
	it('requires an absolute http or https base URL', () => {
		const { fetchImpl } = recordingFetch();
		// A relative base would silently resolve against whatever page loaded
		// the SDK, which is not something a host should discover at runtime.
		expect(() => new KilasFlowClient({ baseUrl: '', fetch: fetchImpl })).toThrow(/baseUrl/);
		expect(() => new KilasFlowClient({ baseUrl: '/api', fetch: fetchImpl })).toThrow(/absolute/);
		expect(() => new KilasFlowClient({ baseUrl: 'ftp://flows.example', fetch: fetchImpl })).toThrow(/http/);
		expect(() => new KilasFlowClient({ baseUrl: 'https://flows.example/', fetch: fetchImpl })).not.toThrow();
	});

	it('sends only the headers the host configured', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: [] });
		await client(fetchImpl, { Authorization: 'Bearer host-key' }).listWorkflows();

		const headers = new Headers(calls[0]!.init.headers);
		expect(headers.get('Authorization')).toBe('Bearer host-key');
		expect(headers.get('Accept')).toBe('application/json');
	});

	it('sends no credential when the host configured none', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: [] });
		await client(fetchImpl).listWorkflows();

		// There is deliberately no ambient key and no environment lookup.
		const headers = new Headers(calls[0]!.init.headers);
		expect(headers.get('Authorization')).toBeNull();
	});
});

describe('workflow methods', () => {
	it('targets the documented endpoints', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		const sdk = client(fetchImpl);

		await sdk.listWorkflows();
		await sdk.getWorkflow('wf-1');
		await sdk.createWorkflow({ schemaVersion: 1, name: 'x', nodes: [], connections: [], settings: {} });
		await sdk.updateWorkflow('wf-1', { schemaVersion: 1, name: 'y', nodes: [], connections: [], settings: {} });
		await sdk.activateWorkflow('wf-1');
		await sdk.runWorkflow('wf-1');
		await sdk.getWorkflowVersion('wf-1', 'wfv-1');

		expect(calls.map((call) => `${call.init.method} ${new URL(call.url).pathname}`)).toEqual([
			'GET /api/v1/workflows',
			'GET /api/v1/workflows/wf-1',
			'POST /api/v1/workflows',
			'PUT /api/v1/workflows/wf-1',
			'POST /api/v1/workflows/wf-1/activate',
			'POST /api/v1/workflows/wf-1/run',
			'GET /api/v1/workflows/wf-1/versions/wfv-1'
		]);
	});

	it('encodes identifiers rather than interpolating them raw', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		await client(fetchImpl).getWorkflow('wf/../../etc');

		// A raw identifier would let a caller walk out of the resource path.
		expect(new URL(calls[0]!.url).pathname).toBe('/api/v1/workflows/wf%2F..%2F..%2Fetc');
	});

	it('returns nothing for a 204', async () => {
		const { fetchImpl } = recordingFetch({ status: 204 });
		await expect(client(fetchImpl).deleteWorkflow('wf-1')).resolves.toBeUndefined();
	});
});

describe('executions', () => {
	it('passes list filters as query parameters', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: { items: [] } });
		await client(fetchImpl).listExecutions({ workflowId: 'wf-1', status: ['succeeded', 'failed'], limit: 10 });

		const url = new URL(calls[0]!.url);
		expect(url.pathname).toBe('/api/v1/executions');
		expect(url.searchParams.get('workflowId')).toBe('wf-1');
		expect(url.searchParams.getAll('status')).toEqual(['succeeded', 'failed']);
		expect(url.searchParams.get('limit')).toBe('10');
	});

	it('builds the live event stream URL', () => {
		const { fetchImpl } = recordingFetch();
		expect(client(fetchImpl).executionEventsUrl('exec-1')).toBe(
			'https://flows.example/api/v1/executions/exec-1/events'
		);
	});
});

describe('embed sessions', () => {
	it('refuses an incomplete request before it reaches the network', async () => {
		const { calls, fetchImpl } = recordingFetch({ status: 201, body: {} });
		const sdk = client(fetchImpl);

		await expect(
			sdk.createEmbedSession({ workflowId: '', scopes: ['workflow:read'], origin: 'https://host.example' })
		).rejects.toThrow(/workflowId/);
		await expect(
			sdk.createEmbedSession({ workflowId: 'wf-1', scopes: ['workflow:read'], origin: '' })
		).rejects.toThrow(/origin/);
		await expect(sdk.createEmbedSession({ workflowId: 'wf-1', scopes: [], origin: 'https://host.example' })).rejects.toThrow(/scope/);
		expect(calls).toHaveLength(0);
	});

	it('posts the session request to the documented endpoint', async () => {
		const { calls, fetchImpl } = recordingFetch({
			status: 201,
			body: { token: 'kfe1.a.b', embedUrl: '/embed/wf-1', scopes: ['workflow:read'], origin: 'https://host.example' }
		});
		const session = await client(fetchImpl, { Authorization: 'Bearer host-key' }).createEmbedSession({
			workflowId: 'wf-1',
			scopes: ['workflow:read'],
			origin: 'https://host.example'
		});

		expect(new URL(calls[0]!.url).pathname).toBe('/api/v1/embed-sessions');
		expect(JSON.parse(String(calls[0]!.init.body))).toMatchObject({ workflowId: 'wf-1', origin: 'https://host.example' });
		expect(session.token).toBe('kfe1.a.b');
	});
});

describe('errors', () => {
	it('turns a problem document into a typed error', async () => {
		const { fetchImpl } = recordingFetch({
			status: 422,
			body: { title: 'Unprocessable Entity', status: 422, detail: 'origin is not allowed' }
		});

		await expect(
			client(fetchImpl).createEmbedSession({ workflowId: 'wf-1', scopes: ['workflow:read'], origin: 'https://evil.example' })
		).rejects.toMatchObject({ name: 'KilasFlowError', status: 422, message: 'origin is not allowed' });
	});

	it('still fails usefully when the error body is not JSON', async () => {
		const fetchImpl = (async () =>
			new Response('<html>gateway</html>', { status: 502, headers: { 'Content-Type': 'text/html' } })) as unknown as typeof globalThis.fetch;

		const error = await client(fetchImpl)
			.listWorkflows()
			.catch((caught: unknown) => caught);
		expect(error).toBeInstanceOf(KilasFlowError);
		expect((error as KilasFlowError).status).toBe(502);
	});
});
