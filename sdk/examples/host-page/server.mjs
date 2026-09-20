/**
 * The host's backend.
 *
 * It is the only place the KilasFlow API credential exists. The browser gets a
 * short-lived, workflow-scoped embed token and nothing else.
 */

import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { dirname, join, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

import { KilasFlowClient } from '@kilasflow/sdk/server';

const here = dirname(fileURLToPath(import.meta.url));
const port = Number(process.env.PORT ?? 4173);
const origin = `http://localhost:${port}`;

// The only directory the page may fetch from, resolved once so a request path
// can never walk out of it.
const sdkDistPrefix = '/node_modules/@kilasflow/sdk/dist/';
const sdkDist = resolve(here, 'node_modules', '@kilasflow', 'sdk', 'dist');

const kilasflow = new KilasFlowClient({
	baseUrl: process.env.KILASFLOW_URL ?? 'http://127.0.0.1:8080',
	// The host's tenant-scoped API key. Explicit host configuration; the SDK
	// never looks one up for you.
	apiKey: process.env.KILASFLOW_API_KEY
});

createServer(async (request, response) => {
	try {
		const pathname = new URL(request.url ?? '/', origin).pathname;

		// Read before writing headers, as the static route does: an index.html
		// that is missing or unreadable then reaches the 500 below with
		// nothing sent yet, instead of failing a second write on a sent
		// response.
		if (pathname === '/' || pathname.startsWith('/index.html')) {
			const page = await readFile(join(here, 'index.html'));
			response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
			response.end(page);
			return;
		}

		// The installed SDK, served to the page. Only the published package's
		// compiled output is reachable — never server sources, never the key.
		// The decoded path is resolved against the dist directory and anything
		// that lands outside it is a 404, so `..%2f` cannot climb out; a file
		// that is not there is a 404 too, not a 500 that names it.
		if (pathname.startsWith(sdkDistPrefix)) {
			let file;
			try {
				file = resolve(sdkDist, decodeURIComponent(pathname.slice(sdkDistPrefix.length)));
			} catch {
				// A malformed percent-escape is a not-found, not a crash.
				response.writeHead(404).end('Not found');
				return;
			}
			if (!file.startsWith(sdkDist + sep)) {
				response.writeHead(404).end('Not found');
				return;
			}
			try {
				const body = await readFile(file);
				response.writeHead(200, { 'Content-Type': 'text/javascript; charset=utf-8' });
				response.end(body);
			} catch {
				response.writeHead(404).end('Not found');
			}
			return;
		}

		// The page asks its own backend for a session. The token it gets back
		// is scoped to one workflow, this origin, and a few minutes.
		if (pathname === '/api/embed-session' && request.method === 'POST') {
			const workflow = await ensureWorkflow();
			const session = await kilasflow.createEmbedSession({
				workflowId: workflow.id,
				scopes: ['workflow:read', 'workflow:write', 'workflow:run'],
				origin,
				branding: { name: 'Acme Flows', accent: '#0ea5e9' }
			});
			response.writeHead(200, { 'Content-Type': 'application/json' });
			response.end(JSON.stringify({ ...session, baseUrl: kilasflow.baseUrl }));
			return;
		}

		// EventSource cannot send an Authorization header, so the page asks
		// its backend for a single-use ticket per execution and spends it as
		// ?ticket=. The key never leaves this process.
		if (pathname === '/api/stream-ticket' && request.method === 'GET') {
			const executionId = new URL(request.url ?? '/', origin).searchParams.get('executionId');
			if (!executionId) {
				response.writeHead(400, { 'Content-Type': 'application/json' });
				response.end(JSON.stringify({ error: 'executionId is required' }));
				return;
			}
			const ticket = await kilasflow.createStreamTicket(executionId);
			response.writeHead(200, { 'Content-Type': 'application/json' });
			response.end(JSON.stringify(ticket));
			return;
		}

		response.writeHead(404).end('Not found');
	} catch (error) {
		// A route that already wrote its response cannot write a 500 over it.
		// Without this guard the throw escapes the async handler as an
		// unhandled rejection and takes the process with it, which is a dead
		// example rather than a failed request.
		if (!response.headersSent) {
			response.writeHead(500, { 'Content-Type': 'application/json' });
			response.end(JSON.stringify({ error: String(error) }));
			return;
		}
		// Headers are already out, so no status can be corrected. The response
		// is still open, though: leaving it that way hangs the client on a
		// request that will never be answered and swallows the error. Say what
		// happened and close the connection.
		console.error(error);
		response.destroy();
	}
}).listen(port, () => console.log(`Host page on ${origin}`));

/** Reuses the demo workflow, creating it the first time. */
async function ensureWorkflow() {
	const existing = await kilasflow.listWorkflows();
	const found = existing.find((workflow) => workflow.name === 'Embedded demo');
	if (found) return found;

	return kilasflow.createWorkflow({
		schemaVersion: 1,
		name: 'Embedded demo',
		nodes: [
			{ id: 'trigger', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 80, y: 80 } },
			{
				id: 'set', name: 'Set', type: 'kilasflow.set', typeVersion: 1, position: { x: 420, y: 80 },
				parameters: { assignments: { status: 'ready' } }
			}
		],
		connections: [
			{
				id: 'c1', kind: 'main',
				source: { nodeId: 'trigger', port: 'main' },
				target: { nodeId: 'set', port: 'main' }
			}
		],
		settings: {}
	});
}
