/**
 * Summariser tests for FEAT-8mymac (method version 2).
 *
 * The summariser must be import-safe (run.mjs imports summarize()) and must
 * never let a smoke run overwrite the published SUMMARY.md or docs page.
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, readdir, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { QUIET_LOAD } from './method.mjs';
import { renderTable, rewriteDocsTable, summarize, writeSummary } from './summarise.mjs';

const START = '<!-- bench:table:start -->';
const END = '<!-- bench:table:end -->';
const HERE = dirname(fileURLToPath(import.meta.url));

function statsOf(p50, n = 30) {
	return { n, min: p50 - 2, p50, p95: p50 + 3, max: p50 + 4, mean: p50 + 0.5, stddev: 1.2 };
}

function result(key, name, p50, extra = {}) {
	return {
		key,
		name,
		status: 'measured',
		summary: statsOf(p50),
		serverMs: statsOf(1),
		verdict: { ok: true, reasons: [], robustCv: 0.05, drift: 0.02 },
		peakRssKib: 51200,
		divergence: extra.divergence ?? null,
		...extra,
	};
}

function rawFixture(overrides = {}) {
	return {
		artifact: 'e2e/benchmark/bench-2026-09-20T10-00-00.json',
		method: {
			version: 2,
			runs: 30,
			warmup: 5,
			block: 5,
			seed: 20260920,
			smoke: false,
			primary: 'client-observed HTTP response latency, request sent to full body received (same function on both engines)',
			secondary: 'server execution record startedAt to finishedAt, 1 ms resolution, fetched after the run',
		},
		environment: {
			date: '2026-09-20T10:00:00.000Z',
			machine: { chip: 'Apple M4', model: 'Mac16,13', uname: 'Darwin 25.5.0 arm64', cpuCount: 10, memBytes: 16 * 1024 ** 3 },
			kilasflow: { version: 'kilasflow 0.9.0', commit: 'abc1234', dirty: false },
			node: 'v24.16.0',
			loadAverage: { before: [1.1, 1.4, 1.6], after: [1.2, 1.5, 1.7] },
			power: { source: 'AC Power' },
			thermal: { state: 'nominal' },
			ollama: { version: '0.12.0', model: 'gemma4:12b-mlx', digest: 'sha256:deadbeef' },
			docker: { available: true, serverVersion: '29.4.0', os: 'OrbStack', cpuCount: 10, memBytes: 12 * 1024 ** 3 },
		},
		stubLatency: statsOf(0.18),
		containerStubLatency: statsOf(3.4),
		hopProbe: { n8n: statsOf(1.1), kilasflow: statsOf(0.4) },
		kilasflow: {
			status: 'measured',
			results: [
				result('webhook-set-respond', 'Webhook → Set → Respond', 4.2),
				result('if-fanout', 'IF branch fan-out', 4.4),
				result('paginated-loop', 'Paginated loop (100 items, batch 10)', 18.1),
				result('http-merge-shape', 'HTTP → Merge → shaping', 7.9),
				result('agent-tool-loop', 'AI-agent tool-loop (local Ollama)', 22000, { gatewayOverheadMs: 12.5 }),
				result('code-node', 'Code node (Go/WASM vs JS)', 26.0, { supplementary: true, divergentByDesign: true }),
			],
		},
		n8n: {
			status: 'measured',
			version: '2.33.7',
			image: 'docker.n8n.io/n8nio/n8n:2.33.7',
			digest: 'sha256:3989d9b8ebb77b4ee8f604519eb73e44f4384bfaa689526e0104eed79a237d30',
			containerEnv: ['N8N_DIAGNOSTICS_ENABLED', 'N8N_LOG_LEVEL'],
			results: [
				result('webhook-set-respond', 'Webhook → Set → Respond', 5.1),
				result('if-fanout', 'IF branch fan-out', 5.4),
				result('paginated-loop', 'Paginated loop (100 items, batch 10)', 24.3),
				result('http-merge-shape', 'HTTP → Merge → shaping', 9.6),
				result('agent-tool-loop', 'AI-agent tool-loop (local Ollama)', 24000, { gatewayOverheadMs: 30 }),
			],
		},
		comparison: [
			{ key: 'webhook-set-respond', name: 'Webhook → Set → Respond', kilasP50: 4.2, n8nP50: 5.1, ratio: 1.21, lo: 1.1, hi: 1.33, label: 'n8n slower than KilasFlow', ok: true },
			{ key: 'if-fanout', name: 'IF branch fan-out', kilasP50: 4.4, n8nP50: 5.4, ratio: 1.23, lo: 1.12, hi: 1.35, label: 'n8n slower than KilasFlow', ok: true },
			{ key: 'paginated-loop', name: 'Paginated loop (100 items, batch 10)', kilasP50: 18.1, n8nP50: 24.3, ratio: 1.34, lo: 1.2, hi: 1.5, label: 'n8n slower than KilasFlow', ok: true },
			{ key: 'http-merge-shape', name: 'HTTP → Merge → shaping', kilasP50: 7.9, n8nP50: 9.6, ratio: 1.22, lo: 1.05, hi: 1.4, label: 'n8n slower than KilasFlow', ok: true },
			{ key: 'agent-tool-loop', name: 'AI-agent tool-loop (local Ollama)', kilasP50: 22000, n8nP50: 24000, ratio: 1.09, lo: 0.9, hi: 1.3, label: 'inconclusive (CI [0.9, 1.3] straddles the 0.95–1.05 band)', ok: false },
		],
		control: { ratio: 1.01, lo: 0.98, hi: 1.04, label: 'no meaningful difference' },
		...overrides,
	};
}

test('renders per-engine tables and a comparison table with ratio, CI and verdict', () => {
	const markdown = summarize(rawFixture());
	assert.match(markdown, /KilasFlow \(client-observed HTTP response latency/);
	assert.match(markdown, /n8n \(client-observed HTTP response latency/);
	assert.match(markdown, /Comparison/);
	assert.match(markdown, /1\.21/);
	assert.match(markdown, /\[1\.1, 1\.33\]/);
	assert.match(markdown, /n8n slower than KilasFlow/);
	assert.match(markdown, /inconclusive/);
	// The supplementary Code row is its own table, never in the headline one.
	assert.match(markdown, /Code node \(Go\/WASM vs JS\)/);
	assert.match(markdown, /divergent by design/);
	// The agent row's gateway-attributed overhead is shown, and not gated.
	assert.match(markdown, /gateway/i);
});

test('shows the PRELIMINARY banner only when the n8n half is not measured', () => {
	const measured = summarize(rawFixture());
	assert.doesNotMatch(measured, /PRELIMINARY/);
	const skipped = summarize(
		rawFixture({
			n8n: { status: 'skipped-off', reason: 'BENCH_N8N=off', results: [] },
			comparison: [],
		}),
	);
	assert.match(skipped, /PRELIMINARY/);
	assert.match(skipped, /BENCH_N8N=off/);
});

test('a row that was not measured is named with its reason, on either side', () => {
	const fixture = rawFixture();
	const markdown = summarize(
		rawFixture({
			kilasflow: {
				status: 'measured',
				results: [
					...fixture.kilasflow.results,
					{ key: 'agent-tool-loop', name: 'AI-agent tool-loop (local Ollama)', status: 'skipped-no-model', note: 'ollama pull gemma4:12b-mlx' },
				],
			},
		}),
	);
	assert.match(markdown, /KilasFlow side — rows not measured/);
	assert.match(markdown, /skipped-no-model/);
	assert.match(markdown, /ollama pull gemma4:12b-mlx/);
});

test('a smoke raw file renders a SMOKE banner and refuses to overwrite SUMMARY.md or the docs page', async () => {
	const smoke = rawFixture({ method: { ...rawFixture().method, smoke: true, runs: 4, warmup: 1, block: 2 } });
	assert.match(summarize(smoke), /SMOKE/);

	const dir = await mkdtemp(join(tmpdir(), 'kilasflow-bench-test-'));
	const summaryPath = join(dir, 'SUMMARY.md');
	const docsPath = join(dir, 'benchmark.md');
	await writeFile(summaryPath, 'sentinel summary\n');
	await writeFile(docsPath, `# docs\n\n${START}\nold table\n${END}\n`);

	const outcome = await writeSummary({ raw: smoke, summaryPath, docsPath });
	assert.equal(outcome.written, false);
	assert.match(outcome.reason, /smoke/i);
	assert.equal(await readFile(summaryPath, 'utf-8'), 'sentinel summary\n');
	assert.match(await readFile(docsPath, 'utf-8'), /old table/);
});

test('docs marker rewrite between <!-- bench:table:start --> and <!-- bench:table:end --> is idempotent', async () => {
	const table = renderTable(rawFixture());
	const docs = `# Benchmark\n\nprose before\n\n${START}\nstale table\n${END}\n\nprose after\n`;
	const once = rewriteDocsTable(docs, table);
	assert.match(once, /prose before/);
	assert.match(once, /prose after/);
	assert.doesNotMatch(once, /stale table/);
	assert.match(once, /Webhook → Set → Respond/);
	const twice = rewriteDocsTable(once, table);
	assert.equal(twice, once);
	assert.throws(() => rewriteDocsTable('# no markers here', table), /marker/i);

	// The real write path uses the same rewrite.
	const dir = await mkdtemp(join(tmpdir(), 'kilasflow-bench-test-'));
	const docsPath = join(dir, 'benchmark.md');
	await writeFile(docsPath, docs);
	const written = await writeSummary({
		raw: rawFixture(),
		summaryPath: join(dir, 'SUMMARY.md'),
		docsPath,
	});
	assert.equal(written.written, true);
	assert.equal(await readFile(docsPath, 'utf-8'), once);
});

test('the limits section names what was stubbed, what was excluded and that numbers are not SLAs, single machine', () => {
	const markdown = summarize(rawFixture());
	const limits = markdown.slice(markdown.indexOf('Limits'));
	assert.match(limits, /stub/i);
	assert.match(limits, /excluded/i);
	assert.match(limits, /credentialed nodes/i);
	assert.match(limits, /queue and Postgres/i);
	assert.match(limits, /throughput|concurrency/i);
	assert.match(limits, /cold start/i);
	assert.match(limits, /multi-tenant/i);
	assert.match(limits, /editor latency/i);
	assert.match(limits, /not SLAs/);
	assert.match(limits, /single[- ]machine/i);
	assert.match(limits, /i\.i\.d\./i);
});

test('the header carries the KilasFlow commit, the dirty flag and the n8n version from the raw file', () => {
	const clean = summarize(rawFixture());
	assert.match(clean, /abc1234/);
	assert.match(clean, /2\.33\.7/);
	assert.doesNotMatch(clean, /abc1234-dirty/);
	const dirty = summarize(rawFixture({ environment: { ...rawFixture().environment, kilasflow: { version: 'kilasflow 0.9.0', commit: 'abc1234', dirty: true } } }));
	assert.match(dirty, /abc1234-dirty/);
});

/**
 * The published raw files, newest first: method version 2 and not a smoke run
 * (a smoke run writes nothing, so it can never be the page's source).
 */
