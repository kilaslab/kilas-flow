import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { COALESCE_WINDOW_MS, emptyHistory, record } from './history';
import { PORT_SETTLE_MS, createPortSettler } from './port-settle';

// BUG-qfp7rf: wires a configuration change strands are removed once the edit
// has settled, not on every keystroke. Backspacing a Merge's "4" to type "6"
// passes through a node with two inputs; removing wires at that instant would
// destroy the third and fourth for good.

describe('createPortSettler', () => {
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => vi.useRealTimers());

	it('runs once, after the last edit of a node, with that edit\'s history key', () => {
		const run = vi.fn();
		const settler = createPortSettler(run);

		settler.schedule('merge', 'merge:parameters:numberInputs');
		vi.advanceTimersByTime(PORT_SETTLE_MS - 1);
		settler.schedule('merge', 'merge:parameters:numberInputs');
		vi.advanceTimersByTime(PORT_SETTLE_MS - 1);
		expect(run).not.toHaveBeenCalled();

		vi.advanceTimersByTime(1);
		expect(run).toHaveBeenCalledTimes(1);
		expect(run).toHaveBeenCalledWith('merge', 'merge:parameters:numberInputs');
	});

	it('keeps one timer per node', () => {
		const run = vi.fn();
		const settler = createPortSettler(run);

		settler.schedule('a', 'a:parameters:mode');
		settler.schedule('b', 'b:parameters:mode');
		vi.advanceTimersByTime(PORT_SETTLE_MS);

		expect(run.mock.calls).toEqual([
			['a', 'a:parameters:mode'],
			['b', 'b:parameters:mode']
		]);
	});

	it('does nothing after it is cancelled', () => {
		const run = vi.fn();
		const settler = createPortSettler(run);

		settler.schedule('a', 'a:parameters:mode');
		settler.cancel();
		vi.advanceTimersByTime(PORT_SETTLE_MS * 4);

		expect(run).not.toHaveBeenCalled();
	});

	it('settles inside the history window, so the removal is part of the edit\'s undo step', () => {
		expect(PORT_SETTLE_MS).toBeLessThan(COALESCE_WINDOW_MS);

		const before = { mode: 'retrieve', wires: ['c-store'] };
		const edited = { mode: 'retrieve-as-tool', wires: ['c-store'] };
		const key = 'store:parameters:mode';
		const t0 = 1_000;

		let history = record(emptyHistory<typeof before>(), before, { key, at: t0 });
		// The settle fires PORT_SETTLE_MS after the edit and records the document the
		// edit left behind, under the same key.
		history = record(history, edited, { key, at: t0 + PORT_SETTLE_MS });

		expect(history.past).toHaveLength(1);
		expect(history.past[0].state).toEqual(before);
	});
});
