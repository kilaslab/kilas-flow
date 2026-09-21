import { test, expect } from '../fixtures';
import { gate } from '../fixtures/gates';
import { startServer } from '../helpers/server';
import {
	activateWorkflow,
	bindWahaCredential,
	completeChattingMigration,
	createStubWahaCredential,
	deliverWebhook,
	fetchDocument,
	importN8nTemplate,
	listExecutions,
	loadChattingTemplate,
	saveDocument,
	wahaPingDelivery,
	waitForNewExecution
} from '../fixtures/waha-migration';

// V2-p11-4: the epic's acceptance scenario as an executable suite. The
// official WAHA chatting template (reply "pong" to "ping", send an image on
// "image", always send seen) is imported, opened in the editor with correct
// icons and parameter panels, activated, given a real webhook delivery, and
// observed replying — with the same template imported twice for two different
// clients.
//
// The WhatsApp side is a stub, not a WAHA server: the trigger's contract is
// an HTTP delivery with a recorded body shape, and the pack builds every
// request URL from its credential's baseUrl, so pointing that credential at
// the loopback stub proves the path end to end without infrastructure. A real
// WAHA server is reserved for the V2-p11-8 capstone.
//
// Licensing: the template lives in the gitignored .corpus/ directory (fetched
// by `make corpus`) because its upstream repository carries no licence file.
// Every test using it skips cleanly with a message when the corpus is absent.

async function loadTemplateOrSkip(): Promise<Record<string, unknown>> {
	const template = await loadChattingTemplate();
	test.skip(
		template === null,
		gate(
			'corpus',
			'run `make corpus` (KILASFLOW_N8N_REFERENCE must point at the read-only n8n checkout) and retry'
		)
	);
	return template!;
}

// The template's expected diagnostics, pinned so a regression that starts
// dropping a field is caught rather than absorbed. Every WAHA node maps to
// the generated pack, every flow node maps to a builtin, and sticky notes
// carry as annotations — so nothing blocks activation. The five dropped
// entries are the instance-local residue n8n cannot carry: its per-node
// webhook identity, its minted route (replaced by the opaque address in
// `webhooks`), the Switch error mode, workflow settings, and instance
// metadata.
function expectChattingDiagnostics(imported: { unsupported: unknown[]; webhooks: unknown[] }): void {
	const issues = imported.unsupported as Array<{
		severity: string;
		nodeName?: string;
		nodeId?: string;
		field?: string;
		reason: string;
	}>;
	expect(
		issues.map((issue) => [issue.severity, issue.nodeName ?? '', issue.field ?? '']),
		'import diagnostics'
	).toEqual([
		['dropped', 'WAHA Trigger', 'webhookId'],
		['dropped', 'WAHA Trigger', 'path'],
		['dropped', 'Switch', 'onError'],
		['dropped', '', 'settings'],
		['dropped', '', 'meta']
	]);
	for (const issue of issues) {
		expect(issue.reason?.length ?? 0, `issue reason for ${issue.nodeName ?? issue.field}`).toBeGreaterThan(0);
	}
}

test('the official WAHA chatting template imports, activates, and replies to a real webhook delivery', async ({
	server,
	stub
}) => {
	const template = await loadTemplateOrSkip();

	const imported = await importN8nTemplate(server.baseURL, template, 'WAHA Chatting (e2e)');
	expect(imported.workflow.id.length).toBeGreaterThan(0);
	expectChattingDiagnostics(imported);

	// The re-pointing contract: one fresh opaque route for the trigger, not
	// the template's path. The sending system must be pointed at `url`.
	expect(imported.webhooks).toHaveLength(1);
	const route = imported.webhooks[0];
	expect(route.url).toMatch(/^\/webhook\/[0-9a-f]{32}$/);
	expect(route.url).not.toContain(route.path);
	expect(route.path).toBe('waha-trigger');

	// n8n credential references are instance-local, so the migrating user
	// creates a real credential and binds every WAHA node to it — and
	// supplies the Send Image media the template leaves blank, which the
	// pack requires. Activation stays refused until both are done.
	const credentialId = await createStubWahaCredential(server.baseURL, 'WAHA stub', stub.origin);
	const document = await fetchDocument(server.baseURL, imported.workflow.id);
	const bound = bindWahaCredential(document, credentialId);
	expect(bound).toContain('WAHA Trigger');
	expect(bound.length).toBeGreaterThanOrEqual(4);
	expect(completeChattingMigration(document, stub.url('/waha-image.png'))).toContain('Send Image');
	await saveDocument(server.baseURL, imported.workflow.id, document);
	await activateWorkflow(server.baseURL, imported.workflow.id);

	// A fresh instance per test, so no execution can predate this delivery.
	const known: string[] = [];
	const status = await deliverWebhook(server.baseURL, route.url, wahaPingDelivery());
	expect([200, 202]).toContain(status);

	// Observed through the execution event stream, not by polling a database.
	const { record, events } = await waitForNewExecution(server.baseURL, imported.workflow.id, known);
	expect(record.status).toBe('succeeded');
	expect(record.workflowId).toBe(imported.workflow.id);
	expect(events.map((event) => event.type)).toContain('execution.completed');

	// The reply reached the stubbed WAHA endpoint: the workflow answered
	// "pong" through the pack's Send Text operation.
	const replies = stub.requests.filter((request) => request.path === '/api/sendText');
	expect(replies.length).toBeGreaterThan(0);
	expect(replies.some((request) => request.body.includes('pong'))).toBe(true);
});

