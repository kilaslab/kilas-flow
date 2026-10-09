import { describe, expect, it } from 'vitest';

import type { Connection, Definition, Document, Node, Port } from '$lib/api/generated/models';

import { describeConnections, staleConnections } from './ports';

// BUG-qfp7rf: a connection that names a port its node no longer has is stored
// but cannot be drawn, so it cannot be selected or deleted. These are the
// connections the editor removes, in the step that stranded them.

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

const definitions = [
	definition('kilasflow.manual', [], [MAIN]),
	definition('kilasflow.set', [MAIN], [MAIN]),
	definition('kilasflow.vectorStorePGVector', [MAIN], [MAIN]),
	definition('kilasflow.vectorStoreTool', [{ name: 'vectorStore', kind: 'ai_vectorStore' }], [{ name: 'tool', kind: 'ai_tool' }]),
	definition('kilasflow.embeddings', [MAIN], [MAIN, { name: 'embedding', kind: 'ai_embedding' }]),
	definition('kilasflow.switch', [MAIN], [{ name: '0', kind: 'main' }]),
	definition('kilasflow.merge', [{ name: 'input1', kind: 'main' }, { name: 'input2', kind: 'main' }], [MAIN]),
	definition('kilasflow.httpRequest', [MAIN], [MAIN])
];

function node(id: string, type: string, parameters?: Node['parameters'], settings?: Node['settings']): Node {
	return {
		id,
		name: id,
		type,
		typeVersion: 1,
		position: { x: 0, y: 0 },
		...(parameters ? { parameters } : {}),
		...(settings ? { settings } : {})
	};
}

function wire(id: string, kind: string, from: [string, string], to: [string, string]): Connection {
	return { id, kind, source: { nodeId: from[0], port: from[1] }, target: { nodeId: to[0], port: to[1] } };
}

function documentOf(nodes: Node[], connections: Connection[]): Document {
	return { schemaVersion: 1, name: 'doc', nodes, connections, settings: {} } as Document;
}

const stranded = (document: Document, nodeID: string) => staleConnections(document, nodeID, definitions).map((connection) => connection.id);

