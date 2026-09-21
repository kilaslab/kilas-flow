import { test, expect } from '../fixtures';
import { createCredential, createWorkflow, manualWorkflowDocument } from '../helpers/seed';

test('Connect with Google opens a popup, not an iframe', async ({ page, server }) => {
	await createCredential(server.baseURL, {
		name: 'E2E Drive',
		type: 'googleDriveOAuth2Api',
		fields: {
			clientId: 'tenant-cid.apps.googleusercontent.com',
			clientSecret: 'tenant-csec'
		}
	});

	await page.addInitScript(() => {
		(window as Window & { __opened?: { url: string; name: string; features: string }[] }).__opened = [];
		window.open = (url?: string | URL, name?: string, features?: string) => {
			(window as Window & { __opened: { url: string; name: string; features: string }[] }).__opened.push({
				url: String(url ?? ''),
				name: String(name ?? ''),
				features: String(features ?? '')
			});
			return { closed: false, close() {}, focus() {} } as Window;
		};
	});

	await page.goto(`${server.baseURL}/credentials`);
	await expect(page.getByRole('heading', { name: 'Credentials' })).toBeVisible();
	await expect(page.getByText('E2E Drive')).toBeVisible();
	await page.getByRole('button', { name: 'Edit' }).click();

	const dialog = page.getByRole('dialog');
	await expect(dialog.getByText('Google blocks sign-in inside an iframe')).toBeVisible();
	await expect(dialog.locator('iframe')).toHaveCount(0);
	await dialog.getByRole('button', { name: 'Connect with Google' }).click();

	await expect
		.poll(async () => page.evaluate(() => (window as Window & { __opened?: unknown[] }).__opened?.length ?? 0))
		.toBe(1);
	const opened = await page.evaluate(
		() => (window as Window & { __opened: { url: string; name: string; features: string }[] }).__opened[0]
	);
	expect(opened.name).toBe('kilasflow-oauth');
	expect(opened.url).toContain('accounts.google.com');
	expect(opened.url).toContain('tenant-cid.apps.googleusercontent.com');
	expect(opened.features).toContain('popup=yes');
	await expect(page.locator('iframe')).toHaveCount(0);
});

test('the node picker offers Drive, Gmail, and PGVector', async ({ page, server }) => {
	const workflow = await createWorkflow(server.baseURL, manualWorkflowDocument('E2E RAG Picker'));
	await page.goto(`${server.baseURL}/app/workflows/${workflow.id}`);
	await expect(page.locator('[data-testid="workflow-canvas"]')).toBeVisible();

	await page.getByRole('button', { name: 'Add step' }).click();
	const dialog = page.getByRole('dialog');
	await expect(dialog).toBeVisible();

	await page.locator('#node-picker-search').fill('Google Drive');
	await expect(dialog.locator('[id="node-option-kilasflow.googleDrive@1"]')).toBeVisible();

	await page.locator('#node-picker-search').fill('Gmail');
	await expect(dialog.locator('[id="node-option-kilasflow.gmail@1"]')).toBeVisible();

	await page.locator('#node-picker-search').fill('PGVector');
	await expect(dialog.locator('[id="node-option-kilasflow.vectorStorePGVector@1"]')).toBeVisible();
});
