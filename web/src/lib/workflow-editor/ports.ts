import type { Connection, Definition, Document, Node, Port } from '$lib/api/generated/models';
import * as m from '$lib/paraglide/messages.js';
import { resolveDefinition } from './document';

// A few node types derive their ports from their own parameters on the server
// (nodes/flow.go, nodes/datastore.go, nodes/rag.go). The canvas has to derive
// them the same way, or a Switch's branches collapse into one dead handle and a
// wire the compiler would accept is refused before it is drawn.
//
// Every type listed here mirrors a Go `PortsFor`, and a hand-written mirror
// drifts. The vector stores and the embeddings node are therefore held to a
// table the Go suite generates from the real registry
// (nodes/testdata/ports_for.golden.json, read by ports.golden.test.ts), and a
// Go test fails when a registered node resolves its ports in Go without
// appearing in that table or being named as mirrored by hand.
const SWITCH_NODE_TYPE = 'kilasflow.switch';
const MERGE_NODE_TYPE = 'kilasflow.merge';
const DATASTORE_NODE_TYPE = 'kilasflow.datastore';
const VECTOR_STORE_NODE_TYPE = 'kilasflow.vectorStore';
const VECTOR_STORE_PGVECTOR_NODE_TYPE = 'kilasflow.vectorStorePGVector';
const EMBEDDINGS_NODE_TYPE = 'kilasflow.embeddings';

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

/**
 * Whether a node draws names next to its outputs — an IF's true/false, a
 * Switch's branches, a datastore row-found row-miss. Layout keeps extra
 * horizontal room after such a node, because the labels sit to the right of
 * the tile where the next rank used to start.
 */
