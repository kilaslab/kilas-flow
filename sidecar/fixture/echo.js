'use strict';

// Fixture sidecar node: fixture.echo.
//
// A programmatic community node in miniature, speaking the host protocol
// (see sidecar/doc.go) with nothing but Node built-ins — no dependencies, no
// package manifest, no framework. The operator installs real packages; this
// file proves the boundary they run behind.
//
// Usage: node echo.js <tenant> <socketPath>
//
// The startup banner below is deliberate: it goes to stdout, which the host
// connects to its log and never parses. If the protocol ever read a byte from
// stdout, this line would corrupt the first message — the fixture test pins
// that it does not.

const net = require('node:net');

const [tenant, socketPath] = process.argv.slice(2);
if (!tenant || !socketPath) {
	console.error('usage: node echo.js <tenant> <socketPath>');
	process.exit(2);
}

console.log(`[fixture-echo] starting for tenant ${tenant} (stdout is diagnostics-only, never parsed)`);

function greet(items) {
	const out = [];
	for (const item of items) {
		const name = item && item.json && item.json.name;
		if (typeof name !== 'string' || name.trim() === '') {
			return { error: { code: 'bad-input', message: 'item is missing its name' } };
		}
		out.push({ json: { name, greeting: `hello, ${name}` } });
	}
	return { items: out };
}

const socket = net.createConnection(socketPath);
socket.setEncoding('utf8');

let buffer = '';
socket.on('data', (chunk) => {
	buffer += chunk;
	let index;
	while ((index = buffer.indexOf('\n')) >= 0) {
		const line = buffer.slice(0, index);
		buffer = buffer.slice(index + 1);
		if (line === '') continue;
		let frame;
		try {
			frame = JSON.parse(line);
		} catch {
			continue; // never happens on the host's own framing; refuse to die on it
		}
		if (frame.type !== 'execute') {
			socket.write(
				JSON.stringify({ type: 'error', id: frame.id, code: 'unknown-frame', message: `no such frame: ${frame.type}` }) + '\n',
			);
			continue;
		}
		const verdict = greet(frame.items || []);
		if (verdict.error) {
			socket.write(JSON.stringify({ type: 'error', id: frame.id, code: verdict.error.code, message: verdict.error.message }) + '\n');
		} else {
			socket.write(JSON.stringify({ type: 'result', id: frame.id, items: verdict.items }) + '\n');
		}
	}
});

socket.on('error', (err) => {
	console.error(`[fixture-echo] socket error: ${err.message}`);
	process.exit(1);
});
