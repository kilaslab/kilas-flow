/**
 * Live-server proof for the embed-scoped datastore subset.
 *
 * This file is JavaScript on purpose, following operation-coverage.test.mjs:
 * the SDK typechecks under `moduleResolution: bundler`, which leaves `node:`
 * imports unresolved, so anything that stands up a real HTTP server lives
 * outside the typechecked tests. The stubbed-transport behaviour tests stay
 * in TypeScript next to it.
 */
import { createServer } from 'node:http';

import { describe, expect, it } from 'vitest';

import { KilasFlowClient } from '../src/server.ts';

describe('datastore methods against a live HTTP server', () => {
	it('round-trips the row listing over real HTTP', async () => {
		// No injected fetch: the real transport talks to a live local
		// server, so this proves the method against HTTP rather than a stub.
		const seen = [];
		const server = createServer((request, response) => {
			seen.push(`${request.method} ${request.url}`);
			response.writeHead(200, { 'Content-Type': 'application/json' });
			response.end(JSON.stringify({ items: [{ id: 1, email: 'ada@example.com' }], nextCursor: null }));
		});
		await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
		try {
			const address = server.address();
			if (!address || typeof address === 'string') throw new Error('the live test server did not bind a port');
			const live = new KilasFlowClient({ baseUrl: `http://127.0.0.1:${address.port}` });
			const page = await live.listDatastoreRows('datastore_1', { limit: 10 });

			expect(seen).toEqual(['GET /api/v1/datastores/datastore_1/rows?limit=10']);
			expect(page.items).toEqual([{ id: 1, email: 'ada@example.com' }]);
		} finally {
			await new Promise((resolve, reject) => server.close((error) => (error ? reject(error) : resolve())));
		}
	});

	it('round-trips a datastore-scoped session mint over real HTTP', async () => {
		const seen = [];
		const server = createServer((request, response) => {
			let body = '';
			request.on('data', (chunk) => (body += chunk));
			request.on('end', () => {
				seen.push(`${request.method} ${request.url} ${body}`);
				response.writeHead(201, { 'Content-Type': 'application/json' });
				response.end(
					JSON.stringify({
						token: 'kfe1.a.b',
						embedUrl: '',
						datastoreId: 'datastore_1',
						scopes: ['datastore:read'],
						origin: 'https://host.example'
					})
				);
			});
		});
		await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
		try {
			const address = server.address();
			if (!address || typeof address === 'string') throw new Error('the live test server did not bind a port');
			const live = new KilasFlowClient({ baseUrl: `http://127.0.0.1:${address.port}` });
			const session = await live.createEmbedSession({
				datastoreId: 'datastore_1',
				scopes: ['datastore:read'],
				origin: 'https://host.example'
			});

			expect(JSON.parse(seen[0].split(' ', 3)[2]).datastoreId).toBe('datastore_1');
			expect(session.datastoreId).toBe('datastore_1');
			expect(session.embedUrl).toBe('');
		} finally {
			await new Promise((resolve, reject) => server.close((error) => (error ? reject(error) : resolve())));
		}
	});

	it('round-trips a CSV export over real HTTP outside the JSON transport', async () => {
		// The export answers text/csv, so this proves the Accept override
		// and the text response path against HTTP rather than a stub.
		const seen = [];
		const server = createServer((request, response) => {
			seen.push(`${request.method} ${request.url} accept=${request.headers.accept}`);
			response.writeHead(200, { 'Content-Type': 'text/csv; charset=utf-8' });
			response.end('email\nada@example.com\n');
		});
		await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
		try {
			const address = server.address();
			if (!address || typeof address === 'string') throw new Error('the live test server did not bind a port');
			const live = new KilasFlowClient({ baseUrl: `http://127.0.0.1:${address.port}` });
			const csv = await live.exportDatastoreRows('datastore_1');

			expect(seen).toEqual(['GET /api/v1/datastores/datastore_1/rows/export accept=text/csv']);
			expect(csv).toBe('email\nada@example.com\n');
		} finally {
			await new Promise((resolve, reject) => server.close((error) => (error ? reject(error) : resolve())));
		}
	});
});
