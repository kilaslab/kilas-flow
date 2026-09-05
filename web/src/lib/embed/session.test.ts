import { describe, expect, it } from 'vitest';

import { SCOPE_PUBLISH, sanitizeBranding, scopeAllows, type EmbedSession } from './session.svelte';

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

	it('lets an ordinary session browse and diff history on its read scope', () => {
		expect(scopeAllows(session(['workflow:read']), 'workflow:read')).toBe(true);
	});

	it('needs the write scope to restore a revision', () => {
		expect(scopeAllows(session(['workflow:read']), 'workflow:write')).toBe(false);
		expect(scopeAllows(session(['workflow:write']), 'workflow:write')).toBe(true);
	});

	it('refuses publishing to every scope a host can mint today', () => {
		// Activation publishes a webhook endpoint for the whole deployment, so
		// it is an owner action. A host that wants its customers to publish has
		// to be granted the publish scope explicitly, and no combination of the
		// three scopes the server currently accepts adds up to it.
		for (const scopes of [['workflow:read'], ['workflow:write'], ['workflow:run'], ['workflow:read', 'workflow:write', 'workflow:run']]) {
			expect(scopeAllows(session(scopes), SCOPE_PUBLISH)).toBe(false);
		}
	});

	it('permits publishing only to a session minted with the publish scope', () => {
		expect(scopeAllows(session([SCOPE_PUBLISH]), SCOPE_PUBLISH)).toBe(true);
	});

	it('lets the publish scope imply read, the way write and run already do', () => {
		expect(scopeAllows(session([SCOPE_PUBLISH]), 'workflow:read')).toBe(true);
	});

	it('does not let the publish scope imply write', () => {
		// Publishing an existing revision and rewriting the canvas are separate
		// grants: a host may want the first without the second.
		expect(scopeAllows(session([SCOPE_PUBLISH]), 'workflow:write')).toBe(false);
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
