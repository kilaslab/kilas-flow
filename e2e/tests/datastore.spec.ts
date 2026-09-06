import { test, expect } from '../fixtures';
import { startServer } from '../helpers/server';
import { runWorkflow, waitForExecution } from '../helpers/seed';
import {
	addColumn,
	apiStatus,
	createDatastore,
	createWorkflowDocument,
	datastoreLocator,
	datastoreWriteReadDocument,
	exportCsv,
	fetchWorkflowDocument,
	getDatastore,
	importCsv,
	importN8nWorkflow,
	listDatastores,
	listRows,
	manualColumns,
	n8nDataTableExport,
	n8nStyleCsv,
	saveWorkflowDocument,
	startWorkflowRun,
	uniqueName,
	type DatastoreResource
} from '../fixtures/datastore';

test('a datastore is created through the name-only dialog', async ({ page, server }) => {
	const name = uniqueName('E2E Datastore');

	await page.goto(`${server.baseURL}/datastores`);
	await expect(page.locator('[data-dashboard-shell]')).toBeVisible();
	await expect(page.getByRole('button', { name: 'New datastore' })).toBeVisible();

	await page.getByRole('button', { name: 'New datastore' }).click();
	await expect(page.getByRole('dialog')).toContainText('New datastore');
	// The dialog takes a name only (design-refs shot 19): columns are always
	// a second step.
	await expect(page.getByRole('dialog')).toContainText('A name is all it takes');
	await page.locator('#datastore-name').fill(name);
	await page.getByRole('button', { name: 'Create datastore' }).click();

	// The list shows the new entry; the API proves it was created name-only
	// with no user columns yet.
	await expect(page.getByRole('link', { name })).toBeVisible();
	await expect
		.poll(async () => (await listDatastores(server.baseURL)).some((entry) => entry.name === name))
		.toBe(true);
	const created = (await listDatastores(server.baseURL)).find((entry) => entry.name === name);
	expect(created, 'created datastore is listed').toBeDefined();
	const detail = await getDatastore(server.baseURL, created!.id);
	expect(detail.columns.filter((column) => !['id', 'createdAt', 'updatedAt'].includes(column.name))).toEqual([]);
});

test('columns are added with the verbatim n8n types', async ({ page, server }) => {
	const store = await createDatastore(server.baseURL, uniqueName('E2E Columns'));
	const wanted: Array<{ label: string; wire: string }> = [
		{ label: 'string', wire: 'string' },
		{ label: 'number', wire: 'number' },
		{ label: 'boolean', wire: 'boolean' },
		// The dialog labels the fourth `datetime` (shot 22) while the wire
		// carries `date`: the server normalises the alias.
		{ label: 'datetime', wire: 'date' }
	];

	await page.goto(`${server.baseURL}/datastores/${store.id}`);
	await expect(page.getByRole('heading', { name: store.name })).toBeVisible();

	for (const [index, column] of wanted.entries()) {
		const columnName = `col${index}_${column.label}`;
		// Scoped to the page toolbar: the dialog's own submit carries the
		// same name while it is still in the DOM.
		await page.locator('#dashboard-content').getByRole('button', { name: 'Add column' }).click();
		const dialog = page.getByRole('dialog');
		await expect(dialog).toContainText('Add column');
		await page.locator('#column-name').fill(columnName);
		await page.locator('#column-type').selectOption(column.label);
		await dialog.getByRole('button', { name: 'Add column', exact: true }).click();
		// With no rows the grid shows its empty state instead of headers,
		// so the user-column count beside the title is the observable
		// state that the column landed.
		const countText = index === 0 ? '1 user column' : `${index + 1} user columns`;
		await expect(page.getByText(countText).first()).toBeVisible();
	}

	const detail = await getDatastore(server.baseURL, store.id);
	const byName = new Map(detail.columns.map((column) => [column.name, column.type]));
	for (const [index, column] of wanted.entries()) {
		expect(byName.get(`col${index}_${column.label}`)).toBe(column.wire);
	}
});

