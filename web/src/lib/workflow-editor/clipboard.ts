import { convertWorkflowFragment } from '$lib/api/generated/interop/interop';
import type { Connection, Document, ImportIssue, Node as WorkflowNode } from '$lib/api/generated/models';

import { insertNodes } from './document';
import { isRecord } from '$lib/type-guards';

/**
 * The marker that identifies this editor's own clipboard payload.
 *
 * Without it a pasted fragment of this editor's JSON would be indistinguishable
 * from an n8n workflow, and the importer (which rewrites types and positions)
 * would mangle a copy that needs nothing done to it.
 */
export const FRAGMENT_KIND = 'kilasflow.node-fragment';

export type PastePayload = {
	nodes: WorkflowNode[];
	connections: Connection[];
	/**
	 * What the import translation could not carry, in the import report's own
	 * terms: blocking, lossy, dropped. Empty for this editor's own fragments.
	 */
	issues: ImportIssue[];
};

/**
 * What a clipboard holds, before anything is converted.
 *
 * An n8n payload is only recognised here. It is converted by the server's
 * importer (`convertN8n`), because a paste has to produce exactly what "Import
 * n8n" would, and the importer is the one translator that knows every mapped
 * node, the `=` expression prefix and the placeholder capsule.
 */
export type ClipboardContent = { kind: 'fragment'; payload: PastePayload } | { kind: 'n8n'; workflow: Record<string, unknown> };

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
 * This editor's own JSON — a copied fragment, or a whole KilasFlow document —
 * keeps its connections as a list of endpoints by id and needs no conversion.
 * Anything else with a node list is read as n8n's: connections keyed by node
 * name, positions as `[x, y]`, types in n8n's namespace.
 *
 * Returns null for text that is not workflow JSON at all, so the caller can let
 * an ordinary paste through to the browser.
 */
export function readClipboard(text: string): ClipboardContent | null {
	let parsed: unknown;
	try {
		parsed = JSON.parse(text);
	} catch {
		return null;
	}
	if (!isRecord(parsed) || !Array.isArray(parsed.nodes)) return null;

	if (parsed.kind === FRAGMENT_KIND || Array.isArray(parsed.connections)) {
		const nodes = parsed.nodes.filter(isWorkflowNode);
		if (nodes.length === 0) return null;
		const connections = Array.isArray(parsed.connections) ? parsed.connections.filter(isConnection) : [];
		return { kind: 'fragment', payload: { nodes, connections, issues: [] } };
	}
	return { kind: 'n8n', workflow: parsed };
}

/**
 * Converts an n8n payload through the server's importer.
 *
 * The request saves nothing. It throws what the importer refused — JSON that
 * is not n8n's, two nodes with one name — as the API error, whose message
 * names the reason.
 */
export async function convertN8n(workflow: Record<string, unknown>, convert = convertWorkflowFragment): Promise<PastePayload> {
	const response = await convert({ format: 'n8n', workflow });
	// A non-2xx answer throws inside the transport; this narrows the union
	// the generated client declares.
	if (response.status !== 200) throw new Error(`the n8n conversion answered ${response.status}`);
	const converted = response.data;
	return {
		nodes: converted.nodes ?? [],
		connections: converted.connections ?? [],
		issues: converted.unsupported ?? []
	};
}

/**
 * Pastes a payload into the document, offsetting it and renaming it uniquely.
 *
 * The report is rewritten to the nodes as they landed: a paste re-ids every
 * node and may rename one that collides, and an entry naming the source's id
 * or name would point at nothing on this canvas, or at a different node.
 */
export function pasteInto(
	document: Document,
	payload: PastePayload,
	offset: { x: number; y: number },
	newID?: () => string
): { document: Document; nodeIDs: string[]; issues: ImportIssue[] } {
	const inserted = insertNodes(document, payload.nodes, payload.connections, offset, newID);
	const landedByID = new Map<string, WorkflowNode>();
	const landedByName = new Map<string, WorkflowNode>();
	const byID = new Map((inserted.document.nodes ?? []).map((node) => [node.id, node]));
	payload.nodes.forEach((node, index) => {
		const placed = byID.get(inserted.nodeIDs[index]);
		if (!placed) return;
		landedByID.set(node.id, placed);
		landedByName.set(node.name, placed);
	});
	// An entry about a connection names its source node but carries no id, so
	// it is matched by the name the node had in the payload — the importer
	// keeps n8n's names as written.
	const issues = payload.issues.map((issue) => {
		const placed = issue.nodeId ? landedByID.get(issue.nodeId) : issue.nodeName ? landedByName.get(issue.nodeName) : undefined;
		return placed ? { ...issue, nodeId: placed.id, nodeName: placed.name } : issue;
	});
	return { ...inserted, issues };
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
