#!/usr/bin/env node
/**
 * Reproducible KilasFlow-vs-n8n benchmark for FEAT-8mymac.
 *
 * Usage:  make bench-compare
 *         BENCH_RUNS=30 BENCH_WARMUP=5 BENCH_SKIP_AGENT=1 node e2e/benchmark/run.mjs
 *
 * What it does:
 *   1. Boots one loopback bench server (stub API + deterministic tool +
 *      byte-transparent /v1 proxy to the local Ollama) and one real
 *      kilasflow binary against a fresh temp database.
 *   2. Executes each workflow in e2e/benchmark/workflows.mjs N>=30 times
 *      sequentially and isolated (warm-ups discarded), recording
 *      client-observed wall time per run plus the server execution record
 *      (startedAt→finishedAt) and sampled peak RSS.
 *   3. Runs the n8n comparison half when N8N_EMAIL/N8N_PASSWORD are set;
 *      otherwise records an honest skip with the exact provisioning
 *      exports. Nothing is ever fabricated for that side.
 *   4. Writes e2e/benchmark/bench-<ts>.json (raw) + e2e/benchmark/SUMMARY.md.
 *
 * It does NOT gate merges and does NOT run on every PR (single-machine,
 * third-party-adjacent variance) — on-demand via `make bench-compare` only.
 */

import { mkdir, writeFile } from 'node:fs/promises';
import { dirname, join, basename } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
	api,
	environment,
	manualTrigger,
	measureWorkflow,
	probeStubLatency,
	runCount,
	startBenchServer,
	startKilasFlow,
	stats,
	waitForExecution,
	warmupCount,
} from './lib.mjs';
import { BENCHMARKS } from './workflows.mjs';
import { isN8nLiveConfigured, N8N_LIVE_SKIP_REASON, N8N_URL, probeN8nVersion, runN8nBenchmark, n8nStats } from './n8n.mjs';
import { summarize } from './summarise.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));
const OLLAMA_BASE_URL = (process.env.KILASFLOW_TEST_OLLAMA_BASE_URL?.trim() || 'http://127.0.0.1:11434/v1').replace(/\/+$/, '');
const OLLAMA_MODEL = process.env.KILASFLOW_TEST_OLLAMA_MODEL?.trim() || 'gemma4:12b-mlx';

async function ollamaModelPresent() {
	try {
		const res = await fetch(`${OLLAMA_BASE_URL}/models`, { signal: AbortSignal.timeout(20_000) });
		if (!res.ok) return { ok: false, reason: `${OLLAMA_BASE_URL} answered ${res.status} for /v1/models (is \`ollama serve\` running? wants \`ollama pull ${OLLAMA_MODEL}\` next).` };
		const payload = await res.json();
		const offered = (payload.data ?? []).map((e) => e.id).filter(Boolean);
		if (!offered.includes(OLLAMA_MODEL)) {
			return { ok: false, reason: `${OLLAMA_BASE_URL} does not serve ${JSON.stringify(OLLAMA_MODEL)} — run \`ollama pull ${OLLAMA_MODEL}\`. It offers: ${offered.join(', ') || '(nothing)'}.` };
		}
		return { ok: true };
	} catch (error) {
		return { ok: false, reason: `${OLLAMA_BASE_URL} is unreachable (${error instanceof Error ? error.message : String(error)}). Start it with \`ollama serve\`, then \`ollama pull ${OLLAMA_MODEL}\`.` };
	}
}

async function listExecutionIds(baseURL, workflowId) {
	const page = await api(baseURL, 'GET', `/executions?workflowId=${encodeURIComponent(workflowId)}&limit=100`);
	return (page.items ?? page).map((item) => item.id);
}

async function waitForNewExecution(baseURL, workflowId, before, timeoutMs = 120_000) {
	const known = new Set(before);
	const deadline = Date.now() + timeoutMs;
	for (;;) {
		const page = await api(baseURL, 'GET', `/executions?workflowId=${encodeURIComponent(workflowId)}&limit=100`);
		const found = (page.items ?? page).find((item) => !known.has(item.id));
		if (found) return waitForExecution(baseURL, found.id, Math.max(10_000, deadline - Date.now()));
		if (Date.now() > deadline) throw new Error(`bench: no new execution for workflow ${workflowId} in ${timeoutMs}ms`);
		await new Promise((r) => setTimeout(r, 25));
	}
}

