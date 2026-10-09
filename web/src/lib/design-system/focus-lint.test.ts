import { describe, expect, it } from 'vitest';

import { APP_CSS_URL } from './app-css';
import { scanSvelte, scanTs, sourceFiles } from './class-scan';
import { formatViolations, lintClassSets, lintCss, lintJsOutline, lintTree } from './focus-lint';

// BUG-ywbvfa criterion 4: no focus indicator is removed without a
// replacement. Runs inside `pnpm test`, so `make lint` and CI run it too
// (`pnpm lint:focus` runs it alone). The fixtures first prove each rule can
// fail: a lint that silently checks nothing passes on any tree.

const SRC = new URL('../../', import.meta.url).pathname;

const svelte = (source: string) => lintClassSets(scanSvelte('fixture.svelte', source).classSets);
const ts = (source: string) => lintClassSets(scanTs('fixture.ts', source).classSets);

describe('rule A: a class that removes the outline needs a replacement', () => {
	it.each([
		['<button class="rounded outline-none">x</button>'],
		['<input class="focus-visible:outline-none" />'],
		['<div tabindex="-1" class="outline-hidden"></div>'],
		['<div class="p-1 {open ? \'outline-none\' : \'\'}"></div>'],
		['<Button class={cn("outline-none", extra)} />'],
		['<script>const c = "h-7 outline-none";</script>']
	])('reports %s', (source) => {
		expect(svelte(source)).toHaveLength(1);
	});

	// An "indicator" that itself removes or hides the ring is not a replacement,
	// and every spelling of a removal counts (review of Track I).
	it.each([
		['<button class="outline-none focus-visible:ring-0">x</button>'],
		['<button class="outline-none focus-visible:outline-0">x</button>'],
		['<button class="outline-none focus:border-transparent">x</button>'],
		['<button class="outline-none focus-visible:bg-transparent">x</button>'],
		['<button class="outline-none focus-visible:outline-offset-2">x</button>'],
		['<button class="outline-none focus-visible:ring-offset-2">x</button>'],
		['<button class="outline-0">x</button>'],
		['<button class="focus-visible:outline-0">x</button>'],
		['<button class="[outline:none]">x</button>'],
		['<button class="focus:[outline:0]">x</button>'],
		['<button class="[outline-width:0]">x</button>'],
		['<button style:outline="none">x</button>'],
		["<button style:outline={on ? 'none' : '2px solid red'}>x</button>"],
		['<button style="color: {tone}; outline: none">x</button>'],
		["<button style=\"outline: {off ? '0' : 'auto'}\">x</button>"]
	])('reports %s', (source) => {
		expect(svelte(source)).toHaveLength(1);
	});

	it.each([
		['<button class="outline-none focus-visible:outline-2">x</button>'],
		['<button class="outline-none focus-visible:ring-2 focus-visible:ring-offset-2">x</button>'],
		['<button style:outline-offset="2px">x</button>'],
		['<button style="color: {tone}">x</button>']
	])('still accepts %s', (source) => {
		expect(svelte(source)).toEqual([]);
	});

	it('reports a tv() string in a .ts file', () => {
		expect(ts('export const v = tv({ base: "inline-flex outline-none" });')).toHaveLength(1);
	});

	it.each([
		['<button class="outline-none focus-visible:ring-2">x</button>'],
		['<button class="outline-hidden focus-visible:outline-2">x</button>'],
		['<div class="outline-hidden data-highlighted:bg-accent"></div>'],
		['<div tabindex="-1" class="focus-region"></div>'],
		['<div class="focus-inset"></div>'],
		['<div class="focus-within-ring"><input class="focus-delegate" /></div>'],
		['<div class="focus-within-ring"><span><textarea class="focus-delegate"></textarea></span></div>'],
		['<div class="focus-within-ring-inset"><input class="focus-delegate" /></div>'],
		['<button class="rounded">x</button>']
	])('accepts %s', (source) => {
		expect(svelte(source)).toEqual([]);
	});

	it('needs focus-within-ring on an enclosing element for focus-delegate', () => {
		expect(svelte('<div class="border"><input class="focus-delegate" /></div>')).toHaveLength(1);
		expect(svelte('<input class="focus-delegate" /><div class="focus-within-ring"></div>')).toHaveLength(1);
	});

	it('says how to fix it', () => {
		const [violation] = svelte('<button class="outline-none">x</button>');
		expect(violation.message).toMatch(/focus-region/);
		expect(violation.message).toMatch(/focus-inset/);
		expect(violation.message).toMatch(/focus-delegate/);
		expect(violation.message).toMatch(/global :focus-visible/);
	});
});

describe('rule B: CSS that removes the outline', () => {
	it.each([
		['.a { outline: none; }'],
		['.a:focus { outline: 0 }'],
		['.a { outline-style: none }'],
		['@media (hover) { .b:focus-visible { outline: none } }'],
		['.c { @apply outline-none; }'],
		['.d:focus-visible { outline-width: 0; }'],
		['.e { @apply outline-0; }']
	])('reports %s', (css) => {
		expect(lintCss('fixture.css', css)).toHaveLength(1);
	});

	it.each([
		['.a:not(:focus-visible) { outline: none; }'],
		['@utility focus-region { outline: none; &:focus-visible { outline: 2px solid red; } }'],
		['@utility focus-delegate { outline: none; }'],
		['.svelte-flow__node.selectable:focus-visible { outline: none; }'],
		['.a { outline: 2px solid var(--kf-focus); }'],
		['.a { outline-offset: 0; }']
	])('accepts %s', (css) => {
		expect(lintCss('fixture.css', css)).toEqual([]);
	});

	it('reads a Svelte <style> block and an inline style attribute', () => {
		const scan = scanSvelte('fixture.svelte', '<div style="outline: none"></div><style>.x { outline: none }</style>');
		expect(scan.styles).toHaveLength(1);
		expect(lintCss(scan.styles[0].file, scan.styles[0].css)).toHaveLength(1);
		expect(lintClassSets(scan.classSets)).toHaveLength(1);
	});
});

describe('rule C: CSS-in-JS that removes the outline', () => {
	it('reports one that is not on the reviewed list', () => {
		const scan = scanTs('lib/x/theme.ts', "EditorView.theme({ '&.cm-focused': { outline: 'none' } })");
		expect(lintJsOutline(scan.jsOutlineRemovals, SRC)).toHaveLength(1);
	});
});

describe('focus lint over web/src', () => {
	it('scans the tree it claims to', () => {
		const files = sourceFiles(SRC);
		expect(files.length).toBeGreaterThan(150);
		expect(files.some((f) => f.endsWith('components/ui/button/button.svelte'))).toBe(true);
		expect(files.some((f) => f.includes('/paraglide/'))).toBe(false);
		expect(APP_CSS_URL.pathname.endsWith('/src/app.css')).toBe(true);
	});

	it('finds no outline removed without a replacement (BUG-ywbvfa criterion 4)', () => {
		expect(formatViolations(lintTree(SRC))).toBe('');
	});
});
