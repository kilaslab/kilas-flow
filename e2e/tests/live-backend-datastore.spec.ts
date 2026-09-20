// Live datastore suite (EPIC-87t47t, FEAT-ds4e0m): the datastore REST surface
// (tables, columns, rows, filters, paging, CSV), the same rows reached through
// a workflow's datastore node, and — gated on a live server — an external
// PostgreSQL reached from a workflow through a postgres credential.
//
// The internal half always runs on the harness's SQLite; the external half
// skips by name when no DSN is configured (the CI e2e job sets
// KILASFLOW_E2E_POSTGRES_DSN, where the skip is forbidden rather than accepted).
import { test, expect } from '../fixtures';
import { startServer } from '../helpers/server';
import { createCredential, runWorkflow, waitForExecution } from '../helpers/seed';
import {
	addColumn,
	apiJson,
	apiStatus,
	createDatastore,
	createWorkflowDocument,
	datastoreAutoMapDocument,
	datastoreWriteReadDocument,
	exportCsv,
	getDatastore,
	importCsv,
	listRows,
	n8nStyleCsv,
	nodeRun,
	pgDsn,
	pgSkipReason,
	renameDatastore,
	startWorkflowRun,
	uniqueName,
	type DatastoreResource
} from '../fixtures/datastore';
import { pgCredentialFields } from '../fixtures/live-backend';

type JsonObject = Record<string, unknown>;

function specNode(id: string, name: string, type: string, parameters?: JsonObject): JsonObject {
	return { id, name, type, typeVersion: 1, position: { x: 0, y: 0 }, ...(parameters ? { parameters } : {}) };
}

function specConn(id: string, source: string, target: string): JsonObject {
	return { id, kind: 'main', source: { nodeId: source, port: 'main' }, target: { nodeId: target, port: 'main' } };
}

function rowFilter(columnName: string, value: unknown): JsonObject {
	return { type: 'and', filters: [{ columnName, condition: 'eq', value }] };
}

function jsonObject(value: unknown, what: string): JsonObject {
	if (typeof value !== 'object' || value === null || Array.isArray(value)) {
		throw new Error(`${what}: expected a JSON object, got ${JSON.stringify(value)}`);
	}
	return value as JsonObject;
}


