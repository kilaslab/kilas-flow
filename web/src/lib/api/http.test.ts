import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError, apiFetch, message } from './http';

const fetchMock = vi.fn<typeof fetch>();

vi.stubGlobal('fetch', fetchMock);

afterEach(() => {
	fetchMock.mockReset();
});

describe('apiFetch', () => {
	it('sends a relative JSON request and returns the response envelope Orval expects', async () => {
		fetchMock.mockResolvedValue(
			new Response(JSON.stringify({ status: 'ok' }), {
				status: 200,
				headers: { 'Content-Type': 'application/json' }
			})
		);

		const result = await apiFetch<{ data: { status: string }; status: number; headers: Headers }>(
			'/api/v1/health',
			{ method: 'GET' }
		);

		expect(result).toMatchObject({ data: { status: 'ok' }, status: 200 });
		expect(result.headers).toBeInstanceOf(Headers);

		expect(fetchMock).toHaveBeenCalledWith(
			'/api/v1/health',
			expect.objectContaining({ method: 'GET' })
		);
		expect(new Headers(fetchMock.mock.calls[0]?.[1]?.headers).get('accept')).toBe('application/json');
	});

	it('serializes object bodies as JSON without overwriting caller headers', async () => {
		fetchMock.mockResolvedValue(new Response(null, { status: 204 }));

		await expect(
			apiFetch<void>('/api/v1/workflows/workflow_1', {
				method: 'PUT',
				body: { name: 'Draft' },
				headers: { 'X-Request-ID': 'request_1' }
			})
		).resolves.toMatchObject({ data: undefined, status: 204 });

		const init = fetchMock.mock.calls[0]?.[1];
		expect(init?.body).toBe('{"name":"Draft"}');
		expect(new Headers(init?.headers).get('content-type')).toBe('application/json');
		expect(new Headers(init?.headers).get('x-request-id')).toBe('request_1');
	});

	it('keeps RFC 9457 problem details on non-success responses', async () => {
		fetchMock.mockResolvedValue(
			new Response(JSON.stringify({ title: 'Validation failed', status: 422, detail: 'name is required' }), {
				status: 422,
				headers: { 'Content-Type': 'application/problem+json' }
			})
		);

		await expect(apiFetch('/api/v1/workflows', { method: 'POST', body: {} })).rejects.toMatchObject({
			name: 'ApiError',
			status: 422,
			message: 'name is required',
			problem: { title: 'Validation failed', status: 422 }
		} satisfies Partial<ApiError>);
	});

	it('passes abort signals through and preserves the cancellation error', async () => {
		const controller = new AbortController();
		const cancellation = new DOMException('The request was cancelled', 'AbortError');
		fetchMock.mockImplementation((_url, init) => {
			expect(init?.signal).toBe(controller.signal);
			return Promise.reject(cancellation);
		});

		await expect(apiFetch('/api/v1/health', { signal: controller.signal })).rejects.toBe(cancellation);
	});

	it('rejects absolute and backslash-prefixed URLs before making a browser request', async () => {
		await expect(apiFetch('https://other.example/api/v1/workflows')).rejects.toThrow(
			'KilasFlow API URLs must be same-origin paths'
		);
		await expect(apiFetch('/\\\\other.example/api/v1/workflows')).rejects.toThrow(
			'KilasFlow API URLs must be same-origin paths'
		);
		expect(fetchMock).not.toHaveBeenCalled();
	});
});

describe('wording a failed request for a user', () => {
	// The status is the one part of an API failure that is stable enough for a
	// user to quote in a bug report, so it is shown. Two of the seven copies
	// this replaced dropped it, which is why an identical 404 read differently
	// in the workflow editor and on the list that links to it.
	it('names the status an API failure came back with', () => {
		expect(message(new ApiError(404, 'Workflow not found'))).toBe('404 — Workflow not found');
	});

	// A local failure — a rejected fetch, a thrown guard — has no status to
	// name, and prefixing one would be inventing it.
	it('passes a plain error through without inventing a status for it', () => {
		expect(message(new Error('Unexpected workflow-list response'))).toBe(
			'Unexpected workflow-list response'
		);
	});

	// Rendering String(error) here is how "[object Object]" reaches a user.
	it.each([[{ detail: 'nope' }], ['nope'], [undefined], [null]])(
		'falls back to a sentence rather than stringifying %o',
		(thrown) => {
			expect(message(thrown)).toBe('The request could not be completed.');
		}
	);
});

/**
 * The reason message() moved here at all was that seven components had each
 * declared their own, and two had drifted. Nothing in the type system stops an
 * eighth from being written, so this reads the source tree instead.
 */
describe('the components that render a failed request', () => {
	const source = fileURLToPath(new URL('../..', import.meta.url));

	function svelteFiles(directory: string): string[] {
		return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
			const path = join(directory, entry.name);
			if (entry.isDirectory()) return svelteFiles(path);
			return entry.name.endsWith('.svelte') ? [path] : [];
		});
	}

	it('declares no message() of its own', () => {
		const declarations = svelteFiles(source).filter((path) =>
			/\bfunction message\s*\(|\bconst message\s*=\s*(?:\(|function|async)/.test(
				readFileSync(path, 'utf8')
			)
		);

		expect(declarations).toEqual([]);
	});

	// The stronger form of the same rule, and the one that actually caught
	// something: the eighth copy was called errorMessage, so a check against
	// the name would have walked straight past it. Recognising an ApiError in
	// order to word it is what makes a component a second opinion on this,
	// whatever the function ends up being called.
	it('leaves recognising an ApiError to this module', () => {
		const opinions = svelteFiles(source).filter((path) =>
			/instanceof ApiError/.test(readFileSync(path, 'utf8'))
		);

		expect(opinions).toEqual([]);
	});

	// A guard against the guard: if the walker ever stopped finding files —
	// a moved directory, a changed extension — the assertion above would pass
	// by looking at nothing at all.
	it('is looking at the components that exist', () => {
		expect(svelteFiles(source).length).toBeGreaterThan(20);
	});
});
