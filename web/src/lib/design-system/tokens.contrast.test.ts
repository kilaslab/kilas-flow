import { describe, expect, it } from 'vitest';

import { appTokenSheet } from './app-css';
import { composite, contrast, deltaEOK } from './color';
import { ALIAS_PAIRS, HOST_ACCENTS, STATE_PAIRS, TOKEN_PAIRS, type ContrastPair } from './contrast-pairs';
import { tokenEnv, type Theme, type TokenEnv } from './css-tokens';

// FEAT-p75n7j criterion 2: every token pair in contrast-pairs.ts clears its
// minimum in both themes, measured on the values app.css actually declares.
// A failure prints the whole table so one run shows every pair that moved.

const sheet = appTokenSheet();
const THEMES: Theme[] = ['dark', 'light'];

interface Measured {
	theme: Theme;
	pair: ContrastPair;
	ratio: number;
}

function measure(env: TokenEnv, theme: Theme, pair: ContrastPair): Measured {
	const backdrop = env.color(pair.over ?? '--kf-raised');
	// A translucent background (the *-subtle tokens) is painted over its
	// backdrop first; so is a translucent foreground, over that result.
	const bg = composite(env.color(pair.bg), backdrop);
	const fg = composite(env.color(pair.fg), bg);
	return { theme, pair, ratio: contrast(fg, bg) };
}

function failures(rows: Measured[]): string {
	const bad = rows.filter((r) => r.ratio < r.pair.min);
	if (bad.length === 0) return '';
	return [
		'theme | fg | bg | ratio | min',
		...bad.map((r) => `${r.theme} | ${r.pair.fg} | ${r.pair.bg}${r.pair.over ? ` over ${r.pair.over}` : ''} | ${r.ratio.toFixed(2)} | ${r.pair.min}`)
	].join('\n');
}

describe('token contrast (FEAT-p75n7j criterion 2)', () => {
	it('keeps the rebuilt pair list at 44 pairs per theme (add pairs, never drop them)', () => {
		expect(TOKEN_PAIRS).toHaveLength(44);
		expect(TOKEN_PAIRS.filter((p) => p.kind === 'text')).toHaveLength(25);
	});

	it.each(THEMES)('every token pair clears its minimum in %s', (theme) => {
		const env = tokenEnv(sheet, theme);
		const rows = TOKEN_PAIRS.map((pair) => measure(env, theme, pair));
		expect(failures(rows)).toBe('');
	});

	it.each(THEMES)('hover and pressed stay separable from every surface and from each other in %s', (theme) => {
		const env = tokenEnv(sheet, theme);
		const rows = STATE_PAIRS.map((pair) => measure(env, theme, pair));
		expect(failures(rows)).toBe('');
	});

	it.each(THEMES)('the shadcn aliases existing markup uses stay legible in %s', (theme) => {
		const env = tokenEnv(sheet, theme);
		const rows = ALIAS_PAIRS.map((pair) => measure(env, theme, pair));
		expect(failures(rows)).toBe('');
	});
});

describe('host accents (FEAT-p75n7j criterion 4)', () => {
	it.each(THEMES.flatMap((theme) => HOST_ACCENTS.map((accent) => [theme, accent] as const)))(
		'%s: the derived accent-fg reads on a %s fill and on its hover and press steps',
		(theme, accent) => {
			const env = tokenEnv(sheet, theme, { '--kf-accent-src': accent });
			const rows = ['--kf-accent', '--kf-accent-hover', '--kf-accent-press'].map((bg) =>
				measure(env, theme, { fg: '--kf-accent-fg', bg, min: 4.5, kind: 'text' })
			);
			expect(failures(rows.slice(0, 1))).toBe('');
			// The fill itself is the criterion; hover/press are reported, and they
			// must at least clear the large-text minimum.
			for (const row of rows) expect(row.ratio).toBeGreaterThanOrEqual(3);
		}
	);

	it('an override reaches every accent step, not just the fill', () => {
		const base = tokenEnv(sheet, 'dark');
		const brand = tokenEnv(sheet, 'dark', { '--kf-accent-src': '#0ea5e9' });
		for (const name of [
			'--kf-accent',
			'--kf-accent-hover',
			'--kf-accent-press',
			'--kf-accent-text',
			'--kf-focus',
			'--kf-selection',
			'--kf-selection-row',
			'--primary',
			'--ring'
		]) {
			expect(deltaEOK(base.color(name), brand.color(name)), name).toBeGreaterThan(0.05);
		}
	});
});

describe('relative-colour fallback (@supports not)', () => {
	it.each(THEMES)('declares a static value for every relative-colour token in %s', (theme) => {
		const env = tokenEnv(sheet, theme);
		const relative = env.names().filter((name) => /oklch\(\s*from\b/.test(env.raw(name)));
		expect(relative.length).toBeGreaterThan(0);
		const fallback = sheet.fallback[theme];
		expect(relative.filter((name) => !fallback.has(name))).toEqual([]);
	});

	it.each(THEMES)('each %s fallback lies within ΔE(OK) 0.01 of the derived default', (theme) => {
		const env = tokenEnv(sheet, theme);
		const fallbackEnv = tokenEnv(sheet, theme, Object.fromEntries(sheet.fallback[theme]));
		const off = [...sheet.fallback[theme].keys()]
			.map((name) => ({ name, d: deltaEOK(env.color(name), fallbackEnv.color(name)) }))
			.filter((row) => row.d >= 0.01)
			.map((row) => `${row.name} ΔE ${row.d.toFixed(4)}`);
		expect(off).toEqual([]);
	});
});
