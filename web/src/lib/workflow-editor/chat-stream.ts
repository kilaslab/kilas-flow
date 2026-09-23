import type { ExecutionEvent } from './event-stream.svelte';

/**
 * What the chat panel shows while an agent or chain is still working.
 *
 * The run's final answer comes from the finished execution; this is only the
 * live picture in between — the text the model is streaming, and which tools
 * it has called — folded out of the execution's `ai.*` events so the panel can
 * show progress instead of a static "Running…".
 */
export type ChatToolStep = {
	/** Stable per call: the same tool may be called more than once. */
	key: string;
	name: string;
	status: 'running' | 'done' | 'failed';
	input?: unknown;
	output?: unknown;
	error?: string;
};

export type ChatStreamState = {
	phase: 'idle' | 'thinking' | 'tool' | 'writing';
	/** Streamed text of the model turn in progress. */
	text: string;
	tools: ChatToolStep[];
};

type AIEventData = {
	kind?: string;
	iteration?: number;
	tool?: string;
	delta?: string;
	detail?: unknown;
	error?: string;
};

function aiData(event: ExecutionEvent): AIEventData | null {
	if (!event.type.startsWith('ai.')) return null;
	const data = event.data;
	if (data === null || typeof data !== 'object' || Array.isArray(data)) return {};
	return data as AIEventData;
}

export function reduceChatStream(events: ExecutionEvent[]): ChatStreamState {
	const state: ChatStreamState = { phase: 'idle', text: '', tools: [] };
	let iteration = -1;
	const calls = new Map<string, number>();

	for (const event of events) {
		const data = aiData(event);
		if (!data) continue;
		switch (event.type) {
			case 'ai.model.started': {
				// A new turn of the tool loop replaces whatever the previous turn
				// streamed: that text was the model thinking aloud before a tool
				// call, not the answer.
				const turn = data.iteration ?? iteration + 1;
				if (turn !== iteration) state.text = '';
				iteration = turn;
				state.phase = 'thinking';
				break;
			}
			case 'ai.model.delta': {
				const turn = data.iteration ?? iteration;
				if (turn !== iteration) {
					state.text = '';
					iteration = turn;
				}
				state.text += data.delta ?? '';
				if (state.text !== '') state.phase = 'writing';
				break;
			}
			case 'ai.tool.started': {
				const name = data.tool ?? 'tool';
				const count = (calls.get(name) ?? 0) + 1;
				calls.set(name, count);
				const step: ChatToolStep = { key: `${name}#${count}`, name, status: 'running' };
				if (data.detail !== undefined) step.input = data.detail;
				state.tools = [...state.tools, step];
				state.phase = 'tool';
				break;
			}
			case 'ai.tool.completed':
			case 'ai.tool.failed': {
				const name = data.tool ?? 'tool';
				const index = findLastRunning(state.tools, name);
				if (index < 0) break;
				const step = { ...state.tools[index] };
				if (event.type === 'ai.tool.completed') {
					step.status = 'done';
					if (data.detail !== undefined) step.output = data.detail;
				} else {
					step.status = 'failed';
					step.error = data.error ?? '';
				}
				state.tools = state.tools.map((existing, position) => (position === index ? step : existing));
				state.phase = state.tools.some((tool) => tool.status === 'running') ? 'tool' : 'thinking';
				break;
			}
			default:
				break;
		}
	}
	return state;
}

function findLastRunning(tools: ChatToolStep[], name: string): number {
	for (let index = tools.length - 1; index >= 0; index -= 1) {
		if (tools[index].name === name && tools[index].status === 'running') return index;
	}
	return -1;
}
