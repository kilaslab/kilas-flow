import { test, expect } from '../fixtures';
import { listExecutions, waitForNewExecution } from '../fixtures/waha-migration';
import { createCredential, createWorkflow } from '../helpers/seed';
import {
	freshPacksDir,
	nodepackgenBinary,
	PACK_DIR_NAME,
	PACK_TYPE,
	runNodepackgen,
	scaffoldPack,
	scaffoldTriggerPack,
	startPackServer,
	TRIGGER_PACK_TYPE,
	TRIGGER_PACK_VERSION
} from '../fixtures/pack-install';
// FEAT-ykyfbd box 4: a trigger pack installed from a directory binds a
// webhook, fans out by event, and registers itself with the remote service on
// activation. The trigger is purpose-built for the suite — two events plus a
// catch-all, an HMAC-free binding, a declarative lifecycle with both set and
// remove — sealed with the operator tooling, which refuses to seal it when it
// does not validate. The remote service is the loopback stub: the lifecycle's
// PUT and DELETE are observed there, and the fragile half — that the URL the
// server minted is the URL the service was told about — is asserted by
// extracting the route back out of the recorded body.

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

function delivery(event: string, id: string): Record<string, unknown> {
	return {
		event,
		session: 'default',
		payload: { from: 'e2e-user', body: 'ping', id }
	};
}

function branchSend(id: string, name: string, marker: string, credentialId: string): Record<string, unknown> {
	return {
		id,
		name,
		type: PACK_TYPE,
		typeVersion: 1,
		position: { x: 240, y: 0 },
		parameters: { resource: 'message', operation: 'sendMessage', chatId: marker, text: `event reached ${marker}` },
		credentials: { wahaApi: credentialId }
	};
}

