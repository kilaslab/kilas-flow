/**
 * The fixed benchmark set for FEAT-8mymac, method version 2.
 *
 * Rows 1–4 are ONE hand-authored n8n workflow JSON each, executed on both
 * engines: n8n runs the JSON directly, KilasFlow imports that same JSON
 * through POST /workflows/import. Identical work is therefore true by
 * construction — there is no second, hand-written copy that can drift, which
 * is what v1 got wrong (its n8n http-merge fixture had no shaping node at all
 * while the KilasFlow document did, and the count-plus-spot-value check could
 * not see the difference).
 *
 * Row 5 (AI agent) and row 6 (Code) keep a native KilasFlow document beside
 * the n8n JSON, and are labelled:
 *   - row 5 because n8n's Ollama chat model cannot be imported without loss
 *     (the importer forces the endpoint to http://localhost:11434/v1 and
 *     reports the credential it cannot carry);
 *   - row 6 because n8n's Code node is JavaScript, which KilasFlow refuses to
 *     run by design — it imports as kilasflow.foreignCode and blocks. The row
 *     exists to isolate the Go/WASM-versus-JS Code cost that v1 buried inside
 *     two headline rows; it is supplementary and divergent by design, and is
 *     not part of the identical-work claim.
 *
 * All third-party calls go to the same loopback bench server (lib.mjs), so the
 * comparison measures engine overhead and not internet variance. The HTTP row
 * reads the stub's address from the delivery payload (`{{ $json.body.stubBase }}`)
 * rather than hard-coding one, because the two engines reach the same server
 * from different network namespaces.
 */

import { randomBytes, randomUUID } from 'node:crypto';

/**
 * One random suffix per harness process, so a stale activation from an
 * earlier run can never be mistaken for this one's. It is part of the webhook
 * node's `path` parameter, which n8n uses in the production URL.
 */
export const RUN_SUFFIX = randomBytes(4).toString('hex');

/** Rows measured on both engines from the same n8n JSON. */
export const SHARED_ROW_KEYS = ['webhook-set-respond', 'if-fanout', 'paginated-loop', 'http-merge-shape'];

/** The Code row: measured on both engines, excluded from the headline claim. */
export const SUPPLEMENTARY_ROW_KEYS = ['code-node'];

const AGENT_PROMPT = 'What is the weather in Utrecht? Use the get_weather tool to find out.';

/** Deterministic stub bodies, shared by the bench server and by the expectations. */
export function stubResponse(pathname) {
	if (pathname === '/api/users' || pathname === '/api/orders') {
		const kind = pathname === '/api/users' ? 'user' : 'order';
		return { status: 200, body: { rows: Array.from({ length: 25 }, (_, i) => ({ id: i + 1, kind, name: `${kind}-${i + 1}` })) } };
	}
	if (pathname === '/api/echo') return { status: 200, body: { ok: true, path: pathname } };
	if (pathname.startsWith('/tool/weather/')) {
		return { status: 200, body: { city: decodeURIComponent(pathname.slice('/tool/weather/'.length)), tempC: 19 } };
	}
	return null;
}

// ---------------------------------------------------------------------------
// n8n document helpers
// ---------------------------------------------------------------------------

/** n8n workflow envelope. `executionOrder: 'v1'` is required or n8n runs the legacy order. */
function n8nDoc(name, nodes, connections) {
	return { name, nodes, connections, settings: { executionOrder: 'v1' } };
}

function n8nNode(id, name, type, typeVersion, position, parameters, extra = {}) {
	return { id, name, type, typeVersion, position, parameters, ...extra };
}

/** The Webhook trigger. `webhookId` is what makes n8n's URL /webhook/<path>. */
function webhookNode(path) {
	return n8nNode('hook', 'Webhook', 'n8n-nodes-base.webhook', 2, [0, 0], {
		httpMethod: 'POST',
		path,
		responseMode: 'responseNode',
		options: {},
	}, { webhookId: randomUUID() });
}

/**
 * Set v3.4. The `assignments` (assignmentCollection) parameter is only read
 * from v3.4 onwards: at v3.0–3.2 the node reads `fields` (fixedCollection)
 * instead and silently passes the input item through, which is exactly the
 * fixture drift the plan review warned about and stage 2 caught on the live
 * instance. Verified against n8n 2.33.7's own node description and by delivery.
 * `includeOtherFields: true` keeps the input fields, which n8n's default drops.
 */
