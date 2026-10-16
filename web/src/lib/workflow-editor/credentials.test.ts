import { describe, expect, it } from 'vitest';

import { ApiError } from '$lib/api/http';
import type { CredentialResource, Definition } from '$lib/api/generated/models';

import { credentialIdsToProbe, credentialPresenceKey, credentialTypesFor, probeCredentials } from './credentials';

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
		parameters: [],
		sharedSettings: [],
		credentials: [
			{ type: 'httpBasicAuth', visibleWhen: [{ key: 'authentication', equals: 'basicAuth' }] },
			{ type: 'httpHeaderAuth', visibleWhen: [{ key: 'authentication', equals: 'headerAuth' }] },
			{ type: 'openAiApi' }
		]
	};
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

describe('probeCredentials', () => {
	const found = { status: 200, data: { id: 'cred_here' } as CredentialResource, headers: new Headers() };

	it('calls a credential deleted only when the server answers 404 for that id', async () => {
		const read = async (id: string) => {
			if (id === 'cred_gone') throw new ApiError(404, 'credential not found');
			return found;
		};

		expect(await probeCredentials(['cred_here', 'cred_gone'], read)).toEqual({ cred_here: 'present', cred_gone: 'deleted' });
	});

	it('never reads a refusal or an outage as a deletion', async () => {
		// An embed session is refused this endpoint outright (403), and its
		// credential listing is confined, so neither "not listed" nor "refused"
		// says the credential is gone.
		const refusals = [new ApiError(403, 'This embed session cannot use this endpoint.'), new ApiError(503, 'credential storage unavailable'), new ApiError(401, 'sign in'), new TypeError('network down')];
		for (const failure of refusals) {
			const read = async () => {
				throw failure;
			};
			expect(await probeCredentials(['cred_x'], read)).toEqual({ cred_x: 'unknown' });
		}
	});

	it('keys the answer by the ids probed, in a stable order', () => {
		expect(credentialPresenceKey(['b', 'a'])).toEqual(credentialPresenceKey(['a', 'b']));
	});
});

describe('credentialIdsToProbe', () => {
	const listed = [{ id: 'cred_live' }];
	const base = { types: ['httpHeaderAuth'], attached: { httpHeaderAuth: 'cred_far' }, listed, shown: true, listLoaded: true };

	it('probes an attached id the list does not hold', () => {
		expect(credentialIdsToProbe(base)).toEqual(['cred_far']);
	});

	it('does not probe an id the list already holds', () => {
		expect(credentialIdsToProbe({ ...base, attached: { httpHeaderAuth: 'cred_live' } })).toEqual([]);
	});

	it('does not probe the id of a credential type the panel does not render', () => {
		// httpBasicAuth is hidden while Authentication is not basicAuth.
		const attached = { httpHeaderAuth: 'cred_far', httpBasicAuth: 'cred_hidden' };
		expect(credentialIdsToProbe({ ...base, attached })).toEqual(['cred_far']);
	});

	it('probes an id shared by two rendered types once', () => {
		const attached = { httpHeaderAuth: 'cred_far', openAiApi: 'cred_far' };
		expect(credentialIdsToProbe({ ...base, types: ['httpHeaderAuth', 'openAiApi'], attached })).toEqual(['cred_far']);
	});

	it('probes nothing while the credential section is not shown, or before the list has loaded', () => {
		expect(credentialIdsToProbe({ ...base, shown: false })).toEqual([]);
		expect(credentialIdsToProbe({ ...base, listLoaded: false })).toEqual([]);
	});

	it('ignores an empty reference and a node with no credentials', () => {
		expect(credentialIdsToProbe({ ...base, attached: { httpHeaderAuth: '' } })).toEqual([]);
		expect(credentialIdsToProbe({ ...base, attached: undefined })).toEqual([]);
	});
});

describe('probeCredentials cancellation', () => {
	it('hands the query signal to every read', async () => {
		const controller = new AbortController();
		const seen: (AbortSignal | undefined)[] = [];
		await probeCredentials(['a', 'b'], async (_id, signal) => void seen.push(signal), controller.signal);
		expect(seen).toEqual([controller.signal, controller.signal]);
	});
});
