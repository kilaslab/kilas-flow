/**
 * The measurement method for FEAT-8mymac, version 2.
 *
 * Everything here is pure and pre-registered: the ABBA schedule, the variance
 * bounds, the bootstrap interval and the ratio band were fixed in the commit
 * that introduced this file, before any n8n number existed, and are never
 * tuned after seeing data. run.mjs is the only caller that talks to a machine.
 *
 * What changed from method v1, and why (numbers from
 * e2e/benchmark/bench-2026-09-06T08-07-31.json):
 *   - v1 timed `deliver → poll for a new execution record → wait for terminal`,
 *     so every sample carried a 25 ms sleep (run.mjs:77, lib.mjs:307) and a
 *     listExecutionIds call (run.mjs:84) inside the timed region. The webhook
 *     row read p50 30.47 ms against a 1 ms server record. deliverTimed() is now
 *     the ONLY timing code and brackets the request and the response body.
 *   - v1's two rows containing a Go/WASM Code node cost 29–42 ms server-side
 *     against 1 ms for the Code-free rows, so "the timed region is the batch
 *     loop, not the seed" was false. Rows 1–4 are Code-free; Code cost is a
 *     labelled supplementary row.
 *   - v1 asserted item counts and one spot value. assertEquivalent() now
 *     deep-equals the canonicalised body, which is what catches a fixture that
 *     is not the same work on both engines.
 *
 * v1 raw files stay in the tree as history and are not comparable with v2.
 */

import { createHash } from 'node:crypto';

/** Timed samples per engine per block. Blocks are the unit the schedule swaps on. */
export const BLOCK_SIZE = 5;

/**
 * Pre-registered bounds. A row that fails one is published as inconclusive
 * with the reason; the bound is never relaxed to make a row green.
 *
 * robustCv: 1.4826 * MAD / median. MAD is used rather than stddev because a
 * single GC pause inflates a standard deviation and would fail every row on a
 * busy machine; the median absolute deviation ignores it.
 * absNoiseMs: below half a millisecond of absolute spread there is nothing to
 * compare, so the relative bound is not applied.
 * drift: relative shift between the medians of the first and second half of
 * the run order — the ABBA schedule exists to cancel it, so any that remains
 * is reported rather than averaged away.
 */
export const BOUNDS = { robustCv: 0.3, absNoiseMs: 0.5, drift: 0.2 };

/** A ratio whose whole 95% CI sits inside this band is "no meaningful difference". */
export const RATIO_BAND = [0.95, 1.05];

/** Bootstrap settings. The seed is fixed so two operators get the same interval. */
export const BOOTSTRAP = { resamples: 2000, seed: 20260920 };

/** 1-minute load above this renders a "not quiet" banner. Banner only, never a bound. */
export const QUIET_LOAD = 2.0;

/**
 * The one timing function, used for both engines.
 *
 * It brackets exactly the HTTP request and the reading of the full response
 * body: no polling, no API call, no parsing, no assertion inside the window.
 * Both engines answer a webhook delivery synchronously (KilasFlow's
 * responseNode mode writes the Respond node's body to the caller, and so does
 * n8n's), so this is the same work on both sides.
 */
export async function deliverTimed(url, body) {
	const started = performance.now();
	const response = await fetch(url, {
		method: 'POST',
		headers: { 'content-type': 'application/json' },
		body: JSON.stringify(body ?? {}),
	});
	const buffer = await response.arrayBuffer();
	const responseMs = performance.now() - started;
	return { status: response.status, text: new TextDecoder().decode(buffer), responseMs };
}

/**
 * The ABBA schedule.
 *
 * `runs` samples per engine, in blocks of `block`, and pairs of blocks are
 * emitted as [first, second] on even pairs and [second, first] on odd ones:
 * A B B A A B B A … No engine ever runs more than two consecutive blocks, so a
 * machine that gets slower halfway through penalises both engines equally.
 *
 * `startEngine` is the workflow index parity, so the engine that opens a
 * workflow alternates between workflows.
 *
 * A single engine (the KilasFlow-only preliminary run) has no order bias to
 * cancel, so its samples are emitted block by block in order; the block
 * numbering still lets verdict() see drift in the run order.
 *
 * Returns one entry per timed slot, in run order.
 */
