import { describe, expect, it } from 'vitest';

import {
	appendPage,
	appendPageIfCurrent,
	canLoadMore,
	drainPages,
	emptyPage,
	headerCursor,
	mergeHead,
	readPage,
	type CursorPage
} from './cursor-page';

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

describe('folding a fresh first page into a list that has paged on', () => {
	// Rows are keyed by themselves here (String is that identity); the object
	// case below keys on a field.

	// A poll must refresh the newest rows without throwing away the older ones
	// the user already loaded, and without losing the cursor Load more resumes
	// from. Replacing the list with the head is the defect this guards (b).
	it('replaces the rows the head covers and keeps the older rows already loaded', () => {
		const loaded = { items: ['e5', 'e4', 'e3', 'e2', 'e1'], nextCursor: 'after:e1' };
		const head = { items: ['e6', 'e5', 'e4'], nextCursor: 'after:e4' };

		expect(mergeHead(loaded, head, String)).toEqual({
			items: ['e6', 'e5', 'e4', 'e3', 'e2', 'e1'],
			nextCursor: 'after:e1'
		});
	});

	it('keeps the cursor of the loaded tail so Load more resumes where it stopped', () => {
		const loaded = { items: ['e5', 'e4', 'e3', 'e2', 'e1'], nextCursor: 'after:e1' };
		const head = { items: ['e6', 'e5', 'e4'], nextCursor: 'after:e4' };

		expect(mergeHead(loaded, head, String).nextCursor).toBe('after:e1');
	});

	it('puts a run that started while the list was open at the top', () => {
		const loaded = { items: ['e3', 'e2', 'e1'], nextCursor: 'after:e1' };
		const head = { items: ['e4', 'e3', 'e2'], nextCursor: 'after:e2' };

		expect(mergeHead(loaded, head, String)).toEqual({
			items: ['e4', 'e3', 'e2', 'e1'],
			nextCursor: 'after:e1'
		});
	});

	it('updates a row in place when its status changed', () => {
		const loaded = {
			items: [
				{ id: 'e2', status: 'running' },
				{ id: 'e1', status: 'succeeded' }
			],
			nextCursor: 'after:e1'
		};
		const head = {
			items: [{ id: 'e2', status: 'succeeded' }],
			nextCursor: 'after:e2'
		};

		expect(mergeHead(loaded, head, (row) => row.id)).toEqual({
			items: [
				{ id: 'e2', status: 'succeeded' },
				{ id: 'e1', status: 'succeeded' }
			],
			nextCursor: 'after:e1'
		});
	});

	// The trap (b): once every page was loaded the cursor was empty, so a poll
	// that replaced the list with the head cut the rows the user had loaded back
	// to the first page and never offered Load more again.
	it('does not cut a list that was loaded to the end back to the first page, and still offers no further page', () => {
		const loaded = {
			items: ['e6', 'e5', 'e4', 'e3', 'e2', 'e1'],
			nextCursor: ''
		};
		const head = { items: ['e6', 'e5', 'e4', 'e3'], nextCursor: 'after:e3' };

		const merged = mergeHead(loaded, head, String);

		expect(merged.items).toEqual(['e6', 'e5', 'e4', 'e3', 'e2', 'e1']);
		expect(canLoadMore(merged)).toBe(false);
	});

	// A row that left a server-side filter (a run that stopped being "running")
	// is inside the rows the head covers but no longer in it, so it goes.
	it('drops a loaded row the head no longer lists, such as one that left the status filter', () => {
		const loaded = { items: ['e5', 'e4', 'e3'], nextCursor: 'after:e3' };
		const head = { items: ['e5', 'e3'], nextCursor: 'after:e3' };

		expect(mergeHead(loaded, head, String)).toEqual({
			items: ['e5', 'e3'],
			nextCursor: 'after:e3'
		});
	});

	// A whole page of new runs between two polls leaves the loaded rows below
	// the head's last row, so splicing them on would silently lose the rows in
	// between. The head alone has no hole; the list shrinks rather than lies.
	it('falls back to the head alone, with its own cursor, when the loaded rows no longer join it', () => {
		const loaded = { items: ['e5', 'e4', 'e3'], nextCursor: 'after:e3' };
		const head = { items: ['e9', 'e8'], nextCursor: 'after:e8' };

		expect(mergeHead(loaded, head, String)).toEqual({
			items: ['e9', 'e8'],
			nextCursor: 'after:e8'
		});
	});

	// No successor means the head page is the whole listing: whatever older rows
	// are on screen no longer exist (retention deleted them, say).
	it('takes the head as the whole list when the head has no successor', () => {
		const loaded = { items: ['e5', 'e4', 'e3'], nextCursor: 'after:e3' };
		const head = { items: ['e5', 'e4'], nextCursor: '' };

		expect(mergeHead(loaded, head, String)).toEqual({ items: ['e5', 'e4'], nextCursor: '' });
	});

	// With no tail past the boundary the held cursor may be behind the head's
	// (it was read before newer runs arrived), so the head's is the truth.
	it('reads the cursor from the head when the loaded rows end exactly at the head boundary', () => {
		const loaded = { items: ['e5'], nextCursor: 'after:e4' };
		const head = { items: ['e6', 'e5'], nextCursor: 'after:e5' };

		expect(mergeHead(loaded, head, String).nextCursor).toBe('after:e5');
	});

	it('returns an empty list when the head is empty', () => {
		const loaded = { items: ['e5', 'e4'], nextCursor: 'after:e4' };

		expect(mergeHead(loaded, emptyPage<string>(), String)).toEqual({ items: [], nextCursor: '' });
	});

	// The head always overlaps the rows it covers, so the merge is where a row
	// could appear twice; the tail is filtered against the head's keys so a
	// response that repeats one past the boundary still lands once.
	it('never lists an id twice', () => {
		const loaded = { items: ['e6', 'e5', 'e4', 'e4', 'e3'], nextCursor: 'after:e3' };
		const head = { items: ['e6', 'e5', 'e4'], nextCursor: 'after:e4' };

		expect(mergeHead(loaded, head, String).items).toEqual(['e6', 'e5', 'e4', 'e3']);
	});

	// The list is reassigned to the merge result in the page, but the state it
	// was read from must stay intact so a discarded response cannot corrupt it.
	it('leaves the list it merged into untouched', () => {
		const loaded = { items: ['e5', 'e4', 'e3'], nextCursor: 'after:e3' };
		const head = { items: ['e6', 'e5', 'e4'], nextCursor: 'after:e4' };

		mergeHead(loaded, head, String);

		expect(loaded).toEqual({ items: ['e5', 'e4', 'e3'], nextCursor: 'after:e3' });
		expect(head).toEqual({ items: ['e6', 'e5', 'e4'], nextCursor: 'after:e4' });
	});
});

