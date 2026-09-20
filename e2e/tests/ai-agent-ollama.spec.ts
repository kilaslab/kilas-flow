import { test, expect } from '@playwright/test';

import { startServer } from '../helpers/server';
import { createCredential, createWorkflow, runWorkflow } from '../helpers/seed';
import { startAIGateway, type AIGateway } from '../fixtures/ai-gateway';
import { gate } from '../fixtures/gates';

// Serial, not parallel: the constrained resource here is one laptop-sized
// model serving every test in this file, and parallel turns make its timing
// — and its tool-calling consistency — nondeterministic. The Go Ollama suite
// bounds its concurrency for the same reason.
test.describe.configure({ mode: 'serial' });

// The AI agent path end to end against a local model.
//
// A green run proves the wiring — the cluster ports, the agent loop, the
// tool invocation, memory sessioning, the outbound allowance and the event
// stream — and nothing more. It is not evidence that agents behave well: every
// assertion below is on structure (a tool ran, an argument parsed, an
// execution completed, two sessions differ), never on the words the model
// chose, so a model update cannot turn this suite into a flaky sentence
// matcher.
//
// The model is gemma4:12b-mlx, pinned by owner instruction (see
// nodes/ai_ollama_test.go, which spends the same runtime one layer down).
// KILASFLOW_TEST_OLLAMA_BASE_URL overrides the endpoint and
// KILASFLOW_TEST_OLLAMA_MODEL overrides the tag; a run against any other
// model is a visible configuration change, not a silent one. A machine
// without Ollama or without the pinned model skips the model tests with the
// exact `ollama pull` command — the validation tests at the bottom still run,
// because they need no model at all.
//
// The model is reached through the instance's outbound policy, never around
// it: the gateway below is the one admitted private endpoint
// (allowed_private_endpoints), allow_private_networks stays off, and a second
// loopback server would still be refused. No Node.js process serves the
// model — /v1/* is forwarded byte-transparent to Ollama, streaming chunks
// included; Node only serves the deterministic tool endpoints so the agent's
// decision is the only variable.

const OLLAMA_BASE_URL = process.env.KILASFLOW_TEST_OLLAMA_BASE_URL?.trim() || 'http://127.0.0.1:11434/v1';
const OLLAMA_MODEL = process.env.KILASFLOW_TEST_OLLAMA_MODEL?.trim() || 'gemma4:12b-mlx';
const TERMINAL_STATUSES = ['succeeded', 'failed', 'cancelled'];

let gateway: AIGateway | undefined;
let skipReason = '';

test.beforeAll(async () => {
	test.info().setTimeout(600_000);
	const catalogue = await fetch(`${trimSlash(OLLAMA_BASE_URL)}/models`, { signal: AbortSignal.timeout(20_000) }).catch(
		(error) => {
			skipReason = gate(
				'model',
				`${OLLAMA_BASE_URL} is set but nothing answered there (${shortError(error)}). ` +
					`Start it with \`ollama serve\`, then \`ollama pull ${OLLAMA_MODEL}\`.`
			);
			return undefined;
		}
	);
	if (catalogue === undefined) return;
	if (!catalogue.ok) {
		skipReason = gate(
			'model',
			`${OLLAMA_BASE_URL} answered ${catalogue.status} for /v1/models ` +
				`(is \`ollama serve\` running? wants \`ollama pull ${OLLAMA_MODEL}\` next).`
		);
		return;
	}
	const payload = (await catalogue.json()) as { data?: Array<{ id?: string }> };
	const offered = (payload.data ?? []).map((entry) => entry.id).filter((id): id is string => typeof id === 'string');
	if (!offered.includes(OLLAMA_MODEL)) {
		skipReason = gate(
			'model',
			`${OLLAMA_BASE_URL} does not serve ${JSON.stringify(OLLAMA_MODEL)} — run \`ollama pull ${OLLAMA_MODEL}\`. ` +
				`The pinned tag is an Apple MLX build, so a non-Apple-Silicon machine has to set ` +
				`KILASFLOW_TEST_OLLAMA_MODEL to a portable tag. It offers: ${offered.join(', ') || '(nothing)'}.`
		);
		return;
	}
	gateway = await startAIGateway(trimSlash(OLLAMA_BASE_URL));
	// The first request after a model load is far slower than the ones after
	// it; pay that cost here, outside any assertion's timeout.
	const warmed = await fetch(`${trimSlash(OLLAMA_BASE_URL)}/chat/completions`, {
		method: 'POST',
		headers: { 'content-type': 'application/json' },
		body: JSON.stringify({
			model: OLLAMA_MODEL,
			temperature: 0,
			stream: false,
			max_tokens: 1,
			messages: [{ role: 'user', content: 'hi' }]
		}),
		signal: AbortSignal.timeout(300_000)
	}).catch((error) => {
		skipReason = gate(
			'model',
			`${OLLAMA_BASE_URL} would not answer a completion for ${JSON.stringify(OLLAMA_MODEL)}: ${shortError(error)}.`
		);
		return undefined;
	});
	if (warmed === undefined) {
		await gateway.close();
		gateway = undefined;
	}
});

