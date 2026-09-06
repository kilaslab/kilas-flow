import { readFile } from 'node:fs/promises';

import { test, expect } from '../fixtures';
import { fetchDocument, saveDocument } from '../fixtures/waha-migration';
import { createCredential, createWorkflow, runWorkflow, waitForExecution } from '../helpers/seed';
import {
	CONVERTED_PACK_DIR_NAME,
	CONVERTED_PACK_TYPE,
	CONVERTED_SOURCE_TYPE,
	convertDeclarativePack,
	freshPacksDir,
	nodepackgenBinary,
	runNodepackgen,
	startPackServer
} from '../fixtures/pack-install';

// FEAT-ykyfbd boxes 7-8: the converter's output is a real pack, and an n8n
// workflow referencing the source node closes the loop through the importer.
// The transcription is operator-copied format facts (resource/operation
// strings, parameter shapes, routing metadata) — fixture data, never
// third-party bytes — converted by nodepack.ConvertDocument via the
// pack-convert driver, which is build-time tooling and never on the server's
// boot path.

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

async function convertedPacksDir(): Promise<{ packsDir: string; reportPath: string }> {
	const packsDir = await freshPacksDir();
	const { reportPath } = await convertDeclarativePack(packsDir, CONVERTED_PACK_DIR_NAME, CONVERTED_PACK_TYPE);
	const binary = await nodepackgenBinary();
	const validated = await runNodepackgen(binary, ['validate', `${packsDir}/${CONVERTED_PACK_DIR_NAME}`]);
	expect(validated.stderr).toContain('packs are valid');
	return { packsDir, reportPath };
}
test('a converter-produced pack loads and runs with the source resource and operation values', async ({ stub }) => {
	const { packsDir, reportPath } = await convertedPacksDir();

	// The values the pack selects on are the source's values byte for byte,
	// read back out of the transcription rather than retyped here.
	const source = JSON.parse(await readFile(new URL('../fixtures/pack-acme-transcription.json', import.meta.url), 'utf-8')) as {
		properties: Array<{ key: string; options?: Array<{ value: string; show?: { resource?: string[] } }> }>;
		requestDefaults: { baseURL: string };
	};
	const operationProperty = source.properties.find((property) => property.key === 'operation');
	const valuesFor = (resource: string): string[] =>
		(operationProperty?.options ?? []).filter((option) => option.show?.resource?.includes(resource)).map((option) => option.value);

	const manifest = JSON.parse(await readFile(`${packsDir}/${CONVERTED_PACK_DIR_NAME}/pack.json`, 'utf-8')) as {
		type: string;
		version: number;
		credentialType: string;
		requestDefaults: { baseURL: string };
		resources: Array<{ name: string; operations: Array<{ name: string }> }>;
	};
	expect(manifest.type).toBe(CONVERTED_PACK_TYPE);
	expect(manifest.version).toBe(1);
	expect(manifest.credentialType).toBe('wahaApi');
	expect(manifest.requestDefaults.baseURL).toBe(source.requestDefaults.baseURL);
	const byResource = new Map(manifest.resources.map((resource) => [resource.name, resource.operations.map((operation) => operation.name)]));
	// Convertible operations only: the five the interpreter cannot express
	// are excluded with named diagnostics, never emitted broken.
	expect([...byResource.keys()]).toEqual(['message', 'mailbox']);
	expect(byResource.get('message')).toEqual(valuesFor('message').filter((value) => ['send', 'get', 'list'].includes(value)));
	expect(byResource.get('mailbox')).toEqual(['list']);

	// The coverage report follows the WAHA report convention: what is absent,
	// named.
	const report = await readFile(reportPath, 'utf-8');
	expect(report).toContain('Operations converted: 4');
	expect(report).toContain('## Operations left out (5)');
	for (const excluded of ['"watch"', '"purge"', '"stream"', '"raw"', '"draft"']) {
		expect(report, `report names excluded operation ${excluded}`).toContain(excluded);
	}
	expect(report).toContain('credential type "wahaApi" is required');

	const server = await startPackServer(stub, packsDir);
	try {
		const catalogue = await api(server.baseURL, 'GET', '/node-types');
		const entries = Array.isArray(catalogue) ? catalogue : (catalogue.items ?? catalogue.data ?? []);
		const installed = entries.find((entry: any) => entry.type === CONVERTED_PACK_TYPE);
		expect(installed, 'the converted pack is listed in the catalogue').toBeDefined();
		expect(installed.source).toBe('pack');

		// The cascade narrows to the converted operations, the way the
		// editor's picker asks.
		const messageCascade = await api(server.baseURL, 'POST', `/node-types/${CONVERTED_PACK_TYPE}/load-options`, {
			property: 'operation',
			parameters: { resource: 'message' }
		});
		expect((messageCascade.options ?? []).map((option: any) => option.value).sort()).toEqual(['get', 'list', 'send']);
		const mailboxCascade = await api(server.baseURL, 'POST', `/node-types/${CONVERTED_PACK_TYPE}/load-options`, {
			property: 'operation',
			parameters: { resource: 'mailbox' }
		});
		expect((mailboxCascade.options ?? []).map((option: any) => option.value)).toEqual(['list']);

		const credential = await createCredential(server.baseURL, {
			name: 'E2E Converted WAHA',
			type: 'wahaApi',
			fields: { baseUrl: stub.origin, apiKey: 'e2e-key' }
		});
		const workflow = await createWorkflow(server.baseURL, {
			schemaVersion: 1,
			name: 'E2E Converted Pack',
			nodes: [
				{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
				{
					id: 'send',
					name: 'Acme Send',
					type: CONVERTED_PACK_TYPE,
					typeVersion: 1,
					position: { x: 240, y: 0 },
					parameters: { resource: 'message', operation: 'send', to: 'a@example.com', subject: 'hi' },
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

		const call = stub.requests.find((request) => request.method === 'POST' && request.path === '/v1/messages');
		// An excluded operation has no cascade route: the source values
		// select nothing, loudly rather than wrongly. The draft saves — the
		// pair is well-formed JSON — and the run fails naming the pair.
		const excluded = await createWorkflow(server.baseURL, {
			schemaVersion: 1,
			name: 'E2E Excluded Operation',
			nodes: [
				{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
				{
					id: 'watch',
					name: 'Acme Watch',
					type: CONVERTED_PACK_TYPE,
					typeVersion: 1,
					position: { x: 240, y: 0 },
					parameters: { resource: 'mailbox', operation: 'watch' },
					credentials: { wahaApi: credential.id }
				}
			],
			connections: [
				{ id: 'c1', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'watch', port: 'main' } }
			],
			settings: {}
		});
		const excludedExecutionId = await runWorkflow(server.baseURL, excluded.id);
		const excludedRecord = await waitForExecution(server.baseURL, excludedExecutionId);
		expect(excludedRecord.status).toBe('failed');
		expect(excludedRecord.error?.message ?? '').toContain('no request is declared for resource "mailbox" operation "watch"');
	} finally {
		await server.close();
	}
});

test('an n8n workflow referencing the converted node imports and binds to it', async ({ stub }) => {
	const { packsDir } = await convertedPacksDir();
	const server = await startPackServer(stub, packsDir);
	try {
		// The n8n document references the source type, exactly as a workflow
		// JSON authored against the community node carries it.
		const imported = await api(server.baseURL, 'POST', '/workflows/import', {
			format: 'n8n',
			name: 'E2E Acme Import',
			workflow: {
				name: 'Acme mail (n8n)',
				nodes: [
					{
						id: 'acme-1',
						name: 'Acme Mail',
						type: CONVERTED_SOURCE_TYPE,
						typeVersion: 1,
						position: [0, 0],
						parameters: { resource: 'message', operation: 'send', to: 'a@example.com', subject: 'hi' }
					}
				],
				connections: {}
			}
		}, 201);

		// No mapping claims the source type, so it arrives as the blocking
		// placeholder — visible, preserved, and refusing to run until it is
		// replaced. The import-report vocabulary (FEAT-0556ck): blocking
		// stops the workflow running.
		const issues = imported.unsupported as Array<{ severity: string; nodeName?: string; type?: string; reason: string }>;
		const placeholder = issues.find((issue) => issue.type === CONVERTED_SOURCE_TYPE);
		expect(placeholder, 'the import names the unmapped source node').toBeDefined();
		expect(placeholder?.severity).toBe('blocking');
		expect(placeholder?.reason).toContain(CONVERTED_SOURCE_TYPE);

		// The capsule keeps the whole source node, so the values the operator
		// rebinds are the values the author wrote — byte for byte.
		const document = await fetchDocument(server.baseURL, imported.workflow.id);
		const capsule = document.nodes.find((node) => node.name === 'Acme Mail');
		expect(capsule?.type).toBe('kilasflow.unsupported');
		const original = (capsule?.parameters?.original as { parameters?: Record<string, unknown> } | undefined)?.parameters;
		expect(original?.resource).toBe('message');
		expect(original?.operation).toBe('send');

		// Binding: the placeholder is replaced by the converted pack node
		// carrying the preserved values, plus a trigger and a real
		// credential — the same rebind step the WAHA migration performs.
		const credential = await createCredential(server.baseURL, {
			name: 'E2E Import WAHA',
			type: 'wahaApi',
			fields: { baseUrl: stub.origin, apiKey: 'e2e-key' }
		});
		document.nodes = [
			{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
			{
				id: capsule?.id ?? 'acme-1',
				name: 'Acme Mail',
				type: CONVERTED_PACK_TYPE,
				typeVersion: 1,
				position: { x: 240, y: 0 },
				parameters: { resource: 'message', operation: 'send', to: 'a@example.com', subject: 'hi' },
				credentials: { wahaApi: credential.id }
			}
		];
		document.connections = [
			{ id: 'c1', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: capsule?.id ?? 'acme-1', port: 'main' } }
		] as unknown[];
		await saveDocument(server.baseURL, imported.workflow.id, document);

		const executionId = await runWorkflow(server.baseURL, imported.workflow.id);
		const record = await waitForExecution(server.baseURL, executionId);
		expect(record.status).toBe('succeeded');

		const call = stub.requests.find((request) => request.method === 'POST' && request.path === '/v1/messages');
		expect(call, 'the rebound converted node reached the stub').toBeDefined();
		expect(JSON.parse(call?.body ?? '{}')).toMatchObject({ to: 'a@example.com', subject: 'hi' });
	} finally {
		await server.close();
	}
});
