// Live webhook handling suite (EPIC-87t47t, FEAT-41m8dj): every delivery path
// through internal/webhook/webhook.go against the real binary — creation and
// deactivation, basic and header auth, the three response modes, delivery
// dedupe, method routing, CORS preflight, and the body ceiling.
//
// Every case mints its own workflow from the pinned webhook-echo sample
// (mintWebhookWorkflows) and reshapes the trigger node in place, so the minted
// route survives and the test is the caller a real sender would be.
import { test, expect } from '../fixtures';
import { deliver, liveApi, mintWebhookWorkflows, waitForTriggerExecution, type ExecutionPage, type WorkflowResource } from '../fixtures/live-backend';
import { createCredential } from '../helpers/seed';
import { activateWorkflow, fetchDocument, importN8nTemplate, saveDocument, type WorkflowDocument } from '../fixtures/waha-migration';
import { uniqueName } from '../fixtures/datastore';

type JsonObject = Record<string, unknown>;

function jsonObject(value: unknown, what: string): JsonObject {
	if (typeof value !== 'object' || value === null || Array.isArray(value)) {
		throw new Error(`${what}: expected a JSON object, got ${JSON.stringify(value)}`);
	}
	return value as JsonObject;
}

function marker(value: string): JsonObject {
	return { mode: 'expression', value };
}

async function executionIds(baseURL: string, workflowId: string): Promise<string[]> {
	const page = await liveApi<ExecutionPage>(
		baseURL,
		'GET',
		`/executions?workflowId=${encodeURIComponent(workflowId)}&limit=100`
	);
	return page.items.map((item) => item.id);
}

// reshapeWebhook patches the minted workflow's trigger node and re-activates
// it. The node's id is preserved, which is what keeps the minted route bound.
async function reshapeWebhook(
	baseURL: string,
	workflowId: string,
	patch: JsonObject,
	credentials?: Record<string, string>
): Promise<void> {
	const document: WorkflowDocument = await fetchDocument(baseURL, workflowId);
	const webhook = document.nodes.find((node) => node.type === 'kilasflow.webhook');
	if (!webhook) throw new Error(`workflow ${workflowId} carries no webhook trigger`);
	webhook.parameters = { ...(webhook.parameters ?? {}), ...patch };
	if (credentials) webhook.credentials = credentials;
	await saveDocument(baseURL, workflowId, document);
	await activateWorkflow(baseURL, workflowId);
}

function webhookDocument(name: string, parameters: JsonObject): JsonObject {
	return {
		schemaVersion: 1,
		name,
		nodes: [
			{ id: 'webhook', name: 'Webhook', type: 'kilasflow.webhook', typeVersion: 1, position: { x: 0, y: 0 }, parameters }
		],
		connections: [],
		settings: {}
	};
}

test('an endpoint that cannot route is refused at activation, and deactivation unbinds it', async ({ server }) => {
	const baseURL = server.baseURL;

	// An empty path never becomes an endpoint: the draft saves, activation says
	// why it cannot.
	for (const [label, parameters] of [
		['empty path', { path: '', httpMethod: 'POST', authentication: 'none', responseMode: 'immediate' }],
		['unknown method', { path: 'brew', httpMethod: 'BREW', authentication: 'none', responseMode: 'immediate' }]
	] as Array<[string, JsonObject]>) {
		const created = await liveApi<WorkflowResource>(
			baseURL,
			'POST',
			'/workflows',
			webhookDocument(uniqueName(`webhook ${label}`), parameters),
			201
		);
		await liveApi(baseURL, 'POST', `/workflows/${created.id}/activate`, {}, 422);
	}

	// n8n's jwtAuth has no verifier here, so the import refuses to activate
	// rather than silently publishing an open endpoint.
	const imported = await importN8nTemplate(
		baseURL,
		{
			name: 'jwt guarded webhook',
			nodes: [
				{
					id: 'a',
					name: 'Webhook',
					type: 'n8n-nodes-base.webhook',
					typeVersion: 2,
					position: [0, 0],
					parameters: { path: 'jwt-guard', httpMethod: 'POST', responseMode: 'immediate', authentication: 'jwtAuth' }
				}
			],
			connections: {},
			active: false,
			settings: {}
		},
		uniqueName('webhook jwt')
	);
	await liveApi(baseURL, 'POST', `/workflows/${imported.workflow.id}/activate`, {}, 422);

	// A deactivated workflow keeps its route reserved but answers 404, and a
	// refused delivery is not an execution.
	const [minted] = await mintWebhookWorkflows(baseURL, 1, uniqueName('webhook matrix'));
	const before = await executionIds(baseURL, minted.workflowId);
	const deactivated = await liveApi<WorkflowResource>(baseURL, 'POST', `/workflows/${minted.workflowId}/deactivate`);
	expect(deactivated.active).toBe(false);
	const refused = await deliver(baseURL, minted.url, { json: { text: 'nobody home' } });
	expect(refused.status).toBe(404);
	expect(refused.text).toContain('No active workflow is bound');
	expect(await executionIds(baseURL, minted.workflowId)).toEqual(before);
});

