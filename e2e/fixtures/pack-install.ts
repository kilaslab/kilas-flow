import { execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import { appendFile, mkdtemp, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';

import { startServer, type E2EServer } from '../helpers/server';
import type { StubServer } from '../helpers/stub';

const execFileAsync = promisify(execFile);

// Pack-install fixtures (FEAT-ykyfbd). Everything the pack-install spec needs
// beyond the shared harness: driving the Go authoring toolchain
// (nodepackgen scaffold/validate/pack — never a Node process serving
// anything), laying out install directories, and booting a server instance
// against one directory via KILASFLOW_PACKS_DIR. The shared harness is
// untouched: these helpers set the environment variable around startServer,
// which spreads process.env into the child, then restore it.

export const PACK_TYPE = 'pack.e2ehello';
export const PACK_DIR_NAME = 'hello';
export const PACKS_ENV_KEY = 'KILASFLOW_PACKS_DIR';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');

// The Go toolchain, built once per worker process. The binary path carries
// the pid so parallel workers never race on one output file.
let binaryPromise: Promise<string> | undefined;
export function nodepackgenBinary(): Promise<string> {
	if (!binaryPromise) {
		binaryPromise = (async () => {
			const out = join(tmpdir(), `kilasflow-nodepackgen-${process.pid}`);
			await execFileAsync('go', ['build', '-o', out, './cmd/nodepackgen'], {
				cwd: repoRoot,
				timeout: 300_000,
				maxBuffer: 64 * 1024 * 1024
			});
			return out;
		})();
	}
	return binaryPromise;
}

export async function runNodepackgen(binary: string, args: string[]): Promise<{ stdout: string; stderr: string }> {
	try {
		const result = await execFileAsync(binary, args, { cwd: repoRoot, timeout: 120_000, maxBuffer: 16 * 1024 * 1024 });
		return { stdout: result.stdout, stderr: result.stderr };
	} catch (error) {
		const failure = error as { stdout?: string; stderr?: string; message?: string };
		throw new Error(`nodepackgen ${args.join(' ')} failed:\n${failure.stderr ?? ''}\n${failure.stdout ?? ''}\n${failure.message ?? ''}`);
	}
}

// The author path: scaffold a minimal pack shaped after packs/telegram
// (one resource, one operation, one credential reference) that authenticates
// with the built-in wahaApi type — header placement, so the scaffold's plain
// "/sendMessage" URL needs no {credential.…} path marker, and baseUrl stays
// a non-secret field the pack can template over.
export async function scaffoldPack(binary: string, packsDir: string, name: string, type: string): Promise<string> {
	const dir = join(packsDir, name);
	await runNodepackgen(binary, [
		'scaffold',
		'-dir',
		dir,
		'-type',
		type,
		'-display-name',
		'E2E Hello',
		'-credential-type',
		'wahaApi'
	]);
	return dir;
}

export function sha256Hex(data: string | Buffer): string {
	return createHash('sha256').update(data).digest('hex');
}

// The operator seal in `sha256sum` shape, the same record WriteChecksum
// produces and LoadDir verifies.
export async function writeChecksum(packDir: string, manifest: string | Buffer): Promise<void> {
	await writeFile(join(packDir, 'pack.sha256'), `${sha256Hex(manifest)}  pack.json\n`);
}

export async function freshPacksDir(): Promise<string> {
	return mkdtemp(join(tmpdir(), 'kilasflow-e2e-packs-'));
}

export async function tamperManifest(packDir: string): Promise<void> {
	// Trailing whitespace keeps the JSON valid while breaking the digest, so
	// the failure is the checksum refusal and nothing else.
	await appendFile(join(packDir, 'pack.json'), '\n');
}

export async function startPackServer(stub: StubServer, packsDir: string): Promise<E2EServer> {
	const previous = process.env[PACKS_ENV_KEY];
	process.env[PACKS_ENV_KEY] = packsDir;
	try {
		return await startServer({ stubEndpoint: `127.0.0.1:${stub.port}` });
	} finally {
		if (previous === undefined) delete process.env[PACKS_ENV_KEY];
		else process.env[PACKS_ENV_KEY] = previous;
	}
}

// Boots against a packs directory that must refuse startup, and returns the
// refusal text (the server's logs via startServer's readiness error). A
// server that comes up anyway is a test failure, not a passing boot: it is
// closed before failing so no stray instance survives.
export async function bootRefusal(stub: StubServer, packsDir: string): Promise<string> {
	let server: E2EServer | undefined;
	try {
		server = await startPackServer(stub, packsDir);
	} catch (error) {
		return error instanceof Error ? error.message : String(error);
	}
	await server.close();
	throw new Error(`expected startup to refuse packs dir ${packsDir}, but the server became ready`);
}
