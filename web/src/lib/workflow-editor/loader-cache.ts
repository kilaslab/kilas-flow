import type { OptionsLoader } from '$lib/api/generated/models';
import { message } from '$lib/api/http';

/**
 * How long a loader waits before it fires.
 *
 * The effect re-runs as soon as something it depends on changes — typing into
 * the very field a loader reads — and every one of those runs used to be a
 * request. A short wait turns a burst of keystrokes into the one request the
 * user's last keystroke actually asks for.
 */
export const LOAD_DEBOUNCE_MS = 250;

/** How many distinct answers are worth remembering. */
const CACHE_LIMIT = 32;

/**
 * What a loader's answer depends on.
 *
 * The loader itself declares the parameters it reads, so those values are the
 * whole dependency: a keystroke in any *other* parameter of the node must not
 * produce a request, which is what happened when the effect tracked the node.
 * The loader's identity and the credential are in the key because two loaders
 * can share a dependency (a resource locator's modes) and the same list can
 * differ per credential.
 */
export function loaderSignature(
	loader: OptionsLoader | undefined,
	values: Record<string, unknown>,
	contextKey = ''
): string {
	if (!loader) return '';
	const dependencies = (loader.dependsOn ?? []).map((key) => `${key}=${scalar(values[key])}`);
	return [contextKey, loader.source, loader.name ?? '', loader.endpoint ?? '', ...dependencies].join('|');
}

function scalar(value: unknown): string {
	if (value === null || value === undefined) return '';
	if (typeof value === 'string') return value;
	if (typeof value === 'number' || typeof value === 'boolean') return String(value);
	try {
		return JSON.stringify(value);
	} catch {
		return '';
	}
}

/**
 * Runs a loader once per dependency key, debounced, and hands the answer to the
 * caller.
 *
 * Returns the effect's cleanup: a key that changes cancels the pending call, so
 * an answer for a dependency the user has already moved on from can never land.
 * Failures are reported through `fail` rather than thrown: the request used to
 * reject into the console with the dropdown left silently empty.
 */
export function loadSoon<T>(options: {
	key: string;
	load: () => Promise<T>;
	apply: (result: T) => void;
	/** What to show when the loader refuses; `reason` is the server's own message. */
	fail: (reason: string) => T;
	cache?: Map<string, T>;
	delay?: number;
}): () => void {
	const { key, load, apply, fail, cache, delay = LOAD_DEBOUNCE_MS } = options;
	if (key === '') return () => {};

	const remembered = cache?.get(key);
	if (remembered !== undefined) {
		apply(remembered);
		return () => {};
	}

	let cancelled = false;
	const timer = setTimeout(() => {
		void load()
			.then((result) => {
				if (cancelled) return;
				if (cache) {
					// A failure is deliberately not cached: the next visit should try
					// again rather than replay a 502 forever.
					cache.set(key, result);
					if (cache.size > CACHE_LIMIT) cache.clear();
				}
				apply(result);
			})
			.catch((error: unknown) => {
				if (!cancelled) apply(fail(message(error)));
			});
	}, delay);

	return () => {
		cancelled = true;
		clearTimeout(timer);
	};
}