test('the datastore REST surface serves tables, columns, rows, filters, paging and CSV', async ({ server }) => {
	const baseURL = server.baseURL;
	const name = uniqueName('live datastore');

	// create: 201 and a Location naming the new table.
	const created = await fetch(`${baseURL}/api/v1/datastores`, {
		method: 'POST',
		headers: { 'content-type': 'application/json' },
		body: JSON.stringify({ name })
	});
	expect(created.status).toBe(201);
	const store = (await created.json()) as DatastoreResource;
	expect(created.headers.get('location')).toBe(`/api/v1/datastores/${store.id}`);

	const renamed = await renameDatastore(baseURL, store.id, `${name} renamed`);
	expect(renamed.name).toBe(`${name} renamed`);

	// columns: add, rename, drop — the schema half of the CRUD surface.
	await addColumn(baseURL, store.id, 'sku', 'string');
	await addColumn(baseURL, store.id, 'qty', 'number');
	await addColumn(baseURL, store.id, 'scratch', 'string');
	const renamedColumn = await apiJson(
		baseURL,
		'PUT',
		`/datastores/${store.id}/columns/scratch`,
		{ name: 'scratch2' }
	);
	expect(renamedColumn.name).toBe('scratch2');
	await apiJson(baseURL, 'DELETE', `/datastores/${store.id}/columns/scratch2`, undefined, 204);
	const afterDrop = await getDatastore(baseURL, store.id);
	expect(afterDrop.columns.map((column) => column.name)).toEqual(['sku', 'qty']);

	// insert, then read the row back by id.
	const row = await apiJson(baseURL, 'POST', `/datastores/${store.id}/rows`, {
		values: { sku: 'SKU-1', qty: 2 }
	}, 201);
	expect(typeof row.id).toBe('number');
	const fetched = await apiJson(baseURL, 'GET', `/datastores/${store.id}/rows/${row.id}`);
	expect(fetched.qty).toBe(2);

	// a filtered update touches exactly the matching row.
	const updated = await apiJson(baseURL, 'PUT', `/datastores/${store.id}/rows`, {
		filter: rowFilter('sku', 'SKU-1'),
		values: { qty: 9 }
	});
	expect(updated.matched).toBe(1);

	// upsert inserts on no match and updates on a match.
	const upserted = await apiJson(
		baseURL,
		'POST',
		`/datastores/${store.id}/rows/upsert`,
		{ filter: rowFilter('sku', 'SKU-2'), values: { sku: 'SKU-2', qty: 1 } }
	);
	expect(upserted.inserted).toBe(true);
	const upsertMatched = await apiJson(
		baseURL,
		'POST',
		`/datastores/${store.id}/rows/upsert`,
		{ filter: rowFilter('sku', 'SKU-2'), values: { qty: 7 } }
	);
	expect(upsertMatched.inserted).toBe(false);
	expect(upsertMatched.matched).toBe(1);

	// a filtered delete removes the row it names.
	const removed = await apiJson(baseURL, 'DELETE', `/datastores/${store.id}/rows`, {
		filter: rowFilter('sku', 'SKU-1')
	});
	expect(removed.deleted).toBe(1);

	// an empty filter is refused, and refusing it removes nothing.
	const refused = await apiStatus(baseURL, 'DELETE', `/datastores/${store.id}/rows`, {
		filter: { type: 'and', filters: [] }
	});
	expect(refused.status).toBe(422);
	const remaining = await listRows(baseURL, store.id);
	expect(remaining.items.length).toBe(1);
	expect(remaining.items[0].sku).toBe('SKU-2');

	// clear empties the table and keeps its schema.
	const cleared = await apiJson(baseURL, 'POST', `/datastores/${store.id}/clear`);
	expect(cleared.deleted).toBe(1);
	const emptied = await getDatastore(baseURL, store.id);
	expect(emptied.columns.map((column) => column.name)).toEqual(['sku', 'qty']);
	expect((await listRows(baseURL, store.id)).items.length).toBe(0);

	// rows page by keyset cursor: the second page continues where the first
	// stopped and never repeats a row.
	for (const index of [1, 2, 3]) {
		await apiJson(baseURL, 'POST', `/datastores/${store.id}/rows`, { values: { sku: `SKU-P${index}`, qty: index } }, 201);
	}
	const firstPage = await apiJson(
		baseURL,
		'GET',
		`/datastores/${store.id}/rows?limit=2`
	);
	expect(firstPage.items.length).toBe(2);
	expect(firstPage.nextCursor).toBeTruthy();
	const secondPage = await apiJson(
		baseURL,
		'GET',
		`/datastores/${store.id}/rows?limit=2&cursor=${encodeURIComponent(firstPage.nextCursor!)}`
	);
	expect(secondPage.items.length).toBe(1);
	const firstIds = new Set<number>((firstPage.items as Array<{ id: number }>).map((item) => item.id));
	expect((secondPage.items as Array<{ id: number }>).some((item) => firstIds.has(item.id))).toBe(false);

	// CSV: the sheet imports, exports, and re-imports byte-identical.
	const sheet = await createDatastore(baseURL, uniqueName('live datastore csv'));
	await addColumn(baseURL, sheet.id, 'email', 'string');
	await addColumn(baseURL, sheet.id, 'score', 'number');
	const report = await importCsv(baseURL, sheet.id, n8nStyleCsv());
	expect(report.inserted).toBe(2);
	expect(report.failed).toEqual([]);
	const exported = await exportCsv(baseURL, sheet.id);
	const fresh = await createDatastore(baseURL, uniqueName('live datastore csv fresh'));
	await addColumn(baseURL, fresh.id, 'email', 'string');
	await addColumn(baseURL, fresh.id, 'score', 'number');
	const reImported = await importCsv(baseURL, fresh.id, exported);
	expect(reImported.inserted).toBe(2);
	expect(await exportCsv(baseURL, fresh.id)).toBe(exported);

	// delete: 204, and the table is gone from every read.
	await apiJson(baseURL, 'DELETE', `/datastores/${store.id}`, undefined, 204);
	const gone = await apiStatus(baseURL, 'GET', `/datastores/${store.id}`);
	expect(gone.status).toBe(404);
});

