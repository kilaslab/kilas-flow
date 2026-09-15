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
