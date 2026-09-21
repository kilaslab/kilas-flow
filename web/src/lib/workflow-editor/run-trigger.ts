import type { Definition, Node as WorkflowNode } from '$lib/api/generated/models';

import { CHAT_TRIGGER_TYPE } from './chat';
import { resolveDefinition } from './document';
import { isAnnotation } from './node-visual';

/**
 * Which trigger a manual run starts from.
 *
 * A workflow that declares several triggers — a webhook beside a nightly
 * schedule is the standard shape — used to fire all of them on Execute, which
 * is a run nobody asked for and, for a webhook trigger, one with an empty item.
 * The run API takes the node to start from instead, so the editor's job is only
 * to say which trigger the user meant.
 *
 * Deliberately conservative: the answer is a trigger the user has actually
 * selected, and nothing else. With one trigger there is nothing to choose, and
 * with none selected the server keeps its existing "run every trigger"
 * behaviour rather than the editor guessing at one.
 *
 * Chat is the exception. A chat trigger seeded with `{}` makes the Agent read
 * an empty `$json.chatInput`. Execute therefore never names a chat trigger,
 * never omits triggerNodeId when a chat trigger is also on the canvas (omitting
 * would run every trigger including chat), and on a chat-only graph opens the
 * Chat panel instead of queueing a run.
 */
export function runTriggerNodeID(nodes: WorkflowNode[], definitions: Definition[], selected: Iterable<string>): string | undefined {
	const triggers = nodes.filter((node) => isTriggerNode(node, definitions));
	if (triggers.length < 2) return undefined;
	const picked = new Set(selected);
	return triggers.find((trigger) => picked.has(trigger.id))?.id;
}

export type ExecuteIntent =
	| { action: 'open-chat' }
	| { action: 'run'; triggerNodeId?: string };

export function executableTriggerNodes(nodes: WorkflowNode[], definitions: Definition[]): WorkflowNode[] {
	return nodes.filter((node) => isTriggerNode(node, definitions) && node.type !== CHAT_TRIGGER_TYPE);
}

export function executeIntent(nodes: WorkflowNode[], definitions: Definition[], selected: Iterable<string>): ExecuteIntent {
	const chat = nodes.some((node) => node.type === CHAT_TRIGGER_TYPE);
	const executable = executableTriggerNodes(nodes, definitions);
	if (chat && executable.length === 0) {
		return { action: 'open-chat' };
	}
	if (chat) {
		const picked = new Set(selected);
		const chosen = executable.find((trigger) => picked.has(trigger.id)) ?? executable[0];
		return { action: 'run', triggerNodeId: chosen.id };
	}
	return { action: 'run', triggerNodeId: runTriggerNodeID(nodes, definitions, selected) };
}

/**
 * Whether a node starts a workflow.
 *
 * Read from the behavioural group the registry declares, not from a type name,
 * and an annotation is never a trigger however it is filed: a sticky note has
 * no ports at all and would otherwise be the one node in the document that
 * cannot run.
 */
export function isTriggerNode(node: WorkflowNode, definitions: Definition[]): boolean {
	const definition = resolveDefinition(node.type, node.typeVersion, definitions);
	if (!definition || isAnnotation(definition)) return false;
	return (definition.group ?? []).includes('trigger');
}
