import type { Frame, Page } from '@playwright/test';

import { contrast, parseCssColor, relativeLuminance } from '../../web/src/lib/design-system/color';
import { test, expect } from '../fixtures';
import { createEmbedSession, createWorkflow } from '../helpers/seed';

// FEAT-yrnkz0. A light-first host picks the editor's palette, font and radius
// through branding. The canvas reads the same tokens, so it switches with the
// shell, and a dialog portalled to <body> takes the palette from <html>.

async function embedFrame(page: Page): Promise<Frame> {
	await expect.poll(() => page.frames().some((f) => f.url().includes('/embed/')), { timeout: 30_000 }).toBe(true);
	return page.frames().find((f) => f.url().includes('/embed/')) as Frame;
}

/** Relative luminance of a computed CSS colour, 0 (black) to 1 (white). */
function lightness(css: string): number {
	return relativeLuminance(parseCssColor(css));
}

async function openEmbed(page: Page, server: { baseURL: string }, stub: { origin: string; url(path: string): string }, query: string) {
	const workflow = await createWorkflow(server.baseURL, {
		schemaVersion: 1,
		name: 'E2E Embed Theme',
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
			query
	);
	await expect(page.frameLocator('#e2e-frame').getByRole('button', { name: 'Add step' })).toBeVisible({ timeout: 30_000 });
	const frame = await embedFrame(page);
	await frame.addStyleTag({ content: '*,*::before,*::after{transition:none!important;animation:none!important}' });
	return frame;
}

test('a light theme, serif font and square corners reach the shell, the canvas and portalled dialogs', async ({ page, server, stub }) => {
	const frame = await openEmbed(page, server, stub, '&theme=light&font=serif&radius=0');

	const shell = frame.locator('[data-embed-shell]');
	await expect(shell).toHaveAttribute('data-theme', 'light');
	expect(await frame.evaluate(() => document.documentElement.getAttribute('data-theme'))).toBe('light');

	const look = await frame.evaluate(() => {
		const shellEl = document.querySelector('[data-embed-shell]') as HTMLElement;
		const pane = document.querySelector('.svelte-flow') as HTMLElement;
		// A step tile: a trigger's left side is a half circle by design.
		const step = [...document.querySelectorAll('.svelte-flow__node')].find((node) => node.textContent?.includes('Set One'));
		const tile = step?.querySelector('.kf-node-tile') as HTMLElement;
		return {
			shellBackground: getComputedStyle(shellEl).backgroundColor,
			shellText: getComputedStyle(shellEl).color,
			canvas: getComputedStyle(pane).backgroundColor,
			font: getComputedStyle(shellEl).fontFamily,
			tileRadius: getComputedStyle(tile).borderTopLeftRadius
		};
	});
	expect(lightness(look.shellBackground), JSON.stringify(look)).toBeGreaterThan(0.7);
	expect(lightness(look.canvas), JSON.stringify(look)).toBeGreaterThan(0.7);
	expect(contrast(parseCssColor(look.shellText), parseCssColor(look.shellBackground))).toBeGreaterThanOrEqual(4.5);
	expect(look.font).toContain('serif');
	expect(look.tileRadius).toBe('0px');

	// The version-history sheet is portalled to <body>, outside the shell.
	await page.frameLocator('#e2e-frame').getByRole('button', { name: 'History' }).click();
	const sheet = frame.locator('[data-history-panel]');
	await expect(sheet).toBeVisible();
	const sheetBackground = await sheet.evaluate((el) => getComputedStyle(el).backgroundColor);
	expect(lightness(sheetBackground), sheetBackground).toBeGreaterThan(0.7);
});

test('system follows the viewer, and an omitted theme keeps the dark default', async ({ page, server, stub }) => {
	await page.emulateMedia({ colorScheme: 'light' });
	const frame = await openEmbed(page, server, stub, '&theme=system');
	await expect(frame.locator('[data-embed-shell]')).toHaveAttribute('data-theme', 'light');
	await page.emulateMedia({ colorScheme: 'dark' });
	await expect(frame.locator('[data-embed-shell]')).toHaveAttribute('data-theme', 'dark');

	const plain = await openEmbed(page, server, stub, '');
	expect(await plain.locator('[data-embed-shell]').getAttribute('data-theme')).toBeNull();
	const background = await plain.evaluate(() => getComputedStyle(document.querySelector('[data-embed-shell]') as HTMLElement).backgroundColor);
	expect(lightness(background), background).toBeLessThan(0.2);
});
