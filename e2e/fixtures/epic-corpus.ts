// FEAT-5fhj6p stage 2: re-measure the n8n corpus against the IMAGE that ships.
//
// The corpus baseline (internal/interop/n8n/corpus/baseline.json) reports
// imported / activatable / runnable counts, and the fidelity statement the
// project publishes is only trustworthy if something re-measures it against the
// shipped artefact rather than the working tree. The Go scorer measures through
// n8n.Import + workflow.Compile + engine.NewRunner; this measures the same
// fixtures through the public API the operator uses (import, activate, run), so
// the two are different paths over the same corpus and a disagreement is a
// finding rather than something to tune away.
//
// Isolation: the fixture workflows contain HTTP and database nodes aimed at
// dummy hosts, and the Go scorer runs them with every side stubbed. The API
// measurement therefore boots the container with closed egress (a placeholder
// allowed_hosts that matches nothing) so an outbound node is *blocked* — which
// is exactly the tier being measured — instead of reaching the internet.
//
// Licensing and privacy: `.corpus` is fetched, unlicensed upstream and never
// committed; `.corpus-private` is the owner's own client exports and is never
// read here at all. Nothing but a fixture's name, its tier and the first line
// of KilasFlow's own error reaches a record — never a payload.
import { readdir, readFile, stat } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { corpusName } from '../scripts/capstone-lib.mjs';
import type { EpicHost } from './epic-proofs';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
const CORPUS_DIR = process.env.KILASFLOW_CORPUS_DIR?.trim() || join(repoRoot, '.corpus');
const AUTHORED_DIR = join(repoRoot, 'internal', 'interop', 'n8n', 'corpus', 'fixtures');
const BASELINE_PATH = join(repoRoot, 'internal', 'interop', 'n8n', 'corpus', 'baseline.json');

// The outbound targets that mean "this fixture wanted a side the scorer stubs,
// and the API's offline policy refused it" — the blocked tier, distinguished
// from a workflow that failed for its own reasons.
const BLOCKED_PHRASES = ['request target is not allowed', 'database target is not allowed'];

const TERMINAL: Record<string, true> = { succeeded: true, failed: true, cancelled: true };

export interface CorpusFixture {
	name: string;
	source: string;
	path: string;
}

export interface CorpusTier {
	imported: number;
	activatable: number;
	runnable: number;
	blocked: number;
}

export interface CorpusMeasurement {
	outcome: 'passed' | 'failed';
	reason: string;
	coverage: { measured: number; baselineTotal: number; partial: boolean };
	tiers: CorpusTier;
	baseline: { tiers: CorpusTier; total: number; blocked: number };
	drift: Array<{ name: string; was: string; now: string }>;
	approximate: Array<{ name: string; reason: string }>;
	gaps: string[];
}

interface Baseline {
	tiers: CorpusTier;
	total: number;
	blocked: number;
	scores: Array<{ name: string; imported?: boolean; activatable?: boolean; runnable?: boolean; blocked?: boolean }>;
}

interface PerFixture {
	name: string;
	imported: boolean;
	activatable: boolean;
	runnable: boolean;
	blocked: boolean;
	detail: string;
}

interface ApiResult {
	status: number;
	text: string;
	json: unknown;
}

// Reads one field out of an unvalidated JSON body. The API is our own, but the
// body crossed a network boundary, so it is checked rather than asserted.
function pick(value: unknown, key: string): unknown {
	if (value && typeof value === 'object' && key in value) return (value as Record<string, unknown>)[key];
	return undefined;
}

function asText(value: unknown): string {
	return typeof value === 'string' ? value : '';
}

async function walkJson(root: string): Promise<string[]> {
	const found: string[] = [];
	async function visit(dir: string, prefix: string): Promise<void> {
		let entries;
		try {
			entries = await readdir(dir, { withFileTypes: true });
		} catch {
			return;
		}
		for (const entry of entries) {
			const relative = prefix === '' ? entry.name : `${prefix}/${entry.name}`;
			if (entry.isDirectory()) {
				await visit(join(dir, entry.name), relative);
			} else if (entry.name.endsWith('.json')) {
				found.push(relative);
			}
		}
	}
	if (!(await stat(root).then(() => true, () => false))) return found;
	await visit(root, '');
	found.sort();
	return found;
}

