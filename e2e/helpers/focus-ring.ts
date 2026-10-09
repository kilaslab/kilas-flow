import type { Page } from '@playwright/test';

import { composite, contrast, parseCssColor, type Oklch, type Srgb } from '../../web/src/lib/design-system/color';

// Reads the focus indicator of whatever has focus, the way BUG-ywbvfa defines
// it: the element that DRAWS the ring, which is not always the focused one.
//   - a Svelte Flow node wrapper takes focus, its .kf-node-tile draws the ring;
//   - a `focus-delegate` control takes focus, its `focus-within-ring`
//     container draws it.
// The surface the ring sits on is the first opaque background behind it (the
// parent's for a ring drawn outside the box, the element's own for an inset
// one), with any translucent layers above it composited in. A ring that pokes
// out of an ancestor that clips its overflow is reported as clipped: the
// computed style looks perfect while the user sees a broken ring.

export interface RingReading {
	/** Short description of the focused element, for failure messages. */
	element: string;
	outlineStyle: string;
	outlineWidth: string;
	outlineColor: string;
	outlineOffset: number;
	/** A box-shadow layer that is a spread ring (0 0 0 Npx), the old shadcn focus style. */
	hasRingShadow: boolean;
	/** Contrast of the outline colour against the surface it is drawn on. */
	surfaceContrast: number;
	/** The ancestor that clips part of the ring, or null when it is whole. */
	clippedBy: string | null;
}

interface RawReading {
	element: string;
	outlineStyle: string;
	outlineWidth: string;
	outlineColor: string;
	outlineOffset: string;
	boxShadow: string;
	backgrounds: string[];
	clippedBy: string | null;
}

export async function readFocusRing(page: Page): Promise<RingReading | null> {
	const raw = await page.evaluate((): RawReading | null => {
		const active = document.activeElement as HTMLElement | null;
		if (!active || active === document.body) return null;
		let drawer: HTMLElement = active;
		if (active.matches('.svelte-flow__node')) drawer = active.querySelector<HTMLElement>('.kf-node-tile') ?? active;
		else if (active.classList.contains('focus-delegate'))
			drawer = active.closest<HTMLElement>('.focus-within-ring, .focus-within-ring-inset') ?? active;
		const style = getComputedStyle(drawer);
		const describe = (el: Element) =>
			`<${el.tagName.toLowerCase()}${el.id ? `#${el.id}` : ''} class="${(el.getAttribute('class') ?? '').slice(0, 60)}">`;
		// How far the ring reaches outside the border box, and whether an
		// ancestor that clips overflow cuts into that reach.
		const reach = Math.max(0, (Number.parseFloat(style.outlineOffset) || 0) + (Number.parseFloat(style.outlineWidth) || 0));
		let clippedBy: string | null = null;
		if (style.outlineStyle !== 'none' && reach > 0) {
			const box = drawer.getBoundingClientRect();
			const ring = { left: box.left - reach, top: box.top - reach, right: box.right + reach, bottom: box.bottom + reach };
			for (let node = drawer.parentElement; node && node !== document.documentElement; node = node.parentElement) {
				const cs = getComputedStyle(node);
				const clipX = cs.overflowX !== 'visible';
				const clipY = cs.overflowY !== 'visible';
				if (!clipX && !clipY) continue;
				const b = node.getBoundingClientRect();
				const left = b.left + Number.parseFloat(cs.borderLeftWidth);
				const right = b.right - Number.parseFloat(cs.borderRightWidth);
				const top = b.top + Number.parseFloat(cs.borderTopWidth);
				const bottom = b.bottom - Number.parseFloat(cs.borderBottomWidth);
				const tolerance = 0.5;
				if (
					(clipX && (ring.left < left - tolerance || ring.right > right + tolerance)) ||
					(clipY && (ring.top < top - tolerance || ring.bottom > bottom + tolerance))
				) {
					clippedBy = describe(node);
					break;
				}
			}
		}
		const backgrounds: string[] = [];
		let node: HTMLElement | null = Number.parseFloat(style.outlineOffset) > 0 ? drawer.parentElement : drawer;
		while (node) {
			backgrounds.push(getComputedStyle(node).backgroundColor);
			node = node.parentElement;
		}
		const label = active.getAttribute('aria-label') ?? active.textContent?.trim().slice(0, 30) ?? '';
		return {
			element: `<${active.tagName.toLowerCase()}${active.id ? `#${active.id}` : ''}> ${label}`,
			outlineStyle: style.outlineStyle,
			outlineWidth: style.outlineWidth,
			outlineColor: style.outlineColor,
			outlineOffset: style.outlineOffset,
			boxShadow: style.boxShadow,
			backgrounds,
			clippedBy
		};
	});
	if (!raw) return null;
	return {
		element: raw.element,
		outlineStyle: raw.outlineStyle,
		outlineWidth: raw.outlineWidth,
		outlineColor: raw.outlineColor,
		outlineOffset: Number.parseFloat(raw.outlineOffset),
		hasRingShadow: /\b0px 0px 0px [1-9]/.test(raw.boxShadow),
		surfaceContrast: contrast(parseCssColor(raw.outlineColor), effectiveSurface(raw.backgrounds)),
		clippedBy: raw.clippedBy
	};
}

/** Composites the background stack (innermost first) down to the first opaque layer. */
export function effectiveSurface(backgrounds: string[]): Srgb | Oklch {
	const layers = backgrounds.map((value) => parseCssColor(value));
	const opaque = layers.findIndex((layer) => layer.alpha >= 0.999);
	// Nothing opaque up to <html>: the canvas behind the page is white.
	let surface: Srgb | Oklch = opaque === -1 ? parseCssColor('#ffffff') : layers[opaque];
	const above = opaque === -1 ? layers : layers.slice(0, opaque);
	for (const layer of above.reverse()) if (layer.alpha > 0) surface = composite(layer, surface);
	return surface;
}

/** The style a reading contributes to the "one combination" count. */
export function ringSignature(reading: RingReading): string {
	return `${reading.outlineStyle}|${reading.outlineWidth}|${reading.outlineColor}|${reading.hasRingShadow ? 'ring-shadow' : 'no-ring-shadow'}`;
}
