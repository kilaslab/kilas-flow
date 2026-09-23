'use strict';

// Version 1 of a node that shares its name with a version 2 in another file,
// the way the owner's package ships two of its nodes at
// versions 1 and 2 under one name. The runner keys its class table by
// (name, version) and dispatches on both.

Object.defineProperty(exports, '__esModule', { value: true });
exports.Foo = void 0;

class Foo {
	constructor() {
		this.description = {
			displayName: 'Fixture Versioned Node',
			name: 'fixtureVersioned',
			group: ['transform'],
			version: 1,
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
			out.push({ json: { version: 1, text: this.getNodeParameter('text', i, '') } });
		}
		return [out];
	}
}

exports.Foo = Foo;
