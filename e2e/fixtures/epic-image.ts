// FEAT-5fhj6p stage 2: the docker IMAGE as an EpicHost.
//
// A second EpicHost implementation beside binaryHost(): the same proofs, the
// same code, a different place to run KilasFlow. Nothing here knows about a
// proof; it boots containers, discovers their published port, judges their
// process tables and reports identity.
//
// The container contract, in one place:
// - Every container this host starts carries `kilasflow.capstone.run=<runId>`,
//   so `docker ps --filter label=...` is the whole cleanup set and an unrelated
//   Node container on the developer's machine is never blamed or removed.
// - KilasFlow listens on 8080 inside its own network namespace, so the host
//   reaches it through a published port on 127.0.0.1 and a stub reaches it
//   through host.docker.internal (the run's own stub binds 0.0.0.0 and hands the
//   server the `host.docker.internal:<port>` form).
// - Outbound stays guarded: allowed_hosts is never empty (an empty list means
//   no host restriction at all, so closed egress needs a placeholder that
//   matches nothing), and allow_private_networks is never set — a stub is
//   admitted through allowed_private_endpoints, one endpoint at a time.
import { execFile } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { chmod } from 'node:fs/promises';
import { setTimeout as delay } from 'node:timers/promises';
import { promisify } from 'node:util';

import { startStub as startLoopbackStub } from '../helpers/stub';
import { classifyPullError, containerStackVerdict, parseDockerTop, redact } from '../scripts/capstone-lib.mjs';
import type { CapstoneConfig } from './epic-config';
import type { EpicHost, EpicServer, HostStartOptions, NoNodeVerdict, PostgresHandle } from './epic-proofs';

const execFileAsync = promisify(execFile);

const RUN_LABEL = 'kilasflow.capstone.run';
const CONTAINER_PORT = 8080;

/** One row of a parsed `docker top` table. */
export interface DockerTopRow {
	pid: number;
	ppid: number;
	comm: string;
	args?: string;
}

/** A classification the lib produced, named here so callers do not restate it. */
export interface PullClassification {
	outcome: 'failed' | 'unavailable';
	cause: string;
	reason: string;
}

/** An error from a docker invocation, carrying what it printed. */
export class DockerError extends Error {
	readonly stdout: string;
	readonly stderr: string;
	constructor(what: string, stdout: string, stderr: string, cause?: unknown) {
		super(`${what}:\n${stderr || stdout || String(cause ?? '')}`);
		this.name = 'DockerError';
		this.stdout = stdout;
		this.stderr = stderr;
	}
}

/** A pull failure carrying its classification, so the record does not guess. */
export class ImagePullError extends Error {
	readonly classification: PullClassification;
	constructor(message: string) {
		super(message);
		this.name = 'ImagePullError';
		this.classification = classifyPullError(message);
	}
}

async function docker(args: string[], what: string): Promise<string> {
	const { stdout } = await dockerCapture(args, what);
	return stdout;
}

/** Both streams: `nodepackgen` reports through stderr, like most CLIs do. */
async function dockerCapture(args: string[], what: string): Promise<{ stdout: string; stderr: string }> {
	try {
		const { stdout, stderr } = await execFileAsync('docker', args, { timeout: 300_000, maxBuffer: 64 * 1024 * 1024 });
		return { stdout, stderr };
	} catch (error) {
		const failure = error as { stdout?: string; stderr?: string };
		throw new DockerError(what, failure.stdout ?? '', failure.stderr ?? '', error);
	}
}

/** A docker call whose failure is not the run's problem (missing, gone, benign). */
async function dockerQuiet(args: string[]): Promise<string> {
	try {
		const { stdout } = await execFileAsync('docker', args, { timeout: 300_000, maxBuffer: 64 * 1024 * 1024 });
		return stdout;
	} catch {
		return '';
	}
}

export interface ImageIdentity {
	ref: string;
	id: string;
	repoDigests: string[];
	version: string | null;
	revision: string | null;
	arch: string;
	entrypoint: string[];
}

export interface ImageServer extends EpicServer {
	container: string;
}

export interface ImageHost extends EpicHost {
	imageIdentity(): Promise<ImageIdentity>;
}

// Handle -> the container it names, so a later `start({ postgres })` can mint a
// fresh database inside it without widening the PostgresHandle contract the
// hermetic host also implements.
const postgresRecords = new WeakMap<PostgresHandle, { container: string }>();

