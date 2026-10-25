import { existsSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { test as base, expect, type Page } from '@playwright/test';

import { startServer, type E2EServer } from '../helpers/server';

export type KfTheme = 'dark' | 'light';

export interface VisualOptions {
	/** The theme forced on <html> before the app hydrates. */
	kfTheme: KfTheme;
}

interface VisualFixtures {
	stable: { goto: (path: string) => Promise<void> };
	baselinesCommitted: void;
}

interface VisualWorkerFixtures {
	server: E2EServer;
}

/**
 * Forces `theme` on <html> before any app script runs. The init script runs
 * before the parser has created <html>, so it waits for the element. Light is
 * reachable only this way until the theme toggle (FEAT-a3dwj2) lands.
 */
export async function forceTheme(page: Page, theme: KfTheme): Promise<void> {
	await page.addInitScript((value) => {
		const apply = () => {
			if (!document.documentElement) return false;
			document.documentElement.setAttribute('data-theme', value);
			return true;
		};
		if (apply()) return;
		const observer = new MutationObserver(() => {
			if (apply()) observer.disconnect();
		});
		observer.observe(document, { childList: true, subtree: true });
	}, theme);
}

// What makes a screenshot deterministic: the theme set before hydration, the
// network idle, the web font actually loaded (a fallback face shifts every
// glyph), and no transition, animation or caret left to catch mid-frame.
const FREEZE = '*,*::before,*::after{transition:none!important;animation:none!important;caret-color:transparent!important}';

// This platform's baseline directory, snapshotPathTemplate's {platform} root.
const platformBaselines = join(dirname(fileURLToPath(import.meta.url)), '__screenshots__', process.platform);

export const test = base.extend<VisualFixtures & VisualOptions, VisualWorkerFixtures>({
	kfTheme: ['dark', { option: true }],
	// Baselines are per platform, and a platform whose baseline directory is
	// not committed has nothing to compare against. CI skips rather than
	// record: a screenshot a runner writes is a baseline only for that
	// runner's font rendering. Local runs keep recording
	// (updateSnapshots: 'missing' in playwright.visual.config.ts), which is
	// how a maintainer produces the directory this gate checks — darwin
	// stays gitignored, linux is committed. An auto fixture rather than a
	// beforeEach: hooks registered in an imported module attach to whichever
	// suite Playwright happens to be loading, and a fixture is the one
	// mechanism that runs for every test using this builder, deterministically.
	baselinesCommitted: [
		async ({}, use) => {
			base.skip(
				process.env.CI !== undefined && !existsSync(platformBaselines),
				`no ${process.platform} visual baselines are committed (e2e/visual/__screenshots__/${process.platform}/ is absent); record them locally with 'cd e2e && pnpm test:visual' and commit them`
			);
			await use();
		},
		{ auto: true }
	],
	// The harness only reads, so one server per worker is enough.
	server: [
		async ({}, use) => {
			const server = await startServer();
			await use(server);
			await server.close();
		},
		{ scope: 'worker' }
	],
	stable: async ({ page, server, kfTheme }, use) => {
		await forceTheme(page, kfTheme);
		await use({
			goto: async (path: string) => {
				await page.goto(`${server.baseURL}${path}`, { waitUntil: 'networkidle' });
				await page.evaluate(() => document.fonts.ready);
				expect(await page.evaluate(() => document.fonts.check('13px "Geist Variable"')), 'Geist must be loaded').toBe(true);
				expect(await page.evaluate(() => document.documentElement.getAttribute('data-theme'))).toBe(kfTheme);
				await page.addStyleTag({ content: FREEZE });
				await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
			}
		});
	}
});

export { expect };
