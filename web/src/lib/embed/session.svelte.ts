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
	/**
	 * When this token stops working, as epoch milliseconds. Null when the host
	 * did not say — a session message without `expiresAt` cannot be warned
	 * about, only refused once the server has refused it.
	 */
	expiresAt: number | null;
};

/**
 * The postMessage protocol this frame speaks.
 *
 * 1 is the original handshake: `embed-ready` answered by one
 * `embed-session`, then editor events only. 2 adds the token refresh without
 * reload (`expiresAt`, a repeat `embed-session`), the `session-expiring`,
 * `unauthorized`, `dirty-changed` and `error` events, and the host commands
 * with their `command-result` replies. The frame announces its version with
 * `embed-ready` and ignores unknown fields on every message it reads, so an
 * older host keeps working against a newer frame.
 */
export const EMBED_PROTOCOL_VERSION = 2;

/** How long before expiry the host is told to refresh, in milliseconds. */
export const EXPIRY_NOTICE_MS = 60_000;

/**
 * When to warn the host that this session is running out.
 *
 * Pure on purpose: the effect that owns the timer only posts the message, so
 * the arithmetic is testable without a component tree. A session already
 * inside the warning window (or past its expiry) warns immediately — the host
 * still has a refresh to do, whenever it starts.
 */
export function expiryNoticePlan(expiresAt: number, now: number): { delayMs: number } | null {
	if (!Number.isFinite(expiresAt)) return null;
	const delayMs = expiresAt - EXPIRY_NOTICE_MS - now;
	return { delayMs: Math.max(0, delayMs) };
}

/** Reads the host's `expiresAt` (an ISO timestamp) as epoch milliseconds. */
export function parseExpiry(value: unknown): number | null {
	if (typeof value !== 'string' || value === '') return null;
	const parsed = Date.parse(value);
	return Number.isNaN(parsed) ? null : parsed;
}

const MESSAGE_TYPE = 'kilasflow:embed-session';
const READY_TYPE = 'kilasflow:embed-ready';
const EXPIRING_TYPE = 'kilasflow:session-expiring';

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
 * Publishing pins a revision as the version production traffic runs, so it is
 * not part of `workflow:write`: a white-label host that wants its own
 * customers to publish mints this scope on purpose, and the server refuses a
 * publish from any session without it (`permits` in the embed middleware).
 */
export const SCOPE_PUBLISH = 'workflow:publish';

/**
 * Activating and deactivating from inside an embedded editor.
 *
 * Activation registers the workflow's triggers — a webhook endpoint for the
 * whole deployment among them — so it is an owner action unless the host mints
 * this scope. With it, the request reaches the same handler the dashboard's
 * does and meets the same compiler. Neither this nor publish implies the
 * other.
 */
export const SCOPE_ACTIVATE = 'workflow:activate';

/**
 * The scopes that carry `workflow:read` with them, mirroring `impliesRead` in
 * internal/embed/embed.go so the two halves agree on what a session can load.
 */
const IMPLIES_READ = ['workflow:write', 'workflow:run', SCOPE_PUBLISH, SCOPE_ACTIVATE];

export function scopeAllows(session: EmbedSession | null, scope: string): boolean {
	if (!session) return false;
	if (session.scopes.includes(scope)) return true;
	if (scope === 'workflow:read') {
		return IMPLIES_READ.some((held) => session.scopes.includes(held));
	}
	return false;
}

/** Which of the editor's controls a session can use. */
export type EmbedControls = {
	canRead: boolean;
	canWrite: boolean;
	canRun: boolean;
	canPublish: boolean;
	canActivate: boolean;
};

/**
 * The editor's controls, each on its own scope.
 *
 * These decide only what is worth showing. The server's embed middleware
 * draws the same lines and is what actually refuses a request, so a control
 * shown by mistake costs a refusal rather than an authority.
 */
export function embedControls(session: EmbedSession | null): EmbedControls {
	return {
		canRead: scopeAllows(session, 'workflow:read'),
		canWrite: scopeAllows(session, 'workflow:write'),
		canRun: scopeAllows(session, 'workflow:run'),
		canPublish: scopeAllows(session, SCOPE_PUBLISH),
		canActivate: scopeAllows(session, SCOPE_ACTIVATE)
	};
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
			origin,
			expiresAt: parseExpiry(data.expiresAt)
		}
	};
}

/**
 * Listens for the host's session message.
 *
 * Returns reactive state rather than a promise so the page can render a
 * waiting state, then a denial, then the editor.
 *
 * The listener stays up for the frame's whole life, so a second session
 * message — a host refreshing an expiring token over protocol v2 — swaps the
 * token in place: `session` changes identity, the token is re-attached, and
 * the editor (which is not keyed on the session object) keeps its canvas,
 * selection and viewport. Only the host can mint a replacement, which is why
 * the frame warns it with `kilasflow:session-expiring` instead of reloading.
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
		// whatever page framed this one. The protocol version rides along so
		// the host knows which half of the vocabulary it may use.
		window.parent.postMessage({ type: READY_TYPE, workflowId: expectedWorkflow, protocol: EMBED_PROTOCOL_VERSION }, expectedOrigin);

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

	// Warns the host while it can still do something about it: sixty seconds
	// before the token expires, the frame posts `session-expiring` so the host
	// can mint a replacement and `setSession` it over the same channel. A
	// session that arrives already inside the window warns at once, and a
	// refresh reschedules the timer for the new expiry.
	$effect(() => {
		const current = session;
		const expiresAt = current?.expiresAt;
		if (!current || expiresAt === null || expiresAt === undefined) return;
		const plan = expiryNoticePlan(expiresAt, Date.now());
		if (!plan) return;
		const origin = current.origin;
		const workflowId = current.workflowId;
		const timer = setTimeout(() => {
			window.parent.postMessage(
				{ type: EXPIRING_TYPE, protocol: EMBED_PROTOCOL_VERSION, workflowId, expiresAt: new Date(expiresAt).toISOString() },
				origin
			);
		}, plan.delayMs);
		return () => clearTimeout(timer);
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
