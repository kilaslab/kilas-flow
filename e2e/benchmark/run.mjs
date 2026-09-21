#!/usr/bin/env node
/**
 * Reproducible KilasFlow-vs-n8n benchmark for FEAT-8mymac, method version 2.
 *
 * Usage:
 *   make bench-compare                                  (N=30 per engine per workflow)
 *   BENCH_SMOKE=1 BENCH_N8N=off BENCH_SKIP_AGENT=1 node e2e/benchmark/run.mjs
 *   BENCH_SMOKE=1 node e2e/benchmark/run.mjs            (managed throwaway n8n)
 *   BENCH_SMOKE=1 BENCH_CONTROL=aa node e2e/benchmark/run.mjs   (A/A negative control)
 *
 * The A/A control is measured in its own short run and attached to the
 * published comparison by pointing both runs at the same
 * BENCH_CONTROL_FILE: the control run writes its block there, the main run
 * reads it, so the raw file that carries the numbers also carries the
 * harness's own negative control.
 *
 * What it does:
 *   1. Boots one loopback bench server (deterministic stub, deterministic tool,
 *      byte-transparent proxy to the local Ollama) and one real kilasflow
 *      binary against a fresh temp database.
 *   2. Starts the managed throwaway n8n container (pinned image, 127.0.0.1
 *      only, labelled, no volume) and signs in, or takes an operator's own
 *      instance through BENCH_N8N=external.
 *   3. Runs every row in e2e/benchmark/workflows.mjs through the ABBA schedule
 *      of method.mjs on BOTH engines: warm-ups discarded, then `runs` timed
 *      deliveries per engine per workflow, settling after every run so a run's
 *      post-response tail never overlaps the next timed request.
 *   4. Times every delivery with the one shared function (deliverTimed) and
 *      asserts full-body equivalence against the fixture's expectation. On the
 *      shared rows it additionally proves the two engines' bodies are equal by
 *      construction — one probe delivery per engine per row, compared before
 *      any timed run.
 *   5. Writes bench-<ts>.json (raw, per-run) and SUMMARY.md.
 *
 * It does NOT gate merges and does NOT run on every PR. A smoke run
 * (BENCH_SMOKE=1) writes only under the OS temp dir.
 *
 * Secrets: the throwaway owner password is generated in memory and lives only
 * in the process, never in a file, a ticket, a log or an argv. The container is
 * removed on normal exit and on SIGINT/SIGTERM.
 */

import { spawn } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, join, basename } from 'node:path';
import { fileURLToPath } from 'node:url';

import {
	api,
	benchPlan,
	classifyHit,
	environment,
	executionRecords,
	hitsSince,
	loadAverage,
	probeHealth,
	probeStubLatency,
	sampleRssKib,
	startBenchServer,
	startKilasFlow,
	stats,
} from './lib.mjs';
import { BENCHMARKS, payloadFor, kilasSourceFor } from './workflows.mjs';
import {
	BOOTSTRAP,
	BOUNDS,
	RATIO_BAND,
	assertEquivalent,
	assertNoSecrets,
	bootstrapRatio,
	buildSchedule,
	canonicalize,
	compareLabel,
	controlFromFile,
	deliverTimed,
	gatewayOverhead,
	pairRecords,
	verdict,
} from './method.mjs';
import { N8nClient, createN8nAdapter, externalN8nConfig } from './n8n.mjs';
import { containerHwmKib, containerRssKib, probeStubLatencyInContainer, startN8nContainer } from './n8n-container.mjs';
import { secretsFromEnv, summarize, writeSummary } from './summarise.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));
const OLLAMA_BASE_URL = (process.env.KILASFLOW_TEST_OLLAMA_BASE_URL?.trim() || 'http://127.0.0.1:11434/v1').replace(/\/+$/, '');
const OLLAMA_MODEL = process.env.KILASFLOW_TEST_OLLAMA_MODEL?.trim() || 'gemma4:12b-mlx';
const TERMINAL = new Set(['succeeded', 'failed', 'cancelled']);

/** Set by the signal handler so an in-flight delivery failure cannot race it. */
let shuttingDown = false;

/** The two shared-row engines must return the same body, byte for byte. */
const CROSS_ENGINE_CHECKED = (bench) => bench.source === 'shared-n8n-json';

/**
 * The only import losses a shared row is allowed to report.
 *
 * n8n needs both fields and KilasFlow has no concept of either: `webhookId` is
 * n8n's own route identity (KilasFlow mints an opaque route instead) and
 * `settings.executionOrder` is n8n's scheduler choice (KilasFlow has its own).
 * Anything else lost on import means the two engines are not running the same
 * graph, and the run fails instead of publishing a comparison.
 */
const KNOWN_IMPORT_LOSSES = ['settings', 'webhookId'];

