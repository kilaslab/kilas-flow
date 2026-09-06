import { describe, expect, it } from 'vitest';

import {
	COLUMN_TYPES,
	coerceValue,
	displayType,
	gridColumns,
	isSystemColumn,
	wireType
} from './columns';

describe('isSystemColumn', () => {
	it.each([['id'], ['createdAt'], ['updatedAt']])('treats %s as system', (name) => {
		expect(isSystemColumn(name)).toBe(true);
	});

	it('treats user columns as non-system', () => {
		expect(isSystemColumn('title')).toBe(false);
	});
});

describe('the add-column vocabulary', () => {
	it('offers the four n8n types verbatim', () => {
		expect([...COLUMN_TYPES]).toEqual(['string', 'number', 'boolean', 'datetime']);
	});

	it('maps the wire date to the datetime dialog label and back', () => {
		expect(displayType('date')).toBe('datetime');
		expect(wireType('datetime')).toBe('date');
		expect(displayType('string')).toBe('string');
	});
});

describe('gridColumns', () => {
	it('renders id, then user columns, then the timestamps', () => {
		const headers = gridColumns([
			{ name: 'title', type: 'string' },
			{ name: 'score', type: 'number' }
		]);

		expect(headers.map((column) => column.name)).toEqual([
			'id',
			'title',
			'score',
			'createdAt',
			'updatedAt'
		]);
	});

	it('marks only the pinned columns as system', () => {
		const headers = gridColumns([{ name: 'title', type: 'string' }]);

		expect(headers.filter((column) => column.system).map((column) => column.name)).toEqual([
			'id',
			'createdAt',
			'updatedAt'
		]);
	});

	it('keeps a catalogue echo of a system name out of the user section', () => {
		const headers = gridColumns([{ name: 'id', type: 'number' }]);

		expect(headers.map((column) => column.name)).toEqual(['id', 'createdAt', 'updatedAt']);
	});
});

describe('coerceValue', () => {
	it('parses numbers and refuses text', () => {
		expect(coerceValue('number', 'score', '3')).toEqual({ ok: true, value: 3 });
		expect(coerceValue('number', 'score', 'abc')).toEqual({
			ok: false,
			error: 'score needs a number.'
		});
	});

	it('parses booleans and refuses anything else', () => {
		expect(coerceValue('boolean', 'flag', 'true')).toEqual({ ok: true, value: true });
		expect(coerceValue('boolean', 'flag', '0')).toEqual({ ok: true, value: false });
		expect(coerceValue('boolean', 'flag', 'maybe')).toEqual({
			ok: false,
			error: 'flag needs true or false.'
		});
	});

	it('normalises datetimes to ISO and refuses blanks', () => {
		const parsed = coerceValue('datetime', 'happened', '2026-09-06T10:00:00Z');
		expect(parsed).toEqual({ ok: true, value: '2026-09-06T10:00:00.000Z' });
		expect(coerceValue('date', 'happened', '')).toEqual({
			ok: false,
			error: 'happened needs a date and time.'
		});
	});

	it('passes strings through untouched', () => {
		expect(coerceValue('string', 'title', '  hello  ')).toEqual({ ok: true, value: '  hello  ' });
	});
});
