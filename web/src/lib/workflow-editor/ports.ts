import type { Connection, Definition, Node, Port } from '$lib/api/generated/models';
import { resolveDefinition } from './document';

type CanvasConnection = {
	source?: string | null;
	sourceHandle?: string | null;
	target?: string | null;
	targetHandle?: string | null;
};

export function canConnect(
	connection: CanvasConnection,
	nodes: Node[],
	definitions: Definition[],
	existing: Connection[]
): boolean {
	if (!connection.source || !connection.sourceHandle || !connection.target || !connection.targetHandle) return false;
	if (connection.source === connection.target) return false;

	const source = lookupPort(connection.source, connection.sourceHandle, 'outputs', nodes, definitions);
	const target = lookupPort(connection.target, connection.targetHandle, 'inputs', nodes, definitions);
	if (!source || !target || source.kind !== target.kind) return false;

	const duplicate = existing.some(
		(edge) =>
			edge.kind === source.kind &&
			edge.source.nodeId === connection.source &&
			edge.source.port === connection.sourceHandle &&
			edge.target.nodeId === connection.target &&
			edge.target.port === connection.targetHandle
	);
	if (duplicate) return false;

	// A port may bound how many edges it accepts — one language model, one
	// memory, many tools. The compiler enforces this too; refusing here is what
	// stops the editor offering a connection the server will reject on save.
	if (target.maxConnections && target.maxConnections > 0) {
		const attached = existing.filter(
			(edge) => edge.target.nodeId === connection.target && edge.target.port === connection.targetHandle
		).length;
		if (attached >= target.maxConnections) return false;
	}

	// And it may name which node types it accepts.
	if (target.allowedNodeTypes && target.allowedNodeTypes.length > 0) {
		const sourceNode = nodes.find((node) => node.id === connection.source);
		if (!sourceNode || !target.allowedNodeTypes.includes(sourceNode.type)) return false;
	}

	return true;
}

/** What the editor labels a port with: its display name, else its own name. */
export function portLabel(port: Port): string {
	return port.displayName || port.name;
}

export function connectionFromCanvas(
	connection: CanvasConnection,
	nodes: Node[],
	definitions: Definition[],
	existing: Connection[],
	newConnectionID: () => string = createID
): Connection | null {
	if (!canConnect(connection, nodes, definitions, existing)) return null;

	const source = lookupPort(connection.source!, connection.sourceHandle!, 'outputs', nodes, definitions);
	if (!source) return null;

	return {
		id: newConnectionID(),
		kind: source.kind,
		source: { nodeId: connection.source!, port: connection.sourceHandle! },
		target: { nodeId: connection.target!, port: connection.targetHandle! }
	};
}

function lookupPort(
	nodeID: string,
	portName: string,
	direction: 'inputs' | 'outputs',
	nodes: Node[],
	definitions: Definition[]
): Port | undefined {
	const node = nodes.find((candidate) => candidate.id === nodeID);
	if (!node) return undefined;
	const definition = resolveDefinition(node.type, node.typeVersion, definitions);
	return definition?.[direction]?.find((port) => port.name === portName);
}

function createID(): string {
	return crypto.randomUUID();
}
