import { MarkerType, type Edge as FlowEdge, type Node as FlowNode } from '@xyflow/svelte';

import type { Connection, Definition, Document, Node as WorkflowNode } from '$lib/api/generated/models';
import * as m from '$lib/paraglide/messages.js';
import { isAnnotation } from './node-visual';
import type { CanvasValidationIssue } from './validation';

export type PropertyScope = 'parameters' | 'settings';

export type EditorNodeData = {
	definition: Definition;
	workflowNode: WorkflowNode;
	validationMessage?: string;
	// Whether the document pins this node's output, by its name: a run feeds
	// the pinned items instead of executing the node. The P key writes it.
	pinned?: boolean;
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
	newNodeID: () => string = createID,
	takenNames: Iterable<string> = []
): WorkflowNode {
	const parameters = valuesFromDefinitions(definition.parameters ?? []);
	const settings = valuesFromDefinitions(definition.sharedSettings ?? []);

	return {
		id: newNodeID(),
		name: uniqueNodeName(definition.displayName, takenNames),
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
export const COLUMN = 152;
const ROW = 136;
/** The pitch a step after a labelled output uses: one label width wider, so
 *  the new tile clears the true/false or branch names its source prints. */
export const BRANCH_COLUMN = COLUMN + 24;

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
 *
 * `column` widens the pitch: a node with named outputs (an IF's true/false, a
 * Switch's branches) carries its labels to the right of the tile, and a step
 * dropped at the ordinary column used to sit inside them.
 */
export function positionAfter(source: { x: number; y: number }, occupied: { x: number; y: number }[], column: number = COLUMN): { x: number; y: number } {
	const candidate = { x: source.x + column, y: source.y };
	// A tile is 68px under a 128px label, so anything closer than this overlaps
	// something the reader needs. Both bounds stay under the grid pitch, so a
	// node in the neighbouring column or row never counts as a collision.
	while (occupied.some((node) => Math.abs(node.x - candidate.x) < 128 && Math.abs(node.y - candidate.y) < 120)) {
		candidate.y += ROW;
	}
	return candidate;
}

/**
 * Where a node added for an attachment slot belongs: directly under the node
 * that owns the slot, where the slot's handle and its dashed "+" are, and pushed
 * one column to the right at a time until nothing is in the way.
 *
 * The mirror of `positionAfter`, which continues a main wire to the right. A
 * model, a memory or a tool is not a next step, and putting it there left the
 * wire to its slot doubling back under the tile it came from.
 */
export function positionBelow(owner: { x: number; y: number }, occupied: { x: number; y: number }[]): { x: number; y: number } {
	const candidate = { x: owner.x, y: owner.y + ROW };
	while (occupied.some((node) => Math.abs(node.x - candidate.x) < 128 && Math.abs(node.y - candidate.y) < 120)) {
		candidate.x += COLUMN;
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
	const { nodeIssues, edgeIssues } = issuesByTarget(validationIssues);
	const labels = nodeLabels(document);
	const pinned = pinnedNames(document);
	const nodes = (document.nodes ?? []).map((workflowNode) => projectNode(workflowNode, document, definitions, nodeIssues.get(workflowNode.id), pinned.has(workflowNode.name)));
	const edges = (document.connections ?? []).map((connection) => projectEdge(connection, labels, edgeIssues.get(connection.id)));

	return {
		nodes,
		edges,
		toDocument: (base) => documentFromFlow(base, nodes, edges)
	};
}

/** The node names the document pins data for. An empty pin (`[]`) is a pin. */
export function pinnedNames(document: Document): Set<string> {
	return new Set(Object.keys(document.pinData ?? {}));
}

/**
 * What built each projected node's and edge's `data`, so a later projection
 * can tell whether the object Svelte Flow holds still describes the document.
 * Keyed by `data` because Flow copies its nodes (`{...node, measured}`) but
 * never their data.
 */
const projectedFrom = new WeakMap<object, { source: WorkflowNode | Connection; definitions: Definition[] | null }>();

/**
 * documentFromCanvas for a canvas that is already on screen.
 *
 * A keystroke in the inspector replaces one node object in the draft, and
 * re-projecting the rest cost a rebuild of every node — and, because Svelte
 * Flow keys its measurements to the object it was given, a re-measure of
 * every tile (BUG-pzkpfr). Here every node and connection the document
 * carried over by reference keeps the very object Flow holds, measurements
 * included; only what changed is rebuilt, and arrays in which nothing changed
 * come back as they were.
 *
 * `selection` is what the editor holds selected; it is written onto the
 * objects whose flag disagrees, and onto nothing else.
 */
export function reconcileCanvas(
	held: { nodes: EditorFlowNode[]; edges: EditorFlowEdge[] },
	document: Document,
	definitions: Definition[],
	validationIssues: CanvasValidationIssue[],
	selection: { nodeIDs: ReadonlySet<string>; edgeIDs: ReadonlySet<string> }
): { nodes: EditorFlowNode[]; edges: EditorFlowEdge[] } {
	const { nodeIssues, edgeIssues } = issuesByTarget(validationIssues);
	const pinned = pinnedNames(document);
	const heldNodes = new Map(held.nodes.map((node) => [node.id, node]));
	const nodes = (document.nodes ?? []).map((workflowNode) => {
		const message = nodeIssues.get(workflowNode.id);
		const isPinned = pinned.has(workflowNode.name);
		const current = heldNodes.get(workflowNode.id);
		const origin = current ? projectedFrom.get(current.data) : undefined;
		const fresh =
			current && origin && origin.source === workflowNode && origin.definitions === definitions && current.data.validationMessage === message && (current.data.pinned ?? false) === isPinned
				? current
				: projectNode(workflowNode, document, definitions, message, isPinned);
		return withSelected(fresh, selection.nodeIDs.has(workflowNode.id));
	});

	const labels = nodeLabels(document);
	const heldEdges = new Map(held.edges.map((edge) => [edge.id, edge]));
	const edges = (document.connections ?? []).map((connection) => {
		const message = edgeIssues.get(connection.id);
		const current = heldEdges.get(connection.id);
		const reusable =
			current && projectedFrom.get(current.data!)?.source === connection && current.data?.validationMessage === message && current.ariaLabel === edgeLabel(connection, labels, message);
		return withSelected(reusable ? current : projectEdge(connection, labels, message), selection.edgeIDs.has(connection.id));
	});

	return { nodes: sameItems(nodes, held.nodes) ? held.nodes : nodes, edges: sameItems(edges, held.edges) ? held.edges : edges };
}

function withSelected<T extends { selected?: boolean }>(item: T, selected: boolean): T {
	return item.selected === selected ? item : { ...item, selected };
}

function sameItems<T>(next: T[], held: T[]): boolean {
	return next.length === held.length && next.every((item, index) => item === held[index]);
}

function issuesByTarget(validationIssues: CanvasValidationIssue[]) {
	return {
		nodeIssues: new Map(validationIssues.filter((issue) => issue.nodeID).map((issue) => [issue.nodeID!, issue.message])),
		edgeIssues: new Map(validationIssues.filter((issue) => issue.connectionID).map((issue) => [issue.connectionID!, issue.message]))
	};
}

function nodeLabels(document: Document): Map<string, string> {
	return new Map((document.nodes ?? []).map((node) => [node.id, node.name]));
}

function projectNode(
	workflowNode: WorkflowNode,
	document: Document,
	definitions: Definition[],
	validationMessage: string | undefined,
	pinned = false
): EditorFlowNode {
	const resolved = resolveDefinition(workflowNode.type, workflowNode.typeVersion, definitions);
	const definition = resolved ?? unavailableDefinition(workflowNode, document.connections ?? []);
	// The workflow node is shared, not cloned: the draft is immutable at
	// every mutation boundary, so the node object can never change under a
	// component that is reading it — and deep-cloning every node here is
	// what made one keystroke cost a full-canvas copy. Only the annotation
	// geometry below is derived, and it is derived from the parameters.
	const annotation = isAnnotation(definition);
	const data: EditorNodeData = { definition, workflowNode, validationMessage };
	if (pinned) data.pinned = true;
	// A placeholder's ports are read off the document's connections, so it is
	// never reused: a new wire can change them without touching the node.
	projectedFrom.set(data, { source: workflowNode, definitions: resolved ? definitions : null });
	return {
		id: workflowNode.id,
		type: 'workflow',
		position: { ...workflowNode.position },
		// A sticky note is a rectangle behind the graph, not a tile: it
		// carries its own size and takes no part in the flow.
		...(annotation
			? {
					style: `width: ${positiveSize(workflowNode.parameters?.width, 240)}px; height: ${positiveSize(workflowNode.parameters?.height, 160)}px`,
					zIndex: -1
				}
			: {}),
		data,
		ariaLabel: validationMessage ? `${workflowNode.name}: ${validationMessage}` : workflowNode.name
	};
}

function edgeLabel(connection: Connection, labels: Map<string, string>, validationMessage: string | undefined): string {
	// Named by the nodes' names, not their ids: a screen reader announcing a
	// connection has to say what it connects, and a UUID says nothing.
	const from = `${labels.get(connection.source.nodeId) ?? connection.source.nodeId} ${connection.source.port}`;
	const to = `${labels.get(connection.target.nodeId) ?? connection.target.nodeId} ${connection.target.port}`;
	return `${from} to ${to}${validationMessage ? `: ${validationMessage}` : ''}`;
}

function projectEdge(connection: Connection, labels: Map<string, string>, validationMessage: string | undefined): EditorFlowEdge {
	// An attachment carries configuration, not items. It is drawn as a dashed
	// curve from below rather than a stepped line with an arrow, so the two
	// connection kinds cannot be mistaken for each other at a glance.
	const attachment = connection.kind !== 'main';
	const data = { connection: clone(connection), validationMessage };
	projectedFrom.set(data, { source: connection, definitions: null });
	return {
		id: connection.id,
		type: attachment ? 'default' : 'smoothstep',
		class: attachment ? 'kf-edge-attachment' : undefined,
		...(attachment ? {} : { markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color: 'var(--xy-edge-stroke)' } }),
		source: connection.source.nodeId,
		sourceHandle: connection.source.port,
		target: connection.target.nodeId,
		targetHandle: connection.target.port,
		data,
		...(validationMessage ? { style: 'stroke: var(--destructive); stroke-width: 2px' } : {}),
		ariaLabel: edgeLabel(connection, labels, validationMessage)
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

// The save body lives in its own module, so the workflows list can build one
// without loading the flow library this file imports.
export { toWorkflowInput } from './workflow-input';

export function updateNodeProperty(
	document: Document,
	nodeID: string,
	scope: PropertyScope,
	key: string,
	value: unknown
): Document {
	let found = false;
	// Only the edited node is rebuilt; every other node object is carried over
	// by reference. Cloning all of them here is what made a keystroke in one
	// field cost a copy of the whole canvas, and the projection downstream
	// re-renders only what actually changed.
	const nodes = (document.nodes ?? []).map((node) => {
		if (node.id !== nodeID) return node;
		found = true;
		return { ...node, [scope]: { ...((node[scope] ?? {}) as Record<string, unknown>), [key]: clone(value) } };
	});
	if (!found) return document;
	return { ...document, nodes };
}

/**
 * Sets or clears one credential reference on a node.
 *
 * The document records only the credential ID; the secret itself never enters
 * workflow JSON, so an export or an n8n-style copy carries no credential
 * material.
 */
export function updateNodeCredential(document: Document, nodeID: string, typeID: string, credentialID: string): Document {
	let found = false;
	const nodes = (document.nodes ?? []).map((node) => {
		if (node.id !== nodeID) return node;
		found = true;
		const credentials = { ...((node.credentials ?? {}) as Record<string, string>) };
		if (credentialID) {
			credentials[typeID] = credentialID;
		} else {
			delete credentials[typeID];
		}
		const next = { ...node };
		if (Object.keys(credentials).length > 0) {
			next.credentials = credentials;
		} else {
			delete next.credentials;
		}
		return next;
	});
	if (!found) return document;
	return { ...document, nodes };
}

export function workflowDocumentEquals(left: Document, right: Document): boolean {
	return stableJSON(left) === stableJSON(right);
}

/**
 * A name no other node is using, n8n-style: "Set" then "Set1", "Set2".
 *
 * Expressions address nodes by name, so two nodes called "Set" make
 * `$('Set')` ambiguous and the second one unreachable. A name already ending
 * in a number counts up from it, which is what makes duplicating a duplicate
 * read as one more copy rather than "Set11".
 */
/**
 * The names of the nodes that run before one node: every node with a path of
 * item connections into it. They are what `$('Name')` can read there, and the
 * order is the order they appear in the document, so a list built from them
 * does not reshuffle while the user edits something unrelated.
 */
export function upstreamNodeNames(document: Document, nodeID: string): string[] {
	const sourcesOf = new Map<string, string[]>();
	for (const connection of document.connections ?? []) {
		if (connection.kind !== 'main') continue;
		const sources = sourcesOf.get(connection.target.nodeId) ?? [];
		sources.push(connection.source.nodeId);
		sourcesOf.set(connection.target.nodeId, sources);
	}
	const seen = new Set<string>();
	const pending = [...(sourcesOf.get(nodeID) ?? [])];
	while (pending.length > 0) {
		const next = pending.pop()!;
		if (next === nodeID || seen.has(next)) continue;
		seen.add(next);
		pending.push(...(sourcesOf.get(next) ?? []));
	}
	return (document.nodes ?? []).filter((node) => seen.has(node.id)).map((node) => node.name);
}

export function uniqueNodeName(desired: string, taken: Iterable<string>): string {
	const used = taken instanceof Set ? taken : new Set(taken);
	const base = desired.trim() === '' ? 'Node' : desired.trim();
	if (!used.has(base)) return base;

	const numbered = /^(\D*)(\d+)$/.exec(base);
	const stem = numbered && numbered[1] !== '' ? numbered[1] : base;
	let counter = numbered && numbered[1] !== '' ? Number(numbered[2]) + 1 : 1;
	while (used.has(`${stem}${counter}`)) counter += 1;
	return `${stem}${counter}`;
}

/**
 * Renames a node and rewrites every reference to it.
 *
 * A rename that left `$('Old name')` behind would break the workflow silently:
 * the expression still parses, the node it named is simply gone. The rewrite
 * covers the reference forms the expression surface accepts — `$('Name')`,
 * `$items('Name')`, `$node['Name']` — in both quote styles.
 *
 * The `original` capsule on an imported placeholder is skipped: it is a
 * verbatim copy of the source node, kept so an export returns it whole, and
 * rewriting inside it would corrupt that.
 */
export function renameNode(document: Document, nodeID: string, rawName: string): Document {
	const name = rawName.trim();
	const target = (document.nodes ?? []).find((node) => node.id === nodeID);
	if (!target || name === '' || name === target.name) return document;

	// The node's pin is keyed by its name, so the rename carries it — a pin
	// left under the old name would pin nothing.
	let { pinData } = document;
	if (pinData && pinData[target.name] !== undefined) {
		pinData = { ...pinData, [name]: pinData[target.name] };
		delete pinData[target.name];
	}

	return {
		...document,
		...(pinData === document.pinData ? {} : { pinData }),
		nodes: (document.nodes ?? []).map((node) => {
			if (node.id === nodeID) return { ...node, name };
			return rewriteNodeReferences(node, target.name, name);
		})
	};
}

/**
 * Inserts a detached set of nodes and connections — a paste, or a duplicate.
 *
 * Ids are regenerated and names made unique, because both operations produce a
 * second copy of something that already exists and the document's identity is
 * its ids and its names. Connections are remapped through the same table, so a
 * fragment never lands pointing at the node it was copied from.
 */
export function insertNodes(
	document: Document,
	nodes: WorkflowNode[],
	connections: Connection[],
	offset: { x: number; y: number },
	newID: () => string = createID
): { document: Document; nodeIDs: string[] } {
	if (nodes.length === 0) return { document, nodeIDs: [] };

	const taken = new Set((document.nodes ?? []).map((node) => node.name));
	const idMap = new Map<string, string>();
	const inserted = nodes.map((node) => {
		const id = newID();
		idMap.set(node.id, id);
		const name = uniqueNodeName(node.name, taken);
		taken.add(name);
		return {
			...clone(node),
			id,
			name,
			position: { x: node.position.x + offset.x, y: node.position.y + offset.y }
		};
	});
	const rewired = connections.flatMap<Connection>((connection) => {
		const source = idMap.get(connection.source.nodeId);
		const target = idMap.get(connection.target.nodeId);
		// A connection to a node outside the fragment is dropped rather than
		// kept pointing at the original, which is the node the user is
		// duplicating.
		if (!source || !target) return [];
		return [{ ...clone(connection), id: newID(), source: { ...connection.source, nodeId: source }, target: { ...connection.target, nodeId: target } }];
	});

	return {
		document: {
			...document,
			nodes: [...(document.nodes ?? []), ...inserted],
			connections: [...(document.connections ?? []), ...rewired]
		},
		nodeIDs: inserted.map((node) => node.id)
	};
}

/** A copy of the named nodes, offset so the copy is visibly a second one. */
export function duplicateNodes(
	document: Document,
	nodeIDs: Iterable<string>,
	offset = { x: 40, y: 40 },
	newID: () => string = createID
): { document: Document; nodeIDs: string[] } {
	const wanted = new Set(nodeIDs);
	const nodes = (document.nodes ?? []).filter((node) => wanted.has(node.id));
	if (nodes.length === 0) return { document, nodeIDs: [] };
	const connections = (document.connections ?? []).filter(
		(connection) => wanted.has(connection.source.nodeId) && wanted.has(connection.target.nodeId)
	);
	return insertNodes(document, nodes, connections, offset, newID);
}

/**
 * Writes back the size a sticky note was dragged to.
 *
 * Size is a parameter rather than a canvas-only measurement, because it is part
 * of what the note *is*: an export of an n8n workflow carries the note's
 * dimensions, and discarding them would lose the layout on the way back.
 */
export function updateNodeSize(document: Document, nodeID: string, width: number, height: number): Document {
	let found = false;
	const nodes = (document.nodes ?? []).map((node) => {
		if (node.id !== nodeID) return node;
		found = true;
		return { ...node, parameters: { ...(node.parameters ?? {}), width: Math.round(width), height: Math.round(height) } };
	});
	if (!found) return document;
	return { ...document, nodes };
}

/**
 * n8n's D: disables the named nodes, or enables them again when every one of
 * them is already disabled. Enabling drops the flag rather than writing
 * `false`, so a node toggled twice saves exactly as it was. Nodes not named
 * keep their identity, which the canvas projection relies on.
 */
export function toggleNodesDisabled(document: Document, nodeIDs: readonly string[]): Document {
	const named = new Set(nodeIDs);
	const targets = (document.nodes ?? []).filter((node) => named.has(node.id));
	if (targets.length === 0) return document;
	const disable = targets.some((node) => !node.disabled);
	return {
		...document,
		nodes: (document.nodes ?? []).map((node) => {
			if (!named.has(node.id)) return node;
			if (disable) return { ...node, disabled: true };
			// eslint-disable-next-line @typescript-eslint/no-unused-vars
			const { disabled, ...enabled } = node;
			return enabled;
		})
	};
}

/**
 * n8n's P: pins the named nodes' output, or unpins them when every one of
 * them is already pinned. A pin is the node's entry in the document's
 * pinData, keyed by name; the editor has no run data of its own to record,
 * so a node pinned here pins an empty output — a run replays no items for it
 * instead of executing it, which is also what n8n's editor pins for a node
 * that has not produced data. Unpinning drops the entry, so a node toggled
 * twice saves exactly as it was; a map left empty is dropped whole.
 */
export function toggleNodesPinned(document: Document, nodeIDs: readonly string[]): Document {
	const named = new Set(nodeIDs);
	const targets = (document.nodes ?? []).filter((node) => named.has(node.id));
	if (targets.length === 0) return document;
	const pins = document.pinData ?? {};
	const pin = targets.some((node) => pins[node.name] === undefined);
	// eslint-disable-next-line @typescript-eslint/no-unused-vars
	const { pinData: _previous, ...rest } = document;
	const entries = { ...pins };
	for (const node of targets) {
		if (pin) entries[node.name] = entries[node.name] ?? [];
		else delete entries[node.name];
	}
	if (Object.keys(entries).length === 0) return rest;
	return { ...document, pinData: entries };
}

function rewriteNodeReferences(node: WorkflowNode, from: string, to: string): WorkflowNode {
	const next = clone(node);
	for (const scope of ['parameters', 'settings'] as const) {
		const values = next[scope] as Record<string, unknown> | undefined;
		if (!values) continue;
		const rewritten: Record<string, unknown> = {};
		for (const [key, value] of Object.entries(values)) {
			rewritten[key] = key === 'original' ? value : rewriteValue(value, from, to);
		}
		next[scope] = rewritten as never;
	}
	return next;
}

function rewriteValue(value: unknown, from: string, to: string): unknown {
	if (typeof value === 'string') return rewriteReferencesInText(value, from, to);
	if (Array.isArray(value)) return value.map((item) => rewriteValue(item, from, to));
	if (value !== null && typeof value === 'object') {
		return Object.fromEntries(Object.entries(value as Record<string, unknown>).map(([key, item]) => [key, rewriteValue(item, from, to)]));
	}
	return value;
}

const REFERENCE_CALL = /(\$\w*\(\s*)(['"`])((?:\\.|(?!\2).)*)\2/g;
const REFERENCE_INDEX = /(\$\w+\[\s*)(['"`])((?:\\.|(?!\2).)*)\2(\s*\])/g;

function rewriteReferencesInText(text: string, from: string, to: string): string {
	if (!text.includes(from)) return text;
	const replace = (all: string, prefix: string, quote: string, name: string, suffix = '') =>
		name === from ? `${prefix}${quote}${to}${quote}${suffix}` : all;
	return text
		.replace(REFERENCE_CALL, (all, prefix: string, quote: string, name: string) => replace(all, prefix, quote, name))
		.replace(REFERENCE_INDEX, (all, prefix: string, quote: string, name: string, suffix: string) => replace(all, prefix, quote, name, suffix));
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
		description: m.canvas_unavailable_node_description(),
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

/** A sticky note's stored dimension, or its default when it has none. */
function positiveSize(value: unknown, fallback: number): number {
	return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : fallback;
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