describe('appending a page only to the list it was read for', () => {
	it('appends when the list still waits on the cursor the request was made with', () => {
		const loaded = readPage({ items: ['a', 'b'], nextCursor: 'cursor_2' });

		expect(appendPageIfCurrent(loaded, 'cursor_2', { items: ['c'], nextCursor: 'cursor_3' })).toEqual({
			items: ['a', 'b', 'c'],
			nextCursor: 'cursor_3'
		});
	});

	// The race this guards (e): a poll that fell back to the head while Load more
	// was in flight moved the cursor, so the arriving page no longer follows the
	// rows it would be appended to. Applying it anyway leaves a hole.
	it('leaves the list untouched when its cursor moved while the request was in flight', () => {
		const loaded = readPage({ items: ['a', 'b'], nextCursor: 'head_cursor' });
		const response = { items: ['c', 'd'], nextCursor: 'cursor_3' };

		expect(appendPageIfCurrent(loaded, 'cursor_2', response)).toEqual(loaded);
	});

	it('leaves the list it was given untouched', () => {
		const loaded = readPage({ items: ['a'], nextCursor: 'cursor_2' });

		appendPageIfCurrent(loaded, 'cursor_2', { items: ['b'], nextCursor: 'cursor_3' });

		expect(loaded).toEqual({ items: ['a'], nextCursor: 'cursor_2' });
	});
});

