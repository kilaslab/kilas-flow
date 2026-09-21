'use strict';

// One node carrying every shape the converter refuses to half-map:
//
//   inputs: ['ai_tool']        a non-main connection type
//   resourceLocator            a property kind v1 does not model
//   numeric option values      an option whose value is not a string
//   an unknown credential      neither package-declared nor a builtin
//
// The node itself loads: the runner's catalogue is a faithful record of what
// the package declares, and the conversion stage is where an unsupported
// shape becomes a named exclusion.

Object.defineProperty(exports, '__esModule', { value: true });
exports.Distinct = void 0;

class Distinct {
	constructor() {
		this.description = {
			displayName: 'Fixture Distinct',
			name: 'fixtureDistinct',
			group: ['transform'],
			version: 1,
			defaults: { name: 'Fixture Distinct' },
			inputs: ['ai_tool'],
			outputs: ['main'],
			credentials: [{ name: 'fixtureUnknownCredential', required: true }],
			properties: [
				{
					displayName: 'Document',
					name: 'documentId',
					type: 'resourceLocator',
					default: { mode: 'list', value: '' },
					modes: [{ displayName: 'From List', name: 'list', type: 'list', typeOptions: { searchListMethod: 'getDocuments' } }],
				},
				{
					displayName: 'Precision',
					name: 'precision',
					type: 'options',
					options: [
						{ name: 'Low', value: 1 },
						{ name: 'High', value: 2 },
					],
					default: 1,
				},
			],
		};
	}

	async execute() {
		const items = this.getInputData();
		return [items.map((item) => ({ json: item.json }))];
	}
}

exports.Distinct = Distinct;
