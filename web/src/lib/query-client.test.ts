import { describe, expect, it } from 'vitest';

import { ApiError } from '$lib/api/http';
import { createKilasFlowQueryClient } from './query-client';

describe('createKilasFlowQueryClient', () => {
	it('retries only one transient read failure and never retries mutations', () => {
		const client = createKilasFlowQueryClient();
		const defaults = client.getDefaultOptions();
		const retry = defaults.queries?.retry;

		expect(defaults.queries?.staleTime).toBe(30_000);
		expect(defaults.mutations?.retry).toBe(false);
		expect(typeof retry).toBe('function');

		if (typeof retry !== 'function') throw new Error('query retry policy is missing');

		expect(retry(0, new Error('network unavailable'))).toBe(true);
		expect(retry(1, new Error('network unavailable'))).toBe(false);
		expect(retry(0, new ApiError(422, 'invalid workflow'))).toBe(false);
	});
});
