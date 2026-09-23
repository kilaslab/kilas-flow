import { describe, expect, it } from 'vitest';

import type { ExecutionEvent } from './event-stream.svelte';
import { reduceChatStream } from './chat-stream';

let id = 0;
function ai(kind: string, data: Record<string, unknown> = {}): ExecutionEvent {
	id += 1;
	return { id, type: kind, executionId: 'ex_1', nodeId: 'agent', at: '2026-09-23T00:00:00Z', data: { kind, iteration: 1, ...data } };
}

describe('reduceChatStream', () => {
	it('is thinking once the model is asked and nothing has streamed', () => {
		expect(reduceChatStream([ai('ai.model.started')])).toMatchObject({ phase: 'thinking', text: '', tools: [] });
	});

	it('joins streamed deltas into the reply being written', () => {
		const state = reduceChatStream([ai('ai.model.started'), ai('ai.model.delta', { delta: 'Hel' }), ai('ai.model.delta', { delta: 'lo **Rina**' })]);
		expect(state).toMatchObject({ phase: 'writing', text: 'Hello **Rina**' });
	});

	it('starts the text over when the loop asks the model again', () => {
		const state = reduceChatStream([
			ai('ai.model.started', { iteration: 1 }),
			ai('ai.model.delta', { iteration: 1, delta: 'Let me check.' }),
			ai('ai.model.started', { iteration: 2 }),
			ai('ai.model.delta', { iteration: 2, delta: 'There are 3.' })
		]);
		expect(state.text).toBe('There are 3.');
	});

	it('tracks a tool call from start to result', () => {
		const running = reduceChatStream([ai('ai.tool.started', { tool: 'calculator', detail: { expression: '2*3' } })]);
		expect(running.phase).toBe('tool');
		expect(running.tools).toEqual([{ key: 'calculator#1', name: 'calculator', status: 'running', input: { expression: '2*3' } }]);

		const done = reduceChatStream([
			ai('ai.tool.started', { tool: 'calculator', detail: { expression: '2*3' } }),
			ai('ai.tool.completed', { tool: 'calculator', detail: '6' })
		]);
		expect(done.tools).toEqual([{ key: 'calculator#1', name: 'calculator', status: 'done', input: { expression: '2*3' }, output: '6' }]);
	});

	it('marks a failed tool call with its error', () => {
		const state = reduceChatStream([ai('ai.tool.started', { tool: 'list_users' }), ai('ai.tool.failed', { tool: 'list_users', error: 'blocked' })]);
		expect(state.tools[0]).toMatchObject({ name: 'list_users', status: 'failed', error: 'blocked' });
	});

	it('keeps two calls to the same tool apart', () => {
		const state = reduceChatStream([
			ai('ai.tool.started', { tool: 'calculator' }),
			ai('ai.tool.completed', { tool: 'calculator', detail: '1' }),
			ai('ai.tool.started', { tool: 'calculator' })
		]);
		expect(state.tools.map((tool) => tool.status)).toEqual(['done', 'running']);
	});

	it('ignores events that are not about an AI step', () => {
		const state = reduceChatStream([
			{ id: 99, type: 'node.started', executionId: 'ex_1', nodeId: 'chat', at: '', status: 'running' },
			ai('ai.model.delta', { delta: 'ok' })
		]);
		expect(state.text).toBe('ok');
	});

	it('reads an event forwarded under the generic name by its type', () => {
		const state = reduceChatStream([{ ...ai('ai.model.delta', { delta: 'x' }), type: 'ai.model.delta' }]);
		expect(state.text).toBe('x');
	});
});
