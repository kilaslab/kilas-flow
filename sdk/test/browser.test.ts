// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest';

import { mountWorkflowEditor, subscribeExecutionEvents, type EditorEvent } from '../src/browser.js';

const EDITOR_ORIGIN = 'https://flows.example';

function session(overrides: Partial<Parameters<typeof mountWorkflowEditor>[0]['session']> = {}) {
	return {
		token: 'kfe1.a.b',
		embedUrl: '/embed/wf-1',
		expiresAt: new Date(Date.now() + 600_000).toISOString(),
		scopes: ['workflow:read'],
		origin: 'https://host.example',
		...overrides
	};
}

/**
 * Stands in for the iframe's window. `contentWindow` is not writable on a real
 * jsdom iframe, so the test defines it — what matters is that the SDK posts to
 * the right window with the right target origin.
 */
function fakeFrameWindow() {
	return { postMessage: vi.fn() } as unknown as Window;
}

function mount(options: Partial<Parameters<typeof mountWorkflowEditor>[0]> = {}) {
	const container = document.createElement('div');
	document.body.appendChild(container);
	const mounted = mountWorkflowEditor({
		container,
		baseUrl: EDITOR_ORIGIN,
		session: session(),
		...options
	});
	const frameWindow = fakeFrameWindow();
	Object.defineProperty(mounted.iframe, 'contentWindow', { value: frameWindow, configurable: true });
	return { container, mounted, frameWindow };
}

afterEach(() => {
	document.body.replaceChildren();
	vi.useRealTimers();
});

describe('mountWorkflowEditor', () => {
	it('creates a sandboxed iframe pointing at the workflow', () => {
		const { mounted, container } = mount();

		expect(container.contains(mounted.iframe)).toBe(true);
		expect(mounted.iframe.src).toBe(`${EDITOR_ORIGIN}/embed/wf-1`);
		const sandbox = mounted.iframe.getAttribute('sandbox') ?? '';
		// The editor needs scripts and its own origin; it is denied top-level
		// navigation and popups so a compromised frame cannot navigate the host
		// page away.
		expect(sandbox).toContain('allow-scripts');
		expect(sandbox).toContain('allow-same-origin');
		expect(sandbox).not.toContain('allow-top-navigation');
		expect(sandbox).not.toContain('allow-popups');
	});

	it('sends the session only after the editor announces itself, to its exact origin', () => {
		const { mounted, frameWindow } = mount();

		expect(frameWindow.postMessage).not.toHaveBeenCalled();

		window.dispatchEvent(
			new MessageEvent('message', {
				origin: EDITOR_ORIGIN,
				source: frameWindow,
				data: { type: 'kilasflow:embed-ready', workflowId: 'wf-1' }
			})
		);

		expect(frameWindow.postMessage).toHaveBeenCalledTimes(1);
		const [payload, targetOrigin] = (frameWindow.postMessage as unknown as ReturnType<typeof vi.fn>).mock.calls[0]!;
		expect(payload).toMatchObject({ type: 'kilasflow:embed-session', token: 'kfe1.a.b', workflowId: 'wf-1' });
		// '*' would hand the token to whatever document occupied the frame.
		expect(targetOrigin).toBe(EDITOR_ORIGIN);
		mounted.unmount();
	});

	it('ignores a ready message from another origin', () => {
		const { mounted, frameWindow } = mount();

		window.dispatchEvent(
			new MessageEvent('message', {
				origin: 'https://evil.example',
				source: frameWindow,
				data: { type: 'kilasflow:embed-ready', workflowId: 'wf-1' }
			})
		);

		expect(frameWindow.postMessage).not.toHaveBeenCalled();
		mounted.unmount();
	});

	it('ignores a message from a window that is not the iframe', () => {
		const { mounted, frameWindow } = mount();

		window.dispatchEvent(
			new MessageEvent('message', {
				origin: EDITOR_ORIGIN,
				source: fakeFrameWindow(),
				data: { type: 'kilasflow:embed-ready', workflowId: 'wf-1' }
			})
		);

		expect(frameWindow.postMessage).not.toHaveBeenCalled();
		mounted.unmount();
	});

	it('forwards editor events to the host', () => {
		const events: EditorEvent[] = [];
		const { mounted, frameWindow } = mount({ onEvent: (event) => events.push(event) });

		for (const data of [
			{ type: 'kilasflow:embed-ready', workflowId: 'wf-1' },
			{ type: 'kilasflow:workflow-saved', workflowId: 'wf-1', revision: 3 },
			{ type: 'kilasflow:execution-finished', workflowId: 'wf-1', executionId: 'exec-1', status: 'succeeded' }
		]) {
			window.dispatchEvent(new MessageEvent('message', { origin: EDITOR_ORIGIN, source: frameWindow, data }));
		}

		expect(events.map((event) => event.type)).toEqual(['ready', 'workflow-saved', 'execution-finished']);
		expect(events[2]).toMatchObject({ executionId: 'exec-1', status: 'succeeded' });
		mounted.unmount();
	});

	it('reports a handshake that never completes', () => {
		vi.useFakeTimers();
		const onError = vi.fn();
		const { mounted } = mount({ onError, handshakeTimeoutMs: 1000 });

		vi.advanceTimersByTime(1500);

		expect(onError).toHaveBeenCalledTimes(1);
		expect(String(onError.mock.calls[0]![0])).toMatch(/handshake/);
		mounted.unmount();
	});

	it('removes its iframe, listener, and timer on unmount', () => {
		vi.useFakeTimers();
		const onError = vi.fn();
		const onEvent = vi.fn();
		const { mounted, container, frameWindow } = mount({ onError, onEvent, handshakeTimeoutMs: 1000 });

		mounted.unmount();

		expect(container.contains(mounted.iframe)).toBe(false);
		// A leaked listener would keep responding after the host tore the
		// editor down.
		window.dispatchEvent(
			new MessageEvent('message', {
				origin: EDITOR_ORIGIN,
				source: frameWindow,
				data: { type: 'kilasflow:embed-ready', workflowId: 'wf-1' }
			})
		);
		expect(onEvent).not.toHaveBeenCalled();
		vi.advanceTimersByTime(5000);
		expect(onError).not.toHaveBeenCalled();
	});

	it('refuses to mount without what it needs', () => {
		const container = document.createElement('div');
		expect(() =>
			mountWorkflowEditor({ container, baseUrl: EDITOR_ORIGIN, session: session({ token: '' }) })
		).toThrow(/token/);
		expect(() =>
			mountWorkflowEditor({ container, baseUrl: EDITOR_ORIGIN, session: session({ embedUrl: '' }) })
		).toThrow(/workflow/);
	});

	it('fails cleanly on a datastore-only session instead of mounting a dead iframe', () => {
		const container = document.createElement('div');
		document.body.appendChild(container);

		// A datastore session carries no editor URL: embedUrl is empty and
		// the scopes name the datastore family.
		expect(() =>
			mountWorkflowEditor({
				container,
				baseUrl: EDITOR_ORIGIN,
				session: session({ embedUrl: '', scopes: ['datastore:read'], datastoreId: 'datastore_1' })
			})
		).toThrow(/datastore-scoped session for datastore datastore_1/);

		// Cleanly means nothing was mounted and nothing is pending: no
		// iframe to handshake, no timer to fire, no listener left behind.
		expect(container.querySelector('iframe')).toBeNull();
	});
});

