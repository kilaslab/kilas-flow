'use strict';

// The relay fixture calls this.helpers.httpRequest exactly the way the owner's
// real nodes do: a method, a URL built from the credential base URL, a
// credential header, a JSON body and `json: true`. The request itself never
// leaves the process — the runner forwards it to the host as a host call, and
// the test answers it with a fake HostHandler, which is how the egress policy
// is proved to be the host's decision and not the package's.

Object.defineProperty(exports, '__esModule', { value: true });
exports.Relay = void 0;

class Relay {
	constructor() {
		this.description = {
			displayName: 'Fixture Relay',
			name: 'fixtureRelay',
			icon: 'file:Relay.svg',
			group: ['output'],
			version: 1,
			description: 'Sends every input item through the host HTTP helper',
			defaults: { name: 'Fixture Relay' },
			inputs: ['main'],
			outputs: ['main'],
			credentials: [{ name: 'fixtureApi', required: true }],
			properties: [
				{
					displayName: 'Path',
					name: 'path',
					type: 'string',
					default: '/echo',
					required: true,
				},
			],
		};
	}

	async execute() {
		const items = this.getInputData();
		const credentials = await this.getCredentials('fixtureApi');
		const out = [];
		for (let i = 0; i < items.length; i++) {
			const path = this.getNodeParameter('path', i, '/echo');
			const response = await this.helpers.httpRequest({
				method: 'POST',
				url: `${credentials.baseUrl}${path}`,
				headers: {
					'X-API-Key': credentials.apiKey,
					'Content-Type': 'application/json',
				},
				body: items[i].json,
				json: true,
			});
			out.push({ json: { relayed: response.echoed, transport: 'host' }, pairedItem: { item: i } });
		}
		return [out];
	}
}

exports.Relay = Relay;
