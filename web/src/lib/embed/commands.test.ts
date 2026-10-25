import { describe, expect, it } from 'vitest';

import { COMMAND_RESULT_TYPE, COMMAND_TYPE, parseHostCommand } from './commands';

describe('parseHostCommand', () => {
	it('reads each command the frame carries', () => {
		expect(parseHostCommand({ type: COMMAND_TYPE, requestId: 'r1', command: 'save' })).toEqual({ command: 'save', requestId: 'r1' });
		expect(parseHostCommand({ type: COMMAND_TYPE, requestId: 'r2', command: 'run' })).toEqual({ command: 'run', requestId: 'r2' });
		expect(parseHostCommand({ type: COMMAND_TYPE, requestId: 'r3', command: 'focusNode', nodeId: 'webhook' })).toEqual({
			command: 'focusNode',
			requestId: 'r3',
			nodeId: 'webhook'
		});
	});

	it('keeps a well-formed request whose command the frame does not carry', () => {
		// The host is waiting on a promise for exactly this request id; an
		// unknown name must be answered, not dropped.
		expect(parseHostCommand({ type: COMMAND_TYPE, requestId: 'r4', command: 'tidy' })).toEqual({ command: 'unknown', requestId: 'r4', name: 'tidy' });
		expect(parseHostCommand({ type: COMMAND_TYPE, requestId: 'r5', command: 42 })).toEqual({ command: 'unknown', requestId: 'r5', name: '' });
	});

	it('ignores anything with no promise to answer', () => {
		// Another protocol's message, a missing or empty request id, a
		// focusNode with nothing to focus: none of these may invent a reply.
		for (const data of [
			null,
			'string',
			{ type: 'kilasflow:embed-session', token: 'tok' },
			{ type: COMMAND_TYPE, command: 'save' },
			{ type: COMMAND_TYPE, requestId: '', command: 'save' },
			{ type: COMMAND_TYPE, requestId: 'r6', command: 'focusNode' },
			{ type: COMMAND_TYPE, requestId: 'r7', command: 'focusNode', nodeId: '' }
		]) {
			expect(parseHostCommand(data)).toBeNull();
		}
	});

	it('names the reply type the editor answers with', () => {
		// The host's promise resolves on exactly this type; a typo here is a
		// command that times out on every host.
		expect(COMMAND_RESULT_TYPE).toBe('kilasflow:command-result');
	});
});
