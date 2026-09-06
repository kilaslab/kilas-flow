import { test, expect } from '../fixtures';
import { createCredential, createWorkflow, runWorkflow, waitForExecution } from '../helpers/seed';
import {
	 freshPacksDir,
	 nodepackgenBinary,
	 PACK_DIR_NAME,
	 PACK_TYPE,
	 runNodepackgen,
	 scaffoldPack,
	 startPackServer
} from '../fixtures/pack-install';

// FEAT-ykyfbd box 6: a pack node's outbound call goes through internal/routing
// and internal/safehttp like any other outbound call. allow_private_networks
// is never set: the stub is admitted through one exact host:port endpoint,
// and anything outside it is refused before it is sent.

async function helloPacksDir(): Promise<string> {
	const binary = await nodepackgenBinary();
	const packsDir = await freshPacksDir();
	await scaffoldPack(binary, packsDir, PACK_DIR_NAME, PACK_TYPE);
	return packsDir;
}

function helloWorkflow(credentialId: string): Record<string, unknown> {
	return {
		schemaVersion: 1,
		name: 'E2E Pack Safety',
		nodes: [
			{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
			{
				id: 'send',
				name: 'Send',
				type: PACK_TYPE,
				typeVersion: 1,
				position: { x: 240, y: 0 },
				parameters: { resource: 'message', operation: 'sendMessage', chatId: 'e2e-chat', text: 'hello packs' },
				credentials: { wahaApi: credentialId }
			}
		],
		connections: [
			{ id: 'c1', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'send', port: 'main' } }
		],
		settings: {}
	};
}

test('a pack node calling a disallowed host fails without reaching it', async ({ stub }) => {
	const packsDir = await helloPacksDir();
	const server = await startPackServer(stub, packsDir);
	try {
		// Same port, different loopback address: neither the allowed_hosts
		// entry (127.0.0.1) nor the exact host:port endpoint exemption covers
		// it — the smoke suite's refused-caller shape, through a pack node.
		const credential = await createCredential(server.baseURL, {
			name: 'E2E Pack Elsewhere',
			type: 'wahaApi',
			fields: { baseUrl: `http://127.0.0.2:${stub.port}`, apiKey: 'e2e-key' }
		});
		const workflow = await createWorkflow(server.baseURL, helloWorkflow(credential.id));
		const executionId = await runWorkflow(server.baseURL, workflow.id);
		const record = await waitForExecution(server.baseURL, executionId);
		expect(record.status).toBe('failed');

		expect(stub.requests.some((request) => request.path === '/sendMessage')).toBe(false);
	} finally {
		await server.close();
	}
});

test('a pack node with a credential scoped to another domain fails without sending', async ({ stub }) => {
	const packsDir = await helloPacksDir();
	const server = await startPackServer(stub, packsDir);
	try {
		// The credential's own AllowedDomains narrow the policy further: a
		// credential scoped at example.com must not authenticate a call to
		// the loopback stub, even though the host policy would admit it.
		const response = await fetch(`${server.baseURL}/api/v1/credentials`, {
			method: 'POST',
			headers: { 'content-type': 'application/json' },
			body: JSON.stringify({
				name: 'E2E Pack Scoped',
				type: 'wahaApi',
				fields: { baseUrl: stub.origin, apiKey: 'e2e-key' },
				allowedDomains: ['example.com']
			})
		});
		expect(response.status).toBe(201);
		const credential = await response.json();

		const workflow = await createWorkflow(server.baseURL, helloWorkflow(credential.id));
		const executionId = await runWorkflow(server.baseURL, workflow.id);
		const record = await waitForExecution(server.baseURL, executionId);
		expect(record.status).toBe('failed');

		expect(stub.requests.some((request) => request.path === '/sendMessage')).toBe(false);
	} finally {
		await server.close();
	}
});
