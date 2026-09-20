import type { Connection, Definition, Node, Port } from '$lib/api/generated/models';
import { resolveDefinition } from './document';

// A few node types derive their ports from their own parameters on the server
// (nodes/flow.go, nodes/datastore.go). The canvas has to derive them the same
// way, or a Switch's branches collapse into one dead handle and a wire the
// compiler would accept is refused before it is drawn.
const SWITCH_NODE_TYPE = 'kilasflow.switch';
const MERGE_NODE_TYPE = 'kilasflow.merge';
const DATASTORE_NODE_TYPE = 'kilasflow.datastore';

// Both numbers come from a document rather than from the user, so both are
// bounded before anything builds a port for them.
const MAX_SWITCH_OUTPUTS = 64;
const MAX_MERGE_INPUTS = 32;

/** The branch the server declares for a Switch whose rules it cannot read. */
const RULE_ONE_PORT: Port = { name: '0', displayName: 'Rule 1', kind: 'main' };

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
	if (!definition) return undefined;
	return resolvedPorts(node, definition)[direction].find((port) => port.name === portName);
}

/** A node's ports as the server resolves them for this configuration. */
export function resolvedPorts(node: Node, definition: Definition): { inputs: Port[]; outputs: Port[] } {
	const inputs = definition.inputs ?? [];
	const outputs = definition.outputs ?? [];

	switch (node.type) {
		case SWITCH_NODE_TYPE:
			return { inputs, outputs: switchOutputs(node.parameters) };
		case MERGE_NODE_TYPE:
			return { inputs: mergeInputs(node.parameters), outputs };
		case DATASTORE_NODE_TYPE:
			return { inputs, outputs: datastoreOutputs(node.parameters, outputs) };
		default:
			return { inputs, outputs };
	}
}

/**
 * A Switch's outputs are its rules. The port *name* is the rule's position as a
 * string — n8n identifies an output positionally, so a renamed branch keeps
 * landing on the same wire and only its label moves.
 *
 * A rule set the server rejects — absent, wrongly shaped, empty, longer than
 * the ceiling, or holding an entry that is not an object — has no ports to
 * mirror, so the canvas draws the same single branch the server declares.
 * Drawing the branches of a configuration the compiler refuses would offer
 * wires that cannot be saved.
 */
function switchOutputs(parameters: Node['parameters']): Port[] {
	const list = switchRuleList(parameters);
	if (!list || list.length === 0 || list.length > MAX_SWITCH_OUTPUTS) return [RULE_ONE_PORT];
	if (list.some((entry) => entry === null || typeof entry !== 'object' || Array.isArray(entry))) {
		return [RULE_ONE_PORT];
	}

	const outputs: Port[] = list.map((entry, index) => {
		const rule = entry as { outputKey?: unknown; renameOutput?: unknown };
		const outputKey = typeof rule.outputKey === 'string' ? rule.outputKey : '';
		const renameOutput = typeof rule.renameOutput === 'string' ? rule.renameOutput : '';
		return {
			name: String(index),
			displayName: outputKey || renameOutput || `Rule ${index + 1}`,
			kind: 'main'
		};
	});
	if (parameters?.['fallbackOutput'] === 'extra') {
		outputs.push({ name: String(outputs.length), displayName: 'Fallback', kind: 'main' });
	}
	return outputs;
}

/** The rules parameter, in either shape a document may hold it. */
function switchRuleList(parameters: Node['parameters']): unknown[] | null {
	const rules = parameters?.['rules'] as { values?: unknown } | undefined;
	if (Array.isArray(rules)) return rules;
	// n8n nests them under `values`; both forms are accepted so an import needs
	// no rewriting.
	const values = rules?.values;
	return Array.isArray(values) ? values : null;
}

/** Merge takes as many streams as it was told to, within the cap. */
function mergeInputs(parameters: Node['parameters']): Port[] {
	const declared = parameters?.['numberInputs'];
	let count = 2;
	if (typeof declared === 'number' && declared >= 2) count = Math.trunc(declared);
	if (count > MAX_MERGE_INPUTS) count = MAX_MERGE_INPUTS;

	const inputs: Port[] = [];
	for (let index = 0; index < count; index++) {
		inputs.push({ name: `input${index + 1}`, displayName: `Input ${index + 1}`, kind: 'main' });
	}
	return inputs;
}

/**
 * The branch operations fork, so they carry a second output; every other
 * operation keeps the definition's list, and the canvas never shows a fork an
 * insert cannot take.
 *
 * The two ports are named `true` and `false` — the outcome the operation tests
 * for and the one it does not — because a port's identity has to hold while
 * its label follows the configuration. Two ports sharing one name is what made
 * the second unreachable: a connection's port is resolved by name, so every
 * wire landed on the first branch.
 */
function datastoreOutputs(parameters: Node['parameters'], declared: Port[]): Port[] {
	const operation = parameters?.['operation'];
	if (operation === 'ifExists') {
		return [
			{ name: 'true', displayName: 'Row found', kind: 'main' },
			{ name: 'false', displayName: 'No row', kind: 'main' }
		];
	}
	if (operation === 'ifNotExists') {
		return [
			{ name: 'true', displayName: 'No row', kind: 'main' },
			{ name: 'false', displayName: 'Row found', kind: 'main' }
		];
	}
	return declared;
}

function createID(): string {
	return crypto.randomUUID();
}
