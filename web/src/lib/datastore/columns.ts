import type { DatastoreColumnResource } from '$lib/api/generated/models';

/**
 * The datastore grid's column model.
 *
 * A fresh datastore already carries the three system columns `id`,
 * `createdAt` and `updatedAt` (design-refs/n8n-v2 shot 20); user columns
 * render between `id` and the timestamps after reload (shot 23). The grid
 * here is the shared table primitive, not AG Grid: KilasFlow has no grid
 * library, and one page does not justify vendoring one (shot 24). Header
 * rename is therefore an inline input swap rather than AG Grid's
 * `inline-editable-area`.
 */

/** Every datastore carries these before any user column exists. */
export const SYSTEM_COLUMNS = ['id', 'createdAt', 'updatedAt'] as const;

/**
 * The add-column types, verbatim from the n8n dropdown (shot 22): `string`,
 * `number`, `boolean`, `datetime`. `datetime` is the dialog label; the wire
 * carries `date` and the server normalises the alias, so this module maps
 * between the two at exactly one boundary.
 */
export const COLUMN_TYPES = ['string', 'number', 'boolean', 'datetime'] as const;

export type ColumnTypeLabel = (typeof COLUMN_TYPES)[number];

export interface GridColumn {
	name: string;
	/** The wire type (`date`, not `datetime`) for user columns; '' for system ones the catalogue never names. */
	type: string;
	system: boolean;
}

export function isSystemColumn(name: string): boolean {
	return (SYSTEM_COLUMNS as readonly string[]).includes(name);
}

/** The dialog label for a wire type: the catalogue stores `date`. */
export function displayType(wire: string): string {
	return wire === 'date' ? 'datetime' : wire;
}

/** The wire value for a dialog label: the UI offers `datetime`. */
export function wireType(label: string): string {
	return label === 'datetime' ? 'date' : label;
}

/**
 * The grid's header row: `id`, then the catalogue's user columns in order,
 * then the timestamps. Unknown system columns keep their catalogue position;
 * only the three known ones are pinned, so a future system column cannot
 * silently land in the user section.
 */
export function gridColumns(userColumns: DatastoreColumnResource[]): GridColumn[] {
	const users = userColumns.filter((column) => !isSystemColumn(column.name));
	return [
		{ name: 'id', type: '', system: true },
		...users.map((column) => ({ name: column.name, type: column.type, system: false })),
		{ name: 'createdAt', type: '', system: true },
		{ name: 'updatedAt', type: '', system: true }
	];
}

export type CoercedValue = { ok: true; value: unknown } | { ok: false; error: string };

/**
 * Coerces one row-editor string against the stored column type, because every
 * editor input produces a string while the API expects typed JSON. A numeric
 * column sent `"3"` matches nothing on SQLite and is rejected on PostgreSQL;
 * the failure surfaces as a correct-looking empty grid, so the refusal
 * happens here, in the dialog, naming the column.
 */
export function coerceValue(wire: string, column: string, raw: string): CoercedValue {
	const text = raw.trim();
	switch (wire) {
		case 'number': {
			if (text === '') return { ok: false, error: `${column} needs a number.` };
			const parsed = Number(text);
			if (!Number.isFinite(parsed)) return { ok: false, error: `${column} needs a number.` };
			return { ok: true, value: parsed };
		}
		case 'boolean': {
			if (/^(true|1|yes)$/i.test(text)) return { ok: true, value: true };
			if (/^(false|0|no)$/i.test(text)) return { ok: true, value: false };
			return { ok: false, error: `${column} needs true or false.` };
		}
		case 'date':
		case 'datetime': {
			if (text === '') return { ok: false, error: `${column} needs a date and time.` };
			const parsed = Date.parse(text);
			if (Number.isNaN(parsed)) return { ok: false, error: `${column} needs a date and time.` };
			return { ok: true, value: new Date(parsed).toISOString() };
		}
		default:
			return { ok: true, value: raw };
	}
}
