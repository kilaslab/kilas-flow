// Live backend API suite (EPIC-87t47t, FEAT-2mth85): a workflow authored over
// REST becomes a callable HTTP API — webhook in, datastore out, Respond to the
// caller — against the real binary on a fresh SQLite data directory per test
// (e2e/fixtures.ts).
//
// Route discovery: GET /workflows/{id}/webhooks reports the opaque address a
// natively authored workflow answers on, before activation and unchanged by it,
// so this suite never reaches into the instance database to learn a URL
// (FEAT-cwmw90).
import { randomBytes } from 'node:crypto';

import { test, expect } from '../fixtures';
import {
	addColumn,
	conditionRow,
	createDatastore,
	datastoreLocator,
	manualColumns,
	uniqueName
} from '../fixtures/datastore';
import {
	deliver,
	liveApi,
	mintWebhookWorkflows,
	waitForTriggerExecution,
	type DeliveryOptions,
	type ExecutionPage,
	type ExecutionRecord,
	type ExecutionSummaryRow,
	type MintedWebhookWorkflow,
	type WorkflowResource
} from '../fixtures/live-backend';
import { readExecutionEvents } from '../helpers/seed';

type JsonObject = Record<string, unknown>;

function specNode(id: string, name: string, type: string, parameters?: JsonObject): JsonObject {
	return { id, name, type, typeVersion: 1, position: { x: 0, y: 0 }, ...(parameters ? { parameters } : {}) };
}

function specConn(id: string, source: string, target: string): JsonObject {
	return { id, kind: 'main', source: { nodeId: source, port: 'main' }, target: { nodeId: target, port: 'main' } };
}

function webhookNode(id: string, parameters: JsonObject): JsonObject {
	return specNode(id, 'Webhook', 'kilasflow.webhook', {
		path: 'live-api',
		httpMethod: 'POST',
		authentication: 'none',
		responseMode: 'responseNode',
		...parameters
	});
}

// jsonObject narrows a delivery or execution payload the caller expects to be
// an object, failing with the actual value rather than a TypeError.
function jsonObject(value: unknown, what: string): JsonObject {
	if (typeof value !== 'object' || value === null || Array.isArray(value)) {
		throw new Error(`${what}: expected a JSON object, got ${JSON.stringify(value)}`);
	}
	return value as JsonObject;
}

// webhookUrl reads the opaque address a workflow's trigger answers on.
//
// It is the API's own published URL — minted on first read — so the suite
// asserts what a sender is told rather than reconstructing it from storage.
async function webhookUrl(baseURL: string, workflowId: string, nodeId: string): Promise<string> {
	const listed = await liveApi<Array<{ nodeId: string; method: string; url: string }>>(
		baseURL,
		'GET',
		`/workflows/${workflowId}/webhooks`
	);
	const route = listed.find((entry) => entry.nodeId === nodeId);
	expect(route, `no webhook address published for node ${nodeId} of ${workflowId}`).toBeTruthy();
	expect(route!.url).toMatch(/^\/webhook\/[0-9a-f]{32}$/);
	return route!.url;
}

async function executionIds(baseURL: string, workflowId: string): Promise<string[]> {
	const page = await liveApi<ExecutionPage>(
		baseURL,
		'GET',
		`/executions?workflowId=${encodeURIComponent(workflowId)}&limit=100`
	);
	return page.items.map((item: ExecutionSummaryRow) => item.id);
}

// simpleApiDocument is the smallest backend: a POST body in, one field echoed
// by a Set node, the caller answered from a Respond node.
function simpleApiDocument(name: string, path: string): JsonObject {
	return {
		schemaVersion: 1,
		name,
		nodes: [
			webhookNode('webhook', { path }),
			specNode('shape', 'Shape', 'kilasflow.set', {
				assignments: { echo: { mode: 'expression', value: '{{ $json.body.text }}' } }
			}),
			specNode('respond', 'Respond', 'kilasflow.respondToWebhook', {
				respondWith: 'json',
				// The explicit expression marker is what the engine evaluates;
				// a bare `{{ }}` string is data. This is the form an n8n import
				// produces from `={{ $json }}` (`fromN8NValue`).
				responseBodyJSON: { mode: 'expression', value: '{{ $json }}' },
				responseCode: 200
			})
		],
		connections: [specConn('c1', 'webhook', 'shape'), specConn('c2', 'shape', 'respond')],
		settings: {}
	};
}