describe('paging a newest-first history that keeps growing', () => {
	// Ten runs, newest first, the way the keyset listing returns them.
	const seeded = ['e10', 'e9', 'e8', 'e7', 'e6', 'e5', 'e4', 'e3', 'e2', 'e1'];

	// A small fake of the executions listing: `rows` is the live history
	// newest-first and `list` returns the page after a cursor exactly as the
	// keyset endpoint does (`started_at DESC, id DESC`, the cursor naming the
	// last row of the page).
	function fakeHistory(ids: string[], limit: number) {
		const rows = [...ids];
		const list = (cursor: string, pageSize = limit): CursorPage<string> => {
			const start = cursor === '' ? 0 : rows.indexOf(cursor.slice('after:'.length)) + 1;
			const items = rows.slice(start, start + pageSize);
			const nextCursor = start + pageSize < rows.length ? `after:${items.at(-1)}` : '';
			return { items, nextCursor };
		};
		return { rows, list };
	}

	/** Loads until the server reports no cursor, as the page's Load more loop does. */
	function loadToEnd(list: (cursor: string) => CursorPage<string>, first: CursorPage<string>) {
		let loaded = first;
		while (canLoadMore(loaded)) {
			const asked = loaded.nextCursor;
			loaded = appendPageIfCurrent(loaded, asked, list(asked));
		}
		return loaded;
	}

	it('reads every row exactly once across Load more', () => {
		const { list } = fakeHistory(seeded, 3);

		const loaded = loadToEnd(list, list(''));

		expect(loaded.items).toEqual(seeded);
	});

	// A run that arrives while the list is open shifts every row down one place.
	// The merge keeps the rows already loaded, so paging on from the held cursor
	// still walks the history exactly once, with no gap and no duplicate.
	it('keeps paging with no gap and no duplicate after new runs arrive and the head is refreshed', () => {
		const { rows, list } = fakeHistory(seeded, 3);

		let loaded = list('');
		loaded = appendPage(loaded, list(loaded.nextCursor));
		rows.unshift('e11');
		rows.unshift('e12');
		loaded = mergeHead(loaded, list(''), String);

		const complete = loadToEnd(list, loaded);

		expect(complete.items).toEqual([
			'e12', 'e11', 'e10', 'e9', 'e8', 'e7', 'e6', 'e5', 'e4', 'e3', 'e2', 'e1'
		]);
	});

	// A whole page of runs arriving between two polls leaves the loaded rows
	// below the head's window, so the merge falls back to the head. Paging on
	// from the head's cursor must then continue contiguously, not resume from the
	// cursor the older rows were waiting on.
	it('resumes correctly from the head when a whole page of runs arrived between polls', () => {
		const { rows, list } = fakeHistory(seeded, 3);

		let loaded = list('');
		loaded = appendPage(loaded, list(loaded.nextCursor));
		for (const id of ['e11', 'e12', 'e13', 'e14']) rows.unshift(id);
		loaded = mergeHead(loaded, list(''), String);

		expect(loaded.items).toEqual(['e14', 'e13', 'e12']);

		const complete = loadToEnd(list, loaded);

		expect(complete.items).toEqual([
			'e14', 'e13', 'e12', 'e11', 'e10', 'e9', 'e8', 'e7', 'e6', 'e5', 'e4', 'e3', 'e2', 'e1'
		]);
	});

	// The race in full: the poll replaces the list with the head while a Load
	// more for the old cursor is in flight. The answer must be dropped, and the
	// next Load more must walk the history from the head with no hole.
	it('does not leave a hole when the head replaced the list while a Load more was in flight', () => {
		const { rows, list } = fakeHistory(seeded, 3);

		const loaded = list('');
		const asked = loaded.nextCursor;
		const inFlight = list(asked);
		for (const id of ['e11', 'e12', 'e13', 'e14']) rows.unshift(id);
		const refreshed = mergeHead(loaded, list(''), String);

		expect(refreshed.items).toEqual(['e14', 'e13', 'e12']);
		expect(appendPageIfCurrent(refreshed, asked, inFlight)).toEqual(refreshed);

		const complete = loadToEnd(list, refreshed);

		expect(complete.items).toEqual([
			'e14', 'e13', 'e12', 'e11', 'e10', 'e9', 'e8', 'e7', 'e6', 'e5', 'e4', 'e3', 'e2', 'e1'
		]);
	});
});