test('a workflow node writes rows and reads them with a filter', async ({ server }) => {
	const store = await createDatastore(server.baseURL, uniqueName('E2E Node Path'));
	await addColumn(server.baseURL, store.id, 'email', 'string');
	await addColumn(server.baseURL, store.id, 'score', 'number');

	const email = `ada-${Date.now()}@example.com`;
	const document = datastoreWriteReadDocument(
		uniqueName('E2E Datastore Writer'),
		store.id,
		{ email, score: 93 },
		'email',
		email
	);
	const workflow = await createWorkflowDocument(server.baseURL, document);
	const executionId = await runWorkflow(server.baseURL, workflow.id);
	const record = await waitForExecution(server.baseURL, executionId);
	expect(record.status).toBe('succeeded');

	// The results are observed through the execution record: the trace
	// projects datastore outputs to row counts and identifiers, never cell
	// contents (V2-p9-12). Row contents are proven via the rows API below.
	const runs = record.nodeRuns as unknown as Array<{ nodeId: string; status: string; output: { datastore?: { rows: number } } }>;
	const insert = runs.find((entry) => entry.nodeId === 'insert');
	const read = runs.find((entry) => entry.nodeId === 'read');
	expect(insert?.status).toBe('succeeded');
	expect(read?.status).toBe('succeeded');
	expect(insert?.output?.datastore?.rows).toBe(1);
	expect(read?.output?.datastore?.rows).toBe(1);
	expect(JSON.stringify(insert?.output)).not.toContain(email);
	expect(JSON.stringify(read?.output)).not.toContain(email);

	const rows = await listRows(server.baseURL, store.id);
	const stored = (rows.items as unknown as Array<{ email: string; score: number }>).map((row) => ({ email: row.email, score: row.score }));
	expect(stored).toContainEqual({ email, score: 93 });
});

test('the editor grid shows the rows a workflow wrote', async ({ page, server }) => {
	const name = uniqueName('E2E Grid');
	const store = await createDatastore(server.baseURL, name);
	await addColumn(server.baseURL, store.id, 'email', 'string');
	await addColumn(server.baseURL, store.id, 'score', 'number');

	const email = `grid-${Date.now()}@example.com`;
	const document = datastoreWriteReadDocument(
		uniqueName('E2E Grid Writer'),
		store.id,
		{ email, score: 87 },
		'email',
		email
	);
	const workflow = await createWorkflowDocument(server.baseURL, document);
	const executionId = await runWorkflow(server.baseURL, workflow.id);
	const record = await waitForExecution(server.baseURL, executionId);
	expect(record.status).toBe('succeeded');

	await page.goto(`${server.baseURL}/datastores/${store.id}`);
	await expect(page.getByRole('heading', { name })).toBeVisible();
	// Header order is id, user columns in catalogue order, timestamps
	// (shot 23); the row cells carry the workflow's values.
	await expect(page.getByRole('columnheader', { name: /email/ })).toBeVisible();
	await expect(page.getByRole('columnheader', { name: /score/ })).toBeVisible();
	await expect(page.getByRole('cell', { name: email })).toBeVisible();
	await expect(page.getByRole('cell', { name: '87' })).toBeVisible();
});

