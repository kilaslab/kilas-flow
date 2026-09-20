import { describe, expect, it } from 'vitest';

import { appendPage, canLoadMore, drainPages, emptyPage, headerCursor, readPage } from './cursor-page';

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

describe('reading the cursor out of a listing response', () => {
	it('reads the next cursor the server put in the header', () => {
		expect(headerCursor(new Headers({ 'X-Next-Cursor': 'cursor_2' }))).toBe('cursor_2');
	});

	// The last page carries no header at all rather than an empty one, and the
	// drain ends on that absence.
	it('reads a missing header as the end of the list', () => {
		expect(headerCursor(new Headers())).toBe('');
	});
});

describe('draining every page of a listing', () => {
	/** A server that pages four rows two at a time, recording what it was asked. */
	function threePages() {
		const requested: string[] = [];
		const pages = new Map<string, { items: string[]; nextCursor?: string }>([
			['', { items: ['a', 'b'], nextCursor: 'cursor_2' }],
			['cursor_2', { items: ['c'], nextCursor: 'cursor_3' }],
			['cursor_3', { items: ['d'] }]
		]);
		const fetchPage = async (cursor: string) => {
			requested.push(cursor);
			return readPage(pages.get(cursor));
		};
		return { requested, fetchPage };
	}

	it('returns every row in the order the pages were read', async () => {
		const { fetchPage } = threePages();

		expect(await drainPages(fetchPage)).toEqual(['a', 'b', 'c', 'd']);
	});

	it('asks for each page once, following the cursor the previous one named', async () => {
		const { requested, fetchPage } = threePages();

		await drainPages(fetchPage);

		expect(requested).toEqual(['', 'cursor_2', 'cursor_3']);
	});

	it('reads a one-page list in a single request', async () => {
		const requested: string[] = [];
		const items = await drainPages(async (cursor) => {
			requested.push(cursor);
			return readPage({ items: ['a'] });
		});

		expect(items).toEqual(['a']);
		expect(requested).toEqual(['']);
	});

	// Half a list is not a list: the caller shows the failure instead of a
	// count over rows it never read, so the rejection has to come back out.
	it('stops at the page that failed and reports it', async () => {
		const failure = new Error('Unexpected workflow-list response');
		const requested: string[] = [];
		const fetchPage = async (cursor: string) => {
			requested.push(cursor);
			if (cursor === 'cursor_2') throw failure;
			return readPage({ items: ['a', 'b'], nextCursor: 'cursor_2' });
		};

		await expect(drainPages(fetchPage)).rejects.toBe(failure);
		expect(requested).toEqual(['', 'cursor_2']);
	});

	// The cursor is opaque, so a server that handed back the one it was given
	// would be asked for the same page forever, appending its rows each time.
	// The second request is the proof that this terminates rather than loops.
	it('terminates on a cursor that does not advance', async () => {
		const requested: string[] = [];
		const fetchPage = async (cursor: string) => {
			requested.push(cursor);
			return readPage({ items: ['a'], nextCursor: 'cursor_2' });
		};

		await expect(drainPages(fetchPage)).rejects.toThrow(/cursor stopped advancing/);
		expect(requested).toEqual(['', 'cursor_2']);
	});
});
