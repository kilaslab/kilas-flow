'use strict';

// Fixture sidecar node: reads its request and exits with a fixed status, so
// the host's diagnostic has to come from the wait status, never from stderr.

const net = require('node:net');

const [, socketPath] = process.argv.slice(2);
const socket = net.createConnection(socketPath);
socket.setEncoding('utf8');

let buffer = '';
socket.on('data', (chunk) => {
	buffer += chunk;
	if (!buffer.includes('\n')) return;
	process.exit(3);
});

socket.on('error', () => process.exit(3));
