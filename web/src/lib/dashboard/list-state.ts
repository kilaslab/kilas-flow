/**
 * Which of a list surface's four states is showing, and whether a failure
 * arrived beside rows that are still worth showing.
 *
 * These are two questions rather than one five-valued answer. A fifth state
 * would make every call site re-derive which of the values still mean "there
 * are rows to render", and the branch that got that wrong would drop the
 * table on the floor.
 *
 * Both are functions of plain booleans rather than methods on a query object.
 * Three of the dashboard's list pages read `isPending`/`isError` off TanStack
 * Query; executions drives the same three states by hand because it pages a
 * cursor Query has no opinion about. A discriminator typed against a query
 * object would have forced executions to be rewritten to fit, and its stale-
 * response guard is the kind of thing that disappears in a rewrite.
 */

export type ListState = 'loading' | 'failed' | 'empty' | 'ready';

export interface ListSignals {
	/** A first load is in flight and there is nothing to show yet. */
	loading: boolean;
	/** The most recent request finished without usable data. */
	failed: boolean;
	/** How many rows are loaded. */
	count: number;
}

/**
 * The state to render, in the order the four existing pages already branch.
 *
 * Loading wins over everything, which is what keeps a retry from flashing the
 * previous attempt's error card while the retry is still in flight.
 *
 * With nothing loaded, a failure explains the emptiness. Reporting "nothing
 * here yet" when the truth is "we could not find out" is the trap this order
 * exists to avoid.
 *
 * With rows loaded, the list wins over the failure. Rows on screen are proof
 * that the request which fetched them succeeded, so whatever failed after
 * that — a page that never arrived, a refresh that never answered — is news
 * about the list rather than grounds for taking it away. That news is
 * `failedBesideRows` below, and it belongs next to the rows instead of in
 * place of them.
 */
export function listState({ loading, failed, count }: ListSignals): ListState {
	if (loading) return 'loading';
	if (count === 0) return failed ? 'failed' : 'empty';
	return 'ready';
}

/**
 * Whether a failure should be reported alongside rows that are already up.
 *
 * The row count is what separates the two kinds of failure. A first load that
 * fails leaves nothing behind and owns the whole surface; a request that fails
 * after rows have arrived cost the user only the part they asked for on top.
 * Both pages that can reach this state hold that separation up: executions
 * empties its loaded page whenever a first load fails, and TanStack Query
 * keeps the last successful data when a refetch errors, so a non-zero count
 * here always means rows that arrived intact.
 *
 * A load in flight suppresses the report for the same reason `listState`
 * shows the skeleton — a failure the user has already asked us to retry is
 * not news any more.
 */
export function failedBesideRows({ loading, failed, count }: ListSignals): boolean {
	return failed && !loading && count > 0;
}
