'use strict';

// Version 2 of fixtureVersioned: a separate file whose class name follows the
// file (FooV2.node.js), sharing the description name with version 1.

Object.defineProperty(exports, '__esModule', { value: true });
exports.FooV2 = void 0;

class FooV2 {
	constructor() {
		this.description = {
			displayName: 'Fixture Versioned Node',
			name: 'fixtureVersioned',
			group: ['transform'],
			version: 2,
			defaults: { name: 'Fixture Versioned Node' },
			inputs: ['main'],
			outputs: ['main'],
			properties: [{ displayName: 'Text', name: 'text', type: 'string', default: '' }],
		};
	}

	async execute() {
		const items = this.getInputData();
		const out = [];
		for (let i = 0; i < items.length; i++) {
			out.push({ json: { version: 2, text: this.getNodeParameter('text', i, '') } });
		}
		return [out];
	}
}

exports.FooV2 = FooV2;