test.afterAll(async () => {
	await gateway?.close();
	gateway = undefined;
});

function requireRuntime(): void {
	test.skip(skipReason !== '', skipReason || gate('model', 'the runtime was not probed'));
	if (!gateway) test.skip(true, skipReason || gate('model', 'the runtime is unreachable'));
}

function trimSlash(value: string): string {
	return value.replace(/\/+$/, '');
}

function shortError(error: unknown): string {
	return error instanceof Error ? error.message : String(error);
}

async function api(baseURL: string, method: string, path: string, body?: unknown): Promise<{ status: number; body: any }> {
	const response = await fetch(`${baseURL}/api/v1${path}`, {
		method,
		headers: { 'content-type': 'application/json' },
		body: body === undefined ? undefined : JSON.stringify(body)
	});
	return { status: response.status, body: await response.json() };
}

function errorText(body: any): string {
	const errors = (body?.errors ?? []) as Array<{ message?: string }>;
	return errors.map((entry) => entry.message ?? '').join('\n');
}

interface DocNode {
	id: string;
	name: string;
	type: string;
	typeVersion: number;
	position: { x: number; y: number };
	parameters?: Record<string, unknown>;
	credentials?: Record<string, string>;
}

function docNode(
	id: string,
	name: string,
	type: string,
	parameters?: Record<string, unknown>,
	credentials?: Record<string, string>
): DocNode {
	const entry: DocNode = { id, name, type, typeVersion: 1, position: { x: 0, y: 0 } };
	if (parameters !== undefined) entry.parameters = parameters;
	if (credentials !== undefined) entry.credentials = credentials;
	return entry;
}

interface DocConnection {
	id: string;
	kind: string;
	source: { nodeId: string; port: string };
	target: { nodeId: string; port: string };
}

function docConn(
	id: string,
	source: string,
	sourcePort: string,
	target: string,
	targetPort: string,
	kind = 'main'
): DocConnection {
	return { id, kind, source: { nodeId: source, port: sourcePort }, target: { nodeId: target, port: targetPort } };
}

// Boots a fresh instance per test — own port, own database — reusing the
// harness boot (real binary, real SPA, real database). The gateway is the one
// admitted private endpoint; instances used by tests that need no model boot
// with no private allowance at all.
async function withInstance<T>(fn: (baseURL: string) => Promise<T>): Promise<T> {
	const server = await startServer(gateway ? { stubEndpoint: gateway.endpoint } : {});
	try {
		return await fn(server.baseURL);
	} finally {
		await server.close();
	}
}

export interface ChannelEvent {
	type: string;
	nodeId: string;
	data: string;
}

