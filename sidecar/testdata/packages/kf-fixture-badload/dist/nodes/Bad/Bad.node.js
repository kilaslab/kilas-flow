'use strict';

// Throws while the module is being evaluated. The package is not refused: this
// file's node is excluded with a named error and the rest of the catalogue
// still loads, so one broken node in one package cannot take the boot down.

throw new Error('kf-fixture-badload refuses to load');

/* eslint-disable no-unreachable */
Object.defineProperty(exports, '__esModule', { value: true });
exports.Bad = void 0;

class Bad {
	constructor() {
		this.description = { displayName: 'Bad', name: 'fixtureBad', version: 1, defaults: { name: 'Bad' }, inputs: ['main'], outputs: ['main'], properties: [] };
	}
}

exports.Bad = Bad;