test('a workflow authored over REST answers as a JSON API', async ({ server }) => {
	const baseURL = server.baseURL;
	const name = uniqueName('live api simple');
	const document = simpleApiDocument(name, 'orders-simple');

	// POST /workflows: 201 and a Location naming the new workflow.
	const created = await fetch(`${baseURL}/api/v1/workflows`, {
		method: 'POST',
		headers: { 'content-type': 'application/json' },
		body: JSON.stringify(document)
	});
	expect(created.status).toBe(201);
	const createdBody = (await created.json()) as WorkflowResource;
	expect(created.headers.get('location')).toBe(`/api/v1/workflows/${createdBody.id}`);
	expect(createdBody.latestVersion.revision).toBe(1);

	// PUT: rename and add a response header on the Respond node. A save is a
	// new immutable revision, not a mutation of the first.
	const evolved = structuredClone(document);
	evolved.name = `${name} v2`;
	(evolved.nodes as JsonObject[])[2].parameters = {
		...(evolved.nodes as JsonObject[])[2].parameters as JsonObject,
		responseHeaders: { 'x-e2e': '1' }
	};
	const saved = await liveApi<WorkflowResource>(baseURL, 'PUT', `/workflows/${createdBody.id}`, evolved);
	expect(saved.latestVersion.revision).toBe(2);

	// The address is published before activation, so a caller can configure the
	// sender while the workflow is still a draft…
	const published = await webhookUrl(baseURL, createdBody.id, 'webhook');

	const activated = await liveApi<WorkflowResource>(baseURL, 'POST', `/workflows/${createdBody.id}/activate`);
	expect(activated.active).toBe(true);

	// …and activation does not change it: a sender already pointed at the URL
	// it was given keeps working.
	const url = await webhookUrl(baseURL, createdBody.id, 'webhook');
	expect(url).toBe(published);
	const before = await executionIds(baseURL, createdBody.id);

	const response = await deliver(baseURL, url, { json: { text: 'hello backend' } });
	expect(response.status).toBe(200);
	const body = jsonObject(response.json, 'the workflow response');
	expect(body.echo).toBe('hello backend');
	expect(response.headers['x-e2e']).toBe('1');

	const record = await waitForTriggerExecution(baseURL, createdBody.id, 'webhook', before);
	expect(record.status).toBe('succeeded');
	expect(record.trigger).toBe('webhook');
	expect(record.nodeRuns?.find((run) => run.nodeId === 'respond')?.status).toBe('succeeded');
	const events = await readExecutionEvents(baseURL, record.id);
	expect(events.map((event) => event.type)).toContain('execution.completed');
});

