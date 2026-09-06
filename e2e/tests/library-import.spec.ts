// Library sampling import/run (FEAT-wdnc03). New file only: the harness is
// untouched, and the gg85se (waha-migration) plus nch9dg (datastore) suites are
// reused by composition — their fixture helpers are imported, their specs are
// never rewritten.
//
// Coverage: the official WAHA chatting template (corpus-pinned, single import;
// the two-tenant variant stays proven by waha-migration.spec.ts 'the same
// template imported twice serves two tenants in isolation' and is not
// restated here) plus one pinned export per family — webhook-triggered,
// scheduled, data-shaping, flow-control, HTTP integration, AI agent — and a
// blocked capsule. Each sample reports imported / activatable / runnable /
// blocked with its nbqye0 diagnostics surfaced and its failure classes
// (unsupported-node / expression-gap / connection-gap / credential-gap)
// counted. The live-n8n layer at the bottom exports the same families from a
// real instance when N8N_EMAIL/N8N_PASSWORD are present and skips by name with
// the exact export commands otherwise — never a silent pass.
import { test, expect } from '../fixtures';
import { waitForExecution } from '../helpers/seed';
import {
	LIBRARY_SAMPLES,
	classifyIssue,
	countGaps,
	exportLiveWorkflow,
	liveSkipReason,
	loadLibraryExport,
	n8nNodeTypes,
	type GapCounts,
	type ImportIssue
} from '../fixtures/library-import';
import {
	addColumn,
	createDatastore,
	datastoreLocator,
	fetchWorkflowDocument,
	listRows,
	manualColumns,
	n8nDataTableExport,
	nodeRun,
	saveWorkflowDocument,
	startWorkflowRun,
	uniqueName
} from '../fixtures/datastore';
import { importN8nTemplate, loadChattingTemplate } from '../fixtures/waha-migration';

async function apiRaw(
	baseURL: string,
	method: string,
	path: string,
	body?: unknown
): Promise<{ status: number; text: string; json: unknown }> {
	const response = await fetch(`${baseURL}/api/v1${path}`, {
		method,
		headers: { 'content-type': 'application/json' },
		body: body === undefined ? undefined : JSON.stringify(body)
	});
	const text = await response.text();
	let json: unknown = null;
	try {
		json = text === '' ? null : JSON.parse(text);
	} catch {
		json = null;
	}
	return { status: response.status, text, json };
}

async function tryActivate(baseURL: string, workflowId: string): Promise<{ status: number; text: string }> {
	const response = await fetch(`${baseURL}/api/v1/workflows/${workflowId}/activate`, { method: 'POST' });
	return { status: response.status, text: await response.text() };
}

async function tryRun(baseURL: string, workflowId: string, input?: unknown): Promise<{ status: number; text: string; json: unknown }> {
	return apiRaw(baseURL, 'POST', `/workflows/${workflowId}/run`, input === undefined ? {} : { input });
}

function errorText(body: unknown): string {
	if (typeof body === 'string') return body;
	try {
		return JSON.stringify(body);
	} catch {
		return String(body);
	}
}

function expectSurfacedDiagnostics(sampleId: string, issues: ImportIssue[]): void {
	for (const issue of issues) {
		expect(['blocking', 'lossy', 'dropped'], `${sampleId}: severity for ${issue.nodeName ?? issue.field}`).toContain(
			issue.severity
		);
		expect(issue.reason?.length ?? 0, `${sampleId}: reason for ${issue.nodeName ?? issue.field}`).toBeGreaterThan(0);
	}
}

