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
export type EditorFlowEdge = FlowEdge<{ connection: Connection; validationMessage?: string }, 'smoothstep' | 'bezier'> & {
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

// Spacing is a function of the tile, not a round number: a node is 88px wide
// under a 160px name, so 220px across clears the widest label and 190px down
// clears the label plus a port row. Wider than that and a compact canvas would
// gain nothing over the card it replaced.
const COLUMN = 220;
const ROW = 190;

export function nextNodePosition(index: number): { x: number; y: number } {
	return { x: 60 + (index % 4) * COLUMN, y: 60 + Math.floor(index / 4) * ROW };
}

/**
 * Where a step added from an output port belongs: one column to the right of
 * the node it continues, stacked downward when that port already feeds others
 * so a branch fans out instead of overlapping.
 */
export function positionAfter(source: { x: number; y: number }, taken: number): { x: number; y: number } {
	return { x: source.x + COLUMN, y: source.y + taken * 150 };
}

export function documentFromCanvas(document: Document, definitions: Definition[], validationIssues: CanvasValidationIssue[] = []): CanvasDocument {
	const definitionByVersion = new Map(definitions.map((definition) => [definitionKey(definition.type, definition.version), definition]));
	const nodeIssues = new Map(validationIssues.filter((issue) => issue.nodeID).map((issue) => [issue.nodeID!, issue.message]));
	const edgeIssues = new Map(validationIssues.filter((issue) => issue.connectionID).map((issue) => [issue.connectionID!, issue.message]));
	const nodes = (document.nodes ?? []).map<EditorFlowNode>((workflowNode) => {
		const definition = definitionByVersion.get(definitionKey(workflowNode.type, workflowNode.typeVersion))
			?? unavailableDefinition(workflowNode, document.connections ?? []);
		return {
			id: workflowNode.id,
			type: 'workflow',
			position: { ...workflowNode.position },
			data: { definition, workflowNode: clone(workflowNode), validationMessage: nodeIssues.get(workflowNode.id) },
			ariaLabel: workflowNode.name
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
			type: attachment ? 'bezier' : 'smoothstep',
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

function definitionKey(type: string, version: number): string {
	return `${type}@${version}`;
}

function unavailableDefinition(node: WorkflowNode, connections: Connection[]): Definition {
	return {
		type: node.type,
		version: node.typeVersion,
		displayName: node.name,
		category: 'Unavailable',
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
	return [...ports.entries()].map(([Name, Kind]) => ({ Name, Kind }));
}

function createID(): string {
	return crypto.randomUUID();
}

function clone<T>(value: T): T {
	if (value === undefined) return value;
	return JSON.parse(JSON.stringify(value)) as T;
}

function stableJSON(value: unknown): string {
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
