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
});

/** Minimal EventSource stand-in; jsdom has none. */
class FakeEventSource {
	static last: FakeEventSource | undefined;
	readonly url: string;
	closed = false;
	listeners = new Map<string, Set<EventListener>>();

	constructor(url: string) {
		this.url = url;
		FakeEventSource.last = this;
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