/** The claim this stage has to prove: the 25 ms poll cadence is gone. */
const WEBHOOK_P50_CEILING_MS = 25;

const EXCLUSIONS = [
	{ what: 'Third-party-credentialed nodes', why: 'an engine cannot be timed on a node whose upstream service this machine has no credential for' },
	{ what: 'Queue mode and Postgres mode', why: 'both engines run in their default single-process configuration; SQLite for KilasFlow' },
	{ what: 'Throughput and concurrency', why: 'every run is settled before the next, so this measures latency, never requests per second' },
	{ what: 'Cold start', why: 'warm-ups are discarded by design' },
	{ what: 'Multi-tenant overhead', why: 'one tenant, one workflow at a time' },
	{ what: 'Editor latency', why: 'browser-only micro-interactions are a different measurement' },
];

function parseBody(text) {
	if (!text || text.trim() === '') return [];
	try {
		return JSON.parse(text);
	} catch {
		throw new Error(`the delivery body is not JSON: ${text.slice(0, 200)}`);
	}
}

function countHits(window) {
	const counts = { stub: 0, tool: 0, model: 0, other: 0, modelMs: 0, toolMs: 0 };
	for (const hit of window) {
		const kind = classifyHit(hit.path);
		counts[kind] += 1;
		if (kind === 'model') counts.modelMs += hit.durMs;
		if (kind === 'tool') counts.toolMs += hit.durMs;
	}
	counts.modelMs = Math.round(counts.modelMs * 100) / 100;
	counts.toolMs = Math.round(counts.toolMs * 100) / 100;
	return counts;
}

async function ollamaModelPresent() {
	try {
		const res = await fetch(`${OLLAMA_BASE_URL.replace(/\/v1$/, '')}/api/tags`, { signal: AbortSignal.timeout(20_000) });
		if (!res.ok) return { ok: false, reason: `Ollama answered ${res.status} for /api/tags (is \`ollama serve\` running?)` };
		const payload = await res.json();
		const offered = (payload.models ?? []).map((entry) => entry.name ?? entry.model).filter(Boolean);
		if (!offered.includes(OLLAMA_MODEL)) {
			return { ok: false, reason: `Ollama does not serve ${JSON.stringify(OLLAMA_MODEL)} — run \`ollama pull ${OLLAMA_MODEL}\`. It offers: ${offered.join(', ') || '(nothing)'}.` };
		}
		return { ok: true };
	} catch (error) {
		return { ok: false, reason: `Ollama is unreachable (${error instanceof Error ? error.message : String(error)}). Start it with \`ollama serve\`.` };
	}
}

/** Poll until the workflow has one terminal execution per delivery. Untimed. */
async function settleKilas(baseURL, workflowId, expected, timeoutMs = 180_000) {
	const deadline = Date.now() + timeoutMs;
	for (;;) {
		const records = await executionRecords(baseURL, workflowId);
		if (records.length >= expected) {
			const latest = records.reduce((left, right) => (Date.parse(left.startedAt ?? 0) >= Date.parse(right.startedAt ?? 0) ? left : right));
			if (TERMINAL.has(latest.status)) return;
		}
		if (Date.now() > deadline) {
			throw new Error(`settle: workflow ${workflowId} reached ${records.length}/${expected} executions in ${timeoutMs}ms`);
		}
		await new Promise((resolve) => setTimeout(resolve, 25));
	}
}

/**
 * The KilasFlow engine adapter.
 *
 * prepare() imports the shared n8n JSON for rows 1–4 (so the graph is
 * identical by construction) or creates a native document for the two rows
 * that cannot be imported, then activates and resolves the minted webhook
 * route. Two instances of this adapter on one server give the A/A control.
 */
