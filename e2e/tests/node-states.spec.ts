import type { Locator, Page } from '@playwright/test';

import { test, expect } from '../fixtures';
import { createWorkflow } from '../helpers/seed';

// FEAT-ez6xtm: a disabled node looks disabled and can be switched back on, a
// node has a context menu that reuses the editor's own actions, and a node's
// note can be written in Settings and shown under it on the canvas.

const DOCUMENT = {
	schemaVersion: 1,
	name: 'Node states',
	nodes: [
		{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
		{
			id: 'noted',
			name: 'Noted Set',
			type: 'kilasflow.set',
			typeVersion: 1,
			position: { x: 240, y: 0 },
			parameters: { assignments: { output: { mode: 'expression', value: '{{ $json.a }}' } } },
			settings: { notes: 'Only fires out of hours', notesInFlow: true }
		},
		{
			id: 'off',
			name: 'Disabled Set',
			type: 'kilasflow.set',
			typeVersion: 1,
			position: { x: 480, y: 0 },
			parameters: { assignments: { output: { mode: 'expression', value: '{{ $json.a }}' } } },
			disabled: true,
			settings: { notes: 'Kept in the document, not shown', notesInFlow: false }
		}
	],
	connections: [
		{ id: 'c1', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'noted', port: 'main' } },
		{ id: 'c2', kind: 'main', source: { nodeId: 'noted', port: 'main' }, target: { nodeId: 'off', port: 'main' } }
	],
	settings: {}
};

const canvas = (page: Page) => page.locator('[data-testid="workflow-canvas"]');
const node = (page: Page, id: string) => canvas(page).locator(`.svelte-flow__node[data-id="${id}"]`);
const tile = (page: Page, id: string) => node(page, id).locator('[data-disabled]').first();
const menu = (page: Page) => page.getByRole('menu');

async function open(page: Page, baseURL: string): Promise<{ id: string }> {
	const workflow = await createWorkflow(baseURL, DOCUMENT);
	await page.goto(`${baseURL}/app/workflows/${workflow.id}`);
	await expect(canvas(page)).toBeVisible();
	await expect(node(page, 'off')).toBeVisible();
	return workflow;
}

async function stored(baseURL: string, id: string): Promise<any> {
	const body = await (await fetch(`${baseURL}/api/v1/workflows/${id}`)).json();
	return body.latestVersion.document;
}

async function save(page: Page): Promise<void> {
	const button = page.getByRole('button', { name: 'Save', exact: true });
	await button.click();
	await expect(button).toBeDisabled();
}

/** The node name's text decoration, which is what draws the strike-through. */
const decoration = (name: Locator) => name.evaluate((element) => getComputedStyle(element).textDecorationLine);

test('a disabled node is dimmed and struck through, and the note shows only where it was asked to', async ({ page, server }) => {
	await open(page, server.baseURL);

	const wrapper = node(page, 'off').locator('[data-disabled="true"]');
	await expect(wrapper).toHaveCount(1);
	expect(Number(await wrapper.evaluate((element) => getComputedStyle(element).opacity))).toBeLessThan(0.7);
	// The name carries a screen-reader cue after the visible text, so it is
	// found by what it starts with rather than matched whole.
	expect(await decoration(node(page, 'off').locator('p', { hasText: /^Disabled Set/ }))).toContain('line-through');
	await expect(node(page, 'off').locator('.sr-only')).toHaveText('(disabled)');
	// An enabled node is neither.
	expect(await decoration(node(page, 'noted').locator('p', { hasText: /^Noted Set/ }))).not.toContain('line-through');
	await expect(node(page, 'noted').locator('.sr-only')).toHaveCount(0);
	await expect(node(page, 'noted').locator('[data-disabled]')).toHaveCount(0);

	await expect(node(page, 'noted').getByText('Only fires out of hours')).toBeVisible();
	await expect(canvas(page).getByText('Kept in the document, not shown')).toHaveCount(0);
});

test('D and the toolbar button disable and enable the selected node, and the flag is saved', async ({ page, server }) => {
	const workflow = await open(page, server.baseURL);

	await node(page, 'noted').click();
	await page.keyboard.press('d');
	await expect(node(page, 'noted').locator('[data-disabled="true"]')).toHaveCount(1);

	// The toolbar offers the opposite action now.
	await page.getByRole('button', { name: 'Enable Noted Set' }).click();
	await expect(node(page, 'noted').locator('[data-disabled]')).toHaveCount(0);
	await page.getByRole('button', { name: 'Disable Noted Set' }).click();
	await expect(node(page, 'noted').locator('[data-disabled="true"]')).toHaveCount(1);

	await save(page);
	const document = await stored(server.baseURL, workflow.id);
	expect(document.nodes.find((candidate: any) => candidate.id === 'noted').disabled).toBe(true);
	expect(document.nodes.find((candidate: any) => candidate.id === 'off').disabled).toBe(true);
});

test('the node context menu offers the editor’s actions and is a keyboard-reachable menu', async ({ page, server }) => {
	await open(page, server.baseURL);

	await node(page, 'noted').click({ button: 'right' });
	await expect(menu(page)).toBeVisible();
	await expect(menu(page)).toHaveAccessibleName('Actions for Noted Set');
	const items = menu(page).getByRole('menuitem');
	await expect(items).toHaveText(['Open', 'Rename', 'Duplicate', 'Disable', 'Copy', 'Delete'].map((label) => new RegExp(`^${label}`)));
	// The first item has focus, and the arrows move it.
	await expect(items.first()).toBeFocused();
	await page.keyboard.press('ArrowDown');
	await expect(items.nth(1)).toBeFocused();
	await page.keyboard.press('End');
	await expect(items.last()).toBeFocused();
	await page.keyboard.press('ArrowDown');
	await expect(items.first()).toBeFocused();

	// Escape closes it and focus goes back to the node.
	await page.keyboard.press('Escape');
	await expect(menu(page)).toHaveCount(0);
	await expect(node(page, 'noted')).toBeFocused();

	// So does a press anywhere else.
	await node(page, 'noted').click({ button: 'right' });
	await expect(menu(page)).toBeVisible();
	await canvas(page).click({ position: { x: 400, y: 300 } });
	await expect(menu(page)).toHaveCount(0);
});

