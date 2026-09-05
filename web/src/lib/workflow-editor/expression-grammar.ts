import type { ExpressionGrammar } from '$lib/api/generated/models';

/**
 * The expression surface the server accepts, fetched rather than duplicated.
 *
 * The editor used to hardcode its own root list and its own error message, so a
 * root added in Go was rejected in the editor until somebody remembered to edit
 * one particular component. Anything cached here comes from
 * `GET /api/v1/expression-grammar`, which is generated from the same Go
 * allowlist the evaluator uses.
 */
let grammar: ExpressionGrammar | null = null;

/** Records the grammar the server reported. */
export function setExpressionGrammar(next: ExpressionGrammar | undefined): void {
	grammar = next ?? null;
}

/**
 * Validates the roots used in a template.
 *
 * Returns null while the grammar has not loaded. Guessing with a stale local
 * list is what this replaces, so saying nothing is better than saying something
 * wrong — and the server is the authority either way.
 */
export function unknownExpressionRoot(text: string): string | null {
	const roots = expressionRoots();
	if (roots.length === 0) return null;
	const used = [...text.matchAll(/\{\{\s*(\$[A-Za-z0-9_]*\(?)/g)].map((match) => match[1]);
	for (const root of used) {
		// `$('Node')` is spelled `$(` in the allowlist, because it takes a
		// quoted argument rather than being a plain name.
		if (roots.includes(root)) continue;
		if (root.endsWith('(') && roots.includes(root.slice(0, -1))) continue;
		return root;
	}
	return null;
}

/** The roots to name in a hint, in the order the server listed them. */
export function expressionRoots(): string[] {
	return grammar?.roots ?? [];
}
