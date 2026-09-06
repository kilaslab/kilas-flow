/**
 * Browser-side KilasFlow integration.
 *
 * Nothing here needs or accepts a privileged API key. A host's backend mints a
 * session with `./server` and passes only the resulting token to the page, so
 * a browser bundle built from this module cannot carry server credentials even
 * by mistake — there is no import path from here to the server client.
 */

/** What `createEmbedSession` returned, as the browser receives it. */
export interface EmbedSessionHandle {
	token: string;
	embedUrl: string;
	expiresAt: string;
	scopes: string[];
	/** The one origin this token may be used from: this page's own origin. */
	origin: string;
	branding?: Record<string, unknown>;
}

export interface MountOptions {
	/** Element the iframe is appended to. */
	container: HTMLElement;
	/** Absolute base URL of the KilasFlow deployment. */
	baseUrl: string;
	/** The session minted by the host's backend. */
	session: EmbedSessionHandle;
	/** Workflow to open. Defaults to the one the session was minted for. */
	workflowId?: string;
	title?: string;
	className?: string;
	/** Called for every message the editor sends back. */
	onEvent?: (event: EditorEvent) => void;
	/** Called when the editor could not be mounted or the handshake failed. */
	onError?: (error: Error) => void;
	/** How long to wait for the editor to announce itself. Default 15s. */
	handshakeTimeoutMs?: number;
}

/** Messages the embedded editor sends to its host. */
export type EditorEvent =
	| { type: 'ready'; workflowId: string }
	| { type: 'workflow-saved'; workflowId: string; revision: number }
	| { type: 'execution-started'; workflowId: string; executionId: string }
	| { type: 'execution-finished'; workflowId: string; executionId: string; status: string };

export interface MountedEditor {
	iframe: HTMLIFrameElement;
	/** Removes the iframe and every listener and timer it installed. */
	unmount(): void;
}

const MESSAGE_PREFIX = 'kilasflow:';

/**
 * Mounts the embedded editor in an iframe and performs the session handshake.
 *
 * The editor announces itself, and only then is the token sent — to the
 * editor's exact origin, never `'*'`. Every message received is checked
 * against `event.origin` before it is read.
 */
export function mountWorkflowEditor(options: MountOptions): MountedEditor {
	const { container, session } = options;
	if (!container) throw new Error('mountWorkflowEditor needs a container element');
	if (!session?.token) throw new Error('mountWorkflowEditor needs an embed session token');

	const editorOrigin = new URL(options.baseUrl).origin;
	const workflowId = options.workflowId ?? workflowIdFrom(session);
	if (!workflowId) throw new Error('mountWorkflowEditor could not determine the workflow to open');

	const iframe = document.createElement('iframe');
	iframe.src = `${editorOrigin}/embed/${encodeURIComponent(workflowId)}`;
	iframe.title = options.title ?? 'Workflow editor';
	if (options.className) iframe.className = options.className;
	iframe.style.border = iframe.style.border || '0';
	iframe.style.width = iframe.style.width || '100%';
	iframe.style.height = iframe.style.height || '100%';
	// The editor needs scripts and same-origin access to its own API; it is
	// denied top-level navigation and popups, so a compromised frame cannot
	// navigate the host page away.
	iframe.setAttribute('sandbox', 'allow-scripts allow-same-origin allow-forms');
	iframe.setAttribute('referrerpolicy', 'origin');

	let settled = false;
	let handshakeTimer: ReturnType<typeof setTimeout> | undefined;

	function onMessage(event: MessageEvent) {
		// Origin first, before the payload is inspected at all.
		if (event.origin !== editorOrigin) return;
		if (event.source !== iframe.contentWindow) return;

		const data = event.data as Record<string, unknown> | null;
		const type = typeof data?.type === 'string' ? data.type : '';
		if (!type.startsWith(MESSAGE_PREFIX)) return;
		const name = type.slice(MESSAGE_PREFIX.length);

		if (name === 'embed-ready') {
			settled = true;
			if (handshakeTimer) clearTimeout(handshakeTimer);
			iframe.contentWindow?.postMessage(
				{
					type: 'kilasflow:embed-session',
					token: session.token,
					workflowId,
					scopes: session.scopes,
					branding: session.branding ?? {}
				},
				// Explicit target origin. '*' would hand the token to whatever
				// document happened to occupy the frame.
				editorOrigin
			);
			options.onEvent?.({ type: 'ready', workflowId });
			return;
		}

		options.onEvent?.({ ...(data as object), type: name } as EditorEvent);
	}

	window.addEventListener('message', onMessage);
	container.appendChild(iframe);

	handshakeTimer = setTimeout(() => {
		if (settled) return;
		options.onError?.(new Error('The KilasFlow editor did not complete its session handshake.'));
	}, options.handshakeTimeoutMs ?? 15_000);

	return {
		iframe,
		unmount() {
			window.removeEventListener('message', onMessage);
			if (handshakeTimer) clearTimeout(handshakeTimer);
			iframe.remove();
		}
	};
}

