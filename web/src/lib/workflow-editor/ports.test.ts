import { describe, expect, it } from 'vitest';

import type { Connection, Definition, Node } from '$lib/api/generated/models';

import { canConnect, connectionFromCanvas, portLabel } from './ports';

const manual: Definition = {
	type: 'kilasflow.manual',
	version: 1,
	displayName: 'Manual Trigger',
	category: 'Triggers',
	group: ['transform'],
	source: 'builtin',
	inputs: [],
	outputs: [{ name: 'main', kind: 'main' }],
	parameters: [],
	sharedSettings: []
};

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

const toolConsumer: Definition = {
	type: 'kilasflow.agent',
	version: 1,
	displayName: 'Agent',
	category: 'AI',
	group: ['transform'],
	source: 'builtin',
	inputs: [{ name: 'tool', kind: 'ai_tool' }],
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

describe('canConnect port limits', () => {
	/**
	 * A port may bound how many edges it accepts — one language model, one
	 * memory, many tools. The compiler enforces this too; refusing here is what
	 * stops the editor offering a connection the server rejects on save.
	 */
	const agentDefinitions: Definition[] = [
		{
			type: 'test.model',
			version: 1,
			displayName: 'Model',
			category: 'AI',
			group: ['transform'],
			source: 'builtin',
			inputs: [],
			outputs: [{ name: 'model', kind: 'ai_languageModel' }],
			parameters: [],
			sharedSettings: []
		},
		{
			type: 'test.agent',
			version: 1,
			displayName: 'Agent',
			category: 'AI',
			group: ['transform'],
			source: 'builtin',
			inputs: [
				{ name: 'model', displayName: 'Chat Model', kind: 'ai_languageModel', maxConnections: 1 },
				{ name: 'tools', displayName: 'Tools', kind: 'ai_tool' }
			],
			outputs: [{ name: 'main', kind: 'main' }],
			parameters: [],
			sharedSettings: []
		}
	];

	const agentNodes: Node[] = [
		{ id: 'm1', name: 'M1', type: 'test.model', typeVersion: 1, position: { x: 0, y: 0 } },
		{ id: 'm2', name: 'M2', type: 'test.model', typeVersion: 1, position: { x: 0, y: 80 } },
		{ id: 'agent', name: 'Agent', type: 'test.agent', typeVersion: 1, position: { x: 200, y: 0 } }
	];

	it('accepts the first connection to a single-slot port', () => {
		expect(
			canConnect(
				{ source: 'm1', sourceHandle: 'model', target: 'agent', targetHandle: 'model' },
				agentNodes,
				agentDefinitions,
				[]
			)
		).toBe(true);
	});

	it('refuses a second connection to a single-slot port', () => {
		const existing: Connection[] = [
			{
				id: 'c1',
				kind: 'ai_languageModel',
				source: { nodeId: 'm1', port: 'model' },
				target: { nodeId: 'agent', port: 'model' }
			}
		];
		expect(
			canConnect(
				{ source: 'm2', sourceHandle: 'model', target: 'agent', targetHandle: 'model' },
				agentNodes,
				agentDefinitions,
				existing
			)
		).toBe(false);
	});
});
describe('portLabel', () => {
	it('prefers the display name and falls back to the port name', () => {
		expect(portLabel({ name: 'model', kind: 'ai_languageModel', displayName: 'Chat Model' })).toBe('Chat Model');
		expect(portLabel({ name: 'main', kind: 'main' })).toBe('main');
	});
});

describe('version-tolerant port lookup', () => {
	it('connects an imported node through its resolved definition', () => {
		const imported: Node[] = [
			{ id: 'manual-1', name: 'Manual Trigger', type: manual.type, typeVersion: 1, position: { x: 0, y: 0 } },
			{ id: 'set-1', name: 'Edit Fields', type: set.type, typeVersion: 3.4, position: { x: 240, y: 0 } }
		];

		expect(
			canConnect(
				{ source: 'manual-1', sourceHandle: 'main', target: 'set-1', targetHandle: 'main' },
				imported,
				[manual, set],
				[]
			)
		).toBe(true);
	});
});