test('the library sample report counts every gap class per workflow', async ({ server }, testInfo) => {
	const rows: Array<{
		id: string;
		family: string;
		provenance: string;
		imported: boolean;
		blocking: number;
		total: number;
		gaps: GapCounts;
	}> = [];
	for (const sample of LIBRARY_SAMPLES) {
		const exported = await loadLibraryExport(sample.id);
		const imported = await importN8nTemplate(server.baseURL, exported, uniqueName(`Library ${sample.id}`));
		expect(imported.workflow.id.length, `${sample.id}: imported`).toBeGreaterThan(0);
		expectSurfacedDiagnostics(sample.id, imported.unsupported);
		rows.push({
			id: sample.id,
			family: sample.family,
			provenance: sample.provenance,
			imported: true,
			blocking: imported.unsupported.filter((issue) => issue.severity === 'blocking').length,
			total: imported.unsupported.length,
			gaps: countGaps(imported.unsupported)
		});
	}

	const byId = Object.fromEntries(rows.map((row) => [row.id, row]));
	// Runnable families import with no blocking diagnostic. webhook-echo is
	// fully clean — its path is carried as the route label, so the import
	// reports zero diagnostics (the chatting template's dropped-path entry
	// comes from its empty path, not from webhooks as such).
	for (const id of ['scheduled-tick', 'data-shaping', 'flow-control', 'http-stub']) {
		expect(byId[id].blocking, `${id}: blocking diagnostics`).toBe(0);
	}
	expect(byId['webhook-echo'].blocking, 'webhook-echo: blocking diagnostics').toBe(0);
	expect(byId['webhook-echo'].total, 'webhook-echo: imports with zero diagnostics').toBe(0);
	// Blocked families name their gap class: the agent arrives unbound
	// (credential-gap), the capsule names its node (unsupported-node) and the
	// edge to the missing node is held back (connection-gap).
	expect(byId['ai-agent'].gaps['credential-gap'], 'ai-agent: credential-gap count').toBeGreaterThanOrEqual(1);
	expect(byId['unsupported-node'].gaps['unsupported-node'], 'capsule: unsupported-node count').toBe(1);
	expect(byId['unsupported-node'].gaps['connection-gap'], 'capsule: connection-gap count').toBeGreaterThanOrEqual(1);
	// The importer carries `=` expressions (see the Go corpus tests), so the
	// expression-gap class is honestly zero rather than unmeasured.
	const expressionTotal = rows.reduce((sum, row) => sum + row.gaps['expression-gap'], 0);
	expect(expressionTotal, 'expression-gap total across samples').toBe(0);

	await testInfo.attach('library-import-report', {
		body: JSON.stringify(rows, null, 2),
		contentType: 'application/json'
	});
});

test('webhook-triggered sample activates and answers a real delivery', async ({ server }) => {
	const imported = await importN8nTemplate(server.baseURL, await loadLibraryExport('webhook-echo'), uniqueName('Library Webhook'));
	expect(imported.webhooks).toHaveLength(1);
	expect(imported.webhooks[0].url).toMatch(/^\/webhook\/[0-9a-f]{32}$/);

	const activation = await tryActivate(server.baseURL, imported.workflow.id);
	expect(`${activation.status} ${activation.text}`, 'webhook-echo activates').toContain('200');

	const delivery = await fetch(`${server.baseURL}${imported.webhooks[0].url}`, {
		method: 'POST',
		headers: { 'content-type': 'application/json' },
		body: JSON.stringify({ text: 'hello library' })
	});
	expect(delivery.status, 'webhook delivery').toBe(200);

	const executions = (await apiRaw(server.baseURL, 'GET', `/executions?workflowId=${imported.workflow.id}&limit=5`)).json as {
		items: Array<{ id: string }>;
	};
	expect(executions.items.length, 'an execution exists for the delivery').toBeGreaterThan(0);
	const record = await waitForExecution(server.baseURL, executions.items[0].id);
	expect(record.status, 'webhook execution').toBe('succeeded');
	expect(nodeRun(record, 'c')?.status, 'Respond node ran').toBe('succeeded');
});