test('basic auth refuses a nameless caller and admits the credential holder', async ({ server }) => {
	const baseURL = server.baseURL;
	const [minted] = await mintWebhookWorkflows(baseURL, 1, uniqueName('webhook basic'));
	const credential = await createCredential(baseURL, {
		name: uniqueName('webhook basic'),
		type: 'httpBasicAuth',
		fields: { user: 'ada', password: 'hunter2' }
	});
	await reshapeWebhook(baseURL, minted.workflowId, { authentication: 'basicAuth' }, { httpBasicAuth: credential.id });

	const before = await executionIds(baseURL, minted.workflowId);
	const anonymous = await deliver(baseURL, minted.url, { json: { text: 'anonymous' } });
	expect(anonymous.status).toBe(401);
	expect(anonymous.headers['www-authenticate']).toContain('Basic realm="webhook"');
	// A refused caller is never queued.
	expect(await executionIds(baseURL, minted.workflowId)).toEqual(before);

	const admitted = await deliver(baseURL, minted.url, {
		headers: { authorization: `Basic ${Buffer.from('ada:hunter2').toString('base64')}` },
		json: { text: 'named caller' }
	});
	expect(admitted.status).toBeLessThan(300);
	const record = await waitForTriggerExecution(baseURL, minted.workflowId, 'webhook', before);
	expect(record.status).toBe('succeeded');
});

test('header auth refuses a wrong key and admits the configured one', async ({ server }) => {
	const baseURL = server.baseURL;
	const [minted] = await mintWebhookWorkflows(baseURL, 1, uniqueName('webhook header'));
	const credential = await createCredential(baseURL, {
		name: uniqueName('webhook header'),
		type: 'httpHeaderAuth',
		fields: { name: 'x-api-key', value: 's3cret' }
	});
	await reshapeWebhook(baseURL, minted.workflowId, { authentication: 'headerAuth' }, { httpHeaderAuth: credential.id });

	const wrong = await deliver(baseURL, minted.url, { headers: { 'x-api-key': 'guess' }, json: { text: 'nope' } });
	expect(wrong.status).toBe(401);

	const before = await executionIds(baseURL, minted.workflowId);
	const right = await deliver(baseURL, minted.url, { headers: { 'x-api-key': 's3cret' }, json: { text: 'yes' } });
	expect(right.status).toBeLessThan(300);
	await waitForTriggerExecution(baseURL, minted.workflowId, 'webhook', before);
});

