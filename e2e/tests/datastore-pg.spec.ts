// FEAT-cpdp8y postgres suite: the same Datastore path against the Postgres
// driver. Gated on a live pgvector server: without an explicit DSN the suite
// skips honestly instead of passing silently.
//
//   KILASFLOW_TEST_POSTGRES_DSN (or KILASFLOW_E2E_POSTGRES_DSN)=postgres://kilas:hunter2@127.0.0.1:55434/kilasflow?sslmode=disable pnpm test datastore-pg
//
// The binary is the same artifact global-setup builds (`make build-all`);
// only the database driver differs (KILASFLOW_DATABASE_DRIVER=postgres).
// Auth stays off, so each instance is its own default tenant in the shared
// Postgres database; tests isolate by unique datastore/workflow names and
// ID-scoped assertions, never by assuming an empty catalogue. No fixed
// sleeps: executions are awaited with expect.poll, UI with assertions.
import { test, expect } from '@playwright/test';

import { startServer } from '../helpers/server';
import { startStub } from '../helpers/stub';
import { runWorkflow, waitForExecution } from '../helpers/seed';
import {
	addColumn,
	createDatastore,
	createWorkflowDocument,
	datastoreLocator,
	datastoreWriteReadDocument,
	exportCsv,
	getDatastore,
	importCsv,
	listDatastores,
	listRows,
	n8nStyleCsv,
	pgDsn,
	pgSkipReason,
	startPostgresServer,
	uniqueName
} from '../fixtures/datastore';

test('the datastore path holds on postgres: create, verbatim columns, node writes, CSV round trip', async ({
	page
}) => {
	const dsn = pgDsn();
	test.skip(!dsn, pgSkipReason());
	const stub = await startStub();
	const pg = await startPostgresServer(dsn!, {
		stubEndpoint: `127.0.0.1:${stub.port}`,
		allowedOrigin: stub.origin
	});
	try {
		// Name-only creation through the editor, against the postgres
		// instance: the same dialog, the same SPA, a different driver.
		const name = uniqueName('E2E PG Datastore');
		await page.goto(`${pg.baseURL}/datastores`);
		await expect(page.locator('[data-dashboard-shell]')).toBeVisible();
		await page.getByRole('button', { name: 'New datastore' }).click();
		await page.locator('#datastore-name').fill(name);
		await page.getByRole('button', { name: 'Create datastore' }).click();
		await expect(page.getByRole('link', { name })).toBeVisible();

		const created = (await listDatastores(pg.baseURL)).find((entry) => entry.name === name);
		expect(created, 'postgres datastore is listed').toBeDefined();

		// Verbatim n8n types, including the datetime alias the wire stores
		// as date (shot 22). Typed filters must return the same rows on
		// both drivers, so the numeric write below is read back by filter.
		await addColumn(pg.baseURL, created!.id, 'email', 'string');
		await addColumn(pg.baseURL, created!.id, 'score', 'number');
		await addColumn(pg.baseURL, created!.id, 'active', 'boolean');
		await addColumn(pg.baseURL, created!.id, 'seen', 'datetime');
		const detail = await getDatastore(pg.baseURL, created!.id);
		const byName = new Map(detail.columns.map((column) => [column.name, column.type]));
		expect(byName.get('email')).toBe('string');
		expect(byName.get('score')).toBe('number');
		expect(byName.get('active')).toBe('boolean');
		expect(byName.get('seen')).toBe('date');

		// A workflow node writes rows and reads them with a filter; the
		// results are observed through the execution record.
		const email = `pg-${Date.now()}@example.com`;
		const document = datastoreWriteReadDocument(
			uniqueName('E2E PG Writer'),
			created!.id,
			{ email, score: 93, active: true },
			'email',
			email
		);
		const workflow = await createWorkflowDocument(pg.baseURL, document);
		const executionId = await runWorkflow(pg.baseURL, workflow.id);
		const record = await waitForExecution(pg.baseURL, executionId);
		expect(record.status).toBe('succeeded');
		const runs = record.nodeRuns as unknown as Array<{ nodeId: string; status: string; output: { datastore?: { rows: number } } }>;
		const insert = runs.find((entry) => entry.nodeId === 'insert');
		const read = runs.find((entry) => entry.nodeId === 'read');
		expect(insert?.status).toBe('succeeded');
		expect(read?.status).toBe('succeeded');
		expect(insert?.output?.datastore?.rows).toBe(1);
		expect(read?.output?.datastore?.rows).toBe(1);
		expect(JSON.stringify(read?.output)).not.toContain(email);

		// Numeric filters coerce against the stored column type: the same
		// rows return on SQLite and PostgreSQL instead of an empty grid.
		const filtered = await (
			await fetch(`${pg.baseURL}/api/v1/datastores/${created!.id}/rows?limit=50&columnName=score&condition=eq&value=93`)
		).json();
		expect((filtered.items as unknown as Array<{ email: string }>).some((row) => row.email === email)).toBe(true);

		// CSV round trip byte-identical on postgres too.
		const exported = await exportCsv(pg.baseURL, created!.id);
		expect(exported.split('\n')[0].trim()).toContain('email');
		const fresh = await createDatastore(pg.baseURL, uniqueName('E2E PG CSV Fresh'));
		await addColumn(pg.baseURL, fresh.id, 'email', 'string');
		await addColumn(pg.baseURL, fresh.id, 'score', 'number');
		await addColumn(pg.baseURL, fresh.id, 'active', 'boolean');
		await addColumn(pg.baseURL, fresh.id, 'seen', 'datetime');
		const before = await exportCsv(pg.baseURL, fresh.id);
		expect(before.trim().split('\n').length).toBe(1);
		const csvOnly = n8nStyleCsv();
		const csvStore = await createDatastore(pg.baseURL, uniqueName('E2E PG CSV Only'));
		await addColumn(pg.baseURL, csvStore.id, 'email', 'string');
		await addColumn(pg.baseURL, csvStore.id, 'score', 'number');
		const report = await importCsv(pg.baseURL, csvStore.id, csvOnly);
		expect(report.inserted).toBe(2);
		const reExported = await exportCsv(pg.baseURL, csvStore.id);
		expect(reExported).toBe(csvOnly);

		// The editor grid shows the workflow's row on postgres.
		await page.goto(`${pg.baseURL}/datastores/${created!.id}`);
		await expect(page.getByRole('cell', { name: email })).toBeVisible();
	} finally {
		await pg.close();
		await stub.close();
	}
});