test('scheduled sample activates and runs to a green execution', async ({ server }) => {
	const imported = await importN8nTemplate(server.baseURL, await loadLibraryExport('scheduled-tick'), uniqueName('Library Scheduled'));

	const activation = await tryActivate(server.baseURL, imported.workflow.id);
	expect(`${activation.status} ${activation.text}`, 'scheduled-tick activates').toContain('200');

	const run = await tryRun(server.baseURL, imported.workflow.id);
	expect(run.status, `scheduled-tick run accepted (body: ${run.text})`).toBe(202);
	const record = await waitForExecution(server.baseURL, (run.json as { id: string }).id);
	expect(record.status, 'scheduled execution').toBe('succeeded');
	expect(nodeRun(record, 'b')?.status, 'Stamp node ran').toBe('succeeded');
});

test('data-shaping and flow-control samples run headless to green', async ({ server }) => {
	const shaping = await importN8nTemplate(server.baseURL, await loadLibraryExport('data-shaping'), uniqueName('Library Shaping'));
	const shapingActivation = await tryActivate(server.baseURL, shaping.workflow.id);
	expect(`${shapingActivation.status} ${shapingActivation.text}`, 'data-shaping activates').toContain('200');
	const shapingRun = await tryRun(server.baseURL, shaping.workflow.id);
	expect(shapingRun.status, `data-shaping run accepted (body: ${shapingRun.text})`).toBe(202);
	const shapingRecord = await waitForExecution(server.baseURL, (shapingRun.json as { id: string }).id);
	expect(shapingRecord.status, 'data-shaping execution').toBe('succeeded');
	expect(nodeRun(shapingRecord, 'c')?.status, 'Sort node ran').toBe('succeeded');

	const flow = await importN8nTemplate(server.baseURL, await loadLibraryExport('flow-control'), uniqueName('Library Flow'));
	const flowActivation = await tryActivate(server.baseURL, flow.workflow.id);
	expect(`${flowActivation.status} ${flowActivation.text}`, 'flow-control activates').toContain('200');
	const flowRun = await tryRun(server.baseURL, flow.workflow.id, { tier: 'gold' });
	expect(flowRun.status, `flow-control run accepted (body: ${flowRun.text})`).toBe(202);
	const flowRecord = await waitForExecution(server.baseURL, (flowRun.json as { id: string }).id);
	expect(flowRecord.status, 'flow-control execution').toBe('succeeded');
	expect(nodeRun(flowRecord, 'c')?.status, 'Gold branch ran').toBe('succeeded');
});

test('http-integration sample re-points at the stub and runs green', async ({ server, stub }) => {
	const imported = await importN8nTemplate(server.baseURL, await loadLibraryExport('http-stub'), uniqueName('Library HTTP'));

	// The pinned URL is a placeholder by design; the migrating user re-points
	// it at the real endpoint — here the per-test loopback stub admitted
	// through the instance's outbound policy.
	const document = await fetchWorkflowDocument(server.baseURL, imported.workflow.id);
	const fetch = (document.nodes as Array<{ name: string; parameters?: Record<string, unknown> }>).find(
		(node) => node.name === 'Fetch'
	);
	expect(fetch, 'Fetch node present').toBeDefined();
	fetch!.parameters = { ...(fetch!.parameters ?? {}), url: stub.url('/library-prices') };
	await saveWorkflowDocument(server.baseURL, imported.workflow.id, document);
	const httpActivation = await tryActivate(server.baseURL, imported.workflow.id);
	expect(`${httpActivation.status} ${httpActivation.text}`, 'http-stub activates').toContain('200');

	const run = await tryRun(server.baseURL, imported.workflow.id);
	expect(run.status, `http-stub run accepted (body: ${run.text})`).toBe(202);
	const record = await waitForExecution(server.baseURL, (run.json as { id: string }).id);
	expect(record.status, 'http execution').toBe('succeeded');
	expect(nodeRun(record, 'b')?.status, 'Fetch node ran').toBe('succeeded');
	expect(
		stub.requests.some((request) => request.path === '/library-prices'),
		'the stub observed the HTTP node'
	).toBe(true);
});

