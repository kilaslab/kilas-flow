/**
 * Summariser for FEAT-8mymac raw results (method version 2).
 *
 * Usable as a module (run.mjs calls summarize() and writeSummary()) and
 * standalone:
 *
 *   node e2e/benchmark/summarise.mjs <raw.json> [--docs docs/src/content/docs/operate/benchmark.md]
 *
 * Importing this file starts nothing: the CLI runs only when it is the entry
 * point. (The v1 file ran a top-level await on process.argv[2], so importing it
 * from a test executed the CLI.)
 *
 * Two rules the tests pin: a smoke raw file never overwrites SUMMARY.md or the
 * docs page, and every write is preceded by assertNoSecrets.
 */

import { readFile, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { QUIET_LOAD, assertNoSecrets } from './method.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));

export const TABLE_START = '<!-- bench:table:start -->';
export const TABLE_END = '<!-- bench:table:end -->';

function row(cells) {
	return `| ${cells.join(' | ')} |`;
}

function fmt(value) {
	if (value === null || value === undefined) return '—';
	return String(value);
}

function mib(kib) {
	if (!kib) return '—';
	return String(Math.round((kib / 1024) * 10) / 10);
}

function measured(results) {
	return (results ?? []).filter((entry) => entry.status === 'measured');
}

function verdictText(entry) {
	if (!entry.verdict) return '—';
	if (entry.verdict.ok) return 'ok';
	return `inconclusive (${(entry.verdict.reasons ?? []).join('; ')})`;
}

function engineTable(title, results) {
	const lines = [
		`### ${title}`,
		'',
		row(['workflow', 'n', 'min (ms)', 'p50 (ms)', 'p95 (ms)', 'max (ms)', 'mean (ms)', 'stddev (ms)', 'server p50 (ms)', 'peak RSS (MiB)', 'verdict']),
		row(['---', '---:', '---:', '---:', '---:', '---:', '---:', '---:', '---:', '---:', '---']),
	];
	for (const entry of results) {
		const s = entry.summary ?? {};
		lines.push(
			row([
				entry.name,
				fmt(s.n),
				fmt(s.min),
				fmt(s.p50),
				fmt(s.p95),
				fmt(s.max),
				fmt(s.mean),
				fmt(s.stddev),
				fmt(entry.serverMs?.p50),
				mib(entry.peakRssKib),
				verdictText(entry),
			]),
		);
	}
	if (results.length === 0) lines.push(row(['(none measured)', '—', '—', '—', '—', '—', '—', '—', '—', '—', '—']));
	return lines.join('\n');
}

function comparisonTable(comparison) {
	const lines = [
		row(['workflow', 'KilasFlow p50 (ms)', 'n8n p50 (ms)', 'ratio n8n/KilasFlow', '95% CI', 'verdict']),
		row(['---', '---:', '---:', '---:', '---', '---']),
	];
	for (const entry of comparison) {
		lines.push(
			row([
				entry.name,
				fmt(entry.kilasP50),
				fmt(entry.n8nP50),
				fmt(entry.ratio),
				`[${fmt(entry.lo)}, ${fmt(entry.hi)}]`,
				entry.label,
			]),
		);
	}
	if (comparison.length === 0) lines.push(row(['(the n8n half is not measured)', '—', '—', '—', '—', '—']));
	return lines.join('\n');
}

function supplementaryTable(results) {
	const lines = [
		row(['workflow', 'KilasFlow p50 (ms)', 'n8n p50 (ms)', 'note']),
		row(['---', '---:', '---:', '---']),
	];
	for (const entry of results) {
		lines.push(row([entry.name, fmt(entry.kilasP50), fmt(entry.n8nP50), entry.label ?? 'divergent by design']));
	}
	return lines.join('\n');
}

function oneMinuteLoad(raw) {
	const before = raw.environment?.loadAverage?.before?.[0];
	const after = raw.environment?.loadAverage?.after?.[0];
	const values = [before, after].filter((value) => typeof value === 'number');
	if (values.length === 0) return null;
	return Math.max(...values);
}

function banners(raw) {
	const lines = [];
	if (raw.method?.smoke) {
		lines.push('> **SMOKE run.** Four samples per engine, one discarded warm-up. This is a harness check, not a measurement: nothing here is published, and the file is written outside the repository.', '');
	}
	if (raw.n8n?.status !== 'measured') {
		lines.push(
			'> **PRELIMINARY: KilasFlow half only.** The n8n comparison half was not measured, so nothing below is a comparison. ' +
				`Reason: ${raw.n8n?.reason ?? raw.n8n?.status ?? 'unknown'}.`,
			'',
		);
	}
	const load = oneMinuteLoad(raw);
	if (load !== null && load > QUIET_LOAD) {
		lines.push(
			`> **The machine was not quiet.** 1-minute load average reached ${load} (the pre-registered banner threshold is ${QUIET_LOAD}). ` +
				'Rows that failed a variance bound read inconclusive; the bounds were not relaxed.',
			'',
		);
	}
	return lines;
}

/** The docs-page block, between the bench:table markers. */
export function renderTable(raw) {
	const comparison = raw.comparison ?? [];
	const headline = comparison.filter((entry) => !entry.supplementary);
	const supplementary = comparison.filter((entry) => entry.supplementary);
	const lines = [
		comparisonTable(headline),
		'',
		`KilasFlow ${raw.environment?.kilasflow?.version ?? 'unknown'} (${commitLabel(raw)}), n8n ${raw.n8n?.version ?? 'not measured'}, ` +
			`${raw.method?.runs ?? '?'} runs per engine after ${raw.method?.warmup ?? '?'} discarded warm-ups, ${raw.environment?.date ?? 'unknown date'}.`,
	];
	if (supplementary.length > 0) {
		lines.push('', '#### Supplementary row — divergent by design, not part of the identical-work claim', '', supplementaryTable(supplementary));
	}
	return lines.join('\n');
}

function commitLabel(raw) {
	const kilas = raw.environment?.kilasflow ?? {};
	return `${kilas.commit ?? 'unknown commit'}${kilas.dirty ? '-dirty' : ''}`;
}

export function summarize(raw) {
	const kilasResults = measured(raw.kilasflow?.results);
	const n8nResults = measured(raw.n8n?.results);
	const comparison = raw.comparison ?? [];
	const headline = comparison.filter((entry) => !entry.supplementary);
	const supplementary = comparison.filter((entry) => entry.supplementary);
	const codeRows = measured(raw.kilasflow?.results).filter((entry) => entry.supplementary);
	const agentRows = [...kilasResults, ...n8nResults].filter((entry) => entry.gatewayOverheadMs !== undefined && entry.gatewayOverheadMs !== null);
	const excluded = (raw.exclusions ?? []).map((entry) => `- **${entry.what}**: ${entry.why}`);
	const divergences = [];
	for (const entry of [...measured(raw.kilasflow?.results), ...measured(raw.n8n?.results)]) {
		if (entry.divergence) divergences.push(`- **${entry.name}**: ${entry.divergence}`);
	}
	const skipped = (raw.n8n?.results ?? []).filter((entry) => entry.status !== 'measured');
	const skippedKilas = (raw.kilasflow?.results ?? []).filter((entry) => entry.status !== 'measured');

	const lines = [
		`# Benchmark summary — ${raw.environment?.date ?? 'unknown date'}`,
		'',
		...banners(raw),
		`KilasFlow ${raw.environment?.kilasflow?.version ?? 'unknown'} (${commitLabel(raw)}), Node ${raw.environment?.node ?? 'unknown'}.`,
		`Machine: ${raw.environment?.machine?.chip ?? 'unknown'} (${raw.environment?.machine?.model ?? '?'}, macOS ${raw.environment?.machine?.macos ?? '?'}, ${raw.environment?.machine?.uname ?? '?'}), ` +
			`${raw.environment?.machine?.cpuCount ?? '?'} CPUs, ${memGib(raw)} GiB.`,
		`n8n: ${raw.n8n?.version ?? 'not measured'}${raw.n8n?.image ? ` (image ${raw.n8n.image}, digest ${raw.n8n.digest ?? 'unknown'})` : ''}.`,
		`Power: ${raw.environment?.power?.source ?? 'unknown'}; thermal: ${raw.environment?.thermal?.state ?? 'unknown'}.`,
		`Load average (1m/5m/15m): before ${formatLoad(raw.environment?.loadAverage?.before)}, after ${formatLoad(raw.environment?.loadAverage?.after)}.`,
		`Ollama: ${raw.environment?.ollama?.version ?? 'not probed'}, model ${raw.environment?.ollama?.model ?? '?'} (digest ${raw.environment?.ollama?.digest ?? 'unknown'}).`,
		...infrastructure(raw),
		`Method: ${raw.method?.runs ?? '?'} timed runs per engine per workflow after ${raw.method?.warmup ?? '?'} discarded warm-ups, blocks of ${raw.method?.block ?? '?'}, ABBA order, seed ${raw.method?.seed ?? '?'}.`,
		`Primary metric: ${raw.method?.primary ?? 'unknown'}.`,
		`Secondary metric: ${raw.method?.secondary ?? 'unknown'}.`,
		`Stub latency (direct, engine excluded): p50 ${raw.stubLatency?.p50 ?? '?'} ms over n=${raw.stubLatency?.n ?? '?'}.`,
		'',
		engineTable('KilasFlow (client-observed HTTP response latency, ms)', kilasResults),
		'',
		engineTable('n8n (client-observed HTTP response latency, ms)', n8nResults),
		'',
		'### Comparison (ratio = n8n p50 / KilasFlow p50; >1 means n8n took longer)',
		'',
		comparisonTable(headline),
		'',
	];
	if (agentRows.length > 0) {
		const parts = [
			...kilasResults.filter((entry) => entry.gatewayOverheadMs !== undefined && entry.gatewayOverheadMs !== null).map((entry) => `KilasFlow ${entry.gatewayOverheadMs} ms`),
			...n8nResults.filter((entry) => entry.gatewayOverheadMs !== undefined && entry.gatewayOverheadMs !== null).map((entry) => `n8n ${entry.gatewayOverheadMs} ms`),
		];
		lines.push(
			`Gateway-attributed engine overhead (agent row, response clock minus gateway-observed model and tool time; supplementary and not variance-gated): ${parts.join('; ')}.`,
			'',
		);
	}
	if (raw.control) {
		lines.push(
			'### A/A negative control (KilasFlow vs KilasFlow through the same machinery)',
			'',
			`ratio ${raw.control.ratio} (95% CI [${raw.control.lo}, ${raw.control.hi}]) — ${raw.control.label}. A CI that excludes 1 would mean the harness itself has an order or position bias.`,
			'',
		);
	}
	if (supplementary.length > 0 || codeRows.length > 0) {
		lines.push(
			'### Supplementary row — divergent by design',
			'',
			'The Code row is **not** identical work (KilasFlow runs compiled Go/WASM, n8n runs JavaScript) and is excluded from the headline five.',
			'',
			supplementary.length > 0 ? supplementaryTable(supplementary) : supplementaryTable(codeRows.map((entry) => ({ name: entry.name, kilasP50: entry.summary?.p50, n8nP50: null, label: 'divergent by design' }))),
			'',
		);
	}
	if (skipped.length > 0) {
		lines.push('### n8n side — rows not measured', '');
		for (const entry of skipped) lines.push(`- **${entry.name ?? entry.key}**: ${entry.status}${entry.note ? ` — ${entry.note}` : ''}`);
		lines.push('');
	}
	if (skippedKilas.length > 0) {
		lines.push('### KilasFlow side — rows not measured', '');
		for (const entry of skippedKilas) lines.push(`- **${entry.name ?? entry.key}**: ${entry.status}${entry.note ? ` — ${entry.note}` : ''}`);
		lines.push('');
	}
	if (divergences.length > 0) {
		// A shared row carries the same divergence text on both engines; the
		// list is a set, so it is not printed twice.
		lines.push('### Expected divergences (not drift)', '', ...[...new Set(divergences)], '');
	}
	lines.push(
		'### Exclusions (and why)',
		'',
		...(excluded.length > 0
			? excluded
			: [
					'- **Third-party-credentialed nodes**: an engine cannot be timed on a node whose upstream service this machine has no credential for.',
					'- **Queue mode and Postgres mode**: the benchmark runs both engines in their default single-process SQLite configuration.',
					'- **Throughput and concurrency**: every run is settled before the next, so this measures latency, never requests per second.',
					'- **Cold start**: warm-ups are discarded by design.',
					'- **Multi-tenant overhead**: one tenant, one workflow at a time.',
					'- **Editor latency**: browser-only micro-interactions are not this benchmark.',
				]),
		'',
		'### Limits',
		'',
		'- Single-machine comparison on a laptop, **not SLAs**. Do not compare across machines or quote these as guarantees.',
		'- Stub-backed by design: all third-party calls go to one loopback stub, so third-party latency is cancelled out and what is measured is engine overhead. The stub is reached at 127.0.0.1 by KilasFlow and host.docker.internal by n8n; that hop difference is a stated divergence.',
		'- The primary number is client-observed HTTP response latency, measured by one shared function on both engines (request sent to full body received). The server execution record is the secondary cross-check at 1 ms resolution.',
		'- Percentiles are nearest-rank and the standard deviation is the population one: with N=30 the p95 is essentially the second-highest sample.',
		'- The 95% CI is a percentile bootstrap over an i.i.d. resample of each row, which assumes the samples are exchangeable; the ABBA schedule tries to make that true and a drifting machine breaks it.',
		'- Peak RSS is sampled after each settled run and is not like-for-like: KilasFlow is a native macOS process while n8n is a set of Linux processes inside a container VM.',
		'- Laptop thermals and background load are recorded per run (load average, power source, thermal state); a throttled or loaded machine can fail a variance bound and the row then reads inconclusive.',
		'- Excluded from this comparison, with reasons above: third-party-credentialed nodes, queue and Postgres modes, throughput and concurrency, cold start, multi-tenant overhead and editor latency.',
		'',
		`Raw runs: \`${raw.artifact ?? 'see e2e/benchmark/'}\``,
		'',
	);
	return lines.join('\n');
}

function memGib(raw) {
	const bytes = raw.environment?.machine?.memBytes;
	return typeof bytes === 'number' ? Math.round(bytes / 1024 ** 3) : '?';
}

/** GiB from a byte count, for the container VM's memory. */
function gib(bytes) {
	return typeof bytes === 'number' ? Math.round(bytes / 1024 ** 3) : '?';
}

/**
 * The container and substrate facts the comparison depends on: which Docker
 * runtime and VM the n8n half ran in, the container's environment variable
 * names (its values and any secret or key variable are dropped, so the raw
 * file can never carry a credential), the loopback stub's latency as measured
 * from inside the container, and the hop probe on each engine's trivial
 * handler — the published-port proxy floor under every n8n row.
 */
function infrastructure(raw) {
	const lines = [];
	const docker = raw.environment?.docker;
	if (docker?.available) {
		lines.push(`Docker: ${docker.os ?? 'unknown'} ${docker.serverVersion ?? '?'}, container VM ${docker.cpuCount ?? '?'} CPUs / ${gib(docker.memBytes)} GiB (shares the host CPUs).`);
	} else if (docker) {
		lines.push(`Docker: unavailable (${docker.reason ?? 'no reason recorded'}).`);
	}
	const envKeys = raw.n8n?.containerEnv;
	if (Array.isArray(envKeys) && envKeys.length > 0) {
		lines.push(`n8n container env keys (secret and key variables dropped): ${envKeys.join(', ')}.`);
	}
	const containerStub = raw.containerStubLatency;
	if (containerStub?.p50 !== undefined) {
		lines.push(`Stub latency from inside the container: p50 ${containerStub.p50} ms over n=${containerStub.n ?? '?'}.`);
	}
	const hopN8n = raw.hopProbe?.n8n;
	const hopKilas = raw.hopProbe?.kilasflow;
	if (hopN8n?.p50 !== undefined && hopKilas?.p50 !== undefined) {
		lines.push(
			`Hop probe (trivial handlers): n8n /healthz p50 ${hopN8n.p50} ms vs KilasFlow /api/v1/health p50 ${hopKilas.p50} ms over n=${hopN8n.n ?? '?'}.`,
		);
	}
	return lines;
}

function formatLoad(values) {
	if (!Array.isArray(values)) return 'unknown';
	return `[${values.join(', ')}]`;
}

/** Replace the block between the docs markers. Idempotent; throws without markers. */
export function rewriteDocsTable(docsText, tableMarkdown) {
	const start = docsText.indexOf(TABLE_START);
	const end = docsText.indexOf(TABLE_END);
	if (start === -1 || end === -1 || end < start) {
		throw new Error(`the docs page has no ${TABLE_START} … ${TABLE_END} marker pair to write between`);
	}
	const before = docsText.slice(0, start + TABLE_START.length);
	const after = docsText.slice(end);
	return `${before}\n${tableMarkdown}\n${after}`;
}

/** Secrets that must never reach a file. Read from env, never from argv. */
export function secretsFromEnv(env = process.env) {
	return [env.N8N_EMAIL, env.N8N_PASSWORD, env.N8N_ENCRYPTION_KEY, env.N8N_SESSION_COOKIE];
}

/**
 * Write the summary and, when asked, the docs table.
 *
 * A smoke run writes nothing: its numbers are a harness check and the paths it
 * is given are the repository's published artifacts.
 */
export async function writeSummary({ raw, summaryPath, docsPath }) {
	if (raw.method?.smoke) {
		return { written: false, reason: 'smoke run: SUMMARY.md and the docs page are left untouched' };
	}
	const markdown = summarize(raw);
	const secrets = secretsFromEnv();
	assertNoSecrets([markdown, JSON.stringify(raw)], secrets);
	await writeFile(summaryPath, markdown);
	if (docsPath) {
		const docs = await readFile(docsPath, 'utf-8');
		const table = renderTable(raw);
		assertNoSecrets([table], secrets);
		await writeFile(docsPath, rewriteDocsTable(docs, table));
	}
	return { written: true, summaryPath, docsPath: docsPath ?? null };
}

async function main() {
	const [inputPath, ...rest] = process.argv.slice(2);
	if (!inputPath) {
		console.error('usage: node e2e/benchmark/summarise.mjs <raw.json> [--docs <file>]');
		process.exit(2);
	}
	const docsIndex = rest.indexOf('--docs');
	const docsPath = docsIndex === -1 ? undefined : rest[docsIndex + 1];
	const raw = JSON.parse(await readFile(inputPath, 'utf-8'));
	const outcome = await writeSummary({ raw, summaryPath: join(HERE, 'SUMMARY.md'), docsPath });
	if (!outcome.written) {
		console.log(`summary not written: ${outcome.reason}`);
		return;
	}
	console.log(`summary written to ${outcome.summaryPath} (from ${inputPath})`);
	if (outcome.docsPath) console.log(`docs table written to ${outcome.docsPath}`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
	await main();
}
