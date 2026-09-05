import { describe, expect, it } from 'vitest';

import {
	moveCondition,
	newCondition,
	readConditions,
	removeCondition,
	updateCondition,
	type Condition
} from './conditions';

const rows: Condition[] = [
	{ field: 'a', operator: 'equals', value: '1' },
	{ field: 'b', operator: 'notEquals', value: '2' },
	{ field: 'c', operator: 'exists' }
];

describe('readConditions', () => {
	it('reads every row in order', () => {
		// The stored value was always an array; the control only ever wrote
		// [next], so the second row was unreachable — a rule that needed two
		// conditions could be imported and could run, but not be edited.
		expect(readConditions(rows).map((row) => row.field)).toEqual(['a', 'b', 'c']);
	});

	it('drops the value of an operator that takes none', () => {
		expect(readConditions([{ field: 'a', operator: 'exists', value: 'stale' }])[0]).toEqual({
			field: 'a',
			operator: 'exists'
		});
	});

	it.each([[undefined], [null], ['nonsense'], [{}]])('reads %o as no rows', (value) => {
		expect(readConditions(value)).toEqual([]);
	});

	it('defaults an unrecognised operator rather than storing it', () => {
		expect(readConditions([{ field: 'a', operator: 'sortOf' }])[0].operator).toBe('equals');
	});
});

describe('updateCondition', () => {
	it('edits one row and leaves the rest', () => {
		const next = updateCondition(rows, 1, { field: 'renamed' });
		expect(next.map((row) => row.field)).toEqual(['a', 'renamed', 'c']);
	});

	// A rule that becomes "exists" must not keep a value nothing reads: the
	// next person to open it would have to wonder whether it still applies.
	it('drops the value when the operator stops taking one', () => {
		expect(updateCondition(rows, 0, { operator: 'notExists' })[0]).toEqual({
			field: 'a',
			operator: 'notExists'
		});
	});
});

describe('moveCondition', () => {
	it('moves a row and keeps the others in order', () => {
		expect(moveCondition(rows, 2, -1).map((row) => row.field)).toEqual(['a', 'c', 'b']);
		expect(moveCondition(rows, 0, 1).map((row) => row.field)).toEqual(['b', 'a', 'c']);
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
		expect(removeCondition(rows, 1).map((row) => row.field)).toEqual(['a', 'c']);
	});
});

describe('newCondition', () => {
	it('starts at the control defaults', () => {
		expect(newCondition()).toEqual({ field: '', operator: 'equals', value: '' });
	});
});