test('data-table sample binds to a datastore and runs; the unbound twin diagnoses', async ({ server }) => {
	const store = await createDatastore(server.baseURL, uniqueName('Library N8N Bind'));
	await addColumn(server.baseURL, store.id, 'email', 'string');

	const imported = await importN8nTemplate(server.baseURL, n8nDataTableExport(), uniqueName('Library N8N Table'));
	const blocking = imported.unsupported.filter((issue) => issue.severity === 'blocking');
	expect(blocking.some((issue) => (issue.reason ?? '').includes('dt_metrics_01')), 'diagnostic names the n8n table id').toBe(true);

	const document = await fetchWorkflowDocument(server.baseURL, imported.workflow.id);
	const table = (document.nodes as Array<{ name: string; parameters?: Record<string, unknown> }>).find(
		(node) => node.name === 'Table'
	);
	expect(table?.parameters?.['resource'], 'Table node resource').toBe('row');
	table!.parameters!['dataTableId'] = datastoreLocator('id', store.id);
	table!.parameters!['columns'] = manualColumns({ email: 'library@example.com' });
	await saveWorkflowDocument(server.baseURL, imported.workflow.id, document);

	const started = await startWorkflowRun(server.baseURL, imported.workflow.id);
	expect(started.status, `bound table run accepted`).toBe(202);
	const record = await waitForExecution(server.baseURL, started.body.id);
	expect(record.status, 'bound table execution').toBe('succeeded');
	const stored = await listRows(server.baseURL, store.id);
	expect(
		(stored.items as Array<{ email: string }>).some((row) => row.email === 'library@example.com'),
		'the mapped row landed in the datastore'
	).toBe(true);
});

test('ai-agent sample stays blocked until a local credential is bound', async ({ server }) => {
	const imported = await importN8nTemplate(server.baseURL, await loadLibraryExport('ai-agent'), uniqueName('Library Agent'));
	const gaps = countGaps(imported.unsupported);
	expect(gaps['credential-gap'], 'ai-agent reports its credential-gap').toBeGreaterThanOrEqual(1);
	expect(
		imported.unsupported.some((issue) => issue.severity === 'blocking' && classifyIssue(issue) === 'credential-gap'),
		'the credential-gap blocks activation'
	).toBe(true);

	// The run names the missing credential instead of crashing: the user-facing
	// validation message, not a runtime failure.
	const run = await tryRun(server.baseURL, imported.workflow.id, { question: 'hi', chatId: 'library' });
	expect(run.status, `agent run refused (body: ${run.text})`).toBe(422);
	expect(errorText(run.json), 'refusal names the OpenAI credential').toContain('openAiApi');
});

test('unsupported-node sample arrives as a capsule and refuses activation', async ({ server }) => {
	const imported = await importN8nTemplate(server.baseURL, await loadLibraryExport('unsupported-node'), uniqueName('Library Blocked'));
	expect(imported.unsupported.filter((issue) => issue.severity === 'blocking').length, 'blocking diagnostics').toBeGreaterThanOrEqual(1);

	const document = await fetchWorkflowDocument(server.baseURL, imported.workflow.id);
	const capsule = (document.nodes as Array<{ name: string; type: string; parameters?: Record<string, unknown> }>).find(
		(node) => node.name === 'Send Email'
	);
	expect(capsule?.type, 'capsule type').toBe('kilasflow.unsupported');
	expect(capsule?.parameters?.['originalType'], 'capsule preserves the n8n type').toBe('n8n-nodes-base.emailSend');

	const activation = await tryActivate(server.baseURL, imported.workflow.id);
	expect(activation.status, `activation refused (body: ${activation.text})`).toBe(422);
	expect(activation.text, 'refusal names the unmapped node').toContain('n8n-nodes-base.emailSend');
});

