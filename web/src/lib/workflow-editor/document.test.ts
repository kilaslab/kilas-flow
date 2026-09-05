import { describe, expect, it } from 'vitest';

import type { Definition, Document } from '$lib/api/generated/models';

import {
	createWorkflowNode,
	nextNodePosition,
	documentFromCanvas,
	toWorkflowInput,
	updateNodeProperty,
	workflowDocumentEquals
} from './document';

const manual: Definition = {
	type: 'kilasflow.manual',
	version: 1,
	displayName: 'Manual Trigger',
	category: 'Triggers',
	inputs: [],
	outputs: [{ Name: 'main', Kind: 'main' }],
	parameters: [],
	sharedSettings: [{ key: 'continueOnFail', label: 'Continue on fail', kind: 'boolean', required: false, default: false }]
};

const set: Definition = {
	type: 'kilasflow.set',
	version: 1,
	displayName: 'Set',
	category: 'Core',
	inputs: [{ Name: 'main', Kind: 'main' }],
	outputs: [{ Name: 'main', Kind: 'main' }],
	parameters: [{ key: 'assignments', label: 'Assignments', kind: 'keyValue', required: true }],
	sharedSettings: []
};

function savedDocument(): Document {
	return {
		id: 'workflow-1',
		schemaVersion: 1,
		name: 'Welcome customer',
		nodes: [
			{ id: 'manual-1', name: 'Manual Trigger', type: manual.type, typeVersion: 1, position: { x: 32, y: 48 }, settings: { continueOnFail: false } },
			{ id: 'set-1', name: 'Set', type: set.type, typeVersion: 1, position: { x: 292, y: 48 }, parameters: { assignments: { status: 'ready' } } }
		],
		connections: [{ id: 'edge-1', kind: 'main', source: { nodeId: 'manual-1', port: 'main' }, target: { nodeId: 'set-1', port: 'main' } }],
		settings: {}
	};
}

describe('workflow editor document helpers', () => {
	it('creates a node only from registry metadata and its declared defaults', () => {
		const node = createWorkflowNode(manual, { x: 120, y: 180 }, () => 'manual-2');

		expect(node).toEqual({
			id: 'manual-2',
			name: 'Manual Trigger',
			type: 'kilasflow.manual',
			typeVersion: 1,
			position: { x: 120, y: 180 },
			settings: { continueOnFail: false }
		});
	});

	it('allocates a client node ID when a picker does not supply one', () => {
		const node = createWorkflowNode(manual, { x: 0, y: 0 });

		expect(node.id).toMatch(/^[0-9a-f-]{36}$/i);
	});

	it('spaces new canvas nodes so their handles remain reachable at the default fit zoom', () => {
		expect(nextNodePosition(0)).toEqual({ x: 60, y: 60 });
		expect(nextNodePosition(1)).toEqual({ x: 280, y: 60 });
		expect(nextNodePosition(4)).toEqual({ x: 60, y: 250 });
	});

	it('round-trips the stored graph without leaking workflow identity into a save input', () => {
		const original = savedDocument();
		const canvas = documentFromCanvas(original, [manual, set]);
		const restored = canvas.toDocument(original);

		expect(restored).toEqual(original);
		expect(toWorkflowInput(restored)).toEqual({
			schemaVersion: 1,
			name: 'Welcome customer',
			nodes: original.nodes,
			connections: original.connections,
			settings: {}
		});
	});

	it('accepts a reactive proxy returned by the server-state client', () => {
		const reactiveDocument = new Proxy(savedDocument(), {});
		const canvas = documentFromCanvas(reactiveDocument, [manual, set]);

		expect(canvas.toDocument(reactiveDocument)).toEqual(savedDocument());
	});

	it('preserves a stored node whose registry version is temporarily unavailable', () => {
		const original = savedDocument();
		original.nodes?.push({
			id: 'retired-1',
			name: 'Retired node',
			type: 'kilasflow.retired',
			typeVersion: 4,
			position: { x: 640, y: 48 },
			parameters: { retained: true }
		});
		original.connections?.push({
			id: 'edge-2',
			kind: 'main',
			source: { nodeId: 'set-1', port: 'main' },
			target: { nodeId: 'retired-1', port: 'input' }
		});

		const canvas = documentFromCanvas(original, [manual, set]);

		expect(canvas.nodes).toHaveLength(3);
		expect(canvas.nodes[2].data.definition.category).toBe('Unavailable');
		expect(canvas.toDocument(original)).toEqual(original);
	});

	it('projects compiler issues onto their stored node and connection without changing the canonical document', () => {
		const original = savedDocument();
		const canvas = documentFromCanvas(original, [manual, set], [
			{ message: 'Set needs an input', nodeID: 'set-1' },
			{ message: 'Ports cannot be connected', connectionID: 'edge-1' }
		]);

		expect(canvas.nodes[1].data.validationMessage).toBe('Set needs an input');
		expect(canvas.edges[0].data.validationMessage).toBe('Ports cannot be connected');
		expect(canvas.edges[0].ariaLabel).toContain('Ports cannot be connected');
		expect(canvas.toDocument(original)).toEqual(original);
	});

	it('updates one generic property without mutating the saved document', () => {
		const original = savedDocument();
		const next = updateNodeProperty(original, 'set-1', 'parameters', 'assignments', { status: 'queued' });

		expect(next.nodes?.[1].parameters).toEqual({ assignments: { status: 'queued' } });
		expect(original.nodes?.[1].parameters).toEqual({ assignments: { status: 'ready' } });
		expect(workflowDocumentEquals(original, next)).toBe(false);
		expect(workflowDocumentEquals(original, savedDocument())).toBe(true);
	});
});
