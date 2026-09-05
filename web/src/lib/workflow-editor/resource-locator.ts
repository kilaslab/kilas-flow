import type { PropertyDefinition, PropertyMode } from '$lib/api/generated/models';

/**
 * Reading and writing a resource locator — the control that picks one resource
 * from a searched list, by name, or by ID.
 *
 * The stored value is a self-describing object carrying an `__rl` sentinel,
 * exactly as n8n stores it, so a document round-trips through this editor
 * unchanged. See `internal/property/property.go` for why the sentinel rather
 * than a sibling `…Mode` parameter.
 */

/** The key that marks a stored resource locator. */
export const LOCATOR_SENTINEL = '__rl';

export type Locator = {
	mode: string;
	/** Still unresolved: it may hold an expression marker like any other value. */
	value: unknown;
	/** What the user last saw in the picker. Display only. */
	cachedResultName?: string;
};

function isObject(value: unknown): value is Record<string, unknown> {
	return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/**
 * Reads a stored locator, falling back to the property's first mode.
 *
 * A bare string is accepted as a locator with no mode yet: that is what a
 * document written before this control existed carries, and refusing it would
 * blank the field on load rather than at the point it matters.
 */
export function readLocator(property: PropertyDefinition, value: unknown): Locator {
	const fallback = (property.modes ?? [])[0]?.name ?? '';
	if (typeof value === 'string') return { mode: fallback, value };
	if (isObject(value) && value[LOCATOR_SENTINEL] === true) {
		return {
			mode: typeof value.mode === 'string' && value.mode !== '' ? value.mode : fallback,
			value: value.value,
			cachedResultName: typeof value.cachedResultName === 'string' ? value.cachedResultName : undefined
		};
	}
	return { mode: fallback, value: '' };
}

/** Renders a locator back into its stored shape. */
export function writeLocator(locator: Locator): Record<string, unknown> {
	const stored: Record<string, unknown> = {
		[LOCATOR_SENTINEL]: true,
		mode: locator.mode,
		value: locator.value
	};
	if (locator.cachedResultName) stored.cachedResultName = locator.cachedResultName;
	return stored;
}

/**
 * The declared mode a locator is currently in, or null when it names one this
 * build does not know.
 *
 * Null is a real answer, not an error: a workflow saved against a newer node
 * can carry a mode this editor has never heard of, and the panel shows it
 * read-only rather than rendering nothing — which reads as "this field is
 * empty" instead of "this editor is older than this node".
 */
export function currentMode(property: PropertyDefinition, locator: Locator): PropertyMode | null {
	return (property.modes ?? []).find((mode) => mode.name === locator.mode) ?? null;
}

/**
 * Switching modes keeps the value only when both modes take free text.
 *
 * A list mode's value is an ID chosen from a fetched list, and carrying a typed
 * name into it would leave the control showing a selection that does not exist.
 * Carrying an ID out into a By ID field, on the other hand, is exactly what the
 * user wants.
 */
export function switchMode(property: PropertyDefinition, locator: Locator, next: string): Locator {
	const from = currentMode(property, locator);
	const to = (property.modes ?? []).find((mode) => mode.name === next) ?? null;
	const keep = from?.kind === 'string' && to?.kind === 'string';
	return { mode: next, value: keep ? locator.value : '' };
}

/**
 * Whether a locator names anything yet.
 *
 * Used for the dependent fields a locator gates — n8n hides a Postgres node's
 * WHERE builder until a table is chosen — so an empty string, a missing value
 * and a locator that was never touched all read the same way.
 */
export function locatorIsSet(value: unknown): boolean {
	if (typeof value === 'string') return value.trim() !== '';
	if (!isObject(value) || value[LOCATOR_SENTINEL] !== true) return false;
	const inner = value.value;
	if (typeof inner === 'string') return inner.trim() !== '';
	return inner !== undefined && inner !== null;
}
