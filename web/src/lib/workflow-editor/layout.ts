import dagre from '@dagrejs/dagre';

import type { Document } from '$lib/api/generated/models';

/**
 * Footprint of a node whose real size the editor did not report. Rank/node
 * separation tracks the COLUMN/ROW pitch `document.ts` uses when placing new
 * steps, so a tidy canvas lands on the same rhythm as hand-placed ones.
 */
export const DEFAULT_NODE_WIDTH = 72;
export const DEFAULT_NODE_HEIGHT = 96;
export const DEFAULT_RANK_SEP = 80;
export const DEFAULT_NODE_SEP = 44;
export const DEFAULT_MARGIN_X = 48;
export const DEFAULT_MARGIN_Y = 48;
/** Vertical clearance between a node and the tiles attached beneath it. */
export const DEFAULT_ATTACHMENT_GAP = 32;

export type LayoutNodeInput = {
	id: string;
	width?: number;
	height?: number;
};

export type LayoutEdgeInput = {
	source: string;
	target: string;
	/**
	 * An attachment (an agent's model, memory or tool) carries configuration
	 * rather than items: it is placed with its consumer, not in the flow. A
	 * provider that also takes part in the main flow keeps its place there.
	 */
	attachment?: boolean;
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
	/** Measured canvas size per node id, so a 240px hub is not laid out as 72px. */
	sizes?: Record<string, { width: number; height: number } | undefined>;
	/** Node ids that annotate the canvas. They are never positioned here. */
	annotations?: Iterable<string>;
	attachmentGap?: number;
};

/**
 * Computes top-left positions for a directed graph using dagre.
 *
 * Dagre reports node centres; this converts them to the top-left coordinates
 * Svelte Flow / the workflow document store. Isolated nodes keep a stable
 * left-to-right order by id so a tidy still packs them instead of stacking
 * everything at the origin.
 *
 * Two kinds of node are deliberately not run through dagre. A canvas annotation
 * is not a step, and feeding it in stacked every sticky note in one column. The
 * model, memory and tool tiles attached to an agent are configuration: dagre
 * sees only their attachment wire, ranks them as upstream steps and puts them
 * in a column to the *left* of the agent they belong to. They are placed
 * beneath their consumer instead.
 *
 * Every returned position is guaranteed not to overlap another: the last pass
 * separates tiles the layout would have collided, which is the one promise a
 * Tidy button has to keep.
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
		marginY = DEFAULT_MARGIN_Y,
		sizes = {},
		annotations = [],
		attachmentGap = DEFAULT_ATTACHMENT_GAP
	} = options;

	const declared = new Map(nodes.map((node) => [node.id, node]));
	const sizeOf = (id: string) => ({
		width: sizes[id]?.width ?? declared.get(id)?.width ?? nodeWidth,
		height: sizes[id]?.height ?? declared.get(id)?.height ?? nodeHeight
	});
	const annotationIDs = new Set(annotations);

	const mainEdges = edges.filter((edge) => !edge.attachment);
	const flowIDs = new Set<string>();
	for (const edge of mainEdges) {
		flowIDs.add(edge.source);
		flowIDs.add(edge.target);
	}
	// A provider that is not itself in the main flow is placed with its
	// consumer; one that is (a Code node feeding a chain and a model) stays
	// where the flow put it.
	const attachedTo = new Map<string, string[]>();
	const clusters = new Set<string>();
	for (const edge of edges) {
		if (!edge.attachment || !declared.has(edge.source) || !declared.has(edge.target)) continue;
		if (flowIDs.has(edge.source)) continue;
		if (!attachedTo.has(edge.target)) attachedTo.set(edge.target, []);
		attachedTo.get(edge.target)!.push(edge.source);
		clusters.add(edge.source);
	}

	const steps = nodes.filter((node) => !annotationIDs.has(node.id) && !clusters.has(node.id));
	const positions = new Map<string, { x: number; y: number }>(dagrePositions(steps, mainEdges, { direction, rankSep, nodeSep, marginX, marginY, sizeOf }));

	// Attached tiles sit beneath the consumer, one row per level, so a chain
	// (loader → splitter → embeddings) reads downward instead of sideways.
	const placedClusters = new Set<string>();
	const placeCluster = (rootID: string) => {
		const root = positions.get(rootID);
		if (!root) return;
		const y = root.y + sizeOf(rootID).height + attachmentGap;
		let x = root.x;
		for (const child of attachedTo.get(rootID) ?? []) {
			if (placedClusters.has(child)) continue;
			placedClusters.add(child);
			positions.set(child, { x, y });
			x += sizeOf(child).width + nodeSep;
		}
		for (const child of attachedTo.get(rootID) ?? []) placeCluster(child);
	};
	for (const rootID of attachedTo.keys()) placeCluster(rootID);

	const laid = nodes
		.filter((node) => positions.has(node.id))
		.map((node) => ({ id: node.id, rect: { ...positions.get(node.id)!, ...sizeOf(node.id) } }));

	return separateOverlaps(laid, attachmentGap).map((entry) => ({ id: entry.id, position: { x: entry.rect.x, y: entry.rect.y } }));
}

type Sized = { id: string; rect: { x: number; y: number; width: number; height: number } };

/**
 * Pushes tiles apart until none of them overlap.
 *
 * The layout above is geometric and does not know where dagre put the rest of
 * the graph relative to a cluster of attached tiles. Rather than teach it, the
 * result is separated: tiles are visited top-to-bottom, left-to-right, and any
 * tile intersecting one already placed moves below it. Deterministic, and it
 * leaves an already-clean layout untouched.
 */
