import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import type { Definition, Node, Port } from '$lib/api/generated/models';
import { hueDistance } from '$lib/design-system/color';

import { BUILTIN_GLYPHS, FALLBACK_GLYPH, NODE_CATEGORIES, TILE, attachmentLabelRows, attachmentPorts, friendlyOriginalType, harmonisedGlyphColour, mainPorts, nodeCategory, nodeIconBadgeClass, nodeShape, nodeSubtitle, nodeVisual, placeholderDefinition, portOffset } from './node-visual';

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
		// An unknown node is a utility: the neutral category token, not a colour
		// picked out of the air.
		expect(visual.category).toBe('utility');
		expect(visual.accent).toBe('var(--kf-cat-utility)');
	});
});

describe('nodeShape for a node whose ports follow its configuration', () => {
	// BUG-pc284d: a vector store is a plain main-to-main step as the picker shows
	// it, and a hub the moment its mode gives it attachment ports. The tile has
	// to be cut from the ports the node really has, or its attachment labels
	// print over the name a step draws beneath it.
	const declared = definition([MAIN], [MAIN], 'AI');
	const retrieve = { inputs: [{ name: 'embedding', kind: 'ai_embedding' }], outputs: [{ name: 'vectorStore', kind: 'ai_vectorStore' }] };
	const insert = { inputs: [{ name: 'embedding', kind: 'ai_embedding' }, { name: 'document', kind: 'ai_document' }], outputs: [MAIN] };
	const cluster = { inputs: [], outputs: [{ name: 'embedding', kind: 'ai_embedding' }] };

	it('reads the resolved ports, not the declared ones', () => {
		expect(nodeShape(declared)).toBe('step');
		expect(nodeShape(declared, retrieve)).toBe('hub');
		expect(nodeShape(declared, insert)).toBe('hub');
		expect(nodeVisual(declared, retrieve).shape).toBe('hub');
	});

	it('draws a cluster embeddings node as the sub-node circle it is', () => {
		const embeddings = definition([MAIN], [MAIN, { name: 'embedding', kind: 'ai_embedding' }], 'AI');
		expect(nodeShape(embeddings, cluster)).toBe('attachment');
		// The sequential shape is the declared one: it still takes items on main.
		expect(nodeShape(embeddings, { inputs: embeddings.inputs ?? [], outputs: embeddings.outputs ?? [] })).toBe('attachment');
	});

	it('keeps the declared silhouette when no resolved ports are offered', () => {
		expect(nodeShape(declared, undefined)).toBe('step');
		expect(nodeVisual(declared).shape).toBe('step');
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

	it('hides a line that renders only its separators', () => {
		// A Telegram tile with resource and operation unset used to print a
		// stray ":" under the node.
		const [n, d] = withSubtitle('{{ $parameter.resource }}: {{ $parameter.operation }}');
		expect(nodeSubtitle(n, d)).toBeNull();
		const [half, halfDefinition] = withSubtitle('{{ $parameter.resource }}: {{ $parameter.operation }}', { resource: 'message' });
		expect(nodeSubtitle(half, halfDefinition)).toBe('message');
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

describe('nodeCategory', () => {
	// FEAT-xj5tv6: six canvas categories, derived from the registry's
	// behavioural signals rather than from iconColor — a colour says nothing
	// about behaviour, and the 24 free hex values in nodes/*.go painted 25
	// border colours on a nine-node canvas.
	const shaped = (category: string, group: string[] = ['transform']): Definition => ({
		...definition([MAIN], [MAIN], category),
		group
	});

	it('reads the trigger from the group, before any category string', () => {
		expect(nodeCategory(shaped('Triggers', ['trigger']))).toBe('trigger');
		// A scheduled node fires the flow: it is a trigger even when the
		// category string has drifted.
		expect(nodeCategory(shaped('Core', ['schedule']))).toBe('trigger');
		expect(nodeCategory(shaped('Triggers'))).toBe('trigger');
	});

	it('keeps the annotation in the utility ground, where nothing executes', () => {
		expect(nodeCategory(shaped('Annotation', ['organization']))).toBe('utility');
	});

	it('folds the registry category names onto the six canvas categories', () => {
		expect(nodeCategory(shaped('AI'))).toBe('ai');
		expect(nodeCategory(shaped('Flow'))).toBe('flow');
		expect(nodeCategory(shaped('Database'))).toBe('data');
		expect(nodeCategory(shaped('Datastore'))).toBe('data');
		expect(nodeCategory(shaped('Transform'))).toBe('data');
		expect(nodeCategory(shaped('Communication'))).toBe('app');
		expect(nodeCategory(shaped('Files'))).toBe('app');
	});

	it('treats Core, Test, Imported and anything unannounced as utility', () => {
		for (const category of ['Core', 'Test', 'Imported', 'Whatever Comes Next']) {
			expect(nodeCategory(shaped(category)), category).toBe('utility');
		}
	});

	it('reads a codex category the registry did not repeat on the definition', () => {
		expect(nodeCategory({ ...shaped('Core'), codex: { categories: ['AI'] } })).toBe('ai');
	});

	it('offers exactly the six categories the canvas can paint', () => {
		expect(NODE_CATEGORIES).toEqual(['trigger', 'ai', 'flow', 'data', 'app', 'utility']);
	});
});

describe('harmonisedGlyphColour', () => {
	it('clamps a brand colour into the palette band as relative colour syntax', () => {
		// Lightness comes from the theme token, chroma is capped, the hue is the
		// brand's own: Gmail stays blue, whatever the canvas is wearing.
		expect(harmonisedGlyphColour('#4285f4', 'app')).toBe('oklch(from #4285f4 var(--kf-glyph-l) min(c, 0.12) 260)');
		expect(harmonisedGlyphColour('#25D366', 'app')).toBe('oklch(from #25D366 var(--kf-glyph-l) min(c, 0.12) 149.7)');
	});

	it('steers a red within 20° of the danger hue off the error band', () => {
		// "Gmail looks like an error" was the finding: a brand glyph may not
		// borrow the colour a fault owns.
		for (const red of ['#ef4444', '#ea4335', '#ea580c']) {
			const out = harmonisedGlyphColour(red, 'app');
			expect(out, red).toBe(`oklch(from ${red} var(--kf-glyph-l) min(c, 0.12) 48)`);
		}
		// Just outside the band, the brand hue survives.
		expect(harmonisedGlyphColour('#f97316', 'app')).toBe('oklch(from #f97316 var(--kf-glyph-l) min(c, 0.12) 47.6)');
	});

	it('renders a black or grey brand as a neutral, not as a red', () => {
		// #000000's hue is powerless; the lightness clamp alone makes it a
		// readable neutral, so the hue keyword passes through.
		expect(harmonisedGlyphColour('#000000', 'utility')).toBe('oklch(from #000000 var(--kf-glyph-l) min(c, 0.12) h)');
		expect(harmonisedGlyphColour('#64748b', 'app')).toBe('oklch(from #64748b var(--kf-glyph-l) min(c, 0.12) 257.4)');
	});

	it('falls back to the category token for anything that is not a hex colour', () => {
		expect(harmonisedGlyphColour(undefined, 'ai')).toBe('var(--kf-cat-ai)');
		expect(harmonisedGlyphColour('', 'trigger')).toBe('var(--kf-cat-trigger)');
		expect(harmonisedGlyphColour('var(--primary)', 'data')).toBe('var(--kf-cat-data)');
	});

	it('keeps every brand hue it will emit out of the danger band', () => {
		// The 24 values nodes/*.go actually declare, plus the WAHA pack's.
		const declared = ['#000000','#0284c7','#0e7490','#0ea5e9','#0f766e','#0f9d58','#10b981','#14b8a6','#16a34a','#2aabee','#336791','#4285f4','#6366f1','#64748b','#8b5cf6','#94a3b8','#a855f7','#ea4335','#ea580c','#eab308','#ef4444','#f59e0b','#f97316','#25d366'];
		for (const hex of declared) {
			const out = harmonisedGlyphColour(hex, 'app');
			const hue = /min\(c, 0\.12\) ([\d.]+|h)\)$/.exec(out)?.[1];
			// A hue keyword (powerless origin) paints a neutral; any literal hue
			// must sit more than 20° from --kf-danger's 25.
			if (hue === 'h') continue;
			expect(hueDistance(Number(hue), 25), hex).toBeGreaterThan(20);
		}
	});
});

describe('nodeVisual', () => {
	it('harmonises the accent from the definition and the category it derived', () => {
		const [, d] = typed('pack.neverSeenBefore');
		const visual = nodeVisual({ ...d, iconColor: '#ff0000' });
		expect(visual.category).toBe('utility');
		// Pure red sits inside the danger band, so the harmoniser moves it off.
		expect(visual.accent).toBe('oklch(from #ff0000 var(--kf-glyph-l) min(c, 0.12) 48)');
	});

	it('resolves a builtin glyph by the name the server chose', () => {
		const [, d] = typed('pack.neverSeenBefore');
		const visual = nodeVisual({ ...d, icon: { light: 'builtin:globe' } });
		expect(visual.iconURL).toBeNull();
		expect(visual.icon).not.toBe(FALLBACK_GLYPH);
	});

	it('ships the glyph the WAHA pack names, so it is not a grey box', () => {
		// The pack asks for `builtin:message-circle`. A generated pack has no
		// frontend entry of its own, so the only thing standing between it and
		// the fallback glyph is that this build imports the one it names.
		const [, d] = typed('pack.waha');
		const visual = nodeVisual({ ...d, icon: { light: 'builtin:message-circle' }, iconColor: '#25d366' });
		expect(visual.icon).not.toBe(FALLBACK_GLYPH);
		expect(visual.iconURL).toBeNull();
		// A brand colour arrives through the harmoniser, keeping its own hue.
		expect(visual.accent).toBe('oklch(from #25d366 var(--kf-glyph-l) min(c, 0.12) 149.7)');
	});
	it('ships the glyph the data-table node names, with its own accent', () => {
		// kilasflow.datastore asks for `builtin:table`. The canvas must draw
		// it with that glyph and the node's own accent rather than the Box
		// and muted-foreground fallbacks.
		const [n, d] = typed('kilasflow.datastore', { resource: 'row', operation: 'insert' });
		const visual = nodeVisual({ ...d, icon: { light: 'builtin:table' }, iconColor: '#0e7490', subtitle: '{{ $parameter.resource }}/{{ $parameter.operation }}' });
		expect(visual.icon).not.toBe(FALLBACK_GLYPH);
		expect(visual.iconURL).toBeNull();
		expect(visual.accent).toBe('oklch(from #0e7490 var(--kf-glyph-l) min(c, 0.12) 223.1)');
		expect(nodeSubtitle(n, { ...d, subtitle: '{{ $parameter.resource }}/{{ $parameter.operation }}' })).toBe('row/insert');
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


<<<<<<< Updated upstream
describe('nodeChrome', () => {
	it('uses the error run token for invalid or failed tiles', () => {
		expect(nodeChromeBorder({ invalid: true })).toBe('var(--kf-run-error)');
		expect(nodeChromeBorder({ runStatus: 'failed' })).toBe('var(--kf-run-error)');
	});

	it('speaks run state in state hues, never the accent (FEAT-r8ph93)', () => {
		expect(nodeChromeBorder({ runStatus: 'running' })).toBe('var(--kf-run-running)');
		expect(nodeChromeBorder({ runStatus: 'cancelling' })).toBe('var(--kf-run-warning)');
		expect(nodeChromeBorder({ runStatus: 'cancelled' })).toBe('var(--kf-run-warning)');
		expect(nodeChromeBorder({ runStatus: 'succeeded' })).toBe('var(--kf-run-success)');
=======
describe('tile chrome (the shared decorator stylesheet)', () => {
	// The border/shadow computation moved out of per-node inline styles into
	// CSS on data-attributes (FEAT-xj5tv6), shared by canvas-node and
	// execution-canvas-node. What the acceptance criteria pin is negative:
	// no tile chrome rule may reference a per-node colour, so a selected node
	// can never borrow its category's hue for the ring.
	const css = readFileSync(fileURLToPath(new URL('../../app.css', import.meta.url)), 'utf8');
	// The tile-chrome rules live between their banner and the glyph chip
	// (which is allowed --node-accent: a category colour meets the surface
	// there, and nowhere else).
	const chromeBlock = css.slice(css.indexOf('/* Canvas node chrome'), css.indexOf('/* The glyph chip'));

	it('paints the idle tile and every state from tokens, never from --node-accent', () => {
		expect(chromeBlock).toContain('.kf-node-tile {\n\tborder-color: var(--kf-border-strong);\n\tbox-shadow: var(--kf-node-shadow);');
		expect(chromeBlock).not.toContain('--node-accent');
	});

	it('draws the one selection ring and halo for every node', () => {
		const selected = "[data-selected='true'] .kf-node-tile";
		expect(chromeBlock).toContain(`${selected} {\n\tborder-color: var(--kf-selection);`);
		expect(chromeBlock).toContain('var(--kf-selection-halo)');
>>>>>>> Stashed changes
	});

	it('speaks run state in the state hues, not the brand accent', () => {
		for (const [status, token] of [
			['succeeded', '--kf-run-success'],
			['running', '--kf-run-running'],
			['failed', '--kf-run-error']
		] as const) {
			expect(chromeBlock, status).toMatch(new RegExp(`\\[data-run-status='${status}'\\] \\.kf-node-tile \\{\\n\\tborder-color: var\\(${token}\\)`));
		}
		// Cancelling and cancelled share one rule, and one warning hue.
		expect(chromeBlock).toMatch(/\[data-run-status='cancelling'\] \.kf-node-tile,\n\[data-run-status='cancelled'\] \.kf-node-tile \{\n\tborder-color: var\(--kf-run-warning\)/);
		expect(chromeBlock).not.toContain('--primary');
	});

<<<<<<< Updated upstream
	it('elevates selected and running tiles more than the idle card shadow', () => {
		const idle = nodeChromeShadow({});
		const selected = nodeChromeShadow({ selected: true });
		const running = nodeChromeShadow({ runStatus: 'running' });
		expect(selected).not.toBe(idle);
		expect(running.length).toBeGreaterThan(idle.length);
		expect(running).toContain('var(--kf-run-running)');
		expect(nodeChromeShadow({ runStatus: 'failed' })).toContain('var(--kf-run-error)');
		expect(selected).toBe('0 0 0 2px var(--kf-selection-halo), var(--kf-shadow-1)');
		expect(selected).not.toContain('--node-accent');
=======
	it('keeps the fault outranking the run states in the stylesheet order', () => {
		// Same specificity, so order is precedence: invalid/failed last.
		expect(chromeBlock.indexOf("[data-run-status='failed']")).toBeGreaterThan(chromeBlock.indexOf("[data-run-status='running']"));
		expect(chromeBlock.indexOf("[data-run-status='failed']")).toBeGreaterThan(chromeBlock.indexOf("[data-selected='true']"));
>>>>>>> Stashed changes
	});

	it('sizes the icon badge per silhouette', () => {
		expect(nodeIconBadgeClass('step')).toContain('size-9');
		expect(nodeIconBadgeClass('trigger')).toContain('rounded-lg');
		expect(nodeIconBadgeClass('hub')).toContain('size-8');
		expect(nodeIconBadgeClass('attachment')).toContain('rounded-full');
	});

	it('keeps TILE silhouettes available for both editor and replay', () => {
		expect(TILE.step).toContain('rounded-lg');
		expect(TILE.step).toContain('h-16');
		expect(TILE.step).toContain('w-16');
		expect(TILE.trigger).toContain('rounded-l');
		expect(TILE.attachment).toContain('size-11');
	});
});

describe('tile geometry (the Kunyit density pitch)', () => {
	it('cuts a 64px step and a 44px sub-node, as the density tokens declare', () => {
		expect(TILE.step).toContain('h-16');
		expect(TILE.step).toContain('w-16');
		expect(TILE.hub).toContain('h-16');
		expect(TILE.attachment).toContain('size-11');
	});
});

describe('shipped glyphs', () => {
	it('implements every glyph name glyphs.json promises', async () => {
		// glyphs.json is the contract the server's own test holds definitions
		// to; a name listed there but missing from GLYPHS renders the fallback
		// box on every canvas for that node.
		const { default: shipped } = await import('./glyphs.json');
		const missing = Object.keys(shipped).filter((name) => !(name in BUILTIN_GLYPHS));

		expect(missing).toEqual([]);
	});

	it('draws two names alike only when glyphs.json says they are the same icon', async () => {
		// The server's distinct-glyph test (nodes/glyphs_test.go) compares the
		// icon each name resolves to in glyphs.json. That holds on the canvas
		// only if GLYPHS resolves the names the same way, so two names glyphs.json
		// keeps apart must not share a component here.
		const { default: shipped } = (await import('./glyphs.json')) as { default: Record<string, string> };
		const clashes: string[] = [];
		const names = Object.keys(shipped);
		for (const [index, a] of names.entries()) {
			for (const b of names.slice(index + 1)) {
				if (shipped[a] !== shipped[b] && BUILTIN_GLYPHS[a] === BUILTIN_GLYPHS[b]) clashes.push(`${a} = ${b}`);
			}
		}

		expect(clashes).toEqual([]);
	});
});

describe('attachmentLabelRows', () => {
	it('keeps the common one- and two-attachment hubs on a single label row', () => {
		expect(attachmentLabelRows(1)).toBe(1);
		expect(attachmentLabelRows(2)).toBe(1);
	});

	it('adds rows as attachments multiply, so same-row labels cannot collide', () => {
		// Four attachments — model, memory, two tools — used to print
		// "Chat Mode|Tools" with the pair overlapping on one row.
		expect(attachmentLabelRows(4)).toBeGreaterThanOrEqual(2);
		expect(attachmentLabelRows(6)).toBeGreaterThan(attachmentLabelRows(4) ?? 0);
		// Same-row spacing is rows * slot; every labelled case must fit a label.
		for (let count = 2; count <= 24; count++) {
			const rows = attachmentLabelRows(count);
			if (rows === null) continue;
			expect(rows * (144 / (count + 1)), `count ${count}`).toBeGreaterThanOrEqual(48);
		}
	});

	it('gives up on labelling rather than stack more rows than the tile is tall', () => {
		expect(attachmentLabelRows(20)).toBeNull();
	});
});

describe('friendlyOriginalType', () => {
	it('reduces a package-qualified type to readable words', () => {
		expect(friendlyOriginalType('@n8n/n8n-nodes-langchain.toolSerpApi')).toBe('Tool Serp Api');
		expect(friendlyOriginalType('n8n-nodes-base.httpRequest')).toBe('Http Request');
	});

	it('tolerates a missing capsule and a bare word', () => {
		expect(friendlyOriginalType(null)).toBeNull();
		expect(friendlyOriginalType(undefined)).toBeNull();
		expect(friendlyOriginalType('slack')).toBe('Slack');
	});
});

describe('placeholderDefinition', () => {
	// The placeholder family declares main in/out plus twelve AI ports each
	// way; these are the shapes the import actually produces.
	const AI_IN: Port[] = [
		{ name: 'inModel', kind: 'ai_languageModel' },
		{ name: 'inTool', kind: 'ai_tool' }
	];
	const AI_OUT: Port[] = [
		{ name: 'outModel', kind: 'ai_languageModel' },
		{ name: 'outTool', kind: 'ai_tool' }
	];
	const placeholder = definition([MAIN, ...AI_IN], [MAIN, { name: 'output1', kind: 'main' }, ...AI_OUT]);

	it('draws a tool wired only by its AI output as a sub-node with no main input', () => {
		// An imported SerpAPI tool: one edge, from its tool output to an agent.
		const drawn = placeholderDefinition(placeholder, new Set(), new Set(['outTool']));
		expect(drawn.inputs).toEqual([]);
		expect(drawn.outputs).toEqual([{ name: 'outTool', kind: 'ai_tool' }]);
		expect(nodeShape(drawn)).toBe('attachment');
	});

	it('keeps an input and an output apart when they share the name main', () => {
		// Wired from its main output only: the main input is not in use, even
		// though it is also called "main".
		const drawn = placeholderDefinition(placeholder, new Set(), new Set(['main']));
		expect(drawn.inputs).toEqual([]);
		expect(drawn.outputs).toEqual([MAIN]);
	});

	it('draws only the wired ports on both sides', () => {
		const drawn = placeholderDefinition(placeholder, new Set(['main', 'inModel']), new Set(['output1']));
		expect(drawn.inputs).toEqual([MAIN, { name: 'inModel', kind: 'ai_languageModel' }]);
		expect(drawn.outputs).toEqual([{ name: 'output1', kind: 'main' }]);
	});

	it('falls back to the plain main in and out only when nothing is wired at all', () => {
		const drawn = placeholderDefinition(placeholder, new Set(), new Set());
		expect(drawn.inputs).toEqual([MAIN]);
		expect(drawn.outputs).toEqual([MAIN]);
		expect(nodeShape(drawn)).toBe('step');
	});

	it('ignores handles the definition does not declare', () => {
		const drawn = placeholderDefinition(placeholder, new Set([null, 'nothing']), new Set([undefined]));
		expect(drawn.inputs).toEqual([MAIN]);
		expect(drawn.outputs).toEqual([MAIN]);
	});
});
