import { afterEach, describe, expect, it } from 'vitest';

import type { Connection, Definition, ExecutionNodeRunResource, Node } from '$lib/api/generated/models';
import { setLocale } from '$lib/i18n/locale.svelte';
import {
	binaryAttachments,
	edgeItemCounts,
	executionDurationMs,
	formatBytes,
	formatDuration,
	formatTimestamp,
	latestNodeRuns,
	nodeRunStatus,
	statusLabel
} from './execution';

function nodeRun(overrides: Partial<ExecutionNodeRunResource> & { nodeId: string }): ExecutionNodeRunResource {
	return {
		attempt: 1,
		// The Nth time the node ran, distinct from attempt, which counts
		// retries of one run.
		runIndex: 0,
		sequence: 1,
		status: 'succeeded',
		startedAt: '2026-09-05T01:00:00Z',
		...overrides
	};
}

describe('latestNodeRuns', () => {
	it('keeps the final attempt for each node', () => {
		const runs = latestNodeRuns([
			nodeRun({ nodeId: 'a', attempt: 1, sequence: 1, status: 'failed' }),
			nodeRun({ nodeId: 'a', attempt: 2, sequence: 2, status: 'succeeded' }),
			nodeRun({ nodeId: 'b', attempt: 1, sequence: 3 })
		]);

		expect(runs.get('a')?.attempt).toBe(2);
		expect(runs.get('a')?.status).toBe('succeeded');
		expect(runs.get('b')?.attempt).toBe(1);
	});

	it('tolerates a missing trace', () => {
		expect(latestNodeRuns(null).size).toBe(0);
		expect(latestNodeRuns(undefined).size).toBe(0);
	});
});

describe('nodeRunStatus', () => {
	const runs = latestNodeRuns([
		nodeRun({ nodeId: 'ran', status: 'succeeded' }),
		nodeRun({ nodeId: 'broke', status: 'failed' })
	]);

	it('reports the recorded status of a node that ran', () => {
		expect(nodeRunStatus('ran', runs)).toBe('succeeded');
		expect(nodeRunStatus('broke', runs)).toBe('failed');
	});

	it('distinguishes a node that never ran from one that failed', () => {
		expect(nodeRunStatus('never-reached', runs)).toBe('skipped');
	});
});

describe('edgeItemCounts', () => {
	const definitions: Definition[] = [
		{
			type: 'kilasflow.if',
			version: 1,
			displayName: 'IF',
			category: 'Core',
			group: ['transform'],
			source: 'builtin',
			description: '',
			inputs: [{ name: 'main', kind: 'main' }],
			outputs: [
				{ name: 'true', kind: 'main' },
				{ name: 'false', kind: 'main' }
			],
			parameters: [],
			sharedSettings: []
		},
		{
			type: 'kilasflow.set',
			version: 1,
			displayName: 'Set',
			category: 'Core',
			group: ['transform'],
			source: 'builtin',
			description: '',
			inputs: [{ name: 'main', kind: 'main' }],
			outputs: [{ name: 'main', kind: 'main' }],
			parameters: [],
			sharedSettings: []
		}
	];
	const nodes: Node[] = [
		{ id: 'if', name: 'IF', type: 'kilasflow.if', typeVersion: 1, position: { x: 0, y: 0 } },
		{ id: 'yes', name: 'Yes', type: 'kilasflow.set', typeVersion: 1, position: { x: 0, y: 0 } },
		{ id: 'no', name: 'No', type: 'kilasflow.set', typeVersion: 1, position: { x: 0, y: 0 } }
	];
	const connections: Connection[] = [
		{ id: 'c-true', kind: 'main', source: { nodeId: 'if', port: 'true' }, target: { nodeId: 'yes', port: 'main' } },
		{ id: 'c-false', kind: 'main', source: { nodeId: 'if', port: 'false' }, target: { nodeId: 'no', port: 'main' } }
	];

	it('counts the items each branch actually carried', () => {
		const runs = latestNodeRuns([
			nodeRun({ nodeId: 'if', output: [[{ json: { a: 1 } }, { json: { a: 2 } }], [{ json: { a: 3 } }]] })
		]);

		const counts = edgeItemCounts(connections, nodes, definitions, runs);

		expect(counts.get('c-true')).toBe(2);
		expect(counts.get('c-false')).toBe(1);
	});

	it('omits a count when the source node produced no recorded output', () => {
		const counts = edgeItemCounts(connections, nodes, definitions, latestNodeRuns([]));

		expect(counts.has('c-true')).toBe(false);
	});

	it('reports an empty branch as zero rather than unknown', () => {
		const runs = latestNodeRuns([nodeRun({ nodeId: 'if', output: [[], [{ json: {} }]] })]);

		const counts = edgeItemCounts(connections, nodes, definitions, runs);

		expect(counts.get('c-true')).toBe(0);
		expect(counts.get('c-false')).toBe(1);
	});

	it('counts through a resolved definition when the stored version is newer', () => {
		const imported: Node[] = [
			{ id: 'if', name: 'IF', type: 'kilasflow.if', typeVersion: 2.2, position: { x: 0, y: 0 } },
			{ id: 'yes', name: 'Yes', type: 'kilasflow.set', typeVersion: 3.4, position: { x: 0, y: 0 } },
			{ id: 'no', name: 'No', type: 'kilasflow.set', typeVersion: 3.4, position: { x: 0, y: 0 } }
		];
		const runs = latestNodeRuns([
			nodeRun({ nodeId: 'if', output: [[{ json: { a: 1 } }], [{ json: { a: 2 } }, { json: { a: 3 } }]] })
		]);

		const counts = edgeItemCounts(connections, imported, definitions, runs);

		expect(counts.get('c-true')).toBe(1);
		expect(counts.get('c-false')).toBe(2);
	});
});

