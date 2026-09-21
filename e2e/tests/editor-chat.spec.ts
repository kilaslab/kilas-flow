import { test, expect } from '../fixtures';
import { chatWorkflowDocument, createWorkflow } from '../helpers/seed';

test('the editor Chat button queues a run with chatInput', async ({ page, server }) => {
	const workflow = await createWorkflow(server.baseURL, chatWorkflowDocument('E2E Chat Workflow'));

	await page.goto(`${server.baseURL}/app/workflows/${workflow.id}`);
	await expect(page.locator('[data-testid="workflow-canvas"]')).toBeVisible();
	await expect(page.getByTestId('workflow-chat-button')).toBeVisible();

	const executeRuns: unknown[] = [];
	page.on('request', (request) => {
		if (request.method() === 'POST' && request.url().includes(`/workflows/${workflow.id}/run`)) {
			executeRuns.push(request.postDataJSON());
		}
	});

	await page.getByTestId('toolbar-run').click();
	await expect(page.getByTestId('workflow-chat-panel')).toBeVisible();
	expect(executeRuns).toEqual([]);

	await page.getByTestId('workflow-chat-input').fill('hello from canvas');
	const queued = page.waitForRequest(
		(request) => request.method() === 'POST' && request.url().includes(`/workflows/${workflow.id}/run`)
	);
	await page.getByTestId('workflow-chat-send').click();
	const request = await queued;
	const body = request.postDataJSON() as {
		triggerNodeId?: string;
		input?: { action?: string; sessionId?: string; chatInput?: string };
	};
	expect(body.triggerNodeId).toBe('chat');
	expect(body.input).toMatchObject({ action: 'sendMessage', chatInput: 'hello from canvas' });
	expect(body.input?.sessionId).toMatch(/^[0-9a-f-]{36}$/i);

	await expect(page.getByTestId('workflow-chat-messages')).toContainText('hello from canvas', { timeout: 30_000 });
});
