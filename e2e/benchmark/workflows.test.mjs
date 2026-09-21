/**
 * Fixture tests for FEAT-8mymac (method version 2).
 *
 * These are the structural half of the "identical work" claim: rows 1–4 are
 * one n8n workflow JSON each, imported into KilasFlow, so the two engines run
 * the same graph. The n8n format facts pinned here were verified against the
 * n8n 2.33.7 image during the plan review (webhookId, executionOrder v1,
 * Split In Batches v3 output numbering, the If operator's required `type`).
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import {
	BENCHMARKS,
	RUN_SUFFIX,
	SHARED_ROW_KEYS,
	SUPPLEMENTARY_ROW_KEYS,
	benchByKey,
	kilasSourceFor,
	payloadFor,
	stubResponse,
} from './workflows.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));
const CONTEXT = {
	origin: 'http://127.0.0.1:49152',
	n8nOrigin: 'http://host.docker.internal:49152',
	modelBaseURL: 'http://127.0.0.1:49152/v1',
};

const shared = SHARED_ROW_KEYS.map(benchByKey);

function nodesOf(json) {
	return json.nodes ?? [];
}

function findNode(json, type) {
	return nodesOf(json).find((node) => node.type === type);
}

function connectionTargets(json) {
	const targets = [];
	for (const [source, channels] of Object.entries(json.connections ?? {})) {
		for (const [kind, slots] of Object.entries(channels)) {
			slots.forEach((slot, index) => {
				for (const edge of slot ?? []) targets.push({ source, kind, index, target: edge.node });
			});
		}
	}
	return targets;
}

test('every shared fixture is a webhook-triggered n8n workflow: one webhook in responseNode mode carrying a webhookId, one respondToWebhook with respondWith allIncomingItems, and settings.executionOrder v1', () => {
	for (const bench of BENCHMARKS) {
		const json = bench.n8n(CONTEXT);
		const hooks = nodesOf(json).filter((node) => node.type === 'n8n-nodes-base.webhook');
		assert.equal(hooks.length, 1, `${bench.key}: one webhook trigger`);
		assert.equal(hooks[0].parameters.responseMode, 'responseNode', `${bench.key}: the delivery must answer from the Respond node`);
		assert.equal(hooks[0].parameters.httpMethod, 'POST', `${bench.key}: POST delivery`);
		assert.ok(hooks[0].webhookId, `${bench.key}: without webhookId n8n's URL is /webhook/<workflowId>/<name>/<path>`);
		const responds = nodesOf(json).filter((node) => node.type === 'n8n-nodes-base.respondToWebhook');
		assert.equal(responds.length, 1, `${bench.key}: one Respond node`);
		assert.equal(responds[0].parameters.respondWith, 'allIncomingItems', `${bench.key}: the body is the item array`);
		// n8n runs the legacy execution order unless this says otherwise.
		assert.equal(json.settings?.executionOrder, 'v1', `${bench.key}: settings.executionOrder`);
	}
});

test('every n8n connection targets an existing node name', () => {
	for (const bench of BENCHMARKS) {
		const json = bench.n8n(CONTEXT);
		const names = new Set(nodesOf(json).map((node) => node.name));
		for (const edge of connectionTargets(json)) {
			assert.ok(names.has(edge.source), `${bench.key}: connection source ${edge.source} exists`);
			assert.ok(names.has(edge.target), `${bench.key}: connection target ${edge.target} exists`);
		}
	}
});

test('paginated-loop wires Split In Batches v3 done as output 0 and loop as output 1, and Body loops back to Loop', () => {
	const json = benchByKey('paginated-loop').n8n(CONTEXT);
	const loop = findNode(json, 'n8n-nodes-base.splitInBatches');
	assert.ok(loop, 'a Split In Batches node');
	assert.equal(loop.typeVersion, 3, 'v1/v2 have a different output shape and import as blocking');
	assert.equal(loop.parameters.batchSize, 10);
	const outputs = json.connections.Loop.main;
	assert.equal(outputs[0][0].node, 'Done', 'output 0 is done');
	assert.equal(outputs[1][0].node, 'Body', 'output 1 is loop');
	assert.equal(json.connections.Body.main[0][0].node, 'Loop', 'the body returns to the loop');
	// The seed arrives in the request payload, so the row contains no Code node.
	const split = findNode(json, 'n8n-nodes-base.splitOut');
	assert.equal(split.parameters.fieldToSplitOut, 'body.items');
});

test('the if operator carries type string and operation equals with options.version 2', () => {
	const json = benchByKey('if-fanout').n8n(CONTEXT);
	const node = findNode(json, 'n8n-nodes-base.if');
	const [condition] = node.parameters.conditions.conditions;
	assert.equal(condition.operator.type, 'string', 'the importer does not default the operator type');
	assert.equal(condition.operator.operation, 'equals');
	assert.equal(condition.leftValue, '={{ $json.v }}');
	assert.equal(condition.rightValue, 'x');
	assert.equal(node.parameters.conditions.options.version, 2);
	assert.equal(node.parameters.conditions.combinator, 'and');
	const outputs = json.connections.If.main;
	assert.equal(outputs[0][0].node, 'Hit', 'output 0 is true');
	assert.equal(outputs[1][0].node, 'Miss', 'output 1 is false');
});

test('rows 1-4 contain no Code node, and the code-node row is flagged supplementary and divergent by design', () => {
	for (const bench of shared) {
		const json = bench.n8n(CONTEXT);
		for (const node of nodesOf(json)) {
			assert.notEqual(node.type, 'n8n-nodes-base.code', `${bench.key}: no Code node in a headline row`);
		}
		assert.equal(bench.supplementary, false);
		assert.equal(bench.divergentByDesign, false);
	}
	const code = benchByKey('code-node');
	assert.equal(code.supplementary, true);
	assert.equal(code.divergentByDesign, true);
	assert.ok(SUPPLEMENTARY_ROW_KEYS.includes(code.key));
	assert.ok(findNode(code.n8n(CONTEXT), 'n8n-nodes-base.code'), 'the supplementary row does run a Code node');
	assert.ok(
		nodesOf(code.kilas(CONTEXT)).some((node) => node.type === 'kilasflow.code'),
		'and KilasFlow runs its compiled Go Code node',
	);
	// n8n's Code node is JavaScript, which KilasFlow refuses to run: the
	// KilasFlow side of this row is a native document, never the imported JSON.
	assert.equal(code.source, 'native-kilasflow-doc');
});

test('the KilasFlow side of every shared row is derived from the same n8n JSON object, not a second hand-written copy', () => {
	for (const bench of shared) {
		assert.equal(bench.kilas, undefined, `${bench.key}: a shared row must not carry a native document`);
		assert.equal(bench.source, 'shared-n8n-json');
		// Identity, not deep equality: the harness imports the very object n8n runs.
		assert.equal(kilasSourceFor(bench, CONTEXT), bench.n8n(CONTEXT), `${bench.key}: one JSON object for both engines`);
		assert.equal(kilasSourceFor(bench, CONTEXT), kilasSourceFor(bench, CONTEXT), `${bench.key}: stable across calls`);
	}
});

test('no fixture hardcodes a host: the HTTP row reads the stub base from the delivery body', () => {
	const json = benchByKey('http-merge-shape').n8n(CONTEXT);
	for (const id of ['users', 'orders']) {
		const node = nodesOf(json).find((entry) => entry.id === id);
		assert.match(node.parameters.url, /^=?\{\{ \$json\.body\.stubBase \}\}\/api\/(users|orders)$/, `${id}: the stub address comes from the payload`);
	}
	// The two engines reach the same server at different addresses, so the
	// payload — not the workflow — carries the origin.
	const payload = payloadFor(benchByKey('http-merge-shape'), 0, CONTEXT);
	assert.equal(payload.stubBase, CONTEXT.origin);
});

test('agent fixture declares ai_languageModel and ai_tool sub-node connections and has no memory node', () => {
	const bench = benchByKey('agent-tool-loop');
	const json = bench.n8n(CONTEXT);
	const kinds = new Set(connectionTargets(json).map((edge) => edge.kind));
	assert.ok(kinds.has('ai_languageModel'), 'the chat model configures the agent');
	assert.ok(kinds.has('ai_tool'), 'the HTTP tool is a tool of the agent');
	for (const node of nodesOf(json)) {
		assert.doesNotMatch(node.type, /memory/i, 'a constant session key would accumulate context across runs');
	}
	const doc = bench.kilas(CONTEXT);
	const kilasKinds = new Set((doc.connections ?? []).map((edge) => edge.kind));
	assert.ok(kilasKinds.has('ai_languageModel'));
	assert.ok(kilasKinds.has('ai_tool'));
	assert.ok(!doc.nodes.some((node) => /memory/i.test(node.type)), 'no memory node on the KilasFlow side either');
	assert.ok(doc.nodes.some((node) => node.type === 'kilasflow.chatModel'));
	assert.ok(doc.nodes.some((node) => node.type === 'kilasflow.httpTool'));
});

test('every bench declares expect.items and expect.stubCalls, and expect.body for the deterministic rows', () => {
	for (const bench of BENCHMARKS) {
		assert.equal(typeof bench.expect.items, 'number', `${bench.key}: expect.items`);
		assert.equal(typeof bench.expect.stubCalls, 'number', `${bench.key}: expect.stubCalls`);
		assert.equal(bench.kind, 'webhook', `${bench.key}: every row is delivered over HTTP on both engines`);
		if (SHARED_ROW_KEYS.includes(bench.key)) {
			assert.ok(Array.isArray(bench.expect.body), `${bench.key}: a deterministic row deep-equals its body`);
		} else {
			assert.equal(typeof bench.expect.contains, 'string', `${bench.key}: an engine-specific row declares a spot value`);
		}
	}
	// The expectations are the served stub bodies, so they cannot drift apart.
	const shape = benchByKey('http-merge-shape').expect.body;
	assert.deepEqual(shape[0], { ...stubResponse('/api/users').body, shaped: true });
	assert.deepEqual(shape[1], { ...stubResponse('/api/orders').body, shaped: true });
	assert.equal(benchByKey('paginated-loop').expect.body.length, 100);
	assert.deepEqual(benchByKey('paginated-loop').expect.body[99], { n: 99, seen: true });
});

test('the webhook path is a parameter with a per-run random suffix', () => {
	assert.match(RUN_SUFFIX, /^[0-9a-f]{8}$/);
	for (const bench of BENCHMARKS) {
		assert.match(bench.webhookPath, /^bench-[a-z0-9-]+-[0-9a-f]{8}$/, `${bench.key}: path shape`);
		assert.ok(bench.webhookPath.endsWith(RUN_SUFFIX), `${bench.key}: the suffix is this process's`);
		const json = bench.n8n(CONTEXT);
		const hook = nodesOf(json).find((node) => node.type === 'n8n-nodes-base.webhook');
		assert.equal(hook.parameters.path, bench.webhookPath, `${bench.key}: the path is the node parameter`);
	}
});

test('no fixture or test file contains an absolute home path', async () => {
	// Built from fragments exactly as internal/guardrails does, so this test
	// does not itself trip the repository's licence-boundary scan.
	const markers = ['/Us' + 'ers/', '/ho' + 'me/', 'mitra' + 'chat/n8n'];
	const files = ['workflows.mjs', 'lib.mjs', 'method.mjs', 'run.mjs', 'summarise.mjs', 'workflows.test.mjs', 'method.test.mjs', 'summarise.test.mjs'];
	for (const file of files) {
		let body;
		try {
			body = await readFile(join(HERE, file), 'utf-8');
		} catch {
			continue; // A stage-1 file list may name a file a later stage adds.
		}
		for (const marker of markers) {
			assert.ok(!body.includes(marker), `${file} contains ${marker}`);
		}
	}
});