function kilasAdapter({ name = 'kilasflow', baseURL, pid, view }) {
	return {
		name,
		view,
		async prepare(bench, context) {
			let workflowId = null;
			let routeUrl = null;
			let importerFindings = [];
			if (bench.source === 'shared-n8n-json') {
				const imported = await api(
					baseURL,
					'POST',
					'/workflows/import',
					{ format: 'n8n', name: `Bench ${bench.key}`, workflow: kilasSourceFor(bench, context) },
					201,
				);
				const issues = imported.unsupported ?? [];
				const blocking = issues.filter((issue) => issue.severity === 'blocking');
				if (blocking.length > 0) throw new Error(`${bench.key}: import blocked: ${JSON.stringify(blocking).slice(0, 400)}`);
				const unexpected = issues.filter((issue) => !KNOWN_IMPORT_LOSSES.includes(issue.field));
				if (unexpected.length > 0) {
					throw new Error(`${bench.key}: import lost more than the two known n8n-only fields: ${JSON.stringify(unexpected).slice(0, 400)}`);
				}
				importerFindings = issues.map((issue) => ({ severity: issue.severity, field: issue.field, nodeName: issue.nodeName, reason: issue.reason }));
				workflowId = imported.workflow.id;
				routeUrl = imported.webhooks?.[0]?.url ?? null;
			} else {
				const document = structuredClone(kilasSourceFor(bench, context));
				const marker = document.credential;
				delete document.credential;
				if (marker) {
					const credential = await api(baseURL, 'POST', '/credentials', { name: `Bench ${bench.key}`, type: marker.kilasKey, fields: { token: 'local' } }, 201);
					const node = document.nodes.find((entry) => entry.id === marker.nodeId);
					if (!node) throw new Error(`${bench.key}: credential marker names a node that is not in the document`);
					node.credentials = { [marker.kilasKey]: credential.id };
				}
				const created = await api(baseURL, 'POST', '/workflows', document, 201);
				workflowId = created.id;
			}
			const activated = await api(baseURL, 'POST', `/workflows/${workflowId}/activate`, {}, 200);
			if (activated.active !== true) throw new Error(`${bench.key}: activation did not stick`);
			if (!routeUrl) {
				const routes = await api(baseURL, 'GET', `/workflows/${workflowId}/webhooks`);
				routeUrl = routes?.[0]?.url ?? null;
			}
			if (!routeUrl) throw new Error(`${bench.key}: no webhook route was minted`);
			return { workflowId, url: `${baseURL}${routeUrl}`, importerFindings };
		},
		deliver(prepared, bench, index, context) {
			return deliverTimed(prepared.url, payloadFor(bench, index, context));
		},
		settle(prepared, expected) {
			return settleKilas(baseURL, prepared.workflowId, expected);
		},
		records(prepared) {
			return executionRecords(baseURL, prepared.workflowId);
		},
		rss() {
			return sampleRssKib(pid);
		},
		async teardown(prepared) {
			await api(baseURL, 'POST', `/workflows/${prepared.workflowId}/deactivate`, {}, 200).catch(() => undefined);
			await api(baseURL, 'DELETE', `/workflows/${prepared.workflowId}`, undefined, 204).catch(() => undefined);
		},
	};
}

/** The per-engine view of the shared context (the stub is reached at two addresses). */
function contextFor(adapter, context) {
	return { ...context, ...(adapter.view ?? {}) };
}

/** One delivery: time it, attribute the gateway hits, assert the body. */
async function deliverOnce({ adapter, prepared, bench, index, context }) {
	const hitsMark = context.benchServer.hits.length;
	const result = await adapter.deliver(prepared, bench, index, context);
	const window = hitsSince(context.benchServer.hits, hitsMark);
	const counts = countHits(window);
	if (result.status !== 200) {
		throw new Error(`${bench.key}/${adapter.name}#${index}: delivery status ${result.status} (body: ${result.text.slice(0, 200)})`);
	}
	const body = parseBody(result.text);
	assertEquivalent(body, bench.expect, `${bench.key}/${adapter.name}#${index}`);
	if (counts.stub !== bench.expect.stubCalls) {
		throw new Error(`${bench.key}/${adapter.name}#${index}: ${counts.stub} stub calls, expected ${bench.expect.stubCalls}`);
	}
	if (bench.expect.toolCallsMin !== undefined && counts.tool < bench.expect.toolCallsMin) {
		throw new Error(`${bench.key}/${adapter.name}#${index}: ${counts.tool} tool calls, expected at least ${bench.expect.toolCallsMin}`);
	}
	if (bench.expect.modelCallsMin !== undefined && counts.model < bench.expect.modelCallsMin) {
		throw new Error(`${bench.key}/${adapter.name}#${index}: ${counts.model} model calls, expected at least ${bench.expect.modelCallsMin}`);
	}
	return { responseMs: result.responseMs, counts, window, body };
}

/**
 * Run one row on every adapter through the ABBA schedule.
 *
 * Warm-ups alternate between engines and are discarded. Before them, one
 * untimed probe delivery per engine validates the hand-authored fixtures and —
 * on the shared rows — proves the two engines returned the same body. settle()
 * runs after EVERY delivery, timed runs included: otherwise engine A's
 * post-response persistence overlaps run i+1 and the row measures throughput,
 * which is out of scope. RSS is sampled after every settled run.
 */
