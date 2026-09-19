import { describe, expect, it } from 'vitest';

import {
	moveCondition,
	newCondition,
	readConditions,
	readFilterValue,
	removeCondition,
	updateCondition,
	writeFilterValue,
	type Condition
} from './conditions';

const rows: Condition[] = [
	{ leftValue: 'a', operator: { type: 'string', operation: 'equals' }, rightValue: '1' },
	{ leftValue: 'b', operator: { type: 'string', operation: 'notEquals' }, rightValue: '2' },
	{ leftValue: 'c', operator: { type: 'string', operation: 'exists' } }
];

describe('readConditions', () => {
	it('reads every row in order', () => {
		expect(readConditions(rows).map((row) => row.leftValue)).toEqual(['a', 'b', 'c']);
	});

	it('reads an n8n filter object without losing the conditions', () => {
		const filter = {
			combinator: 'or',
			conditions: [{ leftValue: '{{ $json.greeting }}', operator: { type: 'string', operation: 'contains' }, rightValue: 'Hello' }],
			options: { caseSensitive: true }
		};
		const read = readFilterValue(filter);
		expect(read.combinator).toBe('or');
		expect(read.conditions).toHaveLength(1);
		expect(read.conditions[0].leftValue).toBe('{{ $json.greeting }}');
		expect(read.conditions[0].operator.operation).toBe('contains');
		// Round-trips: the panel must never overwrite a value it can parse.
		expect(writeFilterValue(read).conditions).toHaveLength(1);
	});

	it('opens a legacy flat array without losing it', () => {
		const legacy = [{ field: 'a', operator: 'equals', value: '1' }];
		expect(readConditions(legacy)[0].leftValue).toBe('a');
	});

	it('drops the value of an operator that takes none', () => {
		expect(readConditions([{ leftValue: 'a', operator: { type: 'string', operation: 'exists' }, rightValue: 'stale' }])[0]).toEqual({
			leftValue: 'a',
			operator: { type: 'string', operation: 'exists' }
		});
	});

	it.each([[undefined], [null], ['nonsense'], [{}]])('reads %o as no rows', (value) => {
		expect(readConditions(value)).toEqual([]);
	});

	it('defaults an unrecognised operator rather than storing it', () => {
		expect(readConditions([{ leftValue: 'a', operator: { type: 'string', operation: 'sortOf' } }])[0].operator.operation).toBe('equals');
	});
});

describe('updateCondition', () => {
	it('edits one row and leaves the rest', () => {
		const next = updateCondition(rows, 1, { leftValue: 'renamed' });
		expect(next.map((row) => row.leftValue)).toEqual(['a', 'renamed', 'c']);
	});

	// A rule that becomes "exists" must not keep a value nothing reads: the
	// next person to open it would have to wonder whether it still applies.
	it('drops the value when the operator stops taking one', () => {
		expect(updateCondition(rows, 0, { operator: { type: 'string', operation: 'notExists' } })[0]).toEqual({
			leftValue: 'a',
			operator: { type: 'string', operation: 'notExists' }
		});
	});
});

describe('moveCondition', () => {
	it('moves a row and keeps the others in order', () => {
		expect(moveCondition(rows, 2, -1).map((row) => row.leftValue)).toEqual(['a', 'c', 'b']);
		expect(moveCondition(rows, 0, 1).map((row) => row.leftValue)).toEqual(['b', 'a', 'c']);
	});

	// Clamped rather than wrapped: wrapping would send the last row to the top
	// on a click meant to nudge it down.
	it('does nothing at either end', () => {
		expect(moveCondition(rows, 0, -1)).toEqual(rows);
		expect(moveCondition(rows, 2, 1)).toEqual(rows);
	});
});

describe('removeCondition', () => {
	it('removes one row', () => {
		expect(removeCondition(rows, 1).map((row) => row.leftValue)).toEqual(['a', 'c']);
	});
});

describe('newCondition', () => {
	it('starts at the control defaults', () => {
		expect(newCondition()).toEqual({ leftValue: '', operator: { type: 'string', operation: 'equals' }, rightValue: '' });
	});
});