describe('formatDuration', () => {
	it('scales the unit to the magnitude', () => {
		expect(formatDuration(0)).toBe('0 ms');
		expect(formatDuration(820)).toBe('820 ms');
		expect(formatDuration(1500)).toBe('1.5 s');
		expect(formatDuration(63_000)).toBe('1 m 3 s');
	});

	it('has nothing to show for an unfinished run', () => {
		expect(formatDuration(null)).toBe('—');
	});
});

describe('executionDurationMs', () => {
	it('measures from start to finish', () => {
		expect(
			executionDurationMs({ startedAt: '2026-09-05T01:00:00.000Z', finishedAt: '2026-09-05T01:00:02.500Z' })
		).toBe(2500);
	});

	it('returns null while a run is still in flight', () => {
		expect(executionDurationMs({ startedAt: '2026-09-05T01:00:00.000Z' })).toBeNull();
	});
});

describe('binaryAttachments', () => {
	it('flattens every reference a run produced, keeping where it came from', () => {
		const attachments = binaryAttachments([
			[
				{ json: { caption: 'hi' }, binary: { data: { id: 'bin-1', fileName: 'a.png', mediaType: 'image/png', size: 2048 } } },
				{ json: { caption: 'no attachment' } }
			],
			[{ json: {}, binary: { report: { id: 'bin-2' }, receipt: { id: 'bin-3' } } }]
		]);

		expect(attachments.map((attachment) => [attachment.port, attachment.item, attachment.property, attachment.reference.id])).toEqual([
			[0, 0, 'data', 'bin-1'],
			[1, 0, 'report', 'bin-2'],
			[1, 0, 'receipt', 'bin-3']
		]);
	});

	it('ignores anything that is not a reference', () => {
		// A node run's output is server JSON, not a typed value, so the shape is
		// checked rather than trusted: a `binary` key holding a string is a bug
		// somewhere, not something to render as an attachment.
		expect(binaryAttachments(null)).toEqual([]);
		expect(binaryAttachments([null, 'nope'])).toEqual([]);
		expect(binaryAttachments([[{ binary: 'not an object' }]])).toEqual([]);
		expect(binaryAttachments([[{ binary: { data: { fileName: 'no id' } } }]])).toEqual([]);
	});
});

describe('formatBytes', () => {
	it('reads the way a download dialog does', () => {
		expect(formatBytes(0)).toBe('0 B');
		expect(formatBytes(999)).toBe('999 B');
		expect(formatBytes(1000)).toBe('1.0 kB');
		expect(formatBytes(2048)).toBe('2.0 kB');
		expect(formatBytes(15_000)).toBe('15 kB');
		expect(formatBytes(16_777_216)).toBe('17 MB');
	});

	it('says so when the size is missing rather than showing a zero', () => {
		expect(formatBytes(undefined)).toBe('unknown size');
		expect(formatBytes(-1)).toBe('unknown size');
		expect(formatBytes(Number.NaN)).toBe('unknown size');
	});
});

describe('statusLabel', () => {
	it('names the statuses a run reports, and capitalises a token no catalog carries', () => {
		expect(statusLabel('queued')).toBe('Queued');
		expect(statusLabel('succeeded')).toBe('Succeeded');
		expect(statusLabel('cancelling')).toBe('Cancelling');
		// The client-only status of a node the execution never reached.
		expect(statusLabel('skipped')).toBe('Not reached');
		// A trigger token is not a status: no catalog carries one, in either
		// locale, so it keeps the capitalised token.
		expect(statusLabel('subworkflow')).toBe('Subworkflow');
	});
});

describe('the Indonesian catalog', () => {
	// The locale is process-wide module state, and every other case in this file
	// reads the base locale, so each one here puts it back.
	afterEach(() => setLocale('en'));

	it('labels a status in Indonesian and leaves a token the catalog does not carry alone', () => {
		setLocale('id');

		expect(statusLabel('succeeded')).toBe('Berhasil');
		expect(statusLabel('failed')).toBe('Gagal');
		// The client-only status a node that never ran carries.
		expect(statusLabel('skipped')).toBe('Tidak tercapai');
		// A server trigger token is not a status: no catalog carries it in either
		// locale, so it keeps the capitalised token.
		expect(statusLabel('manual')).toBe('Manual');
	});

	it('renders durations, sizes and the empty placeholder from the Indonesian catalog', () => {
		setLocale('id');

		expect(formatDuration(1500)).toBe('1.5 detik');
		expect(formatDuration(63_000)).toBe('1 menit 3 detik');
		expect(formatDuration(null)).toBe('—');
		expect(formatBytes(undefined)).toBe('ukuran tidak diketahui');
		expect(formatTimestamp(undefined)).toBe('—');
	});
});
