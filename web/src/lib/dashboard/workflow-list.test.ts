import { describe, expect, it } from 'vitest';

import {
	buildWorkflowListSearch,
	defaultWorkflowListQuery,
	duplicateName,
	filterWorkflows,
	pageWorkflows,
	parseWorkflowListQuery,
	type WorkflowRow,
	workflowCountLabel
} from './workflow-list';

function row(overrides: Partial<WorkflowRow> & { id: string }): WorkflowRow {
	return {
		name: overrides.id,
		active: false,
		latestRevision: 1,
		updatedAt: '2026-09-18T10:00:00Z',
		...overrides
	};
}

describe('parsing the workflows list query from the URL', () => {
	it('reads search, filter, sort and page', () => {
		expect(parseWorkflowListQuery('?q=orders&active=active&sort=name&page=2')).toEqual({
			search: 'orders',
			active: 'active',
			sort: 'name',
			page: 2
		});
	});

	it('falls back to defaults on unknown values', () => {
		expect(parseWorkflowListQuery('?active=bogus&sort=bogus&page=-3')).toEqual({
			...defaultWorkflowListQuery(),
			search: ''
		});
	});

	it('round-trips through the serialiser', () => {
		const query = { search: 'bill', active: 'draft' as const, sort: 'name' as const, page: 3 };
		expect(parseWorkflowListQuery(`?${buildWorkflowListSearch(query)}`)).toEqual(query);
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