describe('staleConnections', () => {
	const retrieveWires = [
		wire('c-store', 'ai_vectorStore', ['store', 'vectorStore'], ['tool', 'vectorStore']),
		wire('c-embeddings', 'ai_embedding', ['embeddings', 'embedding'], ['store', 'embedding'])
	];
	const retrieveNodes = (mode: string) => [
		node('store', 'kilasflow.vectorStorePGVector', { mode }),
		node('tool', 'kilasflow.vectorStoreTool'),
		node('embeddings', 'kilasflow.embeddings', { mode: 'cluster' })
	];

	it('finds nothing when every wire still has its port', () => {
		expect(stranded(documentOf(retrieveNodes('retrieve'), retrieveWires), 'store')).toEqual([]);
	});

	it('finds the vectorStore wire when a store leaves retrieve mode, and keeps the embeddings wire', () => {
		for (const mode of ['retrieve-as-tool', 'insert', 'getMany']) {
			expect(stranded(documentOf(retrieveNodes(mode), retrieveWires), 'store'), mode).toEqual(['c-store']);
		}
	});

	it('finds a wire into a main input the mode no longer draws', () => {
		const nodes = [node('manual', 'kilasflow.manual'), node('store', 'kilasflow.vectorStorePGVector', { mode: 'insert' })];
		const document = documentOf(nodes, [wire('c-main', 'main', ['manual', 'main'], ['store', 'main'])]);

		expect(stranded(document, 'store')).toEqual(['c-main']);
	});

	it('finds the wire of a Switch branch whose rule was removed', () => {
		const nodes = [node('switch', 'kilasflow.switch', { rules: [{}, {}] }), node('a', 'kilasflow.set'), node('b', 'kilasflow.set'), node('c', 'kilasflow.set')];
		const wires = [
			wire('c0', 'main', ['switch', '0'], ['a', 'main']),
			wire('c1', 'main', ['switch', '1'], ['b', 'main']),
			wire('c2', 'main', ['switch', '2'], ['c', 'main'])
		];

		expect(stranded(documentOf(nodes, wires), 'switch')).toEqual(['c2']);
	});

	it('finds the wires of the Merge inputs a lower input count dropped', () => {
		const wires = [
			wire('i1', 'main', ['a', 'main'], ['merge', 'input1']),
			wire('i3', 'main', ['a', 'main'], ['merge', 'input3']),
			wire('i4', 'main', ['a', 'main'], ['merge', 'input4'])
		];
		const withInputs = (numberInputs: number) => documentOf([node('a', 'kilasflow.set'), node('merge', 'kilasflow.merge', { numberInputs })], wires);

		expect(stranded(withInputs(4), 'merge')).toEqual([]);
		expect(stranded(withInputs(3), 'merge')).toEqual(['i4']);
		// Mid-edit, the field passes through a value the node does not accept.
		expect(stranded(withInputs(1), 'merge')).toEqual(['i3', 'i4']);
	});

	it('finds the wire of an error output that was switched off', () => {
		const wires = [wire('c-err', 'main', ['call', 'error'], ['fallback', 'main'])];
		const nodes = (settings: Node['settings']) => [node('call', 'kilasflow.httpRequest', undefined, settings), node('fallback', 'kilasflow.set')];

		expect(stranded(documentOf(nodes({ onError: 'continueErrorOutput' }), wires), 'call')).toEqual([]);
		expect(stranded(documentOf(nodes({ onError: 'stopWorkflow' }), wires), 'call')).toEqual(['c-err']);
	});

	it('looks at the edited node only, and at the target side as well as the source side', () => {
		const wires = [
			wire('c-store', 'ai_vectorStore', ['store', 'vectorStore'], ['tool', 'vectorStore']),
			// An unrelated node's wire to a port it does not have is not this node's to remove.
			wire('other', 'main', ['manual', 'nothing'], ['b', 'main'])
		];
		const nodes = [...retrieveNodes('retrieve-as-tool'), node('manual', 'kilasflow.manual'), node('b', 'kilasflow.set')];

		expect(stranded(documentOf(nodes, wires), 'manual')).toEqual(['other']);
		expect(stranded(documentOf(nodes, wires), 'tool')).toEqual([]);
		expect(stranded(documentOf(nodes, wires), 'b')).toEqual([]);
		expect(stranded(documentOf(nodes, wires), 'store')).toEqual(['c-store']);
	});

	it('cannot judge a node it has no definition for, or one that is gone', () => {
		const document = documentOf([node('x', 'pack.neverRegistered'), node('a', 'kilasflow.set')], [wire('c', 'main', ['x', 'main'], ['a', 'main'])]);

		expect(stranded(document, 'x')).toEqual([]);
		expect(stranded(document, 'missing')).toEqual([]);
	});
});

describe('describeConnections', () => {
	it('names the nodes and ports of each connection, and counts what it left out', () => {
		const nodes = [node('a', 'kilasflow.set'), node('b', 'kilasflow.set')];
		nodes[0].name = 'Policies Store';
		nodes[1].name = 'Handbook Tool';
		const wires = [
			wire('c1', 'ai_vectorStore', ['a', 'vectorStore'], ['b', 'vectorStore']),
			wire('c2', 'main', ['a', 'main'], ['b', 'main']),
			wire('c3', 'main', ['a', 'main'], ['b', 'main']),
			wire('c4', 'main', ['a', 'main'], ['b', 'main'])
		];

		expect(describeConnections(wires.slice(0, 1), nodes)).toBe('Policies Store vectorStore → Handbook Tool vectorStore');
		expect(describeConnections(wires, nodes, 2)).toBe('Policies Store vectorStore → Handbook Tool vectorStore; Policies Store main → Handbook Tool main; +2 more');
	});
});