test('a sampled workflow opens in the editor with icons and no capsule', async ({ page, server }) => {
	const exported = await loadLibraryExport('webhook-echo');
	await page.goto(`${server.baseURL}/app/workflows`);
	await page.getByRole('button', { name: 'Import n8n' }).click();
	await expect(page.getByRole('dialog')).toContainText('Import from n8n');
	await page.locator('#import-paste').fill(JSON.stringify(exported));
	await page.locator('#import-name').fill('Library Webhook (editor)');
	await page.getByRole('button', { name: 'Import workflow' }).evaluate((button) => button.click());

	await expect(page.getByRole('dialog')).toContainText('This workflow will activate as imported.');
	await page.getByRole('button', { name: 'Open in the editor' }).click();
	await expect(page.locator('[data-testid="workflow-canvas"]')).toBeVisible();
	for (const name of ['Webhook', 'Shape', 'Respond']) {
		await expect(page.locator('[data-testid="workflow-canvas"]').getByText(name, { exact: true }).first()).toBeVisible();
	}
	await expect(page.locator('[data-testid="workflow-canvas"]').getByText('Unsupported node')).toHaveCount(0);
});

test('the WAHA chatting template imports with its webhook re-pointed', async ({ server }) => {
	const template = await loadChattingTemplate();
	test.skip(
		template === null,
		'WAHA corpus absent: run `make corpus` (KILASFLOW_N8N_REFERENCE must point at the read-only n8n checkout) and retry'
	);
	// Composition, not restatement: gg85se's waha-migration.spec.ts proves the
	// full migrate → rebind → activate → reply path plus the two-tenant
	// variant (same template active for two clients, isolated deliveries).
	// The library sample reports its own import status; activation past the
	// user-completion step (credential rebind, Send Image media) belongs to
	// that spec and is not repeated here.
	const imported = await importN8nTemplate(server.baseURL, template!, 'WAHA Chatting (library sample)');
	expect(imported.workflow.id.length, 'WAHA sample imported').toBeGreaterThan(0);
	expect(
		imported.unsupported.filter((issue) => issue.severity === 'blocking').length,
		'WAHA sample has no blocking diagnostic'
	).toBe(0);
	expect(imported.webhooks, 'one re-pointed webhook route').toHaveLength(1);
	expect(imported.webhooks[0].url).toMatch(/^\/webhook\/[0-9a-f]{32}$/);
});

test.describe('live n8n library exports (env-gated)', () => {
	for (const sample of LIBRARY_SAMPLES) {
		test(`${sample.id} matches its live-n8n export`, async ({ server }) => {
			const skip = liveSkipReason();
			test.skip(skip !== null, skip ?? 'live n8n unavailable');
			const live = await exportLiveWorkflow(`Library ${sample.id}`);
			const pin = await loadLibraryExport(sample.id);
			// Same node-type shape on both sides: a drift here means the pin
			// no longer describes the library workflow it stands for.
			expect(n8nNodeTypes(live), `${sample.id}: live node types match the pin`).toEqual(n8nNodeTypes(pin));
			const imported = await importN8nTemplate(server.baseURL, live, uniqueName(`Library Live ${sample.id}`));
			expect(imported.workflow.id.length, `${sample.id}: live export imports`).toBeGreaterThan(0);
			const gaps = countGaps(imported.unsupported as ImportIssue[]);
			if (sample.runnable) {
				expect(
					(imported.unsupported as ImportIssue[]).filter((issue) => issue.severity === 'blocking').length,
					`${sample.id}: live export introduces no blocking diagnostic`
				).toBe(0);
			} else if (sample.id === 'ai-agent') {
				expect(gaps['credential-gap'], `${sample.id}: live export keeps its credential-gap`).toBeGreaterThanOrEqual(1);
			} else {
				expect(gaps['unsupported-node'], `${sample.id}: live export keeps its unsupported-node`).toBeGreaterThanOrEqual(1);
			}
		});
	}
});
