import { execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import { appendFile, mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { createServer, type Server } from 'node:http';
import { once } from 'node:events';
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

export async function startPackServerWithEndpoint(stubEndpoint: string, packsDir: string): Promise<E2EServer> {
	const previous = process.env[PACKS_ENV_KEY];
	process.env[PACKS_ENV_KEY] = packsDir;
	try {
		return await startServer({ stubEndpoint });
	} finally {
		if (previous === undefined) delete process.env[PACKS_ENV_KEY];
		else process.env[PACKS_ENV_KEY] = previous;
	}
}

export async function startPackServer(stub: StubServer, packsDir: string): Promise<E2EServer> {
	return startPackServerWithEndpoint(`127.0.0.1:${stub.port}`, packsDir);
}

// A loopback observer that records request headers as well as method, path
// and body. The shared stub records no headers, so a test proving a pack
// node authenticated its call (X-Api-Key placement) points the credential at
// one of these instead and boots its pack server against the observer's
// endpoint. One exact host:port, so the outbound guard stays on for
// everything else and allow_private_networks is never set.
export interface ObservedRequest {
	method: string;
	path: string;
	headers: Record<string, string | string[] | undefined>;
	body: string;
}

export interface HeaderObserver {
	port: number;
	origin: string;
	requests: ObservedRequest[];
	close: () => Promise<void>;
}

export async function startHeaderObserver(): Promise<HeaderObserver> {
	const requests: ObservedRequest[] = [];
	const server: Server = createServer((request, response) => {
		const chunks: Buffer[] = [];
		request.on('data', (chunk: Buffer) => chunks.push(chunk));
		request.on('end', () => {
			const url = new URL(request.url ?? '/', 'http://127.0.0.1');
			requests.push({
				method: request.method ?? 'GET',
				path: url.pathname,
				headers: { ...request.headers },
				body: Buffer.concat(chunks).toString('utf-8')
			});
			response.writeHead(200, { 'content-type': 'application/json' });
			response.end(JSON.stringify({ ok: true, method: request.method, path: url.pathname }));
		});
	});
	server.listen(0, '127.0.0.1');
	await once(server, 'listening');
	const address = server.address();
	if (!address || typeof address === 'string') throw new Error('Unable to start the header observer');
	const port = address.port;
	let closed = false;
	return {
		port,
		origin: `http://127.0.0.1:${port}`,
		requests,
		close: async () => {
			if (closed) return;
			closed = true;
			server.close();
			await once(server, 'close').catch(() => undefined);
		}
	};
}

// Writes a purpose-built minimal trigger pack: two events plus a catch-all,
// an HMAC-free webhook binding, and a declarative lifecycle that registers
// the minted URL with the service on activation and removes it on
// deactivation. Modelled on packs/waha/pack-trigger-202409.json at the
// smallest size that still exercises the path — and under its own type, so
// the disk-loaded install never collides with the embedded WAHA packs the
// server always registers. The caller seals the directory with
// `nodepackgen pack`, which refuses to seal it when it does not validate.
export const TRIGGER_PACK_TYPE = 'pack.e2ealert';
export const TRIGGER_PACK_VERSION = 1;

export async function scaffoldTriggerPack(packsDir: string, name: string, type: string): Promise<string> {
	const dir = join(packsDir, name);
	await mkdir(dir, { recursive: true });
	const manifest = {
		type,
		version: 1,
		displayName: 'E2E Alert Trigger',
		description: 'Starts a workflow when the stub service delivers an event.',
		category: 'Triggers',
		icon: 'builtin:message-circle',
		subtitle: '{{ $parameter.session }}',
		credentialType: 'wahaApi',
		requestDefaults: {},
		trigger: {
			events: ['message', 'session.status'],
			catchAll: 'other',
			eventPath: 'event',
			shape: 'bodyAsItem',
			webhook: { name: 'default', pathParameter: 'path', method: 'POST' },
			lifecycle: {
				id: 'e2e.alert.webhook',
				enabledParameter: 'autoRegister',
				set: {
					method: 'PUT',
					url: '{{ .baseUrl }}/api/subscriptions/{{ .Parameter.session }}',
					headers: { 'Content-Type': 'application/json' },
					body: '{"url":"{{ .PublicURL }}"}',
					credentialType: 'wahaApi'
				},
				remove: {
					method: 'DELETE',
					url: '{{ .baseUrl }}/api/subscriptions/{{ .Parameter.session }}',
					credentialType: 'wahaApi'
				}
			},
			notice: 'The stub service is not configured to deliver here yet. Add {{ url }} to its subscription, or turn on auto-register and activate again.'
		},
		parameters: [
			{ key: 'path', label: 'Path', description: 'A label for this endpoint. The public URL uses an opaque route minted on activation.', kind: 'string', required: true },
			{ key: 'session', label: 'Session', description: 'The service session this trigger belongs to. Used when registering the webhook.', kind: 'string', default: 'default' },
			{ key: 'autoRegister', label: 'Register this URL with the service', description: 'Install this workflow\'s webhook URL into the service session on activation and remove it on deactivation.', kind: 'boolean', default: false }
		],
		generator: { tool: 'nodepackgen', source: 'e2e-fixture' }
	};
	await writeFile(join(dir, 'pack.json'), `${JSON.stringify(manifest, null, 2)}\n`);
	return dir;
}

// The converter path (FEAT-ed6wdy, box 7): a real declarative transcription
// becomes an installable pack directory through nodepack.ConvertDocument,
// never by hand-writing pack.json. The driver is build-time tooling run with
// `go run`, so it is never on the server's boot path.
export const CONVERTED_PACK_TYPE = 'pack.e2eacme';
export const CONVERTED_PACK_DIR_NAME = 'acme';
export const CONVERTED_SOURCE_TYPE = 'n8n-nodes-acme.AcmeMail';

export async function convertDeclarativePack(packsDir: string, name: string, packType: string): Promise<{ packDir: string; reportPath: string }> {
	const packDir = join(packsDir, name);
	const reportPath = join(packsDir, `${name}-report.md`);
	const transcription = join(repoRoot, 'e2e', 'fixtures', 'pack-acme-transcription.json');
	try {
		const result = await execFileAsync(
			'go',
			['run', join(repoRoot, 'e2e', 'fixtures', 'pack-convert-driver.go'), '-in', transcription, '-type', packType, '-out', packDir, '-report', reportPath],
			{ cwd: repoRoot, timeout: 300_000, maxBuffer: 16 * 1024 * 1024 }
		);
		if (!/pack-convert:/.test(result.stderr)) throw new Error(`converter driver printed nothing recognisable:\n${result.stderr}\n${result.stdout}`);
	} catch (error) {
		const failure = error as { stdout?: string; stderr?: string; message?: string };
		throw new Error(`pack-convert failed:\n${failure.stderr ?? ''}\n${failure.stdout ?? ''}\n${failure.message ?? ''}`);
	}
	return { packDir, reportPath };
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
