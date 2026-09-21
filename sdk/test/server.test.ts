import { describe, expect, it, vi } from 'vitest';

import { KilasFlowClient, KilasFlowError, datastoreFilter, paginateCursor, tenantClientFactory } from '../src/server.js';

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

	it('sends apiKey as a bearer token without touching other headers', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: [] });
		const host = new KilasFlowClient({
			baseUrl: 'https://flows.example',
			fetch: fetchImpl,
			apiKey: 'kfa1.acme.secret',
			headers: { 'X-Gateway': 'edge' }
		});
		await host.listWorkflows();

		const headers = new Headers(calls[0]!.init.headers);
		expect(headers.get('Authorization')).toBe('Bearer kfa1.acme.secret');
		expect(headers.get('X-Gateway')).toBe('edge');
	});

	it('lets an explicit Authorization header win over apiKey', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: [] });
		const host = new KilasFlowClient({
			baseUrl: 'https://flows.example',
			fetch: fetchImpl,
			apiKey: 'kfa1.acme.secret',
			headers: { Authorization: 'Gateway signed-value' }
		});
		await host.listWorkflows();

		// The convenience adds; it never replaces what the host spelled out.
		expect(new Headers(calls[0]!.init.headers).get('Authorization')).toBe('Gateway signed-value');
	});

	it('refuses an empty apiKey rather than sending an unauthenticated request by surprise', () => {
		const { fetchImpl } = recordingFetch();
		expect(() => new KilasFlowClient({ baseUrl: 'https://flows.example', fetch: fetchImpl, apiKey: '  ' })).toThrow(/apiKey/);
	});
});

describe('per-tenant clients', () => {
	it('builds one fixed-credential client per tenant key', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: [] });
		const forTenant = tenantClientFactory({ baseUrl: 'https://flows.example', fetch: fetchImpl });

		await forTenant('kfa1.acme.secret').listWorkflows();
		await forTenant('kfa1.globex.secret').listWorkflows();

		// Two tenants, two keys, no path for one to reach the other's.
		expect(new Headers(calls[0]!.init.headers).get('Authorization')).toBe('Bearer kfa1.acme.secret');
		expect(new Headers(calls[1]!.init.headers).get('Authorization')).toBe('Bearer kfa1.globex.secret');
	});

	it('freezes the credential at construction: later mutation changes nothing', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: [] });
		const shared: Record<string, string> = { 'X-Gateway': 'edge' };
		const host = new KilasFlowClient({ baseUrl: 'https://flows.example', fetch: fetchImpl, apiKey: 'kfa1.acme.secret', headers: shared });
		shared['X-Gateway'] = 'mutated';
		shared.Authorization = 'Bearer kfa1.evil.swapped';
		await host.listWorkflows();

		// The constructor copies: no setter, no refresh, no swap between requests.
		const headers = new Headers(calls[0]!.init.headers);
		expect(headers.get('Authorization')).toBe('Bearer kfa1.acme.secret');
		expect(headers.get('X-Gateway')).toBe('edge');
	});

	it('refuses shared Authorization in the factory: the credential comes per tenant', () => {
		const { fetchImpl } = recordingFetch();
		expect(() =>
			tenantClientFactory({ baseUrl: 'https://flows.example', fetch: fetchImpl, headers: { Authorization: 'Bearer shared' } })
		).toThrow(/per tenant/);
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

describe('datastore methods', () => {
	it('targets the documented endpoints, scoped to one datastore', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		const sdk = client(fetchImpl);

		await sdk.getDatastore('datastore_1');
		await sdk.listDatastoreRows('datastore_1', { limit: 25 });
		await sdk.getDatastoreRow('datastore_1', 7);
		await sdk.insertDatastoreRow('datastore_1', { email: 'ada@example.com' });

		expect(calls.map((call) => `${call.init.method} ${new URL(call.url).pathname}`)).toEqual([
			'GET /api/v1/datastores/datastore_1',
			'GET /api/v1/datastores/datastore_1/rows',
			'GET /api/v1/datastores/datastore_1/rows/7',
			'POST /api/v1/datastores/datastore_1/rows'
		]);
		expect(new URL(calls[1]!.url).searchParams.get('limit')).toBe('25');
		expect(JSON.parse(String(calls[3]!.init.body))).toEqual({ values: { email: 'ada@example.com' } });
	});

	it('encodes datastore identifiers rather than interpolating them raw', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		await client(fetchImpl).getDatastore('ds/../etc');

		expect(new URL(calls[0]!.url).pathname).toBe('/api/v1/datastores/ds%2F..%2Fetc');
	});

	it('sends filter triples as documented query parameters and drops nulls', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: { items: [] } });
		await client(fetchImpl).listDatastoreRows('datastore_1', {
			match: 'All Conditions',
			columnName: ['email'],
			condition: ['eq'],
			// Null is how the generated params spell "absent": the client
			// sends nothing rather than a null value the API never defined.
			value: null
		});

		const params = new URL(calls[0]!.url).searchParams;
		expect(params.get('match')).toBe('All Conditions');
		expect(params.getAll('columnName')).toEqual(['email']);
		expect(params.getAll('condition')).toEqual(['eq']);
		expect(params.has('value')).toBe(false);
	});

});

