// Machinery tests for FEAT-5fhj6p: the pure process-table logic behind the
// epic's no-Node.js predicate.
//
// These run on the per-PR path and need nothing: no server, no docker, no
// browser, no skip reason. What they cover is the predicate every epic proof
// calls while its server is live (e2e/fixtures/epic-proofs.ts ->
// EpicHost.assertNoNode -> scripts/capstone-lib.mjs), so a defect here would
// silently weaken all four proofs at once — which is exactly how the previous
// version failed: it matched one exact `comm` string and walked only direct
// children, while its comment claimed a subtree check.
import { test, expect } from '@playwright/test';
import { execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import { once } from 'node:events';
import { mkdtemp, readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';
import { scaffoldExternalSource } from '../fixtures/epic-external';
import {
	classifyFailure,
	classifyNpmError,
	classifyPullError,
	containerStackVerdict,
	corpusName,
	descendants,
	exitCodeFor,
	isNodeLike,
	noNodeVerdict,
	parseDockerTop,
	parsePsTable,
	redact,
	renderMarkdown
} from '../scripts/capstone-lib.mjs';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
const execFileAsync = promisify(execFile);

// A process table in the shape `ps -eo pid,ppid,comm` prints it, header
// included, so parsePsTable is exercised by every case rather than stubbed.
function ps(rows: Array<{ pid: number; ppid: number; comm: string }>): string {
	const lines = ['  PID  PPID COMM'];
	for (const row of rows) lines.push(`${row.pid} ${row.ppid} ${row.comm}`);
	return `${lines.join('\n')}\n`;
}

const KILASFLOW = { pid: 100, ppid: 1, comm: 'kilasflow' };

test('no-Node predicate: names are compared as whole basenames, not substrings', () => {
	// `node_exporter` is a Go binary; `mynode` is nothing to do with Node.
	// An exact-string or substring match over `comm` gets both wrong.
	expect(isNodeLike({ comm: 'node' })).toBe(true);
	expect(isNodeLike({ comm: '/usr/local/bin/node' })).toBe(true);
	expect(isNodeLike({ comm: 'node_exporter' })).toBe(false);
	expect(isNodeLike({ comm: 'mynode' })).toBe(false);
	expect(isNodeLike({ comm: 'kilasflow' })).toBe(false);
	// `env node` is Node launched through env; the first args token is `env`,
	// so the second one is the program that actually runs.
	expect(isNodeLike({ comm: 'env', args: ['env', 'node', 'server.js'] })).toBe(true);
	expect(isNodeLike({ comm: 'sh', args: ['sh', '-c', 'node server.js'] })).toBe(false);
});

test('no-Node predicate: a container whose comm is a thread name is still flagged from its args', () => {
	// Observed on Docker Desktop (2026-09-20): a container running
	// `node -e ...` reports comm `MainThread` — the name of the process's main
	// thread — while the args table carries the real program:
	// `{MainThread} node -e ...`. Matching only the first argv token (or only
	// comm) misses Node running beside the server entirely, which is the one
	// thing this predicate exists to catch.
	expect(isNodeLike({ comm: 'MainThread', args: '{MainThread} node -e setInterval(() => {}, 1000)' })).toBe(true);
	expect(isNodeLike({ comm: 'MainThread', args: '{MainThread} /usr/local/bin/node server.js' })).toBe(true);
	// A thread name on its own is not evidence of anything.
	expect(isNodeLike({ comm: 'MainThread', args: '{MainThread}' })).toBe(false);
	// And the same shape through the stack verdict: it fails naming the
	// container and the pid.
	const verdict = containerStackVerdict([
		{
			name: 'kf-capstone-run1-sidecar',
			rows: [
				{
					pid: 1,
					ppid: 0,
					comm: 'MainThread',
					args: '{MainThread} node -e setInterval(() => {}, 1000)'
				}
			],
			isKilasFlow: false
		}
	]);
	expect(verdict.ok).toBe(false);
	expect(verdict.reason).toContain('kf-capstone-run1-sidecar');
	expect(verdict.reason).toContain('pid 1');
});

test('no-Node predicate: a node process whose comm is a full path is flagged (robustness: comm is not always a basename)', () => {
	const rows = parsePsTable(
		ps([
			KILASFLOW,
			{ pid: 200, ppid: 100, comm: '/usr/local/bin/node' }
		])
	);
	const verdict = noNodeVerdict({ rows, rootPids: [100], scope: 'tcp:1234' });
	expect(verdict.checked).toBe(true);
	expect(verdict.reason).toBe('');
	expect(verdict.serverComm).toContain('kilasflow');
	expect(verdict.nodeProcesses.map((entry) => entry.pid)).toEqual([200]);
});

test('no-Node predicate: a node process reached through a shell child is flagged', () => {
	// The defect this test was written for: `sh -c node ...` runs Node as a
	// GRANDCHILD of the server, and the previous loop only inspected direct
	// children — a sidecar started that way slipped past every proof.
	const rows = parsePsTable(
		ps([
			KILASFLOW,
			{ pid: 200, ppid: 100, comm: 'sh' },
			{ pid: 300, ppid: 200, comm: 'node' }
		])
	);
	const verdict = noNodeVerdict({ rows, rootPids: [100], scope: 'tcp:1234' });
	expect(verdict.checked).toBe(true);
	// The whole subtree is walked, roots included.
	expect(descendants(rows, [100]).map((entry) => entry.pid)).toEqual([100, 200, 300]);
	expect(verdict.nodeProcesses.map((entry) => entry.pid)).toEqual([300]);
});

test('no-Node predicate: an unrelated node process outside the server subtree is ignored', () => {
	// The harness itself is Node (workers, stubs, hosts). Those are never
	// descendants of the server, and the criterion is "in or beside the
	// server", so blaming them would make the suite blame its own driver.
	const rows = parsePsTable(
		ps([
			KILASFLOW,
			{ pid: 200, ppid: 1, comm: 'node' },
			{ pid: 300, ppid: 100, comm: 'sh' }
		])
	);
	const verdict = noNodeVerdict({ rows, rootPids: [100], scope: 'tcp:1234' });
	expect(verdict.checked).toBe(true);
	expect(verdict.nodeProcesses).toEqual([]);
});

test('no-Node predicate: no resolved server pid is reported, never silently clean', () => {
	// A verdict that cannot see the server must not read as "no Node found":
	// proofs assert on `checked`, so an unresolved listener has to say so.
	const verdict = noNodeVerdict({ rows: parsePsTable(ps([KILASFLOW])), rootPids: [], scope: 'tcp:1234' });
	expect(verdict.checked).toBe(false);
	expect(verdict.reason).not.toBe('');
	expect(verdict.nodeProcesses).toEqual([]);
});

test('no-Node predicate: the process table parser reads pid, ppid and comm rows only', () => {
	const rows = parsePsTable(`${ps([KILASFLOW, { pid: 200, ppid: 100, comm: 'node' }])}\n`);
	expect(rows).toEqual([
		{ pid: 100, ppid: 1, comm: 'kilasflow' },
		{ pid: 200, ppid: 100, comm: 'node' }
	]);
});

// --- Capstone machinery: the container, classification, redaction and report
// helpers the image host and the report CLI share. `recorded()` in the
// capstone spec and the walk of records in capstone-report.mjs both depend on
// these, so a defect here would misreport every run rather than one proof.

test('docker top parsing: Docker Desktop and Linux table shapes', () => {
	// Docker Desktop pads each column; Linux `docker top` prints a header too.
	// Verified 2026-09-20: both accept `-eo pid,ppid,comm` / `-eo pid,args` and
	// print a header plus rows. `comm` is the last column and may contain
	// spaces, so the pid and ppid are the only two positional fields.
	const dockerDesktop = parseDockerTop(
		'PID                 PPID                COMM\n1                   0                   /app/kilasflow\n',
		'PID                 COMMAND\n1                   /app/kilasflow'
	);
	expect(dockerDesktop).toEqual([
		{ pid: 1, ppid: 0, comm: '/app/kilasflow', args: '/app/kilasflow' }
	]);

	const linux = parseDockerTop(
		'  PID   PPID  COMM\n    1      0  /app/kilasflow\n   42      1  node server.js\n',
		'  PID   COMMAND\n    1  /app/kilasflow\n   42  node server.js --port 8080\n'
	);
	expect(linux).toEqual([
		{ pid: 1, ppid: 0, comm: '/app/kilasflow', args: '/app/kilasflow' },
		{ pid: 42, ppid: 1, comm: 'node server.js', args: 'node server.js --port 8080' }
	]);

	// An empty table (a container that reported nothing) parses to no rows, and
	// so does a header-only table: the caller must be able to tell "nothing
	// ran" apart from "the parse failed".
	expect(parseDockerTop('PID PPID COMM\n', 'PID COMMAND\n')).toEqual([]);
});

test('container verdict: a stack containing a node process fails naming the container', () => {
	const kilasflow = {
		name: 'kf-capstone-run1-1',
		rows: [{ pid: 1, ppid: 0, comm: '/app/kilasflow', args: '/app/kilasflow' }],
		isKilasFlow: true
	};
	const postgres = {
		name: 'kf-capstone-run1-pg',
		rows: [{ pid: 1, ppid: 0, comm: 'postgres', args: 'postgres' }],
		isKilasFlow: false
	};
	expect(containerStackVerdict([kilasflow, postgres]).ok).toBe(true);

	// The sidecar the epic forbids: a labelled container beside the server
	// running Node. It must fail, and the reason must name the container so a
	// reader knows which one to look at.
	const sidecar = {
		name: 'kf-capstone-run1-sidecar',
		rows: [{ pid: 1, ppid: 0, comm: 'node', args: 'node -e setInterval(()=>{},1000)' }],
		isKilasFlow: false
	};
	const flagged = containerStackVerdict([kilasflow, postgres, sidecar]);
	expect(flagged.ok).toBe(false);
	expect(flagged.reason).toContain('kf-capstone-run1-sidecar');
	expect(flagged.reason).toContain('pid 1');
	expect(flagged.nodeProcesses.map((entry) => entry.container)).toEqual(['kf-capstone-run1-sidecar']);

	// A KilasFlow container whose lowest pid is not the server binary is a
	// different defect with the same shape: report it with the pid.
	const wrongEntrypoint = containerStackVerdict([
		{ name: 'kf-capstone-run1-2', rows: [{ pid: 7, ppid: 1, comm: 'node', args: 'node' }], isKilasFlow: true }
	]);
	expect(wrongEntrypoint.ok).toBe(false);
	expect(wrongEntrypoint.reason).toContain('kf-capstone-run1-2');
	expect(wrongEntrypoint.reason).toContain('pid 7');

	// A container that reported no processes at all is unreadable, not clean.
	const empty = containerStackVerdict([kilasflow, { name: 'kf-capstone-run1-x', rows: [], isKilasFlow: false }]);
	expect(empty.ok).toBe(false);
	expect(empty.reason).toContain('kf-capstone-run1-x');
});

test('classification: an activation 502 with an unhealthy re-probe is unavailable and with a healthy re-probe is failed', () => {
	// The whole point of the re-probe: a 502 during activation is either the
	// third party flapping or our regression, and only a fresh health probe
	// distinguishes them. A delivery that REACHED us and was refused (401, a
	// mismatched secret) is never reclassified.
	const error = new Error('activate workflow worked: unexpected status 502');

	const unhealthy = classifyFailure({
		error,
		reprobe: { healthy: false, cause: 'http-5xx', detail: 'telegram getWebhookInfo: 502' }
	});
	expect(unhealthy.outcome).toBe('unavailable');
	expect(unhealthy.cause).toBe('http-5xx');
	expect(unhealthy.reclassified).toBe(true);
	// The original error text survives reclassification: the record keeps what
	// actually happened, it does not replace it.
	expect(unhealthy.evidence.original).toContain('502');
	expect(unhealthy.reason).toContain('502');

	const healthy = classifyFailure({ error, reprobe: { healthy: true } });
	expect(healthy.outcome).toBe('failed');
	expect(healthy.reclassified ?? false).toBe(false);

	// No re-probe at all: a plain assertion disagreement is a regression.
	expect(classifyFailure({ error: new Error('expected 200, received 404') }).outcome).toBe('failed');
	// A transport failure carries its own cause and stays unavailable.
	const reset = classifyFailure({ error: new Error('fetch failed: ECONNRESET') });
	expect(reset.outcome).toBe('unavailable');
	expect(reset.cause).toBe('network');
	// A refused delivery is a real disagreement even though it is an HTTP error.
	expect(classifyFailure({ error: new Error('sendMessage: status 401 unauthorized') }).outcome).toBe('failed');
});

test('classification: image pull errors', () => {
	// A missing artefact is a failure of the run's premise, never an outage:
	// "the image was never published" must exit 1, not 2.
	expect(classifyPullError('Error response from daemon: manifest unknown').outcome).toBe('failed');
	expect(classifyPullError('Error response from daemon: pull access denied for x, repository does not exist').outcome).toBe(
		'failed'
	);
	expect(classifyPullError('no such manifest: ghcr.io/x/y:latest').outcome).toBe('failed');

	// The registry (or the network in front of it) being down is an outage.
	expect(classifyPullError('TLS handshake timeout').outcome).toBe('unavailable');
	expect(classifyPullError('dial tcp: i/o timeout').outcome).toBe('unavailable');
	expect(classifyPullError('read: connection reset by peer').outcome).toBe('unavailable');
	expect(classifyPullError('unexpected EOF').outcome).toBe('unavailable');
	expect(classifyPullError('received unexpected HTTP status: 503 Service Unavailable').outcome).toBe('unavailable');
	expect(classifyPullError('toomanyrequests: You have reached your pull rate limit').outcome).toBe('unavailable');
	expect(classifyPullError('dial tcp: lookup ghcr.io: no such host').outcome).toBe('unavailable');
});

test('classification: npm errors', () => {
	expect(classifyNpmError('npm error code E404\nnpm error 404 Not Found - GET https://registry.npmjs.org/x').outcome).toBe(
		'failed'
	);
	expect(classifyNpmError('npm error code ETARGET\nnpm error notarget No matching version found').outcome).toBe('failed');
	expect(classifyNpmError('npm error code ETIMEDOUT').outcome).toBe('unavailable');
	expect(classifyNpmError('npm error code ECONNRESET').outcome).toBe('unavailable');
	expect(classifyNpmError('npm error code EAI_AGAIN').outcome).toBe('unavailable');
	expect(classifyNpmError('npm error code E502').outcome).toBe('unavailable');
	expect(classifyNpmError('npm error code ENOTFOUND registry.npmjs.org').outcome).toBe('unavailable');
});

test('redaction: bot token, api keys, webhook routes and bearer values never reach a report', () => {
	const token = '774411:AAFsecret-bot-token';
	const apiKey = 'waha-live-key-abc';
	const text = `bot ${token} key ${apiKey} route /webhook/${'a'.repeat(32)} auth Bearer sk-live.abcdef.ghi`;
	const safe = redact(text, [token, apiKey]);
	expect(safe).not.toContain(token);
	expect(safe).not.toContain(apiKey);
	expect(safe).not.toContain('a'.repeat(32));
	expect(safe).toContain('/webhook/<route>');
	expect(safe).toContain('Bearer [redacted]');
	expect(safe).not.toContain('sk-live.abcdef.ghi');
});

test('report: exit code is 0 all passed, 1 any failed or a missing record, 2 only unavailable', () => {
	expect(
		exitCodeFor(
			[
				{ id: 'a', outcome: 'passed' },
				{ id: 'b', outcome: 'skipped' }
			],
			['a', 'b']
		)
	).toBe(0);
	expect(exitCodeFor([{ id: 'a', outcome: 'failed' }], ['a'])).toBe(1);
	expect(
		exitCodeFor(
			[
				{ id: 'a', outcome: 'failed' },
				{ id: 'b', outcome: 'unavailable' }
			],
			['a', 'b']
		)
	).toBe(1);
	expect(
		exitCodeFor(
			[
				{ id: 'a', outcome: 'unavailable' },
				{ id: 'b', outcome: 'passed' }
			],
			['a', 'b']
		)
	).toBe(2);
	// A test that crashed without writing a record is a failure, not silence.
	expect(exitCodeFor([{ id: 'a', outcome: 'passed' }], ['a', 'b'])).toBe(1);
});

test('report: a skipped proof carries its reason and recovery command in the markdown', () => {
	const markdown = renderMarkdown({
		schema: 1,
		runId: 'run-1',
		generatedAt: '2026-09-20T00:00:00.000Z',
		suite: 'sha',
		artefacts: {
			image: { ref: 'ghcr.io/kilaslab/kilasflow:latest', id: 'sha256:x', version: 'v0.0.0' },
			health: { version: 'v0.0.0' },
			sdk: { source: 'tarball', spec: 'tarball', resolvedVersion: '0.1.0', integrity: 'sha512-y' }
		},
		proofs: [
			{
				id: '02-proof2-waha',
				title: 'proof 2 - WAHA chatting template, two tenants',
				outcome: 'skipped',
				reason: 'KILASFLOW_CAPSTONE_WAHA_URL is not set',
				recovery: 'set KILASFLOW_CAPSTONE_WAHA_URL and re-run `make test-e2e-capstone`',
				gaps: []
			}
		],
		noNode: { checked: true, reason: '', containers: 3, nodeProcesses: [] },
		corpus: { coverage: { measured: 40, baselineTotal: 40 }, tiers: { imported: 40 }, approximate: [] },
		verdict: { outcome: 'skipped', exitCode: 0 }
	});
	expect(markdown).toContain('proof 2');
	expect(markdown).toContain('KILASFLOW_CAPSTONE_WAHA_URL is not set');
	expect(markdown).toContain('make test-e2e-capstone');
});

test('corpus: names match the Go loader and join baseline.json rows', async () => {
	// corpusName mirrors internal/interop/n8n/corpus/corpus.go loadDirectory:
	// the name is the corpus-relative path minus .json, the source is its first
	// segment, and an authored fixture is forced under kilasflow/.
	expect(corpusName('nodes-base/HttpRequest/test/binaryData/binaryData.test.json')).toEqual({
		name: 'nodes-base/HttpRequest/test/binaryData/binaryData.test',
		source: 'nodes-base'
	});
	expect(corpusName('chatting-template/template.json', 'waha-templates')).toEqual({
		name: 'waha-templates/chatting-template/template',
		source: 'waha-templates'
	});
	expect(corpusName('control-datatable.json', 'kilasflow')).toEqual({
		name: 'kilasflow/control-datatable',
		source: 'kilasflow'
	});

	// The join key is the whole contract: a name the baseline does not carry
	// cannot be counted, and a baseline row with no measurement is drift.
	const baseline = JSON.parse(
		await readFile(resolve(repoRoot, 'internal/interop/n8n/corpus/baseline.json'), 'utf-8')
	) as {
		tiers: { imported: number; activatable: number; runnable: number };
		total: number;
		blocked: number;
		scores: Array<{ name: string }>;
	};
	expect(baseline.tiers).toEqual({ activatable: 15, imported: 40, runnable: 5 });
	expect(baseline.total).toBe(40);
	const control = corpusName('control-datatable.json', 'kilasflow');
	expect(baseline.scores.some((row) => row.name === control.name)).toBe(true);
	expect(baseline.scores.every((row) => row.name.includes('/'))).toBe(true);
});

// The registry path of proof 4, exercised without ever publishing: a fake npm
// registry serves one packument and one tarball, and the scratch project
// installs from it with the operator's own `npm install`. `npm publish` is
// forbidden for agents, so this is how the code path that consumes a published
// package is proven; the tarball it serves is the one `npm pack ./sdk` produces,
// which is byte-identical to what publish would upload.
test('registry mode: the scratch project installs from a fake registry and resolves all subpaths', async () => {
	// The bytes the registry would serve, produced the way the release job
	// produces them (npm pack runs the SDK's prepack build).
	const packDir = await mkdtemp(join(tmpdir(), 'epic-registry-'));
	const { stdout: packOut } = await execFileAsync('npm', ['pack', resolve(repoRoot, 'sdk'), '--pack-destination', packDir], {
		timeout: 300_000
	});
	const tarballName = packOut
		.split('\n')
		.map((line) => line.trim())
		.find((line) => line.endsWith('.tgz'));
	expect(tarballName, `npm pack printed a tarball name (${packOut})`).toBeDefined();
	const tarballPath = join(packDir, tarballName!);
	const tarballBytes = await readFile(tarballPath);
	const integrity = `sha512-${createHash('sha512').update(tarballBytes).digest('base64')}`;

	const sdk = JSON.parse(await readFile(resolve(repoRoot, 'sdk', 'package.json'), 'utf-8')) as {
		name: string;
		version: string;
	};
	const requests: string[] = [];
	const server = createServer((request, response) => {
		requests.push(request.url ?? '');
		if ((request.url ?? '').endsWith('.tgz')) {
			response.writeHead(200, { 'content-type': 'application/octet-stream' });
			response.end(tarballBytes);
			return;
		}
		response.writeHead(200, { 'content-type': 'application/json' });
		response.end(
			JSON.stringify({
				name: sdk.name,
				'dist-tags': { latest: sdk.version },
				versions: {
					[sdk.version]: {
						name: sdk.name,
						version: sdk.version,
						dist: {
							tarball: `http://127.0.0.1:${(server.address() as { port: number }).port}/@kilasflow/sdk/-/sdk-${sdk.version}.tgz`,
							integrity
						}
					}
				}
			})
		);
	});
	server.listen(0, '127.0.0.1');
	await once(server, 'listening');
	const port = (server.address() as { port: number }).port;

	// A cold, private npm cache: pacote can satisfy a tarball request out of the
	// shared cache by integrity alone, which would make "did the fake registry
	// serve the bytes" untestable on a machine that has run this before.
	const cacheDir = await mkdtemp(join(tmpdir(), 'epic-registry-cache-'));
	const previousCache = process.env.npm_config_cache;
	process.env.npm_config_cache = cacheDir;

	try {
		const app = await scaffoldExternalSource({
			kind: 'registry',
			spec: `${sdk.name}@${sdk.version}`,
			registry: `http://127.0.0.1:${port}`
		});
		expect(app.source).toBe('registry');
		expect(app.tarball).toBeNull();
		expect(app.sdkVersion).toBe(sdk.version);
		expect(app.integrity).toBe(integrity);
		// Every declared subpath resolves from inside the scratch project — the
		// exports-map proof a repository import cannot give.
		expect(Object.keys(app.subpaths).sort()).toEqual(['.', './browser', './server']);
		for (const url of Object.values(app.subpaths)) expect(url).toContain('node_modules');
		expect(typeof app.KilasFlowClient).toBe('function');
		expect(typeof app.datastoreFilter).toBe('function');
		// The fake registry served both the packument and the tarball: an install
		// that fell back to the public registry, or that resolved the tarball
		// from a warm cache, would not have touched this server at all.
		expect(requests.some((url) => url.includes('@kilasflow'))).toBe(true);
		expect(requests.some((url) => url.endsWith('.tgz'))).toBe(true);
	} finally {
		if (previousCache === undefined) delete process.env.npm_config_cache;
		else process.env.npm_config_cache = previousCache;
		server.close();
		await once(server, 'close').catch(() => undefined);
	}
});
