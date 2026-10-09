/**
 * When the wires a configuration change stranded are removed.
 *
 * Not on the keystroke. A typed field commits on every input event, and the
 * value passes through states the node does not accept: backspacing a Merge's
 * "4" to type "6" goes through a node with two inputs, and removing wires then
 * would destroy the third and fourth for good. Removal waits for the edit to
 * settle, one timer per node, and runs with the edit's own history key.
 *
 * The delay is shorter than the history's coalesce window, so the removal is
 * recorded into the edit's undo step: one Undo takes the property and the wires
 * back together.
 */
export const PORT_SETTLE_MS = 500;

export type PortSettler = {
	/** Starts, or restarts, the countdown for one node's edit. */
	schedule(nodeID: string, historyKey: string): void;
	/** Drops every pending countdown, for when the editor goes away. */
	cancel(): void;
};

export function createPortSettler(run: (nodeID: string, historyKey: string) => void, delay = PORT_SETTLE_MS): PortSettler {
	const pending = new Map<string, ReturnType<typeof setTimeout>>();
	return {
		schedule(nodeID, historyKey) {
			const previous = pending.get(nodeID);
			if (previous !== undefined) clearTimeout(previous);
			pending.set(
				nodeID,
				setTimeout(() => {
					pending.delete(nodeID);
					run(nodeID, historyKey);
				}, delay)
			);
		},
		cancel() {
			for (const timer of pending.values()) clearTimeout(timer);
			pending.clear();
		}
	};
}
