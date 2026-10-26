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
	/**
	 * The one datastore a datastore-scoped session may touch. Present only
	 * on such sessions — which cannot open the workflow editor at all.
	 */
	datastoreId?: string;
}

/**
 * The postMessage protocol this SDK speaks with the embedded editor.
 *
 * Version 2 is everything version 1 was — the `embed-ready` handshake, one
 * `embed-session` reply, editor events — plus a token refresh that swaps the
 * session without reloading the frame (`setSession`, with `expiresAt` warned
 * about by a `session-expiring` event sixty seconds ahead), the
 * `unauthorized`, `dirty-changed` and `error` events, a typed
 * `workflow-published`, and the `save` / `run` / `focusNode` commands. The
 * frame announces its own version with `embed-ready`; commands are refused
 * up front when the frame did not announce at least 2, rather than leaving a
 * promise that nothing will ever answer.
 */
export const EMBED_PROTOCOL_VERSION = 2;

export interface MountOptions {
	/** Element the iframe is appended to. */
	container: HTMLElement;
	/** Absolute base URL of the KilasFlow deployment. */
	baseUrl: string;
	/** The session minted by the host's backend. */
	session: EmbedSessionHandle;
	/** Workflow to open. Defaults to the one the session was minted for. */
	workflowId?: string;
	/**
	 * Language tag for the editor's chrome, e.g. `'id'`.
	 *
	 * The editor validates it against the catalog list it ships and ignores a
	 * tag it does not carry — an unknown locale is never an error, because the
	 * host's language is a preference rather than an authorization. When it is
	 * given, it also outranks any language the reader previously chose on the
	 * editor's own origin: the host decides how its page reads.
	 */
	locale?: string;
	title?: string;
	className?: string;
	/** Called for every message the editor sends back. */
	onEvent?: (event: EditorEvent) => void;
	/** Called when the editor could not be mounted or the handshake failed. */
	onError?: (error: Error) => void;
	/** How long to wait for the editor to announce itself. Default 15s. */
	handshakeTimeoutMs?: number;
	/**
	 * How long a command (`save`, `focusNode`) may go unanswered before its
	 * promise rejects. Default 15s.
	 *
	 * `run` is the exception: it resolves at the run's terminal state, which
	 * is however long the workflow takes, so it is never timed out unless a
	 * value is given here — watch `execution-started` / `execution-finished`
	 * for progress instead.
	 */
	commandTimeoutMs?: number;
}

/** Messages the embedded editor sends to its host. */
export type EditorEvent =
	| { type: 'ready'; workflowId: string; /** The frame's protocol version; 1 when the frame did not say. */ protocol: number }
	| { type: 'workflow-saved'; workflowId: string; revision: number }
	/** A revision was pinned as the one production traffic runs. */
	| { type: 'workflow-published'; workflowId: string; versionId: string; revision: number }
	| { type: 'execution-started'; workflowId: string; executionId: string }
	| { type: 'execution-finished'; workflowId: string; executionId: string; status: string }
	/** The canvas gained or lost unsaved edits (`dirty` is the new state). */
	| { type: 'dirty-changed'; workflowId: string; dirty: boolean }
	/**
	 * The session token is sixty seconds from expiring (or already past it).
	 * Mint a fresh session and hand it over with `setSession` — the editor is
	 * not reloaded and the user's work survives.
	 */
	| { type: 'session-expiring'; workflowId: string; expiresAt: string }
	/**
	 * The server refused the token outright. Nothing this frame can do will
	 * recover it: mint a new session and `setSession` it, or unmount.
	 */
	| { type: 'unauthorized'; workflowId: string }
	/** Something the editor tried failed; `code` names the family. */
	| { type: 'error'; workflowId: string; code: string; message?: string };

/** A command the host sent was refused; `code` names the refusal. */
export type CommandFailure = { ok: false; code: string; message?: string; dirty?: boolean };

export type SaveResult = { ok: true; saved: boolean; dirty: boolean; revision?: number } | CommandFailure;

export type RunResult = { ok: true; executionId: string | null; status?: string } | CommandFailure;

export type FocusResult = { ok: true } | CommandFailure;

export interface MountedEditor {
	iframe: HTMLIFrameElement;
	/**
	 * Swaps the session's token without reloading the editor.
	 *
	 * For a host that listened to `session-expiring` (or `unauthorized`): mint
	 * the replacement on the backend and hand it over. The frame re-attaches
	 * the token in place — the canvas, the selection and the viewport survive,
	 * and nothing the user was doing is interrupted.
	 */
	setSession(session: EmbedSessionHandle): void;
	/**
	 * Saves the canvas. Resolves when the save has settled: `saved` is false
	 * when there was nothing to save, `ok: false` when the save was refused
	 * (`code: 'conflict'` names a newer server revision).
	 */
	save(): Promise<SaveResult>;
	/**
	 * Runs the workflow exactly as the editor's own Execute button does
	 * (saving first when the canvas is dirty). Resolves at the run's terminal
	 * state with the execution id — which is null when the workflow is a chat
	 * graph and the frame opened the chat panel instead.
	 */
	run(): Promise<RunResult>;
	/** Selects a node and opens its inspector; refuses an id not on the canvas. */
	focusNode(nodeId: string): Promise<FocusResult>;
	/** Removes the iframe and every listener and timer it installed. */
	unmount(): void;
}

