import { describe, expect, it } from 'vitest';

import type { Definition, Document } from '$lib/api/generated/models';

import { copySelection, pasteInto, readClipboard } from './clipboard';
import {
	createWorkflowNode,
	duplicateNodes,
	renameNode,
	updateNodeProperty,
	uniqueNodeName
} from './document';
import { emptyHistory, record, redo, undo } from './history';
import { tidyDocument } from './layout';

/**
 * The authoring pipeline as the editor drives it: a mutation records what the
 * draft was, undo hands it back, and a new edit drops the redo path. Each piece
 * has its own suite; this is the seam between them, which is where a mistake
 * would be invisible to both.
 */

const set: Definition = {
	type: 'kilasflow.set',
	version: 1,
	displayName: 'Set',
	category: 'Core',
	group: ['transform'],
	source: 'builtin',
	inputs: [{ name: 'main', kind: 'main' }],
	outputs: [{ name: 'main', kind: 'main' }],
	parameters: [],
	sharedSettings: []
};

const telegram: Definition = { ...set, type: 'pack.telegram', displayName: 'Telegram' };

function document(): Document {
	return {
		id: 'workflow-1',
		schemaVersion: 1,
		name: 'Welcome',
		nodes: [{ id: 'manual', name: 'Set', type: 'kilasflow.set', typeVersion: 1, position: { x: 0, y: 0 }, parameters: { text: 'hello' } }],
		connections: [],
		settings: {}
	};
}

describe('authoring pipeline', () => {
	it('walks a dirty canvas back and forward, and forgets the redo path on a new edit', () => {
		const saved = document();
		let draft = saved;
		let history = emptyHistory<Document>();

		// Two edits of one field coalesce into one step; a third in another field
		// is its own.
		draft = updateNodeProperty(draft, 'manual', 'parameters', 'text', 'hello world');
		history = record(history, saved, { key: 'manual:parameters:text', at: 1_000 });
		draft = updateNodeProperty(draft, 'manual', 'parameters', 'text', 'hello world!');
		history = record(history, saved, { key: 'manual:parameters:text', at: 1_100 });
		expect(history.past).toHaveLength(1);

		const back = undo(history, draft);
		expect(back?.state.nodes?.[0].parameters).toEqual({ text: 'hello' });

		const forward = redo(back!.history, back!.state);
		expect(forward?.state.nodes?.[0].parameters).toEqual({ text: 'hello world!' });

		const after = record(forward!.history, forward!.state, { key: null, at: 2_000 });
		expect(redo(after, forward!.state)).toBeNull();
	});

	it('carries a selection to another workflow with new ids and names, then renames without breaking references', () => {
		const source = document();
		const content = readClipboard(copySelection(source, ['manual'])!);
		if (content?.kind !== 'fragment') throw new Error('not a fragment');
		let counter = 0;
		const pasted = pasteInto(source, content.payload, { x: 200, y: 60 }, () => `copy-${(counter += 1)}`);

		expect(pasted.nodeIDs).toEqual(['copy-1']);
		expect(pasted.document.nodes?.map((node) => node.name)).toEqual(['Set', 'Set1']);

		// The name the second node's expression addresses is rewritten, and the
		// first node is left alone.
		pasted.document.nodes![0].parameters = { text: "={{ $('Set1').json.text }}" };
		const renamed = renameNode(pasted.document, 'copy-1', 'Telegram');
		expect(renamed.nodes?.[0].parameters).toEqual({ text: "={{ $('Telegram').json.text }}" });
		expect(renamed.nodes?.[1].name).toBe('Telegram');
	});

	it('duplicates, renames and tidies without losing a node or leaving two tiles on top of each other', () => {
		let draft = document();
		draft.nodes = [...(draft.nodes ?? []), createWorkflowNode(telegram, { x: 300, y: 0 }, () => 'tg-1', ['Set'])];
		expect(draft.nodes[1].name).toBe('Telegram');

		draft.connections = [{ id: 'edge-1', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'tg-1', port: 'main' } }];
		const duplicated = duplicateNodes(draft, ['manual', 'tg-1'], { x: 40, y: 40 }, (() => {
			let n = 0;
			return () => `dup-${(n += 1)}`;
		})());
		expect(duplicated.nodeIDs).toEqual(['dup-1', 'dup-2']);
		expect(duplicated.document.nodes?.map((node) => node.name)).toEqual(['Set', 'Telegram', 'Set1', 'Telegram1']);
		expect(duplicated.document.connections?.at(-1)).toMatchObject({ source: { nodeId: 'dup-1' }, target: { nodeId: 'dup-2' } });

		const tidied = tidyDocument(duplicated.document, {});
		const boxes = (tidied.nodes ?? []).map((node) => ({ ...node.position, width: 72, height: 96 }));
		for (let left = 0; left < boxes.length; left += 1) {
			for (let right = left + 1; right < boxes.length; right += 1) {
				const a = boxes[left];
				const b = boxes[right];
				const overlaps = a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;
				expect(overlaps, `node ${left} overlaps node ${right}`).toBe(false);
			}
		}
		expect(tidied.nodes).toHaveLength(4);
		expect(uniqueNodeName('Telegram1', tidied.nodes!.map((node) => node.name))).toBe('Telegram2');
	});
});
