/**
 * A bounded undo/redo stack of whole draft documents.
 *
 * Snapshots rather than inverse operations: a draft is already an immutable
 * clone at every mutation boundary, so keeping the previous one costs a
 * reference, while an inverse for every mutation (add, delete, move, connect,
 * rename, parameter edit, tidy) would be a second implementation of each one
 * and would drift from it.
 */

/** How many steps back a canvas can be taken. */
export const HISTORY_LIMIT = 64;

/**
 * Two edits of the same control inside this window collapse into one step.
 *
 * Without it, typing a name would take one undo per keystroke. The key is the
 * control, so moving to the next field starts a new step.
 */
export const COALESCE_WINDOW_MS = 700;

export type HistoryEntry<T> = {
	state: T;
	/** Identifies the control being edited, or null for a step of its own. */
	key: string | null;
	at: number;
};

export type History<T> = {
	past: HistoryEntry<T>[];
	future: HistoryEntry<T>[];
};

export function emptyHistory<T>(): History<T> {
	return { past: [], future: [] };
}

/**
 * Records what the draft looked like *before* a change.
 *
 * Called with the state that is being replaced, so `undo` hands it straight
 * back. A key that matches the previous entry inside the coalesce window
 * extends that entry instead of adding one: the snapshot already stored is the
 * one from before the first keystroke, which is where undo should land.
 */
export function record<T>(
	history: History<T>,
	previous: T,
	options: { key?: string | null; at?: number; window?: number } = {}
): History<T> {
	const key = options.key ?? null;
	const at = options.at ?? Date.now();
	const window = options.window ?? COALESCE_WINDOW_MS;
	const past = history.past;

	if (key !== null && past.length > 0) {
		const top = past[past.length - 1];
		if (top.key === key && at - top.at <= window) {
			return { past: [...past.slice(0, -1), { ...top, at }], future: [] };
		}
	}

	const next = [...past, { state: previous, key, at }];
	// Oldest first: a canvas that has been edited for an hour still undoes the
	// last HISTORY_LIMIT steps rather than forgetting the recent ones.
	return { past: next.length > HISTORY_LIMIT ? next.slice(next.length - HISTORY_LIMIT) : next, future: [] };
}

export type HistoryStep<T> = { history: History<T>; state: T };

export function undo<T>(history: History<T>, current: T): HistoryStep<T> | null {
	const previous = history.past.at(-1);
	if (!previous) return null;
	return {
		history: {
			past: history.past.slice(0, -1),
			future: [...history.future, { state: current, key: null, at: Date.now() }]
		},
		state: previous.state
	};
}

export function redo<T>(history: History<T>, current: T): HistoryStep<T> | null {
	const next = history.future.at(-1);
	if (!next) return null;
	return {
		history: {
			past: [...history.past, { state: current, key: null, at: Date.now() }],
			future: history.future.slice(0, -1)
		},
		state: next.state
	};
}
