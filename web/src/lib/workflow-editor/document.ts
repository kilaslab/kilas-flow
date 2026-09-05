import type { Edge as FlowEdge, Node as FlowNode } from '@xyflow/svelte';

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
export type EditorFlowEdge = FlowEdge<{ connection: Connection; validationMessage?: string }, 'smoothstep'> & {
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

export function nextNodePosition(index: number): { x: number; y: number } {
	return { x: 80 + (index % 3) * 360, y: 80 + Math.floor(index / 3) * 260 };
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
		return {
			id: connection.id,
			type: 'smoothstep',
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