function setNode(id, name, position, assignments, includeOtherFields = false) {
	return n8nNode(id, name, 'n8n-nodes-base.set', 3.4, position, {
		mode: 'manual',
		assignments: { assignments },
		includeOtherFields,
		options: {},
	});
}

function respondNode(id, position) {
	return n8nNode(id, 'Respond', 'n8n-nodes-base.respondToWebhook', 1, position, {
		respondWith: 'allIncomingItems',
		options: {},
	});
}

function mainChain(names) {
	const connections = {};
	for (let i = 0; i < names.length - 1; i++) {
		connections[names[i]] = { main: [[{ node: names[i + 1], type: 'main', index: 0 }]] };
	}
	return connections;
}

// ---------------------------------------------------------------------------
// Native KilasFlow document helpers
// ---------------------------------------------------------------------------

function kilasNode(id, name, type, parameters, typeVersion = 1) {
	return { id, name, type, typeVersion, position: { x: 0, y: 0 }, parameters };
}

function kilasDoc(name, nodes, connections) {
	return { schemaVersion: 1, name, nodes, connections, settings: {} };
}

function kilasConn(id, source, sourcePort, target, targetPort, kind = 'main') {
	return { id, kind, source: { nodeId: source, port: sourcePort }, target: { nodeId: target, port: targetPort } };
}

function kilasWebhook(path) {
	return kilasNode('hook', 'Webhook', 'kilasflow.webhook', { path, httpMethod: 'POST', responseMode: 'responseNode' });
}

function kilasRespond(id = 'resp') {
	return kilasNode(id, 'Respond', 'kilasflow.respondToWebhook', { respondWith: 'allIncomingItems' });
}

// ---------------------------------------------------------------------------
// Rows
// ---------------------------------------------------------------------------

function webhookPath(key) {
	return `bench-${key}-${RUN_SUFFIX}`;
}

/** Built once per process and handed to both engines — one object, not a copy. */
const SHARED_JSON = {};

SHARED_JSON['webhook-set-respond'] = n8nDoc(
	'Bench Webhook Set Respond',
	[
		webhookNode(webhookPath('webhook-set-respond')),
		setNode('pin', 'Pin', [240, 0], [{ id: 'a1', name: 'v', value: 'bench-ok', type: 'string' }]),
		respondNode('resp', [480, 0]),
	],
	mainChain(['Webhook', 'Pin', 'Respond']),
);

SHARED_JSON['if-fanout'] = n8nDoc(
	'Bench IF Fan-out',
	[
		webhookNode(webhookPath('if-fanout')),
		setNode('set', 'Set', [240, 0], [{ id: 'a1', name: 'v', value: 'x', type: 'string' }]),
		n8nNode('if', 'If', 'n8n-nodes-base.if', 2.2, [480, 0], {
			conditions: {
				options: { caseSensitive: true, typeValidation: 'strict', version: 2 },
				conditions: [
					{
						id: 'c1',
						leftValue: '={{ $json.v }}',
						rightValue: 'x',
						// n8n's operator `type` is not defaulted anywhere in the
						// importer, so it must be written explicitly.
						operator: { type: 'string', operation: 'equals', typeVersion: 2 },
					},
				],
				combinator: 'and',
			},
			options: {},
		}),
		n8nNode('hit', 'Hit', 'n8n-nodes-base.noOp', 1, [720, -80], {}),
		n8nNode('miss', 'Miss', 'n8n-nodes-base.noOp', 1, [720, 80], {}),
		respondNode('resp', [960, 0]),
	],
	{
		Webhook: { main: [[{ node: 'Set', type: 'main', index: 0 }]] },
		Set: { main: [[{ node: 'If', type: 'main', index: 0 }]] },
		// Split In Batches v3 and the If node both put the "keep going" branch
		// on a numbered output; If output 0 is true, output 1 is false.
		If: { main: [[{ node: 'Hit', type: 'main', index: 0 }], [{ node: 'Miss', type: 'main', index: 0 }]] },
		Hit: { main: [[{ node: 'Respond', type: 'main', index: 0 }]] },
	},
);

