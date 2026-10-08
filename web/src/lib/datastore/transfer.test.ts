import { describe, expect, it } from 'vitest';

import {
	exportFilename,
	exportRowsURL,
	filenameFromDisposition,
	importRowsURL,
	severityLabel,
	summarizeReport
} from './transfer';

describe('exportRowsURL', () => {
	it('leaves the toggle off the URL when system columns are excluded', () => {
		expect(exportRowsURL('datastore_1', false)).toBe('/api/v1/datastores/datastore_1/rows/export');
	});

	it('carries the toggle when system columns are included', () => {
		expect(exportRowsURL('datastore_1', true)).toBe(
			'/api/v1/datastores/datastore_1/rows/export?includeSystemColumns=true'
		);
	});

	it('encodes the datastore id', () => {
		expect(exportRowsURL('a/b', false)).toBe('/api/v1/datastores/a%2Fb/rows/export');
		expect(importRowsURL('a/b')).toBe('/api/v1/datastores/a%2Fb/rows/import');
	});
});

describe('filenameFromDisposition', () => {
	it('reads a quoted filename', () => {
		expect(filenameFromDisposition('attachment; filename="datastore-abc.csv"')).toBe('datastore-abc.csv');
	});

	it('reads a bare filename', () => {
		expect(filenameFromDisposition('attachment; filename=export.csv')).toBe('export.csv');
	});

	it('falls back to null when no filename rides along', () => {
		expect(filenameFromDisposition(null)).toBeNull();
		expect(filenameFromDisposition('attachment')).toBeNull();
		expect(filenameFromDisposition('attachment; filename=""')).toBeNull();
	});

	it('decodes the RFC 5987 filename* the server sends for a non-ASCII table name', () => {
		expect(filenameFromDisposition("attachment; filename*=utf-8''Donn%C3%A9es%202026.csv")).toBe(
			'Données 2026.csv'
		);
	});

	it('prefers filename* over the plain filename', () => {
		expect(
			filenameFromDisposition(`attachment; filename="Donnees.csv"; filename*=UTF-8''Donn%C3%A9es.csv`)
		).toBe('Données.csv');
	});

	it('unescapes a quoted pair inside a quoted filename', () => {
		expect(filenameFromDisposition('attachment; filename="Q3 \\"final\\" report.csv"')).toBe(
			'Q3 "final" report.csv'
		);
	});
});

describe('exportFilename', () => {
	it('names the file after the table', () => {
		expect(exportFilename('[ux-ops] Leads', 'datastore_1')).toBe('[ux-ops] Leads.csv');
	});

	it('turns path separators into hyphens so the name stays one file', () => {
		expect(exportFilename('north/south\\east', 'datastore_1')).toBe('north-south-east.csv');
	});

	it('falls back to the id when the name is blank', () => {
		expect(exportFilename('   ', 'datastore_1')).toBe('datastore-datastore_1.csv');
	});
});

describe('severityLabel', () => {
	it('renders the workflow report vocabulary', () => {
		expect(severityLabel('blocking')).toBe('Blocking');
		expect(severityLabel('lossy')).toBe('Lossy');
		expect(severityLabel('dropped')).toBe('Dropped');
	});

	it('passes unknown severities through rather than blanking them', () => {
		expect(severityLabel('warning')).toBe('warning');
	});
});

describe('summarizeReport', () => {
	it('counts rows with singular and plural', () => {
		expect(summarizeReport({ inserted: 1, skipped: 0, failed: [] })).toBe('1 row imported');
		expect(summarizeReport({ inserted: 3, skipped: 0, failed: [] })).toBe('3 rows imported');
	});

	it('notes skipped blank lines', () => {
		expect(summarizeReport({ inserted: 2, skipped: 1, failed: [] })).toBe(
			'2 rows imported, 1 blank line skipped'
		);
	});
});