async function runRow({ bench, adapters, plan, context, workflowIndex }) {
	const prepared = {};
	for (const adapter of adapters) prepared[adapter.name] = await adapter.prepare(bench, contextFor(adapter, context));
	const attempts = new Map(adapters.map((adapter) => [adapter.name, []]));
	const peakRssKib = new Map(adapters.map((adapter) => [adapter.name, 0]));
	const loadAverageAfter = new Map();
	const nextAttempt = new Map(adapters.map((adapter) => [adapter.name, 1]));

	const deliver = async (adapter, index, { warmup, block = null, agentRetry = false, probe = false }) => {
		const attempt = nextAttempt.get(adapter.name);
		nextAttempt.set(adapter.name, attempt + 1);
		const entry = { attempt, warmup, probe, block, index, agentRetry, engine: adapter.name };
		attempts.get(adapter.name).push(entry);
		const { responseMs, counts, window, body } = await deliverOnce({
			adapter,
			prepared: prepared[adapter.name],
			bench,
			index,
			context: contextFor(adapter, context),
		});
		Object.assign(entry, { responseMs, ...counts, window, body });
		await adapter.settle(prepared[adapter.name], attempts.get(adapter.name).length);
		const rss = await adapter.rss();
		if (rss !== null && rss > peakRssKib.get(adapter.name)) peakRssKib.set(adapter.name, rss);
		return entry;
	};

	try {
		// Fixture validation: one delivery per engine before anything is timed.
		// The hand-authored n8n JSON was never executed before stage 2, so this
		// is where a wrong parameter shape shows up — and where the shared rows
		// prove the two engines return the same body, not merely the same count.
		const probes = new Map();
		for (const adapter of adapters) probes.set(adapter.name, await deliver(adapter, -1000, { warmup: false, probe: true }));
		if (adapters.length === 2 && CROSS_ENGINE_CHECKED(bench)) {
			const left = JSON.stringify(canonicalize(probes.get(adapters[0].name).body));
			const right = JSON.stringify(canonicalize(probes.get(adapters[1].name).body));
			if (left !== right) {
				throw new Error(`${bench.key}: the two engines returned different bodies across the probe\n  ${adapters[0].name} ${left.slice(0, 400)}\n  ${adapters[1].name} ${right.slice(0, 400)}`);
			}
		}

		for (let i = 0; i < plan.warmup; i++) {
			const adapter = adapters[i % adapters.length];
			await deliver(adapter, -1 - i, { warmup: true });
		}
		const schedule = buildSchedule({ runs: plan.runs, block: plan.block, startEngine: workflowIndex % 2, engines: adapters.map((adapter) => adapter.name) });
		for (const slot of schedule) {
			const adapter = adapters.find((candidate) => candidate.name === slot.engine);
			const maxAttempts = bench.needsModel ? 3 : 1;
			for (let tries = 1; ; tries++) {
				try {
					await deliver(adapter, slot.index, { warmup: false, block: slot.block });
					break;
				} catch (error) {
					// The agent row is model-bound and a transient inference
					// failure is retried, with the failed attempt recorded so a
					// second operator sees the flakiness instead of a clean number.
					if (tries >= maxAttempts) throw error;
					const failed = attempts.get(adapter.name)[attempts.get(adapter.name).length - 1];
					failed.agentRetry = true;
					console.error(`bench: ${bench.key}/${adapter.name}: retrying after ${error instanceof Error ? error.message : String(error)}`);
				}
			}
		}
		loadAverageAfter.set(bench.key, loadAverage());

		const perEngine = {};
		for (const adapter of adapters) {
			const records = await adapter.records(prepared[adapter.name]);
			const paired = pairRecords(attempts.get(adapter.name), records);
			const byAttempt = new Map(paired.map((entry) => [entry.attempt, entry]));
			const rows = attempts
				.get(adapter.name)
				.filter((entry) => !entry.warmup && !entry.probe && entry.responseMs !== undefined && !entry.agentRetry)
				.map((entry) => {
					const gateway = gatewayOverhead(entry.responseMs, entry.window);
					return {
						engine: adapter.name,
						block: entry.block,
						index: entry.index,
						responseMs: round2(entry.responseMs),
						serverMs: byAttempt.get(entry.attempt)?.serverMs ?? null,
						stubCalls: entry.stub,
						toolCalls: entry.tool,
						modelCalls: entry.model,
						modelMs: entry.modelMs,
						toolMs: entry.toolMs,
						gatewayOverheadMs: bench.needsModel ? round2(gateway) : null,
						attempt: entry.attempt,
					};
				});
			perEngine[adapter.name] = {
				rows,
				prepared: prepared[adapter.name],
				peakRssKib: peakRssKib.get(adapter.name),
				retriedRuns: attempts.get(adapter.name).filter((entry) => entry.agentRetry).length,
				attemptCount: attempts.get(adapter.name).length,
			};
		}
		return { perEngine, loadAverageAfter: loadAverageAfter.get(bench.key) };
	} finally {
		for (const adapter of adapters) await adapter.teardown(prepared[adapter.name]).catch(() => undefined);
	}
}

function round2(value) {
	return value === null || value === undefined ? null : Math.round(value * 100) / 100;
}

