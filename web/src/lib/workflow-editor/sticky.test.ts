import { describe, expect, it } from 'vitest';

import { STICKY_SWATCHES, markdownRuns, stickyPalette } from './sticky';

describe('stickyPalette', () => {
	it('maps the stored palette index to a fill, and an unknown one to the first colour', () => {
		expect(stickyPalette(3).border).toBe('var(--kf-sticky-3-border)');
		expect(stickyPalette('5').fill).toBe('var(--kf-sticky-5-fill)');
		expect(stickyPalette(undefined)).toEqual(stickyPalette(1));
		expect(stickyPalette(99)).toEqual(stickyPalette(1));
	});

	it('fills are theme-owned custom properties, so the dark theme can tint dark', () => {
		// The pastel light fills under the dark theme's near-white labels read at
		// ~1.2:1; the values now live in app.css, which flips them per theme.
		expect(stickyPalette(1).fill).toBe('var(--kf-sticky-1-fill)');
		expect(stickyPalette(1).border).toBe('var(--kf-sticky-1-border)');
	});

	it('offers one swatch per palette index for the inspector control', () => {
		expect(STICKY_SWATCHES).toHaveLength(7);
	});
});

describe('markdownRuns', () => {
	it('reports a heading as a heading rather than as literal hashes', () => {
		expect(markdownRuns('## Setup')).toEqual([{ text: 'Setup', heading: true, bold: true }, { text: '\n' }]);
	});

	it('keeps bold and inline code as runs of text, never as markup', () => {
		const runs = markdownRuns('Set **Name** to `hello`');
		expect(runs.filter((run) => run.text !== '\n')).toEqual([
			{ text: 'Set ' },
			{ text: 'Name', bold: true },
			{ text: ' to ' },
			{ text: 'hello', code: true }
		]);
		// Nothing produces markup: content from an imported file cannot inject
		// HTML into a customer's page.
		expect(runs.every((run) => !run.text.includes('<'))).toBe(true);
	});

	it('renders an http(s) link as a link run, and anything else as text', () => {
		const runs = markdownRuns('see [the docs](https://example.com/guide)');
		expect(runs[1]).toEqual({ text: 'the docs', link: 'https://example.com/guide' });
		expect(runs[0]).toEqual({ text: 'see ' });
		expect(runs.at(-1)).toEqual({ text: '\n' });

		// A javascript: URL is a navigation surface the note does not get: it
		// stays the text its author typed.
		expect(markdownRuns('[click](javascript:alert(1))').map((run) => run.text)).toEqual(['[click](javascript:alert(1))', '\n']);
	});

	it('turns an image into its alt text as a chip, the way template notes use them', () => {
		const [run] = markdownRuns('[![Execute Workflow](https://example.com/tutorial.gif)](https://example.com/video)');
		// A linked image becomes a link the alt text labels — the URL is the
		// payload worth keeping, and raw syntax is not readable.
		expect(run).toEqual({ text: 'Execute Workflow', link: 'https://example.com/video' });
		expect(markdownRuns('![](https://example.com/x.png)')[0].text).toBe('image');
	});

	it('marks list items so the canvas can print its own bullet', () => {
		const runs = markdownRuns('- first\n* second\n1. third');
		const bullets = runs.filter((run) => run.bullet);
		expect(bullets.map((run) => run.text)).toEqual(['first', 'second', 'third']);
	});

	it('leaves an unterminated marker as the text it is', () => {
		expect(markdownRuns('**unclosed').map((run) => run.text)).toEqual(['**unclosed', '\n']);
		expect(markdownRuns(null)).toEqual([]);
	});
});
