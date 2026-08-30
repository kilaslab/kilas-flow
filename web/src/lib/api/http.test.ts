import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError, apiFetch } from './http';

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
