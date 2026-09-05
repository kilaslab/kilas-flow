import { describe, expect, it } from 'vitest';

import type { PropertyDefinition } from '$lib/api/generated/models';
import {
	LOCATOR_SENTINEL,
	currentMode,
	locatorIsSet,
	readLocator,
	switchMode,
	writeLocator
} from './resource-locator';

const table: PropertyDefinition = {
	key: 'table',
	label: 'Table',
	kind: 'resourceLocator',
	required: true,
	modes: [
		{ name: 'list', label: 'From list', kind: 'options', loadOptions: { source: 'internal', name: 'sql.tables' } },
		{ name: 'name', label: 'By Name', kind: 'string' },
		{ name: 'id', label: 'By ID', kind: 'string' }
	]
};

describe('readLocator', () => {
	it('reads a stored locator', () => {
		expect(
			readLocator(table, { [LOCATOR_SENTINEL]: true, mode: 'name', value: 'customers', cachedResultName: 'customers' })
		).toEqual({ mode: 'name', value: 'customers', cachedResultName: 'customers' });
	});

	// What a document written before this control existed carries. Refusing it
	// would blank the field on load rather than at the point it matters.
	it('accepts a bare string as the first mode', () => {
		expect(readLocator(table, 'customers')).toEqual({ mode: 'list', value: 'customers' });
	});

	it('falls back to the first mode when none is stored', () => {
		expect(readLocator(table, { [LOCATOR_SENTINEL]: true, value: 'x' }).mode).toBe('list');
	});

	it.each([[undefined], [null], [{ mode: 'name', value: 'x' }], [[1, 2]]])(
		'reads %o as an empty locator',
		(value) => {
			expect(readLocator(table, value)).toEqual({ mode: 'list', value: '' });
		}
	);
});

describe('writeLocator', () => {
	it('carries the sentinel n8n uses, so a document round-trips', () => {
		expect(writeLocator({ mode: 'id', value: '42' })).toEqual({
			[LOCATOR_SENTINEL]: true,
			mode: 'id',
			value: '42'
		});
	});

	it('omits an empty cached name rather than writing a blank one', () => {
		expect(writeLocator({ mode: 'id', value: '42', cachedResultName: '' })).not.toHaveProperty(
			'cachedResultName'
		);
	});
});

describe('currentMode', () => {
	it('finds the declared mode', () => {
		expect(currentMode(table, { mode: 'name', value: '' })?.label).toBe('By Name');
	});

	// A workflow saved against a newer node can name a mode this build has
	// never heard of. Null is the answer the panel needs to show it read-only.
	it('returns null for a mode this build does not know', () => {
		expect(currentMode(table, { mode: 'url', value: 'https://x' })).toBeNull();
	});
});

describe('switchMode', () => {
	// A list mode's value is an ID chosen from a fetched list; carrying a typed
	// name into it would leave the control showing a selection that does not
	// exist.
	it('drops the value when either side is a list', () => {
		expect(switchMode(table, { mode: 'name', value: 'customers' }, 'list').value).toBe('');
		expect(switchMode(table, { mode: 'list', value: 'tbl_1' }, 'name').value).toBe('');
	});

	// Carrying an ID out of By Name into By ID is exactly what the user wants.
	it('keeps the value between two typed modes', () => {
		expect(switchMode(table, { mode: 'name', value: 'customers' }, 'id')).toEqual({
			mode: 'id',
			value: 'customers'
		});
	});
});

describe('locatorIsSet', () => {
	// This gates the dependent fields a locator controls — n8n hides a Postgres
	// node's WHERE builder until a table is chosen — so every shape of "not
	// chosen yet" has to read the same way.
	it.each([
		[undefined, false],
		['', false],
		['   ', false],
		[{ [LOCATOR_SENTINEL]: true, mode: 'name', value: '' }, false],
		[{ mode: 'name', value: 'customers' }, false],
		['customers', true],
		[{ [LOCATOR_SENTINEL]: true, mode: 'name', value: 'customers' }, true],
		[{ [LOCATOR_SENTINEL]: true, mode: 'id', value: 42 }, true]
	])('reads %o as %s', (value, expected) => {
		expect(locatorIsSet(value)).toBe(expected);
	});
});
