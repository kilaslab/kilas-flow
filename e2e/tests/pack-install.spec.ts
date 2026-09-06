import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

import { test, expect } from '../fixtures';
import {
	createCredential,
	createWorkflow,
	readExecutionEvents,
	runWorkflow,
	waitForExecution
} from '../helpers/seed';
import {
	bootRefusal,
	freshPacksDir,
	nodepackgenBinary,
	PACK_DIR_NAME,
	PACK_TYPE,
	runNodepackgen,
	scaffoldPack,
	startPackServer,
	tamperManifest,
	writeChecksum
} from '../fixtures/pack-install';

// FEAT-ykyfbd: the author's file becomes a running workflow. The primary path
// goes through the operator tooling — scaffold, validate, pack — because that
// is what an operator runs and it exercises checksum generation too; direct
// file writes are reserved for the malformed and tampered cases the tooling
// would refuse to produce. nodepackgen is Go: no Node.js process serves
// anything here except the loopback stub the workflow calls.

async function api(baseURL: string, method: string, path: string, body?: unknown, wantStatus = 200): Promise<any> {
	const response = await fetch(`${baseURL}/api/v1${path}`, {
		method,
		headers: { 'content-type': 'application/json' },
		body: body === undefined ? undefined : JSON.stringify(body)
	});
	if (response.status !== wantStatus) {
		throw new Error(`${method} ${path}: status ${response.status}, want ${wantStatus} (body: ${await response.text()})`);
	}
	if (response.status === 204) return null;
	return response.json();
}

test('an authored pack installs from a directory and runs a workflow through it', async ({ stub }) => {
	const binary = await nodepackgenBinary();
	const packsDir = await freshPacksDir();
	const packDir = await scaffoldPack(binary, packsDir, PACK_DIR_NAME, PACK_TYPE);

	const validated = await runNodepackgen(binary, ['validate', packDir]);
	expect(validated.stderr).toContain('packs are valid');

	const sealed = await runNodepackgen(binary, ['pack', '-dir', packDir]);
	expect(sealed.stderr).toContain('pack.sha256');

	const server = await startPackServer(stub, packsDir);
	try {
		// The catalogue tags the installed node as a pack, not a built-in.
		const catalogue = await api(server.baseURL, 'GET', '/node-types');
		const entries = Array.isArray(catalogue) ? catalogue : (catalogue.items ?? catalogue.data ?? []);
		const installed = entries.find((entry: any) => entry.type === PACK_TYPE);
		expect(installed, 'the installed pack is listed in the catalogue').toBeDefined();
		expect(installed.source).toBe('pack');
		expect(installed.version).toBe(1);

		// The pack's credential type is offered by the picker: wahaApi is the
		// built-in type the scaffolded pack authenticates with.
		const credentialTypes = await api(server.baseURL, 'GET', '/credential-types');
		const types = Array.isArray(credentialTypes) ? credentialTypes : (credentialTypes.items ?? credentialTypes.data ?? []);
		expect(types.some((entry: any) => entry.id === 'wahaApi' || entry.type === 'wahaApi')).toBe(true);

		// The generated resource/operation cascade narrows operations to the
		// selected resource, the way the editor's picker asks.
		const cascade = await api(server.baseURL, 'POST', `/node-types/${PACK_TYPE}/load-options`, {
			property: 'operation',
			parameters: { resource: 'message' }
		});
		expect((cascade.options ?? []).map((option: any) => option.value)).toContain('sendMessage');

		const credential = await createCredential(server.baseURL, {
			name: 'E2E Pack WAHA',
			type: 'wahaApi',
			fields: { baseUrl: stub.origin, apiKey: 'e2e-key' }
		});

		const workflow = await createWorkflow(server.baseURL, {
			schemaVersion: 1,
			name: 'E2E Pack Install',
			nodes: [
				{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
				{
					id: 'send',
					name: 'Send',
					type: PACK_TYPE,
					typeVersion: 1,
					position: { x: 240, y: 0 },
					parameters: { resource: 'message', operation: 'sendMessage', chatId: 'e2e-chat', text: 'hello packs' },
					credentials: { wahaApi: credential.id }
				}
			],
			connections: [
				{ id: 'c1', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'send', port: 'main' } }
			],
			settings: {}
		});

		const executionId = await runWorkflow(server.baseURL, workflow.id);
		const record = await waitForExecution(server.baseURL, executionId);
		expect(record.status).toBe('succeeded');

		const call = stub.requests.find((request) => request.method === 'POST' && request.path === '/sendMessage');
		expect(call, 'the pack node reached the stub').toBeDefined();
		expect(JSON.parse(call?.body ?? '{}')).toMatchObject({ chatId: 'e2e-chat', text: 'hello packs' });

		const events = await readExecutionEvents(server.baseURL, executionId);
		expect(events.map((event) => event.type)).toContain('execution.completed');
	} finally {
		await server.close();
	}
});

test('a pack whose manifest no longer matches its checksum refuses startup naming the pack', async ({ stub }) => {
	const binary = await nodepackgenBinary();
	const packsDir = await freshPacksDir();
	const packDir = await scaffoldPack(binary, packsDir, 'tampered', 'pack.e2etampered');
	await runNodepackgen(binary, ['pack', '-dir', packDir]);
	await tamperManifest(packDir);

	const refusal = await bootRefusal(stub, packsDir);
	expect(refusal).toContain('"tampered"');
	expect(refusal).toContain('pack.json');
	expect(refusal).toContain('recorded digest');
});

test('a malformed pack refuses startup naming the pack and its manifest', async ({ stub }) => {
	const binary = await nodepackgenBinary();
	const packsDir = await freshPacksDir();
	const packDir = join(packsDir, 'broken');
	await mkdir(packDir, { recursive: true });
	const manifest = '{"type": "pack.broken", oops';
	await writeFile(join(packDir, 'pack.json'), manifest);
	await writeChecksum(packDir, manifest);

	// The tooling refuses to seal it first, naming the file and the field —
	// the operator learns before the server ever sees it.
	let validateFailed = false;
	try {
		await runNodepackgen(binary, ['validate', packDir]);
	} catch (error) {
		validateFailed = true;
		expect(String(error)).toContain('pack.json');
	}
	expect(validateFailed, 'validate refuses the malformed manifest').toBe(true);

	const refusal = await bootRefusal(stub, packsDir);
	expect(refusal).toContain('"broken"');
	expect(refusal).toContain('pack.json');
});

test('a pack claiming the reserved namespace refuses startup naming the pack', async ({ stub }) => {
	const binary = await nodepackgenBinary();
	const packsDir = await freshPacksDir();
	const packDir = await scaffoldPack(binary, packsDir, 'evil', 'pack.e2etemp');
	const manifest = JSON.parse(await readFile(join(packDir, 'pack.json'), 'utf-8')) as Record<string, unknown>;
	manifest.type = 'kilasflow.evil';
	const rewritten = `${JSON.stringify(manifest, null, 2)}\n`;
	await writeFile(join(packDir, 'pack.json'), rewritten);
	await writeChecksum(packDir, rewritten);

	const refusal = await bootRefusal(stub, packsDir);
	expect(refusal).toContain('"evil"');
	expect(refusal).toContain('kilasflow.');
});
