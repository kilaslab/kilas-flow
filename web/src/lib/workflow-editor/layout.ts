import dagre from '@dagrejs/dagre';

import type { Document } from '$lib/api/generated/models';

/**
 * Default footprint matches the compact canvas tile (68×68) plus the
 * name/subtitle band below it. Rank/node separation tracks the same
 * COLUMN/ROW pitch `document.ts` uses when placing new steps, so a tidy
 * canvas lands on the same rhythm as hand-placed ones.
 *
 * Tuned for n8n-like density after Tidy: shorter rank gaps so LR chains are
 * less “stringy”, with branch rows still clearing labels.
 */
export const DEFAULT_NODE_WIDTH = 72;
export const DEFAULT_NODE_HEIGHT = 96;
export const DEFAULT_RANK_SEP = 80;
export const DEFAULT_NODE_SEP = 44;
export const DEFAULT_MARGIN_X = 48;
export const DEFAULT_MARGIN_Y = 48;

export type LayoutNodeInput = {
	id: string;
	width?: number;
	height?: number;
};

export type LayoutEdgeInput = {
	source: string;
	target: string;
};

export type LayoutPosition = {
	id: string;
	position: { x: number; y: number };
};

export type AutoLayoutOptions = {
	/** Left-to-right like n8n; top-to-bottom is available for tall graphs. */
	direction?: 'LR' | 'TB';
	nodeWidth?: number;
	nodeHeight?: number;
	/** Gap between ranks (columns in LR). */
	rankSep?: number;
	/** Gap between nodes in the same rank. */
	nodeSep?: number;
	marginX?: number;
	marginY?: number;
};

/**
 * Computes top-left positions for a directed graph using dagre.
 *
 * Dagre reports node centres; this converts them to the top-left coordinates
 * Svelte Flow / the workflow document store. Isolated nodes keep a stable
 * left-to-right order by id so a tidy still packs them instead of stacking
 * everything at the origin.
 */
export function layoutGraph(
	nodes: LayoutNodeInput[],
	edges: LayoutEdgeInput[],
	options: AutoLayoutOptions = {}
): LayoutPosition[] {
	if (nodes.length === 0) return [];

	const {
		direction = 'LR',
		nodeWidth = DEFAULT_NODE_WIDTH,
		nodeHeight = DEFAULT_NODE_HEIGHT,
		rankSep = DEFAULT_RANK_SEP,
		nodeSep = DEFAULT_NODE_SEP,
		marginX = DEFAULT_MARGIN_X,
		marginY = DEFAULT_MARGIN_Y
	} = options;

	const graph = new dagre.graphlib.Graph();
	graph.setDefaultEdgeLabel(() => ({}));
	graph.setGraph({
		rankdir: direction,
		nodesep: nodeSep,
		ranksep: rankSep,
		marginx: marginX,
		marginy: marginY
	});

	const known = new Set(nodes.map((node) => node.id));
	for (const node of nodes) {
		graph.setNode(node.id, {
			width: node.width ?? nodeWidth,
			height: node.height ?? nodeHeight
		});
	}

	const seenEdges = new Set<string>();
	for (const edge of edges) {
		if (!known.has(edge.source) || !known.has(edge.target)) continue;
		if (edge.source === edge.target) continue;
		const key = `${edge.source}->${edge.target}`;
		if (seenEdges.has(key)) continue;
		seenEdges.add(key);
		graph.setEdge(edge.source, edge.target);
	}

	dagre.layout(graph);

	return nodes.map((node) => {
		const laid = graph.node(node.id);
		const width = node.width ?? nodeWidth;
		const height = node.height ?? nodeHeight;
		return {
			id: node.id,
			position: {
				x: Math.round((laid?.x ?? 0) - width / 2),
				y: Math.round((laid?.y ?? 0) - height / 2)
			}
		};
	});
}

/**
 * Returns a new workflow document with node positions from the current edges.
 * Connections and parameters are untouched; only `position` changes, so the
 * existing draft sync / save path persists the tidy without a separate API.
 */
export function tidyDocument(document: Document, options: AutoLayoutOptions = {}): Document {
	const nodes = document.nodes ?? [];
	if (nodes.length === 0) return document;

	const positions = layoutGraph(
		nodes.map((node) => ({ id: node.id })),
		(document.connections ?? []).map((connection) => ({
			source: connection.source.nodeId,
			target: connection.target.nodeId
		})),
		options
	);
	const byID = new Map(positions.map((entry) => [entry.id, entry.position]));

	return {
		...document,
		nodes: nodes.map((node) => {
			const next = byID.get(node.id);
			if (!next) return node;
			return { ...node, position: { x: next.x, y: next.y } };
		})
	};
}
