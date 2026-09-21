'use strict';

// Fixture sidecar node: reads its request and then spins forever, so the
// host's wall-clock limit is the only thing that ends the run.

const net = require('node:net');

const [, socketPath] = process.argv.slice(2);
const socket = net.createConnection(socketPath);
socket.setEncoding('utf8');

let buffer = '';
socket.on('data', (chunk) => {
	buffer += chunk;
	if (!buffer.includes('\n')) return;
	// eslint-disable-next-line no-constant-condition
	while (true) {
		// Busy loop: the event loop never runs again, so no socket close and
		// no signal handler can end this politely.
		Math.sqrt(Math.random());
	}
});

socket.on('error', () => {
	// The host closed the socket; still spin, to prove the kill is a kill.
	while (true) Math.sqrt(Math.random());
});
