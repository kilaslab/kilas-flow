'use strict';

// Every escape a package can attempt, selected by the `mode` parameter so one
// file covers the whole matrix. The runner's answer is what the tests pin:
//
//   net/http/https/fetch/dns/dgram/websocket  -> the guard refuses the route
//   signal                                    -> process.kill of another pid
//   child-process, addon                      -> Node's permission model
//   fs-outside                                -> Node's fs-read grant
//   env                                       -> the child environment is empty
//   crash, hang, heap, buffer                 -> the host's limits, not the guard
//
// With `swallow` set, the route swallows its own exception. The run must still
// fail: the guard's record, not the exception, is the evidence.
//
// The network routes are attempts against a closed port on the loopback
// interface. They never need a live server: the guard refuses them before any
// socket is opened, which is the point.

Object.defineProperty(exports, '__esModule', { value: true });
exports.Hostile = void 0;

const ROUTES = {
	net: () => {
		require('node:net').connect(9, '127.0.0.1');
	},
	http: () => {
		require('node:http').get('http://127.0.0.1:9/').on('error', () => {});
	},
	https: () => {
		require('node:https').get('https://127.0.0.1:9/').on('error', () => {});
	},
	fetch: () => {
		globalThis.fetch('http://127.0.0.1:9/').catch(() => {});
	},
	dns: () => {
		require('node:dns').resolve4('localhost', () => {});
	},
	dgram: () => {
		const socket = require('node:dgram').createSocket('udp4');
		socket.send(Buffer.from('x'), 9, '127.0.0.1');
	},
	websocket: () => {
		const socket = new globalThis.WebSocket('ws://127.0.0.1:9/');
		socket.onerror = () => {};
	},
	signal: (context) => {
		process.kill(context.victimPid, 'SIGTERM');
	},
	'child-process': () => {
		require('node:child_process').spawnSync('echo', ['fixture']);
	},
	addon: () => {
		process.dlopen({ exports: {} }, '/nonexistent-fixture-addon.node');
	},
	'fs-outside': () => {
		require('node:fs').readFileSync('/etc/hosts', 'utf8');
	},
	env: () => [{ json: { env: Object.assign({}, process.env) } }],
	crash: () => {
		process.exit(7);
	},
	hang: () => {
		// Never answers and never blocks the event loop: the host's deadline
		// is what ends this run, and the pool can still reach the socket.
		return new Promise(() => {});
	},
	heap: () => {
		// A bounded allocation that crosses a small heap ceiling: arrays of
		// short strings are ordinary heap objects, and the loop stops at a
		// fixed count so an uncapped run cannot put the machine under
		// pressure either.
		const held = [];
		for (let i = 0; i < 2048; i++) held.push(new Array(4096).fill(String(i)));
		return [{ json: { held: held.length } }];
	},
	buffer: async () => {
		// 192 MB of external memory, which --max-old-space-size does not
		// bound, held across several watchdog samples: an allocation that
		// finished between two polls would look like a passing run. Bounded,
		// so a broken watchdog fails the test instead of the machine.
		const held = [];
		for (let i = 0; i < 24; i++) held.push(Buffer.alloc(8 * 1024 * 1024, 7));
		for (let i = 0; i < 20; i++) await new Promise((resolve) => setTimeout(resolve, 100));
		return [{ json: { held: held.length } }];
	},
	'unsupported-helper': function () {
		this.helpers.httpRequestWithAuthentication({ url: 'https://example.invalid' });
	},
	'unsupported-this': function () {
		this.someMemberThisSidecarDoesNotImplement();
	},
	'unsupported-http': function () {
		this.helpers.httpRequest({ url: 'https://example.invalid', skipSslCertificateValidation: true });
	},
};

class Hostile {
	constructor() {
		this.description = {
			displayName: 'Fixture Hostile',
			name: 'fixtureHostile',
			group: ['transform'],
			version: 1,
			description: 'Tries to leave the sidecar, one escape at a time',
			defaults: { name: 'Fixture Hostile' },
			inputs: ['main'],
			outputs: ['main'],
			properties: [
				{ displayName: 'Mode', name: 'mode', type: 'string', default: 'net' },
				{ displayName: 'Swallow', name: 'swallow', type: 'boolean', default: false },
				{ displayName: 'Victim PID', name: 'victimPid', type: 'number', default: 0 },
			],
		};
	}

	async execute() {
		this.getInputData();
		const mode = this.getNodeParameter('mode', 0, 'net');
		const swallow = this.getNodeParameter('swallow', 0, false);
		const victimPid = this.getNodeParameter('victimPid', 0, 0);
		const route = ROUTES[mode];
		if (!route) throw new Error(`unknown hostile fixture mode: ${mode}`);

		if (swallow) {
			let produced = [];
			try {
				produced = (await route.call(this, { victimPid })) || [];
			} catch {
				produced = [];
			}
			return [produced.concat([{ json: { mode, swallowed: true } }])];
		}
		const produced = (await route.call(this, { victimPid })) || [];
		return [produced];
	}
}

exports.Hostile = Hostile;
