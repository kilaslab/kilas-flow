/**
 * Secret-scan tests for FEAT-8mymac (stage 2).
 *
 * The scan exists because stage 2 is the first stage that holds a real
 * credential (the throwaway n8n owner password) in process memory. Its job is
 * to prove the credential never reached a file — the repository, the benchmark
 * directory, a temp directory or the playwright-cli scratch directory — and to
 * name the file, never the value, when it did.
 *
 * The planted value is built from fragments so this test file does not itself
 * contain a secret-shaped literal that the repository-wide scan would flag.
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { collectRoots, collectSecrets, scanSecrets } from './scan-secrets.mjs';

/** A value that satisfies the n8n owner-password rules and is obviously fake. */
function fakeSecret() {
	return `Kf${'0123456789ab'.repeat(1)}Z9`;
}

test('scan-secrets fails naming the file only when a planted fake secret is in a temp file, and passes otherwise', async () => {
	const secret = fakeSecret();
	const dir = await mkdtemp(join(tmpdir(), 'kilasflow-bench-scantest-'));
	const planted = join(dir, 'planted.txt');
	await writeFile(planted, `login body {"password":"${secret}"}\n`);
	try {
		// files: [] keeps this unit test to the one directory it planted; the CLI
		// always adds git's file list on top of the roots it is given.
		const bad = await scanSecrets({ secrets: [secret], roots: [dir], files: [] });
		assert.equal(bad.ok, false);
		assert.deepEqual(
			bad.findings.map((finding) => finding.file),
			[planted],
		);
		// The finding names the file, never the value.
		assert.ok(!JSON.stringify(bad).includes(secret), 'the scan must not echo the secret');

		const clean = await scanSecrets({ secrets: [`not-the-planted-${'value'}`], roots: [dir], files: [] });
		assert.equal(clean.ok, true);
		assert.deepEqual(clean.findings, []);
		assert.equal(clean.scanned, 1);
	} finally {
		await rm(dir, { recursive: true, force: true });
	}
});

test('it reads secrets from env, never argv', () => {
	const env = {
		N8N_EMAIL: `bench-${'aaaa'}@bench.example`,
		N8N_PASSWORD: fakeSecret(),
		N8N_ENCRYPTION_KEY: 'deadbeefdeadbeef',
		N8N_SESSION_COOKIE: undefined,
	};
	const collected = collectSecrets(env);
	assert.deepEqual([...collected].sort(), [`bench-aaaa@bench.example`, fakeSecret(), 'deadbeefdeadbeef'].sort());
	// Unset and implausibly short values are dropped, so the scan never hunts
	// for a one-character string that appears in every file.
	assert.deepEqual(collectSecrets({ N8N_PASSWORD: '', N8N_EMAIL: 'a' }), []);

	// argv contributes directory roots to scan, never a secret to search for.
	const roots = collectRoots(['/tmp/playwright-scratch'], env);
	assert.ok(roots.includes('/tmp/playwright-scratch'));
	assert.ok(roots.every((root) => typeof root === 'string' && !collected.includes(root)));
});
