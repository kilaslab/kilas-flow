import { readFileSync } from 'node:fs';
import { basename as name, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { scanSvelte, scanTs, sourceFiles, utilityOf, type ScanResult } from './class-scan';

// FEAT-r8ph93: the accent and the raw Tailwind palette are gone from source.
//
// - No raw palette class may remain (a `red-600`/`violet-500`-style utility
//   with a colour-step suffix): every colour a status or surface needs comes
//   from a token, in both themes.
// - `text-primary` and `text-accent` are refused as utilities: the kunyit fill
//   reads 1.7:1 as text in light, and shadcn's --accent is a hover surface,
//   never a text colour (BUG-namghh). The fill pairs (`bg-primary` with its
//   `--primary-foreground`) stay legal.
// - `border-primary` (any opacity) is refused: line-weight accent belongs on
//   --kf-selection, which clears the 3:1 a UI boundary needs in both themes.
//
// The fixture tokens are assembled from parts so this file itself carries no
// whole palette class for a plain grep to find.

const SRC = fileURLToPath(new URL('../../', import.meta.url));

const PALETTE_CLASS =
	/(?:bg|text|border|ring|outline|decoration|divide|from|to|via|fill|stroke|caret|accent|shadow)-(?:red|green|emerald|violet|amber|neutral|blue|sky|indigo|yellow|orange|pink|fuchsia|purple|rose|teal|cyan|lime|slate|gray|zinc|stone)-\d/;

const FORBIDDEN_UTILITY = /^(?:text-primary|text-accent|border-primary(?:\/\d+)?)$/;

interface Scanned {
	file: string;
	scan: ScanResult;
}

interface Violation {
	where: string;
	token: string;
	palette: boolean;
}

const fixture = (file: string, source: string): Scanned => {
	const path = join(SRC, file);
	return { file: path, scan: file.endsWith('.svelte') ? scanSvelte(path, source) : scanTs(path, source) };
};

function violations(scans: Scanned[]): Violation[] {
	const bad: Violation[] = [];
	for (const { file, scan } of scans) {
		for (const set of scan.classSets) {
			for (const token of set.tokens) {
				const utility = utilityOf(token);
				const palette = PALETTE_CLASS.test(utility);
				if (palette || FORBIDDEN_UTILITY.test(utility)) {
					bad.push({ where: `${relative(SRC, file)}:${set.line}`, token, palette });
				}
			}
		}
	}
	return bad;
}

describe('palette and accent roles (FEAT-r8ph93)', () => {
	const scans: Scanned[] = sourceFiles(SRC).map((file) => {
		const source = readFileSync(file, 'utf8');
		return { file, scan: name(file).endsWith('.svelte') ? scanSvelte(file, source) : scanTs(file, source) };
	});

	it('scans the sources it claims to scan, and its rules can fail', () => {
		expect(scans.some((s) => s.file.endsWith('execution.ts'))).toBe(true);
		expect(scans.flatMap((s) => s.scan.classSets).length).toBeGreaterThan(0);

		const raw = fixture('a.svelte', `<span class="text-${'red'}-600">no</span>`);
		const variant = fixture('b.svelte', `<span class="dark:bg-${'violet'}-500/15">no</span>`);
		const directive = fixture('c.svelte', `<span class:bg-${'emerald'}-500={on}>no</span>`);
		const script = fixture('d.ts', `export const tone = 'border-${'amber'}-500/30'`);
		expect(violations([raw, variant, directive, script])).toHaveLength(4);

		const textPrimary = fixture('e.svelte', '<a class="text-primary">x</a>');
		const textAccent = fixture('f.svelte', '<a class="text-accent">x</a>');
		const borderPrimary = fixture('g.svelte', '<span class="aria-checked:border-primary/40">x</span>');
		expect(violations([textPrimary, textAccent, borderPrimary])).toHaveLength(3);
	});

	it('keeps every raw Tailwind palette class out of source', () => {
		expect(violations(scans).filter((v) => v.palette)).toEqual([]);
	});

	it('never uses the accent fill as text, the hover surface as text, or the fill as a border', () => {
		expect(violations(scans).filter((v) => !v.palette)).toEqual([]);
	});
});
