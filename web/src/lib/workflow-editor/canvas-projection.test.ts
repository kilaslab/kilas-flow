import { describe, expect, it } from 'vitest';

import type { Definition, Document } from '$lib/api/generated/models';

import { documentFromCanvas, reconcileCanvas, renameNode, updateNodeProperty, type CanvasDocument } from './document';

const set: Definition = {
	type: 'kilasflow.set',
	version: 1,
	displayName: 'Set',
	category: 'Transform',
	group: ['transform'],
	source: 'builtin',
	inputs: [{ name: 'main', kind: 'main' }],
	outputs: [{ name: 'main', kind: 'main' }],
	parameters: []
} as unknown as Definition;
const definitions = [set];

function chain(count: number): Document {
	const nodes = Array.from({ length: count }, (_, index) => ({
		id: `n${index}`,
		name: `set ${index}`,
		type: 'kilasflow.set',
		typeVersion: 1,
		position: { x: index * 200, y: 0 },
		parameters: { value: 'x' }
	}));
	const connections = nodes.slice(1).map((node, index) => ({
		id: `c${index + 1}`,
		kind: 'main',
		source: { nodeId: `n${index}`, port: 'main' },
		target: { nodeId: node.id, port: 'main' }
	}));
	return { schemaVersion: 1, name: 'w', nodes, connections, settings: {} } as unknown as Document;
}

/** What Svelte Flow holds after mounting: its own copies, carrying `measured`. */
function mounted(canvas: CanvasDocument): CanvasDocument {
	return {
		...canvas,
		nodes: canvas.nodes.map((node) => ({ ...node, selected: false, measured: { width: 68, height: 68 } })),
		edges: canvas.edges.map((edge) => ({ ...edge, selected: false }))
	};
}

const none = { nodeIDs: new Set<string>(), edgeIDs: new Set<string>() };

// BUG-pzkpfr: a keystroke re-projected all 300 nodes, and Svelte Flow
// re-measured every one of them because none was the object it held.
describe('reconcileCanvas', () => {
	it('rebuilds only the node a parameter edit touched, and keeps the measured objects Flow holds', () => {
		const before = chain(300);
		const held = mounted(documentFromCanvas(before, definitions));
		const after = updateNodeProperty(before, 'n7', 'parameters', 'value', 'xy');

		const next = reconcileCanvas(held, after, definitions, [], none);

		const rebuilt = next.nodes.filter((node, index) => node !== held.nodes[index]);
		expect(rebuilt.map((node) => node.id)).toEqual(['n7']);
		expect(rebuilt[0].data.workflowNode.parameters?.value).toBe('xy');
		expect(next.nodes[3]).toBe(held.nodes[3]);
		expect(next.nodes[3].measured).toEqual({ width: 68, height: 68 });
		// No connection changed, so Flow is handed the very same edge array.
		expect(next.edges).toBe(held.edges);
	});

	it('hands back the same arrays when nothing changed at all', () => {
		const document = chain(5);
		const held = mounted(documentFromCanvas(document, definitions));
		const next = reconcileCanvas(held, document, definitions, [], none);
		expect(next.nodes).toBe(held.nodes);
		expect(next.edges).toBe(held.edges);
	});

	it('relabels the connections of a renamed node and nothing else', () => {
		const before = chain(5);
		const held = mounted(documentFromCanvas(before, definitions));
		const after = renameNode(before, 'n2', 'renamed');
		const next = reconcileCanvas(held, after, definitions, [], none);
		const relabelled = next.edges.filter((edge, index) => edge !== held.edges[index]).map((edge) => edge.id);
		expect(relabelled).toEqual(['c2', 'c3']);
		expect(next.edges[1].ariaLabel).toContain('renamed');
	});

	it('rebuilds a node and a connection whose validation message changed', () => {
		const document = chain(5);
		const held = mounted(documentFromCanvas(document, definitions));
		const next = reconcileCanvas(
			held,
			document,
			definitions,
			[
				{ nodeID: 'n1', message: 'broken', code: 'x' },
				{ connectionID: 'c4', message: 'bad wire', code: 'y' }
			] as never,
			none
		);
		expect(next.nodes.filter((node, index) => node !== held.nodes[index]).map((node) => node.id)).toEqual(['n1']);
		expect(next.nodes[1].data.validationMessage).toBe('broken');
		expect(next.edges.filter((edge, index) => edge !== held.edges[index]).map((edge) => edge.id)).toEqual(['c4']);
	});

	it('writes the selection the editor holds onto only the objects whose flag differs', () => {
		const document = chain(5);
		const held = mounted(documentFromCanvas(document, definitions));
		const next = reconcileCanvas(held, document, definitions, [], { nodeIDs: new Set(['n3']), edgeIDs: new Set(['c1']) });
		expect(next.nodes[3].selected).toBe(true);
		expect(next.nodes[3].measured).toEqual({ width: 68, height: 68 });
		expect(next.nodes.filter((node, index) => node !== held.nodes[index]).map((node) => node.id)).toEqual(['n3']);
		expect(next.edges[0].selected).toBe(true);
		expect(next.edges.filter((edge, index) => edge !== held.edges[index]).map((edge) => edge.id)).toEqual(['c1']);
	});

	it('adds and drops nodes and connections the document added or removed', () => {
		const before = chain(4);
		const held = mounted(documentFromCanvas(before, definitions));
		const after = {
			...before,
			nodes: (before.nodes ?? []).filter((node) => node.id !== 'n3'),
			connections: (before.connections ?? []).filter((connection) => connection.id !== 'c3')
		};
		const grown = chain(5);
		const shrunk = reconcileCanvas(held, after, definitions, [], none);
		expect(shrunk.nodes.map((node) => node.id)).toEqual(['n0', 'n1', 'n2']);
		expect(shrunk.edges.map((edge) => edge.id)).toEqual(['c1', 'c2']);
		const regrown = reconcileCanvas(shrunk, grown, definitions, [], none);
		expect(regrown.nodes.map((node) => node.id)).toEqual(['n0', 'n1', 'n2', 'n3', 'n4']);
	});

	it('projects exactly what documentFromCanvas projects', () => {
		const document = chain(6);
		const fresh = documentFromCanvas(document, definitions);
		const reconciled = reconcileCanvas({ nodes: [], edges: [] }, document, definitions, [], none);
		const plain = (canvas: { nodes: unknown[]; edges: unknown[] }) => JSON.parse(JSON.stringify({ nodes: canvas.nodes, edges: canvas.edges }));
		expect(plain(reconciled)).toEqual(
			plain({ nodes: fresh.nodes.map((node) => ({ ...node, selected: false })), edges: fresh.edges.map((edge) => ({ ...edge, selected: false })) })
		);
	});

	it('rebuilds everything when the catalogue changes', () => {
		const document = chain(3);
		const held = mounted(documentFromCanvas(document, definitions));
		const next = reconcileCanvas(held, document, [{ ...set }], [], none);
		expect(next.nodes.every((node, index) => node !== held.nodes[index])).toBe(true);
	});
});
