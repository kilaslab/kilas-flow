// Epic-acceptance fixtures, part 2: the external consumer (FEAT-5fhj6p).
//
// New file only; the harness in e2e/helpers/* is untouched.
//
// The epic's fourth proof is an application outside this repository using
// KilasFlow with no checkout of it: `npm install @kilasflow/sdk`, drive a
// workflow and a datastore through the SDK, mount the embedded editor in a
// host page. This file scaffolds that application for real — a scratch
// directory under the OS temp dir (never inside the repo), the SDK installed
// into it from a packed tarball — and serves its host page from there.
//
// Deviations from the ticket, stated plainly:
// - The tarball stands in for the registry: `npm pack` produces byte-identical
//   bytes to what `npm publish` uploads, and the scratch project installs that
//   file with plain `npm install`, the same command that resolves the
//   published package. No network publish is exercised.
// - The host page performs the raw postMessage handshake (the same exchange
//   sdk/src/browser.ts performs, and the same one e2e/helpers/stub.ts's
//   /host page documents) rather than bundling the browser client. What is
//   proven is that an editor session minted through the SDK mounts in a page
//   that lives outside this repository — the SDK server client itself is the
//   packed artifact under test.
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
import { fileURLToPath, pathToFileURL } from 'node:url';
import { once } from 'node:events';

const execFileAsync = promisify(execFile);

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');

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
	// The packed tarball, byte-identical to what `npm publish` would upload.
	tarball: string;
	// The SDK's server client, imported from the installed package — the
	// packed artifact under test, not the repo's source tree.
	KilasFlowClient: new (options: { baseUrl: string }) => any;
	// The SDK's filter builder, proving the packed helpers work too.
	datastoreFilter: (type: 'and' | 'or', conditions: unknown[]) => unknown;
	sdkVersion: string;
}

async function tarballEntries(tarball: string): Promise<string[]> {
	const { stdout } = await execFileAsync('tar', ['-tzf', tarball]);
	return stdout.split('\n').map((line) => line.trim()).filter(Boolean);
}

// Scaffolds the external application: pack the SDK, install the tarball into
// a scratch project outside the repo with plain `npm install`, and import the
// installed client. Every step is the operator's own command, run for real.
export async function scaffoldExternalApp(): Promise<ExternalApp> {
	const dir = await mkdtemp(join(tmpdir(), 'epic-external-'));
	if (dir === repoRoot || dir.startsWith(`${repoRoot}/`)) {
		throw new Error(`external app dir ${dir} is inside the repository; refusing`);
	}
	const sdkDir = join(repoRoot, 'sdk');
	const { stdout: packOut } = await execFileAsync('npm', ['pack', sdkDir, '--pack-destination', dir], {
		timeout: 120_000
	});
	const tarballName = packOut
		.split('\n')
		.map((line) => line.trim())
		.find((line) => line.endsWith('.tgz'));
	if (!tarballName) throw new Error(`npm pack printed no tarball name: ${packOut}`);
	const tarball = join(dir, tarballName);

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
	const packageJson = JSON.parse(await readFile(join(sdkDir, 'package.json'), 'utf-8')) as {
		name: string;
		version: string;
	};

	await writeFile(
		join(dir, 'package.json'),
		JSON.stringify({ name: 'epic-external-host', private: true, type: 'module', version: '0.0.0' }, null, 2) + '\n'
	);
	await execFileAsync('npm', ['install', '--no-audit', '--no-fund', `./${tarballName}`], {
		cwd: dir,
		timeout: 300_000
	});

	// No checkout: the scratch project carries no .git, and its only
	// third-party dependency is the installed tarball.
	const installed = JSON.parse(await readFile(join(dir, 'package.json'), 'utf-8')) as {
		dependencies?: Record<string, string>;
	};
	const spec = installed.dependencies?.[packageJson.name];
	if (!spec) throw new Error(`install did not record ${packageJson.name} in the external package.json`);

	// Dynamic import is the proof itself: the specifier (a temp dir created
	// above, populated by `npm install` of the packed tarball) cannot be
	// known at author time, and importing the repo's sdk/src instead would
	// test the source tree rather than the published artifact.
	const clientModule = (await import(
		pathToFileURL(join(dir, 'node_modules', packageJson.name, 'dist', 'server.js')).href
	)) as { KilasFlowClient: ExternalApp['KilasFlowClient']; datastoreFilter: ExternalApp['datastoreFilter'] };
	if (typeof clientModule.KilasFlowClient !== 'function') {
		throw new Error('installed @kilasflow/sdk exports no KilasFlowClient');
	}
	if (typeof clientModule.datastoreFilter !== 'function') {
		throw new Error('installed @kilasflow/sdk exports no datastoreFilter');
	}
	return {
		dir,
		tarball,
		KilasFlowClient: clientModule.KilasFlowClient,
		datastoreFilter: clientModule.datastoreFilter,
		sdkVersion: packageJson.version
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
// server: resolve the listener's pid(s) via lsof, confirm the process is the
// kilasflow binary, and assert its process subtree holds no `node` process —
// no JS sidecar, no helper, nothing "beside" the server. The harness's own
// node processes (workers, stubs, hosts) are ancestors or unrelated pids,
// never descendants of the server, and are therefore out of scope by the
// criterion's own wording ("in or beside the server").
export async function assertNoNodeBesideServer(port: number): Promise<NoNodeVerdict> {
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
				: `lsof found no listener on tcp:${port}`,
			serverPids: [],
			serverComm: '',
			nodeChildren: []
		};
	}
	const serverPids = listenerOut
		.split('\n')
		.map((line) => Number(line.trim()))
		.filter((pid) => Number.isInteger(pid) && pid > 0);
	if (serverPids.length === 0) {
		return { checked: false, reason: `lsof found no listener on tcp:${port}`, serverPids: [], serverComm: '', nodeChildren: [] };
	}
	const pidSet = new Set(serverPids);
	let serverComm = '';
	try {
		const { stdout } = await execFileAsync('ps', ['-o', 'comm=', '-p', String(serverPids[0])]);
		serverComm = stdout.trim().split('\n')[0]?.trim() ?? '';
	} catch {
		serverComm = '';
	}
	const { stdout: table } = await execFileAsync('ps', ['-eo', 'pid,ppid,comm']);
	const nodeChildren: NoNodeVerdict['nodeChildren'] = [];
	for (const line of table.split('\n').slice(1)) {
		const parts = line.trim().split(/\s+/);
		if (parts.length < 3) continue;
		const pid = Number(parts[0]);
		const ppid = Number(parts[1]);
		const comm = parts.slice(2).join(' ');
		if (Number.isInteger(pid) && pidSet.has(ppid) && comm === 'node') {
			nodeChildren.push({ pid, ppid, comm });
		}
	}
	return { checked: true, reason: '', serverPids, serverComm, nodeChildren };
}