// Reads the whole execution channel, including the agent's nested ai.*
// events. Those arrive as data-only SSE blocks — no `event:` line, with the
// type inside the JSON payload — so the shared readExecutionEvents helper,
// which only keeps named blocks, never sees them. Same replay semantics
// otherwise: retained history first, return at the terminal event.
async function readFullChannel(baseURL: string, executionId: string): Promise<ChannelEvent[]> {
	const response = await fetch(`${baseURL}/api/v1/executions/${executionId}/events`, {
		headers: { accept: 'text/event-stream' }
	});
	if (!response.ok || !response.body) {
		throw new Error(`GET /executions/${executionId}/events: status ${response.status}`);
	}
	const found: ChannelEvent[] = [];
	const reader = response.body.getReader();
	const decoder = new TextDecoder();
	let buffer = '';
	const deadline = Date.now() + 30_000;
	try {
		for (;;) {
			if (Date.now() > deadline) throw new Error(`SSE for execution ${executionId} produced no terminal event in time`);
			const { done, value } = await reader.read();
			if (value) buffer += decoder.decode(value, { stream: true });
			let boundary = buffer.indexOf('\n\n');
			while (boundary !== -1) {
				const block = buffer.slice(0, boundary);
				buffer = buffer.slice(boundary + 2);
				let type = '';
				let nodeId = '';
				const data: string[] = [];
				for (const line of block.split('\n')) {
					if (line.startsWith('event:')) type = line.slice(6).trim();
					else if (line.startsWith('data:')) data.push(line.slice(5).trim());
				}
				const payload = data.join('\n');
				if (!type && payload.startsWith('{')) {
					try {
						const parsed = JSON.parse(payload) as { type?: string; nodeId?: string };
						if (typeof parsed.type === 'string') type = parsed.type;
						if (typeof parsed.nodeId === 'string') nodeId = parsed.nodeId;
					} catch {
						// Not JSON — nothing to classify, same as an unnamed
						// comment block the shared reader would also drop.
					}
				}
				if (type) {
					found.push({ type, nodeId, data: payload });
					if (type === 'execution.completed' || type === 'execution.failed' || type === 'execution.cancelled') {
						return found;
					}
				}
				boundary = buffer.indexOf('\n\n');
			}
			if (done) throw new Error(`SSE for execution ${executionId} ended without a terminal event`);
		}
	} finally {
		reader.releaseLock();
		await response.body.cancel().catch(() => undefined);
	}
}

// The harness poll caps at a minute; a cold model turn can exceed it, so AI
// runs poll longer. Observable terminal status only — nothing here sleeps for
// a fixed time.
async function waitForTerminal(baseURL: string, executionId: string, timeoutMs = 300_000): Promise<any> {
	const started = Date.now();
	for (;;) {
		const record = await api(baseURL, 'GET', `/executions/${executionId}`);
		if (TERMINAL_STATUSES.includes(record.body.status)) return record.body;
		if (Date.now() - started > timeoutMs) {
			throw new Error(`execution ${executionId} reached no terminal status within ${timeoutMs}ms`);
		}
		await new Promise((resolve) => setTimeout(resolve, 2_000));
	}
}

function nodeRun(record: any, nodeId: string): any {
	const run = (record.nodeRuns as any[]).find((entry) => entry.nodeId === nodeId);
	expect(run, `node ${nodeId} has a recorded run`).toBeDefined();
	return run;
}

function runOutputJson(record: any, nodeId: string): any {
	const run = nodeRun(record, nodeId);
	expect(run.status).toBe('succeeded');
	return run.output[0][0].json;
}

// A bearer credential for the local model server. The engine accepts an empty
// token (meaning no Authorization header), but the credentials API requires a
// non-empty value — so this holds a dummy that Ollama ignores (verified: a
// request carrying `Authorization: Bearer local` is answered normally). No
// third-party key anywhere in the environment either way.
async function localBearerCredential(baseURL: string): Promise<string> {
	const created = await createCredential(baseURL, {
		name: 'Local Ollama',
		type: 'httpBearerAuth',
		fields: { token: 'local' }
	});
	return created.id;
}

