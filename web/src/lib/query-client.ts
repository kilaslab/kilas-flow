import { QueryClient } from '@tanstack/svelte-query';

import { ApiError } from '$lib/api/http';

const READ_STALE_TIME_MS = 30_000;

/** Creates the one server-state cache shared by the KilasFlow SPA. */
export function createKilasFlowQueryClient(): QueryClient {
	return new QueryClient({
		defaultOptions: {
			queries: {
				staleTime: READ_STALE_TIME_MS,
				retry: (failureCount, error) => !(error instanceof ApiError) && failureCount < 1,
				// Notify on every result change rather than only on the fields a
				// template happened to read first. The property-tracking default
				// is an optimisation for very large result objects; here it saved
				// nothing and cost correctness — a component whose markup read
				// `isPending` through an {#if} chain never re-rendered when the
				// query resolved, so the editor sat on "Loading…" forever.
				notifyOnChangeProps: 'all'
			},
			mutations: {
				retry: false
			}
		}
	});
}
