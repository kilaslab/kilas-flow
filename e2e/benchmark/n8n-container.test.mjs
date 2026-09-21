/**
 * Container-management tests for FEAT-8mymac (stage 2).
 *
 * These pin the parts of the throwaway n8n container that are pure — the docker
 * argv, the port parser, credential generation, the recorded-env filter and the
 * stranding sweep — plus the two properties the whole construction rests on:
 * credentials never reach an argv or a recorded environment, and importing the
 * module starts nothing and registers nothing.
 *
 * The image is addressed by tag AND digest so a rebuilt tag cannot silently
 * change what was measured; the digest was verified against the running
 * instance before it was written down.
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
	BENCH_LABEL_VALUE,
	DEFAULT_IMAGE,
	LABEL_BENCH,
	LABEL_PID,
	dockerRunArgs,
	generateOwnerCredentials,
	n8nOrigin,
	parseDockerPort,
	recordedContainerEnv,
	selectStaleContainers,
} from './n8n-container.mjs';

const NAME = 'kf-bench-n8n-abcdef12';

function argValue(args, flag) {
	const index = args.indexOf(flag);
	return index === -1 ? null : args[index + 1];
}

test('docker run argv publishes only 127.0.0.1 with an ephemeral port, sets the kilasflow.bench=FEAT-8mymac and kilasflow.bench.pid labels, names the container kf-bench-n8n-<8hex>, adds host.docker.internal:host-gateway, mounts nothing and is not privileged', () => {
	const args = dockerRunArgs({ name: NAME, image: DEFAULT_IMAGE, pid: 4242 });
	const joined = args.join(' ');
	assert.equal(args[0], 'run', 'the argv is the arguments after `docker`');
	assert.ok(args.includes('-d'), 'detached');
	assert.ok(joined.includes('-p 127.0.0.1::5678'), 'an ephemeral host port published on loopback only');
	assert.ok(!joined.includes('0.0.0.0'), 'never published on every interface');
	assert.ok(args.includes('--add-host=host.docker.internal:host-gateway'));
	assert.ok(args.includes(`--label=${LABEL_BENCH}=${BENCH_LABEL_VALUE}`));
	assert.ok(args.includes(`--label=${LABEL_PID}=4242`));
	assert.match(argValue(args, '--name'), /^kf-bench-n8n-[0-9a-f]{8}$/);
	assert.ok(args.includes(DEFAULT_IMAGE));
	// No volume, no privilege escalation.
	assert.ok(!args.some((arg) => arg === '-v' || arg === '--volume' || arg === '--mount'));
	assert.ok(!joined.includes('--privileged'));
	assert.ok(!joined.includes('/var/run/docker.sock'));
});

test('parseDockerPort extracts the host port from 127.0.0.1:49153 and from the two-line IPv4/IPv6 form', () => {
	assert.equal(parseDockerPort('127.0.0.1:49153'), 49153);
	assert.equal(parseDockerPort('  127.0.0.1:49153\n'), 49153);
	assert.equal(parseDockerPort('0.0.0.0:49153\n[::]:49153'), 49153);
	assert.equal(parseDockerPort('[::]:49153\n0.0.0.0:49153'), 49153);
	assert.equal(parseDockerPort(''), null);
	assert.equal(parseDockerPort('no port here'), null);
});

test('generateOwnerCredentials satisfies the n8n password rules, differs between calls and never appears in the docker argv or the recorded container env', () => {
	const first = generateOwnerCredentials({});
	const second = generateOwnerCredentials({});
	assert.notEqual(first.password, second.password);
	assert.notEqual(first.email, second.email);
	for (const credentials of [first, second]) {
		assert.match(credentials.email, /^bench-[0-9a-f]{8}@bench\.example$/);
		assert.ok(credentials.password.length >= 8 && credentials.password.length <= 64, 'n8n wants 8-64 characters');
		assert.match(credentials.password, /[0-9]/, 'n8n wants a digit');
		assert.match(credentials.password, /[A-Z]/, 'n8n wants an uppercase letter');
	}

	const args = dockerRunArgs({ name: NAME, image: DEFAULT_IMAGE, pid: 4242 });
	const argvText = args.join(' ');
	assert.ok(!argvText.includes(first.password), 'the owner password never reaches an argv');
	assert.ok(!argvText.includes(first.email), 'nor the owner email');
	// The container env we record carries keys only, and a generated owner
	// credential is never among them.
	const recorded = recordedContainerEnv([
		'PATH=/usr/local/bin',
		'N8N_LOG_LEVEL=warn',
		`N8N_EMAIL=${first.email}`,
		'N8N_ENCRYPTION_KEY=deadbeef',
	]);
	assert.ok(!recorded.some((entry) => entry.includes(first.password) || entry.includes(first.email)));
});

test('recordedContainerEnv drops N8N_ENCRYPTION_KEY and any *PASSWORD*/*KEY* variable', () => {
	const recorded = recordedContainerEnv([
		'PATH=/usr/local/bin',
		'N8N_LOG_LEVEL=warn',
		'N8N_ENCRYPTION_KEY=deadbeef',
		'MY_PASSWORD=secret',
		'signing_KEY=secret',
		'N8N_DIAGNOSTICS_ENABLED=false',
	]);
	assert.deepEqual([...recorded].sort(), ['N8N_DIAGNOSTICS_ENABLED', 'N8N_LOG_LEVEL', 'PATH'].sort());
	assert.ok(!recorded.some((key) => /password|key/i.test(key)), 'no key material is ever recorded');
});

