import { getMe } from '$lib/api/generated/auth/auth';
import type { PrincipalResource } from '$lib/api/generated/models';
import { getHealth } from '$lib/api/generated/system/system';
import { isRecord } from '$lib/type-guards';

/**
 * Whether this instance requires a credential, and who is signed in.
 *
 * The SPA used to learn "auth is off" by calling /auth/me and reading the 401,
 * so every page of a default install logged a failed request (BUG-719gaz). The
 * public health probe now says it outright (`authEnabled`), and /auth/me is
 * only asked once the probe says there is someone to ask about.
 */

/** The part of a generated-client response this module reads. */
type Answer = { status: number; data: unknown };

export interface AuthModeDeps {
	health: () => Promise<Answer>;
	me: () => Promise<Answer>;
}

export interface AuthMode {
	/** Read once per page load: the server's mode cannot change under a running SPA. */
	authEnabled(): Promise<boolean>;
	/** The signed-in principal, or null when auth is off or nobody is signed in. */
	principal(): Promise<PrincipalResource | null>;
}

export function createAuthMode(deps: AuthModeDeps): AuthMode {
	let known: Promise<boolean> | null = null;

	async function probe(): Promise<boolean> {
		try {
			const response = await deps.health();
			const body = response.data;
			if (response.status === 200 && isRecord(body) && typeof body.authEnabled === 'boolean') {
				return body.authEnabled;
			}
		} catch {
			// Falls through to the conservative answer below.
		}
		// An unreadable probe answers "on": the sign-in flow stays reachable,
		// and the next call asks again instead of keeping the guess.
		known = null;
		return true;
	}

	function authEnabled(): Promise<boolean> {
		known ??= probe();
		return known;
	}

	async function principal(): Promise<PrincipalResource | null> {
		if (!(await authEnabled())) return null;
		try {
			const response = await deps.me();
			return response.status === 200 && isRecord(response.data)
				? (response.data as unknown as PrincipalResource)
				: null;
		} catch {
			return null;
		}
	}

	return { authEnabled, principal };
}

/** The app-wide instance, backed by the generated client. */
export const authMode = createAuthMode({ health: () => getHealth(), me: () => getMe() });

/**
 * Where a sign-in, or a skipped one, lands: the `next` path when it stays
 * inside the app, the workflows list otherwise. A protocol-relative `//host`
 * would leave the app, so it is refused like an absolute URL.
 */
export function safeNext(next: string | null): string {
	return next && next.startsWith('/') && !next.startsWith('//') ? next : '/app/workflows';
}