export function buildSchedule({ runs, block, startEngine, engines }) {
	if (!Array.isArray(engines) || engines.length < 1 || engines.length > 2) {
		throw new Error('buildSchedule: one or two engines are required');
	}
	if (!Number.isInteger(block) || block < 1) throw new Error('buildSchedule: block must be a positive integer');
	if (!Number.isInteger(runs) || runs < 1 || runs % block !== 0) {
		throw new Error(`buildSchedule: runs must be a positive multiple of block (got runs=${runs}, block=${block})`);
	}
	const blocksPerEngine = runs / block;
	const schedule = [];
	const indexes = new Map(engines.map((engine) => [engine, 0]));
	const blockNumbers = new Map(engines.map((engine) => [engine, 0]));
	const emitBlock = (engine) => {
		const blockNumber = blockNumbers.get(engine);
		for (let slot = 0; slot < block; slot++) {
			schedule.push({ engine, block: blockNumber, index: indexes.get(engine) });
			indexes.set(engine, indexes.get(engine) + 1);
		}
		blockNumbers.set(engine, blockNumber + 1);
	};
	if (engines.length === 1) {
		for (let blockNumber = 0; blockNumber < blocksPerEngine; blockNumber++) emitBlock(engines[0]);
		return schedule;
	}
	const [first, second] = engines;
	for (let pair = 0; pair < blocksPerEngine; pair++) {
		const order = pair % 2 === startEngine ? [first, second] : [second, first];
		for (const engine of order) emitBlock(engine);
	}
	return schedule;
}

function median(values) {
	if (values.length === 0) return null;
	const sorted = [...values].sort((a, b) => a - b);
	const middle = sorted.length >> 1;
	return sorted.length % 2 === 1 ? sorted[middle] : (sorted[middle - 1] + sorted[middle]) / 2;
}

/** Median, median absolute deviation, and the MAD-derived relative spread. */
export function robustSpread(values) {
	if (values.length === 0) return { median: null, mad: null, robustCv: 0 };
	const centre = median(values);
	const deviations = values.map((value) => Math.abs(value - centre));
	const mad = median(deviations);
	const robustCv = centre > 0 ? (1.4826 * mad) / centre : 0;
	return { median: centre, mad, robustCv };
}

/**
 * Whether a row's samples are stable enough to compare.
 *
 * Drift is the relative shift between the medians of the two halves of the run
 * order, not the means: one slow sample in the second half moves a mean and
 * would fake a systematic shift that is not there.
 */
export function verdict(xsInRunOrder) {
	const values = [...xsInRunOrder];
	const spread = robustSpread(values);
	const half = values.length >> 1;
	const firstHalf = median(values.slice(0, half));
	const secondHalf = median(values.slice(half));
	const centre = spread.median;
	const drift = centre > 0 && firstHalf !== null && secondHalf !== null ? Math.abs(secondHalf - firstHalf) / centre : 0;
	const reasons = [];
	if (!(spread.robustCv <= BOUNDS.robustCv || spread.mad <= BOUNDS.absNoiseMs)) {
		reasons.push(`robustCv ${round4(spread.robustCv)} > ${BOUNDS.robustCv} and MAD ${round4(spread.mad)}ms > ${BOUNDS.absNoiseMs}ms`);
	}
	if (!(drift <= BOUNDS.drift)) {
		reasons.push(`drift ${round4(drift)} > ${BOUNDS.drift}`);
	}
	return { ok: reasons.length === 0, reasons, robustCv: spread.robustCv, mad: spread.mad, drift, median: centre };
}

function round4(value) {
	return value === null || value === undefined ? value : Math.round(value * 10000) / 10000;
}

