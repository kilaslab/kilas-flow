import { test, expect } from '../fixtures';
import { blockedChatWorkflowDocument, chatWorkflowDocument, createWorkflow } from '../helpers/seed';

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

test('a chat reply renders markdown as formatting and markup as plain text', async ({ page, server }) => {
	const workflow = await createWorkflow(server.baseURL, chatWorkflowDocument('E2E Chat Markdown'));
	await page.goto(`${server.baseURL}/app/workflows/${workflow.id}`);
	await page.getByTestId('workflow-chat-button').click();

	const input = page.getByTestId('workflow-chat-input');
	// Opening the panel puts the cursor in the input.
	await expect(input).toBeFocused();
	// No memory sub-node is wired, so nothing claims the chat remembers.
	await expect(page.getByTestId('workflow-chat-memory-notice')).toHaveCount(0);

	// The Reply node echoes the message back, so this is also the reply.
	await input.fill('**Total:** 3\n- Rina\n- Budi\n<img src=x onerror="window.__xss=1">');
	await page.keyboard.press('Enter');

	const reply = page.getByTestId('workflow-chat-messages').locator('[data-role="assistant"]').last();
	await expect(reply.locator('strong')).toHaveText('Total:', { timeout: 30_000 });
	await expect(reply.locator('li')).toHaveText(['Rina', 'Budi']);
	await expect(reply).toContainText('<img src=x onerror="window.__xss=1">');
	expect(await page.evaluate(() => (window as unknown as { __xss?: number }).__xss)).toBeUndefined();
	await expect(reply.getByRole('link', { name: 'View execution' })).toHaveAttribute('href', /\/executions\/exec_/);
	// Ready for the next message without clicking back into the input.
	await expect(input).toBeFocused();
});

test('a chat blocked by validation names the node and opens it', async ({ page, server }) => {
	const workflow = await createWorkflow(server.baseURL, blockedChatWorkflowDocument('E2E Chat Blocked'));
	await page.goto(`${server.baseURL}/app/workflows/${workflow.id}`);
	await page.getByTestId('workflow-chat-button').click();
	await page.getByTestId('workflow-chat-input').fill('hello');
	await page.keyboard.press('Enter');

	const error = page.getByTestId('workflow-chat-messages').locator('[data-role="error"]').last();
	// The server can name one node more than once (each missing field is an
	// issue); every one of them names it by its name, not its id.
	const issue = error.getByRole('button', { name: /Call CRM/ }).first();
	await expect(issue).toBeVisible();
	await expect(error).not.toContainText('"call"');
	await expect(error).not.toContainText('workflow validation failed');
	await issue.click();
	// Clicking the issue selects the node and opens its inspector.
	await expect(page.getByRole('region', { name: 'Call CRM properties' })).toBeVisible();
});

test('sending from a canvas with unsaved edits saves them first', async ({ page, server }) => {
	const workflow = await createWorkflow(server.baseURL, chatWorkflowDocument('E2E Chat Dirty'));
	await page.goto(`${server.baseURL}/app/workflows/${workflow.id}`);
	await expect(page.locator('[data-testid="workflow-canvas"]')).toBeVisible();

	await page.locator('[data-testid="workflow-canvas"] .svelte-flow__node').filter({ hasText: 'Reply' }).first().click();
	await page.keyboard.press('F2');
	await page.keyboard.type(' v2');
	await page.keyboard.press('Enter');

	await page.getByTestId('workflow-chat-button').click();
	await expect(page.getByTestId('workflow-chat-unsaved')).toBeVisible();
	await expect(page.getByTestId('workflow-chat-input')).toBeEnabled();

	const saved = page.waitForRequest((request) => request.method() === 'PUT' && request.url().includes(`/workflows/${workflow.id}`));
	await page.getByTestId('workflow-chat-input').fill('after save');
	await page.keyboard.press('Enter');
	await saved;
	await expect(page.getByTestId('workflow-chat-messages').locator('[data-role="assistant"]').last()).toContainText('after save', { timeout: 30_000 });
	await expect(page.getByTestId('workflow-chat-unsaved')).toHaveCount(0);
});