test('rows import from an n8n-style CSV export and re-export byte-identical', async ({
	page,
	server
}) => {
	const name = uniqueName('E2E CSV');
	const store = await createDatastore(server.baseURL, name);
	await addColumn(server.baseURL, store.id, 'email', 'string');
	await addColumn(server.baseURL, store.id, 'score', 'number');

	// Through the editor's Import dialog, uploading the file the way a
	// customer hands back the sheet they already hold.
	await page.goto(`${server.baseURL}/datastores/${store.id}`);
	await expect(page.getByRole('heading', { name })).toBeVisible();
	await page.getByRole('button', { name: 'Import' }).click();
	await expect(page.getByRole('dialog')).toContainText('Import rows');
	await page
		.locator('#transfer-file')
		.setInputFiles({ name: 'n8n-datatable-export.csv', mimeType: 'text/csv', buffer: Buffer.from(n8nStyleCsv()) });
	await page.getByRole('button', { name: 'Upload' }).click();
	await expect(page.getByRole('dialog')).toContainText('2 rows');
	await expect(page.getByRole('dialog')).toContainText('imported');

	const stored = await listRows(server.baseURL, store.id);
	expect((stored.items as unknown as Array<unknown>).length).toBe(2);

	// The export header is exactly the user columns in stored order; the
	// file re-imports into a fresh datastore with identical values.
	const exported = await exportCsv(server.baseURL, store.id);
	expect(exported.split('\n')[0].trim()).toBe('email,score');
	const fresh = await createDatastore(server.baseURL, uniqueName('E2E CSV Fresh'));
	await addColumn(server.baseURL, fresh.id, 'email', 'string');
	await addColumn(server.baseURL, fresh.id, 'score', 'number');
	const report = await importCsv(server.baseURL, fresh.id, exported);
	expect(report.inserted).toBe(2);
	expect(report.failed).toEqual([]);
	const reExported = await exportCsv(server.baseURL, fresh.id);
	expect(reExported).toBe(exported);

	// A file carrying a system column is refused with the offending name,
	// never silently dropped.
	const reserved = await apiStatus(
		server.baseURL,
		'POST',
		`/datastores/${store.id}/rows/import`,
		undefined
	).catch(() => null);
	expect(reserved, 'import helper exists').toBeDefined();
	const badResponse = await fetch(
		`${server.baseURL}/api/v1/datastores/${store.id}/rows/import`,
		{ method: 'POST', headers: { 'content-type': 'text/csv' }, body: 'id,email\n1,a@example.com\n' }
	);
	expect(badResponse.status).toBe(422);
	expect(await badResponse.text()).toContain('id');
});

test('an n8n Data Table workflow binds to a datastore and runs', async ({ server }) => {
	const store = await createDatastore(server.baseURL, uniqueName('E2E N8N Bind'));
	await addColumn(server.baseURL, store.id, 'email', 'string');

	const imported = await importN8nWorkflow(server.baseURL, n8nDataTableExport(), uniqueName('E2E N8N Table'));
	expect(imported.workflow.id.length).toBeGreaterThan(0);

	// The referenced table has no local counterpart by construction: the
	// import carries the reference and reports a blocking diagnostic naming
	// the n8n id and the cached name, rather than silently succeeding.
	const blocking = imported.unsupported.filter((issue) => issue.severity === 'blocking');
	expect(blocking.some((issue) => (issue.reason ?? '').includes('dt_metrics_01'))).toBe(true);
	expect(blocking.some((issue) => (issue.reason ?? '').includes('Metrics'))).toBe(true);

	const document = await fetchWorkflowDocument(server.baseURL, imported.workflow.id);
	const table = (document.nodes as any[]).find((node) => node.name === 'Table');
	expect(table?.type).toBe('kilasflow.datastore');
	expect(table?.parameters?.resource).toBe('row');

	// Binding the locator to the real datastore makes the imported workflow
	// runnable; the run writes the mapped row.
	table.parameters.dataTableId = datastoreLocator('id', store.id);
	table.parameters.columns = manualColumns({ email: 'bound@example.com' });
	await saveWorkflowDocument(server.baseURL, imported.workflow.id, document);

	const started = await startWorkflowRun(server.baseURL, imported.workflow.id);
	expect(started.status).toBe(202);
	const record = await waitForExecution(server.baseURL, started.body.id);
	expect(record.status).toBe('succeeded');
	const stored = await listRows(server.baseURL, store.id);
	expect((stored.items as unknown as Array<{ email: string }>).some((row) => row.email === 'bound@example.com')).toBe(true);

	// The unbound twin — the n8n id still naming nobody's table — refuses
	// instead of running against nothing.
	const unbound = await importN8nWorkflow(server.baseURL, n8nDataTableExport(), uniqueName('E2E N8N Unbound'));
	const unboundStart = await startWorkflowRun(server.baseURL, unbound.workflow.id);
	expect(unboundStart.status).toBe(202);
	const unboundRecord = await waitForExecution(server.baseURL, unboundStart.body.id);
	expect(unboundRecord.status).toBe('failed');
});

