import { once } from 'node:events';
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises';
import net from 'node:net';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';

const webDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repoDir = resolve(webDir, '..');
const generatedSpecPath = join(webDir, '.tmp', 'openapi.json');
const workspace = await mkdtemp(join(tmpdir(), 'kilasflow-openapi-'));
const port = await freePort();
const baseURL = `http://127.0.0.1:${port}`;

let server;

try {
	const binary = join(workspace, 'kilasflow');
	await run('go', ['build', '-o', binary, './cmd/kilasflow'], repoDir);

	server = spawn(binary, ['-config', ''], {
		cwd: repoDir,
		env: {
			...process.env,
			KILASFLOW_SERVER_HOST: '127.0.0.1',
			KILASFLOW_SERVER_PORT: String(port),
			KILASFLOW_DATABASE_DSN: join(workspace, 'kilasflow.db')
		},
		stdio: 'pipe'
	});

	const logs = [];
	server.stderr.on('data', (chunk) => logs.push(chunk.toString()));
	server.stdout.on('data', (chunk) => logs.push(chunk.toString()));

	await waitForReady(server, `${baseURL}/api/v1/ready`, logs);

	const response = await fetch(`${baseURL}/api/openapi.json`);
	if (!response.ok) {
		throw new Error(`OpenAPI endpoint returned ${response.status} ${response.statusText}`);
	}

	const document = await response.json();
	if (typeof document !== 'object' || document === null || !String(document.openapi ?? '').startsWith('3.1')) {
		throw new Error('KilasFlow did not expose an OpenAPI 3.1 document');
	}

	await mkdir(dirname(generatedSpecPath), { recursive: true });
	await writeFile(generatedSpecPath, `${JSON.stringify(document, null, 2)}\n`);
	await run('pnpm', ['exec', 'orval', '--config', 'orval.config.ts'], webDir);
} finally {
	await stop(server);
	await rm(workspace, { recursive: true, force: true });
	await rm(dirname(generatedSpecPath), { recursive: true, force: true });
}

async function freePort() {
	const listener = net.createServer();
	listener.listen(0, '127.0.0.1');
	await once(listener, 'listening');
	const address = listener.address();
	if (!address || typeof address === 'string') throw new Error('Unable to reserve a local port');
	listener.close();
	await once(listener, 'close');
	return address.port;
}

async function waitForReady(process, readyURL, logs) {
	for (let attempt = 0; attempt < 80; attempt += 1) {
		if (process.exitCode !== null) {
			throw new Error(`KilasFlow exited before readiness:\n${logs.join('')}`);
		}

		try {
			const response = await fetch(readyURL);
			if (response.ok) return;
		} catch {
			// The binary is still starting.
		}

		await new Promise((resolveDelay) => setTimeout(resolveDelay, 250));
	}

	throw new Error(`KilasFlow did not become ready:\n${logs.join('')}`);
}

async function run(command, args, cwd) {
	const child = spawn(command, args, { cwd, stdio: 'inherit' });
	const [code] = await once(child, 'exit');
	if (code !== 0) throw new Error(`${command} ${args.join(' ')} failed with exit code ${code}`);
}

async function stop(process) {
	if (!process || process.exitCode !== null) return;
	process.kill('SIGTERM');
	let timer;
	await Promise.race([
		once(process, 'exit'),
		new Promise((resolveDelay) => {
			timer = setTimeout(resolveDelay, 5_000);
		})
	]);
	clearTimeout(timer);
	if (process.exitCode === null) process.kill('SIGKILL');
}
