import { describe, expect, it } from 'vitest';

import {
	EXECUTIONS_PAGE_LIMIT,
	buildExecutionSearch,
	buildListExecutionsParams,
	executionListSummary,
	filterWorkflowOptions,
	hasActiveFilters,
	isDeletedWorkflow,
	isStoppableStatus,
	parseExecutionFilters,
	shouldPollHead
} from './execution-list';

describe('parsing executions filters from the URL', () => {
	it('reads the status and workflow id the URL carries', () => {
		expect(parseExecutionFilters('?status=failed&workflowId=wf_1')).toEqual({
			status: 'failed',
			workflowId: 'wf_1'
		});
	});

	it('accepts a query with or without the leading question mark', () => {
		expect(parseExecutionFilters('status=running')).toEqual({ status: 'running', workflowId: '' });
	});

	it('drops an unknown status rather than echoing it into the filter', () => {
		expect(parseExecutionFilters('?status=bogus').status).toBe('');
	});

	it('reads an empty query as no filters', () => {
		expect(parseExecutionFilters('')).toEqual({ status: '', workflowId: '' });
	});
});

describe('serialising executions filters back to the URL', () => {
	it('round-trips through the parser', () => {
		const filters = { status: 'waiting' as const, workflowId: 'wf_9' };
		expect(parseExecutionFilters(`?${buildExecutionSearch(filters)}`)).toEqual(filters);
	});

	it('omits unset filters so the URL stays short', () => {
		expect(buildExecutionSearch({ status: '', workflowId: '' })).toBe('');
		expect(buildExecutionSearch({ status: 'failed', workflowId: '' })).toBe('status=failed');
	});
});

describe('deciding whether a row can be stopped', () => {
	it.each([['running'], ['queued'], ['waiting'], ['cancelling']])('treats %s as stoppable', (status) => {
		expect(isStoppableStatus(status)).toBe(true);
	});

	// Terminal states carry no run to cancel: a Stop button there would
	// answer 409 at best and a confusing no-op at worst.
	it.each([['succeeded'], ['failed'], ['cancelled'], ['']])('treats %s as not stoppable', (status) => {
		expect(isStoppableStatus(status)).toBe(false);
	});
});

describe('marking executions of deleted workflows', () => {
	const names = new Map([['wf_1', 'Orders']]);

	it('marks a row deleted once the workflow list has resolved without it', () => {
		expect(isDeletedWorkflow('wf_gone', true, names)).toBe(true);
	});

	it('withholds the marker while the workflow list is still loading', () => {
		expect(isDeletedWorkflow('wf_gone', false, names)).toBe(false);
	});

	it('never marks a known workflow, even after load', () => {
		expect(isDeletedWorkflow('wf_1', true, names)).toBe(false);
	});
});

describe('searching the workflow filter', () => {
	const options = [
		{ id: 'wf_1', name: 'Orders' },
		{ id: 'wf_2', name: 'Order refunds' },
		{ id: 'wf_3', name: 'Billing' }
	];

	it('matches names case-insensitively', () => {
		expect(filterWorkflowOptions(options, 'order').map((option) => option.id)).toEqual(['wf_1', 'wf_2']);
	});

	it('keeps every option when the search is blank', () => {
		expect(filterWorkflowOptions(options, '  ')).toHaveLength(3);
	});

	it('matches nothing the workspace does not hold', () => {
		expect(filterWorkflowOptions(options, 'zzz')).toEqual([]);
	});
});

describe('separating "nothing yet" from "no match"', () => {
	it('reports no active filters on a fresh page', () => {
		expect(hasActiveFilters({ status: '', workflowId: '' })).toBe(false);
	});

	it('reports a filter as soon as either control is set', () => {
		expect(hasActiveFilters({ status: 'failed', workflowId: '' })).toBe(true);
		expect(hasActiveFilters({ status: '', workflowId: 'wf_1' })).toBe(true);
	});
});

