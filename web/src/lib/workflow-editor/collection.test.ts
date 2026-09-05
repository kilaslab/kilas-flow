import { describe, expect, it } from 'vitest';

import type { PropertyDefinition } from '$lib/api/generated/models';
import {
	addOption,
	addableOptions,
	collectionValue,
	removeOption,
	setOption,
	setOptions,
	strandedOptions,
	unreadable
} from './collection';

const options: PropertyDefinition = {
	key: 'options',
	label: 'Options',
	kind: 'collection',
	fields: [
		{ key: 'connectionTimeout', label: 'Connection Timeout', kind: 'number', default: 30 },
		{ key: 'queryBatching', label: 'Query Batching', kind: 'options', default: 'single' },
		{
			key: 'skipOnConflict',
			label: 'Skip on Conflict',
			kind: 'boolean',
			default: false,
			displayOptions: { show: [{ key: 'operation', values: ['insert'] }] }
		},
		{ key: 'outputColumns', label: 'Output Columns', kind: 'multiOptions' }
	]
} as PropertyDefinition;

describe('collectionValue', () => {
	it('reads a stored collection', () => {
		expect(collectionValue({ queryBatching: 'transaction' })).toEqual({ queryBatching: 'transaction' });
	});

	it('reads the JSON a node stored before this control existed', () => {
		expect(collectionValue('{"queryBatching":"transaction"}')).toEqual({ queryBatching: 'transaction' });
	});

	it('reads anything else as empty rather than throwing', () => {
		expect(collectionValue('[object Object]')).toEqual({});
		expect(collectionValue(null)).toEqual({});
		expect(collectionValue(['a'])).toEqual({});
	});
});

describe('unreadable', () => {
	it('names text this control cannot turn back into settings', () => {
		expect(unreadable('[object Object]')).toBe('[object Object]');
	});

	it('says nothing about a value it can read', () => {
		expect(unreadable('{"a":1}')).toBeNull();
		expect(unreadable({ a: 1 })).toBeNull();
		expect(unreadable('   ')).toBeNull();
	});
});

describe('which options are offered', () => {
	it('hides an option the current operation does not have', () => {
		const keys = addableOptions(options, {}, { operation: 'select' }).map((field) => field.key);
		expect(keys).toEqual(['connectionTimeout', 'queryBatching', 'outputColumns']);
	});

	it('offers it once the operation is the one it belongs to', () => {
		const keys = addableOptions(options, {}, { operation: 'insert' }).map((field) => field.key);
		expect(keys).toContain('skipOnConflict');
	});

	it('stops offering one already added', () => {
		const keys = addableOptions(options, { queryBatching: 'single' }, { operation: 'select' }).map(
			(field) => field.key
		);
		expect(keys).not.toContain('queryBatching');
	});

	it('lists the set ones in the order the definition declares them', () => {
		const keys = setOptions(
			options,
			{ outputColumns: [], connectionTimeout: 5 },
			{ operation: 'select' }
		).map((field) => field.key);
		expect(keys).toEqual(['connectionTimeout', 'outputColumns']);
	});
});

describe('editing the collection', () => {
	it('adds an option at its declared default', () => {
		expect(addOption({}, options.fields![0])).toEqual({ connectionTimeout: 30 });
	});

	it('adds one with no declared default at the empty value for its kind', () => {
		expect(addOption({}, options.fields![3])).toEqual({ outputColumns: [] });
	});

	it('keeps the other members when one changes', () => {
		const next = setOption({ connectionTimeout: 30 }, 'queryBatching', 'transaction');
		expect(next).toEqual({ connectionTimeout: 30, queryBatching: 'transaction' });
	});

	it('removes the key rather than blanking it, so the server default applies again', () => {
		const next = removeOption({ connectionTimeout: 5, queryBatching: 'single' }, 'connectionTimeout');
		expect(next).toEqual({ queryBatching: 'single' });
		expect('connectionTimeout' in next).toBe(false);
	});

	it('does not mutate what it was given', () => {
		const stored = { connectionTimeout: 30 };
		addOption(stored, options.fields![1]);
		removeOption(stored, 'connectionTimeout');
		setOption(stored, 'connectionTimeout', 5);
		expect(stored).toEqual({ connectionTimeout: 30 });
	});
});

describe('strandedOptions', () => {
	// The case that would otherwise be invisible: an option set for one
	// operation stays in the document when the operation changes, and the node
	// then behaves in a way nothing on screen explains.
	it('names a stored option the current operation does not show', () => {
		expect(strandedOptions(options, { skipOnConflict: true }, { operation: 'select' })).toEqual([
			'skipOnConflict'
		]);
	});

	it('names nothing when every stored option is shown', () => {
		expect(strandedOptions(options, { skipOnConflict: true }, { operation: 'insert' })).toEqual([]);
	});
});