test('a workflow node writes and reads the same rows the REST surface serves', async ({ server }) => {
	const baseURL = server.baseURL;
	const store = await createDatastore(baseURL, uniqueName('live datastore node'));
	await addColumn(baseURL, store.id, 'sku', 'string');
	await addColumn(baseURL, store.id, 'qty', 'number');

	// Manual -> insert(defineBelow) -> get(filtered read): the filtered read
	// proving itself means the read node's output carries the inserted row.
	const workflow = await createWorkflowDocument(
		baseURL,
		datastoreWriteReadDocument(uniqueName('live node write'), store.id, { sku: 'SKU-N1', qty: 3 }, 'sku', 'SKU-N1')
	);
	const run = await startWorkflowRun(baseURL, workflow.id);
	expect(run.status).toBe(202);
	const record = await waitForExecution(baseURL, run.body.id);
	expect(record.status).toBe('succeeded');
	// The trace of a datastore node is redacted to counts and ids, so the
	// filtered read proves itself by returning exactly the row the insert
	// wrote — the same identifier on both sides.
	const insertRun = nodeRun(record, 'insert');
	expect(insertRun.status).toBe('succeeded');
	const read = nodeRun(record, 'read');
	expect(read.status).toBe('succeeded');
	const insertedSummary = jsonObject(jsonObject(insertRun.output, 'insert trace').datastore, 'insert summary');
	const readSummary = jsonObject(jsonObject(read.output, 'read trace').datastore, 'read summary');
	expect(insertedSummary.rows).toBe(1);
	expect(readSummary.rows).toBe(1);
	expect(readSummary.ids).toEqual(insertedSummary.ids);

	// The same row is what the REST surface serves under the same filter.
	const filtered = await apiJson(
		baseURL,
		'GET',
		`/datastores/${store.id}/rows?columnName=sku&condition=eq&value=SKU-N1`
	);
	expect(filtered.items).toHaveLength(1);
	expect(filtered.items[0].qty).toBe(3);

	// Manual -> Set -> insert(autoMapInputData): only fields matching the live
	// schema land; the unmatched one is dropped.
	const autoWorkflow = await createWorkflowDocument(
		baseURL,
		datastoreAutoMapDocument(uniqueName('live node automap'), store.id, {
			sku: 'SKU-N2',
			qty: 4,
			unmapped: 'dropped'
		})
	);
	const autoRun = await startWorkflowRun(baseURL, autoWorkflow.id);
	expect(autoRun.status).toBe(202);
	const autoRecord = await waitForExecution(baseURL, autoRun.body.id);
	expect(autoRecord.status).toBe('succeeded');

	const rows = await listRows(baseURL, store.id);
	const items = rows.items as Array<{ sku: string; unmapped?: unknown }>;
	expect(items.map((item) => item.sku)).toContain('SKU-N1');
	expect(items.map((item) => item.sku)).toContain('SKU-N2');
	expect(items.every((item) => item.unmapped === undefined)).toBe(true);
});

test('a workflow reaches an external postgres through its credential', async () => {
	const dsn = pgDsn();
	test.skip(!dsn, pgSkipReason());
	const fields = pgCredentialFields(dsn!);

	// The harness never turns allow_private_networks on; the database endpoint
	// is admitted by name, exactly like the loopback stub is.
	const server = await startServer({ stubEndpoint: `${fields.host}:${fields.port}` });
	try {
		const credential = await createCredential(server.baseURL, {
			name: uniqueName('live postgres'),
			type: 'postgres',
			fields
		});
		const document: JsonObject = {
			schemaVersion: 1,
			name: uniqueName('live postgres query'),
			nodes: [
				specNode('manual', 'Manual Trigger', 'kilasflow.manual'),
				specNode('sql', 'SQL', 'kilasflow.postgres', {
					operation: 'query',
					statement: 'SELECT 1 AS ok'
				})
			],
			connections: [specConn('c1', 'manual', 'sql')],
			settings: {}
		};
		(document.nodes as JsonObject[])[1].credentials = { postgres: credential.id };

		const workflow = await createWorkflowDocument(server.baseURL, document);
		const executionId = await runWorkflow(server.baseURL, workflow.id);
		const record = await waitForExecution(server.baseURL, executionId);
		expect(record.status).toBe('succeeded');
		expect(JSON.stringify(record.output)).toContain('"ok":1');
	} finally {
		await server.close();
	}
});
