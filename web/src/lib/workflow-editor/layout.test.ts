import { describe, expect, it } from 'vitest';

import type { Document } from '$lib/api/generated/models';

import {
	DEFAULT_NODE_HEIGHT,
	DEFAULT_NODE_SEP,
	DEFAULT_NODE_WIDTH,
	DEFAULT_RANK_SEP,
	layoutGraph,
	tidyDocument
} from './layout';

describe('layoutGraph', () => {
	it('returns empty for no nodes', () => {
		expect(layoutGraph([], [])).toEqual([]);
	});

	it('lays out a chain left-to-right', () => {
		const positions = layoutGraph(
			[{ id: 'a' }, { id: 'b' }, { id: 'c' }],
			[
				{ source: 'a', target: 'b' },
				{ source: 'b', target: 'c' }
			]
		);
		const byID = Object.fromEntries(positions.map((p) => [p.id, p.position]));

		expect(byID.a.x).toBeLessThan(byID.b.x);
		expect(byID.b.x).toBeLessThan(byID.c.x);
		// Same rank row for a simple chain.
		expect(Math.abs(byID.a.y - byID.b.y)).toBeLessThan(8);
		expect(Math.abs(byID.b.y - byID.c.y)).toBeLessThan(8);
	});

	it('stacks branches vertically when an IF fans out', () => {
		const positions = layoutGraph(
			[{ id: 'if' }, { id: 'yes' }, { id: 'no' }, { id: 'merge' }],
			[
				{ source: 'if', target: 'yes' },
				{ source: 'if', target: 'no' },
				{ source: 'yes', target: 'merge' },
				{ source: 'no', target: 'merge' }
			]
		);
		const byID = Object.fromEntries(positions.map((p) => [p.id, p.position]));

		expect(byID.if.x).toBeLessThan(byID.yes.x);
		expect(byID.if.x).toBeLessThan(byID.no.x);
		expect(byID.yes.x).toBeLessThan(byID.merge.x);
		expect(Math.abs(byID.yes.y - byID.no.y)).toBeGreaterThan(DEFAULT_NODE_HEIGHT / 2);
	});

	it('places isolated nodes without overlapping', () => {
		const positions = layoutGraph([{ id: 'solo-a' }, { id: 'solo-b' }], []);
		expect(positions).toHaveLength(2);
		const [a, b] = positions;
		const dx = Math.abs(a.position.x - b.position.x);
		const dy = Math.abs(a.position.y - b.position.y);
		expect(dx >= DEFAULT_NODE_WIDTH || dy >= DEFAULT_NODE_HEIGHT / 2).toBe(true);
	});

	it('ignores edges that reference missing nodes or self-loops', () => {
		const positions = layoutGraph(
			[{ id: 'a' }, { id: 'b' }],
			[
				{ source: 'a', target: 'missing' },
				{ source: 'a', target: 'a' },
				{ source: 'a', target: 'b' }
			]
		);
		const byID = Object.fromEntries(positions.map((p) => [p.id, p.position]));
		expect(byID.a.x).toBeLessThan(byID.b.x);
	});

	it('honours TB direction', () => {
		const positions = layoutGraph(
			[{ id: 'a' }, { id: 'b' }],
			[{ source: 'a', target: 'b' }],
			{ direction: 'TB' }
		);
		const byID = Object.fromEntries(positions.map((p) => [p.id, p.position]));
		expect(byID.a.y).toBeLessThan(byID.b.y);
	});
});