test('response modes answer the caller immediately, from the last node, or from Respond', async ({ server }) => {
	const baseURL = server.baseURL;

	// immediate: the configured code and headers, n8n's acknowledgement body.
	const [immediate] = await mintWebhookWorkflows(baseURL, 1, uniqueName('webhook immediate'));
	await reshapeWebhook(baseURL, immediate.workflowId, {
		responseMode: 'immediate',
		options: { responseCode: 201, responseHeaders: { 'x-mode': 'immediate' }, noResponseBody: false }
	});
	const acknowledged = await deliver(baseURL, immediate.url, { json: { text: 'queued fast' } });
	expect(acknowledged.status).toBe(201);
	expect(acknowledged.headers['x-mode']).toBe('immediate');
	expect(jsonObject(acknowledged.json, 'immediate acknowledgement')).toEqual({ message: 'Workflow was started' });

	// lastNode: the last node that produced items is the answer, and the graph
	// has no Respond node — otherwise the node would answer instead. The
	// sample's Set reads a top-level `text`; the n8n webhook item nests the
	// body, so the assignment is pointed at `$json.body.text` the way an author
	// would after seeing the item shape.
	const [lastNode] = await mintWebhookWorkflows(baseURL, 1, uniqueName('webhook lastnode'));
	const document = await fetchDocument(baseURL, lastNode.workflowId);
	const shape = document.nodes.find((node) => node.type === 'kilasflow.set');
	const respond = document.nodes.find((node) => node.type === 'kilasflow.respondToWebhook');
	const webhook = document.nodes.find((node) => node.type === 'kilasflow.webhook');
	if (!respond || !webhook || !shape) throw new Error('the minted sample changed shape');
	shape.parameters = { assignments: { greeting: marker('{{ $json.body.text }}') } };
	webhook.parameters = { ...(webhook.parameters ?? {}), responseMode: 'lastNode', responseData: 'firstEntryJson' };
	document.nodes = document.nodes.filter((node) => node.id !== respond.id);
	document.connections = document.connections.filter((connection) => {
		const edge = connection as { source?: { nodeId?: string }; target?: { nodeId?: string } };
		return edge.source?.nodeId !== respond.id && edge.target?.nodeId !== respond.id;
	});
	await saveDocument(baseURL, lastNode.workflowId, document);
	await activateWorkflow(baseURL, lastNode.workflowId);

	const fromLastNode = await deliver(baseURL, lastNode.url, { json: { text: 'last node' } });
	expect(fromLastNode.status).toBe(200);
	const lastBody = jsonObject(fromLastNode.json, 'lastNode body');
	expect(lastBody.greeting).toBe('last node');
	// The shape proves it is the node's item and not the boundary's envelope.
	expect(jsonObject(lastBody.body, 'lastNode item body').text).toBe('last node');

	// responseNode: the Respond node's own status and body answer the caller
	// while the run continues (awaitResponse on the live event). The durable
	// copy of that response is what a split api+worker deployment reads back;
	// it is asserted at the store level in Go (BUG-cq4yk3), not exposed here.
	const [responseNode] = await mintWebhookWorkflows(baseURL, 1, uniqueName('webhook respond'));
	await reshapeWebhook(baseURL, responseNode.workflowId, { responseMode: 'responseNode' });
	const document2 = await fetchDocument(baseURL, responseNode.workflowId);
	const respond2 = document2.nodes.find((node) => node.type === 'kilasflow.respondToWebhook');
	const shape2 = document2.nodes.find((node) => node.type === 'kilasflow.set');
	if (!respond2 || !shape2) throw new Error('the minted sample changed shape');
	shape2.parameters = { assignments: { greeting: marker('{{ $json.body.text }}') } };
	respond2.parameters = {
		...(respond2.parameters ?? {}),
		respondWith: 'json',
		responseCode: 202,
		responseBodyJSON: marker('{{ $json }}')
	};
	await saveDocument(baseURL, responseNode.workflowId, document2);
	await activateWorkflow(baseURL, responseNode.workflowId);

	const before = await executionIds(baseURL, responseNode.workflowId);
	const shaped = await deliver(baseURL, responseNode.url, { json: { text: 'shaped ack' } });
	expect(shaped.status).toBe(202);
	expect(jsonObject(shaped.json, 'responseNode body').greeting).toBe('shaped ack');
	const record = await waitForTriggerExecution(baseURL, responseNode.workflowId, 'webhook', before);
	expect(record.nodeRuns?.find((run) => run.nodeId === respond2.id)?.status).toBe('succeeded');
});

