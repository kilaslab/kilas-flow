import type { PropertyDefinition, Visibility, Condition } from '$lib/api/generated/models';

/**
 * Whether a property is shown, by exactly the rule the server evaluates.
 *
 * This is a port, not a reimplementation: `internal/property/visibility.go` and
 * this file are checked against the same fixture, because two implementations
 * of one rule drift — that is not a risk, it is a certainty — and the fixture is
 * the only thing that keeps the panel and the compiler agreeing about whether a
 * workflow can be saved.
 *
 * A hidden parent does not suppress its children: every property is evaluated
 * independently against the node's stored parameters. n8n behaves the same way,
 * and a cascade would hide parameters an imported workflow legitimately sets.
 */
export function visible(
	rule: Visibility | undefined,
	parameters: Record<string, unknown>,
	typeVersion = ''
): boolean {
	const show = rule?.show ?? [];
	const hide = rule?.hide ?? [];
	if (show.length === 0 && hide.length === 0) return true;

	for (const condition of show) {
		const [value, present] = controlling(condition.key, parameters, typeVersion);
		// The escape hatch, and it is in the show branch only. If the
		// controlling parameter holds an expression, the dependent property is
		// always shown: nothing can know at edit time what it will evaluate to,
		// and hiding a field the user may need is worse than showing one they
		// may not.
		if (present && isExpressionMarker(value)) continue;
		if (!matches(condition, value, present)) return false;
	}

	for (const condition of hide) {
		const [value, present] = controlling(condition.key, parameters, typeVersion);
		// An absent value never hides.
		if (!present || value === null || value === undefined) continue;
		if (matches(condition, value, present)) return false;
	}
	return true;
}

/**
 * Fills in the parameters a node never stored.
 *
 * A property the user never touched has its declared default, and every
 * visibility rule has to be evaluated against that. Without it, a rule reading
 * `mode` on a node whose `mode` was never written sees nothing and hides a
 * field the user is looking at.
 */
export function withDefaults(
	properties: PropertyDefinition[],
	parameters: Record<string, unknown>
): Record<string, unknown> {
	const filled: Record<string, unknown> = { ...parameters };
	for (const property of properties) {
		if (property.default === undefined || property.default === null) continue;
		if (!(property.key in filled)) filled[property.key] = property.default;
	}
	return filled;
}

/** Whether one property is shown, honouring the visibleWhen shorthand. */
export function propertyVisible(
	property: PropertyDefinition,
	parameters: Record<string, unknown>,
	typeVersion = ''
): boolean {
	const rule = property.displayOptions;
	if (rule && ((rule.show?.length ?? 0) > 0 || (rule.hide?.length ?? 0) > 0)) {
		return visible(rule, parameters, typeVersion);
	}
	const shorthand = property.visibleWhen ?? [];
	if (shorthand.length === 0) return true;
	return visible(
		{ show: shorthand.map((condition) => ({ key: condition.key, values: [condition.equals] })) },
		parameters,
		typeVersion
	);
}

function controlling(
	key: string,
	parameters: Record<string, unknown>,
	typeVersion: string
): [unknown, boolean] {
	// The pseudo-key gating on the node's type version.
	if (key === '@version') return [typeVersion, typeVersion !== ''];
	return [parameters[key], Object.prototype.hasOwnProperty.call(parameters, key)];
}

function isExpressionMarker(value: unknown): boolean {
	if (typeof value !== 'object' || value === null || Array.isArray(value)) return false;
	const record = value as Record<string, unknown>;
	return record.mode === 'expression' && typeof record.value === 'string';
}

function matches(condition: Condition, value: unknown, present: boolean): boolean {
	const operator = condition.operator || 'eq';
	const operands = condition.values ?? [];

	if (operator === 'exists') return present && value !== null && value !== undefined;
	if (operator === 'between') {
		if (operands.length !== 2) return false;
		return compare(value, operands[0]) >= 0 && compare(value, operands[1]) <= 0;
	}
	if (operator === 'not') {
		// Negation is over the whole list: "not one of these".
		return !operands.some((operand) => equal(value, operand));
	}
	return operands.some((operand) => apply(operator, value, operand));
}

function apply(operator: string, value: unknown, operand: unknown): boolean {
	switch (operator) {
		case 'eq':
			return equal(value, operand);
		case 'gt':
			return compare(value, operand) > 0;
		case 'gte':
			return compare(value, operand) >= 0;
		case 'lt':
			return compare(value, operand) < 0;
		case 'lte':
			return compare(value, operand) <= 0;
		case 'startsWith':
			return text(value).startsWith(text(operand));
		case 'endsWith':
			return text(value).endsWith(text(operand));
		case 'includes':
			if (Array.isArray(value)) return value.some((entry) => equal(entry, operand));
			return text(value).includes(text(operand));
		case 'regex':
			try {
				return new RegExp(text(operand)).test(text(value));
			} catch {
				return false;
			}
		default:
			return false;
	}
}

/**
 * Compares by value, not by identity.
 *
 * This is what the previous `===` got wrong: it never matched an object or an
 * array, so a condition on anything but a primitive was silently always false.
 * A JSON number and its string form compare equal, because they are the same
 * value to a user reading a dropdown.
 */
function equal(value: unknown, operand: unknown): boolean {
	const left = numeric(value);
	const right = numeric(operand);
	if (left !== null && right !== null) return left === right;
	if (left !== null || right !== null) return text(value) === text(operand);
	if (typeof value === 'object' && typeof operand === 'object') {
		return JSON.stringify(sorted(value)) === JSON.stringify(sorted(operand));
	}
	return value === operand;
}

function compare(value: unknown, operand: unknown): number {
	const left = numeric(value);
	const right = numeric(operand);
	if (left === null || right === null) {
		const a = text(value);
		const b = text(operand);
		return a < b ? -1 : a > b ? 1 : 0;
	}
	return left < right ? -1 : left > right ? 1 : 0;
}

function numeric(value: unknown): number | null {
	if (typeof value === 'number' && Number.isFinite(value)) return value;
	if (typeof value === 'string' && value.trim() !== '') {
		const parsed = Number(value);
		return Number.isFinite(parsed) ? parsed : null;
	}
	return null;
}

function text(value: unknown): string {
	if (value === null || value === undefined) return '';
	if (typeof value === 'string') return value;
	return String(value);
}

/** Key-sorted so two equal objects stringify identically. */
function sorted(value: unknown): unknown {
	if (Array.isArray(value)) return value.map(sorted);
	if (typeof value !== 'object' || value === null) return value;
	const record = value as Record<string, unknown>;
	const result: Record<string, unknown> = {};
	for (const key of Object.keys(record).sort()) result[key] = sorted(record[key]);
	return result;
}