/** Webhook trigger: deliver at the minted opaque route, await the record. */
function webhookTrigger(routeUrl) {
	return async (baseURL, workflowId) => {
		const before = await listExecutionIds(baseURL, workflowId);
		const t0 = performance.now();
		let deliveryMs = 0;
		const response = await fetch(`${baseURL}${routeUrl}`, {
			method: 'POST',
			headers: { 'content-type': 'application/json' },
			body: JSON.stringify({ bench: workflowId }),
		});
		deliveryMs = performance.now() - t0;
		await response.text();
		if (![200, 202].includes(response.status)) throw new Error(`webhook delivery: status ${response.status}`);
		return {
			id: null,
			wait: async () => {
				const record = await waitForNewExecution(baseURL, workflowId, before);
				return record;
			},
			deliveryMs,
		};
	};
}

async function setupWorkflow(baseURL, bench, context) {
	if (bench.kind === 'webhook') {
		// Import the benchmark's own n8n JSON so the API mints the opaque
		// route; the same JSON is what the n8n half runs unmodified.
		const imported = await api(baseURL, 'POST', '/workflows/import', { format: 'n8n', name: `Bench ${bench.key}`, workflow: bench.n8n() }, 201);
		if ((imported.unsupported ?? []).length > 0) {
			throw new Error(`bench: ${bench.key}: import dropped nodes: ${JSON.stringify(imported.unsupported).slice(0, 400)}`);
		}
		if (!imported.webhooks?.length) throw new Error(`bench: ${bench.key}: import minted no webhook route`);
		const routeUrl = imported.webhooks[0].url;
		const activated = await api(baseURL, 'POST', `/workflows/${imported.workflow.id}/activate`, {});
		if (!activated.active) throw new Error(`bench: ${bench.key}: activation did not stick`);
		return { workflowId: imported.workflow.id, trigger: webhookTrigger(routeUrl) };
	}
	const document = bench.kilas(context.benchServer);
	if (document.credential) {
		const created = await api(baseURL, 'POST', '/credentials', { name: `Bench ${bench.key}`, type: 'httpBearerAuth', fields: { token: 'local' } }, 201);
		const target = document.nodes.find((n) => n.id === document.credential.nodeId);
		target.credentials = { [document.credential.key]: created.id };
		delete document.credential;
	}
	const input = document.input;
	delete document.input;
	const created = await api(baseURL, 'POST', '/workflows', document, 201);
	return { workflowId: created.id, trigger: manualTrigger(input) };
}

