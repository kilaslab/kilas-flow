import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { appTokenSheet, readAppCss } from './app-css';
import { tokenEnv, type Theme } from './css-tokens';

// FEAT-p75n7j criteria 1 and 6: every v2 token group exists in both themes,
// the light block re-declares every colour the dark block declares (so a
// [data-theme='light'] subtree inside a dark page never inherits a dark
// value), and the v1 leftovers are gone.

const sheet = appTokenSheet();
const THEMES: Theme[] = ['dark', 'light'];

const statuses = ['success', 'warning', 'danger', 'info'].flatMap((s) => [
	`--kf-${s}`,
	`--kf-${s}-fill`,
	`--kf-${s}-fill-fg`,
	`--kf-${s}-subtle`
]);

const TOKEN_GROUPS: Record<string, string[]> = {
	surfaces: ['--kf-canvas', '--kf-bg', '--kf-panel', '--kf-raised', '--kf-overlay'],
	interaction: ['--kf-hover', '--kf-pressed', '--kf-scrim', '--kf-skeleton'],
	borders: ['--kf-border-subtle', '--kf-border', '--kf-border-strong'],
	text: ['--kf-fg', '--kf-fg-muted', '--kf-fg-subtle', '--kf-fg-disabled'],
	accent: [
		'--kf-accent-src',
		'--kf-accent',
		'--kf-accent-hover',
		'--kf-accent-press',
		'--kf-accent-fg',
		'--kf-accent-text',
		'--kf-accent-subtle',
		'--kf-accent-border',
		'--kf-focus',
		'--kf-selection',
		'--kf-selection-halo',
		'--kf-selection-row'
	],
	statuses,
	categories: ['trigger', 'ai', 'flow', 'data', 'app', 'utility'].map((c) => `--kf-cat-${c}`),
	run: ['running', 'success', 'warning', 'error', 'waiting', 'disabled', 'pinned'].map((r) => `--kf-run-${r}`),
	canvas: [
		'--kf-grid-dot',
		'--kf-edge',
		'--kf-edge-hover',
		'--kf-edge-attachment',
		'--kf-edge-selected',
		'--kf-node-shadow',
		'--kf-port',
		'--kf-marquee-fill'
	],
	sticky: ['--kf-sticky-l', '--kf-sticky-c', '--kf-sticky-border-l', '--kf-sticky-border-c', '--kf-sticky-text'],
	viz: [1, 2, 3, 4, 5, 6].map((n) => `--kf-viz-${n}`),
	shadows: ['--kf-shadow-1', '--kf-shadow-2', '--kf-shadow-3'],
	radii: ['--kf-radius-xs', '--kf-radius-sm', '--kf-radius-md', '--kf-radius-full'],
	type: ['2xs', 'xs', 'sm', 'md', 'lg', 'xl'].map((s) => `--kf-text-${s}`),
	motion: [
		'--kf-dur-instant',
		'--kf-dur-fast',
		'--kf-dur-base',
		'--kf-dur-panel',
		'--kf-dur-exit',
		'--kf-dur-canvas',
		'--kf-ease-standard',
		'--kf-ease-out',
		'--kf-ease-in'
	],
	density: ['--kf-space', '--kf-control-h-compact', '--kf-control-h', '--kf-control-h-lg', '--kf-row-h', '--kf-node-size'],
	focus: ['--kf-focus-width', '--kf-focus-offset']
};

describe('token groups (FEAT-p75n7j criterion 1)', () => {
	it.each(THEMES)('defines every v2 group in %s', (theme) => {
		const env = tokenEnv(sheet, theme);
		const names = new Set(env.names());
		const missing = Object.entries(TOKEN_GROUPS).flatMap(([group, tokens]) =>
			tokens.filter((t) => !names.has(t)).map((t) => `${group}: ${t}`)
		);
		expect(missing).toEqual([]);
		// Every one of them resolves (no dangling var()).
		for (const tokens of Object.values(TOKEN_GROUPS)) for (const t of tokens) env.resolve(t);
	});

	it('seeds the accent from kunyit in each theme', () => {
		expect(sheet.dark.get('--kf-accent-src')).toBe('oklch(0.82 0.14 95)');
		expect(sheet.light.get('--kf-accent-src')).toBe('oklch(0.82 0.15 95)');
	});

	it('re-declares in the light block every token the dark block declares', () => {
		const missing = [...sheet.dark.keys()].filter((name) => !sheet.light.has(name));
		expect(missing).toEqual([]);
	});

	it('declares every alias on the boundary list so it re-resolves inside a theme or brand subtree', () => {
		for (const alias of ['--background', '--primary', '--ring', '--muted', '--accent', '--border', '--sidebar']) {
			expect(sheet.derived.has(alias), alias).toBe(true);
			expect(sheet.dark.has(alias), `${alias} also on the dark block`).toBe(false);
		}
	});
});

describe('v1 leftovers (FEAT-p75n7j criterion 6)', () => {
	const ROOTS = ['../../', '../../../../docs/src/'].map((p) => fileURLToPath(new URL(p, import.meta.url)));

	function files(dir: string): string[] {
		const out: string[] = [];
		for (const entry of readdirSync(dir)) {
			const path = join(dir, entry);
			if (entry === 'paraglide' || entry === 'node_modules') continue;
			if (statSync(path).isDirectory()) out.push(...files(path));
			else if (/\.(svelte|ts|css|md|mdx|astro)$/.test(entry) && !entry.endsWith('.test.ts')) out.push(path);
		}
		return out;
	}

	it('no source mentions the v1 node category tokens (--node-<category>)', () => {
		const hits = ROOTS.flatMap(files).filter((f) => /--node-(trigger|core|ai|data|imported)\b/.test(readFileSync(f, 'utf8')));
		expect(hits).toEqual([]);
	});

	it('app.css no longer defines the --color-surface* and --color-ink* aliases', () => {
		expect(readAppCss()).not.toMatch(/--color-(surface|ink)[\w-]*\s*:/);
	});
});
