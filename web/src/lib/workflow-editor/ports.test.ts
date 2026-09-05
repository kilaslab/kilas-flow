import { describe, expect, it } from 'vitest';

import type { Definition, Node } from '$lib/api/generated/models';

import { canConnect, connectionFromCanvas } from './ports';

const manual: Definition = {
	type: 'kilasflow.manual',
	version: 1,
	displayName: 'Manual Trigger',
	category: 'Triggers',
	group: ['transform'],
	inputs: [],
	outputs: [{ Name: 'main', Kind: 'main' }],
	parameters: [],
	sharedSettings: []
};

const set: Definition = {
	type: 'kilasflow.set',
	version: 1,
	displayName: 'Set',
	category: 'Core',
	group: ['transform'],
	inputs: [{ Name: 'main', Kind: 'main' }],
	outputs: [{ Name: 'main', Kind: 'main' }],
	parameters: [],
	sharedSettings: []
};

const toolConsumer: Definition = {
	type: 'kilasflow.agent',
	version: 1,
	displayName: 'Agent',
	category: 'AI',
	group: ['transform'],
	inputs: [{ Name: 'tool', Kind: 'ai_tool' }],
	outputs: [],
	parameters: [],
	sharedSettings: []
};

const nodes: Node[] = [
	{ id: 'manual-1', name: 'Manual Trigger', type: manual.type, typeVersion: 1, position: { x: 0, y: 0 } },
	{ id: 'set-1', name: 'Set', type: set.type, typeVersion: 1, position: { x: 240, y: 0 } },
	{ id: 'agent-1', name: 'Agent', type: toolConsumer.type, typeVersion: 1, position: { x: 480, y: 0 } }
];

describe('registry-driven canvas connections', () => {
	it('creates a canonical connection only for matching declared output and input handles', () => {
		const connection = connectionFromCanvas(
			{ source: 'manual-1', sourceHandle: 'main', target: 'set-1', targetHandle: 'main' },
			nodes,
			[manual, set, toolConsumer],
			[],
			() => 'edge-1'
		);

		expect(connection).toEqual({
			id: 'edge-1',
			kind: 'main',
			source: { nodeId: 'manual-1', port: 'main' },
			target: { nodeId: 'set-1', port: 'main' }
		});
	});

	it('rejects incompatible, reversed, duplicate, and self connections before they reach the API', () => {
		const valid = { source: 'manual-1', sourceHandle: 'main', target: 'set-1', targetHandle: 'main' };
		const saved = [{ id: 'edge-1', kind: 'main', source: { nodeId: 'manual-1', port: 'main' }, target: { nodeId: 'set-1', port: 'main' } }];

		expect(canConnect({ source: 'manual-1', sourceHandle: 'main', target: 'agent-1', targetHandle: 'tool' }, nodes, [manual, set, toolConsumer], [])).toBe(false);
		expect(canConnect({ source: 'set-1', sourceHandle: 'main', target: 'manual-1', targetHandle: 'main' }, nodes, [manual, set, toolConsumer], [])).toBe(false);
		expect(canConnect(valid, nodes, [manual, set, toolConsumer], saved)).toBe(false);
		expect(canConnect({ source: 'set-1', sourceHandle: 'main', target: 'set-1', targetHandle: 'main' }, nodes, [manual, set, toolConsumer], [])).toBe(false);
	});
});
