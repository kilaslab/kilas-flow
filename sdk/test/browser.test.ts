// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest';

import { EMBED_PROTOCOL_VERSION, mountWorkflowEditor, subscribeExecutionEvents, type EditorEvent } from '../src/browser.js';

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

/** What the SDK posted to the frame window, in order: [payload, targetOrigin]. */
function postedTo(frameWindow: Window): [unknown, string][] {
	const post = frameWindow.postMessage as unknown as { mock: { calls: [unknown, string][] } };
	return post.mock.calls;
}

/** Clears the frame-window postMessage mock between phases of a test. */
function clearPosted(frameWindow: Window): void {
	(frameWindow.postMessage as unknown as ReturnType<typeof vi.fn>).mockClear();
}

/** Completes the handshake: the frame announces itself, protocol v2 by default. */
function announce(frameWindow: Window, protocol = EMBED_PROTOCOL_VERSION): void {
	window.dispatchEvent(
		new MessageEvent('message', {
			origin: EDITOR_ORIGIN,
			source: frameWindow,
			data: { type: 'kilasflow:embed-ready', workflowId: 'wf-1', ...(protocol ? { protocol } : {}) }
		})
	);
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

	it('posts the mount-time locale with the session', () => {
		const { mounted, frameWindow } = mount({ locale: 'id' });

		window.dispatchEvent(
			new MessageEvent('message', {
				origin: EDITOR_ORIGIN,
				source: frameWindow,
				data: { type: 'kilasflow:embed-ready', workflowId: 'wf-1' }
			})
		);

		const [payload, targetOrigin] = postedTo(frameWindow)[0]!;
		expect(payload).toMatchObject({ locale: 'id' });
		// The locale travels with the token, so it must not widen the target.
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

describe('mountWorkflowEditor protocol v2', () => {
	it('carries the protocol and the expiry with the session, and reports the frame version on ready', () => {
		const events: EditorEvent[] = [];
		const initial = session();
		const { mounted, frameWindow } = mount({ session: initial, onEvent: (event) => events.push(event) });

		announce(frameWindow);

		const [payload, targetOrigin] = postedTo(frameWindow)[0]!;
		expect(payload).toMatchObject({ type: 'kilasflow:embed-session', protocol: EMBED_PROTOCOL_VERSION, expiresAt: initial.expiresAt });
		expect(targetOrigin).toBe(EDITOR_ORIGIN);
		expect(events[0]).toMatchObject({ type: 'ready', protocol: EMBED_PROTOCOL_VERSION });
		mounted.unmount();
	});

	it('setSession swaps the token without reloading the frame', () => {
		const { mounted, frameWindow } = mount();
		announce(frameWindow);
		clearPosted(frameWindow);

		const replacement = session({ token: 'kfe1.c.d', expiresAt: new Date(Date.now() + 900_000).toISOString() });
		mounted.setSession(replacement);

		// One more session message to the same frame at the same exact
		// origin — and the frame's src is untouched, which is what "without
		// a reload" means from the host's side.
		expect(postedTo(frameWindow)).toHaveLength(1);
		const [payload, targetOrigin] = postedTo(frameWindow)[0]!;
		expect(payload).toMatchObject({ type: 'kilasflow:embed-session', token: 'kfe1.c.d', expiresAt: replacement.expiresAt });
		expect(targetOrigin).toBe(EDITOR_ORIGIN);
		expect(mounted.iframe.src).toBe(`${EDITOR_ORIGIN}/embed/wf-1`);
		mounted.unmount();
	});

	it('setSession refuses a session it cannot use', () => {
		const { mounted, frameWindow } = mount();
		// Before the handshake completed, there is no listener on the other
		// side: the message would be lost, so it is a host error instead.
		expect(() => mounted.setSession(session({ token: 'late' }))).toThrow(/handshake/);

		announce(frameWindow);
		expect(() => mounted.setSession(session({ token: '' }))).toThrow(/token/);
		// A token minted for another workflow would be refused by the frame's
		// own check; saying so here names the mistake where it was made.
		expect(() => mounted.setSession(session({ token: 'ok', embedUrl: '/embed/wf-9' }))).toThrow(/wf-9/);
		expect(postedTo(frameWindow)).toHaveLength(1); // only the handshake's session message
		mounted.unmount();
	});

	it('sends commands with a request id and resolves the origin-checked reply', async () => {
		const onEvent = vi.fn();
		const { mounted, frameWindow } = mount({ onEvent });
		announce(frameWindow);
		clearPosted(frameWindow);
		onEvent.mockClear();

		const saving = mounted.save();
		const [payload, targetOrigin] = postedTo(frameWindow)[0]!;
		expect(payload).toMatchObject({ type: 'kilasflow:command', protocol: EMBED_PROTOCOL_VERSION, command: 'save' });
		expect(typeof (payload as { requestId: string }).requestId).toBe('string');
		expect(targetOrigin).toBe(EDITOR_ORIGIN);

		const requestId = (payload as { requestId: string }).requestId;
		window.dispatchEvent(
			new MessageEvent('message', {
				origin: EDITOR_ORIGIN,
				source: frameWindow,
				data: { type: 'kilasflow:command-result', requestId, ok: true, saved: true, dirty: false, revision: 4 }
			})
		);
		await expect(saving).resolves.toEqual({ ok: true, saved: true, dirty: false, revision: 4 });
		// The reply is plumbing for the promise, not an event for the host.
		expect(onEvent).not.toHaveBeenCalled();
		mounted.unmount();
	});

	it('ignores a command reply from another origin, and one it did not ask for', async () => {
		vi.useFakeTimers();
		const { mounted, frameWindow } = mount({ commandTimeoutMs: 1000 });
		announce(frameWindow);
		clearPosted(frameWindow);

		const focusing = mounted.focusNode('webhook');
		// Attached before the timers advance, so the rejection is expected the
		// moment it fires rather than observed afterwards.
		const refused = expect(focusing).rejects.toThrow(/did not answer/);
		const requestId = (postedTo(frameWindow)[0]![0] as { requestId: string }).requestId;

		window.dispatchEvent(
			new MessageEvent('message', {
				origin: 'https://evil.example',
				source: frameWindow,
				data: { type: 'kilasflow:command-result', requestId, ok: true }
			})
		);
		window.dispatchEvent(
			new MessageEvent('message', {
				origin: EDITOR_ORIGIN,
				source: frameWindow,
				data: { type: 'kilasflow:command-result', requestId: 'cmd-not-mine', ok: true }
			})
		);
		await vi.advanceTimersByTimeAsync(1500);
		await refused;
		mounted.unmount();
	});

	it('rejects a command that times out, and another sent to a protocol-1 frame', async () => {
		vi.useFakeTimers();
		const first = mount({ commandTimeoutMs: 1000 });
		announce(first.frameWindow);
		const refused = expect(first.mounted.save()).rejects.toThrow(/did not answer the save command/);
		await vi.advanceTimersByTimeAsync(1500);
		await refused;
		first.mounted.unmount();

		// A frame that announced no protocol version carries no commands at
		// all: refused up front rather than left hanging forever.
		const second = mount();
		announce(second.frameWindow, 0);
		await expect(second.mounted.run()).rejects.toThrow(/protocol/);
		await expect(second.mounted.focusNode('x')).rejects.toThrow(/protocol/);
		second.mounted.unmount();
	});

	it('rejects commands sent before the handshake and pending ones on unmount', async () => {
		const { mounted, frameWindow } = mount();

		await expect(mounted.save()).rejects.toThrow(/not ready/);

		announce(frameWindow);
		const running = mounted.focusNode('trigger');
		mounted.unmount();
		await expect(running).rejects.toThrow(/unmounted/);
	});

	it('forwards the protocol v2 events as they arrive', () => {
		const events: EditorEvent[] = [];
		const { mounted, frameWindow } = mount({ onEvent: (event) => events.push(event) });
		announce(frameWindow);

		for (const data of [
			{ type: 'kilasflow:dirty-changed', workflowId: 'wf-1', dirty: true },
			{ type: 'kilasflow:session-expiring', workflowId: 'wf-1', expiresAt: '2026-10-25T16:00:00.000Z' },
			{ type: 'kilasflow:unauthorized', workflowId: 'wf-1' },
			{ type: 'kilasflow:workflow-published', workflowId: 'wf-1', versionId: 'ver-8', revision: 8 },
			{ type: 'kilasflow:error', workflowId: 'wf-1', code: 'save-failed', message: '409 — stale revision' }
		]) {
			window.dispatchEvent(new MessageEvent('message', { origin: EDITOR_ORIGIN, source: frameWindow, data }));
		}

		expect(events.slice(1).map((event) => event.type)).toEqual([
			'dirty-changed',
			'session-expiring',
			'unauthorized',
			'workflow-published',
			'error'
		]);
		expect(events.at(-1)).toMatchObject({ code: 'save-failed', message: '409 — stale revision' });
		mounted.unmount();
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