test('a trigger pack activates its lifecycle, fans out by event, and unbinds on deactivation', async ({ stub }) => {
	const binary = await nodepackgenBinary();
	const packsDir = await freshPacksDir();
	await scaffoldPack(binary, packsDir, PACK_DIR_NAME, PACK_TYPE);
	await runNodepackgen(binary, ['pack', '-dir', `${packsDir}/${PACK_DIR_NAME}`]);
	const triggerDir = await scaffoldTriggerPack(packsDir, 'alerts', TRIGGER_PACK_TYPE);
	const sealed = await runNodepackgen(binary, ['pack', '-dir', triggerDir]);
	expect(sealed.stderr).toContain('pack.sha256');

	const server = await startPackServer(stub, packsDir);
	try {
		const catalogue = await api(server.baseURL, 'GET', '/node-types');
		const entries = Array.isArray(catalogue) ? catalogue : (catalogue.items ?? catalogue.data ?? []);
		const trigger = entries.find((entry: any) => entry.type === TRIGGER_PACK_TYPE);
		expect(trigger, 'the disk-loaded trigger is listed in the catalogue').toBeDefined();
		expect(trigger.source).toBe('pack');
		expect(trigger.version).toBe(TRIGGER_PACK_VERSION);

		const credential = await createCredential(server.baseURL, {
			name: 'E2E Trigger WAHA',
			type: 'wahaApi',
			fields: { baseUrl: stub.origin, apiKey: 'e2e-key' }
		});
		const workflow = await createWorkflow(server.baseURL, {
			schemaVersion: 1,
			name: 'E2E Pack Trigger',
			nodes: [
				{
					id: 'wt',
					name: 'Alert Trigger',
					type: TRIGGER_PACK_TYPE,
					typeVersion: TRIGGER_PACK_VERSION,
					position: { x: 0, y: 0 },
					parameters: { path: 'e2e-pack-trigger', session: 'default', autoRegister: true },
					credentials: { wahaApi: credential.id }
				},
				branchSend('helloA', 'On Message', 'branch-a', credential.id),
				branchSend('helloB', 'On Other', 'branch-b', credential.id)
			],
			connections: [
				{ id: 'c1', kind: 'main', source: { nodeId: 'wt', port: 'message' }, target: { nodeId: 'helloA', port: 'main' } },
				{ id: 'c2', kind: 'main', source: { nodeId: 'wt', port: 'other' }, target: { nodeId: 'helloB', port: 'main' } }
			],
			settings: {}
		});

		await api(server.baseURL, 'POST', `/workflows/${workflow.id}/activate`, {});
		// Lifecycle registration: the trigger told the remote service its
		// public URL on activation. PublicURL is path-only here because the
		// instance has no public base URL configured — the same empty-renders-
		// path-only rule as the resume links — so the assertion is exact: the
		// told URL is the route the deliveries below go to.
		const registrations = stub.requests.filter((request) => request.method === 'PUT' && request.path === '/api/subscriptions/default');
		expect(registrations, 'activation registered the minted URL with the service').toHaveLength(1);
		const minted = registrations[0].body.match(/\/webhook\/[0-9a-f]{32}/g) ?? [];
		expect(minted, 'the service was told exactly one minted route').toHaveLength(1);
		const route = minted[0];
		const told: unknown = JSON.parse(registrations[0].body);
		expect(told !== null && typeof told === 'object' && 'url' in told && typeof told.url === 'string' ? told.url : undefined, 'the service was told the minted route').toBe(route);

		// Per-event fan-out: a message delivery runs the message branch only.
		const sendsBefore = stub.requests.filter((request) => request.path === '/sendMessage').length;
		const known = (await listExecutions(server.baseURL, workflow.id)).map((item) => item.id);
		const delivered = await fetch(`${server.baseURL}${route}`, {
			method: 'POST',
			headers: { 'content-type': 'application/json' },
			body: JSON.stringify(delivery('message', 'e2e-delivery-message'))
		});
		expect(delivered.status).toBeLessThan(300);
		const first = await waitForNewExecution(server.baseURL, workflow.id, known);
		expect(first.record.status).toBe('succeeded');
		const sendsAfterMessage = stub.requests.filter((request) => request.path === '/sendMessage');
		expect(sendsAfterMessage.length - sendsBefore).toBe(1);
		expect(sendsAfterMessage[sendsAfterMessage.length - 1].body).toContain('branch-a');

		// An unrecognised event goes to the catch-all output instead.
		const knownBeforeOther = (await listExecutions(server.baseURL, workflow.id)).map((item) => item.id);
		const othered = await fetch(`${server.baseURL}${route}`, {
			method: 'POST',
			headers: { 'content-type': 'application/json' },
			body: JSON.stringify(delivery('something.else', 'e2e-delivery-other'))
		});
		expect(othered.status).toBeLessThan(300);
		const second = await waitForNewExecution(server.baseURL, workflow.id, knownBeforeOther);
		expect(second.record.status).toBe('succeeded');
		const sendsAfterOther = stub.requests.filter((request) => request.path === '/sendMessage');
		expect(sendsAfterOther.length - sendsAfterMessage.length).toBe(1);
		expect(sendsAfterOther[sendsAfterOther.length - 1].body).toContain('branch-b');

		// Deactivation unregisters from the service and drops the binding:
		// the declared `remove` fires, the route no longer resolves, and no
		// execution is recorded.
		await api(server.baseURL, 'POST', `/workflows/${workflow.id}/deactivate`, {});
		const removals = stub.requests.filter((request) => request.method === 'DELETE' && request.path === '/api/subscriptions/default');
		expect(removals, 'deactivation unregistered the URL from the service').toHaveLength(1);
		const executionsBefore = (await listExecutions(server.baseURL, workflow.id)).map((item) => item.id);
		const dead = await fetch(`${server.baseURL}${route}`, {
			method: 'POST',
			headers: { 'content-type': 'application/json' },
			body: JSON.stringify(delivery('message', 'e2e-delivery-after'))
		});
		expect(dead.status).toBe(404);
		expect((await listExecutions(server.baseURL, workflow.id)).map((item) => item.id)).toEqual(executionsBefore);
	} finally {
		await server.close();
	}
});
