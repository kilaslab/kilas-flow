import type { Locator, Page } from '@playwright/test';

import { test, expect } from '../fixtures';
import { createCredential, createWorkflow, runWorkflow, waitForExecution } from '../helpers/seed';
import {
	CONVERTED_PACK_DIR_NAME,
	CONVERTED_PACK_TYPE,
	convertDeclarativePack,
	freshPacksDir,
	nodepackgenBinary,
	PACK_DIR_NAME,
	PACK_TYPE,
	scaffoldPack,
	startHeaderObserver,
	startPackServer,
	startPackServerWithEndpoint
} from '../fixtures/pack-install';

// FEAT-ykyfbd boxes 2-3 (editor remainder): the pack's nodes are picked from
// the editor's node picker, configured through the resource/operation
// cascade, saved, and executed against the stub — and the pack's credential
// type is offered by the credential picker with the created credential
// authenticating the outbound call. The API half (catalogue tag, cascade
// load-options, credential-types listing) is proven in pack-install; this is
// the operator driving the real SPA.

async function helloPacksDir(): Promise<string> {
	const binary = await nodepackgenBinary();
	const packsDir = await freshPacksDir();
	await scaffoldPack(binary, packsDir, PACK_DIR_NAME, PACK_TYPE);
	return packsDir;
}

async function openWorkflow(page: Page, baseURL: string, workflowId: string): Promise<void> {
	await page.goto(`${baseURL}/app/workflows/${workflowId}`);
	await expect(page.locator('[data-testid="workflow-canvas"]')).toBeVisible();
}

function canvasNode(page: Page, name: string): Locator {
	return page.locator('[data-testid="workflow-canvas"] .svelte-flow__node').filter({ hasText: name }).first();
}

test('a pack node is picked from the node picker and saved', async ({ page, stub }) => {
	const packsDir = await helloPacksDir();
	const server = await startPackServer(stub, packsDir);
	try {
		const workflow = await createWorkflow(server.baseURL, {
			schemaVersion: 1,
			name: 'E2E Pack Picker',
			nodes: [{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } }],
			connections: [],
			settings: {}
		});
		await openWorkflow(page, server.baseURL, workflow.id);

		// The installed pack is offered by the picker's registry listbox.
		await page.getByRole('button', { name: 'Add step' }).click();
		const dialog = page.getByRole('dialog');
		await expect(dialog.getByRole('listbox', { name: 'Registered node types' })).toBeVisible();
		await page.locator('#node-picker-search').fill('E2E Hello');
		await dialog.getByRole('option', { name: /E2E Hello/ }).click();

		await expect(canvasNode(page, 'E2E Hello')).toBeVisible();

		// The picked node persists through a save.
		const save = page.getByRole('button', { name: 'Save', exact: true });
		await expect(save).toBeEnabled();
		await save.click();
		await expect(save).toBeDisabled();
	} finally {
		await server.close();
	}
});

test('a pack node is configured through the cascade and credential picker, saved, and run', async ({ page, stub }) => {
	const packsDir = await helloPacksDir();
	const server = await startPackServer(stub, packsDir);
	try {
		const credential = await createCredential(server.baseURL, {
			name: 'E2E Pack WAHA',
			type: 'wahaApi',
			fields: { baseUrl: stub.origin, apiKey: 'e2e-key' }
		});
		const workflow = await createWorkflow(server.baseURL, {
			schemaVersion: 1,
			name: 'E2E Pack Editor Run',
			nodes: [
				{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
				{
					id: 'send',
					name: 'Send',
					type: PACK_TYPE,
					typeVersion: 1,
					position: { x: 240, y: 0 },
					parameters: {},
					credentials: {}
				}
			],
			connections: [
				{ id: 'c1', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'send', port: 'main' } }
			],
			settings: {}
		});
		await openWorkflow(page, server.baseURL, workflow.id);

		await canvasNode(page, 'Send').click();
		const panel = page.locator('section[aria-label="Send properties"]');
		await expect(panel).toBeVisible();

		// The credential picker offers the pack's type, with the credential
		// created over the API selectable.
		await expect(panel).toContainText('This node needs a credential before it can run.');
		const credentialSelect = panel.locator('#credential-wahaApi');
		await expect(credentialSelect.locator('option')).toContainText(['E2E Pack WAHA']);
		await credentialSelect.selectOption(credential.id);
		await expect(panel).not.toContainText('This node needs a credential before it can run.');

		// The generated cascade, driven through the panel: resource first,
		// then the operation it narrows to. Property controls are addressed by
		// role and accessible name because their ids are per-instance now
		// (`property-${id}`), so a fixed `#property-resource` no longer exists.
		const resource = panel.getByRole('combobox', { name: 'Resource', exact: true });
		const operation = panel.getByRole('combobox', { name: 'Operation', exact: true });
		await resource.selectOption('message');
		await expect(operation.locator('option[value="sendMessage"]')).toBeAttached();
		await operation.selectOption('sendMessage');
		await panel.getByRole('textbox', { name: 'Chat ID', exact: true }).fill('e2e-chat');
		await panel.getByRole('textbox', { name: 'Text', exact: true }).fill('hello packs');

		const save = page.getByRole('button', { name: 'Save', exact: true });
		await expect(save).toBeEnabled();
		await save.click();
		await expect(save).toBeDisabled();

		await page.getByRole('button', { name: 'Execute', exact: true }).click();
		await expect
			.poll(() => stub.requests.find((request) => request.method === 'POST' && request.path === '/sendMessage')?.body ?? '', {
				message: 'the editor run reaches the stub through the pack node',
				timeout: 60_000
			})
			.toContain('hello packs');
		const call = stub.requests.find((request) => request.method === 'POST' && request.path === '/sendMessage');
		expect(JSON.parse(call?.body ?? '{}')).toMatchObject({ chatId: 'e2e-chat', text: 'hello packs' });
	} finally {
		await server.close();
	}
});