test('four REST-shaped CRUD endpoints over one datastore share their rows', async ({ server }) => {
	const baseURL = server.baseURL;
	const store = await createDatastore(baseURL, uniqueName('live api orders'));
	await addColumn(baseURL, store.id, 'sku', 'string');
	await addColumn(baseURL, store.id, 'qty', 'number');

	const locator = datastoreLocator('id', store.id);
	const stamp = randomBytes(3).toString('hex');
	const sku = `SKU-${stamp}`;
	const [create, get, update, remove] = await mintWebhookWorkflows(baseURL, 4, `live api orders ${stamp}`);

	// One shape per verb: webhook -> data table row operation -> Respond with
	// the node's own first item, so the caller receives the row itself.
	const shape = (
		minted: MintedWebhookWorkflow,
		method: string,
		path: string,
		operation: string,
		parameters: JsonObject
	): JsonObject => ({
		schemaVersion: 1,
		name: `orders ${operation} ${stamp}`,
		nodes: [
			webhookNode(minted.nodeId, { path, httpMethod: method }),
			specNode('rows', 'Data table', 'kilasflow.datastore', {
				resource: 'row',
				operation,
				dataTableId: locator,
				...parameters
			}),
			{
				...specNode('respond', 'Respond', 'kilasflow.respondToWebhook', {
					respondWith: 'firstIncomingItem',
					responseCode: 200
				}),
				// A read that matches nothing delivers zero items, which would
				// prune the Respond node and leave the caller with an empty 200
				// body. An API endpoint answers JSON either way, so the node
				// runs on the empty delivery and its executor substitutes the
				// empty item.
				settings: { alwaysOutputData: true }
			}
		],
		connections: [specConn('c1', minted.nodeId, 'rows'), specConn('c2', 'rows', 'respond')],
		settings: {}
	});

	const marker = (value: string): JsonObject => ({ mode: 'expression', value });
	const shaped: Array<[MintedWebhookWorkflow, JsonObject]> = [
		[
			create,
			shape(create, 'POST', `orders-create-${stamp}`, 'insert', {
				columns: manualColumns({
					sku: marker('{{ $json.body.sku }}'),
					qty: marker('{{ $json.body.qty }}')
				})
			})
		],
		[
			get,
			shape(get, 'GET', `orders-get-${stamp}`, 'get', {
				filters: {
					conditions: [conditionRow('sku', 'eq', marker('{{ $json.body.sku ?? $json.query.sku }}'))]
				},
				match: 'any',
				returnAll: true
			})
		],
		[
			update,
			shape(update, 'PUT', `orders-update-${stamp}`, 'update', {
				filters: { conditions: [conditionRow('sku', 'eq', marker('{{ $json.body.sku }}'))] },
				match: 'any',
				columns: manualColumns({ qty: marker('{{ $json.body.qty }}') })
			})
		],
		[
			remove,
			shape(remove, 'DELETE', `orders-delete-${stamp}`, 'delete', {
				filters: { conditions: [conditionRow('sku', 'eq', marker('{{ $json.body.sku }}'))] },
				match: 'any'
			})
		]
	];
	for (const [minted, document] of shaped) {
		await liveApi<WorkflowResource>(baseURL, 'PUT', `/workflows/${minted.workflowId}`, document);
		const activated = await liveApi<WorkflowResource>(baseURL, 'POST', `/workflows/${minted.workflowId}/activate`);
		expect(activated.active).toBe(true);
	}

	const call = async (
		minted: MintedWebhookWorkflow,
		delivery: DeliveryOptions,
		suffix = ''
	): Promise<{ body: JsonObject; record: ExecutionRecord }> => {
		const before = await executionIds(baseURL, minted.workflowId);
		const response = await deliver(baseURL, minted.url + suffix, delivery);
		expect(response.status, `delivery to ${minted.url}${suffix}: ${response.text}`).toBe(200);
		const record = await waitForTriggerExecution(baseURL, minted.workflowId, 'webhook', before);
		return { body: jsonObject(response.json, `response from ${minted.url}`), record };
	};

	// create -> get: the row lands with the id the caller receives.
	const inserted = (await call(create, { json: { sku, qty: 2 } })).body;
	expect(inserted.qty).toBe(2);
	expect(typeof inserted.id).toBe('number');

	const firstRead = await call(get, { method: 'GET' }, `?sku=${encodeURIComponent(sku)}`);
	expect(firstRead.body.qty).toBe(2);

	// The trace keeps the row's identity and never its cells: GET /executions
	// projects a datastore node's output to counts and ids.
	const rowsRun = firstRead.record.nodeRuns?.find((run) => run.nodeId === 'rows');
	expect(rowsRun?.status).toBe('succeeded');
	const summary = jsonObject(jsonObject(rowsRun?.output, 'datastore nodeRun output').datastore, 'datastore summary');
	expect(summary.rows).toBe(1);
	expect(Array.isArray(summary.ids)).toBe(true);
	expect(summary.ids).toContain(inserted.id);
	expect(JSON.stringify(rowsRun?.output)).not.toContain(sku);

	// update -> get: the new quantity is what a filtered read returns.
	await call(update, { method: 'PUT', json: { sku, qty: 5 } });
	const secondRead = await call(get, { method: 'GET' }, `?sku=${encodeURIComponent(sku)}`);
	expect(secondRead.body.qty).toBe(5);

	// delete -> get: nothing matches, and the Respond node answers with the
	// empty item rather than leaving the caller without an answer.
	await call(remove, { method: 'DELETE', json: { sku } });
	const thirdRead = await call(get, { method: 'GET' }, `?sku=${encodeURIComponent(sku)}`);
	expect(thirdRead.body).toEqual({});
});