function separateOverlaps(entries: Sized[], gap: number): Sized[] {
	const placed: Sized[] = [];
	for (const entry of [...entries].sort((left, right) => left.rect.y - right.rect.y || left.rect.x - right.rect.x)) {
		const rect = { ...entry.rect };
		let moved = true;
		while (moved) {
			moved = false;
			for (const other of placed) {
				if (!intersects(rect, other.rect)) continue;
				rect.y = other.rect.y + other.rect.height + gap;
				moved = true;
			}
		}
		placed.push({ id: entry.id, rect });
	}
	return placed;
}

function intersects(
	left: { x: number; y: number; width: number; height: number },
	right: { x: number; y: number; width: number; height: number }
): boolean {
	return left.x < right.x + right.width && right.x < left.x + left.width && left.y < right.y + right.height && right.y < left.y + left.height;
}

function dagrePositions(
	nodes: LayoutNodeInput[],
	edges: LayoutEdgeInput[],
	options: {
		direction: 'LR' | 'TB';
		rankSep: number;
		nodeSep: number;
		marginX: number;
		marginY: number;
		sizeOf: (id: string) => { width: number; height: number };
	}
): Map<string, { x: number; y: number }> {
	const graph = new dagre.graphlib.Graph();
	graph.setDefaultEdgeLabel(() => ({}));
	graph.setGraph({
		rankdir: options.direction,
		nodesep: options.nodeSep,
		ranksep: options.rankSep,
		marginx: options.marginX,
		marginy: options.marginY
	});

	const known = new Set(nodes.map((node) => node.id));
	for (const node of nodes) graph.setNode(node.id, options.sizeOf(node.id));

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

	const positions = new Map<string, { x: number; y: number }>();
	for (const node of nodes) {
		const laid = graph.node(node.id);
		const size = options.sizeOf(node.id);
		positions.set(node.id, {
			x: Math.round((laid?.x ?? 0) - size.width / 2),
			y: Math.round((laid?.y ?? 0) - size.height / 2)
		});
	}
	return positions;
}

/**
 * Returns a new workflow document with node positions from the current edges.
 * Connections and parameters are untouched; only `position` changes, so the
 * existing draft sync / save path persists the tidy without a separate API.
 *
 * Annotations are not laid out — they are moved by the same distance as the
 * nodes they were covering, which keeps the setup notes that come with an
 * imported template beside the steps they explain. One left covering nothing
 * stays exactly where its author put it.
 */
export function tidyDocument(document: Document, options: AutoLayoutOptions = {}): Document {
	const nodes = document.nodes ?? [];
	if (nodes.length === 0) return document;

	const annotationIDs = new Set(options.annotations ?? []);
	const before = new Map(nodes.map((node) => [node.id, node.position]));
	const positions = new Map(
		layoutGraph(
			nodes.map((node) => ({ id: node.id })),
			(document.connections ?? []).map((connection) => ({
				source: connection.source.nodeId,
				target: connection.target.nodeId,
				attachment: connection.kind !== 'main'
			})),
			options
		).map((entry) => [entry.id, entry.position])
	);

	const sizeOf = (id: string, index: number) => ({
		width: options.sizes?.[id]?.width ?? options.nodeWidth ?? DEFAULT_NODE_WIDTH,
		height: options.sizes?.[id]?.height ?? options.nodeHeight ?? DEFAULT_NODE_HEIGHT,
		annotation: annotationIDs.has(id)
	});

	return {
		...document,
		nodes: nodes.map((node, index) => {
			if (!annotationIDs.has(node.id)) {
				const next = positions.get(node.id);
				return next ? { ...node, position: { x: next.x, y: next.y } } : node;
			}
			const shift = coverageShift(node, nodes, before, positions, sizeOf);
			if (!shift) return node;
			const from = before.get(node.id)!;
			return { ...node, position: { x: from.x + shift.x, y: from.y + shift.y } };
		})
	};
}

/**
 * How far an annotation has to move to stay with what it covered.
 *
 * The average displacement of the steps whose original rectangles it enclosed
 * or overlapped, which is what makes a note pinned beside a chain travel with
 * the chain rather than with one arbitrary node in it.
 */
function coverageShift(
	annotation: { id: string; position: { x: number; y: number } },
	nodes: NonNullable<Document['nodes']>,
	before: Map<string, { x: number; y: number }>,
	after: Map<string, { x: number; y: number }>,
	sizeOf: (id: string, index: number) => { width: number; height: number; annotation: boolean }
): { x: number; y: number } | null {
	const index = nodes.findIndex((node) => node.id === annotation.id);
	const box = sizeOf(annotation.id, index);
	const rect = { ...annotation.position, ...box };
	let totalX = 0;
	let totalY = 0;
	let covered = 0;
	nodes.forEach((node, nodeIndex) => {
		if (node.id === annotation.id) return;
		const nodeBefore = before.get(node.id);
		const nodeAfter = after.get(node.id);
		if (!nodeBefore || !nodeAfter) return;
		const size = sizeOf(node.id, nodeIndex);
		if (size.annotation) return;
		if (!intersects(rect, { ...nodeBefore, ...size })) return;
		totalX += nodeAfter.x - nodeBefore.x;
		totalY += nodeAfter.y - nodeBefore.y;
		covered += 1;
	});
	return covered === 0 ? null : { x: Math.round(totalX / covered), y: Math.round(totalY / covered) };
}
