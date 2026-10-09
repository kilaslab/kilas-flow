import { describe, expect, it } from 'vitest';

import type { Connection, Definition, Node, Port } from '$lib/api/generated/models';

import { connectionForPickedNode, resolvedPorts } from './ports';

// BUG-w1jp70: a node picked from the "+" of an existing node's port is wired
// to that port. From a main OUTPUT the new node is the next step (the port
// feeds its main input). From an empty attachment INPUT slot the new node is
// the provider: its output of the slot's kind feeds the slot. The wiring used
// to look for an input of the slot's kind on the new node, which a sub-node
// does not have, so a node picked for a slot arrived unwired.

const MAIN: Port = { name: 'main', kind: 'main' };

function definition(type: string, inputs: Port[], outputs: Port[]): Definition {
	return {
		type,
		version: 1,
		displayName: type,
		category: 'AI',
		group: ['transform'],
		source: 'builtin',
		inputs,
		outputs,
		parameters: [],
		sharedSettings: []
	};
}

function nodeOf(id: string, type: string): Node {
	return { id, name: id, type, typeVersion: 1, position: { x: 0, y: 0 } };
}

const agentDefinition = definition(
	'kilasflow.agent',
	[
		MAIN,
		{ name: 'model', displayName: 'Chat Model', kind: 'ai_languageModel', required: true, maxConnections: 1 },
		{ name: 'tools', displayName: 'Tools', kind: 'ai_tool' }
	],
	[MAIN]
);
const chatModelDefinition = definition('kilasflow.chatModel', [], [{ name: 'model', kind: 'ai_languageModel' }]);
const toolDefinition = definition('kilasflow.httpTool', [], [{ name: 'tool', kind: 'ai_tool' }]);
const setDefinition = definition('kilasflow.set', [MAIN], [MAIN]);
const manualDefinition = definition('kilasflow.manual', [], [MAIN]);
const definitions = [agentDefinition, chatModelDefinition, toolDefinition, setDefinition, manualDefinition];

function picked(id: string, type: string, definitionOfPicked: Definition) {
	return { node: nodeOf(id, type), ports: resolvedPorts(nodeOf(id, type), definitionOfPicked) };
}

describe('connectionForPickedNode', () => {
	const agent = nodeOf('agent', 'kilasflow.agent');
	const manual = nodeOf('manual', 'kilasflow.manual');

	it('feeds an empty attachment slot from the picked node, not the other way round', () => {
		const { node, ports } = picked('chat', 'kilasflow.chatModel', chatModelDefinition);

		const connection = connectionForPickedNode(
			{ id: node.id, ports },
			{ nodeID: 'agent', port: 'model' },
			'ai_languageModel',
			[agent, node],
			definitions,
			[],
			() => 'wire-1'
		);

		expect(connection).toEqual({
			id: 'wire-1',
			kind: 'ai_languageModel',
			source: { nodeId: 'chat', port: 'model' },
			target: { nodeId: 'agent', port: 'model' }
		});
	});

	it('fills the unbounded Tools slot the same way', () => {
		const { node, ports } = picked('tool', 'kilasflow.httpTool', toolDefinition);

		const connection = connectionForPickedNode({ id: node.id, ports }, { nodeID: 'agent', port: 'tools' }, 'ai_tool', [agent, node], definitions, [], () => 'wire-2');

		expect(connection?.source).toEqual({ nodeId: 'tool', port: 'tool' });
		expect(connection?.target).toEqual({ nodeId: 'agent', port: 'tools' });
		expect(connection?.kind).toBe('ai_tool');
	});

	it('wires a next step after a main output from that output to the new node main input', () => {
		const { node, ports } = picked('set', 'kilasflow.set', setDefinition);

		const connection = connectionForPickedNode({ id: node.id, ports }, { nodeID: 'manual', port: 'main' }, null, [manual, node], definitions, [], () => 'wire-3');

		expect(connection).toEqual({
			id: 'wire-3',
			kind: 'main',
			source: { nodeId: 'manual', port: 'main' },
			target: { nodeId: 'set', port: 'main' }
		});
	});

	it('adds no wire when the picked node has nothing of the slot kind to give', () => {
		// A Set is not a tool; the picker should not have offered it, but a
		// stale pick must not produce an edge the canvas would refuse.
		const { node, ports } = picked('set', 'kilasflow.set', setDefinition);

		expect(connectionForPickedNode({ id: node.id, ports }, { nodeID: 'agent', port: 'tools' }, 'ai_tool', [agent, node], definitions, [])).toBeNull();
	});

	it('adds no wire to a next step that takes no main input', () => {
		const { node, ports } = picked('chat', 'kilasflow.chatModel', chatModelDefinition);

		expect(connectionForPickedNode({ id: node.id, ports }, { nodeID: 'manual', port: 'main' }, null, [manual, node], definitions, [])).toBeNull();
	});

	it('goes through the same validator a dragged wire does, so a full single-slot port refuses a second model', () => {
		const { node, ports } = picked('chat-2', 'kilasflow.chatModel', chatModelDefinition);
		const taken: Connection[] = [
			{ id: 'c1', kind: 'ai_languageModel', source: { nodeId: 'chat', port: 'model' }, target: { nodeId: 'agent', port: 'model' } }
		];

		expect(
			connectionForPickedNode({ id: node.id, ports }, { nodeID: 'agent', port: 'model' }, 'ai_languageModel', [agent, nodeOf('chat', 'kilasflow.chatModel'), node], definitions, taken)
		).toBeNull();
	});
});
