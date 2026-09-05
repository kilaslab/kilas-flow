import { describe, expect, it } from 'vitest';

import type { Connection, Document, Node } from '$lib/api/generated/models';
import { describeNodeChange, diffChangeCount, diffWorkflowDocuments } from './history-diff';

function node(overrides: Partial<Node> & Pick<Node, 'id' | 'name'>): Node {
	return {
		type: 'kilasflow.set',
		typeVersion: 1,
		position: { x: 0, y: 0 },
		parameters: {},
		...overrides
	};
}

function connection(overrides: Partial<Connection> & Pick<Connection, 'id'>): Connection {
	return {
		kind: 'main',
		source: { nodeId: 'a', port: 'main' },
		target: { nodeId: 'b', port: 'main' },
		...overrides
	};
}

function document(overrides: Partial<Document> = {}): Document {
	return {
		id: 'wf-1',
		name: 'Nightly sync',
		schemaVersion: 1,
		nodes: [],
		connections: [],
		settings: {},
		...overrides
	};
}

describe('diffWorkflowDocuments', () => {
	it('reports two identical documents as identical', () => {
		const before = document({ nodes: [node({ id: 'a', name: 'Webhook' })] });
		const after = document({ nodes: [node({ id: 'a', name: 'Webhook' })] });

		const diff = diffWorkflowDocuments(before, after);

		expect(diff.identical).toBe(true);
		expect(diff.cosmeticOnly).toBe(false);
		expect(diffChangeCount(diff)).toBe(0);
	});

	it('names a node the newer document adds', () => {
		const before = document({ nodes: [node({ id: 'a', name: 'Webhook' })] });
		const after = document({ nodes: [node({ id: 'a', name: 'Webhook' }), node({ id: 'b', name: 'Send email' })] });

		const diff = diffWorkflowDocuments(before, after);

		expect(diff.nodes).toEqual([{ nodeID: 'b', name: 'Send email', status: 'added', aspects: [] }]);
		expect(diff.identical).toBe(false);
	});

	it('names a node the newer document no longer has', () => {
		const before = document({ nodes: [node({ id: 'a', name: 'Webhook' }), node({ id: 'b', name: 'Send email' })] });
		const after = document({ nodes: [node({ id: 'a', name: 'Webhook' })] });

		const diff = diffWorkflowDocuments(before, after);

		expect(diff.nodes).toEqual([{ nodeID: 'b', name: 'Send email', status: 'removed', aspects: [] }]);
	});

	it('reads a rename as one renamed node rather than a delete and an add', () => {
		const before = document({ nodes: [node({ id: 'a', name: 'Webhook' })] });
		const after = document({ nodes: [node({ id: 'a', name: 'Order received' })] });

		const diff = diffWorkflowDocuments(before, after);

		expect(diff.nodes).toEqual([{ nodeID: 'a', name: 'Order received', status: 'changed', previousName: 'Webhook', aspects: ['name'] }]);
	});

	it('keeps the parameters of a renamed node attached to it', () => {
		const before = document({ nodes: [node({ id: 'a', name: 'Webhook', parameters: { path: '/old' } })] });
		const after = document({ nodes: [node({ id: 'a', name: 'Order received', parameters: { path: '/new' } })] });

		const diff = diffWorkflowDocuments(before, after);

		expect(diff.nodes).toHaveLength(1);
		expect(diff.nodes[0].aspects).toEqual(['name', 'parameters']);
	});

	it('reports a node that only moved as a cosmetic change', () => {
		const before = document({ nodes: [node({ id: 'a', name: 'Webhook', position: { x: 0, y: 0 } })] });
		const after = document({ nodes: [node({ id: 'a', name: 'Webhook', position: { x: 320, y: 40 } })] });

		const diff = diffWorkflowDocuments(before, after);

		expect(diff.nodes[0].aspects).toEqual(['position']);
		expect(diff.cosmeticOnly).toBe(true);
	});

	it('stops calling the diff cosmetic once a parameter changed alongside a move', () => {
		const before = document({ nodes: [node({ id: 'a', name: 'Webhook', position: { x: 0, y: 0 }, parameters: { path: '/old' } })] });
		const after = document({ nodes: [node({ id: 'a', name: 'Webhook', position: { x: 320, y: 0 }, parameters: { path: '/new' } })] });

		const diff = diffWorkflowDocuments(before, after);

		expect(diff.nodes[0].aspects).toEqual(['position', 'parameters']);
		expect(diff.cosmeticOnly).toBe(false);
	});

	it('separates a credential change from a parameter change', () => {
		const before = document({ nodes: [node({ id: 'a', name: 'HTTP', credentials: { httpBasic: 'cred-1' } })] });
		const after = document({ nodes: [node({ id: 'a', name: 'HTTP', credentials: { httpBasic: 'cred-2' } })] });

		const diff = diffWorkflowDocuments(before, after);

		expect(diff.nodes[0].aspects).toEqual(['credentials']);
	});

	it('reports a node whose type version was upgraded', () => {
		const before = document({ nodes: [node({ id: 'a', name: 'HTTP', typeVersion: 1 })] });
		const after = document({ nodes: [node({ id: 'a', name: 'HTTP', typeVersion: 2 })] });

		const diff = diffWorkflowDocuments(before, after);

		expect(diff.nodes[0].aspects).toEqual(['type']);
	});

	it('treats a missing parameter bag and an empty one as the same node', () => {
		const before = document({ nodes: [node({ id: 'a', name: 'Webhook', parameters: undefined })] });
		const after = document({ nodes: [node({ id: 'a', name: 'Webhook', parameters: {} })] });

		expect(diffWorkflowDocuments(before, after).identical).toBe(true);
	});

	it('ignores the order the same parameter keys were serialized in', () => {
		const before = document({ nodes: [node({ id: 'a', name: 'HTTP', parameters: { url: 'https://example.test', method: 'GET' } })] });
		const after = document({ nodes: [node({ id: 'a', name: 'HTTP', parameters: { method: 'GET', url: 'https://example.test' } })] });

		expect(diffWorkflowDocuments(before, after).identical).toBe(true);
	});

	it('ignores the order nodes appear in the document', () => {
		const before = document({ nodes: [node({ id: 'a', name: 'Webhook' }), node({ id: 'b', name: 'Send email' })] });
		const after = document({ nodes: [node({ id: 'b', name: 'Send email' }), node({ id: 'a', name: 'Webhook' })] });

		expect(diffWorkflowDocuments(before, after).identical).toBe(true);
	});

	it('names a connection the newer document adds, by node name', () => {
		const nodes = [node({ id: 'a', name: 'Webhook' }), node({ id: 'b', name: 'Send email' })];
		const before = document({ nodes });
		const after = document({ nodes, connections: [connection({ id: 'c-1' })] });

		const diff = diffWorkflowDocuments(before, after);

		expect(diff.connections).toEqual([
			{ connectionID: 'c-1', status: 'added', kind: 'main', sourceName: 'Webhook', targetName: 'Send email', sourcePort: 'main', targetPort: 'main' }
		]);
	});

	it('names a connection the newer document dropped', () => {
		const nodes = [node({ id: 'a', name: 'Webhook' }), node({ id: 'b', name: 'Send email' })];
		const before = document({ nodes, connections: [connection({ id: 'c-1' })] });
		const after = document({ nodes });

		const diff = diffWorkflowDocuments(before, after);

		expect(diff.connections).toHaveLength(1);
		expect(diff.connections[0].status).toBe('removed');
	});

	it('does not report a connection that only got a new identifier', () => {
		const nodes = [node({ id: 'a', name: 'Webhook' }), node({ id: 'b', name: 'Send email' })];
		const before = document({ nodes, connections: [connection({ id: 'c-1' })] });
		const after = document({ nodes, connections: [connection({ id: 'c-99' })] });

		expect(diffWorkflowDocuments(before, after).connections).toEqual([]);
	});

	it('reports a connection that was moved to another port', () => {
		const nodes = [node({ id: 'a', name: 'If' }), node({ id: 'b', name: 'Send email' })];
		const before = document({ nodes, connections: [connection({ id: 'c-1', source: { nodeId: 'a', port: 'true' } })] });
		const after = document({ nodes, connections: [connection({ id: 'c-1', source: { nodeId: 'a', port: 'false' } })] });

		const diff = diffWorkflowDocuments(before, after);

		expect(diff.connections.map((change) => `${change.status} ${change.sourcePort}`).sort()).toEqual(['added false', 'removed true']);
	});

	it('describes a removed connection with the name the node had before it went', () => {
		const before = document({
			nodes: [node({ id: 'a', name: 'Webhook' }), node({ id: 'b', name: 'Send email' })],
			connections: [connection({ id: 'c-1' })]
		});
		const after = document({ nodes: [node({ id: 'a', name: 'Webhook' })] });

		const diff = diffWorkflowDocuments(before, after);

		expect(diff.connections[0].targetName).toBe('Send email');
	});

	it('names each document setting that changed, appeared or went', () => {
		const before = document({ settings: { timezone: 'UTC', retryOnFail: true } });
		const after = document({ settings: { timezone: 'Asia/Jakarta', errorWorkflow: 'wf-9' } });

		const diff = diffWorkflowDocuments(before, after);

		expect(diff.settings).toEqual([
			{ key: 'errorWorkflow', status: 'added', before: undefined, after: 'wf-9' },
			{ key: 'retryOnFail', status: 'removed', before: true, after: undefined },
			{ key: 'timezone', status: 'changed', before: 'UTC', after: 'Asia/Jakarta' }
		]);
	});

	it('reports a setting explicitly set to null as removed only when the key itself went', () => {
		const before = document({ settings: { errorWorkflow: 'wf-9' } });
		const after = document({ settings: { errorWorkflow: null } });

		const diff = diffWorkflowDocuments(before, after);

		expect(diff.settings).toEqual([{ key: 'errorWorkflow', status: 'changed', before: 'wf-9', after: null }]);
	});

	it('names a workflow that was renamed', () => {
		const before = document({ name: 'Nightly sync' });
		const after = document({ name: 'Hourly sync' });

		const diff = diffWorkflowDocuments(before, after);

		expect(diff.name).toEqual({ before: 'Nightly sync', after: 'Hourly sync' });
		expect(diff.identical).toBe(false);
	});

	it('groups the node list as added, then removed, then changed', () => {
		const before = document({ nodes: [node({ id: 'a', name: 'Webhook' }), node({ id: 'b', name: 'Gone' })] });
		const after = document({
			nodes: [node({ id: 'a', name: 'Webhook', parameters: { path: '/x' } }), node({ id: 'c', name: 'New' })]
		});

		const diff = diffWorkflowDocuments(before, after);

		expect(diff.nodes.map((change) => `${change.status}:${change.name}`)).toEqual(['added:New', 'removed:Gone', 'changed:Webhook']);
	});

	it('survives a document whose node and connection lists are null', () => {
		const before = document({ nodes: null, connections: null });
		const after = document({ nodes: null, connections: null });

		expect(diffWorkflowDocuments(before, after).identical).toBe(true);
	});

	it('counts every individual difference it found', () => {
		const before = document({ name: 'Old', nodes: [node({ id: 'a', name: 'Webhook' })], settings: { timezone: 'UTC' } });
		const after = document({ name: 'New', nodes: [node({ id: 'b', name: 'Send email' })], settings: { timezone: 'Asia/Jakarta' } });

		expect(diffChangeCount(diffWorkflowDocuments(before, after))).toBe(4);
	});
});

describe('describeNodeChange', () => {
	it('says what an added node is', () => {
		expect(describeNodeChange({ nodeID: 'a', name: 'Webhook', status: 'added', aspects: [] })).toBe('Added');
	});

	it('names the name a renamed node used to have', () => {
		expect(describeNodeChange({ nodeID: 'a', name: 'Order received', status: 'changed', previousName: 'Webhook', aspects: ['name'] })).toBe(
			'Renamed from “Webhook”'
		);
	});

	it('lists every aspect of a node that changed in several ways at once', () => {
		expect(describeNodeChange({ nodeID: 'a', name: 'HTTP', status: 'changed', aspects: ['position', 'parameters', 'credentials'] })).toBe(
			'Moved, parameters changed, credential changed'
		);
	});
});