function median(values) {
	const xs = [...values].sort((a, b) => a - b);
	const middle = xs.length >> 1;
	return xs.length % 2 === 1 ? xs[middle] : (xs[middle - 1] + xs[middle]) / 2;
}

/** The published per-engine block for one row. */
function summarizeEngine(bench, engine, result) {
	const responseMs = result.rows.map((row) => row.responseMs);
	const serverMs = result.rows.map((row) => row.serverMs).filter((value) => value !== null && value !== undefined);
	return {
		engine,
		status: 'measured',
		runs: result.rows,
		summary: stats(responseMs),
		serverMs: serverMs.length > 0 ? stats(serverMs) : null,
		verdict: verdict(responseMs),
		peakRssKib: result.peakRssKib,
		retriedRuns: result.retriedRuns,
		gatewayOverheadMs: bench.needsModel ? round2(median(result.rows.map((row) => row.gatewayOverheadMs))) : null,
		importerFindings: result.prepared.importerFindings ?? [],
	};
}

function startCaffeinate() {
	if (process.platform !== 'darwin') return null;
	try {
		return spawn('caffeinate', ['-i', '-w', String(process.pid)], { stdio: 'ignore' });
	} catch {
		return null; // Absent caffeinate is not a failure; the run just may sleep.
	}
}

/** Resolve which n8n the run should measure, and how. */
function resolveN8nMode(env, control) {
	if (control) return 'off';
	const requested = (env.BENCH_N8N ?? '').trim().toLowerCase();
	if (requested === 'off') return 'off';
	if (requested === 'external') return 'external';
	return 'managed';
}

