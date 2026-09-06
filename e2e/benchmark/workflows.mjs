/**
 * The fixed benchmark set for FEAT-8mymac.
 *
 * Five workflows, each defined twice: once as a native KilasFlow document
 * (what run.mjs executes and times on this side) and once as the equivalent
 * n8n JSON (what the credentialed n8n runner imports into the reference
 * instance). Both describe the same logical work; the n8n JSON files are
 * hand-written for this benchmark, not library exports.
 *
 * All third-party calls go to the same loopback bench server (see lib.mjs),
 * so the comparison measures engine overhead, not internet variance.
 */

function node(id, name, type, typeVersion = 1, parameters, credentials) {
	const entry = { id, name, type, typeVersion, position: { x: 0, y: 0 } };
	if (parameters !== undefined) entry.parameters = parameters;
	if (credentials !== undefined) entry.credentials = credentials;
	return entry;
}

function conn(id, source, sourcePort, target, targetPort, kind = 'main') {
	return { id, kind, source: { nodeId: source, port: sourcePort }, target: { nodeId: target, port: targetPort } };
}

const manual = () => node('manual', 'Manual Trigger', 'kilasflow.manual');

/** Native KilasFlow document envelope. */
function doc(name, nodes, connections) {
	return { schemaVersion: 1, name, nodes, connections, settings: {} };
}

/** n8n workflow envelope (hand-written benchmark fixture). */
function n8nDoc(name, nodes, connections) {
	return { name, nodes, connections, settings: {} };
}

function n8nNode(id, name, type, typeVersion, position, parameters, credentials) {
	const entry = { id, name, type, typeVersion, position, parameters };
	if (credentials !== undefined) entry.credentials = credentials;
	return entry;
}

/**
 * Each entry:
 *   key          — stable id used in raw JSON + summary tables
 *   name         — human name
 *   kind         — 'webhook' (deliver over HTTP after activation) or
 *                  'manual' (POST /workflows/:id/run)
 *   kilas        — (bench) => native KilasFlow document. For the webhook
 *                  workflow the document is n8n JSON imported through
 *                  POST /workflows/import so the minted opaque route is
 *                  returned by the API.
 *   n8n          — equivalent n8n JSON for the reference instance
 *   divergence   — expected engine difference, if any (not drift)
 */
