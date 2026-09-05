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
	remove: (nodeID: string) => void;
};

export function setCanvasActions(actions: CanvasActions): void {
	setContext(KEY, actions);
}

/** Undefined on a read-only canvas that never provided them, such as the replay. */
export function getCanvasActions(): CanvasActions | undefined {
	return getContext<CanvasActions | undefined>(KEY);
}
