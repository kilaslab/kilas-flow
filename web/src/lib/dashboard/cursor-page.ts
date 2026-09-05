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
