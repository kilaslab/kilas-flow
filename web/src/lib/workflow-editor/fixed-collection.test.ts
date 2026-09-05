import { describe, expect, it } from 'vitest';

import type { PropertyDefinition } from '$lib/api/generated/models';
import {
	groupEntries,
	newGroupEntry,
	repeatedGroup,
	visibleGroupFields,
	writeGroupEntries
} from './fixed-collection';

/** The Schedule Trigger's rule, cut down to the fields this test needs. */
const rule: PropertyDefinition = {
	key: 'rule',
	label: 'Trigger Rules',
	kind: 'fixedCollection',
	required: false,
	typeOptions: { multipleValues: true },
	groups: [
		{
			key: 'interval',
			label: 'Trigger Interval',
			fields: [
				{ key: 'field', label: 'Trigger Interval', kind: 'options', required: false, default: 'days' },
				{
					key: 'secondsInterval',
					label: 'Seconds Between Triggers',
					kind: 'number',
					required: false,
					default: 30,
					visibleWhen: [{ key: 'field', equals: 'seconds' }]
				},
				{
					key: 'triggerAtHour',
					label: 'Trigger at Hour',
					kind: 'number',
					required: false,
					default: 0,
					visibleWhen: [{ key: 'field', equals: 'days' }]
				}
			]
		}
	]
};

describe('repeatedGroup', () => {
	it('recognises a single-group repeatable collection', () => {
		expect(repeatedGroup(rule)?.key).toBe('interval');
	});

	it('declines a collection that is not repeatable', () => {
		expect(repeatedGroup({ ...rule, typeOptions: {} })).toBeNull();
	});

	// n8n's multi-group form lets the user choose which kind of entry to add.
	// A control that guessed at that would look finished while being unable to
	// express half the shape, so it stays on the JSON fallback instead.
	it('declines a collection with more than one group', () => {
		expect(
			repeatedGroup({
				...rule,
				groups: [...(rule.groups ?? []), { key: 'other', label: 'Other', fields: [] }]
			})
		).toBeNull();
	});
});

describe('groupEntries', () => {
	const group = repeatedGroup(rule)!;

	it('reads the stored entries', () => {
		const entries = groupEntries(group, { interval: [{ field: 'days' }, { field: 'seconds' }] });
		expect(entries).toHaveLength(2);
		expect(entries[1].field).toBe('seconds');
	});

	// A node that has never been configured, and one whose value arrived as
	// something else entirely, both render as an empty list rather than
	// throwing in the middle of the panel.
	it.each([[undefined], [null], [{}], ['0 * * * *'], [{ interval: 'nonsense' }]])(
		'reads %o as no entries',
		(value) => {
			expect(groupEntries(group, value)).toEqual([]);
		}
	);

	it('skips an entry that is not an object', () => {
		expect(groupEntries(group, { interval: [{ field: 'days' }, 'oops', null, []] })).toHaveLength(1);
	});
});

describe('visibleGroupFields', () => {
	const group = repeatedGroup(rule)!;

	// The property that matters: each entry answers for itself. Evaluating
	// against the node's parameters would show every unit's field on every
	// entry, and against the first entry's would make every later one a copy.
	it('scopes visibility to the entry rather than the node', () => {
		const daily = visibleGroupFields(group, { field: 'days' }).map((field) => field.key);
		const seconds = visibleGroupFields(group, { field: 'seconds' }).map((field) => field.key);

		expect(daily).toEqual(['field', 'triggerAtHour']);
		expect(seconds).toEqual(['field', 'secondsInterval']);
	});

	// An entry stored before a field existed has no value for it, and its
	// declared default is what it means.
	it('reads a missing controlling value as its default', () => {
		expect(visibleGroupFields(group, {}).map((field) => field.key)).toEqual(['field', 'triggerAtHour']);
	});
});

describe('newGroupEntry', () => {
	it('starts at the declared defaults so the form is usable at once', () => {
		expect(newGroupEntry(repeatedGroup(rule)!)).toEqual({
			field: 'days',
			secondsInterval: 30,
			triggerAtHour: 0
		});
	});
});

describe('writeGroupEntries', () => {
	it('keeps sibling keys the property already carried', () => {
		const group = repeatedGroup(rule)!;
		expect(writeGroupEntries(group, { note: 'kept' }, [{ field: 'hours' }])).toEqual({
			note: 'kept',
			interval: [{ field: 'hours' }]
		});
	});
});
