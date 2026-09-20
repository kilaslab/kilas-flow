import { describe, expect, it } from 'vitest';

import type { Connection, Definition, Node } from '$lib/api/generated/models';

import { canConnect, connectionFromCanvas, portLabel, resolvedPorts } from './ports';

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

// Switch, Merge and the datastore derive their ports from their own parameters
// on the server (nodes/flow.go, nodes/datastore.go); these definitions mirror
// what the registry publishes for them.
const switchDefinition: Definition = {
	type: 'kilasflow.switch',
	version: 1,
	displayName: 'Switch',
	category: 'Flow',
	group: ['transform'],
	source: 'builtin',
	inputs: [{ name: 'main', kind: 'main' }],
	outputs: [{ name: '0', displayName: 'Rule 1', kind: 'main' }],
	parameters: [],
	sharedSettings: []
};

const mergeDefinition: Definition = {
	type: 'kilasflow.merge',
	version: 1,
	displayName: 'Merge',
	category: 'Core',
	group: ['transform'],
	source: 'builtin',
	inputs: [
		{ name: 'input1', displayName: 'Input 1', kind: 'main' },
		{ name: 'input2', displayName: 'Input 2', kind: 'main' }
	],
	outputs: [{ name: 'main', kind: 'main' }],
	parameters: [],
	sharedSettings: []
};

const datastoreDefinition: Definition = {
	type: 'kilasflow.datastore',
	version: 1,
	displayName: 'Data table',
	category: 'Datastore',
	group: ['input'],
	source: 'builtin',
	inputs: [{ name: 'main', kind: 'main' }],
	outputs: [{ name: 'main', kind: 'main' }],
	parameters: [],
	sharedSettings: []
};

const switchBase = { id: 'switch-1', name: 'Switch', type: switchDefinition.type, typeVersion: 1, position: { x: 0, y: 0 } };
const mergeBase = { id: 'merge-1', name: 'Merge', type: mergeDefinition.type, typeVersion: 1, position: { x: 0, y: 0 } };
const datastoreBase = {
	id: 'datastore-1',
	name: 'Data table',
	type: datastoreDefinition.type,
	typeVersion: 1,
	position: { x: 0, y: 0 }
};