interface AgentGraph {
	workflowId: string;
}
async function agentWorkflow(baseURL: string, name: string, toolPath: string): Promise<AgentGraph> {
	if (!gateway) throw new Error('agentWorkflow needs the gateway');
	const bearerId = await localBearerCredential(baseURL);
	// Saved documents carry expressions as {mode, value} objects: a bare
	// "{{ }}" string in a saved parameter is literal text. The tool URL keeps
	// its $fromAI call so the model-facing schema carries a typed city
	// property, substituted into the path at invoke time.
	const toolUrl = (path: string) => ({
		mode: 'expression',
		value: `${gateway!.origin}${path}{{ $fromAI('city', 'the city to look up') }}`
	});
	const workflowId = (
		await createWorkflow(baseURL, {
			schemaVersion: 1,
			name,
			nodes: [
				docNode('manual', 'Manual Trigger', 'kilasflow.manual'),
				docNode(
					'model',
					'Chat Model',
					'kilasflow.chatModel',
					// Stream pinned on, not inherited from the node default:
					// the declared default applies in the editor, while an
					// execution without the key runs non-streaming and emits
					// no ai.model.delta events at all.
					{ model: OLLAMA_MODEL, baseUrl: gateway.modelBaseURL, temperature: 0, stream: true },
					{ httpBearerAuth: bearerId }
				),
				docNode('memory', 'Memory', 'kilasflow.memoryBuffer', {
					sessionIdType: 'customKey',
					sessionKey: { mode: 'expression', value: '{{ $json.sessionId }}' }
				}),
				docNode('weather', 'Weather', 'kilasflow.httpTool', {
					toolName: 'get_weather',
					toolDescription: 'Look up the current temperature for a city. Takes a city name.',
					method: 'GET',
					url: toolUrl(toolPath)
				}),
				docNode('agent', 'Agent', 'kilasflow.agent', {
					prompt: { mode: 'expression', value: '{{ $json.question }}' },
					returnIntermediateSteps: true
				})
			],
			connections: [
				docConn('c1', 'manual', 'main', 'agent', 'main'),
				docConn('c2', 'model', 'model', 'agent', 'model', 'ai_languageModel'),
				docConn('c3', 'memory', 'memory', 'agent', 'memory', 'ai_memory'),
				docConn('c4', 'weather', 'tool', 'agent', 'tools', 'ai_tool')
			],
			settings: {}
		})
	).id;
	return { workflowId };
}

const WEATHER_QUESTION = (city: string) => `What is the weather in ${city}? Use the get_weather tool to find out.`;

test('an agent calls a tool the local model chose, and streams the reply', async () => {
	requireRuntime();
	test.slow();
	await withInstance(async (baseURL) => {
		const { workflowId } = await agentWorkflow(baseURL, 'E2E Agent Weather', '/weather/');
		gateway!.reset();

		const executionId = await runWorkflow(baseURL, workflowId, {
			sessionId: 'harbor',
			question: WEATHER_QUESTION('Utrecht')
		});
		const record = await waitForTerminal(baseURL, executionId);
		expect(record.status).toBe('succeeded');

		// The decision, structurally: the agent ran the tool, with an argument
		// of the expected shape — the city substituted into the tool URL —
		// and the tool's result reached the agent.
		const agentOutput = runOutputJson(record, 'agent');
		expect(agentOutput.toolCalls).toBeGreaterThanOrEqual(1);
		expect(typeof agentOutput.output === 'string' && agentOutput.output.trim().length > 0).toBe(true);
		expect(gateway!.hits.map((hit) => hit.path)).toContain('/weather/Utrecht');

		// The same loop observed live: the agent's nested ai.* events join the
		// one standardized execution event channel rather than a parallel one,
		// as data-only blocks whose type rides inside the payload.
		const events = await readFullChannel(baseURL, executionId);
		const types = events.map((event) => event.type);
		expect(types).toContain('ai.tool.started');
		expect(types).toContain('ai.tool.completed');
		expect(types).toContain('execution.completed');
		const toolStarted = events.find((event) => event.type === 'ai.tool.started');
		expect(toolStarted?.data).toContain('get_weather');

		// Streaming arrives as events during the run, not only in the final
		// record: the model streams (pinned on above) and the agent forwards
		// every chunk as ai.model.delta.
		expect(types).toContain('ai.model.delta');
	});
});

