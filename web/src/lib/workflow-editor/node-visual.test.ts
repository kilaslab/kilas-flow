import { describe, expect, it } from 'vitest';

import type { Definition, Node, Port } from '$lib/api/generated/models';

import { TILE, attachmentPorts, mainPorts, nodeShape, nodeSubtitle, nodeVisual, portOffset } from './node-visual';

const MAIN: Port = { Name: 'main', Kind: 'main' };

/**
 * Definitions are described by their port shape rather than copied from the
 * registry. What is under test is the derivation, not the catalogue: a test
 * that hardcodes today's node list fails when a node is added, which teaches
 * nobody anything. Each case is named for the real node whose shape it is.
 */
function definition(inputs: Port[] | null, outputs: Port[] | null, category = 'Core'): Definition {
	return {
		type: 'kilasflow.example',
		version: 1,
		displayName: 'Example',
		category,
		group: ['transform'],
		inputs,
		outputs,
		parameters: [],
		sharedSettings: []
	};
}

function node(parameters: Record<string, unknown>): Node {
	return { id: 'n1', name: 'Example', type: 'kilasflow.example', typeVersion: 1, position: { x: 0, y: 0 }, parameters };
}

function typed(type: string, parameters: Record<string, unknown> = {}): [Node, Definition] {
	return [{ ...node(parameters), type }, { ...definition([MAIN], [MAIN]), type }];
}

describe('nodeShape', () => {
	it('reads a node that only provides an attachment as an attachment, never as a trigger', () => {
		// kilasflow.httpTool: no inputs at all, one ai_tool output. This is the
		// case that forces outputs to be checked before "has no inputs" — if the
		// order is ever swapped, a tool silently renders as a trigger.
		expect(nodeShape(definition(null, [{ Name: 'tool', Kind: 'ai_tool' }], 'AI'))).toBe('attachment');
		// kilasflow.chatModel and kilasflow.memoryBuffer, which use [] not null.
		expect(nodeShape(definition([], [{ Name: 'model', Kind: 'ai_languageModel' }], 'AI'))).toBe('attachment');
		expect(nodeShape(definition([], [{ Name: 'memory', Kind: 'ai_memory' }], 'AI'))).toBe('attachment');
	});

	it('reads a node that consumes attachments as a hub', () => {
		// kilasflow.agent: a main input plus three attachment inputs.
		const agent = definition(
			[MAIN, { Name: 'model', Kind: 'ai_languageModel' }, { Name: 'memory', Kind: 'ai_memory' }, { Name: 'tools', Kind: 'ai_tool' }],
			[MAIN],
			'AI'
		);
		expect(nodeShape(agent)).toBe('hub');
	});

	it('reads a node with no inputs as a trigger', () => {
		expect(nodeShape(definition([], [MAIN], 'Triggers'))).toBe('trigger');
		expect(nodeShape(definition(null, [MAIN], 'Triggers'))).toBe('trigger');
	});

	it('does not promote a branching node to a hub just because it has several outputs', () => {
		// kilasflow.if: two main outputs is still an ordinary step.
		const branch = definition([MAIN], [{ Name: 'true', Kind: 'main' }, { Name: 'false', Kind: 'main' }]);
		expect(nodeShape(branch)).toBe('step');
		// kilasflow.merge: two main inputs, likewise.
		expect(nodeShape(definition([{ Name: 'input1', Kind: 'main' }, { Name: 'input2', Kind: 'main' }], [MAIN]))).toBe('step');
	});

	it('gives a node type the frontend has never heard of the right silhouette anyway', () => {
		// The claim the whole design rests on: a node added on the server arrives
		// drawn correctly with no frontend change.
		const unknown = { ...definition([MAIN], [MAIN], 'Something New'), type: 'kilasflow.inventedLastWeek' };
		expect(nodeShape(unknown)).toBe('step');
		const visual = nodeVisual(unknown);
		expect(visual.icon).toBeTruthy();
		expect(visual.accent).toBe('var(--muted-foreground)');
	});
});

describe('port partitioning', () => {
	it('splits main ports from attachment ports and tolerates a null list', () => {
		const ports = [MAIN, { Name: 'model', Kind: 'ai_languageModel' }, { Name: 'tools', Kind: 'ai_tool' }];
		expect(mainPorts(ports)).toEqual([MAIN]);
		expect(attachmentPorts(ports).map((port) => port.Name)).toEqual(['model', 'tools']);
		expect(mainPorts(null)).toEqual([]);
		expect(attachmentPorts(undefined)).toEqual([]);
	});
});

