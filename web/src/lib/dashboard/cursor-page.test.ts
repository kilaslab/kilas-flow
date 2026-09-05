import { describe, expect, it } from 'vitest';

import { appendPage, canLoadMore, emptyPage, readPage } from './cursor-page';

describe('reading a page of a cursor-paged list', () => {
	it('keeps the rows and the cursor the response carried', () => {
		expect(readPage({ items: ['a', 'b'], nextCursor: 'cursor_2' })).toEqual({
			items: ['a', 'b'],
			nextCursor: 'cursor_2'
		});
	});

	// The API omits nextCursor on the last page rather than sending an empty
	// one. Letting undefined travel would make every call site invent its own
	// fallback, and the one that forgot would page the last cursor forever.
	it('reads a missing cursor as the end of the list', () => {
		expect(readPage({ items: ['a'] }).nextCursor).toBe('');
	});

	// A filter that matches nothing returns a body with no items key at all.
	it.each([[undefined], [null], [{}], [{ items: null }]])('reads %o as no rows', (response) => {
		expect(readPage(response).items).toEqual([]);
	});
});

describe('extending a list with the next page', () => {
	it('keeps the rows already on screen and adds the new ones after them', () => {
		const loaded = readPage({ items: ['a', 'b'], nextCursor: 'cursor_2' });
		expect(appendPage(loaded, { items: ['c'], nextCursor: 'cursor_3' })).toEqual({
			items: ['a', 'b', 'c'],
			nextCursor: 'cursor_3'
		});
	});

	// The trap: if the held cursor survived a final page that reports none,
	// "Load more" would stay on screen and re-request the same last page on
	// every press, appending its rows again each time.
	it('drops the held cursor when the final page reports no successor', () => {
		const loaded = readPage({ items: ['a'], nextCursor: 'cursor_2' });
		const complete = appendPage(loaded, { items: ['b'] });

		expect(complete.nextCursor).toBe('');
		expect(canLoadMore(complete)).toBe(false);
	});

	// The loaded page is replaced rather than mutated, so a response that
	// arrives after its filter was superseded cannot corrupt the rows the
	// user is actually looking at on its way to being discarded.
	it('leaves the page it extended untouched', () => {
		const loaded = readPage({ items: ['a'], nextCursor: 'cursor_2' });
		appendPage(loaded, { items: ['b'], nextCursor: 'cursor_3' });

		expect(loaded).toEqual({ items: ['a'], nextCursor: 'cursor_2' });
	});
});

describe('deciding whether to offer another page', () => {
	it('offers one while the server names a cursor', () => {
		expect(canLoadMore(readPage({ items: [], nextCursor: 'cursor_2' }))).toBe(true);
	});

	// A filtered query can return fewer rows than the page size and still have
	// more behind it, so the cursor is the signal and the row count is not.
	it('offers one even when the page that named it came back nearly empty', () => {
		expect(canLoadMore(readPage({ items: ['a'], nextCursor: 'cursor_9' }))).toBe(true);
	});

	it('offers nothing from a list that has loaded nothing', () => {
		expect(canLoadMore(emptyPage())).toBe(false);
	});
});
