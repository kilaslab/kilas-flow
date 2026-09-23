import { describe, expect, it } from 'vitest';

import type { ExecutionResource } from '$lib/api/generated/models';

import { chatHasMemory, chatReplyFromExecution, chatTriggerIn, CHAT_TRIGGER_TYPE } from './chat';

function execution(partial: Partial<ExecutionResource>): ExecutionResource {
	return {
		id: 'ex_1',
		workflowId: 'wf_1',
		workflowVersionId: 'ver_1',
		status: 'succeeded',
		trigger: 'manual',
		startedAt: '2026-09-21T00:00:00Z',
		nodeRuns: [],
		...partial
	};
}

describe('chatReplyFromExecution', () => {
	it('reads a string output from the last succeeded node', () => {
		const reply = chatReplyFromExecution(
			execution({
				nodeRuns: [
					{ nodeId: 'chat', status: 'succeeded', sequence: 0, attempt: 1, runIndex: 0, startedAt: '', output: [[{ json: { chatInput: 'hi' } }]] },
					{ nodeId: 'agent', status: 'succeeded', sequence: 1, attempt: 1, runIndex: 0, startedAt: '', output: [[{ json: { output: 'hello there' } }]] }
				]
			}),
			'chat'
		);
		expect(reply).toEqual({ kind: 'reply', text: 'hello there' });
	});

	it('stringifies an object output', () => {
		const reply = chatReplyFromExecution(
			execution({
				nodeRuns: [
					{ nodeId: 'chat', status: 'succeeded', sequence: 0, attempt: 1, runIndex: 0, startedAt: '', output: [[{ json: {} }]] },
					{ nodeId: 'agent', status: 'succeeded', sequence: 1, attempt: 1, runIndex: 0, startedAt: '', output: [[{ json: { output: { answer: 42 } } }]] }
				]
			}),
			'chat'
		);
		expect(reply).toEqual({ kind: 'reply', text: '{"answer":42}' });
	});

	it('falls back to text when output is absent', () => {
		const reply = chatReplyFromExecution(
			execution({
				nodeRuns: [
					{ nodeId: 'chain', status: 'succeeded', sequence: 1, attempt: 1, runIndex: 0, startedAt: '', output: [[{ json: { text: 'from the chain' } }]] }
				]
			}),
			'chat'
		);
		expect(reply).toEqual({ kind: 'reply', text: 'from the chain' });
	});

	it('walks back past a succeeded Set that has neither output nor text', () => {
		const reply = chatReplyFromExecution(
			execution({
				nodeRuns: [
					{ nodeId: 'chat', status: 'succeeded', sequence: 0, attempt: 1, runIndex: 0, startedAt: '', output: [[{ json: { chatInput: 'hi' } }]] },
					{ nodeId: 'agent', status: 'succeeded', sequence: 1, attempt: 1, runIndex: 0, startedAt: '', output: [[{ json: { output: 'from the agent' } }]] },
					{ nodeId: 'set', status: 'succeeded', sequence: 2, attempt: 1, runIndex: 0, startedAt: '', output: [[{ json: { status: 'stored' } }]] }
				]
			}),
			'chat'
		);
		expect(reply).toEqual({ kind: 'reply', text: 'from the agent' });
	});

	it('walks back past skipped nodes and the chat trigger', () => {
		const reply = chatReplyFromExecution(
			execution({
				nodeRuns: [
					{ nodeId: 'chat', status: 'succeeded', sequence: 0, attempt: 1, runIndex: 0, startedAt: '', output: [[{ json: { chatInput: 'hi' } }]] },
					{ nodeId: 'agent', status: 'succeeded', sequence: 1, attempt: 1, runIndex: 0, startedAt: '', output: [[{ json: { output: 'kept' } }]] },
					{ nodeId: 'note', status: 'skipped', sequence: 2, attempt: 1, runIndex: 0, startedAt: '', output: [[{ json: { output: 'nope' } }]] }
				]
			}),
			'chat'
		);
		expect(reply).toEqual({ kind: 'reply', text: 'kept' });
	});

	it('names the last succeeded node when neither output nor text is present', () => {
		const reply = chatReplyFromExecution(
			execution({
				nodeRuns: [
					{ nodeId: 'chat', status: 'succeeded', sequence: 0, attempt: 1, runIndex: 0, startedAt: '', output: [[{ json: { chatInput: 'hi' } }]] },
					{ nodeId: 'http', status: 'succeeded', sequence: 1, attempt: 1, runIndex: 0, startedAt: '', output: [[{ json: { status: 200 } }]] }
				]
			}),
			'chat'
		);
		expect(reply).toEqual({ kind: 'missing', nodeId: 'http' });
	});

	it('surfaces a failed run from execution.error', () => {
		const reply = chatReplyFromExecution(
			execution({
				status: 'failed',
				error: { message: 'model timed out' },
				nodeRuns: [{ nodeId: 'agent', status: 'failed', sequence: 1, attempt: 1, runIndex: 0, startedAt: '' }]
			}),
			'chat'
		);
		expect(reply).toEqual({ kind: 'error', text: 'model timed out' });
	});
});

