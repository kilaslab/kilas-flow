/**
 * The two things an author says about a node without configuring it: whether
 * it runs, and what it is for.
 *
 * Both are already in the document, so nothing here widens the wire format.
 * `disabled` is a field of the node: the runner passes a disabled node's items
 * through. The note is n8n's `notes` and `notesInFlow`, which the importer
 * keeps in the node's `settings` beside its error policy (the canonical node
 * has no other place for it) and the exporter writes back, so a note written
 * here survives an n8n round trip.
 *
 * Every function returns the same document when it changes nothing, so a
 * caller that records history only on a new object never records a no-op, and
 * every function leaves the other nodes' objects alone, which the canvas
 * projection relies on.
 */

import type { Document, Node as WorkflowNode } from '$lib/api/generated/models';

/** The settings keys n8n names; they are the document's, not this editor's. */
const NOTE_KEY = 'notes';
const IN_FLOW_KEY = 'notesInFlow';

export type NodeNote = {
	text: string;
	/** Whether the note is drawn under the node on the canvas. */
	inFlow: boolean;
};

/** A node's note as the document holds it; an absent or mistyped value reads as no note. */
export function nodeNote(node: Pick<WorkflowNode, 'settings'>): NodeNote {
	const settings = node.settings as Record<string, unknown> | undefined;
	const text = settings?.[NOTE_KEY];
	return {
		text: typeof text === 'string' ? text : '',
		inFlow: settings?.[IN_FLOW_KEY] === true
	};
}

/**
 * The note the canvas draws under a node, or null when there is none to draw.
 *
 * The flag alone draws nothing: n8n shows an empty note as an empty space, and
 * a node with a blank line under it looks like a rendering fault.
 */
export function noteShownInFlow(node: Pick<WorkflowNode, 'settings'>): string | null {
	const { text, inFlow } = nodeNote(node);
	return inFlow && text.trim() !== '' ? text.trim() : null;
}

/**
 * Sets one node's disabled flag. Enabling drops the flag rather than writing
 * `false`, so a node switched off and on again saves exactly as it was.
 */
export function setNodeDisabled(document: Document, nodeID: string, disabled: boolean): Document {
	const target = (document.nodes ?? []).find((node) => node.id === nodeID);
	if (!target || Boolean(target.disabled) === disabled) return document;
	return {
		...document,
		nodes: (document.nodes ?? []).map((node) => {
			if (node.id !== nodeID) return node;
			if (disabled) return { ...node, disabled: true };
			// eslint-disable-next-line @typescript-eslint/no-unused-vars
			const { disabled: _dropped, ...enabled } = node;
			return enabled;
		})
	};
}

/**
 * Writes a node's note, or part of it.
 *
 * An empty note and an unset flag are removed rather than stored, and a
 * `settings` left empty is removed whole: the Go side omits them, so a node
 * that never had a note must not start saving `settings: {}` after the user
 * typed and deleted one.
 *
 * Only an exactly empty text is a cleared note. Trimming here would eat the
 * first space of a note being typed, because the field is bound to what this
 * wrote.
 */
export function setNodeNote(document: Document, nodeID: string, patch: Partial<NodeNote>): Document {
	const target = (document.nodes ?? []).find((node) => node.id === nodeID);
	if (!target) return document;
	const current = nodeNote(target);
	const textChanges = patch.text !== undefined && patch.text !== current.text;
	const flagChanges = patch.inFlow !== undefined && patch.inFlow !== current.inFlow;
	if (!textChanges && !flagChanges) return document;

	// Only the part the patch names is touched, so a value this editor does not
	// understand under the other key is carried through untouched.
	const settings: Record<string, unknown> = { ...((target.settings ?? {}) as Record<string, unknown>) };
	if (textChanges) {
		if (patch.text === '') delete settings[NOTE_KEY];
		else settings[NOTE_KEY] = patch.text;
	}
	if (flagChanges) {
		if (patch.inFlow) settings[IN_FLOW_KEY] = true;
		else delete settings[IN_FLOW_KEY];
	}

	return {
		...document,
		nodes: (document.nodes ?? []).map((node) => {
			if (node.id !== nodeID) return node;
			// eslint-disable-next-line @typescript-eslint/no-unused-vars
			const { settings: _previous, ...rest } = node;
			return Object.keys(settings).length === 0 ? rest : { ...rest, settings };
		})
	};
}
