'use strict';

// Fixture sidecar node: reports the environment it was started with.
//
// The host sets an explicit environment allowlist, so this fixture must see
// LANG and TZ and nothing else — in particular not the credential master key
// or the database DSN. It reports both the key list and any value containing
// the canary the test sets, because a key-only check would miss a leaked
// value hiding under an innocent name.

const net = require('node:net');

const [, socketPath] = process.argv.slice(2);
const canary = 'leak-canary';

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
			continue;
		}
		if (frame.type !== 'execute') continue;
		const keys = Object.keys(process.env).sort();
		const leaked = keys
			.filter((key) => String(process.env[key]).includes(canary))
			.map((key) => `${key}=${process.env[key]}`);
		socket.write(
			JSON.stringify({
				type: 'result',
				id: frame.id,
				items: [{ json: { keys, leaked } }],
			}) + '\n',
		);
	}
});

socket.on('error', () => process.exit(1));