/** Every fixture this machine has: the authored ones always, the fetched ones when present. */
export async function listCorpusFixtures(): Promise<CorpusFixture[]> {
	const fixtures: CorpusFixture[] = [];
	for (const entry of (await readdir(AUTHORED_DIR).catch(() => [] as string[])).filter((name) =>
		name.endsWith('.json')
	)) {
		const named = corpusName(entry, 'kilasflow');
		fixtures.push({ ...named, path: join(AUTHORED_DIR, entry) });
	}
	for (const relative of await walkJson(CORPUS_DIR)) {
		const named = corpusName(relative);
		fixtures.push({ ...named, path: join(CORPUS_DIR, relative) });
	}
	return fixtures;
}

function tierOf(row: { imported?: boolean; activatable?: boolean; runnable?: boolean; blocked?: boolean }): string {
	if (row.runnable) return 'runnable';
	if (row.blocked) return 'blocked';
	if (row.activatable) return 'activatable';
	if (row.imported) return 'imported';
	return 'not-imported';
}

function firstLine(text: string): string {
	const line = String(text ?? '').split('\n').find((entry) => entry.trim() !== '') ?? '';
	return line.trim().slice(0, 300);
}

async function api(baseURL: string, method: string, path: string, body?: unknown): Promise<ApiResult> {
	const response = await fetch(`${baseURL}/api/v1${path}`, {
		method,
		headers: { 'content-type': 'application/json' },
		body: body === undefined ? undefined : JSON.stringify(body)
	});
	const text = await response.text();
	let json: unknown = null;
	try {
		json = text === '' ? null : JSON.parse(text);
	} catch {
		json = null;
	}
	return { status: response.status, text, json };
}

// One fixture through the operator's path: import, activate (then deactivate),
// run, poll. Every tier is recorded with the first line of KilasFlow's own
// message, never the payload.
async function measureFixture(baseURL: string, fixture: CorpusFixture): Promise<PerFixture> {
	const result: PerFixture = {
		name: fixture.name,
		imported: false,
		activatable: false,
		runnable: false,
		blocked: false,
		detail: ''
	};
	const payload = JSON.parse(await readFile(fixture.path, 'utf-8')) as Record<string, unknown>;
	const imported = await api(baseURL, 'POST', '/workflows/import', { format: 'n8n', workflow: payload });
	if (imported.status !== 201) {
		result.detail = `import ${imported.status}: ${firstLine(asText(pick(imported.json, 'detail')) || imported.text)}`;
		return result;
	}
	result.imported = true;
	const workflowId = asText(pick(pick(imported.json, 'workflow'), 'id'));
	const activated = await api(baseURL, 'POST', `/workflows/${workflowId}/activate`, {});
	if (activated.status !== 200) {
		result.detail = `activate ${activated.status}: ${firstLine(asText(pick(activated.json, 'detail')) || activated.text)}`;
		return result;
	}
	result.activatable = true;
	await api(baseURL, 'POST', `/workflows/${workflowId}/deactivate`, {});

	const started = await api(baseURL, 'POST', `/workflows/${workflowId}/run`, {});
	if (started.status !== 202) {
		result.detail = `run ${started.status}: ${firstLine(asText(pick(started.json, 'detail')) || started.text)}`;
		return result;
	}
	const executionId = asText(pick(started.json, 'id'));
	for (let attempt = 0; attempt < 60; attempt += 1) {
		const record = await api(baseURL, 'GET', `/executions/${executionId}`);
		const status = asText(pick(record.json, 'status'));
		if (TERMINAL[status] === true) {
			const failure = pick(record.json, 'error') ?? pick(record.json, 'failure') ?? '';
			const message = firstLine(typeof failure === 'string' ? failure : JSON.stringify(failure));
			if (status === 'succeeded') {
				result.runnable = true;
			} else if (BLOCKED_PHRASES.some((phrase) => message.includes(phrase))) {
				result.blocked = true;
			}
			result.detail = `${status}: ${message}`;
			return result;
		}
		await new Promise((resolveDelay) => setTimeout(resolveDelay, 250));
	}
	result.detail = 'the execution never reached a terminal status';
	return result;
}