/** Minimal EventSource stand-in; jsdom has none. */
class FakeEventSource {
	static last: FakeEventSource | undefined;
	static instances: FakeEventSource[] = [];
	readonly url: string;
	closed = false;
	listeners = new Map<string, Set<EventListener>>();

	constructor(url: string) {
		this.url = url;
		FakeEventSource.last = this;
		FakeEventSource.instances.push(this);
	}

	addEventListener(name: string, listener: EventListener) {
		if (!this.listeners.has(name)) this.listeners.set(name, new Set());
		this.listeners.get(name)!.add(listener);
	}

	removeEventListener(name: string, listener: EventListener) {
		this.listeners.get(name)?.delete(listener);
	}

	close() {
		this.closed = true;
	}

	emit(name: string, data: unknown) {
		for (const listener of this.listeners.get(name) ?? []) {
			listener(new MessageEvent(name, { data: JSON.stringify(data) }) as unknown as Event);
		}
	}

	/** Fires the error listeners, as a dropped connection would. */
	fail() {
		for (const listener of this.listeners.get('error') ?? []) {
			listener(new Event('error'));
		}
	}

	listenerCount() {
		let total = 0;
		for (const set of this.listeners.values()) total += set.size;
		return total;
	}
}

describe('subscribeExecutionEvents', () => {
	it('subscribes to the execution stream and delivers events', () => {
		vi.stubGlobal('EventSource', FakeEventSource);
		const events: unknown[] = [];
		const unsubscribe = subscribeExecutionEvents({
			baseUrl: EDITOR_ORIGIN,
			executionId: 'exec-1',
			onEvent: (event) => events.push(event)
		});

		expect(FakeEventSource.last?.url).toBe(`${EDITOR_ORIGIN}/api/v1/executions/exec-1/events`);
		FakeEventSource.last!.emit('node.completed', { id: 1, type: 'node.completed', executionId: 'exec-1', at: 'now' });
		expect(events).toHaveLength(1);

		unsubscribe();
		vi.unstubAllGlobals();
	});

	it('closes itself on a terminal event so a finished run is not reconnected to', () => {
		vi.stubGlobal('EventSource', FakeEventSource);
		const onClose = vi.fn();
		subscribeExecutionEvents({
			baseUrl: EDITOR_ORIGIN,
			executionId: 'exec-1',
			onEvent: () => undefined,
			onClose
		});

		FakeEventSource.last!.emit('execution.completed', {
			id: 2,
			type: 'execution.completed',
			executionId: 'exec-1',
			at: 'now'
		});

		expect(FakeEventSource.last!.closed).toBe(true);
		expect(FakeEventSource.last!.listenerCount()).toBe(0);
		expect(onClose).toHaveBeenCalledTimes(1);
		vi.unstubAllGlobals();
	});

	it('unsubscribes cleanly and is safe to call twice', () => {
		vi.stubGlobal('EventSource', FakeEventSource);
		const unsubscribe = subscribeExecutionEvents({
			baseUrl: EDITOR_ORIGIN,
			executionId: 'exec-1',
			onEvent: () => undefined
		});

		unsubscribe();
		unsubscribe();

		expect(FakeEventSource.last!.closed).toBe(true);
		expect(FakeEventSource.last!.listenerCount()).toBe(0);
		vi.unstubAllGlobals();
	});
});

