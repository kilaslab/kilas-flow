import { QueryClient } from '@tanstack/svelte-query';

import { ApiError } from '$lib/api/http';

const READ_STALE_TIME_MS = 30_000;

/** Creates the one server-state cache shared by the KilasFlow SPA. */
export function createKilasFlowQueryClient(): QueryClient {
	return new QueryClient({
		defaultOptions: {
			queries: {
				staleTime: READ_STALE_TIME_MS,
				retry: (failureCount, error) => !(error instanceof ApiError) && failureCount < 1
			},
			mutations: {
				retry: false
			}
		}
	});
}