describe('resolvedPorts', () => {
	it('draws one Switch output per rule, named by position', () => {
		const node: Node = { ...switchBase, parameters: { rules: [{ conditions: [] }, { conditions: [] }, { conditions: [] }] } };
		const { inputs, outputs } = resolvedPorts(node, switchDefinition);

		expect(inputs).toEqual(switchDefinition.inputs);
		expect(outputs.map((port) => port.name)).toEqual(['0', '1', '2']);
		expect(outputs.map(portLabel)).toEqual(['Rule 1', 'Rule 2', 'Rule 3']);
	});

	it('labels a renamed Switch branch without moving its wire', () => {
		const node: Node = {
			...switchBase,
			parameters: {
				rules: [{ outputKey: 'gold' }, { renameOutput: 'silver' }, { outputKey: '', renameOutput: '' }]
			}
		};
		const { outputs } = resolvedPorts(node, switchDefinition);

		expect(outputs.map((port) => port.name)).toEqual(['0', '1', '2']);
		expect(outputs.map(portLabel)).toEqual(['gold', 'silver', 'Rule 3']);
	});

	it('reads the rules out of the n8n {values} wrapper', () => {
		const node: Node = { ...switchBase, parameters: { rules: { values: [{ outputKey: 'gold' }, {}] } } };
		const { outputs } = resolvedPorts(node, switchDefinition);

		expect(outputs.map((port) => port.name)).toEqual(['0', '1']);
		expect(outputs.map(portLabel)).toEqual(['gold', 'Rule 2']);
	});

	it('keeps the one port the picker shows when the server would reject the rule set', () => {
		const unreadable: Node[] = [
			{ ...switchBase, parameters: {} },
			{ ...switchBase, parameters: { rules: 'gold' } },
			{ ...switchBase, parameters: { rules: {}, fallbackOutput: 'extra' } },
			// An empty list is an error server-side too, so it falls back with
			// the rest rather than drawing a node with no way out.
			{ ...switchBase, parameters: { rules: [] } },
			// One entry that is not an object fails the whole node's rule set.
			{ ...switchBase, parameters: { rules: [{}, 'not an object'] } },
			// Past the ceiling the server declares nothing rather than the first
			// sixty-four, so the canvas must not either.
			{ ...switchBase, parameters: { rules: Array.from({ length: 65 }, () => ({})) } }
		];

		for (const node of unreadable) {
			expect(resolvedPorts(node, switchDefinition).outputs).toEqual([
				{ name: '0', displayName: 'Rule 1', kind: 'main' }
			]);
		}
	});

	it('adds the fallback output after the rules that are there', () => {
		const node: Node = { ...switchBase, parameters: { rules: [{}, {}], fallbackOutput: 'extra' } };
		const { outputs } = resolvedPorts(node, switchDefinition);

		expect(outputs.map((port) => port.name)).toEqual(['0', '1', '2']);
		expect(outputs.map(portLabel)).toEqual(['Rule 1', 'Rule 2', 'Fallback']);
	});

	it('draws every rule up to the server ceiling', () => {
		const rules = Array.from({ length: 64 }, (_, index) => ({ outputKey: `rule-${index}` }));
		const node: Node = { ...switchBase, parameters: { rules } };
		const { outputs } = resolvedPorts(node, switchDefinition);

		expect(outputs).toHaveLength(64);
		expect(outputs.at(-1)).toEqual({ name: '63', displayName: 'rule-63', kind: 'main' });
	});

	it('gives Merge as many inputs as it was told to take', () => {
		const absent: Node = { ...mergeBase, parameters: {} };
		const four: Node = { ...mergeBase, parameters: { numberInputs: 4 } };
		const one: Node = { ...mergeBase, parameters: { numberInputs: 1 } };
		const ninetyNine: Node = { ...mergeBase, parameters: { numberInputs: 99 } };

		expect(resolvedPorts(absent, mergeDefinition).inputs.map((port) => port.name)).toEqual(['input1', 'input2']);
		expect(resolvedPorts(four, mergeDefinition).inputs.map(portLabel)).toEqual([
			'Input 1',
			'Input 2',
			'Input 3',
			'Input 4'
		]);
		expect(resolvedPorts(one, mergeDefinition).inputs).toHaveLength(2);
		expect(resolvedPorts(ninetyNine, mergeDefinition).inputs).toHaveLength(32);
		expect(resolvedPorts(four, mergeDefinition).outputs).toEqual(mergeDefinition.outputs);
	});

	it('forks a datastore branch operation into two outputs', () => {
		for (const operation of ['ifExists', 'ifNotExists']) {
			const node: Node = { ...datastoreBase, parameters: { operation } };
			const { inputs, outputs } = resolvedPorts(node, datastoreDefinition);

			expect(inputs).toEqual(datastoreDefinition.inputs);
			expect(outputs).toEqual([
				{ name: 'main', kind: 'main' },
				{ name: 'main', kind: 'main' }
			]);
		}

		const insert: Node = { ...datastoreBase, parameters: { operation: 'insert' } };
		expect(resolvedPorts(insert, datastoreDefinition).outputs).toEqual(datastoreDefinition.outputs);
	});

	it('leaves a node without a port resolver on its definition', () => {
		const node: Node = { id: 'set-1', name: 'Set', type: set.type, typeVersion: 1, position: { x: 0, y: 0 } };
		const { inputs, outputs } = resolvedPorts(node, set);

		expect(inputs).toEqual(set.inputs);
		expect(outputs).toEqual(set.outputs);
	});
});

describe('canConnect through resolved ports', () => {
	it('accepts a wire to a Switch branch the rules declare and refuses one past them', () => {
		const canvas: Node[] = [
			{ ...switchBase, parameters: { rules: [{}, {}, {}] } },
			{ id: 'set-1', name: 'Set', type: set.type, typeVersion: 1, position: { x: 240, y: 0 } }
		];

		expect(
			canConnect(
				{ source: 'switch-1', sourceHandle: '2', target: 'set-1', targetHandle: 'main' },
				canvas,
				[switchDefinition, set],
				[]
			)
		).toBe(true);
		expect(
			canConnect(
				{ source: 'switch-1', sourceHandle: '3', target: 'set-1', targetHandle: 'main' },
				canvas,
				[switchDefinition, set],
				[]
			)
		).toBe(false);
	});

	it('accepts a wire to the fourth Merge input and refuses a fifth', () => {
		const canvas: Node[] = [
			{ id: 'manual-1', name: 'Manual Trigger', type: manual.type, typeVersion: 1, position: { x: 0, y: 0 } },
			{ ...mergeBase, parameters: { numberInputs: 4 } }
		];

		expect(
			canConnect(
				{ source: 'manual-1', sourceHandle: 'main', target: 'merge-1', targetHandle: 'input4' },
				canvas,
				[manual, mergeDefinition],
				[]
			)
		).toBe(true);
		expect(
			canConnect(
				{ source: 'manual-1', sourceHandle: 'main', target: 'merge-1', targetHandle: 'input5' },
				canvas,
				[manual, mergeDefinition],
				[]
			)
		).toBe(false);
	});
});
