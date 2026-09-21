// FEAT-5fhj6p stage 2: the epic acceptance capstone.
//
// This is the second CALLER of the proofs in e2e/fixtures/epic-proofs.ts: the
// hermetic suite (e2e/tests/epic-acceptance.spec.ts) runs them against a locally
// built binary and loopback stubs on every PR; this runs the same code against
// a docker IMAGE, and — once stage 3 lands the real sides — the real Telegram
// Bot API, a real WAHA server and the npm registry. There is no second
// implementation of any proof, deliberately: two would drift and the
// rarely-run one would rot.
//
// It lives outside e2e/tests and on its own Playwright config
// (e2e/playwright.capstone.config.ts), so the per-PR path and its skip budget
// never see it.
//
// Every test writes its own record to capstone-results/records/<NN>-<id>.json
// and nothing else aggregates: Playwright restarts the worker after a failure,
// so module state cannot carry a verdict from one test to the next, and
// e2e/scripts/capstone-report.mjs is the only reader. The records are what make
// a crashed test visible as a missing record rather than as silence.
//
// Outcomes are exactly passed | failed | unavailable | skipped:
// - failed      — an assertion disagreed: a regression signal, exit code 1.
// - unavailable — a third party or the network was down: exit code 2.
// - skipped     — not configured: a reason and a recovery command, never a
//                 silent pass.
import { execFile } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';

import { expect, test } from '@playwright/test';

import { capstoneSecrets, readCapstoneConfig } from '../fixtures/epic-config';
import { measureCorpus } from '../fixtures/epic-corpus';
import { NpmInstallError } from '../fixtures/epic-external';
import { ImagePullError, imageHost } from '../fixtures/epic-image';
import {
	assertNoNodeOk,
	proofDatastoreIsolation,
	proofDatastoreLifecycle,
	proofExternalConsumer,
	type NoNodeVerdict,
	type ProofContext
} from '../fixtures/epic-proofs';
import { classifyFailure, redact } from '../scripts/capstone-lib.mjs';

const execFileAsync = promisify(execFile);

const e2eDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const recordsDir = join(e2eDir, 'capstone-results', 'records');

const config = readCapstoneConfig();
const host = imageHost(config);
const secrets = capstoneSecrets(config);

type Outcome = 'passed' | 'failed' | 'unavailable' | 'skipped';

interface TestMeta {
	index: number;
	title: string;
}

// The record's context: the shared ProofContext plus a way to state a coverage
// gap (what this run did not exercise), which the report prints per proof.
interface CapstoneContext extends ProofContext {
	gap(message: string): void;
}

// An outcome a test decides for itself: what is missing (skipped) or which
// earlier failure blocks it. Anything else thrown is a real assertion
// disagreement unless a re-probe says a third party was down.
class CapstoneAbort extends Error {
	readonly outcome: Outcome;
	readonly recovery: string | undefined;
	constructor(outcome: Outcome, reason: string, recovery?: string) {
		super(reason);
		this.name = 'CapstoneAbort';
		this.outcome = outcome;
		this.recovery = recovery;
	}
}

interface RecordedResult {
	schema: 1;
	id: string;
	index: number;
	title: string;
	runId: string;
	outcome: Outcome;
	cause?: string;
	reason: string;
	recovery?: string;
	evidence: Record<string, unknown>;
	noNode: NoNodeVerdict[];
	gaps: string[];
	startedAt: string;
	finishedAt: string;
	durationMs: number;
}

function recordPath(index: number, id: string): string {
	return join(recordsDir, `${String(index).padStart(2, '0')}-${id}.json`);
}

async function readRecord(index: number, id: string): Promise<RecordedResult | null> {
	return readFile(recordPath(index, id), 'utf-8').then(
		(text) => JSON.parse(text) as RecordedResult,
		() => null
	);
}

// A re-probe is the authority on whether a failure was the third party or us.
// In stage 2 the only third party the core depends on is docker itself, so a
// daemon that has gone away classifies as unavailable; everything else stays a
// failure. Stage 3 extends this with the real Telegram, WAHA and npm probes.
async function reprobeDocker(): Promise<{ healthy: boolean; cause?: string; detail?: string }> {
	try {
		await execFileAsync('docker', ['info', '--format', '{{.ServerVersion}}'], { timeout: 20_000 });
		return { healthy: true };
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		return { healthy: false, cause: 'network', detail: message.split('\n')[0].slice(0, 200) };
	}
}