describe('datastore management', () => {
	it('targets the documented endpoints with the backend credential', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		const sdk = client(fetchImpl);

		await sdk.listDatastores();
		await sdk.createDatastore({ name: 'Customers', columns: [{ name: 'email', type: 'string' }] });
		await sdk.renameDatastore('datastore_1', 'Clients');
		await sdk.deleteDatastore('datastore_1');
		await sdk.clearDatastore('datastore_1');

		expect(calls.map((call) => `${call.init.method} ${new URL(call.url).pathname}`)).toEqual([
			'GET /api/v1/datastores',
			'POST /api/v1/datastores',
			'PUT /api/v1/datastores/datastore_1',
			'DELETE /api/v1/datastores/datastore_1',
			'POST /api/v1/datastores/datastore_1/clear'
		]);
		expect(JSON.parse(String(calls[1]!.init.body))).toEqual({
			name: 'Customers',
			columns: [{ name: 'email', type: 'string' }]
		});
		expect(JSON.parse(String(calls[2]!.init.body))).toEqual({ name: 'Clients' });
	});

	it('omits columns when a datastore is created name-only', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		await client(fetchImpl).createDatastore({ name: 'Bare' });

		expect(JSON.parse(String(calls[0]!.init.body))).toEqual({ name: 'Bare' });
	});

	it('resolves void for the 204 management calls', async () => {
		const gone = recordingFetch({ status: 204 });
		const sdk = client(gone.fetchImpl);

		await expect(sdk.deleteDatastore('datastore_1')).resolves.toBeUndefined();
		await expect(sdk.deleteDatastoreColumn('datastore_1', 'email')).resolves.toBeUndefined();
	});

	it('sends the host apiKey as a bearer token on management calls', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		const host = new KilasFlowClient({ baseUrl: 'https://flows.example', fetch: fetchImpl, apiKey: 'kfa1.acme.secret' });

		await host.createDatastore({ name: 'Customers' });
		await host.deleteDatastore('datastore_1');

		// The management surface is backend-only by design: it carries the
		// tenant key, never an embed token.
		for (const call of calls) {
			expect(new Headers(call.init.headers).get('Authorization')).toBe('Bearer kfa1.acme.secret');
		}
	});
});

describe('datastore columns', () => {
	it('targets the documented column endpoints', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		const sdk = client(fetchImpl);

		await sdk.addDatastoreColumn('datastore_1', { name: 'age', type: 'number' });
		await sdk.renameDatastoreColumn('datastore_1', 'age', 'years');
		await sdk.deleteDatastoreColumn('datastore_1', 'years');

		expect(calls.map((call) => `${call.init.method} ${new URL(call.url).pathname}`)).toEqual([
			'POST /api/v1/datastores/datastore_1/columns',
			'PUT /api/v1/datastores/datastore_1/columns/age',
			'DELETE /api/v1/datastores/datastore_1/columns/years'
		]);
		expect(JSON.parse(String(calls[0]!.init.body))).toEqual({ name: 'age', type: 'number' });
		expect(JSON.parse(String(calls[1]!.init.body))).toEqual({ name: 'years' });
	});

	it('encodes column names rather than interpolating them raw', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		await client(fetchImpl).renameDatastoreColumn('datastore_1', 'a/b', 'c');

		expect(new URL(calls[0]!.url).pathname).toBe('/api/v1/datastores/datastore_1/columns/a%2Fb');
	});

	it('sends the host apiKey as a bearer token on column calls', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		const host = new KilasFlowClient({ baseUrl: 'https://flows.example', fetch: fetchImpl, apiKey: 'kfa1.acme.secret' });

		await host.addDatastoreColumn('datastore_1', { name: 'age', type: 'number' });

		expect(new Headers(calls[0]!.init.headers).get('Authorization')).toBe('Bearer kfa1.acme.secret');
	});
});

