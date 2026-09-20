import { describe, expect, it } from 'vitest';

import { COALESCE_WINDOW_MS, HISTORY_LIMIT, emptyHistory, record, redo, undo } from './history';

describe('canvas history', () => {
	it('hands back the state from before the last change, and the one before that', () => {
		let history = emptyHistory<string>();
		history = record(history, 'a', { at: 0 });
		history = record(history, 'b', { at: 10_000 });

		const undone = undo(history, 'c');
		expect(undone?.state).toBe('b');

		const twice = undo(undone!.history, undone!.state);
		expect(twice?.state).toBe('a');
		expect(undo(twice!.history, twice!.state)).toBeNull();
	});

	it('redoes what undo took back', () => {
		let history = record(emptyHistory<string>(), 'a', { at: 0 });
		const undone = undo(history, 'b');
		expect(undone?.state).toBe('a');

		const redone = redo(undone!.history, undone!.state);
		expect(redone?.state).toBe('b');
		expect(redo(redone!.history, redone!.state)).toBeNull();
	});

	it('drops the redo path once the user edits again', () => {
		const history = record(emptyHistory<string>(), 'a', { at: 0 });
		const undone = undo(history, 'b');
		expect(redo(undone!.history, 'a')).not.toBeNull();

		const edited = record(undone!.history, 'a', { at: 0 });
		expect(redo(edited, 'a+')).toBeNull();
	});

	it('collapses typing in one field into a single step', () => {
		// The regression this exists for: one undo per keystroke, so undoing a
		// word meant pressing the key as many times as the word had letters.
		let history = record(emptyHistory<string>(), 'Set', { key: 'set-1:parameters.text', at: 1_000 });
		history = record(history, 'Set h', { key: 'set-1:parameters.text', at: 1_100 });
		history = record(history, 'Set he', { key: 'set-1:parameters.text', at: 1_200 });

		const undone = undo(history, 'Set hel');
		expect(undone?.state).toBe('Set');
	});

	it('starts a new step at another field, and after the coalesce window', () => {
		const first = record(emptyHistory<string>(), 'a', { key: 'node:parameters.text', at: 0 });
		const otherField = record(first, 'ab', { key: 'node:parameters.subject', at: 10 });
		expect(otherField.past).toHaveLength(2);

		const later = record(first, 'ab', { key: 'node:parameters.text', at: COALESCE_WINDOW_MS + 1 });
		expect(later.past).toHaveLength(2);
	});

	it('keeps the most recent steps when it hits the limit', () => {
		let history = emptyHistory<number>();
		for (let index = 0; index < HISTORY_LIMIT + 20; index += 1) {
			history = record(history, index, { at: index * 10_000 });
		}
		expect(history.past).toHaveLength(HISTORY_LIMIT);
		// The oldest snapshots are the ones dropped, so the first undo is the
		// state right before the last change.
		expect(undo(history, -1)?.state).toBe(HISTORY_LIMIT + 19);
	});
});
