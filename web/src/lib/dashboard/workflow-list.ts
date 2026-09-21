/**
 * Pure helpers for the workflows list (FEAT-0895qc).
 *
 * Client-side search, filter, sort and paging over the workspace's workflows.
 * The list API pages server-side (BUG-fv5fer), so the page drains every page
 * through cursor-page.ts (BUG-th16c1) and this module then filters, sorts and
 * pages the complete list in memory; it owns the decisions and stays
 * unit-testable without a browser.
 */
import * as m from '$lib/paraglide/messages.js';
import type { WorkflowSummary } from '$lib/api/generated/models';

export type WorkflowActiveFilter = 'all' | 'active' | 'draft';

/** Sort orders the list offers. */
export type WorkflowSort = 'updated' | 'created' | 'name';

export interface WorkflowListQuery {
	search: string;
	active: WorkflowActiveFilter;
	sort: WorkflowSort;
	page: number;
}

/** Rows per list page. Small enough to keep a 500-row workspace navigable. */
export const WORKFLOWS_PER_PAGE = 25;

const DEFAULT_QUERY: WorkflowListQuery = { search: '', active: 'all', sort: 'updated', page: 1 };

/**
 * Reads the list query from a URL query. Unknown enum values fall back to
 * their defaults rather than echoing into the controls, and page numbers
 * below 1 clamp to 1.
 */
export function parseWorkflowListQuery(search: string): WorkflowListQuery {
	const params = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search);
	const rawSearch = params.get('q') ?? '';
	const rawActive = params.get('active') ?? 'all';
	const rawSort = params.get('sort') ?? 'updated';
	const rawPage = Number(params.get('page') ?? '1');
	return {
		search: rawSearch,
		active: rawActive === 'active' || rawActive === 'draft' ? rawActive : 'all',
		sort: rawSort === 'name' || rawSort === 'created' ? rawSort : 'updated',
		page: Number.isFinite(rawPage) && rawPage >= 1 ? Math.floor(rawPage) : 1
	};
}

/** Serialises a list query back to a query string (without the leading `?`). */
export function buildWorkflowListSearch(query: WorkflowListQuery): string {
	const params = new URLSearchParams();
	if (query.search.trim()) params.set('q', query.search.trim());
	if (query.active !== 'all') params.set('active', query.active);
	if (query.sort !== 'updated') params.set('sort', query.sort);
	if (query.page > 1) params.set('page', String(query.page));
	return params.toString();
}

export function defaultWorkflowListQuery(): WorkflowListQuery {
	return { ...DEFAULT_QUERY };
}

export interface WorkflowRow extends WorkflowSummary {
	createdAt?: string;
}

/**
 * Applies search, active/draft filter and sort. Search matches
 * case-insensitively on the name; sort falls back to name for equal keys so
 * the order is stable run to run.
 */
export function filterWorkflows(
	workflows: readonly WorkflowRow[],
	query: Pick<WorkflowListQuery, 'search' | 'active' | 'sort'>
): WorkflowRow[] {
	const needle = query.search.trim().toLowerCase();
	const kept = workflows.filter((workflow) => {
		if (query.active === 'active' && !workflow.active) return false;
		if (query.active === 'draft' && workflow.active) return false;
		if (needle && !workflow.name.toLowerCase().includes(needle)) return false;
		return true;
	});
	const byName = (a: WorkflowRow, b: WorkflowRow) => a.name.localeCompare(b.name);
	if (query.sort === 'name') return [...kept].sort(byName);
	if (query.sort === 'created') {
		return [...kept].sort(
			(a, b) => Date.parse(b.createdAt ?? '') - Date.parse(a.createdAt ?? '') || byName(a, b)
		);
	}
	return [...kept].sort(
		(a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt) || byName(a, b)
	);
}

export interface WorkflowPage {
	rows: WorkflowRow[];
	total: number;
	pages: number;
	page: number;
}

/**
 * Slices filtered rows into a page. A page past the end clamps to the last
 * one, so a delete on the final page lands on rows rather than emptiness.
 */
export function pageWorkflows(filtered: readonly WorkflowRow[], page: number): WorkflowPage {
	const total = filtered.length;
	const pages = Math.max(1, Math.ceil(total / WORKFLOWS_PER_PAGE));
	const current = Math.min(Math.max(1, page), pages);
	const start = (current - 1) * WORKFLOWS_PER_PAGE;
	return { rows: filtered.slice(start, start + WORKFLOWS_PER_PAGE), total, pages, page: current };
}

/**
 * The name a duplicate gets: "Copy of X", numbered while it collides.
 * Pure so the numbering rule is pinned by test rather than by clicking.
 */
export function duplicateName(source: string, taken: ReadonlySet<string>): string {
	const base = m.workflows_duplicate_name({ source });
	if (!taken.has(base)) return base;
	let suffix = 2;
	while (taken.has(`${base} (${suffix})`)) suffix += 1;
	return `${base} (${suffix})`;
}

/**
 * What to print for a workflow count: the whole workspace when nothing narrows
 * the list, and shown-of-total when a search or filter does.
 *
 * Printing the filtered count alone under "in this workspace" made a search
 * look like the workspace had shrunk; shown-of-total stays true either way.
 */
export function workflowCountLabel(shown: number, total: number): string {
	if (shown !== total) return m.workflows_count_of({ shown, total });
	return m.workflows_count_of_one({ total });
}
