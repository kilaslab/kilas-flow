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
	| { kind: 'error'; text: string; node?: string; detail?: string }
	| { kind: 'missing'; nodeId: string };

type NamedNode = { id?: string | null; name?: string | null };

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
 * A failed run, worded for the person chatting.
 *
 * The engine's message is built for logs: `execute node "agent": node "AI
 * Agent": model turn 1: … 404: {"error":{"message":"model 'x' not found"}}`.
 * The panel names the node once, puts the provider's own sentence first, and
 * keeps the step that failed as a quieter detail line.
 */
function chatFailure(execution: ExecutionResource, nodes: NamedNode[]): ChatReply {
	const failedRun = [...(execution.nodeRuns ?? [])]
		.sort((a, b) => (b.sequence ?? 0) - (a.sequence ?? 0))
		.find((run) => run.status === 'failed');
	const raw = errorMessage(execution.error) ?? errorMessage(failedRun?.error) ?? `The run ${execution.status}.`;

	let message = raw.replace(/^execute node "[^"]*":\s*/, '');
	let node: string | undefined;
	const named = /^node "([^"]+)":\s*/.exec(message);
	if (named) {
		node = named[1];
		message = message.slice(named[0].length);
	}
	if (!node && failedRun) {
		node = nodes.find((candidate) => candidate.id === failedRun.nodeId)?.name ?? undefined;
	}

	const reply: ChatReply = { kind: 'error', text: message };
	const embedded = /^(.*?):\s*(\{[\s\S]*\})\s*$/.exec(message);
	if (embedded) {
		const provider = providerMessage(embedded[2]);
		if (provider) {
			reply.text = provider;
			reply.detail = embedded[1];
		}
	}
	if (node) reply.node = node;
	return reply;
}

/** The human sentence inside a provider's JSON error body, if there is one. */
function providerMessage(body: string): string | null {
	try {
		const parsed = JSON.parse(body) as unknown;
		const record = asRecord(parsed);
		const nested = asRecord(record?.error);
		return errorMessage(nested) ?? errorMessage(record) ?? (typeof record?.error === 'string' ? record.error : null);
	} catch {
		return null;
	}
}

/**
 * Whether the conversation is remembered at all: a memory sub-node wired into
 * the graph. Without one every message starts fresh, and saying otherwise —
 * or warning about an in-process memory that isn't there — misleads.
 */
export function chatHasMemory(
	nodes: { id?: string | null; type: string }[] | null | undefined,
	connections: { kind: string; source: { nodeId: string } }[] | null | undefined
): boolean {
	const memoryIDs = new Set((nodes ?? []).filter((node) => node.type === 'kilasflow.memoryBuffer' && node.id).map((node) => node.id));
	return (connections ?? []).some((connection) => connection.kind === 'ai_memory' && memoryIDs.has(connection.source.nodeId));
}

/**
 * The chat widget's reply: walk back succeeded node runs, skip the trigger and
 * skipped nodes, then read `output` and `text` from the item JSON. n8n's own
 * last-node rule fails when the chain ends on Set/HTTP/datastore; walking
 * back is what still finds the Agent or Chain that actually answered.
 */
export function chatReplyFromExecution(
	execution: ExecutionResource,
	chatTriggerNodeId: string,
	nodes: NamedNode[] = []
): ChatReply {
	if (execution.status === 'failed' || execution.status === 'cancelled') {
		return chatFailure(execution, nodes);
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
