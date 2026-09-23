import { EXECUTION_EVENT_NAMES, isTerminal, type ExecutionEvent } from './event-stream.svelte';

export { EXECUTION_EVENT_NAMES };

export type EventSourceLike = {
	addEventListener(name: string, listener: (event: MessageEvent<string>) => void): void;
	close(): void;
};

export type ExecutionWatch = {
	/** Settles with the terminal event's type; never settles without a stream. */
	terminal: Promise<string>;
	close(): void;
};

function browserSource(url: string): EventSourceLike | null {
	return typeof EventSource === 'undefined' ? null : new EventSource(url);
}

/**
 * Follows one execution's live events for as long as it runs.
 *
 * A caller that polls the durable trace uses `terminal` to stop waiting the
 * moment the run ends, instead of up to a poll interval later, and `onEvent`
 * to show progress in between. The trace stays authoritative: this only makes
 * the wait shorter and the in-between visible.
 */
export function watchExecutionEvents(
	executionID: string,
	onEvent: (event: ExecutionEvent) => void,
	open: (url: string) => EventSourceLike | null = browserSource
): ExecutionWatch {
	const source = open(`/api/v1/executions/${encodeURIComponent(executionID)}/events`);
	if (!source) {
		return { terminal: new Promise<string>(() => {}), close: () => {} };
	}

	let settle!: (type: string) => void;
	const terminal = new Promise<string>((resolve) => (settle = resolve));
	const onMessage = (message: MessageEvent<string>) => {
		let event: ExecutionEvent;
		try {
			event = JSON.parse(message.data) as ExecutionEvent;
		} catch {
			return;
		}
		onEvent(event);
		if (isTerminal(event.type)) {
			source.close();
			settle(event.type);
		}
	};
	for (const name of EXECUTION_EVENT_NAMES) source.addEventListener(name, onMessage);

	return { terminal, close: () => source.close() };
}
