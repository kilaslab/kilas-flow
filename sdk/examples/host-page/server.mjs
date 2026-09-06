/**
 * The host's backend.
 *
 * It is the only place the KilasFlow API credential exists. The browser gets a
 * short-lived, workflow-scoped embed token and nothing else.
 */

import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { KilasFlowClient } from '@kilasflow/sdk/server';

const here = dirname(fileURLToPath(import.meta.url));
const port = Number(process.env.PORT ?? 4173);
const origin = `http://localhost:${port}`;

const kilasflow = new KilasFlowClient({
	baseUrl: process.env.KILASFLOW_URL ?? 'http://127.0.0.1:8080',
	// The host's tenant-scoped API key. Explicit host configuration; the SDK
	// never looks one up for you.
	apiKey: process.env.KILASFLOW_API_KEY
});

createServer(async (request, response) => {
	try {
		if (request.url === '/' || request.url?.startsWith('/index.html')) {
			response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
			response.end(await readFile(join(here, 'index.html')));
			return;
		}

		// The installed SDK, served to the page. Only the published package's
		// compiled output is reachable — never server sources, never the key.
		if (request.url?.startsWith('/node_modules/@kilasflow/sdk/dist/')) {
			const file = join(here, decodeURIComponent(request.url.slice(1)));
			response.writeHead(200, { 'Content-Type': 'text/javascript; charset=utf-8' });
			response.end(await readFile(file));
			return;
		}

		// The page asks its own backend for a session. The token it gets back
		// is scoped to one workflow, this origin, and a few minutes.
		if (request.url === '/api/embed-session' && request.method === 'POST') {
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
		if (request.url?.startsWith('/api/stream-ticket') && request.method === 'GET') {
			const executionId = new URL(request.url, origin).searchParams.get('executionId');
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
		response.writeHead(500, { 'Content-Type': 'application/json' });
		response.end(JSON.stringify({ error: String(error) }));
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
