import type { ExecutionNodeRunResource, ExecutionResource } from '$lib/api/generated/models';

/** The editor-only chat start. Execute must never fire this node with `{}`. */
export const CHAT_TRIGGER_TYPE = 'kilasflow.chatTrigger';

export type ChatSendPayload = {
	action: 'sendMessage';
	sessionId: string;
	chatInput: string;
};

export function newChatSessionId(): string {
	return crypto.randomUUID();
}

export function chatTriggerIn<T extends { type: string }>(nodes: T[] | undefined | null): T | undefined {
	return (nodes ?? []).find((node) => node.type === CHAT_TRIGGER_TYPE);
}

export type ChatReply =
	| { kind: 'reply'; text: string }
	| { kind: 'error'; text: string }
	| { kind: 'missing'; nodeId: string };

function asRecord(value: unknown): Record<string, unknown> | null {
	if (value === null || typeof value !== 'object' || Array.isArray(value)) return null;
	return value as Record<string, unknown>;
}

function fieldText(value: unknown): string | null {
	if (typeof value === 'string') return value === '' ? null : value;
	if (typeof value === 'number' || typeof value === 'boolean') return String(value);
	if (value !== null && typeof value === 'object') {
		try {
			return JSON.stringify(value);
		} catch {
			return null;
		}
	}
	return null;
}

function itemReply(json: Record<string, unknown> | null): string | null {
	if (!json) return null;
	const output = fieldText(json.output);
	if (output !== null) return output;
	return fieldText(json.text);
}

function firstItemJson(output: unknown): Record<string, unknown> | null {
	if (!Array.isArray(output)) return asRecord(output);
	for (const stream of output) {
		if (!Array.isArray(stream)) {
			const record = asRecord(stream);
			if (record) return asRecord(record.json) ?? record;
			continue;
		}
		for (const item of stream) {
			const record = asRecord(item);
			if (!record) continue;
			return asRecord(record.json) ?? record;
		}
	}
	return null;
}

function errorMessage(error: unknown): string | null {
	if (typeof error === 'string' && error.trim()) return error;
	const record = asRecord(error);
	if (record && typeof record.message === 'string' && record.message.trim()) return record.message;
	return null;
}

/**
 * The chat widget's reply: walk back succeeded node runs, skip the trigger and
 * skipped nodes, then read `output` and `text` from the item JSON. n8n's own
 * last-node rule fails when the chain ends on Set/HTTP/datastore; walking
 * back is what still finds the Agent or Chain that actually answered.
 */
export function chatReplyFromExecution(execution: ExecutionResource, chatTriggerNodeId: string): ChatReply {
	if (execution.status === 'failed' || execution.status === 'cancelled') {
		return {
			kind: 'error',
			text: errorMessage(execution.error) ?? `The run ${execution.status}.`
		};
	}

	const runs = [...(execution.nodeRuns ?? [])].sort((a, b) => (b.sequence ?? 0) - (a.sequence ?? 0));
	let lastSucceeded: ExecutionNodeRunResource | undefined;
	for (const run of runs) {
		if (run.nodeId === chatTriggerNodeId) continue;
		if (run.status === 'skipped') continue;
		if (run.status !== 'succeeded') continue;
		lastSucceeded ??= run;
		const text = itemReply(firstItemJson(run.output));
		if (text !== null) return { kind: 'reply', text };
	}

	return { kind: 'missing', nodeId: lastSucceeded?.nodeId ?? '' };
}