describe('filtered row writes', () => {
	it('targets the documented endpoints with the filter verbatim', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		const sdk = client(fetchImpl);
		const filter = datastoreFilter('and', [{ columnName: 'email', condition: 'eq', value: 'ada@example.com' }]);

		await sdk.updateDatastoreRows('datastore_1', filter, { tier: 'pro' });
		await sdk.deleteDatastoreRows('datastore_1', filter);
		await sdk.upsertDatastoreRow('datastore_1', filter, { email: 'ada@example.com', tier: 'pro' });

		expect(calls.map((call) => `${call.init.method} ${new URL(call.url).pathname}`)).toEqual([
			'PUT /api/v1/datastores/datastore_1/rows',
			'DELETE /api/v1/datastores/datastore_1/rows',
			'POST /api/v1/datastores/datastore_1/rows/upsert'
		]);
		expect(JSON.parse(String(calls[0]!.init.body))).toEqual({
			filter: { type: 'and', filters: [{ columnName: 'email', condition: 'eq', value: 'ada@example.com' }] },
			values: { tier: 'pro' }
		});
		expect(JSON.parse(String(calls[1]!.init.body))).toEqual({
			filter: { type: 'and', filters: [{ columnName: 'email', condition: 'eq', value: 'ada@example.com' }] }
		});
	});

	it('refuses an empty filter client-side without sending', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		const sdk = client(fetchImpl);

		// Both shapes the server refuses — no conditions at all, and an
		// explicit empty list — never reach the network.
		for (const filter of [
			{ type: 'and', filters: [] },
			{ type: 'and', filters: null }
		] as Array<{ type: string; filters: [] | null }>) {
			await expect(sdk.updateDatastoreRows('datastore_1', filter, { tier: 'pro' })).rejects.toThrow(/at least one condition/);
			await expect(sdk.deleteDatastoreRows('datastore_1', filter)).rejects.toThrow(/at least one condition/);
			await expect(sdk.upsertDatastoreRow('datastore_1', filter, { tier: 'pro' })).rejects.toThrow(/at least one condition/);
		}
		expect(calls).toHaveLength(0);
	});

	it('keeps row values out of the empty-filter refusal', async () => {
		const { fetchImpl } = recordingFetch({ body: {} });

		const error = await client(fetchImpl)
			.updateDatastoreRows('datastore_1', { type: 'and', filters: [] }, { ssn: 'secret-value' })
			.catch((caught: unknown) => caught);

		// A refused write must not echo what it refused to write: row
		// contents stay with the caller, never in an error message.
		expect(String((error as Error).message)).not.toContain('secret-value');
	});

	it('sends the host apiKey as a bearer token on filtered writes', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		const host = new KilasFlowClient({ baseUrl: 'https://flows.example', fetch: fetchImpl, apiKey: 'kfa1.acme.secret' });
		const filter = datastoreFilter('or', [{ columnName: 'id', condition: 'gt', value: 7 }]);

		await host.updateDatastoreRows('datastore_1', filter, { tier: 'pro' });

		expect(new Headers(calls[0]!.init.headers).get('Authorization')).toBe('Bearer kfa1.acme.secret');
	});
});

describe('datastore filters', () => {
	it('builds the wire envelope verbatim', () => {
		expect(datastoreFilter('and', [{ columnName: 'email', condition: 'eq', value: 'ada@example.com' }])).toEqual({
			type: 'and',
			filters: [{ columnName: 'email', condition: 'eq', value: 'ada@example.com' }]
		});
		expect(datastoreFilter('or', [{ columnName: 'age', condition: 'gte', value: 18 }]).type).toBe('or');
	});

	it('omits the value slot for the empty tests', () => {
		const [predicate] = datastoreFilter('and', [{ columnName: 'email', condition: 'isEmpty' }]).filters ?? [];

		expect(predicate).toEqual({ columnName: 'email', condition: 'isEmpty' });
		expect('value' in (predicate ?? {})).toBe(false);
	});
});

