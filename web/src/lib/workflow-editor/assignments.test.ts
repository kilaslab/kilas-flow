import { describe, expect, it } from 'vitest';

import {
	defaultForType,
	newAssignmentID,
	readAssignments,
	writeAssignments
} from './assignments';

describe('readAssignments', () => {
	it('keeps the order and the type of the rows it is given', () => {
		const rows = readAssignments({
			assignments: [
				{ id: 'r1', name: 'zebra', type: 'string', value: 'first' },
				{ id: 'r2', name: 'count', type: 'number', value: 7 },
				{ id: 'r3', name: 'zebra', type: 'string', value: 'the later row wins' }
			]
		});
		expect(rows.map((row) => [row.name, row.type])).toEqual([
			['zebra', 'string'],
			['count', 'number'],
			['zebra', 'string']
		]);
		// Two writes to the same field are two rows, which is the thing a plain
		// object could not hold.
		expect(rows).toHaveLength(3);
	});

	it('opens a node saved before assignments had order', () => {
		// Alphabetical is the only stable order a map can offer, and editing
		// such a node upgrades it rather than refusing it.
		const rows = readAssignments({ status: 'ready', count: 2 });
		expect(rows.map((row) => row.name)).toEqual(['count', 'status']);
		expect(rows.every((row) => row.type === 'string')).toBe(true);
	});

	it('is empty rather than throwing for a value it cannot read', () => {
		expect(readAssignments(undefined)).toEqual([]);
		expect(readAssignments('nonsense')).toEqual([]);
		expect(readAssignments({ assignments: ['not a row', null] })).toEqual([]);
	});

	it('falls back to string for a type this build does not know', () => {
		const [row] = readAssignments({ assignments: [{ id: 'r1', name: 'x', type: 'dateTime', value: '' }] });
		expect(row.type).toBe('string');
	});
});

describe('writeAssignments', () => {
	it('hands back a list, never a string', () => {
		// The defect this replaces: an unrecognised kind fell through to a text
		// input, so the first keystroke wrote `[object Object]` back through
		// onChange and the node's parameters were destroyed.
		const written = writeAssignments([{ id: 'r1', name: 'a', type: 'string', value: 'x' }]);
		expect(Array.isArray(written.assignments)).toBe(true);
		expect(written.assignments[0].name).toBe('a');
	});
});

describe('defaultForType', () => {
	it('keeps a value that still fits and resets one that cannot', () => {
		expect(defaultForType('number', 7)).toBe(7);
		expect(defaultForType('number', '7')).toBe(7);
		// Retyping text as a number and keeping the text would send a string to
		// a field the user has just declared numeric.
		expect(defaultForType('number', 'hello')).toBe(0);
		expect(defaultForType('boolean', 'true')).toBe(true);
		expect(defaultForType('boolean', 'no')).toBe(false);
		expect(defaultForType('array', [1])).toEqual([1]);
		expect(defaultForType('array', 'x')).toEqual([]);
		expect(defaultForType('object', { a: 1 })).toEqual({ a: 1 });
		expect(defaultForType('object', [1])).toEqual({});
		expect(defaultForType('string', 12)).toBe('12');
	});
});

describe('newAssignmentID', () => {
	it('does not collide with a row already in the list', () => {
		const rows = [{ id: 'row-0', name: 'a', type: 'string' as const, value: '' }];
		expect(newAssignmentID(rows)).not.toBe('row-0');
	});
});
