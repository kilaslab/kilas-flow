import type { Locator, Page } from '@playwright/test';

import { test, expect } from '../fixtures';
import { createWorkflow, listExecutionIds, waitForExecution } from '../helpers/seed';

// The code editor that code parameters render as (BUG-ngt25j).
//
// The Go Code node's field used to be <input type=text>: its default body is
// one line, so the editor picked a one-line control, Enter inserted nothing
// and a pasted multi-line body collapsed onto one line that did not compile.

const GO_BODY = 'out := []Item{}\nfor _, it := range items {\n\tout = append(out, it)\n}\nreturn out, nil';

function canvasNode(page: Page, name: string) {
	return page.locator('[data-testid="workflow-canvas"] .svelte-flow__node').filter({ hasText: name }).first();
}

/** Pastes text into an editor as the browser's own paste event would. */
async function paste(editor: Locator, text: string): Promise<void> {
	await editor.evaluate((element, pasted) => {
		const data = new DataTransfer();
		data.setData('text/plain', pasted);
		element.dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }));
	}, text);
}

async function openParameters(page: Page, nodeName: string) {
	await canvasNode(page, nodeName).click();
	const panel = page.getByRole('region', { name: `${nodeName} properties` });
	await expect(panel).toBeVisible();
	await panel.getByRole('tab', { name: 'Parameters' }).click();
	return panel;
}

test('a Go body pasted into the Code node keeps its lines, takes Enter, compiles and runs', async ({ page, server }) => {
	const workflow = await createWorkflow(server.baseURL, {
		schemaVersion: 1,
		name: 'Go Code From The Editor',
		nodes: [
			{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
			{ id: 'code', name: 'Code', type: 'kilasflow.code', typeVersion: 1, position: { x: 240, y: 0 }, parameters: { code: 'return items, nil' } }
		],
		connections: [{ id: 'c1', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'code', port: 'main' } }],
		settings: {}
	});
	await page.goto(`${server.baseURL}/app/workflows/${workflow.id}`);
	await expect(page.locator('[data-testid="workflow-canvas"]')).toBeVisible();

	const panel = await openParameters(page, 'Code');
	await expect(panel.locator('[data-code-editor="go"] .cm-lineNumbers')).toBeVisible();
	const code = panel.getByRole('textbox', { name: 'Go code', exact: true });
	await code.click();
	await page.keyboard.press('ControlOrMeta+A');
	await paste(code, GO_BODY);
	await expect(code.locator('.cm-line')).toHaveCount(5);

	// Enter starts a new line, where the old field inserted nothing.
	await page.keyboard.press('ControlOrMeta+End');
	await page.keyboard.press('Enter');
	await page.keyboard.type('// written in the editor');
	await expect(code.locator('.cm-line')).toHaveCount(6);
	expect(await code.evaluate((element) => (element as HTMLElement).innerText.replace(/\n$/, ''))).toBe(`${GO_BODY}\n// written in the editor`);

	const save = page.getByRole('button', { name: 'Save', exact: true });
	await expect(save).toBeEnabled();
	await save.click();
	await expect(save).toBeDisabled();

	const before = await listExecutionIds(server.baseURL, workflow.id);
	await page.getByRole('button', { name: 'Execute', exact: true }).click();
	await expect(page.getByRole('status')).toContainText('Run succeeded.', { timeout: 90_000 });
	let executionId = '';
	await expect
		.poll(async () => (executionId = (await listExecutionIds(server.baseURL, workflow.id)).find((id) => !before.includes(id)) ?? ''))
		.not.toBe('');
	const record = await waitForExecution(server.baseURL, executionId);
	expect(record.status).toBe('succeeded');

	const stored = await (await fetch(`${server.baseURL}/api/v1/workflows/${workflow.id}`)).json();
	const saved = (stored.latestVersion.document.nodes as Array<{ id: string; parameters?: { code?: string } }>).find((node) => node.id === 'code');
	expect(saved?.parameters?.code).toBe(`${GO_BODY}\n// written in the editor`);
});

test('workflow JSON pasted into a code editor is code, not nodes for the canvas', async ({ page, server }) => {
	const workflow = await createWorkflow(server.baseURL, {
		schemaVersion: 1,
		name: 'JSON Into Code',
		nodes: [
			{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
			{ id: 'js', name: 'JS', type: 'kilasflow.jsCode', typeVersion: 1, position: { x: 240, y: 0 }, parameters: { mode: 'runOnceForAllItems', jsCode: 'return items;' } }
		],
		connections: [{ id: 'c1', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'js', port: 'main' } }],
		settings: {}
	});
	await page.goto(`${server.baseURL}/app/workflows/${workflow.id}`);
	const nodes = page.locator('[data-testid="workflow-canvas"] .svelte-flow__node');
	await expect(nodes).toHaveCount(2);

	const panel = await openParameters(page, 'JS');
	const code = panel.getByRole('textbox', { name: 'JavaScript', exact: true });
	await code.click();
	await page.keyboard.press('ControlOrMeta+A');
	const snippet = JSON.stringify({ nodes: [{ name: 'Set', type: 'n8n-nodes-base.set', typeVersion: 3.4, position: [0, 0], parameters: {} }], connections: {} });
	await paste(code, `const fixture = ${snippet};\nreturn items;`);

	await expect(code).toContainText('n8n-nodes-base.set');
	await expect(nodes).toHaveCount(2);
});