describe('cursor iteration', () => {
	/** Serves one canned page per request so a test can script a walk. */
	function queueFetch(pages: unknown[]) {
		const calls: Array<{ url: string; init: RequestInit }> = [];
		const fetchImpl = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
			calls.push({ url: String(url), init: init ?? {} });
			return new Response(JSON.stringify(pages.shift() ?? { items: [] }), {
				status: 200,
				headers: { 'Content-Type': 'application/json' }
			});
		}) as unknown as typeof globalThis.fetch;
		return { calls, fetchImpl };
	}

	it('pages datastore rows to exhaustion through the shared helper', async () => {
		const { calls, fetchImpl } = queueFetch([
			{ items: [{ id: 1 }, { id: 2 }], nextCursor: 'cursor-1' },
			{ items: [{ id: 3 }] }
		]);

		const rows: Array<Record<string, unknown>> = [];
		for await (const row of client(fetchImpl).iterateDatastoreRows('datastore_1', { limit: 2 })) rows.push(row);

		expect(rows).toEqual([{ id: 1 }, { id: 2 }, { id: 3 }]);
		expect(calls).toHaveLength(2);
		expect(new URL(calls[0]!.url).searchParams.get('cursor')).toBeNull();
		expect(new URL(calls[1]!.url).searchParams.get('cursor')).toBe('cursor-1');
	});

	it('pages executions through the same helper rather than a second copy', async () => {
		const { calls, fetchImpl } = queueFetch([
			{ items: [{ id: 'execution_1' }], nextCursor: 'cursor-1' },
			{ items: [{ id: 'execution_2' }] }
		]);

		const ids: string[] = [];
		for await (const execution of client(fetchImpl).iterateExecutions({})) ids.push(execution.id);

		expect(ids).toEqual(['execution_1', 'execution_2']);
		expect(new URL(calls[1]!.url).searchParams.get('cursor')).toBe('cursor-1');
	});

	it('ends the walk when a page carries no cursor', async () => {
		const { calls, fetchImpl } = queueFetch([{ items: [] }]);

		const rows: unknown[] = [];
		for await (const row of paginateCursor<unknown>(async () => ({
			items: (await client(fetchImpl).listDatastoreRows('datastore_1').then((page) => page.items)) ?? []
		})))
			rows.push(row);

		expect(rows).toEqual([]);
		expect(calls).toHaveLength(1);
	});
});