async function main() {
	const plan = benchPlan();
	const control = process.env.BENCH_CONTROL === 'aa';
	const skipAgent = process.env.BENCH_SKIP_AGENT === '1';
	const mode = resolveN8nMode(process.env, control);
	const controlPath = (process.env.BENCH_CONTROL_FILE ?? '').trim() || null;
	console.log(`bench: method v2 runs=${plan.runs} block=${plan.block} warmup=${plan.warmup} smoke=${plan.smoke} n8n=${mode} control=${control}`);

	// The A/A control is measured in its own short run and attached to the
	// published comparison through BENCH_CONTROL_FILE, so the raw file that
	// carries the numbers also carries the harness's own negative control.
	let attachedControl = null;
	if (controlPath && !control) {
		try {
			const parsed = controlFromFile(await readFile(controlPath, 'utf-8'));
			if (parsed.ok) {
				attachedControl = parsed.control;
				console.log(`bench: attaching A/A control from ${controlPath}: ratio ${attachedControl.ratio} (95% CI [${attachedControl.lo}, ${attachedControl.hi}])`);
			} else {
				console.log(`bench: no A/A control attached — ${controlPath} ${parsed.reason}`);
			}
		} catch (error) {
			console.log(`bench: no A/A control attached — ${controlPath}: ${error instanceof Error ? error.message : String(error)}`);
		}
	}

	const caffeinate = startCaffeinate();
	// The machine's condition at the START of the run. environment() is captured
	// after the last row, so its own "before" sample would otherwise be the same
	// instant as its "after" one and the published summary would claim a stable
	// machine it never observed.
	const loadAtStart = loadAverage();
	const benchServer = await startBenchServer(OLLAMA_BASE_URL);
	const kilas = await startKilasFlow(benchServer.endpoint);
	console.log(`bench: bench server at ${benchServer.origin} (stub + tool + Ollama proxy), kilasflow at ${kilas.baseURL}`);

	const cleanups = [() => benchServer.close(), () => kilas.close()];
	if (caffeinate) cleanups.push(async () => caffeinate.kill('SIGTERM'));
	const shutdown = async (signal) => {
		// A signal tears the engines down under any in-flight delivery, which
		// then fails for the right reason (the engine is gone); that failure
		// must not race the signal's own exit code.
		shuttingDown = true;
		console.error(`bench: ${signal} — closing the child, the container and the bench server`);
		for (const cleanup of cleanups.reverse()) await cleanup().catch(() => undefined);
		process.exit(130);
	};
	process.on('SIGINT', () => void shutdown('SIGINT'));
	process.on('SIGTERM', () => void shutdown('SIGTERM'));

	// --- n8n: managed throwaway container, or an operator's own instance ------
	let n8nContainer = null;
	let n8nClient = null;
	let n8nSkip = null;
	let n8nBaseUrl = null;
	// The bench server as the container sees it (host.docker.internal), which is
	// NOT where n8n itself listens: the container reaches the harness's loopback
	// server through OrbStack's host gateway.
	let benchDockerOrigin = benchServer.originFor('host.docker.internal');
	if (mode === 'managed') {
		const started = await startN8nContainer();
		if (started.status !== 'running') {
			n8nSkip = { status: started.status, reason: started.reason };
			console.log(`bench: n8n skipped — ${started.reason}`);
		} else {
			n8nContainer = started;
			cleanups.push(() => n8nContainer.close());
			n8nBaseUrl = started.url;
			try {
				const client = new N8nClient({ url: started.url });
				await client.ownerSetup(started.credentials);
				await client.login(started.credentials);
				n8nClient = client;
				console.log(`bench: n8n ${started.version ?? 'unknown'} at ${started.url} (container ${started.containerName}, digest ${started.digest ?? 'unknown'})`);
			} catch (error) {
				n8nSkip = { status: 'skipped-owner-setup-failed', reason: String(error instanceof Error ? error.message : error).slice(0, 400) };
				console.log(`bench: n8n skipped — owner setup or sign-in failed: ${n8nSkip.reason}`);
			}
		}
	} else if (mode === 'external') {
		const config = externalN8nConfig(process.env);
		if (!config) {
			n8nSkip = { status: 'skipped-external-unconfigured', reason: 'BENCH_N8N=external needs N8N_URL, N8N_EMAIL and N8N_PASSWORD in the environment' };
		} else {
			n8nBaseUrl = config.url;
			if (config.stubOrigin) benchDockerOrigin = config.stubOrigin;
			try {
				const client = new N8nClient({ url: config.url });
				await client.login({ email: config.email, password: config.password });
				n8nClient = client;
				console.log(`bench: external n8n at ${config.url}`);
			} catch (error) {
				n8nSkip = { status: 'skipped-external-login-failed', reason: String(error instanceof Error ? error.message : error).slice(0, 400) };
			}
		}
	}
	const n8nActive = n8nClient !== null && n8nBaseUrl !== null && n8nSkip === null;

	const context = {
		benchServer,
		origin: benchServer.origin,
		n8nOrigin: benchDockerOrigin,
		modelBaseURL: benchServer.modelBaseURL,
	};

	try {
		const stubLatency = await probeStubLatency(benchServer.origin);
		console.log(`bench: stub latency p50=${stubLatency.p50}ms n=${stubLatency.n}`);

		const kilasView = { origin: benchServer.origin, n8nOrigin: benchDockerOrigin, modelBaseURL: benchServer.modelBaseURL };
		const adapters = control
			? [kilasAdapter({ baseURL: kilas.baseURL, pid: kilas.pid, view: kilasView }), kilasAdapter({ name: 'kilasflow-control', baseURL: kilas.baseURL, pid: kilas.pid, view: kilasView })]
			: [kilasAdapter({ baseURL: kilas.baseURL, pid: kilas.pid, view: kilasView })];
		if (n8nActive) {
			const view = { origin: benchDockerOrigin, n8nOrigin: benchDockerOrigin, modelBaseURL: `${benchDockerOrigin}/v1` };
			adapters.push(
				createN8nAdapter({
					client: n8nClient,
					view,
					host: {
						url: n8nBaseUrl,
						containerName: n8nContainer?.containerName ?? null,
						origin: benchDockerOrigin,
						modelBaseUrl: benchDockerOrigin,
						rss: n8nContainer ? () => containerRssKib(n8nContainer.containerName) : null,
					},
				}),
			);
		}

		const engineResults = new Map(adapters.map((adapter) => [adapter.name, []]));
		const controlRows = new Map();
		const rows = control ? BENCHMARKS.filter((bench) => bench.key === 'webhook-set-respond' || bench.key === 'if-fanout') : BENCHMARKS;

		const noteSkip = (bench, status, note) => {
			for (const adapter of adapters) {
				engineResults.get(adapter.name).push({ key: bench.key, name: bench.name, status, note, divergence: bench.divergence, supplementary: bench.supplementary });
			}
			console.log(`bench: ${bench.key}: ${status} — ${note}`);
		};

		for (const [workflowIndex, bench] of rows.entries()) {
			if (bench.needsModel) {
				if (skipAgent) {
					noteSkip(bench, 'skipped-operator-opt-out', 'BENCH_SKIP_AGENT=1; rerun without it to time the agent tool-loop.');
					continue;
				}
				const gate = await ollamaModelPresent();
				if (!gate.ok) {
					noteSkip(bench, 'skipped-no-model', gate.reason);
					continue;
				}
			}
			const loadBefore = loadAverage();
			console.log(`bench: ${bench.key}: ${plan.warmup} warm-up + ${plan.runs} timed per engine…`);
			const outcome = await runRow({ bench, adapters, plan, context, workflowIndex });
			for (const adapter of adapters) {
				const engine = summarizeEngine(bench, adapter.name, outcome.perEngine[adapter.name]);
				const label = `${bench.key}/${adapter.name}`;
				console.log(
					`bench: ${label}: client p50=${engine.summary.p50}ms p95=${engine.summary.p95}ms server p50=${engine.serverMs?.p50 ?? '—'}ms peakRSS=${Math.round((engine.peakRssKib ?? 0) / 1024)}MiB` +
						`${engine.verdict.ok ? '' : ` INCONCLUSIVE (${engine.verdict.reasons.join('; ')})`}${engine.retriedRuns > 0 ? ` retriedRuns=${engine.retriedRuns}` : ''}`,
				);
				if (control) {
					controlRows.set(`${bench.key}/${adapter.name}`, { bench, engine });
					continue;
				}
				engineResults.get(adapter.name).push({
					key: bench.key,
					name: bench.name,
					...engine,
					divergence: bench.divergence,
					supplementary: bench.supplementary,
					divergentByDesign: bench.divergentByDesign,
					loadAverageBefore: loadBefore,
					loadAverageAfter: outcome.loadAverageAfter,
				});
			}
		}

		const kilasResults = engineResults.get('kilasflow') ?? [];
		const n8nResults = engineResults.get('n8n') ?? [];

		// The poll artefact claim, checked rather than assumed: v1's webhook row
		// read p50 30.47 ms against a 1 ms server record because the timed region
		// contained a 25 ms poll cadence. If it comes back, the run is wrong.
		const webhook = kilasResults.find((entry) => entry.key === 'webhook-set-respond' && entry.status === 'measured');
		if (webhook && webhook.summary.p50 >= WEBHOOK_P50_CEILING_MS) {
			throw new Error(
				`webhook-set-respond client p50 ${webhook.summary.p50}ms is not below ${WEBHOOK_P50_CEILING_MS}ms (server p50 ${webhook.serverMs?.p50}ms): ` +
					'the poll artefact may be back in the timed region — investigate, do not relax this ceiling',
			);
		}

		let controlSummary = null;
		if (control) {
			const kilasSide = controlRows.get('webhook-set-respond/kilasflow') ?? controlRows.get('if-fanout/kilasflow');
			const controlSide = controlRows.get('webhook-set-respond/kilasflow-control') ?? controlRows.get('if-fanout/kilasflow-control');
			if (kilasSide && controlSide) {
				const { ratio, lo, hi } = bootstrapRatio(kilasSide.engine.runs.map((row) => row.responseMs), controlSide.engine.runs.map((row) => row.responseMs));
				controlSummary = { ratio, lo, hi, label: compareLabel({ ratio, lo, hi }), kilasP50: kilasSide.engine.summary.p50, controlP50: controlSide.engine.summary.p50 };
				console.log(`bench: A/A control ratio ${ratio} (95% CI [${lo}, ${hi}]) — ${controlSummary.label}`);
				if (controlPath) {
					// The control is a short separate run; this hands its measured
					// block to the main run so the published raw file carries its own
					// negative control. Numbers only — never a secret.
					const payload = JSON.stringify({ control: controlSummary }, null, 2);
					assertNoSecrets([payload], secretsFromEnv());
					await mkdir(dirname(controlPath), { recursive: true });
					await writeFile(controlPath, payload);
					console.log(`bench: A/A control summary → ${controlPath}`);
				}
			}
		}

		// The published comparison: only rows measured on both engines.
		const comparison = [];
		if (!control) {
			for (const bench of BENCHMARKS) {
				const kilasSide = kilasResults.find((entry) => entry.key === bench.key && entry.status === 'measured');
				const n8nSide = n8nResults.find((entry) => entry.key === bench.key && entry.status === 'measured');
				if (!kilasSide || !n8nSide) continue;
				const { ratio, lo, hi } = bootstrapRatio(kilasSide.runs.map((row) => row.responseMs), n8nSide.runs.map((row) => row.responseMs));
				const label = compareLabel({ ratio, lo, hi, kilasVerdict: kilasSide.verdict, n8nVerdict: n8nSide.verdict });
				comparison.push({
					key: bench.key,
					name: bench.name,
					kilasP50: kilasSide.summary.p50,
					n8nP50: n8nSide.summary.p50,
					ratio,
					lo,
					hi,
					label,
					ok: !label.startsWith('inconclusive'),
					supplementary: Boolean(bench.supplementary),
				});
				console.log(`bench: comparison ${bench.key}: ratio ${ratio} (95% CI [${lo}, ${hi}]) — ${label}`);
			}
		}

		// Container-side and hop probes: they quantify the substrate difference
		// (OrbStack's published-port proxy) separately from engine behaviour.
		const hopProbe = { n8n: null, kilasflow: null };
		try {
			hopProbe.kilasflow = await probeHealth(`${kilas.baseURL}/api/v1/health`);
			if (n8nBaseUrl) hopProbe.n8n = await probeHealth(`${n8nBaseUrl}/healthz`);
		} catch (error) {
			hopProbe.error = String(error instanceof Error ? error.message : error).slice(0, 200);
		}
		let containerStubLatency = null;
		let containerHwm = null;
		if (n8nContainer) {
			try {
				containerStubLatency = await probeStubLatencyInContainer(n8nContainer.containerName, benchServer.originFor('host.docker.internal'));
			} catch {
				containerStubLatency = null;
			}
			try {
				containerHwm = await containerHwmKib(n8nContainer.containerName);
			} catch {
				containerHwm = null;
			}
		}

		const env = await environment({ ollamaOrigin: benchServer.origin, model: OLLAMA_MODEL });
		env.loadAverage.before = loadAtStart;
		env.loadAverage.after = loadAverage();
		let n8n;
		if (control) {
			n8n = { status: 'not-measured-in-control', reason: 'the A/A control measures the harness against itself, not n8n', results: [] };
		} else if (n8nActive) {
			let instanceVersion = null;
			try {
				instanceVersion = await n8nClient.version();
			} catch {
				instanceVersion = null;
			}
			n8n = {
				status: 'measured',
				version: instanceVersion ?? n8nContainer?.version ?? null,
				instanceVersion,
				imageVersionLabel: n8nContainer?.version ?? null,
				image: n8nContainer?.image ?? (process.env.N8N_IMAGE || null),
				digest: n8nContainer?.digest ?? null,
				architecture: n8nContainer?.architecture ?? null,
				containerEnv: n8nContainer?.envKeys ?? [],
				container: n8nContainer
					? { name: n8nContainer.containerName, url: n8nContainer.url, hostOrigin: benchDockerOrigin, hwmKib: containerHwm }
					: { external: true, url: n8nBaseUrl, hostOrigin: benchDockerOrigin },
				results: n8nResults,
			};
		} else {
			const status = n8nSkip?.status ?? 'skipped-off';
			const reason = n8nSkip?.reason ?? 'BENCH_N8N=off: the n8n half was not measured';
			n8n = { status, reason, results: BENCHMARKS.map((bench) => ({ key: bench.key, name: bench.name, status, note: reason })) };
		}

		const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
		const outDir = plan.outDir ?? HERE;
		const rawPath = join(outDir, `bench-${stamp}.json`);
		const raw = {
			artifact: outDir === HERE ? `e2e/benchmark/${basename(rawPath)}` : rawPath,
			tool: 'e2e/benchmark/run.mjs (FEAT-8mymac)',
			environment: env,
			method: {
				version: 2,
				runs: plan.runs,
				warmup: plan.warmup,
				block: plan.block,
				seed: BOOTSTRAP.seed,
				smoke: plan.smoke,
				bounds: BOUNDS,
				ratioBand: RATIO_BAND,
				quietLoad: 2.0,
				engines: adapters.map((adapter) => adapter.name),
				isolation: 'sequential, one workflow at a time, one delivery at a time, settled after every run',
				primary: 'client-observed HTTP response latency, request sent to full body received (same function on both engines)',
				secondary: 'server execution record startedAt to finishedAt, 1 ms resolution, fetched after the run',
				ollama: { baseUrl: OLLAMA_BASE_URL, model: OLLAMA_MODEL },
			},
			stubLatency,
			containerStubLatency,
			hopProbe,
			kilasflow: { status: 'measured', results: kilasResults },
			n8n,
			comparison,
			control: controlSummary ?? attachedControl,
			exclusions: EXCLUSIONS,
		};

		assertNoSecrets([JSON.stringify(raw)], secretsFromEnv());
		await mkdir(outDir, { recursive: true });
		await writeFile(rawPath, JSON.stringify(raw, null, 2));
		console.log(`bench: raw → ${rawPath}`);
		if (plan.smoke) {
			const outcome = await writeSummary({ raw, summaryPath: join(outDir, 'SUMMARY.md') });
			console.log(`bench: summary not written (${outcome.reason})`);
			console.log(summarize(raw).split('\n').slice(0, 14).join('\n'));
		} else {
			// BENCH_OUT_DIR redirects the whole output, so a run that is not
			// publishing never overwrites the repository's SUMMARY.md.
			const outcome = await writeSummary({ raw, summaryPath: join(outDir, 'SUMMARY.md') });
			console.log(`bench: summary → ${outcome.summaryPath}`);
		}
		if (n8n.status !== 'measured') console.log(`bench: PRELIMINARY — the n8n half was not measured (${n8n.reason ?? n8n.status}).`);
	} finally {
		for (const cleanup of cleanups) await cleanup().catch(() => undefined);
	}
}

main().catch((error) => {
	if (shuttingDown) return; // The signal handler owns the exit; the delivery failed because its engine was torn down.
	console.error(`bench: FAILED: ${error instanceof Error ? error.stack ?? error.message : String(error)}`);
	process.exit(1);
});
