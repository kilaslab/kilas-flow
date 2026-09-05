import type { PropertyDefinition } from '$lib/api/generated/models';
import { propertyVisible } from './visibility';

/**
 * Reading and writing an options collection — the database nodes' Options, an
 * HTTP node's extras.
 *
 * A collection is not a nested form. Its members are added one at a time and an
 * absent member means "the server's default", which is why every function here
 * distinguishes a key that is present and holding its default from a key that
 * is not there at all: writing every declared default into the document on the
 * first edit would freeze today's defaults into every workflow, and a later
 * change to one of them would then reach no existing node.
 *
 * It lives here rather than in the field component for the reason the fixed
 * collection's helper does: the shape is what the server stores, and a control
 * that also owns its data model grows a second, slightly different copy of it.
 */

/** The stored collection, always an object to render. */
export function collectionValue(value: unknown): Record<string, unknown> {
	if (typeof value === 'object' && value !== null && !Array.isArray(value)) {
		return value as Record<string, unknown>;
	}
	if (typeof value === 'string') {
		// The editor used to render a collection as a textarea, so a node
		// stored before this control existed holds text. JSON is worth trying
		// — it is what a user typing into that textarea would most plausibly
		// have written — and anything else is reported rather than discarded.
		try {
			const parsed: unknown = JSON.parse(value);
			if (typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed)) {
				return parsed as Record<string, unknown>;
			}
		} catch {
			return {};
		}
	}
	return {};
}

/**
 * The text a stored value holds that this control cannot read, or null.
 *
 * Surfaced rather than silently replaced: a collection that arrived as text is
 * a node somebody configured, and quietly showing it as empty would look like
 * the settings had been lost — which, on the next save, they would have been.
 */
export function unreadable(value: unknown): string | null {
	if (typeof value !== 'string' || value.trim() === '') return null;
	try {
		const parsed: unknown = JSON.parse(value);
		if (typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed)) return null;
	} catch {
		return value;
	}
	return value;
}

/**
 * Which members the collection may show at all.
 *
 * Evaluated against the node's own parameters, not against the collection's
 * contents: an option shown only for one operation depends on `operation`,
 * which is the collection's sibling rather than its member.
 */
export function availableOptions(
	property: PropertyDefinition,
	siblings: Record<string, unknown>
): PropertyDefinition[] {
	return (property.fields ?? []).filter((field) => propertyVisible(field, siblings));
}

/** The members currently set, in the order the definition declares them. */
export function setOptions(
	property: PropertyDefinition,
	value: unknown,
	siblings: Record<string, unknown>
): PropertyDefinition[] {
	const stored = collectionValue(value);
	return availableOptions(property, siblings).filter((field) => field.key in stored);
}

/** The members that can still be added. */
export function addableOptions(
	property: PropertyDefinition,
	value: unknown,
	siblings: Record<string, unknown>
): PropertyDefinition[] {
	const stored = collectionValue(value);
	return availableOptions(property, siblings).filter((field) => !(field.key in stored));
}

/**
 * The collection with one member added, at its declared default.
 *
 * At its default rather than empty, so the control appears showing what the
 * server would have done anyway — adding an option and having it change the
 * node's behaviour before the user has touched it would be a surprise.
 */
export function addOption(value: unknown, field: PropertyDefinition): Record<string, unknown> {
	const next = { ...collectionValue(value) };
	next[field.key] = field.default ?? defaultFor(field);
	return next;
}

/** The value a member starts at when its definition declares none. */
function defaultFor(field: PropertyDefinition): unknown {
	switch (field.kind) {
		case 'boolean':
			return false;
		case 'number':
			return 0;
		case 'multiOptions':
			return [];
		default:
			return '';
	}
}

/** The collection with one member removed, so the server's default applies. */
export function removeOption(value: unknown, key: string): Record<string, unknown> {
	const next = { ...collectionValue(value) };
	delete next[key];
	return next;
}

/** The collection with one member's value replaced. */
export function setOption(value: unknown, key: string, next: unknown): Record<string, unknown> {
	return { ...collectionValue(value), [key]: next };
}

/**
 * Members that are stored but no longer shown, by key.
 *
 * Kept in the document and named in the panel rather than deleted. An option
 * set for one operation and then hidden by switching to another is still what
 * the user asked for, and switching back must find it — but leaving it
 * invisible would make a node behave in a way nothing on screen explains.
 */
export function strandedOptions(
	property: PropertyDefinition,
	value: unknown,
	siblings: Record<string, unknown>
): string[] {
	const shown = new Set(availableOptions(property, siblings).map((field) => field.key));
	return Object.keys(collectionValue(value)).filter((key) => !shown.has(key));
}
