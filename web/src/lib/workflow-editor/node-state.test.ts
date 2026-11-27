import { describe, expect, it } from 'vitest';

import type { Document, Node as WorkflowNode } from '$lib/api/generated/models';

import { nodeNote, noteShownInFlow, setNodeDisabled, setNodeNote } from './node-state';

function node(id: string, extra: Partial<WorkflowNode> = {}): WorkflowNode {
	return { id, name: id, type: 'kilasflow.set', typeVersion: 1, position: { x: 0, y: 0 }, ...extra };
}

function doc(...nodes: WorkflowNode[]): Document {
	return { id: 'wf', name: 'wf', nodes, connections: [] } as unknown as Document;
}

describe('setNodeDisabled', () => {
	it('sets the flag on the named node only and keeps the others by reference', () => {
		const start = doc(node('a'), node('b'));
		const next = setNodeDisabled(start, 'a', true);
		expect(next.nodes?.[0].disabled).toBe(true);
		expect(next.nodes?.[1]).toBe(start.nodes?.[1]);
	});

	it('drops the flag when enabling, so a double toggle saves exactly as it was', () => {
		const start = doc(node('a'));
		const back = setNodeDisabled(setNodeDisabled(start, 'a', true), 'a', false);
		expect(back.nodes?.[0]).toEqual(start.nodes?.[0]);
		expect(Object.hasOwn(back.nodes?.[0] ?? {}, 'disabled')).toBe(false);
	});

	it('returns the same document when nothing changes or the node is missing', () => {
		const start = doc(node('a'), node('b', { disabled: true }));
		expect(setNodeDisabled(start, 'a', false)).toBe(start);
		expect(setNodeDisabled(start, 'b', true)).toBe(start);
		expect(setNodeDisabled(start, 'missing', true)).toBe(start);
	});
});

describe('nodeNote', () => {
	it('reads n8n’s notes and notesInFlow from the node’s settings', () => {
		expect(nodeNote(node('a', { settings: { notes: 'Only out of hours', notesInFlow: true } }))).toEqual({
			text: 'Only out of hours',
			inFlow: true
		});
	});

	it('reads an absent or mistyped value as no note', () => {
		expect(nodeNote(node('a'))).toEqual({ text: '', inFlow: false });
		expect(nodeNote(node('a', { settings: { notes: 4, notesInFlow: 'yes' } }))).toEqual({ text: '', inFlow: false });
	});
});

describe('noteShownInFlow', () => {
	it('shows the trimmed note only when the flag is on', () => {
		expect(noteShownInFlow(node('a', { settings: { notes: ' hello \n', notesInFlow: true } }))).toBe('hello');
		expect(noteShownInFlow(node('a', { settings: { notes: 'hello' } }))).toBeNull();
		expect(noteShownInFlow(node('a', { settings: { notes: 'hello', notesInFlow: false } }))).toBeNull();
	});

	it('shows nothing for the flag on an empty or blank note', () => {
		expect(noteShownInFlow(node('a', { settings: { notesInFlow: true } }))).toBeNull();
		expect(noteShownInFlow(node('a', { settings: { notes: '  \n', notesInFlow: true } }))).toBeNull();
	});
});

describe('setNodeNote', () => {
	it('writes the text and the flag under the keys n8n and the importer use', () => {
		const next = setNodeNote(doc(node('a')), 'a', { text: 'Check the date', inFlow: true });
		expect(next.nodes?.[0].settings).toEqual({ notes: 'Check the date', notesInFlow: true });
	});

	it('keeps the node’s other settings, such as its error policy', () => {
		const start = doc(node('a', { settings: { retryOnFail: true, continueOnFail: false } }));
		const next = setNodeNote(start, 'a', { text: 'note' });
		expect(next.nodes?.[0].settings).toEqual({ retryOnFail: true, continueOnFail: false, notes: 'note' });
	});

	it('removes an emptied note and an unset flag, and the settings left empty', () => {
		const start = doc(node('a', { settings: { notes: 'n', notesInFlow: true } }));
		const cleared = setNodeNote(setNodeNote(start, 'a', { text: '' }), 'a', { inFlow: false });
		expect(Object.hasOwn(cleared.nodes?.[0] ?? {}, 'settings')).toBe(false);
	});

	it('leaves the other settings in place when the note goes', () => {
		const start = doc(node('a', { settings: { notes: 'n', retryOnFail: true } }));
		expect(setNodeNote(start, 'a', { text: '' }).nodes?.[0].settings).toEqual({ retryOnFail: true });
	});

	it('does not trim: the first space of a note being typed must stay', () => {
		const next = setNodeNote(doc(node('a')), 'a', { text: ' ' });
		expect(next.nodes?.[0].settings).toEqual({ notes: ' ' });
	});

	it('touches only the part the patch names', () => {
		const start = doc(node('a', { settings: { notes: 7 } }));
		const next = setNodeNote(start, 'a', { inFlow: true });
		expect(next.nodes?.[0].settings).toEqual({ notes: 7, notesInFlow: true });
	});

	it('returns the same document for a no-op or an unknown node, and keeps other nodes by reference', () => {
		const start = doc(node('a', { settings: { notes: 'n' } }), node('b'));
		expect(setNodeNote(start, 'a', { text: 'n' })).toBe(start);
		expect(setNodeNote(start, 'a', {})).toBe(start);
		expect(setNodeNote(start, 'missing', { text: 'x' })).toBe(start);
		const next = setNodeNote(start, 'a', { text: 'changed' });
		expect(next.nodes?.[1]).toBe(start.nodes?.[1]);
	});
});
