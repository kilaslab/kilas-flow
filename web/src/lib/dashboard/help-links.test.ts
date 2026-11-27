import { describe, expect, it } from 'vitest';

import { CHANGELOG_URL, helpLinks } from './help-links';

describe('the Help menu links', () => {
	it('points the API reference and the OpenAPI document at the server pages', () => {
		const byKey = Object.fromEntries(helpLinks.map((link) => [link.key, link]));

		expect(byKey['api-reference'].href).toBe('/docs');
		expect(byKey['openapi'].href).toBe('/api/openapi.json');
	});

	it('opens only the off-site changelog in a new tab', () => {
		expect(helpLinks.filter((link) => link.newTab).map((link) => link.key)).toEqual(['whats-new']);
		expect(helpLinks.find((link) => link.key === 'whats-new')?.href).toBe(CHANGELOG_URL);
		expect(new URL(CHANGELOG_URL).pathname).toMatch(/\/CHANGELOG\.md$/);
	});

	it('gives every link a visible label', () => {
		for (const link of helpLinks) expect(link.label().trim()).not.toBe('');
	});
});