test('the builder lifecycle: conflict, versions, restore, publish, delete, paging', async ({ server }) => {
	const baseURL = server.baseURL;
	const name = uniqueName('live api lifecycle');
	const document = simpleApiDocument(name, `lifecycle-${randomBytes(3).toString('hex')}`);

	const created = await liveApi<WorkflowResource>(baseURL, 'POST', '/workflows', document, 201);
	const v1 = created.latestVersion.id;

	const evolved = structuredClone(document);
	evolved.name = `${name} edited`;
	const v2 = await liveApi<WorkflowResource>(baseURL, 'PUT', `/workflows/${created.id}`, evolved);
	expect(v2.latestVersion.revision).toBe(2);

	// A save carrying a stale base version is refused, not merged.
	const staleBody = { ...structuredClone(evolved), baseVersionId: v1 };
	await liveApi(baseURL, 'PUT', `/workflows/${created.id}`, staleBody, 409);

	// Two more workflows, so the list has a page boundary to page across.
	for (let index = 0; index < 2; index += 1) {
		await liveApi(
			baseURL,
			'POST',
			'/workflows',
			{
				schemaVersion: 1,
				name: `${name} paging ${index}`,
				nodes: [specNode('manual', 'Manual Trigger', 'kilasflow.manual')],
				connections: [],
				settings: {}
			},
			201
		);
	}

	const versions = await liveApi<{ items: Array<{ id: string; revision: number }> }>(
		baseURL,
		'GET',
		`/workflows/${created.id}/versions`
	);
	expect(versions.items.length).toBeGreaterThanOrEqual(2);

	// Restore appends a new revision carrying the old snapshot; history itself
	// is untouched.
	const restored = await liveApi<WorkflowResource>(
		baseURL,
		'POST',
		`/workflows/${created.id}/versions/${v1}/restore`,
		{ reason: 'undoing the bad save' }
	);
	expect(restored.latestVersion.revision).toBe(3);

	const published = await liveApi<WorkflowResource>(
		baseURL,
		'POST',
		`/workflows/${created.id}/versions/${v1}/publish`,
		{ reason: 'rolling back' }
	);
	expect(published.active).toBe(true);

	const publishEvents = await liveApi<Array<{ action: string }>>(
		baseURL,
		'GET',
		`/workflows/${created.id}/publish-events`
	);
	const actions = publishEvents.map((event) => event.action);
	expect(actions).toContain('published');
	expect(actions).toContain('restored');

	// GET /workflows pages by cursor: the first page is a bare array and the
	// cursor rides in X-Next-Cursor.
	const firstPage = await fetch(`${baseURL}/api/v1/workflows?limit=2`);
	expect(firstPage.status).toBe(200);
	const cursor = firstPage.headers.get('x-next-cursor');
	expect(cursor).toBeTruthy();
	const firstItems = (await firstPage.json()) as Array<{ id: string }>;
	const secondPage = await liveApi<Array<{ id: string }>>(
		baseURL,
		'GET',
		`/workflows?limit=2&cursor=${encodeURIComponent(cursor!)}`
	);
	expect(secondPage.length).toBeGreaterThan(0);
	const firstIds = new Set(firstItems.map((item) => item.id));
	expect(secondPage.some((item) => firstIds.has(item.id))).toBe(false);

	await liveApi(baseURL, 'DELETE', `/workflows/${created.id}`, undefined, 204);
	await liveApi(baseURL, 'GET', `/workflows/${created.id}`, undefined, 404);
});
