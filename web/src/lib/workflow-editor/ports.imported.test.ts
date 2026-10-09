import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import type { Connection, Definition, Document, Node, Port } from '$lib/api/generated/models';

import { documentFromCanvas, reconcileCanvas, updateNodeProperty, type EditorFlowNode } from './document';
import { mainPorts, attachmentPorts } from './node-visual';
import { resolvedPorts } from './ports';

// internal/interop/n8n/testdata/imported_ports.golden.json holds the documents
// the n8n importer really produces for the RAG fixtures (templates 16706, 3647,
// 4799, 5908 and the insert / retrieve shapes), with the static ports of every
// definition they use. The importer resolves each edge through PortsFor, so every
// edge names a port the *server* has for that node's mode. The canvas draws an
// edge only when both handles exist, so this is the check that an imported RAG
// workflow arrives with every wire attached (BUG-pc284d).
//
// The golden holds only what decides ports (node identity, mode / operation /
// numberInputs, On Error, the connections), so a default the importer starts
// filling in for some unrelated node does not touch it; the document the editor
// would hold is rebuilt from it below.
type ImportedGolden = {
	definitions: Array<{ type: string; version: number; inputs: Port[] | null; outputs: Port[] | null }>;
	workflows: Array<{ name: string; document: { nodes: Omit<Node, 'position'>[]; connections: Connection[] } }>;
};

const golden = JSON.parse(
	readFileSync(fileURLToPath(new URL('../../../../internal/interop/n8n/testdata/imported_ports.golden.json', import.meta.url)), 'utf8')
) as ImportedGolden;

function documentOf(slim: ImportedGolden['workflows'][number]['document'], name: string): Document {
	return {
		schemaVersion: 1,
		name,
		nodes: slim.nodes.map((node, index) => ({ ...node, position: { x: index * 220, y: 0 } })),
		connections: slim.connections,
		settings: {}
	} as Document;
}

const definitions: Definition[] = golden.definitions.map((declared) => ({
	type: declared.type,
	version: declared.version,
	displayName: declared.type,
	category: 'AI',
	group: ['transform'],
	source: 'builtin',
	inputs: declared.inputs ?? [],
	outputs: declared.outputs ?? [],
	parameters: [],
	sharedSettings: []
}));

/** The handles a projected node draws, by the same helpers canvas-node.svelte uses. */
function handlesOf(node: EditorFlowNode) {
	const ports = resolvedPorts(node.data.workflowNode, node.data.definition);
	return {
		inputs: [...mainPorts(ports.inputs), ...attachmentPorts(ports.inputs)].map((port) => port.name),
		outputs: [...mainPorts(ports.outputs), ...attachmentPorts(ports.outputs)].map((port) => port.name)
	};
}

function workflowNamed(name: string): Document {
	const found = golden.workflows.find((workflow) => workflow.name === name);
	if (!found) throw new Error(`the golden has no workflow ${name}`);
	return documentOf(found.document, name);
}

const none = { nodeIDs: new Set<string>(), edgeIDs: new Set<string>() };

describe('imported n8n RAG workflows on the canvas', () => {
	it('reads the six fixtures the importer produced', () => {
		expect(golden.workflows.map((workflow) => workflow.name)).toHaveLength(6);
		for (const workflow of golden.workflows) expect(workflow.document.connections?.length).toBeGreaterThan(5);
	});

	for (const { name, document: slim } of golden.workflows) {
		it(`${name}: every edge lands on a handle both of its nodes draw`, () => {
			const document = documentOf(slim, name);
			const canvas = documentFromCanvas(document, definitions);
			const byID = new Map(canvas.nodes.map((node) => [node.id, node]));

			expect(canvas.edges).toHaveLength(document.connections?.length ?? 0);
			for (const edge of canvas.edges) {
				const source = byID.get(edge.source);
				const target = byID.get(edge.target);
				expect(source, `edge ${edge.id} source`).toBeDefined();
				expect(target, `edge ${edge.id} target`).toBeDefined();
				expect(handlesOf(source!).outputs, `${source!.data.workflowNode.name} output ${edge.sourceHandle}`).toContain(edge.sourceHandle);
				expect(handlesOf(target!).inputs, `${target!.data.workflowNode.name} input ${edge.targetHandle}`).toContain(edge.targetHandle);
			}
		});
	}

	it('draws template 16706 retrieval side: an embeddings sub-node into a retrieve store into the Vector Store Tool', () => {
		const canvas = documentFromCanvas(workflowNamed('n8n_rag_16706.json'), definitions);
		const handles = (name: string) => handlesOf(canvas.nodes.find((node) => node.data.workflowNode.name === name)!);

		// The query side: a cluster embeddings node has no input and one output,
		// the retrieve store takes embeddings only and gives its store.
		expect(handles('Embeddings Query')).toEqual({ inputs: [], outputs: ['embedding'] });
		expect(handles('Policies Store')).toEqual({ inputs: ['embedding'], outputs: ['vectorStore'] });
		expect(handles('Answer questions with a vector store').inputs).toEqual(expect.arrayContaining(['vectorStore', 'model']));
		// The ingest side: an insert store takes its documents and embeddings on
		// typed ports, and its items go to the loader.
		expect(handles('Postgres PGVector Store')).toEqual({ inputs: ['embedding', 'document'], outputs: ['main'] });
		expect(handles('Default Data Loader').inputs).toEqual(expect.arrayContaining(['main', 'splitter']));
	});

	it('keeps the connection and redraws the handles when the mode of an imported store changes', () => {
		const before = workflowNamed('n8n_rag_16706.json');
		// What Svelte Flow holds after mounting: its own copies, measured.
		const projected = documentFromCanvas(before, definitions);
		const held = {
			nodes: projected.nodes.map((node) => ({ ...node, selected: false, measured: { width: 68, height: 68 } })),
			edges: projected.edges.map((edge) => ({ ...edge, selected: false }))
		};
		const store = before.nodes!.find((node) => node.name === 'Policies Store')!;
		const wire = before.connections!.find((connection) => connection.source.nodeId === store.id && connection.kind === 'ai_vectorStore')!;

		const after = updateNodeProperty(before, store.id, 'parameters', 'mode', 'retrieve-as-tool');
		const next = reconcileCanvas(held, after, definitions, [], none);

		// Only the store is rebuilt, which is also what makes Flow measure it again
		// and register its new handles.
		const rebuilt = next.nodes.filter((node, index) => node !== held.nodes[index]);
		expect(rebuilt.map((node) => node.data.workflowNode.name)).toEqual(['Policies Store']);
		expect(handlesOf(rebuilt[0])).toEqual({ inputs: ['embedding'], outputs: ['tool'] });

		// The wire that no longer fits is neither pruned nor redrawn: the document
		// keeps it, the canvas has no vectorStore handle for it, and the compiler
		// names it when the workflow is activated or run (a save does not compile)
		// — what a Switch that lost a rule does.
		expect(after.connections).toContainEqual(wire);
		const edge = next.edges.find((candidate) => candidate.id === wire.id)!;
		expect(edge.sourceHandle).toBe('vectorStore');
		expect(handlesOf(rebuilt[0]).outputs).not.toContain(edge.sourceHandle);

		// Choosing the mode back restores the handle the wire was drawn on.
		const restored = reconcileCanvas(next, updateNodeProperty(after, store.id, 'parameters', 'mode', 'retrieve'), definitions, [], none);
		const restoredStore = restored.nodes.find((node) => node.id === store.id)!;
		expect(handlesOf(restoredStore).outputs).toContain(edge.sourceHandle);
	});
});
