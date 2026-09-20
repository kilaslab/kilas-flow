import { describe, expect, it } from 'vitest';

import type { Definition, Document } from '$lib/api/generated/models';

import { FRAGMENT_KIND, copySelection, pasteInto, readClipboard } from './clipboard';

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

const telegram: Definition = {
	...set,
	type: 'pack.telegram',
	version: 2,
	displayName: 'Telegram',
	inputs: [{ name: 'main', kind: 'main' }],
	outputs: [{ name: 'main', kind: 'main' }]
};

const agent: Definition = {
	...set,
	type: 'kilasflow.agent',
	displayName: 'AI Agent',
	inputs: [
		{ name: 'main', kind: 'main' },
		{ name: 'model', kind: 'ai_languageModel' }
	],
	outputs: [{ name: 'main', kind: 'main' }]
};

const unsupported: Definition[] = [1, 2, 4, 8].map((arity) => ({
	...set,
	type: 'kilasflow.unsupported',
	version: arity,
	displayName: 'Unsupported node',
	category: 'Imported'
}));

const definitions = [set, telegram, agent, ...unsupported];

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
		expect(readClipboard('hello world', definitions)).toBeNull();
		expect(readClipboard('{"nodes": "nope"}', definitions)).toBeNull();
		expect(readClipboard('{"connections": {}}', definitions)).toBeNull();
	});

	it('reads back its own fragment without touching names or types', () => {
		const payload = readClipboard(copySelection(document(), ['set-1'])!, definitions);

		expect(payload?.nodes).toHaveLength(1);
		expect(payload?.nodes[0].name).toBe('Set');
		expect(payload?.unsupported).toEqual([]);
	});

	it('maps an n8n node type onto the registered node that shares its name', () => {
		const payload = readClipboard(
			JSON.stringify({
				nodes: [{ name: 'Telegram', type: 'n8n-nodes-base.telegram', typeVersion: 1, position: [64, 128], parameters: { chatId: '-100' } }],
				connections: {}
			}),
			definitions
		);

		expect(payload?.nodes[0]).toEqual({
			id: expect.any(String),
			name: 'Telegram',
			type: 'pack.telegram',
			typeVersion: 2,
			position: { x: 64, y: 128 },
			parameters: { chatId: '-100' }
		});
	});

	it('keeps an n8n node it has no equivalent for as a placeholder covering its own edges', () => {
		// The regression this exists for: a pasted snippet whose types are
		// unknown vanished silently, or arrived as a placeholder with too few
		// ports to hold the edges it came with.
		const payload = readClipboard(
			JSON.stringify({
				nodes: [{ name: 'Spreadsheet', type: 'n8n-nodes-base.googleSheets', typeVersion: 4.5, position: [0, 0] }],
				connections: {
					Spreadsheet: { main: [[{ node: 'Target', type: 'main', index: 0 }], [{ node: 'Target', type: 'main', index: 0 }]] },
					Target: { main: [] }
				}
			}),
			definitions
		);

		const placeholder = payload?.nodes.find((node) => node.name === 'Spreadsheet');
		expect(placeholder?.type).toBe('kilasflow.unsupported');
		expect(placeholder?.typeVersion).toBe(2);
		expect(placeholder?.parameters).toMatchObject({ originalType: 'n8n-nodes-base.googleSheets', originalTypeVersion: 4.5 });
		expect(payload?.unsupported).toEqual(['n8n-nodes-base.googleSheets']);
	});

	it('rewires n8n connections through the ports the resolved definitions declare', () => {
		const payload = readClipboard(
			JSON.stringify({
				nodes: [
					{ name: 'Agent', type: 'kilasflow.agent', typeVersion: 1, position: [0, 0] },
					{ name: 'Model', type: 'kilasflow.chatModel', typeVersion: 1, position: [0, 200] },
					{ name: 'Set', type: 'kilasflow.set', typeVersion: 1, position: [400, 0] }
				],
				connections: {
					Agent: { main: [[{ node: 'Set', type: 'main', index: 0 }]] },
					Model: { ai_languageModel: [[{ node: 'Agent', type: 'ai_languageModel', index: 0 }]] }
				}
			}),
			[...definitions, { ...set, type: 'kilasflow.chatModel', displayName: 'Chat Model', outputs: [{ name: 'model', kind: 'ai_languageModel' }] }]
		);

		const byName = new Map(payload?.nodes.map((node) => [node.name, node.id]));
		expect(payload?.connections).toEqual([
			{ id: expect.any(String), kind: 'main', source: { nodeId: byName.get('Agent'), port: 'main' }, target: { nodeId: byName.get('Set'), port: 'main' } },
			{
				id: expect.any(String),
				kind: 'ai_languageModel',
				source: { nodeId: byName.get('Model'), port: 'model' },
				target: { nodeId: byName.get('Agent'), port: 'model' }
			}
		]);
		expect(payload?.dropped).toBe(0);
	});

	it('counts an edge it cannot place instead of inventing a port for it', () => {
		const payload = readClipboard(
			JSON.stringify({
				nodes: [
					{ name: 'Set', type: 'kilasflow.set', typeVersion: 1, position: [0, 0] },
					{ name: 'Model', type: 'kilasflow.chatModel', typeVersion: 1, position: [0, 200] }
				],
				connections: { Model: { ai_languageModel: [[{ node: 'Set', type: 'ai_languageModel', index: 0 }]] } }
			}),
			[...definitions, { ...set, type: 'kilasflow.chatModel', displayName: 'Chat Model', outputs: [{ name: 'model', kind: 'ai_languageModel' }] }]
		);

		expect(payload?.connections).toEqual([]);
		expect(payload?.dropped).toBe(1);
	});
});

describe('pasteInto', () => {
	it('renames, offsets and re-ids what it inserts, leaving the source document alone', () => {
		const original = document();
		const payload = readClipboard(copySelection(original, ['set-1', 'set-2'])!, definitions)!;
		let counter = 0;
		const { document: pasted, nodeIDs } = pasteInto(original, payload, { x: 40, y: 40 }, () => `new-${(counter += 1)}`);

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
});
