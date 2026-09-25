import { describe, expect, it } from 'vitest';

import type { Document, ImportIssue } from '$lib/api/generated/models';

import { FRAGMENT_KIND, convertN8n, copySelection, pasteInto, readClipboard, type PastePayload } from './clipboard';

function document(): Document {
	return {
		id: 'workflow-1',
		schemaVersion: 1,
		name: 'Welcome',
		nodes: [
			{ id: 'set-1', name: 'Set', type: 'kilasflow.set', typeVersion: 1, position: { x: 0, y: 0 }, parameters: { assignments: { status: 'ready' } } },
			{ id: 'set-2', name: 'Set1', type: 'kilasflow.set', typeVersion: 1, position: { x: 200, y: 0 } },
			{ id: 'other', name: 'Telegram', type: 'pack.telegram', typeVersion: 2, position: { x: 400, y: 0 } }
		],
		connections: [
			{ id: 'edge-1', kind: 'main', source: { nodeId: 'set-1', port: 'main' }, target: { nodeId: 'set-2', port: 'main' } },
			{ id: 'edge-2', kind: 'main', source: { nodeId: 'set-1', port: 'main' }, target: { nodeId: 'other', port: 'main' } }
		],
		settings: {}
	};
}

describe('copySelection', () => {
	it('carries the selected nodes and only the connections between them', () => {
		const payload = JSON.parse(copySelection(document(), ['set-1', 'set-2'])!);

		expect(payload.kind).toBe(FRAGMENT_KIND);
		expect(payload.nodes.map((node: { id: string }) => node.id)).toEqual(['set-1', 'set-2']);
		// edge-2 leaves the selection, so it is not carried: pasting must not
		// recreate a wire from a node that stayed behind.
		expect(payload.connections.map((connection: { id: string }) => connection.id)).toEqual(['edge-1']);
	});

	it('returns nothing for an empty selection', () => {
		expect(copySelection(document(), [])).toBeNull();
	});
});

describe('readClipboard', () => {
	it('declines text that is not workflow JSON', () => {
		expect(readClipboard('hello world')).toBeNull();
		expect(readClipboard('{"nodes": "nope"}')).toBeNull();
		expect(readClipboard('{"connections": {}}')).toBeNull();
	});

	it('reads back its own fragment without touching names or types', () => {
		const content = readClipboard(copySelection(document(), ['set-1'])!);

		expect(content?.kind).toBe('fragment');
		const payload = content?.kind === 'fragment' ? content.payload : null;
		expect(payload?.nodes).toHaveLength(1);
		expect(payload?.nodes[0].name).toBe('Set');
		expect(payload?.issues).toEqual([]);
	});

	it('reads a whole KilasFlow document as this editor\'s own shape, not as n8n', () => {
		const content = readClipboard(JSON.stringify(document()));

		expect(content?.kind).toBe('fragment');
		expect(content?.kind === 'fragment' ? content.payload.connections : []).toHaveLength(2);
	});

	it('hands an n8n payload over untranslated, for the importer to convert', () => {
		// BUG-txafja: types were matched here by their last segment, so a
		// JavaScript Code node became the Go one and "=" values stayed literal.
		const n8n = {
			nodes: [{ name: 'Code', type: 'n8n-nodes-base.code', typeVersion: 2, position: [0, 0], parameters: { jsCode: 'return items;' } }],
			connections: {}
		};

		expect(readClipboard(JSON.stringify(n8n))).toEqual({ kind: 'n8n', workflow: n8n });
	});
});

