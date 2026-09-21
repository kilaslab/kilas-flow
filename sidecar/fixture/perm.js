'use strict';

// Fixture sidecar node: tries the things the permission model forbids.
//
// Usage: node perm.js <socketPath> <hostPath> <writePath>
//
// It reports, per attempt, the error code the runtime gave it. The host's
// test asserts each one was denied with ERR_ACCESS_DENIED: spawning a child
// process, reading a file outside the fs-read grant, and writing anywhere.

const net = require('node:net');
const fs = require('node:fs');
const childProcess = require('node:child_process');

const [, socketPath, hostPath, writePath] = process.argv.slice(2);

function attempt(name, action) {
	try {
		action();
		return { name, result: 'allowed' };
	} catch (error) {
		return { name, result: (error && error.code) || String((error && error.message) || error) };
	}
}

const attempts = [
	attempt('child_process', () => childProcess.execSync('true')),
	attempt('fs_read', () => fs.readFileSync(hostPath)),
	attempt('fs_write', () => fs.writeFileSync(writePath, 'x')),
	attempt('process_binding', () => process.binding('fs')),
];

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
			JSON.stringify({ type: 'result', id: frame.id, items: [{ json: { attempts } }] }) + '\n',
		);
	}
});

socket.on('error', () => process.exit(1));