const MESSAGE_PREFIX = 'kilasflow:';
const DEFAULT_COMMAND_TIMEOUT_MS = 15_000;

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
	if (!workflowId) {
		// A datastore-only session names no workflow, so there is no editor
		// URL to load. Say so outright: falling through would mount an
		// iframe at a bare /embed/ that this very token is refused on, then
		// hang until the handshake timer fires.
		if (isDatastoreSession(session)) {
			throw new Error(
				'mountWorkflowEditor cannot open the workflow editor with a datastore-scoped session' +
					(session.datastoreId ? ` for datastore ${session.datastoreId}` : '') +
					': mint a workflow session instead'
			);
		}
		throw new Error('mountWorkflowEditor could not determine the workflow to open');
	}

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
	/** The frame's protocol version, learned from its `embed-ready` announcement. */
	let frameProtocol = 1;
	/** Commands awaiting a reply, by request id. */
	const pending = new Map<
		string,
		{ resolve: (value: never) => void; reject: (error: Error) => void; timer?: ReturnType<typeof setTimeout> }
	>();
	let commandSequence = 0;

	function postSessionMessage(session: EmbedSessionHandle): void {
		iframe.contentWindow?.postMessage(
			{
				type: 'kilasflow:embed-session',
				protocol: EMBED_PROTOCOL_VERSION,
				token: session.token,
				workflowId,
				scopes: session.scopes,
				expiresAt: session.expiresAt,
				branding: session.branding ?? {},
				locale: options.locale
			},
			// Explicit target origin. '*' would hand the token to whatever
			// document happened to occupy the frame.
			editorOrigin
		);
	}

	function failPending(error: Error): void {
		for (const entry of pending.values()) {
			if (entry.timer) clearTimeout(entry.timer);
			entry.reject(error);
		}
		pending.clear();
	}

	function sendCommand<T>(
		command: string,
		args: Record<string, unknown> = {},
		timeoutMs: number | undefined = options.commandTimeoutMs ?? DEFAULT_COMMAND_TIMEOUT_MS
	): Promise<T> {
		if (!settled) return Promise.reject(new Error(`The editor is not ready yet; wait for the ready event before calling ${command}().`));
		if (frameProtocol < EMBED_PROTOCOL_VERSION) {
			return Promise.reject(new Error(`The embedded editor speaks postMessage protocol ${frameProtocol}, which carries no commands.`));
		}
		const requestId = `cmd-${++commandSequence}`;
		return new Promise<T>((resolve, reject) => {
			const entry = { resolve: resolve as (value: never) => void, reject, timer: undefined as ReturnType<typeof setTimeout> | undefined };
			if (timeoutMs !== undefined && timeoutMs > 0) {
				entry.timer = setTimeout(() => {
					pending.delete(requestId);
					reject(new Error(`The editor did not answer the ${command} command within ${timeoutMs}ms.`));
				}, timeoutMs);
			}
			pending.set(requestId, entry);
			iframe.contentWindow?.postMessage({ type: 'kilasflow:command', protocol: EMBED_PROTOCOL_VERSION, requestId, command, ...args }, editorOrigin);
		});
	}

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
			frameProtocol = typeof data?.protocol === 'number' ? data.protocol : 1;
			if (handshakeTimer) clearTimeout(handshakeTimer);
			postSessionMessage(session);
			options.onEvent?.({ type: 'ready', workflowId, protocol: frameProtocol });
			return;
		}

		if (name === 'command-result') {
			// Protocol plumbing, not a host event: routed to the promise it
			// answers, and never forwarded to onEvent.
			const requestId = typeof data?.requestId === 'string' ? data.requestId : '';
			const entry = pending.get(requestId);
			if (!entry) return;
			pending.delete(requestId);
			if (entry.timer) clearTimeout(entry.timer);
			const { requestId: _requestId, type: _type, protocol: _protocol, ...result } = data as Record<string, unknown>;
			(entry.resolve as (value: unknown) => void)(result);
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
		setSession(next: EmbedSessionHandle) {
			if (!next?.token) throw new Error('setSession needs a session with a token');
			if (!settled) throw new Error('setSession cannot run before the editor has completed its handshake.');
			// The frame is open on one workflow; a token minted for another
			// would be refused by the frame's own check, so say so here where
			// the mistake was made.
			const named = workflowIdFrom(next);
			if (named && named !== workflowId) {
				throw new Error(`setSession cannot swap to a session for workflow ${named} while the editor is open on ${workflowId}.`);
			}
			postSessionMessage(next);
		},
		save() {
			return sendCommand<SaveResult>('save');
		},
		run() {
			// No default timeout: a run resolves at its terminal state, which
			// is however long the workflow takes. The frame always replies.
			return sendCommand<RunResult>('run', {}, options.commandTimeoutMs);
		},
		focusNode(nodeId: string) {
			return sendCommand<FocusResult>('focusNode', { nodeId });
		},
		unmount() {
			window.removeEventListener('message', onMessage);
			if (handshakeTimer) clearTimeout(handshakeTimer);
			failPending(new Error('The editor was unmounted before it answered.'));
			iframe.remove();
		}
	};
}

function workflowIdFrom(session: EmbedSessionHandle): string {
	const match = /\/embed\/([^/?#]+)/.exec(session.embedUrl ?? '');
	return match?.[1] ? decodeURIComponent(match[1]) : '';
}

function isDatastoreSession(session: EmbedSessionHandle): boolean {
	return (
		(session.scopes ?? []).some((scope) => scope === 'datastore:read' || scope === 'datastore:write') ||
		(session.datastoreId ?? '') !== ''
	);
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
