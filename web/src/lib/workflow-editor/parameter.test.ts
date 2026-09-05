import { describe, expect, it } from 'vitest';

import { asExpression, asFixed, expressionTemplate, isExpression, parameterMode } from './parameter';

describe('parameter mode', () => {
	it('recognizes only a well-formed expression marker', () => {
		expect(isExpression({ mode: 'expression', value: '{{ $json.a }}' })).toBe(true);
		expect(isExpression('{{ $json.a }}')).toBe(false);
		expect(isExpression({ mode: 'fixed', value: 'x' })).toBe(false);
		expect(isExpression({ mode: 'expression' })).toBe(false);
		expect(isExpression(null)).toBe(false);
	});

	it('reports the mode a value is in', () => {
		expect(parameterMode({ mode: 'expression', value: '' })).toBe('expression');
		expect(parameterMode('plain')).toBe('fixed');
		expect(parameterMode(undefined)).toBe('fixed');
	});
});

describe('switching modes', () => {
	it('carries a fixed value into the expression it becomes', () => {
		expect(asExpression('https://api.test')).toEqual({ mode: 'expression', value: 'https://api.test' });
		expect(asExpression(42)).toEqual({ mode: 'expression', value: '42' });
		expect(asExpression(undefined)).toEqual({ mode: 'expression', value: '' });
	});

	it('carries the template back out when switching to fixed', () => {
		expect(asFixed({ mode: 'expression', value: 'literal text' })).toBe('literal text');
		// Switching back must not leave the user staring at an expression they
		// can no longer edit as one.
		expect(asFixed({ mode: 'expression', value: '{{ $json.a }}' })).toBe('{{ $json.a }}');
		expect(asFixed('already fixed')).toBe('already fixed');
	});

	it('reads the template out of a marker', () => {
		expect(expressionTemplate({ mode: 'expression', value: '{{ $json.a }}' })).toBe('{{ $json.a }}');
		expect(expressionTemplate('plain')).toBe('');
	});
});
