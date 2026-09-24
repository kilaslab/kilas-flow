import { describe, expect, it } from 'vitest';

import type { ExecutionNodeRunResource } from '$lib/api/generated/models';
import { applyEvents, isTerminal, isTerminalStatus, latestExecutionStatus, liveConsole, type ExecutionEvent } from './event-stream.svelte';

function event(overrides: Partial<ExecutionEvent> & { type: string }): ExecutionEvent {
	return { id: 1, executionId: 'exec-1', at: '2026-09-05T01:00:00Z', ...overrides };
}

function run(nodeId: string, status: string): ExecutionNodeRunResource {
	return { nodeId, attempt: 1, runIndex: 0, sequence: 1, status, startedAt: '2026-09-05T01:00:00Z' };
}

// The subscription gate: a trace that already ended must not open a stream, or
// every finished execution page holds an idle connection until the server
// hangs up on it.
describe('isTerminalStatus', () => {
	it('recognizes a trace that will never emit another event', () => {
		expect(isTerminalStatus('succeeded')).toBe(true);
		expect(isTerminalStatus('failed')).toBe(true);
		expect(isTerminalStatus('cancelled')).toBe(true);
	});

	it('keeps streaming a run that can still move', () => {
		expect(isTerminalStatus('running')).toBe(false);
		expect(isTerminalStatus('queued')).toBe(false);
		// A wait is not an end: the run resumes when its timer or webhook does.
		expect(isTerminalStatus('waiting')).toBe(false);
		expect(isTerminalStatus(undefined)).toBe(false);
	});
});

describe('isTerminal', () => {
	it('recognizes the events that end a stream', () => {
		expect(isTerminal('execution.completed')).toBe(true);
		expect(isTerminal('execution.failed')).toBe(true);
		expect(isTerminal('execution.cancelled')).toBe(true);
		expect(isTerminal('node.completed')).toBe(false);
		expect(isTerminal('execution.started')).toBe(false);
	});
});

describe('applyEvents', () => {
	it('starts from the durable trace', () => {
		const statuses = applyEvents(new Map([['a', run('a', 'succeeded')]]), []);

		expect(statuses.get('a')).toBe('succeeded');
	});

	it('advances a node as its events arrive', () => {
		const statuses = applyEvents(
			new Map([['a', run('a', 'running')]]),
			[event({ type: 'node.completed', nodeId: 'a', status: 'succeeded' })]
		);

		expect(statuses.get('a')).toBe('succeeded');
	});

	it('adds nodes the fetched trace did not yet contain', () => {
		const statuses = applyEvents(new Map(), [event({ type: 'node.failed', nodeId: 'b', status: 'failed' })]);

		expect(statuses.get('b')).toBe('failed');
	});

	it('ignores execution-level events, which carry no node', () => {
		const statuses = applyEvents(new Map([['a', run('a', 'succeeded')]]), [
			event({ type: 'execution.completed', status: 'succeeded' })
		]);

		expect([...statuses.keys()]).toEqual(['a']);
	});

	it('keeps the last status when a node reports more than once', () => {
		const statuses = applyEvents(new Map(), [
			event({ id: 1, type: 'node.started', nodeId: 'a', status: 'running' }),
			event({ id: 2, type: 'node.completed', nodeId: 'a', status: 'succeeded' })
		]);

		expect(statuses.get('a')).toBe('succeeded');
	});
});

describe('liveConsole', () => {
	it('folds every code.console event for a node into one ordered list', () => {
		const merged = liveConsole('a', [
			event({ id: 1, type: 'code.console', nodeId: 'a', data: { lines: [{ level: 'log', text: 'first' }] } }),
			event({ id: 2, type: 'node.started', nodeId: 'a', status: 'running' }),
			event({ id: 3, type: 'code.console', nodeId: 'a', data: { lines: [{ level: 'error', text: 'second' }], truncated: true } })
		]);

		expect(merged.lines.map((line) => line.text)).toEqual(['first', 'second']);
		expect(merged.truncated).toBe(true);
	});

	it('ignores console events belonging to a different node', () => {
		const merged = liveConsole('a', [
			event({ type: 'code.console', nodeId: 'b', data: { lines: [{ level: 'log', text: 'not mine' }] } })
		]);

		expect(merged.lines).toEqual([]);
	});

	it('reports nothing printed rather than throwing when no console event has arrived', () => {
		expect(liveConsole('a', [])).toEqual({ lines: [], truncated: false });
	});
});

describe('latestExecutionStatus', () => {
	it('reports the most recent execution-level status', () => {
		expect(
			latestExecutionStatus([
				event({ id: 1, type: 'execution.started', status: 'running' }),
				event({ id: 2, type: 'node.completed', nodeId: 'a', status: 'succeeded' }),
				event({ id: 3, type: 'execution.failed', status: 'failed' })
			])
		).toBe('failed');
	});

	it('has nothing to report before any execution event', () => {
		expect(latestExecutionStatus([])).toBeNull();
		expect(latestExecutionStatus([event({ type: 'node.started', nodeId: 'a', status: 'running' })])).toBeNull();
	});
});