describe('tidyDocument', () => {
	it('leaves an empty document unchanged', () => {
		const doc: Document = { id: 'wf_empty', schemaVersion: 1, name: 'Empty', nodes: [], connections: [], settings: {} };
		expect(tidyDocument(doc)).toBe(doc);
	});

	it('rewrites positions from connections and preserves everything else', () => {
		const doc: Document = {
			id: 'wf_trip',
			schemaVersion: 1,
			name: 'Trip',
			nodes: [
				{
					id: 'manual',
					name: 'Manual',
					type: 'kilasflow.manualTrigger',
					typeVersion: 1,
					position: { x: 999, y: 999 },
					parameters: { keep: true }
				},
				{
					id: 'set',
					name: 'Set',
					type: 'kilasflow.set',
					typeVersion: 1,
					position: { x: 10, y: 10 }
				}
			],
			connections: [
				{
					id: 'c1',
					kind: 'main',
					source: { nodeId: 'manual', port: 'main' },
					target: { nodeId: 'set', port: 'main' }
				}
			],
			settings: { timezone: 'UTC' }
		};

		const tidied = tidyDocument(doc);
		expect(tidied).not.toBe(doc);
		expect(tidied.name).toBe('Trip');
		expect(tidied.settings).toEqual({ timezone: 'UTC' });
		expect(tidied.connections).toEqual(doc.connections);
		expect(tidied.nodes?.[0].parameters).toEqual({ keep: true });

		const manual = tidied.nodes!.find((n) => n.id === 'manual')!;
		const set = tidied.nodes!.find((n) => n.id === 'set')!;
		expect(manual.position.x).toBeLessThan(set.position.x);
		expect(manual.position).not.toEqual({ x: 999, y: 999 });
	});
});


describe('tidy density defaults', () => {
	it('keeps rank/node separation compact (n8n-like, not stringy)', () => {
		// Previous defaults were rankSep 132 / nodeSep 70 and produced wide
		// horizontal chains after Tidy on Create Trip. Guard the denser floor.
		// Compact pass tightened further for the 68px tile (rankSep 80 /
		// nodeSep 44) so long chains fit.
		expect(DEFAULT_RANK_SEP).toBeLessThanOrEqual(90);
		expect(DEFAULT_NODE_SEP).toBeLessThanOrEqual(50);
		expect(DEFAULT_NODE_WIDTH).toBe(72);
		expect(DEFAULT_NODE_HEIGHT).toBeLessThanOrEqual(100);
	});

	it('packs a chain tighter than the pre-tune rank separation', () => {
		const nodes = [{ id: 'a' }, { id: 'b' }, { id: 'c' }];
		const edges = [
			{ source: 'a', target: 'b' },
			{ source: 'b', target: 'c' }
		];
		const dense = layoutGraph(nodes, edges);
		const stringy = layoutGraph(nodes, edges, { rankSep: 132, nodeSep: 70 });
		const span = (positions: ReturnType<typeof layoutGraph>) => {
			const xs = positions.map((p) => p.position.x);
			return Math.max(...xs) - Math.min(...xs);
		};
		expect(span(dense)).toBeLessThan(span(stringy));
	});
});

