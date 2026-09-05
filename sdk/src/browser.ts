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
 * Subscribes to an execution's live events.
 *
 * `EventSource` reconnects and resends `Last-Event-ID` on its own, so a
 * dropped connection resumes rather than replaying the run. The subscription
 * closes itself on a terminal event, because the server closes too and a
 * client that kept reconnecting would hammer a finished run.
 *
 * Returns an unsubscribe function that is safe to call more than once.
 */
export function subscribeExecutionEvents(options: ExecutionEventOptions): () => void {
	const origin = new URL(options.baseUrl).origin;
	const source = new EventSource(`${origin}/api/v1/executions/${encodeURIComponent(options.executionId)}/events`);
	let closed = false;

	function close() {
		if (closed) return;
		closed = true;
		for (const name of EXECUTION_EVENT_NAMES) source.removeEventListener(name, onMessage as EventListener);
		source.removeEventListener('error', onError);
		source.close();
	}

	function onMessage(message: MessageEvent<string>) {
		let event: ExecutionEvent;
		try {
			event = JSON.parse(message.data) as ExecutionEvent;
		} catch {
			return;
		}
		options.onEvent(event);
		if (TERMINAL_EVENTS.includes(event.type)) {
			close();
			options.onClose?.();
		}
	}

	function onError(event: Event) {
		options.onError?.(event);
	}

	// The server names each event, so there is no default `message` type to
	// listen on; every known name routes to the same handler.
	for (const name of EXECUTION_EVENT_NAMES) source.addEventListener(name, onMessage as EventListener);
	source.addEventListener('error', onError);

	return close;
}
