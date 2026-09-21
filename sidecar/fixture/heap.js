'use strict';

// Fixture sidecar node: allocates on the V8 heap until it aborts, which is
// how a heap OOM exits (SIGABRT, status 134) — not a signal the host sent.

const net = require('node:net');

const [, socketPath] = process.argv.slice(2);
const socket = net.createConnection(socketPath);
socket.setEncoding('utf8');

let buffer = '';
socket.on('data', (chunk) => {
	buffer += chunk;
	if (!buffer.includes('\n')) return;
	const held = [];
	for (;;) {
		held.push(new Array(1 << 20).fill(1));
	}
});

socket.on('error', () => {
	const held = [];
	for (;;) {
		held.push(new Array(1 << 20).fill(1));
	}
});
