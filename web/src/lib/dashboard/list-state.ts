/**
 * Which of a list surface's four states is showing.
 *
 * It is a function of plain booleans rather than a method on a query object.
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
	/** The load finished without usable data. */
	failed: boolean;
	/** How many rows are loaded. */
	count: number;
}

/**
 * The state to render, in the order the four existing pages already branch.
 *
 * Loading wins over failed, and failed over empty. The order is what keeps a
 * retry from flashing the previous attempt's error card while it is in
 * flight, and keeps a failed load — which leaves `count` at zero — from being
 * reported to the user as "nothing here yet" when the truth is "we could not
 * find out".
 */
export function listState({ loading, failed, count }: ListSignals): ListState {
	if (loading) return 'loading';
	if (failed) return 'failed';
	return count === 0 ? 'empty' : 'ready';
}
