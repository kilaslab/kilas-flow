'use strict';

// A file whose peer dependency is not installed. Requiring it fails with
// MODULE_NOT_FOUND at load time, which must exclude this node with a named
// error and leave the rest of the package (and every other package) loadable:
// that is the per-file failure policy the ticket fixes.

const absentPeer = require('kf-absent-peer');

Object.defineProperty(exports, '__esModule', { value: true });
exports.Trigger = void 0;

class Trigger {
	constructor() {
		this.description = {
			displayName: 'Fixture Missing Peer',
			name: 'fixtureMissingPeer',
			group: ['trigger'],
			version: 1,
			defaults: { name: 'Fixture Missing Peer' },
			inputs: [],
			outputs: ['main'],
			properties: [],
		};
		void absentPeer;
	}
}

exports.Trigger = Trigger;
