/**
 * Expression authoring helpers for the NDV expression editor (FEAT-56nep4).
 *
 * Pure and unit-tested: completion candidates from the served grammar plus
 * upstream node names and field paths, and a server-echoed preview model
 * (item stepping over a resolved value list). The panel owns fetching and
 * focus; this module owns the decisions.
 */
import { expressionFunctions, expressionRoots } from './expression-grammar';

export interface CompletionCandidate {
	/** What the editor inserts. */
	insert: string;
	/** One-line meaning shown beside the candidate. */
	detail: string;
}

/**
 * `$('Name')` for one node, with the name escaped as a single-quoted string.
 *
 * Node names are free text: a quote or a backslash in one, spliced in as it
 * is, writes an expression that no longer parses.
 */
export function nodeReference(name: string): string {
	return `$('${name.replaceAll('\\', '\\\\').replaceAll("'", "\\'")}')`;
}

/**
 * Completion candidates for a template prefix: served roots first, then
 * upstream node names as `$('Name')`, then `$json` field paths. Matching
 * is a case-insensitive substring on the insert text; an empty prefix
 * returns the leading window of each group rather than everything.
 */
export function expressionCompletions(
	prefix: string,
	options: { nodeNames?: readonly string[]; fieldPaths?: readonly string[] } = {}
): CompletionCandidate[] {
	const roots = expressionRoots();
	const functions = expressionFunctions();
	const needle = prefix.trim().toLowerCase();
	const matches = (text: string) => !needle || text.toLowerCase().includes(needle);
	const take = (candidates: CompletionCandidate[]) =>
		needle ? candidates.filter((candidate) => matches(candidate.insert)) : candidates.slice(0, 8);

	const rootCandidates = roots
		.filter((root) => root !== '$(')
		.map((root) => ({ insert: root, detail: 'expression root' }));
	const nodeCandidates = (options.nodeNames ?? []).map((name) => ({
		insert: ` ${nodeReference(name)} `,
		detail: 'upstream node'
	}));
	const fieldCandidates = (options.fieldPaths ?? []).map((path) => ({
		insert: ` $json.${path} `,
		detail: 'input field'
	}));
	const functionCandidates = functions.map((name) => ({ insert: `${name}()`, detail: 'expression function' }));
	return [
		...take(rootCandidates),
		...take(nodeCandidates),
		...take(fieldCandidates),
		...take(functionCandidates)
	].slice(0, 24);
}

/**
 * Pulls dotted `$json` paths out of a JSON value for completions, one level
 * plus one nested level deep. Deeper shapes stay explorable through the
 * input pane; the completion list is a shortcut, not a schema browser.
 */
export function fieldPathsFromValue(value: unknown): string[] {
	if (value === null || typeof value !== 'object' || Array.isArray(value)) return [];
	const paths: string[] = [];
	for (const [key, nested] of Object.entries(value as Record<string, unknown>)) {
		if (!key) continue;
		paths.push(key);
		if (nested !== null && typeof nested === 'object' && !Array.isArray(nested)) {
			for (const inner of Object.keys(nested as Record<string, unknown>)) {
				if (inner) paths.push(`${key}.${inner}`);
			}
		}
	}
	return paths.slice(0, 40);
}

/** What a keystroke asks of the open suggestion list. */
export type AssistAction = 'move' | 'accept' | 'dismiss';

export interface AssistKeyDecision {
	/** The candidate the highlight sits on once the key has been handled. */
	index: number;
	/** What the key meant to the list, or null when the list ignores it. */
	action: AssistAction | null;
}

/**
 * What an open suggestion list does with a keystroke, and which candidate the
 * highlight lands on afterwards.
 *
 * The list is rendered as a listbox and the textarea keeps focus — typing has
 * to keep working — so the arrows move a highlight that aria-activedescendant
 * points at, Enter inserts the highlighted candidate, and Escape puts the list
 * away. Everything else, Space and Tab included, stays the textarea's; that
 * whole keyboard contract is here rather than in a switch in the component so
 * it is one decision with one test rather than a branch per handler.
 *
 * The highlight does not wrap: the ends are the ends, as in the node picker.
 * An index past the end of a list that shrank under it (a narrower prefix
 * matched less) clamps to the last row rather than pointing at nothing.
 */
export function assistKey(index: number, count: number, key: string): AssistKeyDecision {
	if (count <= 0) return { index: 0, action: null };
	const last = count - 1;
	const held = Math.min(Math.max(index, 0), last);
	if (key === 'ArrowDown') return { index: Math.min(held + 1, last), action: 'move' };
	if (key === 'ArrowUp') return { index: Math.max(held - 1, 0), action: 'move' };
	if (key === 'Enter') return { index: held, action: 'accept' };
	if (key === 'Escape') return { index: held, action: 'dismiss' };
	return { index: held, action: null };
}

/**
 * The body text after a candidate is accepted: the typed prefix is *replaced*
 * by what the candidate inserts, not appended to.
 *
 * The prefix is what the suggestion list matched (`$`, `$json.`, `$('Set').`),
 * so appending would leave `{{ $$json }}` for a prefix that ended in a bare
 * `$`. The closing braces belong to the caller — the component writes
 * `{{ <body> }}` — so this returns the body alone.
 */
export function completionInsertion(template: string, prefix: string, insert: string): string {
	const body = prefix !== '' && template.endsWith(prefix) ? template.slice(0, template.length - prefix.length) : template;
	return `${body}${insert.trim()}`;
}

export interface PreviewStep {
	index: number;
	total: number;
	value: string;
}

/**
 * The preview stepping model: which resolved value of a list is showing.
 * Clamps out-of-range indexes so a shorter re-resolution cannot strand the
 * stepper past the end.
 */
export function previewStep(values: readonly string[], index: number): PreviewStep {
	const total = values.length;
	if (total === 0) return { index: 0, total: 0, value: '' };
	const current = Math.min(Math.max(0, index), total - 1);
	return { index: current, total, value: values[current] };
}
