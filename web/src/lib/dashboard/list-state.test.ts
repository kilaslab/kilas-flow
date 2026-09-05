import { describe, expect, it } from 'vitest';

import { failedBesideRows, listState } from './list-state';

describe('a list surface deciding what to show', () => {
	it('shows the skeleton while the first load is in flight', () => {
		expect(listState({ loading: true, failed: false, count: 0 })).toBe('loading');
	});

	// A retry sets loading again without clearing the previous failure. If
	// failed won, the user would watch the error card sit there through a
	// retry they just asked for and have no idea it was running.
	it('keeps showing the skeleton when a retry runs over a previous failure', () => {
		expect(listState({ loading: true, failed: true, count: 0 })).toBe('loading');
	});

	// The trap this ordering exists to avoid: a failed load leaves count at
	// zero, so an empty-first order would tell the user their workspace is
	// empty when the truth is that the server never answered.
	it('reports a failure rather than emptiness when a failed load left no rows', () => {
		expect(listState({ loading: false, failed: true, count: 0 })).toBe('failed');
	});

	it('keeps the rows already loaded when the next page fails', () => {
		expect(listState({ loading: false, failed: true, count: 3 })).toBe('ready');
	});

	// The distinction the whole fix turns on, in one place. The same failure
	// flag means two different things: with nothing loaded it is the only
	// thing there is to say, and with rows loaded it is a footnote to a list
	// the user can still read.
	it('replaces the surface for a first-load failure but not for one over loaded rows', () => {
		expect(listState({ loading: false, failed: true, count: 0 })).toBe('failed');
		expect(listState({ loading: false, failed: true, count: 4 })).toBe('ready');
	});

	it('offers the empty state only once a load has succeeded with nothing in it', () => {
		expect(listState({ loading: false, failed: false, count: 0 })).toBe('empty');
	});

	it('shows the list once there is something to show', () => {
		expect(listState({ loading: false, failed: false, count: 1 })).toBe('ready');
	});
});

describe('a list reporting a failure beside rows it already has', () => {
	// Without this the failure would go unmentioned, which is the other half
	// of keeping the rows: a list that silently stops at the page that failed
	// looks exactly like a list that has reached its end.
	it('reports a failure that landed on top of loaded rows', () => {
		expect(failedBesideRows({ loading: false, failed: true, count: 3 })).toBe(true);
	});

	it('says nothing when the first load failed, because the surface reports that itself', () => {
		expect(failedBesideRows({ loading: false, failed: true, count: 0 })).toBe(false);
	});

	it('says nothing while a retry of the failed request is in flight', () => {
		expect(failedBesideRows({ loading: true, failed: true, count: 3 })).toBe(false);
	});

	it('says nothing when every request so far has succeeded', () => {
		expect(failedBesideRows({ loading: false, failed: false, count: 3 })).toBe(false);
	});
});
