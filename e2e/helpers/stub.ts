import { createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http';
import { once } from 'node:events';

export interface StubRequest {
	method: string;
	path: string;
	// query is the raw query string, leading '?' included, so a credential that
	// injects a parameter is observable.
	query: string;
	// headers is what the workflow actually sent; a credential-injected header
	// is the observable under test.
	headers: Record<string, string | string[] | undefined>;
	body: string;
}

export interface StubServer {
	port: number;
	origin: string;
	url: (path?: string) => string;
	requests: StubRequest[];
	close: () => Promise<void>;
}

// A real HTTP server on loopback that a workflow under test reaches through
// the instance's outbound policy (allowed_hosts plus one
// allowed_private_endpoints entry), instead of the suite hitting the internet.
// Playwright request interception cannot do this job: it runs in the browser,
// while a workflow's HTTP node is executed by the Go process.
//
// The same server also hosts the embed test's host page, which keeps the host
// on a different origin than the instance — the realistic cross-origin shape.
export async function startStub(): Promise<StubServer> {
	const requests: StubRequest[] = [];
	const server: Server = createServer((request: IncomingMessage, response: ServerResponse) => {
		const chunks: Buffer[] = [];
		request.on('data', (chunk: Buffer) => chunks.push(chunk));
		request.on('end', () => {
			const url = new URL(request.url ?? '/', 'http://127.0.0.1');
			requests.push({
				method: request.method ?? 'GET',
				path: url.pathname,
				query: url.search,
				headers: { ...request.headers },
				body: Buffer.concat(chunks).toString('utf-8')
			});
			if (url.pathname === '/host') {
				response.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
				response.end(hostPage());
				return;
			}
			response.writeHead(200, { 'content-type': 'application/json' });
			response.end(JSON.stringify({ ok: true, method: request.method, path: url.pathname }));
		});
	});
	server.listen(0, '127.0.0.1');
	await once(server, 'listening');
	const address = server.address();
	if (!address || typeof address === 'string') throw new Error('Unable to start the stub server');
	const port = address.port;
	const origin = `http://127.0.0.1:${port}`;
	let closed = false;
	return {
		port,
		origin,
		url: (path = '/') => `${origin}${path}`,
		requests,
		close: async () => {
			if (closed) return;
			closed = true;
			server.close();
			await once(server, 'close').catch(() => undefined);
		}
	};
}

// Minimal host application for the embed handshake: it frames the editor,
// answers its `kilasflow:embed-ready` announcement with the session the test
// minted over the API — the same exchange sdk/src/browser.ts performs — and
// records every message so the test can await them instead of sleeping.
function hostPage(): string {
	return `<!doctype html>
<html><head><meta charset="utf-8"><title>e2e host</title></head>
<body>
<iframe id="e2e-frame" title="Embedded editor" style="width:100%;height:90vh;border:0"></iframe>
<script>
window.__e2eEvents = [];
var query = new URLSearchParams(location.search);
var server = query.get('server');
var workflowId = query.get('workflow');
var token = query.get('token');
var scopes = (query.get('scopes') || '').split(',').filter(Boolean);
var locale = query.get('locale');
var frame = document.getElementById('e2e-frame');
window.addEventListener('message', function (event) {
	window.__e2eEvents.push({ origin: event.origin, type: event.data && event.data.type });
	if (event.data && event.data.type === 'kilasflow:embed-ready') {
		event.source.postMessage(
			{ type: 'kilasflow:embed-session', token: token, workflowId: workflowId, scopes: scopes, branding: {}, locale: locale },
			event.origin
		);
	}
});
frame.src = server + '/embed/' + encodeURIComponent(workflowId);
</script>
</body></html>
`;
}