describe('datastore CSV transfer', () => {
	it('asks for text/csv and resolves the export as text', async () => {
		const calls: Array<{ url: string; init: RequestInit }> = [];
		const fetchImpl = (async (url: string | URL | Request, init?: RequestInit) => {
			calls.push({ url: String(url), init: init ?? {} });
			return new Response('email\nada@example.com\n', { status: 200, headers: { 'Content-Type': 'text/csv; charset=utf-8' } });
		}) as unknown as typeof globalThis.fetch;

		const csv = await client(fetchImpl).exportDatastoreRows('datastore_1', { includeSystemColumns: true });

		expect(csv).toBe('email\nada@example.com\n');
		expect(new URL(calls[0]!.url).pathname).toBe('/api/v1/datastores/datastore_1/rows/export');
		expect(new URL(calls[0]!.url).searchParams.get('includeSystemColumns')).toBe('true');
		expect(new Headers(calls[0]!.init.headers).get('Accept')).toBe('text/csv');
	});

	it('posts the file as raw text/csv rather than JSON', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: { inserted: 2, skipped: 0, failed: [] } });

		const report = await client(fetchImpl).importDatastoreRows('datastore_1', 'email\nada@example.com\n');

		expect(new URL(calls[0]!.url).pathname).toBe('/api/v1/datastores/datastore_1/rows/import');
		expect(new Headers(calls[0]!.init.headers).get('Content-Type')).toBe('text/csv');
		// Verbatim: no JSON quoting around the file.
		expect(calls[0]!.init.body).toBe('email\nada@example.com\n');
		expect(report).toMatchObject({ inserted: 2, skipped: 0 });
	});

	it('sends the host apiKey as a bearer token on the transfer pair', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: { inserted: 0, skipped: 0, failed: [] } });
		const host = new KilasFlowClient({ baseUrl: 'https://flows.example', fetch: fetchImpl, apiKey: 'kfa1.acme.secret' });

		await host.importDatastoreRows('datastore_1', 'email\n');

		expect(new Headers(calls[0]!.init.headers).get('Authorization')).toBe('Bearer kfa1.acme.secret');
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

	it('needs exactly one subject before it reaches the network', async () => {
		const { calls, fetchImpl } = recordingFetch({ status: 201, body: {} });
		const sdk = client(fetchImpl);

		// Neither subject names nothing; both name an ambiguous session.
		// Either way the server could mint only a token that reaches nowhere.
		await expect(
			sdk.createEmbedSession({ scopes: ['datastore:read'], origin: 'https://host.example' })
		).rejects.toThrow(/exactly one/);
		await expect(
			sdk.createEmbedSession({
				workflowId: 'wf-1',
				datastoreId: 'datastore_1',
				scopes: ['workflow:read'],
				origin: 'https://host.example'
			})
		).rejects.toThrow(/exactly one/);
		expect(calls).toHaveLength(0);
	});

	it('posts a datastore session request to the documented endpoint', async () => {
		const { calls, fetchImpl } = recordingFetch({
			status: 201,
			body: { token: 'kfe1.a.b', embedUrl: '', datastoreId: 'datastore_1', scopes: ['datastore:read'], origin: 'https://host.example' }
		});
		const session = await client(fetchImpl).createEmbedSession({
			datastoreId: 'datastore_1',
			scopes: ['datastore:read'],
			origin: 'https://host.example'
		});

		expect(new URL(calls[0]!.url).pathname).toBe('/api/v1/embed-sessions');
		expect(JSON.parse(String(calls[0]!.init.body))).toMatchObject({ datastoreId: 'datastore_1', origin: 'https://host.example' });
		expect(session.datastoreId).toBe('datastore_1');
		expect(session.embedUrl).toBe('');
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

	it('tells a wrong key from a key reaching past its tenant without parsing text', async () => {
		const unauthorized = recordingFetch({ status: 401, body: { title: 'Unauthorized', status: 401 } });
		const forbidden = recordingFetch({ status: 403, body: { title: 'Forbidden', status: 403 } });

		const wrongKey = await client(unauthorized.fetchImpl)
			.listWorkflows()
			.catch((caught: unknown) => caught);
		const otherTenant = await client(forbidden.fetchImpl)
			.listWorkflows()
			.catch((caught: unknown) => caught);

		// The status is the signal: 401 is "your key is wrong", 403 is "your
		// key is fine and this is not yours". No message text is read.
		expect((wrongKey as KilasFlowError).status).toBe(401);
		expect((otherTenant as KilasFlowError).status).toBe(403);
		expect(wrongKey).toBeInstanceOf(KilasFlowError);
		expect(otherTenant).toBeInstanceOf(KilasFlowError);
	});
});

