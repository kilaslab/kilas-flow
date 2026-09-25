import { describe, expect, it } from 'vitest';

import { defaultScope } from './credential-scope';

describe('what an empty allowed-hosts list means', () => {
	it('names the fixed hosts a provider credential is confined to', () => {
		expect(defaultScope({ defaultDomains: ['api.openai.com'], fields: [] })).toEqual({
			kind: 'hosts',
			hosts: ['api.openai.com']
		});
	});

	// A Bot API server may be Telegram's own or a local one, so the default
	// is whatever host the credential's base URL names — shown by the label
	// the author sees on the form.
	it('names the field whose host is the default for a self-addressed type', () => {
		expect(
			defaultScope({
				defaultDomainsFrom: 'baseUrl',
				fields: [{ key: 'baseUrl', label: 'Base URL', required: true, secret: false }]
			})
		).toEqual({ kind: 'derived', field: 'Base URL' });
	});

	it('falls back to the key when the field is not in the definition', () => {
		expect(defaultScope({ defaultDomainsFrom: 'baseUrl', fields: null })).toEqual({ kind: 'derived', field: 'baseUrl' });
	});

	it('means any host for a generic type, and before the catalogue has loaded', () => {
		expect(defaultScope({ defaultDomains: null, fields: [] })).toEqual({ kind: 'any' });
		expect(defaultScope({ defaultDomains: [' '], fields: [] })).toEqual({ kind: 'any' });
		expect(defaultScope(null)).toEqual({ kind: 'any' });
	});
});
