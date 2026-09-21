import { afterEach, describe, expect, it, vi } from 'vitest';

import { apiFetch, setEmbedToken } from '$lib/api/http';
import { SCOPE_PUBLISH, acceptEmbedSession, sanitizeBranding, scopeAllows, type EmbedSession } from './session.svelte';

function session(scopes: string[]): EmbedSession {
	return { token: 't', workflowId: 'wf-1', scopes, branding: {}, locale: 'en', origin: 'https://host.example' };
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

describe('accepting the host session', () => {
	afterEach(() => {
		setEmbedToken(null);
		vi.unstubAllGlobals();
	});

	const message = { type: 'kilasflow:embed-session', token: 'tok-123', workflowId: 'wf-1', scopes: ['workflow:read'] };

	// The regression this covers: the editor mounted on the render the session
	// appears in and fired its first three queries from that mount, which ran
	// before the page-level effect that attached the token — so every one of
	// them answered 401. Attaching the token is therefore part of accepting the
	// message, not something that happens after it.
	it('has the token on the wire by the time the first request is made', async () => {
		const sent: Headers[] = [];
		vi.stubGlobal(
			'fetch',
			vi.fn(async (_url: string, init?: RequestInit) => {
				sent.push(new Headers(init?.headers));
				return new Response('{}', { status: 200, headers: { 'content-type': 'application/json' } });
			})
		);

		const accepted = acceptEmbedSession(message, 'wf-1', 'https://host.example');
		expect('session' in accepted).toBe(true);

		await apiFetch('/api/v1/workflows/wf-1');

		expect(sent[0]?.get('X-KilasFlow-Embed')).toBe('tok-123');
	});

	it('accepts a session that names no workflow of its own', () => {
		const accepted = acceptEmbedSession({ type: 'kilasflow:embed-session', token: 'tok', scopes: ['workflow:run'] }, 'wf-9', 'https://host.example');

		expect('session' in accepted && accepted.session.workflowId).toBe('wf-9');
	});

	it('keeps the locale the host declared', () => {
		const accepted = acceptEmbedSession({ ...message, locale: 'id' }, 'wf-1', 'https://host.example');

		expect('session' in accepted && accepted.session.locale).toBe('id');
	});

	it('falls back to the base locale for a locale its catalogs do not carry', () => {
		// The host's locale is a preference, not an authorization: a tag this
		// build does not ship, or one of the wrong type, must leave the frame
		// in the base locale rather than refuse the handshake and show the
		// host's user an error instead of the editor.
		for (const declared of [undefined, 'fr', 42, null, {}]) {
			const accepted = acceptEmbedSession({ ...message, locale: declared }, 'wf-1', 'https://host.example');

			expect('session' in accepted && accepted.session.locale).toBe('en');
		}
	});

	it('attaches nothing when the message is refused', () => {
		const refused = [
			acceptEmbedSession(message, 'wf-2', 'https://host.example'),
			acceptEmbedSession({ ...message, token: '' }, 'wf-1', 'https://host.example'),
			acceptEmbedSession({ ...message, scopes: [] }, 'wf-1', 'https://host.example'),
			acceptEmbedSession({ type: 'kilasflow:other', token: 'tok-123' }, 'wf-1', 'https://host.example')
		];

		for (const answer of refused) expect('error' in answer).toBe(true);

		const sent: Headers[] = [];
		vi.stubGlobal(
			'fetch',
			vi.fn(async (_url: string, init?: RequestInit) => {
				sent.push(new Headers(init?.headers));
				return new Response('{}', { status: 200, headers: { 'content-type': 'application/json' } });
			})
		);
		return apiFetch('/api/v1/workflows/wf-1').then(() => {
			expect(sent[0]?.has('X-KilasFlow-Embed')).toBe(false);
		});
	});
});