test('a repeated delivery with the same identifier runs the workflow once', async ({ server }) => {
	const baseURL = server.baseURL;
	const [minted] = await mintWebhookWorkflows(baseURL, 1, uniqueName('webhook dedupe'));
	await reshapeWebhook(baseURL, minted.workflowId, { deliveryIdHeader: 'x-e2e-delivery' });

	const before = await executionIds(baseURL, minted.workflowId);
	const first = await deliver(baseURL, minted.url, {
		headers: { 'x-e2e-delivery': 'd-1' },
		json: { text: 'first attempt' }
	});
	expect(first.status).toBeLessThan(300);
	await expect
		.poll(async () => (await executionIds(baseURL, minted.workflowId)).length, {
			message: 'the first delivery queues exactly one execution'
		})
		.toBe(before.length + 1);
	const queued = (await executionIds(baseURL, minted.workflowId)).filter((id) => !before.includes(id));
	expect(queued).toHaveLength(1);

	// The retry is answered without running the workflow again, and names the
	// original execution — the sender learns its first attempt did land.
	const duplicate = await deliver(baseURL, minted.url, {
		headers: { 'x-e2e-delivery': 'd-1' },
		json: { text: 'retry' }
	});
	expect(duplicate.status).toBeLessThan(300);
	const duplicateBody = jsonObject(duplicate.json, 'duplicate answer');
	expect(duplicateBody.duplicate).toBe(true);
	expect(duplicateBody.executionId).toBe(queued[0]);
	expect((await executionIds(baseURL, minted.workflowId)).length).toBe(before.length + 1);

	// A different identifier is a different delivery.
	await deliver(baseURL, minted.url, { headers: { 'x-e2e-delivery': 'd-2' }, json: { text: 'second' } });
	await expect
		.poll(async () => (await executionIds(baseURL, minted.workflowId)).length, {
			message: 'a distinct delivery identifier queues its own execution'
		})
		.toBe(before.length + 2);

	// Without the header every request runs: two identical bodies are two
	// messages, not one.
	await deliver(baseURL, minted.url, { json: { text: 'same' } });
	await deliver(baseURL, minted.url, { json: { text: 'same' } });
	await expect
		.poll(async () => (await executionIds(baseURL, minted.workflowId)).length, {
			message: 'deliveries without an identifier are never deduped'
		})
		.toBe(before.length + 4);
});

test('routing answers the bound method, the CORS preflight, and the body ceiling', async ({ server }) => {
	const baseURL = server.baseURL;
	const [minted] = await mintWebhookWorkflows(baseURL, 1, uniqueName('webhook routing'));

	// The route answers its bound method only; a wrong method is a 404 that
	// does not confirm the endpoint exists.
	const wrongMethod = await deliver(baseURL, minted.url, { method: 'GET' });
	expect(wrongMethod.status).toBe(404);
	expect(wrongMethod.text).toContain('No active workflow is bound');

	// A browser's preflight is answered from what is actually bound.
	const preflight = await deliver(baseURL, minted.url, {
		method: 'OPTIONS',
		headers: { origin: 'https://app.example', 'access-control-request-method': 'POST' }
	});
	expect(preflight.status).toBe(204);
	expect(preflight.headers['access-control-allow-methods']).toContain('POST');
	expect(preflight.headers['access-control-allow-origin']).toBe('https://app.example');

	// The body ceiling is the configured one mebibyte: a larger delivery is
	// refused before anything is decoded or queued.
	const before = await executionIds(baseURL, minted.workflowId);
	const oversized = await deliver(baseURL, minted.url, {
		headers: { 'content-type': 'application/json' },
		rawBody: 'x'.repeat((1 << 20) + 1)
	});
	expect(oversized.status).toBe(413);
	expect(await executionIds(baseURL, minted.workflowId)).toEqual(before);
});
