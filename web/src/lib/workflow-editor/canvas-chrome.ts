/**
 * Where the canvas's floating chrome sits, and how the view follows a node.
 *
 * The zoom controls, the minimap and the Chat button are three overlays on one
 * canvas. Left to their defaults the minimap was drawn exactly over the zoom,
 * fit and tidy buttons on any workflow past twelve nodes, so none of them could
 * be clicked (BUG-bcahaj). Each now owns a corner: the controls bottom-left, the
 * minimap bottom-right as in n8n, and the Chat button bottom-right *above* the
 * minimap when there is one.
 */

/** Svelte Flow's own `.svelte-flow__panel` margin, which places both the controls and the minimap. */
export const PANEL_MARGIN = 15;
/** The minimap's size, passed to `<MiniMap>` so the Chat dock can clear it. */
export const MINIMAP_WIDTH = 200;
export const MINIMAP_HEIGHT = 150;
/**
 * The minimap's distance from the canvas's bottom edge. The panel margin alone
 * left it over the bottom-right "Svelte Flow" attribution, about 19 px tall.
 */
export const MINIMAP_BOTTOM = 26;
/** Bottom offset of the Chat dock with nothing beneath it. */
export const CHAT_DOCK_BOTTOM = 16;
/** Space left between the minimap's top edge and the Chat button. */
const CHAT_DOCK_GAP = 12;

/** A minimap over a few nodes is noise; on a 63-node import it is the only way to know where you are. */
export function showsMinimap(nodeCount: number, narrow: boolean): boolean {
	return nodeCount > 12 && !narrow;
}

/** Pixels between the canvas's bottom edge and the Chat dock in the bottom-right corner. */
export function chatDockBottom(minimap: boolean): number {
	return minimap ? MINIMAP_BOTTOM + MINIMAP_HEIGHT + CHAT_DOCK_GAP : CHAT_DOCK_BOTTOM;
}

export type Viewport = { x: number; y: number; zoom: number };
export type Box = { x: number; y: number; width: number; height: number };

/**
 * The viewport that brings `node` (flow coordinates) fully into a canvas of
 * `area`'s size, or null when it is already there.
 *
 * Opening the inspector narrows the canvas, and a node near the right edge
 * ended up behind it while the view stayed put. Only an axis the node is out
 * of moves, and it moves to centre the node on that axis; the zoom is kept so
 * selecting never feels like the view jumped.
 */
export function viewportRevealing(node: Box, viewport: Viewport, area: { width: number; height: number }, margin = 16): Viewport | null {
	if (area.width <= 0 || area.height <= 0) return null;
	const { zoom } = viewport;
	const axis = (start: number, size: number, offset: number, extent: number): number => {
		const from = start * zoom + offset;
		const to = from + size * zoom;
		if (from >= margin && to <= extent - margin) return offset;
		return extent / 2 - (start + size / 2) * zoom;
	};
	const x = axis(node.x, node.width, viewport.x, area.width);
	const y = axis(node.y, node.height, viewport.y, area.height);
	if (x === viewport.x && y === viewport.y) return null;
	return { x, y, zoom };
}
