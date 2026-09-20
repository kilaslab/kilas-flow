import { getContext, setContext } from 'svelte';

const KEY = Symbol('kilasflow.canvas-actions');

/**
 * What a node on the canvas can ask the editor to do.
 *
 * Nodes are rendered by Svelte Flow rather than by the editor's own markup, so
 * there is no prop path between them. Context carries the few actions a node
 * needs; everything else stays with the editor, which owns the document.
 *
 * Each member is a function so a node reads the current value at call time and
 * cannot capture a stale one.
 */
export type CanvasActions = {
	readOnly: () => boolean;
	/** Adds a step already connected to this output port. */
	addFrom: (nodeID: string, port: string) => void;
	/**
	 * Fills an attachment slot — an agent's model, memory or tool.
	 *
	 * Filtered by the port's kind so the picker offers only what can attach
	 * there; the tile lands under the slot already wired to it.
	 */
	addAttached: (nodeID: string, port: string, kind: string) => void;
	remove: (nodeID: string) => void;
	/** Splices a new step into this connection, between the two nodes it joins. */
	splice: (edgeID: string) => void;
	removeEdge: (edgeID: string) => void;
	/** Opens the inline rename editor on this node (F2, or a double click). */
	rename: (nodeID: string) => void;
	/** Writes back the size a sticky note was dragged to. */
	resize: (nodeID: string, width: number, height: number) => void;
};

export function setCanvasActions(actions: CanvasActions): void {
	setContext(KEY, actions);
}

/** Undefined on a read-only canvas that never provided them, such as the replay. */
export function getCanvasActions(): CanvasActions | undefined {
	return getContext<CanvasActions | undefined>(KEY);
}
