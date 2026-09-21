/**
 * Shared benchmark plumbing for FEAT-8mymac (method version 2).
 *
 * What this owns: booting a real kilasflow binary against a fresh temp
 * database (same composition as e2e/helpers/server.ts), the single loopback
 * bench server (deterministic stub API, deterministic tool, and a
 * byte-transparent proxy to the local Ollama for both the OpenAI-compatible
 * /v1/* paths and Ollama's own /api/* paths), the pre-registered run plan,
 * statistics and environment capture.
 *
 * What this does not own: product code (nothing outside e2e/benchmark/), the
 * measurement rules (method.mjs), secrets (never in a file, see RULES), or
 * merge gates (make bench-test is a local target, never CI).
 */

import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { once } from 'node:events';
import { createWriteStream } from 'node:fs';
import { mkdtemp, rm } from 'node:fs/promises';
import { createServer, request as proxyRequest } from 'node:http';
import net from 'node:net';
import { loadavg, tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';

import { BLOCK_SIZE } from './method.mjs';
import { stubResponse } from './workflows.mjs';

const execFileAsync = promisify(execFile);
const REPO_DIR = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');

export const RUNS_FLOOR = 30;
export const WARMUP_DEFAULT = 5;

/**
 * The pre-registered run plan.
 *
 * Non-smoke floors the run count at the ticket's N>=30 and rounds it up to a
 * whole number of blocks, because the ABBA schedule swaps on block boundaries.
 * BENCH_SMOKE=1 is the harness's own smoke test: four samples per engine, two
 * per block, one discarded warm-up, and a default output directory under the
 * OS temp dir so a smoke run writes nothing into the repository.
 */
export function benchPlan(env = process.env) {
	if (env.BENCH_SMOKE === '1') {
		return {
			runs: 4,
			block: 2,
			warmup: 1,
			smoke: true,
			outDir: env.BENCH_OUT_DIR || join(tmpdir(), `kilasflow-bench-smoke-${process.pid}`),
		};
	}
	const block = BLOCK_SIZE;
	let runs = Number(env.BENCH_RUNS ?? RUNS_FLOOR);
	if (!Number.isInteger(runs) || runs < RUNS_FLOOR) runs = RUNS_FLOOR;
	runs = Math.ceil(runs / block) * block;
	let warmup = Number(env.BENCH_WARMUP ?? WARMUP_DEFAULT);
	if (!Number.isInteger(warmup) || warmup < 0) warmup = WARMUP_DEFAULT;
	return { runs, block, warmup, smoke: false, outDir: env.BENCH_OUT_DIR || null };
}

/** Plain REST client against the KilasFlow API (auth off in bench instances). */
export async function api(baseURL, method, path, body, wantStatus = 200) {
	const response = await fetch(`${baseURL}/api/v1${path}`, {
		method,
		headers: { 'content-type': 'application/json' },
		body: body === undefined ? undefined : JSON.stringify(body),
	});
	if (response.status !== wantStatus) {
		throw new Error(
			`${method} ${path}: status ${response.status}, want ${wantStatus} (body: ${await response.text()})`,
		);
	}
	if (response.status === 204) return null;
	return response.json();
}

async function freePort() {
	const listener = net.createServer();
	listener.listen(0, '127.0.0.1');
	await once(listener, 'listening');
	const address = listener.address();
	if (!address || typeof address === 'string') throw new Error('bench: cannot reserve a port');
	const port = address.port;
	listener.close();
	await once(listener, 'close');
	return port;
}

/** Ollama's own paths, proxied next to /v1/*. None collides with the stub's exact paths. */
const OLLAMA_NATIVE_PATHS = ['/api/chat', '/api/generate', '/api/tags', '/api/show', '/api/version', '/api/ps'];

/** How a gateway hit is attributed. Mirrors method.mjs's rules for gatewayOverhead. */
export function classifyHit(path) {
	if (typeof path !== 'string') return 'other';
	if (path === '/api/users' || path === '/api/orders' || path === '/api/echo') return 'stub';
	if (path.startsWith('/tool/')) return 'tool';
	if (path.startsWith('/v1/') || OLLAMA_NATIVE_PATHS.includes(path)) return 'model';
	return 'other';
}

/** Every hit recorded after `index`, in arrival order, each with its own durMs. */
export function hitsSince(hits, index) {
	return hits.slice(Math.max(0, index));
}

/**
 * The single server both engines admit as their one loopback dependency.
 *
 * KilasFlow reaches it at 127.0.0.1 (its one allowed_private_endpoints entry,
 * with allow_private_networks left off); n8n reaches it at
 * host.docker.internal, because the container's loopback is its own. The stub
 * answers are shared with the fixtures' expectations (workflows.mjs), so the
 * expected body cannot drift from the served body.
 */
export async function startBenchServer(ollamaBaseURL, options = {}) {
	const bindHost = options.bindHost ?? process.env.BENCH_BIND_HOST ?? '127.0.0.1';
	const ollama = new URL(ollamaBaseURL);
	const ollamaHost = ollama.hostname;
	const ollamaPort = Number(ollama.port || (ollama.protocol === 'https:' ? 443 : 80));
	const hits = [];

	const server = createServer((incoming, outgoing) => {
		const received = performance.now();
		const chunks = [];
		incoming.on('data', (chunk) => chunks.push(chunk));
		incoming.on('end', () => {
			const body = Buffer.concat(chunks);
			const url = new URL(incoming.url ?? '/', 'http://127.0.0.1');
			const path = url.pathname;
			const record = () => {
				hits.push({ method: incoming.method ?? 'GET', path, at: Date.now(), durMs: performance.now() - received });
			};
			if (path.startsWith('/v1/') || OLLAMA_NATIVE_PATHS.includes(path)) {
				const proxy = proxyRequest(
					{
						host: ollamaHost,
						port: ollamaPort,
						method: incoming.method,
						path: path + url.search,
						headers: { ...incoming.headers, host: `${ollamaHost}:${ollamaPort}` },
					},
					(proxyRes) => {
						// durMs is "request received to response finished", and
						// "finished" is the LAST byte, not the first: Ollama streams
						// its tokens, so recording at header time would report the
						// time to first byte and charge the engine with the model's
						// whole token stream as its own overhead.
						let recorded = false;
						const recordOnce = () => {
							if (recorded) return;
							recorded = true;
							record();
						};
						proxyRes.once('end', recordOnce);
						proxyRes.once('error', recordOnce);
						outgoing.once('close', recordOnce);
						outgoing.writeHead(proxyRes.statusCode ?? 502, proxyRes.headers);
						proxyRes.pipe(outgoing);
					},
				);
				proxy.on('error', () => {
					record();
					outgoing.writeHead(502, { 'content-type': 'application/json' });
					outgoing.end(JSON.stringify({ ok: false, error: 'bench-server: ollama unreachable' }));
				});
				proxy.end(body);
				return;
			}
			const stub = stubResponse(path);
			record();
			if (!stub) {
				outgoing.writeHead(404, { 'content-type': 'application/json' });
				outgoing.end(JSON.stringify({ ok: false, error: `unknown bench path ${path}` }));
				return;
			}
			outgoing.writeHead(stub.status, { 'content-type': 'application/json' });
			outgoing.end(JSON.stringify(stub.body));
		});
	});
	server.listen(0, bindHost);
	await once(server, 'listening');
	const address = server.address();
	if (!address || typeof address === 'string') throw new Error('bench: bench server failed to start');
	const port = address.port;
	const originFor = (host) => `http://${host}:${port}`;
	const origin = originFor('127.0.0.1');
	let closed = false;
	return {
		origin,
		port,
		bindHost,
		originFor,
		/** KilasFlow's one allowed private endpoint, as the deployment config wants it. */
		endpoint: `127.0.0.1:${port}`,
		modelBaseURL: `${origin}/v1`,
		hits,
		close: async () => {
			if (closed) return;
			closed = true;
			server.close();
			await once(server, 'close').catch(() => undefined);
		},
	};
}

/** Boots a real kilasflow binary against a fresh temp SQLite database. */
export async function startKilasFlow(benchEndpoint) {
	const binary = join(REPO_DIR, 'bin', 'kilasflow');
	const dataDir = await mkdtemp(join(tmpdir(), 'kilasflow-bench-'));
	const port = await freePort();
	const baseURL = `http://127.0.0.1:${port}`;
	const logPath = join(dataDir, `kilasflow-${port}.log`);
	const logStream = createWriteStream(logPath, { flags: 'a' });
	const chunks = [];
	const child = spawn(binary, ['-config', ''], {
		cwd: REPO_DIR,
		env: {
			...process.env,
			KILASFLOW_SERVER_HOST: '127.0.0.1',
			KILASFLOW_SERVER_PORT: String(port),
			KILASFLOW_DATABASE_DSN: join(dataDir, 'kilasflow.db'),
			KILASFLOW_OUTBOUND_ALLOWED_HOSTS: '127.0.0.1',
			KILASFLOW_OUTBOUND_ALLOWED_PRIVATE_ENDPOINTS: benchEndpoint,
			KILASFLOW_EMBED_SIGNING_KEY: randomBytes(32).toString('base64'),
			KILASFLOW_ENCRYPTION_KEY: randomBytes(32).toString('base64'),
		},
		stdio: ['ignore', 'pipe', 'pipe'],
	});
	child.stdout?.on('data', (chunk) => {
		chunks.push(chunk.toString());
		if (!logStream.writableEnded) logStream.write(chunk);
	});
	child.stderr?.on('data', (chunk) => {
		chunks.push(chunk.toString());
		if (!logStream.writableEnded) logStream.write(chunk);
	});

	const deadline = Date.now() + 60_000;
	for (;;) {
		if (child.exitCode !== null) throw new Error(`bench: kilasflow exited early: ${chunks.join('').slice(-2000)}`);
		try {
			const res = await fetch(`${baseURL}/api/v1/ready`);
			if (res.ok) break;
		} catch {
			// Not up yet.
		}
		if (Date.now() > deadline) {
			child.kill('SIGKILL');
			throw new Error(`bench: kilasflow not ready in 60s: ${chunks.join('').slice(-2000)}`);
		}
		await new Promise((r) => setTimeout(r, 200));
	}

	let closed = false;
	return {
		baseURL,
		port,
		pid: child.pid,
		dataDir,
		logPath,
		close: async () => {
			if (closed) return;
			closed = true;
			logStream.end();
			child.kill('SIGTERM');
			const exited = await Promise.race([once(child, 'exit'), new Promise((r) => setTimeout(() => r('timeout'), 10_000))]);
			if (exited === 'timeout') child.kill('SIGKILL');
			// The temp database, its WAL files and the log are this run's only
			// local footprint; leave none of them behind.
			await rm(dataDir, { recursive: true, force: true }).catch(() => undefined);
		},
	};
}

/** Sampled peak RSS (KiB) of a process, via ps. Documented as sampled, not traced. */
export async function sampleRssKib(pid) {
	try {
		const { stdout } = await execFileAsync('ps', ['-p', String(pid), '-o', 'rss=']);
		const value = Number(stdout.trim());
		return Number.isFinite(value) && value > 0 ? value : null;
	} catch {
		return null;
	}
}

/** Polls one execution to a terminal status. Status-observed, never a sleep. */
export async function waitForExecution(baseURL, executionId, timeoutMs = 120_000) {
	const terminal = new Set(['succeeded', 'failed', 'cancelled']);
	const deadline = Date.now() + timeoutMs;
	for (;;) {
		const record = await api(baseURL, 'GET', `/executions/${executionId}`);
		if (terminal.has(record.status)) return record;
		if (Date.now() > deadline) throw new Error(`bench: execution ${executionId} not terminal in ${timeoutMs}ms`);
		await new Promise((r) => setTimeout(r, 25));
	}
}

/**
 * Every execution record for a workflow, newest first, following the API's
 * cursor so a row with more than one page is still complete.
 */
export async function executionRecords(baseURL, workflowId, maxPages = 10) {
	const items = [];
	let cursor = '';
	for (let page = 0; page < maxPages; page++) {
		const query = `/executions?workflowId=${encodeURIComponent(workflowId)}&limit=100${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`;
		const body = await api(baseURL, 'GET', query);
		const batch = body.items ?? [];
		items.push(...batch);
		cursor = body.nextCursor ?? '';
		if (!cursor) break;
	}
	return items;
}

/** Descriptive stats over an array of numbers. */
export function stats(values) {
	const xs = [...values].sort((a, b) => a - b);
	const n = xs.length;
	const min = xs[0];
	const max = xs[n - 1];
	const mean = xs.reduce((a, b) => a + b, 0) / n;
	const variance = xs.reduce((a, b) => a + (b - mean) ** 2, 0) / n;
	// Nearest-rank percentile: p95 of 30 samples is the 29th value, which the
	// summary states as a limit rather than implying an interpolated quantile.
	const quantile = (q) => xs[Math.min(n - 1, Math.ceil(q * n) - 1)];
	return {
		n,
		min: round2(min),
		p50: round2(quantile(0.5)),
		p95: round2(quantile(0.95)),
		max: round2(max),
		mean: round2(mean),
		stddev: round2(Math.sqrt(variance)),
	};
}

function round2(value) {
	return value === null || value === undefined ? null : Math.round(value * 100) / 100;
}

async function command(file, args) {
	try {
		const { stdout } = await execFileAsync(file, args);
		return stdout.trim();
	} catch {
		return null;
	}
}

/** 1-minute load average, rounded, for the quiet-machine banner. */
export function loadAverage() {
	const [one, five, fifteen] = loadavg();
	return [round2(one), round2(five), round2(fifteen)];
}

/**
 * Machine spec, versions and conditions, recorded into every raw file.
 *
 * This machine is a laptop that also runs a dozen parallel builds, so power
 * source, thermal state and the load average are part of the measurement, not
 * decoration: a number produced under throttle or load is not comparable with
 * one produced on an idle machine.
 */
export async function environment({ ollamaOrigin, model } = {}) {
	const [chip, model_, memBytes, ncpu, uname, macos, power, therm, docker, kilasflowVersion, dirty, ollama] = await Promise.all([
		command('sysctl', ['-n', 'machdep.cpu.brand_string']),
		command('sysctl', ['-n', 'hw.model']),
		command('sysctl', ['-n', 'hw.memsize']),
		command('sysctl', ['-n', 'hw.ncpu']),
		command('uname', ['-smr']),
		command('sw_vers', ['-productVersion']),
		command('pmset', ['-g', 'batt']),
		command('pmset', ['-g', 'therm']),
		dockerFacts(),
		command(join(REPO_DIR, 'bin', 'kilasflow'), ['-version']),
		gitDirty(),
		ollamaFacts(ollamaOrigin, model),
	]);
	return {
		date: new Date().toISOString(),
		machine: {
			chip: chip ?? 'unknown',
			model: model_ ?? 'unknown',
			uname: uname ?? 'unknown',
			macos: macos ?? 'unknown',
			cpuCount: ncpu !== null ? Number(ncpu) : 'unknown',
			memBytes: memBytes !== null ? Number(memBytes) : 'unknown',
		},
		kilasflow: { version: (kilasflowVersion ?? 'unknown').split('\n')[0], commit: await gitCommit(), dirty },
		node: process.version,
		loadAverage: { before: loadAverage(), after: null },
		power: { source: power ? power.split('\n')[0] : 'unknown' },
		thermal: thermalState(therm),
		docker,
		ollama,
	};
}

/** 100 means no thermal throttle; anything less is recorded verbatim. */
function thermalState(raw) {
	if (!raw) return { state: 'unknown' };
	const limit = /CPU_Speed_Limit\s*=\s*(\d+)/.exec(raw);
	if (!limit) return { state: raw.split('\n')[0] || 'unknown' };
	return { state: Number(limit[1]) >= 100 ? 'nominal' : `throttled (CPU_Speed_Limit ${limit[1]})`, cpuSpeedLimit: Number(limit[1]) };
}

/** Tolerant of Docker being absent: the KilasFlow half still runs. */
async function dockerFacts() {
	const version = await command('docker', ['version', '--format', '{{.Server.Version}}']);
	if (!version) return { available: false, reason: 'docker CLI or daemon unavailable' };
	const info = await command('docker', ['info', '--format', '{{.OperatingSystem}}|{{.NCPU}}|{{.MemTotal}}']);
	const [os, cpus, memBytes] = (info ?? '').split('|');
	return {
		available: true,
		serverVersion: version,
		os: os || 'unknown',
		cpuCount: cpus ? Number(cpus) : 'unknown',
		memBytes: memBytes ? Number(memBytes) : 'unknown',
	};
}

/** Ollama version and the digest of the model under test, read through the gateway. */
async function ollamaFacts(origin, model) {
	if (!origin) return { version: null, model: model ?? null, digest: null };
	try {
		const version = await fetch(`${origin}/api/version`, { signal: AbortSignal.timeout(10_000) });
		const tags = await fetch(`${origin}/api/tags`, { signal: AbortSignal.timeout(10_000) });
		const versionBody = version.ok ? await version.json() : null;
		const tagsBody = tags.ok ? await tags.json() : null;
		const entry = (tagsBody?.models ?? []).find((candidate) => candidate.name === model || candidate.model === model);
		return { version: versionBody?.version ?? null, model: model ?? null, digest: entry?.digest ?? null };
	} catch (error) {
		return { version: null, model: model ?? null, digest: null, error: String(error).slice(0, 200) };
	}
}

async function gitCommit() {
	const out = await command('git', ['rev-parse', 'HEAD']);
	return out ?? 'unknown';
}

/**
 * Whether the tracked tree differs from the recorded commit, ignoring the
 * benchmark's own output directory and the ticket notes: those are written by
 * the harness and by the operator, not part of the binary under test.
 */
async function gitDirty() {
	const out = await command('git', ['status', '--porcelain']);
	if (out === null) return null;
	const relevant = out
		.split('\n')
		.filter((line) => line.trim() !== '')
		.filter((line) => {
			const path = line.slice(3).trim();
			return !path.startsWith('e2e/benchmark/') && !path.startsWith('.pine/');
		});
	return relevant.length > 0;
}

/**
 * Trivial-handler latency of an engine's own health endpoint.
 *
 * This is the hop probe: n8n is reached through OrbStack's published-port proxy
 * while native KilasFlow is not, so this quantifies the floor that difference
 * puts under every n8n row, separately from anything the workflow does.
 */
export async function probeHealth(url, n = 30) {
	const samples = [];
	for (let i = 0; i < n; i++) {
		const started = performance.now();
		const res = await fetch(url);
		await res.text();
		samples.push(performance.now() - started);
	}
	return stats(samples);
}

/** Direct latency probe of the bench server (engine overhead excluded). */
export async function probeStubLatency(origin, n = 30) {
	const samples = [];
	for (let i = 0; i < n; i++) {
		const started = performance.now();
		const res = await fetch(`${origin}/api/echo`);
		await res.text();
		samples.push(performance.now() - started);
	}
	return stats(samples);
}
