/**
 * Pure helpers for the executions list (BUG-6bqh51 ops slice).
 *
 * They live here rather than in `+page.svelte` so they are unit-testable
 * without a browser: URL parsing, the stoppable set, the deleted-workflow
 * marker rule, the workflow-filter search, the listing request (page size and
 * filters), the auto-refresh poll decision and the "what this list holds"
 * line. The page owns fetching, polling and navigation; this module owns the
 * decisions.
 */
import type { ListExecutionsParams } from '$lib/api/generated/models';

/** Statuses the list can filter by. Mirrors the page's STATUSES. */
export const EXECUTION_STATUSES = [
	'queued',
	'running',
	'cancelling',
	'waiting',
	'succeeded',
	'failed',
	'cancelled'
] as const;

export type ExecutionStatusFilter = (typeof EXECUTION_STATUSES)[number] | '';

export interface ExecutionFilters {
	status: ExecutionStatusFilter;
	workflowId: string;
}

/**
 * Statuses a run can still be stopped from. Mirrors the detail page's
 * `stoppable`, so list rows and the detail header agree on when Stop shows.
 */
const STOPPABLE_STATUS: Record<string, true> = {
	running: true,
	queued: true,
	waiting: true,
	cancelling: true
};

/** Whether a Stop control makes sense for this execution status. */
export function isStoppableStatus(status: string): boolean {
	return STOPPABLE_STATUS[status] === true;
}

/**
 * Reads the list filters from a URL query. Unknown statuses are dropped
 * rather than echoed into the select, so a bookmarked typo shows "All"
 * instead of a filter that matches nothing.
 */
export function parseExecutionFilters(search: string): ExecutionFilters {
	const params = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search);
	const rawStatus = params.get('status') ?? '';
	const status: ExecutionStatusFilter = (EXECUTION_STATUSES as readonly string[]).includes(rawStatus)
		? (rawStatus as ExecutionStatusFilter)
		: '';
	return { status, workflowId: params.get('workflowId') ?? '' };
}

/** Serialises filters back to a query string (without the leading `?`). */
export function buildExecutionSearch(filters: ExecutionFilters): string {
	const params = new URLSearchParams();
	if (filters.status) params.set('status', filters.status);
	if (filters.workflowId) params.set('workflowId', filters.workflowId);
	return params.toString();
}

/**
 * Whether the workflow behind an execution row should read as deleted.
 *
 * The executions API returns only the id, and the name comes from the
 * workflows list. A missing name while that list is still loading means
 * nothing — it becomes "deleted" only once the list has resolved and the
 * id is still absent (soft-deleted workflows leave their executions).
 */
export function isDeletedWorkflow(
	workflowId: string,
	workflowsLoaded: boolean,
	names: ReadonlyMap<string, string>
): boolean {
	if (!workflowId) return false;
	if (!workflowsLoaded) return false;
	return !names.has(workflowId);
}

export interface WorkflowOption {
	id: string;
	name: string;
}

/**
 * Narrows the workflow filter options by a free-text query, so a workspace
 * with hundreds of workflows stays usable without a combobox dependency.
 * Matches case-insensitively on the name; an empty query keeps everything.
 */
export function filterWorkflowOptions(
	workflows: readonly WorkflowOption[],
	query: string
): WorkflowOption[] {
	const needle = query.trim().toLowerCase();
	if (!needle) return [...workflows];
	return workflows.filter((workflow) => workflow.name.toLowerCase().includes(needle));
}

/** Whether any filter is set — what separates "nothing yet" from "no match". */
export function hasActiveFilters(filters: ExecutionFilters): boolean {
	return filters.status !== '' || filters.workflowId !== '';
}

/**
 * The page size the executions list asks for.
 *
 * Twice the server default of 25 and half the API maximum of 100
 * (`MaxExecutionPageSize`, internal/repository/executions.go:38, and
 * `maximum:"100"` on the operation), so the first load, every "Load more" and
 * every auto-refresh agree on one number instead of taking whatever the server
 * happens to default to.
 */
export const EXECUTIONS_PAGE_LIMIT = 50;

/** How often the executions list refreshes its newest page while it is idle. */
export const HEAD_POLL_INTERVAL_MS = 5000;

/**
 * The query for one executions listing request.
 *
 * Status and workflow are server-side filters, so they narrow the whole
 * history rather than the rows that happen to be loaded, and unset ones are
 * left out of the query rather than sent empty.
 */
export function buildListExecutionsParams(
	filters: { status: string; workflowId: string },
	cursor = ''
): ListExecutionsParams {
	return {
		limit: EXECUTIONS_PAGE_LIMIT,
		...(filters.status ? { status: [filters.status] } : {}),
		...(filters.workflowId ? { workflowId: filters.workflowId } : {}),
		...(cursor ? { cursor } : {})
	};
}

export interface HeadPollSignals {
	/** Auto-refresh is on. */
	enabled: boolean;
	/** A first load or a manual Refresh is in flight. */
	loading: boolean;
	/** "Load more" is in flight. */
	loadingMore: boolean;
	/** The tab is in the background. */
	hidden: boolean;
	/** A first-load failure is on screen, so there is no list to refresh. */
	failedFirstLoad: boolean;
}

/**
 * Whether the auto-refresh poll should run now.
 *
 * Deliberately says nothing about cursors or loaded rows: polling used to stop
 * the moment the list carried a cursor — which is every workspace with more
 * runs than fit on one page — and a poll that merges into what is loaded
 * (`mergeHead` in cursor-page.ts) is what makes polling a paged list safe.
 */
export function shouldPollHead(signals: HeadPollSignals): boolean {
	return (
		signals.enabled &&
		!signals.loading &&
		!signals.loadingMore &&
		!signals.hidden &&
		!signals.failedFirstLoad
	);
}

/**
 * One line saying what the executions list is showing.
 *
 * It counts the rows that are loaded and nothing else: the listing is read page
 * by page, so the page holds no total and printing one would be a number the
 * server never sent. All of this copy lives here (FEAT-15k49d will move it into
 * the strings catalog) so the page renders one expression.
 */
export function executionListSummary(input: {
	count: number;
	hasMore: boolean;
	filtered: boolean;
}): string {
	const { count, hasMore, filtered } = input;
	const scope = filtered ? ' matching these filters' : '';
	if (count === 0) return `Showing no executions${scope}.`;
	// The singular cases drop the count rather than pluralise it: "the newest 1
	// executions" reads wrong, and there is only one way to say it right.
	if (count === 1) {
		return hasMore
			? `Showing the newest execution${scope}. More are available.`
			: `Showing the only execution${scope}.`;
	}
	const head = hasMore ? `the newest ${count} executions` : `all ${count} executions`;
	return `Showing ${head}${scope}.${hasMore ? ' More are available.' : ''}`;
}
