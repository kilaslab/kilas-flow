import { contrast, parseCssColor } from '../../web/src/lib/design-system/color';
import { effectiveSurface } from '../helpers/focus-ring';
import { forceStates } from './force-state';
import { expect, test } from './fixtures';

// BUG-ywbvfa criterion 2: the ring reads at ≥3:1 against the surface it is
// drawn on in both themes, and around a filled control the 2px offset leaves
// the surface visible between ring and fill — the kunyit primary button's old
// 50% kunyit ring on a kunyit fill was invisible. The 4x zoomed shot is the
// evidence a reviewer looks at; the computed checks are what fails a build.

test.use({ deviceScaleFactor: 4 });

test('the primary button focus ring is a separate 2px ring on the surface', async ({ page, stable }) => {
	await stable.goto('/design/states?section=button');
	// Row "default", column "Focus visible".
	const button = page.getByTestId('matrix').locator('button[data-force~="focus-visible"]').first();
	await expect(button).toHaveText('Button');
	await forceStates(page);

	const ring = await button.evaluate((el) => {
		const style = getComputedStyle(el);
		const backgrounds: string[] = [];
		for (let node = el.parentElement; node; node = node.parentElement) backgrounds.push(getComputedStyle(node).backgroundColor);
		return {
			style: style.outlineStyle,
			width: style.outlineWidth,
			offset: style.outlineOffset,
			color: style.outlineColor,
			fill: style.backgroundColor,
			backgrounds
		};
	});
	expect(ring.style).toBe('solid');
	expect(ring.width).toBe('2px');
	expect(Number.parseFloat(ring.offset)).toBeGreaterThanOrEqual(2);
	const surface = effectiveSurface(ring.backgrounds);
	expect(contrast(parseCssColor(ring.color), surface)).toBeGreaterThanOrEqual(3);
	// The ring is not the fill: the gap would otherwise be the only separation.
	expect(ring.color).not.toBe(ring.fill);

	const box = await button.boundingBox();
	if (!box) throw new Error('focused button has no box');
	const pad = 8;
	await expect(page).toHaveScreenshot('focus-primary-button.png', {
		// The suite shoots at CSS scale; this one keeps the 4x device pixels.
		scale: 'device',
		clip: { x: box.x - pad, y: box.y - pad, width: box.width + pad * 2, height: box.height + pad * 2 }
	});
});
