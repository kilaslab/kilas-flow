/**
 * Standalone recording stub for end-to-end work by hand.
 *
 * The Playwright harness boots its own stub per test (see e2e/helpers/stub.ts,
 * which this mirrors); this script is for reproducing a run outside the suite:
 * point a workflow's HTTP node at the printed origin and watch the requests
 * arrive. It answers every API call with a small JSON echo.
 *
 * Usage: node scripts/e2e-stub.mjs [--port 0]
 */

import { createServer } from 'node:http';
import net from 'node:net';
import { once } from 'node:events';

const portArg = process.argv.indexOf('--port');
const wanted = portArg === -1 ? 0 : Number(process.argv[portArg + 1]);
if (!Number.isInteger(wanted) || wanted < 0 || wanted > 65535) {
	throw new Error('e2e-stub: --port takes a number between 0 and 65535');
}

async function freePort() {
	const listener = net.createServer();
	listener.listen(0, '127.0.0.1');
	await once(listener, 'listening');
	const address = listener.address();
	if (!address || typeof address === 'string') throw new Error('Unable to reserve a local port');
	const port = address.port;
	listener.close();
	await once(listener, 'close');
	return port;
}

const port = wanted === 0 ? await freePort() : wanted;
const server = createServer((request, response) => {
	const chunks = [];
	request.on('data', (chunk) => chunks.push(chunk));
	request.on('end', () => {
		const url = new URL(request.url ?? '/', 'http://127.0.0.1');
		const body = Buffer.concat(chunks).toString('utf-8');
		console.log(`${request.method} ${url.pathname}${body ? ` ${body}` : ''}`);
		response.writeHead(200, { 'content-type': 'application/json' });
		response.end(JSON.stringify({ ok: true, method: request.method, path: url.pathname }));
	});
});
server.listen(port, '127.0.0.1');
await once(server, 'listening');
console.log(`e2e-stub: listening on http://127.0.0.1:${port}`);
console.log(`e2e-stub: allow it with KILASFLOW_OUTBOUND_ALLOWED_HOSTS=127.0.0.1`);
console.log(`e2e-stub: and KILASFLOW_OUTBOUND_ALLOWED_PRIVATE_ENDPOINTS=127.0.0.1:${port}`);
