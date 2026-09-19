import { describe, expect, it } from 'vitest';

import { setExpressionGrammar } from './expression-grammar';
import { expressionCompletions, fieldPathsFromValue, previewStep } from './expression-assist';

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
