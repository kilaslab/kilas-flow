/**
 * The ordered, typed rows of an `assignmentCollection` property.
 *
 * A Set node's fields used to be a plain object, which cannot hold two writes
 * to the same field and loses the order the author typed the moment it is
 * saved. These helpers keep the array an array: the panel edits rows and hands
 * the whole list back, so nothing here can turn a list into a string.
 */

/** The types a row may declare. The server refuses anything else. */
export const ASSIGNMENT_TYPES = ['string', 'number', 'boolean', 'array', 'object'] as const;

export type AssignmentType = (typeof ASSIGNMENT_TYPES)[number];

export type Assignment = {
	id: string;
	name: string;
	type: AssignmentType;
	value: unknown;
};

/** The wrapper the document stores, matching what an imported node carries. */
export type AssignmentCollection = { assignments: Assignment[] };

/**
 * Reads a property's value as rows, whatever it currently holds.
 *
 * A node saved before assignments had order is a plain `{name: value}` object,
 * and it has to keep opening. It is read as string rows in a stable order — the
 * only order a map can offer — so editing an old node upgrades it rather than
 * refusing it.
 */
export function readAssignments(value: unknown): Assignment[] {
	if (!value || typeof value !== 'object') return [];
	const wrapper = value as Record<string, unknown>;
	const rows = wrapper.assignments;
	if (Array.isArray(rows)) {
		return rows
			.filter((row): row is Record<string, unknown> => !!row && typeof row === 'object' && !Array.isArray(row))
			.map((row, index) => ({
				id: typeof row.id === 'string' && row.id ? row.id : `row-${index}`,
				name: typeof row.name === 'string' ? row.name : '',
				type: assignmentType(row.type),
				value: row.value
			}));
	}
	return Object.keys(wrapper)
		.sort()
		.map((name, index) => ({ id: `legacy-${index}`, name, type: 'string' as const, value: wrapper[name] }));
}

/** Wraps rows back into the shape the document stores. */
export function writeAssignments(rows: Assignment[]): AssignmentCollection {
	return { assignments: rows };
}

function assignmentType(declared: unknown): AssignmentType {
	return ASSIGNMENT_TYPES.includes(declared as AssignmentType) ? (declared as AssignmentType) : 'string';
}

/** A row id that does not collide with one already in the list. */
export function newAssignmentID(rows: Assignment[]): string {
	let candidate = rows.length;
	const taken = new Set(rows.map((row) => row.id));
	while (taken.has(`row-${candidate}`)) candidate += 1;
	return `row-${candidate}`;
}

/**
 * The value a row starts with when its type changes.
 *
 * Changing a row's type keeps the value when it still fits and resets it when
 * it cannot — retyping `"hello"` as a number and keeping the text would send a
 * string to a field the user has just declared numeric.
 */
export function defaultForType(type: AssignmentType, current: unknown): unknown {
	switch (type) {
		case 'number':
			return typeof current === 'number' ? current : Number(current) || 0;
		case 'boolean':
			return typeof current === 'boolean' ? current : current === 'true';
		case 'array':
			return Array.isArray(current) ? current : [];
		case 'object':
			return current && typeof current === 'object' && !Array.isArray(current) ? current : {};
		default:
			return typeof current === 'string' ? current : current === undefined || current === null ? '' : JSON.stringify(current);
	}
}