/** Seeded mulberry32, so the bootstrap interval is reproducible. */
function mulberry32(seed) {
	let state = seed >>> 0;
	return () => {
		state = (state + 0x6d2b79f5) >>> 0;
		let t = state;
		t = Math.imul(t ^ (t >>> 15), t | 1);
		t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
}

/**
 * Bootstrap 95% interval for median(n8n) / median(kilas).
 *
 * The i.i.d. resample assumption is stated in the summary's limits: it treats
 * the samples within a row as exchangeable, which the ABBA schedule tries to
 * make true and which a drifting machine breaks.
 */
export function bootstrapRatio(kilasSamples, n8nSamples) {
	const random = mulberry32(BOOTSTRAP.seed);
	const ratio = median(n8nSamples) / median(kilasSamples);
	const ratios = [];
	for (let resample = 0; resample < BOOTSTRAP.resamples; resample++) {
		const kilasDraw = [];
		for (let i = 0; i < kilasSamples.length; i++) kilasDraw.push(kilasSamples[Math.floor(random() * kilasSamples.length)]);
		const n8nDraw = [];
		for (let i = 0; i < n8nSamples.length; i++) n8nDraw.push(n8nSamples[Math.floor(random() * n8nSamples.length)]);
		const drawRatio = median(n8nDraw) / median(kilasDraw);
		if (Number.isFinite(drawRatio)) ratios.push(drawRatio);
	}
	ratios.sort((a, b) => a - b);
	const quantile = (q) => ratios[Math.min(ratios.length - 1, Math.max(0, Math.ceil(q * ratios.length) - 1))];
	return { ratio: round4(ratio), lo: round4(quantile(0.025)), hi: round4(quantile(0.975)) };
}

/**
 * The published label for one row.
 *
 * A ratio above 1 means n8n took longer than KilasFlow. The label is
 * deliberately worded from the engines' point of view, never as a win.
 */
export function compareLabel({ ratio, lo, hi, kilasVerdict, n8nVerdict }) {
	const failed = [];
	if (kilasVerdict && kilasVerdict.ok === false) failed.push(`KilasFlow variance bound failed (${(kilasVerdict.reasons ?? []).join('; ')})`);
	if (n8nVerdict && n8nVerdict.ok === false) failed.push(`n8n variance bound failed (${(n8nVerdict.reasons ?? []).join('; ')})`);
	if (failed.length > 0) return `inconclusive (${failed.join('; ')})`;
	if (lo >= RATIO_BAND[1]) return 'n8n slower than KilasFlow';
	if (hi <= RATIO_BAND[0]) return 'n8n faster than KilasFlow';
	if (lo >= RATIO_BAND[0] && hi <= RATIO_BAND[1]) return 'no meaningful difference';
	return `inconclusive (CI [${lo}, ${hi}] straddles the ${RATIO_BAND[0]}–${RATIO_BAND[1]} band edge)`;
}

/**
 * Pair delivery attempts with execution records.
 *
 * One record per attempt: warm-ups, timed runs and agent retries all produce
 * an execution, so the counts must agree. Records are ordered by startedAt
 * because a paginated listing is not guaranteed to be in run order. A failed
 * record is tolerated only for an attempt explicitly flagged as an agent
 * retry (a retry that failed is exactly why it was retried); any other
 * failure is a red run, not a slow one.
 *
 * serverMs is the execution record's own clock at 1 ms resolution.
 */
export function pairRecords(attempts, records) {
	if (!Array.isArray(attempts) || !Array.isArray(records)) throw new Error('pairRecords: attempts and records must be arrays');
	if (attempts.length !== records.length) {
		throw new Error(`pairRecords: count mismatch — ${attempts.length} attempts, ${records.length} records`);
	}
	const sorted = [...records].sort((a, b) => {
		const left = Date.parse(a?.startedAt ?? '');
		const right = Date.parse(b?.startedAt ?? '');
		if (!Number.isFinite(left) || !Number.isFinite(right)) throw new Error('pairRecords: a record has no parsable startedAt');
		return left - right;
	});
	for (let i = 1; i < sorted.length; i++) {
		if (sorted[i].startedAt === sorted[i - 1].startedAt && sorted[i].id === sorted[i - 1].id) {
			throw new Error('pairRecords: duplicate record in the listing');
		}
	}
	return attempts.map((attempt, index) => {
		const record = sorted[index];
		const status = record.status;
		const succeeded = status === 'succeeded' || status === 'success';
		if (!succeeded && !attempt.agentRetry) {
			throw new Error(`pairRecords: attempt ${attempt.attempt ?? index + 1} ended ${status}, which is only tolerated for an agent retry`);
		}
		let serverMs = null;
		if (record.startedAt && record.finishedAt) {
			serverMs = new Date(record.finishedAt).getTime() - new Date(record.startedAt).getTime();
		}
		return { attempt: attempt.attempt ?? index + 1, status, serverMs, executionId: record.id ?? null };
	});
}

/** Sorted-key, JSON-safe clone: two bodies that differ only in key order are equal. */
export function canonicalize(value) {
	if (Array.isArray(value)) return value.map(canonicalize);
	if (value && typeof value === 'object') {
		const out = {};
		for (const key of Object.keys(value).sort()) {
			if (value[key] === undefined) continue;
			out[key] = canonicalize(value[key]);
		}
		return out;
	}
	return value;
}

function describe(value) {
	const text = JSON.stringify(canonicalize(value));
	return text === undefined ? String(value) : text.length > 400 ? `${text.slice(0, 400)}…` : text;
}

/**
 * Full-body equivalence for one run.
 *
 * Count, canonicalised body and declared spot values. The body comparison is
 * the point: v1 checked a count and one value, which passed while the two
 * engines were returning different objects.
 */
export function assertEquivalent(actual, expect, label = 'row') {
	const items = Array.isArray(actual) ? actual : [actual];
	if (typeof expect.items === 'number' && items.length !== expect.items) {
		throw new Error(`${label}: items ${items.length}, expected ${expect.items} (body: ${describe(actual)})`);
	}
	if (expect.body !== undefined) {
		const got = JSON.stringify(canonicalize(items));
		const want = JSON.stringify(canonicalize(expect.body));
		if (got !== want) throw new Error(`${label}: body mismatch\n  got  ${got.slice(0, 600)}\n  want ${want.slice(0, 600)}`);
	}
	if (expect.contains !== undefined) {
		const serialized = JSON.stringify(canonicalize(items));
		if (!serialized.includes(expect.contains)) {
			throw new Error(`${label}: contains check failed — the body does not contain ${JSON.stringify(expect.contains)} (body: ${describe(actual)})`);
		}
	}
	return items;
}

/**
 * Refuse to write a secret anywhere.
 *
 * Called before every file write, including the docs page. The message names
 * the field, never the value: an error that echoes the secret writes it to a
 * log.
 */
export function assertNoSecrets(texts, secrets) {
	const needles = (Array.isArray(secrets) ? secrets : [secrets]).filter((value) => typeof value === 'string' && value.length > 0);
	const bodies = Array.isArray(texts) ? texts : [texts];
	for (const body of bodies) {
		if (typeof body !== 'string') continue;
		for (const needle of needles) {
			if (body.includes(needle)) {
				throw new Error(`secret material found in a file that was about to be written (${needle.length} characters); it was not written`);
			}
		}
	}
}

/**
 * Engine overhead on a row whose response is dominated by the model: the
 * response clock minus every gateway-observed model and tool duration. Clamped
 * at zero, because the gateway clock and the client clock are different clocks.
 */
export function gatewayOverhead(responseMs, hits) {
	const observed = (hits ?? [])
		.filter((hit) => classifyHitKind(hit.path) === 'model' || classifyHitKind(hit.path) === 'tool')
		.reduce((sum, hit) => sum + (Number(hit.durMs) || 0), 0);
	return Math.max(0, responseMs - observed);
}

// Kept here so gatewayOverhead stays pure; lib.mjs re-exports the same rules.
function classifyHitKind(path) {
	if (typeof path !== 'string') return 'other';
	if (path === '/api/users' || path === '/api/orders' || path === '/api/echo') return 'stub';
	if (path.startsWith('/tool/')) return 'tool';
	if (path.startsWith('/v1/') || path === '/api/chat' || path === '/api/generate' || path === '/api/tags' || path === '/api/show' || path === '/api/version' || path === '/api/ps') return 'model';
	return 'other';
}

/** Stable short digest, used to record what the harness ran. */
export function digest(value) {
	return createHash('sha256').update(typeof value === 'string' ? value : JSON.stringify(canonicalize(value))).digest('hex').slice(0, 16);
}

/**
 * Read back an A/A control summary measured by a separate short run.
 *
 * The published comparison carries its own negative control so the summary can
 * render `ratio (95% CI [lo, hi]) — label` beside the rows, and the control
 * short run hands that block over through BENCH_CONTROL_FILE. Anything that is
 * not a complete measured summary (a truncated write, a numberless object, a
 * missing label) is refused with a reason: a published summary saying
 * `undefined (95% CI [undefined, undefined])` is worse than no control line.
 */
export function controlFromFile(text) {
	let parsed;
	try {
		parsed = JSON.parse(text);
	} catch (error) {
		return { ok: false, reason: `not JSON: ${error instanceof Error ? error.message : String(error)}` };
	}
	const control = parsed?.control ?? parsed;
	if (!control || typeof control !== 'object') return { ok: false, reason: 'no control object' };
	for (const field of ['ratio', 'lo', 'hi']) {
		if (!Number.isFinite(control[field])) return { ok: false, reason: `control.${field} is not a finite number` };
	}
	if (typeof control.label !== 'string' || control.label === '') return { ok: false, reason: 'control.label is missing' };
	return {
		ok: true,
		control: {
			ratio: control.ratio,
			lo: control.lo,
			hi: control.hi,
			label: control.label,
			kilasP50: control.kilasP50 ?? null,
			controlP50: control.controlP50 ?? null,
		},
	};
}
