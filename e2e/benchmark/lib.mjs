/**
 * Shared benchmark plumbing for FEAT-8mymac.
 *
 * What this owns: booting a real kilasflow binary against a fresh temp
 * database (same composition as e2e/helpers/server.ts), the single loopback
 * bench server (stub API + deterministic tool + byte-transparent /v1 proxy to
 * the local Ollama), the timed-run loop, statistics, and environment capture.
 *
 * What this does not own: product code (nothing outside e2e/benchmark/),
 * secrets (n8n creds live in n8n.mjs and come from env only), or merge gates
 * (make bench-compare is on-demand only, never CI).
 */

import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { once } from 'node:events';
import { createWriteStream } from 'node:fs';
import { mkdtemp } from 'node:fs/promises';
import {
	createServer,
	request as proxyRequest,
} from 'node:http';
import net from 'node:net';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);
const REPO_DIR = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');

export const RUNS_DEFAULT = 30;
export const WARMUP_DEFAULT = 5;

/** Floor: the ticket contract is N>=30 per engine per workflow. */
export function runCount() {
	const raw = Number(process.env.BENCH_RUNS ?? RUNS_DEFAULT);
	if (!Number.isInteger(raw) || raw < RUNS_DEFAULT) return RUNS_DEFAULT;
	return raw;
}

export function warmupCount() {
	const raw = Number(process.env.BENCH_WARMUP ?? WARMUP_DEFAULT);
	if (!Number.isInteger(raw) || raw < 0) return WARMUP_DEFAULT;
	return raw;
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

/**
 * The single loopback server the bench instance admits through its one
 * allowed_private_endpoints entry (allow_private_networks stays off).
 *
 * One endpoint covers three loopback dependencies because the koanf env
 * mapping carries one value per variable:
 *   /api/*      — deterministic stub API for the HTTP benchmark workflows
 *   /tool/*     — deterministic tool endpoints for the agent benchmark
 *   /v1/*       — byte-transparent proxy to the real local Ollama (every
 *                 model byte comes from Ollama; Node only forwards)
 */
export async function startBenchServer(ollamaBaseURL) {
	const ollama = new URL(ollamaBaseURL);
	const ollamaHost = ollama.hostname;
	const ollamaPort = Number(ollama.port || (ollama.protocol === 'https:' ? 443 : 80));
	const hits = [];

	const server = createServer((incoming, outgoing) => {
		const chunks = [];
		incoming.on('data', (chunk) => chunks.push(chunk));
		incoming.on('end', () => {
			const body = Buffer.concat(chunks);
			const url = new URL(incoming.url ?? '/', 'http://127.0.0.1');
			hits.push({ method: incoming.method ?? 'GET', path: url.pathname, at: Date.now() });
			if (url.pathname === '/v1/models' || url.pathname.startsWith('/v1/')) {
				const proxy = proxyRequest(
					{
						host: ollamaHost,
						port: ollamaPort,
						method: incoming.method,
						path: url.pathname + url.search,
						headers: { ...incoming.headers, host: `${ollamaHost}:${ollamaPort}` },
					},
					(proxyRes) => {
						outgoing.writeHead(proxyRes.statusCode ?? 502, proxyRes.headers);
						proxyRes.pipe(outgoing);
					},
				);
				proxy.on('error', () => {
					outgoing.writeHead(502, { 'content-type': 'application/json' });
					outgoing.end(JSON.stringify({ ok: false, error: 'bench-server: ollama unreachable' }));
				});
				proxy.end(body);
				return;
			}
			if (url.pathname.startsWith('/tool/weather/')) {
				const city = decodeURIComponent(url.pathname.slice('/tool/weather/'.length));
				outgoing.writeHead(200, { 'content-type': 'application/json' });
				outgoing.end(JSON.stringify({ city, tempC: 19 }));
				return;
			}
			if (url.pathname === '/api/users' || url.pathname === '/api/orders') {
				const kind = url.pathname === '/api/users' ? 'user' : 'order';
				const rows = Array.from({ length: 25 }, (_, i) => ({ id: i + 1, kind, name: `${kind}-${i + 1}` }));
				outgoing.writeHead(200, { 'content-type': 'application/json' });
				outgoing.end(JSON.stringify({ rows }));
				return;
			}
			if (url.pathname === '/api/echo') {
				outgoing.writeHead(200, { 'content-type': 'application/json' });
				outgoing.end(JSON.stringify({ ok: true, method: incoming.method, path: url.pathname }));
				return;
			}
			outgoing.writeHead(404, { 'content-type': 'application/json' });
			outgoing.end(JSON.stringify({ ok: false, error: `unknown bench path ${url.pathname}` }));
		});
	});
	server.listen(0, '127.0.0.1');
	await once(server, 'listening');
	const address = server.address();
	if (!address || typeof address === 'string') throw new Error('bench: bench server failed to start');
	const port = address.port;
	const origin = `http://127.0.0.1:${port}`;
	let closed = false;
	return {
		origin,
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
		},
	};
}

/** Sampled peak RSS (KiB) of a process, via ps. Documented as sampled. */
export async function sampleRssKib(pid) {
	try {
		const { stdout } = await execFileAsync('ps', ['-p', String(pid), '-o', 'rss=']);
		const value = Number(stdout.trim());
		return Number.isFinite(value) && value > 0 ? value : null;
	} catch {
		return null;
	}
}