describe('chatTriggerIn', () => {
	it('finds the chat trigger by type, ignoring canvas selection', () => {
		expect(chatTriggerIn([{ id: 'a', type: 'kilasflow.manual' }, { id: 'c', type: CHAT_TRIGGER_TYPE }])?.id).toBe('c');
		expect(chatTriggerIn([{ id: 'a', type: 'kilasflow.manual' }])).toBeUndefined();
	});
});

describe('chatReplyFromExecution failures', () => {
	const providerError =
		'model turn 1: model request failed with status 404: {"error":{"message":"model \'no-such-model\' not found","type":"not_found_error","param":null,"code":null}}';

	it('names the failed node and lifts the provider message out of its JSON', () => {
		const reply = chatReplyFromExecution(
			execution({
				status: 'failed',
				error: { message: `execute node "agent": node "AI Agent": ${providerError}` },
				nodeRuns: [
					{ nodeId: 'chat', status: 'succeeded', sequence: 0, attempt: 1, runIndex: 0, startedAt: '' },
					{ nodeId: 'agent', status: 'failed', sequence: 1, attempt: 1, runIndex: 0, startedAt: '', error: { message: `node "AI Agent": ${providerError}` } }
				]
			}),
			'chat',
			[{ id: 'agent', name: 'AI Agent' }]
		);
		expect(reply).toEqual({
			kind: 'error',
			node: 'AI Agent',
			text: "model 'no-such-model' not found",
			detail: 'model turn 1: model request failed with status 404'
		});
	});

	it('strips the engine prefixes from a plain error', () => {
		const reply = chatReplyFromExecution(
			execution({
				status: 'failed',
				error: { message: 'execute node "set": node "Format": cannot read toUpperCase() of undefined' },
				nodeRuns: [{ nodeId: 'set', status: 'failed', sequence: 1, attempt: 1, runIndex: 0, startedAt: '' }]
			}),
			'chat',
			[{ id: 'set', name: 'Format' }]
		);
		expect(reply).toEqual({ kind: 'error', node: 'Format', text: 'cannot read toUpperCase() of undefined' });
	});
});

describe('chatHasMemory', () => {
	it('is true only when a memory sub-node is wired to something', () => {
		const nodes = [
			{ id: 'agent', type: 'kilasflow.agent' },
			{ id: 'mem', type: 'kilasflow.memoryBuffer' }
		];
		const wired = [{ id: 'c1', kind: 'ai_memory', source: { nodeId: 'mem', port: 'memory' }, target: { nodeId: 'agent', port: 'memory' } }];
		expect(chatHasMemory(nodes, wired)).toBe(true);
		expect(chatHasMemory(nodes, [])).toBe(false);
		expect(chatHasMemory([{ id: 'agent', type: 'kilasflow.agent' }], [])).toBe(false);
	});
});