/**
 * Measures the corpus against one isolated container and joins it to the
 * committed baseline. The container's egress is closed: a fixture whose
 * outbound node is refused lands in the blocked tier, which is the tier the
 * Go scorer also measures with its stubs.
 */
export async function measureCorpus(host: EpicHost, baselinePath = BASELINE_PATH): Promise<CorpusMeasurement> {
	const baseline = JSON.parse(await readFile(baselinePath, 'utf-8')) as Baseline;
	const fixtures = await listCorpusFixtures();
	const server = await host.start({ allowedHosts: ['corpus.invalid'] });
	const rows: PerFixture[] = [];
	try {
		for (const fixture of fixtures) {
			try {
				rows.push(await measureFixture(server.baseURL, fixture));
			} catch (error) {
				// A fixture the measurement itself could not drive is reported
				// as un-imported with its reason, never silently dropped.
				rows.push({
					name: fixture.name,
					imported: false,
					activatable: false,
					runnable: false,
					blocked: false,
					detail: `measurement failed: ${firstLine(error instanceof Error ? error.message : String(error))}`
				});
			}
		}
	} finally {
		await server.close();
	}

	const tiers: CorpusTier = {
		imported: rows.filter((row) => row.imported).length,
		activatable: rows.filter((row) => row.activatable).length,
		runnable: rows.filter((row) => row.runnable).length,
		blocked: rows.filter((row) => row.blocked).length
	};
	const coverage = { measured: rows.length, baselineTotal: baseline.total, partial: rows.length < baseline.total };

	const byName = new Map(rows.map((row) => [row.name, row]));
	const drift: Array<{ name: string; was: string; now: string }> = [];
	for (const row of baseline.scores) {
		const now = byName.get(row.name);
		if (!now) continue;
		const was = tierOf(row);
		const current = tierOf(now);
		if (was !== current) drift.push({ name: row.name, was, now: current });
	}
	// The method differs (API import/activate/run vs Import/Compile/Runner), so
	// a disagreement is reported as approximate rather than tuned away.
	const approximate = drift.map((entry) => ({
		name: entry.name,
		reason: `the API measurement reports ${entry.now} where the Go scorer baseline says ${entry.was}`
	}));

	const regressed =
		tiers.imported < baseline.tiers.imported ||
		tiers.activatable < baseline.tiers.activatable ||
		tiers.runnable < baseline.tiers.runnable;
	const strict = process.env.KILASFLOW_CAPSTONE_CORPUS_STRICT === '1';
	const failed = regressed && (!coverage.partial || strict);

	const gaps: string[] = [];
	if (coverage.partial) {
		gaps.push(
			`coverage ${rows.length}/${baseline.total} (partial): the third-party corpus is not materialised on this ` +
				'machine (CI never fetches it). Run `make corpus` and re-run for a fidelity statement.'
		);
	}
	for (const entry of approximate) gaps.push(`approximate: ${entry.name} — ${entry.reason}`);

	return {
		outcome: failed ? 'failed' : 'passed',
		reason: failed
			? `tier regression against the baseline: imported ${tiers.imported}/${baseline.tiers.imported}, ` +
				`activatable ${tiers.activatable}/${baseline.tiers.activatable}, runnable ${tiers.runnable}/${baseline.tiers.runnable}`
			: `coverage ${rows.length}/${baseline.total}${coverage.partial ? ' (partial)' : ''}, ` +
				`imported ${tiers.imported}, activatable ${tiers.activatable}, runnable ${tiers.runnable}, blocked ${tiers.blocked}`,
		coverage,
		tiers,
		baseline: { tiers: baseline.tiers, total: baseline.total, blocked: baseline.blocked },
		drift,
		approximate,
		gaps
	};
}
