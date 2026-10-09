import { describe, expect, it } from 'vitest';

import {
	buildWorkflowListSearch,
	defaultWorkflowListQuery,
	duplicateName,
	filterWorkflows,
	pageWorkflows,
	parseTagInput,
	parseWorkflowListQuery,
	type WorkflowRow,
	workflowCountLabel,
	workflowTagOptions
} from './workflow-list';

function row(overrides: Partial<WorkflowRow> & { id: string }): WorkflowRow {
	return {
		name: overrides.id,
		active: false,
		latestRevision: 1,
		updatedAt: '2026-09-18T10:00:00Z',
		tags: [],
		...overrides
	};
}

describe('parsing the workflows list query from the URL', () => {
	it('reads search, filter, sort and page', () => {
		expect(parseWorkflowListQuery('?q=orders&active=active&sort=name&page=2')).toEqual({
			search: 'orders',
			active: 'active',
			sort: 'name',
			page: 2,
			tag: ''
		});
	});

	it('falls back to defaults on unknown values', () => {
		expect(parseWorkflowListQuery('?active=bogus&sort=bogus&page=-3')).toEqual({
			...defaultWorkflowListQuery(),
			search: ''
		});
	});

	it('round-trips through the serialiser', () => {
		const query = { search: 'bill', active: 'draft' as const, sort: 'name' as const, page: 3, tag: 'Prod' };
		expect(parseWorkflowListQuery(`?${buildWorkflowListSearch(query)}`)).toEqual(query);
	});

	it('reads the tag filter and drops a blank one', () => {
		expect(parseWorkflowListQuery('?tag=billing').tag).toBe('billing');
		expect(parseWorkflowListQuery('?tag=%20Prod%20').tag).toBe('Prod');
		expect(parseWorkflowListQuery('?tag=%20%20').tag).toBe('');
		expect(parseWorkflowListQuery('').tag).toBe('');
		expect(buildWorkflowListSearch({ ...defaultWorkflowListQuery(), tag: 'a b' })).toBe('tag=a+b');
	});

	it('omits defaults so the URL stays short', () => {
		expect(buildWorkflowListSearch(defaultWorkflowListQuery())).toBe('');
	});
});

describe('filtering and sorting workflows', () => {
	const rows = [
		row({ id: 'wf_1', name: 'Zebra', active: true, updatedAt: '2026-09-18T12:00:00Z' }),
		row({ id: 'wf_2', name: 'apple', active: false, updatedAt: '2026-09-17T12:00:00Z' }),
		row({ id: 'wf_3', name: 'Mango', active: true, updatedAt: '2026-09-19T12:00:00Z' })
	];

	it('searches names case-insensitively', () => {
		const found = filterWorkflows(rows, { search: 'APP', active: 'all', sort: 'name' });
		expect(found.map((entry) => entry.id)).toEqual(['wf_2']);
	});

	it('filters active from drafts', () => {
		expect(
			filterWorkflows(rows, { search: '', active: 'active', sort: 'name' }).map((entry) => entry.id)
		).toEqual(['wf_3', 'wf_1']);
		expect(
			filterWorkflows(rows, { search: '', active: 'draft', sort: 'name' }).map((entry) => entry.id)
		).toEqual(['wf_2']);
	});

	it('sorts newest-updated first by default', () => {
		expect(
			filterWorkflows(rows, { search: '', active: 'all', sort: 'updated' }).map((entry) => entry.id)
		).toEqual(['wf_3', 'wf_1', 'wf_2']);
	});
});

describe('filtering workflows by tag', () => {
	const rows = [
		row({ id: 'wf_1', name: 'One', tags: ['Prod', 'billing'] }),
		row({ id: 'wf_2', name: 'Two', tags: ['staging'] }),
		row({ id: 'wf_3', name: 'Three' })
	];
	const base = { search: '', active: 'all' as const, sort: 'name' as const };

	it('keeps only the workflows carrying the tag, ignoring case', () => {
		for (const tag of ['prod', 'PROD', 'Prod']) {
			expect(filterWorkflows(rows, { ...base, tag }).map((entry) => entry.id)).toEqual(['wf_1']);
		}
		expect(filterWorkflows(rows, { ...base, tag: 'nothing' })).toEqual([]);
	});

	it('shows everything without a tag filter, and works beside the other filters', () => {
		expect(filterWorkflows(rows, base)).toHaveLength(3);
		expect(filterWorkflows(rows, { ...base, tag: '' })).toHaveLength(3);
		expect(filterWorkflows(rows, { ...base, tag: 'staging', search: 'tw' }).map((e) => e.id)).toEqual(['wf_2']);
		expect(filterWorkflows(rows, { ...base, tag: 'staging', search: 'one' })).toEqual([]);
	});

	it('copes with a row from an older server that sent no tags', () => {
		const legacy = { ...row({ id: 'wf_4' }), tags: undefined } as unknown as WorkflowRow;
		expect(filterWorkflows([legacy], { ...base, tag: 'prod' })).toEqual([]);
		expect(filterWorkflows([legacy], base)).toHaveLength(1);
	});

	it('offers each distinct tag once, case-insensitively, in alphabetical order', () => {
		const options = workflowTagOptions([
			row({ id: 'a', tags: ['staging', 'Prod'] }),
			row({ id: 'b', tags: ['prod', 'billing'] }),
			row({ id: 'c' })
		]);
		expect(options).toEqual(['billing', 'Prod', 'staging']);
		expect(workflowTagOptions([])).toEqual([]);
	});
});

