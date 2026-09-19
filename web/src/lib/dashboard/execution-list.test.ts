import { describe, expect, it } from 'vitest';

import {
	buildExecutionSearch,
	filterWorkflowOptions,
	hasActiveFilters,
	isDeletedWorkflow,
	isStoppableStatus,
	parseExecutionFilters
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
