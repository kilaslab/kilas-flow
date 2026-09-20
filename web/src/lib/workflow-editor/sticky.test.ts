import { describe, expect, it } from 'vitest';

import { markdownRuns, stickyPalette } from './sticky';

describe('stickyPalette', () => {
	it('maps the stored palette index to a fill, and an unknown one to the first colour', () => {
		expect(stickyPalette(3).border).toBe('#ef4444');
		expect(stickyPalette('5').fill).toBe('#dbeafe');
		expect(stickyPalette(undefined)).toEqual(stickyPalette(1));
		expect(stickyPalette(99)).toEqual(stickyPalette(1));
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

	it('leaves an unterminated marker and a link as the text they are', () => {
		expect(markdownRuns('see [docs](https://example.com)').map((run) => run.text)).toEqual(['see [docs](https://example.com)', '\n']);
		expect(markdownRuns('**unclosed').map((run) => run.text)).toEqual(['**unclosed', '\n']);
		expect(markdownRuns(null)).toEqual([]);
	});
});
