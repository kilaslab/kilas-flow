import type { Connection, Definition, Node, Port } from '$lib/api/generated/models';

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
	if (!source || !target || source.Kind !== target.Kind) return false;

	return !existing.some(
		(edge) =>
			edge.kind === source.Kind &&
			edge.source.nodeId === connection.source &&
			edge.source.port === connection.sourceHandle &&
			edge.target.nodeId === connection.target &&
			edge.target.port === connection.targetHandle
	);
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
		kind: source.Kind,
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
	const definition = definitions.find((candidate) => candidate.type === node.type && candidate.version === node.typeVersion);
	return definition?.[direction]?.find((port) => port.Name === portName);
}

function createID(): string {
	return crypto.randomUUID();
}
