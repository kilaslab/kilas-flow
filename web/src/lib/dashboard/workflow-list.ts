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
import type { Document, WorkflowDocumentInput, WorkflowSummary } from '$lib/api/generated/models';
import { toWorkflowInput } from '$lib/workflow-editor/workflow-input';

export type WorkflowActiveFilter = 'all' | 'active' | 'draft';

/** Sort orders the list offers. */
export type WorkflowSort = 'updated' | 'created' | 'name';

export interface WorkflowListQuery {
	search: string;
	active: WorkflowActiveFilter;
	sort: WorkflowSort;
	page: number;
	/** A tag the rows must carry; empty means any. */
	tag: string;
}

/** Rows per list page. Small enough to keep a 500-row workspace navigable. */
export const WORKFLOWS_PER_PAGE = 25;

const DEFAULT_QUERY: WorkflowListQuery = { search: '', active: 'all', sort: 'updated', page: 1, tag: '' };

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
		page: Number.isFinite(rawPage) && rawPage >= 1 ? Math.floor(rawPage) : 1,
		tag: (params.get('tag') ?? '').trim()
	};
}

/** Serialises a list query back to a query string (without the leading `?`). */
export function buildWorkflowListSearch(query: WorkflowListQuery): string {
	const params = new URLSearchParams();
	if (query.search.trim()) params.set('q', query.search.trim());
	if (query.active !== 'all') params.set('active', query.active);
	if (query.sort !== 'updated') params.set('sort', query.sort);
	if (query.page > 1) params.set('page', String(query.page));
	if (query.tag.trim()) params.set('tag', query.tag.trim());
	return params.toString();
}

export function defaultWorkflowListQuery(): WorkflowListQuery {
	return { ...DEFAULT_QUERY };
}

export interface WorkflowRow extends WorkflowSummary {
	createdAt?: string;
}

/** The tag limits the server enforces (workflow.MaxTags and MaxTagLength). */
export const MAX_TAGS = 20;
export const MAX_TAG_LENGTH = 24;

/** Two tags are the same tag when they differ only in case. */
const tagKey = (tag: string) => tag.toLowerCase();

/**
 * Applies search, active/draft filter, tag filter and sort. Search matches
 * case-insensitively on the name, a tag matches case-insensitively too; sort
 * falls back to name for equal keys so the order is stable run to run.
 */
export function filterWorkflows(
	workflows: readonly WorkflowRow[],
	query: Pick<WorkflowListQuery, 'search' | 'active' | 'sort'> & Partial<Pick<WorkflowListQuery, 'tag'>>
): WorkflowRow[] {
	const needle = query.search.trim().toLowerCase();
	const wantedTag = tagKey((query.tag ?? '').trim());
	const kept = workflows.filter((workflow) => {
		if (query.active === 'active' && !workflow.active) return false;
		if (query.active === 'draft' && workflow.active) return false;
		if (needle && !workflow.name.toLowerCase().includes(needle)) return false;
		if (wantedTag && !(workflow.tags ?? []).some((tag) => tagKey(tag) === wantedTag)) return false;
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

/**
 * The tags the filter offers: each distinct tag once (the first spelling seen
 * wins), in alphabetical order.
 */
export function workflowTagOptions(workflows: readonly Pick<WorkflowRow, 'tags'>[]): string[] {
	const seen = new Map<string, string>();
	for (const workflow of workflows) {
		for (const tag of workflow.tags ?? []) {
			if (!seen.has(tagKey(tag))) seen.set(tagKey(tag), tag);
		}
	}
	return [...seen.values()].sort((a, b) => a.localeCompare(b));
}

/** Why a typed tag list cannot be saved; the page words it from the catalog. */
export type TagInputProblem = { kind: 'too_many' } | { kind: 'too_long'; tag: string } | { kind: 'control'; tag: string };

/** Control characters and line separators, which the server refuses in a tag. */
const CONTROL_CHARACTER = /[\u0000-\u001f\u007f-\u009f\u2028\u2029]/;

/**
 * Reads a comma-separated tag field, applying the rules the server enforces so
 * a refusal is worded here rather than as a 422. Tags are trimmed, empty
 * segments dropped, and a repeat that differs only in case collapses into the
 * first spelling. Length is JavaScript's own string length, which counts
 * UTF-16 code units: the unit the server and n8n measure the limit in.
 */
export function parseTagInput(raw: string): { tags: string[]; problem: TagInputProblem | null } {
	const tags: string[] = [];
	const seen = new Set<string>();
	for (const segment of raw.split(',')) {
		const tag = segment.trim();
		if (!tag || seen.has(tagKey(tag))) continue;
		if (CONTROL_CHARACTER.test(tag)) return { tags, problem: { kind: 'control', tag } };
		if (tag.length > MAX_TAG_LENGTH) return { tags, problem: { kind: 'too_long', tag } };
		seen.add(tagKey(tag));
		tags.push(tag);
	}
	if (tags.length > MAX_TAGS) return { tags, problem: { kind: 'too_many' } };
	return { tags, problem: null };
}

/** The sentence for a tag-input problem, from the catalog. */
export function tagProblemMessage(problem: TagInputProblem): string {
	if (problem.kind === 'too_many') return m.workflows_error_tags_too_many({ max: MAX_TAGS });
	if (problem.kind === 'too_long') return m.workflows_error_tag_too_long({ tag: problem.tag, max: MAX_TAG_LENGTH });
	return m.workflows_error_tag_control({ tag: problem.tag });
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
 * The save body for renaming a workflow from the list, built from the freshly
 * read document through the same function the editor saves with, so a field the
 * document gains is carried here too. `editedTags` is what the user typed;
 * null means the tags field was left alone, which keeps the tags just read from
 * the server rather than the list's older snapshot of them, so a rename never
 * overwrites a tag change another client made in between.
 */
export function renamedWorkflowInput(
	document: Document,
	name: string,
	editedTags: string[] | null
): WorkflowDocumentInput {
	const input = toWorkflowInput(document);
	return editedTags === null ? { ...input, name } : { ...input, name, tags: editedTags };
}

/** The create body for duplicating a workflow: the whole document under a new name. */
export function duplicatedWorkflowInput(document: Document, name: string): WorkflowDocumentInput {
	return { ...toWorkflowInput(document), name };
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
