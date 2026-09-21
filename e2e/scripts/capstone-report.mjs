#!/usr/bin/env node
// FEAT-5fhj6p stage 2: the capstone's verdict.
//
// This is the only aggregator of a capstone run. Each test writes its own
// record (Playwright restarts the worker after a failure, so nothing in memory
// survives from one test to the next); this reads them, cross-checks the
// Playwright JSON report so a test that crashed without a record counts as a
// failure rather than as silence, and writes report.json and report.md.
//
// Exit code, through exitCodeFor:
//   0  every record passed or skipped (a skip is an explained non-run, not a
//      pass — it is printed in the report with its recovery command)
//   1  any record failed, or an expected record is missing
//   2  nothing failed, but at least one proof was unavailable (a third party
//      was down, which is not a regression signal)
//
// `--unavailable-ok` is the schedule's opinion of that last case and nothing
// else: the verdict written into the report is unchanged, and the process exits
// 0 with the reason printed. A weekly run that goes red because somebody else's
// API was down is a run people learn to ignore, which is worse than no schedule.
// Exit 1 stays fatal in both modes — an assertion disagreement is the signal
// this suite exists for.
//
// Nothing from the environment reaches the report unredacted: credentials, the
// bearer values a service echoed and the webhook routes (a capability in their
// own right) are rewritten before a byte is written or printed.
import { execFileSync } from 'node:child_process';
import { appendFile, mkdir, readdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { exitCodeFor, redact, renderMarkdown } from './capstone-lib.mjs';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
const e2eDir = join(repoRoot, 'e2e');
const resultsDir = join(e2eDir, 'capstone-results');
const recordsDir = join(resultsDir, 'records');

// The expected records, in order. A missing one is a failure: a test that never
// wrote a record crashed, and silence must not read as success.
const EXPECTED = [
	{ index: 0, id: 'artefacts', title: 'artefacts - image identity' },
	{ index: 1, id: 'proof1-telegram', title: 'proof 1 - Telegram bot through an AI agent' },
	{ index: 2, id: 'proof2-waha', title: 'proof 2 - WAHA chatting template, two tenants' },
	{ index: 3, id: 'proof3-datastore', title: 'proof 3 - datastore on PostgreSQL' },
	{ index: 4, id: 'proof3-isolation', title: 'proof 3 - a second tenant reads nothing' },
	{ index: 5, id: 'proof4-external', title: 'proof 4 - external consumer' },
	{ index: 6, id: 'corpus', title: 'corpus fidelity' }
];

const SECRET_ENV = [
	'KILASFLOW_CAPSTONE_TELEGRAM_BOT_TOKEN',
	'KILASFLOW_CAPSTONE_OPENROUTER_API_KEY',
	'KILASFLOW_CAPSTONE_WAHA_API_KEY',
	'KILASFLOW_CAPSTONE_WAHA_URL',
	'KILASFLOW_CAPSTONE_NPM_REGISTRY'
];

function secretsFromEnv() {
	return SECRET_ENV.map((name) => process.env[name]).filter((value) => typeof value === 'string' && value !== '');
}

async function loadRecords() {
	const names = await readdir(recordsDir).catch(() => []);
	const records = [];
	for (const name of names) {
		const match = name.match(/^(\d+)-(.+)\.json$/);
		if (!match) continue;
		const text = await readFile(join(recordsDir, name), 'utf-8');
		records.push(JSON.parse(text));
	}
	records.sort((a, b) => a.index - b.index);
	return records;
}

// The Playwright report is only consulted for tests that failed without writing
// a record; a test the runner reports as failing that DID write one keeps its
// own record, because the record carries the classification.
async function crashedWithoutRecord() {
	const path = join(resultsDir, 'playwright.json');
	const raw = await readFile(path, 'utf-8').catch(() => null);
	if (!raw) return new Map();
	const report = JSON.parse(raw);
	const states = new Map();
	const visit = (suite) => {
		for (const child of suite.suites ?? []) visit(child);
		for (const spec of suite.specs ?? []) {
			const results = spec.tests?.flatMap((entry) => entry.results ?? []) ?? [];
			const failed = results.some((entry) => entry.status === 'failed' || entry.status === 'timedOut');
			states.set(spec.title, failed);
		}
	};
	for (const suite of report.suites ?? []) visit(suite);
	return states;
}

async function suiteSha() {
	try {
		return execFileSync('git', ['rev-parse', '--short', 'HEAD'], { cwd: repoRoot, encoding: 'utf-8' }).trim();
	} catch {
		return 'unknown';
	}
}

function summariseNoNode(records) {
	const verdicts = records.flatMap((record) => record.noNode ?? []);
	return {
		checked: verdicts.length > 0 && verdicts.every((verdict) => verdict.checked === true),
		checks: verdicts.length,
		scopes: [...new Set(verdicts.map((verdict) => verdict.scope))],
		reason: verdicts.find((verdict) => !verdict.checked)?.reason ?? '',
		nodeProcesses: verdicts.flatMap((verdict) => verdict.nodeProcesses ?? [])
	};
}

function verdictFor(records, exitCode) {
	if (exitCode === 1) return 'failed';
	if (exitCode === 2) return 'unavailable';
	if (records.some((record) => record.outcome === 'skipped')) return 'skipped';
	return 'passed';
}

async function main() {
	const secrets = secretsFromEnv();
	const unavailableOk = process.argv.includes('--unavailable-ok');
	const records = await loadRecords();
	const crashed = await crashedWithoutRecord();

	// A missing record is a failure; the synthetic one makes the reason visible
	// in the report instead of leaving a gap.
	const byId = new Map(records.map((record) => [record.id, record]));
	for (const expected of EXPECTED) {
		if (byId.has(expected.id)) continue;
		records.push({
			schema: 1,
			id: expected.id,
			index: expected.index,
			title: expected.title,
			runId: records[0]?.runId ?? 'unknown',
			outcome: 'failed',
			reason: crashed.get(expected.title)
				? 'the test failed in Playwright without writing a record'
				: 'no record was written: the test did not run or crashed during collection',
			evidence: {},
			noNode: [],
			gaps: [],
			startedAt: null,
			finishedAt: null,
			durationMs: 0
		});
	}
	records.sort((a, b) => a.index - b.index);

	const expectedIds = EXPECTED.map((entry) => entry.id);
	const exitCode = exitCodeFor(records, expectedIds);
	const artefactsRecord = byId.get('artefacts');
	const proof4Evidence = byId.get('proof4-external')?.evidence ?? {};
	const corpusRecord = byId.get('corpus')?.evidence?.corpus ?? null;
	const image = artefactsRecord?.evidence?.['artefacts.image'] ?? null;
	const health = artefactsRecord?.evidence?.['artefacts.health'] ?? null;
	// What the external-consumer proof installed, as it observed it: the source
	// it used, the spec it asked for, the version that resolved and the
	// integrity the lockfile recorded. A skipped proof 4 leaves it null rather
	// than guessing from the environment.
	const sdk = proof4Evidence['external.sdkVersion']
		? {
				source: proof4Evidence['external.source'] ?? null,
				spec: proof4Evidence['external.sdkSpec'] ?? null,
				resolvedVersion: proof4Evidence['external.sdkVersion'],
				integrity: proof4Evidence['external.integrity'] ?? null,
				subpaths: proof4Evidence['external.subpaths'] ?? null
			}
		: null;

	const report = redact(
		JSON.stringify(
			{
				schema: 1,
				runId: records[0]?.runId ?? process.env.KILASFLOW_CAPSTONE_RUN_ID ?? 'unknown',
				generatedAt: new Date().toISOString(),
				suite: await suiteSha(),
				artefacts: {
					image,
					health,
					sdk
				},
				proofs: records.map((record) => ({
					id: record.id,
					index: record.index,
					title: record.title,
					outcome: record.outcome,
					cause: record.cause ?? null,
					reason: record.reason ?? '',
					recovery: record.recovery ?? '',
					gaps: record.gaps ?? [],
					durationMs: record.durationMs ?? 0
				})),
				noNode: summariseNoNode(records),
				corpus: corpusRecord,
				verdict: { outcome: verdictFor(records, exitCode), exitCode }
			},
			null,
			2
		),
		secrets
	);

	await mkdir(resultsDir, { recursive: true });
	await writeFile(join(resultsDir, 'report.json'), `${report}\n`);
	const markdown = redact(renderMarkdown(JSON.parse(report)), secrets);
	await writeFile(join(resultsDir, 'report.md'), markdown);
	if (process.env.GITHUB_STEP_SUMMARY) {
		await appendFile(process.env.GITHUB_STEP_SUMMARY, `${markdown}\n`);
	}

	const parsed = JSON.parse(report);
	const counts = parsed.proofs.reduce((tally, proof) => {
		tally[proof.outcome] = (tally[proof.outcome] ?? 0) + 1;
		return tally;
	}, {});
	console.log(
		`capstone ${parsed.verdict.outcome}: ${parsed.proofs.length} proofs ` +
			`(${Object.entries(counts)
				.map(([outcome, count]) => `${count} ${outcome}`)
				.join(', ') || 'none'}) — report at e2e/capstone-results/report.md`
	);
	process.exitCode = exitCode;
	// The verdict above is unchanged; only the status this process returns is.
	// A caller that treats an outage as a failure passes no flag.
	if (exitCode === 2 && unavailableOk) {
		console.log(
			'capstone unavailable: nothing disagreed, but an artefact or a third party was missing — exiting 0 (--unavailable-ok)'
		);
		process.exitCode = 0;
	}
}

await main();
