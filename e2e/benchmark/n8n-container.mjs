/**
 * The managed throwaway n8n container for FEAT-8mymac (stage 2).
 *
 * Stage 2 measures the comparison half against a real n8n. The requester's own
 * instance is not reachable and no credentials for it exist, so the harness
 * stands one up itself: a container from a pinned image (tag AND digest),
 * published on 127.0.0.1 with an ephemeral port, labelled with this ticket and
 * the owning pid, with no volume and no privileges, and removed afterwards.
 *
 * Secrets. The owner password is generated in memory, kept in the container
 * object only for the length of the run, and never written to a file, a ticket,
 * a log or an argv. The n8n encryption key is passed to `docker run` as a
 * value-less `-e N8N_ENCRYPTION_KEY`, so docker copies it from the client's own
 * environment and the value never appears in the argv; `recordedContainerEnv`
 * drops it (and any other key naming key material) from what the raw file
 * records.
 *
 * Lifecycle. Nothing happens at import time: `startN8nContainer` starts the
 * container and only then registers its SIGINT/SIGTERM cleanup handler. A
 * stranded container from a crashed earlier run is swept first, and the sweep
 * only ever removes a container that carries this ticket's label AND whose
 * recorded owner pid is dead — never a concurrent run, never a kf-pg-* scratch
 * Postgres belonging to another ticket.
 *
 * Missing Docker or an un-obtainable image is an honest skip: the KilasFlow
 * half still runs and the summary stays PRELIMINARY. No number is ever
 * fabricated.
 */

import { execFile } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { promisify } from 'node:util';

import { stats } from './lib.mjs';

const execFileAsync = promisify(execFile);

export const LABEL_BENCH = 'kilasflow.bench';
export const LABEL_PID = 'kilasflow.bench.pid';
export const BENCH_LABEL_VALUE = 'FEAT-8mymac';

/**
 * Pinned by tag and digest. The digest was verified against the running
 * instance before it was written down: the 2.33.7 tag's OCI index digest equals
 * the digest of the image that answered /rest/settings with versionCli 2.33.7,
 * so one portable ref pins both the version and the bytes.
 */
export const DEFAULT_IMAGE =
	process.env.N8N_IMAGE || 'docker.n8n.io/n8nio/n8n:2.33.7@sha256:3989d9b8ebb77b4ee8f604519eb73e44f4384bfaa689526e0104eed79a237d30';

/** The container does not get a volume, so the image's own defaults are the state. */
const CONTAINER_PORT = 5678;

/** Container environment: non-secret flags as values, the key by reference. */
function containerEnv() {
	return [
		{ key: 'N8N_DIAGNOSTICS_ENABLED', value: 'false' },
		{ key: 'N8N_VERSION_NOTIFICATIONS_ENABLED', value: 'false' },
		{ key: 'N8N_TEMPLATES_ENABLED', value: 'false' },
		{ key: 'N8N_PERSONALIZATION_ENABLED', value: 'false' },
		{ key: 'N8N_HIRING_BANNER_ENABLED', value: 'false' },
		{ key: 'N8N_SECURE_COOKIE', value: 'false' },
		{ key: 'N8N_LOG_LEVEL', value: 'warn' },
		{ key: 'GENERIC_TIMEZONE', value: 'UTC' },
		// Value-less on purpose: docker copies it from the client's environment,
		// so the key is not in the argv a `ps` would show.
		{ key: 'N8N_ENCRYPTION_KEY', value: undefined },
	];
}

/** The `docker run` arguments after `docker`, for inspection and for spawning. */
export function dockerRunArgs({ name, image = DEFAULT_IMAGE, pid = process.pid, env = containerEnv() }) {
	const args = [
		'run',
		'-d',
		'--name',
		name,
		`--label=${LABEL_BENCH}=${BENCH_LABEL_VALUE}`,
		`--label=${LABEL_PID}=${pid}`,
		'--add-host=host.docker.internal:host-gateway',
		'-p',
		`127.0.0.1::${CONTAINER_PORT}`,
	];
	for (const entry of env) {
		args.push('-e', entry.value === undefined ? entry.key : `${entry.key}=${entry.value}`);
	}
	args.push(image);
	return args;
}

/** Host port from `docker port` output: `127.0.0.1:49153` or the IPv4/IPv6 pair. */
export function parseDockerPort(text) {
	for (const line of String(text ?? '').split('\n')) {
		const match = /:(\d+)\s*$/.exec(line.trim());
		if (match) return Number(match[1]);
	}
	return null;
}

