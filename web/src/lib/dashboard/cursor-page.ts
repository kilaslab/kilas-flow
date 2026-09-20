/**
 * The page size the dashboard asks for while it drains a listing.
 *
 * Every listing endpoint caps a page at 500 rows. The dashboard lists hold the
 * whole list — they filter, sort and count over what they have — so they ask
 * for the largest page the API serves and keep asking until the cursor runs
 * out. Leaving the size to the server default (100) would multiply the round
 * trips for a tenant with more rows than that.
 */
export const DRAIN_PAGE_LIMIT = 500;

/**
 * The cursor a listing response carries in its `X-Next-Cursor` header.
 *
 * The header rather than the body: every listing endpoint pages this way, and
 * the empty header on the last page is what ends a drain.
 */
export function headerCursor(headers: Headers): string {
	return headers.get('X-Next-Cursor') ?? '';
}

/**
 * Every page of a cursor-paged listing, in the order the server named them.
 *
 * The listings use this instead of one request per view: the API pages them
 * server-side, so a single call is only the first page, and a page that showed
 * one as if it were the workspace put rows out of reach, under-counted its
 * heading and labelled executions of the workflows it could not see "deleted".
 *
 * The first rejection ends the drain and is rethrown. Half a list is not a
 * list: a caller that rendered one would print a count and a "deleted" badge
 * that are wrong rather than absent, which is the defect this exists to fix.
 * Callers show the failure instead, and the rows they already had, if any.
 *
 * A cursor that does not advance ends it too, with an error rather than
 * another request: the cursor is opaque, so a server that handed one back
 * unchanged would otherwise be asked for the same page forever, and every
 * round trip would append its rows again.
 */
export async function drainPages<T>(
	fetchPage: (cursor: string) => Promise<CursorPage<T>>
): Promise<T[]> {
	const items: T[] = [];
	const requested = new Set<string>();
	let cursor = '';
	for (;;) {
		requested.add(cursor);
		const page = await fetchPage(cursor);
		items.push(...page.items);
		if (page.nextCursor === '') return items;
		if (requested.has(page.nextCursor)) {
			throw new Error('The list cursor stopped advancing before the last page');
		}
		cursor = page.nextCursor;
	}
}

/**
 * What a cursor-paged list has loaded so far.
 *
 * The rows and the cursor move together — every page that arrives replaces the
 * cursor and extends the rows — so they are one value rather than two pieces
 * of component state that a later edit can update by halves.
 */

export interface CursorPage<T> {
	items: T[];
	/** The cursor for the page after this one, or '' when there is none. */
	nextCursor: string;
}

/** A list that has loaded nothing: the state before the first request and after a failure. */
export function emptyPage<T>(): CursorPage<T> {
	return { items: [], nextCursor: '' };
}

/**
 * One page of a list response, with the absences the API is allowed to send
 * normalised away.
 *
 * The server omits `nextCursor` entirely on the last page rather than sending
 * an empty one, and omits `items` on a page with nothing in it. Collapsing
 * both to '' and [] here means every caller has one shape to reason about;
 * leaving `undefined` to travel means each caller invents its own `?? ''`, and
 * the one that forgets pages the last cursor forever.
 */
export function readPage<T>(
	response: { items?: T[] | null; nextCursor?: string | null } | null | undefined
): CursorPage<T> {
	return {
		items: response?.items ?? [],
		nextCursor: response?.nextCursor ?? ''
	};
}

/**
 * The loaded list with one more page on the end.
 *
 * The arriving cursor replaces the held one rather than being merged with it.
 * Keeping the old cursor when the new page reports none is how a "Load more"
 * button outlives the list it pages: it stays on screen and re-requests the
 * final page on every press.
 */
export function appendPage<T>(
	loaded: CursorPage<T>,
	response: { items?: T[] | null; nextCursor?: string | null } | null | undefined
): CursorPage<T> {
	const arriving = readPage<T>(response);
	return { items: [...loaded.items, ...arriving.items], nextCursor: arriving.nextCursor };
}

/**
 * Whether there is another page to ask for.
 *
 * The emptiness of the cursor is the signal, not the fullness of the last
 * page. A filtered query can return fewer rows than the page size and still
 * have more behind it, so counting rows would stop paging early and hide them.
 */
export function canLoadMore<T>(loaded: CursorPage<T>): boolean {
	return loaded.nextCursor !== '';
}
