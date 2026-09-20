import type { Definition } from '$lib/api/generated/models';

/**
 * Node types the registry carries only so an import stays visible.
 *
 * `kilasflow.unsupported` is the placeholder an unmappable imported node
 * becomes and `kilasflow.foreignCode` holds an imported Code node. Both refuse
 * at compile time by construction, so adding one from the picker can only
 * produce a workflow that cannot be activated — which is exactly what the
 * picker must not offer.
 *
 * Named here rather than in the payload because the catalogue has no flag for
 * it yet; when the registry can mark a definition import-only, this list is
 * what it replaces.
 */
export const UNSUPPORTED_TYPE = 'kilasflow.unsupported';
export const IMPORT_ONLY_TYPES: Record<string, true> = { [UNSUPPORTED_TYPE]: true, 'kilasflow.foreignCode': true };

export type CatalogFilter = {
	/** Only nodes that start a workflow. */
	triggersOnly?: boolean;
	/** Only nodes that can receive `main` items after an existing step. */
	acceptsMain?: boolean;
	/** Only nodes that provide an output of this kind — an agent slot asking for a model. */
	providesKind?: string;
	/** Only nodes that can be attached to an input of this kind. */
	acceptsKind?: string;
};

/**
 * The picker's catalogue: what can be added to a canvas, once each.
 *
 * The registry is versioned — MySQL is registered at v1 and v2, and the import
 * placeholder at four arities — and a picker entry per version shows the same
 * node four times with no way to tell them apart. An existing node keeps its
 * older version; only what is *offered* is collapsed, which is what n8n does.
 */
export function catalogEntries(definitions: Definition[], filter: CatalogFilter = {}): Definition[] {
	const latest = new Map<string, Definition>();
	for (const definition of definitions) {
		if (IMPORT_ONLY_TYPES[definition.type]) continue;
		if (definition.unavailable) continue;
		if (filter.triggersOnly && !(definition.group ?? []).includes('trigger')) continue;
		// Behaviour comes from the group and the ports, never from the display
		// category: a node filed somewhere unexpected is still a trigger if it
		// starts workflows.
		if (filter.acceptsMain && !(definition.inputs ?? []).some((port) => port.kind === 'main')) continue;
		if (filter.providesKind && !(definition.outputs ?? []).some((port) => port.kind === filter.providesKind)) continue;
		if (filter.acceptsKind && !(definition.inputs ?? []).some((port) => port.kind === filter.acceptsKind)) continue;
		const current = latest.get(definition.type);
		if (!current || definition.version > current.version) latest.set(definition.type, definition);
	}
	return [...latest.values()].sort(
		(left, right) => left.category.localeCompare(right.category) || left.displayName.localeCompare(right.displayName)
	);
}

/**
 * Search results, best match first.
 *
 * A node whose *name* matches is what the user asked for; a node that only
 * shares a word with its category (`form` in Transform) is a footnote. Ranking
 * by where the match landed keeps the answer at the top of the list instead of
 * buried under its own category.
 */
export function searchCatalog(entries: Definition[], query: string): Definition[] {
	const needle = query.trim().toLocaleLowerCase();
	if (needle === '') return entries;
	const scored: { definition: Definition; score: number }[] = [];
	for (const definition of entries) {
		const score = matchRank(definition, needle);
		if (score === null) continue;
		scored.push({ definition, score });
	}
	scored.sort((left, right) => left.score - right.score);
	return scored.map((entry) => entry.definition);
}

/** Lower is a better match; null means the node is not in the result at all. */
function matchRank(definition: Definition, needle: string): number | null {
	const name = definition.displayName.toLocaleLowerCase();
	if (name.startsWith(needle)) return 0;
	if (name.includes(needle)) return 1;
	if (definition.type.toLocaleLowerCase().includes(needle)) return 2;
	if ((definition.codex?.aliases ?? []).some((alias) => alias.toLocaleLowerCase().includes(needle))) return 2;
	if ((definition.description ?? '').toLocaleLowerCase().includes(needle)) return 3;
	if (definition.category.toLocaleLowerCase().includes(needle)) return 4;
	// The codex sections name where a node is filed, and they are what a user
	// browsing rather than searching is reading.
	if (categories(definition).some((item) => item.toLocaleLowerCase().includes(needle))) return 5;
	return null;
}

function categories(definition: Definition): string[] {
	return [
		...(definition.codex?.categories ?? []),
		...Object.values(definition.codex?.subcategories ?? {}).flatMap((items) => items ?? [])
	];
}
