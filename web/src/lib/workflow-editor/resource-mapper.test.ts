import { describe, expect, it } from 'vitest';

import type { MapperColumn, PropertyDefinition } from '$lib/api/generated/models';
import {
	MAPPING_AUTO,
	MAPPING_MANUAL,
	columnControl,
	columnText,
	defaultMatchingColumns,
	editColumn,
	fromAIType,
	isModelDefined,
	isWholeExpression,
	knownColumnType,
	matchableColumns,
	modelDefinedValue,
	readMapping,
	setColumnSource,
	writableColumns,
	writeMapping,
	type Mapping
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

const optedIn: PropertyDefinition = {
	...columns,
	mapper: { schema: { source: 'internal', name: 'workflows.inputs' }, modelDefinable: true }
};

const inputs: MapperColumn[] = [
	{ id: 'tempC', displayName: 'tempC', type: 'number' },
	{ id: 'unit', displayName: 'unit', type: 'string' },
	{ id: 'verbose', displayName: 'verbose', type: 'boolean' },
	{ id: 'payload', displayName: 'payload', type: 'object' }
];

const manual = (value: Record<string, unknown>): Mapping => ({
	mappingMode: MAPPING_MANUAL,
	value,
	matchingColumns: []
});

describe('readMapping of a plain object written before the property was a mapper', () => {
	// The editor used to treat this as an empty mapping, and the next edit
	// wrote it back empty: every static input the author had set was gone.
	it('reads its keys as the columns a mapper opted into the model switch holds', () => {
		expect(readMapping(optedIn, { unit: 'F', note: { mode: 'expression', value: '{{ $json.note }}' } })).toEqual({
			mappingMode: MAPPING_MANUAL,
			value: { unit: 'F', note: { mode: 'expression', value: '{{ $json.note }}' } },
			matchingColumns: []
		});
	});

	it('does not do so for a mapper that never opted in, which renders exactly as it did', () => {
		expect(readMapping(columns, { unit: 'F' }).value).toEqual({});
	});

	it.each([
		['an expression marker', { mode: 'expression', value: '{{ { unit: 1 } }}' }],
		['a partial mapper with only its mode', { mappingMode: MAPPING_MANUAL }],
		['a partial mapper with only its schema', { schema: [] }],
		['a mapper with its columns', { mappingMode: MAPPING_MANUAL, value: { unit: 'F' }, matchingColumns: [] }]
	])('does not read %s as columns', (_name, stored) => {
		const read = readMapping(optedIn, stored);
		expect(Object.keys(read.value)).not.toContain('mode');
		expect(Object.keys(read.value)).not.toContain('mappingMode');
		expect(Object.keys(read.value)).not.toContain('schema');
	});

	it('flags a whole expression, which no column editor can show', () => {
		expect(isWholeExpression({ mode: 'expression', value: '{{ { unit: 1 } }}' })).toBe(true);
		expect(isWholeExpression({ unit: 'F' })).toBe(false);
		expect(isWholeExpression(undefined)).toBe(false);
	});
});

describe('fromAIType', () => {
	it.each([
		['number', 'number'],
		['boolean', 'boolean'],
		['string', 'string'],
		// $fromAI has four types. Everything it cannot say is json, which is how
		// an object or array reaches the model untyped and the runtime refines it.
		['object', 'json'],
		['array', 'json'],
		['dateTime', 'json'],
		['', 'json'],
		[undefined, 'json'],
		['geography', 'json']
	])('names %s as %s', (columnType, expected) => {
		expect(fromAIType(columnType)).toBe(expected);
	});
});

describe('modelDefinedValue', () => {
	it('is one whole $fromAI call, typed as the column is', () => {
		expect(modelDefinedValue({ id: 'tempC', type: 'number' })).toEqual({
			mode: 'expression',
			value: "{{ $fromAI('tempC', '', 'number') }}"
		});
	});

	// A column id is whatever the sub-workflow's author typed.
	it('escapes a quote and a backslash in the id', () => {
		expect(modelDefinedValue({ id: "it's\\ok", type: 'string' }).value).toBe(
			"{{ $fromAI('it\\'s\\\\ok', '', 'string') }}"
		);
	});
});

describe('isModelDefined', () => {
	it('holds for a column nothing was written for', () => {
		expect(isModelDefined(manual({}), 'tempC')).toBe(true);
	});

	it.each([
		["{{ $fromAI('tempC', '', 'number') }}"],
		['{{$fromAI("tempC")}}'],
		["{{ $FROMAI('tempC', ``, 'json') }}"],
		["  {{ $fromAI('tempC', 'degrees (celsius)', 'number') }}\n"]
	])('holds for the whole call %s', (template) => {
		expect(isModelDefined(manual({ tempC: { mode: 'expression', value: template } }), 'tempC')).toBe(true);
	});

	it.each([
		['fixed text', 'F'],
		['null', null],
		['an empty string', ''],
		['zero', 0],
		['false', false],
		['another expression', { mode: 'expression', value: '{{ $json.note }}' }],
		['a call plus arithmetic', { mode: 'expression', value: "{{ $fromAI('tempC', '', 'number') + 1 }}" }],
		['a call inside text', { mode: 'expression', value: "t={{ $fromAI('tempC') }}" }],
		['two calls', { mode: 'expression', value: "{{ $fromAI('a') }}{{ $fromAI('b') }}" }],
		['a call whose text only looks like one', "{{ $fromAI('tempC') }}"]
	])('does not hold for %s', (_name, stored) => {
		expect(isModelDefined(manual({ tempC: stored }), 'tempC')).toBe(false);
	});
});

describe('setColumnSource', () => {
	it('hands a column to the model as its $fromAI value and leaves the others alone', () => {
		const next = setColumnSource(manual({ unit: 'F', tempC: 7 }), inputs[0], 'model');
		expect(next.value).toEqual({ unit: 'F', tempC: modelDefinedValue(inputs[0]) });
		expect(isModelDefined(next, 'tempC')).toBe(true);
	});

	it('fixes a column to an empty value of its own kind', () => {
		const start = manual({ tempC: modelDefinedValue(inputs[0]), verbose: modelDefinedValue(inputs[2]) });
		expect(setColumnSource(start, inputs[0], 'fixed').value).toMatchObject({ tempC: '' });
		expect(setColumnSource(start, inputs[2], 'fixed').value).toMatchObject({ verbose: false });
		expect(isModelDefined(setColumnSource(start, inputs[0], 'fixed'), 'tempC')).toBe(false);
	});

	// Switching one column must not turn the rest of an untouched tool from
	// "the model's, because nothing is written" into fixed nothing.
	it('writes only the column it was asked about', () => {
		const next = setColumnSource(manual({}), inputs[1], 'fixed');
		expect(Object.keys(next.value)).toEqual(['unit']);
	});

	it('does not mutate the mapping it was given', () => {
		const start = manual({ unit: 'F' });
		setColumnSource(start, inputs[0], 'model');
		expect(start.value).toEqual({ unit: 'F' });
	});
});

describe('columnText and editColumn', () => {
	// Showing the marker's JSON in a text box, and writing back whatever was
	// typed over it, destroyed an expression with the first keystroke.
	it('shows an expression as its template', () => {
		expect(columnText({ mode: 'expression', value: '{{ $json.note }}' })).toBe('{{ $json.note }}');
		expect(columnText('F')).toBe('F');
		expect(columnText(null)).toBe('');
		expect(columnText(undefined)).toBe('');
		expect(columnText(3)).toBe('3');
		expect(columnText({ a: 1 })).toBe('{"a":1}');
	});

	it('edits an expression column as an expression, keeping it one', () => {
		const previous = { mode: 'expression', value: '{{ $json.note }}' };
		expect(editColumn(previous, '{{ $json.other }}', 'string')).toEqual({ mode: 'expression', value: '{{ $json.other }}' });
		expect(editColumn(previous, 'plain', 'string')).toEqual({ mode: 'expression', value: 'plain' });
		expect(editColumn(previous, '7', 'number')).toEqual({ mode: 'expression', value: '7' });
	});

	it('turns typed braces into an expression, as an assignment row does', () => {
		expect(editColumn('F', 'x {{ $json.unit }}', 'string')).toEqual({ mode: 'expression', value: 'x {{ $json.unit }}' });
	});

	it('writes fixed text and numbers as themselves', () => {
		expect(editColumn('', 'F', 'string')).toBe('F');
		expect(editColumn('', '37', 'number')).toBe(37);
		// Clearing a number must not become zero: the author has said nothing yet.
		expect(editColumn(37, '', 'number')).toBe('');
		expect(editColumn('', '3x', 'number')).toBe('3x');
	});
});
