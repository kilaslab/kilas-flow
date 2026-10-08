import { describe, expect, it } from 'vitest';

import { CHAT_DOCK_BOTTOM, chatDockBottom, MINIMAP_BOTTOM, MINIMAP_HEIGHT, MINIMAP_WIDTH, PANEL_MARGIN, showsMinimap, viewportRevealing } from './canvas-chrome';

type Rect = { left: number; top: number; right: number; bottom: number };

function overlaps(a: Rect, b: Rect): boolean {
	return a.left < b.right && b.left < a.right && a.top < b.bottom && b.top < a.bottom;
}

/**
 * The three floating pieces of canvas chrome, laid out the way the editor
 * places them: controls bottom-left, minimap bottom-right, the Chat button in
 * the bottom-right corner above whatever the minimap occupies.
 */
function chrome(width: number, height: number, nodeCount: number) {
	const controls: Rect = { left: PANEL_MARGIN, top: height - PANEL_MARGIN - 4 * 26, right: PANEL_MARGIN + 26, bottom: height - PANEL_MARGIN };
	const minimap: Rect | null = showsMinimap(nodeCount, false)
		? { left: width - PANEL_MARGIN - MINIMAP_WIDTH, top: height - MINIMAP_BOTTOM - MINIMAP_HEIGHT, right: width - PANEL_MARGIN, bottom: height - MINIMAP_BOTTOM }
		: null;
	// Svelte Flow's attribution link, bottom-right, measured at about 59x19.
	const attribution: Rect = { left: width - 60, top: height - 20, right: width, bottom: height };
	const chatBottom = height - chatDockBottom(minimap !== null);
	const chat: Rect = { left: width - 16 - 90, top: chatBottom - 40, right: width - 16, bottom: chatBottom };
	return { controls, minimap, chat, attribution };
}

describe('canvas chrome', () => {
	it('shows the minimap only past twelve nodes and never on a narrow canvas', () => {
		expect(showsMinimap(12, false)).toBe(false);
		expect(showsMinimap(13, false)).toBe(true);
		expect(showsMinimap(300, true)).toBe(false);
	});

	it('keeps the Chat button in its corner when there is no minimap to clear', () => {
		expect(chatDockBottom(false)).toBe(CHAT_DOCK_BOTTOM);
	});

	it('lifts the Chat button clear of the minimap', () => {
		expect(chatDockBottom(true)).toBeGreaterThan(MINIMAP_BOTTOM + MINIMAP_HEIGHT);
	});

	it('never lets the minimap, the controls and the Chat button overlap', () => {
		for (const width of [480, 648, 900, 1100, 1384]) {
			for (const nodeCount of [0, 5, 12, 13, 63, 300]) {
				const { controls, minimap, chat, attribution } = chrome(width, 640, nodeCount);
				expect(overlaps(controls, chat), `controls/chat at ${width}px, ${nodeCount} nodes`).toBe(false);
				if (minimap) {
					expect(overlaps(controls, minimap), `controls/minimap at ${width}px`).toBe(false);
					expect(overlaps(minimap, chat), `minimap/chat at ${width}px`).toBe(false);
					expect(overlaps(minimap, attribution), `minimap/attribution at ${width}px`).toBe(false);
				}
			}
		}
	});
});

describe('viewportRevealing', () => {
	const area = { width: 600, height: 500 };
	const tile = { x: 100, y: 100, width: 64, height: 64 };

	it('leaves the view alone when the node is already in sight', () => {
		expect(viewportRevealing(tile, { x: 0, y: 0, zoom: 1 }, area)).toBeNull();
	});

	it('pans a node hidden past the right edge back into the canvas', () => {
		// At x=0 the tile sits at 900..964 on screen, beyond a 600 px canvas:
		// this is the node that disappeared behind the opening inspector.
		const next = viewportRevealing({ ...tile, x: 900 }, { x: 0, y: 0, zoom: 1 }, area);
		expect(next).not.toBeNull();
		const left = 900 * next!.zoom + next!.x;
		expect(left).toBeGreaterThanOrEqual(0);
		expect(left + 64).toBeLessThanOrEqual(area.width);
		// Only the axis that was out of view moves.
		expect(next!.y).toBe(0);
		expect(next!.zoom).toBe(1);
	});

	it('pans a node above the canvas down into view and keeps the zoom', () => {
		const next = viewportRevealing(tile, { x: 0, y: -400, zoom: 0.5 }, area);
		expect(next).not.toBeNull();
		const top = 100 * 0.5 + next!.y;
		expect(top).toBeGreaterThanOrEqual(0);
		expect(top + 32).toBeLessThanOrEqual(area.height);
		expect(next!.x).toBe(0);
		expect(next!.zoom).toBe(0.5);
	});

	it('treats a node that is only partly visible as hidden', () => {
		expect(viewportRevealing({ ...tile, x: 560 }, { x: 0, y: 0, zoom: 1 }, area)).not.toBeNull();
	});

	it('does nothing before the canvas has a size', () => {
		expect(viewportRevealing(tile, { x: -5000, y: 0, zoom: 1 }, { width: 0, height: 0 })).toBeNull();
	});
});
