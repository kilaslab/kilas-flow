import { describe, expect, it } from 'vitest';

import { setExpressionGrammar } from './expression-grammar';
import { assistKey, completionInsertion, expressionCompletions, fieldPathsFromValue, previewStep } from './expression-assist';

describe('expression completions', () => {
	it('offers served roots for an empty prefix without flooding', () => {
		setExpressionGrammar({ roots: ['$json', '$now', '$('], functions: ['toUpperCase'] });
		const completions = expressionCompletions('');
		expect(completions.some((candidate) => candidate.insert === '$json')).toBe(true);
		expect(completions.length).toBeLessThanOrEqual(24);
	});

	it('narrows upstream nodes and fields by substring', () => {
		setExpressionGrammar({ roots: ['$json'], functions: [] });
		const completions = expressionCompletions('order', {
			nodeNames: ['Orders', 'Billing'],
			fieldPaths: ['orderId', 'total']
		});
		expect(completions.map((candidate) => candidate.insert)).toContain(" $('Orders') ");
		expect(completions.map((candidate) => candidate.insert)).toContain(' $json.orderId ');
		expect(completions.map((candidate) => candidate.insert)).not.toContain(" $('Billing') ");
	});
});

describe('reading field paths from a value', () => {
	it('lists top-level keys plus one nested level', () => {
		expect(fieldPathsFromValue({ id: 1, customer: { name: 'A', city: 'B' } })).toEqual([
			'id',
			'customer',
			'customer.name',
			'customer.city'
		]);
	});

	it('reads nothing from non-objects', () => {
		expect(fieldPathsFromValue([1, 2])).toEqual([]);
		expect(fieldPathsFromValue(null)).toEqual([]);
	});
});

describe('stepping through resolved preview values', () => {
	it('clamps past-the-end indexes after a shorter re-resolution', () => {
		expect(previewStep(['a', 'b'], 9)).toEqual({ index: 1, total: 2, value: 'b' });
	});

	it('reports an empty model when nothing resolved', () => {
		expect(previewStep([], 0)).toEqual({ index: 0, total: 0, value: '' });
	});
});

describe('the keyboard contract of the suggestion list', () => {
	it('moves the highlight down and up one row at a time', () => {
		expect(assistKey(0, 3, 'ArrowDown')).toEqual({ index: 1, action: 'move' });
		expect(assistKey(1, 3, 'ArrowUp')).toEqual({ index: 0, action: 'move' });
	});

	// The ends are the ends: wrapping from the last row to the first would look
	// like the key was ignored on a list already scrolled to the bottom.
	it('holds at both ends instead of wrapping', () => {
		expect(assistKey(2, 3, 'ArrowDown')).toEqual({ index: 2, action: 'move' });
		expect(assistKey(0, 3, 'ArrowUp')).toEqual({ index: 0, action: 'move' });
	});

	// A narrower prefix matches less, so the list can shrink under the
	// highlight: Enter has to insert a row that exists.
	it('clamps a highlight the list shrank out from under', () => {
		expect(assistKey(6, 2, 'Enter')).toEqual({ index: 1, action: 'accept' });
		expect(assistKey(6, 2, 'ArrowDown')).toEqual({ index: 1, action: 'move' });
	});

	it('accepts on Enter and dismisses on Escape', () => {
		expect(assistKey(1, 3, 'Enter')).toEqual({ index: 1, action: 'accept' });
		expect(assistKey(1, 3, 'Escape')).toEqual({ index: 1, action: 'dismiss' });
	});

	// Space, Tab and the letters belong to the textarea the user is typing in,
	// and the caret keys must not stop moving when the list is not showing.
	it('ignores every other key', () => {
		for (const key of [' ', 'Tab', 'a', 'ArrowLeft', 'Home']) {
			expect(assistKey(1, 3, key)).toEqual({ index: 1, action: null });
		}
	});

	it('claims nothing when there is nothing to show', () => {
		expect(assistKey(0, 0, 'Enter')).toEqual({ index: 0, action: null });
	});
});

describe('completionInsertion', () => {
	it('replaces the typed prefix instead of appending to it', () => {
		expect(completionInsertion('{{ $', '$', ' $json.name ')).toBe('{{ $json.name');
		expect(completionInsertion('{{ $json.', '$json.', ' $json.name ')).toBe('{{ $json.name');
		expect(completionInsertion("{{ $('Set').", "$('Set').", " $('Set').item.json.v ")).toBe(
			"{{ $('Set').item.json.v"
		);
	});

	it('leaves a template with no matching prefix alone', () => {
		expect(completionInsertion('{{ ', '', ' $json.name ')).toBe('{{ $json.name');
		expect(completionInsertion('{{ 1 + ', '$json.', ' $json.name ')).toBe('{{ 1 + $json.name');
	});
});