test('n8nOrigin rewrites 127.0.0.1:<port> to host.docker.internal:<port>', () => {
	assert.equal(n8nOrigin('http://127.0.0.1:49153'), 'http://host.docker.internal:49153');
	assert.equal(n8nOrigin('http://127.0.0.1:49153/'), 'http://host.docker.internal:49153');
	assert.equal(n8nOrigin('http://host.docker.internal:1'), 'http://host.docker.internal:1');
});

test('the stale sweep selects only containers carrying the label whose owner pid is dead, and never a kf-pg-* container or a live peer run', () => {
	const containers = [
		{ name: 'kf-bench-n8n-aaaaaaaa', labels: { [LABEL_BENCH]: BENCH_LABEL_VALUE, [LABEL_PID]: '111' } },
		{ name: 'kf-bench-n8n-bbbbbbbb', labels: { [LABEL_BENCH]: BENCH_LABEL_VALUE, [LABEL_PID]: '222' } },
		{ name: 'kf-bench-n8n-cccccccc', labels: { [LABEL_BENCH]: 'OTHER-TICKET', [LABEL_PID]: '111' } },
		{ name: 'kf-pg-fpqvwx', labels: { [LABEL_BENCH]: BENCH_LABEL_VALUE, [LABEL_PID]: '111' } },
		{ name: 'kf-bench-n8n-dddddddd', labels: {} },
		{ name: 'kf-bench-n8n-eeeeeeee', labels: { [LABEL_BENCH]: BENCH_LABEL_VALUE } },
	];
	// 111 is alive (this run); 222 is dead (a crashed earlier run).
	assert.deepEqual(selectStaleContainers(containers, [111]), ['kf-bench-n8n-bbbbbbbb']);
	assert.deepEqual(selectStaleContainers(containers, [111, 222]), []);
});

test('importing n8n-container.mjs registers no signal handlers and starts nothing', async () => {
	const before = ['SIGINT', 'SIGTERM'].map((signal) => process.listenerCount(signal));
	const module = await import(`./n8n-container.mjs?import-safe=${Date.now()}`);
	const after = ['SIGINT', 'SIGTERM'].map((signal) => process.listenerCount(signal));
	assert.deepEqual(after, before, 'no signal handler is registered at import');
	assert.equal(typeof module.startN8nContainer, 'function', 'the module evaluates');
});
