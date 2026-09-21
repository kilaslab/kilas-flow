import { describe, expect, it } from 'vitest';

import type { Definition, Node as WorkflowNode } from '$lib/api/generated/models';

import { executeIntent, isTriggerNode, runTriggerNodeID } from './run-trigger';

const trigger: Definition = {
	type: 'pack.webhook',
	version: 1,
	displayName: 'Webhook',
	category: 'Triggers',
	group: ['trigger'],
	source: 'builtin',
	inputs: [],
	outputs: [{ name: 'main', kind: 'main' }],
	parameters: [],
	sharedSettings: []
};

const schedule: Definition = { ...trigger, type: 'kilasflow.schedule', displayName: 'Schedule' };
const chat: Definition = { ...trigger, type: 'kilasflow.chatTrigger', displayName: 'When chat message received' };
const set: Definition = { ...trigger, type: 'kilasflow.set', displayName: 'Set', group: ['transform'], inputs: [{ name: 'main', kind: 'main' }] };
const note: Definition = {
	...trigger,
	type: 'kilasflow.stickyNote',
	displayName: 'Sticky Note',
	category: 'Annotation',
	group: ['organization']
};

const definitions = [trigger, schedule, chat, set, note];

function node(id: string, type: string): WorkflowNode {
	return { id, name: id, type, typeVersion: 1, position: { x: 0, y: 0 } };
}

describe('isTriggerNode', () => {
	it('reads the behavioural group, and never an annotation', () => {
		expect(isTriggerNode(node('hook', 'pack.webhook'), definitions)).toBe(true);
		expect(isTriggerNode(node('step', 'kilasflow.set'), definitions)).toBe(false);
		expect(isTriggerNode(node('note', 'kilasflow.stickyNote'), definitions)).toBe(false);
		// A node whose definition is not in this catalogue cannot be claimed to
		// start anything.
		expect(isTriggerNode(node('unknown', 'kilasflow.retired'), definitions)).toBe(false);
	});
});

describe('runTriggerNodeID', () => {
	it('names the trigger the user selected once a workflow declares several', () => {
		const nodes = [node('hook', 'pack.webhook'), node('nightly', 'kilasflow.schedule'), node('work', 'kilasflow.set')];

		expect(runTriggerNodeID(nodes, definitions, ['nightly'])).toBe('nightly');
		expect(runTriggerNodeID(nodes, definitions, ['work', 'hook'])).toBe('hook');
	});

	it('says nothing when there is one trigger, or none selected', () => {
		// The regression this exists for: Execute fired every trigger of a
		// multi-trigger workflow, including the one with an empty item.
		const several = [node('hook', 'pack.webhook'), node('nightly', 'kilasflow.schedule')];
		expect(runTriggerNodeID(several, definitions, [])).toBeUndefined();
		expect(runTriggerNodeID(several, definitions, ['hook', 'nightly'])).toBe('hook');

		// One trigger: the server's default already is that trigger.
		const single = [node('hook', 'pack.webhook'), node('work', 'kilasflow.set')];
		expect(runTriggerNodeID(single, definitions, ['hook'])).toBeUndefined();

		// A step is not a trigger, however it is selected.
		expect(runTriggerNodeID(several, definitions, ['work'])).toBeUndefined();
	});

	it('ignores an annotation sitting in the selection', () => {
		const nodes = [node('hook', 'pack.webhook'), node('nightly', 'kilasflow.schedule'), node('note', 'kilasflow.stickyNote')];
		expect(runTriggerNodeID(nodes, definitions, ['note'])).toBeUndefined();
		expect(runTriggerNodeID(nodes, definitions, ['note', 'nightly'])).toBe('nightly');
	});
});

describe('executeIntent', () => {
	it('opens chat instead of running when the only trigger is chat', () => {
		const nodes = [node('chat', 'kilasflow.chatTrigger'), node('work', 'kilasflow.set')];
		expect(executeIntent(nodes, definitions, [])).toEqual({ action: 'open-chat' });
		expect(executeIntent(nodes, definitions, ['chat'])).toEqual({ action: 'open-chat' });
	});

	it('never omits triggerNodeId when a chat trigger shares the canvas', () => {
		const nodes = [node('chat', 'kilasflow.chatTrigger'), node('hook', 'pack.webhook'), node('work', 'kilasflow.set')];
		expect(executeIntent(nodes, definitions, [])).toEqual({ action: 'run', triggerNodeId: 'hook' });
		expect(executeIntent(nodes, definitions, ['chat'])).toEqual({ action: 'run', triggerNodeId: 'hook' });
		expect(executeIntent(nodes, definitions, ['hook'])).toEqual({ action: 'run', triggerNodeId: 'hook' });
	});

	it('picks the selected non-chat trigger among several', () => {
		const nodes = [
			node('chat', 'kilasflow.chatTrigger'),
			node('hook', 'pack.webhook'),
			node('nightly', 'kilasflow.schedule')
		];
		expect(executeIntent(nodes, definitions, ['nightly'])).toEqual({ action: 'run', triggerNodeId: 'nightly' });
	});

	it('leaves a workflow with no chat trigger on the existing Execute path', () => {
		const several = [node('hook', 'pack.webhook'), node('nightly', 'kilasflow.schedule')];
		expect(executeIntent(several, definitions, [])).toEqual({ action: 'run', triggerNodeId: undefined });
		expect(executeIntent(several, definitions, ['hook'])).toEqual({ action: 'run', triggerNodeId: 'hook' });
		const single = [node('hook', 'pack.webhook'), node('work', 'kilasflow.set')];
		expect(executeIntent(single, definitions, ['hook'])).toEqual({ action: 'run', triggerNodeId: undefined });
	});
});
