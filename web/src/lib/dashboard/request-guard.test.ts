import { describe, expect, it } from 'vitest';

import { RequestGuard } from './request-guard';

describe('a list guarding against an out-of-order response', () => {
	it('accepts the response to the only request in flight', () => {
		const guard = new RequestGuard();
		expect(guard.holds(guard.start())).toBe(true);
	});

	// The defect this whole module exists for. The user picks a status, the
	// first request is slow, the second is fast, and the first one lands last.
	// Without the guard the page shows the previous filter's executions under
	// the new filter's controls, with no error and no symptom at all.
	it('discards a slow response that a newer filter has already superseded', () => {
		const guard = new RequestGuard();
		const stale = guard.start();
		const fresh = guard.start();

		expect(guard.holds(stale)).toBe(false);
		expect(guard.holds(fresh)).toBe(true);
	});

	// "Load more" extends the request already in flight rather than replacing
	// it. Had it called start(), that request would fail its own holds() check
	// on arrival, discard its results and never clear the loading flag — the
	// page would sit on a skeleton forever.
	it('lets a follow-on page join the current request instead of starting one', () => {
		const guard = new RequestGuard();
		const first = guard.start();
		const more = guard.current;

		expect(more).toBe(first);
		expect(guard.holds(more)).toBe(true);
	});

	it('supersedes a follow-on page too when a filter changes while it is in flight', () => {
		const guard = new RequestGuard();
		guard.start();
		const more = guard.current;
		guard.start();

		expect(guard.holds(more)).toBe(false);
	});

	// Tokens start at 1 so that no request ever carries the counter's resting
	// value. A guard that handed out 0 first would make "the request I issued"
	// and "I have issued nothing" the same number, and a stale response would
	// be accepted by a page that had moved on.
	it('never issues the token a fresh guard is already resting on', () => {
		const guard = new RequestGuard();
		expect(guard.current).toBe(0);
		expect(guard.start()).toBe(1);
	});
});