describe('convertN8n', () => {
	it('asks the importer and answers its nodes, connections and report', async () => {
		const asked: unknown[] = [];
		const issue: ImportIssue = { severity: 'blocking', nodeId: 'b', nodeName: 'Mystery', reason: 'no equivalent' };
		const convert = (async (body: unknown) => {
			asked.push(body);
			return {
				status: 200,
				headers: new Headers(),
				data: { nodes: [{ id: 'b', name: 'Mystery', type: 'kilasflow.unsupported', typeVersion: 1, position: { x: 0, y: 0 } }], connections: null, unsupported: [issue] }
			};
		}) as unknown as Parameters<typeof convertN8n>[1];

		const payload = await convertN8n({ nodes: [] }, convert);

		expect(asked).toEqual([{ format: 'n8n', workflow: { nodes: [] } }]);
		expect(payload.nodes.map((node) => node.type)).toEqual(['kilasflow.unsupported']);
		expect(payload.connections).toEqual([]);
		expect(payload.issues).toEqual([issue]);
	});

	it('lets the importer\'s refusal through, so the editor can name it', async () => {
		const convert = (async () => {
			throw new Error('two nodes are both named "A"');
		}) as unknown as Parameters<typeof convertN8n>[1];

		await expect(convertN8n({ nodes: [] }, convert)).rejects.toThrow('both named "A"');
	});
});

describe('pasteInto', () => {
	it('renames, offsets and re-ids what it inserts, leaving the source document alone', () => {
		const original = document();
		const content = readClipboard(copySelection(original, ['set-1', 'set-2'])!);
		if (content?.kind !== 'fragment') throw new Error('not a fragment');
		let counter = 0;
		const { document: pasted, nodeIDs } = pasteInto(original, content.payload, { x: 40, y: 40 }, () => `new-${(counter += 1)}`);

		expect(nodeIDs).toEqual(['new-1', 'new-2']);
		expect(pasted.nodes?.map((node) => node.name)).toEqual(['Set', 'Set1', 'Telegram', 'Set2', 'Set3']);
		expect(pasted.nodes?.[3].position).toEqual({ x: 40, y: 40 });
		expect(pasted.connections).toHaveLength(3);
		expect(pasted.connections?.[2]).toMatchObject({ kind: 'main', source: { nodeId: 'new-1' }, target: { nodeId: 'new-2' } });
		// The workflow being pasted into is unchanged: the editor swaps the
		// draft for the returned one so undo can hand the original back.
		expect(original.nodes).toHaveLength(3);
		expect(original.connections).toHaveLength(2);
	});

	it('points the report at the nodes as they landed, renamed and re-ided', () => {
		const payload: PastePayload = {
			nodes: [{ id: 'n8n-a', name: 'Set', type: 'kilasflow.set', typeVersion: 1, position: { x: 0, y: 0 } }],
			connections: [],
			issues: [
				{ severity: 'lossy', nodeId: 'n8n-a', nodeName: 'Set', field: 'notes', reason: 'not carried' },
				{ severity: 'dropped', field: 'pinData', reason: 'not carried' }
			]
		};

		const { issues } = pasteInto(document(), payload, { x: 0, y: 0 }, () => 'landed-1');

		// "Set" is taken on this canvas, so the pasted node is "Set2".
		expect(issues[0]).toMatchObject({ nodeId: 'landed-1', nodeName: 'Set2', field: 'notes' });
		expect(issues[1]).toEqual(payload.issues[1]);
	});

	it('points a connection entry, which names its node but carries no id, at the node as it landed', () => {
		const payload: PastePayload = {
			nodes: [{ id: 'n8n-a', name: 'Set', type: 'kilasflow.set', typeVersion: 1, position: { x: 0, y: 0 } }],
			connections: [],
			issues: [
				{ severity: 'lossy', nodeName: 'Set', reason: 'the connection from "Set" to "Model" was held back' },
				{ severity: 'lossy', nodeName: 'Elsewhere', reason: 'a connection starts at "Elsewhere", which is not a node' }
			]
		};

		const { issues } = pasteInto(document(), payload, { x: 0, y: 0 }, () => 'landed-1');

		expect(issues[0]).toMatchObject({ nodeId: 'landed-1', nodeName: 'Set2' });
		// A name that is not one of the pasted nodes is left as the importer wrote it,
		// never attached to the canvas's own node of that name.
		expect(issues[1]).toEqual(payload.issues[1]);
	});
});