test('memory persists within one session across executions and isolates another', async () => {
	requireRuntime();
	test.slow();
	await withInstance(async (baseURL) => {
		const { workflowId } = await agentWorkflow(baseURL, 'E2E Agent Memory', '/weather/');

		gateway!.reset();
		const firstId = await runWorkflow(baseURL, workflowId, {
			sessionId: 'harbor',
			question: WEATHER_QUESTION('Utrecht')
		});
		const first = await waitForTerminal(baseURL, firstId);
		expect(first.status).toBe('succeeded');
		const firstSteps = runOutputJson(first, 'agent').intermediateSteps as unknown[];
		expect(Array.isArray(firstSteps) && firstSteps.length > 0).toBe(true);

		// Same session, second execution: the earlier turn is still in the
		// window, so the ordered message list grows instead of restarting.
		gateway!.reset();
		const secondId = await runWorkflow(baseURL, workflowId, {
			sessionId: 'harbor',
			question: WEATHER_QUESTION('Bandung')
		});
		const second = await waitForTerminal(baseURL, secondId);
		expect(second.status).toBe('succeeded');
		const secondSteps = runOutputJson(second, 'agent').intermediateSteps as unknown[];
		expect(secondSteps.length).toBeGreaterThan(firstSteps.length);
		expect(JSON.stringify(secondSteps)).toContain('Utrecht');
		expect(gateway!.hits.map((hit) => hit.path)).toContain('/weather/Bandung');

		// A different session key starts a separate history: nothing from the
		// harbor conversation — whose prompts named Utrecht — is visible here.
		gateway!.reset();
		const thirdId = await runWorkflow(baseURL, workflowId, {
			sessionId: 'inland',
			question: WEATHER_QUESTION('Bandung')
		});
		const third = await waitForTerminal(baseURL, thirdId);
		expect(third.status).toBe('succeeded');
		const thirdSteps = runOutputJson(third, 'agent').intermediateSteps as unknown[];
		expect(JSON.stringify(thirdSteps)).not.toContain('Utrecht');
		expect(gateway!.hits.map((hit) => hit.path)).toContain('/weather/Bandung');
	});
});

test('a chain makes one model call per incoming item', async () => {
	requireRuntime();
	test.slow();
	await withInstance(async (baseURL) => {
		if (!gateway) throw new Error('chain test needs the gateway');
		const bearerId = await localBearerCredential(baseURL);
		const workflowId = (
			await createWorkflow(baseURL, {
				schemaVersion: 1,
				name: 'E2E Chain Fanout',
				nodes: [
					docNode('manual', 'Manual Trigger', 'kilasflow.manual'),
					docNode('split', 'Split', 'kilasflow.splitOut', { fieldToSplitOut: 'lines' }),
					docNode(
						'model',
						'Chat Model',
						'kilasflow.chatModel',
						{ model: OLLAMA_MODEL, baseUrl: gateway.modelBaseURL, temperature: 0 },
						{ httpBearerAuth: bearerId }
					),
					docNode('chain', 'Chain', 'kilasflow.chainLlm', {
						promptType: 'define',
						text: 'Reply with the single word done.'
					})
				],
				connections: [
					docConn('c1', 'manual', 'main', 'split', 'main'),
					docConn('c2', 'split', 'main', 'chain', 'main'),
					docConn('c3', 'model', 'model', 'chain', 'model', 'ai_languageModel')
				],
				settings: {}
			})
		).id;

		const executionId = await runWorkflow(baseURL, workflowId, { lines: ['first', 'second', 'third'] });
		const record = await waitForTerminal(baseURL, executionId);
		expect(record.status).toBe('succeeded');

		// One output per item, each a real reply rather than an empty echo.
		const run = nodeRun(record, 'chain');
		expect(run.output[0].length).toBe(3);
		for (const item of run.output[0]) {
			expect(typeof item.json.output === 'string' && item.json.output.trim().length > 0).toBe(true);
		}

		// And one model turn per item on the event channel — a chain must not
		// loop, batch, or collapse its items into a single call.
		const events = await readFullChannel(baseURL, executionId);
		const chainTurns = events.filter((event) => event.type === 'ai.model.started' && event.nodeId === 'chain').length;
		expect(chainTurns).toBeGreaterThanOrEqual(3);
	});
});

