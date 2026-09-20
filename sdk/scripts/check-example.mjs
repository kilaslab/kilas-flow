/**
 * The host-page example, run the way a consumer runs it.
 *
 * `sdk-package-check` proves the package a consumer installs; this proves the
 * example that consumes it actually runs. It packs the SDK, copies the three
 * example files into a scratch directory outside the repository, rewrites the
 * example's dependency to the packed tarball, installs it, starts a stub
 * KilasFlow and the example's own backend, and drives both over HTTP.
 *
 * The static route is asserted with raw `node:http` requests rather than
 * `fetch`: fetch normalises a `..%2f` path before it leaves the process, which
 * would hide the traversal this exists to catch. The example serves the
 * installed SDK's `dist/` out of its own directory, so a request that walks up
 * from there is a real file-disclosure bug, not a theoretical one.
 *
 * Flags:
 *   --keep   keep the scratch directory for inspection
 */
import { spawn } from 'node:child_process';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { createServer, request as httpRequest } from 'node:http';
import { tmpdir } from 'node:os';
import { dirname, join, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

import { packSdk, run } from './lib/pack.mjs';

const sdkDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repoDir = resolve(sdkDir, '..');

const apiKey = 'kfa1_0123456789ab_host-page-secret';

/** A free TCP port, bound and released so the example can take it. */
function freePort() {
	return new Promise((resolvePort, reject) => {
		const server = createServer();
		server.on('error', reject);
		server.listen(0, '127.0.0.1', () => {
			const { port } = server.address();
			server.close((error) => (error ? reject(error) : resolvePort(port)));
		});
	});
}

/**
 * How long one request may take before the example is declared hung. An
 * example that accepts a request and never answers it (say a route that throws
 * after its headers are out) would otherwise stall the whole check until its
 * outer timeout, which reports "the check did not finish" instead of naming
 * the route that hung.
 */
const rawRequestTimeoutMs = 10_000;

/** One request, with the path sent verbatim — no URL normalisation. */
function rawRequest({ port, path, method = 'GET' }) {
	return new Promise((resolveResponse, reject) => {
		const request = httpRequest({ host: '127.0.0.1', port, path, method }, (response) => {
			const chunks = [];
			response.on('data', (chunk) => chunks.push(chunk));
			response.on('end', () => {
				clearTimeout(timer);
				resolveResponse({ status: response.statusCode, body: Buffer.concat(chunks).toString('utf8') });
			});
		});
		const timer = setTimeout(() => {
			clearTimeout(timer);
			request.destroy();
			reject(
				new Error(`no response for ${method} ${path} within ${rawRequestTimeoutMs}ms: the example left it open`)
			);
		}, rawRequestTimeoutMs);
		request.on('close', () => clearTimeout(timer));
		request.on('error', (error) => {
			clearTimeout(timer);
			reject(new Error(`${method} ${path} failed: ${error.message}`));
		});
		request.end();
	});
}

/**
 * A stand-in for KilasFlow: enough of the API for the example's two backend
 * routes. It records the Authorization header so the check can prove the
 * example sent the host's key rather than minting a session unauthenticated.
 */
async function startStub() {
	const seen = { authorization: null, requests: [] };
	const server = createServer((request, response) => {
		const chunks = [];
		request.on('data', (chunk) => chunks.push(chunk));
		request.on('end', () => {
			seen.requests.push(`${request.method} ${request.url}`);
			if (typeof request.headers.authorization === 'string') seen.authorization = request.headers.authorization;
			const json = (status, body) => {
				response.writeHead(status, { 'Content-Type': 'application/json' });
				response.end(JSON.stringify(body));
			};
			const { pathname } = new URL(request.url, 'http://127.0.0.1');
			if (request.method === 'GET' && pathname === '/api/v1/workflows') return json(200, []);
			if (request.method === 'POST' && pathname === '/api/v1/workflows') {
				return json(201, {
					id: 'wf-1',
					name: 'Embedded demo',
					active: false,
					latestRevision: 1,
					updatedAt: new Date().toISOString()
				});
			}
			if (request.method === 'POST' && pathname === '/api/v1/embed-sessions') {
				return json(201, {
					token: 'kfe1.stub-session',
					workflowId: 'wf-1',
					scopes: ['workflow:read', 'workflow:write', 'workflow:run'],
					origin: 'http://localhost:4173'
				});
			}
			if (request.method === 'POST' && pathname === '/api/v1/stream-tickets') {
				return json(201, {
					ticket: 'ticket-1',
					executionId: 'exec-1',
					expiresAt: new Date(Date.now() + 60_000).toISOString()
				});
			}
			return json(404, { title: 'not found', status: 404 });
		});
	});
	await new Promise((ready) => server.listen(0, '127.0.0.1', ready));
	return { server, port: server.address().port, seen };
}

/** The example's backend, ready once it prints the line its README quotes. */
function startExample({ cwd, port, kilasflowUrl }) {
	const child = spawn(process.execPath, ['server.mjs'], {
		cwd,
		env: { ...process.env, PORT: String(port), KILASFLOW_URL: kilasflowUrl, KILASFLOW_API_KEY: apiKey },
		stdio: ['ignore', 'pipe', 'pipe']
	});
	let stdout = '';
	let stderr = '';
	const ready = new Promise((resolveReady, reject) => {
		const timer = setTimeout(
			() => reject(new Error(`no "Host page on" line after 15s:\n${stdout}\n${stderr}`)),
			15_000
		);
		child.stdout.on('data', (chunk) => {
			stdout += chunk;
			if (stdout.includes('Host page on')) {
				clearTimeout(timer);
				resolveReady();
			}
		});
		child.stderr.on('data', (chunk) => {
			stderr += chunk;
			// The example only writes here on a failure it had to report itself
			// (a route that threw after its headers were out). Keep it in front
			// of the check's own output so the cause is readable next to the
			// step that failed, instead of being swallowed with the pipe.
			process.stderr.write(`example: ${chunk}`);
		});
		child.on('exit', (code) => {
			clearTimeout(timer);
			reject(new Error(`the example exited (${code}) before it was ready:\n${stdout}\n${stderr}`));
		});
	});
	return { child, ready };
}

async function main() {
	const keep = process.argv.includes('--keep');
	const scratch = await mkdtemp(join(tmpdir(), 'kilasflow-sdk-example-'));
	if (scratch.startsWith(repoDir + sep)) {
		await rm(scratch, { recursive: true, force: true });
		console.error(`the scratch directory must live outside the repository, got ${scratch}`);
		return 1;
	}

	const failures = [];
	const ok = (name) => console.log(`ok   ${name}`);
	const fail = (name, error) => {
		const detail = error instanceof Error ? error.message : String(error);
		failures.push(`${name}: ${detail}`);
		console.error(`FAIL ${name}\n${detail}`);
	};
	let blocker = null;
	const step = async (name, body) => {
		if (blocker !== null) {
			failures.push(`${name}: not run, ${blocker}`);
			console.error(`SKIP ${name}: ${blocker}`);
			return false;
		}
		try {
			await body();
			ok(name);
			return true;
		} catch (error) {
			fail(name, error);
			return false;
		}
	};

	let stub = null;
	let example = null;
	try {
		console.log(`scratch: ${scratch}`);
		const packed = await packSdk({ sdkDir, dest: join(scratch, 'pack') });
		console.log(`packed ${packed.name}@${packed.version}`);

		const exampleDir = join(scratch, 'example');
		await mkdir(exampleDir, { recursive: true });
		for (const file of ['index.html', 'server.mjs', 'package.json']) {
			await writeFile(join(exampleDir, file), await readFile(join(sdkDir, 'examples', 'host-page', file)));
		}
		const manifestPath = join(exampleDir, 'package.json');
		const manifest = JSON.parse(await readFile(manifestPath, 'utf8'));
		manifest.dependencies['@kilasflow/sdk'] = `file:${packed.tarball}`;
		await writeFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);

		const installed = await step('install the packed SDK into the copied example', async () => {
			const { stdout } = await run('npm', ['install', '--offline', '--no-audit', '--no-fund'], { cwd: exampleDir });
			console.log(String(stdout).trim());
		});
		if (!installed) blocker = 'the example install failed';

		stub = await startStub();
		const port = await freePort();
		example = startExample({ cwd: exampleDir, port, kilasflowUrl: `http://127.0.0.1:${stub.port}` });

		const booted = await step('the example boots', async () => {
			await example.ready;
		});
		if (!booted) blocker = 'the example did not boot';

		await step('GET / serves the page that mounts the editor', async () => {
			const { status, body } = await rawRequest({ port, path: '/' });
			if (status !== 200) throw new Error(`GET / answered ${status}`);
			if (!body.includes('mountWorkflowEditor')) throw new Error('the page does not mount the editor');
		});

		await step('the installed SDK is served to the page', async () => {
			for (const path of [
				'/node_modules/@kilasflow/sdk/dist/browser.js',
				'/node_modules/@kilasflow/sdk/dist/http.js'
			]) {
				const { status } = await rawRequest({ port, path });
				if (status !== 200) throw new Error(`GET ${path} answered ${status}`);
			}
		});

		await step('POST /api/embed-session returns a token and sends the host key to KilasFlow', async () => {
			const { status, body } = await rawRequest({ port, path: '/api/embed-session', method: 'POST' });
			if (status !== 200) throw new Error(`answered ${status}: ${body}`);
			const session = JSON.parse(body);
			if (typeof session.token !== 'string' || session.token === '') {
				throw new Error(`no token in ${body}`);
			}
			if (stub.seen.authorization !== `Bearer ${apiKey}`) {
				throw new Error(`the stub saw Authorization ${JSON.stringify(stub.seen.authorization)}`);
			}
		});

		await step('GET /api/stream-ticket without executionId is 400', async () => {
			const { status } = await rawRequest({ port, path: '/api/stream-ticket' });
			if (status !== 400) throw new Error(`answered ${status}`);
		});

		await step('the static route refuses to serve outside the SDK dist directory', async () => {
			const probes = [
				'/node_modules/@kilasflow/sdk/dist/..%2f..%2f..%2fpackage.json',
				'/node_modules/@kilasflow/sdk/dist/..%2f..%2f..%2f..%2fserver.mjs',
				'/node_modules/@kilasflow/sdk/dist/missing.js'
			];
			for (const path of probes) {
				const { status, body } = await rawRequest({ port, path });
				if (status !== 404) throw new Error(`GET ${path} answered ${status}, expected 404`);
				if (body.includes(apiKey)) throw new Error(`GET ${path} leaked the API key`);
			}
		});

		// A consumer who copied server.mjs and package.json but not index.html,
		// or an unreadable one: the page route must answer, not die. The example
		// reads index.html per request, so removing it here is the same state.
		await step('GET / with index.html missing answers 500 and the example stays up', async () => {
			await rm(join(exampleDir, 'index.html'));
			const { status } = await rawRequest({ port, path: '/' });
			if (status !== 500) throw new Error(`GET / answered ${status}, expected 500`);
			if (example.child.exitCode !== null) {
				throw new Error(`the example exited (${example.child.exitCode}) on a missing index.html`);
			}
			const after = await rawRequest({ port, path: '/node_modules/@kilasflow/sdk/dist/browser.js' });
			if (after.status !== 200) {
				throw new Error(`the example stopped serving after the 500: GET dist/browser.js answered ${after.status}`);
			}
		});
	} catch (error) {
		fail('check-example', error);
	} finally {
		if (example?.child && example.child.exitCode === null) example.child.kill('SIGTERM');
		if (stub !== null) await new Promise((closed) => stub.server.close(closed));
		if (keep) {
			console.log(`kept ${scratch}`);
		} else {
			await rm(scratch, { recursive: true, force: true });
		}
	}

	if (failures.length > 0) {
		console.error(`\n${failures.length} failure(s):\n${failures.map((failure) => `  ${failure}`).join('\n')}`);
		return 1;
	}
	return 0;
}

main()
	.then((code) => {
		process.exitCode = code;
	})
	.catch((error) => {
		console.error(error);
		process.exitCode = 1;
	});
