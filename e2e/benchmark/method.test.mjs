/**
 * Method tests for FEAT-8mymac (method version 2).
 *
 * These pin the pre-registered measurement rules before any n8n data exists:
 * the ABBA schedule, the variance bounds, the bootstrap interval and the
 * full-body equivalence check. Bounds are never tuned after a run.
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';

import {
	BLOCK_SIZE,
	BOOTSTRAP,
	BOUNDS,
	QUIET_LOAD,
	RATIO_BAND,
	assertEquivalent,
	assertNoSecrets,
	bootstrapRatio,
	buildSchedule,
	canonicalize,
	compareLabel,
	controlFromFile,
	gatewayOverhead,
	pairRecords,
	robustSpread,
	verdict,
} from './method.mjs';
import { benchPlan, classifyHit, hitsSince, startBenchServer, stats } from './lib.mjs';

const ENGINES = ['kilasflow', 'n8n'];

/** Collapse a schedule into the engine of each block, in order. */
function blockOrder(schedule) {
	const order = [];
	for (const entry of schedule) {
		const key = `${entry.engine}#${entry.block}`;
		if (order[order.length - 1] !== key) order.push(key);
	}
	return order.map((key) => key.split('#')[0]);
}

function series(base, n, step = 0) {
	return Array.from({ length: n }, (_, i) => base + (i % 2 === 0 ? step : -step));
}

test('buildSchedule gives each engine exactly runs samples in blocks of block size', () => {
	const schedule = buildSchedule({ runs: 10, block: 5, startEngine: 0, engines: ENGINES });
	assert.equal(schedule.length, 20);
	const counts = schedule.reduce((acc, entry) => acc.set(entry.engine, (acc.get(entry.engine) ?? 0) + 1), new Map());
	assert.equal(counts.get('kilasflow'), 10);
	assert.equal(counts.get('n8n'), 10);
	// Every same-engine stretch is a whole number of blocks, and never more
	// than two: that is what makes the schedule counterbalanced.
	const stretch = [];
	for (const entry of schedule) {
		if (stretch[stretch.length - 1]?.engine === entry.engine) stretch[stretch.length - 1].length += 1;
		else stretch.push({ engine: entry.engine, length: 1 });
	}
	for (const run of stretch) {
		assert.equal(run.length % 5, 0, `${run.engine} ran ${run.length} consecutive samples, not a whole block`);
		assert.ok(run.length <= 2 * 5, `${run.engine} ran ${run.length} consecutive samples, more than two blocks`);
	}
	// Block numbering is per engine, ascending, and slots carry their index.
	for (const engine of ENGINES) {
		const own = schedule.filter((e) => e.engine === engine);
		assert.deepEqual(own.map((e) => e.index), Array.from({ length: 10 }, (_, i) => i));
		assert.deepEqual(own.map((e) => e.block), [0, 0, 0, 0, 0, 1, 1, 1, 1, 1]);
	}
});

test('buildSchedule counterbalances ABBA so no engine gets more than two consecutive blocks', () => {
	const schedule = buildSchedule({ runs: 10, block: 5, startEngine: 0, engines: ENGINES });
	assert.deepEqual(blockOrder(schedule), ['kilasflow', 'n8n', 'n8n', 'kilasflow']);
	const runs = [];
	for (const entry of schedule) {
		if (runs[runs.length - 1]?.engine === entry.engine) runs[runs.length - 1].length += 1;
		else runs.push({ engine: entry.engine, length: 1 });
	}
	for (const run of runs) assert.ok(run.length <= 2 * BLOCK_SIZE, `${run.engine} ran ${run.length} samples unbroken`);
});

test('buildSchedule startEngine flips with the workflow index', () => {
	const even = buildSchedule({ runs: 10, block: 5, startEngine: 0, engines: ENGINES });
	const odd = buildSchedule({ runs: 10, block: 5, startEngine: 1, engines: ENGINES });
	assert.deepEqual(blockOrder(even), ['kilasflow', 'n8n', 'n8n', 'kilasflow']);
	assert.deepEqual(blockOrder(odd), ['n8n', 'kilasflow', 'kilasflow', 'n8n']);
});