export function hasLabelledOutputs(node: Node, definitions: Definition[]): boolean {
	const definition = resolveDefinition(node.type, node.typeVersion, definitions);
	if (!definition) return false;
	return resolvedPorts(node, definition).outputs.filter((port) => port.kind === 'main').length > 1;
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

/**
 * The connection a node picked from the picker gets, when the picker was opened
 * from a port of an existing node.
 *
 * Two directions share this path. Opened from a main OUTPUT (`kind` null), the
 * new node is the next step: the port feeds its main input. Opened from an
 * empty attachment INPUT slot (`kind` is the slot's kind), the new node is the
 * provider — a model, a memory, a tool — so its own output of that kind feeds
 * the slot. Looking for an input of the slot's kind on the new node, as this
 * did, finds nothing on a sub-node and added it unwired (BUG-w1jp70).
 *
 * Built through `connectionFromCanvas`, so a connection the canvas would refuse
 * (a full single-slot port, a node type the port does not accept) is never made.
 */
export function connectionForPickedNode(
	picked: { id: string; ports: { inputs: Port[]; outputs: Port[] } },
	from: { nodeID: string; port: string },
	kind: string | null,
	nodes: Node[],
	definitions: Definition[],
	existing: Connection[],
	newConnectionID?: () => string
): Connection | null {
	if (kind) {
		const output = picked.ports.outputs.find((port) => port.kind === kind);
		if (!output) return null;
		return connectionFromCanvas(
			{ source: picked.id, sourceHandle: output.name, target: from.nodeID, targetHandle: from.port },
			nodes,
			definitions,
			existing,
			newConnectionID
		);
	}
	const input = picked.ports.inputs.find((port) => port.kind === 'main');
	if (!input) return null;
	return connectionFromCanvas(
		{ source: from.nodeID, sourceHandle: from.port, target: picked.id, targetHandle: input.name },
		nodes,
		definitions,
		existing,
		newConnectionID
	);
}

/**
 * The connections of one node that name a port its current configuration does
 * not have.
 *
 * The canvas draws an edge only when both of its handles exist, so a wire to a
 * port a mode change, a lost Switch rule, a lower Merge input count or a
 * switched-off error output took away is stored but never drawn: it cannot be
 * selected or deleted, and the compiler names it only when the workflow is
 * activated or run (BUG-qfp7rf). The editor removes these in the step that
 * stranded them.
 *
 * Only the edited node's own connections are judged, on both sides of it. A
 * node whose type has no definition here is not judged at all: its ports are
 * unknown, which is not the same as absent.
 */
export function staleConnections(document: Document, nodeID: string, definitions: Definition[]): Connection[] {
	const node = (document.nodes ?? []).find((candidate) => candidate.id === nodeID);
	if (!node) return [];
	const definition = resolveDefinition(node.type, node.typeVersion, definitions);
	if (!definition) return [];

	const ports = resolvedPorts(node, definition);
	return (document.connections ?? []).filter(
		(connection) =>
			(connection.source.nodeId === nodeID && !ports.outputs.some((port) => port.name === connection.source.port)) ||
			(connection.target.nodeId === nodeID && !ports.inputs.some((port) => port.name === connection.target.port))
	);
}

/**
 * One line naming connections the way a reader knows them — by node name and
 * port, not by id — for the notice that says what was removed. At most `limit`
 * are spelled out; the rest are counted.
 */
export function describeConnections(connections: Connection[], nodes: Node[] | null | undefined, limit = 3): string {
	const names = new Map((nodes ?? []).map((node) => [node.id, node.name]));
	const label = (id: string, port: string) => `${names.get(id) ?? id} ${port}`;
	const shown = connections.slice(0, limit).map((connection) => `${label(connection.source.nodeId, connection.source.port)} → ${label(connection.target.nodeId, connection.target.port)}`);
	const hidden = connections.length - shown.length;
	return hidden > 0 ? `${shown.join('; ')}; +${hidden} more` : shown.join('; ');
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

// The extra output a node declares when its failed items route to their own
// branch, mirroring the server's `withErrorPort` (internal/workflow/compiler.go):
// the connection to a node downstream of a failure names this port, and a
// canvas that does not draw it leaves the error branch invisible and
// uneditable even though the engine routes items down it.
const ERROR_PORT_NAME = 'error';

const ERROR_PORT: Port = { name: ERROR_PORT_NAME, kind: 'main', displayName: 'Error' };

function withErrorPort(outputs: Port[], settings: Node['settings']): Port[] {
	if (settings?.['onError'] !== 'continueErrorOutput') return outputs;
	if (outputs.some((port) => port.name === ERROR_PORT_NAME)) return outputs;
	const labelled = outputs.map((port, index) =>
		index === 0 && port.kind === 'main' && !port.displayName ? { ...port, displayName: 'Success' } : port
	);
	return [...labelled, ERROR_PORT];
}

/** A node's ports as the server resolves them for this configuration. */
export function resolvedPorts(node: Node, definition: Definition): { inputs: Port[]; outputs: Port[] } {
	const inputs = definition.inputs ?? [];
	const outputs = definition.outputs ?? [];

	switch (node.type) {
		case SWITCH_NODE_TYPE:
			return { inputs, outputs: withErrorPort(switchOutputs(node.parameters), node.settings) };
		case MERGE_NODE_TYPE:
			return { inputs: mergeInputs(node.parameters), outputs: withErrorPort(outputs, node.settings) };
		case DATASTORE_NODE_TYPE:
			return { inputs, outputs: withErrorPort(datastoreOutputs(node.parameters, outputs), node.settings) };
		case VECTOR_STORE_NODE_TYPE:
		case VECTOR_STORE_PGVECTOR_NODE_TYPE: {
			const store = vectorStorePorts(node.parameters);
			return { inputs: store.inputs, outputs: withErrorPort(store.outputs, node.settings) };
		}
		case EMBEDDINGS_NODE_TYPE: {
			const embeddings = embeddingsPorts(node.parameters);
			return { inputs: embeddings.inputs, outputs: withErrorPort(embeddings.outputs, node.settings) };
		}
		default:
			return { inputs, outputs: withErrorPort(outputs, node.settings) };
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
			{ name: 'true', displayName: m.canvas_port_row_found(), kind: 'main' },
			{ name: 'false', displayName: m.canvas_port_no_row(), kind: 'main' }
		];
	}
	if (operation === 'ifNotExists') {
		return [
			{ name: 'true', displayName: m.canvas_port_no_row(), kind: 'main' },
			{ name: 'false', displayName: m.canvas_port_row_found(), kind: 'main' }
		];
	}
	return declared;
}

const MAIN_PORT: Port = { name: 'main', kind: 'main' };
const EMBEDDING_PORT: Port = { name: 'embedding', kind: 'ai_embedding' };
const DOCUMENT_PORT: Port = { name: 'document', kind: 'ai_document' };
const TOOL_PORT: Port = { name: 'tool', kind: 'ai_tool' };
const VECTOR_STORE_PORT: Port = { name: 'vectorStore', kind: 'ai_vectorStore' };
/** What a retrieve-mode store needs: one embeddings model, and the compiler refuses it without. */
const RETRIEVE_EMBEDDING_PORT: Port = { ...EMBEDDING_PORT, required: true, maxConnections: 1 };

/**
 * Go's textValue (nodes/http.go) followed by TrimSpace: an absent, null or
 * empty value is the fallback, a string is itself, and any other JSON value
 * becomes its JSON text. None of those last ones names a mode, but they are
 * not empty either, so they must not fall through to the `operation`.
 */
function goText(value: unknown, fallback: string): string {
	if (value === null || value === undefined) return fallback.trim();
	if (typeof value === 'string') return (value === '' ? fallback : value).trim();
	return (JSON.stringify(value) ?? fallback).trim();
}

/** Whether the document carries a `mode` key at all, which is all Go's `_, hasMode := parameters["mode"]` asks. */
function hasModeKey(parameters: Node['parameters']): boolean {
	return parameters !== undefined && parameters !== null && Object.hasOwn(parameters, 'mode') && parameters['mode'] !== undefined;
}

/**
 * The mode a vector store runs in: nodes/rag.go `vectorStoreModeOf`, line for line.
 *
 * `retrieve` is only ever a *mode* — a native graph's `operation: retrieve`
 * kept meaning a search. The lower-cased switch never sees the mixed-case
 * "getMany"; it arrives through the default arm unchanged, and Go's
 * `vectorStorePortsFor` matches it there. The golden pins the oddity (a
 * "GETMANY" mode resolves to the plain main ports on the server), so it is
 * reproduced rather than tidied.
 */
function vectorStoreModeOf(parameters: Node['parameters']): string {
	let mode = goText(parameters?.['mode'], '');
	if (mode.toLowerCase() === 'retrieve') return 'retrieve';
	if (mode === '') mode = goText(parameters?.['operation'], 'insert');
	switch (mode.toLowerCase()) {
		case 'insert':
			return 'insert';
		case 'get_many':
		case 'load':
		case 'retrieve':
		case 'search':
			return 'getMany';
		case 'retrieve-as-tool':
		case 'retrieve_as_tool':
		case 'retrieveastool':
			return 'retrieve-as-tool';
		case 'delete':
			return 'delete';
		default:
			return mode;
	}
}

/**
 * The ports a vector store has in its mode: nodes/rag.go `vectorStorePortsFor`.
 *
 * A native graph that sets only `operation` stays on main in and out. Once a
 * `mode` key is present — an n8n import writes one, and so does choosing it in
 * the inspector — insert and Get Many take their embeddings (and documents) on
 * typed ports, and the retrieve modes hand the store itself, or its tool, to
 * the node that queries it.
 */
function vectorStorePorts(parameters: Node['parameters']): { inputs: Port[]; outputs: Port[] } {
	const mainToMain = { inputs: [MAIN_PORT], outputs: [MAIN_PORT] };
	switch (vectorStoreModeOf(parameters)) {
		case 'insert':
			return hasModeKey(parameters) ? { inputs: [EMBEDDING_PORT, DOCUMENT_PORT], outputs: [MAIN_PORT] } : mainToMain;
		case 'getMany':
			return hasModeKey(parameters) ? { inputs: [MAIN_PORT, EMBEDDING_PORT], outputs: [MAIN_PORT] } : mainToMain;
		case 'retrieve-as-tool':
			return { inputs: [EMBEDDING_PORT], outputs: [TOOL_PORT] };
		case 'retrieve':
			return { inputs: [RETRIEVE_EMBEDDING_PORT], outputs: [VECTOR_STORE_PORT] };
		default:
			return mainToMain;
	}
}

/**
 * The embeddings node: nodes/rag.go `embeddingsPortsFor`. In cluster mode (what
 * an n8n import writes) it is a sub-node of the store, with no input and only
 * the embedding output; otherwise it also embeds the items on main.
 */
function embeddingsPorts(parameters: Node['parameters']): { inputs: Port[]; outputs: Port[] } {
	if (goText(parameters?.['mode'], '').toLowerCase() === 'cluster') {
		return { inputs: [], outputs: [EMBEDDING_PORT] };
	}
	return { inputs: [MAIN_PORT], outputs: [MAIN_PORT, EMBEDDING_PORT] };
}

function createID(): string {
	return crypto.randomUUID();
}
