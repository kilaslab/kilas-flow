import { describe, expect, it } from 'vitest';

import type { Definition } from '$lib/api/generated/models';

import { catalogEntries, searchCatalog } from './catalog';

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

const catalog: Definition[] = [
	base,
	{ ...base, version: 2 },
	{ ...base, type: 'kilasflow.mysql', version: 1, displayName: 'MySQL', category: 'Database' },
	{ ...base, type: 'kilasflow.mysql', version: 2, displayName: 'MySQL', category: 'Database' },
	{ ...base, type: 'kilasflow.unsupported', version: 1, displayName: 'Unsupported node', category: 'Imported' },
	{ ...base, type: 'kilasflow.unsupported', version: 4, displayName: 'Unsupported node', category: 'Imported' },
	{ ...base, type: 'kilasflow.foreignCode', version: 1, displayName: 'Code (JavaScript or Python)', category: 'Core' },
	{ ...base, type: 'pack.telegramTrigger', version: 1, displayName: 'Telegram Trigger', category: 'Messaging', group: ['trigger'], inputs: [] },
	{ ...base, type: 'kilasflow.vectorStore', version: 1, displayName: 'Vector Store', unavailable: 'no vector driver' },
	{ ...base, type: 'kilasflow.chatModel', version: 1, displayName: 'Chat Model', category: 'AI', outputs: [{ name: 'model', kind: 'ai_languageModel' }] },
	{ ...base, type: 'kilasflow.agent', version: 1, displayName: 'AI Agent', category: 'AI', inputs: [{ name: 'model', kind: 'ai_languageModel' }] }
];

describe('catalogEntries', () => {
	it('offers one entry per node type, at the latest version', () => {
		// The regression this exists for: MySQL was listed twice, once per
		// registered version, with nothing to tell the two rows apart.
		const entries = catalogEntries(catalog);
		const mysql = entries.filter((definition) => definition.type === 'kilasflow.mysql');

		expect(mysql).toHaveLength(1);
		expect(mysql[0].version).toBe(2);
		expect(entries.filter((definition) => definition.type === 'kilasflow.set')[0].version).toBe(2);
	});

	it('never offers a node that cannot run here', () => {
		const entries = catalogEntries(catalog);
		const types = entries.map((definition) => definition.type);

		expect(types).not.toContain('kilasflow.unsupported');
		expect(types).not.toContain('kilasflow.foreignCode');
		expect(types).not.toContain('kilasflow.vectorStore');
		expect(types).toContain('kilasflow.set');
	});

	it('offers the JavaScript Code node, unless the deployment turned JavaScript off', () => {
		// The imported placeholder stays out of the picker; new JavaScript is
		// written in the node that runs it.
		const jsCode: Definition = { ...base, type: 'kilasflow.jsCode', displayName: 'Code (JavaScript)' };
		const placeholder: Definition = { ...base, type: 'kilasflow.foreignCode', displayName: 'Code (JavaScript or Python)' };

		expect(catalogEntries([jsCode, placeholder]).map((definition) => definition.type)).toEqual(['kilasflow.jsCode']);
		expect(
			catalogEntries([{ ...jsCode, unavailable: "this node's code is written in JavaScript, which this server does not run." }])
		).toEqual([]);
	});

	it('filters by behaviour and by the port a context needs', () => {
		expect(catalogEntries(catalog, { triggersOnly: true }).map((definition) => definition.type)).toEqual(['pack.telegramTrigger']);
		expect(catalogEntries(catalog, { providesKind: 'ai_languageModel' }).map((definition) => definition.type)).toEqual(['kilasflow.chatModel']);
		expect(catalogEntries(catalog, { acceptsKind: 'ai_languageModel' }).map((definition) => definition.type)).toEqual(['kilasflow.agent']);
		expect(
			catalogEntries(catalog, { acceptsMain: true })
				.map((definition) => definition.type)
				.includes('pack.telegramTrigger')
		).toBe(false);
	});
});

describe('searchCatalog', () => {
	it('ranks a name match above a category match', () => {
		// The regression this exists for: searching "form" returned the whole
		// Transform category, because a category caption matched a node name.
		const entries = catalogEntries([
			{ ...base, type: 'kilasflow.form', displayName: 'Form Trigger', category: 'Triggers', group: ['trigger'] },
			{ ...base, type: 'kilasflow.aggregate', displayName: 'Aggregate', category: 'Transform' }
		]);

		const results = searchCatalog(entries, 'form');
		// The Transform node matches on its category caption and is therefore
		// still listed — but below the node whose name is what was typed.
		expect(results.map((definition) => definition.type)).toEqual(['kilasflow.form', 'kilasflow.aggregate']);
	});

	it('finds a node by its type and by an alias, not by a caption alone', () => {
		const entries = catalogEntries([
			{ ...base, type: 'pack.gmail', displayName: 'Gmail', category: 'Communication', codex: { aliases: ['email', 'mail'] } },
			{ ...base, type: 'kilasflow.sort', displayName: 'Sort', category: 'Transform' }
		]);

		expect(searchCatalog(entries, 'pack.gmail').map((definition) => definition.type)).toEqual(['pack.gmail']);
		expect(searchCatalog(entries, 'mail').map((definition) => definition.type)).toEqual(['pack.gmail']);
		expect(searchCatalog(entries, 'communication').map((definition) => definition.type)).toEqual(['pack.gmail']);
		expect(searchCatalog(entries, 'nothing-here')).toEqual([]);
	});

	it('returns the whole catalogue for an empty query', () => {
		const entries = catalogEntries(catalog);
		expect(searchCatalog(entries, '  ')).toEqual(entries);
	});
});
