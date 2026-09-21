'use strict';

// Fixture credential class: one secret field (typeOptions.password) and one
// plain field, which is the pair the ticket's credential policy turns on —
// every field of a community credential is stored as a secret, so both arrive
// write-only at the API.

Object.defineProperty(exports, '__esModule', { value: true });
exports.FixtureApi = void 0;

class FixtureApi {
	constructor() {
		this.name = 'fixtureApi';
		this.displayName = 'Fixture API';
		this.documentationUrl = 'https://example.invalid/docs/fixture-api';
		this.properties = [
			{
				displayName: 'API Key',
				name: 'apiKey',
				type: 'string',
				typeOptions: { password: true },
				default: '',
				required: true,
			},
			{
				displayName: 'Base URL',
				name: 'baseUrl',
				type: 'string',
				default: 'https://example.invalid',
				required: true,
			},
		];
	}
}

exports.FixtureApi = FixtureApi;
