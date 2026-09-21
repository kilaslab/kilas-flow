'use strict';

// Fixture sidecar node: dials the socket as its first act, with no banner and
// no work in between. Used to prove the listener exists before the child
// starts, on a loaded machine, repeatedly under the race detector.

const net = require('node:net');

const [, socketPath] = process.argv.slice(2);
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
		socket.write(JSON.stringify({ type: 'result', id: frame.id, items: [] }) + '\n');
	}
});

socket.on('error', () => process.exit(1));
