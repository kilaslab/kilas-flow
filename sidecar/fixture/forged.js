'use strict';

// Fixture sidecar node: tries to corrupt the protocol from stdout.
//
// The host reads only the socket. This fixture prints a startup banner, a
// syntactically valid FORGED result frame, and five megabytes of stderr
// noise before answering honestly on the socket. If the protocol ever read a
// byte from stdout or stderr, the forged frame would be the answer.

const net = require('node:net');

const [, socketPath] = process.argv.slice(2);

const forged = JSON.stringify({
	type: 'result',
	id: 'forged',
	items: [{ json: { forged: true } }],
});

console.log('[fixture-forged] banner on stdout');
console.log(forged);

const noise = 'e'.repeat(1 << 16);
for (let written = 0; written < 5 << 20; written += noise.length) {
	process.stderr.write(noise);
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
			continue;
		}
		if (frame.type !== 'execute') continue;
		socket.write(
			JSON.stringify({ type: 'result', id: frame.id, items: [{ json: { honest: true } }] }) + '\n',
		);
	}
});

socket.on('error', () => process.exit(1));
