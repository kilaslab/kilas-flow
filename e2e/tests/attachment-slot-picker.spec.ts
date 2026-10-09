import { test, expect } from '../fixtures';
import { createWorkflow } from '../helpers/seed';

// The dashed "+" under an empty attachment slot (BUG-w1jp70). It used to open a
// picker with no results, because the list required a main input as well as an
// output of the slot's kind and a chat model has no main input; and a node
// picked there was added unwired, because the connection looked for an INPUT of
// the slot's kind on the new node instead of feeding the slot from its output.

const AGENT_DOCUMENT = {
	schemaVersion: 1,
	name: 'Agent With An Empty Model Slot',
	nodes: [
		{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
		{ id: 'agent', name: 'Agent', type: 'kilasflow.agent', typeVersion: 1, position: { x: 300, y: 0 }, parameters: { prompt: 'Say hello.' } }
	],
	connections: [{ id: 'c-main', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'agent', port: 'main' } }],
	settings: {}
};

test('the + under an Agent’s empty Chat Model slot lists chat models and wires the one picked to the slot', async ({ page, server }) => {
	const workflow = await createWorkflow(server.baseURL, AGENT_DOCUMENT);
	await page.goto(`${server.baseURL}/app/workflows/${workflow.id}`);
	await expect(page.locator('[data-testid="workflow-canvas"]')).toBeVisible();

	const add = page.getByRole('button', { name: 'Add a Chat Model to Agent' });
	await add.click();

	// The list is the providers of a language model, not every node and not nothing.
	const picker = page.getByRole('dialog');
	await expect(picker).toBeVisible();
	const options = picker.getByRole('option');
	await expect(options.first()).toBeVisible();
	await expect(options.filter({ hasText: 'HTTP Request' })).toHaveCount(0);
	await expect(options.filter({ hasText: 'Chat Model' }).first()).toBeVisible();

	await options.filter({ hasText: 'Chat Model' }).first().click();
	await expect(picker).toBeHidden();

	// The new node feeds the slot, and the slot no longer offers a "+".
	await expect(add).toHaveCount(0);
	await expect(page.locator('[data-testid="workflow-canvas"] .svelte-flow__edge[aria-label$=" model to Agent model"]')).toHaveCount(1);

	const save = page.getByRole('button', { name: 'Save', exact: true });
	await save.click();
	await expect(save).toBeDisabled();
	const stored = await (await fetch(`${server.baseURL}/api/v1/workflows/${workflow.id}`)).json();
	const document = stored.latestVersion.document as { nodes: Array<{ id: string; type: string }>; connections: Array<{ kind: string; source: { nodeId: string; port: string }; target: { nodeId: string; port: string } }> };
	const wire = document.connections.find((connection) => connection.kind === 'ai_languageModel');
	expect(wire, 'an ai_languageModel connection was saved').toBeDefined();
	expect(wire!.target).toEqual({ nodeId: 'agent', port: 'model' });
	expect(wire!.source.port).toBe('model');
	const provider = document.nodes.find((node) => node.id === wire!.source.nodeId);
	expect(provider?.type).toMatch(/ChatModel|lmChat/);
});