describe('nodeSubtitle', () => {
	it('shows the host rather than the whole URL for a request', () => {
		const [n, d] = typed('kilasflow.httpRequest', { method: 'POST', url: 'https://api.shop.test/v1/orders?page=2' });
		expect(nodeSubtitle(n, d)).toBe('POST api.shop.test');
	});

	it('defaults the method when only a URL is set', () => {
		const [n, d] = typed('kilasflow.httpRequest', { url: 'https://api.shop.test/orders' });
		expect(nodeSubtitle(n, d)).toBe('GET api.shop.test');
	});

	it('shows an expression verbatim instead of inventing a resolved value', () => {
		// The server owns evaluation. Showing a stale or guessed host would be
		// worse than showing the template the user wrote.
		const [n, d] = typed('kilasflow.httpRequest', { url: { mode: 'expression', value: '{{ $json.endpoint }}' } });
		expect(nodeSubtitle(n, d)).toBe('GET {{ $json.endpoint }}');
	});

	it('does not throw on a URL that is not a URL', () => {
		const [n, d] = typed('kilasflow.httpRequest', { url: 'not a url at all' });
		expect(nodeSubtitle(n, d)).toBe('GET not a url at all');
	});

	it('normalises a webhook path however many slashes it was given', () => {
		const [n, d] = typed('kilasflow.webhook', { httpMethod: 'POST', path: '///tickets' });
		expect(nodeSubtitle(n, d)).toBe('POST /tickets');
	});

	it('counts assignments, singular and plural, and omits the line when there are none', () => {
		expect(nodeSubtitle(...typed('kilasflow.set', { assignments: { a: 1 } }))).toBe('1 field');
		expect(nodeSubtitle(...typed('kilasflow.set', { assignments: { a: 1, b: 2 } }))).toBe('2 fields');
		expect(nodeSubtitle(...typed('kilasflow.set', { assignments: {} }))).toBeNull();
		// An array is not an assignment map, and must not be counted as one.
		expect(nodeSubtitle(...typed('kilasflow.set', { assignments: ['a', 'b'] }))).toBeNull();
	});

	it('coerces a numeric response code and falls back to 200', () => {
		expect(nodeSubtitle(...typed('kilasflow.respondToWebhook', { responseCode: 404 }))).toBe('404');
		expect(nodeSubtitle(...typed('kilasflow.respondToWebhook', {}))).toBe('200');
	});

	it('survives a condition list that is empty, missing, or the wrong type', () => {
		expect(nodeSubtitle(...typed('kilasflow.if', { conditions: [{ field: 'tier' }] }))).toBe('tier');
		expect(nodeSubtitle(...typed('kilasflow.if', { conditions: [] }))).toBeNull();
		expect(nodeSubtitle(...typed('kilasflow.if', { conditions: 'nonsense' }))).toBeNull();
		expect(nodeSubtitle(...typed('kilasflow.if', {}))).toBeNull();
	});

	it('names the node an import could not map', () => {
		expect(nodeSubtitle(...typed('kilasflow.unsupported', { originalType: 'n8n-nodes-base.slack' }))).toBe('n8n-nodes-base.slack');
	});

	it('has no opinion about a node it has no branch for, and no opinion about missing parameters', () => {
		expect(nodeSubtitle(...typed('kilasflow.merge', {}))).toBeNull();
		const [n, d] = typed('kilasflow.httpRequest');
		delete n.parameters;
		expect(nodeSubtitle(n, d)).toBe('GET');
	});
});

describe('shared geometry', () => {
	it('gives the editor and the replay canvas the same tile for a shape', () => {
		// The acceptance criterion is that a graph reads identically in both
		// views. Both components import this table, so the criterion is enforced
		// by construction rather than by two copies staying in sync.
		expect(TILE.trigger).toContain('rounded-l-');
		expect(TILE.step).toBe('h-22 w-22 rounded-xl');
		expect(TILE.attachment).toContain('rounded-full');
		expect(TILE.hub).toContain('max-w-');
	});

	it('spaces ports evenly along an edge', () => {
		expect(portOffset(0, 1)).toBe('50%');
		expect(portOffset(0, 2)).toBe('33.33333333333333%');
		expect(portOffset(1, 2)).toBe('66.66666666666666%');
	});

	it('reads a node that both takes and provides attachments as a hub', () => {
		// An agent exposed as a tool to another agent. Nothing in the registry
		// does this yet; the promise is that it would arrive drawn correctly.
		const both = definition(
			[MAIN, { Name: 'model', Kind: 'ai_languageModel' }],
			[{ Name: 'tool', Kind: 'ai_tool' }],
			'AI'
		);
		// A circle has no edge to hang attachment ports from; a hub does.
		expect(nodeShape(both)).toBe('hub');
	});
});
