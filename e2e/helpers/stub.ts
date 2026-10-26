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

// A real HTTP server that a workflow under test reaches through the
// instance's outbound policy (allowed_hosts plus one
// allowed_private_endpoints entry), instead of the suite hitting the internet.
// Playwright request interception cannot do this job: it runs in the browser,
// while a workflow's HTTP node is executed by the Go process.
//
// The same server also hosts the embed test's host page, which keeps the host
// on a different origin than the instance — the realistic cross-origin shape.
//
// `bindHost` defaults to loopback, which is what the binary host's stub needs.
// The image host passes 0.0.0.0 because its server reaches the stub as
// host.docker.internal, an address that is not 127.0.0.1 inside the
// container's own network namespace.
export async function startStub({ bindHost = '127.0.0.1' }: { bindHost?: string } = {}): Promise<StubServer> {
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
			// A real RSS document, so the RSS Read node and the feed trigger
			// run against the shape they meet in the world rather than the
			// stub's JSON default.
			if (url.pathname === '/feed') {
				response.writeHead(200, { 'content-type': 'application/rss+xml' });
				response.end(rssFeed());
				return;
			}
			response.writeHead(200, { 'content-type': 'application/json' });
			response.end(JSON.stringify({ ok: true, method: request.method, path: url.pathname }));
		});
	});
	server.listen(0, bindHost);
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

// The feed the /feed path serves: two entries, an author, categories, and a
// content body long enough to need the snippet cut.
function rssFeed(): string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:content="http://purl.org/rss/1.0/modules/content/">
  <channel>
    <title>Stub Wire</title>
    <link>http://127.0.0.1/feed</link>
    <description>the stub's own feed</description>
    <item>
      <title>First dispatch</title>
      <link>http://127.0.0.1/feed/1</link>
      <pubDate>Mon, 26 Oct 2026 08:00:00 +0000</pubDate>
      <guid isPermaLink="false">stub-1</guid>
      <dc:creator>Stub</dc:creator>
      <category>coverage</category>
      <description>a short teaser</description>
      <content:encoded><![CDATA[<p>The first dispatch carries enough words to require the one hundred and twenty character snippet cut that the reader applies to every body it renders, and then some more.</p>]]></content:encoded>
    </item>
    <item>
      <title>Second dispatch</title>
      <link>http://127.0.0.1/feed/2</link>
      <pubDate>Tue, 27 Oct 2026 08:00:00 +0000</pubDate>
      <guid isPermaLink="false">stub-2</guid>
      <dc:creator>Stub</dc:creator>
      <category>coverage</category>
      <description>another teaser</description>
      <content:encoded><![CDATA[<p>The second dispatch is shorter.</p>]]></content:encoded>
    </item>
  </channel>
</rss>`;
}

// Minimal host application for the embed handshake: it frames the editor,
// answers its `kilasflow:embed-ready` announcement with the session the test
// minted over the API — the same exchange sdk/src/browser.ts performs — and
// records every message so the test can await them instead of sleeping. A
// record keeps the fields a run's messages carry, so a test can read how the
// run it started ended. An `accent` query parameter is passed through as the
// session's branding accent, the one value a host re-accents the editor with.
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
var accent = query.get('accent');
var frame = document.getElementById('e2e-frame');
window.addEventListener('message', function (event) {
	var data = event.data || {};
	window.__e2eEvents.push({ origin: event.origin, type: data.type, executionId: data.executionId, status: data.status });
	if (event.data && event.data.type === 'kilasflow:embed-ready') {
		event.source.postMessage(
			{ type: 'kilasflow:embed-session', token: token, workflowId: workflowId, scopes: scopes, branding: accent ? { accent: accent } : {}, locale: locale },
			event.origin
		);
	}
});
frame.src = server + '/embed/' + encodeURIComponent(workflowId);
</script>
</body></html>
`;
}