test('a failing tool leaves a terminal execution with a tool-failed diagnostic', async () => {
	requireRuntime();
	test.slow();
	await withInstance(async (baseURL) => {
		const { workflowId } = await agentWorkflow(baseURL, 'E2E Agent Tool Failure', '/boom/');
		gateway!.reset();

		const executionId = await runWorkflow(baseURL, workflowId, {
			sessionId: 'harbor',
			question: WEATHER_QUESTION('Utrecht')
		});
		const record = await waitForTerminal(baseURL, executionId);
		expect(TERMINAL_STATUSES).toContain(record.status);

		// Defined state, not a hang or a partial record: the failed tool turn
		// is diagnosed on the event channel under the tool's own name.
		const events = await readFullChannel(baseURL, executionId);
		const types = events.map((event) => event.type);
		expect(types).toContain('ai.tool.failed');
		const toolFailed = events.find((event) => event.type === 'ai.tool.failed');
		expect(toolFailed?.data).toContain('get_weather');
		expect(gateway!.hits.map((hit) => hit.path)).toContain('/boom/Utrecht');
	});
});

test('a sub-node wired to the wrong port kind is refused at run time', async () => {
	// No model needed: the compiler refuses the graph before anything runs.
	await withInstance(async (baseURL) => {
		const workflowId = (
			await createWorkflow(baseURL, {
				schemaVersion: 1,
				name: 'E2E Agent Wrong Port',
				nodes: [
					docNode('manual', 'Manual Trigger', 'kilasflow.manual'),
					docNode('memory', 'Memory', 'kilasflow.memoryBuffer', {
						sessionIdType: 'customKey',
						sessionKey: 'fixed'
					}),
					docNode('agent', 'Agent', 'kilasflow.agent', { prompt: 'hi' })
				],
				connections: [
					docConn('c1', 'manual', 'main', 'agent', 'main'),
					// The memory port speaks ai_memory; the tools port listens
					// for ai_tool. The kind matches neither endpoint contract.
					docConn('c2', 'memory', 'memory', 'agent', 'tools', 'ai_memory')
				],
				settings: {}
			})
		).id;
		// Drafts save — incompleteness is legal in a draft — and the run
		// refuses with the message naming the problem, which is the seam the
		// coverage suite already establishes for every other cluster refusal.
		const attempt = await api(baseURL, 'POST', `/workflows/${workflowId}/run`, {});
		expect(attempt.status).toBe(422);
		expect(errorText(attempt.body)).toContain('connection kind must match both endpoint ports');
	});
});

test('an agent with no language model names the missing port', async () => {
	await withInstance(async (baseURL) => {
		const workflowId = (
			await createWorkflow(baseURL, {
				schemaVersion: 1,
				name: 'E2E Agent Bare',
				nodes: [
					docNode('manual', 'Manual Trigger', 'kilasflow.manual'),
					docNode('agent', 'Agent', 'kilasflow.agent', { prompt: 'hi' })
				],
				connections: [docConn('c1', 'manual', 'main', 'agent', 'main')],
				settings: {}
			})
		).id;
		const attempt = await api(baseURL, 'POST', `/workflows/${workflowId}/run`, {});
		expect(attempt.status).toBe(422);
		expect(errorText(attempt.body)).toContain('requires a connection on port "Chat Model"');
	});
});
