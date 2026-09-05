import { describe, expect, it } from 'vitest';

import { sanitizeBranding, scopeAllows, type EmbedSession } from './session.svelte';

function session(scopes: string[]): EmbedSession {
	return { token: 't', workflowId: 'wf-1', scopes, branding: {}, origin: 'https://host.example' };
}

describe('scopeAllows', () => {
	it('mirrors the server rule that write and run imply read', () => {
		expect(scopeAllows(session(['workflow:read']), 'workflow:read')).toBe(true);
		expect(scopeAllows(session(['workflow:write']), 'workflow:read')).toBe(true);
		expect(scopeAllows(session(['workflow:run']), 'workflow:read')).toBe(true);
	});

	it('does not let read imply anything else', () => {
		expect(scopeAllows(session(['workflow:read']), 'workflow:write')).toBe(false);
		expect(scopeAllows(session(['workflow:read']), 'workflow:run')).toBe(false);
		// Write must not imply run: saving and executing are separate grants.
		expect(scopeAllows(session(['workflow:write']), 'workflow:run')).toBe(false);
	});

	it('allows nothing without a session', () => {
		expect(scopeAllows(null, 'workflow:read')).toBe(false);
	});
});

describe('sanitizeBranding', () => {
	it('keeps values the frame knows how to render', () => {
		expect(
			sanitizeBranding({ name: 'Acme Flows', logoUrl: 'https://cdn.example/logo.png', accent: '#0ea5e9', hideRun: true })
		).toEqual({
			name: 'Acme Flows',
			logoUrl: 'https://cdn.example/logo.png',
			accent: '#0ea5e9',
			hideRun: true,
			hideSave: false
		});
	});

	it('drops a logo that is not an absolute https URL', () => {
		// A javascript: or data: URL in an img src is the classic way markup
		// injection arrives through a "safe" string field.
		for (const logoUrl of ['javascript:alert(1)', 'data:image/svg+xml,<svg/>', 'http://host.example/l.png', '/l.png', 'not a url']) {
			expect(sanitizeBranding({ logoUrl }).logoUrl).toBeUndefined();
		}
	});

	it('drops a name or accent that could escape its element', () => {
		expect(sanitizeBranding({ name: '<script>alert(1)</script>' }).name).toBeUndefined();
		expect(sanitizeBranding({ accent: 'red; background: url(javascript:alert(1))' }).accent).toBeUndefined();
		expect(sanitizeBranding({ accent: 'expression(alert(1))' }).accent).toBeUndefined();
	});

	it('treats a missing or malformed payload as no branding', () => {
		expect(sanitizeBranding(null)).toEqual({});
		expect(sanitizeBranding('nope')).toEqual({});
		expect(sanitizeBranding(undefined)).toEqual({});
	});

	it('coerces the visibility flags rather than trusting truthiness', () => {
		// A host sending "false" or 0 must not accidentally hide a control.
		expect(sanitizeBranding({ hideRun: 'true' }).hideRun).toBe(false);
		expect(sanitizeBranding({ hideSave: 1 }).hideSave).toBe(false);
		expect(sanitizeBranding({ hideSave: true }).hideSave).toBe(true);
	});
});
