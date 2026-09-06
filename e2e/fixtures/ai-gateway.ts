import { createServer, request as proxyRequest, type IncomingMessage, type Server, type ServerResponse } from 'node:http';
import { once } from 'node:events';

export interface GatewayHit {
	method: string;
	path: string;
	body: string;
}

export interface AIGateway {
	// The single loopback origin the test instance admits through its one
	// allowed_private_endpoints entry (allow_private_networks stays off).
	origin: string;
	// The `host:port` the instance's outbound policy names.
	endpoint: string;
	// Where the chat model node points: the real Ollama catalogue and
	// completions, byte-transparent through this server.
	modelBaseURL: string;
	hits: GatewayHit[];
	reset: () => void;
	close: () => Promise<void>;
}

// Starts the dual-purpose loopback server the AI agent suite reaches through
// the instance's outbound policy.
//
// One admitted endpoint has to cover two loopback dependencies — the model
// and the tool — because the harness boots each instance with a single
// allowed_private_endpoints entry (the koanf environment mapping carries one
// value per variable). So this server does both jobs: every path under /v1/
// is proxied unmodified to the real Ollama named by ollamaBaseURL, and the
// tool paths below are served here with deterministic behaviour, making the
// agent's decision the only variable in the suite.
//
// What this is not: a model served from Node. Every model byte comes from
// Ollama, including the streaming chunks; this process only forwards them.
// A green run therefore proves the agent loop against a real model — the
// wiring, not the quality of agent output.
export async function startAIGateway(ollamaBaseURL: string): Promise<AIGateway> {
	const ollama = new URL(ollamaBaseURL);
	const ollamaHost = ollama.hostname;
	const ollamaPort = Number(ollama.port || (ollama.protocol === 'https:' ? 443 : 80));
	const hits: GatewayHit[] = [];

	const server: Server = createServer((incoming: IncomingMessage, outgoing: ServerResponse) => {
		const chunks: Buffer[] = [];
		incoming.on('data', (chunk: Buffer) => chunks.push(chunk));
		incoming.on('end', () => {
			const body = Buffer.concat(chunks);
			const url = new URL(incoming.url ?? '/', 'http://127.0.0.1');
			if (url.pathname === '/v1/models' || url.pathname.startsWith('/v1/')) {
				forwardToOllama(incoming, outgoing, ollamaHost, ollamaPort, url, body);
				return;
			}
			if (url.pathname.startsWith('/weather/')) {
				const city = decodeURIComponent(url.pathname.slice('/weather/'.length));
				hits.push({ method: incoming.method ?? 'GET', path: url.pathname, body: body.toString('utf-8') });
				outgoing.writeHead(200, { 'content-type': 'application/json' });
				outgoing.end(JSON.stringify({ city, tempC: 19 }));
				return;
			}
			if (url.pathname.startsWith('/boom')) {
				hits.push({ method: incoming.method ?? 'GET', path: url.pathname, body: body.toString('utf-8') });
				outgoing.writeHead(500, { 'content-type': 'application/json' });
				outgoing.end(JSON.stringify({ ok: false, error: 'e2e tool failure' }));
				return;
			}
			outgoing.writeHead(404, { 'content-type': 'application/json' });
			outgoing.end(JSON.stringify({ ok: false, error: `unknown gateway path ${url.pathname}` }));
		});
	});
	server.listen(0, '127.0.0.1');
	await once(server, 'listening');
	const address = server.address();
	if (!address || typeof address === 'string') throw new Error('Unable to start the AI gateway');
	const origin = `http://127.0.0.1:${address.port}`;
	let closed = false;
	return {
		origin,
		endpoint: `127.0.0.1:${address.port}`,
		modelBaseURL: `${origin}/v1`,
		hits,
		reset: () => {
			hits.length = 0;
		},
		close: async () => {
			if (closed) return;
			closed = true;
			server.close();
			await once(server, 'close').catch(() => undefined);
		}
	};
}

// Forwards one model request to Ollama untouched: same path and query, same
// method, same body. Headers pass through except hop-by-hop ones Node manages
// itself. The response — status, headers, and every chunk, which is what
// carries the streaming deltas — is piped back unmodified.
function forwardToOllama(
	incoming: IncomingMessage,
	outgoing: ServerResponse,
	ollamaHost: string,
	ollamaPort: number,
	url: URL,
	body: Buffer
): void {
	const upstream = proxyRequest(
		{
			host: ollamaHost,
			port: ollamaPort,
			method: incoming.method,
			path: `${url.pathname}${url.search}`,
			headers: { ...incoming.headers, host: `${ollamaHost}:${ollamaPort}` }
		},
		(upstreamResponse) => {
			outgoing.writeHead(upstreamResponse.statusCode ?? 502, upstreamResponse.headers);
			upstreamResponse.pipe(outgoing);
		}
	);
	upstream.on('error', () => {
		if (!outgoing.headersSent) outgoing.writeHead(502, { 'content-type': 'application/json' });
		outgoing.end(JSON.stringify({ ok: false, error: 'ai gateway could not reach ollama' }));
	});
	if (body.length > 0) upstream.write(body);
	upstream.end();
}