// Pull and npm failures already carry a classification from the lib; everything
// else is a disagreement unless the re-probe finds docker gone. An unavailable
// outcome carries a recovery command, because the proofs it blocks will report
// "blocked by artefacts" and a reader needs to know what to restore.
async function classifyError(error: unknown): Promise<{
	outcome: Outcome;
	cause?: string;
	reason: string;
	recovery?: string;
}> {
	const recovery = 'make `docker info` and `docker pull <the image under test>` succeed, then re-run `make test-e2e-capstone e2e-capstone-report`';
	if (error instanceof ImagePullError) {
		return {
			outcome: error.classification.outcome,
			cause: error.classification.cause,
			reason: error.message,
			...(error.classification.outcome === 'unavailable' ? { recovery } : {})
		};
	}
	if (error instanceof NpmInstallError) {
		return {
			outcome: error.classification.outcome,
			cause: error.classification.cause,
			reason: error.message,
			...(error.classification.outcome === 'unavailable' ? { recovery } : {})
		};
	}
	const probe = await reprobeDocker();
	const classified = classifyFailure({ error, reprobe: probe });
	return {
		outcome: classified.outcome,
		cause: classified.cause,
		reason: classified.reason,
		...(classified.outcome === 'unavailable' ? { recovery } : {})
	};
}

// Writes the record, then turns the outcome into the Playwright result: skip
// for unavailable/skipped (only after the record exists) and a rethrow for a
// real failure, so the runner reports it as a failure rather than a skip.
async function recorded(
	id: string,
	meta: TestMeta,
	body: (ctx: CapstoneContext) => Promise<void>
): Promise<void> {
	const startedAt = new Date().toISOString();
	const startedMs = Date.now();
	const evidence: Record<string, unknown> = {};
	const noNode: NoNodeVerdict[] = [];
	const gaps: string[] = [];
	const ctx: CapstoneContext = {
		note(key, value) {
			evidence[key] = value;
		},
		recordNoNode(verdict) {
			noNode.push(verdict);
			if (!verdict.checked) gaps.push(`the no-Node check could not be read: ${verdict.reason}`);
			for (const process of verdict.nodeProcesses) {
				gaps.push(`Node-like process in or beside the server: pid ${process.pid} (${process.comm})`);
			}
		},
		gap(message) {
			gaps.push(message);
		}
	};

	let outcome: Outcome = 'passed';
	let cause: string | undefined;
	let reason = '';
	let recovery: string | undefined;
	let failure: unknown;
	try {
		await body(ctx);
	} catch (error) {
		failure = error;
		if (error instanceof CapstoneAbort) {
			outcome = error.outcome;
			reason = error.message;
			recovery = error.recovery;
		} else {
			const classified = await classifyError(error);
			outcome = classified.outcome;
			cause = classified.cause;
			reason = classified.reason;
			recovery = classified.recovery;
		}
	}

	const record: RecordedResult = {
		schema: 1,
		id,
		index: meta.index,
		title: meta.title,
		runId: config.runId,
		outcome,
		...(cause ? { cause } : {}),
		reason: redact(reason, secrets),
		...(recovery ? { recovery: redact(recovery, secrets) } : {}),
		evidence,
		noNode,
		gaps: gaps.map((gap) => redact(gap, secrets)),
		startedAt,
		finishedAt: new Date().toISOString(),
		durationMs: Date.now() - startedMs
	};
	await mkdir(recordsDir, { recursive: true });
	await writeFile(recordPath(meta.index, id), `${JSON.stringify(record, null, 2)}\n`);

	if (outcome === 'passed') return;
	if (outcome === 'failed') throw failure instanceof Error ? failure : new Error(reason);
	test.skip(true, `${id}: ${reason}`);
}

// Every later test is blocked by a failed artefacts check: without a running
// image there is nothing to prove anything against, and the report must say
// that rather than showing unrelated failures.
async function blockedByArtefacts(): Promise<CapstoneAbort | null> {
	const record = await readRecord(0, 'artefacts');
	if (record && record.outcome === 'passed') return null;
	if (!record) return new CapstoneAbort('failed', 'blocked by artefacts: no artefacts record was written');
	return new CapstoneAbort(record.outcome, `blocked by artefacts: ${record.reason || record.outcome}`, record.recovery);
}

// Stage 2 ships the capstone core; stage 3 adds the real sides. The credential
// reasons come from readCapstoneConfig().missing(); a machine that somehow has
// the credentials still must not claim to have run a side that does not exist.
function realSidesStage2(proofId: string): CapstoneAbort {
	const missing = config.missing(proofId);
	if (missing) return new CapstoneAbort('skipped', missing.reason, missing.recovery);
	return new CapstoneAbort(
		'skipped',
		'the real Telegram/OpenRouter/WAHA sides are not implemented yet (stage 2 ships the capstone core)',
		'stage 3 of FEAT-5fhj6p adds e2e/fixtures/epic-real.ts and wires the sides here, then run `make test-e2e-capstone`'
	);
}