test('buildSchedule rejects runs that are not a multiple of block', () => {
	assert.throws(() => buildSchedule({ runs: 7, block: 5, startEngine: 0, engines: ENGINES }), /multiple of block/);
	assert.throws(() => buildSchedule({ runs: 0, block: 5, startEngine: 0, engines: ENGINES }), /multiple of block/);
	assert.throws(() => buildSchedule({ runs: 10, block: 5, startEngine: 0, engines: [] }), /one or two engines/);
	assert.throws(() => buildSchedule({ runs: 10, block: 5, startEngine: 0, engines: [...ENGINES, 'third'] }), /one or two engines/);
});

test('buildSchedule emits a single engine block by block, with no swap to make', () => {
	// The stage-1 preliminary run measures KilasFlow alone; with one engine
	// there is no order bias to cancel, but the blocks are still numbered so
	// drift in the run order stays visible.
	const schedule = buildSchedule({ runs: 10, block: 5, startEngine: 0, engines: ['kilasflow'] });
	assert.equal(schedule.length, 10);
	assert.deepEqual(schedule.map((entry) => entry.block), [0, 0, 0, 0, 0, 1, 1, 1, 1, 1]);
	assert.deepEqual(schedule.map((entry) => entry.index), [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]);
});

test('benchPlan floors runs at 30, rounds up to a multiple of the block, and BENCH_SMOKE gives runs 4 block 2 warmup 1 with smoke true', () => {
	const base = benchPlan({});
	assert.equal(base.runs, 30);
	assert.equal(base.block, BLOCK_SIZE);
	assert.equal(base.warmup, 5);
	assert.equal(base.smoke, false);
	assert.equal(benchPlan({ BENCH_RUNS: '4' }).runs, 30, 'floors at the ticket contract of 30');
	assert.equal(benchPlan({ BENCH_RUNS: '31' }).runs, 35, 'rounds up to a whole number of blocks');
	assert.equal(benchPlan({ BENCH_RUNS: '30', BENCH_WARMUP: '0' }).warmup, 0);

	const smoke = benchPlan({ BENCH_SMOKE: '1' });
	assert.equal(smoke.runs, 4);
	assert.equal(smoke.block, 2);
	assert.equal(smoke.warmup, 1);
	assert.equal(smoke.smoke, true);
	assert.ok(smoke.outDir.startsWith(tmpdir()), `smoke output dir ${smoke.outDir} must sit under the OS temp dir`);
	assert.ok(smoke.outDir.includes('kilasflow-bench'));
});

test('robustSpread of a constant series is zero', () => {
	const spread = robustSpread([5, 5, 5, 5, 5]);
	assert.equal(spread.median, 5);
	assert.equal(spread.mad, 0);
	assert.equal(spread.robustCv, 0);
});

test('verdict passes a stable series', () => {
	const result = verdict(series(10, 30, 0.1));
	assert.equal(result.ok, true);
	assert.deepEqual(result.reasons, []);
	assert.ok(result.robustCv <= BOUNDS.robustCv);
	assert.ok(result.drift <= BOUNDS.drift);
});

test('verdict marks a noisy series inconclusive and names robustCv', () => {
	const noisy = [10, 40, 12, 60, 11, 55, 10, 44, 13, 51, 10, 48, 12, 57, 11, 46];
	const result = verdict(noisy);
	assert.equal(result.ok, false);
	assert.ok(result.reasons.some((reason) => reason.includes('robustCv')), `reasons = ${JSON.stringify(result.reasons)}`);
});

test('verdict marks a drifting series inconclusive and names drift', () => {
	const drifting = [...Array(8).fill(10), ...Array(8).fill(14)];
	const result = verdict(drifting);
	assert.equal(result.ok, false);
	assert.ok(result.reasons.some((reason) => reason.includes('drift')), `reasons = ${JSON.stringify(result.reasons)}`);
});

