import { describe, expect, it } from 'vitest';

import { listState } from './list-state';

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

	// Executions keeps rows on screen when a "Load more" fails, so failed and
	// a non-zero count coexist. The error still wins: something the user asked
	// for did not happen, and silently showing a stale list hides that.
	it('reports a failure even when rows are already on screen', () => {
		expect(listState({ loading: false, failed: true, count: 3 })).toBe('failed');
	});

	it('offers the empty state only once a load has succeeded with nothing in it', () => {
		expect(listState({ loading: false, failed: false, count: 0 })).toBe('empty');
	});

	it('shows the list once there is something to show', () => {
		expect(listState({ loading: false, failed: false, count: 1 })).toBe('ready');
	});
});