/**
 * The ephemeral owner credentials for a fresh instance.
 *
 * Used only in process memory: passed to `POST /rest/owner/setup` and
 * `POST /rest/login` and never written anywhere. n8n's rules are 8–64
 * characters with at least one digit and one uppercase letter; the `Kf` prefix
 * and `Z9` suffix guarantee the uppercase and the digit regardless of what the
 * random hex draws.
 */
export function generateOwnerCredentials(env = process.env) {
	const email = env.N8N_EMAIL?.trim() || `bench-${randomBytes(4).toString('hex')}@bench.example`;
	const password = env.N8N_PASSWORD?.trim() || `Kf${randomBytes(12).toString('hex')}Z9`;
	return { email, password };
}

/**
 * Container environment as recorded in the raw file: the KEYS only, and never
 * a key that names key material. `docker inspect` reports the encryption key's
 * value in Config.Env, so this is the filter that keeps it out of the artifact.
 */
export function recordedContainerEnv(lines) {
	const out = [];
	for (const line of lines ?? []) {
		const key = String(line).split('=')[0];
		if (!key) continue;
		if (/password|key/i.test(key)) continue;
		out.push(key);
	}
	return out;
}

/** n8n in a container reaches the host's loopback at host.docker.internal. */
export function n8nOrigin(url) {
	const parsed = new URL(url);
	return `http://host.docker.internal:${parsed.port}`;
}

/**
 * Which labelled containers are stranded.
 *
 * A container is ours only if it carries this ticket's label. It is stranded
 * only if its recorded owner pid is no longer alive — a live pid is either this
 * run or a concurrent sibling. kf-pg-* containers are never n8n and are never
 * touched, whatever labels they carry.
 */
export function selectStaleContainers(containers, alivePids = []) {
	const alive = new Set(alivePids.map(Number));
	const names = [];
	for (const container of containers ?? []) {
		const labels = container?.labels ?? {};
		if (labels[LABEL_BENCH] !== BENCH_LABEL_VALUE) continue;
		const name = container?.name ?? '';
		if (!name.startsWith('kf-bench-n8n-')) continue;
		const pid = Number(labels[LABEL_PID]);
		if (!Number.isInteger(pid) || pid <= 0) continue;
		if (alive.has(pid)) continue;
		names.push(name);
	}
	return names;
}

/** Docker server version, or null when the CLI or daemon is unavailable. */
export async function dockerVersion() {
	try {
		const { stdout } = await execFileAsync('docker', ['version', '--format', '{{.Server.Version}}']);
		return stdout.trim() || null;
	} catch {
		return null;
	}
}

export async function dockerAvailable() {
	return (await dockerVersion()) !== null;
}

async function inspectImage(image) {
	try {
		const { stdout } = await execFileAsync('docker', ['image', 'inspect', image]);
		const [first] = JSON.parse(stdout);
		return first ?? null;
	} catch {
		return null;
	}
}

/**
 * Make sure the pinned image is present and report what it actually is. Pulls
 * only when absent; an image that cannot be obtained is reported, never worked
 * around by pulling a moving tag.
 */
export async function ensureImage(image = DEFAULT_IMAGE) {
	let inspected = await inspectImage(image);
	if (!inspected) {
		await execFileAsync('docker', ['pull', image], { timeout: 900_000, maxBuffer: 64 * 1024 * 1024 });
		inspected = await inspectImage(image);
	}
	if (!inspected) throw new Error(`the image ${image} is still unavailable after a pull`);
	const repoDigests = inspected.RepoDigests ?? [];
	return {
		image,
		digest: repoDigests.find((ref) => ref.includes('@sha256:'))?.split('@')[1] ?? null,
		repoDigests,
		version: inspected.Config?.Labels?.['org.opencontainers.image.version'] ?? null,
		architecture: inspected.Architecture ?? null,
		os: inspected.Os ?? null,
	};
}