test('verdict computes drift on medians, so one spike cannot fake drift', () => {
	// A single slow sample in the second half: the means differ by a lot, the
	// medians do not. Drift is a systematic shift, not one outlier.
	const spiked = [10, 10, 10, 10, 10, 1000];
	assert.ok(!verdict(spiked).reasons.includes('drift'), 'a lone spike is not drift');
	// The same series judged on means would flag it, which is why medians are used.
	const first = [10, 10, 10];
	const second = [10, 10, 1000];
	const mean = (xs) => xs.reduce((a, b) => a + b, 0) / xs.length;
	assert.ok(Math.abs(mean(second) - mean(first)) / mean([...first, ...second]) > BOUNDS.drift);
});

test('bootstrapRatio of identical samples brackets 1', () => {
	const samples = series(10, 30, 0.2);
	const { ratio, lo, hi } = bootstrapRatio(samples, [...samples]);
	assert.equal(ratio, 1);
	assert.ok(lo <= 1 && hi >= 1, `CI [${lo}, ${hi}] must bracket 1`);
});

test('bootstrapRatio of a 2x slower series excludes 1 and is deterministic for the fixed seed', () => {
	const kilas = series(10, 30, 0.2);
	const n8n = kilas.map((x) => x * 2);
	const first = bootstrapRatio(kilas, n8n);
	assert.ok(first.ratio > 1.9 && first.ratio < 2.1, `ratio = ${first.ratio}`);
	assert.ok(first.lo > 1, `CI [${first.lo}, ${first.hi}] must exclude 1`);
	assert.deepEqual(first, bootstrapRatio(kilas, n8n), 'same seed, same interval');
	assert.equal(BOOTSTRAP.seed, 20260920);
});

test('compareLabel returns no meaningful difference when the whole CI sits inside 0.95-1.05, faster/slower when the whole CI is outside the band on one side, and inconclusive naming the reason when either side failed its bound or the CI straddles a band edge', () => {
	assert.equal(RATIO_BAND[0], 0.95);
	assert.equal(RATIO_BAND[1], 1.05);
	assert.match(compareLabel({ ratio: 1.0, lo: 0.97, hi: 1.03 }), /no meaningful difference/);
	assert.match(compareLabel({ ratio: 1.4, lo: 1.2, hi: 1.6 }), /slower/);
	assert.match(compareLabel({ ratio: 0.7, lo: 0.6, hi: 0.8 }), /faster/);
	const straddle = compareLabel({ ratio: 1.02, lo: 0.98, hi: 1.12 });
	assert.match(straddle, /inconclusive/);
	assert.match(straddle, /straddle/i);
	const failed = compareLabel({ ratio: 1.4, lo: 1.2, hi: 1.6, kilasVerdict: { ok: false, reasons: ['robustCv'] } });
	assert.match(failed, /inconclusive/);
	assert.match(failed, /robustCv/);
	const n8nFailed = compareLabel({ ratio: 1.4, lo: 1.2, hi: 1.6, n8nVerdict: { ok: false, reasons: ['drift'] } });
	assert.match(n8nFailed, /inconclusive/);
	assert.match(n8nFailed, /drift/);
});

test('pairRecords pairs by start time, accepts a failed record only for an attempt flagged as an agent retry, and throws on a count or status mismatch otherwise', () => {
	const attempts = [{ attempt: 1 }, { attempt: 2 }, { attempt: 3, agentRetry: true }];
	const records = [
		{ id: 'a', status: 'succeeded', startedAt: '2026-09-20T10:00:00.100Z', finishedAt: '2026-09-20T10:00:00.104Z' },
		{ id: 'b', status: 'success', startedAt: '2026-09-20T10:00:01.000Z', finishedAt: '2026-09-20T10:00:01.003Z' },
		{ id: 'c', status: 'failed', startedAt: '2026-09-20T10:00:02.000Z', finishedAt: '2026-09-20T10:00:02.009Z' },
	];
	const paired = pairRecords(attempts, records);
	assert.equal(paired.length, 3);
	assert.deepEqual(paired.map((p) => p.attempt), [1, 2, 3]);
	assert.equal(paired[0].serverMs, 4);
	assert.equal(paired[2].status, 'failed');
	// Records are ordered by startedAt, not by the order the API returned them.
	const shuffled = pairRecords(attempts, [records[2], records[0], records[1]]);
	assert.deepEqual(shuffled.map((p) => p.serverMs), [4, 3, 9]);
	assert.throws(() => pairRecords(attempts, records.slice(0, 2)), /count/i);
	assert.throws(() => pairRecords([{ attempt: 1 }], [records[2]]), /status|failed/i);
	assert.throws(() => pairRecords([{ attempt: 1 }, { attempt: 2 }], [records[0], records[0]]), /startedAt|duplicate/i);
});