describe('tidy with attachments, annotations and real sizes', () => {
	it('places an agent\'s model beneath it instead of in a column beside the graph', () => {
		const positions = layoutGraph(
			[{ id: 'trigger' }, { id: 'agent', width: 144, height: 56 }, { id: 'model', width: 48, height: 48 }, { id: 'memory', width: 48, height: 48 }],
			[
				{ source: 'trigger', target: 'agent' },
				{ source: 'model', target: 'agent', attachment: true },
				{ source: 'memory', target: 'agent', attachment: true }
			]
		);
		const byID = Object.fromEntries(positions.map((p) => [p.id, p.position]));

		// The regression this exists for: attachment wires were ranked as
		// upstream steps, so the model and memory were laid out to the left of
		// the agent that consumes them.
		expect(byID.model.y).toBeGreaterThan(byID.agent.y + 56);
		expect(byID.memory.y).toBeGreaterThan(byID.agent.y + 56);
		expect(byID.model.y).toBe(byID.memory.y);
		expect(byID.model.x).toBeLessThan(byID.memory.x);
	});

	it('leaves no two tiles overlapping, whatever the shape', () => {
		const positions = layoutGraph(
			[
				{ id: 'trigger', width: 68, height: 68 },
				{ id: 'agent', width: 144, height: 56 },
				{ id: 'model', width: 48, height: 48 },
				{ id: 'memory', width: 48, height: 48 },
				{ id: 'tool', width: 48, height: 48 },
				{ id: 'loader', width: 68, height: 68 },
				{ id: 'splitter', width: 68, height: 68 }
			],
			[
				{ source: 'trigger', target: 'agent' },
				{ source: 'model', target: 'agent', attachment: true },
				{ source: 'memory', target: 'agent', attachment: true },
				{ source: 'tool', target: 'agent', attachment: true },
				{ source: 'splitter', target: 'loader', attachment: true },
				{ source: 'loader', target: 'agent', attachment: true }
			]
		);
		const sizes: Record<string, { width: number; height: number }> = {
			trigger: { width: 68, height: 68 },
			agent: { width: 144, height: 56 },
			model: { width: 48, height: 48 },
			memory: { width: 48, height: 48 },
			tool: { width: 48, height: 48 },
			loader: { width: 68, height: 68 },
			splitter: { width: 68, height: 68 }
		};

		for (let left = 0; left < positions.length; left += 1) {
			for (let right = left + 1; right < positions.length; right += 1) {
				const one = { ...positions[left].position, ...sizes[positions[left].id] };
				const other = { ...positions[right].position, ...sizes[positions[right].id] };
				const overlaps =
					one.x < other.x + other.width && other.x < one.x + one.width && one.y < other.y + other.height && other.y < one.y + one.height;
				expect(overlaps, `${positions[left].id} overlaps ${positions[right].id}`).toBe(false);
			}
		}
		expect(positions).toHaveLength(7);
	});

	it('keeps a sticky note out of the layout and moves it with the nodes it covered', () => {
		const doc: Document = {
			id: 'wf_sticky',
			schemaVersion: 1,
			name: 'Annotated',
			nodes: [
				{ id: 'manual', name: 'Manual', type: 'kilasflow.manual', typeVersion: 1, position: { x: 900, y: 900 } },
				{ id: 'set', name: 'Set', type: 'kilasflow.set', typeVersion: 1, position: { x: 1120, y: 900 } },
				{ id: 'note', name: 'Sticky Note', type: 'kilasflow.stickyNote', typeVersion: 1, position: { x: 860, y: 860 }, parameters: { width: 400, height: 200 } },
				{ id: 'lonely', name: 'Sticky Note1', type: 'kilasflow.stickyNote', typeVersion: 1, position: { x: 0, y: 0 }, parameters: { width: 200, height: 160 } }
			],
			connections: [{ id: 'c1', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'set', port: 'main' } }],
			settings: {}
		};

		const tidied = tidyDocument(doc, { annotations: ['note', 'lonely'] });
		const note = tidied.nodes!.find((node) => node.id === 'note')!;
		const lonely = tidied.nodes!.find((node) => node.id === 'lonely')!;
		const manual = tidied.nodes!.find((node) => node.id === 'manual')!;

		// The note travelled with the chain it annotated, keeping its inset.
		expect(note.position.x - manual.position.x).toBe(860 - 900);
		expect(note.position.y - manual.position.y).toBe(860 - 900);
		// A note covering nothing is the author's placement, not the layout's.
		expect(lonely.position).toEqual({ x: 0, y: 0 });
	});

	it('lays out a hub at its measured width so wide tiles do not collide', () => {
		const nodes = [
			{ id: 'a', width: 240, height: 56 },
			{ id: 'b', width: 240, height: 56 },
			{ id: 'start', width: 68, height: 68 }
		];
		const edges = [
			{ source: 'start', target: 'a' },
			{ source: 'start', target: 'b' }
		];
		const positions = Object.fromEntries(layoutGraph(nodes, edges).map((entry) => [entry.id, entry.position]));

		// Sampled sizes reach dagre: 240px tiles in one rank are separated by at
		// least their width, which the 72px default could never guarantee.
		expect(Math.abs(positions.a.y - positions.b.y)).toBeGreaterThanOrEqual(56);
	});
});
