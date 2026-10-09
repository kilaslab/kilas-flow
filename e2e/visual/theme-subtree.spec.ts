import { parseCssColor } from '../../web/src/lib/design-system/color';
import { forceStates } from './force-state';
import { expect, test } from './fixtures';

// FEAT-p75n7j criterion 5: a [data-theme='light'] subtree inside the dark page
// renders light, including the Svelte Flow canvas and its edges, because the
// aliases and the Svelte Flow variables re-resolve at the theme boundary. The
// brand subtree next to it re-accents with one --kf-accent-src. Dark project
// only (playwright.visual.config.ts): the point is a light island in a dark
// page.

test('a light subtree in the dark page renders light surfaces, canvas and edges', async ({ page, stable }) => {
	await stable.goto('/design/states?section=theme-subtree');
	const subtree = page.getByTestId('light-subtree');
	await expect(subtree.locator('.svelte-flow__node')).toHaveCount(2);
	await expect(subtree.locator('.svelte-flow__edge-path')).toHaveCount(1);

	const read = await page.evaluate(() => {
		const light = document.querySelector('[data-testid="light-subtree"]') as HTMLElement;
		const flow = light.querySelector('.svelte-flow') as HTMLElement;
		const edge = light.querySelector('.svelte-flow__edge-path') as SVGPathElement;
		const probe = document.createElement('div');
		probe.style.color = 'var(--kf-edge)';
		light.appendChild(probe);
		const lightEdge = getComputedStyle(probe).color;
		probe.remove();
		return {
			canvas: getComputedStyle(flow).backgroundColor,
			surface: getComputedStyle(light).backgroundColor,
			edgeStroke: getComputedStyle(edge).stroke,
			lightEdge,
			body: getComputedStyle(document.body).backgroundColor
		};
	});

	// Light --kf-canvas is oklch(0.972 0.003 235); light --kf-bg 0.985.
	expect(parseCssColor(read.canvas).l).toBeCloseTo(0.972, 2);
	expect(parseCssColor(read.surface).l).toBeCloseTo(0.985, 2);
	// The edge stroke is the LIGHT --kf-edge (0.62), not the dark one (0.52).
	expect(parseCssColor(read.edgeStroke).l).toBeCloseTo(parseCssColor(read.lightEdge).l, 3);
	expect(parseCssColor(read.edgeStroke).l).toBeCloseTo(0.62, 2);
	// …and the page around it is still dark.
	expect(parseCssColor(read.body).l).toBeCloseTo(0.172, 2);

	await forceStates(page);
	await expect(page).toHaveScreenshot('theme-subtree.png');
});
