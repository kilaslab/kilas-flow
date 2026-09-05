/**
 * Embed session handshake.
 *
 * Third-party cookies are unreliable, so the host page mints a session on its
 * backend and hands the token to this iframe over `postMessage`. Every message
 * is checked against `event.origin` before it is read, and every reply names
 * an exact target origin — `'*'` is never used, because it would broadcast the
 * frame's state to whatever page happened to embed it.
 */

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
			error = 'This editor is only available inside a host application.';
			waiting = false;
			return;
		}
		if (!expectedOrigin) {
			error = 'The embedding page could not be identified.';
			waiting = false;
			return;
		}

		function onMessage(event: MessageEvent) {
			// The origin check comes first, before the payload is inspected at
			// all: an attacker-framed page must not be able to reach any of the
			// parsing below.
			if (event.origin !== expectedOrigin) return;
			const data = event.data as Record<string, unknown> | null;
			if (!data || data.type !== MESSAGE_TYPE) return;

			const token = typeof data.token === 'string' ? data.token : '';
			const scopes = Array.isArray(data.scopes) ? data.scopes.filter((s): s is string => typeof s === 'string') : [];
			if (!token || scopes.length === 0) {
				error = 'The host sent an incomplete embed session.';
				waiting = false;
				return;
			}
			// The frame is loaded at /embed/:workflowID, so a token for another
			// workflow is a host mistake worth naming rather than silently
			// letting the server reject every call.
			const tokenWorkflow = typeof data.workflowId === 'string' ? data.workflowId : expectedWorkflow;
			if (tokenWorkflow !== expectedWorkflow) {
				error = 'The embed session is for a different workflow.';
				waiting = false;
				return;
			}

			session = {
				token,
				workflowId: expectedWorkflow,
				scopes,
				branding: sanitizeBranding(data.branding),
				origin: expectedOrigin
			};
			error = null;
			waiting = false;
		}

		window.addEventListener('message', onMessage);
		// The target origin is explicit. '*' would announce readiness to
		// whatever page framed this one.
		window.parent.postMessage({ type: READY_TYPE, workflowId: expectedWorkflow }, expectedOrigin);

		const timeout = setTimeout(() => {
			if (!session) {
				error = 'The host did not provide an embed session.';
				waiting = false;
			}
		}, 10_000);

		return () => {
			window.removeEventListener('message', onMessage);
			clearTimeout(timeout);
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