SHARED_JSON['paginated-loop'] = n8nDoc(
	'Bench Paginated Loop',
	[
		webhookNode(webhookPath('paginated-loop')),
		n8nNode('split', 'Split', 'n8n-nodes-base.splitOut', 1, [240, 0], {
			fieldToSplitOut: 'body.items',
			options: {},
		}),
		n8nNode('loop', 'Loop', 'n8n-nodes-base.splitInBatches', 3, [480, 0], { batchSize: 10, options: {} }),
		setNode('body', 'Body', [720, 0], [{ id: 'a1', name: 'seen', value: true, type: 'boolean' }], true),
		n8nNode('done', 'Done', 'n8n-nodes-base.noOp', 1, [960, 0], {}),
		respondNode('resp', [1200, 0]),
	],
	{
		Webhook: { main: [[{ node: 'Split', type: 'main', index: 0 }]] },
		Split: { main: [[{ node: 'Loop', type: 'main', index: 0 }]] },
		// Split In Batches v3: output 0 is `done`, output 1 is `loop`.
		Loop: { main: [[{ node: 'Done', type: 'main', index: 0 }], [{ node: 'Body', type: 'main', index: 0 }]] },
		Body: { main: [[{ node: 'Loop', type: 'main', index: 0 }]] },
		Done: { main: [[{ node: 'Respond', type: 'main', index: 0 }]] },
	},
);

SHARED_JSON['http-merge-shape'] = n8nDoc(
	'Bench HTTP Merge Shape',
	[
		webhookNode(webhookPath('http-merge-shape')),
		// The stub's address comes from the delivery body, because KilasFlow is
		// a host process (127.0.0.1) and n8n a container (host.docker.internal).
		// Reading it from the payload keeps ONE workflow JSON for both engines.
		n8nNode('users', 'Users', 'n8n-nodes-base.httpRequest', 4, [240, -80], {
			method: 'GET',
			url: '={{ $json.body.stubBase }}/api/users',
			options: {},
		}),
		n8nNode('orders', 'Orders', 'n8n-nodes-base.httpRequest', 4, [240, 80], {
			method: 'GET',
			url: '={{ $json.body.stubBase }}/api/orders',
			options: {},
		}),
		n8nNode('merge', 'Merge', 'n8n-nodes-base.merge', 3, [480, 0], { mode: 'append', options: {} }),
		setNode('shape', 'Shape', [720, 0], [{ id: 'a1', name: 'shaped', value: true, type: 'boolean' }], true),
		respondNode('resp', [960, 0]),
	],
	{
		Webhook: { main: [[{ node: 'Users', type: 'main', index: 0 }, { node: 'Orders', type: 'main', index: 0 }]] },
		Users: { main: [[{ node: 'Merge', type: 'main', index: 0 }]] },
		Orders: { main: [[{ node: 'Merge', type: 'main', index: 1 }]] },
		Merge: { main: [[{ node: 'Shape', type: 'main', index: 0 }]] },
		Shape: { main: [[{ node: 'Respond', type: 'main', index: 0 }]] },
	},
);

/** Row 4's expectation is the two stub bodies plus the shaping field. */
function mergeShapeExpectation() {
	return [
		{ ...stubResponse('/api/users').body, shaped: true },
		{ ...stubResponse('/api/orders').body, shaped: true },
	];
}

function loopExpectation() {
	return Array.from({ length: 100 }, (_, n) => ({ n, seen: true }));
}

