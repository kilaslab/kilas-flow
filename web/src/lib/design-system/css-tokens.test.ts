import { describe, expect, it } from 'vitest';

import { parseTokenSheet, tokenEnv } from './css-tokens';

const FIXTURE = `
@import 'tailwindcss';
@custom-variant dark (&:not(:where([data-theme='light'])));
@theme inline { --color-primary: var(--primary); }
/* a comment with { braces } and a fake --kf-bg: red; */
:root { --kf-space: 4px; --kf-radius: 6px; }
:root,
[data-theme='dark'],
.theme-dark {
	color-scheme: dark;
	--kf-neutral-h: 235;
	--kf-accent-src: oklch(0.82 0.14 95);
	--kf-bg: oklch(0.172 0.007 var(--kf-neutral-h));
	--kf-dl: 0.04;
}
[data-theme='light'],
.theme-light {
	--kf-accent-src: oklch(0.82 0.15 95);
	--kf-bg: oklch(0.985 0.002 var(--kf-neutral-h));
	--kf-dl: -0.04;
}
:root,
[data-theme],
.theme-dark,
.theme-light,
[data-kf-brand] {
	--kf-accent-hover: oklch(from var(--kf-accent-src) calc(l + var(--kf-dl)) c h);
	--background: var(--kf-bg);
	--loop-a: var(--loop-b);
	--loop-b: var(--loop-a);
	--with-fallback: var(--missing, oklch(0.5 0 0));
}
@supports not (color: oklch(from red l c h)) {
	:root, [data-theme='dark'], .theme-dark, [data-kf-brand] { --kf-accent-hover: oklch(0.86 0.14 95); }
	[data-theme='light'], .theme-light { --kf-accent-hover: oklch(0.78 0.15 95); }
}
@layer base { * { outline-color: red; } }
.svelte-flow { --xy-background-color: var(--kf-bg); }
`;

describe('parseTokenSheet', () => {
	const sheet = parseTokenSheet(FIXTURE);

	it('sorts the rule blocks by the selector list they are declared on', () => {
		expect(sheet.scales.get('--kf-space')).toBe('4px');
		expect(sheet.dark.get('--kf-bg')).toBe('oklch(0.172 0.007 var(--kf-neutral-h))');
		expect(sheet.light.get('--kf-bg')).toBe('oklch(0.985 0.002 var(--kf-neutral-h))');
		expect(sheet.derived.get('--background')).toBe('var(--kf-bg)');
		expect(sheet.dark.get('color-scheme')).toBe('dark');
	});

	it('ignores comments, statement at-rules, @theme, @layer and unrelated rules', () => {
		expect(sheet.dark.has('--color-primary')).toBe(false);
		expect([...sheet.scales.keys()]).toEqual(['--kf-space', '--kf-radius']);
	});

	it('captures the relative-colour fallback block per theme', () => {
		expect(sheet.fallback.dark.get('--kf-accent-hover')).toBe('oklch(0.86 0.14 95)');
		expect(sheet.fallback.light.get('--kf-accent-hover')).toBe('oklch(0.78 0.15 95)');
	});
});

describe('parseTokenSheet refuses tokens it cannot place', () => {
	// A token declared on a selector the reader does not classify would be
	// silently left out of every test; that is a test passing on a sheet it
	// never read. Other custom properties (Svelte Flow's --xy-*) are fine.
	it.each([['.panel { --kf-bg: red; }'], ['.theme-sepia { --primary: var(--kf-accent); }'], ['body, html { --kf-sticky-l: 0.3; }']])(
		'throws on %s',
		(css) => {
			expect(() => parseTokenSheet(css)).toThrow(/unclassified/);
		}
	);

	it('accepts other custom properties on other rules', () => {
		expect(() => parseTokenSheet('.svelte-flow { --xy-edge-stroke: var(--kf-edge); color: red; }')).not.toThrow();
	});
});

describe('tokenEnv', () => {
	const sheet = parseTokenSheet(FIXTURE);

	it('resolves var() through scales, the theme and the derived layer', () => {
		expect(tokenEnv(sheet, 'dark').resolve('--background')).toBe('oklch(0.172 0.007 235)');
		expect(tokenEnv(sheet, 'light').resolve('--background')).toBe('oklch(0.985 0.002 235)');
	});

	it('re-derives accent steps per theme and per override', () => {
		expect(tokenEnv(sheet, 'dark').color('--kf-accent-hover').l).toBeCloseTo(0.86, 6);
		expect(tokenEnv(sheet, 'light').color('--kf-accent-hover').l).toBeCloseTo(0.78, 6);
		const brand = tokenEnv(sheet, 'dark', { '--kf-accent-src': 'oklch(0.5 0.1 200)' });
		expect(brand.color('--kf-accent-hover').l).toBeCloseTo(0.54, 6);
		expect(brand.color('--kf-accent-hover').h).toBeCloseTo(200, 6);
	});

	it('uses a var() fallback only when the property is undefined', () => {
		expect(tokenEnv(sheet, 'dark').resolve('--with-fallback')).toBe('oklch(0.5 0 0)');
	});

	it('reports a reference cycle instead of overflowing the stack', () => {
		expect(() => tokenEnv(sheet, 'dark').resolve('--loop-a')).toThrow(/cycle/);
	});

	it('throws on an undefined token so a typo in a pair list cannot pass', () => {
		expect(() => tokenEnv(sheet, 'dark').resolve('--kf-nope')).toThrow(/--kf-nope/);
	});
});
