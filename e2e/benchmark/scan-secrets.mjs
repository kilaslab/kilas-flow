/**
 * Secret scanner for FEAT-8mymac (stage 2).
 *
 * Stage 2 is the first stage that holds a real credential in process memory:
 * the throwaway n8n owner password, generated from `openssl rand` and exported
 * only as a shell environment variable. The rule is absolute — a credential
 * never lands in a file, a ticket, a log, an argv shown in output or a commit —
 * so this scan is the enforcement: it looks for the live secret values across
 * every file that could have captured them and fails loudly, naming the FILE
 * and never the value (an error that echoes a secret writes it to a log).
 *
 * What it scans, over the union of:
 *   - every path `git ls-files -co --exclude-standard` reports (all tracked and
 *     untracked-but-not-ignored files, not only modified ones, so stage-1 files
 *     stay covered);
 *   - e2e/benchmark/ (the harness's own directory);
 *   - os.tmpdir()/kilasflow-bench-* (the harness's temp data and smoke output);
 *   - every directory passed as a command-line argument (the playwright-cli
 *     scratch directory the browser discovery runs in).
 *
 * Where the secrets come from: the environment only. `collectSecrets` reads the
 * four variables `summarise.mjs` already treats as secret; argv carries
 * directory names and nothing else.
 *
 * Standalone:
 *
 *   node e2e/benchmark/scan-secrets.mjs <dir> [<dir> ...]
 *
 * exits 0 when clean, 1 when a secret is found.
 */

import { execFile } from 'node:child_process';
import { readdirSync } from 'node:fs';
import { readdir, readFile, realpath, stat } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

import { secretsFromEnv } from './summarise.mjs';

const execFileAsync = promisify(execFile);
const REPO_DIR = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
const TMP_PREFIX = 'kilasflow-bench-';

/** Shortest value worth searching for; below this the scan is all false hits. */
const MIN_SECRET_LENGTH = 6;

/** Cap per file so a stray multi-gigabyte artifact cannot wedge the scan. */
const MAX_FILE_BYTES = 64 * 1024 * 1024;

/**
 * The live secrets, from the environment and nowhere else.
 *
 * The n8n session cookie is included because a browser or a script that logged
 * in may have captured it into a snapshot; the encryption key because it
 * decrypts every stored credential in the throwaway instance.
 */
export function collectSecrets(env = process.env) {
	return secretsFromEnv(env).filter((value) => typeof value === 'string' && value.length >= MIN_SECRET_LENGTH);
}

/** Does this text/buffer contain any of the secrets? Index of the first match. */
function firstMatchingSecret(bytes, secrets) {
	for (const secret of secrets) {
		if (bytes.includes(secret)) return secret;
	}
	return null;
}

/** Every directory the CLI should walk: the defaults plus the argv directories. */
export function collectRoots(argv = [], env = process.env) {
	const roots = [join(REPO_DIR, 'e2e', 'benchmark'), ...tmpBenchDirs(tmpdir())];
	for (const entry of argv) {
		if (typeof entry === 'string' && entry.trim() !== '') roots.push(resolve(entry));
	}
	// env is accepted so the signature is symmetric with collectSecrets and a
	// caller cannot pass a secret where a root is expected without noticing.
	void env;
	return [...new Set(roots)];
}

/** os.tmpdir()/kilasflow-bench-* directories, if any exist. */
export function tmpBenchDirs(root = tmpdir()) {
	try {
		return readdirSync(root, { withFileTypes: true })
			.filter((entry) => entry.isDirectory() && entry.name.startsWith(TMP_PREFIX))
			.map((entry) => join(root, entry.name));
	} catch {
		return [];
	}
}

/** Tracked plus untracked-but-not-ignored files, as git reports them. */
async function gitFiles(cwd) {
	try {
		const { stdout } = await execFileAsync('git', ['ls-files', '-co', '--exclude-standard', '-z'], { cwd });
		return stdout
			.split('\x00')
			.filter((entry) => entry !== '')
			.map((entry) => join(cwd, entry));
	} catch {
		return [];
	}
}

/** Recursively list regular files under a directory; symlinks are not followed. */
async function walk(dir, out = []) {
	let entries;
	try {
		entries = await readdir(dir, { withFileTypes: true });
	} catch {
		return out;
	}
	for (const entry of entries) {
		const path = join(dir, entry.name);
		if (entry.isDirectory()) await walk(path, out);
		else if (entry.isFile()) out.push(path);
	}
	return out;
}

/**
 * Scan a set of files and directories for the live secrets.
 *
 * roots/files are explicit when given (the CLI and the tests pass what they
 * mean); otherwise the defaults run: git's file list, e2e/benchmark and the
 * harness's temp directories.
 */
export async function scanSecrets({ secrets = collectSecrets(), roots, files, cwd = process.cwd() } = {}) {
	const dirs = roots ?? [join(REPO_DIR, 'e2e', 'benchmark'), ...tmpBenchDirs(tmpdir())];
	// The git file list is always included: it is the set of bytes the repository
	// carries, and a caller passing extra roots (a scratch dir) must not narrow
	// that coverage. Only an explicit `files` list replaces it.
	const explicitFiles = files ?? (await gitFiles(cwd));

	const candidates = [...explicitFiles];
	for (const dir of dirs) await walk(dir, candidates);

	const seen = new Set();
	const findings = [];
	let scanned = 0;
	for (const candidate of candidates) {
		let real;
		try {
			real = await realpath(candidate);
		} catch {
			continue; // A dangling path is not a finding.
		}
		if (seen.has(real)) continue;
		seen.add(real);
		let stats;
		try {
			stats = await stat(real);
		} catch {
			continue;
		}
		if (!stats.isFile() || stats.size > MAX_FILE_BYTES) continue;
		scanned++;
		let bytes;
		try {
			bytes = await readFile(real);
		} catch {
			continue;
		}
		if (firstMatchingSecret(bytes.toString('latin1'), secrets) !== null) findings.push({ file: candidate });
	}

	return { ok: findings.length === 0, findings, scanned, secretsChecked: secrets.length };
}

/** Also used by a caller that already has the bytes in hand (e.g. a probe). */
export function scanText(text, secrets = collectSecrets()) {
	return firstMatchingSecret(typeof text === 'string' ? text : String(text), secrets) === null;
}

async function main() {
	const args = process.argv.slice(2);
	const secrets = collectSecrets();
	if (secrets.length === 0) {
		console.log('scan-secrets: no N8N_* secrets in the environment; nothing to search for (this is a pass, not a skip)');
		return;
	}
	const roots = collectRoots(args);
	const result = await scanSecrets({ secrets, roots });
	if (result.ok) {
		console.log(`scan-secrets: ${result.scanned} files scanned, no secret material found`);
		return;
	}
	for (const finding of result.findings) console.error(`scan-secrets: FAIL — secret material found in ${finding.file}`);
	process.exitCode = 1;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
	await main();
}
