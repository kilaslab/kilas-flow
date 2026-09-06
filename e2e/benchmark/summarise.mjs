/**
 * Summariser for FEAT-8mymac raw results.
 *
 * Reads a raw bench JSON file (as written by run.mjs) and renders the
 * Markdown summary table. Usable as a module (run.mjs calls summarize())
 * and standalone: `node e2e/benchmark/summarise.mjs <raw.json>`.
 */

import { readFile, writeFile } from 'node:fs/promises';
import { dirname, join, basename } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));

function row(cells) {
	return `| ${cells.join(' | ')} |`;
}

function fmt(value) {
	if (value === null || value === undefined) return '—';
	return String(value);
}

function statsTable(title, entries) {
	const lines = [
		`### ${title}`,
		'',
		row(['workflow', 'n', 'min (ms)', 'p50 (ms)', 'p95 (ms)', 'max (ms)', 'mean (ms)', 'stddev (ms)', 'peak RSS (MiB)']),
		row(['---', '---:', '---:', '---:', '---:', '---:', '---:', '---:', '---:']),
	];
	for (const entry of entries) {
		const s = entry.summary;
		lines.push(
			row([
				entry.name,
				String(s?.n ?? '—'),
				fmt(s?.min),
				fmt(s?.p50),
				fmt(s?.p95),
				fmt(s?.max),
				fmt(s?.mean),
				fmt(s?.stddev),
				entry.peakRssMib !== null && entry.peakRssMib !== undefined ? String(entry.peakRssMib) : '—',
			]),
		);
	}
	return lines.join('\n');
}

export function summarize(raw) {
	const kilasEntries = (raw.kilasflow?.results ?? []).map((r) => ({
		name: r.name,
		summary: r.summary,
		peakRssMib: r.peakRssKib ? Math.round((r.peakRssKib / 1024) * 10) / 10 : null,
	}));
	const n8nEntries = (raw.n8n?.results ?? [])
		.filter((r) => r.summary)
		.map((r) => ({ name: r.name ?? r.key, summary: r.summary, peakRssMib: null }));

	const skipped = (raw.n8n?.results ?? []).filter((r) => !r.summary);
	const agentNote = (raw.kilasflow?.results ?? []).find((r) => r.status === 'skipped-no-model');

	const lines = [
		`# Benchmark summary — ${raw.environment?.date ?? 'unknown date'}`,
		'',
		raw.n8n?.status === 'measured'
			? 'Both engines measured on the same machine. Read client-p50 as the headline; server-ms excludes delivery and polling.'
			: '> **Preliminary: KilasFlow half only.** The n8n comparison half awaits credentials (`e2e/benchmark/README.md`, "The n8n half") and no n8n number below is real. Do not quote this as a comparison.',
		'',
		`KilasFlow ${raw.environment?.kilasflow?.version ?? ''} (${raw.environment?.kilasflow?.commit ?? 'unknown commit'}), Node ${raw.environment?.node ?? ''}.`,
		`Machine: ${raw.environment?.machine?.model ?? 'unknown'} (${raw.environment?.machine?.uname ?? ''}), ${raw.environment?.machine?.cpuCount ?? '?'} CPUs.`,
		`Method: ${raw.method?.runs ?? '?'} timed runs per workflow after ${raw.method?.warmup ?? '?'} discarded warm-ups, sequential and isolated; primary number is client-observed wall time, secondary is the server execution record (startedAt→finishedAt).`,
		`Stub latency (direct, engine excluded): p50 ${raw.stubLatency?.p50 ?? '?'} ms over n=${raw.stubLatency?.n ?? '?'}.`,
		'',
		statsTable('KilasFlow (client wall-clock ms)', kilasEntries),
		'',
	];
	if (n8nEntries.length > 0) {
		lines.push(statsTable('n8n (client wall-clock ms)', n8nEntries), '');
	}
	if (skipped.length > 0) {
		lines.push('### n8n side — not measured here', '');
		for (const s of skipped) {
			lines.push(`- **${s.name ?? s.key}**: ${s.status}${s.note ? ` — ${s.note}` : ''}`);
		}
		lines.push('', raw.n8n?.skipReason ?? '', '');
	}
	if (agentNote) {
		lines.push(`### Agent workflow — ${agentNote.status}`, '', `${agentNote.note ?? ''}`, '');
	}
	const divergences = (raw.kilasflow?.results ?? []).filter((r) => r.divergence);
	if (divergences.length > 0) {
		lines.push('### Expected divergences (not drift)', '');
		for (const d of divergences) lines.push(`- **${d.name}**: ${d.divergence}`);
		lines.push('');
	}
	lines.push(
		'### Limits',
		'',
		'- Single-machine numbers, not SLAs. Do not compare across machines.',
		'- Stub-backed: third-party latency is cancelled out by design; what is measured is engine overhead.',
		'- KilasFlow manual workflows time the run API (queue → terminal record); webhook workflows time HTTP delivery → terminal record. The n8n half times webhook deliveries only.',
		'- Peak RSS is sampled after each run via ps(1), not traced; treat it as approximate.',
		'',
		`Raw runs: \`${raw.artifact ?? 'see e2e/benchmark/'}\``,
		'',
	);
	return lines.join('\n');
}

const inputPath = process.argv[2];
if (inputPath) {
	const raw = JSON.parse(await readFile(inputPath, 'utf-8'));
	const markdown = summarize(raw);
	const outPath = join(HERE, 'SUMMARY.md');
	await writeFile(outPath, markdown);
	console.log(`summary written to ${outPath} (from ${basename(inputPath)})`);
}
