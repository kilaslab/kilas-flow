'use strict';

// Opens a socket while the module is being evaluated, before any run exists.
// Loading is exactly as untrusted as running: the guard is installed before
// the first require, so this is a fatal load error naming the package rather
// than a package that quietly phones home at boot.

require('node:net').connect(9, '127.0.0.1');

Object.defineProperty(exports, '__esModule', { value: true });
exports.Net = void 0;

class Net {
	constructor() {
		this.description = { displayName: 'Net', name: 'fixtureNet', version: 1, defaults: { name: 'Net' }, inputs: ['main'], outputs: ['main'], properties: [] };
	}
}

exports.Net = Net;
