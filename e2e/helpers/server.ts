import { spawn, type ChildProcess } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { once } from 'node:events';
import { createWriteStream } from 'node:fs';
import { mkdtemp } from 'node:fs/promises';
import net from 'node:net';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export interface E2EServerOptions {
	// One "host:port" the instance may reach on loopback while the
	// private-address guard stays on for everything else. A single entry: the
	// koanf environment mapping carries one value per variable, which is all a
	// per-test stub needs.
	stubEndpoint?: string;
	// Origins allowed to embed the editor. Single origin for the same reason.
	allowedOrigin?: string;
}

export interface E2EServer {
	baseURL: string;
	port: number;
	// pid is the kilasflow process's own id, so a test can inspect the
	// processes it starts, such as its JavaScript workers.
	pid: number;
	dataDir: string;
	logPath: string;
	close: () => Promise<void>;
}

// Boots a real kilasflow binary against a fresh SQLite database in a fresh
// temp directory, the same composition cmd/kilasflow/main.go builds in
// production: registry, executors, routing, scheduler, lifecycle checks.
//
// Authentication stays off (the default), so the API is tenant-scoped to the
// bootstrapped default tenant with no login step. Embedding stays on through
// a random per-instance signing key, so embed sessions can be minted over the
// API. Outbound HTTP stays guarded: the loopback stub is admitted through
// outbound.allowed_hosts plus outbound.allowed_private_endpoints, and
// allow_private_networks is never set.
export async function startServer(options: E2EServerOptions = {}): Promise<E2EServer> {
	const repoDir = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
	const binary = join(repoDir, 'bin', 'kilasflow');
	const dataDir = await mkdtemp(join(tmpdir(), 'kilasflow-e2e-'));
	const port = await freePort();
	const baseURL = `http://127.0.0.1:${port}`;
	const logPath = join(dataDir, `kilasflow-${port}.log`);

	const logStream = createWriteStream(logPath, { flags: 'a' });
	const chunks: string[] = [];
	const child: ChildProcess = spawn(binary, ['-config', ''], {
		cwd: repoDir,
		env: {
			...process.env,
			KILASFLOW_SERVER_HOST: '127.0.0.1',
			KILASFLOW_SERVER_PORT: String(port),
			KILASFLOW_DATABASE_DSN: join(dataDir, 'kilasflow.db'),
			KILASFLOW_OUTBOUND_ALLOWED_HOSTS: '127.0.0.1',
			...(options.stubEndpoint
				? { KILASFLOW_OUTBOUND_ALLOWED_PRIVATE_ENDPOINTS: options.stubEndpoint }
				: {}),
			KILASFLOW_EMBED_SIGNING_KEY: randomBytes(32).toString('base64'),
			KILASFLOW_ENCRYPTION_KEY: randomBytes(32).toString('base64'),
			...(options.allowedOrigin ? { KILASFLOW_EMBED_ALLOWED_ORIGINS: options.allowedOrigin } : {})
		},
		stdio: ['ignore', 'pipe', 'pipe']
	});
	child.stdout?.on('data', (chunk: Buffer) => {
		chunks.push(chunk.toString());
		// Teardown ends the stream while the child can still flush: an
		// unguarded write crashes the worker with ERR_STREAM_WRITE_AFTER_END.
		if (!logStream.writableEnded) logStream.write(chunk);
	});
	child.stderr?.on('data', (chunk: Buffer) => {
		chunks.push(chunk.toString());
		if (!logStream.writableEnded) logStream.write(chunk);
	});

	try {
		await waitForReady(child, `${baseURL}/api/v1/ready`, chunks);
	} catch (error) {
		logStream.end();
		await stop(child);
		throw error;
	}

	let closed = false;
	return {
		baseURL,
		port,
		pid: child.pid ?? 0,
		dataDir,
		logPath,
		close: async () => {
			if (closed) return;
			closed = true;
			logStream.end();
			await stop(child);
		}
	};
}

// Reserves a free loopback port by opening and closing a listener, the same
// pattern scripts/openapi-spec.mjs uses: parallel workers that picked ports
// blind would collide.
async function freePort(): Promise<number> {
	const listener = net.createServer();
	listener.listen(0, '127.0.0.1');
	await once(listener, 'listening');
	const address = listener.address();
	if (!address || typeof address === 'string') throw new Error('Unable to reserve a local port');
	listener.close();
	await once(listener, 'close');
	return address.port;
}

async function waitForReady(child: ChildProcess, readyURL: string, logs: string[]): Promise<void> {
	for (let attempt = 0; attempt < 80; attempt += 1) {
		if (child.exitCode !== null) {
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

async function stop(child: ChildProcess): Promise<void> {
	if (!child || child.exitCode !== null) return;
	child.kill('SIGTERM');
	let timer: ReturnType<typeof setTimeout> | undefined;
	await Promise.race([
		once(child, 'exit'),
		new Promise((resolveDelay) => {
			timer = setTimeout(resolveDelay, 5_000);
		})
	]);
	if (timer) clearTimeout(timer);
	if (child.exitCode === null) child.kill('SIGKILL');
}
