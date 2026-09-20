import { describe, expect, it } from 'vitest';

import type { ImportIssue } from '$lib/api/generated/models';
import { diagnosticSummaryLabel, summarizeDiagnostics, worstSeverity } from './import-diagnostics';

const issue = (severity: ImportIssue['severity'], extra: Partial<ImportIssue> = {}): ImportIssue => ({
	severity,
	reason: `${severity} reason`,
	...extra
});

describe('import diagnostics', () => {
	it('counts the report by severity, worst first', () => {
		const issues = [
			issue('dropped', { field: 'pinData' }),
			issue('blocking', { nodeId: 'mystery', nodeName: 'Mystery' }),
			issue('lossy', { nodeId: 'http', field: 'retryOnFail' }),
			issue('blocking', { nodeId: 'slack' })
		];

		expect(summarizeDiagnostics(issues)).toEqual({ total: 4, blocking: 2, lossy: 1, dropped: 1 });
		expect(summarizeDiagnostics([])).toEqual({ total: 0, blocking: 0, lossy: 0, dropped: 0 });
	});

	// A node's badge is one dot, so it has to carry the severity that decides
	// whether the workflow runs: a blocking entry beside a lossy one cannot be
	// drawn as "lossy" without the count that says otherwise.
	it('leads a badge with the worst severity the node carries', () => {
		expect(worstSeverity([issue('dropped'), issue('lossy')])).toBe('lossy');
		expect(worstSeverity([issue('lossy'), issue('blocking')])).toBe('blocking');
		expect(worstSeverity([issue('dropped')])).toBe('dropped');
		expect(worstSeverity([])).toBeNull();
	});

	it('names the severities a report or a node carries, not just their number', () => {
		expect(diagnosticSummaryLabel([issue('blocking'), issue('lossy'), issue('lossy')])).toBe(
			'1 blocking, 2 lossy import issues'
		);
		expect(diagnosticSummaryLabel([issue('dropped')])).toBe('1 dropped import issue');
	});
});