describe('building an executions listing request', () => {
	it('always asks for the explicit page size', () => {
		expect(buildListExecutionsParams({ status: '', workflowId: '' }).limit).toBe(EXECUTIONS_PAGE_LIMIT);
	});

	// The API rejects a limit above 100 (MaxExecutionPageSize in
	// internal/repository/executions.go, `maximum:"100"` on the operation), so
	// the constant has to stay inside that even if someone raises it later.
	it('keeps the page size within what the API allows', () => {
		expect(EXECUTIONS_PAGE_LIMIT).toBeGreaterThan(0);
		expect(EXECUTIONS_PAGE_LIMIT).toBeLessThanOrEqual(100);
	});

	it('omits filters and a cursor that are not set', () => {
		expect(buildListExecutionsParams({ status: '', workflowId: '' })).toEqual({
			limit: EXECUTIONS_PAGE_LIMIT
		});
	});

	// The API takes the status as a repeated query parameter, not a string.
	it('sends the status as the array the API expects', () => {
		expect(buildListExecutionsParams({ status: 'failed', workflowId: '' })).toEqual({
			limit: EXECUTIONS_PAGE_LIMIT,
			status: ['failed']
		});
	});

	it('passes the workflow filter as a plain id', () => {
		expect(buildListExecutionsParams({ status: '', workflowId: 'wf_9' })).toEqual({
			limit: EXECUTIONS_PAGE_LIMIT,
			workflowId: 'wf_9'
		});
	});

	it('passes the cursor through', () => {
		expect(buildListExecutionsParams({ status: '', workflowId: '' }, 'after:run_9')).toEqual({
			limit: EXECUTIONS_PAGE_LIMIT,
			cursor: 'after:run_9'
		});
	});
});

describe('deciding whether the newest page should be polled', () => {
	const idle = {
		enabled: true,
		loading: false,
		loadingMore: false,
		hidden: false,
		failedFirstLoad: false
	};

	// The trap (a): the page used to stop polling as soon as the list carried a
	// cursor, which is every workspace with more runs than fit on one page. The
	// signals carry nothing about cursors or loaded rows, so a paged-on list
	// cannot switch the poll off here.
	it('polls while idle even when more pages exist', () => {
		expect(shouldPollHead(idle)).toBe(true);
	});

	it('does not poll with auto-refresh off', () => {
		expect(shouldPollHead({ ...idle, enabled: false })).toBe(false);
	});

	it('does not poll while a first load is in flight', () => {
		expect(shouldPollHead({ ...idle, loading: true })).toBe(false);
	});

	it('does not poll while Load more is in flight', () => {
		expect(shouldPollHead({ ...idle, loadingMore: true })).toBe(false);
	});

	// The trap (c): the page read document.hidden once, so polling never came
	// back after the tab had been in the background.
	it('does not poll while the tab is hidden', () => {
		expect(shouldPollHead({ ...idle, hidden: true })).toBe(false);
	});

	it('does not poll while a first-load failure owns the surface', () => {
		expect(shouldPollHead({ ...idle, failedFirstLoad: true })).toBe(false);
	});
});

describe('saying what the executions list holds', () => {
	it('says how many are loaded and that more exist', () => {
		expect(executionListSummary({ count: 50, hasMore: true, filtered: false })).toBe(
			'Showing the newest 50 executions. More are available.'
		);
	});

	it('says the loaded rows match the filters when they are on', () => {
		expect(executionListSummary({ count: 50, hasMore: true, filtered: true })).toBe(
			'Showing the newest 50 executions matching these filters. More are available.'
		);
	});

	it('says the list is complete once no cursor remains', () => {
		expect(executionListSummary({ count: 12, hasMore: false, filtered: false })).toBe(
			'Showing all 12 executions.'
		);
	});

	it('keeps the filter wording when the complete list is filtered', () => {
		expect(executionListSummary({ count: 12, hasMore: false, filtered: true })).toBe(
			'Showing all 12 executions matching these filters.'
		);
	});

	it('reads a one-row list in the singular', () => {
		expect(executionListSummary({ count: 1, hasMore: false, filtered: false })).toBe(
			'Showing the only execution.'
		);
	});

	it('keeps the singular with a filter on', () => {
		expect(executionListSummary({ count: 1, hasMore: false, filtered: true })).toBe(
			'Showing the only execution matching these filters.'
		);
	});

	// The grammar trap: "the newest 1 executions" reads wrong, so a single
	// loaded row with more behind it drops the count instead of pluralising it.
	it('drops the count for a single row with more behind it', () => {
		expect(executionListSummary({ count: 1, hasMore: true, filtered: false })).toBe(
			'Showing the newest execution. More are available.'
		);
	});

	// The line describes the loaded rows and nothing else: a total the server
	// never sent would be a number the page cannot know.
	it('never states a total it was not given', () => {
		for (const count of [0, 1, 2, 50, 618]) {
			for (const hasMore of [true, false]) {
				for (const filtered of [true, false]) {
					const text = executionListSummary({ count, hasMore, filtered });
					expect(text).not.toMatch(/of \d+/);
					expect(text).not.toContain('total');
				}
			}
		}
	});
});
