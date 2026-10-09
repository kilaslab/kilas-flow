import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import type { Definition, Node, Port } from '$lib/api/generated/models';

import { resolvedPorts } from './ports';

// nodes/testdata/ports_for.golden.json is written by the Go suite
// (nodes/ports_for_golden_test.go) from the real registry: the static ports of
// each definition, and the ports node.Definition.PortsFor resolves for each
// configuration. resolvedPorts is a hand-written mirror of those Go functions,
// so this file is what turns "the two agree today" into "a change on either side
// fails a test": regenerate the golden after a Go change and this suite names
// the configurations the canvas now draws wrongly.
type GoldenCase = {
	name: string;
	nodeType: string;
	parameters: Record<string, unknown> | null;
	inputs: Port[] | null;
	outputs: Port[] | null;
};

type Golden = {
	definitions: Record<string, { inputs: Port[] | null; outputs: Port[] | null }>;
	cases: GoldenCase[];
};

const golden = JSON.parse(
	readFileSync(fileURLToPath(new URL('../../../../nodes/testdata/ports_for.golden.json', import.meta.url)), 'utf8')
) as Golden;

function definitionOf(nodeType: string): Definition {
	const declared = golden.definitions[nodeType];
	if (!declared) throw new Error(`the golden carries no static ports for ${nodeType}`);
	return {
		type: nodeType,
		version: 1,
		displayName: nodeType,
		category: 'AI',
		group: ['transform'],
		source: 'builtin',
		inputs: declared.inputs ?? [],
		outputs: declared.outputs ?? [],
		parameters: [],
		sharedSettings: []
	};
}

function nodeOf(nodeType: string, parameters: Record<string, unknown> | null): Node {
	return {
		id: 'node-1',
		name: 'Node',
		type: nodeType,
		typeVersion: 1,
		position: { x: 0, y: 0 },
		...(parameters ? { parameters } : {})
	} as Node;
}

describe('resolvedPorts against the ports Go resolves', () => {
	it('reads a golden with the vector stores and the embeddings node in it', () => {
		const types = new Set(golden.cases.map((c) => c.nodeType));
		expect([...types].sort()).toEqual(['kilasflow.embeddings', 'kilasflow.vectorStore', 'kilasflow.vectorStorePGVector']);
		expect(golden.cases.length).toBeGreaterThan(60);
	});

	for (const c of golden.cases) {
		it(`${c.nodeType.replace('kilasflow.', '')}: ${c.name}`, () => {
			const resolved = resolvedPorts(nodeOf(c.nodeType, c.parameters), definitionOf(c.nodeType));

			expect(resolved.inputs).toEqual(c.inputs ?? []);
			expect(resolved.outputs).toEqual(c.outputs ?? []);
		});
	}
});
