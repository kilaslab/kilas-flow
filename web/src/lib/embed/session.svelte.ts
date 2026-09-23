/**
 * Embed session handshake.
 *
 * Third-party cookies are unreliable, so the host page mints a session on its
 * backend and hands the token to this iframe over `postMessage`. Every message
 * is checked against `event.origin` before it is read, and every reply names
 * an exact target origin — `'*'` is never used, because it would broadcast the
 * frame's state to whatever page happened to embed it.
 */

import { setEmbedToken } from '$lib/api/http';
import * as m from '$lib/paraglide/messages.js';
import { baseLocale, isLocale, type Locale } from '$lib/paraglide/runtime.js';
import { setLocale } from '$lib/i18n/locale.svelte';

export type EmbedBranding = {
	name?: string;
	logoUrl?: string;
	accent?: string;
	hideRun?: boolean;
	hideSave?: boolean;
};

export type EmbedSession = {
	token: string;
	workflowId: string;
	scopes: string[];
	branding: EmbedBranding;
	/**
	 * The language the host asked its editor to speak, already checked against
	 * the catalogs this build carries. It is a preference rather than an
	 * authorization, so an absent or unsupported tag resolves to the base
	 * locale instead of failing the handshake.
	 */
	locale: Locale;
	origin: string;
};

const MESSAGE_TYPE = 'kilasflow:embed-session';
const READY_TYPE = 'kilasflow:embed-ready';

/**
 * The parent's origin, taken from `document.referrer`.
 *
 * `window.parent.origin` is unreadable cross-origin, so the referrer is the
 * only origin the frame can learn before the first message. It is treated as a
 * candidate to compare against, never as proof of anything: a message is
 * accepted only when its own `event.origin` matches.
 */
export function parentOrigin(): string {
	try {
		if (!document.referrer) return '';
		return new URL(document.referrer).origin;
	} catch {
		return '';
	}
}

/** True when this document is framed by another page. */
export function isFramed(): boolean {
	try {
		return window.self !== window.top;
	} catch {
		// A cross-origin frame throws on the comparison, which is itself the
		// answer: something is framing us.
		return true;
	}
}

/**
 * Publishing from inside an embedded editor.
 *
 * Activation publishes a webhook endpoint for the whole deployment, which is
 * why `permits` in the embed middleware refuses activate and deactivate to
 * every session. A white-label host that wants its own customers to publish
 * needs to say so explicitly, and this is the scope that would say it.
 *
 * The server does not mint it yet — `normalizeScopes` rejects any scope it
 * does not know by name, and it knows read, write and run — so no session can
 * carry this today and every publish control in the embed stays hidden. That
 * is the intended default rather than an oversight: the gate is written here
 * so the frontend is already correct when the server grants the scope, instead
 * of the two halves landing out of step and briefly offering a button the API
 * refuses.
 */
export const SCOPE_PUBLISH = 'workflow:publish';

export function scopeAllows(session: EmbedSession | null, scope: string): boolean {
	if (!session) return false;
	if (session.scopes.includes(scope)) return true;
	// Write, run and publish all imply read, matching the server's rule.
	if (scope === 'workflow:read') {
		return session.scopes.includes('workflow:write') || session.scopes.includes('workflow:run') || session.scopes.includes(SCOPE_PUBLISH);
	}
	return false;
}

/**
 * Why a session message was refused.
 *
 * `notSessionMessage` marks a message that was not ours at all — a host page
 * may post anything — so the handshake keeps waiting for the real one instead
 * of failing. The rest are genuine refusals of our own message, and `error` is
 * what the frame shows for them.
 */
export type EmbedSessionRefusal = { error: string; notSessionMessage?: true };

/**
 * Accepts the host's session message, or says why it cannot.
 *
 * The token is attached *before* the session is handed back, and that order is
 * the whole point: the editor mounts on the render the session appears in and
 * fires its first three queries from that mount's effects, which run after
 * this function has returned. A page-level `$effect` that called
 * `setEmbedToken` looked equivalent and was not — it ran after its children's,
 * so the workflow, node-catalogue and credential requests all went out without
 * the header and the frame answered 401 to every one of them.
 *
 * The locale is resolved here rather than trusted: `isLocale` is the generated
 * runtime's own check against the catalog list, so the frame has one locale
 * list and no second regex. A host that sends a tag this build does not carry,
 * or sends something that is not a string at all, gets the base locale — its
 * language is a preference, not something worth refusing an editor over.
 */