describe('idempotency keys', () => {
	/** The 409 body both conflict codes share; only `value.code` differs. */
	const conflict = (code: string) => ({
		title: 'Conflict',
		status: 409,
		detail: 'this Idempotency-Key was already used for a different request; send a new key for a new request',
		errors: [{ message: 'the key is in use', location: 'header.Idempotency-Key', value: { code } }]
	});
	const filter = datastoreFilter('and', [{ columnName: 'email', condition: 'eq', value: 'ada@example.com' }]);
	/** A key the server refuses and will not silently drop. */
	const unusableKeys = ['', ' ', 'has space', 'kunci-ñ', 'k'.repeat(256)];

	it('sends the key on runWorkflow and leaves an absent key absent', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: { id: 'exec_1', status: 'queued' } });
		const host = new KilasFlowClient({ baseUrl: 'https://flows.example', fetch: fetchImpl, apiKey: 'kfa1.acme.secret' });

		await host.runWorkflow('workflow_1', { rows: 3 }, { idempotencyKey: 'op-1234' });
		await host.runWorkflow('workflow_1', { rows: 3 });

		const keyed = new Headers(calls[0]!.init.headers);
		expect(keyed.get('Idempotency-Key')).toBe('op-1234');
		expect(new Headers(calls[1]!.init.headers).get('Idempotency-Key')).toBeNull();
		// A per-request header is added beside the configured ones, and the
		// defaults a request already carried, never instead of them.
		expect(keyed.get('Authorization')).toBe('Bearer kfa1.acme.secret');
		expect(keyed.get('Content-Type')).toBe('application/json');
	});

	it('still takes an AbortSignal where the options object goes', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: { id: 'exec_1', status: 'queued' } });
		const controller = new AbortController();

		await client(fetchImpl).runWorkflow('workflow_1', {}, controller.signal);

		// The caller's own signal still reaches the request rather than being
		// mistaken for an options object and dropped.
		const signal = calls[0]!.init.signal as AbortSignal;
		controller.abort();
		expect(signal.aborted).toBe(true);
	});

	it('sends the key on both datastore write methods', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		const sdk = client(fetchImpl);

		await sdk.insertDatastoreRow('datastore_1', { email: 'ada@example.com' }, { idempotencyKey: 'insert-1' });
		await sdk.upsertDatastoreRow('datastore_1', filter, { tier: 'pro' }, { idempotencyKey: 'k'.repeat(255) });

		expect(new Headers(calls[0]!.init.headers).get('Idempotency-Key')).toBe('insert-1');
		// 255 printable characters is the boundary the server accepts.
		expect(new Headers(calls[1]!.init.headers).get('Idempotency-Key')).toBe('k'.repeat(255));
	});

	it('refuses an unusable key before calling fetch', async () => {
		const { calls, fetchImpl } = recordingFetch({ body: {} });
		const sdk = client(fetchImpl);

		// An empty or malformed key is refused, never dropped: sending no
		// header would look like idempotency to the caller and give none.
		for (const idempotencyKey of unusableKeys) {
			await expect(sdk.runWorkflow('workflow_1', {}, { idempotencyKey })).rejects.toThrow(/Idempotency-Key/);
			await expect(sdk.insertDatastoreRow('datastore_1', { email: 'ada@example.com' }, { idempotencyKey })).rejects.toThrow(
				/Idempotency-Key/
			);
			await expect(sdk.upsertDatastoreRow('datastore_1', filter, { tier: 'pro' }, { idempotencyKey })).rejects.toThrow(
				/Idempotency-Key/
			);
		}
		expect(calls).toHaveLength(0);
	});

	it('surfaces Retry-After from an in-flight 409 and nothing from a reused key', async () => {
		const inFlight = recordingFetch({ status: 409, body: conflict('idempotency_key_in_flight'), headers: { 'Retry-After': '3' } });
		const reused = recordingFetch({ status: 409, body: conflict('idempotency_key_reused') });

		const wait = (await client(inFlight.fetchImpl)
			.runWorkflow('workflow_1', {}, { idempotencyKey: 'op-1' })
			.catch((caught: unknown) => caught)) as KilasFlowError;
		const giveUp = (await client(reused.fetchImpl)
			.runWorkflow('workflow_1', {}, { idempotencyKey: 'op-1' })
			.catch((caught: unknown) => caught)) as KilasFlowError;

		// The guide tells a host to wait and retry with the same key, which
		// it can only obey if the header survives into the error.
		expect(wait).toBeInstanceOf(KilasFlowError);
		expect(wait.status).toBe(409);
		expect(wait.problem?.errors?.[0]?.value).toMatchObject({ code: 'idempotency_key_in_flight' });
		expect(wait.retryAfterSeconds).toBe(3);
		// Only a new key helps a reused one, so there is no hint to wait on.
		expect(giveUp.retryAfterSeconds).toBeUndefined();
	});

	it('treats a Retry-After that is not delta-seconds as no hint', async () => {
		const dated = recordingFetch({
			status: 409,
			body: conflict('idempotency_key_in_flight'),
			headers: { 'Retry-After': 'Wed, 21 Oct 2026 07:28:00 GMT' }
		});

		const error = (await client(dated.fetchImpl)
			.runWorkflow('workflow_1', {}, { idempotencyKey: 'op-1' })
			.catch((caught: unknown) => caught)) as KilasFlowError;

		// An HTTP-date would need the caller's clock; the SDK does not guess.
		expect(error.retryAfterSeconds).toBeUndefined();
	});
});
