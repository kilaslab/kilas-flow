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

/**
 * Whether a fixed value needs a multi-line control.
 *
 * Browsers strip line breaks from an `<input>`'s value, so a value that
 * arrived multi-line must never be bound to one: the first keystroke would
 * write the flattened text back and silently destroy expressions, JSON
 * bodies and sticky-note markdown. `rows` is the definition's own request;
 * a value carrying `\n` upgrades itself even when the definition asks for
 * none, which is also what keeps old nodes and Go-side defaults safe
 * without touching node definitions owned by other slices.
 */
export function needsMultiline(value: unknown, rows?: number): boolean {
	if ((rows ?? 0) > 1) return true;
	return typeof value === 'string' && value.includes('\n');
}
