import * as m from '$lib/paraglide/messages.js';
import { apiDownload, apiFetch } from '$lib/api/http';

/**
 * CSV transfer for one datastore: the download and upload the detail page's
 * transfer dialog wires to. The export never goes through the generated
 * client — `apiFetch` answers every non-204 response with `response.json()`,
 * which throws on `text/csv` bytes — so both directions call the transport
 * directly against the same paths the OpenAPI document carries.
 */

export interface CSVImportIssue {
	line: number;
	column: string;
	severity: string;
	reason: string;
}

export interface CSVImportReport {
	inserted: number;
	skipped: number;
	failed: CSVImportIssue[];
}

/** The export download URL: the toggle stays off the URL when excluded, which is also the server default. */
export function exportRowsURL(id: string, includeSystemColumns: boolean): string {
	const base = `/api/v1/datastores/${encodeURIComponent(id)}/rows/export`;
	return includeSystemColumns ? `${base}?includeSystemColumns=true` : base;
}

/** The import upload URL. */
export function importRowsURL(id: string): string {
	return `/api/v1/datastores/${encodeURIComponent(id)}/rows/import`;
}

/**
 * The filename the server suggests in Content-Disposition, falling back to
 * null so the caller names the file itself. The server names the file after
 * the table, so a non-ASCII name arrives as RFC 5987 `filename*`, which wins
 * over a plain `filename` the way it does in a browser; a quoted filename
 * may carry backslash-escaped quotes. Anything else is not a filename.
 */
export function filenameFromDisposition(header: string | null): string | null {
	if (!header) return null;
	const extended = /filename\*\s*=\s*([^']*)'[^']*'([^;]*)/i.exec(header);
	if (extended) {
		try {
			const decoded = decodeURIComponent(extended[2].trim()).trim();
			if (decoded !== '') return decoded;
		} catch {
			// A malformed percent sequence falls through to the plain filename.
		}
	}
	const match = /filename\s*=\s*"((?:[^"\\]|\\.)*)"|filename\s*=\s*([^;]*)/i.exec(header);
	const quoted = match?.[1]?.replace(/\\(.)/g, '$1');
	const name = (quoted ?? match?.[2] ?? '').trim();
	return name === '' ? null : name;
}

/**
 * The download name built from the table's own name, for when the server
 * suggests none. It follows the server's rule: path separators become a
 * hyphen, and a blank name falls back to the id.
 */
export function exportFilename(name: string, id: string): string {
	const base = name.replace(/[/\\\u0000-\u001f\u007f]/g, '-').trim();
	return `${base === '' ? `datastore-${id}` : base}.csv`;
}

/** Saves downloaded bytes through a temporary anchor: the SPA's first download. */
export function saveBlob(blob: Blob, filename: string): void {
	const url = URL.createObjectURL(blob);
	const anchor = document.createElement('a');
	anchor.href = url;
	anchor.download = filename;
	document.body.appendChild(anchor);
	anchor.click();
	anchor.remove();
	URL.revokeObjectURL(url);
}

/** Downloads the export and saves it, returning the filename it saved under. */
export async function downloadExport(
	id: string,
	name: string,
	includeSystemColumns: boolean
): Promise<{ filename: string }> {
	const result = await apiDownload(exportRowsURL(id, includeSystemColumns));
	const filename =
		filenameFromDisposition(result.headers.get('content-disposition')) ?? exportFilename(name, id);
	saveBlob(result.data, filename);
	return { filename };
}

/** Uploads a CSV file and returns the server's import report. */
export async function uploadImport(id: string, file: Blob): Promise<CSVImportReport> {
	const response = await apiFetch<{ data: CSVImportReport; status: number; headers: Headers }>(
		importRowsURL(id),
		{ method: 'POST', headers: { 'Content-Type': 'text/csv' }, body: file }
	);
	return response.data;
}

/**
 * The display word for a report severity. The words match the workflow
 * import report's vocabulary — Blocking, Lossy, Dropped — so the two
 * reports read as one product even though a CSV import only ever emits
 * blocking issues today.
 */
export function severityLabel(severity: string): string {
	switch (severity) {
		case 'blocking':
			return m.datastores_severity_blocking();
		case 'lossy':
			return m.datastores_severity_lossy();
		case 'dropped':
			return m.datastores_severity_dropped();
		default:
			return severity;
	}
}

/** One sentence for a successful import: rows landed, blank lines noted. */
export function summarizeReport(report: CSVImportReport): string {
	return report.skipped > 0
		? m.datastores_imported_rows_with_blanks({ rows: report.inserted, blanks: report.skipped })
		: m.datastores_imported_rows({ rows: report.inserted });
}