test('the cascade narrows operations to the selected resource in the editor', async ({ page, stub }) => {
	const packsDir = await freshPacksDir();
	await convertDeclarativePack(packsDir, CONVERTED_PACK_DIR_NAME, CONVERTED_PACK_TYPE);
	const server = await startPackServer(stub, packsDir);
	try {
		const workflow = await createWorkflow(server.baseURL, {
			schemaVersion: 1,
			name: 'E2E Pack Cascade',
			nodes: [
				{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
				{
					id: 'acme',
					name: 'Acme Send',
					type: CONVERTED_PACK_TYPE,
					typeVersion: 1,
					position: { x: 240, y: 0 },
					parameters: { resource: 'message', operation: 'send' },
					credentials: {}
				}
			],
			connections: [
				{ id: 'c1', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'acme', port: 'main' } }
			],
			settings: {}
		});
		await openWorkflow(page, server.baseURL, workflow.id);

		await canvasNode(page, 'Acme Send').click();
		const panel = page.locator('section[aria-label="Acme Send properties"]');
		await expect(panel).toBeVisible();

		const operation = panel.getByRole('combobox', { name: 'Operation', exact: true });
		await expect(operation.locator('option[value="send"]')).toBeAttached();
		await panel.getByRole('combobox', { name: 'Resource', exact: true }).selectOption('mailbox');
		// The settled state, not the loader's pre-response window: the resource
		// change makes the loader fetch the mailbox operations, and the saved
		// operation ("send") — not one of them — stays selectable as its own
		// option rather than being silently read back as the first one
		// (property-field.svelte renders an out-of-list value that way). A
		// count asserted before the loader answers passes on the stale list and
		// then fails once it arrives.
		await expect(operation.locator('option')).toHaveText(['send', 'list']);
		await operation.selectOption('list');

		const save = page.getByRole('button', { name: 'Save', exact: true });
		await expect(save).toBeEnabled();
		await save.click();
		await expect(save).toBeDisabled();
	} finally {
		await server.close();
	}
});

test('a credential created for the pack type authenticates the outbound call', async ({ stub }) => {
	const observer = await startHeaderObserver();
	const packsDir = await helloPacksDir();
	const server = await startPackServerWithEndpoint(`127.0.0.1:${observer.port}`, packsDir);
	try {
		const credential = await createCredential(server.baseURL, {
			name: 'E2E Pack Observed',
			type: 'wahaApi',
			fields: { baseUrl: observer.origin, apiKey: 'e2e-key' }
		});
		const workflow = await createWorkflow(server.baseURL, {
			schemaVersion: 1,
			name: 'E2E Pack Auth',
			nodes: [
				{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
				{
					id: 'send',
					name: 'Send',
					type: PACK_TYPE,
					typeVersion: 1,
					position: { x: 240, y: 0 },
					parameters: { resource: 'message', operation: 'sendMessage', chatId: 'e2e-chat', text: 'signed call' },
					credentials: { wahaApi: credential.id }
				}
			],
			connections: [
				{ id: 'c1', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'send', port: 'main' } }
			],
			settings: {}
		});

		const executionId = await runWorkflow(server.baseURL, workflow.id);
		const record = await waitForExecution(server.baseURL, executionId);
		expect(record.status).toBe('succeeded');

		// wahaApi places its key in the X-Api-Key header: the call the stub
		// side cannot see is observed here, proving the credential was not
		// merely attached but applied.
		const call = observer.requests.find((request) => request.method === 'POST' && request.path === '/sendMessage');
		expect(call, 'the pack node reached the observer').toBeDefined();
		expect(call?.headers['x-api-key']).toBe('e2e-key');
		expect(JSON.parse(call?.body ?? '{}')).toMatchObject({ chatId: 'e2e-chat', text: 'signed call' });
	} finally {
		await server.close();
		await observer.close();
	}
});
