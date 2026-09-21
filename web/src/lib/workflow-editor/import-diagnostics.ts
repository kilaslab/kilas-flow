import * as m from '$lib/paraglide/messages.js';
import { getContext, setContext } from 'svelte';

import type { ImportIssue, ImportIssueSeverity } from '$lib/api/generated/models';

/**
 * The import report for the revision the canvas is showing.
 *
 * An n8n import keeps a report with the revision it created (blocking, lossy
 * and dropped entries, each naming the node or field it is about), and the
 * editor reads that report back over the API rather than remembering the
 * import response: the response died with the dialog, the stored report
 * survives a reload and reaches whoever opens the workflow next.
 *
 * Nodes are rendered by Svelte Flow rather than by the editor's own markup, so
 * there is no prop path between the page and a tile. Context carries the
 * surface a node needs, the same way `canvas-actions` carries what it can ask
 * the editor to do; the value is a function so a node reads the current report
 * at call time and cannot capture a stale one.
 */
export type ImportDiagnosticsSurface = {
	issues: readonly ImportIssue[];
	/**
	 * Opens the report. It shows the whole thing rather than one node's
	 * entries: its first line is whether the workflow can activate at all,
	 * and a filtered view would answer that question about a subset.
	 */
	openReport: () => void;
};

const KEY = Symbol('kilasflow.import-diagnostics');

export function setImportDiagnostics(surface: () => ImportDiagnosticsSurface): void {
	setContext(KEY, surface);
}

/** Undefined on a canvas whose host reads no report, such as a replay. */
export function getImportDiagnostics(): (() => ImportDiagnosticsSurface) | undefined {
	return getContext<(() => ImportDiagnosticsSurface) | undefined>(KEY);
}

/**
 * Severities worst first.
 *
 * Blocking decides whether the workflow runs at all, so it is what a badge
 * leads with; lossy and dropped only decide how closely the import matched.
 */
export const DIAGNOSTIC_SEVERITIES: readonly ImportIssueSeverity[] = ['blocking', 'lossy', 'dropped'];

export const DIAGNOSTIC_SEVERITY_LABELS: Record<ImportIssueSeverity, string> = {
	blocking: 'Blocking',
	lossy: 'Lossy',
	dropped: 'Dropped'
};

export type ImportDiagnosticsSummary = {
	total: number;
	blocking: number;
	lossy: number;
	dropped: number;
};

export function summarizeDiagnostics(issues: readonly ImportIssue[]): ImportDiagnosticsSummary {
	const counts: Record<ImportIssueSeverity, number> = { blocking: 0, lossy: 0, dropped: 0 };
	for (const issue of issues) {
		if (issue.severity in counts) counts[issue.severity] += 1;
	}
	return { total: issues.length, ...counts };
}

/**
 * The severity a node's badge leads with: the worst one it carries.
 */
export function worstSeverity(issues: readonly ImportIssue[]): ImportIssueSeverity | null {
	for (const severity of DIAGNOSTIC_SEVERITIES) {
		if (issues.some((issue) => issue.severity === severity)) return severity;
	}
	return null;
}

/**
 * One sentence naming what a node or a report carries: the reasons, not just a
 * count, because the point of the badge and of the report button is that the
 * detail is available without opening anything.
 */
export function diagnosticSummaryLabel(issues: readonly ImportIssue[]): string {
	const { blocking, lossy, dropped } = summarizeDiagnostics(issues);
	const counts: Record<ImportIssueSeverity, number> = { blocking, lossy, dropped };
	const parts = DIAGNOSTIC_SEVERITIES.filter((severity) => counts[severity] > 0).map(
		(severity) => `${counts[severity]} ${DIAGNOSTIC_SEVERITY_LABELS[severity].toLowerCase()}`
	);
	return `${parts.join(', ')} ${m.workflows_import_issues({ count: issues.length })}`;
}
