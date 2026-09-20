import type { Connection, Definition, Document, Node as WorkflowNode, Position } from '$lib/api/generated/models';

import { UNSUPPORTED_TYPE } from './catalog';
import { insertNodes } from './document';
import { isRecord } from '$lib/type-guards';

/**
 * The marker that identifies this editor's own clipboard payload.
 *
 * Without it a pasted fragment of this editor's JSON would be indistinguishable
 * from an n8n workflow, and the importer path (which rewrites types and
 * positions) would mangle a copy that needs nothing done to it.
 */
export const FRAGMENT_KIND = 'kilasflow.node-fragment';

export type PastePayload = {
	nodes: WorkflowNode[];
	connections: Connection[];
	/** Source node types this catalogue has no equivalent for, now placeholders. */
	unsupported: string[];
	/** Connections the payload asked for that no port here could accept. */
	dropped: number;
};

/**
 * The selection as workflow JSON.
 *
 * Node ids and names travel with it, because a fragment pasted into *another*
 * workflow has to be de-duplicated on arrival, and that decision belongs to the
 * document being pasted into — not to the one being copied from.
 */
export function copySelection(document: Document, nodeIDs: Iterable<string>): string | null {
	const wanted = new Set(nodeIDs);
	const nodes = (document.nodes ?? []).filter((node) => wanted.has(node.id));
	if (nodes.length === 0) return null;
	const connections = (document.connections ?? []).filter(
		(connection) => wanted.has(connection.source.nodeId) && wanted.has(connection.target.nodeId)
	);
	return JSON.stringify({ kind: FRAGMENT_KIND, version: 1, nodes, connections });
}

/**
 * Reads a clipboard payload, in either shape.
 *
 * Both are n8n-shaped `{ nodes, connections }` documents, because that is what
 * people copy out of n8n and paste here: this editor's own fragments use the
 * connection list this product stores, and anything else is read as an n8n
 * payload — connection graph keyed by node name, positions as `[x, y]`, types
 * in n8n's namespace.
 *
 * Returns null for text that is not workflow JSON at all, so the caller can let
 * an ordinary paste through to the browser.
 */
export function readClipboard(text: string, definitions: Definition[]): PastePayload | null {
	let parsed: unknown;
	try {
		parsed = JSON.parse(text);
	} catch {
		return null;
	}
	if (!isRecord(parsed) || !Array.isArray(parsed.nodes)) return null;

	if (parsed.kind === FRAGMENT_KIND) {
		const nodes = parsed.nodes.filter(isWorkflowNode);
		return { nodes, connections: Array.isArray(parsed.connections) ? parsed.connections.filter(isConnection) : [], unsupported: [], dropped: 0 };
	}
	return fromN8n(parsed, definitions);
}

/** Pastes a payload into the document, offsetting it and renaming it uniquely. */
export function pasteInto(
	document: Document,
	payload: PastePayload,
	offset: { x: number; y: number },
	newID?: () => string
): { document: Document; nodeIDs: string[] } {
	return insertNodes(document, payload.nodes, payload.connections, offset, newID);
}

type RawNode = Record<string, unknown>;

type Converted = {
	source: RawNode;
	definition: Definition | null;
	node: WorkflowNode;
};

/**
 * Converts an n8n payload.
 *
 * Types are matched against the catalogue rather than translated by a table
 * kept here: `n8n-nodes-base.telegram` and this product's `pack.telegram` share
 * their last segment, and a node the catalogue does not know becomes the same
 * placeholder an import produces, carrying the original identity — so a pasted
 * snippet is never silently dropped and never silently becomes a different
 * node.
 */
function fromN8n(payload: Record<string, unknown>, definitions: Definition[]): PastePayload {
	const sources = (Array.isArray(payload.nodes) ? payload.nodes : []).filter(isRecord);
	const usage = connectionUsage(payload.connections);
	const converted = new Map<string, Converted>();
	const unsupported: string[] = [];

	for (const source of sources) {
		const name = typeof source.name === 'string' && source.name !== '' ? source.name : 'Node';
		const type = typeof source.type === 'string' ? source.type : '';
		const definition = matchDefinition(type, definitions);
		if (!definition) unsupported.push(type);
		converted.set(name, {
			source,
			definition,
			node: convertNode(source, name, type, definition, definitions, usage.get(name) ?? 0)
		});
	}

	const { connections, dropped } = convertConnections(payload.connections, converted);
	return { nodes: [...converted.values()].map((entry) => entry.node), connections, unsupported, dropped };
}

/**
 * How many connection slots each named node uses.
 *
 * Read before the nodes are converted, because the placeholder family is
 * registered one definition per arity: a placeholder declared with fewer ports
 * than the payload's own edges is rejected for an unknown port, which reads as
 * a confusing topology error rather than as "this node is unsupported".
 */
function connectionUsage(connections: unknown): Map<string, number> {
	const usage = new Map<string, number>();
	if (!isRecord(connections)) return usage;
	const note = (name: string, slots: number) => usage.set(name, Math.max(usage.get(name) ?? 0, slots));
	for (const [sourceName, byKind] of Object.entries(connections)) {
		if (!isRecord(byKind)) continue;
		for (const slots of Object.values(byKind)) {
			if (!Array.isArray(slots)) continue;
			note(sourceName, slots.length);
			slots.forEach((targets, outputIndex) => {
				if (!Array.isArray(targets)) return;
				note(sourceName, outputIndex + 1);
				for (const entry of targets) {
					if (!isRecord(entry) || typeof entry.node !== 'string') continue;
					const index = typeof entry.index === 'number' ? entry.index : 0;
					note(entry.node, index + 1);
				}
			});
		}
	}
	return usage;
}

