/**
 * The SDK's single request layer.
 *
 * Authentication is always explicit host configuration. There is no ambient
 * key, no environment lookup, and no default credential: a caller that has not
 * supplied one gets an unauthenticated request rather than a surprise.
 */

/** RFC 9457 problem document, as the API returns it. */
export interface ProblemDetail {
	title: string;
	status: number;
	detail?: string;
	instance?: string;
	errors?: Array<{ message: string; location?: string; value?: unknown }>;
}

/** Every non-success response becomes one of these. */
export class KilasFlowError extends Error {
	readonly status: number;
	readonly problem: ProblemDetail | undefined;

	constructor(status: number, message: string, problem?: ProblemDetail) {
		super(message);
		this.name = 'KilasFlowError';
		this.status = status;
		this.problem = problem;
	}
}

export interface TransportOptions {
	/** Absolute base URL of the KilasFlow deployment, e.g. https://flows.example. */
	baseUrl: string;
	/**
	 * The host's API key (`kfa1_…`), sent as `Authorization: Bearer <key>`.
	 *
	 * A convenience layered on `headers`, never a replacement for it: when
	 * `headers` already carries an `Authorization` entry, that explicit value
	 * wins and `apiKey` is ignored, so gateway headers, signed proxies and
	 * other shapes keep working exactly as before.
	 */
	apiKey?: string;
	/**
	 * Headers added to every request. This is where a host puts anything that
	 * is not the API key — a gateway header, a signed proxy value — or an
	 * `Authorization` value `apiKey` cannot express.
	 *
	 * It stays a plain record because deployments authenticate differently,
	 * and inventing one shape would force the others to work around it. It is
	 * a supported credential path, not a transitional one superseded by
	 * `apiKey`.
	 */
	headers?: Record<string, string>;
	/** Injected for tests, or to add tracing/retries around the SDK. */
	fetch?: typeof globalThis.fetch;
	/** Aborts a request that takes too long. Defaults to 30 seconds. */
	timeoutMs?: number;
}

const DEFAULT_TIMEOUT_MS = 30_000;

export class Transport {
	readonly baseUrl: string;
	readonly #headers: Record<string, string>;
	readonly #fetch: typeof globalThis.fetch;
	readonly #timeoutMs: number;

	constructor(options: TransportOptions) {
		const baseUrl = (options.baseUrl ?? '').trim().replace(/\/+$/, '');
		if (!baseUrl) throw new Error('KilasFlow SDK requires a baseUrl');
		// A relative base would silently resolve against whatever page loaded
		// the SDK, which is not something a host should discover at runtime.
		let parsed: URL;
		try {
			parsed = new URL(baseUrl);
		} catch {
			throw new Error(`KilasFlow SDK baseUrl must be absolute, got ${JSON.stringify(options.baseUrl)}`);
		}
		if (parsed.protocol !== 'https:' && parsed.protocol !== 'http:') {
			throw new Error(`KilasFlow SDK baseUrl must be http or https, got ${parsed.protocol}`);
		}

		this.baseUrl = baseUrl;
		const headers = { ...(options.headers ?? {}) };
		if (options.apiKey !== undefined) {
			if (!options.apiKey.trim()) throw new Error('KilasFlow SDK apiKey must not be empty; omit it for an unauthenticated request');
			// An explicit Authorization header is the host saying it knows
			// better than the convenience — a gateway shape, a signed proxy —
			// so it wins and apiKey stays out of the way.
			headers.Authorization ??= `Bearer ${options.apiKey}`;
		}
		this.#headers = headers;
		this.#fetch = options.fetch ?? globalThis.fetch?.bind(globalThis);
		this.#timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
		if (!this.#fetch) {
			throw new Error('KilasFlow SDK needs a fetch implementation');
		}
	}

	/** Builds an absolute API URL with encoded query parameters. */
	url(path: string, query?: Record<string, string | number | boolean | string[] | undefined>): string {
		const target = new URL(this.baseUrl + '/api/v1' + path);
		for (const [key, value] of Object.entries(query ?? {})) {
			if (value === undefined) continue;
			if (Array.isArray(value)) {
				for (const entry of value) target.searchParams.append(key, entry);
				continue;
			}
			target.searchParams.set(key, String(value));
		}
		return target.toString();
	}

	async request<T>(
		method: string,
		path: string,
		options: { body?: unknown; query?: Record<string, string | number | boolean | string[] | undefined>; signal?: AbortSignal } = {}
	): Promise<T> {
		const headers = new Headers(this.#headers);
		headers.set('Accept', 'application/json');
		if (options.body !== undefined) headers.set('Content-Type', 'application/json');

		// A caller's own signal and the SDK's timeout both have to abort the
		// request, so they are combined rather than one replacing the other.
		const timeout = AbortSignal.timeout(this.#timeoutMs);
		const signal = options.signal ? AbortSignal.any([options.signal, timeout]) : timeout;

		const response = await this.#fetch(this.url(path, options.query), {
			method,
			headers,
			signal,
			...(options.body === undefined ? {} : { body: JSON.stringify(options.body) })
		});

		if (response.status === 204) return undefined as T;
		if (!response.ok) throw await problemFrom(response);
		if (response.headers.get('Content-Type')?.includes('json') === false) {
			return (await response.text()) as T;
		}
		return (await response.json()) as T;
	}
}

async function problemFrom(response: Response): Promise<KilasFlowError> {
	let problem: ProblemDetail | undefined;
	let detail = `${response.status} ${response.statusText}`;
	try {
		const body = (await response.json()) as ProblemDetail;
		if (body && typeof body === 'object') {
			problem = body;
			if (body.detail) detail = body.detail;
			else if (body.title) detail = body.title;
		}
	} catch {
		// A non-JSON error body is still an error; the status carries it.
	}
	return new KilasFlowError(response.status, detail, problem);
}