test('the migrated workflow opens in the editor with icons, parameter panels, and copyable webhook URLs', async ({
	page,
	server
}) => {
	const template = await loadTemplateOrSkip();

	await page.goto(`${server.baseURL}/app/workflows`);
	// Two triggers carry this name on an empty workspace — the header's and the
	// empty state's — so the name alone is ambiguous; this is the header's, the
	// same one library-import.spec.ts drives.
	await page.getByRole('button', { name: 'Import n8n' }).first().click();
	await expect(page.getByRole('dialog')).toContainText('Import from n8n');
	await page.locator('#import-paste').fill(JSON.stringify(template));
	await page.locator('#import-name').fill('WAHA Chatting (editor)');
	// The dialog footer sits below the fold once a 9KB paste expands the
	// form, and no scroll reaches it — click in-page, same handler.
	await page.getByRole('button', { name: 'Import workflow' }).evaluate((button) => button.click());

	// The import report, read as a report rather than a success toast: the
	// activation verdict, every minted webhook URL beside its node, and a
	// copy button per URL so the sending system can be re-pointed.
	await expect(page.getByRole('dialog')).toContainText('This workflow will activate as imported.');
	await expect(page.getByRole('heading', { name: /Webhook addresses · 1/ })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Copy webhook URL for WAHA Trigger' })).toBeVisible();
	await expect(page.getByRole('dialog').locator('code', { hasText: '/webhook/' })).toBeVisible();

	await page.getByRole('button', { name: 'Open in the editor' }).click();
	await expect(page.locator('[data-testid="workflow-canvas"]')).toBeVisible();

	// Every node renders with its name and server-driven icon. A node the
	// editor had no entry for would arrive as an unstyled fallback; a node
	// the importer had no mapping for would arrive as an "Unsupported node"
	// capsule — neither is acceptable for this template.
	for (const name of ['WAHA Trigger', 'Send "pong"', 'Send Seen', 'Send Image', 'body=ping', 'body=image', 'Switch']) {
		await expect(page.locator('[data-testid="workflow-canvas"]').getByText(name, { exact: true }).first()).toBeVisible();
	}
	await expect(page.locator('[data-testid="workflow-canvas"]').getByText('Unsupported node')).toHaveCount(0);

	// Parameter panels are server-driven metadata, so selecting a node must
	// show its fields: the trigger's session and path, the reply's text. The
	// click lands on the SvelteFlow node wrapper — the tile's own text never
	// receives pointer events.
	const canvasNode = (name: string) =>
		page.locator('[data-testid="workflow-canvas"] .svelte-flow__node').filter({ hasText: name }).first();
	await canvasNode('Send "pong"').click();
	await expect(page.getByText('Chat Id', { exact: true }).first()).toBeVisible();
	await expect(page.getByText('Reply To', { exact: true }).first()).toBeVisible();
	await expect(page.getByText('This node needs a credential before it can run.').first()).toBeVisible();
});

test('the same template imported twice serves two tenants in isolation', async ({ server, stub }) => {
	const template = await loadTemplateOrSkip();

	// Tenant B is a second instance with its own database: the strongest
	// isolation the harness offers, and the shape the white-label product
	// ships — one agency, many clients, no shared rows.
	const tenantB = await startServer({
		stubEndpoint: `127.0.0.1:${stub.port}`,
		allowedOrigin: stub.origin
	});
	try {
		const tenantA = await importN8nTemplate(server.baseURL, template, 'WAHA Chatting (client A)');
		const otherTenant = await importN8nTemplate(tenantB.baseURL, template, 'WAHA Chatting (client B)');
		expectChattingDiagnostics(tenantA);
		expectChattingDiagnostics(otherTenant);

		// Fresh opaque routes per tenant and workflow: the same template
		// never mints the same address twice.
		const urlA = tenantA.webhooks[0].url;
		const urlB = otherTenant.webhooks[0].url;
		expect(tenantA.webhooks).toHaveLength(1);
		expect(otherTenant.webhooks).toHaveLength(1);
		expect(urlA).not.toBe(urlB);

		// A route minted on one tenant means nothing on the other.
		expect(await deliverWebhook(tenantB.baseURL, urlA, wahaPingDelivery())).toBe(404);

		for (const [baseURL, imported, label] of [
			[server.baseURL, tenantA, 'client A'],
			[tenantB.baseURL, otherTenant, 'client B']
		] as const) {
			const credentialId = await createStubWahaCredential(baseURL, `WAHA stub (${label})`, stub.origin);
			const document = await fetchDocument(baseURL, imported.workflow.id);
			bindWahaCredential(document, credentialId);
			completeChattingMigration(document, stub.url('/waha-image.png'));
			await saveDocument(baseURL, imported.workflow.id, document);
			await activateWorkflow(baseURL, imported.workflow.id);
		}

		const knownA = (await listExecutions(server.baseURL, tenantA.workflow.id)).map((item) => item.id);
		expect(await deliverWebhook(server.baseURL, urlA, wahaPingDelivery())).toBeLessThan(300);
		const first = await waitForNewExecution(server.baseURL, tenantA.workflow.id, knownA);
		expect(first.record.status).toBe('succeeded');

		// Client A's delivery reached only client A's execution: B is silent.
		expect(await listExecutions(tenantB.baseURL, otherTenant.workflow.id)).toHaveLength(0);

		const knownB = (await listExecutions(tenantB.baseURL, otherTenant.workflow.id)).map((item) => item.id);
		expect(await deliverWebhook(tenantB.baseURL, urlB, wahaPingDelivery())).toBeLessThan(300);
		const second = await waitForNewExecution(tenantB.baseURL, otherTenant.workflow.id, knownB);
		expect(second.record.status).toBe('succeeded');
		expect(second.record.workflowId).toBe(otherTenant.workflow.id);

		// Both tenants' replies passed through the stubbed WAHA endpoint.
		expect(stub.requests.filter((request) => request.path === '/api/sendText').length).toBeGreaterThanOrEqual(2);
	} finally {
		await tenantB.close();
	}
});

test('an unmapped node arrives as a capsule that names itself and refuses activation', async ({ page, server }) => {
	const capsule = {
		name: 'Capsule proof',
		nodes: [
			{ id: 'm1', name: 'Manual Trigger', type: 'n8n-nodes-base.manualTrigger', typeVersion: 1, position: [0, 0], parameters: {} },
			{
				id: 'x1',
				name: 'Mystery Box',
				type: 'n8n-nodes-base.mysteryBox',
				typeVersion: 1,
				position: [260, 0],
				parameters: { foo: 'bar' }
			}
		],
		connections: { 'Manual Trigger': { main: [[{ node: 'Mystery Box', type: 'main', index: 0 }]] } },
		active: false,
		settings: {}
	};

	const imported = await importN8nTemplate(server.baseURL, capsule, 'Capsule proof');

	// Diagnostics as data: one blocking issue naming the node, never a
	// sentence the test has to parse.
	expect(imported.unsupported).toHaveLength(1);
	expect(imported.unsupported[0].severity).toBe('blocking');
	expect(imported.unsupported[0].nodeName).toBe('Mystery Box');
	expect(imported.unsupported[0].type).toBe('n8n-nodes-base.mysteryBox');

	// The capsule preserves the original type and version for the editor and
	// for a round-trip export.
	const document = await fetchDocument(server.baseURL, imported.workflow.id);
	const placeholder = document.nodes.find((node) => node.name === 'Mystery Box');
	expect(placeholder?.type).toBe('kilasflow.unsupported');
	expect(placeholder?.parameters?.['originalType']).toBe('n8n-nodes-base.mysteryBox');
	expect(placeholder?.parameters?.['originalTypeVersion']).toBe(1);

	// Activation is refused with a message naming the node.
	const activation = await fetch(`${server.baseURL}/api/v1/workflows/${imported.workflow.id}/activate`, { method: 'POST' });
	expect(activation.status).toBe(422);
	expect(await activation.text()).toContain('n8n-nodes-base.mysteryBox');
	// And the capsule renders in the editor under the node's own name, with
	// its original identity visible once selected.
	await page.goto(`${server.baseURL}/app/workflows/${imported.workflow.id}`);
	await expect(page.locator('[data-testid="workflow-canvas"]')).toBeVisible();
	await expect(page.locator('[data-testid="workflow-canvas"]').getByText('Mystery Box', { exact: true }).first()).toBeVisible();
	await page.locator('[data-testid="workflow-canvas"]').getByText('Mystery Box', { exact: true }).first().click();
	await expect(page.getByText('Original node type').first()).toBeVisible();
	await expect(page.getByText('n8n-nodes-base.mysteryBox').first()).toBeVisible();
});
