import { render } from 'svelte/server';
import { describe, expect, it } from 'vitest';

import type { WebhookAddress } from '$lib/workflow-editor/webhook-address';

import WebhookAddressView from './webhook-address.svelte';

/**
 * Each answer the panel can be in, rendered on its own.
 *
 * `properties-panel.test.ts` covers how the panel reaches these; this pins
 * what each one says, including the ones the panel test cannot seed a cache
 * for. SSR needs no DOM, as in `property-field.test.ts`.
 */
function html(address: WebhookAddress, onRetry?: () => void): string {
	const rendered = render(WebhookAddressView, { props: { address, onRetry } });
	return rendered.body.replace(/<!--.*?-->/g, '');
}

const ABSENT = ['needs-path', 'needs-save', 'loading', 'unavailable', 'unbound'] as const;

describe('a ready address', () => {
	const ready: WebhookAddress = { kind: 'ready', url: 'https://flow.example.com/webhook/abc123', live: true };

	it('renders the URL as selectable text beside a copy button', () => {
		const markup = html(ready);

		expect(markup).toContain('data-testid="webhook-url"');
		expect(markup).toContain('>https://flow.example.com/webhook/abc123<');
		expect(markup).toContain('select-all');
		expect(markup).toContain('Copy URL');
	});

	it('says whether it answers yet', () => {
		expect(html(ready)).toContain('Live');
		expect(html({ ...ready, live: false })).toContain('Not live yet');
		expect(html({ ...ready, live: false })).toContain('activated');
	});
});

describe('an address that is not there', () => {
	it('never renders a URL or a copy button', () => {
		for (const kind of ABSENT) {
			const markup = html({ kind });

			expect(markup).not.toContain('data-testid="webhook-url"');
			expect(markup).not.toContain('Copy URL');
			expect(markup).not.toContain('/webhook/');
		}
	});

	it('gives each state its own sentence', () => {
		const sentences = ABSENT.map((kind) => html({ kind }).replace(/<[^>]*>/g, '').trim());

		expect(new Set(sentences).size).toBe(sentences.length);
	});

	it('offers a retry after a failed lookup, and only then', () => {
		expect(html({ kind: 'unavailable' }, () => {})).toContain('Try again');
		expect(html({ kind: 'unavailable' })).not.toContain('Try again');
		expect(html({ kind: 'unbound' }, () => {})).not.toContain('Try again');
	});
});
