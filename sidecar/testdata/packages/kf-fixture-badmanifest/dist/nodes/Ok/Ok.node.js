'use strict';

// A loadable file in a package whose manifest is refused for other reasons:
// proving that a manifest defect skips the whole package rather than
// half-loading it.

Object.defineProperty(exports, '__esModule', { value: true });
exports.Ok = void 0;

class Ok {
	constructor() {
		this.description = { displayName: 'Ok', name: 'fixtureOk', version: 1, defaults: { name: 'Ok' }, inputs: ['main'], outputs: ['main'], properties: [] };
	}
}

exports.Ok = Ok;
