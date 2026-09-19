import { MarkerType, type Edge as FlowEdge, type Node as FlowNode } from '@xyflow/svelte';

import type { Connection, Definition, Document, Node as WorkflowNode, WorkflowDocumentInput } from '$lib/api/generated/models';
import type { CanvasValidationIssue } from './validation';

export type PropertyScope = 'parameters' | 'settings';

export type EditorNodeData = {
	definition: Definition;
	workflowNode: WorkflowNode;
	validationMessage?: string;
	// Set only by the read-only execution replay, which annotates the same
	// projection with each node's recorded outcome.
	runStatus?: string;
};

export type EditorFlowNode = FlowNode<EditorNodeData, 'workflow'>;
export type EditorFlowEdge = FlowEdge<{ connection: Connection; validationMessage?: string }, 'smoothstep' | 'default'> & {
	data: { connection: Connection; validationMessage?: string };
};

export type CanvasDocument = {
	nodes: EditorFlowNode[];
	edges: EditorFlowEdge[];
	toDocument: (base: Document) => Document;
};

export function createWorkflowNode(
	definition: Definition,
	position: { x: number; y: number },
	newNodeID: () => string = createID
): WorkflowNode {
	const parameters = valuesFromDefinitions(definition.parameters ?? []);
	const settings = valuesFromDefinitions(definition.sharedSettings ?? []);

	return {
		id: newNodeID(),
		name: definition.displayName,
		type: definition.type,
		typeVersion: definition.version,
		position: { ...position },
		...(Object.keys(parameters).length > 0 ? { parameters } : {}),
		...(Object.keys(settings).length > 0 ? { settings } : {})
	};
}

// Spacing is a function of the tile, not a round number: a node is 68px wide
// under a 128px name. Pitch tracks layout.ts tidy defaults (rankSep 80 /
// nodeSep 44) so hand-placed steps and Tidy land on the same denser rhythm.
const COLUMN = 152;
const ROW = 136;

export function nextNodePosition(index: number): { x: number; y: number } {
	return { x: 60 + (index % 4) * COLUMN, y: 60 + Math.floor(index / 4) * ROW };
}

/**
 * Where a step added from an output port belongs: one column to the right of the
 * node it continues, pushed down a row at a time until it lands somewhere empty.
 *
 * Counting the port's existing connections would not work — the button that
 * adds the step only exists while the port has none — so the second branch of
 * an IF would land exactly on the first. Testing the destination against every
 * node also covers a node the user dragged there earlier.
 */
export function positionAfter(source: { x: number; y: number }, occupied: { x: number; y: number }[]): { x: number; y: number } {
	const candidate = { x: source.x + COLUMN, y: source.y };
	// A tile is 68px under a 128px label, so anything closer than this overlaps
	// something the reader needs. Both bounds stay under the grid pitch, so a
	// node in the neighbouring column or row never counts as a collision.
	while (occupied.some((node) => Math.abs(node.x - candidate.x) < 128 && Math.abs(node.y - candidate.y) < 120)) {
		candidate.y += ROW;
	}
	return candidate;
}

export function resolveDefinition(
	nodeType: string,
	typeVersion: number | undefined | null,
	definitions: Definition[]
): Definition | null {
	let best: Definition | null = null;
	for (const candidate of definitions) {
		if (candidate.type !== nodeType) continue;
		if (typeVersion === undefined || typeVersion === null) {
			if (best === null || candidate.version > best.version) best = candidate;
			continue;
		}
		if (candidate.version > typeVersion) continue;
		if (best === null || best.version > typeVersion || candidate.version > best.version) best = candidate;
	}
	if (best !== null) return best;
	// Nothing registered at or below the stored version: fall back to the
	// latest registered, so an imported node always opens its inspector.
	for (const candidate of definitions) {
		if (candidate.type !== nodeType) continue;
		if (best === null || candidate.version > best.version) best = candidate;
	}
	return best;
}

export function documentFromCanvas(document: Document, definitions: Definition[], validationIssues: CanvasValidationIssue[] = []): CanvasDocument {
	const nodeIssues = new Map(validationIssues.filter((issue) => issue.nodeID).map((issue) => [issue.nodeID!, issue.message]));
	const edgeIssues = new Map(validationIssues.filter((issue) => issue.connectionID).map((issue) => [issue.connectionID!, issue.message]));
	const nodes = (document.nodes ?? []).map<EditorFlowNode>((workflowNode) => {
		const definition =
			resolveDefinition(workflowNode.type, workflowNode.typeVersion, definitions) ?? unavailableDefinition(workflowNode, document.connections ?? []);
		return {
			id: workflowNode.id,
			type: 'workflow',
			position: { ...workflowNode.position },
			data: { definition, workflowNode: clone(workflowNode), validationMessage: nodeIssues.get(workflowNode.id) },
			ariaLabel: nodeIssues.has(workflowNode.id) ? `${workflowNode.name}: ${nodeIssues.get(workflowNode.id)}` : workflowNode.name
		};
	});
	const edges = (document.connections ?? []).map<EditorFlowEdge>((connection) => {
		const validationMessage = edgeIssues.get(connection.id);
		// An attachment carries configuration, not items. It is drawn as a dashed
		// curve from below rather than a stepped line with an arrow, so the two
		// connection kinds cannot be mistaken for each other at a glance.
		const attachment = connection.kind !== 'main';
		return {
			id: connection.id,
			type: attachment ? 'default' : 'smoothstep',
			class: attachment ? 'kf-edge-attachment' : undefined,
			...(attachment ? {} : { markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color: 'var(--xy-edge-stroke)' } }),
			source: connection.source.nodeId,
			sourceHandle: connection.source.port,
			target: connection.target.nodeId,
			targetHandle: connection.target.port,
			data: { connection: clone(connection), validationMessage },
			...(validationMessage ? { style: 'stroke: var(--destructive); stroke-width: 2px' } : {}),
			ariaLabel: `${connection.source.nodeId} ${connection.source.port} to ${connection.target.nodeId} ${connection.target.port}${validationMessage ? `: ${validationMessage}` : ''}`
		};
	});

	return {
		nodes,
		edges,
		toDocument: (base) => documentFromFlow(base, nodes, edges)
	};
}

