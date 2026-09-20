/**
 * The canonical record guard.
 *
 * `Record<string, unknown>` says "an object", never anything about its fields,
 * so this is only ever the first step of a boundary check: each caller then
 * tests the properties it actually reads with `typeof`/`Array.isArray`. One
 * definition, because a second copy of a guard is a second answer to "what
 * counts as an object", and the copy that drifts is the one that lets a
 * malformed payload through.
 */
export function isRecord(value: unknown): value is Record<string, unknown> {
	return value !== null && typeof value === 'object' && !Array.isArray(value);
}
