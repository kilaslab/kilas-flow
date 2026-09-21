'use strict';

// Hand-written CommonJS in the shape the TypeScript compiler emits
// (Object.defineProperty(exports, '__esModule'), a named export, the
// description as an instance property), so the runner is exercised against
// what a real published package looks like rather than against a convenience
// shape of its own.
//
// The property list deliberately covers every shape the converter has to map:
// string, options with static values, an options property that resolves its
// values at run time (loadOptionsMethod, no static options), boolean,
// collection, fixedCollection, a hidden property, displayOptions show/hide,
// noDataExpression and an expression default. The node has two outputs and
// stamps pairedItem on every item.

Object.defineProperty(exports, '__esModule', { value: true });
exports.Greet = void 0;

class Greet {
	constructor() {
		this.description = {
			displayName: 'Fixture Greet',
			name: 'fixtureGreet',
			icon: 'file:Greet.svg',
			group: ['transform'],
			version: 1,
			description: 'Greets every input item using the fixture credential',
			defaults: { name: 'Fixture Greet' },
			inputs: ['main'],
			outputs: ['main', 'main'],
			credentials: [{ name: 'fixtureApi', required: true }],
			properties: [
				{
					displayName: 'Name',
					name: 'name',
					type: 'string',
					default: '={{ $json.name }}',
					required: true,
					description: 'The name to greet',
				},
				{
					displayName: 'Mode',
					name: 'mode',
					type: 'options',
					options: [
						{ name: 'Plain', value: 'plain' },
						{ name: 'Shout', value: 'shout' },
					],
					default: 'plain',
				},
				{
					displayName: 'Provider',
					name: 'providerId',
					type: 'options',
					typeOptions: { loadOptionsMethod: 'getProviders' },
					default: '',
					required: true,
					description: 'Selected at run time, so there are no static options',
				},
				{
					displayName: 'Loud',
					name: 'loud',
					type: 'boolean',
					default: false,
					noDataExpression: true,
				},
				{
					displayName: 'Extras',
					name: 'extras',
					type: 'collection',
					placeholder: 'Add Extra',
					default: {},
					options: [
						{ displayName: 'Suffix', name: 'suffix', type: 'string', default: '' },
						{ displayName: 'Repeat', name: 'repeat', type: 'number', default: 1 },
					],
				},
				{
					displayName: 'Headers',
					name: 'headers',
					type: 'fixedCollection',
					typeOptions: { multipleValues: true },
					default: {},
					options: [
						{
							displayName: 'Header',
							name: 'values',
							values: [
								{ displayName: 'Key', name: 'key', type: 'string', default: '' },
								{ displayName: 'Value', name: 'value', type: 'string', default: '' },
							],
						},
					],
				},
				{
					displayName: 'Attachment Filename',
					name: 'attachmentFilename',
					type: 'string',
					default: '',
					displayOptions: { show: { mode: ['plain'] } },
				},
				{
					displayName: 'Attachment Note',
					name: 'attachmentNote',
					type: 'string',
					default: '',
					displayOptions: { hide: { mode: ['shout'] } },
				},
				{
					displayName: 'Hidden Value',
					name: 'hiddenValue',
					type: 'hidden',
					default: 'kept-out-of-the-editor',
				},
			],
		};
		this.methods = {
			loadOptions: {
				async getProviders() {
					return [
						{ name: 'Fixture', value: 'fixture' },
						{ name: 'Other', value: 'other' },
					];
				},
			},
		};
	}

	async execute() {
		const items = this.getInputData();
		const credentials = await this.getCredentials('fixtureApi');
		const greeted = [];
		for (let i = 0; i < items.length; i++) {
			const name = this.getNodeParameter('name', i);
			const mode = this.getNodeParameter('mode', i, 'plain');
			const loud = this.getNodeParameter('loud', i, false);
			const provider = this.getNodeParameter('providerId', i, '');
			const extras = this.getNodeParameter('extras', i, {});
			const decorated = `${name}${extras.suffix || ''}`;
			const greeting = `${loud || mode === 'shout' ? decorated.toUpperCase() : decorated} via ${credentials.baseUrl}`;
			greeted.push({
				json: {
					greeting,
					provider,
					item: i,
				},
				pairedItem: { item: i },
			});
		}
		return [greeted, [{ json: { count: greeted.length, mode: this.getMode() } }]];
	}
}

exports.Greet = Greet;
