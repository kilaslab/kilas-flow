import { describe, expect, it } from 'vitest';

import type { Definition, Node, Port } from '$lib/api/generated/models';

import { FALLBACK_GLYPH, TILE, attachmentPorts, mainPorts, nodeShape, nodeSubtitle, nodeVisual, portOffset } from './node-visual';

const MAIN: Port = { name: 'main', kind: 'main' };

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
		source: 'builtin',
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
		expect(nodeShape(definition(null, [{ name: 'tool', kind: 'ai_tool' }], 'AI'))).toBe('attachment');
		// kilasflow.chatModel and kilasflow.memoryBuffer, which use [] not null.
		expect(nodeShape(definition([], [{ name: 'model', kind: 'ai_languageModel' }], 'AI'))).toBe('attachment');
		expect(nodeShape(definition([], [{ name: 'memory', kind: 'ai_memory' }], 'AI'))).toBe('attachment');
	});

	it('reads a node that consumes attachments as a hub', () => {
		// kilasflow.agent: a main input plus three attachment inputs.
		const agent = definition(
			[MAIN, { name: 'model', kind: 'ai_languageModel' }, { name: 'memory', kind: 'ai_memory' }, { name: 'tools', kind: 'ai_tool' }],
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
		const branch = definition([MAIN], [{ name: 'true', kind: 'main' }, { name: 'false', kind: 'main' }]);
		expect(nodeShape(branch)).toBe('step');
		// kilasflow.merge: two main inputs, likewise.
		expect(nodeShape(definition([{ name: 'input1', kind: 'main' }, { name: 'input2', kind: 'main' }], [MAIN]))).toBe('step');
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
		const ports = [MAIN, { name: 'model', kind: 'ai_languageModel' }, { name: 'tools', kind: 'ai_tool' }];
		expect(mainPorts(ports)).toEqual([MAIN]);
		expect(attachmentPorts(ports).map((port) => port.name)).toEqual(['model', 'tools']);
		expect(mainPorts(null)).toEqual([]);
		expect(attachmentPorts(undefined)).toEqual([]);
	});
});

describe('nodeSubtitle', () => {
	/**
	 * The subtitle used to be a switch over eleven known node types with a null
	 * default, so a node the editor had never seen had no subtitle at all.
	 * These cases now feed a server-shaped definition in, which is what proves
	 * a node added on the server gets one with no frontend change.
	 */
	function withSubtitle(subtitle: string, parameters: Record<string, unknown> = {}): [Node, Definition] {
		const [n, d] = typed('pack.neverSeenBefore', parameters);
		return [n, { ...d, subtitle }];
	}

	it('renders a template over the node’s own parameters', () => {
		const [n, d] = withSubtitle('{{ $parameter.method }} {{ $parameter.url }}', {
			method: 'POST',
			url: 'https://api.shop.test/v1/orders'
		});
		expect(nodeSubtitle(n, d)).toBe('POST https://api.shop.test/v1/orders');
	});

	it('gives a node type the editor has never seen a subtitle', () => {
		const [n, d] = withSubtitle('{{ $parameter.session }}', { session: 'default' });
		expect(nodeSubtitle(n, d)).toBe('default');
	});

	it('omits a missing parameter rather than printing undefined', () => {
		const [n, d] = withSubtitle('{{ $parameter.method }} {{ $parameter.url }}', { method: 'GET' });
		expect(nodeSubtitle(n, d)).toBe('GET');
	});

	it('yields null when nothing resolves, so an unconfigured node shows no line', () => {
		const [n, d] = withSubtitle('{{ $parameter.method }} {{ $parameter.url }}');
		expect(nodeSubtitle(n, d)).toBeNull();
	});

	it('yields null for a definition that declares no subtitle', () => {
		const [n, d] = typed('pack.plain', { anything: 'here' });
		expect(nodeSubtitle(n, { ...d, subtitle: undefined })).toBeNull();
	});

	it('marks an expression rather than printing its template', () => {
		// Printing the raw {{ … }} on the canvas reads as a rendering bug, and
		// the canvas cannot resolve it — there is no item to resolve against.
		const [n, d] = withSubtitle('{{ $parameter.url }}', {
			url: { mode: 'expression', value: '{{ $json.endpoint }}' }
		});
		expect(nodeSubtitle(n, d)).toBe('ƒx');
	});

	it('survives a node with no parameters at all', () => {
		const [n, d] = withSubtitle('{{ $parameter.method }}');
		delete n.parameters;
		expect(nodeSubtitle(n, d)).toBeNull();
	});
});

describe('nodeVisual', () => {
	it('takes the accent from the definition rather than from a category map', () => {
		const [, d] = typed('pack.neverSeenBefore');
		expect(nodeVisual({ ...d, iconColor: '#ff0000' }).accent).toBe('#ff0000');
	});

	it('resolves a builtin glyph by the name the server chose', () => {
		const [, d] = typed('pack.neverSeenBefore');
		const visual = nodeVisual({ ...d, icon: { light: 'builtin:globe' } });
		expect(visual.iconURL).toBeNull();
		expect(visual.icon).not.toBe(FALLBACK_GLYPH);
	});

	it('falls back visibly when it does not ship the named glyph', () => {
		// Means "this editor is older than this node", which is a different
		// thing from "this node looks like a box".
		const [, d] = typed('pack.neverSeenBefore');
		expect(nodeVisual({ ...d, icon: { light: 'builtin:nothing-like-this' } }).icon).toBe(FALLBACK_GLYPH);
	});

	it('serves a node’s own artwork from the icon route', () => {
		const [, d] = typed('pack.wahaAction');
		const visual = nodeVisual({ ...d, icon: { light: 'waha.svg' }, version: 202502 });
		expect(visual.iconURL).toContain('/api/v1/node-types/pack.wahaAction/icon');
		expect(visual.iconURL).toContain('version=202502');
	});
});