export function acceptEmbedSession(
	data: Record<string, unknown> | null,
	expectedWorkflow: string,
	origin: string,
	attachToken: (token: string | null, parent: string | null) => void = setEmbedToken
): { session: EmbedSession } | EmbedSessionRefusal {
	if (!data || data.type !== MESSAGE_TYPE) return { error: m.embed_not_session_message(), notSessionMessage: true };

	const token = typeof data.token === 'string' ? data.token : '';
	const scopes = Array.isArray(data.scopes) ? data.scopes.filter((scope): scope is string => typeof scope === 'string') : [];
	if (!token || scopes.length === 0) return { error: m.embed_session_incomplete() };
	// The frame is loaded at /embed/:workflowID, so a token for another workflow
	// is a host mistake worth naming rather than silently letting the server
	// reject every call.
	const tokenWorkflow = typeof data.workflowId === 'string' ? data.workflowId : expectedWorkflow;
	if (tokenWorkflow !== expectedWorkflow) return { error: m.embed_session_other_workflow() };

	const locale = isLocale(typeof data.locale === 'string' ? data.locale : '') ? (data.locale as Locale) : baseLocale;

	// The parent goes with it: the frame's writes carry KilasFlow's origin, and
	// the server accepts that only alongside the host this frame verified.
	attachToken(token, origin);
	return {
		session: {
			token,
			workflowId: expectedWorkflow,
			scopes,
			branding: sanitizeBranding(data.branding),
			locale,
			origin
		}
	};
}

/**
 * Listens for the host's session message.
 *
 * Returns reactive state rather than a promise so the page can render a
 * waiting state, then a denial, then the editor.
 */
export function embedSession(workflowID: () => string) {
	let session = $state<EmbedSession | null>(null);
	let error = $state<string | null>(null);
	let waiting = $state(true);

	$effect(() => {
		const expectedWorkflow = workflowID();
		const expectedOrigin = parentOrigin();

		if (!isFramed()) {
			error = m.embed_host_only();
			waiting = false;
			return;
		}
		if (!expectedOrigin) {
			error = m.embed_page_unidentified();
			waiting = false;
			return;
		}

		function onMessage(event: MessageEvent) {
			// The origin check comes first, before the payload is inspected at
			// all: an attacker-framed page must not be able to reach any of the
			// parsing below.
			if (event.origin !== expectedOrigin) return;

			const accepted = acceptEmbedSession(event.data as Record<string, unknown> | null, expectedWorkflow, expectedOrigin);
			if ('error' in accepted) {
				// A message that is not ours leaves the handshake still waiting
				// for the real one.
				if (accepted.notSessionMessage) return;
				error = accepted.error;
				waiting = false;
				return;
			}

			// Before the session is published, for the same reason the token is
			// attached inside `acceptEmbedSession`: the editor mounts on the
			// render this session appears in, so a locale applied afterwards
			// would paint one language and then swap it.
			setLocale(accepted.session.locale);
			session = accepted.session;
			error = null;
			waiting = false;
		}

		window.addEventListener('message', onMessage);
		// The target origin is explicit. '*' would announce readiness to
		// whatever page framed this one.
		window.parent.postMessage({ type: READY_TYPE, workflowId: expectedWorkflow }, expectedOrigin);

		const timeout = setTimeout(() => {
			if (!session) {
				error = m.embed_no_session_from_host();
				waiting = false;
			}
		}, 10_000);

		return () => {
			window.removeEventListener('message', onMessage);
			clearTimeout(timeout);
			// The frame is gone: nothing may answer with the token afterwards.
			setEmbedToken(null);
		};
	});

	return {
		get session() {
			return session;
		},
		get error() {
			return error;
		},
		get waiting() {
			return waiting;
		}
	};
}

/**
 * Keeps only the branding shapes the frame knows how to render.
 *
 * The server already validates these, but the token arrives through the host's
 * browser, so the frame re-checks rather than trusting whatever the parent
 * chose to send.
 */
export function sanitizeBranding(value: unknown): EmbedBranding {
	if (!value || typeof value !== 'object') return {};
	const raw = value as Record<string, unknown>;
	const branding: EmbedBranding = {};

	if (typeof raw.name === 'string' && /^[\p{L}\p{N} .,'&()\-_]{1,60}$/u.test(raw.name)) {
		branding.name = raw.name;
	}
	if (typeof raw.logoUrl === 'string') {
		try {
			const url = new URL(raw.logoUrl);
			// Only https. A javascript: or data: URL in an img src is the
			// classic way markup injection arrives through a "safe" field.
			if (url.protocol === 'https:') branding.logoUrl = url.toString();
		} catch {
			// Not a URL; drop it.
		}
	}
	if (typeof raw.accent === 'string' && /^(#[0-9a-fA-F]{3,8}|oklch\([0-9a-zA-Z%.\s/]+\)|rgb\([0-9,\s%.]+\)|[a-zA-Z]{3,20})$/.test(raw.accent)) {
		branding.accent = raw.accent;
	}
	branding.hideRun = raw.hideRun === true;
	branding.hideSave = raw.hideSave === true;
	return branding;
}