test.describe('epic capstone', () => {
	test('artefacts - image identity', async () => {
		await recorded('artefacts', { index: 0, title: 'artefacts - image identity' }, async (proof) => {
			const identity = await host.imageIdentity();
			proof.note('artefacts.image', identity);

			// Identity is metadata until a container proves the entrypoint, the
			// health endpoint and the version label line up.
			const server = await host.start({ allowedHosts: ['capstone.invalid'] });
			try {
				const health = (await (await fetch(`${server.baseURL}/api/v1/health`)).json()) as { version?: unknown };
				const version = typeof health.version === 'string' ? health.version : '';
				proof.note('artefacts.health', { version });
				if (identity.version && !['0.0.0-dev', 'unknown'].includes(identity.version)) {
					expect(version, 'the health version equals the image version label').toBe(identity.version);
				}
				const tag = identity.ref.slice(identity.ref.lastIndexOf(':') + 1);
				if (/^v\d+\.\d+\.\d+$/.test(tag)) {
					expect(version, 'the health version equals the published tag').toBe(tag);
				}
				await assertNoNodeOk(host, server, proof);
			} finally {
				await server.close();
			}
		});
	});

	test('proof 1 - Telegram bot through an AI agent', async () => {
		await recorded('proof1-telegram', { index: 1, title: 'proof 1 - Telegram bot through an AI agent' }, async () => {
			const blocked = await blockedByArtefacts();
			if (blocked) throw blocked;
			// Wired but gated: the shared proof is the one the hermetic suite
			// already runs green against the stub side; this stage has no real
			// Telegram side to hand it.
			throw realSidesStage2('proof1-telegram');
		});
	});

	test('proof 2 - WAHA chatting template, two tenants', async () => {
		await recorded('proof2-waha', { index: 2, title: 'proof 2 - WAHA chatting template, two tenants' }, async () => {
			const blocked = await blockedByArtefacts();
			if (blocked) throw blocked;
			throw realSidesStage2('proof2-waha');
		});
	});

	test('proof 3 - datastore on PostgreSQL', async () => {
		await recorded('proof3-datastore', { index: 3, title: 'proof 3 - datastore on PostgreSQL' }, async (proof) => {
			const blocked = await blockedByArtefacts();
			if (blocked) throw blocked;
			const pg = await host.startPostgres();
			if (!pg) throw new CapstoneAbort('skipped', 'the image host could not start a PostgreSQL container');
			try {
				await proofDatastoreLifecycle(host, pg, proof);
			} finally {
				await pg.close();
			}
		});
	});

	test('proof 3 - a second tenant reads nothing', async () => {
		await recorded('proof3-isolation', { index: 4, title: 'proof 3 - a second tenant reads nothing' }, async (proof) => {
			const blocked = await blockedByArtefacts();
			if (blocked) throw blocked;
			const pg = await host.startPostgres();
			if (!pg) throw new CapstoneAbort('skipped', 'the image host could not start a PostgreSQL container');
			try {
				await proofDatastoreIsolation(host, pg, proof);
			} finally {
				await pg.close();
			}
		});
	});

	test('proof 4 - external consumer', async ({ page }) => {
		await recorded('proof4-external', { index: 5, title: 'proof 4 - external consumer' }, async (proof) => {
			const blocked = await blockedByArtefacts();
			if (blocked) throw blocked;
			const source =
				config.sdkSpec === 'tarball'
					? ({ kind: 'tarball' } as const)
					: ({
							kind: 'registry',
							spec: config.sdkSpec,
							...(config.npmRegistry ? { registry: config.npmRegistry } : {})
						} as const);
			proof.note('external.sdkSpec', config.sdkSpec);
			await proofExternalConsumer(host, source, page, proof);
		});
	});

	test('corpus fidelity', async () => {
		await recorded('corpus', { index: 6, title: 'corpus fidelity' }, async (proof) => {
			const blocked = await blockedByArtefacts();
			if (blocked) throw blocked;
			const measurement = await measureCorpus(host);
			proof.note('corpus', measurement);
			for (const gap of measurement.gaps) proof.gap(gap);
			if (measurement.outcome === 'failed') throw new CapstoneAbort('failed', measurement.reason);
		});
	});
});
