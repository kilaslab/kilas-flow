import { describe, expect, it } from 'vitest';

import { credentialTypesFor } from './credentials';
import type { Definition } from '$lib/api/generated/models';

function definition(): Definition {
	return {
		category: 'Core',
		displayName: 'Webhook',
		group: [],
		inputs: [],
		outputs: [],
		source: 'test',
		type: 'kilasflow.webhook',
		version: 1,
		credentials: [
			{ type: 'httpBasicAuth', visibleWhen: [{ key: 'authentication', equals: 'basicAuth' }] },
			{ type: 'httpHeaderAuth', visibleWhen: [{ key: 'authentication', equals: 'headerAuth' }] },
			{ type: 'openAiApi' }
		]
	} as Definition;
}

describe('credentialTypesFor', () => {
	it('shows only the credential matching the chosen auth mode', () => {
		expect(credentialTypesFor(definition(), { authentication: 'basicAuth' })).toEqual(['httpBasicAuth', 'openAiApi']);
		expect(credentialTypesFor(definition(), { authentication: 'headerAuth' })).toEqual(['httpHeaderAuth', 'openAiApi']);
	});

	it('hides both gated credentials while authentication is none', () => {
		expect(credentialTypesFor(definition(), { authentication: 'none' })).toEqual(['openAiApi']);
	});

	it('lists everything when no parameters are given', () => {
		expect(credentialTypesFor(definition())).toEqual(['httpBasicAuth', 'httpHeaderAuth', 'openAiApi']);
	});
});