test('Disable, Duplicate and Delete from the context menu act on the node, and an enabled-again node reads Disable', async ({ page, server }) => {
	await open(page, server.baseURL);

	await node(page, 'noted').click({ button: 'right' });
	await menu(page).getByRole('menuitem', { name: /^Disable/ }).click();
	await expect(menu(page)).toHaveCount(0);
	await expect(node(page, 'noted').locator('[data-disabled="true"]')).toHaveCount(1);

	// The disabled node's menu now offers the opposite.
	await node(page, 'noted').click({ button: 'right' });
	await expect(menu(page).getByRole('menuitem', { name: /^Enable/ })).toBeVisible();
	await page.keyboard.press('Escape');

	const nodes = canvas(page).locator('.svelte-flow__node');
	await expect(nodes).toHaveCount(3);
	await node(page, 'noted').click({ button: 'right' });
	await menu(page).getByRole('menuitem', { name: /^Duplicate/ }).click();
	await expect(nodes).toHaveCount(4);

	await node(page, 'off').click({ button: 'right' });
	await menu(page).getByRole('menuitem', { name: /^Delete/ }).click();
	await expect(nodes).toHaveCount(3);
	await expect(node(page, 'off')).toHaveCount(0);
});

test('a menu opened inside a multi-selection acts on all of it, and on its own for a node outside it', async ({ page, server }) => {
	const workflow = await open(page, server.baseURL);

	await node(page, 'noted').click();
	await page.keyboard.press('ControlOrMeta+a');
	await node(page, 'noted').click({ button: 'right' });
	await expect(menu(page)).toHaveAccessibleName('Actions for 3 selected nodes');
	// Open and Rename name one node; Disable reads for what the D key would do
	// (one of the three is enabled, so it disables).
	await expect(menu(page).getByRole('menuitem')).toHaveText(['Duplicate', 'Disable', 'Copy', 'Delete'].map((label) => new RegExp(`^${label}`)));
	await menu(page).getByRole('menuitem', { name: /^Disable/ }).click();
	await expect(canvas(page).locator('[data-disabled="true"]')).toHaveCount(3);

	// A node outside the selection is acted on alone.
	await canvas(page).click({ position: { x: 400, y: 500 } });
	await node(page, 'manual').click({ button: 'right' });
	await expect(menu(page)).toHaveAccessibleName('Actions for Manual Trigger');
	await menu(page).getByRole('menuitem', { name: /^Enable/ }).click();
	await expect(canvas(page).locator('[data-disabled="true"]')).toHaveCount(2);

	await save(page);
	const document = await stored(server.baseURL, workflow.id);
	expect(document.nodes.filter((candidate: any) => candidate.disabled === true).map((candidate: any) => candidate.id).sort()).toEqual(['noted', 'off']);
});

test('Shift+F10 on a focused node opens its menu', async ({ page, server }) => {
	await open(page, server.baseURL);

	await node(page, 'noted').focus();
	await page.keyboard.press('Shift+F10');
	await expect(menu(page)).toBeVisible();
	await expect(menu(page).getByRole('menuitem').first()).toBeFocused();
	// Choosing with the keyboard works: Enter on Open opens the inspector.
	await page.keyboard.press('Enter');
	await expect(menu(page)).toHaveCount(0);
	await expect(page.getByRole('region', { name: /Noted Set/ }).first()).toBeVisible();
});

test('Settings edits the disabled flag, the note and whether it shows in the flow, and all of it is saved', async ({ page, server }) => {
	const workflow = await open(page, server.baseURL);

	await node(page, 'off').click();
	await page.getByRole('tab', { name: 'Settings' }).click();
	const disabled = page.getByRole('checkbox', { name: 'Disabled', exact: true });
	await expect(disabled).toBeChecked();
	await disabled.uncheck();
	await expect(node(page, 'off').locator('[data-disabled]')).toHaveCount(0);

	// Typing a note and turning the flag on draws it under the node.
	const notes = page.getByRole('textbox', { name: 'Notes' });
	await expect(notes).toHaveValue('Kept in the document, not shown');
	await notes.fill('Check the date format first');
	await expect(canvas(page).getByText('Check the date format first')).toHaveCount(0);
	await page.getByRole('checkbox', { name: 'Display note in flow' }).check();
	await expect(node(page, 'off').getByText('Check the date format first')).toBeVisible();

	await save(page);
	const document = await stored(server.baseURL, workflow.id);
	const saved = document.nodes.find((candidate: any) => candidate.id === 'off');
	expect(saved.disabled).toBeUndefined();
	expect(saved.settings).toEqual({ notes: 'Check the date format first', notesInFlow: true });

	// Clearing the note takes the keys out rather than saving empty ones.
	await notes.fill('');
	await page.getByRole('checkbox', { name: 'Display note in flow' }).uncheck();
	await save(page);
	const cleared = (await stored(server.baseURL, workflow.id)).nodes.find((candidate: any) => candidate.id === 'off');
	expect(cleared.settings).toBeUndefined();
});
