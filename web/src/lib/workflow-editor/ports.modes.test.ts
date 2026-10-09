import { describe, expect, it } from 'vitest';

import type { Connection, Definition, Node, Port } from '$lib/api/generated/models';

import { canConnect, resolvedPorts } from './ports';

// BUG-pc284d: the vector stores and the embeddings node resolve their ports
// from their mode on the server (nodes/rag.go vectorStorePortsFor,
// embeddingsPortsFor). The expectations below are written from those Go cases
// (nodes/vectorstore_retrieve_test.go among them); ports.golden.test.ts holds the
// same functions to the full Go-generated table.

const MAIN: Port = { name: 'main', kind: 'main' };
const EMBEDDING: Port = { name: 'embedding', kind: 'ai_embedding' };
const DOCUMENT: Port = { name: 'document', kind: 'ai_document' };
// n8n's retrieve store takes exactly one embeddings model, and the compiler
// refuses a store without one.
const RETRIEVE_EMBEDDING: Port = { name: 'embedding', kind: 'ai_embedding', required: true, maxConnections: 1 };
const VECTOR_STORE: Port = { name: 'vectorStore', kind: 'ai_vectorStore' };
const TOOL: Port = { name: 'tool', kind: 'ai_tool' };

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

function nodeOf(id: string, type: string, parameters?: Node['parameters'], settings?: Node['settings']): Node {
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

describe('vector store ports follow the mode', () => {
	const storeTypes = ['kilasflow.vectorStore', 'kilasflow.vectorStorePGVector'];

	const modes: Array<{ parameters: Node['parameters']; inputs: Port[]; outputs: Port[]; why: string }> = [
		{ parameters: { mode: 'insert' }, inputs: [EMBEDDING, DOCUMENT], outputs: [MAIN], why: 'insert takes its embeddings and its documents on typed ports, never items on main' },
		{ parameters: { mode: 'getMany' }, inputs: [MAIN, EMBEDDING], outputs: [MAIN], why: 'Get Many takes the query items on main and the embeddings beside them' },
		{ parameters: { mode: 'load' }, inputs: [MAIN, EMBEDDING], outputs: [MAIN], why: "load is n8n's own value for Get Many" },
		{ parameters: { mode: 'retrieve' }, inputs: [RETRIEVE_EMBEDDING], outputs: [VECTOR_STORE], why: 'retrieve hands the store to a Vector Store Tool: embeddings in, vectorStore out, no items' },
		{ parameters: { mode: 'retrieve-as-tool' }, inputs: [EMBEDDING], outputs: [TOOL], why: 'retrieve-as-tool is the store seen as an agent tool' },
		{ parameters: { mode: 'delete' }, inputs: [MAIN], outputs: [MAIN], why: 'delete is a native sequential operation and stays on main' }
	];

	for (const type of storeTypes) {
		const declared = definition(type, [MAIN], [MAIN]);

		for (const { parameters, inputs, outputs, why } of modes) {
			it(`${type} in mode ${String(parameters?.mode)}: ${why}`, () => {
				const resolved = resolvedPorts(nodeOf('store', type, parameters), declared);

				expect(resolved.inputs).toEqual(inputs);
				expect(resolved.outputs).toEqual(outputs);
			});
		}

		it(`${type} keeps main to main for a native graph that sets only an operation`, () => {
			for (const operation of ['insert', 'search', 'delete', 'retrieve']) {
				const resolved = resolvedPorts(nodeOf('store', type, { operation }), declared);

				expect(resolved.inputs, `operation ${operation}`).toEqual([MAIN]);
				expect(resolved.outputs, `operation ${operation}`).toEqual([MAIN]);
			}
			expect(resolvedPorts(nodeOf('store', type), declared).inputs).toEqual([MAIN]);
		});
	}

	it('lets a mode, not an operation, say retrieve', () => {
		// A stored native graph's `operation: retrieve` meant a search before the
		// mode existed, and it still does (nodes/vectorstore_retrieve_test.go).
		const declared = definition('kilasflow.vectorStore', [MAIN], [MAIN]);
		const searching = resolvedPorts(nodeOf('store', 'kilasflow.vectorStore', { operation: 'retrieve' }), declared);
		const retrieving = resolvedPorts(nodeOf('store', 'kilasflow.vectorStore', { mode: 'retrieve' }), declared);

		expect(searching.outputs.map((port) => port.kind)).toEqual(['main']);
		expect(retrieving.outputs.map((port) => port.kind)).toEqual(['ai_vectorStore']);
	});

	it('draws the typed ports once a mode is chosen, whatever the stale operation says', () => {
		const declared = definition('kilasflow.vectorStore', [MAIN], [MAIN]);
		const resolved = resolvedPorts(nodeOf('store', 'kilasflow.vectorStore', { operation: 'insert', mode: 'retrieve' }), declared);

		expect(resolved.inputs).toEqual([RETRIEVE_EMBEDDING]);
		expect(resolved.outputs).toEqual([VECTOR_STORE]);
	});

	it('adds the error output beside a retrieve store port as it does for every node', () => {
		const declared = definition('kilasflow.vectorStorePGVector', [MAIN], [MAIN]);
		const resolved = resolvedPorts(
			nodeOf('store', 'kilasflow.vectorStorePGVector', { mode: 'retrieve-as-tool' }, { onError: 'continueErrorOutput' }),
			declared
		);

		expect(resolved.outputs.map((port) => port.name)).toEqual(['tool', 'error']);
	});
});

describe('embeddings node ports follow the mode', () => {
	const declared = definition('kilasflow.embeddings', [MAIN], [MAIN, EMBEDDING]);

	it('is a cluster sub-node with no input and only the embedding output in cluster mode', () => {
		for (const mode of ['cluster', 'Cluster', ' cluster ']) {
			const resolved = resolvedPorts(nodeOf('embeddings', 'kilasflow.embeddings', { mode }), declared);

			expect(resolved.inputs, mode).toEqual([]);
			expect(resolved.outputs, mode).toEqual([EMBEDDING]);
		}
	});

	it('keeps main in and main plus embedding out in every other mode', () => {
		const others: Array<Node['parameters']> = [undefined, {}, { mode: 'sequential' }, { mode: '' }, { mode: null }, { mode: 'frobnicate' }, { model: 'text-embedding-3-small' }];
		for (const parameters of others) {
			const resolved = resolvedPorts(nodeOf('embeddings', 'kilasflow.embeddings', parameters), declared);

			expect(resolved.inputs, JSON.stringify(parameters)).toEqual([MAIN]);
			expect(resolved.outputs, JSON.stringify(parameters)).toEqual([MAIN, EMBEDDING]);
		}
	});
});

describe('canConnect through the ports a mode resolves', () => {
	const storeDefinition = definition('kilasflow.vectorStorePGVector', [MAIN], [MAIN]);
	const embeddingsDefinition = definition('kilasflow.embeddings', [MAIN], [MAIN, EMBEDDING]);
	const toolDefinition = definition(
		'kilasflow.vectorStoreTool',
		[
			{ name: 'vectorStore', displayName: 'Vector Store', kind: 'ai_vectorStore', required: true, maxConnections: 1 },
			{ name: 'model', displayName: 'Chat Model', kind: 'ai_languageModel', required: true, maxConnections: 1 }
		],
		[TOOL]
	);
	const manualDefinition = definition('kilasflow.manual', [], [MAIN]);
	const definitions = [storeDefinition, embeddingsDefinition, toolDefinition, manualDefinition];

	const canvas = (mode: string): Node[] => [
		nodeOf('store', storeDefinition.type, { mode }),
		nodeOf('embeddings', embeddingsDefinition.type, { mode: 'cluster' }),
		nodeOf('tool', toolDefinition.type),
		nodeOf('manual', manualDefinition.type)
	];

	const toTool = { source: 'store', sourceHandle: 'vectorStore', target: 'tool', targetHandle: 'vectorStore' };
	const fromEmbeddings = { source: 'embeddings', sourceHandle: 'embedding', target: 'store', targetHandle: 'embedding' };

	it('lets a retrieve store feed a Vector Store Tool and takes its embeddings', () => {
		const nodes = canvas('retrieve');

		expect(canConnect(toTool, nodes, definitions, [])).toBe(true);
		expect(canConnect(fromEmbeddings, nodes, definitions, [])).toBe(true);
	});

	it('gives a retrieve store one embeddings model, as the compiler does', () => {
		const nodes = [...canvas('retrieve'), nodeOf('embeddings-2', embeddingsDefinition.type, { mode: 'cluster' })];
		const taken: Connection[] = [
			{ id: 'c1', kind: 'ai_embedding', source: { nodeId: 'embeddings', port: 'embedding' }, target: { nodeId: 'store', port: 'embedding' } }
		];

		expect(canConnect({ ...fromEmbeddings, source: 'embeddings-2' }, nodes, definitions, taken)).toBe(false);
	});

	it('draws no vectorStore output on a store that is not in retrieve mode', () => {
		for (const mode of ['insert', 'getMany', 'retrieve-as-tool']) {
			expect(canConnect(toTool, canvas(mode), definitions, []), mode).toBe(false);
		}
	});

	it('draws no main input on an insert store, so the items go to its loader', () => {
		const nodes = canvas('insert');
		const items = { source: 'manual', sourceHandle: 'main', target: 'store', targetHandle: 'main' };

		expect(canConnect(items, nodes, definitions, [])).toBe(false);
		expect(canConnect(fromEmbeddings, nodes, definitions, [])).toBe(true);
	});

	it('refuses to re-draw a wire the new mode has no handle for', () => {
		// The wire is not pruned when the mode changes: the document keeps the
		// connection, the canvas has no handle to draw it on, and the compiler names
		// it on save (unknown_port). A Switch that lost a rule takes the same path.
		const retrieveAsTool = canvas('retrieve-as-tool');

		expect(resolvedPorts(retrieveAsTool[0], storeDefinition).outputs.map((port) => port.name)).toEqual(['tool']);
		expect(canConnect(toTool, retrieveAsTool, definitions, [])).toBe(false);
	});
});
