import { describe, expect, it } from 'vitest';

import type { Definition } from '$lib/api/generated/models';

import { searchCatalog } from './catalog';
import { buildPickerRows } from './picker-rows';

const base: Definition = {
	type: 'kilasflow.set',
	version: 1,
	displayName: 'Set',
	category: 'Core',
	group: ['transform'],
	source: 'builtin',
	inputs: [{ name: 'main', kind: 'main' }],
	outputs: [{ name: 'main', kind: 'main' }],
	parameters: [],
	sharedSettings: []
};

// Two AI nodes, one Core node and one Data node whose names all match the
// same short queries, so the ranked order interleaves the categories — the
// shape that used to freeze the picker list on stale rows.
const catalog: Definition[] = [
	{ ...base, type: 'kilasflow.agent', displayName: 'AI Agent', category: 'AI' },
	{ ...base, type: 'kilasflow.embeddings', displayName: 'Embeddings', category: 'AI' },
	{ ...base, type: 'kilasflow.editImage', displayName: 'Edit Image', category: 'Core' },
	{ ...base, type: 'kilasflow.dataTable', displayName: 'Data table', category: 'Data' },
	{ ...base, type: 'kilasflow.dataTableTool', displayName: 'Data table Tool', category: 'AI' },
	{ ...base, type: 'kilasflow.merge', displayName: 'Merge', category: 'Core' }
];

describe('buildPickerRows', () => {
	it('emits one group per category even when ranked results interleave categories', () => {
		// The regression this exists for: grouping by *adjacent* category runs
		// produced two "AI" groups for query "e", and the keyed each aborted the
		// render with each_key_duplicate, freezing the list on stale rows while
		// the footer showed the new count.
		for (const query of ['e', 'a', 'ai', 'da', 'me']) {
			const ranked = searchCatalog(catalog, query);
			const { groups } = buildPickerRows(ranked);
			const categories = groups.map((group) => group.category);

			expect(new Set(categories).size, `query "${query}"`).toBe(categories.length);
			expect(groups.map((group) => group.rows.length).reduce((sum, n) => sum + n, 0), `query "${query}"`).toBe(ranked.length);
		}
	});

	it('keeps the flat rows in ranked order, with the heading row first per group', () => {
		// "da" ranks Data table (starts with) before Data table Tool (contains),
		// with every row inside its category group.
		const { flat, groups } = buildPickerRows(searchCatalog(catalog, 'da'));

		expect(flat.map((row) => row.definition.displayName)).toEqual(['Data table', 'Data table Tool']);
		expect(groups.map((group) => group.category)).toEqual(['Data', 'AI']);
		expect(groups[0].rows.map((row) => row.definition.displayName)).toEqual(['Data table']);
		expect(groups[1].rows.map((row) => row.definition.displayName)).toEqual(['Data table Tool']);
	});

	it('indexes the flat rows so Enter inserts the row the list highlights', () => {
		// Enter walks rows.flat at the active index; the ids must be unique and
		// the indices must match the flat position for the highlight and the
		// insert to agree.
		const { flat } = buildPickerRows(searchCatalog(catalog, 'e'));

		expect(flat.map((row) => row.index)).toEqual(flat.map((_, index) => index));
		expect(new Set(flat.map((row) => row.id)).size).toBe(flat.length);
		// Both names start with "e"; the stable sort keeps catalog order, so
		// Enter inserts Embeddings — the row the list showed highlighted.
		expect(flat[0].definition.displayName).toBe('Embeddings');
	});
});
