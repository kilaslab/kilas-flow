import type { Frame, Page } from '@playwright/test';

import { contrast, parseCssColor, toHex } from '../../web/src/lib/design-system/color';
import { test, expect } from '../fixtures';
import { createEmbedSession, createWorkflow } from '../helpers/seed';

// FEAT-p75n7j criterion 4. A host re-accents the embedded editor with ONE
// value: branding.accent lands as --kf-accent-src on the [data-kf-brand] shell,
// and every accent use inside it re-derives from that. Each probe below is read
// from the live frame and compared with the same token read outside the shell,
// where the default kunyit still applies, so a step that stayed on kunyit (an
// alias resolved at :root, a hard-coded colour) fails by name.

const HOST_ACCENT = '#0ea5e9';

async function embedFrame(page: Page): Promise<Frame> {
	await expect.poll(() => page.frames().some((f) => f.url().includes('/embed/')), { timeout: 30_000 }).toBe(true);
	return page.frames().find((f) => f.url().includes('/embed/')) as Frame;
}

/** Reads `property` of a throwaway element styled with `style`, inside or outside the brand shell. */
async function probe(frame: Frame, where: 'brand' | 'outside', style: string, property: string): Promise<string> {
	return frame.evaluate(
		([where, style, property]) => {
			const parent = where === 'brand' ? document.querySelector('[data-kf-brand]') : document.body;
			if (!parent) throw new Error('no [data-kf-brand] shell in the frame');
			const el = document.createElement('div');
			el.setAttribute('style', style);
			parent.appendChild(el);
			const value = getComputedStyle(el).getPropertyValue(property);
			el.remove();
			return value;
		},
		[where, style, property] as const
	);
}

test('one host accent recolours every accent use inside the embed shell', async ({ page, server, stub }) => {
	const workflow = await createWorkflow(server.baseURL, {
		schemaVersion: 1,
		name: 'E2E Embed Branding',
		nodes: [
			{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
			{ id: 'set1', name: 'Set One', type: 'kilasflow.set', typeVersion: 1, position: { x: 240, y: 0 }, parameters: { assignments: { v: 'x' } } }
		],
		connections: [{ id: 'c1', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'set1', port: 'main' } }],
		settings: {}
	});
	const session = await createEmbedSession(server.baseURL, { workflowId: workflow.id, origin: stub.origin });

	await page.goto(
		`${stub.url('/host')}?server=${encodeURIComponent(server.baseURL)}` +
			`&workflow=${encodeURIComponent(workflow.id)}` +
			`&token=${encodeURIComponent(session.token)}` +
			`&scopes=${encodeURIComponent(session.scopes.join(','))}` +
			`&accent=${encodeURIComponent(HOST_ACCENT)}`
	);
	const frameLocator = page.frameLocator('#e2e-frame');
	const addStep = frameLocator.getByRole('button', { name: 'Add step' });
	await expect(addStep).toBeVisible({ timeout: 30_000 });
	const frame = await embedFrame(page);
	// Colour transitions would make a read mid-animation; the values, not the
	// motion, are under test.
	await frame.addStyleTag({ content: '*,*::before,*::after{transition:none!important;animation:none!important}' });

	// The shell carries the host's value and nothing else re-accents it.
	expect(await frame.locator('[data-kf-brand]').getAttribute('style')).toBe(`--kf-accent-src: ${HOST_ACCENT};`);
	expect(toHex(parseCssColor(await probe(frame, 'outside', 'background-color: var(--kf-accent)', 'background-color')))).toBe('#e1c34b');

	const bg = () => addStep.evaluate((el) => getComputedStyle(el).backgroundColor);
	const brand: Record<string, string> = {};
	brand.buttonRest = await bg();

	// Keyboard focus: the ring is drawn on a Tab stop, so focus arrives by Tab.
	await addStep.focus();
	await page.keyboard.press('Tab');
	const ring = await frame.evaluate(() => {
		const el = document.activeElement as HTMLElement;
		const style = getComputedStyle(el);
		return { matches: el.matches(':focus-visible'), style: style.outlineStyle, color: style.outlineColor, html: el.outerHTML.slice(0, 300) };
	});
	expect(ring.matches).toBe(true);
	expect(ring.style, ring.html).toBe('solid');
	brand.focusRing = ring.color;

	await frameLocator.locator('.svelte-flow__node').filter({ hasText: 'Set One' }).first().click();
	const tile = frameLocator.locator('.svelte-flow__node.selected .kf-node-tile');
	await expect(tile).toBeVisible();
	brand.nodeSelection = await tile.evaluate((el) => getComputedStyle(el).borderTopColor);

	// Hover and press last: releasing the press may still count as a click and
	// open the node picker, which would steal focus from the probes above.
	await addStep.hover();
	brand.buttonHover = await bg();
	await page.mouse.down();
	brand.buttonPressed = await bg();
	await page.mouse.move(0, 0);
	await page.mouse.up();

	brand.link = await probe(frame, 'brand', 'color: var(--kf-accent-text)', 'color');
	brand.rowSelection = await probe(frame, 'brand', 'background-color: var(--kf-selection-row)', 'background-color');

	const kunyit: Record<string, string> = {
		buttonRest: await probe(frame, 'outside', 'background-color: var(--kf-accent)', 'background-color'),
		buttonHover: await probe(frame, 'outside', 'background-color: var(--kf-accent-hover)', 'background-color'),
		buttonPressed: await probe(frame, 'outside', 'background-color: var(--kf-accent-press)', 'background-color'),
		focusRing: await probe(frame, 'outside', 'outline: 2px solid var(--kf-focus)', 'outline-color'),
		nodeSelection: await probe(frame, 'outside', 'border: 1px solid var(--kf-selection)', 'border-top-color'),
		link: await probe(frame, 'outside', 'color: var(--kf-accent-text)', 'color'),
		rowSelection: await probe(frame, 'outside', 'background-color: var(--kf-selection-row)', 'background-color')
	};

	// The button's states are three different shades, all from the host accent.
	expect(toHex(parseCssColor(brand.buttonRest))).toBe(HOST_ACCENT);
	expect(new Set([brand.buttonRest, brand.buttonHover, brand.buttonPressed]).size).toBe(3);
	const stillKunyit = Object.keys(kunyit).filter((key) => toHex(parseCssColor(brand[key])) === toHex(parseCssColor(kunyit[key])));
	expect(stillKunyit, JSON.stringify({ brand, kunyit }, null, 2)).toEqual([]);

	// Cross-check that Chrome resolves the relative-colour maths the unit
	// tests model (l in 0..1, the ink/white flip at L 0.60).
	const fg = parseCssColor(await probe(frame, 'brand', 'color: var(--kf-accent-fg)', 'color'));
	const fill = parseCssColor(await probe(frame, 'brand', 'background-color: var(--kf-accent)', 'background-color'));
	expect(contrast(fg, fill)).toBeGreaterThanOrEqual(4.5);
	const label = parseCssColor(await addStep.evaluate((el) => getComputedStyle(el).color));
	expect(toHex(label)).toBe(toHex(fg));
});
