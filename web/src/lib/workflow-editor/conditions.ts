/**
 * Reading and writing a `conditions` property's rows.
 *
 * The stored value has always been an array; the control only ever wrote
 * `[next]`, so the second row was unreachable — a rule that needed two
 * conditions could be imported and could run, but could not be edited without
 * hand-writing JSON.
 */

export type ConditionOperator = 'equals' | 'notEquals' | 'exists' | 'notExists';

export type Condition = {
	field: string;
	operator: ConditionOperator;
	/** Absent for the operators that take no value. */
	value?: unknown;
};

/** The operators that compare against nothing. */
export const VALUELESS_OPERATORS: ConditionOperator[] = ['exists', 'notExists'];

const OPERATORS: ConditionOperator[] = ['equals', 'notEquals', 'exists', 'notExists'];

function isObject(value: unknown): value is Record<string, unknown> {
	return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function operatorOf(value: unknown): ConditionOperator {
	return OPERATORS.includes(value as ConditionOperator) ? (value as ConditionOperator) : 'equals';
}

/** Reads the stored rows, in order, skipping anything that is not one. */
export function readConditions(value: unknown): Condition[] {
	if (!Array.isArray(value)) return [];
	return value.filter(isObject).map((row) => {
		const operator = operatorOf(row.operator);
		const condition: Condition = { field: typeof row.field === 'string' ? row.field : '', operator };
		if (!VALUELESS_OPERATORS.includes(operator)) condition.value = row.value;
		return condition;
	});
}

/** A fresh row, at the defaults the control shows. */
export function newCondition(): Condition {
	return { field: '', operator: 'equals', value: '' };
}

/**
 * Applies a patch to one row, dropping the value when the operator stops
 * taking one — so a rule that becomes "exists" does not keep a stale value
 * that nothing reads and the next reader has to wonder about.
 */
export function updateCondition(rows: Condition[], index: number, patch: Partial<Condition>): Condition[] {
	return rows.map((row, position) => {
		if (position !== index) return row;
		const next: Condition = { ...row, ...patch };
		if (VALUELESS_OPERATORS.includes(next.operator)) delete next.value;
		return next;
	});
}

export function removeCondition(rows: Condition[], index: number): Condition[] {
	return rows.filter((_, position) => position !== index);
}

/**
 * Moves one row by an offset, clamped rather than wrapped.
 *
 * Order is meaningful — the rules are read top to bottom — so this exists at
 * all; wrapping would make the last row jump to the top on a click meant to
 * nudge it down, which is the kind of surprise that costs someone their rule.
 */
export function moveCondition(rows: Condition[], index: number, offset: number): Condition[] {
	const target = index + offset;
	if (index < 0 || index >= rows.length || target < 0 || target >= rows.length) return rows;
	const moved = [...rows];
	const [row] = moved.splice(index, 1);
	moved.splice(target, 0, row);
	return moved;
}
