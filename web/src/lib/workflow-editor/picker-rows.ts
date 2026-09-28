import type { Definition } from '$lib/api/generated/models';

export type PickerRow = { definition: Definition; id: string; index: number };
export type PickerGroup = { category: string; rows: PickerRow[] };

/**
 * Lay the ranked entries out for the picker list: one flat row list the
 * keyboard walks, plus one group per category for the headings.
 *
 * The grouping is by category first — a Map keyed on category, preserving
 * first-seen order — not by adjacent runs of the same category. Ranked
 * results routinely interleave categories (AI, Core, AI, …), and collapsing
 * only adjacent runs emitted two groups with the same key, so the keyed each
 * in the picker aborted the render with `each_key_duplicate` and froze the
 * list on stale rows while the footer moved on.
 */
export function buildPickerRows(entries: Definition[]): { flat: PickerRow[]; groups: PickerGroup[] } {
	const flat: PickerRow[] = [];
	const byCategory = new Map<string, PickerGroup>();
	for (const definition of entries) {
		const row: PickerRow = { definition, id: `node-option-${definition.type}@${definition.version}`, index: flat.length };
		flat.push(row);
		let group = byCategory.get(definition.category);
		if (!group) {
			group = { category: definition.category, rows: [] };
			byCategory.set(definition.category, group);
		}
		group.rows.push(row);
	}
	return { flat, groups: [...byCategory.values()] };
}