export function documentFromFlow(base: Document, nodes: EditorFlowNode[], edges: EditorFlowEdge[]): Document {
	return {
		...clone(base),
		nodes: nodes.map(({ position, data }) => ({
			...clone(data.workflowNode),
			position: { x: position.x, y: position.y }
		})),
		connections: edges.map(({ data, source, sourceHandle, target, targetHandle }) => ({
			...clone(data.connection),
			source: { nodeId: source, port: sourceHandle ?? data.connection.source.port },
			target: { nodeId: target, port: targetHandle ?? data.connection.target.port }
		}))
	};
}

export function toWorkflowInput(document: Document): WorkflowDocumentInput {
	return {
		schemaVersion: document.schemaVersion,
		name: document.name,
		nodes: clone(document.nodes ?? []),
		connections: clone(document.connections ?? []),
		settings: clone(document.settings ?? {})
	};
}

export function updateNodeProperty(
	document: Document,
	nodeID: string,
	scope: PropertyScope,
	key: string,
	value: unknown
): Document {
	return {
		...clone(document),
		nodes: (document.nodes ?? []).map((node) => {
			if (node.id !== nodeID) return clone(node);
			return {
				...clone(node),
				[scope]: { ...(clone(node[scope] ?? {}) as Record<string, unknown>), [key]: clone(value) }
			};
		})
	};
}

/**
 * Sets or clears one credential reference on a node.
 *
 * The document records only the credential ID; the secret itself never enters
 * workflow JSON, so an export or an n8n-style copy carries no credential
 * material.
 */
export function updateNodeCredential(document: Document, nodeID: string, typeID: string, credentialID: string): Document {
	return {
		...clone(document),
		nodes: (document.nodes ?? []).map((node) => {
			if (node.id !== nodeID) return clone(node);
			const credentials = { ...(clone(node.credentials ?? {}) as Record<string, string>) };
			if (credentialID) {
				credentials[typeID] = credentialID;
			} else {
				delete credentials[typeID];
			}
			const next = clone(node);
			if (Object.keys(credentials).length > 0) {
				next.credentials = credentials;
			} else {
				delete next.credentials;
			}
			return next;
		})
	};
}

export function workflowDocumentEquals(left: Document, right: Document): boolean {
	return stableJSON(left) === stableJSON(right);
}

export function cloneWorkflowDocument(document: Document): Document {
	return clone(document);
}

function valuesFromDefinitions(definitions: NonNullable<Definition['parameters']>): Record<string, unknown> {
	const values: Record<string, unknown> = {};
	for (const definition of definitions) {
		if (Object.hasOwn(definition, 'default')) values[definition.key] = clone(definition.default);
	}
	return values;
}

function unavailableDefinition(node: WorkflowNode, connections: Connection[]): Definition {
	return {
		type: node.type,
		version: node.typeVersion,
		displayName: node.name,
		category: 'Unavailable',
		group: ['transform'],
		source: 'builtin',
		description: 'This stored node version is not available in the current registry. Its configuration will be preserved.',
		inputs: portsFromConnections(node.id, connections, 'target'),
		outputs: portsFromConnections(node.id, connections, 'source'),
		parameters: [],
		sharedSettings: []
	};
}

function portsFromConnections(nodeID: string, connections: Connection[], endpoint: 'source' | 'target'): Definition['inputs'] {
	const ports = new Map<string, string>();
	for (const connection of connections) {
		const point = connection[endpoint];
		if (point.nodeId === nodeID) ports.set(point.port, connection.kind);
	}
	return [...ports.entries()].map(([name, kind]) => ({ name, kind }));
}

function createID(): string {
	return crypto.randomUUID();
}

function clone<T>(value: T): T {
	if (value === undefined) return value;
	return JSON.parse(JSON.stringify(value)) as T;
}

/**
 * Serializes a value with object keys in sorted order.
 *
 * Exported so the history diff compares node parameters against the same
 * notion of equality the dirty check uses. Two copies of this would drift, and
 * a diff that disagreed with the "unsaved changes" indicator about whether
 * anything changed is worse than no diff at all.
 */
export function stableJSON(value: unknown): string {
	if (Array.isArray(value)) return `[${value.map(stableJSON).join(',')}]`;
	if (value !== null && typeof value === 'object') {
		const object = value as Record<string, unknown>;
		return `{${Object.keys(object)
			.sort()
			.map((key) => `${JSON.stringify(key)}:${stableJSON(object[key])}`)
			.join(',')}}`;
	}
	return JSON.stringify(value);
}