describe('subscribeExecutionEvents with a stream ticket', () => {
	it('spends a fixed ticket as a query parameter', () => {
		vi.stubGlobal('EventSource', FakeEventSource);
		const unsubscribe = subscribeExecutionEvents({
			baseUrl: EDITOR_ORIGIN,
			executionId: 'exec-1',
			onEvent: () => undefined,
			ticket: 'single-use-ticket'
		});

		expect(FakeEventSource.last?.url).toBe(
			`${EDITOR_ORIGIN}/api/v1/executions/exec-1/events?ticket=single-use-ticket`
		);
		unsubscribe();
		vi.unstubAllGlobals();
	});

	it('mints a fresh ticket per connect and resumes after the last event received', async () => {
		vi.useFakeTimers();
		vi.stubGlobal('EventSource', FakeEventSource);
		FakeEventSource.instances.length = 0;
		let minted = 0;
		const ticket = vi.fn(async () => `ticket-${++minted}`);
		const onError = vi.fn();
		const events: unknown[] = [];
		const unsubscribe = subscribeExecutionEvents({
			baseUrl: EDITOR_ORIGIN,
			executionId: 'exec-1',
			onEvent: (event) => events.push(event),
			onError,
			ticket
		});

		await vi.advanceTimersByTimeAsync(0);
		expect(ticket).toHaveBeenCalledTimes(1);
		// No resume position on the first connect: nothing received yet.
		expect(FakeEventSource.last?.url).toBe(
			`${EDITOR_ORIGIN}/api/v1/executions/exec-1/events?ticket=ticket-1`
		);

		const first = FakeEventSource.last!;
		first.emit('node.completed', { id: 7, type: 'node.completed', executionId: 'exec-1', at: 'now' });
		expect(events).toHaveLength(1);
		first.fail();
		expect(onError).toHaveBeenCalledTimes(1);
		expect(first.closed).toBe(true);

		await vi.advanceTimersByTimeAsync(2000);
		// A fresh ticket, never the spent one — and the resume cursor the
		// server replays retained events after, so nothing is lost.
		expect(ticket).toHaveBeenCalledTimes(2);
		expect(FakeEventSource.last?.url).toBe(
			`${EDITOR_ORIGIN}/api/v1/executions/exec-1/events?ticket=ticket-2&from=7`
		);
		expect(FakeEventSource.last).not.toBe(first);

		unsubscribe();
		vi.unstubAllGlobals();
	});

	it('still closes on a terminal event when ticketed, without minting again', async () => {
		vi.useFakeTimers();
		vi.stubGlobal('EventSource', FakeEventSource);
		let minted = 0;
		const ticket = vi.fn(async () => `ticket-${++minted}`);
		const onClose = vi.fn();
		const unsubscribe = subscribeExecutionEvents({
			baseUrl: EDITOR_ORIGIN,
			executionId: 'exec-1',
			onEvent: () => undefined,
			onClose,
			ticket
		});

		await vi.advanceTimersByTimeAsync(0);
		FakeEventSource.last!.emit('execution.completed', {
			id: 9,
			type: 'execution.completed',
			executionId: 'exec-1',
			at: 'now'
		});
		await vi.advanceTimersByTimeAsync(10_000);

		expect(onClose).toHaveBeenCalledTimes(1);
		expect(ticket).toHaveBeenCalledTimes(1);
		expect(FakeEventSource.last!.closed).toBe(true);
		unsubscribe();
		vi.unstubAllGlobals();
	});

	it('cancels a pending reconnect on unsubscribe', async () => {
		vi.useFakeTimers();
		vi.stubGlobal('EventSource', FakeEventSource);
		let minted = 0;
		const ticket = vi.fn(async () => `ticket-${++minted}`);
		const unsubscribe = subscribeExecutionEvents({
			baseUrl: EDITOR_ORIGIN,
			executionId: 'exec-1',
			onEvent: () => undefined,
			ticket
		});

		await vi.advanceTimersByTimeAsync(0);
		FakeEventSource.last!.fail();
		unsubscribe();
		await vi.advanceTimersByTimeAsync(10_000);

		expect(ticket).toHaveBeenCalledTimes(1);
		vi.unstubAllGlobals();
	});
});
