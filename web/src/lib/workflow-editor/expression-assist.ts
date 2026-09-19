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
		insert: ` $('${name}') `,
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
