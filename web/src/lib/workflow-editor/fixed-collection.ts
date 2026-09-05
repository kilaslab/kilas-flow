import type { PropertyDefinition, PropertyGroup } from '$lib/api/generated/models';
import { propertyVisible, withDefaults } from './visibility';

/**
 * Reading and writing a repeatable named group — a Schedule Trigger's rules, an
 * HTTP node's query pairs.
 *
 * It lives here rather than in the field component for the same reason
 * assignments and key-value pairs do: the shape is what the server stores, the
 * rules for reading it are worth testing on their own, and a control that also
 * owns its data model tends to grow a second, slightly different copy of it the
 * first time something else needs the same shape.
 */

/**
 * The one group of a repeatable fixedCollection, or null when the property is
 * not one.
 *
 * Only the single-group form is recognised. n8n's multi-group fixedCollection
 * lets the user choose which kind of entry to add, and a control that guessed at
 * that would look finished while quietly being unable to express half the shape.
 */
export function repeatedGroup(property: PropertyDefinition): PropertyGroup | null {
	if (property.kind !== 'fixedCollection') return null;
	if (!(property.typeOptions?.multipleValues ?? false)) return null;
	const groups = property.groups ?? [];
	return groups.length === 1 ? groups[0] : null;
}

/** The stored entries of a repeatable group, always an array to render. */
export function groupEntries(group: PropertyGroup, value: unknown): Record<string, unknown>[] {
	const stored = (value as Record<string, unknown> | null | undefined)?.[group.key];
	if (!Array.isArray(stored)) return [];
	return stored.filter(
		(entry): entry is Record<string, unknown> => typeof entry === 'object' && entry !== null && !Array.isArray(entry)
	);
}

/**
 * Which of a group's fields are shown for one entry.
 *
 * Scoped to the entry, never to the node. A rule's "Seconds Between Triggers"
 * depends on *that rule's* interval kind: evaluating it against the node's own
 * parameters would show every unit's field on every entry, and against the first
 * entry's would make every later one a copy of the first.
 */
export function visibleGroupFields(group: PropertyGroup, entry: Record<string, unknown>): PropertyDefinition[] {
	const fields = group.fields ?? [];
	const filled = withDefaults(fields, entry);
	return fields.filter((field) => propertyVisible(field, filled));
}

/**
 * A new entry, at its declared defaults.
 *
 * Not empty: the visibility rules need something to read, and an entry that
 * appeared with no fields showing would look broken rather than new.
 */
export function newGroupEntry(group: PropertyGroup): Record<string, unknown> {
	const fresh: Record<string, unknown> = {};
	for (const field of group.fields ?? []) {
		if (field.default !== undefined && field.default !== null) fresh[field.key] = field.default;
	}
	return fresh;
}

/** The property's new value with these entries in it, keeping any sibling keys. */
export function writeGroupEntries(
	group: PropertyGroup,
	value: unknown,
	entries: Record<string, unknown>[]
): Record<string, unknown> {
	const existing = typeof value === 'object' && value !== null ? (value as Record<string, unknown>) : {};
	return { ...existing, [group.key]: entries };
}