/** Remove stranded containers carrying this ticket's label and a dead owner. */
export async function sweepStaleContainers() {
	let stdout;
	try {
		({ stdout } = await execFileAsync('docker', [
			'ps',
			'-a',
			'--filter',
			`label=${LABEL_BENCH}=${BENCH_LABEL_VALUE}`,
			'--format',
			'{{json .}}',
		]));
	} catch {
		return [];
	}
	const containers = stdout
		.split('\n')
		.filter((line) => line.trim() !== '')
		.map((line) => {
			try {
				const row = JSON.parse(line);
				const labels = {};
				for (const pair of String(row.Labels ?? '').split(',')) {
					const index = pair.indexOf('=');
					if (index > 0) labels[pair.slice(0, index).trim()] = pair.slice(index + 1).trim();
				}
				return { name: row.Names ?? '', labels };
			} catch {
				return null;
			}
		})
		.filter(Boolean);
	let alive = [];
	try {
		// process.kill(pid, 0) is the liveness test; the pids come from the labels.
		alive = containers
			.map((container) => Number(container.labels[LABEL_PID]))
			.filter((pid) => Number.isInteger(pid) && pid > 0)
			.filter((pid) => {
				try {
					process.kill(pid, 0);
					return true;
				} catch {
					return false;
				}
			});
	} catch {
		alive = [];
	}
	const stale = selectStaleContainers(containers, [...alive, process.pid]);
	for (const name of stale) await execFileAsync('docker', ['rm', '-f', '-v', name]).catch(() => undefined);
	return stale;
}

async function fetchJson(url, timeoutMs = 10_000) {
	const response = await fetch(url, { signal: AbortSignal.timeout(timeoutMs) });
	if (!response.ok) throw new Error(`${url} answered ${response.status}`);
	return response.json();
}

/**
 * Start the throwaway container.
 *
 * Returns either a running handle ({status: 'running', url, containerName, image,
 * digest, version, architecture, envKeys, credentials, close}) or an honest skip
 * ({status: 'skipped-no-docker' | 'skipped-no-image', reason}). The caller runs
 * the KilasFlow half either way.
 */
export async function startN8nContainer({ image = DEFAULT_IMAGE, env = process.env, readyTimeoutMs = 180_000 } = {}) {
	if (!(await dockerAvailable())) {
		return { status: 'skipped-no-docker', reason: 'the docker CLI or daemon is unavailable' };
	}
	await sweepStaleContainers();

	let facts;
	try {
		facts = await ensureImage(image);
	} catch (error) {
		return { status: 'skipped-no-image', reason: `the pinned image could not be obtained: ${error instanceof Error ? error.message : String(error)}` };
	}

	const credentials = generateOwnerCredentials(env);
	const encryptionKey = randomBytes(32).toString('hex');
	const name = `kf-bench-n8n-${randomBytes(4).toString('hex')}`;
	const args = dockerRunArgs({ name, image: facts.image, pid: process.pid });
	let containerId;
	try {
		({ stdout: containerId } = await execFileAsync('docker', args, {
			env: { ...process.env, N8N_ENCRYPTION_KEY: encryptionKey },
			timeout: 120_000,
		}));
	} catch (error) {
		return { status: 'skipped-no-image', reason: `docker run failed: ${error instanceof Error ? error.message : String(error)}` };
	}
	containerId = containerId.trim();

	let closing = null;
	const close = () => {
		if (closing) return closing;
		closing = execFileAsync('docker', ['rm', '-f', '-v', name])
			.then(() => undefined)
			.catch(() => undefined);
		return closing;
	};

	let url = null;
	try {
		const { stdout: portOutput } = await execFileAsync('docker', ['port', name, `${CONTAINER_PORT}/tcp`]);
		const port = parseDockerPort(portOutput);
		if (!port) throw new Error(`could not resolve the published port from ${JSON.stringify(portOutput)}`);
		url = `http://127.0.0.1:${port}`;

		const deadline = Date.now() + readyTimeoutMs;
		let ready = false;
		while (Date.now() < deadline) {
			try {
				const response = await fetch(`${url}/healthz/readiness`, { signal: AbortSignal.timeout(5_000) });
				if (response.ok) {
					ready = true;
					break;
				}
			} catch {
				// Not up yet.
			}
			await new Promise((resolve) => setTimeout(resolve, 1_000));
		}
		if (!ready) throw new Error(`n8n did not answer /healthz/readiness within ${readyTimeoutMs}ms`);

		const settings = await fetchJson(`${url}/rest/settings`);
		const setupPending = settings?.data?.userManagement?.showSetupOnFirstLoad;
		if (setupPending !== true) throw new Error(`expected showSetupOnFirstLoad true on a fresh instance, saw ${JSON.stringify(setupPending)}`);
	} catch (error) {
		await close();
		return { status: 'skipped-no-image', reason: `the container never became ready: ${error instanceof Error ? error.message : String(error)}` };
	}

	let envKeys = [];
	try {
		const { stdout } = await execFileAsync('docker', ['inspect', '--format', '{{json .Config.Env}}', name]);
		envKeys = recordedContainerEnv(JSON.parse(stdout));
	} catch {
		envKeys = [];
	}

	const container = {
		status: 'running',
		url,
		containerId,
		containerName: name,
		image: facts.image,
		digest: facts.digest,
		repoDigests: facts.repoDigests,
		version: facts.version,
		architecture: facts.architecture,
		envKeys,
		credentials,
		close,
	};

	// Registered here, never at import: a signal removes the container and lets
	// the harness's own handler finish closing the engines and exit.
	const onSignal = () => {
		void close();
	};
	container.signalHandlers = { onSignal };
	process.once('SIGINT', onSignal);
	process.once('SIGTERM', onSignal);

	return container;
}

