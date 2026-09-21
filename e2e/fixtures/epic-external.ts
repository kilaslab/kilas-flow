// Epic-acceptance fixtures, part 2: the external consumer (FEAT-5fhj6p).
//
// New file only; the harness in e2e/helpers/* is untouched (startStub gained
// one optional parameter for the image host).
//
// The epic's fourth proof is an application outside this repository using
// KilasFlow with no checkout of it: `npm install @kilasflow/sdk`, drive a
// workflow and a datastore through the SDK, mount the embedded editor in a
// host page, resolve the package's subpaths the way a consumer's bundler does.
// This file scaffolds that application for real — a scratch directory under
// the OS temp dir (never inside the repo).
//
// Two sources, one code path: the registry (what the criterion names, and what
// the capstone uses by default) and the packed tarball (the rehearsal, the
// bytes `npm publish` would upload). `npm publish` is forbidden for agents, so
// the registry path is proven against a fake registry in the machinery test
// without ever publishing.
//
// Deviations from the ticket, stated plainly:
// - In tarball mode the packed file stands in for the registry: `npm pack`
//   produces byte-identical bytes to what `npm publish` uploads, and the
//   scratch project installs that file with plain `npm install`.
// - The host page performs the raw postMessage handshake (the same exchange
//   sdk/src/browser.ts performs, and the same one e2e/helpers/stub.ts's
//   /host page documents) rather than bundling the browser client. What is
//   proven is that an editor session minted through the SDK mounts in a page
//   that lives outside this repository — the SDK server client itself is the
//   installed artifact under test.
// - The no-Node.js constraint is the ticket's acceptance criterion read
//   literally: no Node.js process runs *in or beside the server*. The SDK is
//   an npm package — its consumer is Node by definition — and the Playwright
//   harness is Node too, so "anywhere" cannot include the driver or the
//   consumer without forbidding the suite itself. assertNoNodeBesideServer
//   checks the server's own process subtree mechanically; the stub and host
//   doubles are test fixtures, never server sidecars.
import { execFile } from 'node:child_process';
import { mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';
import { once } from 'node:events';

import { classifyNpmError, noNodeVerdict, parsePsTable } from '../scripts/capstone-lib.mjs';
import type { NoNodeVerdict } from './epic-proofs';

const execFileAsync = promisify(execFile);

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');

const SDK_PACKAGE = '@kilasflow/sdk';

// Runs `fn` with extra environment variables visible to the child processes
// it spawns — the same mechanism e2e/fixtures/pack-install.ts uses to point
// the harness at a packs directory, extended here to the public-URL override.
// startServer spreads process.env into the kilasflow child, so this is how a
// new file tunes server configuration without touching the harness.
export async function withEnv<T>(vars: Record<string, string>, fn: () => Promise<T>): Promise<T> {
	const previous = new Map<string, string | undefined>();
	for (const [key, value] of Object.entries(vars)) {
		previous.set(key, process.env[key]);
		process.env[key] = value;
	}
	try {
		return await fn();
	} finally {
		for (const [key, value] of previous) {
			if (value === undefined) delete process.env[key];
			else process.env[key] = value;
		}
	}
}
export interface ExternalApp {
	// Scratch directory. Outside the repository by construction (os.tmpdir()),
	// asserted below so a refactor can never silently move it back inside.
	dir: string;
	// The packed tarball in tarball mode; null in registry mode, where the
	// bytes came from the registry and never existed on this machine.
	tarball: string | null;
	// The SDK's server client, imported from the installed package — the
	// installed artifact under test, not the repo's source tree.
	KilasFlowClient: new (options: { baseUrl: string }) => ExternalClient;
	// The SDK's filter builder, proving the installed helpers work too.
	datastoreFilter: (type: 'and' | 'or', conditions: unknown[]) => unknown;
	sdkVersion: string;
	// Where the bytes came from: 'tarball' (./sdk packed locally) or 'registry'.
	source: 'tarball' | 'registry';
	// The resolved URL of each declared subpath, as a consumer resolves it from
	// inside the scratch project. This is the exports-map proof: it fails when
	// the published package cannot be resolved by a project that is not this
	// repository.
	subpaths: Record<string, string>;
	// The npm integrity recorded in the scratch project's lockfile, when the
	// install recorded one (a registry install does; a file: install need not).
	integrity: string | null;
}

// The packed server client's surface as this proof consumes it. Declared here
// rather than left as `any` so the proof asserts against a shape, not a blob.
export interface ExternalClient {
	getReady(): Promise<unknown>;
	listNodeTypes(): Promise<unknown[]>;
	createCredential(input: { name: string; type: string; fields: Record<string, string> }): Promise<{ id: string }>;
	createWorkflow(document: unknown): Promise<{ id: string }>;
	runWorkflow(workflowId: string): Promise<{ id: string }>;
	getExecution(executionId: string): Promise<{ status: string }>;
	createDatastore(input: { name: string; columns: Array<{ name: string; type: string }> }): Promise<{ id: string }>;
	addDatastoreColumn(datastoreId: string, column: { name: string; type: string }): Promise<unknown>;
	insertDatastoreRow(datastoreId: string, values: Record<string, unknown>): Promise<unknown>;
	updateDatastoreRows(datastoreId: string, filter: unknown, patch: Record<string, unknown>): Promise<unknown>;
	iterateDatastoreRows(datastoreId: string): AsyncIterable<unknown>;
	deleteDatastoreRows(datastoreId: string, filter: unknown): Promise<unknown>;
	importWorkflow(input: { format: string; name: string; workflow: unknown }): Promise<{
		workflow: { id: string };
		webhooks?: Array<{ url: string }>;
	}>;
	activateWorkflow(workflowId: string): Promise<{ active: boolean }>;
	createEmbedSession(input: { workflowId: string; origin: string; scopes: string[] }): Promise<{
		token: string;
		scopes: string[];
	}>;
}

// How the external app gets the SDK: the registry (the published package the
// criterion names) or the packed tarball (the bytes `npm publish` would upload,
// used as the local rehearsal). The source is a parameter so the capstone and
// the hermetic suite run the same proof against different bytes.
export type ExternalSource = { kind: 'tarball' } | { kind: 'registry'; spec: string; registry?: string };

/** An npm failure carrying its classification, so the record does not guess. */
export class NpmInstallError extends Error {
	readonly classification: ReturnType<typeof classifyNpmError>;
	constructor(message: string) {
		super(message);
		this.name = 'NpmInstallError';
		this.classification = classifyNpmError(message);
	}
}

export async function scaffoldExternalSource(source: ExternalSource = { kind: 'tarball' }): Promise<ExternalApp> {
	return scaffoldExternalApp(source);
}

async function tarballEntries(tarball: string): Promise<string[]> {
	const { stdout } = await execFileAsync('tar', ['-tzf', tarball]);
	return stdout.split('\n').map((line) => line.trim()).filter(Boolean);
}

// The specifier is a constant declared here, never operator input, so the
// -e script has nothing to inject; the cwd is the scratch project, which is
// what makes the resolution a consumer's rather than this repository's.
async function resolveFromProject(dir: string, specifier: string): Promise<string> {
	const { stdout } = await execFileAsync(
		'node',
		['--input-type=module', '-e', `console.log(import.meta.resolve(${JSON.stringify(specifier)}))`],
		{ cwd: dir, timeout: 60_000, maxBuffer: 4 * 1024 * 1024 }
	);
	const url = stdout.trim().split('\n').pop()?.trim() ?? '';
	if (!url.startsWith('file:')) {
		throw new Error(`import.meta.resolve(${specifier}) from ${dir} returned ${url}, want a file: URL`);
	}
	return url;
}

export async function scaffoldExternalApp(source: ExternalSource = { kind: 'tarball' }): Promise<ExternalApp> {
	const dir = await mkdtemp(join(tmpdir(), 'epic-external-'));
	if (dir === repoRoot || dir.startsWith(`${repoRoot}/`)) {
		throw new Error(`external app dir ${dir} is inside the repository; refusing`);
	}
	let tarball: string | null = null;
	if (source.kind === 'tarball') {
		const sdkDir = join(repoRoot, 'sdk');
		const { stdout: packOut } = await execFileAsync('npm', ['pack', sdkDir, '--pack-destination', dir], {
			timeout: 120_000
		});
		const tarballName = packOut
			.split('\n')
			.map((line) => line.trim())
			.find((line) => line.endsWith('.tgz'));
		if (!tarballName) throw new Error(`npm pack printed no tarball name: ${packOut}`);
		tarball = join(dir, tarballName);

		// The published tarball contains dist and README (plus CHANGELOG and the
		// manifest) and nothing else — no source, no tests. Assert the shape
		// rather than assuming `files` stayed right.
		const entries = await tarballEntries(tarball);
		if (!entries.some((entry) => entry === 'package/dist/server.js')) {
			throw new Error(`packed SDK has no dist/server.js: ${entries.slice(0, 10).join(', ')}`);
		}
		if (entries.some((entry) => entry.startsWith('package/src/'))) {
			throw new Error('packed SDK leaks src/: the published tarball must be dist-only');
		}
	}

	await writeFile(
		join(dir, 'package.json'),
		JSON.stringify({ name: 'epic-external-host', private: true, type: 'module', version: '0.0.0' }, null, 2) + '\n'
	);
	// The operator's own commands, run for real: `npm install` of a tarball or
	// of a registry spec. A registry failure is classified rather than guessed
	// at, because "the package is not published" and "the registry is down" are
	// different verdicts.
	const installArgs =
		source.kind === 'tarball'
			? ['install', '--no-audit', '--no-fund', `./${tarball!.split('/').pop()}`]
			: [
					'install',
					'--no-audit',
					'--no-fund',
					source.spec,
					...(source.registry ? ['--registry', source.registry] : [])
				];
	try {
		await execFileAsync('npm', installArgs, { cwd: dir, timeout: 300_000, maxBuffer: 16 * 1024 * 1024 });
	} catch (error) {
		const failure = error as { stdout?: string; stderr?: string };
		throw new NpmInstallError(`npm ${installArgs.join(' ')} failed:\n${failure.stderr ?? ''}\n${failure.stdout ?? ''}`);
	}

	// What was installed, not what the repository says: the scratch project's
	// own manifest entry and the installed package's own version.
	const installedPackage = JSON.parse(
		await readFile(join(dir, 'node_modules', SDK_PACKAGE, 'package.json'), 'utf-8')
	) as { name: string; version: string };
	const rootManifest = JSON.parse(await readFile(join(dir, 'package.json'), 'utf-8')) as {
		dependencies?: Record<string, string>;
	};
	if (!rootManifest.dependencies?.[SDK_PACKAGE]) {
		throw new Error(`install did not record ${SDK_PACKAGE} in the external package.json`);
	}
	const lockfile = JSON.parse(await readFile(join(dir, 'package-lock.json'), 'utf-8')) as {
		packages?: Record<string, { integrity?: string }>;
	};
	const integrity = lockfile.packages?.[`node_modules/${SDK_PACKAGE}`]?.integrity ?? null;

	// Resolution happens from inside the scratch project, which is the only way
	// to prove the exports map: importing the repository's own sdk/dist would
	// test this checkout instead of the installed artifact.
	const subpaths: Record<string, string> = {};
	for (const subpath of ['', '/server', '/browser']) {
		subpaths[subpath === '' ? '.' : `.${subpath}`] = await resolveFromProject(dir, `${SDK_PACKAGE}${subpath}`);
	}
	for (const url of Object.values(subpaths)) {
		if (!url.includes('node_modules')) {
			throw new Error(`a subpath resolved outside the scratch project: ${url}`);
		}
	}
	// Runtime-selected by construction: the specifier is a path under a temp
	// directory created above and populated by `npm install`, unknown at author
	// time. Importing the repository's sdk/dist instead would test this
	// checkout rather than the installed artifact, which is the whole point.
	const clientModule = (await import(subpaths['./server'])) as {
		KilasFlowClient: ExternalApp['KilasFlowClient'];
		datastoreFilter: ExternalApp['datastoreFilter'];
	};
	if (typeof clientModule.KilasFlowClient !== 'function') {
		throw new Error(`installed ${SDK_PACKAGE} exports no KilasFlowClient`);
	}
	if (typeof clientModule.datastoreFilter !== 'function') {
		throw new Error(`installed ${SDK_PACKAGE} exports no datastoreFilter`);
	}
	return {
		dir,
		tarball,
		KilasFlowClient: clientModule.KilasFlowClient,
		datastoreFilter: clientModule.datastoreFilter,
		sdkVersion: installedPackage.version,
		source: source.kind,
		subpaths,
		integrity
	};
}

// Writes the external host's page into its own directory: it frames the
// editor and answers the `kilasflow:embed-ready` announcement with the
// SDK-minted session — the same exchange sdk/src/browser.ts performs —
// recording every message so the test awaits the handshake, never sleeps.
export async function writeHostPage(dir: string): Promise<void> {
	await writeFile(
		join(dir, 'host.html'),
		`<!doctype html>
<html><head><meta charset="utf-8"><title>external host</title></head>
<body>
<iframe id="external-frame" title="Embedded editor" style="width:100%;height:90vh;border:0"></iframe>
<script>
window.__externalEvents = [];
var query = new URLSearchParams(location.search);
var server = query.get('server');
var workflowId = query.get('workflow');
var token = query.get('token');
var scopes = (query.get('scopes') || '').split(',').filter(Boolean);
var frame = document.getElementById('external-frame');
window.addEventListener('message', function (event) {
	window.__externalEvents.push({ origin: event.origin, type: event.data && event.data.type });
	if (event.data && event.data.type === 'kilasflow:embed-ready') {
		event.source.postMessage(
			{ type: 'kilasflow:embed-session', token: token, workflowId: workflowId, scopes: scopes, branding: {} },
			event.origin
		);
	}
});
frame.src = server + '/embed/' + encodeURIComponent(workflowId);
</script>
</body></html>
`
	);
}

export interface ExternalHost {
	origin: string;
	url: (path?: string) => string;
	close: () => Promise<void>;
}

// Serves the scratch directory over loopback: the host page is fetched from
// outside the repository, on its own origin, the realistic cross-origin shape.
export async function startExternalHost(dir: string): Promise<ExternalHost> {
	const server: Server = createServer((request: IncomingMessage, response: ServerResponse) => {
		const url = new URL(request.url ?? '/', 'http://127.0.0.1');
		if (url.pathname !== '/host.html') {
			response.writeHead(404, { 'content-type': 'text/plain' });
			response.end('not found');
			return;
		}
		readFile(join(dir, 'host.html'), 'utf-8').then(
			(body) => {
				response.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
				response.end(body);
			},
			() => {
				response.writeHead(500, { 'content-type': 'text/plain' });
				response.end('host page missing');
			}
		);
	});
	server.listen(0, '127.0.0.1');
	await once(server, 'listening');
	const address = server.address();
	if (!address || typeof address === 'string') throw new Error('Unable to start the external host');
	const origin = `http://127.0.0.1:${address.port}`;
	let closed = false;
	return {
		origin,
		url: (path = '/') => `${origin}${path}`,
		close: async () => {
			if (closed) return;
			closed = true;
			server.close();
			await once(server, 'close').catch(() => undefined);
		}
	};
}

// The ticket's no-Node.js criterion, checked mechanically against the live
// server: resolve the listener's pid(s) via lsof, then judge the server's
// process subtree with scripts/capstone-lib.mjs — the one implementation this
// fixture and the capstone's report CLI share. The harness's own node
// processes (workers, stubs, hosts) are ancestors or unrelated pids, never
// descendants of the server, and are therefore out of scope by the criterion's
// own wording ("in or beside the server").
export async function assertNoNodeBesideServer(port: number, scope = `tcp:${port}`): Promise<NoNodeVerdict> {
	let listenerOut: string;
	try {
		// LISTEN only: without the state filter every established client
		// socket (the harness's own fetch calls, which are Node) matches
		// the port too, and the check would blame the driver.
		const result = await execFileAsync('lsof', ['-ti', `tcp:${port}`, '-sTCP:LISTEN']);
		listenerOut = result.stdout;
	} catch (error: unknown) {
		const missing = error instanceof Error && 'code' in error && (error as { code?: string }).code === 'ENOENT';
		return {
			checked: false,
			reason: missing
				? 'lsof is not installed; cannot resolve the server pid'
				: `lsof found no listener on ${scope}`,
			scope,
			serverComm: '',
			processes: [],
			nodeProcesses: []
		};
	}
	const serverPids = listenerOut
		.split('\n')
		.map((line) => Number(line.trim()))
		.filter((pid) => Number.isInteger(pid) && pid > 0);
	if (serverPids.length === 0) {
		return {
			checked: false,
			reason: `lsof found no listener on ${scope}`,
			scope,
			serverComm: '',
			processes: [],
			nodeProcesses: []
		};
	}
	const { stdout: table } = await execFileAsync('ps', ['-eo', 'pid,ppid,comm']);
	return noNodeVerdict({ rows: parsePsTable(table), rootPids: serverPids, scope });
}
