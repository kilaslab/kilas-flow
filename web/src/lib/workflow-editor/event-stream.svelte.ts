import type { ExecutionNodeRunResource } from '$lib/api/generated/models';

/** One standardized execution event as it arrives on the live feed. */
export type ExecutionEvent = {
	id: number;
	type: string;
	executionId: string;
	workflowId?: string;
	nodeId?: string;
	status?: string;
	sequence?: number;
	at: string;
	data?: unknown;
};

const TERMINAL = new Set(['execution.completed', 'execution.failed', 'execution.cancelled']);

export function isTerminal(type: string): boolean {
	return TERMINAL.has(type);
}

/**
 * The statuses an execution never leaves.
 *
 * The distinction from `isTerminal` matters at the point of subscription: an
 * event *type* is terminal when it arrives, while a *status* read from the
 * durable trace is terminal before anything arrives. A finished run has
 * nothing left to stream, and opening a source for it costs a connection that
 * lives until the server hangs up.
 */
const TERMINAL_STATUSES: Record<string, true> = {
	succeeded: true,
	failed: true,
	cancelled: true
};

/** Whether a trace status means the run will never emit another event. */
export function isTerminalStatus(status: string | null | undefined): status is string {
	return typeof status === 'string' && TERMINAL_STATUSES[status] === true;
}

/**
 * Live execution feed.
 *
 * `EventSource` handles reconnection and resends Last-Event-ID on its own, so
 * a dropped connection resumes where it left off rather than replaying the run
 * from the start. The stream is closed explicitly on a terminal event: the
 * server closes too, and without this the browser would reconnect forever to a
 * run that already finished.
 *
 * `live` is what the durable trace already says about the run. A finished
 * execution is never streamed: the trace is complete, and a subscription for
 * it would hold an idle connection open for as long as the page is.
 */
export function executionEvents(executionID: () => string, live: () => boolean = () => true) {
	let events = $state<ExecutionEvent[]>([]);
	let connected = $state(false);
	let finished = $state(false);

	$effect(() => {
		const id = executionID();
		if (!id || !live()) return;

		events = [];
		connected = false;
		finished = false;

		const source = new EventSource(`/api/v1/executions/${encodeURIComponent(id)}/events`);
		const onOpen = () => (connected = true);
		const onMessage = (message: MessageEvent<string>) => {
			let event: ExecutionEvent;
			try {
				event = JSON.parse(message.data) as ExecutionEvent;
			} catch {
				return;
			}
			events = [...events, event];
			if (isTerminal(event.type)) {
				finished = true;
				source.close();
				connected = false;
			}
		};

		source.addEventListener('open', onOpen);
		// The server names each event, so there is no default `message` type to
		// listen on; every known name routes to the same handler.
		for (const name of [
			'execution.started',
			'execution.completed',
			'execution.failed',
			'execution.cancelled',
			'execution.waiting',
			'node.started',
			'node.output',
			'node.completed',
			'node.failed',
			'workflow.saved'
		]) {
			source.addEventListener(name, onMessage as EventListener);
		}
		source.addEventListener('error', () => (connected = false));

		return () => source.close();
	});

	return {
		get events() {
			return events;
		},
		get connected() {
			return connected;
		},
		get finished() {
			return finished;
		}
	};
}

/**
 * Folds live events into node statuses.
 *
 * The durable trace stays authoritative; this only fills in what has happened
 * since it was fetched, so a refresh and a live update converge on the same
 * picture.
 */
export function applyEvents(
	runs: Map<string, ExecutionNodeRunResource>,
	events: ExecutionEvent[]
): Map<string, string> {
	const statuses = new Map<string, string>();
	for (const [nodeID, run] of runs) statuses.set(nodeID, run.status);
	for (const event of events) {
		if (!event.nodeId || !event.status) continue;
		statuses.set(event.nodeId, event.status);
	}
	return statuses;
}

/** The execution-level status implied by the latest event, if any. */
export function latestExecutionStatus(events: ExecutionEvent[]): string | null {
	for (let index = events.length - 1; index >= 0; index -= 1) {
		const event = events[index];
		if (event.type.startsWith('execution.') && event.status) return event.status;
	}
	return null;
}
