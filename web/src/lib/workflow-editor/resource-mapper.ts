import type { MapperColumn, PropertyDefinition } from '$lib/api/generated/models';

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

/** Reads a stored mapping, defaulting to the mode the property supports. */
export function readMapping(property: PropertyDefinition, value: unknown): Mapping {
	const fallback = property.mapper?.supportsAutoMap ? MAPPING_AUTO : MAPPING_MANUAL;
	if (!isObject(value)) return { mappingMode: fallback, value: {}, matchingColumns: [] };
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
