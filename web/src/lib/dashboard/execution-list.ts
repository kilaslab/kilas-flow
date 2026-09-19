/**
 * Pure helpers for the executions list (BUG-6bqh51 ops slice).
 *
 * They live here rather than in `+page.svelte` so they are unit-testable
 * without a browser: URL parsing, the stoppable set, the deleted-workflow
 * marker rule, and the workflow-filter search. The page owns fetching,
 * polling and navigation; this module owns the decisions.
 */

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
