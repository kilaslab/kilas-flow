import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures';
import { createWorkflow, manualWorkflowDocument } from '../helpers/seed';

// Pasting nodes copied from an n8n canvas (BUG-txafja).
//
// A paste used to be translated in the browser by matching type names on
// their last segment: a Manual Trigger became an unsupported placeholder, the
// JavaScript Code node became the Go Code node with its source gone, and "="
// values stayed literal text. It now goes through the server's importer, so
// it yields what "Import n8n" would, with the same report.

// What n8n puts on the clipboard: nodes, name-keyed connections, and the
// instance metadata every copy carries.
const N8N_CLIPBOARD = {
	nodes: [
		{ id: 'm', name: "When clicking 'Execute workflow'", type: 'n8n-nodes-base.manualTrigger', typeVersion: 1, position: [0, 0], parameters: {} },
		{
			id: 's',
			name: 'Edit Fields',
			type: 'n8n-nodes-base.set',
			typeVersion: 3.4,
			position: [220, 0],
			parameters: { assignments: { assignments: [{ id: 'a1', name: 'city', value: 'Oslo', type: 'string' }] }, options: {} }
		},
		{
			id: 'h',
			name: 'HTTP Request',
			type: 'n8n-nodes-base.httpRequest',
			typeVersion: 4.2,
			position: [440, 0],
			parameters: { url: '=https://example.com/{{ $json.city }}', options: {} }
		},
		{
			id: 'c',
			name: 'Code',
			type: 'n8n-nodes-base.code',
			typeVersion: 2,
			position: [660, 0],
			parameters: { jsCode: "return items.map((item) => ({ json: { city: item.json.city, pasted: true } }));" }
		},
		{ id: 'x', name: 'Mystery', type: 'n8n-nodes-base.noSuchNode', typeVersion: 1, position: [880, 0], parameters: {} }
	],
	connections: {
		"When clicking 'Execute workflow'": { main: [[{ node: 'Edit Fields', type: 'main', index: 0 }]] },
		'Edit Fields': { main: [[{ node: 'HTTP Request', type: 'main', index: 0 }]] },
		'HTTP Request': { main: [[{ node: 'Code', type: 'main', index: 0 }]] }
	},
	meta: { instanceId: '0123456789abcdef' }
};

/** Pastes onto the canvas as the browser's own paste event would. */
async function pasteOnCanvas(page: Page, text: string): Promise<void> {
	await page.locator('[data-testid="workflow-canvas"]').evaluate((element, pasted) => {
		const data = new DataTransfer();
		data.setData('text/plain', pasted);
		element.dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }));
	}, text);
}

test('n8n nodes pasted onto the canvas arrive as an import would make them, with its report', async ({ page, server }) => {
	const workflow = await createWorkflow(server.baseURL, manualWorkflowDocument('Paste From n8n'));
	await page.goto(`${server.baseURL}/app/workflows/${workflow.id}`);
	const nodes = page.locator('[data-testid="workflow-canvas"] .svelte-flow__node');
	await expect(nodes).toHaveCount(1);

	await pasteOnCanvas(page, JSON.stringify(N8N_CLIPBOARD));
	// Counted from the status and the saved document below, not from the DOM:
	// the canvas renders only the nodes in view.
	const status = page.getByRole('status').filter({ hasText: 'Pasted 5 nodes' });
	await expect(status).toContainText('1 blocking');

	// The report is the import report's own component.
	await status.getByRole('button', { name: 'View paste report' }).click();
	const report = page.getByRole('dialog', { name: 'Paste report' });
	await expect(report).toBeVisible();
	await expect(report).toContainText('Mystery');
	await expect(report).toContainText('n8n-nodes-base.noSuchNode');
	await expect(report).not.toContainText('instance metadata');
	await page.keyboard.press('Escape');

	const save = page.getByRole('button', { name: 'Save', exact: true });
	await save.click();
	await expect(save).toBeDisabled();

	const stored = await (await fetch(`${server.baseURL}/api/v1/workflows/${workflow.id}`)).json();
	const pasted = stored.latestVersion.document.nodes as Array<{ name: string; type: string; parameters?: Record<string, any> }>;
	const byName = (name: string) => pasted.find((node) => node.name === name);

	// A Manual Trigger is a manual trigger, not a placeholder…
	expect(byName("When clicking 'Execute workflow'")?.type).toBe('kilasflow.manual');
	// …a JavaScript Code node is the JavaScript one, carrying its source…
	expect(byName('Code')?.type).toBe('kilasflow.jsCode');
	expect(byName('Code')?.parameters?.jsCode).toContain('pasted: true');
	// …and an "=" value is an expression.
	expect(byName('HTTP Request')?.parameters?.url).toMatchObject({ mode: 'expression' });
	expect(byName('HTTP Request')?.parameters?.url?.value).toContain('$json.city');
	// Only the unknown node is a placeholder, and the wires between the rest landed.
	expect(byName('Mystery')?.type).toBe('kilasflow.unsupported');
	expect(pasted).toHaveLength(6);
	expect((stored.latestVersion.document.connections as unknown[]).length).toBe(3);
});

test('a paste the importer refuses names the reason and changes nothing', async ({ page, server }) => {
	const workflow = await createWorkflow(server.baseURL, manualWorkflowDocument('Refused Paste'));
	await page.goto(`${server.baseURL}/app/workflows/${workflow.id}`);
	const nodes = page.locator('[data-testid="workflow-canvas"] .svelte-flow__node');
	await expect(nodes).toHaveCount(1);

	const duplicate = { name: 'A', type: 'n8n-nodes-base.noOp', typeVersion: 1, position: [0, 0], parameters: {} };
	await pasteOnCanvas(page, JSON.stringify({ nodes: [duplicate, duplicate], connections: {} }));

	await expect(page.getByRole('status').filter({ hasText: 'Could not paste the n8n nodes' })).toContainText('both named');
	await expect(nodes).toHaveCount(1);
});