test('assertEquivalent checks item count, deep-equals the canonicalised (sorted-key) body against expect.body when given, checks contains/spot values, and rejects a wrong count or a wrong body', () => {
	const expect = { items: 1, body: [{ v: 'bench-ok' }], stubCalls: 0 };
	assert.deepEqual(canonicalize({ b: 1, a: { d: 2, c: 3 } }), { a: { c: 3, d: 2 }, b: 1 });
	assert.deepEqual(canonicalize([{ b: 1, a: 2 }]), [{ a: 2, b: 1 }]);
	assertEquivalent([{ v: 'bench-ok' }], expect, 'webhook-set-respond');
	assertEquivalent([{ v: 'bench-ok' }], expect, 'webhook-set-respond');
	assert.throws(() => assertEquivalent([], expect, 'webhook-set-respond'), /items|count/i);
	assert.throws(() => assertEquivalent([{ v: 'nope' }], expect, 'webhook-set-respond'), /body/i);
	// Key order in the actual body must not matter.
	assertEquivalent([{ v: 'bench-ok', extra: undefined }], expect, 'row');
	// A declared contains value is checked on the serialised body.
	assertEquivalent([{ text: 'temp 19C' }], { items: 1, contains: '19', stubCalls: 0 }, 'agent-tool-loop');
	assert.throws(() => assertEquivalent([{ text: 'temp 12C' }], { items: 1, contains: '19', stubCalls: 0 }, 'agent'), /contains/i);
});

test('assertNoSecrets throws when a secret substring is present and passes otherwise', () => {
	const secret = 'Kf' + 'deadbeef' + 'Z9';
	assert.throws(() => assertNoSecrets([`login body {"password":"${secret}"}`], [secret]), /secret/i);
	assert.throws(() => assertNoSecrets(`email=bench@bench.example token=${secret}`, [secret, 'bench@bench.example']), /secret/i);
	// The message names the field, never the value.
	try {
		assertNoSecrets([`password ${secret}`], [secret]);
	} catch (error) {
		assert.ok(!String(error.message).includes(secret), 'the error must not echo the secret');
	}
	assert.doesNotThrow(() => assertNoSecrets(['nothing to see'], [secret]));
	// Empty secrets (unset env) must not make every text match.
	assert.doesNotThrow(() => assertNoSecrets(['anything'], ['', undefined]));
});

test('classifyHit says stub, tool, model or other, and hitsSince attributes hits by index window including per-hit durMs', () => {
	assert.equal(classifyHit('/api/users'), 'stub');
	assert.equal(classifyHit('/api/orders'), 'stub');
	assert.equal(classifyHit('/api/echo'), 'stub');
	assert.equal(classifyHit('/tool/weather/Utrecht'), 'tool');
	assert.equal(classifyHit('/v1/chat/completions'), 'model');
	assert.equal(classifyHit('/api/chat'), 'model');
	assert.equal(classifyHit('/api/tags'), 'model');
	assert.equal(classifyHit('/api/generate'), 'model');
	assert.equal(classifyHit('/something-else'), 'other');

	const hits = [
		{ path: '/api/users', durMs: 1 },
		{ path: '/v1/chat/completions', durMs: 5 },
		{ path: '/tool/weather/Utrecht', durMs: 2 },
	];
	const window = hitsSince(hits, 1);
	assert.equal(window.length, 2);
	assert.deepEqual(window.map((h) => h.durMs), [5, 2]);
	assert.equal(hitsSince(hits, 3).length, 0);
});

