'use strict';

// Fixture sidecar node: grows its resident set with Buffer allocations, which
// live outside V8's old space and are therefore invisible to
// --max-old-space-size. Only the host's RSS watchdog can stop this.
//
// Allocation is deliberately bounded: chunks of 32 MB every 50 ms with a hard
// 1 GB cap, so the test can never exhaust the machine or a CI runner even if
// the watchdog never fires.

const net = require('node:net');

const [, socketPath] = process.argv.slice(2);
const socket = net.createConnection(socketPath);
socket.setEncoding('utf8');

const chunkBytes = 32 << 20;
const hardCap = 1 << 30;
const held = [];
let total = 0;

const grow = setInterval(() => {
	if (total >= hardCap) {
		clearInterval(grow);
		return;
	}
	held.push(Buffer.alloc(chunkBytes, 7));
	total += chunkBytes;
}, 50);

let buffer = '';
socket.on('data', (chunk) => {
	buffer += chunk;
	if (!buffer.includes('\n')) return;
	// Hold the run open; the watchdog is the only exit.
	grow.ref();
});

socket.on('error', () => process.exit(1));
