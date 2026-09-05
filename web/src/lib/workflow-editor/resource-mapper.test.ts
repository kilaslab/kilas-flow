import { describe, expect, it } from 'vitest';

import type { MapperColumn, PropertyDefinition } from '$lib/api/generated/models';
import {
	MAPPING_AUTO,
	MAPPING_MANUAL,
	columnControl,
	defaultMatchingColumns,
	knownColumnType,
	matchableColumns,
	readMapping,
	writableColumns,
	writeMapping
} from './resource-mapper';

const schema: MapperColumn[] = [
	{ id: 'id', displayName: 'id', type: 'number', canBeUsedToMatch: true, defaultMatch: true, readOnly: true },
	{ id: 'email', displayName: 'email', type: 'string', required: true, canBeUsedToMatch: true },
	{ id: 'tier', displayName: 'tier', type: 'string' },
	{ id: 'geo', displayName: 'geo', type: 'geography' }
];

const columns: PropertyDefinition = {
	key: 'columns',
	label: 'Columns',
	kind: 'resourceMapper',
	required: true,
	mapper: { schema: { source: 'internal', name: 'sql.columns' }, supportsAutoMap: true }
};

describe('readMapping', () => {
	it('reads a stored mapping', () => {
		expect(
			readMapping(columns, {
				mappingMode: MAPPING_MANUAL,
				value: { email: 'ada@example.test' },
				matchingColumns: ['id']
			})
		).toMatchObject({ mappingMode: MAPPING_MANUAL, value: { email: 'ada@example.test' }, matchingColumns: ['id'] });
	});

	it('defaults to the mode the property supports', () => {
		expect(readMapping(columns, undefined).mappingMode).toBe(MAPPING_AUTO);
		expect(
			readMapping({ ...columns, mapper: { schema: { source: 'internal', name: 'x' } } }, undefined).mappingMode
		).toBe(MAPPING_MANUAL);
	});

	it.each([[null], ['nonsense'], [[1, 2]]])('reads %o as an empty mapping', (value) => {
		expect(readMapping(columns, value).value).toEqual({});
	});
});

describe('writeMapping', () => {
	it("carries n8n's own keys so an export is not lossy", () => {
		const stored = writeMapping(
			{ mappingMode: MAPPING_MANUAL, value: { tier: 'gold' }, matchingColumns: ['id'] },
			schema
		);
		expect(Object.keys(stored).sort()).toEqual(['mappingMode', 'matchingColumns', 'schema', 'value'].sort());
	});

	// A mapper whose columns have not loaded yet must not have its stored copy
	// blanked on the next keystroke.
	it('keeps the stored schema copy when the live one is empty', () => {
		const stored = writeMapping(
			{ mappingMode: MAPPING_MANUAL, value: {}, matchingColumns: [], schema },
			[]
		);
		expect(stored.schema).toEqual(schema);
	});
});

describe('writableColumns', () => {
	// A read-only column is filled in by the database, and a matching column
	// identifies the row rather than supplying it.
	it('omits read-only and matching columns', () => {
		const writable = writableColumns(schema, {
			mappingMode: MAPPING_MANUAL,
			value: {},
			matchingColumns: ['email']
		});
		expect(writable.map((column) => column.id)).toEqual(['tier', 'geo']);
	});
});

describe('matchableColumns and defaultMatchingColumns', () => {
	it('offers only the columns that can identify a row', () => {
		expect(matchableColumns(schema).map((column) => column.id)).toEqual(['id', 'email']);
	});

	// Preselecting the primary key is what makes an update usable without the
	// user first working out which column identifies a row.
	it('preselects the schema default', () => {
		expect(defaultMatchingColumns(schema)).toEqual(['id']);
	});
});

describe('columnControl', () => {
	it.each([
		['number', 'number'],
		['boolean', 'boolean'],
		['dateTime', 'dateTime'],
		['object', 'json'],
		['array', 'json'],
		['string', 'string'],
		// Anything unrecognised is text with a warning. Dropping the column
		// would read as "this table has no such column", a worse lie than "we
		// are not sure what this one is".
		['geography', 'string'],
		[undefined, 'string']
	])('renders %s as %s', (type, expected) => {
		expect(columnControl(type)).toBe(expected);
	});

	it('knows which types it has a control for', () => {
		expect(knownColumnType('number')).toBe(true);
		expect(knownColumnType(undefined)).toBe(true);
		expect(knownColumnType('geography')).toBe(false);
	});
});