/** Run one command inside the container; returns stdout or null. */
export async function containerExec(containerName, args, timeoutMs = 30_000) {
	try {
		const { stdout } = await execFileAsync('docker', ['exec', containerName, ...args], { timeout: timeoutMs, maxBuffer: 16 * 1024 * 1024 });
		return stdout;
	} catch {
		return null;
	}
}

/**
 * The n8n server's resident memory, in KiB.
 *
 * n8n runs as a main process plus a JS task runner, so the sum over the node
 * processes is what "the server's RSS" means here. The probe is a node process
 * inside the container too, so its own pid is excluded. Sampled, not traced:
 * `ps`/`docker exec` cost is untimed and is the same on both engines' accounts.
 */
export async function containerRssKib(containerName) {
	const script = [
		"const fs=require('fs');",
		'let total=0;const self=process.pid;',
		"for(const d of fs.readdirSync('/proc')){if(!/^\\d+$/.test(d))continue;const pid=Number(d);if(pid===self)continue;",
		"try{const st=fs.readFileSync('/proc/'+d+'/status','utf8');const m=/VmRSS:\\s*(\\d+)\\s*kB/.exec(st);if(!m)continue;",
		"const cmd=fs.readFileSync('/proc/'+d+'/cmdline','utf8');if(!/node|n8n/.test(cmd))continue;total+=Number(m[1]);}catch{}}",
		"console.log(JSON.stringify({rssKib:total}));",
	].join('');
	const stdout = await containerExec(containerName, ['node', '-e', script]);
	if (!stdout) return null;
	try {
		const value = Number(JSON.parse(stdout).rssKib);
		return Number.isFinite(value) && value > 0 ? value : null;
	} catch {
		return null;
	}
}

/** A kernel-tracked high-water mark, summed over the same processes, as a cross-check. */
export async function containerHwmKib(containerName) {
	const script = [
		"const fs=require('fs');",
		'let total=0;const self=process.pid;',
		"for(const d of fs.readdirSync('/proc')){if(!/^\\d+$/.test(d))continue;const pid=Number(d);if(pid===self)continue;",
		"try{const st=fs.readFileSync('/proc/'+d+'/status','utf8');const m=/VmHWM:\\s*(\\d+)\\s*kB/.exec(st);if(!m)continue;",
		"const cmd=fs.readFileSync('/proc/'+d+'/cmdline','utf8');if(!/node|n8n/.test(cmd))continue;total+=Number(m[1]);}catch{}}",
		"console.log(JSON.stringify({hwmKib:total}));",
	].join('');
	const stdout = await containerExec(containerName, ['node', '-e', script]);
	if (!stdout) return null;
	try {
		const value = Number(JSON.parse(stdout).hwmKib);
		return Number.isFinite(value) && value > 0 ? value : null;
	} catch {
		return null;
	}
}

/**
 * The stub's latency as seen from inside the container, which is the network
 * path n8n actually pays. Same helper shape as the host probe: N fetches of the
 * trivial stub path, reported as descriptive statistics.
 */
export async function probeStubLatencyInContainer(containerName, origin, n = 30) {
	const script = [
		`const origin=${JSON.stringify(origin)};const n=${n};`,
		'(async()=>{const samples=[];',
		'for(let i=0;i<n;i++){const started=performance.now();',
		"await (await fetch(origin+'/api/echo')).arrayBuffer();samples.push(performance.now()-started);}",
		"console.log(JSON.stringify(samples));})();",
	].join('');
	const stdout = await containerExec(containerName, ['node', '-e', script], 120_000);
	if (!stdout) return null;
	try {
		const samples = JSON.parse(stdout);
		if (!Array.isArray(samples) || samples.length === 0) return null;
		return stats(samples);
	} catch {
		return null;
	}
}
