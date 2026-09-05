/**
 * Fixed and expression parameter values.
 *
 * A parameter is fixed unless it carries the explicit marker
 * `{"mode":"expression","value":"…"}`. Requiring the marker is what lets a
 * fixed string containing `{{ }}` stay data — the server applies exactly the
 * same rule, so what the editor shows and what the engine evaluates cannot
 * drift apart.
 */

export type ParameterMode = 'fixed' | 'expression';

export type ExpressionValue = { mode: 'expression'; value: string };

export function isExpression(value: unknown): value is ExpressionValue {
	if (value === null || typeof value !== 'object' || Array.isArray(value)) return false;
	const object = value as Record<string, unknown>;
	return object.mode === 'expression' && typeof object.value === 'string';
}

export function parameterMode(value: unknown): ParameterMode {
	return isExpression(value) ? 'expression' : 'fixed';
}

export function expressionTemplate(value: unknown): string {
	return isExpression(value) ? value.value : '';
}

/** Converts a fixed value into an expression, keeping what the user typed. */
export function asExpression(value: unknown): ExpressionValue {
	if (isExpression(value)) return value;
	if (value === undefined || value === null) return { mode: 'expression', value: '' };
	return { mode: 'expression', value: typeof value === 'string' ? value : String(value) };
}

/** Converts an expression back to a fixed value, keeping the template text. */
export function asFixed(value: unknown): string {
	return isExpression(value) ? value.value : typeof value === 'string' ? value : value === undefined || value === null ? '' : String(value);
}