test('gatewayOverhead is responseMs minus summed gateway-observed model and tool durations and never negative', () => {
	const hits = [
		{ path: '/v1/chat/completions', durMs: 5 },
		{ path: '/tool/weather/Utrecht', durMs: 2 },
		{ path: '/api/users', durMs: 1 },
	];
	assert.equal(gatewayOverhead(20, hits), 13);
	assert.equal(gatewayOverhead(3, hits), 0, 'clamped at zero when the model outran the response clock');
	assert.equal(gatewayOverhead(20, []), 20);
});

test('stats matches known min/p50/p95/max/mean/stddev', () => {
	// Nearest-rank percentiles, population (not sample) stddev.
	const s = stats([1, 2, 3, 4, 5]);
	assert.equal(s.n, 5);
	assert.equal(s.min, 1);
	assert.equal(s.p50, 3);
	assert.equal(s.p95, 5);
	assert.equal(s.max, 5);
	assert.equal(s.mean, 3);
	assert.equal(s.stddev, 1.41);
	const t = stats([10, 20, 30, 40, 50, 60, 70, 80, 90, 100]);
	assert.equal(t.p50, 50);
	assert.equal(t.p95, 100);
	assert.equal(t.mean, 55);
	assert.equal(QUIET_LOAD, 2);
});

/**
 * The published comparison carries its own A/A negative control, so the
 * summary renders `ratio (95% CI [lo, hi]) — label` from the main run's raw
 * file. A control measured in a separate short run (BENCH_CONTROL=aa) reaches
 * the published file through BENCH_CONTROL_FILE; a missing, truncated or
 * numberless file must be refused with a reason rather than attached, because
 * `undefined (95% CI [undefined, undefined])` in a published summary is worse
 * than no control line at all.
 */
test('controlFromFile accepts a measured A/A summary and refuses a malformed one with a reason', () => {
	const measured = { ratio: 1.02, lo: 0.88, hi: 1.19, label: 'no meaningful difference', kilasP50: 5.1, controlP50: 5.2 };
	const ok = controlFromFile(JSON.stringify(measured));
	assert.equal(ok.ok, true);
	assert.deepEqual(ok.control, measured);

	const wrapped = controlFromFile(JSON.stringify({ control: measured }));
	assert.equal(wrapped.ok, true, 'a raw file is accepted as well as a bare summary');
	assert.equal(wrapped.control.ratio, 1.02);

	const truncated = controlFromFile('{"ratio": 1.02, "lo": 0.88');
	assert.equal(truncated.ok, false);
	assert.match(truncated.reason, /JSON/i);

	const numberless = controlFromFile(JSON.stringify({ ratio: 1.02, lo: 0.88, label: 'no meaningful difference' }));
	assert.equal(numberless.ok, false);
	assert.match(numberless.reason, /hi/);

	const unlabelled = controlFromFile(JSON.stringify({ ratio: 1.02, lo: 0.88, hi: 1.19 }));
	assert.equal(unlabelled.ok, false);
	assert.match(unlabelled.reason, /label/);
});

/**
 * `durMs` is "request received to response finished". Ollama streams its
 * tokens, so a hit recorded when the response *headers* arrive would report
 * the time to the first byte and the engine would be charged with the whole
 * token stream as its own overhead — which is exactly what the agent row's
 * gateway attribution must not do.
 */
test('a streamed proxied hit records durMs to the last byte, not the first', async () => {
	const upstream = createServer((_request, response) => {
		response.writeHead(200, { 'content-type': 'application/json' });
		response.write('{"models":');
		setTimeout(() => response.end('[]}'), 300);
	});
	upstream.listen(0, '127.0.0.1');
	await once(upstream, 'listening');
	const upstreamPort = upstream.address().port;
	const bench = await startBenchServer(`http://127.0.0.1:${upstreamPort}`);
	try {
		const mark = bench.hits.length;
		const response = await fetch(`${bench.origin}/api/tags`);
		assert.equal(await response.text(), '{"models":[]}', 'the proxied body is byte-transparent');
		const [hit] = hitsSince(bench.hits, mark);
		assert.equal(hit.path, '/api/tags');
		assert.ok(hit.durMs >= 250, `durMs ${hit.durMs} must cover the streamed body, not just its headers`);
	} finally {
		await bench.close();
		upstream.close();
		await once(upstream, 'close').catch(() => undefined);
	}
});
