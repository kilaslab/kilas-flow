import { existsSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { defineConfig } from '@playwright/test';

import type { VisualOptions } from './visual/fixtures';

// The visual regression suite (VR-03, started by BUG-ywbvfa and FEAT-p75n7j;
// FEAT-egm8bf adds the 1280 projects and the product surfaces). It
// screenshots the /design/states harness, which only builds made with
// KILASFLOW_DESIGN_HARNESS=1 contain; global-setup.ts sets that for both
// suites, which share one build.
//
// Kept apart from playwright.config.ts on purpose: its own output directory
// and report, so scripts/skip-budget.mjs never reads it, and no retries, so a
// flaky screenshot fails instead of passing on a second try.
//
// Baselines are per platform. darwin ones are generated locally and
// gitignored; linux ones are what CI compares against. CI bootstraps itself:
// while visual/__screenshots__/linux/ does not exist the run writes the
// missing baselines (uploaded as an artefact, to be committed) and passes —
// 'changed' writes a missing snapshot without failing, whereas 'missing' writes
// it and still fails the test — while every plain expect() keeps gating; once
// they are committed, a missing or different screenshot fails.
const here = dirname(fileURLToPath(import.meta.url));
const linuxBaselines = existsSync(join(here, 'visual', '__screenshots__', 'linux'));

export default defineConfig<VisualOptions>({
	testDir: './visual',
	globalSetup: './global-setup.ts',
	snapshotPathTemplate: '{testDir}/__screenshots__/{platform}/{projectName}/{testFilePath}/{arg}{ext}',
	outputDir: './test-results-visual',
	reporter: [['line'], ['json', { outputFile: 'test-results-visual/report.json' }]],
	timeout: 60_000,
	retries: 0,
	fullyParallel: true,
	workers: process.env.CI ? 2 : undefined,
	updateSnapshots: process.env.CI ? (linuxBaselines ? 'none' : 'changed') : 'missing',
	expect: {
		timeout: 15_000,
		toHaveScreenshot: {
			animations: 'disabled',
			caret: 'hide',
			scale: 'css',
			threshold: 0.2,
			maxDiffPixelRatio: 0.001
		}
	},
	use: {
		viewport: { width: 1440, height: 900 },
		deviceScaleFactor: 1,
		locale: 'en-US',
		timezoneId: 'UTC',
		trace: 'retain-on-failure'
	},
	projects: [
		{ name: 'dark-1440', use: { kfTheme: 'dark' } },
		// The light-subtree check is a dark page by definition.
		{ name: 'light-1440', use: { kfTheme: 'light' }, testIgnore: /theme-subtree/ }
	]
});
