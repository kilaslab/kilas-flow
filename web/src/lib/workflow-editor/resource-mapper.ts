import type { MapperColumn, PropertyDefinition } from '$lib/api/generated/models';
import { isExpression, type ExpressionValue } from './parameter';

/**
 * Reading and writing a resource mapper — the control that types the columns
 * inside whatever a resource locator picked.
 *
 * The stored shape is n8n's ResourceMapperValue, so an imported mapper reads
 * back without translation and an export is not lossy.
 */

export const MAPPING_AUTO = 'autoMapInputData';
export const MAPPING_MANUAL = 'defineBelow';

export type Mapping = {
	mappingMode: string;
	value: Record<string, unknown>;
	matchingColumns: string[];
	/**
	 * The schema copy persisted alongside the choices.
	 *
	 * n8n persists it and so does this, because an imported mapper carries one
	 * and dropping it would make export lossy. It is display data only — the
	 * executor re-reads the live schema, since a copy taken when the node was
	 * last opened has no authority over a table that has changed since.
	 */
	schema?: MapperColumn[];
};

function isObject(value: unknown): value is Record<string, unknown> {
	return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/**
 * Whether an object is a stored mapper. The server's rule exactly
 * (property.ReadMapping): its mappingMode is a non-empty string. Any other key
 * the mapper uses (`value`, `schema`, `matchingColumns`) can also be the name of
 * a legacy fixed input, so none of them says anything on its own.
 */
function isMapperShaped(value: Record<string, unknown>): boolean {
	return typeof value.mappingMode === 'string' && value.mappingMode !== '';
}

/**
 * Whether a stored value is one expression standing for the whole mapper.
 *
 * Which columns it produces is only known when it runs, so no per-column editor
 * can show it; the form says so instead of writing an empty mapping over it.
 */
export function isWholeExpression(value: unknown): boolean {
	return isExpression(value);
}

/**
 * Reads a stored mapping, defaulting to the mode the property supports.
 *
 * A mapper that offers the model switch also reads the plain object its
 * property held before it was a mapper — `{unit: "F"}`, the keys being the
 * columns — instead of treating it as empty. Reading it as empty made the next
 * edit write an empty mapping back, silently deleting every static input the
 * author had set. It is done for any object that is not a stored mapper (the
 * server's own test, below) and not an expression marker; and only for a mapper
 * that opted in, so every other mapper reads exactly as it did.
 */
export function readMapping(property: PropertyDefinition, value: unknown): Mapping {
	const fallback = property.mapper?.supportsAutoMap ? MAPPING_AUTO : MAPPING_MANUAL;
	if (!isObject(value)) return { mappingMode: fallback, value: {}, matchingColumns: [] };
	if (property.mapper?.modelDefinable && !isWholeExpression(value) && !isMapperShaped(value)) {
		return { mappingMode: MAPPING_MANUAL, value: { ...value }, matchingColumns: [] };
	}
	return {
		mappingMode: typeof value.mappingMode === 'string' && value.mappingMode !== '' ? value.mappingMode : fallback,
		value: isObject(value.value) ? value.value : {},
		matchingColumns: Array.isArray(value.matchingColumns)
			? value.matchingColumns.filter((column): column is string => typeof column === 'string')
			: [],
		schema: Array.isArray(value.schema) ? (value.schema as MapperColumn[]) : undefined
	};
}

/** Renders a mapping back into its stored shape. */
export function writeMapping(mapping: Mapping, schema: MapperColumn[]): Record<string, unknown> {
	const stored: Record<string, unknown> = {
		mappingMode: mapping.mappingMode,
		value: mapping.value,
		matchingColumns: mapping.matchingColumns
	};
	// The live schema when there is one, the stored copy otherwise: a mapper
	// whose columns have not loaded yet must not have its copy blanked on the
	// next keystroke.
	const persisted = schema.length > 0 ? schema : (mapping.schema ?? []);
	if (persisted.length > 0) stored.schema = persisted;
	return stored;
}

/** The columns a user may set a value for. */
export function writableColumns(schema: MapperColumn[], mapping: Mapping): MapperColumn[] {
	// A read-only column is filled in by the database, and a matching column
	// identifies the row rather than supplying it — neither takes a value.
	return schema.filter((column) => !column.readOnly && !mapping.matchingColumns.includes(column.id));
}

/** The columns that may be used to match rows. */
export function matchableColumns(schema: MapperColumn[]): MapperColumn[] {
	return schema.filter((column) => column.canBeUsedToMatch);
}

/**
 * The matching columns a fresh mapping starts with.
 *
 * Preselecting the schema's default match — a primary key, usually — is what
 * makes an update usable without the user first working out which column
 * identifies a row.
 */
export function defaultMatchingColumns(schema: MapperColumn[]): string[] {
	return schema.filter((column) => column.defaultMatch && column.canBeUsedToMatch).map((column) => column.id);
}

/** The control a column's type implies. */
export function columnControl(type: string | undefined): 'string' | 'number' | 'boolean' | 'dateTime' | 'json' {
	switch (type) {
		case 'number':
			return 'number';
		case 'boolean':
			return 'boolean';
		case 'dateTime':
			return 'dateTime';
		case 'object':
		case 'array':
			return 'json';
		default:
			// Anything unrecognised renders as text, with a warning beside it.
			// Dropping the column from the form would read as "this table has
			// no such column", which is a worse lie than "we are not sure what
			// this one is".
			return 'string';
	}
}

const KNOWN_TYPES = ['string', 'number', 'boolean', 'dateTime', 'object', 'array'];

/** Whether this build has a control matched to the column's declared type. */
export function knownColumnType(type: string | undefined): boolean {
	return type === undefined || type === '' || KNOWN_TYPES.includes(type);
}

/*
 * Leaving a column to the model.
 *
 * A mapper that opts in (`mapper.modelDefinable`) lets each column be either
 * the model's or fixed. The model's column is stored as one whole `$fromAI`
 * call, which is what the tool already turns into a typed argument the model
 * fills in; a fixed column is whatever the author wrote. A column nothing was
 * written for is the model's too, so a freshly picked workflow works at once.
 * The server reads the same rule (ai.WholeFromAICall, nodes.WorkflowToolInputs),
 * so what this form shows is what the model is offered.
 */

export type ColumnSource = 'model' | 'fixed';

/** The type name a `$fromAI` call can carry for a column. */
export function fromAIType(columnType: string | undefined): 'string' | 'number' | 'boolean' | 'json' {
	switch (columnType) {
		case 'string':
		case 'number':
		case 'boolean':
			return columnType;
		default:
			// $fromAI has four types. An object, an array and an untyped column
			// are json; the server refines the first two from the declared type.
			return 'json';
	}
}

/** The stored value that hands a column to the model. */
export function modelDefinedValue(column: Pick<MapperColumn, 'id' | 'type'>): ExpressionValue {
	const id = column.id.replace(/\\/g, '\\\\').replace(/'/g, "\\'");
	return { mode: 'expression', value: `{{ $fromAI('${id}', '', '${fromAIType(column.type)}') }}` };
}

/**
 * Whether a template is nothing but one `$fromAI(…)` call, in any quote style
 * or case. A call inside text or arithmetic is still offered to the model, but
 * it is an expression the author wrote, not the model's value.
 */
function isWholeFromAICall(template: string): boolean {
	const trimmed = template.trim();
	if (!trimmed.startsWith('{{') || !trimmed.endsWith('}}')) return false;
	const inner = trimmed.slice(2, -2).trim();
	const opener = /^\$fromai\s*\(/i.exec(inner);
	if (!opener) return false;
	let depth = 0;
	let quote = '';
	for (let index = opener[0].length - 1; index < inner.length; index++) {
		const char = inner[index];
		if (quote !== '') {
			if (char === '\\') index++;
			else if (char === quote) quote = '';
			continue;
		}
		if (char === "'" || char === '"' || char === '`') quote = char;
		else if (char === '(') depth++;
		else if (char === ')' && --depth === 0) return index === inner.length - 1;
	}
	return false;
}

/** Whether the model fills in this column when it calls the tool. */
export function isModelDefined(mapping: Mapping, id: string): boolean {
	if (!Object.hasOwn(mapping.value, id)) return true;
	const stored = mapping.value[id];
	return isExpression(stored) && isWholeFromAICall(stored.value);
}

/**
 * Switches one column between the model's and fixed, leaving the others as
 * they are. A column that becomes fixed starts empty — `false` for a boolean,
 * which has no empty — rather than keeping the call it had.
 */
export function setColumnSource(mapping: Mapping, column: MapperColumn, source: ColumnSource): Mapping {
	const next = source === 'model' ? modelDefinedValue(column) : column.type === 'boolean' ? false : '';
	return { ...mapping, value: { ...mapping.value, [column.id]: next } };
}

/** Switches a column to the other source, which is what clicking its switch does. */
export function flipColumnSource(mapping: Mapping, column: MapperColumn): Mapping {
	return setColumnSource(mapping, column, isModelDefined(mapping, column.id) ? 'fixed' : 'model');
}

/** What a column's text box shows: an expression as its template. */
export function columnText(value: unknown): string {
	if (isExpression(value)) return value.value;
	if (value === undefined || value === null) return '';
	return typeof value === 'string' ? value : JSON.stringify(value);
}

/**
 * The stored value after the author types into a column's text box.
 *
 * An expression stays one, with the typed text as its template: writing the
 * text over the marker turned an expression into plain text with the first
 * keystroke. That holds for every mapper.
 *
 * The rest is for a mapper that opted into the model switch only (`modelDefinable`),
 * so every other mapper (a table's columns) stores what it always did. Typing
 * `{{` makes a fixed column an expression, as an assignment row does. A number
 * column keeps what is typed until it parses, and clearing it is empty rather
 * than zero. Elsewhere typed braces stay literal text and a number column stores
 * Number(text).
 */
export function editColumn(
	previous: unknown,
	text: string,
	control: ReturnType<typeof columnControl>,
	modelDefinable = false
): unknown {
	if (isExpression(previous)) return { mode: 'expression', value: text };
	if (!modelDefinable) return control === 'number' ? Number(text) : text;
	if (text.includes('{{')) return { mode: 'expression', value: text };
	if (control === 'number') {
		if (text.trim() === '') return '';
		const parsed = Number(text);
		return Number.isNaN(parsed) ? text : parsed;
	}
	return text;
}
