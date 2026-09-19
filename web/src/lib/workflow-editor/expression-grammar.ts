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

/** The function names to name in a hint, in the order the server listed them. */
export function expressionFunctions(): string[] {
	return grammar?.functions ?? [];
}

/**
 * Best-effort expression check beyond the root allowlist: brace balance plus
 * unknown roots (delegated) plus method names the served function list does
 * not carry. The served functions are method names (`toUpperCase`,
 * `toISOString`, …): the engine exposes no free calls and no `JSON` or
 * Luxon surface, so `JSON.stringify(` and `.toFormat(` are both rejected
 * shapes. The server remains the authority — this never claims a value,
 * only flags shapes the server will reject.
 */
export function validateExpressionShape(text: string): string | null {
	const opens = (text.match(/\{\{/g) ?? []).length;
	const closes = (text.match(/\}\}/g) ?? []).length;
	if (opens === 0) return 'No {{ }} expression yet — this will be sent as literal text.';
	if (opens !== closes) return 'Unbalanced {{ }} — the server will reject this expression.';
	const unknown = unknownExpressionRoot(text);
	if (unknown) {
		const roots = expressionRoots();
		return roots.length > 0
			? `${unknown} is not an available root. Use ${roots.join(', ')}.`
			: `${unknown} is not an available root.`;
	}
	const functions = expressionFunctions();
	if (functions.length > 0) {
		for (const match of text.matchAll(/\.([A-Za-z_][A-Za-z0-9_]*)\s*\(/g)) {
			const name = match[1];
			if (!functions.includes(name)) {
				return `${name}() is not available in expressions. The server will reject this expression.`;
			}
		}
		for (const match of text.matchAll(/(^|[^\w$.)])([A-Za-z_][A-Za-z0-9_]*)\s*\(/g)) {
			const name = match[2];
			if (name === 'if' || name === 'for' || name === 'while' || name === 'function') continue;
			if (!functions.includes(name)) {
				return `${name}() is not available in expressions. The server will reject this expression.`;
			}
		}
	}
	return null;
}