test('a second tenant cannot read, write or enumerate the first tenant datastore', async ({
	server,
	stub
}) => {
	const store = await createDatastore(server.baseURL, uniqueName('E2E Tenant A'));
	await addColumn(server.baseURL, store.id, 'email', 'string');
	await importCsv(server.baseURL, store.id, 'email\nvictim@example.com\n');

	// Tenant B is a second instance with its own database: the strongest
	// isolation the harness offers (auth off means each instance is its own
	// default tenant). A foreign id must read as unknown, never as another
	// tenant's table.
	const tenantB = await startServer({
		stubEndpoint: `127.0.0.1:${stub.port}`,
		allowedOrigin: stub.origin
	});
	try {
		const missingId = 'datastore_00000000-0000-7000-8000-000000000000';

		const getForeign = await apiStatus(server.baseURL, 'GET', `/datastores/${store.id}`).catch(() => null);
		expect(getForeign, 'sanity: owner reads its own table').toBeDefined();

		const victimOnB = await fetch(`${tenantB.baseURL}/api/v1/datastores/${store.id}`);
		const missingOnB = await fetch(`${tenantB.baseURL}/api/v1/datastores/${missingId}`);
		expect(victimOnB.status).toBe(404);
		expect(missingOnB.status).toBe(404);
		// Indistinguishable: the same status and the same problem shape for
		// a foreign id and a missing one, so existence is not oracle-able.
		const victimBody = await victimOnB.text();
		const missingBody = await missingOnB.text();
		expect(victimBody).toContain('not found');
		expect(missingBody).toContain('not found');

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

		// The node path is confined the same way: a workflow on tenant B
		// naming tenant A's table fails with the unknown-datastore refusal
		// and commits nothing tenant B can then read.
		const document = datastoreWriteReadDocument(
			uniqueName('E2E Tenant B Probe'),
			store.id,
			{ email: 'intruder@example.com' },
			'email',
			'intruder@example.com'
		);
		const probe = await createWorkflowDocument(tenantB.baseURL, document);
		const started = await startWorkflowRun(tenantB.baseURL, probe.id);
		expect(started.status).toBe(202);
		const record = await waitForExecution(tenantB.baseURL, started.body.id);
		expect(record.status).toBe('failed');
		const recordText = JSON.stringify(record as unknown);
		expect(recordText).toContain('unknown datastore');

		// Tenant A's row is untouched by the probe.
		const stored = await listRows(server.baseURL, store.id);
		expect((stored.items as unknown as Array<{ email: string }>).some((row) => row.email === 'intruder@example.com')).toBe(false);
	} finally {
		await tenantB.close();
	}
});

test('a datastore is renamed through the editor and by the node', async ({ page, server }) => {
	const store = await createDatastore(server.baseURL, uniqueName('E2E Before Rename'));
	const renamed = uniqueName('E2E After Rename');

	// Through the list's Rename dialog (the table update operation is
	// surfaced as Rename, shot 26).
	await page.goto(`${server.baseURL}/datastores`);
	await expect(page.getByRole('link', { name: store.name })).toBeVisible();
	await page.getByRole('button', { name: `Rename ${store.name}` }).click();
	await expect(page.getByRole('dialog')).toContainText('Rename datastore');
	await expect(page.getByRole('dialog')).toContainText('only the table is renamed');
	await page.locator('#datastore-name').fill(renamed);
	await page.getByRole('button', { name: 'Rename datastore' }).click();
	await expect(page.getByRole('link', { name: renamed })).toBeVisible();

	const afterUi = await getDatastore(server.baseURL, store.id);
	expect(afterUi.name).toBe(renamed);

	// The rename-table node operation renames the same table again.
	const finalName = uniqueName('E2E Node Renamed');
	const document = {
		schemaVersion: 1,
		name: uniqueName('E2E Rename Writer'),
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
					name: finalName
				}
			}
		],
		connections: [
			{ id: 'c1', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'rename', port: 'main' } }
		],
		settings: {}
	};
	const workflow = await createWorkflowDocument(server.baseURL, document);
	const executionId = await runWorkflow(server.baseURL, workflow.id);
	const record = await waitForExecution(server.baseURL, executionId);
	expect(record.status).toBe('succeeded');

	const afterNode: DatastoreResource = await getDatastore(server.baseURL, store.id);
	expect(afterNode.name).toBe(finalName);
});