test('a second tenant reads nothing on postgres (404, indistinguishable)', async () => {
	const dsn = pgDsn();
	test.skip(!dsn, pgSkipReason());
	const stub = await startStub();
	const tenantA = await startPostgresServer(dsn!, {
		stubEndpoint: `127.0.0.1:${stub.port}`,
		allowedOrigin: stub.origin
	});
	// Tenant B is a separate sqlite instance with its own database: the
	// strongest isolation the harness offers (auth off means each instance
	// is its own default tenant). The victim id lives on postgres and must
	// read as unknown elsewhere; same-DB multi-tenancy is pinned by the Go
	// fixedTenant suites, this proves the cross-instance 404 surface on the
	// postgres driver.
	const tenantB = await startServer({
		stubEndpoint: `127.0.0.1:${stub.port}`,
		allowedOrigin: stub.origin
	});
	try {
		const store = await createDatastore(tenantA.baseURL, uniqueName('E2E PG Tenant A'));
		await addColumn(tenantA.baseURL, store.id, 'email', 'string');
		await importCsv(tenantA.baseURL, store.id, 'email\nvictim@example.com\n');

		const missingId = 'datastore_00000000-0000-7000-8000-000000000000';
		const victimOnB = await fetch(`${tenantB.baseURL}/api/v1/datastores/${store.id}`);
		const missingOnB = await fetch(`${tenantB.baseURL}/api/v1/datastores/${missingId}`);
		expect(victimOnB.status).toBe(404);
		expect(missingOnB.status).toBe(404);
		expect(await victimOnB.text()).toContain('not found');
		expect(await missingOnB.text()).toContain('not found');

		const rowsOnB = await fetch(`${tenantB.baseURL}/api/v1/datastores/${store.id}/rows?limit=20`);
		expect(rowsOnB.status).toBe(404);

		const writeOnB = await fetch(`${tenantB.baseURL}/api/v1/datastores/${store.id}/rows`, {
			method: 'POST',
			headers: { 'content-type': 'application/json' },
			body: JSON.stringify({ values: { email: 'intruder@example.com' } })
		});
		expect(writeOnB.status).toBe(404);

		const listed = (await listDatastores(tenantB.baseURL)).map((entry) => entry.id);
		expect(listed).not.toContain(store.id);

		// The node path is confined too: tenant B naming tenant A's table
		// fails with unknown-datastore and leaks no rows.
		const document = datastoreWriteReadDocument(
			uniqueName('E2E PG Tenant B Probe'),
			store.id,
			{ email: 'intruder@example.com' },
			'email',
			'intruder@example.com'
		);
		const probe = await createWorkflowDocument(tenantB.baseURL, document);
		const started = await fetch(`${tenantB.baseURL}/api/v1/workflows/${probe.id}/run`, {
			method: 'POST',
			headers: { 'content-type': 'application/json' },
			body: '{}'
		});
		expect(started.status).toBe(202);
		const record = await waitForExecution(tenantB.baseURL, (await started.json()).id);
		expect(record.status).toBe('failed');
		expect(JSON.stringify(record)).toContain('unknown datastore');
	} finally {
		await tenantB.close();
		await tenantA.close();
		await stub.close();
	}
});

test('rename-table holds on postgres via API and node', async () => {
	const dsn = pgDsn();
	test.skip(!dsn, pgSkipReason());
	const stub = await startStub();
	const pg = await startPostgresServer(dsn!, {
		stubEndpoint: `127.0.0.1:${stub.port}`,
		allowedOrigin: stub.origin
	});
	try {
		const store = await createDatastore(pg.baseURL, uniqueName('E2E PG Before Rename'));
		const renamed = uniqueName('E2E PG After Rename');
		const renamedBack = uniqueName('E2E PG Node Renamed');

		const viaApi = await (
			await fetch(`${pg.baseURL}/api/v1/datastores/${store.id}`, {
				method: 'PUT',
				headers: { 'content-type': 'application/json' },
				body: JSON.stringify({ name: renamed })
			})
		).json();
		expect(viaApi.name).toBe(renamed);

		const document = {
			schemaVersion: 1,
			name: uniqueName('E2E PG Rename Writer'),
			nodes: [
				{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
				{
					id: 'rename',
					name: 'Rename',
					type: 'kilasflow.datastore',
					typeVersion: 1,
					position: { x: 240, y: 0 },
					parameters: {
						resource: 'table',
						operation: 'rename',
						dataTableId: datastoreLocator('id', store.id),
						name: renamedBack
					}
				}
			],
			connections: [
				{ id: 'c1', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'rename', port: 'main' } }
			],
			settings: {}
		};
		const workflow = await createWorkflowDocument(pg.baseURL, document);
		const executionId = await runWorkflow(pg.baseURL, workflow.id);
		const record = await waitForExecution(pg.baseURL, executionId);
		expect(record.status).toBe('succeeded');
		expect((await getDatastore(pg.baseURL, store.id)).name).toBe(renamedBack);
	} finally {
		await pg.close();
		await stub.close();
	}
});