async function publishedRaws() {
	const entries = await readdir(HERE);
	const raws = [];
	for (const name of entries) {
		if (!name.startsWith('bench-') || !name.endsWith('.json')) continue;
		try {
			const parsed = JSON.parse(await readFile(join(HERE, name), 'utf-8'));
			if (parsed.method?.version === 2 && parsed.method?.smoke !== true) raws.push({ name, raw: parsed });
		} catch {
			// A half-written artifact is not a published run; the next one is.
		}
	}
	return raws.sort((a, b) => (a.name < b.name ? 1 : -1));
}

/**
 * The page's table is generated, never hand-written: it is the newest
 * published v2 raw file's table, byte for byte, between the markers. This is
 * what stops the docs page from drifting away from the JSON it summarises.
 */
test('docs-table-matches-latest-raw: the docs table is the newest published v2 raw file, regenerated', async () => {
	const docsPath = join(HERE, '..', '..', 'docs', 'src', 'content', 'docs', 'operate', 'benchmark.md');
	const docs = await readFile(docsPath, 'utf-8');
	assert.ok(docs.includes(START) && docs.includes(END), `the docs page must carry the ${START} … ${END} marker pair`);

	const [newest] = await publishedRaws();
	if (!newest) {
		// No non-smoke v2 run has been published yet, so there is no generated
		// table to drift from: the marker pair above is all this can assert.
		return;
	}
	const between = docs.slice(docs.indexOf(START) + START.length, docs.indexOf(END)).trim();
	assert.equal(
		between,
		renderTable(newest.raw).trim(),
		`the docs table is out of date with ${newest.name}: regenerate it with ` +
			`\`node e2e/benchmark/summarise.mjs e2e/benchmark/${newest.name} --docs docs/src/content/docs/operate/benchmark.md\``,
	);
});