/**
 * Runs one benchmark workflow N times, sequentially and isolated (no
 * parallel cross-workflow runs — the caller sequences workflows).
 *
 * setup(baseURL) creates the workflow and returns { workflowId, trigger }.
 * trigger(run) starts one run and returns { id, wait() } where wait()
 * resolves to the terminal execution record.
 */
export async function measureWorkflow({ baseURL, pid, key, setup, runs, warmup, maxAttempts = 3 }) {
	const { workflowId, trigger } = await setup(baseURL);

	const execOne = async () => {
		const t0 = performance.now();
		const started = await trigger(baseURL, workflowId);
		const record = await started.wait();
		const t1 = performance.now();
		const rss = await sampleRssKib(pid);
		if (record.status !== 'succeeded') {
			throw new Error(`bench: ${key}: run ended ${record.status}: ${JSON.stringify(record.error ?? record).slice(0, 500)}`);
		}
		let serverMs = null;
		if (record.startedAt && record.finishedAt) {
			serverMs = new Date(record.finishedAt).getTime() - new Date(record.startedAt).getTime();
		}
		return { clientMs: t1 - t0, serverMs, rssKib: rss };
	};

	// Timed slots retry transient failures (model long-tail, GC pause) up to
	// maxAttempts; every extra attempt is counted in retriedRuns so a second
	// operator sees the flakiness instead of a cleaned number. A slot that
	// fails every attempt aborts the benchmark: a persistent failure is a
	// red run, not a slow one.
	const execSlot = async (discard) => {
		let attempts = 0;
		for (;;) {
			attempts++;
			try {
				return { sample: await execOne(), retried: attempts - 1 };
			} catch (error) {
				if (discard || attempts >= maxAttempts) throw error;
			}
		}
	};

	for (let i = 0; i < warmup; i++) await execSlot(true); // JIT/GC/cold-cache, discarded
	const samples = [];
	let peakRssKib = 0;
	let retriedRuns = 0;
	for (let i = 0; i < runs; i++) {
		const { sample, retried } = await execSlot(false);
		retriedRuns += retried;
		samples.push({ clientMs: sample.clientMs, serverMs: sample.serverMs });
		if (sample.rssKib !== null && sample.rssKib > peakRssKib) peakRssKib = sample.rssKib;
	}
	return { workflowId, runs: samples, peakRssKib, retriedRuns };
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

/** Manual-run trigger: POST /workflows/:id/run with an optional input. */
export function manualTrigger(input) {
	return async (baseURL, workflowId) => {
		const response = await fetch(`${baseURL}/api/v1/workflows/${workflowId}/run`, {
			method: 'POST',
			headers: { 'content-type': 'application/json' },
			body: JSON.stringify(input === undefined ? {} : { input }),
		});
		if (response.status !== 202) throw new Error(`run: status ${response.status} (body: ${await response.text()})`);
		const started = await response.json();
		return { id: started.id, wait: () => waitForExecution(baseURL, started.id) };
	};
}

/** Descriptive stats over an array of numbers. */
export function stats(values) {
	const xs = [...values].sort((a, b) => a - b);
	const n = xs.length;
	const min = xs[0];
	const max = xs[n - 1];
	const mean = xs.reduce((a, b) => a + b, 0) / n;
	const variance = xs.reduce((a, b) => a + (b - mean) ** 2, 0) / n;
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

function sysctl(key) {
	return execFileAsync('sysctl', ['-n', key])
		.then(({ stdout }) => stdout.trim())
		.catch(() => null);
}

/** Machine spec + versions + date, recorded into every raw result file. */
export async function environment() {
	const { stdout: uname } = await execFileAsync('uname', ['-smr']).catch(() => ({ stdout: 'unknown' }));
	const [model, memBytes, ncpu] = await Promise.all([
		sysctl('hw.model'),
		sysctl('hw.memsize'),
		sysctl('hw.ncpu'),
	]);
	let kilasflowVersion = 'unknown';
	try {
		const { stdout } = await execFileAsync(join(REPO_DIR, 'bin', 'kilasflow'), ['-version']);
		kilasflowVersion = stdout.trim().split('\n')[0];
	} catch {
		// Recorded as unknown; the commit below still pins the build.
	}
	let commit = 'unknown';
	try {
		commit = (await execFileAsync('git', ['rev-parse', 'HEAD'], { cwd: REPO_DIR })).stdout.trim();
	} catch {
		// Non-git checkout (tarball repro): version above still pins it.
	}
	return {
		date: new Date().toISOString(),
		machine: {
			uname: uname.trim(),
			model: model ?? 'unknown',
			cpuCount: ncpu !== null ? Number(ncpu) : 'unknown',
			memBytes: memBytes !== null ? Number(memBytes) : 'unknown',
		},
		kilasflow: { version: kilasflowVersion, commit },
		node: process.version,
	};
}

/** Direct latency probe of the bench server (engine overhead excluded). */
export async function probeStubLatency(origin, n = 30) {
	const samples = [];
	for (let i = 0; i < n; i++) {
		const t0 = performance.now();
		const res = await fetch(`${origin}/api/echo`);
		await res.text();
		samples.push(performance.now() - t0);
	}
	return stats(samples);
}
