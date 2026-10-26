import { readFileSync } from 'node:fs';
import { basename as name, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { scanSvelte, scanTs, sourceFiles, utilityOf, variantsOf, type ClassSet, type ScanResult } from './class-scan';

// The design budget (FEAT-npc3ge), as three static assertions over the source
// the browser renders:
//
// 1. Row heights — every dashboard list rides the row tokens: 40px single
//    line, 52px two-line, 36px for the dense tables.
// 2. Control heights — an interactive control carries an explicit height only
//    from {24, 28} on a fine pointer; 32 exists solely behind a
//    `pointer-coarse:` variant. Elements without an explicit height are the
//    type rhythm's business (FEAT-pqnxx4), and list-row links are rows, not
//    controls.
// 3. One content width — every dashboard page's content column is
//    --kf-page-max (70rem); the old 896/1024/full spread is gone.

const SRC = fileURLToPath(new URL('../../', import.meta.url));

const PX = { '6': 24, '7': 28, '8': 32 } as const;
const ROW_TOKENS = ['--kf-row-h', '--kf-row-h-2line', '--kf-row-h-table'];

/** An element the height budget governs: a real form control or button, or one of the control primitives. */
function isControl(set: ClassSet): boolean {
	const element = set.element;
	if (element === null) return false;
	if (set.attrs['aria-hidden'] === 'true') return false;
	// Native checkboxes and radios are indicators inside their row or label;
	// the density scale has no say over their 16px box.
	if (element.toLowerCase() === 'input' && ['checkbox', 'radio'].includes(set.attrs['type'] ?? '')) return false;
	if (/^(?:button|a|input|select|textarea|label|summary)$/i.test(element)) return true;
	// Components: only the control primitives, never the icons and indicators
	// that live inside them (their size-* is the glyph rhythm, not a control).
	return /^(?:Button|Input|Textarea)$/.test(element) || /\.(?:Trigger)$/.test(element);
}

function isRow(tokens: string[]): boolean {
	return tokens.some((token) => ROW_TOKENS.some((row) => token.includes(row)));
}

/** Fixed-height utility on a control: `h-7`, `size-6`, but not `h-[calc(...)]`
 *  (relative), an `[&_svg]` descendant selector, or an `after:`/`before:`
 *  pseudo-element's own dimension. */
function fixedHeight(tokens: string[]): { step: string; token: string } | null {
	for (const token of tokens) {
		if (token.startsWith('[') || variantsOf(token).some((variant) => variant.startsWith('['))) continue;
		const variants = variantsOf(token);
		if (variants.includes('after') || variants.includes('before')) continue;
		const utility = utilityOf(token);
		const mo = /^(h|size)-([0-9.]+|px)$/.exec(utility);
		// Above 40px a "height" is an illustrated surface (the empty-canvas
		// tile draws itself node-sized on purpose), not a density control; the
		// audit's own baseline range was 20–40px.
		if (mo && !variants.includes('file')) return { step: mo[2], token };
	}
	return null;
}

function controlHeightViolations(scans: { file: string; scan: ScanResult }[]): string[] {
	const bad: string[] = [];
	for (const { file, scan } of scans) {
		for (const set of scan.classSets) {
			if (!isControl(set) || isRow(set.tokens)) continue;
			const height = fixedHeight(set.tokens);
			if (!height) continue;
			const numeric = Number(height.step);
			if (Number.isFinite(numeric) && numeric > 10) continue; // >40px: an illustrated surface, not a control
			const px = PX[height.step as keyof typeof PX];
			if (px === undefined) {
				bad.push(`${relative(SRC, file)}:${set.line} <${set.element}> ${height.token} is not a control-height step`);
			} else if (px === 32 && !set.tokens.some((token) => variantsOf(token).includes('pointer-coarse'))) {
				bad.push(`${relative(SRC, file)}:${set.line} <${set.element}> ${height.token} is 32px without a pointer-coarse guard`);
			}
		}
	}
	return bad;
}

function hasToken(scans: { file: string; scan: ScanResult }[], token: string): boolean {
	return scans.some(({ scan }) => scan.classSets.some((set: ClassSet) => set.tokens.includes(token)));
}

function page(path: string): { file: string; scan: ScanResult } {
	const file = join(SRC, path);
	const source = readFileSync(file, 'utf8');
	return { file, scan: scanSvelte(file, source) };
}

const DASHBOARD_PAGES = [
	'routes/(dashboard)/app/workflows/+page.svelte',
	'routes/(dashboard)/executions/+page.svelte',
	'routes/(dashboard)/executions/[id]/+page.svelte',
	'routes/(dashboard)/credentials/+page.svelte',
	'routes/(dashboard)/datastores/+page.svelte',
	'routes/(dashboard)/datastores/[id]/+page.svelte',
	'routes/(dashboard)/schedules/+page.svelte',
	'routes/(dashboard)/settings/+page.svelte'
].map(page);

describe('design budget: rows, controls, one width (FEAT-npc3ge)', () => {
	const scans = sourceFiles(SRC).map((file) => {
		const source = readFileSync(file, 'utf8');
		return { file, scan: name(file).endsWith('.svelte') ? scanSvelte(file, source) : scanTs(file, source) };
	});

	it('the five dashboard lists ride the row tokens: 40 single-line, 52 two-line, 36 dense', () => {
		// 40px: workflows rows and their <li>, the datastores list rows.
		const workflows = DASHBOARD_PAGES[0];
		expect(workflows.scan.classSets.some((set) => set.element === 'a' && set.tokens.includes('h-(--kf-row-h)'))).toBe(true);
		expect(workflows.scan.classSets.some((set) => set.element === 'li' && set.tokens.includes('min-h-(--kf-row-h)'))).toBe(true);
		expect(DASHBOARD_PAGES[4].scan.classSets.some((set) => set.element === 'li' && set.tokens.includes('h-(--kf-row-h)'))).toBe(true);
		// 52px two-line: credentials, schedules, API keys.
		for (const index of [3, 6, 7]) {
			expect(DASHBOARD_PAGES[index].scan.classSets.some((set) => set.element === 'li' && set.tokens.includes('min-h-(--kf-row-h-2line)'))).toBe(true);
		}
		// 36px dense tables: the executions list and the datastore grid.
		for (const index of [1, 5]) {
			expect(DASHBOARD_PAGES[index].scan.classSets.some((set) => set.tokens.includes('[&_td]:h-(--kf-row-h-table)'))).toBe(true);
		}
		// The skeleton keeps the page's row height, so loading does not jump.
		expect(hasToken(DASHBOARD_PAGES.slice(0, 1), 'h-(--kf-row-h)')).toBe(true);
		expect(hasToken(DASHBOARD_PAGES.slice(3, 4), 'h-(--kf-row-h-2line)')).toBe(true);
		expect(hasToken(DASHBOARD_PAGES.slice(1, 2), 'h-(--kf-row-h-table)')).toBe(true);
	});

	it('one content width: every dashboard page column is --kf-page-max', () => {
		for (const { file, scan } of DASHBOARD_PAGES) {
			expect(
				scan.classSets.some((set) => set.tokens.includes('max-w-(--kf-page-max)')),
				relative(SRC, file)
			).toBe(true);
			expect(
				scan.classSets.some((set) => set.tokens.some((token) => /max-w-(?:4xl|5xl|7xl)$/.test(utilityOf(token)))),
				`${relative(SRC, file)} still carries an old page width`
			).toBe(false);
		}
	});

	it('interactive control heights are 24 or 28, and 32 only under a coarse pointer', () => {
		expect(controlHeightViolations(scans)).toEqual([]);
	});

	it('the rules can fail', () => {
		const fixture = (file: string, source: string) => ({ file: join(SRC, file), scan: scanSvelte(join(SRC, file), source) });
		expect(controlHeightViolations([fixture('x.svelte', '<button class="h-9">x</button>')])).toHaveLength(1);
		expect(controlHeightViolations([fixture('y.svelte', '<button class="h-8">x</button>')])).toHaveLength(1);
		expect(controlHeightViolations([fixture('z.svelte', '<button class="pointer-coarse:h-8 h-7">x</button>')])).toEqual([]);
		// A row link is a row, whatever its height token; a span is not a control.
		expect(controlHeightViolations([fixture('r.svelte', '<a class="h-(--kf-row-h)">row</a>')])).toEqual([]);
		expect(controlHeightViolations([fixture('s.svelte', '<span class="size-4">dot</span>')])).toEqual([]);
	});
});