test('the summariser prints the Docker spec, the container env keys, the container stub latency and the hop probe', () => {
	const markdown = summarize(rawFixture());
	assert.match(markdown, /Docker: OrbStack 29\.4\.0/);
	assert.match(markdown, /container VM 10 CPUs \/ 12 GiB/);
	assert.match(markdown, /n8n container env keys \(secret and key variables dropped\): N8N_DIAGNOSTICS_ENABLED, N8N_LOG_LEVEL/);
	assert.match(markdown, /Stub latency from inside the container: p50 3\.4 ms/);
	assert.match(markdown, /Hop probe \(trivial handlers\): n8n \/healthz p50 1\.1 ms vs KilasFlow \/api\/v1\/health p50 0\.4 ms/);
	// A KilasFlow-only run has none of those facts and must still render.
	const withoutN8n = summarize(
		rawFixture({
			n8n: { status: 'skipped-off', reason: 'BENCH_N8N=off', results: [] },
			containerStubLatency: null,
			hopProbe: { n8n: null, kilasflow: null },
		}),
	);
	assert.doesNotMatch(withoutN8n, /Stub latency from inside the container/);
	assert.doesNotMatch(withoutN8n, /Hop probe/);
});

test('the gateway-attributed overhead line names the engine, and a shared divergence is listed once', () => {
	const raw = rawFixture();
	for (const side of [raw.kilasflow.results, raw.n8n.results]) {
		side.find((entry) => entry.key === 'agent-tool-loop').divergence = 'DIVERGENCE-A';
	}
	const markdown = summarize(raw);
	assert.equal(markdown.split('DIVERGENCE-A').length - 1, 1, 'two engines with the same divergence must not list it twice');
	assert.match(markdown, /KilasFlow 12\.5 ms/);
	assert.match(markdown, /n8n 30 ms/);
});

test('a noisy pre-run load (1-minute load above 2.0) renders a not-quiet banner', () => {
	const quiet = summarize(rawFixture());
	assert.doesNotMatch(quiet, /not quiet/i);
	assert.equal(QUIET_LOAD, 2);
	const noisy = summarize(
		rawFixture({
			environment: { ...rawFixture().environment, loadAverage: { before: [3.4, 2.9, 2.2], after: [3.1, 3.0, 2.4] } },
		}),
	);
	assert.match(noisy, /not quiet/i);
	assert.match(noisy, /3\.4/);
});
