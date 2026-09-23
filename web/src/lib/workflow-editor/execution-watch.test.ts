import { describe, expect, it } from 'vitest';

import { EXECUTION_EVENT_NAMES, watchExecutionEvents, type EventSourceLike } from './execution-watch';

class FakeSource implements EventSourceLike {
	listeners = new Map<string, ((event: MessageEvent<string>) => void)[]>();
	closed = false;
	constructor(public url: string) {}
	addEventListener(name: string, listener: (event: MessageEvent<string>) => void) {
		this.listeners.set(name, [...(this.listeners.get(name) ?? []), listener]);
	}
	close() {
		this.closed = true;
	}
	emit(name: string, payload: unknown) {
		for (const listener of this.listeners.get(name) ?? []) {
			listener({ data: JSON.stringify(payload) } as MessageEvent<string>);
		}
	}
}

describe('watchExecutionEvents', () => {
	it('subscribes to the execution and forwards every named event, ai.* included', () => {
		let source!: FakeSource;
		const seen: string[] = [];
		watchExecutionEvents('ex 1', (event) => seen.push(event.type), (url) => (source = new FakeSource(url)));

		expect(source.url).toBe('/api/v1/executions/ex%201/events');
		expect(EXECUTION_EVENT_NAMES).toContain('ai.model.delta');
		source.emit('node.started', { id: 1, type: 'node.started' });
		source.emit('ai.model.delta', { id: 2, type: 'ai.model.delta' });
		source.emit('execution.event', { id: 3, type: 'something.new' });
		expect(seen).toEqual(['node.started', 'ai.model.delta', 'something.new']);
	});

	it('resolves on a terminal event and closes the stream', async () => {
		let source!: FakeSource;
		const watch = watchExecutionEvents('ex_1', () => {}, (url) => (source = new FakeSource(url)));
		source.emit('execution.completed', { id: 9, type: 'execution.completed', status: 'succeeded' });
		await expect(watch.terminal).resolves.toBe('execution.completed');
		expect(source.closed).toBe(true);
	});

	it('ignores a frame that is not JSON', () => {
		let source!: FakeSource;
		const seen: unknown[] = [];
		watchExecutionEvents('ex_1', (event) => seen.push(event), (url) => (source = new FakeSource(url)));
		for (const listener of source.listeners.get('node.started') ?? []) listener({ data: 'not json' } as MessageEvent<string>);
		expect(seen).toEqual([]);
	});

	it('degrades to a stream that never settles when no EventSource exists', async () => {
		const watch = watchExecutionEvents('ex_1', () => {}, () => null);
		const winner = await Promise.race([watch.terminal, new Promise((resolve) => setTimeout(() => resolve('timeout'), 20))]);
		expect(winner).toBe('timeout');
		watch.close();
	});
});