/**
 * The docker image as a host. `config.image` is what every container runs;
 * `config.pull` false means the image is already local (a rehearsal against
 * `make docker`'s kilasflow:latest).
 */
export function imageHost(config: CapstoneConfig): ImageHost {
	const runId = config.runId;
	const network = `kf-capstone-${runId}`;
	// Playwright restarts the worker after a failure, so a counter alone would
	// reuse a name the previous incarnation already created. The per-process
	// suffix keeps names unique; the label is what cleanup and the no-Node check
	// key on, so names never need to be predictable.
	const processSuffix = randomBytes(3).toString('hex');
	let counter = 0;
	let networkReady = false;
	let identity: Promise<ImageIdentity> | null = null;

	async function ensureNetwork(): Promise<void> {
		if (networkReady) return;
		// The label makes the network part of the same cleanup set as the
		// containers. A name collision means a previous run of the same runId
		// left it behind; reusing it is harmless and better than failing.
		await dockerQuiet(['network', 'create', '--label', `${RUN_LABEL}=${runId}`, network]);
		networkReady = true;
	}

	async function waitForReady(container: string, baseURL: string): Promise<void> {
		for (let attempt = 0; attempt < 120; attempt += 1) {
			const state = (await dockerQuiet(['inspect', '-f', '{{.State.Running}}', container])).trim();
			if (state === 'false') {
				throw new Error(`container ${container} exited before readiness:\n${await dockerQuiet(['logs', container])}`);
			}
			try {
				const response = await fetch(`${baseURL}/api/v1/ready`);
				if (response.ok) return;
			} catch {
				// Still starting.
			}
			await delay(500);
		}
		throw new Error(`container ${container} did not become ready:\n${await dockerQuiet(['logs', container])}`);
	}

	async function start(o: HostStartOptions = {}): Promise<ImageServer> {
		await ensureNetwork();
		const container = `kf-capstone-${runId}-${processSuffix}-${(counter += 1)}`;
		const privateEndpoints = o.privateEndpoints ?? [];
		// allowed_hosts is derived from the private endpoints when the proof did
		// not name hosts itself, because that is what the binary host's
		// equivalent (`127.0.0.1`) means: the endpoint plus the host it lives on.
		// With neither, a placeholder makes the list non-empty, which is what
		// closes egress (an empty list means no restriction at all).
		const allowedHosts = o.allowedHosts ?? privateEndpoints.map((entry) => entry.split(':')[0]);
		const env: Record<string, string> = {
			KILASFLOW_ENCRYPTION_KEY: randomBytes(32).toString('base64'),
			KILASFLOW_EMBED_SIGNING_KEY: randomBytes(32).toString('base64'),
			KILASFLOW_OUTBOUND_ALLOWED_HOSTS: allowedHosts.length > 0 ? allowedHosts.join(',') : 'capstone.invalid'
		};
		if (privateEndpoints.length > 0) {
			env.KILASFLOW_OUTBOUND_ALLOWED_PRIVATE_ENDPOINTS = privateEndpoints.join(',');
		}
		if (o.allowedOrigins && o.allowedOrigins.length > 0) {
			env.KILASFLOW_EMBED_ALLOWED_ORIGINS = o.allowedOrigins.join(',');
		}
		if (o.publicUrl) env.KILASFLOW_SERVER_PUBLIC_URL = o.publicUrl;

		const args = [
			'run',
			'-d',
			'--name',
			container,
			'--label',
			`${RUN_LABEL}=${runId}`,
			'--network',
			network,
			// Linux engines have no host.docker.internal by default; Docker
			// Desktop already provides it, and this makes the two equal.
			'--add-host',
			'host.docker.internal:host-gateway',
			'-p',
			o.hostPort ? `127.0.0.1:${o.hostPort}:${CONTAINER_PORT}` : `127.0.0.1::${CONTAINER_PORT}`
		];
		for (const [key, value] of Object.entries(env)) args.push('-e', `${key}=${value}`);
		if (o.packsDir) {
			args.push('-v', `${o.packsDir}:/packs:ro`, '-e', 'KILASFLOW_PACKS_DIR=/packs');
		}
		if (o.postgres) {
			const record = postgresRecords.get(o.postgres);
			if (!record) throw new Error('the postgres handle was not minted by this host');
			// A fresh database per server: two proofs must never share tables,
			// and a leftover run must not be read as this one.
			const database = `kf_${randomBytes(6).toString('hex')}`;
			await docker(
				['exec', record.container, 'psql', '-U', 'kilas', '-d', 'kilasflow', '-c', `CREATE DATABASE ${database}`],
				`create database ${database}`
			);
			const dsn = `postgres://kilas:hunter2@${record.container}:5432/${database}?sslmode=disable`;
			args.push('-e', 'KILASFLOW_DATABASE_DRIVER=postgres', '-e', `KILASFLOW_DATABASE_DSN=${dsn}`);
		}
		args.push(config.image);
		try {
			await docker(args, `docker run ${config.image}`);
		} catch (error) {
			// A missing local image is the pull classification's problem, not
			// the run's: `docker run` prints "Unable to find image ... locally".
			const text = error instanceof Error ? error.message : String(error);
			if (/Unable to find image|manifest unknown|pull access denied|no such image/i.test(text)) {
				throw new ImagePullError(text);
			}
			throw error;
		}

		const portOutput = await docker(['port', container, `${CONTAINER_PORT}/tcp`], `docker port ${container}`);
		const line = portOutput
			.split('\n')
			.map((entry) => entry.trim())
			.find((entry) => entry !== '');
		const port = line ? Number(line.slice(line.lastIndexOf(':') + 1)) : Number.NaN;
		if (!Number.isInteger(port) || port <= 0) {
			throw new Error(`could not read the published port of ${container} from: ${portOutput}`);
		}
		const baseURL = `http://127.0.0.1:${port}`;
		await waitForReady(container, baseURL);

		let closed = false;
		return {
			baseURL,
			port,
			container,
			close: async () => {
				if (closed) return;
				closed = true;
				if (config.keep) {
					// eslint-disable-next-line no-console
					console.log(`KILASFLOW_CAPSTONE_KEEP=1: leaving ${container} running`);
					return;
				}
				await dockerQuiet(['stop', '-t', '5', container]);
				await dockerQuiet(['rm', '-f', container]);
			},
			logs: async () => redact(await dockerQuiet(['logs', container]))
		};
	}

	async function startPostgres(): Promise<PostgresHandle | null> {
		await ensureNetwork();
		const container = `kf-capstone-${runId}-${processSuffix}-pg`;
		await docker(
			[
				'run',
				'-d',
				'--name',
				container,
				'--label',
				`${RUN_LABEL}=${runId}`,
				'--network',
				network,
				'-e',
				'POSTGRES_USER=kilas',
				'-e',
				'POSTGRES_PASSWORD=hunter2',
				'-e',
				'POSTGRES_DB=kilasflow',
				config.pgImage
			],
			`docker run ${config.pgImage}`
		);
		// `pg_isready` is not enough here: the postgres entrypoint runs a
		// TEMPORARY server while it initialises the cluster, which accepts
		// connections and then shuts down ("the database system is shutting
		// down") before the real one starts. Only a query against the real
		// database settles it.
		let ready = false;
		for (let attempt = 0; attempt < 120; attempt += 1) {
			const check = await dockerQuiet(['exec', container, 'psql', '-U', 'kilas', '-d', 'kilasflow', '-tAc', 'SELECT 1']);
			if (check.trim() === '1') {
				ready = true;
				break;
			}
			await delay(500);
		}
		if (!ready) {
			throw new Error(`postgres container ${container} never became ready:\n${await dockerQuiet(['logs', container])}`);
		}
		const handle: PostgresHandle = {
			label: container,
			close: async () => {
				if (config.keep) return;
				await dockerQuiet(['stop', '-t', '5', container]);
				await dockerQuiet(['rm', '-f', container]);
			}
		};
		postgresRecords.set(handle, { container });
		return handle;
	}

	async function assertNoNode(server: EpicServer): Promise<NoNodeVerdict> {
		const listed = await dockerQuiet([
			'ps',
			'-a',
			'--filter',
			`label=${RUN_LABEL}=${runId}`,
			'--format',
			'{{.Names}}'
		]);
		const names = listed
			.split('\n')
			.map((entry) => entry.trim())
			.filter((entry) => entry !== '');
		const serverContainer = (server as ImageServer).container;
		const stack: Array<{ name: string; rows: DockerTopRow[]; isKilasFlow: boolean }> = [];
		for (const name of names) {
			// A container that vanished between listing and inspection is not
			// evidence either way: `docker top` prints nothing and it is skipped.
			const comm = await dockerQuiet(['top', name, '-eo', 'pid,ppid,comm']);
			if (comm.trim() === '') continue;
			const args = await dockerQuiet(['top', name, '-eo', 'pid,args']);
			stack.push({ name, rows: parseDockerTop(comm, args), isKilasFlow: name === serverContainer });
		}
		if (stack.length === 0) {
			return {
				checked: false,
				reason: `no labelled container of run ${runId} could be inspected`,
				scope: `docker:${RUN_LABEL}=${runId}`,
				serverComm: '',
				processes: [],
				nodeProcesses: []
			};
		}
		const verdict = containerStackVerdict(stack);
		const serverRoot = verdict.roots.find((root) => root.container === serverContainer);
		return {
			checked: true,
			reason: verdict.reason,
			scope: `docker:${RUN_LABEL}=${runId}`,
			serverComm: serverRoot?.comm ?? '',
			processes: stack.flatMap((entry) => entry.rows.map((row) => ({ pid: row.pid, ppid: row.ppid, comm: row.comm }))),
			nodeProcesses: verdict.nodeProcesses.map((entry) => ({ pid: entry.pid, ppid: entry.ppid, comm: entry.comm }))
		};
	}

	async function imageIdentity(): Promise<ImageIdentity> {
		if (!identity) {
			identity = (async (): Promise<ImageIdentity> => {
				if (config.pull) {
					try {
						await docker(['pull', config.image], `docker pull ${config.image}`);
					} catch (error) {
						throw new ImagePullError(error instanceof Error ? error.message : String(error));
					}
				}
				const raw = await docker(['image', 'inspect', config.image], `docker image inspect ${config.image}`);
				const parsed = JSON.parse(raw) as Array<{
					Id: string;
					RepoDigests?: string[] | null;
					Architecture?: string;
					Config?: { Entrypoint?: string[] | null; Labels?: Record<string, string> | null };
				}>;
				const image = parsed[0];
				if (!image) throw new Error(`docker image inspect ${config.image} returned nothing`);
				const labels = image.Config?.Labels ?? {};
				const entrypoint = image.Config?.Entrypoint ?? [];
				if (JSON.stringify(entrypoint) !== JSON.stringify(['/app/kilasflow'])) {
					throw new Error(
						`${config.image} entrypoint is ${JSON.stringify(entrypoint)}, want ["/app/kilasflow"]: ` +
							'the image under test is not the KilasFlow server'
					);
				}
				return {
					ref: config.image,
					id: image.Id,
					repoDigests: image.RepoDigests ?? [],
					version: labels['org.opencontainers.image.version'] ?? null,
					revision: labels['org.opencontainers.image.revision'] ?? null,
					arch: image.Architecture ?? '',
					entrypoint
				};
			})();
		}
		return identity;
	}

	return {
		kind: 'image',
		label: `image:${config.image}`,
		start,
		startPostgres,
		async startStub() {
			// The stub is bound to every interface because the server reaches it
			// as host.docker.internal, which is not 127.0.0.1 inside the
			// container's network namespace.
			const stub = await startLoopbackStub({ bindHost: '0.0.0.0' });
			return {
				...stub,
				reachableOrigin: `http://host.docker.internal:${stub.port}`,
				reachableEndpoint: `host.docker.internal:${stub.port}`
			};
		},
		assertNoNode,
		async nodepackgen(packsRoot: string, args: string[]) {
			// The image runs as nonroot uid 65532, so the host's packs directory
			// has to be writable by it (scripts/smoke-docker.sh does the same).
			await chmod(packsRoot, 0o777).catch(() => undefined);
			return dockerCapture(
				[
					'run',
					'--rm',
					'--entrypoint',
					'/app/nodepackgen',
					'-v',
					`${packsRoot}:/work`,
					'-w',
					'/work',
					config.image,
					...args.map((arg) => arg.replaceAll('{root}', '/work'))
				],
				`nodepackgen ${args.join(' ')}`
			);
		},
		imageIdentity
	};
}

/**
 * Removes everything a run left behind: every container carrying its label and
 * the network itself. Used by the global teardown and by hand after a failure
 * with KILASFLOW_CAPSTONE_KEEP=1.
 */
export async function teardownRun(runId: string): Promise<void> {
	const listed = await dockerQuiet(['ps', '-a', '--filter', `label=${RUN_LABEL}=${runId}`, '--format', '{{.Names}}']);
	for (const name of listed.split('\n').map((entry) => entry.trim()).filter((entry) => entry !== '')) {
		await dockerQuiet(['rm', '-f', name]);
	}
	await dockerQuiet(['network', 'rm', `kf-capstone-${runId}`]);
}
