/**
 * Reading and writing a `conditions` property's rows.
 *
 * The stored value is n8n's filter object
 * `{combinator, conditions: [{leftValue, operator: {type, operation}, rightValue}], options}`
 * — the shape the runtime executes and the importer writes. A bare array of
 * legacy `{field, operator, value}` rows (what this editor used to write) is
 * still read, so old documents keep opening.
 */

export type ConditionOperator =
	| 'equals'
	| 'notEquals'
	| 'contains'
	| 'notContains'
	| 'startsWith'
	| 'notStartsWith'
	| 'endsWith'
	| 'notEndsWith'
	| 'regex'
	| 'notRegex'
	| 'empty'
	| 'notEmpty'
	| 'exists'
	| 'notExists'
	| 'gt'
	| 'gte'
	| 'lt'
	| 'lte'
	| 'true'
	| 'false'
	| 'after'
	| 'before';

export type ConditionValueType = 'string' | 'number' | 'boolean' | 'dateTime' | 'array' | 'object';

export type Combinator = 'and' | 'or';

export type Condition = {
	leftValue: unknown;
	operator: { type: ConditionValueType; operation: ConditionOperator };
	rightValue?: unknown;
};

export type FilterValue = {
	combinator: Combinator;
	conditions: Condition[];
	options: { caseSensitive: boolean };
};

/** The operators that compare against nothing. */
export const VALUELESS_OPERATORS: ConditionOperator[] = ['exists', 'notExists', 'empty', 'notEmpty', 'true', 'false'];

const OPERATOR_TYPES: Record<ConditionOperator, ConditionValueType> = {
	equals: 'string',
	notEquals: 'string',
	contains: 'string',
	notContains: 'string',
	startsWith: 'string',
	notStartsWith: 'string',
	endsWith: 'string',
	notEndsWith: 'string',
	regex: 'string',
	notRegex: 'string',
	empty: 'string',
	notEmpty: 'string',
	exists: 'string',
	notExists: 'string',
	gt: 'number',
	gte: 'number',
	lt: 'number',
	lte: 'number',
	true: 'boolean',
	false: 'boolean',
	after: 'dateTime',
	before: 'dateTime'
};

const KNOWN_OPERATIONS = new Set<string>(Object.keys(OPERATOR_TYPES));

/**
 * Legacy aliases folded to the canonical vocabulary the evaluator runs.
 * n8n v1 spelled the numeric comparisons larger/largerEqual/smaller/
 * smallerEqual; the Go evaluator folds both spellings at the comparison
 * (numberOperation), so the editor canonicalises at the read boundary and
 * documents never carry the old names after a save.
 */
const OPERATOR_ALIASES: Record<string, ConditionOperator> = {
	larger: 'gt',
	largerEqual: 'gte',
	smaller: 'lt',
	smallerEqual: 'lte'
};

function isObject(value: unknown): value is Record<string, unknown> {
	return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function operatorOf(type: unknown, operation: unknown): { type: ConditionValueType; operation: ConditionOperator } {
	const name = String(operation);
	const canonical = OPERATOR_ALIASES[name] ?? (KNOWN_OPERATIONS.has(name) ? (name as ConditionOperator) : 'equals');
	const declared = type === 'string' || type === 'number' || type === 'boolean' || type === 'dateTime' || type === 'array' || type === 'object' ? (type as ConditionValueType) : null;
	return { type: declared ?? OPERATOR_TYPES[canonical], operation: canonical };
}

/**
 * The type family an operation compares in. Switching a row from `equals` to
 * `gt` while keeping `string` would send the comparison down the string
 * branch, which refuses it — so the operator picker coerces the type when the
 * operation demands its own family, and leaves it alone otherwise.
 */
export function typeForOperation(operation: ConditionOperator): ConditionValueType | null {
	switch (operation) {
		case 'gt':
		case 'gte':
		case 'lt':
		case 'lte':
			return 'number';
		case 'after':
		case 'before':
			return 'dateTime';
		case 'true':
		case 'false':
			return 'boolean';
		default:
			return null;
	}
}

function combinatorOf(value: unknown): Combinator {
	return value === 'or' ? 'or' : 'and';
}

/** Reads the stored filter, whatever shape it currently holds. */
export function readFilterValue(value: unknown): FilterValue {
	if (isObject(value) && Array.isArray(value.conditions)) {
		const options = isObject(value.options) ? value.options : {};
		return {
			combinator: combinatorOf(value.combinator),
			conditions: value.conditions.filter(isObject).map((row) => readRichRow(row)),
			options: { caseSensitive: typeof options.caseSensitive === 'boolean' ? options.caseSensitive : true }
		};
	}
	return { combinator: 'and', conditions: readConditions(value), options: { caseSensitive: true } };
}

function readRichRow(row: Record<string, unknown>): Condition {
	const operator = isObject(row.operator) ? row.operator : {};
	const parsed = operatorOf(operator.type, operator.operation);
	const condition: Condition = { leftValue: row.leftValue ?? '', operator: parsed };
	if (!VALUELESS_OPERATORS.includes(parsed.operation)) condition.rightValue = row.rightValue ?? '';
	return condition;
}

/** Writes rows back in the rich shape the runtime executes. */
export function writeFilterValue(filter: FilterValue): Record<string, unknown> {
	return {
		combinator: filter.combinator,
		conditions: filter.conditions.map((row) => {
			const entry: Record<string, unknown> = {
				leftValue: row.leftValue,
				operator: { type: row.operator.type, operation: row.operator.operation }
			};
			if (!VALUELESS_OPERATORS.includes(row.operator.operation)) entry.rightValue = row.rightValue ?? '';
			return entry;
		}),
		options: { caseSensitive: filter.options.caseSensitive }
	};
}

/** Reads the stored rows, in order, skipping anything that is not one. */
export function readConditions(value: unknown): Condition[] {
	if (!Array.isArray(value)) return [];
	return value.filter(isObject).map((row) => {
		if (isObject(row.operator)) return readRichRow(row);
		const operator = operatorOf(undefined, row.operator);
		const condition: Condition = { leftValue: typeof row.field === 'string' ? row.field : '', operator };
		if (!VALUELESS_OPERATORS.includes(operator.operation)) condition.rightValue = row.value;
		return condition;
	});
}

/** A fresh row, at the defaults the control shows. */
export function newCondition(): Condition {
	return { leftValue: '', operator: { type: 'string', operation: 'equals' }, rightValue: '' };
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
		if (next.operator && VALUELESS_OPERATORS.includes(next.operator.operation)) delete next.rightValue;
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