export const BENCHMARKS = [
	{
		key: 'webhook-set-respond',
		name: 'Webhook → Set → Respond',
		kind: 'webhook',
		source: 'shared-n8n-json',
		supplementary: false,
		divergentByDesign: false,
		divergence: null,
		expect: { items: 1, body: [{ v: 'bench-ok' }], stubCalls: 0 },
		webhookPath: webhookPath('webhook-set-respond'),
		n8n: () => SHARED_JSON['webhook-set-respond'],
		payload: () => ({}),
	},
	{
		key: 'if-fanout',
		name: 'IF branch fan-out',
		kind: 'webhook',
		source: 'shared-n8n-json',
		supplementary: false,
		divergentByDesign: false,
		divergence: null,
		expect: { items: 1, body: [{ v: 'x' }], stubCalls: 0 },
		webhookPath: webhookPath('if-fanout'),
		n8n: () => SHARED_JSON['if-fanout'],
		payload: () => ({}),
	},
	{
		key: 'paginated-loop',
		name: 'Paginated loop (100 items, batch 10)',
		kind: 'webhook',
		source: 'shared-n8n-json',
		supplementary: false,
		divergentByDesign: false,
		divergence:
			'KilasFlow answers a Respond with allIncomingItems by re-encoding the whole array once per incoming item ' +
			'(nodes/webhook.go), which is quadratic on this row; n8n answers once. Real engine behaviour, reported as a follow-up, not fixed here.',
		expect: { items: 100, body: loopExpectation(), stubCalls: 0 },
		webhookPath: webhookPath('paginated-loop'),
		n8n: () => SHARED_JSON['paginated-loop'],
		payload: () => ({ items: Array.from({ length: 100 }, (_, n) => ({ n })) }),
	},
	{
		key: 'http-merge-shape',
		name: 'HTTP → Merge → shaping',
		kind: 'webhook',
		source: 'shared-n8n-json',
		supplementary: false,
		divergentByDesign: false,
		divergence:
			'Both engines call the same loopback stub through the same bench server; the address differs because KilasFlow is a host process and ' +
			'n8n a container, so the payload carries it. KilasFlow re-checks the stub address against its SSRF policy on every dial while n8n 2.33.7 has ' +
			'its SSRF protection off by default; both engines stay at their defaults.',
		expect: { items: 2, body: mergeShapeExpectation(), stubCalls: 2 },
		webhookPath: webhookPath('http-merge-shape'),
		n8n: () => SHARED_JSON['http-merge-shape'],
		payload: (index, context) => ({ stubBase: context.origin }),
	},
	{
		key: 'agent-tool-loop',
		name: 'AI-agent tool-loop (local Ollama)',
		kind: 'webhook',
		source: 'native-kilasflow-doc',
		supplementary: false,
		divergentByDesign: false,
		needsModel: true,
		divergence:
			'Not identical in bytes: n8n’s Ollama chat model cannot be imported without loss (the importer forces http://localhost:11434/v1 and ' +
			'reports the credential it cannot carry), so KilasFlow runs a native document with the same wiring. The model is the same local Ollama on ' +
			'both sides but the wire protocol differs (/v1/chat/completions versus native /api/chat). No memory node on either side: a constant session ' +
			'key would accumulate context across every run. This row is expected to be model-bound and usually inconclusive under the uniform variance rule.',
		expect: { items: 1, contains: '19', stubCalls: 0, toolCallsMin: 1, modelCallsMin: 2 },
		webhookPath: webhookPath('agent-tool-loop'),
		payload: () => ({ question: AGENT_PROMPT }),
		kilas: (context) =>
			kilasDoc(
				'Bench Agent Tool Loop',
				[
					kilasWebhook(webhookPath('agent-tool-loop')),
					kilasNode('model', 'Chat Model', 'kilasflow.chatModel', {
						model: process.env.KILASFLOW_TEST_OLLAMA_MODEL?.trim() || 'gemma4:12b-mlx',
						baseUrl: context.modelBaseURL,
						temperature: 0,
						stream: true,
					}),
					kilasNode('weather', 'Weather', 'kilasflow.httpTool', {
						toolName: 'get_weather',
						toolDescription: 'Look up the current temperature for a city. Takes a city name.',
						method: 'GET',
						url: { mode: 'expression', value: `${context.origin}/tool/weather/{{ $fromAI('city', 'the city to look up') }}` },
					}),
					kilasNode('agent', 'Agent', 'kilasflow.agent', {
						prompt: { mode: 'expression', value: '{{ $json.body.question }}' },
						returnIntermediateSteps: true,
					}),
					kilasRespond(),
				],
				[
					kilasConn('c1', 'hook', 'main', 'agent', 'main'),
					kilasConn('c2', 'model', 'model', 'agent', 'model', 'ai_languageModel'),
					kilasConn('c3', 'weather', 'tool', 'agent', 'tools', 'ai_tool'),
					kilasConn('c4', 'agent', 'main', 'resp', 'main'),
				],
			),
		n8n: (context) =>
			n8nDoc(
				'Bench Agent Tool Loop',
				[
					webhookNode(webhookPath('agent-tool-loop')),
					n8nNode('agent', 'Agent', '@n8n/n8n-nodes-langchain.agent', 2, [480, 0], {
						promptType: 'define',
						text: '={{ $json.body.question }}',
						options: { returnIntermediateSteps: true },
					}),
					n8nNode(
						'model',
						'Chat Model',
						'@n8n/n8n-nodes-langchain.lmChatOllama',
						1,
						[240, -120],
						{
							model: process.env.KILASFLOW_TEST_OLLAMA_MODEL?.trim() || 'gemma4:12b-mlx',
							options: { temperature: 0 },
						},
					),
					n8nNode('weather', 'get_weather', '@n8n/n8n-nodes-langchain.toolHttpRequest', 1.1, [240, 120], {
						toolDescription: 'Look up the current temperature for a city. Takes a city name.',
						method: 'GET',
						url: `${context.n8nOrigin}/tool/weather/{city}`,
						placeholderDefinitions: { values: [{ name: 'city', description: 'the city to look up' }] },
					}),
					respondNode('resp', [720, 0]),
				],
				{
					Webhook: { main: [[{ node: 'Agent', type: 'main', index: 0 }]] },
					'Chat Model': { ai_languageModel: [[{ node: 'Agent', type: 'ai_languageModel', index: 0 }]] },
					get_weather: { ai_tool: [[{ node: 'Agent', type: 'ai_tool', index: 0 }]] },
					Agent: { main: [[{ node: 'Respond', type: 'main', index: 0 }]] },
				},
			),
		// Filled by the harness once the credential exists (n8n) and the node
		// id is known (KilasFlow). Both sides name the node, never a value.
		credential: { nodeId: 'model', key: 'ollamaApi', kilasKey: 'httpBearerAuth' },
	},
	{
		key: 'code-node',
		name: 'Code node (Go/WASM vs JS)',
		kind: 'webhook',
		source: 'native-kilasflow-doc',
		supplementary: true,
		divergentByDesign: true,
		divergence:
			'Divergent by design and excluded from the identical-work claim: KilasFlow runs a compiled Go/WASM Code node while n8n runs JavaScript in its ' +
			'task runner. The row exists to isolate the Code cost that v1 buried inside two headline rows (29–42 ms server-side against 1 ms for the Code-free rows).',
		expect: { items: 1, contains: 'engine', stubCalls: 0 },
		webhookPath: webhookPath('code-node'),
		payload: () => ({}),
		kilas: () =>
			kilasDoc(
				'Bench Code Node',
				[
					kilasWebhook(webhookPath('code-node')),
					kilasNode('code', 'Code', 'kilasflow.code', {
						code: 'return []Item{{JSON: map[string]any{"engine": "kilasflow-go"}}}, nil',
					}),
					kilasRespond(),
				],
				[kilasConn('c1', 'hook', 'main', 'code', 'main'), kilasConn('c2', 'code', 'main', 'resp', 'main')],
			),
		n8n: () =>
			n8nDoc(
				'Bench Code Node',
				[
					webhookNode(webhookPath('code-node')),
					n8nNode('code', 'Code', 'n8n-nodes-base.code', 2, [240, 0], {
						mode: 'runOnceForAllItems',
						jsCode: 'return [{ json: { engine: "n8n-js" } }];',
					}),
					respondNode('resp', [480, 0]),
				],
				mainChain(['Webhook', 'Code', 'Respond']),
			),
	},
];

/**
 * The document each engine executes for a row.
 *
 * Shared rows hand back the very same object for both engines — the harness
 * imports `n8n(ctx)` into KilasFlow, so there is no second copy to drift.
 */
export function kilasSourceFor(bench, context) {
	return bench.kilas ? bench.kilas(context) : bench.n8n(context);
}

/** The delivery body for one run on one engine. */
export function payloadFor(bench, index, context) {
	return bench.payload(index, context);
}

/** Look a row up by key, loudly. */
export function benchByKey(key) {
	const bench = BENCHMARKS.find((entry) => entry.key === key);
	if (!bench) throw new Error(`unknown benchmark row ${key}`);
	return bench;
}