export const BENCHMARKS = [
	{
		key: 'webhook-set-respond',
		name: 'Webhook → Set → Respond',
		kind: 'webhook',
		n8n: () =>
			n8nDoc(
				'Bench Webhook Set Respond',
				[
					n8nNode('hook', 'Webhook', 'n8n-nodes-base.webhook', 2, [0, 0], {
						httpMethod: 'POST',
						path: 'bench-hook',
						responseMode: 'responseNode',
					}),
					n8nNode('pin', 'Pin', 'n8n-nodes-base.set', 3, [240, 0], {
						assignments: { assignments: [{ id: 'a1', name: 'v', value: 'bench-ok', type: 'string' }] },
						options: {},
					}),
					n8nNode('resp', 'Respond', 'n8n-nodes-base.respondToWebhook', 1, [480, 0], {
						respondWith: 'json',
						responseBody: '={{ $json }}',
						options: {},
					}),
				],
				{ Webhook: { main: [[{ node: 'Pin', type: 'main', index: 0 }]] }, Pin: { main: [[{ node: 'Respond', type: 'main', index: 0 }]] } },
			),
		divergence: null,
	},
	{
		key: 'if-fanout',
		name: 'IF branch fan-out',
		kind: 'manual',
		kilas: () =>
			doc(
				'Bench IF Fan-out',
				[
					manual(),
					node('in', 'In', 'kilasflow.set', 1, { assignments: { v: 'x' } }),
					node('if', 'If', 'kilasflow.if', 1, { conditions: [{ field: 'v', operator: 'equals', value: 'x' }] }),
					node('hit', 'Hit', 'kilasflow.noOp'),
					node('miss', 'Miss', 'kilasflow.noOp'),
				],
				[
					conn('c1', 'manual', 'main', 'in', 'main'),
					conn('c2', 'in', 'main', 'if', 'main'),
					conn('c3', 'if', 'true', 'hit', 'main'),
					conn('c4', 'if', 'false', 'miss', 'main'),
				],
			),
		n8n: () =>
			n8nDoc(
				'Bench IF Fan-out',
				[
					n8nNode('hook', 'Webhook', 'n8n-nodes-base.webhook', 2, [0, 0], {
						httpMethod: 'POST',
						path: 'bench-if',
						responseMode: 'responseNode',
					}),
					n8nNode('set', 'Set', 'n8n-nodes-base.set', 3, [240, 0], {
						assignments: { assignments: [{ id: 'a1', name: 'v', value: 'x', type: 'string' }] },
						options: {},
					}),
					n8nNode('if', 'If', 'n8n-nodes-base.if', 2, [480, 0], {
						conditions: { options: { caseSensitive: true }, conditions: [{ id: 'c1', leftValue: '={{ $json.v }}', rightValue: 'x', operator: { typeVersion: 2, operation: 'equals' } }], combinator: 'and' },
						options: {},
					}),
					n8nNode('hit', 'Hit', 'n8n-nodes-base.noOp', 1, [720, -80], {}),
					n8nNode('miss', 'Miss', 'n8n-nodes-base.noOp', 1, [720, 80], {}),
					n8nNode('resp', 'Respond', 'n8n-nodes-base.respondToWebhook', 1, [960, 0], { respondWith: 'json', responseBody: '={{ $json }}', options: {} }),
				],
				{
					Webhook: { main: [[{ node: 'Set', type: 'main', index: 0 }]] },
					Set: { main: [[{ node: 'If', type: 'main', index: 0 }]] },
					If: { main: [[{ node: 'Hit', type: 'main', index: 0 }], [{ node: 'Miss', type: 'main', index: 0 }]] },
					Hit: { main: [[{ node: 'Respond', type: 'main', index: 0 }]] },
				},
			),
		divergence:
			'Trigger differs by side: KilasFlow times a manual run, n8n times a webhook delivery of the same downstream nodes. Delivery overhead is included on the n8n side only.',
	},
	{
		key: 'paginated-loop',
		name: 'Paginated Split-equivalent loop (100 items, batch 10)',
		kind: 'manual',
		kilas: () =>
			doc(
				'Bench Paginated Loop',
				[
					manual(),
					node('seed', 'Seed', 'kilasflow.code', 1, {
						code: 'out := make([]Item, 100); for i := range out { out[i] = Item{JSON: map[string]any{"n": i}} }; return out, nil',
					}),
					node('loop', 'Loop', 'kilasflow.loop', 1, { batchSize: 10 }),
					node('body', 'Body', 'kilasflow.set', 1, { assignments: { seen: true } }),
					node('done', 'Done', 'kilasflow.noOp'),
				],
				[
					conn('c1', 'manual', 'main', 'seed', 'main'),
					conn('c2', 'seed', 'main', 'loop', 'main'),
					conn('c3', 'loop', 'loop', 'body', 'main'),
					conn('c4', 'body', 'main', 'loop', 'main'),
					conn('c5', 'loop', 'done', 'done', 'main'),
				],
			),
		n8n: () =>
			n8nDoc(
				'Bench Paginated Loop',
				[
					n8nNode('hook', 'Webhook', 'n8n-nodes-base.webhook', 2, [0, 0], {
						httpMethod: 'POST',
						path: 'bench-loop',
						responseMode: 'responseNode',
					}),
					n8nNode('seed', 'Seed', 'n8n-nodes-base.code', 2, [240, 0], {
						mode: 'runOnceForAllItems',
						jsCode: 'return Array.from({length: 100}, (_, i) => ({json: {n: i}}));',
					}),
					n8nNode('loop', 'Loop', 'n8n-nodes-base.splitInBatches', 3, [480, 0], { batchSize: 10, options: {} }),
					n8nNode('body', 'Body', 'n8n-nodes-base.set', 3, [720, 0], {
						assignments: { assignments: [{ id: 'a1', name: 'seen', value: true, type: 'boolean' }] },
						options: {},
					}),
					n8nNode('done', 'Done', 'n8n-nodes-base.noOp', 1, [960, 0], {}),
					n8nNode('resp', 'Respond', 'n8n-nodes-base.respondToWebhook', 1, [1200, 0], { respondWith: 'json', responseBody: '={{ $json }}', options: {} }),
				],
				{
					Webhook: { main: [[{ node: 'Seed', type: 'main', index: 0 }]] },
					Seed: { main: [[{ node: 'Loop', type: 'main', index: 0 }]] },
					Loop: { main: [[{ node: 'Body', type: 'main', index: 0 }], [{ node: 'Done', type: 'main', index: 0 }]] },
					Body: { main: [[{ node: 'Loop', type: 'main', index: 0 }]] },
					Done: { main: [[{ node: 'Respond', type: 'main', index: 0 }]] },
				},
			),
		divergence:
			'Seed language differs (KilasFlow code node vs n8n JS code node) but both emit 100 {n} items; the timed region is the batch loop, not the seed. Trigger differs as in if-fanout.',
	},
	{
		key: 'http-merge-shape',
		name: 'HTTP → Merge → data-shaping',
		kind: 'manual',
		kilas: (bench) =>
			doc(
				'Bench HTTP Merge Shape',
				[
					manual(),
					node('users', 'Users', 'kilasflow.httpRequest', 1, { method: 'GET', url: `${bench.origin}/api/users` }),
					node('orders', 'Orders', 'kilasflow.httpRequest', 1, { method: 'GET', url: `${bench.origin}/api/orders` }),
					node('merge', 'Merge', 'kilasflow.merge', 1, { mode: 'append' }),
					node('shape', 'Shape', 'kilasflow.code', 1, { code: 'return items, nil' }),
					node('out', 'Out', 'kilasflow.set', 1, { assignments: { shaped: true } }),
				],
				[
					conn('c1', 'manual', 'main', 'users', 'main'),
					conn('c2', 'manual', 'main', 'orders', 'main'),
					conn('c3', 'users', 'main', 'merge', 'input1'),
					conn('c4', 'orders', 'main', 'merge', 'input2'),
					conn('c5', 'merge', 'main', 'shape', 'main'),
					conn('c6', 'shape', 'main', 'out', 'main'),
				],
			),
		n8n: (bench) =>
			n8nDoc(
				'Bench HTTP Merge Shape',
				[
					n8nNode('hook', 'Webhook', 'n8n-nodes-base.webhook', 2, [0, 0], {
						httpMethod: 'POST',
						path: 'bench-shape',
						responseMode: 'responseNode',
					}),
					n8nNode('users', 'Users', 'n8n-nodes-base.httpRequest', 4, [240, -80], {
						method: 'GET',
						url: `${bench.origin}/api/users`,
						options: {},
					}),
					n8nNode('orders', 'Orders', 'n8n-nodes-base.httpRequest', 4, [240, 80], {
						method: 'GET',
						url: `${bench.origin}/api/orders`,
						options: {},
					}),
					n8nNode('merge', 'Merge', 'n8n-nodes-base.merge', 3, [480, 0], { mode: 'append', options: {} }),
					n8nNode('shape', 'Shape', 'n8n-nodes-base.code', 2, [720, 0], {
						mode: 'runOnceForAllItems',
						jsCode: 'return $input.all();',
					}),
					n8nNode('resp', 'Respond', 'n8n-nodes-base.respondToWebhook', 1, [960, 0], { respondWith: 'json', responseBody: '={{ $json }}', options: {} }),
				],
				{
					Webhook: { main: [[{ node: 'Users', type: 'main', index: 0 }, { node: 'Orders', type: 'main', index: 0 }]] },
					Users: { main: [[{ node: 'Merge', type: 'main', index: 0 }]] },
					Orders: { main: [[{ node: 'Merge', type: 'main', index: 1 }]] },
					Merge: { main: [[{ node: 'Shape', type: 'main', index: 0 }]] },
					Shape: { main: [[{ node: 'Respond', type: 'main', index: 0 }]] },
				},
			),
		divergence:
			'Both engines call the same loopback stub, so stub latency cancels out; it is still reported separately. Trigger differs as in if-fanout.',
	},
	{
		key: 'agent-tool-loop',
		name: 'AI-agent tool-loop (local Ollama)',
		kind: 'manual',
		needsModel: true,
		kilas: (bench) => ({
			schemaVersion: 1,
			name: 'Bench Agent Tool Loop',
			nodes: [
				manual(),
				node('model', 'Chat Model', 'kilasflow.chatModel', 1, {
					model: process.env.KILASFLOW_TEST_OLLAMA_MODEL?.trim() || 'gemma4:12b-mlx',
					baseUrl: bench.modelBaseURL,
					temperature: 0,
					stream: true,
				}),
				node('memory', 'Memory', 'kilasflow.memoryBuffer', 1, {
					sessionIdType: 'customKey',
					sessionKey: { mode: 'expression', value: '{{ $json.sessionId }}' },
				}),
				node('weather', 'Weather', 'kilasflow.httpTool', 1, {
					toolName: 'get_weather',
					toolDescription: 'Look up the current temperature for a city. Takes a city name.',
					method: 'GET',
					url: {
						mode: 'expression',
						value: `${bench.origin}/tool/weather/{{ $fromAI('city', 'the city to look up') }}`,
					},
				}),
				node('agent', 'Agent', 'kilasflow.agent', 1, {
					prompt: { mode: 'expression', value: '{{ $json.question }}' },
					returnIntermediateSteps: true,
				}),
			],
			connections: [
				conn('c1', 'manual', 'main', 'agent', 'main'),
				conn('c2', 'model', 'model', 'agent', 'model', 'ai_languageModel'),
				conn('c3', 'memory', 'memory', 'agent', 'memory', 'ai_memory'),
				conn('c4', 'weather', 'tool', 'agent', 'tools', 'ai_tool'),
			],
			settings: {},
			// Filled by run.mjs after the bearer credential is minted.
			credential: { nodeId: 'model', key: 'httpBearerAuth' },
			input: { sessionId: 'bench', question: 'What is the weather in Utrecht? Use the get_weather tool to find out.' },
		}),
		n8n: () =>
			n8nDoc(
				'Bench Agent Tool Loop',
				[
					n8nNode('hook', 'Webhook', 'n8n-nodes-base.webhook', 2, [0, 0], {
						httpMethod: 'POST',
						path: 'bench-agent',
						responseMode: 'responseNode',
					}),
					// Operator wires their Ollama chat model + memory + HTTP tool
					// here; the reference run records which model served it.
					n8nNode('agent', 'Agent', '@n8n/n8n-nodes-langchain.agent', 2, [240, 0], {
						promptType: 'define',
						text: '=What is the weather in Utrecht? Use the get_weather tool to find out.',
						options: { returnIntermediateSteps: true },
					}),
				],
				{ Webhook: { main: [[{ node: 'Agent', type: 'main', index: 0 }]] } },
			),
		divergence:
			'Model inventory differs: KilasFlow runs the pinned local Ollama model through the bench gateway; the n8n side needs the same model wired by the operator, otherwise the agent rows are not comparable and are marked as such.',
	},
];