async function main() {
	const runs = runCount();
	const warmup = warmupCount();
	console.log(`bench: runs=${runs} warmup=${warmup} (contract: N>=30, sequential, isolated)`);

	const benchServer = await startBenchServer(OLLAMA_BASE_URL);
	console.log(`bench: bench server at ${benchServer.origin} (stub + tool + ollama proxy)`);
	const kilas = await startKilasFlow(benchServer.endpoint);
	console.log(`bench: kilasflow at ${kilas.baseURL} (log: ${kilas.logPath})`);

	const context = { benchServer };
	const kilasResults = [];
	try {
		const stubLatency = await probeStubLatency(benchServer.origin);
		console.log(`bench: stub latency p50=${stubLatency.p50}ms n=${stubLatency.n}`);

		for (const bench of BENCHMARKS) {
			if (bench.needsModel) {
				if (process.env.BENCH_SKIP_AGENT === '1') {
					kilasResults.push({ key: bench.key, name: bench.name, status: 'skipped-operator-opt-out', note: 'BENCH_SKIP_AGENT=1; rerun without it to time the agent tool-loop.' });
					console.log(`bench: ${bench.key}: skipped (BENCH_SKIP_AGENT=1)`);
					continue;
				}
				const gate = await ollamaModelPresent();
				if (!gate.ok) {
					kilasResults.push({ key: bench.key, name: bench.name, status: 'skipped-no-model', note: gate.reason });
					console.log(`bench: ${bench.key}: skipped — ${gate.reason}`);
					continue;
				}
				// Pay the post-load first-token cost outside the timed runs.
				console.log(`bench: ${bench.key}: model present, warming up first-token cost…`);
				await fetch(`${OLLAMA_BASE_URL}/chat/completions`, {
					method: 'POST',
					headers: { 'content-type': 'application/json' },
					body: JSON.stringify({ model: OLLAMA_MODEL, temperature: 0, stream: false, max_tokens: 1, messages: [{ role: 'user', content: 'hi' }] }),
					signal: AbortSignal.timeout(300_000),
				}).then((r) => r.text()).catch(() => undefined);
			}
			console.log(`bench: ${bench.key}: ${warmup} warm-up + ${runs} timed…`);
			const measured = await measureWorkflow({
				baseURL: kilas.baseURL,
				pid: kilas.pid,
				key: bench.key,
				setup: (baseURL) => setupWorkflow(baseURL, bench, context),
				runs,
				warmup,
			});
			const client = stats(measured.runs.map((r) => r.clientMs));
			const serverSamples = measured.runs.map((r) => r.serverMs).filter((v) => v !== null);
			kilasResults.push({
				key: bench.key,
				name: bench.name,
				status: 'measured',
				// Per-run wall-clock values, so a second operator can
				// recompute every number in summary from this file.
				runs: measured.runs,
				summary: client,
				serverMs: serverSamples.length > 0 ? stats(serverSamples) : null,
				peakRssKib: measured.peakRssKib,
				retriedRuns: measured.retriedRuns,
				divergence: bench.divergence,
			});
			console.log(`bench: ${bench.key}: client p50=${client.p50}ms p95=${client.p95}ms peakRSS=${Math.round(measured.peakRssKib / 1024)}MiB${measured.retriedRuns > 0 ? ` retriedRuns=${measured.retriedRuns}` : ''}`);
		}
		// The n8n half: measured only with credentials, honestly skipped else.
		let n8n;
		if (!isN8nLiveConfigured()) {
			console.log('bench: n8n half skipped — N8N_EMAIL/N8N_PASSWORD are not set (verified absent, not assumed).');
			n8n = {
				status: 'skipped-no-creds',
				skipReason: N8N_LIVE_SKIP_REASON,
				versionProbe: await probeN8nVersion(N8N_URL),
				results: BENCHMARKS.map((b) => ({ key: b.key, name: b.name, status: 'skipped-no-creds', note: N8N_LIVE_SKIP_REASON })),
			};
		} else {
			console.log('bench: n8n creds present — measuring the comparison half…');
			const outcome = await runN8nBenchmark({
				benchmarks: BENCHMARKS,
				runs,
				warmup,
				benchOrigin: benchServer.origin,
				onProgress: (key, i, n) => console.log(`bench: n8n ${key}: ${i}/${n}`),
			});
			n8n = {
				status: outcome.status,
				versionProbe: await probeN8nVersion(N8N_URL),
				results: outcome.results.map((r) => ({
					key: r.key,
					name: BENCHMARKS.find((b) => b.key === r.key)?.name ?? r.key,
					status: r.status,
					summary: r.runs?.length ? n8nStats(r.runs) : undefined,
					serverMs: null,
					note: r.note,
				})),
			};
		}

		const env = await environment();
		const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
		const raw = {
			artifact: `e2e/benchmark/bench-${stamp}.json`,
			tool: 'e2e/benchmark/run.mjs (FEAT-8mymac)',
			environment: env,
			method: {
				runs,
				warmup,
				isolation: 'sequential, one workflow at a time, fresh workflows per benchmark, shared warm server',
				primary: 'client-observed wall time (delivery/run-call → terminal execution record)',
				secondary: 'server execution record startedAt→finishedAt',
				peakRss: 'sampled after each run via ps(1), max kept; approximate, not traced',
				ollama: { baseUrl: OLLAMA_BASE_URL, model: OLLAMA_MODEL },
				n8nUrl: N8N_URL,
			},
			stubLatency,
			kilasflow: { status: 'measured', results: kilasResults },
			n8n,
		};

		await mkdir(HERE, { recursive: true });
		const rawPath = join(HERE, `bench-${stamp}.json`);
		raw.artifact = `e2e/benchmark/${basename(rawPath)}`;
		await writeFile(rawPath, JSON.stringify(raw, null, 2));
		const markdown = summarize(raw);
		await writeFile(join(HERE, 'SUMMARY.md'), markdown);
		console.log(`bench: raw → ${raw.artifact}`);
		console.log('bench: summary → e2e/benchmark/SUMMARY.md');
		if (n8n.status !== 'measured') {
			console.log('bench: PRELIMINARY — KilasFlow half only; the comparison half awaits creds. Do not quote as a comparison.');
		}
	} finally {
		await kilas.close().catch(() => undefined);
		await benchServer.close().catch(() => undefined);
	}
}

main().catch((error) => {
	console.error(`bench: FAILED: ${error instanceof Error ? error.stack ?? error.message : String(error)}`);
	process.exit(1);
});
