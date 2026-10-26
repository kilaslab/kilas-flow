import { readFileSync } from 'node:fs';
import { basename as name, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { scanSvelte, scanTs, sourceFiles, utilityOf, type ScanResult } from './class-scan';

// The spacing lint (FEAT-npc3ge): every gap, padding and margin sits on the
// 4px grid, and none of them is an arbitrary pixel value.
//
// The grid means whole Tailwind steps only — 0, 1, 2, 3, 4 … map to 0, 4, 8,
// 12, 16 … px. The half steps (0.5/1.5/2.5/3.5 = 2/6/10/14 px), the `px`
// unit (1px) and bracketed values (`p-[3px]`, `mt-[0.3125rem]`) are off it;
// `auto` is layout, not spacing. Enforced as part of lint:tokens.

const SRC = fileURLToPath(new URL('../../', import.meta.url));

const SPACING = /^-?(?:p[xytblrse]?|m[xytblrse]?|gap(?:-[xy])?|space-[xy])-(.+)$/;

interface Violation {
	where: string;
	token: string;
	why: 'arbitrary' | 'off-grid';
}

const fixture = (file: string, source: string): { file: string; scan: ScanResult } => {
	const path = join(SRC, file);
	return { file: path, scan: file.endsWith('.svelte') ? scanSvelte(path, source) : scanTs(path, source) };
};

function violations(scans: { file: string; scan: ScanResult }[]): Violation[] {
	const bad: Violation[] = [];
	for (const { file, scan } of scans) {
		for (const set of scan.classSets) {
			for (const token of set.tokens) {
				const step = SPACING.exec(utilityOf(token))?.[1];
				if (step === undefined) continue;
				if (step.startsWith('[')) bad.push({ where: `${relative(SRC, file)}:${set.line}`, token, why: 'arbitrary' });
				else if (step !== 'auto' && !/^\d+$/.test(step)) bad.push({ where: `${relative(SRC, file)}:${set.line}`, token, why: 'off-grid' });
			}
		}
	}
	return bad;
}

describe('spacing on the 4px grid (FEAT-npc3ge)', () => {
	const scans = sourceFiles(SRC).map((file) => {
		const source = readFileSync(file, 'utf8');
		return { file, scan: name(file).endsWith('.svelte') ? scanSvelte(file, source) : scanTs(file, source) };
	});

	it('scans the sources it claims to scan, and its rules can fail', () => {
		expect(scans.some((s) => s.file.endsWith('app.css'))).toBe(false);
		expect(scans.flatMap((s) => s.scan.classSets).length).toBeGreaterThan(0);

		const half = fixture('a.svelte', '<p class="py-1.5 px-2.5">x</p>');
		const unit = fixture('b.svelte', '<p class="mt-px mb-0.5">x</p>');
		const arbitrary = fixture('c.svelte', '<p class="p-[3px]">x</p>');
		const variant = fixture('d.svelte', '<p class="hover:gap-1.5">x</p>');
		const negative = fixture('e.svelte', '<p class="-mt-2.5">x</p>');
		expect(violations([half, unit, arbitrary, variant, negative])).toHaveLength(7);

		const onGrid = fixture('f.svelte', '<p class="py-1 px-2 mt-2 -mb-4 mx-auto gap-3">x</p>');
		expect(violations([onGrid])).toEqual([]);
	});

	it('reports no arbitrary spacing classes', () => {
		expect(violations(scans).filter((v) => v.why === 'arbitrary')).toEqual([]);
	});

	it('keeps every gap and padding on the 4px grid', () => {
		expect(violations(scans).filter((v) => v.why === 'off-grid')).toEqual([]);
	});
});
