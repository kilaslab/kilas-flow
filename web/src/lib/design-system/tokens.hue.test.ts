import { readFileSync } from 'node:fs';
import { basename as name, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { appTokenSheet } from './app-css';
import { scanSvelte, scanTs, sourceFiles, utilityOf, type ScanResult } from './class-scan';
import { hueDistance } from './color';
import { tokenEnv, type Theme } from './css-tokens';

// FEAT-p75n7j criterion 3: brand and state never share a hue, the accent
// stays out of the competitor exclusion zones measured in audit §6.1, the
// neutrals are cool slate, and warning is never a button fill and always
// travels with a triangle glyph.

const sheet = appTokenSheet();
const THEMES: Theme[] = ['dark', 'light'];

const STATUS_MIN_DISTANCE: Record<string, number> = {
	success: 50,
	danger: 60,
	info: 90,
	warning: 30
};

// Audit §6.1: qonflo.ai (292, 321, 351, 203) and n8n (10, 30, 44, 55).
const COMPETITOR_HUES = [292, 321, 351, 203, 10, 30, 44, 55];

const NEUTRAL_TOKENS = [
	'--kf-canvas',
	'--kf-bg',
	'--kf-panel',
	'--kf-raised',
	'--kf-overlay',
	'--kf-hover',
	'--kf-pressed',
	'--kf-skeleton',
	'--kf-border-subtle',
	'--kf-border',
	'--kf-border-strong',
	'--kf-fg',
	'--kf-fg-muted',
	'--kf-fg-subtle',
	'--kf-fg-disabled',
	'--kf-grid-dot',
	'--kf-edge',
	'--kf-edge-hover',
	'--kf-edge-attachment'
];

describe('hue separation (FEAT-p75n7j criterion 3)', () => {
	it.each(THEMES)('keeps every status hue clear of the accent in %s', (theme) => {
		const env = tokenEnv(sheet, theme);
		const accent = env.color('--kf-accent-src').h;
		expect(accent).toBeCloseTo(95, 6);
		const short: string[] = [];
		for (const [status, min] of Object.entries(STATUS_MIN_DISTANCE)) {
			for (const name of [`--kf-${status}`, `--kf-${status}-fill`]) {
				const d = hueDistance(accent, env.color(name).h);
				if (d < min) short.push(`${name}: Δh ${d.toFixed(1)} < ${min}`);
			}
		}
		expect(short).toEqual([]);
	});

	it.each(THEMES)('keeps the accent ≥40° from every competitor hue in %s', (theme) => {
		const accent = tokenEnv(sheet, theme).color('--kf-accent-src').h;
		for (const hue of COMPETITOR_HUES) {
			expect(hueDistance(accent, hue), `competitor hue ${hue}`).toBeGreaterThanOrEqual(40);
		}
	});

	it.each(THEMES)('builds the %s neutrals on cool slate h 235 at C ≤ 0.012', (theme) => {
		const env = tokenEnv(sheet, theme);
		expect(env.resolve('--kf-neutral-h')).toBe('235');
		const off: string[] = [];
		for (const name of NEUTRAL_TOKENS) {
			const c = env.color(name);
			if (c.c > 0.012 + 1e-9) off.push(`${name}: C ${c.c.toFixed(4)}`);
			if (c.c > 0.0001 && c.h >= 270) off.push(`${name}: h ${c.h.toFixed(1)} is in the purple zone`);
		}
		expect(off).toEqual([]);
	});

	// FEAT-xj5tv6: category colour is glyph-and-chip only, and it stays clear
	// of the competitor exclusion zones (audit §6.1) in both themes — nothing
	// a node is categorised with may sit in the red/orange band n8n owns
	// (0–60) or the violet/magenta band qonflo.ai owns (270–360).
	it.each(THEMES)('keeps every --kf-cat-* hue out of 0–60 and 270–360 in %s', (theme) => {
		const env = tokenEnv(sheet, theme);
		const off: string[] = [];
		for (const category of ['trigger', 'ai', 'flow', 'data', 'app', 'utility']) {
			const { c, h } = env.color(`--kf-cat-${category}`);
			// The neutral categories carry a hue only nominally (C ≤ 0.012);
			// what is checked is that no *chromatic* category enters a zone.
			if (c <= 0.012) continue;
			if (h < 60 || h >= 270) off.push(`--kf-cat-${category}: h ${h.toFixed(1)}`);
		}
		expect(off).toEqual([]);
	});

	it.each(THEMES)('caps chroma on every oklch() literal written on the neutral hue in %s', (theme) => {
		const env = tokenEnv(sheet, theme);
		const neutralLiteral = /oklch\(\s*([\d.]+)\s+([\d.]+)\s+(?:var\(--kf-neutral-h\)|235)\b/g;
		const off: string[] = [];
		for (const name of env.names()) {
			for (const match of env.raw(name).matchAll(neutralLiteral)) {
				if (Number(match[2]) > 0.012) off.push(`${name}: ${match[0]}`);
			}
		}
		expect(off).toEqual([]);
	});
});

// ---------------------------------------------------------------------------
// warning is never a button fill and always travels with a triangle glyph

const SRC = fileURLToPath(new URL('../../', import.meta.url));

/** `bg-warning` (no opacity modifier) or anything naming --kf-warning-fill (not -fg). */
const isSolidWarning = (token: string) => utilityOf(token) === 'bg-warning' || /--kf-warning-fill(?!-)/.test(token);

interface Scanned {
	file: string;
	source: string;
	scan: ScanResult;
}

function warningOnButtons(scans: Scanned[]): string[] {
	const bad: string[] = [];
	for (const { file, scan } of scans) {
		const isButtonPrimitive = /components\/ui\/button\//.test(file);
		for (const set of scan.classSets) {
			if (!set.tokens.some(isSolidWarning)) continue;
			if (isButtonPrimitive || set.element === 'button' || set.element === 'Button') {
				bad.push(`${relative(SRC, file)}:${set.line} <${set.element ?? 'buttonVariants'}>`);
			}
		}
	}
	return bad;
}

function warningWithoutTriangle(scans: Scanned[]): string[] {
	const bad: string[] = [];
	for (const { file, source, scan } of scans) {
		// A decorative aria-hidden dot sits beside the text that carries the
		// meaning; it is not a warning rendered on its own.
		const fills = scan.classSets.filter((set) => set.tokens.some(isSolidWarning) && set.attrs['aria-hidden'] !== 'true');
		if (fills.length === 0) continue;
		if (!/import\s+TriangleAlert\w*\s+from\s+'@lucide\/svelte\/icons\/triangle-alert'/.test(source)) {
			bad.push(`${relative(SRC, file)}:${fills[0].line}`);
		}
	}
	return bad;
}

const fixture = (name: string, source: string): Scanned => {
	const file = join(SRC, name);
	return { file, source, scan: name.endsWith('.svelte') ? scanSvelte(file, source) : scanTs(file, source) };
};

describe('warning rendering (FEAT-p75n7j criterion 3)', () => {
	const scans: Scanned[] = sourceFiles(SRC).map((file) => {
		const source = readFileSync(file, 'utf8');
		return { file, source, scan: name(file).endsWith('.svelte') ? scanSvelte(file, source) : scanTs(file, source) };
	});

	it('scans the sources it claims to scan, and its rules can fail', () => {
		expect(scans.some((s) => s.file.endsWith('canvas-node.svelte'))).toBe(true);
		expect(scans.some((s) => s.file.endsWith('button.svelte'))).toBe(true);
		expect(scans.flatMap((s) => s.scan.classSets).some((set) => set.element === 'Button')).toBe(true);

		const onButton = fixture('x.svelte', '<Button class="h-7 bg-warning">Retry</Button>');
		const onSpan = fixture('y.svelte', '<span class="rounded {lossy ? \'bg-warning\' : \'\'}">3</span>');
		const dot = fixture('z.svelte', '<span aria-hidden="true" class="size-1.5 rounded-full bg-warning"></span>');
		const tinted = fixture('t.svelte', '<p class="bg-warning/10 text-warning">note</p>');
		const variants = fixture('components/ui/button/v.ts', "tv({ variants: { warn: 'bg-(--kf-warning-fill)' } })");
		expect(warningOnButtons([onButton, variants])).toHaveLength(2);
		expect(warningOnButtons([onSpan, dot, tinted])).toEqual([]);
		expect(warningWithoutTriangle([onSpan])).toHaveLength(1);
		expect(warningWithoutTriangle([dot, tinted])).toEqual([]);
		const withGlyph = fixture('w.svelte', "<script>import TriangleAlert from '@lucide/svelte/icons/triangle-alert';</script><span class=\"bg-warning\"><TriangleAlert /></span>");
		expect(warningWithoutTriangle([withGlyph])).toEqual([]);
	});

	it('never uses a solid warning fill on a button or in the button variants', () => {
		expect(warningOnButtons(scans)).toEqual([]);
	});

	it('pairs every solid warning fill that carries meaning with a TriangleAlert glyph', () => {
		expect(warningWithoutTriangle(scans)).toEqual([]);
	});
});