describe('reading tags typed into a comma-separated field', () => {
	it('splits on commas, trims, drops blanks and keeps the written case and order', () => {
		expect(parseTagInput(' Prod ,billing,, Nightly job ,').tags).toEqual(['Prod', 'billing', 'Nightly job']);
		expect(parseTagInput('').tags).toEqual([]);
		expect(parseTagInput('  , ').tags).toEqual([]);
		expect(parseTagInput('a, b').problem).toBeNull();
	});

	it('collapses a repeat that differs only in case, keeping the first spelling', () => {
		expect(parseTagInput('Prod, billing, prod, PROD').tags).toEqual(['Prod', 'billing']);
	});

	it('refuses what the server would refuse, naming the tag', () => {
		const long = 'x'.repeat(25);
		expect(parseTagInput(long).problem).toEqual({ kind: 'too_long', tag: long });
		expect(parseTagInput('ok, a\u0007b').problem).toEqual({ kind: 'control', tag: 'a\u0007b' });
		expect(parseTagInput('a\tb').problem).toEqual({ kind: 'control', tag: 'a\tb' });

		const twentyOne = Array.from({ length: 21 }, (_, index) => `t${index}`).join(',');
		expect(parseTagInput(twentyOne).problem).toEqual({ kind: 'too_many' });
	});

	it('accepts twenty tags of twenty-four characters, measured the way the server measures', () => {
		const tag = (index: number) => `${'ж'.repeat(22)}${String(index).padStart(2, '0')}`;
		const twenty = Array.from({ length: 20 }, (_, index) => tag(index)).join(',');
		const parsed = parseTagInput(twenty);
		expect(parsed.problem).toBeNull();
		expect(parsed.tags).toHaveLength(20);
		// Thirteen emoji are 26 UTF-16 code units, which n8n and the server count.
		expect(parseTagInput('😀'.repeat(13)).problem?.kind).toBe('too_long');
		expect(parseTagInput('😀'.repeat(12)).problem).toBeNull();
	});
});

describe('paging the filtered workflows', () => {
	it('clamps a page past the end to the last page', () => {
		const filtered = [row({ id: 'wf_1' }), row({ id: 'wf_2' })];
		const paged = pageWorkflows(filtered, 9);
		expect(paged.page).toBe(1);
		expect(paged.rows).toHaveLength(2);
		expect(paged.total).toBe(2);
	});

	it('reports the total so the heading can say "N in this workspace"', () => {
		const filtered = [row({ id: 'wf_1' })];
		expect(pageWorkflows(filtered, 1).total).toBe(1);
	});
});

describe('naming a duplicated workflow', () => {
	it('prefixes with "Copy of" and numbers collisions', () => {
		expect(duplicateName('Orders', new Set())).toBe('Copy of Orders');
		expect(duplicateName('Orders', new Set(['Copy of Orders']))).toBe('Copy of Orders (2)');
		expect(duplicateName('Orders', new Set(['Copy of Orders', 'Copy of Orders (2)']))).toBe(
			'Copy of Orders (3)'
		);
	});
});

describe('counting workflows honestly', () => {
	it('reads a plain count when nothing narrows the list', () => {
		expect(workflowCountLabel(618, 618)).toBe('618 workflows');
	});

	// The trap: the heading printed the filtered count under "in this workspace",
	// so a search made the workspace look smaller than it is. Shown-of-total is
	// the only wording that stays true while a search narrows the list.
	it('reads shown-of-total while a search or filter narrows it', () => {
		expect(workflowCountLabel(3, 618)).toBe('3 of 618 workflows');
	});

	it('uses the singular for one', () => {
		expect(workflowCountLabel(1, 1)).toBe('1 workflow');
	});
});