function convertNode(
	source: RawNode,
	name: string,
	type: string,
	definition: Definition | null,
	definitions: Definition[],
	usedSlots: number
): WorkflowNode {
	const typeVersion = typeof source.typeVersion === 'number' ? source.typeVersion : 1;
	const parameters = isRecord(source.parameters) ? source.parameters : {};
	const id = typeof source.id === 'string' && source.id !== '' ? source.id : crypto.randomUUID();
	if (definition) {
		return {
			id,
			name,
			type: definition.type,
			typeVersion: definition.version,
			position: sourcePosition(source.position),
			...(Object.keys(parameters).length > 0 ? { parameters } : {})
		};
	}
	// The same capsule an import writes: the original type, its version, and the
	// whole source node, so an export of the pasted fragment returns it as it
	// arrived.
	return {
		id,
		name,
		type: UNSUPPORTED_TYPE,
		typeVersion: placeholderArity(Math.max(usedSlots, 1), definitions),
		position: sourcePosition(source.position),
		parameters: { originalType: type, originalTypeVersion: typeVersion, original: source }
	};
}

/** The registered node a source type names, exact first and by suffix second. */
function matchDefinition(type: string, definitions: Definition[]): Definition | null {
	if (type === '') return null;
	const exact = definitions.filter((definition) => definition.type.toLowerCase() === type.toLowerCase());
	if (exact.length > 0) return highestVersion(exact);

	// `n8n-nodes-base.telegram` → `telegram`: the namespace is n8n's, the last
	// segment is the node both products call it.
	const suffix = `.${type.split('.').pop()?.toLowerCase() ?? ''}`;
	const named = definitions.filter((definition) => definition.type.toLowerCase().endsWith(suffix));
	return named.length > 0 ? highestVersion(named) : null;
}

function highestVersion(definitions: Definition[]): Definition {
	return definitions.reduce((best, candidate) => (candidate.version > best.version ? candidate : best));
}

/** The smallest registered placeholder that covers `arity` slots, else the largest. */
function placeholderArity(arity: number, definitions: Definition[]): number {
	const registered = definitions.filter((definition) => definition.type === UNSUPPORTED_TYPE);
	if (registered.length === 0) return arity;
	const covering = registered.filter((definition) => definition.version >= arity);
	return covering.length > 0
		? covering.reduce((smallest, candidate) => (candidate.version < smallest.version ? candidate : smallest)).version
		: highestVersion(registered).version;
}

function convertConnections(connections: unknown, converted: Map<string, Converted>): { connections: Connection[]; dropped: number } {
	// This editor's own shape: a flat list of endpoints by id.
	if (Array.isArray(connections)) {
		return { connections: connections.filter(isConnection), dropped: 0 };
	}
	if (!isRecord(connections)) return { connections: [], dropped: 0 };

	const result: Connection[] = [];
	let dropped = 0;
	for (const [sourceName, byKind] of Object.entries(connections)) {
		const origin = converted.get(sourceName);
		if (!origin || !isRecord(byKind)) continue;
		for (const [kind, slots] of Object.entries(byKind)) {
			if (!Array.isArray(slots)) continue;
			slots.forEach((targets, outputIndex) => {
				if (!Array.isArray(targets)) return;
				const sourcePort = portOfKind(origin.definition?.outputs, kind, outputIndex);
				for (const entry of targets) {
					const destination = isRecord(entry) && typeof entry.node === 'string' ? converted.get(entry.node) : undefined;
					const inputIndex = isRecord(entry) && typeof entry.index === 'number' ? entry.index : 0;
					const targetPort = portOfKind(destination?.definition?.inputs, kind, inputIndex);
					if (!destination || !sourcePort || !targetPort) {
						dropped += 1;
						continue;
					}
					result.push({
						id: crypto.randomUUID(),
						kind,
						source: { nodeId: origin.node.id, port: sourcePort },
						target: { nodeId: destination.node.id, port: targetPort }
					});
				}
			});
		}
	}
	return { connections: result, dropped };
}

/**
 * The port a definition declares for a kind and slot, or null it declares none.
 *
 * A null source port drops the edge for a node the catalogue knows and a null
 * target port drops it whatever the source is, which is the honest outcome: an
 * edge whose port does not exist here cannot be invented.
 */
function portOfKind(ports: Definition['inputs'] | undefined, kind: string, index: number): string | null {
	const matching = (ports ?? []).filter((port) => port.kind === kind);
	if (matching.length === 0) return null;
	// A placeholder declares its slots in order, and so does every definition
	// with more than one port of a kind.
	return (matching[index] ?? (kind === 'main' && index > 0 ? { name: `output${index + 1}` } : matching[0])).name;
}

function sourcePosition(value: unknown): Position {
	if (Array.isArray(value)) return { x: Number(value[0]) || 0, y: Number(value[1]) || 0 };
	if (isRecord(value)) return { x: Number(value.x) || 0, y: Number(value.y) || 0 };
	return { x: 0, y: 0 };
}

function isWorkflowNode(value: unknown): value is WorkflowNode {
	return (
		isRecord(value) &&
		typeof value.id === 'string' &&
		typeof value.name === 'string' &&
		typeof value.type === 'string' &&
		isRecord(value.position)
	);
}

function isConnection(value: unknown): value is Connection {
	return (
		isRecord(value) &&
		typeof value.id === 'string' &&
		typeof value.kind === 'string' &&
		isRecord(value.source) &&
		typeof value.source.nodeId === 'string' &&
		typeof value.source.port === 'string' &&
		isRecord(value.target) &&
		typeof value.target.nodeId === 'string' &&
		typeof value.target.port === 'string'
	);
}
