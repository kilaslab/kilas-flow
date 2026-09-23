import * as m from '$lib/paraglide/messages.js';

/**
 * The only HTTP transport used by generated API clients.
 *
 * KilasFlow is intentionally same-origin in development, production, and
 * embeds. Rejecting an absolute URL keeps generated clients from silently
 * bypassing that boundary.
 */

export interface ProblemDetail {
	title: string;
	status: number;
	detail?: string;
	instance?: string;
	errors?: Array<{ message: string; location?: string; value?: unknown }>;
}

export class ApiError extends Error {
	readonly status: number;
	readonly problem: ProblemDetail | undefined;

	constructor(status: number, message: string, problem?: ProblemDetail) {
		super(message);
		this.name = 'ApiError';
		this.status = status;
		this.problem = problem;
	}
}

/**
 * Orval detects this named mutator export and applies it as the generated
 * TanStack Query error type. HTTP failures always throw ApiError rather than
 * a success/error response body from the OpenAPI schema.
 */
export type ErrorType<_ResponseError = unknown> = ApiError;

export interface ApiFetchOptions extends Omit<RequestInit, 'body'> {
	body?: unknown;
}

let embedToken: string | null = null;
let embedParent: string | null = null;

/**
 * Sets the embed token attached to every subsequent API request, and the
 * origin of the host page it was handed over by.
 *
 * The embedded editor calls this once the host's postMessage handshake has
 * completed; the internal dashboard never calls it, so its requests are
 * unaffected.
 *
 * The parent rides along as X-KilasFlow-Embed-Parent. The frame is served by
 * KilasFlow, so its writes carry KilasFlow's origin rather than the host's,
 * and the server admits that origin only for the host the token was minted
 * for. The frame alone knows which host that is, from the browser-verified
 * `event.origin` of the session message, so the two are set and cleared
 * together.
 */
export function setEmbedToken(token: string | null, parent: string | null = null): void {
	embedToken = token;
	embedParent = token ? parent : null;
}

function currentEmbedToken(): string | null {
	return embedToken;
}

/** Attaches the embed session's headers, unless the caller named a token itself. */
function attachEmbedHeaders(headers: Headers): void {
	const token = currentEmbedToken();
	if (!token || headers.has('X-KilasFlow-Embed')) return;
	headers.set('X-KilasFlow-Embed', token);
	if (embedParent) headers.set('X-KilasFlow-Embed-Parent', embedParent);
}

/**
 * Performs a same-origin API request and returns Orval's standard response
 * envelope. Generated operation types therefore retain status and headers,
 * while non-success responses still become a rich ApiError.
 */
export async function apiFetch<T>(url: string, options: ApiFetchOptions = {}): Promise<T> {
	assertSameOriginPath(url);

	const { body, headers, ...init } = options;
	const jsonBody = body !== undefined && !isBodyInit(body);
	const requestHeaders = new Headers(headers);
	requestHeaders.set('Accept', requestHeaders.get('Accept') ?? 'application/json');
	// An embedded editor has no session cookie, so its token rides on every
	// request. Attaching it here rather than at each call site means a
	// generated client cannot accidentally omit it.
	attachEmbedHeaders(requestHeaders);

	if (jsonBody && !requestHeaders.has('Content-Type')) {
		requestHeaders.set('Content-Type', 'application/json');
	}

	const response = await fetch(url, {
		...init,
		headers: requestHeaders,
		...(body === undefined ? {} : { body: jsonBody ? JSON.stringify(body) : body })
	});

	// Auth-enabled deployments answer 401 to every unauthenticated call.
	// The SPA owns a sign-in route, so a 401 redirects there with the
	// current location as `next` and the existing error block still throws.
	// Skipped for auth calls themselves, the login page, embeds (which
	// emit session-expired instead of navigating the iframe), and while an
	// embed token is attached — approved shape per WebEditorCore.
	if (response.status === 401 && typeof window !== 'undefined' && !currentEmbedToken()) {
		const pathname = window.location.pathname;
		const onLoginPage = pathname === '/login' || pathname.startsWith('/login/');
		const isEmbed = pathname.startsWith('/embed/');
		const isAuthCall = url.includes('/auth/');
		if (!isAuthCall && !onLoginPage && !isEmbed) {
			window.location.assign(`/login?next=${encodeURIComponent(pathname + window.location.search)}`);
		}
	}

	if (!response.ok) {
		const problem = await readProblem(response);
		throw new ApiError(response.status, problem?.detail ?? problem?.title ?? response.statusText, problem);
	}

	if (response.status === 204) {
		return { data: undefined, status: response.status, headers: response.headers } as T;
	}

	return { data: await response.json(), status: response.status, headers: response.headers } as T;
}

/**
 * Downloads a non-JSON response as a Blob. `apiFetch` calls
 * `response.json()` on every non-204 answer, so a `text/csv` download
 * through it throws a parse error instead of yielding bytes — this helper
 * exists for that one case. The embed token rides along exactly as in
 * `apiFetch`, so an embedded editor downloads through the same identity.
 */
export interface ApiDownloadResult {
	data: Blob;
	status: number;
	headers: Headers;
}

export async function apiDownload(url: string, headers?: HeadersInit): Promise<ApiDownloadResult> {
	assertSameOriginPath(url);

	const requestHeaders = new Headers(headers);
	requestHeaders.set('Accept', requestHeaders.get('Accept') ?? 'text/csv');
	attachEmbedHeaders(requestHeaders);

	const response = await fetch(url, { headers: requestHeaders });

	if (!response.ok) {
		const problem = await readProblem(response);
		throw new ApiError(response.status, problem?.detail ?? problem?.title ?? response.statusText, problem);
	}

	return { data: await response.blob(), status: response.status, headers: response.headers };
}

function assertSameOriginPath(url: string): void {
	if (!url.startsWith('/') || url.startsWith('//') || url.includes('\\')) {
		throw new TypeError('KilasFlow API URLs must be same-origin paths');
	}
}

function isBodyInit(value: unknown): value is BodyInit {
	return (
		typeof value === 'string' ||
		value instanceof Blob ||
		value instanceof FormData ||
		value instanceof URLSearchParams ||
		value instanceof ArrayBuffer ||
		ArrayBuffer.isView(value) ||
		value instanceof ReadableStream
	);
}

async function readProblem(response: Response): Promise<ProblemDetail | undefined> {
	try {
		return (await response.json()) as ProblemDetail;
	} catch {
		return undefined;
	}
}

/**
 * The one sentence a failed request is allowed to show a user.
 *
 * It lives beside ApiError because every caller already imports ApiError to
 * recognise one, and because seven page-local copies had produced two
 * spellings of the same failure: a 404 read as "Not found" in the workflow
 * editor and "404 — Not found" on the list that links to it. Whether the
 * status is shown is a product decision, and a product decision cannot be
 * held in seven places.
 *
 * It shows the status. A bare "Not found" leaves the user with nothing to put
 * in a bug report, and the number is the one part of an API failure stable
 * enough to search for.
 *
 * The fallback sentence covers a rejection that is not an Error at all — a
 * thrown string, a rejected promise carrying a response object. Rendering
 * String(error) there is how "[object Object]" reaches a user.
 */
export function message(error: unknown): string {
	if (error instanceof ApiError) return `${error.status} — ${error.message}`;
	return error instanceof Error ? error.message : m.common_request_failed();
}