function workflowIdFrom(session: EmbedSessionHandle): string {
	const match = /\/embed\/([^/?#]+)/.exec(session.embedUrl ?? '');
	return match?.[1] ? decodeURIComponent(match[1]) : '';
}

/** One standardized execution event, as the live feed sends it. */
export interface ExecutionEvent {
	id: number;
	type: string;
	executionId: string;
	workflowId?: string;
	nodeId?: string;
	status?: string;
	sequence?: number;
	at: string;
	data?: unknown;
}

export interface ExecutionEventOptions {
	baseUrl: string;
	executionId: string;
	onEvent: (event: ExecutionEvent) => void;
	onError?: (error: Event) => void;
	/** Called once the run reaches a terminal state and the stream closes. */
	onClose?: () => void;
	/**
	 * A stream ticket for an authenticated server, spent as `?ticket=`.
	 * `EventSource` cannot send an `Authorization` header, so the host
	 * backend mints one per execution with
	 * `KilasFlowClient.createStreamTicket` and hands the mint to the page —
	 * never the API key.
	 *
	 * A ticket is single-use and lives for seconds, and the mint must sit
	 * adjacent to the connect rather than at component setup. Pass a function
	 * and the SDK calls it before every connect — first and every reconnect
	 * — so a dropped stream reopens with a fresh ticket instead of replaying
	 * a spent one, which the server refuses with a 401. A fixed string is
	 * spent once and only suits a stream that never reconnects.
	 */
	ticket?: string | (() => Promise<string>);
}

const TERMINAL_EVENTS = ['execution.completed', 'execution.failed', 'execution.cancelled'];

const EXECUTION_EVENT_NAMES = [
	'execution.started',
	'execution.completed',
	'execution.failed',
	'execution.cancelled',
	'node.started',
	'node.output',
	'node.completed',
	'node.failed',
	'workflow.saved'
];

/**
 * Delay before reopening a dropped ticketed stream. The server advertises
 * `Retry: 2000` on the stream for native retries; the manual reconnect for a
 * ticketed stream waits the same, so a flap does not turn into a mint loop.
 */
const RECONNECT_DELAY_MS = 2000;

/**
 * Subscribes to an execution's live events.
 *
 * Without a ticket this is one `EventSource`: it reconnects and resends
 * `Last-Event-ID` on its own, so a dropped connection resumes rather than
 * replaying the run. The subscription closes itself on a terminal event,
 * because the server closes too and a client that kept reconnecting would
 * hammer a finished run.
 *
 * With a ticket minter the SDK takes over reconnecting. A ticket is
 * single-use, so the dead connection's ticket is spent and a native retry
 * would fail with a 401 forever; on an error the SDK closes the dead source,
 * mints a fresh ticket, and opens a new source resuming after the last event
 * received (`?from=`, the query fallback the server offers clients that
 * cannot set headers). Terminal events still close everything: the run is
 * over, and there is nothing to resume.
 *
 * Returns an unsubscribe function that is safe to call more than once. It
 * also cancels a mint or reconnect that has not happened yet.
 */
export function subscribeExecutionEvents(options: ExecutionEventOptions): () => void {
	const origin = new URL(options.baseUrl).origin;
	const minter = typeof options.ticket === 'function' ? options.ticket : undefined;
	const fixedTicket = typeof options.ticket === 'string' ? options.ticket : undefined;
	let closed = false;
	let source: EventSource | undefined;
	let lastEventId: string | undefined;
	let reconnectTimer: ReturnType<typeof setTimeout> | undefined;

	function streamUrl(ticket: string | undefined): string {
		let url = `${origin}/api/v1/executions/${encodeURIComponent(options.executionId)}/events`;
		const query = new URLSearchParams();
		if (ticket !== undefined) query.set('ticket', ticket);
		// The server replays retained events after this id, so nothing the
		// run published between the drop and the reconnect is lost.
		if (lastEventId !== undefined) query.set('from', lastEventId);
		const suffix = query.toString();
		if (suffix) url += `?${suffix}`;
		return url;
	}

	function attach(next: EventSource): void {
		source = next;
		// The server names each event, so there is no default `message` type
		// to listen on; every known name routes to the same handler.
		for (const name of EXECUTION_EVENT_NAMES) next.addEventListener(name, onMessage as EventListener);
		next.addEventListener('error', onError);
	}

	function detach(next: EventSource): void {
		for (const name of EXECUTION_EVENT_NAMES) next.removeEventListener(name, onMessage as EventListener);
		next.removeEventListener('error', onError);
		next.close();
	}

	function close() {
		if (closed) return;
		closed = true;
		if (reconnectTimer !== undefined) {
			clearTimeout(reconnectTimer);
			reconnectTimer = undefined;
		}
		if (source) detach(source);
		source = undefined;
	}

	function onMessage(message: MessageEvent<string>) {
		let event: ExecutionEvent;
		try {
			event = JSON.parse(message.data) as ExecutionEvent;
		} catch {
			return;
		}
		// Prefer the transport's own cursor; fall back to the payload id.
		if (message.lastEventId) lastEventId = message.lastEventId;
		else if (event.id !== undefined) lastEventId = String(event.id);
		options.onEvent(event);
		if (TERMINAL_EVENTS.includes(event.type)) {
			close();
			options.onClose?.();
		}
	}

	function onError(event: Event) {
		options.onError?.(event);
		// No ticket minter, no takeover: the single EventSource retries on
		// its own with Last-Event-ID, exactly as before. A fixed string is
		// spent on the first connect, so retrying with it is equally
		// pointless — but the host chose a string, and silently upgrading it
		// to a minter would hide that the stream cannot survive a drop.
		if (closed || minter === undefined || source === undefined) return;
		detach(source);
		source = undefined;
		// Adjacent to the connect, never earlier: a ticket minted now and
		// spent after a long pause may expire before it is used.
		reconnectTimer = setTimeout(() => void connect(), RECONNECT_DELAY_MS);
	}

	async function connect(): Promise<void> {
		if (closed || source) return;
		if (minter === undefined) {
			attach(new EventSource(streamUrl(fixedTicket)));
			return;
		}
		let ticket: string;
		try {
			ticket = await minter();
		} catch {
			// The mint failed; the run is still going, so try again rather
			// than abandoning the stream. Unsubscribe clears the timer.
			if (!closed) reconnectTimer = setTimeout(() => void connect(), RECONNECT_DELAY_MS);
			return;
		}
		if (closed) return;
		attach(new EventSource(streamUrl(ticket)));
	}

	void connect();
	return close;
}
